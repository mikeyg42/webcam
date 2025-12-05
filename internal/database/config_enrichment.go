package database

import (
	"fmt"
	"log"

	"github.com/mikeyg42/webcam/internal/config"
)

// EnrichConfigWithUserCredentials loads user credentials from database and populates config
// This happens AFTER Tailscale authentication, so we know the user's email
func EnrichConfigWithUserCredentials(cfg *config.Config, db *DB, userEmail string) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}

	if userEmail == "" {
		return fmt.Errorf("user email is empty")
	}

	// Fetch user credentials from database
	creds, err := db.GetUserCredentials(userEmail)
	if err != nil {
		return fmt.Errorf("get user credentials: %w", err)
	}

	if creds == nil {
		// No credentials stored yet - this is okay for first-time users
		log.Printf("No credentials found for user %s - will need to set them", userEmail)
		return nil
	}

	// Populate config with decrypted credentials
	cfg.WebRTC.Username = creds.WebRTCUsername
	cfg.WebRTC.Password = creds.WebRTCPassword

	cfg.Storage.MinIO.AccessKeyID = creds.MinIOAccessKey
	cfg.Storage.MinIO.SecretAccessKey = creds.MinIOSecretKey

	cfg.Storage.Postgres.Username = creds.PostgresUsername
	cfg.Storage.Postgres.Password = creds.PostgresPassword

	// Zero out credentials from memory after copying to config
	// Note: Config struct now holds the credentials, but at least the
	// database-fetched copy is cleared
	secureZero(&creds.WebRTCPassword)
	secureZero(&creds.MinIOSecretKey)
	secureZero(&creds.PostgresPassword)

	log.Printf("✓ Loaded credentials for user %s from secure storage", userEmail)
	return nil
}

// ValidateUserHasCredentials checks if user has stored credentials
func ValidateUserHasCredentials(db *DB, userEmail string) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("database is nil")
	}

	creds, err := db.GetUserCredentials(userEmail)
	if err != nil {
		return false, fmt.Errorf("get user credentials: %w", err)
	}

	return creds != nil, nil
}
