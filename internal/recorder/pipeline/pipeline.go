package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

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

// SegmenterMetrics tracks segmenter performance
type SegmenterMetrics struct {
	SegmentsCreated   atomic.Uint64
	SegmentsCompleted atomic.Uint64
	SegmentsFailed    atomic.Uint64
	BytesWritten      atomic.Uint64
	FramesWritten     atomic.Uint64
}

// MKVWriter handles MKV container writing (placeholder mux)
type MKVWriter struct {
	file       *os.File
	path       string
	startTime  time.Time
	frameCount int64
	size       int64
	hasVideo   bool
	hasAudio   bool
	width      int
	height     int
	frameRate  float64

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

	// Create MKV writer
	writer, err := NewMKVWriter(segment.TempPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create MKV writer: %w", err)
	}
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
	// Fast path: read lock to find the active segment
	s.mu.RLock()
	segment := s.segments[recordingID]
	s.mu.RUnlock()

	// Lazy-create a segment if missing
	if segment == nil {
		var err error
		if segment, err = s.NewSegment(recordingID); err != nil {
			return fmt.Errorf("failed to create segment: %w", err)
		}
	}

	segment.mu.Lock()
	defer segment.mu.Unlock()

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
	return nil
}

// ShouldRotate checks if a segment should be rotated
// NOTE: The returned *Segment is a snapshot; do not assume it remains current after this call.
func (s *Segmenter) ShouldRotate(recordingID string) (*Segment, bool) {
	s.mu.RLock()
	segment := s.segments[recordingID]
	s.mu.RUnlock()

	if segment == nil {
		return nil, false
	}

	segment.mu.RLock()
	defer segment.mu.RUnlock()

	// Duration-based
	if time.Since(segment.StartTime) >= s.segmentDuration {
		return segment, true
	}
	// Size-based (> 100MB)
	if segment.Size > 100*1024*1024 {
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

// calculateChecksum computes SHA256 checksum of a file (placeholder)
func (s *Segmenter) calculateChecksum(path string) string {
	// TODO: compute real SHA256
	return fmt.Sprintf("sha256_%s", filepath.Base(path))
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

// Cleanup removes the segment file from disk (only after upload)
func (seg *Segment) Cleanup() error {
	seg.mu.Lock()
	defer seg.mu.Unlock()

	if seg.Status != SegmentStatusUploaded {
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

// NewMKVWriter creates a new MKV container writer (placeholder)
func NewMKVWriter(path string) (*MKVWriter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}
	w := &MKVWriter{
		file:      file,
		path:      path,
		startTime: time.Now(),
	}
	// Write header
	if err := w.writeHeader(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return w, nil
}

// writeHeader writes the MKV container header (placeholder)
func (w *MKVWriter) writeHeader() error {
	header := []byte("MKV_HEADER_PLACEHOLDER")
	_, err := w.file.Write(header)
	return err
}

// WriteFrame writes an encoded frame to the MKV container
func (w *MKVWriter) WriteFrame(data []byte, _ time.Time) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed.Load() {
		return 0, fmt.Errorf("writer is closed")
	}
	n, err := w.file.Write(data)
	if err != nil {
		return n, err
	}
	w.frameCount++
	w.size += int64(n)
	return n, nil
}

// Close closes the MKV writer
func (w *MKVWriter) Close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	// TODO: write cues/index if needed
	return w.file.Close()
}

// GetStats returns writer statistics
func (w *MKVWriter) GetStats() (frameCount, size int64, duration time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.frameCount, w.size, time.Since(w.startTime)
}
