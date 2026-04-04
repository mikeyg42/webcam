package framestream

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/mediadevices/pkg/wave"
)

// ============================================================================
//  FRAME DISTRIBUTOR
// ============================================================================

// FrameDistributor manages a single camera source and distributes frames to multiple consumers
type FrameDistributor struct {
	// Camera configuration
	camera        mediadevices.MediaDeviceInfo
	stream        mediadevices.MediaStream

	// Audio configuration
	microphone         mediadevices.MediaDeviceInfo
	audioEnabled       bool
	audioStream        mediadevices.MediaStream

	// Lifecycle management
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup  // Track goroutines for clean shutdown

	// State management with atomic for race-free checks
	isRunning atomic.Bool

	// Broadcasters for subscription-based distribution
	// These survive Stop/Start cycles - subscriptions remain valid
	webrtcBroadcaster *Broadcaster[image.Image]
	motionBroadcaster *Broadcaster[image.Image]
	recordBroadcaster *Broadcaster[image.Image]
	audioBroadcaster  *Broadcaster[*AudioFrame]

	// Pre-motion buffer
	preMotionBuffer *CircularFrameBuffer

	// Statistics with atomic counters for lock-free updates
	stats struct {
		totalFrames    atomic.Int64
		droppedFrames  atomic.Int64
		webrtcSent     atomic.Int64  // H.264 frames sent to WebRTC
		motionSent     atomic.Int64
		recordSent     atomic.Int64
		audioSent      atomic.Int64  // Audio frames sent to recorder
		lastFrameTime  atomic.Value // stores time.Time
	}
}

// DistributorStats tracks frame distribution performance metrics
type DistributorStats struct {
	TotalFrames   int64
	DroppedFrames int64
	WebRTCSent    int64  // H.264 frames sent to WebRTC
	MotionSent    int64
	RecordSent    int64
	AudioSent     int64  // Audio frames sent to recorder
	LastFrameTime time.Time
}

// ImageFrame wraps an image with metadata for buffering and tracking
type ImageFrame struct {
	Image     image.Image
	Timestamp time.Time
	Sequence  int64
}

// AudioFrame wraps audio data with metadata for recording
type AudioFrame struct {
	Data      []byte
	Timestamp time.Time
	Duration  time.Duration
}

// NewFrameDistributor creates a new single-source frame distributor
func NewFrameDistributor(ctx context.Context, camera mediadevices.MediaDeviceInfo,
	microphone mediadevices.MediaDeviceInfo, audioEnabled bool) (*FrameDistributor, error) {

	fdCtx, cancel := context.WithCancel(ctx)

	// Pre-motion buffer: 5 seconds at 15fps = 75 frames
	preMotionBuffer := NewCircularFrameBuffer(75)

	fd := &FrameDistributor{
		camera:            camera,
		microphone:        microphone,
		audioEnabled:      audioEnabled,
		ctx:               fdCtx,
		cancel:            cancel,
		webrtcBroadcaster: NewBroadcaster[image.Image]("WebRTC", 10),
		motionBroadcaster: NewBroadcaster[image.Image]("Motion", 5),
		recordBroadcaster: NewBroadcaster[image.Image]("Record", 60),
		audioBroadcaster:  NewBroadcaster[*AudioFrame]("Audio", 50),
		preMotionBuffer:   preMotionBuffer,
	}

	// Initialize last frame time
	fd.stats.lastFrameTime.Store(time.Now())

	return fd, nil
}

// Start begins frame capture and distribution
func (fd *FrameDistributor) Start(width, height int) error {
	// Atomic compare-and-swap prevents race conditions
	if !fd.isRunning.CompareAndSwap(false, true) {
		log.Printf("[FrameDistributor] Already running")
		return nil
	}

	log.Printf("[FrameDistributor] Starting single camera capture at %dx%d", width, height)

	// Resume broadcasters (subscriptions survive restarts)
	fd.webrtcBroadcaster.Resume()
	fd.motionBroadcaster.Resume()
	fd.recordBroadcaster.Resume()
	fd.audioBroadcaster.Resume()

	// Create new cancellable context for this run
	fd.ctx, fd.cancel = context.WithCancel(context.Background())

	// Configure camera constraints (no codec - raw frames only)
	constraints := mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(fd.camera.DeviceID)
			c.Width = prop.IntExact(width)
			c.Height = prop.IntExact(height)
			c.FrameRate = prop.FloatExact(15)  // 15fps
		},
	}

	// Create media stream
	stream, err := mediadevices.GetUserMedia(constraints)
	if err != nil {
		fd.isRunning.Store(false)
		return fmt.Errorf("failed to get user media: %v", err)
	}
	fd.stream = stream

	// Start distribution goroutine
	fd.wg.Add(1)
	go fd.distributeFrames()

	// Start audio capture if enabled
	if fd.audioEnabled && fd.microphone.DeviceID != "" {
		if err := fd.startAudioCapture(); err != nil {
			log.Printf("[FrameDistributor] Warning: Failed to start audio capture: %v", err)
		} else {
			log.Printf("[FrameDistributor] Audio capture started")
		}
	}

	log.Printf("[FrameDistributor] Started successfully - single camera source active")
	return nil
}

// distributeFrames continuously reads from camera and fans out to consumers
func (fd *FrameDistributor) distributeFrames() {
	defer fd.wg.Done()
	defer fd.cleanup()

	// Get video track
	videoTracks := fd.stream.GetVideoTracks()
	if len(videoTracks) == 0 {
		log.Printf("[FrameDistributor] ERROR: No video tracks available")
		return
	}

	track := videoTracks[0]
	log.Printf("[FrameDistributor] Processing frames from track: %s", track.ID())

	videoTrack, ok := track.(*mediadevices.VideoTrack)
	if !ok {
		log.Printf("[FrameDistributor] ERROR: Track is not a VideoTrack: %T", track)
		return
	}

	// Create reader for raw frames
	videoReader := videoTrack.NewReader(false)

	var frameSequence int64

	// Main processing loop
	for {
		select {
		case <-fd.ctx.Done():
			log.Printf("[FrameDistributor] Stopping due to context cancellation")
			return

		default:
			// Read next frame
			img, release, err := videoReader.Read()
			if err != nil {
				log.Printf("[FrameDistributor] Error reading frame: %v", err)
				time.Sleep(10 * time.Millisecond)
				continue
			}

			frameSequence++
			fd.processFrame(img, release, frameSequence)
		}
	}
}

// processFrame handles a single frame from the camera
func (fd *FrameDistributor) processFrame(img image.Image, release func(), sequence int64) {
	defer func() {
		if release != nil {
			release()
		}
	}()

	if img == nil {
		return
	}

	// Update stats atomically (no locks needed!)
	fd.stats.totalFrames.Add(1)
	fd.stats.lastFrameTime.Store(time.Now())

	// Store frame in pre-motion buffer with efficient cloning
	fd.storeInPreMotionBuffer(img, sequence)

	// Distribute to all consumers
	fd.sendToConsumers(img)

	// Log stats every 150 frames (~10 seconds at 15fps)
	if sequence%150 == 0 {
		fd.logStats()
	}
}

// storeInPreMotionBuffer saves frame with efficient cloning
func (fd *FrameDistributor) storeInPreMotionBuffer(img image.Image, sequence int64) {
	cloned := fd.efficientClone(img)
	if cloned == nil {
		return
	}

	frame := &ImageFrame{
		Image:     cloned,
		Timestamp: time.Now(),
		Sequence:  sequence,
	}

	fd.preMotionBuffer.Add(frame)
}

// efficientClone creates an optimized copy of the image
// Uses type-specific fast paths instead of slow pixel-by-pixel copying
func (fd *FrameDistributor) efficientClone(img image.Image) image.Image {
	switch src := img.(type) {
	case *image.RGBA:
		// Fast path: copy struct and pixel buffer
		dst := *src
		dst.Pix = make([]byte, len(src.Pix))
		copy(dst.Pix, src.Pix)
		return &dst

	case *image.YCbCr:
		// Fast path: copy struct and YCbCr planes
		dst := *src
		dst.Y = make([]byte, len(src.Y))
		dst.Cb = make([]byte, len(src.Cb))
		dst.Cr = make([]byte, len(src.Cr))
		copy(dst.Y, src.Y)
		copy(dst.Cb, src.Cb)
		copy(dst.Cr, src.Cr)
		return &dst

	default:
		// Fallback: use draw.Draw (much faster than At/Set loop!)
		bounds := img.Bounds()
		dst := image.NewRGBA(bounds)
		draw.Draw(dst, bounds, img, bounds.Min, draw.Src)
		return dst
	}
}

// sendToConsumers distributes frame to all subscribers via broadcasters
func (fd *FrameDistributor) sendToConsumers(img image.Image) {
	// CRITICAL: Clone the image before broadcasting!
	// The camera's underlying buffer is released after processFrame returns (via release()).
	// Without cloning, subscribers receive frames that may be overwritten by the next capture.
	// This caused calibration to show 0% motion - optical flow compared identical/corrupted frames.
	cloned := fd.efficientClone(img)
	if cloned == nil {
		return
	}

	// Debug: log frame data fingerprint every 30 frames to verify cloning works
	seq := fd.stats.totalFrames.Load()
	if seq%30 == 0 {
		if ycbcr, ok := cloned.(*image.YCbCr); ok && len(ycbcr.Y) > 100 {
			// Sample first few Y pixels as fingerprint
			log.Printf("[FrameDistributor] Frame %d fingerprint: Y[0:8]=%v", seq, ycbcr.Y[0:8])
		}
	}

	// Broadcast cloned frame to all subscribers
	fd.webrtcBroadcaster.Broadcast(cloned)
	fd.stats.webrtcSent.Add(1)

	fd.motionBroadcaster.Broadcast(cloned)
	fd.stats.motionSent.Add(1)

	fd.recordBroadcaster.Broadcast(cloned)
	fd.stats.recordSent.Add(1)
}

// logStats prints current statistics
func (fd *FrameDistributor) logStats() {
	total := fd.stats.totalFrames.Load()
	dropped := fd.stats.droppedFrames.Load()
	webrtc := fd.stats.webrtcSent.Load()
	motion := fd.stats.motionSent.Load()
	record := fd.stats.recordSent.Load()

	dropRate := float64(0)
	if total > 0 {
		dropRate = float64(dropped) * 100.0 / float64(total*3) // 3 channels
	}

	log.Printf("[FrameDistributor] Stats - Total: %d, WebRTC: %d, Motion: %d, Record: %d, Drop rate: %.1f%%",
		total, webrtc, motion, record, dropRate)
}

// Stop gracefully stops the frame distributor
func (fd *FrameDistributor) Stop() {
	// Atomic compare-and-swap for race-free state change
	if !fd.isRunning.CompareAndSwap(true, false) {
		return  // Already stopped
	}

	log.Printf("[FrameDistributor] Stopping...")
	fd.cancel()

	// Wait for goroutine to finish with timeout
	done := make(chan struct{})
	go func() {
		fd.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("[FrameDistributor] Stopped cleanly")
	case <-time.After(5 * time.Second):
		log.Printf("[FrameDistributor] Stop timeout - forcing shutdown")
	}
}

// cleanup releases all resources
// Called automatically by distributeFrames when it exits
func (fd *FrameDistributor) cleanup() {
	log.Printf("[FrameDistributor] Cleaning up resources...")

	// Close media stream first
	if fd.stream != nil {
		for _, track := range fd.stream.GetTracks() {
			track.Close()
		}
	}

	// Close audio stream if it exists
	if fd.audioStream != nil {
		for _, track := range fd.audioStream.GetTracks() {
			track.Close()
		}
	}

	// Pause broadcasters (subscriptions remain valid for restart)
	fd.webrtcBroadcaster.Pause()
	fd.motionBroadcaster.Pause()
	fd.recordBroadcaster.Pause()
	fd.audioBroadcaster.Pause()

	// Clear pre-motion buffer
	fd.preMotionBuffer.Clear()

	log.Printf("[FrameDistributor] Cleanup completed")
}

// startAudioCapture initializes microphone capture and starts audio distribution
func (fd *FrameDistributor) startAudioCapture() error {
	// Configure microphone constraints for Opus-compatible settings
	constraints := mediadevices.MediaStreamConstraints{
		Audio: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(fd.microphone.DeviceID)
			c.SampleRate = prop.IntExact(48000)  // 48kHz for Opus
			c.ChannelCount = prop.IntExact(2)    // Stereo
		},
	}

	// Create audio stream
	audioStream, err := mediadevices.GetUserMedia(constraints)
	if err != nil {
		return fmt.Errorf("failed to get audio media: %v", err)
	}
	fd.audioStream = audioStream

	// Start audio distribution goroutine
	fd.wg.Add(1)
	go fd.distributeAudioFrames()

	return nil
}

// distributeAudioFrames reads from the microphone and distributes to the audio channel
func (fd *FrameDistributor) distributeAudioFrames() {
	defer fd.wg.Done()

	// Get audio track
	audioTracks := fd.audioStream.GetAudioTracks()
	if len(audioTracks) == 0 {
		log.Printf("[FrameDistributor] ERROR: No audio tracks available")
		return
	}

	track := audioTracks[0]
	log.Printf("[FrameDistributor] Processing audio from track: %s", track.ID())

	audioTrack, ok := track.(*mediadevices.AudioTrack)
	if !ok {
		log.Printf("[FrameDistributor] ERROR: Track is not an AudioTrack: %T", track)
		return
	}

	// Create reader for raw audio samples
	audioReader := audioTrack.NewReader(false)

	for {
		select {
		case <-fd.ctx.Done():
			log.Printf("[FrameDistributor] Stopping audio capture due to context cancellation")
			return

		default:
			// Read next audio chunk
			chunk, release, err := audioReader.Read()
			if err != nil {
				log.Printf("[FrameDistributor] Error reading audio: %v", err)
				time.Sleep(10 * time.Millisecond)
				continue
			}

			// Get chunk info for duration calculation
			info := chunk.ChunkInfo()

			// Calculate duration from samples: duration = samples / sampleRate
			var durationNs int64
			if info.SamplingRate > 0 {
				durationNs = int64(info.Len) * int64(time.Second) / int64(info.SamplingRate)
			} else {
				durationNs = int64(20 * time.Millisecond) // Default Opus frame duration
			}

			// Convert audio samples to raw bytes
			var audioData []byte
			switch audio := chunk.(type) {
			case *wave.Int16Interleaved:
				// Convert int16 samples to bytes (little-endian)
				audioData = make([]byte, len(audio.Data)*2)
				for i, sample := range audio.Data {
					audioData[i*2] = byte(sample)
					audioData[i*2+1] = byte(sample >> 8)
				}
			case *wave.Float32Interleaved:
				// Convert float32 samples to int16 bytes (little-endian)
				audioData = make([]byte, len(audio.Data)*2)
				for i, sample := range audio.Data {
					// Clamp and convert float32 [-1.0, 1.0] to int16
					s := int16(sample * 32767)
					audioData[i*2] = byte(s)
					audioData[i*2+1] = byte(s >> 8)
				}
			default:
				// Generic fallback: read samples using the interface
				audioData = make([]byte, info.Len*info.Channels*2)
				for i := 0; i < info.Len; i++ {
					for ch := 0; ch < info.Channels; ch++ {
						sample := chunk.At(i, ch)
						// Convert sample to int16
						var s int16
						switch v := sample.(type) {
						case wave.Int16Sample:
							s = int16(v)
						case wave.Float32Sample:
							s = int16(float32(v) * 32767)
						}
						idx := (i*info.Channels + ch) * 2
						audioData[idx] = byte(s)
						audioData[idx+1] = byte(s >> 8)
					}
				}
			}

			// Create audio frame
			frame := &AudioFrame{
				Data:      audioData,
				Timestamp: time.Now(),
				Duration:  time.Duration(durationNs),
			}

			// Release the original buffer
			if release != nil {
				release()
			}

			// Broadcast to all audio subscribers
			fd.audioBroadcaster.Broadcast(frame)
			fd.stats.audioSent.Add(1)
		}
	}
}

// SubscribeWebRTC creates a subscription for WebRTC encoder frames.
// The subscription survives distributor restarts.
func (fd *FrameDistributor) SubscribeWebRTC() *Subscription[image.Image] {
	return fd.webrtcBroadcaster.Subscribe()
}

// SubscribeMotion creates a subscription for motion detection frames.
// The subscription survives distributor restarts.
func (fd *FrameDistributor) SubscribeMotion() *Subscription[image.Image] {
	return fd.motionBroadcaster.Subscribe()
}

// SubscribeRecord creates a subscription for recording frames.
// The subscription survives distributor restarts.
func (fd *FrameDistributor) SubscribeRecord() *Subscription[image.Image] {
	return fd.recordBroadcaster.Subscribe()
}

// SubscribeAudio creates a subscription for audio recording frames.
// The subscription survives distributor restarts.
func (fd *FrameDistributor) SubscribeAudio() *Subscription[*AudioFrame] {
	return fd.audioBroadcaster.Subscribe()
}

// GetPreMotionFrames returns buffered frames from before motion was detected
func (fd *FrameDistributor) GetPreMotionFrames() []*ImageFrame {
	return fd.preMotionBuffer.GetAll()
}

// IsRunning returns whether the distributor is currently running
func (fd *FrameDistributor) IsRunning() bool {
	return fd.isRunning.Load()
}

// UpdateDevices updates the camera and microphone devices.
// Must be called while the distributor is stopped.
// Returns error if the distributor is currently running or if device validation fails.
func (fd *FrameDistributor) UpdateDevices(camera mediadevices.MediaDeviceInfo,
	microphone mediadevices.MediaDeviceInfo, audioEnabled bool) error {
	if fd.isRunning.Load() {
		return fmt.Errorf("cannot update devices while distributor is running")
	}

	// Validate camera device - DeviceID is required
	if camera.DeviceID == "" {
		return fmt.Errorf("camera DeviceID is required")
	}

	// Validate microphone device if audio is enabled
	if audioEnabled && microphone.DeviceID == "" {
		return fmt.Errorf("microphone DeviceID is required when audio is enabled")
	}

	fd.camera = camera
	fd.microphone = microphone
	fd.audioEnabled = audioEnabled

	log.Printf("[FrameDistributor] Devices updated - Camera: %s, Microphone: %s, Audio: %v",
		camera.Label, microphone.Label, audioEnabled)
	return nil
}

// GetStats returns current statistics
func (fd *FrameDistributor) GetStats() DistributorStats {
	lastTime, _ := fd.stats.lastFrameTime.Load().(time.Time)

	return DistributorStats{
		TotalFrames:   fd.stats.totalFrames.Load(),
		DroppedFrames: fd.stats.droppedFrames.Load(),
		WebRTCSent:    fd.stats.webrtcSent.Load(),
		MotionSent:    fd.stats.motionSent.Load(),
		RecordSent:    fd.stats.recordSent.Load(),
		AudioSent:     fd.stats.audioSent.Load(),
		LastFrameTime: lastTime,
	}
}

// GetStream has been removed - no longer needed with GStreamer pipeline
// The GStreamer pipeline now handles encoding and RTP packetization directly

// ============================================================================
// CIRCULAR FRAME BUFFER FOR PRE-MOTION RECORDING
// ============================================================================

// CircularFrameBuffer maintains a ring buffer of recent frames
// Used to capture video from before motion is detected
type CircularFrameBuffer struct {
	buffer     []*ImageFrame
	capacity   int
	writeIndex int
	count      int
	mu         sync.Mutex
}

// NewCircularFrameBuffer creates a new circular buffer with given capacity
func NewCircularFrameBuffer(capacity int) *CircularFrameBuffer {
	return &CircularFrameBuffer{
		buffer:   make([]*ImageFrame, capacity),
		capacity: capacity,
	}
}

// Add inserts a new frame into the buffer
// Oldest frame is overwritten if buffer is full
func (cfb *CircularFrameBuffer) Add(frame *ImageFrame) {
	cfb.mu.Lock()
	defer cfb.mu.Unlock()

	// Store frame at current write position
	cfb.buffer[cfb.writeIndex] = frame

	// Advance write index with wraparound
	cfb.writeIndex = (cfb.writeIndex + 1) % cfb.capacity

	// Track total frames stored (up to capacity)
	if cfb.count < cfb.capacity {
		cfb.count++
	}
}

// GetAll returns all frames in chronological order
// Oldest frames first, newest last
func (cfb *CircularFrameBuffer) GetAll() []*ImageFrame {
	cfb.mu.Lock()
	defer cfb.mu.Unlock()

	if cfb.count == 0 {
		return nil
	}

	result := make([]*ImageFrame, cfb.count)

	if cfb.count < cfb.capacity {
		// Buffer not full yet, return frames 0 to count-1
		copy(result, cfb.buffer[:cfb.count])
	} else {
		// Buffer is full, need to return in chronological order
		// Start from oldest frame (at writeIndex) and wrap around
		for i := 0; i < cfb.capacity; i++ {
			idx := (cfb.writeIndex + i) % cfb.capacity
			result[i] = cfb.buffer[idx]
		}
	}

	return result
}

// GetRecent returns the N most recent frames
func (cfb *CircularFrameBuffer) GetRecent(n int) []*ImageFrame {
	cfb.mu.Lock()
	defer cfb.mu.Unlock()

	if cfb.count == 0 || n <= 0 {
		return nil
	}

	// Limit n to available frames
	if n > cfb.count {
		n = cfb.count
	}

	result := make([]*ImageFrame, n)

	// Walk backwards from most recent write position
	for i := 0; i < n; i++ {
		// Calculate index walking backwards with wraparound
		idx := (cfb.writeIndex - 1 - i + cfb.capacity) % cfb.capacity
		result[n-1-i] = cfb.buffer[idx]  // Reverse to get chronological order
	}

	return result
}

// Clear removes all frames from the buffer
func (cfb *CircularFrameBuffer) Clear() {
	cfb.mu.Lock()
	defer cfb.mu.Unlock()

	// Clear all references to allow GC
	for i := range cfb.buffer {
		cfb.buffer[i] = nil
	}

	cfb.writeIndex = 0
	cfb.count = 0
}

// Size returns the current number of frames in the buffer
func (cfb *CircularFrameBuffer) Size() int {
	cfb.mu.Lock()
	defer cfb.mu.Unlock()
	return cfb.count
}

// Capacity returns the maximum number of frames the buffer can hold
func (cfb *CircularFrameBuffer) Capacity() int {
	return cfb.capacity
}