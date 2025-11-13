package motion

import (
	"fmt"
	"image"
	"log"
	"sync"
	"time"

	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/notification"
)

// ============================================================================
// DATA STRUCTURES
// ============================================================================

// MotionStats tracks detection performance metrics
type MotionStats struct {
	FramesProcessed   int64
	MotionEvents      int64
	LastMotionTime    time.Time
	AverageMotionArea float64
	MaxMotionArea     float64
	MinMotionArea     float64
	ProcessingTime    time.Duration
	LastProcessedTime time.Time
}

// ============================================================================
// DETECTOR - Optical flow motion detection with fixed thresholds
// ============================================================================

// MotionCallback is called when motion is detected
type MotionCallback func(confidence float64, regions []image.Rectangle, timestamp time.Time)

// Detector performs motion detection using Farneback optical flow
// After calibration, it uses fixed thresholds for consistent detection
type Detector struct {
	config   *config.MotionConfig
	notifier *notification.Notifier

	// Motion event callback (for recording service)
	motionCallback MotionCallback
	callbackMu     sync.RWMutex

	// Persistent Mats - allocated once, reused throughout lifetime
	prevSmall *gocv.Mat // Previous downsampled frame
	currSmall *gocv.Mat // Current downsampled frame
	flow      *gocv.Mat // Optical flow field (2-channel)

	// Workspace Mats - temporary buffers for processing
	tempGray  *gocv.Mat // Grayscale conversion buffer
	tempSmall *gocv.Mat // Downsampling buffer
	magnitude *gocv.Mat // Flow magnitude buffer
	mask      *gocv.Mat // Binary motion mask (float32)
	maskU8    *gocv.Mat // Binary motion mask (uint8) - PRE-ALLOCATED, NO POOL NEEDED

	// Detection parameters (fixed after calibration)
	baselineFlow   float64 // Baseline motion level from calibration
	fixedThreshold float64 // Detection threshold
	isCalibrated   bool    // Whether calibration has been performed

	// State management
	stateMu   sync.RWMutex
	isRunning bool

	// Statistics
	statsMu    sync.Mutex
	stats      MotionStats
	frameCount int64

	// Consecutive frame tracking for noise reduction
	consecutiveMotionFrames int
	lastLogTime             time.Time

	// SIMPLE DATA COLLECTION - First 100 motion values
	flowDataMu    sync.Mutex
	flowDataCount int
	maxDataPoints int
}

// NewDetector creates a detector that will be calibrated later
func NewDetector(config *config.MotionConfig, notifier *notification.Notifier) (*Detector, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Pre-allocate ALL persistent Mats including maskU8
	prevSmall := gocv.NewMat()
	currSmall := gocv.NewMat()
	flow := gocv.NewMat()
	tempGray := gocv.NewMat()
	tempSmall := gocv.NewMat()
	magnitude := gocv.NewMat()
	mask := gocv.NewMat()
	maskU8 := gocv.NewMat() // Pre-allocate the uint8 mask - NO POOL NEEDED

	return &Detector{
		config:         config,
		notifier:       notifier,
		prevSmall:      &prevSmall,
		currSmall:      &currSmall,
		flow:           &flow,
		tempGray:       &tempGray,
		tempSmall:      &tempSmall,
		magnitude:      &magnitude,
		mask:           &mask,
		maskU8:         &maskU8, // Store the pre-allocated Mat
		baselineFlow:   0.0,     // Will be set by SetCalibration
		fixedThreshold: 0.3,     // Will be set by SetCalibration
		stats:          MotionStats{},
		lastLogTime:    time.Now(),
		maxDataPoints:  100, // Collect first 100 data points
		flowDataCount:  0,
	}, nil
}

// SetCalibration sets the baseline and threshold from calibration
func (d *Detector) SetCalibration(baseline, threshold float64) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	d.baselineFlow = baseline
	d.fixedThreshold = threshold
	d.isCalibrated = true
	log.Printf("[Detector] Calibration applied - Baseline: %.4f%%, Threshold: %.4f%%", baseline, threshold)
}

// SetMotionCallback registers a callback for motion events (e.g., for recording service)
func (d *Detector) SetMotionCallback(callback MotionCallback) {
	d.callbackMu.Lock()
	defer d.callbackMu.Unlock()
	d.motionCallback = callback
}

// triggerMotionCallback safely calls the motion callback if set
func (d *Detector) triggerMotionCallback(confidence float64, regions []image.Rectangle, timestamp time.Time) {
	d.callbackMu.RLock()
	callback := d.motionCallback
	d.callbackMu.RUnlock()

	if callback != nil {
		// Call asynchronously to avoid blocking detection
		go func() {
			callback(confidence, regions, timestamp)
		}()
	}
}

// Start begins motion detection
func (d *Detector) Start() error {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	if d.isRunning {
		return nil
	}

	// Validate calibration has been performed
	if !d.isCalibrated {
		return fmt.Errorf("cannot start motion detector without calibration - call SetCalibration() first")
	}

	// Reset state
	d.stats = MotionStats{}
	d.frameCount = 0
	d.consecutiveMotionFrames = 0

	// Reset data collection
	d.flowDataMu.Lock()
	d.flowDataCount = 0
	d.flowDataMu.Unlock()

	log.Println("[Detector] Starting data collection for first 100 frames...")

	// Clear previous frame buffer
	if !d.prevSmall.Empty() {
		d.prevSmall.Close()
		*d.prevSmall = gocv.NewMat()
	}

	d.isRunning = true
	log.Println("[Detector] Started optical flow motion detection")
	return nil
}

// Stop halts motion detection
func (d *Detector) Stop() error {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	if !d.isRunning {
		return nil
	}

	d.isRunning = false
	log.Println("[Detector] Stopped")
	return nil
}

// IsRunning returns whether the detector is active
func (d *Detector) IsRunning() bool {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.isRunning
}

// IsCalibrated returns whether calibration has been performed
func (d *Detector) IsCalibrated() bool {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.isCalibrated
}

// GetCalibration returns the current calibration values
func (d *Detector) GetCalibration() (baseline, threshold float64, calibrated bool) {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.baselineFlow, d.fixedThreshold, d.isCalibrated
}

// Detect is the main detection loop processing frames
func (d *Detector) Detect(frameChan <-chan gocv.Mat, motionChan chan<- bool) {
	defer close(motionChan)

	var (
		lastNotification   = time.Time{}
		inMotionState      = false
		lastMotionDetected = time.Time{}
		processedFrames    = int64(0)
	)

	log.Printf("[Detector] Detection loop started")

	for frame := range frameChan {
		// Check if we should stop
		if !d.IsRunning() {
			frame.Close()
			break
		}

		// Process frame with optical flow
		hasMotion, motionArea := d.processFrameOpticalFlow(frame)
		frame.Close()

		processedFrames++
		d.frameCount++

		// SIMPLE DATA LOGGING - First 100 frames
		d.logFlowData(motionArea)

		// Track consecutive frames for noise reduction
		if hasMotion {
			d.consecutiveMotionFrames++
		} else {
			d.consecutiveMotionFrames = 0
		}

		// Require minimum consecutive frames to confirm motion
		motionConfirmed := d.consecutiveMotionFrames >= d.config.MinConsecutiveFrames

		now := time.Now()

		// Handle motion state transitions
		if motionConfirmed {
			lastMotionDetected = now

			if !inMotionState {
				// Check cooldown period
				if now.Sub(lastNotification) >= d.config.CooldownPeriod {
					inMotionState = true
					lastNotification = now

					// Send motion event
					select {
					case motionChan <- true:
						log.Printf("[Detector] 🎯 MOTION DETECTED - Area: %.2f%% (threshold: %.2f%%)",
							motionArea, d.fixedThreshold)
					default:
						// Channel full, skip
					}

					// Send notification
					d.sendNotification()

					// Trigger recording callback
					// Normalize confidence: motionArea relative to threshold
					confidence := motionArea / d.fixedThreshold
					if confidence > 1.0 {
						confidence = 1.0
					}
					d.triggerMotionCallback(confidence, nil, now)

					// Update stats
					d.statsMu.Lock()
					d.stats.MotionEvents++
					d.stats.LastMotionTime = now
					d.statsMu.Unlock()
				}
			}
		} else if inMotionState {
			// Check if motion has stopped
			if now.Sub(lastMotionDetected) >= d.config.NoMotionDelay {
				inMotionState = false

				// Send motion stopped event
				select {
				case motionChan <- false:
					log.Println("[Detector] Motion stopped")
				default:
					// Channel full, skip
				}
			}
		}

		// Periodic status update (every 30 seconds)
		if now.Sub(d.lastLogTime) >= 30*time.Second {
			d.statsMu.Lock()
			avgProcessingTime := d.stats.ProcessingTime
			events := d.stats.MotionEvents
			d.statsMu.Unlock()

			log.Printf("[Detector] Status - Frames: %d, Events: %d, Avg processing: %v",
				processedFrames, events, avgProcessingTime)
			d.lastLogTime = now
		}
	}

	log.Printf("[Detector] Detection loop ended - processed %d frames", processedFrames)
}

// logFlowData logs the first N motion area values for analysis
func (d *Detector) logFlowData(motionArea float64) {
	d.flowDataMu.Lock()
	defer d.flowDataMu.Unlock()

	if d.flowDataCount >= d.maxDataPoints {
		return // Already collected enough data
	}

	d.flowDataCount++

	// Log with special tag for easy grep/filtering
	log.Printf("[FLOWDATA] Frame %03d: %.6f%% (baseline: %.4f, threshold: %.4f)",
		d.flowDataCount, motionArea, d.baselineFlow, d.fixedThreshold)

	// Print completion message
	if d.flowDataCount == d.maxDataPoints {
		log.Println("[FLOWDATA] ======== DATA COLLECTION COMPLETE ========")
		log.Printf("[FLOWDATA] Collected %d motion area values", d.maxDataPoints)
		log.Println("[FLOWDATA] To extract: grep '\\[FLOWDATA\\]' from logs")
		log.Println("[FLOWDATA] ==========================================")
	}
}

// processFrameOpticalFlow analyzes a single frame using optical flow
func (d *Detector) processFrameOpticalFlow(frame gocv.Mat) (bool, float64) {
	start := time.Now()

	// Convert to grayscale
	if frame.Channels() > 1 {
		gocv.CvtColor(frame, d.tempGray, gocv.ColorBGRToGray)
	} else {
		frame.CopyTo(d.tempGray)
	}

	// Downsample for performance
	size := image.Pt(d.tempGray.Cols()/2, d.tempGray.Rows()/2)
	gocv.PyrDown(*d.tempGray, d.tempSmall, size, gocv.BorderDefault)

	// Store first frame
	if d.prevSmall.Empty() {
		d.tempSmall.CopyTo(d.prevSmall)
		return false, 0
	}

	// Copy current frame
	d.tempSmall.CopyTo(d.currSmall)

	// Calculate optical flow using Farneback algorithm
	gocv.CalcOpticalFlowFarneback(
		*d.prevSmall,
		*d.currSmall,
		d.flow,
		0.5, // Pyramid scale
		3,   // Levels
		15,  // Window size
		3,   // Iterations
		5,   // Polynomial expansion
		1.2, // Gaussian standard deviation
		gocv.OptflowFarnebackGaussian,
	)

	// Analyze flow field to get motion area percentage
	motionArea := d.analyzeFlow()

	// Swap frames for next iteration
	d.currSmall.CopyTo(d.prevSmall)

	// Update processing time stats
	processingTime := time.Since(start)
	d.updateStats(motionArea, processingTime)

	// Simple threshold check
	hasMotion := motionArea > d.fixedThreshold

	return hasMotion, motionArea
}

// analyzeFlow computes motion area percentage from optical flow
func (d *Detector) analyzeFlow() float64 {
	if d.flow.Empty() {
		return 0
	}

	// Split flow into X and Y components
	flowChannels := gocv.Split(*d.flow)
	defer func() {
		for _, ch := range flowChannels {
			ch.Close()
		}
	}()

	if len(flowChannels) < 2 {
		return 0
	}

	// Calculate magnitude of flow vectors
	gocv.Magnitude(flowChannels[0], flowChannels[1], d.magnitude)

	// Create binary mask of pixels with significant motion
	gocv.Threshold(*d.magnitude, d.mask, float32(0.3), 255, gocv.ThresholdBinary)

	// Convert to uint8 for counting - USE PRE-ALLOCATED MAT, NO POOL
	d.mask.ConvertTo(d.maskU8, gocv.MatTypeCV8U)

	// Count motion pixels
	motionPixels := gocv.CountNonZero(*d.maskU8)
	totalPixels := d.magnitude.Rows() * d.magnitude.Cols()

	// Calculate percentage
	motionArea := 0.0
	if totalPixels > 0 {
		motionArea = float64(motionPixels) * 100.0 / float64(totalPixels)
	}

	return motionArea
}

// updateStats updates motion statistics
func (d *Detector) updateStats(motionArea float64, processingTime time.Duration) {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()

	d.stats.FramesProcessed++
	d.stats.ProcessingTime = processingTime
	d.stats.LastProcessedTime = time.Now()

	// Update motion area statistics
	if d.stats.FramesProcessed == 1 {
		d.stats.AverageMotionArea = motionArea
		d.stats.MinMotionArea = motionArea
		d.stats.MaxMotionArea = motionArea
	} else {
		// Running average
		d.stats.AverageMotionArea = (d.stats.AverageMotionArea*float64(d.stats.FramesProcessed-1) + motionArea) / float64(d.stats.FramesProcessed)

		// Track min/max
		if motionArea > d.stats.MaxMotionArea {
			d.stats.MaxMotionArea = motionArea
		}
		if motionArea < d.stats.MinMotionArea && motionArea > 0 {
			d.stats.MinMotionArea = motionArea
		}
	}
}

// sendNotification sends motion alert if configured
func (d *Detector) sendNotification() {
	if d.notifier == nil {
		return
	}

	// Send asynchronously to avoid blocking detection
	go func() {
		if err := (*d.notifier).SendNotification(); err != nil {
			log.Printf("[Detector] Notification failed: %v", err)
		}
	}()
}

// GetStats returns copy of current statistics
func (d *Detector) GetStats() MotionStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	return d.stats
}

// ResetStats clears all statistics
func (d *Detector) ResetStats() {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	d.stats = MotionStats{}
	d.frameCount = 0
}

// Close releases all resources
func (d *Detector) Close() error {
	// Stop detection first
	if err := d.Stop(); err != nil {
		return err
	}

	// Clean up ALL Mats including the pre-allocated maskU8
	mats := []*gocv.Mat{
		d.prevSmall, d.currSmall, d.flow,
		d.tempGray, d.tempSmall, d.magnitude, d.mask,
		d.maskU8, // Clean up the pre-allocated uint8 mask
	}

	for _, mat := range mats {
		if mat != nil && !mat.Empty() {
			mat.Close()
		}
	}

	d.ResetStats()
	log.Println("[Detector] Resources released")
	return nil
}
