package config

import "time"

// Config holds all application configuration
type Config struct {
	WebSocketAddr   string
	MailSlurpConfig MailSlurpConfig
	VideoConfig     VideoConfig
	MotionConfig    MotionConfig
}

type MailSlurpConfig struct {
	APIKey   string
	InboxID  string
	SMTPHost string
	SMTPPort int
	ToEmail  string
}

type VideoConfig struct {
	Width      int
	Height     int
	Framerate  int
	BitRate    int
	OutputPath string
}

type MotionConfig struct {
	MinimumArea    int           // Minimum area size for motion detection
	FrameSkip      int           // Process every nth frame
	Threshold      float32       // Threshold for motion detection
	DilationSize   int           // Size of dilation kernel
	CooldownPeriod time.Duration // Minimum time between notifications
	NoMotionDelay  time.Duration // Duration to wait before declaring no motion
}

// NewDefaultConfig returns a Config with default values
func NewDefaultConfig() *Config {
	return &Config{
		WebSocketAddr: "localhost:7000",
		MailSlurpConfig: MailSlurpConfig{
			SMTPHost: "mx.mailslurp.com",
			SMTPPort: 2525,
			APIKey:   "your-mailslurp-api-key",
			InboxID:  "your-inbox-id",
			ToEmail:  "your-email@example.com",
		},
		VideoConfig: VideoConfig{
			Width:      640,
			Height:     480,
			Framerate:  25,
			BitRate:    500_000,
			OutputPath: "recordings/",
		},
		MotionConfig: MotionConfig{
			MinimumArea:    3000,
			FrameSkip:      5,
			Threshold:      25.0,
			DilationSize:   3,
			CooldownPeriod: 30 * time.Second,
			NoMotionDelay:  10 * time.Second,
		},
	}
}
