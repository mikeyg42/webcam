package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ContinuousRecorder handles continuous recording
type ContinuousRecorder struct {
	segmenter        *Segmenter
	rotationInterval time.Duration
	retentionPeriod  time.Duration
	maxSegmentSize   int64

	recordings map[string]*ContinuousRecording
	mu         sync.RWMutex

	metrics ContinuousMetrics
}

// ContinuousRecording represents an ongoing continuous recording
type ContinuousRecording struct {
	ID             string
	StartTime      time.Time
	CurrentSegment *Segment
	Segments       []*Segment
	Status         string
	TotalSize      int64
	TotalFrames    int64
	TotalDuration  time.Duration

	lastRotation time.Time
	mu           sync.RWMutex
}

// ContinuousMetrics tracks continuous recording statistics
type ContinuousMetrics struct {
	ActiveRecordings atomic.Int32
	TotalSegments    atomic.Uint64
	TotalBytes       atomic.Uint64
	TotalFrames      atomic.Uint64
	SegmentRotations atomic.Uint64
	CleanedSegments  atomic.Uint64
}

// ContinuousConfig contains continuous recording configuration
type ContinuousConfig struct {
	RotationInterval time.Duration
	RetentionPeriod  time.Duration
	MaxSegmentSize   int64
	AutoStart        bool
}

// NewContinuousRecorder creates a new continuous recorder
func NewContinuousRecorder(segmenter *Segmenter, config ContinuousConfig) *ContinuousRecorder {
	return &ContinuousRecorder{
		segmenter:        segmenter,
		rotationInterval: config.RotationInterval,
		retentionPeriod:  config.RetentionPeriod,
		maxSegmentSize:   config.MaxSegmentSize,
		recordings:       make(map[string]*ContinuousRecording),
	}
}

// StartRecording begins a new continuous recording
func (cr *ContinuousRecorder) StartRecording(recordingID string) (*ContinuousRecording, error) {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if _, exists := cr.recordings[recordingID]; exists {
		return nil, fmt.Errorf("recording %s already active", recordingID)
	}

	rec := &ContinuousRecording{
		ID:           recordingID,
		StartTime:    time.Now(),
		Status:       "recording",
		Segments:     make([]*Segment, 0),
		lastRotation: time.Now(),
	}

	seg, err := cr.segmenter.NewSegment(recordingID)
	if err != nil {
		return nil, fmt.Errorf("failed to create initial segment: %w", err)
	}
	rec.CurrentSegment = seg
	rec.Segments = append(rec.Segments, seg)

	cr.recordings[recordingID] = rec
	cr.metrics.ActiveRecordings.Add(1)

	cr.segmenter.logger.Infow("Started continuous recording",
		"recording_id", recordingID,
		"segment_id", seg.ID)

	return rec, nil
}

// StopRecording stops an active continuous recording
func (cr *ContinuousRecorder) StopRecording(recordingID string) (*ContinuousRecording, error) {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	rec, exists := cr.recordings[recordingID]
	if !exists {
		return nil, fmt.Errorf("recording %s not found", recordingID)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	// Finalize current segment
	if rec.CurrentSegment != nil {
		if finalSeg := cr.segmenter.Finalize(recordingID); finalSeg != nil {
			rec.Segments = append(rec.Segments, finalSeg)
		}
	}

	rec.Status = "stopped"
	rec.TotalDuration = time.Since(rec.StartTime)

	delete(cr.recordings, recordingID)
	cr.metrics.ActiveRecordings.Add(-1)

	cr.segmenter.logger.Infow("Stopped continuous recording",
		"recording_id", recordingID,
		"duration", rec.TotalDuration,
		"segments", len(rec.Segments),
		"total_size", rec.TotalSize)

	return rec, nil
}

// ProcessFrame processes a frame for continuous recording
func (cr *ContinuousRecorder) ProcessFrame(recordingID string, frameData []byte, timestamp time.Time) error {
	cr.mu.RLock()
	rec, exists := cr.recordings[recordingID]
	cr.mu.RUnlock()
	if !exists {
		return fmt.Errorf("recording %s not found", recordingID)
	}

	// Write frame to segmenter
	if err := cr.segmenter.WriteFrame(recordingID, frameData, timestamp); err != nil {
		return fmt.Errorf("failed to write frame: %w", err)
	}

	// Update per-recording stats
	rec.mu.Lock()
	rec.TotalFrames++
	rec.TotalSize += int64(len(frameData))
	cr.metrics.TotalBytes.Add(uint64(len(frameData)))
	cr.metrics.TotalFrames.Add(1)
	rec.mu.Unlock()

	// Rotation heuristic: time or size
	if cr.shouldRotate(rec) {
		if err := cr.rotateSegment(rec); err != nil {
			cr.segmenter.logger.Errorw("Failed to rotate segment",
				"recording_id", recordingID,
				"error", err)
		}
	}
	return nil
}

// shouldRotate checks if segment rotation is needed
func (cr *ContinuousRecorder) shouldRotate(rec *ContinuousRecording) bool {
	rec.mu.RLock()
	defer rec.mu.RUnlock()

	if rec.CurrentSegment == nil {
		return false
	}
	// Time-based
	if cr.rotationInterval > 0 && time.Since(rec.lastRotation) >= cr.rotationInterval {
		return true
	}
	// Size-based (heuristic; reads Size without segment lock)
	if cr.maxSegmentSize > 0 && rec.CurrentSegment.Size >= cr.maxSegmentSize {
		return true
	}
	return false
}

// rotateSegment rotates to a new segment
func (cr *ContinuousRecorder) rotateSegment(rec *ContinuousRecording) error {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	// Finalize current segment and append
	if rec.CurrentSegment != nil {
		if finalized := cr.segmenter.Finalize(rec.ID); finalized != nil {
			rec.Segments = append(rec.Segments, finalized)
			cr.metrics.TotalSegments.Add(1)
		}
	}

	// Create new segment
	newSeg, err := cr.segmenter.NewSegment(rec.ID)
	if err != nil {
		return fmt.Errorf("failed to create new segment: %w", err)
	}
	rec.CurrentSegment = newSeg
	rec.Segments = append(rec.Segments, newSeg)
	rec.lastRotation = time.Now()

	cr.metrics.SegmentRotations.Add(1)

	cr.segmenter.logger.Debugw("Rotated segment",
		"recording_id", rec.ID,
		"new_segment_id", newSeg.ID,
		"total_segments", len(rec.Segments))

	return nil
}

// CleanupOldSegments removes segments older than retention period
func (cr *ContinuousRecorder) CleanupOldSegments(_ context.Context) error {
	cr.mu.RLock()
	recordings := make([]*ContinuousRecording, 0, len(cr.recordings))
	for _, r := range cr.recordings {
		recordings = append(recordings, r)
	}
	cr.mu.RUnlock()

	cutoff := time.Now().Add(-cr.retentionPeriod)
	totalCleaned := 0

	for _, rec := range recordings {
		rec.mu.Lock()

		toClean := make([]*Segment, 0)
		remaining := make([]*Segment, 0, len(rec.Segments))
		for _, seg := range rec.Segments {
			if !seg.EndTime.IsZero() && seg.EndTime.Before(cutoff) && seg.Status == SegmentStatusUploaded {
				toClean = append(toClean, seg)
			} else {
				remaining = append(remaining, seg)
			}
		}
		rec.Segments = remaining
		rec.mu.Unlock()

		for _, seg := range toClean {
			if err := seg.Cleanup(); err != nil {
				cr.segmenter.logger.Errorw("Failed to cleanup segment",
					"segment_id", seg.ID,
					"error", err)
			} else {
				totalCleaned++
				cr.metrics.CleanedSegments.Add(1)
			}
		}
	}

	if totalCleaned > 0 {
		cr.segmenter.logger.Infow("Cleaned up old segments",
			"count", totalCleaned,
			"cutoff", cutoff)
	}
	return nil
}

// GetRecording returns a continuous recording by ID
func (cr *ContinuousRecorder) GetRecording(recordingID string) (*ContinuousRecording, error) {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	rec, exists := cr.recordings[recordingID]
	if !exists {
		return nil, fmt.Errorf("recording %s not found", recordingID)
	}
	return rec, nil
}

// GetActiveRecordings returns all active continuous recordings
func (cr *ContinuousRecorder) GetActiveRecordings() []*ContinuousRecording {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	res := make([]*ContinuousRecording, 0, len(cr.recordings))
	for _, r := range cr.recordings {
		res = append(res, r)
	}
	return res
}

// GetMetrics returns continuous recorder metrics
func (cr *ContinuousRecorder) GetMetrics() map[string]interface{} {
	cr.mu.RLock()
	active := len(cr.recordings)
	cr.mu.RUnlock()

	return map[string]interface{}{
		"active_recordings": active,
		"total_segments":    cr.metrics.TotalSegments.Load(),
		"total_bytes":       cr.metrics.TotalBytes.Load(),
		"total_frames":      cr.metrics.TotalFrames.Load(),
		"segment_rotations": cr.metrics.SegmentRotations.Load(),
		"cleaned_segments":  cr.metrics.CleanedSegments.Load(),
	}
}

// RunMaintenanceTask performs periodic maintenance
func (cr *ContinuousRecorder) RunMaintenanceTask(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := cr.CleanupOldSegments(ctx); err != nil {
				cr.segmenter.logger.Errorw("Maintenance task failed", "error", err)
			}
		}
	}
}

// PauseRecording pauses an active continuous recording
func (cr *ContinuousRecorder) PauseRecording(recordingID string) error {
	cr.mu.RLock()
	rec, exists := cr.recordings[recordingID]
	cr.mu.RUnlock()
	if !exists {
		return fmt.Errorf("recording %s not found", recordingID)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if rec.Status != "recording" {
		return fmt.Errorf("recording %s is not active (status: %s)", recordingID, rec.Status)
	}
	rec.Status = "paused"

	cr.segmenter.logger.Infow("Paused continuous recording",
		"recording_id", recordingID)
	return nil
}

// ResumeRecording resumes a paused continuous recording
func (cr *ContinuousRecorder) ResumeRecording(recordingID string) error {
	cr.mu.RLock()
	rec, exists := cr.recordings[recordingID]
	cr.mu.RUnlock()
	if !exists {
		return fmt.Errorf("recording %s not found", recordingID)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if rec.Status != "paused" {
		return fmt.Errorf("recording %s is not paused (status: %s)", recordingID, rec.Status)
	}

	// Create new segment for resumed recording
	newSeg, err := cr.segmenter.NewSegment(recordingID)
	if err != nil {
		return fmt.Errorf("failed to create segment for resume: %w", err)
	}

	rec.CurrentSegment = newSeg
	rec.Segments = append(rec.Segments, newSeg)
	rec.Status = "recording"
	rec.lastRotation = time.Now()

	cr.segmenter.logger.Infow("Resumed continuous recording",
		"recording_id", recordingID,
		"new_segment_id", newSeg.ID)

	return nil
}
