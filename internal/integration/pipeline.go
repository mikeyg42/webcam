package integration

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/framestream"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/video"
)

// Debug image saving - only save once
var (
	debugImagesSaved sync.Once
)

// Pipeline manages the integration between frame distribution, motion detection, and recording
type Pipeline struct {
	ctx              context.Context
	cancel           context.CancelFunc
	config           *config.Config
	frameDistributor *framestream.FrameDistributor
	motionDetector   *motion.Detector
	videoRecorder    *video.Recorder
}

// NewPipeline creates a new integration pipeline
func NewPipeline(ctx context.Context, cfg *config.Config, frameDistributor *framestream.FrameDistributor,
	motionDetector *motion.Detector, videoRecorder *video.Recorder) *Pipeline {

	pipelineCtx, cancel := context.WithCancel(ctx)

	return &Pipeline{
		ctx:              pipelineCtx,
		cancel:           cancel,
		config:           cfg,
		frameDistributor: frameDistributor,
		motionDetector:   motionDetector,
		videoRecorder:    videoRecorder,
	}
}

// Start begins all pipeline connections
func (p *Pipeline) Start() error {
	log.Println("[Pipeline] Starting integration pipeline")

	// Start WebRTC frame consumer (testing mode - just consumes frames)
	go p.consumeWebRTCFrames()

	// Start motion detection pipeline
	go p.runMotionDetection()

	// Start recording frame consumer
	go p.consumeRecordingFrames()

	log.Println("[Pipeline] All pipeline connections started")
	return nil
}

// Stop stops all pipeline connections
func (p *Pipeline) Stop() {
	log.Println("[Pipeline] Stopping integration pipeline")
	p.cancel()
}

// consumeWebRTCFrames handles VP9 frames (TESTING MODE: disabled WebRTC streaming)
func (p *Pipeline) consumeWebRTCFrames() {
	vp9Channel := p.frameDistributor.GetVP9Channel()
	log.Println("[Pipeline] TESTING MODE: WebRTC streaming disabled - consuming VP9 frames silently")

	frameCount := 0
	lastLogTime := time.Now()

	for {
		select {
		case <-p.ctx.Done():
			log.Println("[Pipeline] WebRTC consumer stopping due to context cancellation")
			return
		case frame, ok := <-vp9Channel:
			if !ok {
				log.Println("[Pipeline] VP9 channel closed, stopping WebRTC consumer")
				return
			}

			if frame != nil {
				frameCount++

				// Log progress every 5 seconds
				if time.Since(lastLogTime) >= 5*time.Second {
					bounds := frame.Bounds()
					log.Printf("[Pipeline] TESTING: Consuming VP9 frames (%d processed, %dx%d) - WebRTC disabled",
						frameCount, bounds.Dx(), bounds.Dy())
					lastLogTime = time.Now()
				}

				// In production: send to WebRTC encoder
				// For testing: do nothing to isolate motion detection + recording
			}
		}
	}
}

// runMotionDetection handles motion detection pipeline (calibration now done in main)
func (p *Pipeline) runMotionDetection() {
	motionChannel := p.frameDistributor.GetMotionChannel()
	log.Println("[Pipeline] Starting motion detection pipeline (detector already calibrated)")

	// Create persistent channels for motion detection with larger buffers
	frameChan := make(chan gocv.Mat, 200)   // Larger buffer to handle processing delays
	motionChan := make(chan bool, 50)

	// Start motion detector in goroutine (detector is already calibrated from main)
	go func() {
		defer close(motionChan)
		log.Println("[Pipeline] Starting motion detector Detect() loop")

		// Detector is already started and calibrated in main.go
		// Just run the Detect loop
		p.motionDetector.Detect(frameChan, motionChan)
		log.Println("[Pipeline] Motion detector stopped")
	}()

	// Start motion event handler
	go p.handleMotionEvents(motionChan)

	// Main frame processing loop
	frameCount := 0
	lastLogTime := time.Now()

	for {
		select {
		case <-p.ctx.Done():
			log.Println("[Pipeline] Motion detection stopping due to context cancellation")
			close(frameChan)
			return
		case frame, ok := <-motionChannel:
			if !ok {
				log.Println("[Pipeline] Motion channel closed, stopping motion detection")
				close(frameChan)
				return
			}

			if frame != nil {
				frameCount++

				// Log progress every 5 seconds
				if time.Since(lastLogTime) >= 5*time.Second {
					bounds := frame.Bounds()
					log.Printf("[Pipeline] Processing motion frames (%d processed, %dx%d)",
						frameCount, bounds.Dx(), bounds.Dy())
					lastLogTime = time.Now()
				}

				// Convert image.Image to gocv.Mat for motion detection
				mat, err := imageToMat(frame)
				if err != nil {
					log.Printf("[Pipeline] Error converting frame to Mat: %v", err)
					continue
				}

				// Save debug images (first frame only)
				saveDebugImages(frame, mat)

				// Send frame to motion detector (non-blocking)
				select {
				case frameChan <- mat:
					// Frame sent successfully
				case <-p.ctx.Done():
					mat.Close()
					return
				default:
					// Motion detection busy, drop this frame
					mat.Close()
					log.Printf("[Pipeline] Motion detector busy, dropping frame")
				}
			}
		}
	}
}

// handleMotionEvents processes motion detection events and manages recording
func (p *Pipeline) handleMotionEvents(motionChan <-chan bool) {
	log.Println("[Pipeline] Starting motion event handler")
	isRecording := false
	var recordingStart time.Time
	lastMotionTime := time.Time{}

	for {
		select {
		case <-p.ctx.Done():
			log.Println("[Pipeline] Motion handler stopping due to context cancellation")
			if isRecording {
				log.Println("[Pipeline] Stopping recording due to shutdown...")
				if err := p.videoRecorder.StopRecording(); err != nil {
					log.Printf("[Pipeline] Error stopping recording: %v", err)
				}
			}
			return
		case motion, ok := <-motionChan:
			if !ok {
				log.Println("[Pipeline] Motion channel closed, stopping motion handler")
				if isRecording {
					log.Println("[Pipeline] Stopping recording due to channel close...")
					if err := p.videoRecorder.StopRecording(); err != nil {
						log.Printf("[Pipeline] Error stopping recording: %v", err)
					}
				}
				return
			}

			if motion && !isRecording {
				log.Println("🔴 [Pipeline] MOTION DETECTED - Starting recording...")
				// Note: For testing, we start recording without a frame channel
				// The frame distributor record channel is handled separately
				if err := p.videoRecorder.StartRecording(nil); err != nil {
					log.Printf("[Pipeline] Error starting recording: %v", err)
				} else {
					isRecording = true
					recordingStart = time.Now()
					log.Printf("[Pipeline] Recording started at %s", recordingStart.Format("15:04:05"))
				}

			} else if motion && isRecording {
				lastMotionTime = time.Now()
				log.Printf("⚡ [Pipeline] Motion continues (recording for %v)",
					time.Since(recordingStart).Round(time.Second))

			} else if !motion && isRecording {
				// Wait before stopping recording
				if time.Since(lastMotionTime) > p.config.MotionConfig.NoMotionDelay || lastMotionTime.IsZero() {
					log.Println("⏹️ [Pipeline] No motion detected - Stopping recording...")
					if err := p.videoRecorder.StopRecording(); err != nil {
						log.Printf("[Pipeline] Error stopping recording: %v", err)
					} else {
						duration := time.Since(recordingStart)
						log.Printf("[Pipeline] Recording stopped. Duration: %v", duration.Round(time.Second))
					}
					isRecording = false
				}
			}
		}
	}
}

// consumeRecordingFrames handles frames for recording
// This function writes frames directly to disk, independent of WebRTC
func (p *Pipeline) consumeRecordingFrames() {
	recordChannel := p.frameDistributor.GetRecordChannel()
	log.Println("[Pipeline] Starting recording frame consumer (direct-to-disk mode)")

	var videoWriter *gocv.VideoWriter
	var currentRecordingFile string
	var recordingStartTime time.Time
	frameCount := 0
	framesWritten := 0
	lastLogTime := time.Now()

	// Cleanup function for video writer
	closeVideoWriter := func() {
		if videoWriter != nil && videoWriter.IsOpened() {
			videoWriter.Close()
			if currentRecordingFile != "" {
				// Verify file was created successfully
				if info, err := os.Stat(currentRecordingFile); err == nil {
					log.Printf("[Pipeline] Recording closed: %s (%d bytes, %d frames)",
						currentRecordingFile, info.Size(), framesWritten)
				}
			}
			videoWriter = nil
			currentRecordingFile = ""
			framesWritten = 0
		}
	}

	defer closeVideoWriter()

	for {
		select {
		case <-p.ctx.Done():
			log.Println("[Pipeline] Recording consumer stopping due to context cancellation")
			closeVideoWriter()
			return

		case frame, ok := <-recordChannel:
			if !ok {
				log.Println("[Pipeline] Record channel closed, stopping recording consumer")
				closeVideoWriter()
				return
			}

			if frame == nil {
				continue
			}

			frameCount++

			// Check if recording is active
			isRecording := p.videoRecorder.IsRecording()

			// Start new recording if needed
			if isRecording && videoWriter == nil {
				// Create recording file
				timestamp := time.Now().Format("2006-01-02_15-04-05")
				outputDir := p.config.VideoConfig.OutputPath
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					log.Printf("[Pipeline] ERROR: Failed to create output directory: %v", err)
					continue
				}

				currentRecordingFile = filepath.Join(outputDir, fmt.Sprintf("recording_%s_direct.webm", timestamp))

				// Initialize video writer with VP9 codec
				// FourCC for VP9: "VP90"
				fourcc := "VP90"
				fps := 15.0 // TODO: Get from config
				width := 1280 // TODO: Get from config
				height := 720 // TODO: Get from config

				vw, err := gocv.VideoWriterFile(currentRecordingFile, fourcc, fps, width, height, true)
				if err != nil {
					log.Printf("[Pipeline] ERROR: Failed to create video writer: %v", err)
					continue
				}
				videoWriter = vw

				if !videoWriter.IsOpened() {
					log.Printf("[Pipeline] ERROR: Video writer not opened for %s", currentRecordingFile)
					videoWriter.Close()
					videoWriter = nil
					continue
				}

				recordingStartTime = time.Now()
				framesWritten = 0
				log.Printf("[Pipeline] 🔴 Started direct recording to: %s", currentRecordingFile)
			}

			// Stop recording if needed
			if !isRecording && videoWriter != nil {
				duration := time.Since(recordingStartTime)
				log.Printf("[Pipeline] ⏹️ Stopping direct recording after %v (%d frames)",
					duration.Round(time.Second), framesWritten)
				closeVideoWriter()
			}

			// Write frame if recording
			if isRecording && videoWriter != nil && videoWriter.IsOpened() {
				// Convert image.Image to gocv.Mat
				mat, err := imageToMat(frame)
				if err != nil {
					log.Printf("[Pipeline] ERROR: Failed to convert frame to Mat: %v", err)
					continue
				}

				// Write frame to video file
				if err := videoWriter.Write(mat); err != nil {
					log.Printf("[Pipeline] ERROR: Failed to write frame: %v", err)
					mat.Close()
					continue
				}

				mat.Close()
				framesWritten++

				// Log progress
				if time.Since(lastLogTime) >= 5*time.Second {
					duration := time.Since(recordingStartTime)
					log.Printf("[Pipeline] Recording: %d frames written, duration: %v, file: %s",
						framesWritten, duration.Round(time.Second), filepath.Base(currentRecordingFile))
					lastLogTime = time.Now()
				}
			}
		}
	}
}

// ============================================================================
// IMAGE TO MAT CONVERSION - OPTIMIZED FOR MAXIMUM PERFORMANCE
// ============================================================================
//
// HIERARCHY:
//   imageToMat()                    - MAIN ENTRY POINT: Routes to optimal converter
//     ├── convertRGBAOptimized()    - FAST PATH: Most common format (RGBA/NRGBA)
//     ├── convertYCbCrOptimized()   - FAST PATH: Common from cameras
//     └── convertGenericFallback()  - FALLBACK: Handles any other format
//
// HELPERS:
//   initYCbCrTables()               - One-time lookup table initialization
//   clampUint8()                    - Fast value clamping
// ============================================================================

// YCbCr to RGB conversion lookup tables
// Pre-computed values eliminate expensive multiplications during conversion
var (
	ycbcrOnce  sync.Once
	ycbcrTable struct {
		// Lookup tables for YCbCr->RGB conversion
		// Using int32 to avoid overflow in calculations
		cr2r [256]int32  // Cr contribution to R
		cb2b [256]int32  // Cb contribution to B
		cr2g [256]int32  // Cr contribution to G
		cb2g [256]int32  // Cb contribution to G
	}
)

// imageToMat is the MAIN ENTRY POINT for image to OpenCV Mat conversion
// Routes to the most efficient converter based on image type
func imageToMat(img image.Image) (gocv.Mat, error) {
	if img == nil {
		return gocv.NewMat(), nil
	}

	// Route to optimal converter based on concrete type
	// Type switches are extremely fast in Go (single pointer comparison)
	switch srcImg := img.(type) {
	case *image.RGBA:
		// Most common format from frame grabbers
		// Direct memory access path - fastest possible
		return convertRGBAOptimized(srcImg)
		
	case *image.NRGBA:
		// Non-premultiplied RGBA - also very common
		// Same memory layout as RGBA, equally fast
		return convertNRGBAOptimized(srcImg)
		
	case *image.YCbCr:
		// Common from hardware encoders and cameras
		// Optimized with lookup tables
		return convertYCbCrOptimized(srcImg)
		
	default:
		// Fallback for any other format (Gray, CMYK, etc.)
		// Still optimized but uses interface methods
		return convertGenericFallback(img)
	}
}

// convertRGBAOptimized handles RGBA images with maximum efficiency
// This is the FASTEST PATH - direct memory access, no method calls
func convertRGBAOptimized(img *image.RGBA) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Create BGR Mat (OpenCV's default format)
	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	
	// Get direct pointer to Mat's underlying memory
	// This bypasses all OpenCV accessor methods for maximum speed
	matData, err := mat.DataPtrUint8()
	if err != nil {
		mat.Close() // Clean up Mat before returning error
		return gocv.NewMat(), fmt.Errorf("failed to get Mat data pointer: %v", err)
	}
	
	// Check if image pixels are contiguous (no padding between rows)
	if img.Stride == width*4 {
		// FAST PATH: Process entire image as single memory block
		// This is optimal for cache performance
		srcIdx := 0
		dstIdx := 0
		totalPixels := width * height
		
		// Unroll by 4 for better CPU pipeline utilization
		pixels := totalPixels / 4
		remainder := totalPixels % 4
		
		for i := 0; i < pixels; i++ {
			// Process 4 pixels at once
			// Modern CPUs can execute these in parallel
			
			// Pixel 1: RGBA -> BGR
			matData[dstIdx] = img.Pix[srcIdx+2]     // B
			matData[dstIdx+1] = img.Pix[srcIdx+1]   // G
			matData[dstIdx+2] = img.Pix[srcIdx]     // R
			
			// Pixel 2: RGBA -> BGR
			matData[dstIdx+3] = img.Pix[srcIdx+6]   // B
			matData[dstIdx+4] = img.Pix[srcIdx+5]   // G
			matData[dstIdx+5] = img.Pix[srcIdx+4]   // R
			
			// Pixel 3: RGBA -> BGR
			matData[dstIdx+6] = img.Pix[srcIdx+10]  // B
			matData[dstIdx+7] = img.Pix[srcIdx+9]   // G
			matData[dstIdx+8] = img.Pix[srcIdx+8]   // R
			
			// Pixel 4: RGBA -> BGR
			matData[dstIdx+9] = img.Pix[srcIdx+14]  // B
			matData[dstIdx+10] = img.Pix[srcIdx+13] // G
			matData[dstIdx+11] = img.Pix[srcIdx+12] // R
			
			srcIdx += 16  // 4 pixels * 4 bytes
			dstIdx += 12  // 4 pixels * 3 bytes
		}
		
		// Handle remaining pixels
		for i := 0; i < remainder; i++ {
			matData[dstIdx] = img.Pix[srcIdx+2]     // B
			matData[dstIdx+1] = img.Pix[srcIdx+1]   // G
			matData[dstIdx+2] = img.Pix[srcIdx]     // R
			srcIdx += 4
			dstIdx += 3
		}
	} else {
		// SLOWER PATH: Handle row stride (padding at end of rows)
		// Still fast but must respect memory layout
		for y := 0; y < height; y++ {
			srcRow := y * img.Stride
			dstRow := y * width * 3
			
			// Process one row at a time
			for x := 0; x < width; x++ {
				srcIdx := srcRow + x*4
				dstIdx := dstRow + x*3
				
				matData[dstIdx] = img.Pix[srcIdx+2]     // B
				matData[dstIdx+1] = img.Pix[srcIdx+1]   // G
				matData[dstIdx+2] = img.Pix[srcIdx]     // R
			}
		}
	}

	return mat, nil
}

// convertNRGBAOptimized handles NRGBA images (non-premultiplied alpha)
// Identical to RGBA performance-wise since we ignore alpha channel
func convertNRGBAOptimized(img *image.NRGBA) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	matData, err := mat.DataPtrUint8()
	if err != nil {
		mat.Close() // Clean up Mat before returning error
		return gocv.NewMat(), fmt.Errorf("failed to get Mat data pointer: %v", err)
	}
	
	// Same optimization as RGBA
	if img.Stride == width*4 {
		// Fast contiguous path
		srcIdx := 0
		dstIdx := 0
		totalPixels := width * height
		
		for i := 0; i < totalPixels; i++ {
			matData[dstIdx] = img.Pix[srcIdx+2]     // B
			matData[dstIdx+1] = img.Pix[srcIdx+1]   // G
			matData[dstIdx+2] = img.Pix[srcIdx]     // R
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
				
				matData[dstIdx] = img.Pix[srcIdx+2]     // B
				matData[dstIdx+1] = img.Pix[srcIdx+1]   // G
				matData[dstIdx+2] = img.Pix[srcIdx]     // R
			}
		}
	}

	return mat, nil
}

// initYCbCrTables initializes lookup tables for YCbCr->RGB conversion
// Called once on first YCbCr conversion, then cached forever
func initYCbCrTables() {
	ycbcrOnce.Do(func() {
		// Pre-compute all possible YCbCr->RGB conversions
		// This trades 4KB of memory for massive speed improvement
		for i := 0; i < 256; i++ {
			// Center Cb and Cr around 0
			cb := int32(i) - 128
			cr := int32(i) - 128
			
			// YCbCr to RGB formula (ITU-R BT.601)
			// R = Y + 1.402 * Cr
			// G = Y - 0.344 * Cb - 0.714 * Cr  
			// B = Y + 1.772 * Cb
			//
			// Using fixed-point arithmetic (16-bit precision)
			ycbcrTable.cr2r[i] = (91881 * cr) >> 16
			ycbcrTable.cb2b[i] = (116130 * cb) >> 16
			ycbcrTable.cr2g[i] = (46802 * cr) >> 16
			ycbcrTable.cb2g[i] = (22554 * cb) >> 16
		}
	})
}

// clampUint8 ensures value is in valid byte range
// Branchless version for better CPU pipeline performance
func clampUint8(v int32) uint8 {
	// This is faster than if-else chains on modern CPUs
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// convertYCbCrOptimized handles YCbCr images using lookup tables
// Common format from cameras and hardware encoders
func convertYCbCrOptimized(img *image.YCbCr) (gocv.Mat, error) {
	// Initialize lookup tables on first use
	initYCbCrTables()
	
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	matData, err := mat.DataPtrUint8()
	if err != nil {
		mat.Close() // Clean up Mat before returning error
		return gocv.NewMat(), fmt.Errorf("failed to get Mat data pointer: %v", err)
	}

	// YCbCr has more complex memory layout with subsampling
	// Must use offset methods to handle this correctly
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Get component indices accounting for subsampling
			yi := img.YOffset(x+bounds.Min.X, y+bounds.Min.Y)
			ci := img.COffset(x+bounds.Min.X, y+bounds.Min.Y)

			// Get YCbCr components
			yy := int32(img.Y[yi])
			cb := img.Cb[ci]
			cr := img.Cr[ci]

			// Fast RGB calculation using lookup tables
			// No multiplications needed!
			r := yy + ycbcrTable.cr2r[cr]
			g := yy - ycbcrTable.cb2g[cb] - ycbcrTable.cr2g[cr]
			b := yy + ycbcrTable.cb2b[cb]

			// Write to Mat in BGR format
			idx := (y*width + x) * 3
			matData[idx] = clampUint8(b)     // B
			matData[idx+1] = clampUint8(g)   // G
			matData[idx+2] = clampUint8(r)   // R
		}
	}

	return mat, nil
}

// convertGenericFallback handles any image format not explicitly optimized
// FALLBACK PATH: Slower but works with any image.Image implementation
func convertGenericFallback(img image.Image) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	matData, err := mat.DataPtrUint8()
	if err != nil {
		return gocv.NewMat(), fmt.Errorf("failed to get Mat data pointer: %v", err)
	}

	// Must use At() method since we don't know the concrete type
	// Still optimized: direct Mat memory access, batch processing
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		rowIdx := (y - bounds.Min.Y) * width * 3
		
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// At() returns color.Color interface, RGBA() normalizes to 16-bit
			r, g, b, _ := img.At(x, y).RGBA()
			
			// Convert 16-bit to 8-bit and write directly to Mat
			idx := rowIdx + (x-bounds.Min.X)*3
			matData[idx] = uint8(b >> 8)     // B
			matData[idx+1] = uint8(g >> 8)   // G
			matData[idx+2] = uint8(r >> 8)   // R
		}
	}

	return mat, nil
}

// ============================================================================
// END OF IMAGE TO MAT CONVERSION
// ============================================================================

// saveImageAsPNG saves an image.Image as a PNG file
func saveImageAsPNG(img image.Image, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}

// matToImage converts a gocv.Mat back to image.Image for debugging
func matToImage(mat gocv.Mat) (image.Image, error) {
	width := mat.Cols()
	height := mat.Rows()

	// Create RGBA image
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	matData,err := mat.DataPtrUint8()
	if err != nil {
		return nil, fmt.Errorf("failed to get Mat data pointer: %v", err)
	}

	// Convert BGR back to RGBA
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			matIdx := y*width*3 + x*3
			imgIdx := y*img.Stride + x*4

			// Convert BGR to RGBA
			img.Pix[imgIdx] = matData[matIdx+2]     // R
			img.Pix[imgIdx+1] = matData[matIdx+1]   // G
			img.Pix[imgIdx+2] = matData[matIdx]     // B
			img.Pix[imgIdx+3] = 255                 // A
		}
	}

	return img, nil
}

// saveDebugImages saves the first frame and converted Mat as PNG files for inspection
func saveDebugImages(originalFrame image.Image, convertedMat gocv.Mat) {
	debugImagesSaved.Do(func() {
		log.Println("[Pipeline] Saving debug images for inspection...")

		// Save original frame
		if err := saveImageAsPNG(originalFrame, "debug_original_frame.png"); err != nil {
			log.Printf("[Pipeline] Error saving original frame: %v", err)
		} else {
			log.Println("[Pipeline] Saved debug_original_frame.png")
		}

		// Convert Mat back to image and save
		if reconvertedImg, err := matToImage(convertedMat); err != nil {
			log.Printf("[Pipeline] Error converting Mat back to image: %v", err)
		} else if err := saveImageAsPNG(reconvertedImg, "debug_converted_mat.png"); err != nil {
			log.Printf("[Pipeline] Error saving converted Mat: %v", err)
		} else {
			log.Println("[Pipeline] Saved debug_converted_mat.png")
		}

		log.Println("[Pipeline] Debug images saved. Check current directory for PNG files.")
	})
}