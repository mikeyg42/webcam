package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all application configuration
type Config struct {
	WebSocketAddr           string
	EmailMethod             string // "mailersend", "gmail", or "disabled"
	MailSendConfig          MailSendConfig
	GmailOAuth2Config       GmailOAuth2Config
	VideoConfig             VideoConfig
	MotionConfig            MotionConfig
	RecordVideo             bool
	WebrtcAuth              WebRTCAuth
	StatsCollectionInterval time.Duration
	TailscaleConfig         TailscaleConfig
}

type MailSendConfig struct {
	APIToken  string
	ToEmail   string
	FromEmail string
	Debug     bool
}

type GmailOAuth2Config struct {
	ClientID           string
	ClientSecret       string
	ToEmail            string
	FromEmail          string
	RedirectURL        string
	TokenStorePath     string
	TokenEncryptionKey string
	Debug              bool
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
	// Get MailSend API token from environment, or use default for development
	mailSendAPIToken := os.Getenv("MAILSEND_API_TOKEN")
	if mailSendAPIToken == "" {
		mailSendAPIToken = "your-mailsend-token-here" // Set MAILSEND_API_TOKEN environment variable
	}

	// Get recipient email from environment, or use default for development
	toEmail := os.Getenv("NOTIFICATION_EMAIL")
	if toEmail == "" {
		toEmail = "your-email@example.com" // Set NOTIFICATION_EMAIL environment variable
	}

	// Get from email from environment, or use default
	fromEmail := os.Getenv("FROM_EMAIL")
	if fromEmail == "" {
		fromEmail = "security@webcam-system.local"
	}

	// Determine email method priority
	emailMethod := getEnvString("EMAIL_METHOD", "auto")
	if emailMethod == "auto" {
		// Auto-detect based on available configuration
		if mailSendAPIToken != "" && mailSendAPIToken != "your-mailsend-token-here" {
			emailMethod = "mailersend"
		} else if os.Getenv("GMAIL_CLIENT_ID") != "" {
			emailMethod = "gmail"
		} else {
			emailMethod = "disabled"
		}
	}

	return &Config{
		RecordVideo:   true,
		WebSocketAddr: "localhost:7001",
		EmailMethod:   emailMethod,
		MailSendConfig: MailSendConfig{
			APIToken:  mailSendAPIToken,
			ToEmail:   toEmail,
			FromEmail: fromEmail,
			Debug:     false, // Will be set via command line flag
		},
		GmailOAuth2Config: GmailOAuth2Config{
			ClientID:           getEnvString("GMAIL_CLIENT_ID", ""),
			ClientSecret:       getEnvString("GMAIL_CLIENT_SECRET", ""),
			ToEmail:            toEmail,
			FromEmail:          getEnvString("GMAIL_FROM_EMAIL", ""),
			RedirectURL:        getEnvString("GMAIL_REDIRECT_URL", ""),
			TokenStorePath:     getEnvString("GMAIL_TOKEN_PATH", "./gmail_token.json"),
			TokenEncryptionKey: getEnvString("GMAIL_TOKEN_KEY", ""),
			Debug:              false, // Will be set via command line flag
		},
		VideoConfig: VideoConfig{
			Width:      640,
			Height:     480,
			Framerate:  25,
			BitRate:    500_000,
			OutputPath: "recordings/",
		},
		MotionConfig: MotionConfig{
			MinimumArea:          500,   // Much more sensitive - was 3000
			FrameSkip:            2,     // Process every 2nd frame (every other frame) for verbose testing
			Threshold:            20.0,  // Lower threshold - was 25.0
			DilationSize:         3,
			CooldownPeriod:       10 * time.Second,  // Shorter cooldown - was 30s
			NoMotionDelay:        3 * time.Second,   // Shorter delay - was 10s
			BlurSize:             7,
			MaxConsecutiveFrames: 45,
			MinConsecutiveFrames: 2,     // Need fewer consecutive frames - was 3
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
		Port:      getEnvInt("TURN_PORT", 3479),
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