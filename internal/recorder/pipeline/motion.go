package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MotionRecorder handles motion-triggered recording
type MotionRecorder struct {
	segmenter        *Segmenter
	preBufferSize    time.Duration
	postBufferSize   time.Duration
	minEventDuration time.Duration
	maxEventDuration time.Duration

	activeEvents map[string]*MotionEvent
	mu           sync.RWMutex

	metrics       MotionMetrics
	confidenceSum float64 // running sum for average confidence
}

// MotionEvent represents an active motion recording event
type MotionEvent struct {
	ID                string
	RecordingID       string
	StartTime         time.Time
	LastMotionTime    time.Time
	EndTime           time.Time
	TriggerConfidence float64
	PeakConfidence    float64
	MotionRegions     []MotionRegion
	PreBufferFrames   int
	Status            string

	postMotionTimer *time.Timer
	segments        []*Segment
	mu              sync.Mutex
}

// MotionRegion represents a region where motion was detected
type MotionRegion struct {
	X, Y, Width, Height int
	Confidence          float64
	Timestamp           time.Time
}

// MotionMetrics tracks motion recording statistics
type MotionMetrics struct {
	EventsStarted     int64
	EventsCompleted   int64
	EventsCancelled   int64
	TotalDuration     time.Duration
	AverageConfidence float64 // computed on GetMetrics()
	FalsePositives    int64
}

// MotionTrigger represents a motion detection trigger
type MotionTrigger struct {
	Timestamp  time.Time
	Confidence float64
	Regions    []MotionRegion
	FrameData  []byte // Optional: include frame that triggered motion
}

// MotionConfig contains motion recording configuration
type MotionConfig struct {
	PreBufferDuration  time.Duration
	PostBufferDuration time.Duration
	MinEventDuration   time.Duration
	MaxEventDuration   time.Duration
	MinConfidence      float64
}

// NewMotionRecorder creates a new motion-triggered recorder
func NewMotionRecorder(segmenter *Segmenter, config MotionConfig) *MotionRecorder {
	return &MotionRecorder{
		segmenter:        segmenter,
		preBufferSize:    config.PreBufferDuration,
		postBufferSize:   config.PostBufferDuration,
		minEventDuration: config.MinEventDuration,
		maxEventDuration: config.MaxEventDuration,
		activeEvents:     make(map[string]*MotionEvent),
	}
}

// StartEvent begins a new motion-triggered recording
func (mr *MotionRecorder) StartEvent(trigger MotionTrigger, preBufferFrames [][]byte) (*MotionEvent, error) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	// Create new event
	event := &MotionEvent{
		ID:                uuid.New().String(),
		RecordingID:       fmt.Sprintf("motion_%d", time.Now().Unix()),
		StartTime:         trigger.Timestamp.Add(-mr.preBufferSize),
		LastMotionTime:    trigger.Timestamp,
		TriggerConfidence: trigger.Confidence,
		PeakConfidence:    trigger.Confidence,
		MotionRegions:     trigger.Regions,
		PreBufferFrames:   len(preBufferFrames),
		Status:            "recording",
		segments:          make([]*Segment, 0),
	}

	// Create recording segment
	segment, err := mr.segmenter.NewSegment(event.RecordingID)
	if err != nil {
		return nil, fmt.Errorf("failed to create segment: %w", err)
	}
	event.segments = append(event.segments, segment)

	// Write pre-buffer frames if provided (assume ~30fps)
	if len(preBufferFrames) > 0 {
		mr.segmenter.logger.Infow("Writing pre-buffer frames",
			"event_id", event.ID,
			"frames", len(preBufferFrames))

		for i, frameData := range preBufferFrames {
			frameTime := trigger.Timestamp.Add(-mr.preBufferSize + time.Duration(i)*33*time.Millisecond)
			if err := mr.segmenter.WriteFrame(event.RecordingID, frameData, frameTime); err != nil {
				mr.segmenter.logger.Errorw("Failed to write pre-buffer frame",
					"event_id", event.ID,
					"frame", i,
					"error", err)
			}
		}
	}

	// Setup post-motion timer (ends the event if no more motion)
	event.postMotionTimer = time.AfterFunc(mr.postBufferSize, func() {
		_, _ = mr.EndEvent(event.ID, "timeout")
	})

	mr.activeEvents[event.ID] = event
	mr.metrics.EventsStarted++
	mr.confidenceSum += trigger.Confidence

	mr.segmenter.logger.Infow("Motion event started",
		"event_id", event.ID,
		"confidence", trigger.Confidence,
		"regions", len(trigger.Regions),
		"pre_buffer_frames", len(preBufferFrames))

	return event, nil
}

// ExtendEvent extends an active motion event with new motion
func (mr *MotionRecorder) ExtendEvent(eventID string, trigger MotionTrigger) error {
	mr.mu.RLock()
	event, exists := mr.activeEvents[eventID]
	mr.mu.RUnlock()
	if !exists {
		return fmt.Errorf("event %s not found", eventID)
	}

	event.mu.Lock()
	defer event.mu.Unlock()

	event.LastMotionTime = trigger.Timestamp
	if trigger.Confidence > event.PeakConfidence {
		event.PeakConfidence = trigger.Confidence
	}
	event.MotionRegions = append(event.MotionRegions, trigger.Regions...)

	// Reset post-motion timer
	if event.postMotionTimer != nil {
		event.postMotionTimer.Stop()
		event.postMotionTimer.Reset(mr.postBufferSize)
	}

	// Cap max duration
	if time.Since(event.StartTime) > mr.maxEventDuration {
		mr.segmenter.logger.Warnw("Motion event exceeded max duration, ending",
			"event_id", eventID,
			"duration", time.Since(event.StartTime))
		go mr.EndEvent(eventID, "max_duration")
	}
	return nil
}

// ProcessFrame processes a frame for an active motion event
func (mr *MotionRecorder) ProcessFrame(eventID string, frameData []byte, timestamp time.Time) error {
	mr.mu.RLock()
	event, exists := mr.activeEvents[eventID]
	mr.mu.RUnlock()
	if !exists {
		return fmt.Errorf("event %s not found", eventID)
	}
	return mr.segmenter.WriteFrame(event.RecordingID, frameData, timestamp)
}

// EndEvent ends a motion recording event
func (mr *MotionRecorder) EndEvent(eventID string, reason string) (*MotionEvent, error) {
	mr.mu.Lock()
	event, exists := mr.activeEvents[eventID]
	if !exists {
		mr.mu.Unlock()
		return nil, fmt.Errorf("event %s not found", eventID)
	}
	delete(mr.activeEvents, eventID)
	mr.mu.Unlock()

	event.mu.Lock()
	defer event.mu.Unlock()

	// Stop timer
	if event.postMotionTimer != nil {
		event.postMotionTimer.Stop()
		event.postMotionTimer = nil
	}

	event.EndTime = time.Now()
	event.Status = "completed"

	duration := event.EndTime.Sub(event.StartTime)

	// If too short, mark as discarded
	if duration < mr.minEventDuration {
		mr.segmenter.logger.Infow("Motion event too short, discarding",
			"event_id", eventID,
			"duration", duration,
			"reason", reason)

		event.Status = "discarded"
		mr.metrics.FalsePositives++
		mr.metrics.EventsCancelled++

		// NOTE: still finalizes (enqueues). Add a Discard() path if you truly want to drop.
		mr.segmenter.Finalize(event.RecordingID)
		return event, nil
	}

	// Finalize recording segment
	if seg := mr.segmenter.Finalize(event.RecordingID); seg != nil {
		event.segments = append(event.segments, seg)
	}

	mr.metrics.EventsCompleted++
	mr.metrics.TotalDuration += duration

	mr.segmenter.logger.Infow("Motion event completed",
		"event_id", eventID,
		"duration", duration,
		"peak_confidence", event.PeakConfidence,
		"segments", len(event.segments),
		"reason", reason)

	return event, nil
}

// GetActiveEvents returns all currently active motion events
func (mr *MotionRecorder) GetActiveEvents() []*MotionEvent {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	events := make([]*MotionEvent, 0, len(mr.activeEvents))
	for _, event := range mr.activeEvents {
		events = append(events, event)
	}
	return events
}

// HandleMotionTrigger processes a new motion detection
func (mr *MotionRecorder) HandleMotionTrigger(_ context.Context, trigger MotionTrigger, preBufferFrames [][]byte) (*MotionEvent, error) {
	// Try to join an existing event still within postBuffer window
	mr.mu.RLock()
	var activeEvent *MotionEvent
	now := time.Now()
	for _, event := range mr.activeEvents {
		if now.Sub(event.LastMotionTime) < mr.postBufferSize {
			activeEvent = event
			break
		}
	}
	mr.mu.RUnlock()

	if activeEvent != nil {
		if err := mr.ExtendEvent(activeEvent.ID, trigger); err != nil {
			return nil, err
		}
		return activeEvent, nil
	}

	// Start a new event
	return mr.StartEvent(trigger, preBufferFrames)
}

// CleanupExpiredEvents removes events that exceeded max duration
func (mr *MotionRecorder) CleanupExpiredEvents() {
	mr.mu.RLock()
	expired := make([]string, 0)
	now := time.Now()
	for id, event := range mr.activeEvents {
		if now.Sub(event.StartTime) > mr.maxEventDuration {
			expired = append(expired, id)
		}
	}
	mr.mu.RUnlock()

	for _, id := range expired {
		_, _ = mr.EndEvent(id, "expired")
	}
}

// GetMetrics returns motion recorder metrics (AverageConfidence computed)
func (mr *MotionRecorder) GetMetrics() MotionMetrics {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	m := mr.metrics
	if m.EventsStarted > 0 {
		m.AverageConfidence = mr.confidenceSum / float64(m.EventsStarted)
	}
	return m
}

// IsMotionActive checks if any motion events are currently active
func (mr *MotionRecorder) IsMotionActive() bool {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	return len(mr.activeEvents) > 0
}

// GetEventByRecordingID finds an event by its recording ID
func (mr *MotionRecorder) GetEventByRecordingID(recordingID string) *MotionEvent {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	for _, event := range mr.activeEvents {
		if event.RecordingID == recordingID {
			return event
		}
	}
	return nil
}
