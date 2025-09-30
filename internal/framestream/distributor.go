package framestream

import (
	"context"
	"fmt"
	"image"
	"log"
	"sync"
	"time"

	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/prop"
)

// ============================================================================
// MAIN FRAME DISTRIBUTOR
// ============================================================================

// FrameDistributor manages a single camera source and distributes frames to multiple consumers
// This solves the "driver is already opened" issue by having only ONE camera access point
type FrameDistributor struct {
	// Camera configuration
	camera        mediadevices.MediaDeviceInfo
	codecSelector *mediadevices.CodecSelector
	stream        mediadevices.MediaStream
	
	// Lifecycle management
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	isRunning     bool

	// Consumer channels - buffered for smooth operation
	vp9Channel    chan image.Image  // Raw frames for VP9 encoder (WebRTC)
	motionChannel chan image.Image  // Raw frames for motion detection  
	recordChannel chan image.Image  // Raw frames for recording

	// Pre-motion buffer for recording X seconds before motion detected
	// This allows capturing the events leading up to motion
	preMotionBuffer *CircularFrameBuffer
	
	// Statistics tracking
	stats         DistributorStats
	statsMu       sync.RWMutex  // Separate mutex for stats to reduce contention
}

// DistributorStats tracks frame distribution performance metrics
type DistributorStats struct {
	TotalFrames      int64
	DroppedFrames    int64
	ProcessingTime   time.Duration
	LastFrameTime    time.Time
	VP9Sent         int64  // Frames sent to VP9 channel
	MotionSent      int64  // Frames sent to motion channel
	RecordSent      int64  // Frames sent to record channel
}

// ImageFrame wraps an image with metadata for buffering and tracking
type ImageFrame struct {
	Image     image.Image
	Timestamp time.Time
	Sequence  int64  // Frame sequence number for debugging
}

// NewFrameDistributor creates a new single-source frame distributor
func NewFrameDistributor(ctx context.Context, camera mediadevices.MediaDeviceInfo, 
	codecSelector *mediadevices.CodecSelector) (*FrameDistributor, error) {
	
	fdCtx, cancel := context.WithCancel(ctx)

	// Create pre-motion buffer to store last 5 seconds of frames (at 15fps = 75 frames)
	preMotionBuffer := NewCircularFrameBuffer(75)

	return &FrameDistributor{
		camera:        camera,
		codecSelector: codecSelector,
		ctx:           fdCtx,
		cancel:        cancel,

		// Channel sizes tuned for typical processing speeds
		vp9Channel:      make(chan image.Image, 10),   // VP9 encoding is usually fast
		motionChannel:   make(chan image.Image, 5),    // Motion detection is fast
		recordChannel:   make(chan image.Image, 30),   // Recording may buffer more
		preMotionBuffer: preMotionBuffer,
		stats:          DistributorStats{},
	}, nil
}

// Start begins frame capture and distribution from the single camera source
func (fd *FrameDistributor) Start(width, height int) error {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	if fd.isRunning {
		log.Printf("[FrameDistributor] Already running, ignoring start request")
		return nil
	}

	log.Printf("[FrameDistributor] Starting single camera capture at %dx%d", width, height)

	// Configure camera constraints - SINGLE point of camera access
	constraints := mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(fd.camera.DeviceID)
			c.Width = prop.IntExact(width)
			c.Height = prop.IntExact(height)
			c.FrameRate = prop.FloatExact(15)  // 15fps for stability and performance
		},
		Codec: fd.codecSelector,
	}

	// Create the SINGLE media stream - no competing GetUserMedia calls
	stream, err := mediadevices.GetUserMedia(constraints)
	if err != nil {
		return fmt.Errorf("failed to get user media: %v", err)
	}
	fd.stream = stream

	// Start frame processing in background
	go fd.distributeFrames()

	fd.isRunning = true
	log.Printf("[FrameDistributor] Started successfully - single camera source active")
	return nil
}

// distributeFrames continuously reads from camera and fans out to consumers
func (fd *FrameDistributor) distributeFrames() {
	defer fd.cleanup()

	// Get video track from stream
	videoTracks := fd.stream.GetVideoTracks()
	if len(videoTracks) == 0 {
		log.Printf("[FrameDistributor] ERROR: No video tracks available")
		return
	}

	track := videoTracks[0]
	log.Printf("[FrameDistributor] Processing frames from track: %s", track.ID())

	// Cast to mediadevices VideoTrack for raw frame access
	videoTrack, ok := track.(*mediadevices.VideoTrack)
	if !ok {
		log.Printf("[FrameDistributor] ERROR: Track is not a VideoTrack: %T", track)
		return
	}

	// Create reader for raw frames (false = don't copy frames)
	videoReader := videoTrack.NewReader(false)
	
	// Frame sequence counter for debugging
	var frameSequence int64

	// Main processing loop
	for {
		select {
		case <-fd.ctx.Done():
			log.Printf("[FrameDistributor] Stopping due to context cancellation")
			return
			
		default:
			// Read next frame from camera
			img, release, err := videoReader.Read()
			if err != nil {
				log.Printf("[FrameDistributor] Error reading frame: %v", err)
				time.Sleep(10 * time.Millisecond)  // Brief pause before retry
				continue
			}

			// Process and distribute the frame
			frameSequence++
			fd.processFrame(img, release, frameSequence)
		}
	}
}

// processFrame handles a single frame from the camera
func (fd *FrameDistributor) processFrame(img image.Image, release func(), sequence int64) {
	// Always release the frame when done
	defer func() {
		if release != nil {
			release()
		}
	}()

	if img == nil {
		return
	}

	startTime := time.Now()
	
	// Store frame in pre-motion buffer for recording
	// This allows us to capture events leading up to motion detection
	fd.storeInPreMotionBuffer(img, sequence)

	// Fan out to all consumers (non-blocking with statistics)
	fd.sendToConsumers(img)

	// Update performance statistics
	fd.updateStats(startTime)
}

// storeInPreMotionBuffer saves frame for pre-motion recording capability
func (fd *FrameDistributor) storeInPreMotionBuffer(img image.Image, sequence int64) {
	// Clone image since original will be released
	// Use simple type assertion for common cases to avoid full copy
	var cloned image.Image
	
	switch src := img.(type) {
	case *image.RGBA:
		// Fast clone for RGBA
		dst := &image.RGBA{
			Pix:    make([]byte, len(src.Pix)),
			Stride: src.Stride,
			Rect:   src.Rect,
		}
		copy(dst.Pix, src.Pix)
		cloned = dst
		
	case *image.YCbCr:
		// Fast clone for YCbCr
		dst := &image.YCbCr{
			Y:              make([]byte, len(src.Y)),
			Cb:             make([]byte, len(src.Cb)),
			Cr:             make([]byte, len(src.Cr)),
			YStride:        src.YStride,
			CStride:        src.CStride,
			SubsampleRatio: src.SubsampleRatio,
			Rect:          src.Rect,
		}
		copy(dst.Y, src.Y)
		copy(dst.Cb, src.Cb)
		copy(dst.Cr, src.Cr)
		cloned = dst
		
	default:
		// Fallback: draw to new RGBA image
		bounds := img.Bounds()
		rgba := image.NewRGBA(bounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				rgba.Set(x, y, img.At(x, y))
			}
		}
		cloned = rgba
	}

	frame := &ImageFrame{
		Image:     cloned,
		Timestamp: time.Now(),
		Sequence:  sequence,
	}
	
	fd.preMotionBuffer.Add(frame)
}

// sendToConsumers distributes frame to all consumer channels
func (fd *FrameDistributor) sendToConsumers(img image.Image) {
	// Try to send to each consumer channel
	// Non-blocking sends with drop policy if channel is full
	
	// VP9 encoder channel
	select {
	case fd.vp9Channel <- img:
		fd.incrementStat(func(s *DistributorStats) { s.VP9Sent++ })
	default:
		fd.incrementStat(func(s *DistributorStats) { s.DroppedFrames++ })
		// Only log every 100th dropped frame to avoid spam
		if fd.getDroppedCount()%100 == 0 {
			log.Printf("[FrameDistributor] VP9 channel full, total dropped: %d", fd.getDroppedCount())
		}
	}

	// Motion detection channel  
	select {
	case fd.motionChannel <- img:
		fd.incrementStat(func(s *DistributorStats) { s.MotionSent++ })
	default:
		fd.incrementStat(func(s *DistributorStats) { s.DroppedFrames++ })
	}

	// Recording channel
	select {
	case fd.recordChannel <- img:
		fd.incrementStat(func(s *DistributorStats) { s.RecordSent++ })
	default:
		fd.incrementStat(func(s *DistributorStats) { s.DroppedFrames++ })
	}
}

// updateStats updates performance metrics
func (fd *FrameDistributor) updateStats(startTime time.Time) {
	fd.statsMu.Lock()
	defer fd.statsMu.Unlock()
	
	fd.stats.TotalFrames++
	fd.stats.ProcessingTime = time.Since(startTime)
	fd.stats.LastFrameTime = time.Now()
}

// incrementStat safely updates a statistic
func (fd *FrameDistributor) incrementStat(updateFunc func(*DistributorStats)) {
	fd.statsMu.Lock()
	defer fd.statsMu.Unlock()
	updateFunc(&fd.stats)
}

// getDroppedCount returns the current dropped frame count
func (fd *FrameDistributor) getDroppedCount() int64 {
	fd.statsMu.RLock()
	defer fd.statsMu.RUnlock()
	return fd.stats.DroppedFrames
}

// GetVP9Channel returns the channel for VP9 encoder frames
func (fd *FrameDistributor) GetVP9Channel() <-chan image.Image {
	return fd.vp9Channel
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
// This enables recording video from before the motion trigger
func (fd *FrameDistributor) GetPreMotionFrames() []*ImageFrame {
	return fd.preMotionBuffer.GetAll()
}

// Stop gracefully stops the frame distributor
func (fd *FrameDistributor) Stop() {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	if !fd.isRunning {
		return
	}

	log.Printf("[FrameDistributor] Stopping...")
	fd.cancel()
	fd.isRunning = false
}

// cleanup releases all resources
func (fd *FrameDistributor) cleanup() {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	log.Printf("[FrameDistributor] Cleaning up resources...")

	// Close all consumer channels
	close(fd.vp9Channel)
	close(fd.motionChannel)
	close(fd.recordChannel)

	// Close media stream and tracks
	if fd.stream != nil {
		for _, track := range fd.stream.GetTracks() {
			track.Close()
		}
	}

	// Clear pre-motion buffer
	fd.preMotionBuffer.Clear()

	log.Printf("[FrameDistributor] Cleanup completed")
}

// GetStats returns a copy of current statistics
func (fd *FrameDistributor) GetStats() DistributorStats {
	fd.statsMu.RLock()
	defer fd.statsMu.RUnlock()
	return fd.stats
}

// IsRunning returns whether the distributor is currently active
func (fd *FrameDistributor) IsRunning() bool {
	fd.mu.RLock()
	defer fd.mu.RUnlock()
	return fd.isRunning
}

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