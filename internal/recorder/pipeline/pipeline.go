package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/at-wat/ebml-go/webm"
	"github.com/google/uuid"
)

// Logger is a minimal structured-logging interface (compatible with zap.SugaredLogger)
type Logger interface {
	Debugw(msg string, keysAndValues ...interface{})
	Infow(msg string, keysAndValues ...interface{})
	Warnw(msg string, keysAndValues ...interface{})
	Errorw(msg string, keysAndValues ...interface{})
}

// noopLogger is used by default; it discards logs.
type noopLogger struct{}

func (noopLogger) Debugw(string, ...interface{}) {}
func (noopLogger) Infow(string, ...interface{})  {}
func (noopLogger) Warnw(string, ...interface{})  {}
func (noopLogger) Errorw(string, ...interface{}) {}

// SegmentStatus represents the state of a recording segment
type SegmentStatus string

const (
	SegmentStatusRecording  SegmentStatus = "recording"
	SegmentStatusFinalizing SegmentStatus = "finalizing"
	SegmentStatusCompleted  SegmentStatus = "completed"
	SegmentStatusUploading  SegmentStatus = "uploading"
	SegmentStatusUploaded   SegmentStatus = "uploaded"
	SegmentStatusFailed     SegmentStatus = "failed"
)

// Segment represents a recording segment
type Segment struct {
	ID          string
	RecordingID string
	Index       int
	StartTime   time.Time
	EndTime     time.Time
	Duration    time.Duration
	FilePath    string
	TempPath    string
	Size        int64
	FrameCount  int64
	Checksum    string
	StorageKey  string
	Status      SegmentStatus
	UploadedAt  time.Time
	Error       error

	writer *MKVWriter
	mu     sync.RWMutex
}

// Segmenter manages recording segments
type Segmenter struct {
	segmentDuration time.Duration
	tempDir         string
	outputDir       string

	// Video config for MKV writer
	videoWidth  int
	videoHeight int
	frameRate   float64

	// Cached AV1 Sequence Header for sharing between segments
	// This ensures new segments can start playable even without a keyframe
	cachedSequenceHeader []byte

	logger Logger

	segments map[string]*Segment // recordingID -> current segment
	pending  []*Segment          // segments pending upload
	mu       sync.RWMutex

	metrics SegmenterMetrics
}

// SetLogger lets you inject your own logger (e.g., zap.L().Named("segmenter").Sugar()).
func (s *Segmenter) SetLogger(l Logger) {
	if l == nil {
		s.logger = noopLogger{}
		return
	}
	s.logger = l
}

// SetVideoConfig sets the video configuration for MKV writing
func (s *Segmenter) SetVideoConfig(width, height int, frameRate float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.videoWidth = width
	s.videoHeight = height
	s.frameRate = frameRate
}

// SegmenterMetrics tracks segmenter performance
type SegmenterMetrics struct {
	SegmentsCreated   atomic.Uint64
	SegmentsCompleted atomic.Uint64
	SegmentsFailed    atomic.Uint64
	BytesWritten      atomic.Uint64
	FramesWritten     atomic.Uint64
}

// MKVWriter handles MKV container writing using ebml-go
type MKVWriter struct {
	file           *os.File
	path           string
	startTime      time.Time
	frameCount     int64
	size           int64
	hasVideo       bool
	hasAudio       bool
	width          int
	height         int
	frameRate      float64
	frameDurNs     int64 // frame duration in nanoseconds
	blockWriter    webm.BlockWriteCloser
	sequenceHeader []byte // Cached AV1 Sequence Header for prepending to non-keyframe starts

	mu     sync.Mutex
	closed atomic.Bool
}

// NewSegmenter creates a new segmenter
func NewSegmenter(segmentDuration time.Duration, tempDir string) *Segmenter {
	return &Segmenter{
		segmentDuration: segmentDuration,
		tempDir:         tempDir,
		outputDir:       filepath.Join(tempDir, "segments"),
		segments:        make(map[string]*Segment),
		pending:         make([]*Segment, 0),
		logger:          noopLogger{},
	}
}

// Initialize prepares the segmenter
func (s *Segmenter) Initialize() error {
	s.logger.Infow("Segmenter initializing",
		"segment_duration", s.segmentDuration,
		"temp_dir", s.tempDir,
		"output_dir", s.outputDir)

	// Create directories
	dirs := []string{s.tempDir, s.outputDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	// Clean up any stale segments
	s.cleanupStaleSegments()
	return nil
}

// NewSegment creates a new segment for a recording
func (s *Segmenter) NewSegment(recordingID string) (*Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Finalize existing segment if present
	if existing, ok := s.segments[recordingID]; ok && existing != nil {
		s.finalizeSegmentLocked(existing)
	}

	// Determine segment index
	index := s.getNextSegmentIndex(recordingID)

	segment := &Segment{
		ID:          uuid.New().String(),
		RecordingID: recordingID,
		Index:       index,
		StartTime:   time.Now(),
		Status:      SegmentStatusRecording,
		TempPath:    filepath.Join(s.tempDir, fmt.Sprintf("%s_%03d.mkv.tmp", recordingID, index)),
		FilePath:    filepath.Join(s.outputDir, fmt.Sprintf("%s_%03d.mkv", recordingID, index)),
	}

	// Create MKV writer with video config and cached sequence header
	cfg := MKVWriterConfig{
		Width:        s.videoWidth,
		Height:       s.videoHeight,
		FrameRate:    s.frameRate,
		CodecPrivate: s.cachedSequenceHeader, // Pass cached AV1 Sequence Header
	}
	writer, err := NewMKVWriterWithConfig(segment.TempPath, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create MKV writer: %w", err)
	}

	// Pass the cached sequence header to the writer so it can prepend to non-keyframe starts
	writer.sequenceHeader = s.cachedSequenceHeader

	segment.writer = writer

	s.segments[recordingID] = segment
	s.metrics.SegmentsCreated.Add(1)

	s.logger.Debugw("Created new segment",
		"segment_id", segment.ID,
		"recording_id", recordingID,
		"index", index)

	return segment, nil
}

// WriteFrame writes an encoded frame to the current segment
func (s *Segmenter) WriteFrame(recordingID string, data []byte, timestamp time.Time) error {
	// Use write lock for atomic segment lookup/creation to prevent race conditions
	s.mu.Lock()
	segment := s.segments[recordingID]
	if segment == nil {
		// Create segment while still holding the lock to prevent races
		s.mu.Unlock() // Release before NewSegment (which acquires its own lock)
		var err error
		if segment, err = s.NewSegment(recordingID); err != nil {
			return fmt.Errorf("failed to create segment: %w", err)
		}
	} else {
		s.mu.Unlock()
	}

	segment.mu.Lock()
	defer segment.mu.Unlock()

	if segment.writer == nil {
		return fmt.Errorf("segment writer is nil")
	}

	// Try to extract and cache sequence header from incoming data
	// This ensures we have it available for future segments even before finalization
	if s.cachedSequenceHeader == nil {
		if seqHdr := extractSequenceHeader(data); len(seqHdr) > 0 {
			s.mu.Lock()
			if s.cachedSequenceHeader == nil {
				s.cachedSequenceHeader = seqHdr
			}
			s.mu.Unlock()
		}
	}

	n, err := segment.writer.WriteFrame(data, timestamp)
	if err != nil {
		segment.Status = SegmentStatusFailed
		segment.Error = err
		s.metrics.SegmentsFailed.Add(1)
		return fmt.Errorf("failed to write frame: %w", err)
	}

	segment.Size += int64(n)
	segment.FrameCount++
	s.metrics.BytesWritten.Add(uint64(n))
	s.metrics.FramesWritten.Add(1)
	return nil
}

// ShouldRotate checks if a segment should be rotated
// NOTE: The returned *Segment is a snapshot; do not assume it remains current after this call.
func (s *Segmenter) ShouldRotate(recordingID string) (*Segment, bool) {
	s.mu.RLock()
	segment := s.segments[recordingID]
	segmentDur := s.segmentDuration
	s.mu.RUnlock()

	if segment == nil {
		return nil, false
	}

	segment.mu.RLock()
	elapsed := time.Since(segment.StartTime)
	size := segment.Size
	segment.mu.RUnlock()

	// Duration-based rotation
	if elapsed >= segmentDur {
		return segment, true
	}
	// Size-based rotation (> 100MB)
	if size > 100*1024*1024 {
		return segment, true
	}
	return nil, false
}

// Finalize finalizes the current segment for a recording
func (s *Segmenter) Finalize(recordingID string) *Segment {
	s.mu.Lock()
	defer s.mu.Unlock()

	segment := s.segments[recordingID]
	if segment == nil {
		return nil
	}

	s.finalizeSegmentLocked(segment)
	delete(s.segments, recordingID)
	return segment
}

// finalizeSegmentLocked finalizes a segment (requires s.mu held)
func (s *Segmenter) finalizeSegmentLocked(segment *Segment) {
	segment.mu.Lock()
	defer segment.mu.Unlock()

	if segment.Status != SegmentStatusRecording {
		return
	}

	segment.Status = SegmentStatusFinalizing
	segment.EndTime = time.Now()
	segment.Duration = segment.EndTime.Sub(segment.StartTime)

	// Cache the sequence header from this writer for future segments
	if segment.writer != nil && segment.writer.sequenceHeader != nil {
		s.cachedSequenceHeader = segment.writer.sequenceHeader
	}

	// Close writer
	if segment.writer != nil {
		if err := segment.writer.Close(); err != nil {
			s.logger.Warnw("Error closing segment writer",
				"segment_id", segment.ID,
				"error", err)
		}
		segment.writer = nil
	}

	// Move from temp to final location
	if err := os.Rename(segment.TempPath, segment.FilePath); err != nil {
		s.logger.Errorw("Failed to move segment file",
			"segment_id", segment.ID,
			"error", err)
		segment.Status = SegmentStatusFailed
		segment.Error = err
		s.metrics.SegmentsFailed.Add(1)
		return
	}

	// Calculate checksum (placeholder)
	segment.Checksum = s.calculateChecksum(segment.FilePath)

	segment.Status = SegmentStatusCompleted
	s.metrics.SegmentsCompleted.Add(1)

	// Enqueue for upload
	s.pending = append(s.pending, segment)

	s.logger.Infow("Segment finalized",
		"segment_id", segment.ID,
		"duration", segment.Duration,
		"size", segment.Size,
		"frames", segment.FrameCount)
}

// GetPendingSegments returns segments waiting to be uploaded and clears the queue
func (s *Segmenter) GetPendingSegments() []*Segment {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Return copy
	result := make([]*Segment, len(s.pending))
	copy(result, s.pending)

	// Clear pending
	s.pending = s.pending[:0]
	return result
}

// GetCurrentSegment returns the current segment for a recording
func (s *Segmenter) GetCurrentSegment(recordingID string) *Segment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.segments[recordingID]
}

// getNextSegmentIndex determines the next segment index for a recording
func (s *Segmenter) getNextSegmentIndex(recordingID string) int {
	// Simple heuristic: count files on disk
	pattern := filepath.Join(s.outputDir, fmt.Sprintf("%s_*.mkv", recordingID))
	matches, _ := filepath.Glob(pattern)
	return len(matches)
}

// calculateChecksum computes SHA256 checksum of a file
func (s *Segmenter) calculateChecksum(path string) string {
	file, err := os.Open(path)
	if err != nil {
		s.logger.Warnw("Failed to open file for checksum", "path", path, "error", err)
		return ""
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		s.logger.Warnw("Failed to compute checksum", "path", path, "error", err)
		return ""
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// cleanupStaleSegments removes old temporary files
func (s *Segmenter) cleanupStaleSegments() {
	tempFiles, _ := filepath.Glob(filepath.Join(s.tempDir, "*.tmp"))
	for _, file := range tempFiles {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		// Remove files older than 1 hour
		if time.Since(info.ModTime()) > time.Hour {
			_ = os.Remove(file)
			s.logger.Debugw("Removed stale segment", "file", file)
		}
	}
}

// Cleanup removes the segment file from disk (after upload or on failure)
func (seg *Segment) Cleanup() error {
	seg.mu.Lock()
	defer seg.mu.Unlock()

	// Allow cleanup after upload, completion, or failure
	if seg.Status != SegmentStatusUploaded && seg.Status != SegmentStatusCompleted && seg.Status != SegmentStatusFailed {
		return fmt.Errorf("cannot cleanup segment in status %s", seg.Status)
	}
	if err := os.Remove(seg.FilePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// GetMetrics returns segmenter metrics
func (s *Segmenter) GetMetrics() map[string]interface{} {
	s.mu.RLock()
	activeSegments := len(s.segments)
	pendingCount := len(s.pending)
	s.mu.RUnlock()

	return map[string]interface{}{
		"segments_created":   s.metrics.SegmentsCreated.Load(),
		"segments_completed": s.metrics.SegmentsCompleted.Load(),
		"segments_failed":    s.metrics.SegmentsFailed.Load(),
		"bytes_written":      s.metrics.BytesWritten.Load(),
		"frames_written":     s.metrics.FramesWritten.Load(),
		"active_segments":    activeSegments,
		"pending_uploads":    pendingCount,
	}
}

// AV1 OBU types for keyframe detection
const (
	obuTypeSequenceHeader = 1
	obuTypeFrameHeader    = 3
	obuTypeFrame          = 6
)

// isAV1Keyframe detects if the AV1 OBU data represents a keyframe
// Returns true if the data contains a Sequence Header OBU or a KEY_FRAME
func isAV1Keyframe(data []byte) bool {
	if len(data) < 2 {
		return false
	}

	offset := 0
	for offset < len(data) {
		if offset >= len(data) {
			break
		}

		// Parse OBU header
		// Format: [forbidden_bit(1) | obu_type(4) | obu_extension_flag(1) | obu_has_size_field(1) | reserved(1)]
		header := data[offset]
		obuType := (header >> 3) & 0x0F
		hasExtension := (header>>2)&1 == 1
		hasSizeField := (header>>1)&1 == 1
		offset++

		// Skip extension header if present
		if hasExtension && offset < len(data) {
			offset++
		}

		// Read OBU size if present
		var obuSize int
		if hasSizeField && offset < len(data) {
			// LEB128 encoded size
			obuSize, offset = readLEB128(data, offset)
			if obuSize < 0 {
				return false
			}
		} else {
			// Remaining data is the OBU payload
			obuSize = len(data) - offset
		}

		// Sequence Header OBU indicates start of a keyframe sequence
		if obuType == obuTypeSequenceHeader {
			return true
		}

		// Bounds check before moving to next OBU
		if obuSize > len(data)-offset {
			// Malformed data - OBU size exceeds remaining data
			return false
		}

		// Move to next OBU
		offset += obuSize
	}

	return false
}

// extractSequenceHeader extracts the Sequence Header OBU from AV1 data if present
// This is used as CodecPrivate data for the Matroska track
func extractSequenceHeader(data []byte) []byte {
	if len(data) < 2 {
		return nil
	}

	offset := 0
	for offset < len(data) {
		if offset >= len(data) {
			break
		}

		startOffset := offset

		// Parse OBU header
		header := data[offset]
		obuType := (header >> 3) & 0x0F
		hasExtension := (header>>2)&1 == 1
		hasSizeField := (header>>1)&1 == 1
		offset++

		// Skip extension header if present
		if hasExtension && offset < len(data) {
			offset++
		}

		// Read OBU size if present
		var obuSize int
		if hasSizeField && offset < len(data) {
			obuSize, offset = readLEB128(data, offset)
			if obuSize < 0 {
				return nil
			}
		} else {
			obuSize = len(data) - offset
		}

		// Bounds check before processing
		if obuSize > len(data)-offset {
			// Malformed data - OBU size exceeds remaining data
			return nil
		}

		// Found Sequence Header
		if obuType == obuTypeSequenceHeader {
			endOffset := offset + obuSize
			// Return the complete OBU including header
			result := make([]byte, endOffset-startOffset)
			copy(result, data[startOffset:endOffset])
			return result
		}

		offset += obuSize
	}

	return nil
}

// readLEB128 reads an unsigned LEB128 encoded integer from data at the given offset
// Returns the value and new offset, or -1 and original offset on error
func readLEB128(data []byte, offset int) (int, int) {
	value := 0
	shift := 0
	for i := 0; i < 8 && offset < len(data); i++ {
		b := data[offset]
		offset++
		value |= int(b&0x7F) << shift
		if b&0x80 == 0 {
			return value, offset
		}
		shift += 7
	}
	return -1, offset
}

// MKVWriterConfig holds configuration for MKV writer
type MKVWriterConfig struct {
	Width        int
	Height       int
	FrameRate    float64
	CodecPrivate []byte // AV1 Sequence Header for decoder initialization
}

// NewMKVWriter creates a new MKV container writer using ebml-go
func NewMKVWriter(path string) (*MKVWriter, error) {
	return NewMKVWriterWithConfig(path, MKVWriterConfig{
		Width:     640,
		Height:    480,
		FrameRate: 30,
	})
}

// NewMKVWriterWithConfig creates a new MKV container writer with specific config
func NewMKVWriterWithConfig(path string, cfg MKVWriterConfig) (*MKVWriter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	// Default values if not specified
	if cfg.Width == 0 {
		cfg.Width = 640
	}
	if cfg.Height == 0 {
		cfg.Height = 480
	}
	if cfg.FrameRate == 0 {
		cfg.FrameRate = 30
	}

	frameDurNs := int64(float64(time.Second) / cfg.FrameRate)

	// Define video track for AV1
	// CodecID for AV1 in Matroska is "V_AV1"
	track := webm.TrackEntry{
		Name:            "Video",
		TrackNumber:     1,
		TrackUID:        1,
		CodecID:         "V_AV1",
		TrackType:       1, // 1 = video
		DefaultDuration: uint64(frameDurNs),
		Video: &webm.Video{
			PixelWidth:  uint64(cfg.Width),
			PixelHeight: uint64(cfg.Height),
		},
	}

	// Add CodecPrivate (AV1 Sequence Header) if provided
	// This allows decoders to initialize without needing a keyframe
	if len(cfg.CodecPrivate) > 0 {
		track.CodecPrivate = cfg.CodecPrivate
	}

	tracks := []webm.TrackEntry{track}

	// Create block writers using ebml-go
	writers, err := webm.NewSimpleBlockWriter(file, tracks)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("failed to create block writer: %w", err)
	}

	if len(writers) == 0 {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("no block writers created")
	}

	w := &MKVWriter{
		file:        file,
		path:        path,
		startTime:   time.Now(),
		width:       cfg.Width,
		height:      cfg.Height,
		frameRate:   cfg.FrameRate,
		frameDurNs:  frameDurNs,
		hasVideo:    true,
		blockWriter: writers[0],
	}

	return w, nil
}

// WriteFrame writes an encoded AV1 frame to the MKV container
func (w *MKVWriter) WriteFrame(data []byte, ts time.Time) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed.Load() {
		return 0, fmt.Errorf("writer is closed")
	}

	if w.blockWriter == nil {
		return 0, fmt.Errorf("block writer not initialized")
	}

	// Calculate timestamp in nanoseconds relative to start
	relativeTime := ts.Sub(w.startTime)
	if relativeTime < 0 {
		relativeTime = time.Duration(w.frameCount) * time.Duration(w.frameDurNs)
	}

	// Detect actual AV1 keyframes by parsing the OBU bitstream
	// Keyframes contain a Sequence Header OBU
	keyframe := isAV1Keyframe(data)

	// Cache sequence header from first keyframe for later use
	if keyframe && w.sequenceHeader == nil {
		if seqHdr := extractSequenceHeader(data); seqHdr != nil {
			w.sequenceHeader = seqHdr
		}
	}

	// For the first frame, if it's not a keyframe but we have a cached sequence header,
	// prepend it to make the segment playable from the start
	frameData := data
	if w.frameCount == 0 && !keyframe && w.sequenceHeader != nil {
		frameData = append(w.sequenceHeader, data...)
		keyframe = true // Now it starts with sequence header, mark as keyframe
	}

	_, err := w.blockWriter.Write(keyframe, int64(relativeTime.Nanoseconds()/1000), frameData)
	if err != nil {
		return 0, fmt.Errorf("failed to write block: %w", err)
	}

	w.frameCount++
	w.size += int64(len(data)) // Track original data size
	return len(data), nil
}

// Close closes the MKV writer and finalizes the container
func (w *MKVWriter) Close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	// Close the block writer first (this finalizes clusters)
	if w.blockWriter != nil {
		if err := w.blockWriter.Close(); err != nil {
			// Log but continue to close file
			_ = err
		}
	}

	// Close the file
	return w.file.Close()
}

// GetStats returns writer statistics
func (w *MKVWriter) GetStats() (frameCount, size int64, duration time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.frameCount, w.size, time.Since(w.startTime)
}
