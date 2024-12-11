package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"

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
	config         *config.Config
	webrtcManager  *webrtc.Manager
	motionDetector *motion.Detector
	videoRecorder  *video.Recorder
	wsConnection   *websocket.Conn
	notifier       *notification.Notifier
}
type DeviceInfo struct {
	Device     mediadevices.MediaDeviceInfo
	IsWorking  bool
	DeviceName string
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
