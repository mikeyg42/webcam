// helper functions for working with recorder configuration a thin layer
// above the general config thaty that includes storage specific config types 
// and validates recorder-specific requirements.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikeyg42/webcam/internal/recorder/storage"
)

// CreateStorageConfigs maps main config to storage-package-specific types
func CreateStorageConfigs(cfg *Config) (storage.MinIOConfig, storage.PostgresConfig, error) {
	
	if err:=ValidateConfig(cfg); err !=nil {
		return storage.MinIOConfig{}, storage.PostgresConfig{}, fmt.Errorf("config failed validation for recorder layer: %s", err)
	}

	// MinIO configuration
	minioCfg := storage.MinIOConfig{
		Endpoint:        cfg.Storage.MinIO.Endpoint,
		AccessKeyID:     cfg.Storage.MinIO.AccessKeyID,
		SecretAccessKey: cfg.Storage.MinIO.SecretAccessKey,
		UseSSL:          cfg.Storage.MinIO.UseSSL,
		Bucket:          cfg.Storage.MinIO.Bucket,
		Region:          cfg.Storage.MinIO.Region,
		MaxUploads:      cfg.Storage.MinIO.MaxUploads,
		MaxDownloads:    cfg.Storage.MinIO.MaxDownloads,
		ConnectTimeout:  cfg.Storage.MinIO.ConnectTimeout,
		RequestTimeout:  cfg.Storage.MinIO.RequestTimeout,
		PartSize:        cfg.Storage.MinIO.PartSize * 1024 * 1024, // Convert MB to bytes
		Concurrency:     cfg.Storage.MinIO.Concurrency,
	}

	// PostgreSQL configuration
	pgCfg := storage.PostgresConfig{
		Host:            cfg.Storage.Postgres.Host,
		Port:            cfg.Storage.Postgres.Port,
		Database:        cfg.Storage.Postgres.Database,
		Username:        cfg.Storage.Postgres.Username,
		Password:        cfg.Storage.Postgres.Password,
		SSLMode:         cfg.Storage.Postgres.SSLMode,
		MaxConnections:  cfg.Storage.Postgres.MaxConnections,
		MaxIdleConns:    cfg.Storage.Postgres.MaxIdleConns,
		ConnMaxLifetime: cfg.Storage.Postgres.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Storage.Postgres.ConnMaxIdleTime,
	}

	return minioCfg, pgCfg, nil
}

// GetDatabaseDSN returns the PostgreSQL connection string from main config
func GetDatabaseDSN(cfg *Config) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Storage.Postgres.Username,
		cfg.Storage.Postgres.Password,
		cfg.Storage.Postgres.Host,
		cfg.Storage.Postgres.Port,
		cfg.Storage.Postgres.Database,
		cfg.Storage.Postgres.SSLMode,
	)
}

// ValidateConfig performs recorder-specific validation on the main config
func ValidateConfig(cfg *Config) error {
	// Check required storage paths
	if cfg.Recording.TempDir == "" {
		return fmt.Errorf("recording.temp_dir is required")
	}

	// Ensure temp directory exists
	if err := os.MkdirAll(cfg.Recording.TempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory %s: %w", cfg.Recording.TempDir, err)
	}

	// Ensure spool directory exists
	if cfg.Recording.SpoolDir == "" {
		cfg.Recording.SpoolDir = filepath.Join(cfg.Recording.TempDir, "spool")
	}
	if err := os.MkdirAll(cfg.Recording.SpoolDir, 0755); err != nil {
		return fmt.Errorf("failed to create spool directory %s: %w", cfg.Recording.SpoolDir, err)
	}

	// Check MinIO settings if using MinIO storage
	if cfg.Storage.Type == "minio" || cfg.Storage.Type == "" {
		if cfg.Storage.MinIO.Endpoint == "" {
			return fmt.Errorf("storage.minio.endpoint is required when using MinIO")
		}
		if cfg.Storage.MinIO.Bucket == "" {
			return fmt.Errorf("storage.minio.bucket is required when using MinIO")
		}
	}

	// Check PostgreSQL settings
	if cfg.Storage.Postgres.Host == "" {
		return fmt.Errorf("storage.postgres.host is required for metadata storage")
	}
	if cfg.Storage.Postgres.Database == "" {
		return fmt.Errorf("storage.postgres.database is required for metadata storage")
	}

	// Check video settings
	if cfg.Video.Width <= 0 || cfg.Video.Height <= 0 {
		return fmt.Errorf("invalid video dimensions: %dx%d", cfg.Video.Width, cfg.Video.Height)
	}
	if cfg.Video.FrameRate <= 0 {
		return fmt.Errorf("invalid frame rate: %d", cfg.Video.FrameRate)
	}

	// Check recording buffer settings
	if cfg.Recording.RingBufferSize <= 0 {
		return fmt.Errorf("recording.ring_buffer_size must be positive")
	}

	return nil
}
