package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
type pendingFrame struct {
	data      []byte
	timestamp time.Time
}

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

	writer              *MKVWriter
	pendingWriterConfig *MKVWriterConfig // Deferred MKV creation until first keyframe
	cachedSeqHdr        []byte           // Sequence header available at segment creation time
	pendingFrames       []pendingFrame   // Frames buffered before writer is created
	lastSyncTime        time.Time        // Last time we called fsync
	syncInterval        time.Duration    // Interval between fsyncs (default 5s)
	mu                  sync.RWMutex
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

	// Audio config for MKV writer
	audioEnabled    bool
	audioSampleRate int
	audioChannels   int

	// Cached AV1 Sequence Header for sharing between segments
	// This ensures new segments can start playable even without a keyframe
	cachedSequenceHeader []byte

	logger Logger

	segments     map[string]*Segment // recordingID -> current segment
	segmentIndex map[string]int      // recordingID -> next segment index (monotonic, never reused)
	mu           sync.RWMutex

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

// SetAudioConfig sets the audio configuration for MKV writing
func (s *Segmenter) SetAudioConfig(enabled bool, sampleRate, channels int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audioEnabled = enabled
	s.audioSampleRate = sampleRate
	s.audioChannels = channels
}

// SegmenterMetrics tracks segmenter performance
type SegmenterMetrics struct {
	SegmentsCreated   atomic.Uint64
	SegmentsCompleted atomic.Uint64
	SegmentsFailed    atomic.Uint64
	BytesWritten      atomic.Uint64
	FramesWritten     atomic.Uint64
}

// RecoveryReport contains results from crash recovery
type RecoveryReport struct {
	OrphanedFiles    int           // Total orphaned .tmp files found
	RecoveredFiles   int           // Files successfully recovered (.recovered.mkv)
	QuarantinedFiles int           // Files that failed validation (.quarantine.mkv)
	RecoveryTime     time.Duration // Time taken for recovery
}

// MKVWriter handles MKV container writing using ebml-go
type MKVWriter struct {
	file           *os.File
	path           string
	startTime      time.Time
	frameCount     int64
	audioCount     int64 // Audio frame count
	size           int64
	hasVideo       bool
	hasAudio       bool
	width          int
	height         int
	frameRate      float64
	frameDurNs     int64 // frame duration in nanoseconds
	blockWriter    webm.BlockWriteCloser
	audioWriter    webm.BlockWriteCloser // Audio track writer
	sequenceHeader []byte                // Cached AV1 Sequence Header for prepending to non-keyframe starts

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
		segmentIndex:    make(map[string]int),
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

	// Run crash recovery to handle orphaned files from previous runs
	report := s.recoverFromCrash()
	if report.OrphanedFiles > 0 || report.RecoveredFiles > 0 || report.QuarantinedFiles > 0 {
		s.logger.Infow("Crash recovery completed",
			"orphaned_files", report.OrphanedFiles,
			"recovered_files", report.RecoveredFiles,
			"quarantined_files", report.QuarantinedFiles,
			"recovery_time", report.RecoveryTime)
	}

	return nil
}

// NewSegment creates a new segment for a recording.
// This acquires the lock internally - for lock-held scenarios use newSegmentLocked.
func (s *Segmenter) NewSegment(recordingID string) (*Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newSegmentLocked(recordingID)
}

// newSegmentLocked creates a new segment (requires s.mu to be held).
// This is the core segment creation logic used by both NewSegment and WriteFrame.
func (s *Segmenter) newSegmentLocked(recordingID string) (*Segment, error) {
	// Finalize existing segment if present
	if existing, ok := s.segments[recordingID]; ok && existing != nil {
		s.finalizeSegmentLocked(existing)
	}

	// Get monotonic segment index (never reused even after file deletion)
	index := s.getNextSegmentIndexLocked(recordingID)

	segment := &Segment{
		ID:           uuid.New().String(),
		RecordingID:  recordingID,
		Index:        index,
		StartTime:    time.Now(),
		Status:       SegmentStatusRecording,
		TempPath:     filepath.Join(s.tempDir, fmt.Sprintf("%s_%03d.mkv.tmp", recordingID, index)),
		FilePath:     filepath.Join(s.outputDir, fmt.Sprintf("%s_%03d.mkv", recordingID, index)),
		lastSyncTime: time.Now(),
		syncInterval: 5 * time.Second,
	}

	// Store the MKV config but defer writer creation until the first keyframe arrives.
	// This ensures CodecPrivate (AV1 Sequence Header) is always present in the track header,
	// which is required for any decoder (browser or ffmpeg) to play the file.
	segment.pendingWriterConfig = &MKVWriterConfig{
		Width:        s.videoWidth,
		Height:       s.videoHeight,
		FrameRate:    s.frameRate,
		CodecPrivate: s.cachedSequenceHeader,
		AudioEnabled: s.audioEnabled,
		SampleRate:   s.audioSampleRate,
		Channels:     s.audioChannels,
	}
	segment.cachedSeqHdr = s.cachedSequenceHeader

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
	// Atomic segment lookup/creation under single lock to prevent race conditions.
	// Two goroutines (e.g., pre-motion buffer + live frames) could both see nil and
	// race to create segments if we released the lock between check and create.
	s.mu.Lock()
	segment := s.segments[recordingID]
	if segment == nil {
		// Create segment while holding the lock - prevents race where multiple
		// goroutines see nil and all try to create segments
		var err error
		segment, err = s.newSegmentLocked(recordingID)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("failed to create segment: %w", err)
		}
	}

	// Extract and cache sequence header while we have s.mu
	if s.cachedSequenceHeader == nil {
		if seqHdr := extractSequenceHeader(data); len(seqHdr) > 0 {
			s.cachedSequenceHeader = seqHdr
		}
	}
	s.mu.Unlock()

	// Now lock the segment for writing
	segment.mu.Lock()
	defer segment.mu.Unlock()

	// Lazy writer creation: buffer frames until we have a sequence header,
	// then create the MKV writer with CodecPrivate set correctly.
	if segment.writer == nil && segment.pendingWriterConfig != nil {
		seqHdr := segment.cachedSeqHdr
		if seqHdr == nil {
			seqHdr = extractSequenceHeader(data)
		}
		if seqHdr != nil {
			// We have a sequence header — create the writer now
			segment.pendingWriterConfig.CodecPrivate = seqHdr
			writer, err := NewMKVWriterWithConfig(segment.TempPath, *segment.pendingWriterConfig)
			if err != nil {
				return fmt.Errorf("failed to create MKV writer: %w", err)
			}
			writer.sequenceHeader = seqHdr
			segment.writer = writer
			segment.pendingWriterConfig = nil

			// Flush any frames that were buffered while waiting
			for _, pf := range segment.pendingFrames {
				if _, err := writer.WriteFrame(pf.data, pf.timestamp); err != nil {
					s.logger.Warnw("Failed to write buffered frame", "error", err)
				}
			}
			segment.pendingFrames = nil
		} else {
			// Still no sequence header — buffer this frame (limit to avoid unbounded growth)
			if len(segment.pendingFrames) < 300 {
				buf := make([]byte, len(data))
				copy(buf, data)
				segment.pendingFrames = append(segment.pendingFrames, pendingFrame{data: buf, timestamp: timestamp})
			}
			return nil
		}
	}

	if segment.writer == nil {
		return fmt.Errorf("segment writer is nil")
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

	// Periodic fsync to ensure data durability
	if time.Since(segment.lastSyncTime) > segment.syncInterval {
		if syncErr := segment.writer.Sync(); syncErr != nil {
			s.logger.Warnw("Failed to fsync segment",
				"segment_id", segment.ID,
				"error", syncErr)
		}
		segment.lastSyncTime = time.Now()
	}

	return nil
}

// WriteAudioFrame writes an encoded audio frame to the current segment
func (s *Segmenter) WriteAudioFrame(recordingID string, data []byte, timestamp time.Time) error {
	s.mu.RLock()
	segment := s.segments[recordingID]
	s.mu.RUnlock()

	if segment == nil {
		return fmt.Errorf("no active segment for recording %s", recordingID)
	}

	segment.mu.Lock()
	defer segment.mu.Unlock()

	if segment.writer == nil {
		return fmt.Errorf("segment writer is nil")
	}

	_, err := segment.writer.WriteAudioFrame(data, timestamp)
	if err != nil {
		segment.Status = SegmentStatusFailed
		segment.Error = err
		s.metrics.SegmentsFailed.Add(1)
		return fmt.Errorf("failed to write audio frame: %w", err)
	}

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

	// Full durability sequence for crash resilience:
	// 1. Fsync file data
	// 2. Fsync temp directory (ensures file's directory entry is durable)
	// 3. Rename (atomic move)
	// 4. Fsync output directory (ensures rename is durable)

	// Step 1: Fsync file data
	if f, err := os.OpenFile(segment.TempPath, os.O_RDONLY, 0); err == nil {
		if syncErr := f.Sync(); syncErr != nil {
			s.logger.Warnw("Failed to fsync segment data",
				"segment_id", segment.ID,
				"error", syncErr)
		}
		f.Close()
	}

	// Step 2: Fsync temp directory
	s.fsyncDir(s.tempDir)

	// Step 3: Atomic rename
	if err := os.Rename(segment.TempPath, segment.FilePath); err != nil {
		s.logger.Errorw("Failed to move segment file",
			"segment_id", segment.ID,
			"error", err)
		segment.Status = SegmentStatusFailed
		segment.Error = err
		s.metrics.SegmentsFailed.Add(1)
		return
	}

	// Step 4: Fsync output directory to make rename durable
	s.fsyncDir(s.outputDir)

	// Calculate checksum
	segment.Checksum = s.calculateChecksum(segment.FilePath)

	segment.Status = SegmentStatusCompleted
	s.metrics.SegmentsCompleted.Add(1)

	s.logger.Infow("Segment finalized",
		"segment_id", segment.ID,
		"duration", segment.Duration,
		"size", segment.Size,
		"frames", segment.FrameCount)
}

// GetCurrentSegment returns the current segment for a recording
func (s *Segmenter) GetCurrentSegment(recordingID string) *Segment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.segments[recordingID]
}

// getNextSegmentIndexLocked returns the next monotonic segment index for a recording.
// MUST be called with s.mu held. Indices are never reused, even after files are deleted.
func (s *Segmenter) getNextSegmentIndexLocked(recordingID string) int {
	index := s.segmentIndex[recordingID]
	s.segmentIndex[recordingID] = index + 1
	return index
}

// SetInitialSegmentIndex sets the starting segment index for a recording.
// This should be called when starting/resuming a recording, with the value
// from MAX(segment_index)+1 in the database to ensure crash persistence.
// Without this, a process restart would reset the counter to 0.
func (s *Segmenter) SetInitialSegmentIndex(recordingID string, index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.segmentIndex[recordingID] = index
}

// ClearRecordingState removes all state for a finished recording.
// This prevents memory leaks from accumulating segmentIndex entries.
func (s *Segmenter) ClearRecordingState(recordingID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.segmentIndex, recordingID)
	delete(s.segments, recordingID)
}

// fsyncDir fsyncs a directory to ensure directory entries are durable.
// This is required for crash resilience: without it, a power loss after rename
// could leave the directory entry missing even though the file data exists.
func (s *Segmenter) fsyncDir(dirPath string) {
	dir, err := os.Open(dirPath)
	if err != nil {
		s.logger.Warnw("Failed to open directory for fsync",
			"dir", dirPath,
			"error", err)
		return
	}
	defer dir.Close()

	if err := dir.Sync(); err != nil {
		s.logger.Warnw("Failed to fsync directory",
			"dir", dirPath,
			"error", err)
	}
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

// recoverFromCrash handles orphaned temporary files from crashes/power outages
func (s *Segmenter) recoverFromCrash() RecoveryReport {
	start := time.Now()
	report := RecoveryReport{}

	// Find all .tmp files (any age) - these are potentially incomplete recordings
	tmpFiles, err := filepath.Glob(filepath.Join(s.tempDir, "*.mkv.tmp"))
	if err != nil {
		s.logger.Warnw("Failed to glob for orphaned files", "error", err)
		return report
	}
	report.OrphanedFiles = len(tmpFiles)

	for _, tmpFile := range tmpFiles {
		info, err := os.Stat(tmpFile)
		if err != nil {
			continue
		}

		s.logger.Infow("Found orphaned segment",
			"file", tmpFile,
			"size", info.Size(),
			"modified", info.ModTime())

		if s.isValidMKV(tmpFile) {
			// Valid MKV structure - rename to .recovered.mkv for manual review
			recoveredPath := strings.TrimSuffix(tmpFile, ".tmp") + ".recovered.mkv"
			if err := os.Rename(tmpFile, recoveredPath); err != nil {
				s.logger.Warnw("Failed to rename recovered segment",
					"file", tmpFile,
					"error", err)
				continue
			}
			report.RecoveredFiles++
			s.logger.Infow("Recovered orphaned segment",
				"original", tmpFile,
				"recovered", recoveredPath,
				"size", info.Size())
		} else {
			// Failed validation - quarantine instead of delete for safety
			// Files may still be partially recoverable with specialized tools
			quarantinePath := strings.TrimSuffix(tmpFile, ".tmp") + ".quarantine.mkv"
			if err := os.Rename(tmpFile, quarantinePath); err != nil {
				s.logger.Warnw("Failed to quarantine corrupted segment",
					"file", tmpFile,
					"error", err)
				continue
			}
			report.QuarantinedFiles++
			s.logger.Warnw("Quarantined corrupted segment",
				"file", tmpFile,
				"quarantine", quarantinePath,
				"size", info.Size())
		}
	}

	report.RecoveryTime = time.Since(start)
	return report
}

// isValidMKV checks if a file is a valid, potentially recoverable MKV.
// This goes beyond just checking the magic bytes - it verifies:
// 1. Minimum file size (files too small can't contain useful data)
// 2. EBML header magic (0x1A 0x45 0xDF 0xA3)
// 3. Presence of Segment element (0x18 0x53 0x80 0x67)
// 4. Presence of at least one Cluster (0x1F 0x43 0xB6 0x75)
//
// Files that pass these checks are likely to be at least partially playable.
func (s *Segmenter) isValidMKV(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false
	}

	// Minimum size check: require at least 1KB to be worth recovering
	// (EBML header + Segment header + some actual content)
	const minRecoverableSize = 1024
	if info.Size() < minRecoverableSize {
		s.logger.Debugw("File too small to recover",
			"path", path,
			"size", info.Size(),
			"min_required", minRecoverableSize)
		return false
	}

	// Read first chunk of file for structure validation
	// Scan up to 256KB to ensure we find the first Cluster element,
	// which may appear after headers, tracks, and codec private data
	scanSize := int64(256 * 1024)
	if info.Size() < scanSize {
		scanSize = info.Size()
	}
	data := make([]byte, scanSize)
	n, err := f.Read(data)
	if err != nil || n < 4 {
		return false
	}
	data = data[:n]

	// Check 1: EBML header magic (0x1A 0x45 0xDF 0xA3)
	if data[0] != 0x1A || data[1] != 0x45 || data[2] != 0xDF || data[3] != 0xA3 {
		s.logger.Debugw("Invalid EBML magic", "path", path)
		return false
	}

	// Check 2: Look for Segment element ID (0x18 0x53 0x80 0x67)
	segmentID := []byte{0x18, 0x53, 0x80, 0x67}
	hasSegment := containsSequence(data, segmentID)
	if !hasSegment {
		s.logger.Debugw("No Segment element found", "path", path)
		return false
	}

	// Check 3: Look for at least one Cluster element ID (0x1F 0x43 0xB6 0x75)
	// A file without clusters has no actual media data
	clusterID := []byte{0x1F, 0x43, 0xB6, 0x75}
	hasCluster := containsSequence(data, clusterID)
	if !hasCluster {
		s.logger.Debugw("No Cluster element found", "path", path)
		return false
	}

	return true
}

// containsSequence checks if data contains the given byte sequence
func containsSequence(data, seq []byte) bool {
	if len(seq) == 0 || len(data) < len(seq) {
		return false
	}
	for i := 0; i <= len(data)-len(seq); i++ {
		match := true
		for j := 0; j < len(seq); j++ {
			if data[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// cleanupStaleSegments removes old temporary files (legacy method, kept for compatibility)
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
	s.mu.RUnlock()

	return map[string]interface{}{
		"segments_created":   s.metrics.SegmentsCreated.Load(),
		"segments_completed": s.metrics.SegmentsCompleted.Load(),
		"segments_failed":    s.metrics.SegmentsFailed.Load(),
		"bytes_written":      s.metrics.BytesWritten.Load(),
		"frames_written":     s.metrics.FramesWritten.Load(),
		"active_segments":    activeSegments,
	}
}

// AV1 OBU types for keyframe detection
const (
	obuTypeSequenceHeader = 1
	obuTypeFrameHeader    = 3
	obuTypeFrame          = 6
)

// IsAV1Keyframe detects if the AV1 OBU data represents a keyframe.
// Returns true if the data contains a Sequence Header OBU, which indicates
// an independently decodable frame (IDR/keyframe in AV1).
// This function is exported for use by the recording service during segment rotation.
func IsAV1Keyframe(data []byte) bool {
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
	AudioEnabled bool   // Whether to include audio track
	SampleRate   int    // Audio sample rate (48000 for Opus)
	Channels     int    // Audio channel count (2 for stereo)
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
	if cfg.AudioEnabled && cfg.SampleRate == 0 {
		cfg.SampleRate = 48000 // Default for Opus
	}
	if cfg.AudioEnabled && cfg.Channels == 0 {
		cfg.Channels = 2 // Stereo
	}

	frameDurNs := int64(float64(time.Second) / cfg.FrameRate)

	// Define video track for AV1
	// CodecID for AV1 in Matroska is "V_AV1"
	videoTrack := webm.TrackEntry{
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
		videoTrack.CodecPrivate = cfg.CodecPrivate
	}

	tracks := []webm.TrackEntry{videoTrack}

	// Add audio track if enabled
	// Audio data arrives as raw PCM (int16 little-endian) from FrameDistributor
	if cfg.AudioEnabled {
		audioTrack := webm.TrackEntry{
			Name:        "Audio",
			TrackNumber: 2,
			TrackUID:    2,
			CodecID:     "A_PCM/INT/LIT",
			TrackType:   2, // 2 = audio
			Audio: &webm.Audio{
				SamplingFrequency: float64(cfg.SampleRate),
				Channels:          uint64(cfg.Channels),
			},
		}
		tracks = append(tracks, audioTrack)
	}

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
		hasAudio:    cfg.AudioEnabled,
		blockWriter: writers[0],
	}

	// Store audio writer if audio is enabled
	if cfg.AudioEnabled && len(writers) > 1 {
		w.audioWriter = writers[1]
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

	// Calculate timestamp in milliseconds relative to start (ebml-go TimecodeScale = 1ms)
	relativeTime := ts.Sub(w.startTime)
	if relativeTime < 0 {
		relativeTime = time.Duration(w.frameCount) * time.Duration(w.frameDurNs)
	}

	// Detect actual AV1 keyframes by parsing the OBU bitstream
	// Keyframes contain a Sequence Header OBU
	keyframe := IsAV1Keyframe(data)

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

	_, err := w.blockWriter.Write(keyframe, int64(relativeTime.Milliseconds()), frameData)
	if err != nil {
		return 0, fmt.Errorf("failed to write block: %w", err)
	}

	w.frameCount++
	w.size += int64(len(data)) // Track original data size
	return len(data), nil
}

// WriteAudioFrame writes a raw PCM audio frame to the MKV container
func (w *MKVWriter) WriteAudioFrame(data []byte, ts time.Time) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed.Load() {
		return 0, fmt.Errorf("writer is closed")
	}

	if !w.hasAudio || w.audioWriter == nil {
		return 0, fmt.Errorf("audio track not configured")
	}

	// Calculate timestamp in milliseconds relative to start (ebml-go TimecodeScale = 1ms)
	relativeTime := ts.Sub(w.startTime)
	if relativeTime < 0 {
		relativeTime = 0
	}

	// Audio frames in MKV are always keyframes
	_, err := w.audioWriter.Write(true, int64(relativeTime.Milliseconds()), data)
	if err != nil {
		return 0, fmt.Errorf("failed to write audio block: %w", err)
	}

	w.audioCount++
	w.size += int64(len(data))
	return len(data), nil
}

// Close closes the MKV writer and finalizes the container
func (w *MKVWriter) Close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	// Close the video block writer first (this finalizes clusters)
	if w.blockWriter != nil {
		if err := w.blockWriter.Close(); err != nil {
			// Log but continue
			_ = err
		}
	}

	// Close the audio block writer if present
	if w.audioWriter != nil {
		if err := w.audioWriter.Close(); err != nil {
			// Log but continue
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

// GetAudioStats returns audio-specific statistics
func (w *MKVWriter) GetAudioStats() (audioCount int64, hasAudio bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.audioCount, w.hasAudio
}

// Sync flushes data to disk via fsync
func (w *MKVWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil && !w.closed.Load() {
		return w.file.Sync()
	}
	return nil
}
