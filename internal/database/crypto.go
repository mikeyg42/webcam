package database

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

// getSaltFilePath returns the path to the salt file
func getSaltFilePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".webcam2", "crypto.salt"), nil
}

// getOrCreateSalt retrieves or generates a per-installation salt
func getOrCreateSalt() ([]byte, error) {
	saltPath, err := getSaltFilePath()
	if err != nil {
		return nil, err
	}

	// Try to read existing salt
	salt, err := os.ReadFile(saltPath)
	if err == nil && len(salt) == 32 {
		return salt, nil
	}

	// Generate new random salt
	salt = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	// Ensure directory exists
	saltDir := filepath.Dir(saltPath)
	if err := os.MkdirAll(saltDir, 0700); err != nil {
		return nil, fmt.Errorf("create salt directory: %w", err)
	}

	// Save salt to file with restricted permissions
	if err := os.WriteFile(saltPath, salt, 0600); err != nil {
		return nil, fmt.Errorf("save salt: %w", err)
	}

	return salt, nil
}

// getEncryptionKey retrieves the encryption key from environment
// SECURITY: This function will fail if CREDENTIALS_ENCRYPTION_KEY is not set
func getEncryptionKey() ([]byte, error) {
	keyEnv := os.Getenv("CREDENTIALS_ENCRYPTION_KEY")

	if keyEnv == "" {
		return nil, fmt.Errorf("CREDENTIALS_ENCRYPTION_KEY environment variable is required for credential encryption")
	}

	// Validate key length (minimum 16 characters for security)
	if len(keyEnv) < 16 {
		return nil, fmt.Errorf("CREDENTIALS_ENCRYPTION_KEY must be at least 16 characters long (got %d)", len(keyEnv))
	}

	// Get per-installation salt for key derivation
	salt, err := getOrCreateSalt()
	if err != nil {
		return nil, fmt.Errorf("get salt: %w", err)
	}

	// Use Argon2id for proper key derivation (more secure than SHA-256)
	// Argon2id parameters: time=3, memory=64MB, threads=4, keyLen=32
	// Increased time parameter from 1 to 3 for better security
	key := argon2.IDKey([]byte(keyEnv), salt, 3, 64*1024, 4, 32)
	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM
func Encrypt(plaintext string) ([]byte, error) {
	key, err := getEncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("get encryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM
func Decrypt(ciphertext []byte) (string, error) {
	key, err := getEncryptionKey()
	if err != nil {
		return "", fmt.Errorf("get encryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// EncryptToBase64 encrypts and returns base64-encoded string (for display/debugging)
func EncryptToBase64(plaintext string) (string, error) {
	encrypted, err := Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// DecryptFromBase64 decrypts from base64-encoded string
func DecryptFromBase64(encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	return Decrypt(ciphertext)
}
