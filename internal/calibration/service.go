package calibration

import (
	"context"
	"fmt"
	"image"
	"log"
	"math"
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
	Baseline  float64 // Mean + standard deviation of motion samples
	Threshold float64 // Baseline + sensitivity offset (0.05 for "hair trigger")
	Samples   int     // Number of samples collected
	Mean      float64 // Mean motion area
	StdDev    float64 // Standard deviation
}

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

// NewService creates a calibration service
func NewService(outputDir string) *Service {
	return &Service{
		calibrationDuration: 10 * time.Second,
		outputDir:           outputDir,
		videoFormat:         "mp4",
		state:               StateIdle,
	}
}

// StartCalibration begins async calibration process
// It receives frames from frameChan, records them to video, and computes calibration
func (s *Service) StartCalibration(ctx context.Context, frameChan <-chan image.Image) error {
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

	// Run calibration in background
	go s.runCalibration(calibCtx, frameChan)

	return nil
}

// runCalibration performs the calibration workflow
func (s *Service) runCalibration(ctx context.Context, frameChan <-chan image.Image) {
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
				close(calibChan)
				result := <-doneChan
				s.finalizeCalibration(videoPath, result)
				return
			}

			frameCount++

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
			close(calibChan)
			result := <-doneChan
			s.finalizeCalibration(videoPath, result)
			return

		case <-ctx.Done():
			// Cancelled
			close(calibChan)
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

	for frame := range calibChan {
		select {
		case <-ctx.Done():
			frame.Close()
			doneChan <- s.calculateResult(samples)
			return
		default:
		}

		// Process frame with optical flow
		motionArea := s.processFrame(frame, &prevSmall, &currSmall, &flow, &tempGray, &tempSmall, &magnitude)
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
func (s *Service) processFrame(frame gocv.Mat, prevSmall, currSmall, flow, tempGray, tempSmall, magnitude *gocv.Mat) float64 {
	// Convert to grayscale
	if frame.Channels() > 1 {
		gocv.CvtColor(frame, tempGray, gocv.ColorBGRToGray)
	} else {
		frame.CopyTo(tempGray)
	}

	// Downsample for performance
	size := image.Pt(tempGray.Cols()/2, tempGray.Rows()/2)
	gocv.PyrDown(*tempGray, tempSmall, size, gocv.BorderDefault)

	// First frame - just store
	if prevSmall.Empty() {
		tempSmall.CopyTo(prevSmall)
		return -1
	}

	// Copy current frame
	tempSmall.CopyTo(currSmall)

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

// imageToMat converts image.Image to gocv.Mat
func imageToMat(img image.Image) (gocv.Mat, error) {
	return imgconv.ToMat(img)
}
