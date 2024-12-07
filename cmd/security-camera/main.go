package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"gocv.io/x/gocv"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/notification"
	"github.com/mikeyg42/webcam/internal/video"
	"github.com/mikeyg42/webcam/internal/webrtc"
)

// Application struct that holds all components
type Application struct {
	config         *config.Config
	webrtcManager  *webrtc.Manager
	motionDetector *motion.Detector
	videoRecorder  *video.Recorder
	wsConnection   *websocket.Conn
	notifier       *notification.Notifier
	wg             sync.WaitGroup
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

	return &Application{
		config:         cfg,
		motionDetector: motionDetector,
		videoRecorder:  videoRecorder,
		notifier:       notifier,
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
	if err := app.connectWebSocket(); err != nil {
		return fmt.Errorf("failed to connect to WebSocket server: %v", err)
	}

	webrtcManager, err := webrtc.NewManager(app.config, app.wsConnection, app.videoRecorder)
	if err != nil {
		return fmt.Errorf("failed to create WebRTC manager: %v", err)
	}
	app.webrtcManager = webrtcManager

	// Select devices
	camera, microphone, err := app.selectDevices()
	if err != nil {
		return fmt.Errorf("failed to select devices: %v", err)
	}

	if err := app.webrtcManager.SetupMediaTracks(camera, microphone); err != nil {
		return fmt.Errorf("failed to setup media tracks: %v", err)
	}

	if err := app.webrtcManager.SetupSignaling(); err != nil {
		return fmt.Errorf("failed to setup signaling: %v", err)
	}

	return nil
}

func (app *Application) connectWebSocket() error {
	u := url.URL{Scheme: "ws", Host: app.config.WebSocketAddr, Path: "/ws"}
	log.Printf("Connecting to %s", u.String())

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %v", err)
	}
	app.wsConnection = conn
	return nil
}

// evaluates what devices are available and allows the user to select a camera and microphone
func (app *Application) selectDevices() (camera, microphone mediadevices.MediaDeviceInfo, err error) {
	// Enumerate available devices
	devices := mediadevices.EnumerateDevices()

	var cameras []mediadevices.MediaDeviceInfo
	var microphones []mediadevices.MediaDeviceInfo

	for _, device := range devices {
		switch device.Kind {
		case mediadevices.VideoInput:
			cameras = append(cameras, device)
		case mediadevices.AudioInput:
			microphones = append(microphones, device)
		}
	}

	if len(cameras) == 0 {
		return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{}, fmt.Errorf("No camera devices found")
	}
	if len(microphones) == 0 {
		return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{}, fmt.Errorf("No microphone devices found")
	}

	// List available cameras
	fmt.Println("Available cameras:")
	for i, device := range cameras {
		fmt.Printf("%d: %s\n", i, device.Label)
	}

	// Select a camera
	fmt.Print("Select a camera (0 for the first camera): ")
	var cameraIndex int
	_, err = fmt.Scan(&cameraIndex)
	if err != nil || cameraIndex < 0 || cameraIndex >= len(cameras) {
		return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{}, fmt.Errorf("Invalid camera selection")
	}
	camera = cameras[cameraIndex]

	// List available microphones
	fmt.Println("Available microphones:")
	for i, device := range microphones {
		fmt.Printf("%d: %s\n", i, device.Label)
	}

	// Select a microphone
	fmt.Print("Select a microphone (0 for the first microphone): ")
	var micIndex int
	_, err = fmt.Scan(&micIndex)
	if err != nil || micIndex < 0 || micIndex >= len(microphones) {
		return mediadevices.MediaDeviceInfo{}, mediadevices.MediaDeviceInfo{}, fmt.Errorf("Invalid microphone selection")
	}
	microphone = microphones[micIndex]

	return camera, microphone, err
}

func (app *Application) startProcessing() error {
	frameChan := make(chan gocv.Mat)
	motionChan := make(chan bool)
	done := make(chan struct{})

	// Start WebSocket message reader
	go app.readMessages(done)

	// Start motion detection
	go app.motionDetector.Detect(frameChan, motionChan)

	// Start recording handler
	go app.handleRecording(motionChan)

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

func (app *Application) handleRecording(motionChan <-chan bool) {
	for motion := range motionChan {
		if motion {
			if err := app.videoRecorder.StartRecording(); err != nil {
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
