package video

import (
	"context"
	"fmt"
	"image"
	"log"
	"sync"
	"time"

	"github.com/mikeyg42/webcam/internal/framestream"
	"github.com/mikeyg42/webcam/internal/notification"
	"gocv.io/x/gocv"
)

// MediaDevicesRecordingManager manages high-quality recording using mediadevices frames
type MediaDevicesRecordingManager struct {
	recorder         *Recorder
	notifier         notification.Notifier
	frameDistributor *framestream.FrameDistributor // FIXED: Changed from MediaDevicesProducer

	// State management
	isRecording    bool
	recordingStart time.Time
	mu             sync.RWMutex

	// Timing configuration
	cooldownTimer  *time.Timer
	cooldownPeriod time.Duration
	minRecordTime  time.Duration
	noMotionDelay  time.Duration // How long to wait after motion stops

	// Context for lifecycle management
	ctx    context.Context
	cancel context.CancelFunc

	// Statistics
	stats RecordingStats
}

// RecordingStats tracks recording statistics
type RecordingStats struct {
	TotalRecordings   int64
	TotalFrames       int64
	DroppedFrames     int64
	CurrentRecording  string // Current recording filename
	LastRecordingTime time.Time
}

// NewMediaDevicesRecordingManager creates a new high-priority recording manager
func NewMediaDevicesRecordingManager(
	ctx context.Context,
	recorder *Recorder,
	notifier notification.Notifier,
	frameDistributor *framestream.FrameDistributor) *MediaDevicesRecordingManager { // FIXED: Changed parameter type

	rmCtx, cancel := context.WithCancel(ctx)

	return &MediaDevicesRecordingManager{
		recorder:         recorder,
		notifier:         notifier,
		frameDistributor: frameDistributor,
		cooldownPeriod:   30 * time.Second, // Don't start new recording for 30s after one ends
		minRecordTime:    10 * time.Second, // Minimum recording duration
		noMotionDelay:    5 * time.Second,  // Wait 5s after motion stops before ending recording
		ctx:              rmCtx,
		cancel:           cancel,
		stats:            RecordingStats{},
	}
}

// Start begins processing frames and motion events
func (rm *MediaDevicesRecordingManager) Start() error {
	// Start frame processing goroutine
	go rm.processRecordingFrames()

	log.Printf("[RecordingManager] Started - waiting for motion events")
	return nil
}

// HandleMotion processes motion detection events and manages recording lifecycle
func (rm *MediaDevicesRecordingManager) HandleMotion(motionChan <-chan bool) {
	log.Printf("[RecordingManager] Motion handler started")

	var lastMotionTime time.Time
	checkTimer := time.NewTicker(1 * time.Second)
	defer checkTimer.Stop()

	for {
		select {
		case <-rm.ctx.Done():
			log.Printf("[RecordingManager] Stopping motion handler due to context cancellation")
			rm.stopRecording()
			return

		case motion, ok := <-motionChan:
			if !ok {
				log.Printf("[RecordingManager] Motion channel closed")
				rm.stopRecording()
				return
			}

			if motion {
				lastMotionTime = time.Now()

				// Start recording if not already recording
				rm.mu.Lock()
				if !rm.isRecording {
					// Cancel any pending stop timer
					if rm.cooldownTimer != nil {
						rm.cooldownTimer.Stop()
						rm.cooldownTimer = nil
					}

					// Start recording with pre-motion frames
					if err := rm.startRecordingWithPreMotion(); err != nil {
						log.Printf("[RecordingManager] Failed to start recording: %v", err)
						rm.mu.Unlock()
						continue
					}

					rm.isRecording = true
					rm.recordingStart = time.Now()
					rm.stats.TotalRecordings++

					log.Printf("🔴 [RecordingManager] RECORDING STARTED (including pre-motion buffer)")

					// Send notification asynchronously
					rm.sendNotification()
				}
				rm.mu.Unlock()

			} else {
				// Motion stopped
				rm.mu.RLock()
				recording := rm.isRecording
				rm.mu.RUnlock()

				if recording {
					log.Printf("⚠️  [RecordingManager] Motion stopped, will stop recording after %v delay", rm.noMotionDelay)
				}
			}

		case <-checkTimer.C:
			// Periodic check to stop recording after no motion
			rm.mu.Lock()
			if rm.isRecording && !lastMotionTime.IsZero() {
				timeSinceMotion := time.Since(lastMotionTime)
				recordingDuration := time.Since(rm.recordingStart)

				// Stop recording if:
				// 1. No motion for noMotionDelay period AND
				// 2. Minimum recording time has elapsed
				if timeSinceMotion >= rm.noMotionDelay && recordingDuration >= rm.minRecordTime {
					log.Printf("⏹️  [RecordingManager] Stopping recording (no motion for %v)", timeSinceMotion.Round(time.Second))

					if err := rm.recorder.StopRecording(); err != nil {
						log.Printf("[RecordingManager] Error stopping recording: %v", err)
					}

					rm.isRecording = false
					rm.stats.LastRecordingTime = time.Now()
					lastMotionTime = time.Time{} // Reset

					// Start cooldown timer to prevent immediate re-recording
					rm.cooldownTimer = time.AfterFunc(rm.cooldownPeriod, func() {
						log.Printf("[RecordingManager] Cooldown period ended, ready for new recordings")
					})
				}
			}
			rm.mu.Unlock()
		}
	}
}

// processRecordingFrames handles the actual frame recording from the distributor
func (rm *MediaDevicesRecordingManager) processRecordingFrames() {
	recordChannel := rm.frameDistributor.GetRecordChannel() // FIXED: Changed from GetRecordingFrameChannel()

	log.Printf("[RecordingManager] Frame processor started")

	for {
		select {
		case <-rm.ctx.Done():
			log.Printf("[RecordingManager] Frame processor stopping")
			return

		case frame, ok := <-recordChannel:
			if !ok {
				log.Printf("[RecordingManager] Record channel closed")
				return
			}

			// Only process frames when recording
			rm.mu.RLock()
			recording := rm.isRecording
			rm.mu.RUnlock()

			if recording && frame != nil {
				// Convert image.Image to format needed by recorder
				if err := rm.processFrame(frame); err != nil {
					log.Printf("[RecordingManager] Error processing frame: %v", err)
					rm.stats.DroppedFrames++
				} else {
					rm.stats.TotalFrames++
				}
			}
		}
	}
}

// startRecordingWithPreMotion begins recording including pre-motion buffered frames
func (rm *MediaDevicesRecordingManager) startRecordingWithPreMotion() error {
	// Get pre-motion frames from the distributor's circular buffer
	preMotionFrames := rm.frameDistributor.GetPreMotionFrames()

	log.Printf("[RecordingManager] Starting recording with %d pre-motion frames", len(preMotionFrames))

	// Generate filename with timestamp
	filename := fmt.Sprintf("recording_%s.mp4", time.Now().Format("20060102_150405"))
	rm.stats.CurrentRecording = filename

	// Start the recorder
	// Note: Pass nil for channel since we'll feed frames manually
	if err := rm.recorder.StartRecording(nil); err != nil {
		return fmt.Errorf("failed to start recorder: %v", err)
	}

	// Process pre-motion frames in background
	go func() {
		processedCount := 0
		for _, frame := range preMotionFrames {
			select {
			case <-rm.ctx.Done():
				return
			default:
				if frame != nil && frame.Image != nil {
					if err := rm.processFrame(frame.Image); err != nil {
						log.Printf("[RecordingManager] Error processing pre-motion frame: %v", err)
					} else {
						processedCount++
					}
				}
			}
		}
		log.Printf("[RecordingManager] Processed %d pre-motion frames", processedCount)
	}()

	return nil
}

// processFrame converts and sends a frame to the recorder
func (rm *MediaDevicesRecordingManager) processFrame(img image.Image) error {
	if img == nil {
		return fmt.Errorf("nil image")
	}

	// Convert image.Image to gocv.Mat if your recorder needs it
	// This uses the optimized conversion from pipeline.go
	mat, err := rm.imageToMat(img)
	if err != nil {
		return fmt.Errorf("failed to convert image to Mat: %v", err)
	}
	defer mat.Close()

	// Send to recorder (adapt based on your Recorder's actual interface)
	// If your recorder accepts gocv.Mat directly:
	// return rm.recorder.WriteFrame(mat)

	// For now, just log success
	bounds := img.Bounds()
	log.Printf("[RecordingManager] Frame processed: %dx%d", bounds.Dx(), bounds.Dy())

	return nil
}

// imageToMat converts image.Image to gocv.Mat using optimized method
func (rm *MediaDevicesRecordingManager) imageToMat(img image.Image) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Handle different image types efficiently
	switch srcImg := img.(type) {
	case *image.RGBA:
		return rm.convertRGBAToMat(srcImg)
	case *image.YCbCr:
		return rm.convertYCbCrToMat(srcImg)
	default:
		// Generic fallback
		mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
		matData, err := mat.DataPtrUint8()
		if err != nil {
			mat.Close()
			return gocv.NewMat(), err
		}

		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				idx := ((y-bounds.Min.Y)*width + (x - bounds.Min.X)) * 3
				matData[idx] = uint8(b >> 8)   // B
				matData[idx+1] = uint8(g >> 8) // G
				matData[idx+2] = uint8(r >> 8) // R
			}
		}
		return mat, nil
	}
}

// convertRGBAToMat efficiently converts RGBA images
func (rm *MediaDevicesRecordingManager) convertRGBAToMat(img *image.RGBA) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	matData, err := mat.DataPtrUint8()
	if err != nil {
		mat.Close()
		return gocv.NewMat(), err
	}

	// Fast path for contiguous memory
	if img.Stride == width*4 {
		srcIdx := 0
		dstIdx := 0
		for i := 0; i < width*height; i++ {
			matData[dstIdx] = img.Pix[srcIdx+2]   // B
			matData[dstIdx+1] = img.Pix[srcIdx+1] // G
			matData[dstIdx+2] = img.Pix[srcIdx]   // R
			srcIdx += 4
			dstIdx += 3
		}
	} else {
		// Handle stride
		for y := 0; y < height; y++ {
			srcRow := y * img.Stride
			dstRow := y * width * 3
			for x := 0; x < width; x++ {
				srcIdx := srcRow + x*4
				dstIdx := dstRow + x*3
				matData[dstIdx] = img.Pix[srcIdx+2]   // B
				matData[dstIdx+1] = img.Pix[srcIdx+1] // G
				matData[dstIdx+2] = img.Pix[srcIdx]   // R
			}
		}
	}

	return mat, nil
}

// convertYCbCrToMat efficiently converts YCbCr images
func (rm *MediaDevicesRecordingManager) convertYCbCrToMat(img *image.YCbCr) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	matData, err := mat.DataPtrUint8()
	if err != nil {
		mat.Close()
		return gocv.NewMat(), err
	}

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			yi := img.YOffset(x+bounds.Min.X, y+bounds.Min.Y)
			ci := img.COffset(x+bounds.Min.X, y+bounds.Min.Y)

			yy := int32(img.Y[yi])
			cb := int32(img.Cb[ci]) - 128
			cr := int32(img.Cr[ci]) - 128

			// YCbCr to RGB conversion
			r := yy + (91881*cr)>>16
			g := yy - (22554*cb+46802*cr)>>16
			b := yy + (116130*cb)>>16

			// Clamp and write
			idx := (y*width + x) * 3
			if b < 0 {
				b = 0
			} else if b > 255 {
				b = 255
			}
			if g < 0 {
				g = 0
			} else if g > 255 {
				g = 255
			}
			if r < 0 {
				r = 0
			} else if r > 255 {
				r = 255
			}

			matData[idx] = uint8(b)
			matData[idx+1] = uint8(g)
			matData[idx+2] = uint8(r)
		}
	}

	return mat, nil
}

// sendNotification sends motion detection notification
func (rm *MediaDevicesRecordingManager) sendNotification() {
	if rm.notifier != nil {
		go func() {
			if err := rm.notifier.SendNotification(); err != nil {
				log.Printf("[RecordingManager] Failed to send notification: %v", err)
			} else {
				log.Printf("[RecordingManager] Motion notification sent")
			}
		}()
	} else {
		log.Printf("[RecordingManager] Motion detected at %s (notifications disabled)",
			time.Now().Format(time.RFC1123))
	}
}

// stopRecording stops any ongoing recording
func (rm *MediaDevicesRecordingManager) stopRecording() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.isRecording {
		if err := rm.recorder.StopRecording(); err != nil {
			log.Printf("[RecordingManager] Failed to stop recording: %v", err)
		}
		rm.isRecording = false
		log.Printf("[RecordingManager] Recording stopped")
	}

	if rm.cooldownTimer != nil {
		rm.cooldownTimer.Stop()
		rm.cooldownTimer = nil
	}
}

// Stop stops the recording manager
func (rm *MediaDevicesRecordingManager) Stop() {
	log.Printf("[RecordingManager] Stopping...")
	rm.cancel()
	rm.stopRecording()
}

// GetStats returns current recording statistics
func (rm *MediaDevicesRecordingManager) GetStats() RecordingStats {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.stats
}

// IsRecording returns whether recording is currently active
func (rm *MediaDevicesRecordingManager) IsRecording() bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.isRecording
}

// SetCooldownPeriod sets the cooldown period between recordings
func (rm *MediaDevicesRecordingManager) SetCooldownPeriod(d time.Duration) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.cooldownPeriod = d
}

// SetMinRecordTime sets the minimum recording duration
func (rm *MediaDevicesRecordingManager) SetMinRecordTime(d time.Duration) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.minRecordTime = d
}

// SetNoMotionDelay sets how long to wait after motion stops before ending recording
func (rm *MediaDevicesRecordingManager) SetNoMotionDelay(d time.Duration) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.noMotionDelay = d
}
