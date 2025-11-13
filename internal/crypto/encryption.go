// Package crypto provides encryption utilities for sensitive data
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// EncryptPassword encrypts a plaintext password using AES-256-GCM
// Returns base64-encoded ciphertext
func EncryptPassword(plaintext, masterKey string) (string, error) {
	if plaintext == "" {
		return "", nil // Empty passwords stay empty
	}

	// Decode master key from base64
	key, err := base64.StdEncoding.DecodeString(masterKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode master key: %w", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt plaintext
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Encode to base64
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptPassword decrypts an encrypted password using AES-256-GCM
// Takes base64-encoded ciphertext
func DecryptPassword(ciphertext, masterKey string) (string, error) {
	if ciphertext == "" {
		return "", nil // Empty ciphertext stays empty
	}

	// Decode master key from base64
	key, err := base64.StdEncoding.DecodeString(masterKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode master key: %w", err)
	}

	// Decode ciphertext from base64
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, encryptedData := data[:nonceSize], data[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// GenerateMasterKey generates a new random 256-bit (32-byte) master key
// Returns base64-encoded key
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32) // 256 bits
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("failed to generate master key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(key), nil
}

// IsEncrypted checks if a string appears to be base64-encoded encrypted data
// This is a heuristic check, not foolproof
func IsEncrypted(data string) bool {
	if data == "" {
		return false
	}

	// Try to decode as base64
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return false
	}

	// Encrypted data should be at least nonce size (12 bytes) + some ciphertext
	return len(decoded) >= 20
}
