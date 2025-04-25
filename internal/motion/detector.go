package motion

import (
	"fmt"
	"image"
	"log"
	"sync"
	"time"

	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/config"
)

// Detector handles motion detection using background subtraction
type Detector struct {
	config       *config.MotionConfig
	notifier     Notifier
	mog2         *gocv.BackgroundSubtractorMOG2
	mu           sync.Mutex
	isRunning    bool
	frameCount   int
	stats        MotionStats
	motionBuffer []bool // Add this: circular buffer for motion detection
	bufferIndex  int    // Add this: current position in circular buffer
}

// Notifier interface for sending notifications
type Notifier interface {
	SendNotification() error
}

// Add this to the Config struct

type MotionStats struct {
	FramesProcessed   int64
	MotionEvents      int64
	LastMotionTime    time.Time
	AverageMotionArea float64
	MaxMotionArea     float64 // Add this
	MinMotionArea     float64 // Add this
	FalsePositives    int64
	ProcessingTime    time.Duration
	LastProcessedTime time.Time // Add this
}

func (d *Detector) GetStats() MotionStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}

// Send notification asynchronously - only if notifier is available
func (d *Detector) sendNotification() {
	if d.notifier == nil {
		log.Println("Motion detected (notifications disabled)")
		return
	}

	go func() {
		if err := d.notifier.SendNotification(); err != nil {
			log.Printf("Failed to send notification: %v", err)
		}
	}()
}

// Detect processes incoming frames and detects motion
func (d *Detector) Detect(frameChan <-chan gocv.Mat, motionChan chan<- bool) {
	defer close(motionChan)

	lastNotification := time.Time{}
	currentlyDetectingMotion := false
	lastMotionTime := time.Time{}

	// Timer for tracking no-motion period
	noMotionTimer := time.NewTimer(0)
	if !noMotionTimer.Stop() {
		<-noMotionTimer.C
	}

	for frame := range frameChan {
		d.frameCount++
		if d.frameCount%d.config.FrameSkip != 0 {
			frame.Close()
			continue
		}

		motion, err := d.processFrame(frame)
		frame.Close()

		if err != nil {
			log.Printf("Error processing frame: %v", err)
			continue
		}

		// Handle motion state changes with cooldown
		if motion {
			// Reset or stop the no-motion timer if it's running
			if !noMotionTimer.Stop() {
				select {
				case <-noMotionTimer.C:
				default:
				}
			}

			lastMotionTime = time.Now()

			if !currentlyDetectingMotion && time.Since(lastNotification) > d.config.CooldownPeriod {
				currentlyDetectingMotion = true
				lastNotification = time.Now()
				motionChan <- true

				// Use the helper method instead of direct notification
				d.sendNotification()
			}
		} else if currentlyDetectingMotion {
			// If no motion detected and we're currently in motion detection state
			if time.Since(lastMotionTime) >= d.config.NoMotionDelay {
				// If the timer isn't already running, start it
				if !noMotionTimer.Stop() {
					select {
					case <-noMotionTimer.C:
					default:
					}
				}
				noMotionTimer.Reset(d.config.NoMotionDelay)

				// Wait for either more motion or the timer to expire
				select {
				case <-noMotionTimer.C:
					// No motion detected for the full delay period
					currentlyDetectingMotion = false
					motionChan <- false
					log.Printf("No motion detected for %v, stopping detection", d.config.NoMotionDelay)
				default:
					// Continue monitoring
				}
			}
		}
	}

	// Cleanup
	noMotionTimer.Stop()
}

func NewDetector(config *config.MotionConfig, notifier Notifier) (*Detector, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create background subtractor
	mog2 := gocv.NewBackgroundSubtractorMOG2()

	return &Detector{
		config:       config,
		notifier:     notifier, // Can be nil
		mog2:         &mog2,
		stats:        MotionStats{},
		motionBuffer: make([]bool, config.MaxConsecutiveFrames),
	}, nil
}

func (d *Detector) processFrame(frame gocv.Mat) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	start := time.Now()
	defer func() {
		d.stats.ProcessingTime = time.Since(start)
	}()

	if !d.isRunning {
		return false, nil
	}

	// Convert to grayscale if needed
	var gray gocv.Mat
	if frame.Channels() > 1 {
		gray = gocv.NewMat()
		defer gray.Close()
		gocv.CvtColor(frame, &gray, gocv.ColorBGRToGray)
	} else {
		gray = frame
	}

	// Apply Gaussian blur to reduce noise
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{X: d.config.BlurSize, Y: d.config.BlurSize}, 0, 0, gocv.BorderDefault)

	// Create foreground mask using MOG2
	fgMask := gocv.NewMat()
	defer fgMask.Close()
	d.mog2.Apply(blurred, &fgMask)

	// Apply threshold
	thresh := gocv.NewMat()
	defer thresh.Close()
	gocv.Threshold(fgMask, &thresh, float32(d.config.Threshold), 255, gocv.ThresholdBinary)

	// Apply morphological operations
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{X: d.config.DilationSize, Y: d.config.DilationSize})
	defer kernel.Close()

	dilated := gocv.NewMat()
	defer dilated.Close()
	gocv.Dilate(thresh, &dilated, kernel)

	// Find contours
	contours := gocv.FindContours(dilated, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	// Analyze contours
	var totalArea float64
	motionDetected := false
	for i := 0; i < contours.Size(); i++ {
		area := gocv.ContourArea(contours.At(i))
		totalArea += area
		if area >= float64(d.config.MinimumArea) {
			motionDetected = true
		}
	}

	// Update stats
	d.stats.FramesProcessed++
	if motionDetected {
		d.stats.MotionEvents++
		d.stats.LastMotionTime = time.Now()
		d.stats.AverageMotionArea = (d.stats.AverageMotionArea*float64(d.stats.MotionEvents-1) + totalArea) / float64(d.stats.MotionEvents)

		// Update min/max areas
		if totalArea > d.stats.MaxMotionArea {
			d.stats.MaxMotionArea = totalArea
		}
		if d.stats.MinMotionArea == 0 || totalArea < d.stats.MinMotionArea {
			d.stats.MinMotionArea = totalArea
		}
	}

	d.stats.LastProcessedTime = time.Now()

	d.motionBuffer = append(d.motionBuffer, motionDetected)
	if len(d.motionBuffer) > d.config.MaxConsecutiveFrames {
		d.motionBuffer = d.motionBuffer[1:]
	}

	// Count consecutive motion frames
	consecutiveFrames := 0
	for i := len(d.motionBuffer) - 1; i >= 0; i-- {
		if !d.motionBuffer[i] {
			break
		}
		consecutiveFrames++
	}

	// Return true only if we have enough consecutive frames with motion
	return d.updateMotionBuffer(motionDetected), nil
}

func (d *Detector) updateMotionBuffer(motion bool) bool {
	// Add current motion state to buffer
	d.motionBuffer[d.bufferIndex] = motion
	d.bufferIndex = (d.bufferIndex + 1) % len(d.motionBuffer)

	// Count consecutive motion frames
	consecutiveFrames := 0
	for i := 0; i < len(d.motionBuffer); i++ {
		if d.motionBuffer[i] {
			consecutiveFrames++
		}
	}

	// Check if we have enough consecutive frames
	return consecutiveFrames >= d.config.MinConsecutiveFrames
}

func (d *Detector) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isRunning {
		return nil
	}

	// Reset stats and counters
	d.stats = MotionStats{}
	d.frameCount = 0
	d.bufferIndex = 0
	for i := range d.motionBuffer {
		d.motionBuffer[i] = false
	}
	d.isRunning = true
	return nil
}

func (d *Detector) ResetStats() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stats = MotionStats{}
	d.frameCount = 0
}

// Stop halts motion detection
func (d *Detector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isRunning {
		return nil
	}

	d.isRunning = false
	return nil
}

// Close releases resources
func (d *Detector) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.Stop(); err != nil {
		return fmt.Errorf("error stopping detector: %v", err)
	}

	if d.mog2 != nil {
		d.mog2.Close()
		d.mog2 = nil
	}

	// Clear stats
	d.stats = MotionStats{}
	d.frameCount = 0
	return nil
}

// IsRunning returns the current detection status
func (d *Detector) IsRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isRunning
}
