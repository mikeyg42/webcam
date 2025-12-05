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
	"path/filepath"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/driver"

	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/api"
	"github.com/mikeyg42/webcam/internal/calibration"
	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/database"
	"github.com/mikeyg42/webcam/internal/encoder"
	"github.com/mikeyg42/webcam/internal/framestream"
	"github.com/mikeyg42/webcam/internal/imgconv"

	"github.com/mikeyg42/webcam/internal/integration"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/notification"
	"github.com/mikeyg42/webcam/internal/permissions"
	"github.com/mikeyg42/webcam/internal/quality"
	"github.com/mikeyg42/webcam/internal/recorder"
	"github.com/mikeyg42/webcam/internal/recorder/recorderlog"
	"github.com/mikeyg42/webcam/internal/rtcManager"
	"github.com/mikeyg42/webcam/internal/tailscale"
	"github.com/mikeyg42/webcam/internal/validate"

	// call these without the "_" identifier so that we can call methods from these drivers
	"github.com/pion/mediadevices/pkg/driver/camera"
	"github.com/pion/mediadevices/pkg/driver/microphone"
)

// Application holds all system components
type Application struct {
	config             *config.Config
	webrtcManager      *rtcManager.Manager
	motionDetector     *motion.Detector
	recorderService    *recorder.RecordingService
	calibrationService *calibration.Service
	wsConnection       *websocket.Conn
	notifier           *notification.Notifier
	frameDistributor   *framestream.FrameDistributor
	gstPipeline        *encoder.GStreamerPipeline // GStreamer H.264 encoder
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

	// On macOS, ensure we have camera and microphone permissions before proceeding
	// Check permissions FIRST
	log.Println("Checking camera and microphone permissions...")
	if err := permissions.EnsureMediaPermissions(); err != nil {
		log.Fatalf("Failed to obtain media permissions: %v", err)
	}
	log.Println("Media permissions granted")

	// NOW re-initialize the drivers to discover devices AFTER permissions granted
	log.Println("Re-initializing camera and microphone drivers...")
	camera.Initialize()     // Re-enumerate camera devices
	microphone.Initialize() // Re-enumerate microphone devices
	log.Println("Drivers re-initialized with proper permissions")

	// Poll for devices with exponential backoff instead of fixed delay (faster on modern systems)
	var devices []mediadevices.MediaDeviceInfo
	for i := 0; i < 5; i++ {
		devices = mediadevices.EnumerateDevices()
		if len(devices) > 0 {
			log.Printf("Devices available after initialization: %d", len(devices))
			break
		}
		if i < 4 {
			delay := 20 * time.Millisecond * time.Duration(1<<uint(i)) // Exponential backoff: 20, 40, 80, 160ms
			time.Sleep(delay)
		}
	}
	if len(devices) == 0 {
		log.Fatal("No devices found even after permissions granted. Please check system settings.")
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Load and validate configuration
	// Try to load saved config, fall back to defaults if not found
	cfg := config.NewDefaultConfig()

	// Determine config file path
	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Printf("Warning: Could not determine home directory: %v", err)
		} else {
			configFile = filepath.Join(homeDir, ".webcam2", "config.json")
			// Ensure config directory exists
			configDir := filepath.Dir(configFile)
			if err := os.MkdirAll(configDir, 0755); err != nil {
				log.Printf("Warning: Could not create config directory: %v", err)
			}
		}
	}

	// Load saved config if it exists
	if configFile != "" {
		if savedCfg, err := config.LoadConfig(configFile); err == nil {
			cfg = savedCfg
			log.Printf("✓ Loaded configuration from %s", configFile)
		} else {
			log.Printf("Using default configuration (no saved config found: %v)", err)
		}
	}

	if err := validate.ValidateConfig(cfg); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	// Parse command line flags
	var (
		debugMode   bool
		testingMode bool
	)
	flag.StringVar(&cfg.WebSocket.ListenAddr, "addr", cfg.WebSocket.ListenAddr, "WebSocket server address")
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

	// Initialize credential database for encrypted storage
	var credDB *database.DB
	var tsManager *tailscale.TailscaleManager
	homeDir, err := os.UserHomeDir()
	if err == nil {
		dbPath := filepath.Join(homeDir, ".webcam2", "credentials.db")
		credDB, err = database.NewDB(dbPath)
		if err != nil {
			log.Printf("Warning: Could not initialize credential database: %v", err)
			credDB = nil
		} else {
			log.Printf("✓ Credential database initialized at %s", dbPath)
			defer credDB.Close()
		}
	} else {
		log.Printf("Warning: Could not determine home directory for credential database: %v", err)
	}

	// Initialize Tailscale manager if enabled
	if cfg.Tailscale.Enabled {
		tsManager, err = tailscale.NewTailscaleManager(ctx, &cfg.Tailscale)
		if err != nil {
			log.Printf("Warning: Could not initialize Tailscale manager: %v", err)
			tsManager = nil
		} else {
			log.Printf("✓ Tailscale manager initialized for user authentication")
		}
	}

	// Start API server AFTER Initialize() completes to avoid race conditions
	// API endpoints need fully initialized components (calibrationService, motionDetector, frameDistributor)
	apiServer := api.NewServer(ctx, cfg, ":8081", app.calibrationService, app.motionDetector, app.frameDistributor, credDB, tsManager)
	apiServer.StartInBackground()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		apiServer.Shutdown(shutdownCtx)
	}()

	// Wire quality priority callback to allow runtime updates from API
	if app.webrtcManager != nil && app.webrtcManager.ConnectionDoctor != nil {
		configHandler := apiServer.GetConfigHandler()
		configHandler.SetQualityPriorityCallback(func(priorityStr string) error {
			qm := app.webrtcManager.ConnectionDoctor.GetQualityManager()
			if qm == nil {
				return fmt.Errorf("quality manager not initialized")
			}

			priority, err := quality.ParsePriority(priorityStr)
			if err != nil {
				return fmt.Errorf("invalid priority: %v", err)
			}

			qm.SetPriority(priority)
			return nil
		})
		log.Println("[Main] Quality priority callback wired to config API")

		// Register quality metrics API endpoint
		apiServer.SetQualityHandler(app.webrtcManager.ConnectionDoctor)
	}

	// DON'T start frame distributor yet - it will be started when user clicks "Calibrate Camera"
	// This prevents the camera from running before calibration

	// Start main processing (but nothing will happen until calibration starts the camera)
	log.Println("\n================================")
	log.Println("SECURITY CAMERA READY")
	log.Println("================================")
	log.Println("Web GUI available at: http://localhost:8081")
	log.Println("Video viewer: http://localhost:3000")
	log.Println("Camera is OFF - click 'Calibrate Camera' in the web interface to begin")
	log.Println("================================\n")

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

	// Use channels to collect results from concurrent initialization
	type initResult struct {
		name  string
		err   error
		value interface{}
	}
	resultChan := make(chan initResult, 3) // 3 concurrent tasks: motionDetector+notifier, recorderService, webrtc

	// Start concurrent initialization tasks

	// 1. Setup email notifications + motion detector (sequential due to dependency, but async from other tasks)
	go func() {
		// Setup notifier first
		notifier, err := setupEmailNotifications(appCtx, cfg)
		if err != nil {
			log.Printf("Email setup failed: %v", err)
			notifier = nil
		}

		// Create motion detector with notifier
		motionDetector, err := motion.NewDetector(&cfg.Motion, notifier)
		if err != nil {
			resultChan <- initResult{name: "motionDetector", err: err, value: nil}
			return
		}

		// Return both in a single result
		resultChan <- initResult{name: "motionDetector", err: nil, value: map[string]interface{}{
			"detector": motionDetector,
			"notifier": notifier,
		}}
	}()

	// 3. Create recorder service with MinIO/PostgreSQL (async - this is the slow one)
	go func() {
		logger.Info("Initializing recording service with MinIO and PostgreSQL (async)")
		recorderService, err := recorder.NewRecordingService(cfg, logger)
		if err != nil {
			log.Printf("Failed to initialize recorder service: %v", err)
		}
		resultChan <- initResult{name: "recorderService", err: err, value: recorderService}
	}()

	// 4. WebSocket and WebRTC setup (async if not testing mode)
	go func() {
		if testingMode {
			resultChan <- initResult{name: "webrtc", err: nil, value: nil}
			return
		}

		// Connect to WebSocket server
		addr := cfg.WebSocket.ListenAddr
		if len(addr) > 0 && addr[0] == ':' {
			addr = "localhost" + addr
		}
		wsURL := fmt.Sprintf("ws://%s/ws?roomId=cameraRoom", addr)
		log.Printf("Connecting to WebSocket: %s", wsURL)

		dialer := websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
		}
		wsConn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			resultChan <- initResult{name: "webrtc", err: fmt.Errorf("WebSocket connection failed: %v", err), value: nil}
			return
		}
		log.Println("WebSocket connected")

		// Create WebRTC manager
		webrtcManager, err := rtcManager.NewManager(appCtx, cfg, wsConn, nil)
		if err != nil {
			wsConn.Close()
			resultChan <- initResult{name: "webrtc", err: fmt.Errorf("failed to create WebRTC manager: %v", err), value: nil}
			return
		}

		resultChan <- initResult{name: "webrtc", err: nil, value: map[string]interface{}{
			"conn":    wsConn,
			"manager": webrtcManager,
		}}
	}()

	// Collect results
	var (
		notifier           *notification.Notifier
		motionDetector     *motion.Detector
		recorderService    *recorder.RecordingService
		wsConn             *websocket.Conn
		webrtcManager      *rtcManager.Manager
	)

	// Wait for all 3 goroutines to complete (motionDetector+notifier, recorderService, webrtc)
	for i := 0; i < 3; i++ {
		result := <-resultChan
		if result.err != nil {
			cancel()
			return nil, fmt.Errorf("%s initialization failed: %w", result.name, result.err)
		}

		switch result.name {
		case "motionDetector":
			// Extract both detector and notifier
			detectorData := result.value.(map[string]interface{})
			motionDetector = detectorData["detector"].(*motion.Detector)
			if detectorData["notifier"] != nil {
				notifier = detectorData["notifier"].(*notification.Notifier)
			}
		case "recorderService":
			recorderService = result.value.(*recorder.RecordingService)
		case "webrtc":
			if result.value != nil {
				webrtcData := result.value.(map[string]interface{})
				wsConn = webrtcData["conn"].(*websocket.Conn)
				webrtcManager = webrtcData["manager"].(*rtcManager.Manager)
			}
		}
	}

	// Create calibration service (fast, no need to parallelize)
	calibrationService := calibration.NewService(cfg.Video.OutputPath)

	app := &Application{
		config:             cfg,
		ctx:                appCtx,
		cancel:             cancel,
		motionDetector:     motionDetector,
		recorderService:    recorderService,
		calibrationService: calibrationService,
		notifier:           notifier,
		wsConnection:       wsConn,
		webrtcManager:      webrtcManager,
		testingMode:        testingMode,
		logger:             logger,
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

	logger.Info("Application initialized successfully (concurrent initialization complete)")
	return app, nil
}

// Initialize sets up all components
func (app *Application) Initialize() error {
	log.Println("Initializing application...")

	// Defensive nil checks for components from concurrent initialization
	if app.motionDetector == nil {
		return fmt.Errorf("motion detector not initialized")
	}
	if app.recorderService == nil {
		return fmt.Errorf("recorder service not initialized")
	}
	if !app.testingMode && app.webrtcManager == nil {
		return fmt.Errorf("WebRTC manager not initialized")
	}

	var camera, microphone mediadevices.MediaDeviceInfo
	var err error

	// Select devices (no codec selector needed - we'll use GStreamer)
	if app.testingMode {
		camera, microphone, err = app.selectDevicesForTesting()
	} else {
		// Initialize WebRTC manager (creates peer connection)
		_, err = app.webrtcManager.Initialize()
		if err != nil {
			return fmt.Errorf("WebRTC initialization failed: %v", err)
		}

		// Select devices without codec selector
		camera, microphone, err = app.selectDevicesSimple()
	}

	if err != nil {
		return fmt.Errorf("device selection failed: %v", err)
	}

	app.selectedCamera = camera
	app.selectedMicrophone = microphone

	// Create frame distributor (captures raw frames only)
	app.frameDistributor, err = framestream.NewFrameDistributor(app.ctx, camera)
	if err != nil {
		return fmt.Errorf("failed to create frame distributor: %v", err)
	}

	// Start camera at 640x480
	log.Println("Starting camera for raw frame capture...")
	if err := app.frameDistributor.Start(640, 480); err != nil {
		return fmt.Errorf("failed to start camera: %v", err)
	}
	log.Println("Camera started successfully at 640x480")

	// Create GStreamer encoder pipeline
	encoderConfig := encoder.DefaultEncoderConfig()
	encoderConfig.Width = 640
	encoderConfig.Height = 480
	encoderConfig.FrameRate = 15
	encoderConfig.BitRateKbps = 1500
	encoderConfig.PreferHardware = true

	app.gstPipeline, err = encoder.NewGStreamerPipeline(encoderConfig)
	if err != nil {
		return fmt.Errorf("failed to create GStreamer pipeline: %w", err)
	}

	if err := app.gstPipeline.Start(app.ctx); err != nil {
		return fmt.Errorf("failed to start GStreamer pipeline: %w", err)
	}
	log.Println("GStreamer H.264 encoder started successfully")

	// Update device capability with actual hardware encoder detection
	if app.webrtcManager != nil && app.webrtcManager.ConnectionDoctor != nil {
		qm := app.webrtcManager.ConnectionDoctor.GetQualityManager()
		if qm != nil {
			hasHWEncoder := app.gstPipeline.IsHardwareEncoder()
			deviceCap := quality.DetectDeviceCapability(
				app.config.Video.Width,
				app.config.Video.Height,
				app.config.Video.FrameRate,
				hasHWEncoder,
			)
			qm.UpdateDeviceCapability(deviceCap)
		}
	}

	// Feed frames from distributor to GStreamer pipeline
	go func() {
		rawFrames := app.frameDistributor.GetWebRTCChannel()
		for {
			select {
			case frame, ok := <-rawFrames:
				if !ok {
					log.Println("[Main] Raw frame channel closed")
					return
				}
				if err := app.gstPipeline.FeedFrame(frame); err != nil {
					log.Printf("[Main] Failed to feed frame to pipeline: %v", err)
				}
			case <-app.ctx.Done():
				return
			}
		}
	}()

	// Connect GStreamer output to motion detector
	app.motionDetector.SetFrameSource(app.gstPipeline.GetMotionChannel())

	// Note: RecordingService doesn't need SetFrameSource - it receives frames via motion events

	// Create integration pipeline with WebRTC manager
	app.pipeline = integration.NewPipeline(app.ctx, app.config, app.frameDistributor,
		app.motionDetector, app.recorderService, app.webrtcManager)

	// Setup WebRTC signaling
	if !app.testingMode && app.webrtcManager != nil {
		// Setup passthrough tracks for external RTP input from GStreamer FIRST
		// These tracks don't capture from devices - they're just conduits for RTP packets
		// IMPORTANT: Tracks must exist on peer connection BEFORE signaling creates the SDP offer
		log.Println("Setting up WebRTC passthrough tracks for GStreamer RTP...")

		// Get the actual codec from GStreamer to ensure track matches encoder output
		videoCodec := app.gstPipeline.GetCodecMimeType()
		if err := app.webrtcManager.SetupPassthroughTracks(videoCodec); err != nil {
			return fmt.Errorf("failed to setup passthrough tracks: %v", err)
		}
		log.Printf("WebRTC passthrough tracks setup complete (video codec: %s)", videoCodec)

		log.Println("Setting up WebRTC signaling...")

		// Setup signaling (creates SDP offer with the tracks we just added)
		if err := app.webrtcManager.SetupSignaling(); err != nil {
			return fmt.Errorf("signaling setup failed: %v", err)
		}
		log.Println("WebRTC signaling setup complete - waiting for browser connection")

		// Wire GStreamer RTP output to WebRTC tracks
		log.Println("Attaching GStreamer RTP source to WebRTC manager...")
		videoRTPChan := app.gstPipeline.GetRTPChannel()

		// Note: Audio RTP not yet implemented in GStreamer pipeline, passing nil
		if err := app.webrtcManager.AttachExternalRTPSource(videoRTPChan, nil); err != nil {
			return fmt.Errorf("failed to attach external RTP source: %v", err)
		}
		log.Println("GStreamer RTP source successfully attached to WebRTC - video streaming active")

		// Wire up quality manager callbacks for dynamic bitrate/profile adjustment
		if app.webrtcManager.ConnectionDoctor != nil {
			qm := app.webrtcManager.ConnectionDoctor.GetQualityManager()
			if qm == nil {
				log.Println("[QualityManager] Warning: Quality manager not initialized")
			} else {
				// Callback for bitrate adjustments
				qm.SetBitrateCallback(func(newBitrateKbps int) error {
					if app.gstPipeline != nil && app.gstPipeline.SupportsDynamicBitrate() {
						if err := app.gstPipeline.SetBitrate(newBitrateKbps); err != nil {
							log.Printf("[QualityManager] Failed to adjust encoder bitrate: %v", err)
							return err
						}
						log.Printf("[QualityManager] Encoder bitrate adjusted to %d Kbps", newBitrateKbps)
						return nil
					}
					return nil
				})

				// Callback for profile changes (resolution/framerate adjustments)
				// NOTE: Runtime resolution changes are complex and not yet implemented
				// They would require: 1) Stop GStreamer pipeline, 2) Reconfigure encoder,
				// 3) Restart pipeline, 4) Renegotiate WebRTC SDP
				// For MVP, bitrate-only adaptation is sufficient
				qm.SetProfileCallback(func(newProfile *quality.QualityProfile) error {
					log.Printf("[QualityManager] Profile change requested: %s (%dx%d@%dfps, %d-%d Kbps) - NOT IMPLEMENTED",
						newProfile.Name,
						newProfile.Resolution.Width,
						newProfile.Resolution.Height,
						newProfile.FrameRate,
						newProfile.BitrateRange.Min,
						newProfile.BitrateRange.Max)
					log.Println("[QualityManager] Runtime resolution changes not yet implemented - bitrate adaptation only")
					return nil
				})

				log.Println("[QualityManager] Dynamic quality adjustment callbacks wired successfully")
			}
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
	webrtcChan := app.frameDistributor.GetWebRTCChannel()
	recordChan := app.frameDistributor.GetRecordChannel()

	for {
		select {
		case _, ok := <-webrtcChan:
			if !ok {
				webrtcChan = nil // Prevent further reads from closed channel
			}
		case _, ok := <-recordChan:
			if !ok {
				recordChan = nil
			}
		case <-done:
			log.Println("[Calibration] Stopping channel drain")
			return
		case <-app.ctx.Done():
			return
		default:
			if webrtcChan == nil && recordChan == nil {
				return // Both channels closed
			}
		}
	}
}

// feedCalibrationFrames converts and feeds frames for calibration
func (app *Application) feedCalibrationFrames(calibChan chan<- gocv.Mat) {
	motionChan := app.frameDistributor.GetMotionChannel()
	timeout := time.After(11 * time.Second)

	defer func() {
		close(calibChan)
		for range motionChan {
			// drain to prevent leaks
		}
	}()

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
				// Sent successfully

			default:
				// channel full, close to prevent leaks
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

	// Frame distributor is ALREADY running from main()!
	// Don't start it again - that would cause "panic: close of closed channel"

	// Start recorder service
	if err := app.recorderService.Start(app.ctx); err != nil {
		return fmt.Errorf("failed to start recorder service: %v", err)
	}
	app.logger.Info("Recording service started")

	// DON'T start motion detector automatically - it requires calibration first
	// The GUI will start it via API after calibration is complete
	log.Println("Motion detector ready (waiting for calibration and GUI trigger)")

	// Start integration pipeline
	if err := app.pipeline.Start(); err != nil {
		return fmt.Errorf("failed to start pipeline: %v", err)
	}

	// Stop the drain goroutine if it was running
	if app.drainDoneChannel != nil {
		close(app.drainDoneChannel)
		app.drainDoneChannel = nil
	}

	log.Println("All systems running - waiting for calibration via web interface")

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

		// Stop GStreamer pipeline
		if app.gstPipeline != nil {
			log.Println("Stopping GStreamer pipeline...")
			app.gstPipeline.Stop()
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

// selectDevicesSimple selects devices without codec selector (for GStreamer)
func (app *Application) selectDevicesSimple() (
	cameraDevice, microphoneDevice mediadevices.MediaDeviceInfo, err error) {

	// Enumerate all available devices
	devices := mediadevices.EnumerateDevices()

	if len(devices) == 0 {
		// Try re-initializing drivers one more time
		camera.Initialize()
		microphone.Initialize()
		time.Sleep(100 * time.Millisecond) // Brief delay for enumeration
		devices = mediadevices.EnumerateDevices()
	}

	log.Printf("EnumerateDevices found %d total devices", len(devices))
	for i, dev := range devices {
		log.Printf("Device %d: Type=%s, Kind=%d, ID=%s, Label=%s", i, dev.DeviceType, dev.Kind, dev.DeviceID, dev.Label)
	}

	// Get configured device IDs from config
	configuredCameraID := app.config.Video.DeviceID
	configuredMicID := app.config.Audio.DeviceID

	log.Printf("Looking for configured camera: %s", configuredCameraID)
	log.Printf("Looking for configured microphone: %s", configuredMicID)

	var cameraFound, micFound bool

	// First, try to find devices matching configured IDs
	for _, device := range devices {
		if device.Kind == mediadevices.VideoInput && device.DeviceID == configuredCameraID {
			cameraDevice = device
			cameraFound = true
			log.Printf("Found configured camera: %s (%s)", device.Label, device.DeviceID)
		}
		if device.Kind == mediadevices.AudioInput && device.DeviceID == configuredMicID {
			microphoneDevice = device
			micFound = true
			log.Printf("Found configured microphone: %s (%s)", device.Label, device.DeviceID)
		}

		if cameraFound && micFound {
			return cameraDevice, microphoneDevice, nil
		}
	}

	// Fallback: if configured devices not found, use first available devices
	log.Println("Configured devices not found, falling back to first available devices")

	for _, device := range devices {
		if device.Kind == mediadevices.VideoInput && !cameraFound {
			cameraDevice = device
			cameraFound = true
			log.Printf("Using fallback camera: %s (%s)", device.Label, device.DeviceID)
		}
		if device.Kind == mediadevices.AudioInput && !micFound {
			microphoneDevice = device
			micFound = true
			log.Printf("Using fallback microphone: %s (%s)", device.Label, device.DeviceID)
		}

		if cameraFound && micFound {
			break
		}
	}

	if !cameraFound {
		return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{},
			fmt.Errorf("no camera found - please check camera permissions and ensure a camera is connected")
	}

	if !micFound {
		log.Println("Warning: No microphone found, audio will be disabled")
	}

	return cameraDevice, microphoneDevice, nil
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
	return imgconv.ToMat(img)
}

// decodeFriendlyDeviceName converts hex-encoded labels
func decodeFriendlyDeviceName(label string) string {
	if decoded, err := hex.DecodeString(label); err == nil {
		return string(decoded)
	}
	return label
}
