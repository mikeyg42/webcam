// internal/recorder/recorder.go
package recorder

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/framestream"
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

// HealthStatus represents recording health state
type HealthStatus string

const (
	HealthStatusHealthy  HealthStatus = "healthy"
	HealthStatusWarning  HealthStatus = "warning"
	HealthStatusCritical HealthStatus = "critical"
	HealthStatusFailed   HealthStatus = "failed"
)

// DiskStatus represents disk space state
type DiskStatus int

const (
	DiskStatusHealthy   DiskStatus = iota // >2GB available
	DiskStatusWarning                     // <2GB available
	DiskStatusCritical                    // <500MB available
	DiskStatusEmergency                   // <100MB available
)

// RecordingHealth tracks per-recording health
type RecordingHealth struct {
	RecordingID       string
	LastFrameWrite    atomic.Value  // time.Time
	LastFrameCount    atomic.Uint64
	ConsecutiveStalls atomic.Uint32
	Status            atomic.Value  // HealthStatus
	WarningIssued     atomic.Bool
	CriticalIssued    atomic.Bool
}

// WatchdogConfig holds thresholds
type WatchdogConfig struct {
	CheckInterval      time.Duration // 1s
	WarningThreshold   time.Duration // 5s
	RestartThreshold   time.Duration // 10s
	FailureThreshold   time.Duration // 30s
	MaxRestartAttempts int           // 3
}

// DefaultWatchdogConfig returns sensible defaults
func DefaultWatchdogConfig() WatchdogConfig {
	return WatchdogConfig{
		CheckInterval:      1 * time.Second,
		WarningThreshold:   5 * time.Second,
		RestartThreshold:   10 * time.Second,
		FailureThreshold:   30 * time.Second,
		MaxRestartAttempts: 3,
	}
}

// RecordingService manages the entire recording pipeline.
//
// The encoder is initialized lazily on the first frame received, allowing the service
// to adapt to the actual camera resolution rather than failing if the configured
// resolution doesn't match what the camera produces. This design choice prioritizes
// runtime flexibility over strict configuration enforcement.
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

	// Lazy encoder initialization: the encoder is created on the first frame
	// using the actual frame dimensions rather than config values. This allows
	// graceful handling of resolution mismatches between config and camera hardware.
	// The encoder is also recreated if the frame resolution changes (e.g., during calibration).
	encoderConfig      encoder.EncoderConfig // Template config (dimensions may be overridden)
	encoderMu          sync.Mutex            // Guards encoder creation
	encoderInitialized atomic.Bool           // Fast path check to avoid mutex contention
	encoderWidth       int                   // Current encoder width (for change detection)
	encoderHeight      int                   // Current encoder height (for change detection)

	// Channels
	frameInput   chan *buffer.Frame
	audioInput   chan *framestream.AudioFrame // Audio frames for recording
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
	audioEnabled      bool              // Whether audio recording is enabled
	bufferWarnLogged  atomic.Bool       // Rate-limit buffer full warnings

	// Segment rotation state: tracks recordings waiting for a keyframe to complete rotation.
	// When a rotation is needed, we request a keyframe from the encoder and set this flag.
	// The actual rotation happens when a keyframe arrives, ensuring the new segment starts
	// with a decodable frame. Key: recordingID, Value: segment to upload after rotation.
	pendingRotation     map[string]*pipeline.Segment
	pendingRotationTime map[string]time.Time // When rotation was requested (for timeout)

	// Watchdog state
	watchdogConfig  WatchdogConfig
	recordingHealth map[string]*RecordingHealth
	healthMu        sync.RWMutex
	restartAttempts map[string]int

	// Upload backpressure state
	consecutiveUploadFailures atomic.Uint32
	maxUploadFailures         uint32 // Threshold before pausing (default 5)
	uploadBackpressureActive  atomic.Bool

	// Emergency buffer: fallback when primary frameInput channel is full.
	// Prevents frame loss during encoder slowdowns or processing spikes.
	emergencyBuffer     *buffer.RingBuffer
	emergencyBufferSize int           // Configured size (frames)
	emergencyDrainStop  chan struct{} // Signal to stop drain goroutine

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

	// Segment rotation metrics - helps detect encoder keyframe issues
	KeyframeRotations atomic.Uint64 // Rotations triggered by keyframe arrival (ideal)
	ForcedRotations   atomic.Uint64 // Rotations forced by timeout (degraded, may cause playback issues)

	// Watchdog metrics
	WatchdogChecks    atomic.Uint64 // Total watchdog checks performed
	StallsDetected    atomic.Uint64 // Number of frame stalls detected
	EncoderRestarts   atomic.Uint64 // Number of encoder restart attempts
	RecordingFailures atomic.Uint64 // Number of recordings marked as failed

	// Emergency buffer metrics
	EmergencyBufferWrites atomic.Uint64 // Frames written to emergency buffer
	EmergencyBufferDrains atomic.Uint64 // Frames drained from emergency buffer
	EmergencyBufferDrops  atomic.Uint64 // Frames dropped when emergency buffer also full
}

// NewRecordingService creates a new recording service.
//
// The encoder is NOT created here - it's lazily initialized on the first frame
// using ensureEncoder(). This allows the service to adapt to the actual camera
// resolution if it differs from the configured values.
func NewRecordingService(cfg *config.Config, logger recorderlog.Logger) (*RecordingService, error) {
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
	if err := segmenter.Initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize segmenter: %w", err)
	}
	// Set video config for MKV muxing
	segmenter.SetVideoConfig(cfg.Video.Width, cfg.Video.Height, float64(cfg.Video.FrameRate))

	// Prepare encoder config template. The Width/Height values here are from config
	// but may be overridden during lazy initialization if the actual camera produces
	// frames at a different resolution.
	encConfig := encoder.EncoderConfig{
		Width:            cfg.Video.Width,
		Height:           cfg.Video.Height,
		FrameRate:        float64(cfg.Video.FrameRate),
		Bitrate:          cfg.Video.BitRate, // Already in bps from config
		KeyframeInterval: 30,                 // ~2 seconds at 15fps, ensures fast segment rotation
		Codec:            "av1",
		RealTime:         false, // Recording mode for maximum quality
	}

	// Configure audio for segmenter if enabled
	audioEnabled := cfg.Audio.Enabled
	if audioEnabled {
		segmenter.SetAudioConfig(true, 48000, 2) // 48kHz stereo for Opus
	}

	// Emergency buffer: 20 seconds of frames as fallback when primary buffer is full.
	// This provides resilience during encoder slowdowns without losing frames.
	emergencyBufferFrames := int(cfg.Video.FrameRate) * 20 // 20 seconds
	emergencyBuffer := buffer.NewRingBuffer(emergencyBufferFrames)

	logger.Info("Recording service initialized (encoder will be created on first frame)",
		recorderlog.Int("configured_width", cfg.Video.Width),
		recorderlog.Int("configured_height", cfg.Video.Height),
		recorderlog.Int("configured_fps", cfg.Video.FrameRate),
		recorderlog.Bool("audio_enabled", audioEnabled),
		recorderlog.Int("emergency_buffer_frames", emergencyBufferFrames))

	return &RecordingService{
		config:            cfg,
		logger:            logger,
		metrics:           &Metrics{},
		encoderConfig:     encConfig,
		// encoder is nil - will be lazily initialized in ensureEncoder()
		objectStore:       objectStore,
		metadataStore:     metadataStore,
		ringBuffer:        ringBuffer,
		segmenter:         segmenter,
		frameInput:        make(chan *buffer.Frame, int(cfg.Video.FrameRate)*10), // ~10s of frames (handles encoder init delay)
		audioInput:        make(chan *framestream.AudioFrame, 100),               // ~2s of audio frames
		motionEvents:      make(chan MotionEvent, 16),
		stopCh:            make(chan struct{}),
		continuousEnabled:   cfg.Recording.ContinuousEnabled,
		eventEnabled:        cfg.Recording.EventEnabled,
		audioEnabled:        audioEnabled,
		pendingRotation:     make(map[string]*pipeline.Segment),
		pendingRotationTime: make(map[string]time.Time),
		watchdogConfig:      DefaultWatchdogConfig(),
		recordingHealth:     make(map[string]*RecordingHealth),
		restartAttempts:     make(map[string]int),
		maxUploadFailures:   5, // Pause recording after 5 consecutive upload failures
		emergencyBuffer:     emergencyBuffer,
		emergencyBufferSize: emergencyBufferFrames,
		emergencyDrainStop:  make(chan struct{}),
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
	r.wg.Add(6)
	go r.frameProcessor(ctx)
	go r.motionHandler(ctx)
	go r.metricsReporter(ctx)
	go r.watchdogMonitor(ctx)
	go r.emergencyBufferDrain(ctx)
	go r.retentionCleanup(ctx)

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
	// Clear pending rotation state before finalizing
	for k := range r.pendingRotation {
		delete(r.pendingRotation, k)
		delete(r.pendingRotationTime, k)
	}
	cur := r.currentRecording
	evt := r.eventRecording
	r.currentRecording = nil
	r.eventRecording = nil
	r.mu.Unlock()

	if cur != nil {
		r.finalizeRecording(cur)
	}
	if evt != nil {
		r.finalizeRecording(evt)
	}

	// Flush and close encoder if it was initialized (may be nil if no frames were ever received)
	r.encoderMu.Lock()
	if r.encoder != nil {
		// Flush any remaining encoded frames
		if flushedFrames, err := r.encoder.Flush(); err != nil {
			r.logger.Error("Failed to flush encoder", recorderlog.Error(err))
		} else if len(flushedFrames) > 0 {
			r.logger.Info("Flushed remaining frames from encoder",
				recorderlog.Int("frame_count", len(flushedFrames)))
			r.writeFlushedFrames(flushedFrames)
		}

		if err := r.encoder.Close(); err != nil {
			r.logger.Error("Failed to close encoder", recorderlog.Error(err))
		}
	}
	r.encoderMu.Unlock()

	return nil
}

// HandleFrame ingests a raw frame.
// Uses a two-tier buffer strategy: primary channel for normal operation,
// emergency ring buffer as fallback when primary is full.
func (r *RecordingService) HandleFrame(img image.Image, ts time.Time) error {
	if !r.running.Load() {
		return fmt.Errorf("service not running")
	}

	frame := &buffer.Frame{
		Image:     img,
		Timestamp: ts,
		PTS:       time.Since(r.getStartTime()),
	}

	// Try primary channel first (fast path)
	select {
	case r.frameInput <- frame:
		r.metrics.FramesReceived.Add(1)
		// Note: Don't clear bufferWarnLogged here. The flag is only cleared when
		// the emergency buffer drains to empty (in drainEmergencyBuffer), which
		// properly indicates recovery from an overload episode. Clearing on every
		// successful write would cause log spam during oscillating conditions.
		return nil
	default:
		// Primary buffer full - use emergency buffer as fallback
	}

	// Emergency buffer fallback: prevents frame loss during encoder slowdowns.
	// Reject at 90% capacity to prevent unbounded memory under sustained overload
	// and to leave headroom for the drain goroutine to make progress.
	currentSize := r.emergencyBuffer.Size()
	threshold := (r.emergencyBufferSize * 90) / 100
	if currentSize >= threshold {
		r.metrics.EmergencyBufferDrops.Add(1)
		r.metrics.FramesDropped.Add(1)
		if !r.bufferWarnLogged.Load() {
			r.logger.Warn("Emergency buffer at capacity threshold - dropping frames",
				recorderlog.Int("current_size", currentSize),
				recorderlog.Int("threshold", threshold),
				recorderlog.Int("capacity", r.emergencyBufferSize))
		}
		return nil
	}

	if err := r.emergencyBuffer.Write(frame); err != nil {
		// Emergency buffer write failed (shouldn't happen with ring buffer)
		r.metrics.EmergencyBufferDrops.Add(1)
		r.metrics.FramesDropped.Add(1)
		return nil
	}

	r.metrics.EmergencyBufferWrites.Add(1)
	r.metrics.FramesReceived.Add(1)

	// Log warning once per overflow episode
	if !r.bufferWarnLogged.Swap(true) {
		r.logger.Warn("Primary buffer full - using emergency buffer",
			recorderlog.Int("emergency_buffer_size", r.emergencyBuffer.Size()),
			recorderlog.Int("emergency_buffer_capacity", r.emergencyBufferSize))
	}

	return nil
}

// HandleAudioFrame ingests an audio frame for recording
func (r *RecordingService) HandleAudioFrame(frame *framestream.AudioFrame) error {
	if !r.running.Load() {
		return fmt.Errorf("service not running")
	}

	if !r.audioEnabled {
		return nil // Audio disabled, silently ignore
	}

	select {
	case r.audioInput <- frame:
		return nil
	default:
		// Audio buffer full, drop frame (audio drops are less critical than video)
		return nil
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

// getEncoder safely returns the current encoder with mutex protection.
// This prevents races during encoder restart when the watchdog may be closing
// the encoder while processFrame is trying to use it.
func (r *RecordingService) getEncoder() encoder.Encoder {
	r.encoderMu.Lock()
	enc := r.encoder
	r.encoderMu.Unlock()
	return enc
}

// ensureEncoder lazily initializes the encoder on the first frame, and recreates it if
// the frame resolution changes (e.g., during calibration when camera switches resolutions).
//
// This method solves a common problem: the configured video resolution may not match
// what the camera actually produces. Rather than failing at startup or dropping frames,
// we create the encoder using the actual frame dimensions from the first frame received.
//
// Thread-safety: Uses double-checked locking pattern with atomic.Bool for the fast path
// and mutex for the slow path (encoder creation). This ensures:
//   - Most calls (after initialization) only check an atomic bool
//   - Only one goroutine creates the encoder
//   - All goroutines see the encoder once created
//
// Returns error only on encoder creation failure; returns nil if encoder already exists.
func (r *RecordingService) ensureEncoder(frame *buffer.Frame) error {
	// Extract actual dimensions from the frame
	bounds := frame.Image.Bounds()
	actualWidth := bounds.Dx()
	actualHeight := bounds.Dy()

	// Fast path: encoder already initialized AND dimensions match
	if r.encoderInitialized.Load() {
		// Check if resolution changed (e.g., calibration switched camera resolution)
		if r.encoderWidth == actualWidth && r.encoderHeight == actualHeight {
			return nil
		}
		// Resolution changed - need to reinitialize encoder
		r.logger.Warn("Frame resolution changed - reinitializing encoder",
			recorderlog.Int("old_width", r.encoderWidth),
			recorderlog.Int("old_height", r.encoderHeight),
			recorderlog.Int("new_width", actualWidth),
			recorderlog.Int("new_height", actualHeight))
	}

	// Slow path: need to initialize or reinitialize encoder
	r.encoderMu.Lock()
	defer r.encoderMu.Unlock()

	// Double-check after acquiring lock (another goroutine may have initialized)
	if r.encoderInitialized.Load() && r.encoderWidth == actualWidth && r.encoderHeight == actualHeight {
		return nil
	}

	// Close old encoder if it exists (resolution change case)
	if r.encoder != nil {
		r.logger.Info("Closing old encoder for resolution change")
		if err := r.encoder.Close(); err != nil {
			r.logger.Warn("Error closing old encoder", recorderlog.Error(err))
		}
		r.encoder = nil
		r.encoderInitialized.Store(false)
	}

	// Check if dimensions differ from config and log appropriately
	configWidth := r.encoderConfig.Width
	configHeight := r.encoderConfig.Height

	if actualWidth != configWidth || actualHeight != configHeight {
		r.logger.Warn("Camera resolution differs from config - using actual camera resolution",
			recorderlog.Int("config_width", configWidth),
			recorderlog.Int("config_height", configHeight),
			recorderlog.Int("actual_width", actualWidth),
			recorderlog.Int("actual_height", actualHeight))
	}

	// Create encoder config with actual dimensions
	actualConfig := r.encoderConfig
	actualConfig.Width = actualWidth
	actualConfig.Height = actualHeight

	r.logger.Info("Creating AV1 encoder with actual frame dimensions",
		recorderlog.Int("width", actualWidth),
		recorderlog.Int("height", actualHeight),
		recorderlog.Float64("framerate", actualConfig.FrameRate),
		recorderlog.Int("bitrate_bps", actualConfig.Bitrate))

	enc, err := encoder.NewGStreamerAV1Encoder(actualConfig)
	if err != nil {
		return fmt.Errorf("failed to create AV1 encoder: %w", err)
	}

	r.encoder = enc
	r.encoderWidth = actualWidth
	r.encoderHeight = actualHeight
	r.encoderInitialized.Store(true)

	// Update segmenter with actual video dimensions for MKV muxing
	r.segmenter.SetVideoConfig(actualWidth, actualHeight, actualConfig.FrameRate)

	r.logger.Info("Encoder initialized successfully",
		recorderlog.Int("width", actualWidth),
		recorderlog.Int("height", actualHeight))

	return nil
}

func (r *RecordingService) frameProcessor(ctx context.Context) {
	defer r.wg.Done()
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("PANIC in frame processor - attempting emergency save",
				recorderlog.Any("panic", p),
				recorderlog.String("stack", string(debug.Stack())))
			r.metrics.Errors.Add(1)
			r.emergencyFinalize()
		}
	}()

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

		case audioFrame := <-r.audioInput:
			r.processAudioFrame(audioFrame)

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

	// Lazily initialize encoder on first frame using actual frame dimensions.
	// This handles the case where config resolution doesn't match camera hardware.
	if err := r.ensureEncoder(frame); err != nil {
		r.logger.Error("Failed to initialize encoder", recorderlog.Error(err))
		r.metrics.Errors.Add(1)
		return
	}

	// Always write to ring buffer (best effort)
	_ = r.ringBuffer.Write(frame)

	// Get encoder safely (may be nil during restart)
	enc := r.getEncoder()
	if enc == nil {
		r.logger.Warn("Encoder unavailable during restart, frame buffered in ring buffer")
		return
	}

	// Encode
	data, err := enc.Encode(frame.Image, frame.PTS)
	if err != nil {
		r.logger.Error("Failed to encode frame", recorderlog.Error(err))
		r.metrics.Errors.Add(1)
		return
	}
	if data == nil {
		// encoder buffered internally
		return
	}

	// Check if this encoded frame is a keyframe (needed for segment rotation)
	isKeyframe := pipeline.IsAV1Keyframe(data)

	if isKeyframe {
		r.logger.Debug("Keyframe detected in encoded frame",
			recorderlog.Int("size", len(data)))
	}

	// Get active recordings and pending rotations while holding lock briefly
	r.mu.Lock()
	var recs []*storage.Recording
	if r.currentRecording != nil {
		recs = append(recs, r.currentRecording)
	}
	if r.eventRecording != nil {
		recs = append(recs, r.eventRecording)
	}

	// Collect pending rotations to process (if this is a keyframe)
	// We gather the data under lock but perform the actual rotation outside the lock
	var toRotate []struct {
		rec    *storage.Recording
		oldSeg *pipeline.Segment
	}
	if isKeyframe {
		for _, rec := range recs {
			if oldSeg, pending := r.pendingRotation[rec.ID]; pending {
				toRotate = append(toRotate, struct {
					rec    *storage.Recording
					oldSeg *pipeline.Segment
				}{rec, oldSeg})
				// Clear pending state while we have the lock
				delete(r.pendingRotation, rec.ID)
				delete(r.pendingRotationTime, rec.ID)
			}
		}
	}
	r.mu.Unlock()

	// Process pending rotations OUTSIDE the lock
	// The segmenter has its own mutex for thread safety
	for _, item := range toRotate {
		r.logger.Info("Keyframe received, completing segment rotation",
			recorderlog.String("recording_id", item.rec.ID),
			recorderlog.String("old_segment_id", item.oldSeg.ID))

		// Create new segment (this finalizes the old segment)
		if _, err := r.segmenter.NewSegment(item.rec.ID); err != nil {
			r.logger.Error("Failed to create new segment during rotation",
				recorderlog.String("recording_id", item.rec.ID),
				recorderlog.Error(err))
			r.metrics.Errors.Add(1)
		} else {
			r.metrics.SegmentsCreated.Add(1)
			r.metrics.KeyframeRotations.Add(1)
			// Upload old segment in background
			go r.uploadSegment(item.rec, item.oldSeg)
		}
	}

	// Write frame to all active recordings
	// If we just rotated, this keyframe becomes the first frame of the new segment
	for _, rec := range recs {
		if err := r.segmenter.WriteFrame(rec.ID, data, frame.Timestamp); err != nil {
			r.logger.Error("Failed to write frame to segment",
				recorderlog.String("recording_id", rec.ID),
				recorderlog.Error(err))
			r.metrics.Errors.Add(1)
		} else {
			r.metrics.BytesWritten.Add(uint64(len(data)))
			r.updateHeartbeat(rec.ID)
		}
	}
}

// processAudioFrame writes an audio frame to all active recordings
func (r *RecordingService) processAudioFrame(frame *framestream.AudioFrame) {
	if frame == nil || len(frame.Data) == 0 {
		return
	}

	// Get active recordings
	r.mu.RLock()
	var recs []*storage.Recording
	if r.currentRecording != nil {
		recs = append(recs, r.currentRecording)
	}
	if r.eventRecording != nil {
		recs = append(recs, r.eventRecording)
	}
	r.mu.RUnlock()

	// Write audio to all active recordings
	for _, rec := range recs {
		if err := r.segmenter.WriteAudioFrame(rec.ID, frame.Data, frame.Timestamp); err != nil {
			// Audio write failures are not fatal - video is more important
			// Only log at debug level to avoid spam
			r.logger.Debug("Failed to write audio frame",
				recorderlog.String("recording_id", rec.ID),
				recorderlog.Error(err))
		}
	}
}

func (r *RecordingService) motionHandler(ctx context.Context) {
	defer r.wg.Done()
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("PANIC in motion handler - attempting emergency save",
				recorderlog.Any("panic", p),
				recorderlog.String("stack", string(debug.Stack())))
			r.metrics.Errors.Add(1)
			r.emergencyFinalize()
		}
	}()

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

// checkSegmentRotation checks if segments should be rotated for any active recording.
// Instead of immediately rotating (which caused blank segments due to timing issues),
// this method now sets a pending rotation flag and requests a keyframe from the encoder.
// The actual rotation happens in processFrame when a keyframe arrives, ensuring the
// new segment starts with a decodable frame.
//
// A timeout mechanism ensures rotation completes even if keyframe generation fails.
// At 5 seconds, we re-request a keyframe (encoder may have missed first request).
// After 15 seconds, the rotation is forced to prevent segment growth indefinitely.
func (r *RecordingService) checkSegmentRotation() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Natural keyframes arrive every KeyframeInterval frames (~2s at 15fps with interval=30).
	// We trust natural keyframe arrival rather than relying solely on ForceKeyframe events,
	// which SVT-AV1 may not honor reliably. The force timeout is a safety net only.
	const forceTimeout = 6 * time.Second // Slightly over one full GOP period

	var recs []*storage.Recording
	if r.currentRecording != nil {
		recs = append(recs, r.currentRecording)
	}
	if r.eventRecording != nil {
		recs = append(recs, r.eventRecording)
	}

	enc := r.getEncoder()

	for _, rec := range recs {
		// Check if there's a pending rotation
		if oldSeg, pending := r.pendingRotation[rec.ID]; pending {
			requestTime := r.pendingRotationTime[rec.ID]
			elapsed := time.Since(requestTime)

			if elapsed > forceTimeout {
				// Timeout exceeded - force rotation without keyframe
				r.logger.Warn("Pending rotation timed out, forcing rotation without keyframe",
					recorderlog.String("recording_id", rec.ID),
					recorderlog.String("segment_id", oldSeg.ID),
					recorderlog.Duration("wait_time", elapsed),
					recorderlog.Int("keyframe_interval", r.encoderConfig.KeyframeInterval))

				if _, err := r.segmenter.NewSegment(rec.ID); err != nil {
					r.logger.Error("Failed to create new segment during forced rotation",
						recorderlog.String("recording_id", rec.ID),
						recorderlog.Error(err))
					r.metrics.Errors.Add(1)
				} else {
					r.metrics.SegmentsCreated.Add(1)
					r.metrics.ForcedRotations.Add(1)
					// Upload old segment in background
					go r.uploadSegment(rec, oldSeg)
				}

				// Clear pending state
				delete(r.pendingRotation, rec.ID)
				delete(r.pendingRotationTime, rec.ID)
			}
			continue
		}

		seg, shouldRotate := r.segmenter.ShouldRotate(rec.ID)
		if shouldRotate && seg != nil {
			r.logger.Debug("Segment rotation needed, requesting keyframe",
				recorderlog.String("recording_id", rec.ID),
				recorderlog.String("segment_id", seg.ID),
				recorderlog.Int64("segment_frames", seg.FrameCount),
				recorderlog.Duration("segment_duration", time.Since(seg.StartTime)))

			// Mark this recording as pending rotation, storing the segment to upload later
			r.pendingRotation[rec.ID] = seg
			r.pendingRotationTime[rec.ID] = time.Now()

			// Request keyframe from encoder - rotation completes when keyframe arrives
			if enc != nil {
				enc.ForceKeyframe()
			}
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
	if err := r.objectStore.PutFile(ctx, key, seg.FilePath, storage.WithContentType("video/webm")); err != nil {
		r.logger.Error("Failed to upload segment",
			recorderlog.String("segment_id", seg.ID),
			recorderlog.String("key", key),
			recorderlog.Error(err))
		r.metrics.Errors.Add(1)

		// Track consecutive upload failures for backpressure
		failures := r.consecutiveUploadFailures.Add(1)
		if failures >= r.maxUploadFailures {
			r.handleUploadBackpressure()
		}
		return
	}

	// Upload succeeded - reset failure counter and clear backpressure
	r.consecutiveUploadFailures.Store(0)
	if r.uploadBackpressureActive.Load() {
		r.uploadBackpressureActive.Store(false)
		r.logger.Info("Upload backpressure cleared - storage connectivity restored")
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

// handleUploadBackpressure pauses recording when storage is unreachable
func (r *RecordingService) handleUploadBackpressure() {
	if r.uploadBackpressureActive.Swap(true) {
		return // Already active
	}

	r.logger.Error("Upload backpressure activated - too many consecutive upload failures",
		recorderlog.Int("consecutive_failures", int(r.consecutiveUploadFailures.Load())),
		recorderlog.Int("threshold", int(r.maxUploadFailures)))

	// Finalize current recordings to prevent data loss
	r.mu.Lock()
	if r.currentRecording != nil {
		r.logger.Warn("Finalizing continuous recording due to storage backpressure",
			recorderlog.String("recording_id", r.currentRecording.ID))
		r.finalizeRecording(r.currentRecording)
		r.currentRecording = nil
		r.metrics.RecordingsEnded.Add(1)
	}
	if r.eventRecording != nil {
		r.logger.Warn("Finalizing event recording due to storage backpressure",
			recorderlog.String("recording_id", r.eventRecording.ID))
		r.finalizeRecording(r.eventRecording)
		r.eventRecording = nil
		r.metrics.RecordingsEnded.Add(1)
	}

	// Disable new recordings until storage is restored
	r.continuousEnabled = false
	r.eventEnabled = false
	r.mu.Unlock()

	r.logger.Error("Recording paused - will automatically retry storage connectivity")

	// Start automatic recovery with exponential backoff
	go r.retryStorageConnectivity()
}

// retryStorageConnectivity attempts to restore storage connectivity with exponential backoff
func (r *RecordingService) retryStorageConnectivity() {
	initialDelay := 30 * time.Second
	maxDelay := 5 * time.Minute
	currentDelay := initialDelay

	for r.uploadBackpressureActive.Load() && r.running.Load() {
		time.Sleep(currentDelay)

		// Check if we're still running and backpressure is still active
		if !r.running.Load() || !r.uploadBackpressureActive.Load() {
			return
		}

		// Test storage connectivity
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := r.objectStore.HealthCheck(ctx)
		cancel()

		if err != nil {
			r.logger.Warn("Storage connectivity check failed, will retry",
				recorderlog.Error(err),
				recorderlog.Duration("next_retry", currentDelay*2))

			// Exponential backoff with cap
			currentDelay *= 2
			if currentDelay > maxDelay {
				currentDelay = maxDelay
			}
			continue
		}

		// Storage is reachable - restore recording
		r.logger.Info("Storage connectivity restored")

		r.mu.Lock()
		r.consecutiveUploadFailures.Store(0)
		r.uploadBackpressureActive.Store(false)

		// Restore original config settings
		r.continuousEnabled = r.config.Recording.ContinuousEnabled
		r.eventEnabled = r.config.Recording.EventEnabled

		// Restart continuous recording if it was enabled
		if r.continuousEnabled && r.currentRecording == nil {
			r.mu.Unlock()
			if err := r.startContinuousRecording(context.Background()); err != nil {
				r.logger.Error("Failed to restart continuous recording after backpressure recovery",
					recorderlog.Error(err))
			} else {
				r.logger.Info("Continuous recording restarted after backpressure recovery")
			}
		} else {
			r.mu.Unlock()
		}

		r.logger.Info("Recording service fully restored after storage backpressure")
		return
	}
}

// processBufferedFrames writes pre-motion buffered frames.
// These are frames from the ring buffer captured before motion was detected,
// allowing recordings to include context leading up to the motion event.
func (r *RecordingService) processBufferedFrames(recordingID string, frames []*buffer.Frame) {
	if len(frames) == 0 {
		return
	}

	// Ensure encoder is initialized using the first buffered frame's dimensions
	if err := r.ensureEncoder(frames[0]); err != nil {
		r.logger.Error("Failed to initialize encoder for buffered frames", recorderlog.Error(err))
		return
	}

	enc := r.getEncoder()
	if enc == nil {
		r.logger.Error("Encoder unavailable for buffered frames")
		return
	}

	for _, f := range frames {
		data, err := enc.Encode(f.Image, f.PTS)
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

// writeFlushedFrames writes flushed encoded frames to all active recordings.
// This is called during shutdown to capture any frames still in the encoder pipeline.
func (r *RecordingService) writeFlushedFrames(frames [][]byte) {
	if len(frames) == 0 {
		return
	}

	r.mu.RLock()
	var recs []*storage.Recording
	if r.currentRecording != nil {
		recs = append(recs, r.currentRecording)
	}
	if r.eventRecording != nil {
		recs = append(recs, r.eventRecording)
	}
	r.mu.RUnlock()

	now := time.Now()
	for _, data := range frames {
		for _, rec := range recs {
			if err := r.segmenter.WriteFrame(rec.ID, data, now); err != nil {
				r.logger.Error("Failed to write flushed frame",
					recorderlog.String("recording_id", rec.ID),
					recorderlog.Error(err))
			} else {
				r.metrics.BytesWritten.Add(uint64(len(data)))
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

	// Note: pendingRotation cleanup for this recording ID must be done by the
	// caller under r.mu before calling finalizeRecording. See Stop() and
	// StopManualRecording() which handle this correctly.

	// Finalize current segment and upload
	if seg := r.segmenter.Finalize(rec.ID); seg != nil {
		r.uploadSegment(rec, seg)
	}

	// Clean up segmenter state to prevent memory leak from accumulated segmentIndex entries
	r.segmenter.ClearRecordingState(rec.ID)

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

// metricsReporter periodically logs metrics and checks disk space
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

			// Check disk space during runtime
			diskStatus := r.checkDiskSpaceRuntime()
			switch diskStatus {
			case DiskStatusEmergency:
				r.handleDiskEmergency()
			case DiskStatusCritical:
				r.handleDiskCritical()
			case DiskStatusWarning:
				r.logger.Warn("WARNING: Disk space below 2GB")
			}
		}
	}
}

// reportMetrics logs current service metrics
func (r *RecordingService) reportMetrics() {
	// Use getEncoder() for safe access during potential watchdog restart
	enc := r.getEncoder()

	// Emergency buffer stats (always available)
	emergencySize := r.emergencyBuffer.Size()
	emergencyWrites := r.metrics.EmergencyBufferWrites.Load()
	emergencyDrains := r.metrics.EmergencyBufferDrains.Load()

	if enc == nil {
		r.logger.Info("Recording service metrics",
			recorderlog.Uint64("frames_received", r.metrics.FramesReceived.Load()),
			recorderlog.Uint64("frames_processed", r.metrics.FramesProcessed.Load()),
			recorderlog.Uint64("frames_dropped", r.metrics.FramesDropped.Load()),
			recorderlog.Uint64("recordings_started", r.metrics.RecordingsStarted.Load()),
			recorderlog.Uint64("recordings_ended", r.metrics.RecordingsEnded.Load()),
			recorderlog.Uint64("segments_created", r.metrics.SegmentsCreated.Load()),
			recorderlog.Uint64("keyframe_rotations", r.metrics.KeyframeRotations.Load()),
			recorderlog.Uint64("forced_rotations", r.metrics.ForcedRotations.Load()),
			recorderlog.Uint64("bytes_written", r.metrics.BytesWritten.Load()),
			recorderlog.Uint64("errors", r.metrics.Errors.Load()),
			recorderlog.Int("emergency_buffer_size", emergencySize),
			recorderlog.Uint64("emergency_buffer_writes", emergencyWrites),
			recorderlog.Uint64("emergency_buffer_drains", emergencyDrains),
		)
		return
	}

	em := enc.GetMetrics()
	r.logger.Info("Recording service metrics",
		recorderlog.Uint64("frames_received", r.metrics.FramesReceived.Load()),
		recorderlog.Uint64("frames_processed", r.metrics.FramesProcessed.Load()),
		recorderlog.Uint64("frames_dropped", r.metrics.FramesDropped.Load()),
		recorderlog.Uint64("recordings_started", r.metrics.RecordingsStarted.Load()),
		recorderlog.Uint64("recordings_ended", r.metrics.RecordingsEnded.Load()),
		recorderlog.Uint64("segments_created", r.metrics.SegmentsCreated.Load()),
		recorderlog.Uint64("keyframe_rotations", r.metrics.KeyframeRotations.Load()),
		recorderlog.Uint64("forced_rotations", r.metrics.ForcedRotations.Load()),
		recorderlog.Uint64("bytes_written", r.metrics.BytesWritten.Load()),
		recorderlog.Uint64("errors", r.metrics.Errors.Load()),
		recorderlog.Float64("encoder_bitrate", em.CurrentBitrate),
		recorderlog.Uint64("encoder_frames", em.FramesEncoded),
		recorderlog.Uint64("encoder_keyframes", em.KeyFrames),
		recorderlog.Int("emergency_buffer_size", emergencySize),
		recorderlog.Uint64("emergency_buffer_writes", emergencyWrites),
		recorderlog.Uint64("emergency_buffer_drains", emergencyDrains),
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

// DeleteRecording removes a recording and its segments from both object storage and the database.
// Object deletion failures are logged but do not abort the database cleanup, since dangling objects
// are preferable to dangling database rows.
func (r *RecordingService) DeleteRecording(ctx context.Context, id string) error {
	segments, err := r.metadataStore.GetSegments(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get segments for deletion: %w", err)
	}

	if len(segments) > 0 {
		keys := make([]string, len(segments))
		for i, seg := range segments {
			keys[i] = seg.StorageKey
		}
		if err := r.objectStore.DeleteMultiple(ctx, keys); err != nil {
			r.logger.Error("Failed to delete segments from object store", recorderlog.String("recording_id", id), recorderlog.Error(err))
		}
	}

	if err := r.metadataStore.DeleteRecording(ctx, id); err != nil {
		return fmt.Errorf("failed to delete recording metadata: %w", err)
	}

	r.logger.Info("Recording deleted", recorderlog.String("recording_id", id), recorderlog.Int("segments_removed", len(segments)))
	return nil
}

// UpdateRecordingName writes a display name into the recording's JSONB metadata column.
// The rest of the existing metadata map is preserved.
func (r *RecordingService) UpdateRecordingName(ctx context.Context, id string, name string) error {
	rec, err := r.metadataStore.GetRecording(ctx, id)
	if err != nil {
		return fmt.Errorf("recording not found: %w", err)
	}
	if rec.Metadata == nil {
		rec.Metadata = make(map[string]interface{})
	}
	rec.Metadata["name"] = name

	metaJSON, err := json.Marshal(rec.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	return r.metadataStore.UpdateRecording(ctx, id, map[string]interface{}{
		"metadata": string(metaJSON),
	})
}

// GetRecordingWithSegments retrieves a recording with its full segment list.
// Each segment with a valid storage key gets a fresh pre-signed URL (1-hour expiry).
func (r *RecordingService) GetRecordingWithSegments(ctx context.Context, id string) (*storage.Recording, error) {
	rec, err := r.metadataStore.GetRecording(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("recording not found: %w", err)
	}

	segments, err := r.metadataStore.GetSegments(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get segments: %w", err)
	}

	for _, seg := range segments {
		if seg.StorageKey != "" {
			url, err := r.objectStore.GeneratePresignedURL(ctx, seg.StorageKey, time.Hour)
			if err == nil {
				seg.PresignedURL = url
			}
		}
	}

	rec.Segments = segments
	return rec, nil
}

// GetObjectStore returns the underlying object store.
// The download handler uses this to stream raw segment bytes directly to the client.
func (r *RecordingService) GetObjectStore() storage.ObjectStore {
	return r.objectStore
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

// checkDiskSpaceRuntime checks disk space during runtime and returns status
func (r *RecordingService) checkDiskSpaceRuntime() DiskStatus {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(r.config.Recording.TempDir, &stat); err != nil {
		r.logger.Warn("Failed to check disk space", recorderlog.Error(err))
		return DiskStatusWarning // Assume warning if can't check
	}

	availableMB := (stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024)

	switch {
	case availableMB < 100:
		return DiskStatusEmergency
	case availableMB < 500:
		return DiskStatusCritical
	case availableMB < 2048:
		return DiskStatusWarning
	default:
		return DiskStatusHealthy
	}
}

// handleDiskEmergency handles emergency disk space situation
func (r *RecordingService) handleDiskEmergency() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Error("EMERGENCY: Disk nearly full, stopping all recordings")

	// Finalize all recordings immediately
	if r.currentRecording != nil {
		r.finalizeRecording(r.currentRecording)
		r.currentRecording = nil
		r.metrics.RecordingsEnded.Add(1)
	}
	if r.eventRecording != nil {
		r.finalizeRecording(r.eventRecording)
		r.eventRecording = nil
		r.metrics.RecordingsEnded.Add(1)
	}

	// Prevent new recordings
	r.continuousEnabled = false
	r.eventEnabled = false
}

// handleDiskCritical handles critical disk space situation
func (r *RecordingService) handleDiskCritical() {
	r.logger.Warn("CRITICAL: Low disk space, forcing segment rotation")

	// Force segment rotation to minimize data in flight
	// Use getEncoder() for safe access with correct mutex (encoderMu)
	if enc := r.getEncoder(); enc != nil {
		enc.ForceKeyframe()
	}
}

// emergencyFinalize performs best-effort finalization after a panic.
// Uses TryLock to avoid deadlock if another goroutine holds the lock.
func (r *RecordingService) emergencyFinalize() {
	r.logger.Warn("Emergency finalize triggered after panic")

	// Use a separate recovery to prevent double-panic
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("Secondary panic during emergency finalize",
				recorderlog.Any("panic", p))
		}
	}()

	// Use TryLock to avoid deadlock - if we can't get the lock, bail
	// It's better to lose some data than to deadlock the recovery
	if !r.mu.TryLock() {
		r.logger.Error("Emergency finalize: could not acquire lock, another goroutine may be holding it")
		return
	}
	defer r.mu.Unlock()

	// Best-effort finalization of continuous recording
	if r.currentRecording != nil {
		r.currentRecording.Status = storage.RecordingStatusPanicRecovered
		// Persist status to database so we can track panic recoveries
		if r.metadataStore != nil {
			if err := r.metadataStore.UpdateRecordingStatus(context.Background(), r.currentRecording.ID, storage.RecordingStatusPanicRecovered); err != nil {
				r.logger.Warn("Failed to persist panic_recovered status",
					recorderlog.String("recording_id", r.currentRecording.ID),
					recorderlog.Error(err))
			}
		}
		if seg := r.segmenter.Finalize(r.currentRecording.ID); seg != nil {
			// Try to upload what we have in background
			go r.uploadSegment(r.currentRecording, seg)
		}
		r.currentRecording = nil
	}

	// Best-effort finalization of event recording
	if r.eventRecording != nil {
		r.eventRecording.Status = storage.RecordingStatusPanicRecovered
		if r.metadataStore != nil {
			if err := r.metadataStore.UpdateRecordingStatus(context.Background(), r.eventRecording.ID, storage.RecordingStatusPanicRecovered); err != nil {
				r.logger.Warn("Failed to persist panic_recovered status",
					recorderlog.String("recording_id", r.eventRecording.ID),
					recorderlog.Error(err))
			}
		}
		if seg := r.segmenter.Finalize(r.eventRecording.ID); seg != nil {
			go r.uploadSegment(r.eventRecording, seg)
		}
		r.eventRecording = nil
	}
}

// watchdogMonitor periodically checks recording health and takes recovery actions
func (r *RecordingService) watchdogMonitor(ctx context.Context) {
	defer r.wg.Done()
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("PANIC in watchdog monitor",
				recorderlog.Any("panic", p),
				recorderlog.String("stack", string(debug.Stack())))
			r.metrics.Errors.Add(1)
			// Don't call emergencyFinalize from watchdog - it's a monitoring goroutine
			// The frame processor or motion handler will handle finalization
		}
	}()

	ticker := time.NewTicker(r.watchdogConfig.CheckInterval)
	defer ticker.Stop()

	r.logger.Info("Recording watchdog started",
		recorderlog.Duration("check_interval", r.watchdogConfig.CheckInterval),
		recorderlog.Duration("warning_threshold", r.watchdogConfig.WarningThreshold),
		recorderlog.Duration("restart_threshold", r.watchdogConfig.RestartThreshold),
		recorderlog.Duration("failure_threshold", r.watchdogConfig.FailureThreshold))

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("Recording watchdog stopped (context cancelled)")
			return
		case <-r.stopCh:
			r.logger.Info("Recording watchdog stopped")
			return
		case <-ticker.C:
			r.checkRecordingHealth()
		}
	}
}

// emergencyBufferDrain continuously moves frames from emergency buffer to main processing.
// Runs in background to recover from temporary overload conditions without frame loss.
func (r *RecordingService) emergencyBufferDrain(ctx context.Context) {
	defer r.wg.Done()
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("PANIC in emergency buffer drain",
				recorderlog.Any("panic", p),
				recorderlog.String("stack", string(debug.Stack())))
			r.metrics.Errors.Add(1)
		}
	}()

	// Check emergency buffer every 10ms for low latency drain
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	r.logger.Info("Emergency buffer drain started",
		recorderlog.Int("buffer_capacity", r.emergencyBufferSize))

	for {
		select {
		case <-ctx.Done():
			r.drainEmergencyBufferFinal()
			r.logger.Info("Emergency buffer drain stopped (context cancelled)")
			return
		case <-r.stopCh:
			r.drainEmergencyBufferFinal()
			r.logger.Info("Emergency buffer drain stopped")
			return
		case <-r.emergencyDrainStop:
			r.drainEmergencyBufferFinal()
			r.logger.Info("Emergency buffer drain stopped (signal)")
			return
		case <-ticker.C:
			r.drainEmergencyBuffer()
		}
	}
}

// drainEmergencyBuffer moves frames from emergency buffer to main channel when capacity exists.
func (r *RecordingService) drainEmergencyBuffer() {
	// Don't drain if emergency buffer is empty
	if r.emergencyBuffer.IsEmpty() {
		return
	}

	// Adaptive drain rate: scale up when buffer is under pressure.
	// This helps recover faster from overload conditions.
	bufferSize := r.emergencyBuffer.Size()
	fillRatio := float64(bufferSize) / float64(r.emergencyBufferSize)

	maxDrainPerTick := 5 // Base rate: 5 frames per 10ms tick
	if fillRatio > 0.5 {
		maxDrainPerTick = 15 // Elevated: buffer >50% full
	}
	if fillRatio > 0.8 {
		maxDrainPerTick = 30 // Aggressive: buffer >80% full
	}

	drained := 0

	for i := 0; i < maxDrainPerTick; i++ {
		if r.emergencyBuffer.IsEmpty() {
			break
		}

		// Check if primary channel has capacity BEFORE reading from emergency buffer.
		// This prevents the read-then-write-back pattern that causes frame reordering.
		if len(r.frameInput) >= cap(r.frameInput) {
			break // No capacity in primary channel, try again next tick
		}

		// Read from emergency buffer (advances cursor)
		frame, err := r.emergencyBuffer.Read()
		if err != nil {
			break // No more frames or error
		}

		// Send to primary channel - should succeed since we checked capacity,
		// but use select with default to handle rare race conditions
		select {
		case r.frameInput <- frame:
			r.metrics.EmergencyBufferDrains.Add(1)
			drained++
		default:
			// Race condition: channel filled between capacity check and send.
			// Drop this frame to preserve ordering of remaining frames in emergency buffer.
			r.metrics.FramesDropped.Add(1)
			r.logger.Warn("Emergency drain race: frame dropped to preserve ordering")
			break // Stop trying this tick
		}
	}

	// Log recovery progress periodically
	if drained > 0 && r.emergencyBuffer.Size() > 0 {
		r.logger.Debug("Emergency buffer draining",
			recorderlog.Int("drained_this_tick", drained),
			recorderlog.Int("remaining", r.emergencyBuffer.Size()))
	} else if drained > 0 && r.emergencyBuffer.IsEmpty() {
		r.logger.Info("Emergency buffer fully drained - normal operation resumed",
			recorderlog.Uint64("total_emergency_writes", r.metrics.EmergencyBufferWrites.Load()),
			recorderlog.Uint64("total_emergency_drains", r.metrics.EmergencyBufferDrains.Load()))
		r.bufferWarnLogged.Store(false) // Reset warning flag
	}
}

// drainEmergencyBufferFinal attempts to drain all remaining frames during shutdown.
func (r *RecordingService) drainEmergencyBufferFinal() {
	if r.emergencyBuffer.IsEmpty() {
		return
	}

	remaining := r.emergencyBuffer.Size()
	r.logger.Info("Draining emergency buffer before shutdown",
		recorderlog.Int("frames_remaining", remaining))

	drained := 0
	timeout := time.After(5 * time.Second)

	for !r.emergencyBuffer.IsEmpty() {
		select {
		case <-timeout:
			r.logger.Warn("Emergency buffer drain timeout - some frames may be lost",
				recorderlog.Int("frames_lost", r.emergencyBuffer.Size()))
			return
		default:
		}

		frame, err := r.emergencyBuffer.Read()
		if err != nil {
			break
		}

		// Try to send with short timeout
		select {
		case r.frameInput <- frame:
			r.metrics.EmergencyBufferDrains.Add(1)
			drained++
		case <-time.After(100 * time.Millisecond):
			// Channel blocked, frame will be lost
			r.metrics.FramesDropped.Add(1)
		}
	}

	r.logger.Info("Emergency buffer shutdown drain complete",
		recorderlog.Int("frames_drained", drained),
		recorderlog.Int("frames_remaining", r.emergencyBuffer.Size()))
}

// retentionCleanup periodically deletes old segments from both object storage and the database.
// Uses config.Storage.CleanupInterval for the tick period and config.Recording.RetentionDays for the cutoff.
func (r *RecordingService) retentionCleanup(ctx context.Context) {
	defer r.wg.Done()

	retentionDays := r.config.Recording.RetentionDays
	if retentionDays <= 0 {
		r.logger.Info("Retention cleanup disabled (retentionDays <= 0)")
		return
	}

	interval := r.config.Storage.CleanupInterval
	if interval <= 0 {
		interval = 1 * time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	r.logger.Info("Retention cleanup started",
		recorderlog.Int("retention_days", retentionDays),
		recorderlog.Duration("interval", interval))

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("Retention cleanup stopped (context cancelled)")
			return
		case <-r.stopCh:
			r.logger.Info("Retention cleanup stopped")
			return
		case <-ticker.C:
			r.runRetentionCleanup(ctx, retentionDays)
		}
	}
}

func (r *RecordingService) runRetentionCleanup(ctx context.Context, retentionDays int) {
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Query DB for storage keys of expired segments (avoids expensive List("") on object store)
	keys, err := r.metadataStore.GetExpiredSegmentKeys(cleanupCtx, cutoff)
	if err != nil {
		r.logger.Error("Retention cleanup: failed to query expired segments", recorderlog.Error(err))
		return
	}

	if len(keys) == 0 {
		return
	}

	// Delete from object storage first
	if err := r.objectStore.DeleteMultiple(cleanupCtx, keys); err != nil {
		r.logger.Error("Retention cleanup: failed to delete from object storage",
			recorderlog.Error(err),
			recorderlog.Int("count", len(keys)))
	}

	// Then delete metadata from database
	deleted, err := r.metadataStore.DeleteOldSegments(cleanupCtx, cutoff)
	if err != nil {
		r.logger.Error("Retention cleanup: failed to delete old segments from database",
			recorderlog.Error(err))
		return
	}

	r.logger.Info("Retention cleanup completed",
		recorderlog.Int64("db_segments_deleted", deleted),
		recorderlog.Int("storage_objects_removed", len(keys)))
}

// checkRecordingHealth examines all active recordings for stalls
func (r *RecordingService) checkRecordingHealth() {
	r.metrics.WatchdogChecks.Add(1)

	// Get active recordings
	r.mu.RLock()
	var activeRecs []*storage.Recording
	if r.currentRecording != nil {
		activeRecs = append(activeRecs, r.currentRecording)
	}
	if r.eventRecording != nil {
		activeRecs = append(activeRecs, r.eventRecording)
	}
	r.mu.RUnlock()

	now := time.Now()

	for _, rec := range activeRecs {
		health := r.getOrCreateHealth(rec.ID)
		lastWrite := r.getLastFrameWrite(health)

		// Skip if never written (recording just started or encoder not initialized)
		if lastWrite.IsZero() {
			continue
		}

		staleDuration := now.Sub(lastWrite)

		// Escalating actions based on stale duration
		if staleDuration >= r.watchdogConfig.FailureThreshold {
			r.handleRecordingFailure(rec, health, staleDuration)
		} else if staleDuration >= r.watchdogConfig.RestartThreshold {
			r.handleEncoderRestart(rec, health, staleDuration)
		} else if staleDuration >= r.watchdogConfig.WarningThreshold {
			r.handleStallWarning(rec, health, staleDuration)
		} else {
			// Healthy - reset warning/critical flags if previously set
			r.resetHealthStatus(rec.ID, health)
		}
	}
}

// getOrCreateHealth returns existing health tracker or creates a new one
func (r *RecordingService) getOrCreateHealth(recordingID string) *RecordingHealth {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()

	health, exists := r.recordingHealth[recordingID]
	if !exists {
		health = &RecordingHealth{
			RecordingID: recordingID,
		}
		health.Status.Store(HealthStatusHealthy)
		health.LastFrameWrite.Store(time.Time{})
		r.recordingHealth[recordingID] = health
	}
	return health
}

// getLastFrameWrite safely retrieves the last frame write time
func (r *RecordingService) getLastFrameWrite(health *RecordingHealth) time.Time {
	if val := health.LastFrameWrite.Load(); val != nil {
		if t, ok := val.(time.Time); ok {
			return t
		}
	}
	return time.Time{}
}

// updateHeartbeat records a successful frame write
func (r *RecordingService) updateHeartbeat(recordingID string) {
	health := r.getOrCreateHealth(recordingID)
	health.LastFrameWrite.Store(time.Now())
	health.LastFrameCount.Add(1)
}

// handleStallWarning logs a warning for stalled recording
func (r *RecordingService) handleStallWarning(rec *storage.Recording, health *RecordingHealth, staleDuration time.Duration) {
	if health.WarningIssued.Load() {
		return // Already warned
	}

	health.WarningIssued.Store(true)
	health.Status.Store(HealthStatusWarning)
	health.ConsecutiveStalls.Add(1)
	r.metrics.StallsDetected.Add(1)

	r.logger.Warn("Recording stall detected",
		recorderlog.String("recording_id", rec.ID),
		recorderlog.String("type", rec.Type),
		recorderlog.Duration("stale_duration", staleDuration),
		recorderlog.Uint64("frame_count", health.LastFrameCount.Load()))
}

// handleEncoderRestart attempts to restart the encoder for a stalled recording
func (r *RecordingService) handleEncoderRestart(rec *storage.Recording, health *RecordingHealth, staleDuration time.Duration) {
	if health.CriticalIssued.Load() {
		return // Already attempted restart at this level
	}

	// Check restart attempts
	r.healthMu.Lock()
	attempts := r.restartAttempts[rec.ID]
	if attempts >= r.watchdogConfig.MaxRestartAttempts {
		r.healthMu.Unlock()
		return // Max restarts exceeded, wait for failure threshold
	}
	r.restartAttempts[rec.ID] = attempts + 1
	r.healthMu.Unlock()

	health.CriticalIssued.Store(true)
	health.Status.Store(HealthStatusCritical)
	r.metrics.EncoderRestarts.Add(1)

	r.logger.Warn("Attempting encoder restart due to stall",
		recorderlog.String("recording_id", rec.ID),
		recorderlog.Duration("stale_duration", staleDuration),
		recorderlog.Int("restart_attempt", attempts+1),
		recorderlog.Int("max_attempts", r.watchdogConfig.MaxRestartAttempts))

	// Perform encoder restart under mutex
	r.encoderMu.Lock()
	defer r.encoderMu.Unlock()

	if r.encoder == nil {
		r.logger.Warn("Cannot restart encoder - encoder is nil")
		return
	}

	// Close existing encoder
	if err := r.encoder.Close(); err != nil {
		r.logger.Error("Failed to close encoder during restart",
			recorderlog.Error(err))
	}

	// Clear initialized flag to force re-creation on next frame
	r.encoderInitialized.Store(false)
	r.encoder = nil

	r.logger.Info("Encoder closed for restart - will reinitialize on next frame",
		recorderlog.String("recording_id", rec.ID))
}

// handleRecordingFailure marks a recording as failed after exceeding failure threshold
func (r *RecordingService) handleRecordingFailure(rec *storage.Recording, health *RecordingHealth, staleDuration time.Duration) {
	// Check if already failed
	if val := health.Status.Load(); val != nil {
		if status, ok := val.(HealthStatus); ok && status == HealthStatusFailed {
			return
		}
	}

	health.Status.Store(HealthStatusFailed)
	r.metrics.RecordingFailures.Add(1)

	r.logger.Error("Recording marked as failed due to prolonged stall",
		recorderlog.String("recording_id", rec.ID),
		recorderlog.String("type", rec.Type),
		recorderlog.Duration("stale_duration", staleDuration),
		recorderlog.Int("consecutive_stalls", int(health.ConsecutiveStalls.Load())))

	// Finalize the failed recording
	r.mu.Lock()
	if r.currentRecording != nil && r.currentRecording.ID == rec.ID {
		r.finalizeRecording(r.currentRecording)
		r.currentRecording = nil
		r.metrics.RecordingsEnded.Add(1)
	}
	if r.eventRecording != nil && r.eventRecording.ID == rec.ID {
		r.finalizeRecording(r.eventRecording)
		r.eventRecording = nil
		r.metrics.RecordingsEnded.Add(1)
	}
	r.mu.Unlock()

	// Clean up health tracking for this recording
	r.healthMu.Lock()
	delete(r.recordingHealth, rec.ID)
	delete(r.restartAttempts, rec.ID)
	r.healthMu.Unlock()
}

// resetHealthStatus clears warning/critical flags when recording returns to healthy
func (r *RecordingService) resetHealthStatus(recordingID string, health *RecordingHealth) {
	wasWarning := health.WarningIssued.Load()
	wasCritical := health.CriticalIssued.Load()

	if wasWarning || wasCritical {
		health.WarningIssued.Store(false)
		health.CriticalIssued.Store(false)
		health.Status.Store(HealthStatusHealthy)
		health.ConsecutiveStalls.Store(0)

		// Reset restart attempts on recovery
		r.healthMu.Lock()
		r.restartAttempts[recordingID] = 0
		r.healthMu.Unlock()

		r.logger.Info("Recording health recovered",
			recorderlog.String("recording_id", recordingID))
	}
}

// GetHealthStatus returns the current health status for all recordings (for API exposure)
func (r *RecordingService) GetHealthStatus() map[string]*RecordingHealthInfo {
	r.mu.RLock()
	var activeRecs []*storage.Recording
	if r.currentRecording != nil {
		activeRecs = append(activeRecs, r.currentRecording)
	}
	if r.eventRecording != nil {
		activeRecs = append(activeRecs, r.eventRecording)
	}
	r.mu.RUnlock()

	r.healthMu.RLock()
	defer r.healthMu.RUnlock()

	result := make(map[string]*RecordingHealthInfo)
	now := time.Now()

	for _, rec := range activeRecs {
		health, exists := r.recordingHealth[rec.ID]
		if !exists {
			// Recording exists but no health tracking yet (just started)
			result[rec.ID] = &RecordingHealthInfo{
				RecordingID:       rec.ID,
				Type:              rec.Type,
				Status:            HealthStatusHealthy,
				FrameCount:        0,
				ConsecutiveStalls: 0,
				RestartAttempts:   0,
			}
			continue
		}

		lastWrite := r.getLastFrameWrite(health)
		var staleDuration time.Duration
		if !lastWrite.IsZero() {
			staleDuration = now.Sub(lastWrite)
		}

		status := HealthStatusHealthy
		if val := health.Status.Load(); val != nil {
			if s, ok := val.(HealthStatus); ok {
				status = s
			}
		}

		result[rec.ID] = &RecordingHealthInfo{
			RecordingID:       rec.ID,
			Type:              rec.Type,
			Status:            status,
			LastFrameWrite:    lastWrite,
			StaleDuration:     staleDuration,
			FrameCount:        health.LastFrameCount.Load(),
			ConsecutiveStalls: health.ConsecutiveStalls.Load(),
			RestartAttempts:   r.restartAttempts[rec.ID],
		}
	}

	return result
}

// RecordingHealthInfo is an exportable health status for API responses
type RecordingHealthInfo struct {
	RecordingID       string        `json:"recording_id"`
	Type              string        `json:"type"`
	Status            HealthStatus  `json:"status"`
	LastFrameWrite    time.Time     `json:"last_frame_write,omitempty"`
	StaleDuration     time.Duration `json:"stale_duration,omitempty"`
	FrameCount        uint64        `json:"frame_count"`
	ConsecutiveStalls uint32        `json:"consecutive_stalls"`
	RestartAttempts   int           `json:"restart_attempts"`
}

// GetMetrics returns the current metrics for API exposure
func (r *RecordingService) GetMetrics() *MetricsSnapshot {
	return &MetricsSnapshot{
		FramesReceived:        r.metrics.FramesReceived.Load(),
		FramesProcessed:       r.metrics.FramesProcessed.Load(),
		FramesDropped:         r.metrics.FramesDropped.Load(),
		RecordingsStarted:     r.metrics.RecordingsStarted.Load(),
		RecordingsEnded:       r.metrics.RecordingsEnded.Load(),
		SegmentsCreated:       r.metrics.SegmentsCreated.Load(),
		BytesWritten:          r.metrics.BytesWritten.Load(),
		Errors:                r.metrics.Errors.Load(),
		WatchdogChecks:        r.metrics.WatchdogChecks.Load(),
		StallsDetected:        r.metrics.StallsDetected.Load(),
		EncoderRestarts:       r.metrics.EncoderRestarts.Load(),
		RecordingFailures:     r.metrics.RecordingFailures.Load(),
		EmergencyBufferWrites: r.metrics.EmergencyBufferWrites.Load(),
		EmergencyBufferDrains: r.metrics.EmergencyBufferDrains.Load(),
		EmergencyBufferDrops:  r.metrics.EmergencyBufferDrops.Load(),
		EmergencyBufferSize:   r.emergencyBuffer.Size(),
	}
}

// MetricsSnapshot is a point-in-time copy of metrics for API responses
type MetricsSnapshot struct {
	FramesReceived    uint64 `json:"frames_received"`
	FramesProcessed   uint64 `json:"frames_processed"`
	FramesDropped     uint64 `json:"frames_dropped"`
	RecordingsStarted uint64 `json:"recordings_started"`
	RecordingsEnded   uint64 `json:"recordings_ended"`
	SegmentsCreated   uint64 `json:"segments_created"`
	BytesWritten      uint64 `json:"bytes_written"`
	Errors            uint64 `json:"errors"`
	WatchdogChecks    uint64 `json:"watchdog_checks"`
	StallsDetected    uint64 `json:"stalls_detected"`
	EncoderRestarts   uint64 `json:"encoder_restarts"`
	RecordingFailures uint64 `json:"recording_failures"`

	// Emergency buffer metrics
	EmergencyBufferWrites uint64 `json:"emergency_buffer_writes"`
	EmergencyBufferDrains uint64 `json:"emergency_buffer_drains"`
	EmergencyBufferDrops  uint64 `json:"emergency_buffer_drops"`
	EmergencyBufferSize   int    `json:"emergency_buffer_current_size"`
}

// RecordingState describes the current operational state of the recording service
type RecordingState struct {
	State              string   `json:"state"`                // idle, continuous, event, manual
	ActiveRecordingIDs []string `json:"active_recording_ids"`
	IsRunning          bool     `json:"is_running"`
}

// GetRecordingState returns the current operational state
func (r *RecordingService) GetRecordingState() RecordingState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state := RecordingState{
		State:     "idle",
		IsRunning: r.running.Load(),
	}

	if r.currentRecording != nil {
		state.State = "continuous"
		state.ActiveRecordingIDs = append(state.ActiveRecordingIDs, r.currentRecording.ID)
	}
	if r.eventRecording != nil {
		if state.State == "continuous" {
			state.State = "continuous+event"
		} else {
			state.State = "event"
		}
		state.ActiveRecordingIDs = append(state.ActiveRecordingIDs, r.eventRecording.ID)
	}

	return state
}

// StartManualRecording starts a continuous recording session on demand.
// startContinuousRecording writes r.currentRecording, so we hold the lock throughout.
// The only I/O is a single DB insert (SaveRecording), which is fast enough under lock.
func (r *RecordingService) StartManualRecording(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentRecording != nil {
		return r.currentRecording.ID, nil
	}

	if err := r.startContinuousRecording(ctx); err != nil {
		return "", fmt.Errorf("failed to start manual recording: %w", err)
	}

	if r.currentRecording != nil {
		return r.currentRecording.ID, nil
	}
	return "", fmt.Errorf("recording started but no recording ID available")
}

// StopManualRecording stops the current continuous recording.
// We extract the recording reference under lock, clear pending state, then finalize without the lock.
func (r *RecordingService) StopManualRecording() error {
	r.mu.Lock()
	if r.currentRecording == nil {
		r.mu.Unlock()
		return fmt.Errorf("no active continuous recording")
	}
	rec := r.currentRecording
	r.currentRecording = nil
	delete(r.pendingRotation, rec.ID)
	delete(r.pendingRotationTime, rec.ID)
	r.mu.Unlock()

	// finalizeRecording does I/O (DB updates, encoder flush) — must not hold mu
	r.finalizeRecording(rec)
	return nil
}
