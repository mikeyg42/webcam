package integration

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"sync"
	"time"

	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/framestream"
	"github.com/mikeyg42/webcam/internal/imgconv"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/recorder"
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
	recorderService  *recorder.RecordingService
	webrtcManager    interface{} // Optional - nil if not using WebRTC
}

// NewPipeline creates a new integration pipeline
func NewPipeline(ctx context.Context, cfg *config.Config, frameDistributor *framestream.FrameDistributor,
	motionDetector *motion.Detector, recorderService *recorder.RecordingService, webrtcManager interface{}) *Pipeline {

	pipelineCtx, cancel := context.WithCancel(ctx)

	return &Pipeline{
		ctx:              pipelineCtx,
		cancel:           cancel,
		config:           cfg,
		frameDistributor: frameDistributor,
		motionDetector:   motionDetector,
		recorderService:  recorderService,
		webrtcManager:    webrtcManager,
	}
}

// Start begins all pipeline connections
func (p *Pipeline) Start() error {
	log.Println("[Pipeline] Starting integration pipeline")

	// Start WebRTC frame consumer (testing mode - just consumes frames)
	go p.consumeWebRTCFrames()

	// Start motion detection pipeline
	go p.runMotionDetection()

	// Start recording frame consumer (feeds frames to recorder service)
	go p.consumeRecordingFrames()

	log.Println("[Pipeline] All pipeline connections started")
	return nil
}

// Stop stops all pipeline connections
func (p *Pipeline) Stop() {
	log.Println("[Pipeline] Stopping integration pipeline")
	p.cancel()
}

// consumeWebRTCFrames handles H.264 WebRTC channel consumption
// Note: WebRTC now gets frames directly from the media stream via rtcManager
// This consumer just prevents the channel from blocking
func (p *Pipeline) consumeWebRTCFrames() {
	webrtcChannel := p.frameDistributor.GetWebRTCChannel()
	log.Println("[Pipeline] Starting H.264 WebRTC channel consumer (WebRTC streams directly from media tracks)")

	frameCount := 0
	lastLogTime := time.Now()

	for {
		select {
		case <-p.ctx.Done():
			log.Println("[Pipeline] H.264 WebRTC consumer stopping due to context cancellation")
			return
		case frame, ok := <-webrtcChannel:
			if !ok {
				log.Println("[Pipeline] H.264 WebRTC channel closed, stopping consumer")
				return
			}

			if frame != nil {
				frameCount++

				// Log progress every 5 seconds
				if time.Since(lastLogTime) >= 5*time.Second {
					bounds := frame.Bounds()
					log.Printf("[Pipeline] H.264 frames flowing (%d processed, %dx%d) - WebRTC active",
						frameCount, bounds.Dx(), bounds.Dy())
					lastLogTime = time.Now()
				}

				// Frames are consumed to prevent channel blocking
				// WebRTC gets its frames directly from the media stream (H.264 hardware accelerated)
			}
		}
	}
}

// runMotionDetection handles motion detection pipeline
func (p *Pipeline) runMotionDetection() {
	motionChannel := p.frameDistributor.GetMotionChannel()
	log.Println("[Pipeline] Motion detection pipeline ready (waiting for detector to start)")

	// Create persistent channels for motion detection with larger buffers
	frameChan := make(chan gocv.Mat, 200)   // Larger buffer to handle processing delays
	motionChan := make(chan bool, 50)

	// Start motion detector in goroutine - but wait until it's actually started
	go func() {
		// NOTE: Don't close motionChan here - Detect() already closes it

		// Wait for detector to be started via GUI
		log.Println("[Pipeline] Waiting for motion detector to be calibrated and started via GUI...")
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-p.ctx.Done():
				log.Println("[Pipeline] Context cancelled while waiting for detector start")
				return
			case <-ticker.C:
				if p.motionDetector.IsRunning() {
					log.Println("[Pipeline] Motion detector started - beginning Detect() loop")
					p.motionDetector.Detect(frameChan, motionChan)
					log.Println("[Pipeline] Motion detector stopped")
					return
				}
			}
		}
	}()

	// Motion events are handled via callback to recorder service
	// (configured in main.go via motionDetector.SetMotionCallback)
	// Just consume the motionChan to prevent blocking
	go func() {
		for range motionChan {
			// Events already sent to recorder via callback
		}
	}()

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
				// Only process frames if detector is actually running
				if !p.motionDetector.IsRunning() {
					// Detector not started yet - just discard frames silently
					continue
				}

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

// consumeRecordingFrames feeds frames to the recorder service
// The recorder service handles encoding, segmentation, and storage
func (p *Pipeline) consumeRecordingFrames() {
	recordChannel := p.frameDistributor.GetRecordChannel()
	log.Println("[Pipeline] Starting recording frame consumer (new recorder service)")

	frameCount := 0
	lastLogTime := time.Now()

	for {
		select {
		case <-p.ctx.Done():
			log.Println("[Pipeline] Recording consumer stopping due to context cancellation")
			return

		case frame, ok := <-recordChannel:
			if !ok {
				log.Println("[Pipeline] Record channel closed, stopping recording consumer")
				return
			}

			if frame == nil {
				continue
			}

			frameCount++

			// Send frame to recorder service
			if err := p.recorderService.HandleFrame(frame, time.Now()); err != nil {
				log.Printf("[Pipeline] ERROR: Failed to handle frame: %v", err)
			}

			// Log progress every 5 seconds
			if time.Since(lastLogTime) >= 5*time.Second {
				log.Printf("[Pipeline] Processed %d recording frames", frameCount)
				lastLogTime = time.Now()
			}
		}
	}
}

// imageToMat is the MAIN ENTRY POINT for image to OpenCV Mat conversion
// Routes to the most efficient converter based on image type
func imageToMat(img image.Image) (gocv.Mat, error) {
	if img == nil {
		return gocv.NewMat(), nil
	}
	return imgconv.ToMat(img)
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