package config

import (
	"os"
	"time"
)

// Config holds all application configuration
type Config struct {
	WebSocketAddr           string
	MailSlurpConfig         MailSlurpConfig
	VideoConfig             VideoConfig
	MotionConfig            MotionConfig
	RecordVideo             bool
	WebrtcAuth              WebRTCAuth
	StatsCollectionInterval time.Duration
}

type MailSlurpConfig struct {
	APIKey   string
	InboxID  string
	SMTPHost string
	SMTPPort int
	ToEmail  string
	Debug    bool
}

type VideoConfig struct {
	Width      int
	Height     int
	Framerate  int
	BitRate    int
	OutputPath string
}

type MotionConfig struct {
	MinimumArea          int           // Minimum area size for motion detection
	FrameSkip            int           // Process every nth frame
	LearningRate         float64       // Learning rate for background subtraction
	Threshold            float32       // Threshold for motion detection
	DilationSize         int           // Size of dilation kernel
	BlurSize             int           // Size of Gaussian blur kernel
	CooldownPeriod       time.Duration // Minimum time between notifications
	NoMotionDelay        time.Duration // Duration to wait before declaring no motion
	MaxConsecutiveFrames int           //
	MinConsecutiveFrames int           //
}

type TURNConfigs struct {
	PublicIP  string
	Port      int
	Users     string //"user=pass,user=pass, ..."
	Realm     string
	ThreadNum int
}
type WebRTCAuth struct {
	Username string
	Password string
}

// NewDefaultConfig returns a Config with default values
func NewDefaultConfig() *Config {
	// Get MailSlurp API key from environment, or use default for development
	mailSlurpAPIKey := os.Getenv("MAILSLURP_API_KEY")
	if mailSlurpAPIKey == "" {
		mailSlurpAPIKey = "4f6d68998fd08a2d051c71dbaeadca66558e5a69edbc102134af9c3e0ac867bc"
	}

	// Get MailSlurp Inbox ID from environment, or use default for development
	mailSlurpInboxID := os.Getenv("MAILSLURP_INBOX_ID")
	if mailSlurpInboxID == "" {
		mailSlurpInboxID = "f7c87f5b-54a1-43c5-91e6-6a3009d4e9a9"
	}

	// Get recipient email from environment, or use default for development
	toEmail := os.Getenv("NOTIFICATION_EMAIL")
	if toEmail == "" {
		toEmail = "user-f7c87f5b-54a1-43c5-91e6-6a3009d4e9a9@mailslurp.biz"
	}

	return &Config{
		RecordVideo:   true,
		WebSocketAddr: "localhost:7000",
		MailSlurpConfig: MailSlurpConfig{
			SMTPHost: "mx.mailslurp.com",
			SMTPPort: 2525,
			APIKey:   mailSlurpAPIKey,
			InboxID:  mailSlurpInboxID,
			ToEmail:  toEmail,
			Debug:    false, // Will be set via command line flag
		},
		VideoConfig: VideoConfig{
			Width:      640,
			Height:     480,
			Framerate:  25,
			BitRate:    500_000,
			OutputPath: "recordings/",
		},
		MotionConfig: MotionConfig{
			MinimumArea:          3000,
			FrameSkip:            5,
			Threshold:            25.0,
			DilationSize:         3,
			CooldownPeriod:       30 * time.Second,
			NoMotionDelay:        10 * time.Second,
			BlurSize:             7,
			MaxConsecutiveFrames: 45,
			MinConsecutiveFrames: 3,
		},
		WebrtcAuth: WebRTCAuth{
			Username: "awsome",
			Password: "awsome",
		},
		StatsCollectionInterval: 5 * time.Second,
	}
}

func GetTurnConfigs() *TURNConfigs {
	return &TURNConfigs{
		PublicIP:  "192.168.1.143",
		Port:      3478,
		Users:     "user1=pass1,user2=pass2,pion=ion,pion2=ion2",
		Realm:     "pion.ly",
		ThreadNum: 4,
	}
}

const (
	MinBitrate = 100000  // 100 kbps
	MaxBitrate = 2000000 // 2 Mbps
)
