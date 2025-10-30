package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"os/signal"

	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/driver"

	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/api"
	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/framestream"
	"github.com/mikeyg42/webcam/internal/gui"
	"github.com/mikeyg42/webcam/internal/integration"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/notification"
	"github.com/mikeyg42/webcam/internal/recorder"
	"github.com/mikeyg42/webcam/internal/recorder/adapter"
	"github.com/mikeyg42/webcam/internal/recorder/recorderlog"
	"github.com/mikeyg42/webcam/internal/rtcManager"
	"github.com/mikeyg42/webcam/internal/validate"

	_ "github.com/pion/mediadevices/pkg/driver/camera"
	_ "github.com/pion/mediadevices/pkg/driver/microphone"
)

// Application holds all system components
type Application struct {
	config             *config.Config
	webrtcManager      *rtcManager.Manager
	motionDetector     *motion.Detector
	recorderService    *recorder.RecordingService
	wsConnection       *websocket.Conn
	notifier           *notification.Notifier
	frameDistributor   *framestream.FrameDistributor
	pipeline           *integration.Pipeline
	selectedCamera     mediadevices.MediaDeviceInfo
	selectedMicrophone mediadevices.MediaDeviceInfo
	testingMode        bool
	ctx                context.Context
	cancel             context.CancelFunc
	drainDoneChannel   chan struct{} // Signal to stop draining channels
	logger             recorderlog.Logger
}

// CalibrationResult holds calibration values
type CalibrationResult struct {
	Baseline  float64
	Threshold float64
}

func main() {
	// Create root context for lifecycle management
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Load and validate configuration
	cfg := config.NewDefaultConfig()
	if err := validate.ValidateConfig(cfg); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	// Parse command line flags
	var (
		debugMode   bool
		testingMode bool
	)
	flag.StringVar(&cfg.WebSocketAddr, "addr", cfg.WebSocketAddr, "WebSocket server address")
	flag.BoolVar(&debugMode, "debug", false, "Enable debug mode")
	flag.BoolVar(&testingMode, "testing", false, "Testing mode (skip WebRTC)")
	flag.Parse()

	// Create application instance
	app, err := NewApplication(ctx, cfg, testingMode)
	if err != nil {
		log.Fatalf("Failed to create application: %v", err)
	}
	defer app.Cleanup()

	// Initialize all components
	if err := app.Initialize(); err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}

	// Start API server for configuration management
	apiServer := api.NewServer(cfg, ":8081")
	apiServer.StartInBackground()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		apiServer.Shutdown(shutdownCtx)
	}()

	// Run calibration phase
	calibration := app.runCalibrationPhase()

	// Apply calibration to detector
	app.motionDetector.SetCalibration(calibration.Baseline, calibration.Threshold)

	// Wait for user to start detection
	fmt.Println("\n================================")
	fmt.Println("CALIBRATION COMPLETE")
	fmt.Println("================================")
	fmt.Print("Press ENTER to start motion detection: ")
	bufio.NewReader(os.Stdin).ReadString('\n')

	// Start main processing
	errChan := make(chan error, 1)
	go func() {
		errChan <- app.startProcessing()
	}()

	// Wait for shutdown signal
	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			log.Printf("Application error: %v", err)
		}
	case sig := <-sigChan:
		log.Printf("Received signal: %v", sig)
	case <-ctx.Done():
		log.Printf("Context cancelled")
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
}

// NewApplication creates a new application instance
func NewApplication(ctx context.Context, cfg *config.Config, testingMode bool) (*Application, error) {
	appCtx, cancel := context.WithCancel(ctx)

	// Create logger for recorder service
	logger := recorderlog.NewStdLogger().Named("security-camera")

	// Setup email notifications
	notifier, err := setupEmailNotifications(appCtx, cfg)
	if err != nil {
		log.Printf("Email setup failed: %v", err)
		notifier = nil
	}

	// Create motion detector
	motionDetector, err := motion.NewDetector(&cfg.MotionConfig, notifier)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create motion detector: %v", err)
	}

	// Create recorder config
	recorderCfg, err := adapter.CreateRecorderConfig(cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create recorder config: %w", err)
	}

	// Validate recorder config
	if err := adapter.ValidateConfig(recorderCfg); err != nil {
		cancel()
		return nil, fmt.Errorf("invalid recorder config: %w", err)
	}

	// Create recorder service with real storage
	logger.Info("Initializing recording service with MinIO and PostgreSQL")
	recorderService, err := recorder.NewRecordingService(recorderCfg, logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create recorder service: %w", err)
	}

	var wsConn *websocket.Conn
	var webrtcManager *rtcManager.Manager

	if !testingMode {
		// Connect to WebSocket server
		wsURL := fmt.Sprintf("ws://%s/ws?roomId=cameraRoom", cfg.WebSocketAddr)
		log.Printf("Connecting to WebSocket: %s", wsURL)

		dialer := websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
		}
		wsConn, _, err = dialer.Dial(wsURL, nil)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("WebSocket connection failed: %v", err)
		}
		log.Println("WebSocket connected")

		// Note: WebRTC manager no longer needs videoRecorder
		// (recording is handled independently by recorderService)
		webrtcManager, err = rtcManager.NewManager(appCtx, cfg, wsConn, nil)
		if err != nil {
			cancel()
			wsConn.Close()
			return nil, fmt.Errorf("failed to create WebRTC manager: %v", err)
		}
	}

	app := &Application{
		config:          cfg,
		ctx:             appCtx,
		cancel:          cancel,
		motionDetector:  motionDetector,
		recorderService: recorderService,
		notifier:        notifier,
		wsConnection:    wsConn,
		webrtcManager:   webrtcManager,
		testingMode:     testingMode,
		logger:          logger,
	}

	// Wire motion detector callback to recorder service
	motionDetector.SetMotionCallback(func(confidence float64, regions []image.Rectangle, timestamp time.Time) {
		// Convert to recorder's MotionEvent format
		app.recorderService.HandleMotionEvent(recorder.MotionEvent{
			Timestamp:  timestamp,
			Confidence: confidence,
			Regions:    regions,
		})
	})

	logger.Info("Application initialized successfully")
	return app, nil
}

// Initialize sets up all components
func (app *Application) Initialize() error {
	log.Println("Initializing application...")

	var codecSelector *mediadevices.CodecSelector
	var camera, microphone mediadevices.MediaDeviceInfo
	var err error

	if app.testingMode {
		// Testing mode - basic setup
		codecSelector = &mediadevices.CodecSelector{}
		camera, microphone, err = app.selectDevicesForTesting()
	} else {
		// Production mode - full WebRTC setup
		codecSelector, err = app.webrtcManager.Initialize()
		if err != nil {
			return fmt.Errorf("WebRTC initialization failed: %v", err)
		}

		camera, microphone, err = app.selectDevices(codecSelector)
	}

	if err != nil {
		return fmt.Errorf("device selection failed: %v", err)
	}

	app.selectedCamera = camera
	app.selectedMicrophone = microphone

	// Create frame distributor
	app.frameDistributor, err = framestream.NewFrameDistributor(app.ctx, camera, codecSelector)
	if err != nil {
		return fmt.Errorf("failed to create frame distributor: %v", err)
	}

	// Create integration pipeline
	app.pipeline = integration.NewPipeline(app.ctx, app.config, app.frameDistributor,
		app.motionDetector, app.recorderService)

	// Setup WebRTC signaling
	if !app.testingMode && app.webrtcManager != nil {
		if err := app.webrtcManager.SetupSignaling(); err != nil {
			return fmt.Errorf("signaling setup failed: %v", err)
		}
	}

	log.Println("Initialization complete")
	return nil
}

// runCalibrationPhase performs optical flow calibration
func (app *Application) runCalibrationPhase() CalibrationResult {
	fmt.Println("\n================================")
	fmt.Println("MOTION DETECTION CALIBRATION")
	fmt.Println("================================")
	fmt.Println("Please ensure the camera view is EMPTY")
	fmt.Println("No people, pets, or moving objects should be visible")
	fmt.Print("\nPress ENTER when ready to calibrate: ")

	bufio.NewReader(os.Stdin).ReadString('\n')

	log.Println("Starting 10-second calibration...")

	// Start frame distributor at FULL resolution (1280x720)
	// It will stay running through calibration and into production
	if err := app.frameDistributor.Start(1280, 720); err != nil {
		log.Fatalf("Failed to start frame distributor: %v", err)
	}

	// Create channels for calibration
	calibChan := make(chan gocv.Mat, 30)
	doneChan := make(chan CalibrationResult, 1)

	// Drain other channels during calibration
	drainDone := make(chan struct{})
	go app.drainChannelsDuringCalibration(drainDone)

	// Run calibration processor
	go app.runCalibrationProcessor(calibChan, doneChan)

	// Feed frames to calibration
	go app.feedCalibrationFrames(calibChan)

	// Wait for calibration result
	result := <-doneChan

	// Keep draining running - don't stop it yet!
	// It will be stopped when the pipeline starts consuming frames
	app.drainDoneChannel = drainDone // Store for later

	log.Printf("Calibration complete - Baseline: %.4f%%, Threshold: %.4f%%",
		result.Baseline, result.Threshold)

	return result
}

// drainChannelsDuringCalibration prevents channel overflow
// Runs until pipeline is ready to consume frames
func (app *Application) drainChannelsDuringCalibration(done chan struct{}) {
	vp9Chan := app.frameDistributor.GetVP9Channel()
	recordChan := app.frameDistributor.GetRecordChannel()

	for {
		select {
		case <-vp9Chan:
			// Drain VP9 frames silently
		case <-recordChan:
			// Drain record frames silently
		case <-done:
			// Pipeline started, stop draining
			log.Println("[Calibration] Stopping channel drain - pipeline taking over")
			return
		case <-app.ctx.Done():
			return
		}
	}
}

// feedCalibrationFrames converts and feeds frames for calibration
func (app *Application) feedCalibrationFrames(calibChan chan<- gocv.Mat) {
	motionChan := app.frameDistributor.GetMotionChannel()
	timeout := time.After(11 * time.Second)

	defer close(calibChan)

	for {
		select {
		case frame := <-motionChan:
			if frame == nil {
				continue
			}
			
			// Convert to Mat
			mat, err := imageToMat(frame)
			if err != nil {
				continue
			}

			// Non-blocking send
			select {
			case calibChan <- mat:
			default:
				mat.Close()
			}

		case <-timeout:
			return
		case <-app.ctx.Done():
			return
		}
	}
}

// runCalibrationProcessor performs the actual calibration
func (app *Application) runCalibrationProcessor(calibChan <-chan gocv.Mat, doneChan chan<- CalibrationResult) {
	// Optical flow calibration components
	var (
		prevSmall = gocv.NewMat()
		currSmall = gocv.NewMat()
		flow      = gocv.NewMat()
		tempGray  = gocv.NewMat()
		tempSmall = gocv.NewMat()
		magnitude = gocv.NewMat()
	)

	// Clean up when done
	defer func() {
		prevSmall.Close()
		currSmall.Close()
		flow.Close()
		tempGray.Close()
		tempSmall.Close()
		magnitude.Close()
	}()

	samples := make([]float64, 0, 150)
	startTime := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case frame, ok := <-calibChan:
			if !ok {
				// Channel closed, calculate result
				result := calculateCalibrationResult(samples)
				doneChan <- result
				return
			}

			// Process frame with optical flow
			motionArea := processCalibrationFrame(frame, &prevSmall, &currSmall, 
				&flow, &tempGray, &tempSmall, &magnitude)
			frame.Close()

			if motionArea >= 0 {
				samples = append(samples, motionArea)
			}

		case <-ticker.C:
			elapsed := time.Since(startTime).Seconds()
			remaining := 10.0 - elapsed
			if remaining > 0 {
				log.Printf("[Calibration] %.0f seconds remaining...", remaining)
			}

		case <-time.After(10 * time.Second):
			// Timeout - calculate result
			result := calculateCalibrationResult(samples)
			doneChan <- result
			return
		}
	}
}

// processCalibrationFrame analyzes a single frame during calibration
func processCalibrationFrame(frame gocv.Mat, prevSmall, currSmall, flow, 
	tempGray, tempSmall, magnitude *gocv.Mat) float64 {

	// Convert to grayscale
	if frame.Channels() > 1 {
		gocv.CvtColor(frame, tempGray, gocv.ColorBGRToGray)
	} else {
		frame.CopyTo(tempGray)
	}

	// Downsample
	size := image.Pt(tempGray.Cols()/2, tempGray.Rows()/2)
	gocv.PyrDown(*tempGray, tempSmall, size, gocv.BorderDefault)

	// First frame
	if prevSmall.Empty() {
		tempSmall.CopyTo(prevSmall)
		return -1
	}

	// Copy current
	tempSmall.CopyTo(currSmall)

	// Calculate optical flow
	gocv.CalcOpticalFlowFarneback(
		*prevSmall, *currSmall, flow,
		0.5, 3, 15, 3, 5, 1.2,
		gocv.OptflowFarnebackGaussian,
	)

	// Analyze flow
	motionArea := analyzeCalibrationFlow(*flow, magnitude)

	// Swap frames
	currSmall.CopyTo(prevSmall)

	return motionArea
}

// analyzeCalibrationFlow computes motion area from optical flow
func analyzeCalibrationFlow(flow gocv.Mat, magnitude *gocv.Mat) float64 {
	if flow.Empty() {
		return 0
	}

	flowChannels := gocv.Split(flow)
	defer func() {
		for _, ch := range flowChannels {
			ch.Close()
		}
	}()

	if len(flowChannels) < 2 {
		return 0
	}

	// Calculate magnitude
	gocv.Magnitude(flowChannels[0], flowChannels[1], magnitude)

	// Create motion mask
	mask := gocv.NewMat()
	defer mask.Close()
	gocv.Threshold(*magnitude, &mask, float32(0.3), 255, gocv.ThresholdBinary)

	// Convert to uint8
	maskU8 := gocv.NewMat()
	defer maskU8.Close()
	mask.ConvertTo(&maskU8, gocv.MatTypeCV8U)

	// Calculate motion area percentage
	motionPixels := gocv.CountNonZero(maskU8)
	totalPixels := magnitude.Rows() * magnitude.Cols()

	if totalPixels > 0 {
		return float64(motionPixels) * 100.0 / float64(totalPixels)
	}

	return 0
}

// calculateCalibrationResult computes baseline and threshold from samples
func calculateCalibrationResult(samples []float64) CalibrationResult {
	if len(samples) < 10 {
		log.Printf("Warning: Only %d calibration samples collected", len(samples))
		return CalibrationResult{
			Baseline:  0.5,
			Threshold: 1.0,
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

	log.Printf("[Calibration] Samples: %d, Mean: %.4f, StdDev: %.4f", 
		len(samples), mean, stddev)

	return CalibrationResult{
		Baseline:  baseline,
		Threshold: threshold,
	}
}

// startProcessing begins main application processing
func (app *Application) startProcessing() error {
	log.Println("Starting main processing...")

	// Frame distributor is ALREADY running from calibration phase!
	// Don't start it again - that would cause "panic: close of closed channel"

	// Start recorder service
	if err := app.recorderService.Start(app.ctx); err != nil {
		return fmt.Errorf("failed to start recorder service: %v", err)
	}
	app.logger.Info("Recording service started")

	// Start motion detector
	if err := app.motionDetector.Start(); err != nil {
		return fmt.Errorf("failed to start motion detector: %v", err)
	}

	// Start integration pipeline
	if err := app.pipeline.Start(); err != nil {
		return fmt.Errorf("failed to start pipeline: %v", err)
	}

	// NOW stop the drain goroutine - pipeline is consuming frames
	if app.drainDoneChannel != nil {
		close(app.drainDoneChannel)
		app.drainDoneChannel = nil
	}

	log.Println("All systems running")

	// Wait for context cancellation
	<-app.ctx.Done()
	return app.ctx.Err()
}

// Shutdown performs graceful shutdown
func (app *Application) Shutdown(ctx context.Context) error {
	log.Println("Shutting down...")

	done := make(chan struct{})

	go func() {
		// Stop components in reverse order
		if app.pipeline != nil {
			app.pipeline.Stop()
		}

		if app.frameDistributor != nil {
			app.frameDistributor.Stop()
		}

		if app.motionDetector != nil {
			app.motionDetector.Close()
		}

		if app.recorderService != nil {
			if err := app.recorderService.Stop(); err != nil {
				log.Printf("Error stopping recorder service: %v", err)
			}
		}

		if app.wsConnection != nil {
			app.wsConnection.Close()
		}

		if app.webrtcManager != nil {
			app.webrtcManager.Cleanup()
		}

		close(done)
	}()

	select {
	case <-done:
		log.Println("Shutdown complete")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown timeout")
	}
}

// Cleanup releases resources
func (app *Application) Cleanup() {
	app.cancel()
}

// selectDevices selects camera and microphone (simplified for brevity)
func (app *Application) selectDevices(codecSelector *mediadevices.CodecSelector) (
	camera, microphone mediadevices.MediaDeviceInfo, err error) {
	
	// Using hardcoded devices as in your original
	camera = mediadevices.MediaDeviceInfo{
		DeviceID:   "hardcoded-camera-0",
		Label:      "0x83330001d6c0103",
		Kind:       mediadevices.VideoInput,
		DeviceType: driver.Camera,
	}

	microphone = mediadevices.MediaDeviceInfo{
		DeviceID:   "hardcoded-microphone-2",
		Label:      "4170706c65555342417564696f456e67696e653a4d61727368616c6c20456c656374726f6e69637320202020203a4d584c203939302055534220202020202020202020202020203a383333343030303a31",
		Kind:       mediadevices.AudioInput,
		DeviceType: driver.Microphone,
	}

	return camera, microphone, nil
}

// selectDevicesForTesting selects devices in testing mode
func (app *Application) selectDevicesForTesting() (
	mediadevices.MediaDeviceInfo, mediadevices.MediaDeviceInfo, error) {
	
	devices := mediadevices.EnumerateDevices()

	var camera, microphone mediadevices.MediaDeviceInfo
	var cameraFound, micFound bool

	for _, device := range devices {
		if device.Kind == mediadevices.VideoInput && !cameraFound {
			camera = device
			cameraFound = true
		}
		if device.Kind == mediadevices.AudioInput && !micFound {
			microphone = device
			micFound = true
		}

		if cameraFound && micFound {
			break
		}
	}

	if !cameraFound {
		return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{}, 
			fmt.Errorf("no camera found")
	}

	return camera, microphone, nil
}

// setupEmailNotifications configures email alerts from main config
func setupEmailNotifications(ctx context.Context, cfg *config.Config) (*notification.Notifier, error) {
	switch cfg.EmailMethod {
	case "mailersend":
		// Create MailerSend notifier
		mailsendCfg := &notification.MailSendConfig{
			APIToken:  cfg.MailSendConfig.APIToken,
			ToEmail:   cfg.MailSendConfig.ToEmail,
			FromEmail: cfg.MailSendConfig.FromEmail,
			Debug:     cfg.MailSendConfig.Debug,
		}

		notifier, err := notification.NewMailSendNotifier(mailsendCfg, "WebCam Security")
		if err != nil {
			return nil, fmt.Errorf("failed to create MailerSend notifier: %w", err)
		}
		var n notification.Notifier = notifier
		log.Println("Email notifications enabled (MailerSend)")
		return &n, nil

	case "gmail":
		// Create Gmail OAuth2 notifier
		gmailCfg := &notification.GmailConfig{
			ClientID:           cfg.GmailOAuth2Config.ClientID,
			ClientSecret:       cfg.GmailOAuth2Config.ClientSecret,
			ToEmail:            cfg.GmailOAuth2Config.ToEmail,
			FromEmail:          cfg.GmailOAuth2Config.FromEmail,
			RedirectURL:        cfg.GmailOAuth2Config.RedirectURL,
			TokenStorePath:     cfg.GmailOAuth2Config.TokenStorePath,
			TokenEncryptionKey: cfg.GmailOAuth2Config.TokenEncryptionKey,
			Debug:              cfg.GmailOAuth2Config.Debug,
		}

		notifier, err := notification.NewGmailNotifier(ctx, gmailCfg, "WebCam Security")
		if err != nil {
			return nil, fmt.Errorf("failed to create Gmail notifier: %w", err)
		}
		var n notification.Notifier = notifier
		log.Println("Email notifications enabled (Gmail OAuth2)")
		return &n, nil

	case "disabled":
		log.Println("Email notifications disabled")
		return nil, nil

	default:
		log.Printf("Unknown email method '%s', notifications disabled", cfg.EmailMethod)
		return nil, nil
	}
}

// imageToMat converts image.Image to gocv.Mat
func imageToMat(img image.Image) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)

	// Simple conversion - can be optimized further
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			mat.SetUCharAt(y, x*3+2, uint8(r>>8))
			mat.SetUCharAt(y, x*3+1, uint8(g>>8))
			mat.SetUCharAt(y, x*3, uint8(b>>8))
		}
	}

	return mat, nil
}

// decodeFriendlyDeviceName converts hex-encoded labels
func decodeFriendlyDeviceName(label string) string {
	if decoded, err := hex.DecodeString(label); err == nil {
		return string(decoded)
	}
	return label
}