package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/notification"
	"github.com/mikeyg42/webcam/internal/video"
	"github.com/mikeyg42/webcam/internal/webrtc"
	_ "github.com/pion/mediadevices/pkg/driver/camera"     // This is required to register camera adapter
	_ "github.com/pion/mediadevices/pkg/driver/microphone" // This is required to register microphone adapter
	_ "github.com/pion/mediadevices/pkg/driver/screen"
	"github.com/pion/mediadevices/pkg/prop"
)

// Application struct that holds all components
type Application struct {
	config              *config.Config
	webrtcManager       *webrtc.Manager
	motionDetector      *motion.Detector
	videoRecorder       *video.Recorder
	wsConnection        *websocket.Conn
	notifier            *notification.Notifier
	frameProducer       *FrameProducer
	recordingManager    *RecordingManager
	webSocketManager    *WebSocketManager
	selectedCameraIndex int
}
type FrameProducer struct {
	camera      *gocv.VideoCapture
	frameChan   chan gocv.Mat
	stopChan    chan struct{}
	deviceIndex int
}
type DeviceInfo struct {
	Device     mediadevices.MediaDeviceInfo
	IsWorking  bool
	DeviceName string
}
type RecordingManager struct {
	recorder       *video.Recorder
	notifier       *notification.Notifier
	isRecording    bool
	cooldownTimer  *time.Timer
	mu             sync.Mutex
	cooldownPeriod time.Duration
	minRecordTime  time.Duration
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
}

type ConfigValidator struct {
	errors []string
}

func main() {
	// Load configuration
	cfg := config.NewDefaultConfig()

	// Parse command line flags
	flag.StringVar(&cfg.WebSocketAddr, "addr", cfg.WebSocketAddr, "WebSocket address to use")
	flag.Parse()

	// Create new application instance
	app, err := NewApplication(cfg)
	if err != nil {
		log.Fatalf("Failed to create application: %v", err)
	}
	defer app.Cleanup()

	// Initialize the application
	if err := app.Initialize(); err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}

	// Start the main processing loop
	if err := app.startProcessing(); err != nil {
		log.Fatalf("Error during processing: %v", err)
	}
}

func NewApplication(cfg *config.Config) (*Application, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %v", err)
	}

	notifier, err := notification.NewNotifier(&notification.MailSlurpConfig{
		APIKey:   cfg.MailSlurpConfig.APIKey,
		InboxID:  cfg.MailSlurpConfig.InboxID,
		SMTPHost: cfg.MailSlurpConfig.SMTPHost,
		SMTPPort: cfg.MailSlurpConfig.SMTPPort,
		ToEmail:  cfg.MailSlurpConfig.ToEmail,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create notifier: %v", err)
	}

	motionDetector, err := motion.NewDetector(&cfg.MotionConfig, notifier) // Pass the MotionConfig directly
	if err != nil {
		return nil, fmt.Errorf("failed to create motion detector: %v", err)
	}

	videoRecorder := video.NewRecorder(&video.VideoConfig{
		Width:      cfg.VideoConfig.Width,
		Height:     cfg.VideoConfig.Height,
		Framerate:  cfg.VideoConfig.Framerate,
		BitRate:    cfg.VideoConfig.BitRate,
		OutputPath: cfg.VideoConfig.OutputPath,
	})
	recordingManager := NewRecordingManager(videoRecorder, notifier)
	return &Application{
		config:           cfg,
		motionDetector:   motionDetector,
		videoRecorder:    videoRecorder,
		notifier:         notifier,
		recordingManager: recordingManager,
	}, nil
}

func (app *Application) Cleanup() {
	if app.wsConnection != nil {
		app.wsConnection.Close()
	}
	if app.motionDetector != nil {
		app.motionDetector.Close()
	}
	if app.videoRecorder != nil {
		app.videoRecorder.StopRecording()
	}
}

func (app *Application) Initialize() error {
	if err := app.setupWebSocket(); err != nil {
		return fmt.Errorf("failed to connect to WebSocket server: %v", err)
	}

	webrtcManager, err := webrtc.NewManager(app.config, app.wsConnection, app.videoRecorder)
	if err != nil {
		return fmt.Errorf("failed to create WebRTC manager: %v", err)
	}
	app.webrtcManager = webrtcManager

	var codecSelector *mediadevices.CodecSelector

	// Initialize WebRTC first, generating the codec selector
	if codecSelector, err = app.webrtcManager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize WebRTC: %v", err)
	}

	// Select devices
	camera, microphone, err := app.selectDevices()
	if err != nil {
		return fmt.Errorf("failed to select devices: %v", err)
	}

	if err := app.webrtcManager.SetupMediaTracks(camera, microphone, codecSelector); err != nil {
		return fmt.Errorf("failed to setup media tracks: %v", err)
	}

	if err := app.webrtcManager.SetupSignaling(); err != nil {
		return fmt.Errorf("failed to setup signaling: %v", err)
	}

	return nil
}

func (app *Application) setupWebSocket() error {
	wsManager, err := NewWebSocketManager(app.config.WebSocketAddr)
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

func (app *Application) testDevice(device mediadevices.MediaDeviceInfo, index int) (*DeviceInfo, error) {
	info := &DeviceInfo{
		Device: device,
	}

	switch device.Kind {
	case mediadevices.VideoInput:
		// For cameras, try using the index instead of DeviceID
		if device.DeviceType == "camera" { // Only test actual cameras, not screen captures
			webcam, err := gocv.OpenVideoCapture(index)
			if err != nil {
				return info, fmt.Errorf("failed to open camera: %v", err)
			}
			defer webcam.Close()

			// Try to read a frame
			img := gocv.NewMat()
			defer img.Close()

			if ok := webcam.Read(&img); !ok {
				return info, fmt.Errorf("failed to read frame from camera")
			}
			if img.Empty() {
				return info, fmt.Errorf("received empty frame from camera")
			}

			info.IsWorking = true
			info.DeviceName = fmt.Sprintf("%s (Working Camera)", device.Label)
		}

	case mediadevices.AudioInput:
		// Test audio devices using mediadevices
		s, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
			Audio: func(c *mediadevices.MediaTrackConstraints) {
				c.DeviceID = prop.String(device.DeviceID)
			},
		})
		if err != nil {
			return info, fmt.Errorf("failed to test microphone: %v", err)
		}
		defer func() {
			for _, track := range s.GetTracks() {
				track.Close()
			}
		}()

		tracks := s.GetTracks()
		if len(tracks) > 0 {
			info.IsWorking = true
			info.DeviceName = fmt.Sprintf("%s (Working Microphone)", device.Label)
		}
	}

	return info, nil
}

func (app *Application) selectDevices() (camera, microphone mediadevices.MediaDeviceInfo, err error) {
	// Enumerate available devices
	devices := mediadevices.EnumerateDevices()

	var workingCameras []*DeviceInfo
	var workingMicrophones []*DeviceInfo

	// Keep track of camera indices
	cameraIndex := 0

	// Test each device
	for i, device := range devices {
		// Skip screen capture devices
		if device.DeviceType == "screen" {
			continue
		}

		var info *DeviceInfo
		var err error

		if device.Kind == mediadevices.VideoInput {
			info, err = app.testDevice(device, cameraIndex)
			cameraIndex++
		} else {
			info, err = app.testDevice(device, i)
		}

		if err != nil {
			log.Printf("Device %s failed testing: %v", device.Label, err)
			continue
		}

		if info.IsWorking {
			switch device.Kind {
			case mediadevices.VideoInput:
				workingCameras = append(workingCameras, info)
			case mediadevices.AudioInput:
				workingMicrophones = append(workingMicrophones, info)
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
		if err != nil || micIndex < 0 || micIndex >= len(workingMicrophones) {
			return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{},
				fmt.Errorf("invalid microphone selection")
		}
		microphone = workingMicrophones[micIndex].Device
	}

	return camera, microphone, nil
}

func (app *Application) startProcessing() error {
	frameProducer, err := NewFrameProducer(app.selectedCameraIndex) // You'll need to store this during device selection
	if err != nil {
		return fmt.Errorf("failed to create frame producer: %v", err)
	}
	app.frameProducer = frameProducer

	motionChan := make(chan bool)
	done := make(chan struct{})

	// Start frame producer
	app.frameProducer.Start()

	// Start WebSocket message reader
	go app.readMessages(done)

	// Start motion detection
	go app.motionDetector.Detect(app.frameProducer.frameChan, motionChan)

	// start recording manager
	go app.recordingManager.handleMotion(motionChan, app.frameProducer.frameChan)

	// Start recording handler
	go app.handleRecording(motionChan, app.frameProducer.frameChan)

	<-done
	return nil
}

func (app *Application) readMessages(done chan struct{}) {
	defer close(done)
	for {
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

func NewFrameProducer(deviceIndex int) (*FrameProducer, error) {
	camera, err := gocv.OpenVideoCapture(deviceIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to open camera: %v", err)
	}

	return &FrameProducer{
		camera:      camera,
		frameChan:   make(chan gocv.Mat, 10), // Buffer size of 10
		stopChan:    make(chan struct{}),
		deviceIndex: deviceIndex,
	}, nil
}

func (fp *FrameProducer) Start() {
	go func() {
		defer fp.camera.Close()

		for {
			select {
			case <-fp.stopChan:
				return
			default:
				img := gocv.NewMat()
				if ok := fp.camera.Read(&img); !ok {
					img.Close()
					log.Printf("Failed to read frame, attempting recovery...")
					fp.attemptRecovery()
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
				default:
					// Channel full, drop frame
					img.Close()
				}
			}
		}
	}()
}

func (fp *FrameProducer) attemptRecovery() {
	fp.camera.Close()
	for i := 0; i < 3; i++ { // Try 3 times
		time.Sleep(time.Second)
		camera, err := gocv.OpenVideoCapture(fp.deviceIndex)
		if err == nil {
			fp.camera = camera
			return
		}
	}
	log.Printf("Failed to recover camera after 3 attempts")
}

func (fp *FrameProducer) Stop() {
	close(fp.stopChan)
}

// ....................
func NewRecordingManager(recorder *video.Recorder, notifier *notification.Notifier) *RecordingManager {
	return &RecordingManager{
		recorder:       recorder,
		notifier:       notifier,
		cooldownPeriod: 30 * time.Second,
		minRecordTime:  10 * time.Second,
	}
}

func (rm *RecordingManager) handleMotion(motionChan <-chan bool, frameChan <-chan gocv.Mat) {
	var recordingStartTime time.Time

	for motion := range motionChan {
		rm.mu.Lock()

		if motion && !rm.isRecording {
			if rm.cooldownTimer != nil && !rm.cooldownTimer.Stop() {
				<-rm.cooldownTimer.C
			}

			if err := rm.recorder.StartRecording(frameChan); err != nil {
				log.Printf("Failed to start recording: %v", err)
				rm.mu.Unlock()
				continue
			}

			recordingStartTime = time.Now()
			rm.isRecording = true

			// Send notification
			if err := rm.notifier.SendNotification(); err != nil {
				log.Printf("Failed to send notification: %v", err)
			}

		} else if !motion && rm.isRecording {
			// Check if minimum recording time has elapsed
			if time.Since(recordingStartTime) < rm.minRecordTime {
				rm.mu.Unlock()
				continue
			}

			// Start cooldown timer
			rm.cooldownTimer = time.AfterFunc(rm.cooldownPeriod, func() {
				rm.mu.Lock()
				if rm.isRecording {
					if err := rm.recorder.StopRecording(); err != nil {
						log.Printf("Failed to stop recording: %v", err)
					}
					rm.isRecording = false
				}
				rm.mu.Unlock()
			})
		}

		rm.mu.Unlock()
	}
}

//....................

func NewWebSocketManager(addr string) (*WebSocketManager, error) {
	u := url.URL{Scheme: "ws", Host: addr, Path: "/ws"}
	return &WebSocketManager{
		url:             u,
		reconnectPeriod: 5 * time.Second,
		maxRetries:      5,
		stopChan:        make(chan struct{}),
		messageChan:     make(chan []byte, 100), // Buffered channel for messages
	}, nil
}

func (wsm *WebSocketManager) Start() error {
	if err := wsm.connect(); err != nil {
		return err
	}

	go wsm.readPump()
	go wsm.reconnectLoop()
	return nil
}

func (wsm *WebSocketManager) connect() error {
	wsm.mu.Lock()
	defer wsm.mu.Unlock()

	conn, _, err := websocket.DefaultDialer.Dial(wsm.url.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %v", err)
	}

	wsm.conn = conn
	wsm.isConnected = true
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

			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket read error: %v", err)
				}
				wsm.handleDisconnect()
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

func (cv *ConfigValidator) addError(format string, args ...interface{}) {
	cv.errors = append(cv.errors, fmt.Sprintf(format, args...))
}

func ValidateConfig(cfg *config.Config) error {
	v := &ConfigValidator{}

	// Validate VideoConfig
	if cfg.VideoConfig.Width == 0 || cfg.VideoConfig.Height == 0 {
		v.addError("invalid video dimensions: width=%d, height=%d",
			cfg.VideoConfig.Width, cfg.VideoConfig.Height)
	}
	if cfg.VideoConfig.Framerate <= 0 {
		v.addError("invalid framerate: %d", cfg.VideoConfig.Framerate)
	}
	if cfg.VideoConfig.BitRate <= 0 {
		v.addError("invalid bitrate: %d", cfg.VideoConfig.BitRate)
	}
	if cfg.VideoConfig.OutputPath == "" {
		v.addError("output path cannot be empty")
	}

	// Validate MailSlurpConfig
	if cfg.MailSlurpConfig.APIKey == "" {
		v.addError("MailSlurp API key cannot be empty")
	}
	if cfg.MailSlurpConfig.InboxID == "" {
		v.addError("MailSlurp inbox ID cannot be empty")
	}
	if cfg.MailSlurpConfig.SMTPPort <= 0 || cfg.MailSlurpConfig.SMTPPort > 65535 {
		v.addError("invalid SMTP port: %d", cfg.MailSlurpConfig.SMTPPort)
	}
	if cfg.MailSlurpConfig.ToEmail == "" {
		v.addError("recipient email cannot be empty")
	}

	// Validate MotionConfig
	if cfg.MotionConfig.Threshold <= 0 {
		v.addError("invalid motion threshold: %f", cfg.MotionConfig.Threshold)
	}
	if cfg.MotionConfig.MinimumArea <= 0 {
		v.addError("invalid minimum area: %d", cfg.MotionConfig.MinimumArea)
	}

	if len(v.errors) > 0 {
		return fmt.Errorf("configuration validation failed:\n%s",
			strings.Join(v.errors, "\n"))
	}

	return nil
}
