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
// MAT POOL - Optimized memory management for GoCV Mats
// ============================================================================

// MatPool manages a pool of GoCV Mats with proper C++ object lifecycle handling
// This prevents memory leaks and reduces allocations
type MatPool struct {
	pool  sync.Pool
	stats struct {
		sync.Mutex
		gets, puts, creates int64
	}
}

// NewMatPool creates a pool with pre-warmed Mat pointers
func NewMatPool(warmupSize int) *MatPool {
	mp := &MatPool{}
	mp.pool = sync.Pool{
		New: func() interface{} {
			mp.stats.Lock()
			mp.stats.creates++
			mp.stats.Unlock()
			// Return pointer to empty Mat - lazy initialization
			return &gocv.Mat{}
		},
	}

	// Pre-warm the pool to avoid allocations during processing
	for i := 0; i < warmupSize; i++ {
		mp.pool.Put(&gocv.Mat{})
	}

	return mp
}

// Get retrieves a Mat from the pool, allocating if necessary
func (mp *MatPool) Get() *gocv.Mat {
	mp.stats.Lock()
	mp.stats.gets++
	mp.stats.Unlock()

	mat := mp.pool.Get().(*gocv.Mat)

	// Lazy initialization - only allocate when actually needed
	if mat.Empty() {
		*mat = gocv.NewMat()
	}

	return mat
}

// Put returns a Mat to the pool after cleaning it
func (mp *MatPool) Put(mat *gocv.Mat) {
	if mat == nil {
		return
	}

	mp.stats.Lock()
	mp.stats.puts++
	mp.stats.Unlock()

	// Clean the Mat but keep the pointer for reuse
	if !mat.Empty() {
		mat.Close()
	}

	mp.pool.Put(mat)
}

// Stats returns pool usage statistics for monitoring
func (mp *MatPool) Stats() (gets, puts, creates int64) {
	mp.stats.Lock()
	defer mp.stats.Unlock()
	return mp.stats.gets, mp.stats.puts, mp.stats.creates
}

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

// Detector performs motion detection using Farneback optical flow
// After calibration, it uses fixed thresholds for consistent detection
type Detector struct {
	config   *config.MotionConfig
	notifier *notification.Notifier

	// Persistent Mats - allocated once, reused throughout lifetime
	prevSmall *gocv.Mat // Previous downsampled frame
	currSmall *gocv.Mat // Current downsampled frame
	flow      *gocv.Mat // Optical flow field (2-channel)

	// Workspace Mats - temporary buffers for processing
	tempGray  *gocv.Mat // Grayscale conversion buffer
	tempSmall *gocv.Mat // Downsampling buffer
	magnitude *gocv.Mat // Flow magnitude buffer
	mask      *gocv.Mat // Binary motion mask

	// Mat pool for dynamic allocations
	matPool *MatPool

	// Detection parameters (fixed after calibration)
	baselineFlow   float64 // Baseline motion level from calibration
	fixedThreshold float64 // Detection threshold

	// State management
	stateMu   sync.RWMutex
	isRunning bool

	// Statistics
	statsMu    sync.Mutex
	stats      MotionStats
	frameCount int64

	// Consecutive frame tracking for noise reduction
	consecutiveMotionFrames int
	lastLogTime            time.Time
}

// NewDetector creates a detector that will be calibrated later
func NewDetector(config *config.MotionConfig, notifier *notification.Notifier) (*Detector, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create Mat pool with small warm-up
	matPool := NewMatPool(4)

	// Pre-allocate all persistent Mats
	prevSmall := gocv.NewMat()
	currSmall := gocv.NewMat()
	flow := gocv.NewMat()
	tempGray := gocv.NewMat()
	tempSmall := gocv.NewMat()
	magnitude := gocv.NewMat()
	mask := gocv.NewMat()

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
		matPool:        matPool,
		baselineFlow:   0.0, // Will be set by SetCalibration
		fixedThreshold: 0.3, // Will be set by SetCalibration
		stats:          MotionStats{},
		lastLogTime:    time.Now(),
	}, nil
}

// SetCalibration sets the baseline and threshold from calibration
func (d *Detector) SetCalibration(baseline, threshold float64) {
	d.baselineFlow = baseline
	d.fixedThreshold = threshold
	log.Printf("[Detector] Calibration applied - Baseline: %.4f%%, Threshold: %.4f%%", baseline, threshold)
}

// Start begins motion detection
func (d *Detector) Start() error {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	if d.isRunning {
		return nil
	}

	// Reset state
	d.stats = MotionStats{}
	d.frameCount = 0
	d.consecutiveMotionFrames = 0

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

	// Log final pool statistics
	if d.matPool != nil {
		gets, puts, creates := d.matPool.Stats()
		log.Printf("[Detector] Mat pool final stats - Gets: %d, Puts: %d, Creates: %d", gets, puts, creates)
	}

	log.Println("[Detector] Stopped")
	return nil
}

// IsRunning returns whether the detector is active
func (d *Detector) IsRunning() bool {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.isRunning
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

	// Convert to uint8 for counting
	maskU8 := d.matPool.Get()
	defer d.matPool.Put(maskU8)
	d.mask.ConvertTo(maskU8, gocv.MatTypeCV8U)

	// Count motion pixels
	motionPixels := gocv.CountNonZero(*maskU8)
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

	// Clean up all Mats
	mats := []*gocv.Mat{
		d.prevSmall, d.currSmall, d.flow,
		d.tempGray, d.tempSmall, d.magnitude, d.mask,
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