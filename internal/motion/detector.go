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
	config     *config.MotionConfig
	notifier   Notifier
	mog2       *gocv.BackgroundSubtractorMOG2
	mu         sync.Mutex
	isRunning  bool
	frameCount int
}

// Notifier interface for sending notifications
type Notifier interface {
	SendNotification() error
}

// Add this to the Config struct
type Config struct {
	MinimumArea    int
	FrameSkip      int
	LearningRate   float64
	Threshold      float32
	BlurSize       int
	DilationSize   int
	CooldownPeriod time.Duration
	NoMotionDelay  time.Duration // Duration to wait before declaring no motion (e.g., 10 seconds)
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

				// Send notification asynchronously
				go func() {
					if err := d.notifier.SendNotification(); err != nil {
						log.Printf("Failed to send notification: %v", err)
					}
				}()
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
	if notifier == nil {
		return nil, fmt.Errorf("notifier cannot be nil")
	}

	// Create background subtractor
	mog2 := gocv.NewBackgroundSubtractorMOG2()

	return &Detector{
		config:   config,
		notifier: notifier,
		mog2:     &mog2,
	}, nil
}

// processFrame analyzes a single frame for motion
func (d *Detector) processFrame(frame gocv.Mat) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isRunning {
		return false, nil
	}

	// Create foreground mask using MOG2
	fgMask := gocv.NewMat()
	defer fgMask.Close()

	// Apply MOG2 background subtraction
	d.mog2.Apply(frame, &fgMask)

	// Apply threshold to clean up the mask
	thresh := gocv.NewMat()
	defer thresh.Close()
	gocv.Threshold(fgMask, &thresh, d.config.Threshold, 255, gocv.ThresholdBinary)

	// Apply dilation to connect nearby motion areas
	kernel := gocv.GetStructuringElement(gocv.MorphRect,
		image.Point{X: d.config.DilationSize, Y: d.config.DilationSize})
	defer kernel.Close()

	dilated := gocv.NewMat()
	defer dilated.Close()
	gocv.Dilate(thresh, &dilated, kernel)

	// Find contours
	contours := gocv.FindContours(dilated, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	// Check for significant motion
	for i := 0; i < contours.Size(); i++ {
		area := gocv.ContourArea(contours.At(i))
		if area >= float64(d.config.MinimumArea) {
			return true, nil
		}
	}

	return false, nil
}

// Start begins motion detection
func (d *Detector) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isRunning {
		return nil
	}

	d.isRunning = true
	d.frameCount = 0
	return nil
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
	d.Stop()
	if d.mog2 != nil {
		d.mog2.Close()
		d.mog2 = nil
	}
	return nil
}

// IsRunning returns the current detection status
func (d *Detector) IsRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isRunning
}
