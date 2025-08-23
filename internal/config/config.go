package config

import (
	"os"
	"strconv"
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
	TailscaleConfig         TailscaleConfig
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

type TailscaleConfig struct {
	Enabled    bool
	NodeName   string
	AuthKey    string
	DERPMap    string // Custom DERP server map URL if needed
	Hostname   string // Tailscale hostname for this device
	ListenPort int    // UDP port for Tailscale to listen on
}

// NewDefaultConfig returns a Config with default values
func NewDefaultConfig() *Config {
	// Get MailSlurp API key from environment, or use default for development
	mailSlurpAPIKey := os.Getenv("MAILSLURP_API_KEY")
	if mailSlurpAPIKey == "" {
		mailSlurpAPIKey = "your-mailslurp-api-key-here" // Set MAILSLURP_API_KEY environment variable
	}

	// Get MailSlurp Inbox ID from environment, or use default for development
	mailSlurpInboxID := os.Getenv("MAILSLURP_INBOX_ID")
	if mailSlurpInboxID == "" {
		mailSlurpInboxID = "your-inbox-id-here" // Set MAILSLURP_INBOX_ID environment variable
	}

	// Get recipient email from environment, or use default for development
	toEmail := os.Getenv("NOTIFICATION_EMAIL")
	if toEmail == "" {
		toEmail = "your-email@example.com" // Set NOTIFICATION_EMAIL environment variable
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
			Username: getEnvString("WEBRTC_USERNAME", "camera_user"),
			Password: getEnvString("WEBRTC_PASSWORD", "change-this-password"),
		},
		StatsCollectionInterval: 5 * time.Second,
		TailscaleConfig: TailscaleConfig{
			Enabled:    getEnvBool("TAILSCALE_ENABLED", false),
			NodeName:   getEnvString("TAILSCALE_NODE_NAME", "webcam-security"),
			AuthKey:    os.Getenv("TAILSCALE_AUTH_KEY"),
			DERPMap:    getEnvString("TAILSCALE_DERP_MAP", ""),
			Hostname:   getEnvString("TAILSCALE_HOSTNAME", ""),
			ListenPort: getEnvInt("TAILSCALE_LISTEN_PORT", 41641),
		},
	}
}

// GetTurnConfigs returns TURN server configuration (fallback when Tailscale is disabled)
func GetTurnConfigs() *TURNConfigs {
	return &TURNConfigs{
		PublicIP:  getEnvString("TURN_PUBLIC_IP", "127.0.0.1"), // Set to your local IP for network access
		Port:      getEnvInt("TURN_PORT", 3478),
		Users:     getEnvString("TURN_USERS", "camera_user=change-this-password"), // Should match WEBRTC_* vars
		Realm:     getEnvString("TURN_REALM", "pion.ly"),
		ThreadNum: getEnvInt("TURN_THREADS", 4),
	}
}

const (
	MinBitrate = 100000  // 100 kbps
	MaxBitrate = 2000000 // 2 Mbps
)

// Helper functions for environment variables
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1"
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}