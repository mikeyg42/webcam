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
)

// ============================================================================
//  FRAME DISTRIBUTOR
// ============================================================================

// FrameDistributor manages a single camera source and distributes frames to multiple consumers
type FrameDistributor struct {
	// Camera configuration
	camera        mediadevices.MediaDeviceInfo
	stream        mediadevices.MediaStream

	// Lifecycle management
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup  // Track goroutines for clean shutdown

	// State management with atomic for race-free checks
	isRunning atomic.Bool

	// Consumer channels
	webrtcChannel chan image.Image  // H.264 for WebRTC streaming
	motionChannel chan image.Image
	recordChannel chan image.Image

	// Pre-motion buffer
	preMotionBuffer *CircularFrameBuffer

	// Statistics with atomic counters for lock-free updates
	stats struct {
		totalFrames    atomic.Int64
		droppedFrames  atomic.Int64
		webrtcSent     atomic.Int64  // H.264 frames sent to WebRTC
		motionSent     atomic.Int64
		recordSent     atomic.Int64
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
	LastFrameTime time.Time
}

// ImageFrame wraps an image with metadata for buffering and tracking
type ImageFrame struct {
	Image     image.Image
	Timestamp time.Time
	Sequence  int64
}

// NewFrameDistributor creates a new single-source frame distributor
func NewFrameDistributor(ctx context.Context, camera mediadevices.MediaDeviceInfo) (*FrameDistributor, error) {

	fdCtx, cancel := context.WithCancel(ctx)

	// Pre-motion buffer: 5 seconds at 15fps = 75 frames
	preMotionBuffer := NewCircularFrameBuffer(75)

	fd := &FrameDistributor{
		camera:          camera,
		ctx:             fdCtx,
		cancel:          cancel,
		webrtcChannel:   make(chan image.Image, 10),  // H.264 for WebRTC
		motionChannel:   make(chan image.Image, 5),
		recordChannel:   make(chan image.Image, 30),
		preMotionBuffer: preMotionBuffer,
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

// sendToConsumers distributes frame to all consumer channels
func (fd *FrameDistributor) sendToConsumers(img image.Image) {
	// H.264 WebRTC encoder channel (non-blocking)
	select {
	case fd.webrtcChannel <- img:
		fd.stats.webrtcSent.Add(1)
	default:
		fd.stats.droppedFrames.Add(1)
		// Only log every 100th dropped frame to avoid spam
		if fd.stats.droppedFrames.Load()%100 == 0 {
			log.Printf("[FrameDistributor] VP9 channel full, total dropped: %d",
				fd.stats.droppedFrames.Load())
		}
	}

	// Motion detection channel
	select {
	case fd.motionChannel <- img:
		fd.stats.motionSent.Add(1)
	default:
		fd.stats.droppedFrames.Add(1)
	}

	// Recording channel
	select {
	case fd.recordChannel <- img:
		fd.stats.recordSent.Add(1)
	default:
		fd.stats.droppedFrames.Add(1)
	}
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

	// Close channels (safe because distributeFrames has exited)
	close(fd.webrtcChannel)
	close(fd.motionChannel)
	close(fd.recordChannel)

	// Clear pre-motion buffer
	fd.preMotionBuffer.Clear()

	log.Printf("[FrameDistributor] Cleanup completed")
}

// GetWebRTCChannel returns the channel for H.264 WebRTC encoder frames
func (fd *FrameDistributor) GetWebRTCChannel() <-chan image.Image {
	return fd.webrtcChannel
}

// GetMotionChannel returns the channel for motion detection frames
func (fd *FrameDistributor) GetMotionChannel() <-chan image.Image {
	return fd.motionChannel
}

// GetRecordChannel returns the channel for recording frames
func (fd *FrameDistributor) GetRecordChannel() <-chan image.Image {
	return fd.recordChannel
}

// GetPreMotionFrames returns buffered frames from before motion was detected
func (fd *FrameDistributor) GetPreMotionFrames() []*ImageFrame {
	return fd.preMotionBuffer.GetAll()
}

// IsRunning returns whether the distributor is currently running
func (fd *FrameDistributor) IsRunning() bool {
	return fd.isRunning.Load()
}

// GetStats returns current statistics
func (fd *FrameDistributor) GetStats() DistributorStats {
	lastTime, _ := fd.stats.lastFrameTime.Load().(time.Time)

	return DistributorStats{
		TotalFrames:   fd.stats.totalFrames.Load(),
		DroppedFrames: fd.stats.droppedFrames.Load(),
		WebRTCSent:   fd.stats.webrtcSent.Load(),
		MotionSent:   fd.stats.motionSent.Load(),
		RecordSent:   fd.stats.recordSent.Load(),
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