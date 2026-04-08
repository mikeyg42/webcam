// Standalone test for optical flow calibration algorithm
// Usage: go run scripts/test_optical_flow.go <video_file.mp4>
//
// This tests the exact same optical flow code used in calibration
// without needing to start the full application.

package main

import (
	"fmt"
	"image"
	"log"
	"math"
	"os"

	"gocv.io/x/gocv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run scripts/test_optical_flow.go <video_file.mp4>")
		fmt.Println("\nThis tests the optical flow algorithm used in calibration.")
		fmt.Println("Use a video with visible movement to verify detection works.")
		os.Exit(1)
	}

	videoPath := os.Args[1]
	fmt.Printf("Testing optical flow with: %s\n\n", videoPath)

	// Open video
	video, err := gocv.VideoCaptureFile(videoPath)
	if err != nil {
		log.Fatalf("Failed to open video: %v", err)
	}
	defer video.Close()

	// Get video properties
	fps := video.Get(gocv.VideoCaptureFPS)
	width := int(video.Get(gocv.VideoCaptureFrameWidth))
	height := int(video.Get(gocv.VideoCaptureFrameHeight))
	frameCount := int(video.Get(gocv.VideoCaptureFrameCount))

	fmt.Printf("Video: %dx%d @ %.1f fps, %d frames\n\n", width, height, fps, frameCount)

	// Allocate optical flow components (same as calibration service)
	prevSmall := gocv.NewMat()
	currSmall := gocv.NewMat()
	flow := gocv.NewMat()
	tempGray := gocv.NewMat()
	tempSmall := gocv.NewMat()
	magnitude := gocv.NewMat()

	defer prevSmall.Close()
	defer currSmall.Close()
	defer flow.Close()
	defer tempGray.Close()
	defer tempSmall.Close()
	defer magnitude.Close()

	frame := gocv.NewMat()
	defer frame.Close()

	samples := make([]float64, 0, 300)
	frameNum := 0

	for {
		if ok := video.Read(&frame); !ok || frame.Empty() {
			break
		}
		frameNum++

		// Debug: check raw frame data on first few frames
		if frameNum <= 3 {
			fmt.Printf("\n=== RAW FRAME %d ===\n", frameNum)
			fmt.Printf("  Size: %dx%d, Channels: %d, Type: %d\n",
				frame.Cols(), frame.Rows(), frame.Channels(), frame.Type())
			if data, err := frame.DataPtrUint8(); err == nil && len(data) > 100 {
				fmt.Printf("  Raw pixels[0:20]: %v\n", data[0:20])
				// Check if all zeros
				nonZero := 0
				for i := 0; i < len(data) && i < 10000; i++ {
					if data[i] != 0 {
						nonZero++
					}
				}
				fmt.Printf("  Non-zero pixels in first 10000: %d\n", nonZero)
			}
		}

		// Process frame (same algorithm as calibration)
		motionArea := processFrame(frame, &prevSmall, &currSmall, &flow, &tempGray, &tempSmall, &magnitude, frameNum)

		if motionArea >= 0 {
			samples = append(samples, motionArea)
		}

		// Progress indicator
		if frameNum%30 == 0 {
			fmt.Printf("Frame %d: motion=%.4f%%\n", frameNum, motionArea)
		}
	}

	// Calculate results (same as calibration)
	fmt.Printf("\n--- Results ---\n")
	fmt.Printf("Total frames processed: %d\n", frameNum)
	fmt.Printf("Samples collected: %d\n", len(samples))

	if len(samples) > 0 {
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

		// Find min/max
		minVal, maxVal := samples[0], samples[0]
		for _, v := range samples {
			if v < minVal {
				minVal = v
			}
			if v > maxVal {
				maxVal = v
			}
		}

		baseline := mean + stddev
		threshold := baseline + 0.05

		fmt.Printf("\nMotion Statistics:\n")
		fmt.Printf("  Min:      %.4f%%\n", minVal)
		fmt.Printf("  Max:      %.4f%%\n", maxVal)
		fmt.Printf("  Mean:     %.4f%%\n", mean)
		fmt.Printf("  StdDev:   %.4f%%\n", stddev)
		fmt.Printf("  Baseline: %.4f%%\n", baseline)
		fmt.Printf("  Threshold: %.4f%%\n", threshold)

		if maxVal < 0.01 {
			fmt.Printf("\n⚠️  WARNING: Very low motion detected (max < 0.01%%)\n")
			fmt.Printf("   This suggests the optical flow algorithm may not be working correctly,\n")
			fmt.Printf("   or the video has no movement.\n")
		} else if mean > 1.0 {
			fmt.Printf("\n✓ Good motion detection (mean > 1%%)\n")
		}
	}
}

// processFrame - exact copy of calibration service's processFrame
func processFrame(frame gocv.Mat, prevSmall, currSmall, flow, tempGray, tempSmall, magnitude *gocv.Mat, frameNum int) float64 {
	// Debug: check input frame
	if frameNum <= 3 {
		if data, err := frame.DataPtrUint8(); err == nil && len(data) > 20 {
			fmt.Printf("  [processFrame %d] input frame[0:10]: %v (channels=%d)\n",
				frameNum, data[0:10], frame.Channels())
		}
	}

	// Convert to grayscale
	if frame.Channels() > 1 {
		gocv.CvtColor(frame, tempGray, gocv.ColorBGRToGray)
	} else {
		frame.CopyTo(tempGray)
	}

	// Debug: check after grayscale conversion
	if frameNum <= 3 {
		fmt.Printf("  [processFrame %d] tempGray: %dx%d, empty=%v, type=%d\n",
			frameNum, tempGray.Cols(), tempGray.Rows(), tempGray.Empty(), tempGray.Type())
		if data, err := tempGray.DataPtrUint8(); err == nil && len(data) > 20 {
			fmt.Printf("  [processFrame %d] grayscale[0:10]: %v\n", frameNum, data[0:10])
		} else {
			fmt.Printf("  [processFrame %d] grayscale DataPtrUint8 error: %v\n", frameNum, err)
		}
	}

	// Downsample for performance
	// NOTE: PyrDown with explicit size was producing zeros. Use empty size to auto-calculate.
	// Also try Resize as an alternative if PyrDown fails.
	gocv.Resize(*tempGray, tempSmall, image.Point{}, 0.5, 0.5, gocv.InterpolationLinear)

	// Debug: check after downsampling
	if frameNum <= 3 {
		fmt.Printf("  [processFrame %d] tempSmall: %dx%d, empty=%v\n",
			frameNum, tempSmall.Cols(), tempSmall.Rows(), tempSmall.Empty())
		if data, err := tempSmall.DataPtrUint8(); err == nil && len(data) > 20 {
			fmt.Printf("  [processFrame %d] downsampled[0:10]: %v\n", frameNum, data[0:10])
		}
	}

	// Debug: log pixel values every 30 frames
	if frameNum%30 == 0 {
		if data, err := tempSmall.DataPtrUint8(); err == nil && len(data) > 100 {
			fmt.Printf("  [DEBUG] Frame %d grayscale[0:8]: %v (size: %dx%d)\n",
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
				// Check if they're identical
				identical := true
				for i := 0; i < 100; i++ {
					if prevData[i] != currData[i] {
						identical = false
						break
					}
				}
				fmt.Printf("  [DEBUG] prev[0:8]=%v, curr[0:8]=%v, identical=%v\n",
					prevData[0:8], currData[0:8], identical)
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
	motionArea := analyzeFlow(*flow, magnitude)

	// Swap frames for next iteration
	currSmall.CopyTo(prevSmall)

	return motionArea
}

// analyzeFlow - exact copy of calibration service's analyzeFlow
func analyzeFlow(flow gocv.Mat, magnitude *gocv.Mat) float64 {
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
