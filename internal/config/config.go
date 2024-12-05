package config

// Config holds all application configuration
type Config struct {
	WebSocketAddr   string
	MinimumArea     int
	FrameSkip       int
	MailSlurpConfig MailSlurpConfig
	VideoConfig     VideoConfig
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

// NewDefaultConfig returns a Config with default values
func NewDefaultConfig() *Config {
	return &Config{
		WebSocketAddr: "localhost:7000",
		MinimumArea:   3000,
		FrameSkip:     5,
		MailSlurpConfig: MailSlurpConfig{
			SMTPHost: "mx.mailslurp.com",
			SMTPPort: 2525,
			APIKey:   "your-mailslurp-api-key", // Replace with your actual API key
			InboxID:  "your-inbox-id",          // Replace with your actual inbox ID
			ToEmail:  "your-email@example.com", // Replace with your actual email
		},
		VideoConfig: VideoConfig{
			Width:      640,
			Height:     480,
			Framerate:  25,
			BitRate:    500_000,
			OutputPath: "recordings/",
		},
	}
}
