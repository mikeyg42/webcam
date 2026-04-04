package calibration

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mikeyg42/webcam/internal/imgconv"
	"gocv.io/x/gocv"
)

// CalibrationState represents the current state of calibration
type CalibrationState string

const (
	StateIdle       CalibrationState = "idle"       // Not calibrating
	StateRecording  CalibrationState = "recording"  // Recording calibration video
	StateProcessing CalibrationState = "processing" // Processing frames
	StateComplete   CalibrationState = "complete"   // Calibration finished
	StateError      CalibrationState = "error"      // Error occurred
)

// CalibrationResult contains the computed baseline and threshold
type CalibrationResult struct {
	Version   int     `json:"version"`   // Schema version for persistence compatibility
	Baseline  float64 `json:"baseline"`  // Mean + standard deviation of motion samples
	Threshold float64 `json:"threshold"` // Baseline + sensitivity offset (0.05 for "hair trigger")
	Samples   int     `json:"samples"`   // Number of samples collected
	Mean      float64 `json:"mean"`      // Mean motion area
	StdDev    float64 `json:"stddev"`    // Standard deviation
}

const calibrationResultVersion = 1

// CalibrationProgress tracks the current progress
type CalibrationProgress struct {
	State       CalibrationState
	Progress    float64 // 0-100%
	Message     string
	VideoPath   string           // Path to recorded calibration video
	Result      *CalibrationResult
	Error       error
}

// Service manages async calibration workflow
type Service struct {
	// Configuration
	calibrationDuration time.Duration
	outputDir           string
	videoFormat         string

	// State
	mu       sync.RWMutex
	state    CalibrationState
	progress float64
	message  string
	result   *CalibrationResult
	err      error
	videoPath string

	// Cancellation
	cancelMu sync.Mutex
	cancelFn context.CancelFunc
}

// NewService creates a calibration service.
// It attempts to load a previously persisted calibration result from disk.
func NewService(outputDir string) *Service {
	s := &Service{
		calibrationDuration: 10 * time.Second,
		outputDir:           outputDir,
		videoFormat:         "mp4",
		state:               StateIdle,
	}
	s.loadPersistedResult()
	return s
}

// StartCalibration begins async calibration process
// It receives frames from frameChan, records them to video, and computes calibration
// The optional cleanup function is called when calibration completes (success or failure)
func (s *Service) StartCalibration(ctx context.Context, frameChan <-chan image.Image, cleanup ...func()) error {
	s.mu.Lock()
	if s.state != StateIdle && s.state != StateComplete && s.state != StateError {
		s.mu.Unlock()
		return fmt.Errorf("calibration already in progress (state: %s)", s.state)
	}

	// Reset state
	s.state = StateRecording
	s.progress = 0
	s.message = "Starting calibration..."
	s.result = nil
	s.err = nil
	s.videoPath = ""
	s.mu.Unlock()

	// Create cancellable context
	calibCtx, cancel := context.WithCancel(ctx)
	s.cancelMu.Lock()
	s.cancelFn = cancel
	s.cancelMu.Unlock()

	// Combine cleanup functions
	var cleanupFn func()
	if len(cleanup) > 0 {
		cleanupFn = func() {
			for _, fn := range cleanup {
				if fn != nil {
					fn()
				}
			}
		}
	}

	// Run calibration in background
	go s.runCalibration(calibCtx, frameChan, cleanupFn)

	return nil
}

// runCalibration performs the calibration workflow
func (s *Service) runCalibration(ctx context.Context, frameChan <-chan image.Image, cleanup func()) {
	// Ensure cleanup is called when calibration finishes
	if cleanup != nil {
		defer cleanup()
	}

	log.Println("[Calibration] Starting calibration workflow...")

	// Generate video path
	timestamp := time.Now().Format("20060102_150405")
	videoFilename := fmt.Sprintf("calibration_%s.mp4", timestamp)
	videoPath := filepath.Join(s.outputDir, videoFilename)

	s.updateState(StateRecording, 0, "Recording calibration video...")

	// Create OpenCV video writer
	videoWriter, err := gocv.VideoWriterFile(videoPath, "avc1", 30.0, 1280, 720, true)
	if err != nil {
		s.setError(fmt.Errorf("failed to create video writer: %v", err))
		return
	}
	defer videoWriter.Close()

	// Create calibration processing channels
	calibChan := make(chan gocv.Mat, 30)
	doneChan := make(chan CalibrationResult, 1)

	// Use sync.Once to prevent double-close of calibChan which causes panic
	var closeOnce sync.Once
	closeCalibChan := func() {
		closeOnce.Do(func() {
			close(calibChan)
		})
	}
	defer closeCalibChan()

	// Start calibration processor
	go s.processCalibration(ctx, calibChan, doneChan)

	// Record frames and feed to calibration
	timeout := time.After(s.calibrationDuration)
	startTime := time.Now()
	frameCount := 0

	for {
		select {
		case frame, ok := <-frameChan:
			if !ok {
				// Input channel closed
				closeCalibChan()
				result := <-doneChan
				s.finalizeCalibration(videoPath, result)
				return
			}

			frameCount++

			// Debug: log frame fingerprint to verify frames are different
			if frameCount%30 == 0 {
				if ycbcr, ok := frame.(*image.YCbCr); ok && len(ycbcr.Y) > 100 {
					log.Printf("[Calibration] Received frame %d fingerprint: Y[0:8]=%v", frameCount, ycbcr.Y[0:8])
				}
			}

			// Convert image.Image to gocv.Mat
			mat, err := imageToMat(frame)
			if err != nil {
				log.Printf("[Calibration] Failed to convert frame: %v", err)
				continue
			}

			// Record to video
			if err := videoWriter.Write(mat); err != nil {
				log.Printf("[Calibration] Failed to write frame: %v", err)
			}

			// Send copy to calibration processor
			matCopy := mat.Clone()
			select {
			case calibChan <- matCopy:
			default:
				matCopy.Close()
			}

			mat.Close()

			// Update progress
			elapsed := time.Since(startTime)
			progress := (elapsed.Seconds() / s.calibrationDuration.Seconds()) * 100
			if progress > 100 {
				progress = 100
			}
			s.updateState(StateRecording, progress, fmt.Sprintf("Recording... %d frames", frameCount))

		case <-timeout:
			// Calibration duration complete
			closeCalibChan()
			result := <-doneChan
			s.finalizeCalibration(videoPath, result)
			return

		case <-ctx.Done():
			// Cancelled
			closeCalibChan()
			s.setError(fmt.Errorf("calibration cancelled"))
			return
		}
	}
}

// processCalibration analyzes frames using optical flow
func (s *Service) processCalibration(ctx context.Context, calibChan <-chan gocv.Mat, doneChan chan<- CalibrationResult) {
	// Allocate optical flow components
	var (
		prevSmall = gocv.NewMat()
		currSmall = gocv.NewMat()
		flow      = gocv.NewMat()
		tempGray  = gocv.NewMat()
		tempSmall = gocv.NewMat()
		magnitude = gocv.NewMat()
	)

	defer func() {
		prevSmall.Close()
		currSmall.Close()
		flow.Close()
		tempGray.Close()
		tempSmall.Close()
		magnitude.Close()
	}()

	samples := make([]float64, 0, 150)
	frameNum := 0

	for frame := range calibChan {
		frameNum++
		select {
		case <-ctx.Done():
			frame.Close()
			doneChan <- s.calculateResult(samples)
			return
		default:
		}

		// Process frame with optical flow
		motionArea := s.processFrame(frame, &prevSmall, &currSmall, &flow, &tempGray, &tempSmall, &magnitude, frameNum)
		frame.Close()

		if motionArea >= 0 {
			samples = append(samples, motionArea)
		}
	}

	// Calculate final result
	result := s.calculateResult(samples)
	doneChan <- result
}

// processFrame analyzes a single frame using optical flow
func (s *Service) processFrame(frame gocv.Mat, prevSmall, currSmall, flow, tempGray, tempSmall, magnitude *gocv.Mat, frameNum int) float64 {
	// Convert to grayscale
	if frame.Channels() > 1 {
		gocv.CvtColor(frame, tempGray, gocv.ColorBGRToGray)
	} else {
		frame.CopyTo(tempGray)
	}

	// Downsample for performance
	// NOTE: PyrDown was producing all-zero output on some gocv versions.
	// Using Resize with 0.5 scale factor instead - more reliable.
	gocv.Resize(*tempGray, tempSmall, image.Point{}, 0.5, 0.5, gocv.InterpolationLinear)

	// Debug: log pixel values every 30 frames
	if frameNum%30 == 0 {
		if data, err := tempSmall.DataPtrUint8(); err == nil && len(data) > 100 {
			log.Printf("[Calibration] Frame %d grayscale pixels[0:8]: %v (size: %dx%d)",
				frameNum, data[0:8], tempSmall.Cols(), tempSmall.Rows())
		}
	}

	// First frame - just store
	if prevSmall.Empty() {
		tempSmall.CopyTo(prevSmall)
		return -1
	}

	// Copy current frame
	tempSmall.CopyTo(currSmall)

	// Debug: compare prev vs curr pixels
	if frameNum%30 == 0 {
		if prevData, err := prevSmall.DataPtrUint8(); err == nil && len(prevData) > 100 {
			if currData, err := currSmall.DataPtrUint8(); err == nil && len(currData) > 100 {
				log.Printf("[Calibration] Frame %d comparison - prev[0:8]=%v, curr[0:8]=%v",
					frameNum, prevData[0:8], currData[0:8])
			}
		}
	}

	// Calculate optical flow using Farneback algorithm
	gocv.CalcOpticalFlowFarneback(
		*prevSmall, *currSmall, flow,
		0.5, // Pyramid scale
		3,   // Levels
		15,  // Window size
		3,   // Iterations
		5,   // Polynomial expansion
		1.2, // Gaussian standard deviation
		gocv.OptflowFarnebackGaussian,
	)

	// Analyze flow to get motion area
	motionArea := s.analyzeFlow(*flow, magnitude)

	// Debug: log motion area
	if frameNum%30 == 0 {
		log.Printf("[Calibration] Frame %d motion area: %.6f%%", frameNum, motionArea)
	}

	// Swap frames for next iteration
	currSmall.CopyTo(prevSmall)

	return motionArea
}

// analyzeFlow computes motion area percentage from optical flow
func (s *Service) analyzeFlow(flow gocv.Mat, magnitude *gocv.Mat) float64 {
	if flow.Empty() {
		return 0
	}

	// Split flow into X and Y components
	flowChannels := gocv.Split(flow)
	defer func() {
		for _, ch := range flowChannels {
			ch.Close()
		}
	}()

	if len(flowChannels) < 2 {
		return 0
	}

	// Calculate magnitude of flow vectors
	gocv.Magnitude(flowChannels[0], flowChannels[1], magnitude)

	// Create binary mask of pixels with significant motion
	mask := gocv.NewMat()
	defer mask.Close()
	gocv.Threshold(*magnitude, &mask, float32(0.3), 255, gocv.ThresholdBinary)

	// Convert to uint8 for counting
	maskU8 := gocv.NewMat()
	defer maskU8.Close()
	mask.ConvertTo(&maskU8, gocv.MatTypeCV8U)

	// Count motion pixels
	motionPixels := gocv.CountNonZero(maskU8)
	totalPixels := magnitude.Rows() * magnitude.Cols()

	if totalPixels > 0 {
		return float64(motionPixels) * 100.0 / float64(totalPixels)
	}

	return 0
}

// calculateResult computes baseline and threshold from samples
func (s *Service) calculateResult(samples []float64) CalibrationResult {
	if len(samples) < 10 {
		log.Printf("[Calibration] Warning: Only %d samples collected", len(samples))
		return CalibrationResult{
			Baseline:  0.5,
			Threshold: 1.0,
			Samples:   len(samples),
		}
	}

	// Calculate mean
	sum := 0.0
	for _, v := range samples {
		sum += v
	}
	mean := sum / float64(len(samples))

	// Calculate standard deviation
	variance := 0.0
	for _, v := range samples {
		diff := v - mean
		variance += diff * diff
	}
	stddev := math.Sqrt(variance / float64(len(samples)))

	// Set baseline and threshold
	baseline := mean + stddev
	threshold := baseline + 0.05 // Hair trigger - very sensitive

	log.Printf("[Calibration] Samples: %d, Mean: %.4f, StdDev: %.4f, Baseline: %.4f, Threshold: %.4f",
		len(samples), mean, stddev, baseline, threshold)

	return CalibrationResult{
		Baseline:  baseline,
		Threshold: threshold,
		Samples:   len(samples),
		Mean:      mean,
		StdDev:    stddev,
	}
}

// finalizeCalibration completes the calibration process
func (s *Service) finalizeCalibration(videoPath string, result CalibrationResult) {
	log.Println("[Calibration] Finalizing calibration...")

	// Update final state
	s.mu.Lock()
	s.state = StateComplete
	s.progress = 100
	s.message = "Calibration complete"
	s.result = &result
	s.videoPath = videoPath
	s.mu.Unlock()

	log.Printf("[Calibration] Complete - Video: %s, Baseline: %.4f%%, Threshold: %.4f%%",
		videoPath, result.Baseline, result.Threshold)

	s.persistResult(&result)
}

// persistResult writes the calibration result to disk as JSON using atomic write-then-rename.
func (s *Service) persistResult(result *CalibrationResult) {
	result.Version = calibrationResultVersion

	path := s.persistPath()
	data, err := json.Marshal(result)
	if err != nil {
		log.Printf("[Calibration] Warning: failed to marshal result for persistence: %v", err)
		return
	}

	// Atomic write: write to temp file, then rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		log.Printf("[Calibration] Warning: failed to write temp file %s: %v", tmpPath, err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		log.Printf("[Calibration] Warning: failed to rename %s to %s: %v", tmpPath, path, err)
		return
	}
	log.Printf("[Calibration] Result persisted to %s", path)
}

// loadPersistedResult loads a previously saved calibration result from disk
func (s *Service) loadPersistedResult() {
	path := s.persistPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return // No persisted result or can't read — not an error
	}

	var result CalibrationResult
	if err := json.Unmarshal(data, &result); err != nil {
		log.Printf("[Calibration] Warning: failed to parse persisted result: %v", err)
		return
	}

	if result.Version != calibrationResultVersion {
		log.Printf("[Calibration] Ignoring persisted result with incompatible version %d (expected %d)",
			result.Version, calibrationResultVersion)
		return
	}

	s.result = &result
	s.state = StateComplete
	s.progress = 100
	s.message = "Calibration loaded from disk"
	log.Printf("[Calibration] Loaded persisted result: baseline=%.4f%%, threshold=%.4f%%",
		result.Baseline, result.Threshold)
}

func (s *Service) persistPath() string {
	return filepath.Join(s.outputDir, "calibration_result.json")
}

// updateState updates the calibration state and progress
func (s *Service) updateState(state CalibrationState, progress float64, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.progress = progress
	s.message = message
}

// setError sets the error state
func (s *Service) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateError
	s.err = err
	s.message = err.Error()
	log.Printf("[Calibration] Error: %v", err)
}

// GetProgress returns the current calibration progress
func (s *Service) GetProgress() CalibrationProgress {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return CalibrationProgress{
		State:     s.state,
		Progress:  s.progress,
		Message:   s.message,
		VideoPath: s.videoPath,
		Result:    s.result,
		Error:     s.err,
	}
}

// Cancel stops the current calibration
func (s *Service) Cancel() {
	s.cancelMu.Lock()
	if s.cancelFn != nil {
		s.cancelFn()
		s.cancelFn = nil
	}
	s.cancelMu.Unlock()

	s.updateState(StateIdle, 0, "Calibration cancelled")
}

// Reset clears any error state and returns to idle.
// This allows the user to retry calibration after an error.
func (s *Service) Reset() {
	s.cancelMu.Lock()
	if s.cancelFn != nil {
		s.cancelFn()
		s.cancelFn = nil
	}
	s.cancelMu.Unlock()

	s.mu.Lock()
	s.state = StateIdle
	s.progress = 0
	s.message = "Ready for calibration"
	s.result = nil
	s.err = nil
	s.videoPath = ""
	s.mu.Unlock()

	log.Println("[Calibration] State reset to idle")
}

// imageToMat converts image.Image to gocv.Mat
func imageToMat(img image.Image) (gocv.Mat, error) {
	return imgconv.ToMat(img)
}
