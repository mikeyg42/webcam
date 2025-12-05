// internal/recorder/recorder.go
package recorder

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/recorder/buffer"
	"github.com/mikeyg42/webcam/internal/recorder/encoder"
	"github.com/mikeyg42/webcam/internal/recorder/pipeline"
	"github.com/mikeyg42/webcam/internal/recorder/recorderlog"
	"github.com/mikeyg42/webcam/internal/recorder/storage"
)

// MotionEvent represents a motion detection trigger
type MotionEvent struct {
	Timestamp  time.Time
	Confidence float64
	Regions    []image.Rectangle
}

// RecordingService manages the entire recording pipeline
type RecordingService struct {
	config  *config.Config
	logger  recorderlog.Logger
	metrics *Metrics

	// Core components
	encoder       encoder.Encoder
	objectStore   storage.ObjectStore
	metadataStore storage.MetadataStore
	ringBuffer    *buffer.RingBuffer
	segmenter     *pipeline.Segmenter

	// Channels
	frameInput   chan *buffer.Frame
	motionEvents chan MotionEvent
	stopCh       chan struct{}

	// State management
	mu                sync.RWMutex
	running           atomic.Bool
	currentRecording  *storage.Recording
	eventRecording    *storage.Recording
	lastMotionTime    time.Time
	continuousEnabled bool
	eventEnabled      bool

	// Workers
	wg sync.WaitGroup
}

// Metrics tracks service performance
type Metrics struct {
	FramesReceived    atomic.Uint64
	FramesProcessed   atomic.Uint64
	FramesDropped     atomic.Uint64
	RecordingsStarted atomic.Uint64
	RecordingsEnded   atomic.Uint64
	SegmentsCreated   atomic.Uint64
	BytesWritten      atomic.Uint64
	Errors            atomic.Uint64
}

// NewRecordingService creates a new recording service
func NewRecordingService(cfg *config.Config, logger recorderlog.Logger) (*RecordingService, error) {
	// Create GStreamer AV1 encoder for maximum quality recording
	enc, err := encoder.NewGStreamerAV1Encoder(encoder.EncoderConfig{
		Width:            cfg.Video.Width,
		Height:           cfg.Video.Height,
		FrameRate:        float64(cfg.Video.FrameRate),
		Bitrate:          cfg.Video.BitRate * 1000, // Convert kbps to bps
		KeyframeInterval: 60,                        // 2 seconds at 30fps
		Codec:            "av1",
		RealTime:         false, // Recording mode for maximum quality
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create AV1 encoder: %w", err)
	}

	// Initialize storage using adapter helper
	minioCfg, pgCfg, err := config.CreateStorageConfigs(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage configs: %w", err)
	}

	objectStore, err := storage.NewMinIOStore(minioCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create object store: %w", err)
	}

	metadataStore, err := storage.NewPostgresStore(pgCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata store: %w", err)
	}

	// Ring buffer sized by time * fps
	bufferFrames := int(cfg.Recording.RingBufferSize.Seconds() * float64(cfg.Video.FrameRate))
	ringBuffer := buffer.NewRingBuffer(bufferFrames)

	// Segmenter
	segmenter := pipeline.NewSegmenter(cfg.Recording.SegmentDuration, cfg.Recording.TempDir)

	return &RecordingService{
		config:            cfg,
		logger:            logger,
		metrics:           &Metrics{},
		encoder:           enc,
		objectStore:       objectStore,
		metadataStore:     metadataStore,
		ringBuffer:        ringBuffer,
		segmenter:         segmenter,
		frameInput:        make(chan *buffer.Frame, int(cfg.Video.FrameRate)*3), // ~3s of frames (increased buffer)
		motionEvents:      make(chan MotionEvent, 16),
		stopCh:            make(chan struct{}),
		continuousEnabled: cfg.Recording.ContinuousEnabled,
		eventEnabled:      cfg.Recording.EventEnabled,
	}, nil
}

// Start begins the recording service
func (r *RecordingService) Start(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return fmt.Errorf("service already running")
	}

	// Check disk space before starting
	if err := r.checkDiskSpace(); err != nil {
		r.running.Store(false)
		return fmt.Errorf("insufficient disk space: %w", err)
	}

	r.logger.Info("Starting recording service",
		recorderlog.Bool("continuous", r.continuousEnabled),
		recorderlog.Bool("events", r.eventEnabled))

	// Start continuous recording if enabled
	if r.continuousEnabled {
		if err := r.startContinuousRecording(ctx); err != nil {
			r.running.Store(false)
			return fmt.Errorf("failed to start continuous recording: %w", err)
		}
	}

	// Workers
	r.wg.Add(3)
	go r.frameProcessor(ctx)
	go r.motionHandler(ctx)
	go r.metricsReporter(ctx)

	return nil
}

// Stop gracefully shuts down the recording service
func (r *RecordingService) Stop() error {
	if !r.running.CompareAndSwap(true, false) {
		return nil
	}

	r.logger.Info("Stopping recording service")
	close(r.stopCh)

	// Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		r.logger.Info("Recording service stopped gracefully")
	case <-time.After(30 * time.Second):
		r.logger.Warn("Recording service stop timeout")
	}

	// Finalize any active recordings
	r.mu.Lock()
	if r.currentRecording != nil {
		r.finalizeRecording(r.currentRecording)
	}
	if r.eventRecording != nil {
		r.finalizeRecording(r.eventRecording)
	}
	r.mu.Unlock()

	// Close components
	if err := r.encoder.Close(); err != nil {
		r.logger.Error("Failed to close encoder", recorderlog.Error(err))
	}

	return nil
}

// HandleFrame ingests a raw frame
func (r *RecordingService) HandleFrame(img image.Image, ts time.Time) error {
	if !r.running.Load() {
		return fmt.Errorf("service not running")
	}

	frame := &buffer.Frame{
		Image:     img,
		Timestamp: ts,
		PTS:       time.Since(r.getStartTime()),
	}

	select {
	case r.frameInput <- frame:
		r.metrics.FramesReceived.Add(1)
		return nil
	default:
		r.metrics.FramesDropped.Add(1)
		return fmt.Errorf("frame buffer full")
	}
}

// HandleMotionEvent queues a motion event
func (r *RecordingService) HandleMotionEvent(event MotionEvent) {
	if !r.running.Load() || !r.eventEnabled {
		return
	}

	select {
	case r.motionEvents <- event:
		r.logger.Debug("Motion event queued",
			recorderlog.Float64("confidence", event.Confidence),
			recorderlog.Time("timestamp", event.Timestamp))
	default:
		r.logger.Warn("Motion event buffer full")
	}
}

func (r *RecordingService) frameProcessor(ctx context.Context) {
	defer r.wg.Done()

	rotateTick := time.NewTicker(time.Second)
	defer rotateTick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return

		case frame := <-r.frameInput:
			r.processFrame(frame)

		case <-rotateTick.C:
			r.checkSegmentRotation()
		}
	}
}

func (r *RecordingService) processFrame(frame *buffer.Frame) {
	r.metrics.FramesProcessed.Add(1)

	// Validate frame before processing
	if frame == nil || frame.Image == nil {
		r.metrics.Errors.Add(1)
		return
	}

	// Validate encoder is initialized
	if r.encoder == nil {
		r.metrics.Errors.Add(1)
		return
	}

	// Always write to ring buffer (best effort)
	_ = r.ringBuffer.Write(frame)

	// Encode
	data, err := r.encoder.Encode(frame.Image, frame.PTS)
	if err != nil {
		r.logger.Error("Failed to encode frame", recorderlog.Error(err))
		r.metrics.Errors.Add(1)
		return
	}
	if data == nil {
		// encoder buffered internally
		return
	}

	// Write to all active recordings
	r.mu.RLock()
	var recs []*storage.Recording
	if r.currentRecording != nil {
		recs = append(recs, r.currentRecording)
	}
	if r.eventRecording != nil {
		recs = append(recs, r.eventRecording)
	}
	r.mu.RUnlock()

	for _, rec := range recs {
		if err := r.segmenter.WriteFrame(rec.ID, data, frame.Timestamp); err != nil {
			r.logger.Error("Failed to write frame to segment",
				recorderlog.String("recording_id", rec.ID),
				recorderlog.Error(err))
			r.metrics.Errors.Add(1)
		} else {
			r.metrics.BytesWritten.Add(uint64(len(data)))
		}
	}
}

func (r *RecordingService) motionHandler(ctx context.Context) {
	defer r.wg.Done()

	// Post-motion timer
	postMotion := time.NewTimer(time.Hour)
	if !postMotion.Stop() {
		<-postMotion.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return

		case ev := <-r.motionEvents:
			r.handleMotionEvent(ev, postMotion)

		case <-postMotion.C:
			r.endEventRecording()
		}
	}
}

func (r *RecordingService) handleMotionEvent(ev MotionEvent, timer *time.Timer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lastMotionTime = ev.Timestamp

	// Start event recording if not already active
	if r.eventRecording == nil {
		r.logger.Info("Starting motion-triggered recording",
			recorderlog.Float64("confidence", ev.Confidence))

		recID := uuid.New().String()
		rec := &storage.Recording{
			ID:               recID,
			Type:             "event",
			Status:           "recording",
			StartedAt:        ev.Timestamp,
			Bucket:           r.config.Storage.MinIO.Bucket,
			BaseKey:          fmt.Sprintf("event/%s/%s", ev.Timestamp.Format("2006-01-02"), recID),
			MotionConfidence: sql.NullFloat64{Float64: ev.Confidence, Valid: true},
		}

		// Include pre-motion frames (best effort)
		frames, err := r.ringBuffer.Dump()
		if err != nil {
			r.logger.Error("Failed to dump ring buffer", recorderlog.Error(err))
		} else if len(frames) > 0 {
			r.logger.Info("Including pre-motion buffer",
				recorderlog.Int("frames", len(frames)),
				recorderlog.Duration("config_prebuffer", r.config.Recording.PreMotionBuffer))
			go r.processBufferedFrames(rec.ID, frames) // NOTE: []*buffer.Frame
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.metadataStore.SaveRecording(ctx, rec); err != nil {
			r.logger.Error("Failed to save event recording metadata", recorderlog.Error(err))
			return
		}

		r.eventRecording = rec
		r.metrics.RecordingsStarted.Add(1)
	}

	// Reset the post-motion timer
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(r.config.Recording.PostMotionBuffer)
}

func (r *RecordingService) endEventRecording() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.eventRecording == nil {
		return
	}

	r.logger.Info("Ending motion-triggered recording",
		recorderlog.String("id", r.eventRecording.ID),
		recorderlog.Duration("duration", time.Since(r.eventRecording.StartedAt)))

	r.finalizeRecording(r.eventRecording)
	r.eventRecording = nil
	r.metrics.RecordingsEnded.Add(1)
}

func (r *RecordingService) startContinuousRecording(ctx context.Context) error {
	recID := uuid.New().String()
	rec := &storage.Recording{
		ID:        recID,
		Type:      "continuous",
		Status:    "recording",
		StartedAt: time.Now(),
		Bucket:    r.config.Storage.MinIO.Bucket,
		BaseKey:   fmt.Sprintf("continuous/%s/%s", time.Now().Format("2006-01-02"), recID),
	}
	if err := r.metadataStore.SaveRecording(ctx, rec); err != nil {
		return fmt.Errorf("failed to save recording metadata: %w", err)
	}
	r.currentRecording = rec
	r.metrics.RecordingsStarted.Add(1)

	r.logger.Info("Started continuous recording",
		recorderlog.String("id", rec.ID))
	return nil
}

// checkSegmentRotation rotates segments for any active recording
func (r *RecordingService) checkSegmentRotation() {
	r.mu.Lock()
	defer r.mu.Unlock()

	var recs []*storage.Recording
	if r.currentRecording != nil {
		recs = append(recs, r.currentRecording)
	}
	if r.eventRecording != nil {
		recs = append(recs, r.eventRecording)
	}

	for _, rec := range recs {
		seg, rotate := r.segmenter.ShouldRotate(rec.ID)
		if rotate && seg != nil {
			r.logger.Debug("Rotating segment",
				recorderlog.String("recording_id", rec.ID),
				recorderlog.String("segment_id", seg.ID))
			// Upload in background
			go r.uploadSegment(rec, seg)
			r.metrics.SegmentsCreated.Add(1)
			// Start next segment
			r.segmenter.NewSegment(rec.ID)
		}
	}
}

// uploadSegment uploads a finalized segment to object storage and records metadata
func (r *RecordingService) uploadSegment(rec *storage.Recording, seg *pipeline.Segment) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build key: {type}/{YYYY-MM-DD}/{recordingID}/segment_{index}.mkv
	key := fmt.Sprintf("%s/%s/%s/segment_%03d.mkv",
		rec.Type,
		rec.StartedAt.Format("2006-01-02"),
		rec.ID,
		seg.Index)

	// Upload file
	if err := r.objectStore.PutFile(ctx, key, seg.FilePath, storage.WithContentType("video/x-matroska")); err != nil {
		r.logger.Error("Failed to upload segment",
			recorderlog.String("segment_id", seg.ID),
			recorderlog.String("key", key),
			recorderlog.Error(err))
		r.metrics.Errors.Add(1)
		return
	}

	// Gather file size (robust if pipeline.Segment doesn't carry a size)
	var size int64
	if stat, err := os.Stat(seg.FilePath); err == nil {
		size = stat.Size()
	}

	// Persist segment metadata (convert pipeline.Segment -> storage.Segment)
	stSeg := &storage.Segment{
		ID:          seg.ID,
		RecordingID: seg.RecordingID,
		Index:       seg.Index,
		StartTime:   seg.StartTime,
		EndTime:     seg.EndTime,
		Duration:    seg.EndTime.Sub(seg.StartTime),
		StorageKey:  key,
		Size:        size,
		// Best effort if pipeline.Segment carries counts/checksum
		FrameCount: seg.FrameCount,
		Checksum:   seg.Checksum,
		Status:     storage.SegmentStatusCompleted,
		UploadedAt: sql.NullTime{Time: time.Now(), Valid: true},
	}

	// Save to DB
	if err := r.metadataStore.SaveSegment(ctx, stSeg); err != nil {
		r.logger.Error("Failed to save segment metadata",
			recorderlog.String("segment_id", seg.ID),
			recorderlog.Error(err))
	}

	r.logger.Info("Segment uploaded successfully",
		recorderlog.String("recording_id", rec.ID),
		recorderlog.String("segment_id", seg.ID),
		recorderlog.String("key", key))

	// Cleanup local file (best effort)
	if err := seg.Cleanup(); err != nil {
		r.logger.Warn("Failed to cleanup segment file",
			recorderlog.String("segment_id", seg.ID),
			recorderlog.Error(err))
	}
}

// processBufferedFrames writes pre-motion buffered frames
func (r *RecordingService) processBufferedFrames(recordingID string, frames []*buffer.Frame) {
	for _, f := range frames {
		data, err := r.encoder.Encode(f.Image, f.PTS)
		if err != nil {
			r.logger.Error("Failed to encode buffered frame", recorderlog.Error(err))
			continue
		}
		if data != nil {
			if err := r.segmenter.WriteFrame(recordingID, data, f.Timestamp); err != nil {
				r.logger.Error("Failed to write buffered frame", recorderlog.Error(err))
			}
		}
	}
}

// finalizeRecording completes the active segment and updates DB
func (r *RecordingService) finalizeRecording(rec *storage.Recording) {
	end := time.Now()

	// storage.Recording uses sql.NullTime; set safely
	rec.EndedAt = sql.NullTime{Time: end, Valid: true}
	rec.Status = "completed"
	rec.Duration = end.Sub(rec.StartedAt).Seconds()

	// Finalize current segment and upload
	if seg := r.segmenter.Finalize(rec.ID); seg != nil {
		r.uploadSegment(rec, seg)
	}

	// Update DB
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.metadataStore.UpdateRecording(ctx, rec.ID, map[string]interface{}{
		"status":           rec.Status,
		"ended_at":         end,
		"duration_seconds": rec.Duration,
	}); err != nil {
		r.logger.Error("Failed to update recording metadata",
			recorderlog.String("id", rec.ID),
			recorderlog.Error(err))
	}
}

// metricsReporter periodically logs metrics
func (r *RecordingService) metricsReporter(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.reportMetrics()
		}
	}
}

// reportMetrics logs current service metrics
func (r *RecordingService) reportMetrics() {
	// Validate encoder is initialized before accessing metrics
	if r.encoder == nil {
		r.logger.Info("Recording service metrics",
			recorderlog.Uint64("frames_received", r.metrics.FramesReceived.Load()),
			recorderlog.Uint64("frames_processed", r.metrics.FramesProcessed.Load()),
			recorderlog.Uint64("frames_dropped", r.metrics.FramesDropped.Load()),
			recorderlog.Uint64("recordings_started", r.metrics.RecordingsStarted.Load()),
			recorderlog.Uint64("recordings_ended", r.metrics.RecordingsEnded.Load()),
			recorderlog.Uint64("segments_created", r.metrics.SegmentsCreated.Load()),
			recorderlog.Uint64("bytes_written", r.metrics.BytesWritten.Load()),
			recorderlog.Uint64("errors", r.metrics.Errors.Load()),
		)
		return
	}

	em := r.encoder.GetMetrics()
	r.logger.Info("Recording service metrics",
		recorderlog.Uint64("frames_received", r.metrics.FramesReceived.Load()),
		recorderlog.Uint64("frames_processed", r.metrics.FramesProcessed.Load()),
		recorderlog.Uint64("frames_dropped", r.metrics.FramesDropped.Load()),
		recorderlog.Uint64("recordings_started", r.metrics.RecordingsStarted.Load()),
		recorderlog.Uint64("recordings_ended", r.metrics.RecordingsEnded.Load()),
		recorderlog.Uint64("segments_created", r.metrics.SegmentsCreated.Load()),
		recorderlog.Uint64("bytes_written", r.metrics.BytesWritten.Load()),
		recorderlog.Uint64("errors", r.metrics.Errors.Load()),
		recorderlog.Float64("encoder_bitrate", em.CurrentBitrate),
		recorderlog.Uint64("encoder_frames", em.FramesEncoded),
		recorderlog.Uint64("encoder_keyframes", em.KeyFrames),
	)
}

// getStartTime picks a reference time for PTS
func (r *RecordingService) getStartTime() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.currentRecording != nil {
		return r.currentRecording.StartedAt
	}
	if r.eventRecording != nil {
		return r.eventRecording.StartedAt
	}
	return time.Now()
}

// GetRecording exposes metadata
func (r *RecordingService) GetRecording(ctx context.Context, id string) (*storage.Recording, error) {
	return r.metadataStore.GetRecording(ctx, id)
}

// ListRecordings queries metadata
func (r *RecordingService) ListRecordings(ctx context.Context, q storage.RecordingQuery) ([]*storage.Recording, error) {
	return r.metadataStore.QueryRecordings(ctx, q)
}

// GenerateStreamURL creates a pre-signed URL for a segment
func (r *RecordingService) GenerateStreamURL(ctx context.Context, recordingID string, segmentIndex int) (string, error) {
	// ensure recording exists
	if _, err := r.metadataStore.GetRecording(ctx, recordingID); err != nil {
		return "", fmt.Errorf("recording not found: %w", err)
	}

	seg, err := r.metadataStore.GetSegment(ctx, recordingID, segmentIndex)
	if err != nil {
		return "", fmt.Errorf("segment not found: %w", err)
	}

	url, err := r.objectStore.GeneratePresignedURL(ctx, seg.StorageKey, time.Hour)
	if err != nil {
		return "", fmt.Errorf("failed to generate URL: %w", err)
	}
	return url, nil
}

// UpdateConfig updates the recording service configuration at runtime
// Only safe parameters (continuous/event enabled flags) are updated.
// Other parameters like segment duration, ring buffer size require restart.
func (r *RecordingService) UpdateConfig(newConfig *config.RecordingConfig) {
	if newConfig == nil {
		r.logger.Warn("Ignoring nil recording config update")
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Update safe runtime parameters
	oldContinuous := r.continuousEnabled
	oldEvent := r.eventEnabled

	r.continuousEnabled = newConfig.ContinuousEnabled
	r.eventEnabled = newConfig.EventEnabled

	r.logger.Info("Recording configuration updated",
		recorderlog.Bool("continuous_enabled", r.continuousEnabled),
		recorderlog.Bool("event_enabled", r.eventEnabled),
		recorderlog.Bool("continuous_changed", oldContinuous != r.continuousEnabled),
		recorderlog.Bool("event_changed", oldEvent != r.eventEnabled))

	// Note: Changes to segment duration, ring buffer size, temp dir, etc.
	// require application restart to take effect
}

// checkDiskSpace verifies sufficient disk space is available
func (r *RecordingService) checkDiskSpace() error {
	// Check temp directory space
	var stat syscall.Statfs_t
	if err := syscall.Statfs(r.config.Recording.TempDir, &stat); err != nil {
		return fmt.Errorf("failed to stat temp dir: %w", err)
	}

	// Calculate available space in MB
	availableMB := (stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024)

	// Require at least 1GB free space
	minRequiredMB := uint64(1024)
	if availableMB < minRequiredMB {
		return fmt.Errorf("insufficient disk space: %d MB available, %d MB required",
			availableMB, minRequiredMB)
	}

	r.logger.Info("Disk space check passed",
		recorderlog.Uint64("available_mb", availableMB),
		recorderlog.Uint64("required_mb", minRequiredMB))

	return nil
}
