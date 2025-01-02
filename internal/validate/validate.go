package validate

import (
	"fmt"
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

	validateVideoConfig(v, &cfg.VideoConfig)
	validateMotionConfig(v, &cfg.MotionConfig)
	validateMailSlurpConfig(v, &cfg.MailSlurpConfig)

	if v.HasErrors() {
		return fmt.Errorf("configuration validation failed:\n%s",
			strings.Join(v.Errors(), "\n"))
	}
	return nil
}

func validateMailSlurpConfig(v *Validator, cfg *config.MailSlurpConfig) {
	if cfg.APIKey == "" {
		v.AddError("MailSlurp API key cannot be empty")
	}
	if cfg.InboxID == "" {
		v.AddError("MailSlurp inbox ID cannot be empty")
	}
	if cfg.SMTPPort <= 0 || cfg.SMTPPort > 65535 {
		v.AddError("invalid SMTP port: %d", cfg.SMTPPort)
	}
	if cfg.ToEmail == "" {
		v.AddError("recipient email cannot be empty")
	}
}

func validateVideoConfig(v *Validator, cfg *config.VideoConfig) {
	if cfg.Width == 0 || cfg.Height == 0 {
		v.AddError("invalid video dimensions: width=%d, height=%d",
			cfg.Width, cfg.Height)
	}
	if cfg.Framerate <= 0 {
		v.AddError("invalid framerate: %d", cfg.Framerate)
	}
	if cfg.BitRate <= 0 {
		v.AddError("invalid bitrate: %d", cfg.BitRate)
	}
	if cfg.OutputPath == "" {
		v.AddError("output path cannot be empty")
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
