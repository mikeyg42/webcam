package validate

import (
	"fmt"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"os/exec"

	"github.com/mikeyg42/webcam/internal/config"
)

// -----------------------------------------------------------------------------
// Top-level full-config validation
// -----------------------------------------------------------------------------

type Validator struct{ errors []string }

func (v *Validator) AddError(format string, args ...interface{}) {
	v.errors = append(v.errors, fmt.Sprintf(format, args...))
}
func (v *Validator) HasErrors() bool  { return len(v.errors) > 0 }
func (v *Validator) Errors() []string { return v.errors }

// ValidateConfig delegates to per-section validators.
func ValidateConfig(cfg *config.Config) error {
	v := &Validator{}

	validateNetworkConfig(v, cfg)
	validateVideoConfig(v, cfg)
	validateMotionConfig(v, cfg)
	validateEmailConfig(v, cfg)
	validateWebRTCAuth(v, &cfg.WebRTC)

	// minimal tailscale validation (enabled ⇒ installed + hostname + writable state dir)
	validateTailscaleConfig(v, &cfg.Tailscale)

	validateGeneralConfig(v, cfg)

	if v.HasErrors() {
		return fmt.Errorf("configuration validation failed:\n%s", strings.Join(v.Errors(), "\n"))
	}
	return nil
}

// -----------------------------------------------------------------------------
// Minimal Tailscale validation
// -----------------------------------------------------------------------------

// ValidateTailscaleConfig is exported for direct callers that only want tailscale checks.
func ValidateTailscaleConfig(cfg *config.TailscaleConfig) error {
	v := &Validator{}
	validateTailscaleConfig(v, cfg)
	if v.HasErrors() {
		return fmt.Errorf("tailscale config invalid:\n%s", strings.Join(v.Errors(), "\n"))
	}
	return nil
}

func validateTailscaleConfig(v *Validator, cfg *config.TailscaleConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	if !isTailscaleInstalled() {
		v.AddError("tailscale is not installed. install from https://tailscale.com/download")
	}
	if strings.TrimSpace(cfg.Hostname) == "" {
		v.AddError("tailscale node name (hostname) cannot be empty")
	}

	// StateDir must exist and be writable. We keep this simple and reliable:
	// mkdir -p; create a temp file; delete it.
	stateDir := strings.TrimSpace(cfg.StateDir)
	if stateDir == "" {
		v.AddError("tailscale state_dir cannot be empty")
		return
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		v.AddError("cannot create state_dir %q: %v", stateDir, err)
		return
	}
	f, err := os.CreateTemp(stateDir, ".ts-permcheck-*")
	if err != nil {
		v.AddError("state_dir %q is not writable: %v", stateDir, err)
		return
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	_, _ = filepath.Abs(stateDir) // normalize for logs (optional)
}

func isTailscaleInstalled() bool {
	_, err := exec.LookPath("tailscale")
	return err == nil
}

// -----------------------------------------------------------------------------
// Other sections (kept as-is, with light sanity checks)
// -----------------------------------------------------------------------------

func validateNetworkConfig(v *Validator, cfg *config.Config) {
	if cfg.WebSocket.ListenAddr == "" {
		v.AddError("WebSocket address cannot be empty")
		return
	}
	host, portStr, err := net.SplitHostPort(cfg.WebSocket.ListenAddr)
	if err != nil {
		v.AddError("WebSocket address must be host:port: %v", err)
		return
	}
	if host != "" && host != "localhost" {
		if ip := net.ParseIP(host); ip == nil && !isValidHostname(host) {
			v.AddError("invalid hostname in WebSocket address: %s", host)
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		v.AddError("invalid port in WebSocket address: %s", portStr)
	}
}

func validateEmailConfig(v *Validator, cfg *config.Config) {
	switch cfg.EmailMethod {
	case "disabled":
		return
	case "mailersend":
		validateMailSendConfig(v, &cfg.MailSendConfig)
	case "gmail":
		validateGmailConfig(v, &cfg.GmailOAuth2Config)
	case "auto":
		v.AddError("email method 'auto' should be resolved during configuration loading")
	default:
		v.AddError("invalid email method: %s (must be 'mailersend', 'gmail', or 'disabled')", cfg.EmailMethod)
	}
}

func validateMailSendConfig(v *Validator, cfg *config.MailSendConfig) {
	if cfg.APIToken == "" || cfg.APIToken == "your-mailsend-token-here" {
		return
	}
	if len(cfg.APIToken) < 10 || !isAlphanumericWithDashes(cfg.APIToken) {
		v.AddError("MailSend API token appears invalid (should be alphanumeric with dashes)")
	}
	if cfg.ToEmail == "" || cfg.ToEmail == "your-email@example.com" {
		v.AddError("recipient email must be configured when MailSend token is provided")
	} else if !isValidEmail(cfg.ToEmail) {
		v.AddError("invalid recipient email: %s", cfg.ToEmail)
	}
	if cfg.FromEmail != "" && !isValidEmail(cfg.FromEmail) {
		v.AddError("invalid from email: %s", cfg.FromEmail)
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
		v.AddError("invalid Gmail recipient email: %s", cfg.ToEmail)
	}
	if cfg.FromEmail != "" && !isValidEmail(cfg.FromEmail) {
		v.AddError("invalid Gmail from email: %s", cfg.FromEmail)
	}
	if cfg.RedirectURL != "" && !isValidURL(cfg.RedirectURL) {
		v.AddError("invalid Gmail redirect URL: %s", cfg.RedirectURL)
	}
	if cfg.TokenStorePath != "" && !isValidFilePath(cfg.TokenStorePath) {
		v.AddError("invalid Gmail token store path: %s", cfg.TokenStorePath)
	}
}

func validateVideoConfig(v *Validator, cfg *config.Config) {
	vcfg := cfg.Video
	if vcfg.Width <= 0 || vcfg.Height <= 0 {
		v.AddError("invalid video dimensions: width=%d height=%d", vcfg.Width, vcfg.Height)
	}
	if vcfg.Width > 4096 || vcfg.Height > 4096 {
		v.AddError("video dimensions too large: %dx%d (max 4096x4096)", vcfg.Width, vcfg.Height)
	}
	aspect := float64(vcfg.Width) / float64(vcfg.Height)
	if aspect < 0.5 || aspect > 3.0 {
		v.AddError("unusual aspect ratio: %dx%d (%.2f)", vcfg.Width, vcfg.Height, aspect)
	}
	if vcfg.FrameRate <= 0 || vcfg.FrameRate > 120 {
		v.AddError("invalid FrameRate: %d (1–120)", vcfg.FrameRate)
	} else if vcfg.FrameRate < 5 {
		v.AddError("FrameRate too low: %d (min 5)", vcfg.FrameRate)
	}
	if vcfg.BitRate <= 0 {
		v.AddError("invalid BitRate: %d", vcfg.BitRate)
	} else {
		// Convert Encoder min/max from kbps to bps for comparison (vcfg.BitRate is in bps)
		minBitRateBps := cfg.Encoder.MinBitRate * 1000
		maxBitRateBps := cfg.Encoder.MaxBitRate * 1000
		if vcfg.BitRate < minBitRateBps {
			v.AddError("BitRate too low: %d bps (min %d kbps = %d bps)", vcfg.BitRate, cfg.Encoder.MinBitRate, minBitRateBps)
		} else if vcfg.BitRate > maxBitRateBps {
			v.AddError("BitRate too high: %d bps (max %d kbps = %d bps)", vcfg.BitRate, cfg.Encoder.MaxBitRate, maxBitRateBps)
		}
	}
	if vcfg.OutputPath == "" || !isValidDirectoryPath(vcfg.OutputPath) {
		v.AddError("invalid output path: %s", vcfg.OutputPath)
	}
}

func validateMotionConfig(v *Validator, configs *config.Config) {
	cfg:=configs.Motion
	if cfg.MinArea <= 0 {
		v.AddError("minimum area must be positive")
	}

	if cfg.LearningRate < 0 || cfg.LearningRate > 1 {
		v.AddError("learning rate must be 0..1")
	}
	if cfg.Threshold < 0 || cfg.Threshold > 255 {
		v.AddError("threshold must be 0..255")
	}
	if cfg.BlurSize%2 == 0 || cfg.BlurSize < 3 {
		v.AddError("blur size must be odd and >=3")
	}
	if cfg.DilationSize < 1 {
		v.AddError("dilation size must be positive")
	}
	if cfg.CooldownPeriod < time.Second {
		v.AddError("cooldown period must be >= 1s")
	}
	if cfg.NoMotionDelay < time.Second {
		v.AddError("no motion delay must be >= 1s")
	}
	if cfg.MinConsecutiveFrames < 1 || cfg.MinConsecutiveFrames > cfg.MaxConsecutiveFrames {
		v.AddError("min consecutive frames must be positive and <= max")
	}
}

func validateWebRTCAuth(v *Validator, cfg *config.WebRTCAuth) {
	if cfg.Username == "" || len(cfg.Username) < 3 || len(cfg.Username) > 64 || !isAlphanumericWithUnderscore(cfg.Username) {
		v.AddError("invalid WebRTC username")
	}
	// Allow encrypted passwords (longer than 128 chars) or require plaintext 8-128 chars
	if cfg.Password == "" || cfg.Password == "change-this-password" {
		v.AddError("invalid WebRTC password: must be set")
	} else if len(cfg.Password) < 8 && len(cfg.Password) <= 128 {
		v.AddError("invalid WebRTC password: must be at least 8 characters")
	}
	// Encrypted passwords (>128 chars) are valid and skip further validation

	// Validate quality priority mode
	validPriorities := map[string]bool{
		"maximize_quality":        true,
		"minimize_latency":        true,
		"minimize_device_strain":  true,
	}
	if cfg.QualityPriority != "" && !validPriorities[cfg.QualityPriority] {
		v.AddError("invalid quality_priority: %s (must be maximize_quality, minimize_latency, or minimize_device_strain)", cfg.QualityPriority)
	}
}

func validateGeneralConfig(v *Validator, cfg *config.Config) {
	if cfg.StatsCollectionInterval <= 0 {
		v.AddError("stats collection interval must be positive")
	} else if cfg.StatsCollectionInterval < time.Second {
		v.AddError("stats collection interval too short (min 1s)")
	} else if cfg.StatsCollectionInterval > 5*time.Minute {
		v.AddError("stats collection interval too long (max 5m)")
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func isValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

func isValidURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func isValidHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > 253 {
		return false
	}
	re := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)
	labels := strings.Split(hostname, ".")
	for _, l := range labels {
		if !re.MatchString(l) {
			return false
		}
	}
	return true
}

func isValidFilePath(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "" && !strings.Contains(path, "\x00")
}

func isValidDirectoryPath(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "" && !strings.Contains(path, "\x00") && !strings.HasPrefix(clean, "..")
}

func isAlphanumericWithDashes(s string) bool {
	if s == "" {
		return false
	}
	return regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`).MatchString(s)
}

func isAlphanumericWithUnderscore(s string) bool {
	if s == "" {
		return false
	}
	return regexp.MustCompile(`^[a-zA-Z0-9_]+$`).MatchString(s)
}
