package validate

import (
	"fmt"
	"net"
	"net/mail"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikeyg42/webcam/internal/config"
)

type Validator struct {
	errors []string
}

func (v *Validator) AddError(format string, args ...interface{}) {
	v.errors = append(v.errors, fmt.Sprintf(format, args...))
}

func (v *Validator) HasErrors() bool {
	return len(v.errors) > 0
}

func (v *Validator) Errors() []string {
	return v.errors
}

func ValidateConfig(cfg *config.Config) error {
	v := &Validator{}

	// Validate all config sections
	validateNetworkConfig(v, cfg)
	validateVideoConfig(v, &cfg.VideoConfig)
	validateMotionConfig(v, &cfg.MotionConfig)
	validateEmailConfig(v, cfg)
	validateWebRTCAuth(v, &cfg.WebrtcAuth)
	validateTailscaleConfig(v, &cfg.TailscaleConfig)
	validateGeneralConfig(v, cfg)

	if v.HasErrors() {
		return fmt.Errorf("configuration validation failed:\n%s",
			strings.Join(v.Errors(), "\n"))
	}
	return nil
}

// validateNetworkConfig validates network-related settings
func validateNetworkConfig(v *Validator, cfg *config.Config) {
	if cfg.WebSocketAddr == "" {
		v.AddError("WebSocket address cannot be empty")
		return
	}

	// Validate host:port format
	host, portStr, err := net.SplitHostPort(cfg.WebSocketAddr)
	if err != nil {
		v.AddError("WebSocket address must be in host:port format: %v", err)
		return
	}

	// Validate hostname/IP
	if host != "" && host != "localhost" {
		if ip := net.ParseIP(host); ip == nil {
			// Not an IP, check if it's a valid hostname
			if !isValidHostname(host) {
				v.AddError("invalid hostname in WebSocket address: %s", host)
			}
		}
	}

	// Validate port
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		v.AddError("invalid port in WebSocket address: %s", portStr)
	}
}

// validateEmailConfig validates email configuration based on method
func validateEmailConfig(v *Validator, cfg *config.Config) {
	switch cfg.EmailMethod {
	case "disabled":
		// No validation needed for disabled email
		return
	case "mailersend":
		validateMailSendConfig(v, &cfg.MailSendConfig)
	case "gmail":
		validateGmailConfig(v, &cfg.GmailOAuth2Config)
	case "auto":
		// Auto-detection is handled in config loading, shouldn't reach here
		v.AddError("email method 'auto' should be resolved during configuration loading")
	default:
		v.AddError("invalid email method: %s (must be 'mailersend', 'gmail', or 'disabled')", cfg.EmailMethod)
	}
}

func validateMailSendConfig(v *Validator, cfg *config.MailSendConfig) {
	// MailSend validation is optional - the system should work without it
	if cfg.APIToken == "" || cfg.APIToken == "your-mailsend-token-here" {
		// This is acceptable - email notifications will be disabled
		return
	}

	// Validate API token format (basic check)
	if len(cfg.APIToken) < 10 || !isAlphanumericWithDashes(cfg.APIToken) {
		v.AddError("MailSend API token appears invalid (should be alphanumeric with dashes)")
	}

	if cfg.ToEmail == "" || cfg.ToEmail == "your-email@example.com" {
		v.AddError("recipient email must be configured when MailSend token is provided")
	} else if !isValidEmail(cfg.ToEmail) {
		v.AddError("invalid recipient email format: %s", cfg.ToEmail)
	}

	if cfg.FromEmail != "" && !isValidEmail(cfg.FromEmail) {
		v.AddError("invalid from email format: %s", cfg.FromEmail)
	}
}

func validateGmailConfig(v *Validator, cfg *config.GmailOAuth2Config) {
	if cfg.ClientID == "" {
		v.AddError("Gmail OAuth2 client ID is required")
	}
	if cfg.ClientSecret == "" {
		v.AddError("Gmail OAuth2 client secret is required")
	}
	if cfg.ToEmail == "" || cfg.ToEmail == "your-email@example.com" {
		v.AddError("recipient email must be configured for Gmail")
	} else if !isValidEmail(cfg.ToEmail) {
		v.AddError("invalid Gmail recipient email format: %s", cfg.ToEmail)
	}
	if cfg.FromEmail != "" && !isValidEmail(cfg.FromEmail) {
		v.AddError("invalid Gmail from email format: %s", cfg.FromEmail)
	}
	if cfg.RedirectURL != "" && !isValidURL(cfg.RedirectURL) {
		v.AddError("invalid Gmail redirect URL format: %s", cfg.RedirectURL)
	}
	if cfg.TokenStorePath != "" && !isValidFilePath(cfg.TokenStorePath) {
		v.AddError("invalid Gmail token store path: %s", cfg.TokenStorePath)
	}
}

func validateVideoConfig(v *Validator, cfg *config.VideoConfig) {
	// Validate dimensions
	if cfg.Width <= 0 || cfg.Height <= 0 {
		v.AddError("invalid video dimensions: width=%d, height=%d (must be positive)",
			cfg.Width, cfg.Height)
	}
	if cfg.Width > 4096 || cfg.Height > 4096 {
		v.AddError("video dimensions too large: width=%d, height=%d (max 4096x4096)",
			cfg.Width, cfg.Height)
	}
	// Check for common aspect ratios and warn if unusual
	aspectRatio := float64(cfg.Width) / float64(cfg.Height)
	if aspectRatio < 0.5 || aspectRatio > 3.0 {
		v.AddError("unusual aspect ratio: %dx%d (ratio=%.2f), consider standard ratios like 16:9 or 4:3",
			cfg.Width, cfg.Height, aspectRatio)
	}

	// Validate framerate
	if cfg.Framerate <= 0 {
		v.AddError("invalid framerate: %d (must be positive)", cfg.Framerate)
	} else if cfg.Framerate > 120 {
		v.AddError("framerate too high: %d (max 120 fps)", cfg.Framerate)
	} else if cfg.Framerate < 5 {
		v.AddError("framerate too low: %d (min 5 fps for security camera)", cfg.Framerate)
	}

	// Validate bitrate
	if cfg.BitRate <= 0 {
		v.AddError("invalid bitrate: %d (must be positive)", cfg.BitRate)
	} else if cfg.BitRate < config.MinBitrate {
		v.AddError("bitrate too low: %d (min %d bps)", cfg.BitRate, config.MinBitrate)
	} else if cfg.BitRate > config.MaxBitrate {
		v.AddError("bitrate too high: %d (max %d bps)", cfg.BitRate, config.MaxBitrate)
	}

	// Validate output path
	if cfg.OutputPath == "" {
		v.AddError("output path cannot be empty")
	} else if !isValidDirectoryPath(cfg.OutputPath) {
		v.AddError("invalid output path: %s", cfg.OutputPath)
	}
}

func validateMotionConfig(v *Validator, cfg *config.MotionConfig) {
	if cfg.MinimumArea <= 0 {
		v.AddError("minimum area must be positive")
	}
	if cfg.FrameSkip < 1 {
		v.AddError("frame skip must be at least 1")
	}
	if cfg.LearningRate < 0 || cfg.LearningRate > 1 {
		v.AddError("learning rate must be between 0 and 1")
	}
	if cfg.Threshold < 0 || cfg.Threshold > 255 {
		v.AddError("threshold must be between 0 and 255")
	}
	if cfg.BlurSize%2 == 0 || cfg.BlurSize < 3 {
		v.AddError("blur size must be odd and at least 3")
	}
	if cfg.DilationSize < 1 {
		v.AddError("dilation size must be positive")
	}
	if cfg.CooldownPeriod < time.Second {
		v.AddError("cooldown period must be at least 1 second")
	}
	if cfg.NoMotionDelay < time.Second {
		v.AddError("no motion delay must be at least 1 second")
	}
	if cfg.MaxConsecutiveFrames < 1 {
		v.AddError("max consecutive frames must be positive")
	}
	if cfg.MinConsecutiveFrames < 1 || cfg.MinConsecutiveFrames > cfg.MaxConsecutiveFrames {
		v.AddError("min consecutive frames must be positive and less than max")
	}
}

// validateWebRTCAuth validates WebRTC authentication settings
func validateWebRTCAuth(v *Validator, cfg *config.WebRTCAuth) {
	if cfg.Username == "" {
		v.AddError("WebRTC username cannot be empty")
	} else if len(cfg.Username) < 3 {
		v.AddError("WebRTC username too short (minimum 3 characters)")
	} else if len(cfg.Username) > 64 {
		v.AddError("WebRTC username too long (maximum 64 characters)")
	} else if !isAlphanumericWithUnderscore(cfg.Username) {
		v.AddError("WebRTC username must be alphanumeric with underscores only")
	}

	if cfg.Password == "" {
		v.AddError("WebRTC password cannot be empty")
	} else if cfg.Password == "change-this-password" {
		v.AddError("WebRTC password must be changed from default value")
	} else if len(cfg.Password) < 8 {
		v.AddError("WebRTC password too short (minimum 8 characters)")
	} else if len(cfg.Password) > 128 {
		v.AddError("WebRTC password too long (maximum 128 characters)")
	}
}

// validateTailscaleConfig validates Tailscale configuration
func validateTailscaleConfig(v *Validator, cfg *config.TailscaleConfig) {
	if !cfg.Enabled {
		// No validation needed when Tailscale is disabled
		return
	}

	if cfg.NodeName == "" {
		v.AddError("Tailscale node name cannot be empty when Tailscale is enabled")
	} else if !isValidHostname(cfg.NodeName) {
		v.AddError("invalid Tailscale node name: %s", cfg.NodeName)
	}

	if cfg.AuthKey == "" {
		v.AddError("Tailscale auth key is required when Tailscale is enabled")
	} else if !isTailscaleAuthKey(cfg.AuthKey) {
		v.AddError("invalid Tailscale auth key format")
	}

	if cfg.ListenPort <= 0 || cfg.ListenPort > 65535 {
		v.AddError("invalid Tailscale listen port: %d (must be 1-65535)", cfg.ListenPort)
	}

	if cfg.DERPMap != "" && !isValidURL(cfg.DERPMap) {
		v.AddError("invalid Tailscale DERP map URL: %s", cfg.DERPMap)
	}

	if cfg.Hostname != "" && !isValidHostname(cfg.Hostname) {
		v.AddError("invalid Tailscale hostname: %s", cfg.Hostname)
	}
}

// validateGeneralConfig validates general application settings
func validateGeneralConfig(v *Validator, cfg *config.Config) {
	if cfg.StatsCollectionInterval <= 0 {
		v.AddError("stats collection interval must be positive")
	} else if cfg.StatsCollectionInterval < time.Second {
		v.AddError("stats collection interval too short (minimum 1 second)")
	} else if cfg.StatsCollectionInterval > 5*time.Minute {
		v.AddError("stats collection interval too long (maximum 5 minutes)")
	}
}

// Helper validation functions

func isValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

func isValidURL(urlStr string) bool {
	if urlStr == "" {
		return false
	}
	// Basic URL validation - check for http/https scheme
	return strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://")
}

func isValidHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > 253 {
		return false
	}
	// Basic hostname validation
	hostnameRegex := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)
	labels := strings.Split(hostname, ".")
	for _, label := range labels {
		if !hostnameRegex.MatchString(label) {
			return false
		}
	}
	return true
}

func isValidFilePath(path string) bool {
	if path == "" {
		return false
	}
	// Check for invalid characters
	cleanPath := filepath.Clean(path)
	return cleanPath != "" && !strings.Contains(path, "\x00")
}

func isValidDirectoryPath(path string) bool {
	if path == "" {
		return false
	}
	// Clean the path and check it's reasonable
	cleanPath := filepath.Clean(path)
	return cleanPath != "" && !strings.Contains(path, "\x00") && !strings.HasPrefix(cleanPath, "..")
}

func isAlphanumericWithDashes(s string) bool {
	if s == "" {
		return false
	}
	alphanumericRegex := regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`)
	return alphanumericRegex.MatchString(s)
}

func isAlphanumericWithUnderscore(s string) bool {
	if s == "" {
		return false
	}
	alphanumericRegex := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	return alphanumericRegex.MatchString(s)
}

func isTailscaleAuthKey(key string) bool {
	if key == "" {
		return false
	}
	// Basic Tailscale auth key validation
	// Keys typically start with "tskey-" and contain alphanumeric characters
	return strings.HasPrefix(key, "tskey-") && len(key) > 20 && isAlphanumericWithDashes(key[6:])
}

// ValidateUserInput provides general user input sanitization
func ValidateUserInput(input string, fieldName string, maxLength int) error {
	v := &Validator{}

	if input == "" {
		v.AddError("%s cannot be empty", fieldName)
	}

	if len(input) > maxLength {
		v.AddError("%s exceeds maximum length of %d characters", fieldName, maxLength)
	}

	// Check for potentially dangerous characters
	if strings.ContainsAny(input, "\x00\r\n<>\"';&|`$(){}") {
		v.AddError("%s contains invalid characters", fieldName)
	}

	if v.HasErrors() {
		return fmt.Errorf("input validation failed for %s: %s", fieldName, strings.Join(v.Errors(), ", "))
	}
	return nil
}

// ValidateRoomID validates WebSocket room IDs
func ValidateRoomID(roomID string) error {
	v := &Validator{}

	if roomID == "" {
		v.AddError("room ID cannot be empty")
	} else if len(roomID) > 64 {
		v.AddError("room ID too long (max 64 characters)")
	} else if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(roomID) {
		v.AddError("room ID contains invalid characters (only alphanumeric, underscore, and dash allowed)")
	}

	if v.HasErrors() {
		return fmt.Errorf("room ID validation failed: %s", strings.Join(v.Errors(), ", "))
	}
	return nil
}
