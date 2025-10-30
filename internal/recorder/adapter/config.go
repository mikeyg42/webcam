// Package adapter provides configuration mapping between the main application config
// and the recorder service config.
package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mikeyg42/webcam/internal/config"
	recorderConfig "github.com/mikeyg42/webcam/internal/recorder/config"
)

// CreateRecorderConfig maps the main application config to recorder config
func CreateRecorderConfig(mainCfg *config.Config) (*recorderConfig.Config, error) {
	// Get temp directory from main config or use default
	tempDir := mainCfg.VideoConfig.OutputPath
	if tempDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		tempDir = filepath.Join(homeDir, ".webcam2", "recordings")
	}

	// Ensure temp directory exists
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	cfg := &recorderConfig.Config{
		// Service configuration
		Service: recorderConfig.ServiceConfig{
			Name:                "webcam2-recorder",
			Environment:         "development",
			ShutdownTimeout:     30 * time.Second,
			HealthCheckInterval: 30 * time.Second,
		},

		// Recording configuration
		Recording: recorderConfig.RecordingConfig{
			// Event-based recording by default (user requested)
			ContinuousEnabled: false,
			EventEnabled:      true,

			// Segment settings
			SegmentDuration: 5 * time.Minute,
			MaxSegmentSize:  100 * 1024 * 1024, // 100MB

			// Buffer configuration for pre-motion capture
			RingBufferSize:  60 * time.Second,
			PreMotionBuffer: 10 * time.Second,
			PostMotionBuffer: 30 * time.Second,

			// Event limits
			MinEventDuration: 3 * time.Second,
			MaxEventDuration: 5 * time.Minute,

			// Retention
			RetentionPeriod: 168 * time.Hour, // 7 days

			// Storage paths
			TempDir:  tempDir,
			SpoolDir: filepath.Join(tempDir, "spool"),
		},

		// Video configuration (from main config)
		Video: recorderConfig.VideoConfig{
			Width:       mainCfg.VideoConfig.Width,
			Height:      mainCfg.VideoConfig.Height,
			FrameRate:   float64(mainCfg.VideoConfig.Framerate),
			Quality:     "high",
			InputFormat: "RGBA",
			ColorSpace:  "sRGB",
			PixelFormat: "yuv420p",
		},

		// Audio configuration (disabled for now)
		Audio: recorderConfig.AudioConfig{
			Enabled:    false,
			SampleRate: 48000,
			Channels:   2,
			BitDepth:   16,
			Codec:      "opus",
			Bitrate:    128000,
		},

		// Encoder configuration
		Encoder: recorderConfig.EncoderConfig{
			Codec:            "h264",
			Profile:          "main",
			Preset:           "balanced",
			Bitrate:          mainCfg.VideoConfig.BitRate,
			MaxBitrate:       mainCfg.VideoConfig.BitRate * 3 / 2,
			BufferSize:       mainCfg.VideoConfig.BitRate * 2,
			KeyframeInterval: 60,
			MaxBFrames:       2,
			HardwareAccel:    true,
			HardwareDevice:   "auto",
			Threads:          0, // auto
		},

		// Storage configuration (MinIO + PostgreSQL)
		Storage: recorderConfig.StorageConfig{
			Type: "minio",

			// MinIO settings
			MinIO: recorderConfig.MinIOConfig{
				Endpoint:        "localhost:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
				UseSSL:          false,
				Bucket:          "recordings",
				Region:          "us-east-1",
				MaxUploads:      10,
				MaxDownloads:    20,
				ConnectTimeout:  30 * time.Second,
				RequestTimeout:  5 * time.Minute,
				PartSize:        64 * 1024 * 1024, // 64MB
				Concurrency:     4,
			},

			// PostgreSQL settings
			Postgres: recorderConfig.PostgresConfig{
				Host:            "localhost",
				Port:            5432,
				Database:        "recordings",
				Username:        "recorder",
				Password:        "recorder",
				SSLMode:         "disable",
				MaxConnections:  25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 5 * time.Minute,
			},

			// Local storage fallback
			Local: recorderConfig.LocalConfig{
				BasePath: tempDir,
			},

			// Upload settings
			UploadConcurrency: 3,
			UploadRetries:     3,
			UploadTimeout:     5 * time.Minute,

			// Cleanup
			CleanupInterval:  1 * time.Hour,
			CleanupBatchSize: 100,
		},

		// Motion detection configuration (from main config)
		Motion: recorderConfig.MotionConfig{
			Enabled:        true,
			Sensitivity:    float64(mainCfg.MotionConfig.Threshold),
			MinConfidence:  0.6,
			MinArea:        100,
			MaxArea:        100000,
			CooldownPeriod: mainCfg.MotionConfig.CooldownPeriod,
			EnableZones:    false,
		},

		// Pipeline configuration
		Pipeline: recorderConfig.PipelineConfig{
			FrameQueueSize:  200,
			EncoderWorkers:  2,
			UploaderWorkers: 3,
		},

		// Buffer configuration
		Buffer: recorderConfig.BufferConfig{
			RingBufferFrames: 450, // 30 seconds at 15fps
			FramePoolSize:    100,
			ImagePoolSize:    50,
			MaxMemoryMB:      512,
		},

		// API configuration (disabled for now)
		API: recorderConfig.APIConfig{
			Enabled:    false,
			ListenAddr: ":8080",
		},

		// Metrics configuration
		Metrics: recorderConfig.MetricsConfig{
			Enabled:           true,
			ListenAddr:        ":9090",
			PrometheusEnabled: true,
			CollectInterval:   10 * time.Second,
		},

		// Logging configuration
		Log: recorderConfig.LogConfig{
			Level:            "info",
			Format:           "console",
			OutputPaths:      []string{"stdout"},
			RotateMaxSize:    100,
			RotateMaxBackups: 3,
			RotateMaxAge:     7,
		},
	}

	return cfg, nil
}

// GetDatabaseDSN returns the PostgreSQL connection string
func GetDatabaseDSN(cfg *recorderConfig.Config) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Storage.Postgres.Username,
		cfg.Storage.Postgres.Password,
		cfg.Storage.Postgres.Host,
		cfg.Storage.Postgres.Port,
		cfg.Storage.Postgres.Database,
		cfg.Storage.Postgres.SSLMode,
	)
}

// ValidateConfig performs basic validation on the recorder config
func ValidateConfig(cfg *recorderConfig.Config) error {
	// Check required storage paths
	if cfg.Recording.TempDir == "" {
		return fmt.Errorf("temp directory is required")
	}

	// Check MinIO settings
	if cfg.Storage.MinIO.Endpoint == "" {
		return fmt.Errorf("MinIO endpoint is required")
	}
	if cfg.Storage.MinIO.Bucket == "" {
		return fmt.Errorf("MinIO bucket is required")
	}

	// Check PostgreSQL settings
	if cfg.Storage.Postgres.Host == "" {
		return fmt.Errorf("PostgreSQL host is required")
	}
	if cfg.Storage.Postgres.Database == "" {
		return fmt.Errorf("PostgreSQL database is required")
	}

	// Check video settings
	if cfg.Video.Width <= 0 || cfg.Video.Height <= 0 {
		return fmt.Errorf("invalid video dimensions: %dx%d", cfg.Video.Width, cfg.Video.Height)
	}
	if cfg.Video.FrameRate <= 0 {
		return fmt.Errorf("invalid frame rate: %f", cfg.Video.FrameRate)
	}

	return nil
}
