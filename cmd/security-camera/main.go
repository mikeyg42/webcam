package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/driver"
	"github.com/pion/webrtc/v4"
	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/notification"
	"github.com/mikeyg42/webcam/internal/rtcManager"
	"github.com/mikeyg42/webcam/internal/validate"
	"github.com/mikeyg42/webcam/internal/video"

	_ "github.com/pion/mediadevices/pkg/driver/camera"     // This is required to register camera adapter
	_ "github.com/pion/mediadevices/pkg/driver/microphone" // This is required to register microphone adapter
	//_ "github.com/pion/mediadevices/pkg/driver/screen"
)

// Application struct that holds all components
type Application struct {
	config              *config.Config
	webrtcManager       *rtcManager.Manager
	motionDetector      *motion.Detector
	videoRecorder       *video.Recorder
	wsConnection        *websocket.Conn
	notifier            *notification.Notifier
	frameProducer       *FrameProducer
	recordingManager    *RecordingManager
	webSocketManager    *WebSocketManager
	selectedCameraIndex int
	state               ApplicationState
	stateMu             sync.RWMutex
	startTime           time.Time
	ctx                 context.Context
	cancel              context.CancelFunc
}

type ApplicationState int

const (
	StateInitializing ApplicationState = iota
	StateRunning
	StateShuttingDown
	StateStopped
)

type FrameProducer struct {
	camera      *gocv.VideoCapture
	frameChan   chan gocv.Mat
	deviceIndex int
	ctx         context.Context    // Add context
	cancel      context.CancelFunc // Add cancel function
}

type DeviceInfo struct {
	Device     mediadevices.MediaDeviceInfo
	IsWorking  bool
	DeviceName string
	DeviceType string
}

type RecordingManager struct {
	recorder       *video.Recorder
	notifier       *notification.Notifier
	isRecording    bool
	cooldownTimer  *time.Timer
	mu             sync.Mutex
	cooldownPeriod time.Duration
	minRecordTime  time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
}

type WebSocketManager struct {
	conn            *websocket.Conn
	url             url.URL
	reconnectPeriod time.Duration
	maxRetries      int
	isConnected     bool
	mu              sync.RWMutex
	stopChan        chan struct{}
	messageChan     chan []byte
	ctx             context.Context
	cancel          context.CancelFunc
}

// application state
func (app *Application) setState(state ApplicationState) {
	app.stateMu.Lock()
	defer app.stateMu.Unlock()
	app.state = state
}

func (app *Application) GetState() ApplicationState {
	app.stateMu.RLock()
	defer app.stateMu.RUnlock()
	return app.state
}

func main() {
	// Create root context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Load configuration
	cfg := config.NewDefaultConfig()

	// Validate configuration
	if err := validate.ValidateConfig(cfg); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	// Parse command line flags
	flag.StringVar(&cfg.WebSocketAddr, "addr", cfg.WebSocketAddr, "WebSocket address to use")
	var debugMode bool
	flag.BoolVar(&debugMode, "debug", false, "Enable debug mode with startup notification")
	flag.Parse()

	cfg.MailSlurpConfig.Debug = debugMode

	flag.Parse()

	// Create new application instance
	app, err := NewApplication(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to create application: %v", err)
	}
	defer app.Cleanup()

	// Initialize the application
	if err := app.Initialize(); err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}

	// Start application in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- app.startProcessing()
	}()

	// Wait for either error, shutdown signal, or context cancellation
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

	// Initiate graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
}

func NewApplication(ctx context.Context, cfg *config.Config) (*Application, error) {
	appCtx, cancel := context.WithCancel(ctx)

	// Setup signal handling
	notifier, err := notification.NewNotifier(&notification.MailSlurpConfig{
		APIKey:   cfg.MailSlurpConfig.APIKey,
		InboxID:  cfg.MailSlurpConfig.InboxID,
		SMTPHost: cfg.MailSlurpConfig.SMTPHost,
		SMTPPort: cfg.MailSlurpConfig.SMTPPort,
		ToEmail:  cfg.MailSlurpConfig.ToEmail,
	})
	if err != nil {
		cancel() // Clean up if we fail
		return nil, fmt.Errorf("failed to create notifier: %v", err)
	}

	motionDetector, err := motion.NewDetector(&cfg.MotionConfig, notifier)
	if err != nil {
		cancel() // Clean up if we fail
		return nil, fmt.Errorf("failed to create motion detector: %v", err)
	}

	videoRecorder := video.NewRecorder(&video.VideoConfig{
		Width:      cfg.VideoConfig.Width,
		Height:     cfg.VideoConfig.Height,
		Framerate:  cfg.VideoConfig.Framerate,
		BitRate:    cfg.VideoConfig.BitRate,
		OutputPath: cfg.VideoConfig.OutputPath,
	})

	recordingManager := NewRecordingManager(ctx, videoRecorder, notifier)

	return &Application{
		config:           cfg,
		ctx:              appCtx,
		cancel:           cancel,
		motionDetector:   motionDetector,
		videoRecorder:    videoRecorder,
		notifier:         notifier,
		recordingManager: recordingManager,
		startTime:        time.Now(),
	}, nil
}

func (app *Application) Cleanup() {
	app.cancel() // Cancel context to signal all goroutines to stop

	// clean up resources in reverse order
	if app.videoRecorder != nil {
		app.videoRecorder.StopRecording()
	}
	if app.motionDetector != nil {
		app.motionDetector.Close()
	}
	if app.webrtcManager != nil {
		app.webrtcManager.Cleanup()
	}
	if app.frameProducer != nil {
		app.frameProducer.Stop()
	}
	if app.wsConnection != nil {
		app.wsConnection.Close()
	}
}

func (app *Application) Initialize() error {
	if err := app.setupWebSocket(); err != nil {
		return fmt.Errorf("failed to connect to WebSocket server: %v", err)
	}

	webrtcManager, err := rtcManager.NewManager(app.config, app.wsConnection, app.videoRecorder)
	if err != nil {
		return fmt.Errorf("failed to create WebRTC manager: %v", err)
	}
	app.webrtcManager = webrtcManager

	// Setup connection state callbacks
	connectionStateChange := make(chan webrtc.PeerConnectionState)
	app.webrtcManager.peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("PeerConnection State: %s", state.String())
		select {
		case connectionStateChange <- state:
		default:
			log.Printf("Warning: connectionStateChange channel full")
		}

		switch state {
		case webrtc.PeerConnectionStateConnecting:
			log.Println("PeerConnection is establishing...")
		case webrtc.PeerConnectionStateConnected:
			log.Println("PeerConnection established successfully!")
		case webrtc.PeerConnectionStateDisconnected:
			log.Println("PeerConnection disconnected - attempting to reconnect...")
		case webrtc.PeerConnectionStateFailed:
			log.Println("PeerConnection failed - need to restart")
		case webrtc.PeerConnectionStateClosed:
			log.Println("PeerConnection closed")
		}
	})

	// Setup ICE connection state callbacks
	app.webrtcManager.peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State: %s", state.String())

		switch state {
		case webrtc.ICEConnectionStateChecking:
			log.Println("ICE checking candidates...")
		case webrtc.ICEConnectionStateConnected:
			log.Println("ICE connection established!")
		case webrtc.ICEConnectionStateFailed:
			log.Println("ICE connection failed")
		case webrtc.ICEConnectionStateDisconnected:
			log.Println("ICE connection disconnected")
		}
	})

	var codecSelector *mediadevices.CodecSelector

	// Initialize WebRTC first, generating the codec selector
	if codecSelector, err = app.webrtcManager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize WebRTC: %v", err)
	}

	// Select devices
	camera, microphone, err := app.selectDevices(codecSelector)
	if err != nil {
		return fmt.Errorf("failed to select devices: %v", err)
	}

	if err := app.webrtcManager.SetupMediaTracks(camera, microphone, codecSelector); err != nil {
		return fmt.Errorf("failed to setup media tracks: %v", err)
	}

	if err := app.webrtcManager.SetupSignaling(); err != nil {
		return fmt.Errorf("failed to setup signaling: %v", err)
	}

	// Wait for initial connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	select {
	case state := <-connectionStateChange:
		if state == webrtc.PeerConnectionStateConnected {
			log.Println("Connection established successfully")
			return nil
		}
		return fmt.Errorf("connection reached unexpected state: %s", state)
	case <-ctx.Done():
		return fmt.Errorf("connection timeout after 30 seconds")
	}
}

func (app *Application) setupWebSocket() error {

	wsManager, err := NewWebSocketManager(app.ctx, app.config.WebSocketAddr)
	if err != nil {
		return fmt.Errorf("failed to create websocket manager: %v", err)
	}

	if err := wsManager.Start(); err != nil {
		return fmt.Errorf("failed to start websocket manager: %v", err)
	}

	app.webSocketManager = wsManager
	app.wsConnection = wsManager.conn // Keep this for compatibility
	return nil
}

func (app *Application) testDevice(device mediadevices.MediaDeviceInfo, codecSelector *mediadevices.CodecSelector) (*DeviceInfo, error) {
	info := &DeviceInfo{
		Device: device,
	}

	// Create temporary manager config for testing
	tempManager := app.webrtcManager

	switch device.Kind {
	case mediadevices.VideoInput:
		// Set the device in manager temporarily
		tempManager.SetCamera(device)

		// Try to generate a stream with just video
		stream, err := tempManager.GenerateStream(codecSelector)
		if err != nil {
			return info, fmt.Errorf("failed to generate video stream: %v", err)
		}
		defer func() {
			for _, track := range stream.GetTracks() {
				track.Close()
			}
		}()

		// Check if we got video tracks
		videoTracks := stream.GetVideoTracks()
		if len(videoTracks) == 0 {
			return info, fmt.Errorf("no video tracks in stream")
		}

		info.IsWorking = true
		info.DeviceName = fmt.Sprintf("%s (Working Camera)", device.Label)

	case mediadevices.AudioInput:
		// Set the device in manager temporarily
		tempManager.SetMicrophone(device)

		// Try to generate a stream with just audio
		stream, err := tempManager.GenerateStream(codecSelector)
		if err != nil {
			return info, fmt.Errorf("failed to generate audio stream: %v", err)
		}
		defer func() {
			for _, track := range stream.GetTracks() {
				track.Close()
			}
		}()

		// Check if we got audio tracks
		audioTracks := stream.GetAudioTracks()
		if len(audioTracks) == 0 {
			return info, fmt.Errorf("no audio tracks in stream")
		}

		info.IsWorking = true
		info.DeviceName = fmt.Sprintf("%s (Working Microphone)", device.Label)
	}

	return info, nil
}

func (app *Application) selectDevices(codecSelector *mediadevices.CodecSelector) (camera, microphone mediadevices.MediaDeviceInfo, err error) {
	// Enumerate available devices
	devices := mediadevices.EnumerateDevices()

	var workingCameras []*DeviceInfo
	var workingMicrophones []*DeviceInfo

	log.Println("Available devices:")
	for _, device := range devices {
		log.Printf("ID: %s | Label: %s | Kind: %v | DeviceType: %v ||\n",
			device.DeviceID,
			device.Label,
			device.Kind,       // e.g. VideoInput, AudioInput
			device.DeviceType, // e.g. "camera", "microphone", "screen"
		)

		// Skip screen capture devices
		if device.DeviceType == driver.Screen {
			log.Printf("Skipping screen device: %s", device.Label)
			continue
		}

		// Skip specific device IDs (case insensitive)
		if strings.Contains(strings.ToUpper(device.DeviceID), "3EB17E44") ||
			strings.Contains(strings.ToUpper(device.Label), "3EB17E44") {
			log.Printf("Skipping iPhone input: %s", device.Label)
			continue
		}

		var info *DeviceInfo
		var err error

		info, err = app.testDevice(device, codecSelector)
		if err != nil {
			log.Printf("Device '%s' failed testing: %v", device.Label, err)
			continue
		}

		if info.IsWorking {
			switch device.Kind {
			case mediadevices.VideoInput:
				workingCameras = append(workingCameras, info)
				log.Printf("Added working camera: %s", info.DeviceName)
			case mediadevices.AudioInput:
				workingMicrophones = append(workingMicrophones, info)
				log.Printf("Added working microphone: %s", info.DeviceName)
			}
		}
	}

	if len(workingCameras) == 0 {
		return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{},
			fmt.Errorf("no working cameras found")
	}
	if len(workingMicrophones) == 0 {
		return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{},
			fmt.Errorf("no working microphones found")
	}

	if len(workingCameras) == 1 {
		camera = workingCameras[0].Device
		app.selectedCameraIndex = 0
		fmt.Printf("\nAutomatically selected camera: %s\n", workingCameras[0].DeviceName)
	} else {
		// List available working cameras
		fmt.Println("\nAvailable working cameras:")
		for i, info := range workingCameras {
			fmt.Printf("%d: %s\n", i, info.DeviceName)
		}

		// Select a camera
		fmt.Print("Select a camera (0 for the first camera): ")
		var cameraIndex int
		_, err = fmt.Scan(&cameraIndex)
		if err != nil || cameraIndex < 0 || cameraIndex >= len(workingCameras) {
			return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{},
				fmt.Errorf("invalid camera selection")
		}
		camera = workingCameras[cameraIndex].Device
		app.selectedCameraIndex = cameraIndex
	}

	// List available working microphones
	if len(workingMicrophones) == 1 {
		microphone = workingMicrophones[0].Device
		fmt.Printf("\nAutomatically selected microphone: %s\n", workingMicrophones[0].DeviceName)
	} else {
		fmt.Println("\nAvailable working microphones:")
		for i, info := range workingMicrophones {
			fmt.Printf("%d: %s\n", i, info.DeviceName)
		}

		// Select a microphone
		fmt.Print("Select a microphone (0 for the first microphone): ")
		var micIndex int
		_, err = fmt.Scan(&micIndex)
		if err != nil {
			return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{},
				fmt.Errorf("invalid microphone selection")
		}
		microphone = workingMicrophones[micIndex].Device
	}

	return camera, microphone, nil
}

func (app *Application) startProcessing() error {
	app.setState(StateRunning)
	defer app.setState(StateStopped)

	frameProducer, err := NewFrameProducer(app.ctx, app.selectedCameraIndex) // You'll need to store this during device selection
	if err != nil {
		return fmt.Errorf("failed to create frame producer: %v", err)
	}
	app.frameProducer = frameProducer

	motionChan := make(chan bool)

	// Start frame producer
	app.frameProducer.Start()

	// Start WebSocket message reader
	go app.readMessages()

	// Start motion detection
	go app.motionDetector.Detect(app.frameProducer.frameChan, motionChan)

	// start recording manager
	go app.recordingManager.handleMotion(motionChan, app.frameProducer.frameChan)

	// Start recording handler
	go app.handleRecording(motionChan, app.frameProducer.frameChan)

	<-app.ctx.Done()
	return app.ctx.Err()
}

func (app *Application) readMessages() {
	for {
		select {
		case <-app.ctx.Done():
			return
		default:
			_, message, err := app.wsConnection.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket error: %v", err)
				}
				return
			}

			if err := app.webrtcManager.HandleIncomingMessage(message); err != nil {
				log.Printf("Error handling message: %v", err)
			}
		}
	}
}

func (app *Application) handleRecording(motionChan <-chan bool, frameChan <-chan gocv.Mat) {
	for motion := range motionChan {
		if motion {
			if err := app.videoRecorder.StartRecording(frameChan); err != nil {
				log.Printf("Failed to start recording: %v", err)
			}
			if err := app.notifier.SendNotification(); err != nil {
				log.Printf("Failed to send notification: %v", err)
			}
		} else {
			if err := app.videoRecorder.StopRecording(); err != nil {
				log.Printf("Failed to stop recording: %v", err)
			}
		}
	}
}

func (app *Application) Shutdown(ctx context.Context) error {
	app.setState(StateShuttingDown)
	// Create a channel to signal completion
	done := make(chan struct{})

	go func() {
		// Stop frame producer
		if app.frameProducer != nil {
			app.frameProducer.Stop()
		}

		// Close WebSocket connection
		if app.wsConnection != nil {
			app.wsConnection.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			)
			app.wsConnection.Close()
		}

		// Stop WebRTC
		if app.webrtcManager != nil {
			app.webrtcManager.Cleanup()
		}

		// Stop motion detector
		if app.motionDetector != nil {
			app.motionDetector.Close()
		}

		// Stop video recorder
		if app.videoRecorder != nil {
			app.videoRecorder.StopRecording()
		}

		close(done)
	}()

	// Wait for shutdown to complete or context to timeout
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown timed out: %v", ctx.Err())
	}
}

// ....................

func NewFrameProducer(ctx context.Context, deviceIndex int) (*FrameProducer, error) {
	// Create child context
	fpCtx, cancel := context.WithCancel(ctx)

	camera, err := gocv.OpenVideoCapture(deviceIndex)
	if err != nil {
		cancel() // Clean up if we fail
		return nil, fmt.Errorf("failed to open camera: %v", err)
	}

	return &FrameProducer{
		camera:      camera,
		frameChan:   make(chan gocv.Mat, 10),
		deviceIndex: deviceIndex,
		ctx:         fpCtx,
		cancel:      cancel,
	}, nil
}

func (fp *FrameProducer) Start() {
	go func() {
		defer fp.camera.Close()
		defer fp.cancel() // Ensure context is cancelled when we're done

		for {
			select {
			case <-fp.ctx.Done():
				return
			default:
				img := gocv.NewMat()
				if ok := fp.camera.Read(&img); !ok {
					img.Close()
					log.Printf("Failed to read frame, attempting recovery...")
					if err := fp.attemptRecovery(); err != nil {
						log.Printf("Recovery failed: %v", err)
						return
					}
					continue
				}

				if img.Empty() {
					img.Close()
					continue
				}

				// Non-blocking send to channel
				select {
				case fp.frameChan <- img:
					// Frame sent successfully
				case <-fp.ctx.Done():
					img.Close()
					return
				default:
					// Channel full, drop frame
					img.Close()
				}
			}
		}
	}()
}

func (fp *FrameProducer) attemptRecovery() error {
	fp.camera.Close()
	for i := 0; i < 3; i++ { // Try 3 times
		select {
		case <-fp.ctx.Done():
			return fmt.Errorf("context cancelled during recovery")
		case <-time.After(time.Second):
			camera, err := gocv.OpenVideoCapture(fp.deviceIndex)
			if err == nil {
				fp.camera = camera
				return nil
			}
			log.Printf("Recovery attempt %d failed: %v", i+1, err)
		}
	}
	return fmt.Errorf("failed to recover camera after 3 attempts")
}

func (fp *FrameProducer) Stop() {
	fp.cancel()
}

// ....................
func NewRecordingManager(ctx context.Context, recorder *video.Recorder, notifier *notification.Notifier) *RecordingManager {
	rmCtx, cancel := context.WithCancel(ctx)

	return &RecordingManager{
		recorder:       recorder,
		notifier:       notifier,
		cooldownPeriod: 30 * time.Second,
		minRecordTime:  10 * time.Second,
		ctx:            rmCtx,
		cancel:         cancel,
	}
}

func (rm *RecordingManager) handleMotion(motionChan <-chan bool, frameChan <-chan gocv.Mat) {
	var recordingStartTime time.Time

	for {
		select {
		case <-rm.ctx.Done():
			rm.stopRecording()
			return

		case motion, ok := <-motionChan:
			if !ok {
				rm.stopRecording()
				return
			}

			rm.mu.Lock()
			if motion && !rm.isRecording {
				if rm.cooldownTimer != nil {
					rm.cooldownTimer.Stop()
				}

				if err := rm.recorder.StartRecording(frameChan); err != nil {
					log.Printf("Failed to start recording: %v", err)
					rm.mu.Unlock()
					continue
				}

				recordingStartTime = time.Now()
				rm.isRecording = true

				// Send notification with context
				go func() {
					if err := rm.notifier.SendNotification(); err != nil {
						log.Printf("Failed to send notification: %v", err)
					}
				}()

			} else if !motion && rm.isRecording {
				// Check if minimum recording time has elapsed
				if time.Since(recordingStartTime) < rm.minRecordTime {
					rm.mu.Unlock()
					continue
				}

				// Start cooldown timer with context awareness
				rm.cooldownTimer = time.AfterFunc(rm.cooldownPeriod, func() {
					select {
					case <-rm.ctx.Done():
						return
					default:
						rm.mu.Lock()
						if rm.isRecording {
							if err := rm.recorder.StopRecording(); err != nil {
								log.Printf("Failed to stop recording: %v", err)
							}
							rm.isRecording = false
						}
						rm.mu.Unlock()
					}
				})
			}
			rm.mu.Unlock()
		}
	}
}

func (rm *RecordingManager) stopRecording() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.isRecording {
		if err := rm.recorder.StopRecording(); err != nil {
			log.Printf("Failed to stop recording during shutdown: %v", err)
		}
		rm.isRecording = false
	}

	if rm.cooldownTimer != nil {
		rm.cooldownTimer.Stop()
	}
}

func (rm *RecordingManager) Stop() {
	rm.cancel()
	rm.stopRecording()
}

//....................

func NewWebSocketManager(ctx context.Context, addr string) (*WebSocketManager, error) {
	wsCtx, cancel := context.WithCancel(ctx)
	u := url.URL{Scheme: "ws", Host: addr, Path: "/ws"}
	return &WebSocketManager{
		url:             u,
		reconnectPeriod: 5 * time.Second,
		maxRetries:      5,
		stopChan:        make(chan struct{}),
		messageChan:     make(chan []byte, 100), // Buffered channel for messages
		ctx:             wsCtx,
		cancel:          cancel,
	}, nil
}

func (wsm *WebSocketManager) Start() error {
	// initial connection timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try initial connection with timeout
	connected := make(chan error, 1)
	go func() {
		connected <- wsm.connect()
	}()

	select {
	case err := <-connected:
		if err != nil {
			return fmt.Errorf("initial connection failed: %v", err)
		}
	case <-ctx.Done():
		return fmt.Errorf("connection timeout")
	}

	// Start read and reconnect loops
	go wsm.readPump()
	go wsm.reconnectLoop()

	return nil
}

func (wsm *WebSocketManager) connect() error {
	wsm.mu.Lock()
	defer wsm.mu.Unlock()

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.Dial(wsm.url.String(), nil)
	if err != nil {
		if resp != nil {
			log.Printf("WebSocket connection failed with status: %d", resp.StatusCode)
		}
		return fmt.Errorf("websocket dial failed: %v", err)
	}

	// Setup ping handler
	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})

	wsm.conn = conn
	wsm.isConnected = true
	log.Printf("Successfully connected to WebSocket server")
	return nil
}
func (wsm *WebSocketManager) reconnectLoop() {
	for {
		select {
		case <-wsm.stopChan:
			return
		default:
			wsm.mu.RLock()
			if !wsm.isConnected {
				wsm.mu.RUnlock()
				retries := 0
				for retries < wsm.maxRetries {
					log.Printf("Attempting to reconnect... (attempt %d/%d)", retries+1, wsm.maxRetries)
					if err := wsm.connect(); err == nil {
						break
					}
					retries++
					time.Sleep(wsm.reconnectPeriod)
				}
				if retries == wsm.maxRetries {
					log.Printf("Failed to reconnect after %d attempts", wsm.maxRetries)
				}
			} else {
				wsm.mu.RUnlock()
			}
			time.Sleep(wsm.reconnectPeriod)
		}
	}
}

func (wsm *WebSocketManager) readPump() {
	for {
		select {
		case <-wsm.stopChan:
			return
		default:
			wsm.mu.RLock()
			conn := wsm.conn
			wsm.mu.RUnlock()

			if conn == nil {
				time.Sleep(time.Second)
				continue
			}

			messageType, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket read error: %v", err)
				}
				wsm.handleDisconnect()
				// delay before retry prevents tight loop
				time.Sleep(time.Second)
				continue
			}

			// Handle ping/pong messages
			if messageType == websocket.PingMessage {
				if err := conn.WriteMessage(websocket.PongMessage, nil); err != nil {
					log.Printf("Failed to send pong: %v", err)
				}
				continue
			}

			select {
			case wsm.messageChan <- message:
			default:
				log.Printf("Message channel full, dropping message")
			}
		}
	}
}

func (wsm *WebSocketManager) handleDisconnect() {
	wsm.mu.Lock()
	defer wsm.mu.Unlock()

	if wsm.conn != nil {
		wsm.conn.Close()
	}
	wsm.isConnected = false
}

func (wsm *WebSocketManager) Stop() {
	close(wsm.stopChan)
	wsm.mu.Lock()
	defer wsm.mu.Unlock()
	if wsm.conn != nil {
		wsm.conn.Close()
	}
}

//....................
