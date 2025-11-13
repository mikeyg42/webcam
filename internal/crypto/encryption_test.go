package crypto

import (
	"testing"
)

func TestGenerateMasterKey(t *testing.T) {
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	if key == "" {
		t.Fatal("Generated key is empty")
	}

	// Generate another key and ensure they're different
	key2, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate second master key: %v", err)
	}

	if key == key2 {
		t.Fatal("Generated keys should be unique")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	testCases := []struct {
		name      string
		plaintext string
	}{
		{"Simple password", "testing123"},
		{"Complex password", "Abcd1234!@#$%^&*()"},
		{"With hyphen", "QWERTY-123456"},
		{"Long password", "ThisIsAVeryLongPasswordWithManyCharacters1234567890!@#$"},
		{"Special chars", "🔐 Unicode 密码"},
		{"Empty string", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encrypt
			encrypted, err := EncryptPassword(tc.plaintext, masterKey)
			if err != nil {
				t.Fatalf("Encryption failed: %v", err)
			}

			// Empty plaintext should result in empty ciphertext
			if tc.plaintext == "" {
				if encrypted != "" {
					t.Fatal("Empty plaintext should result in empty ciphertext")
				}
				return
			}

			// Encrypted should be different from plaintext
			if encrypted == tc.plaintext {
				t.Fatal("Ciphertext should differ from plaintext")
			}

			// Decrypt
			decrypted, err := DecryptPassword(encrypted, masterKey)
			if err != nil {
				t.Fatalf("Decryption failed: %v", err)
			}

			// Should match original
			if decrypted != tc.plaintext {
				t.Fatalf("Decrypted text doesn't match. Expected %q, got %q", tc.plaintext, decrypted)
			}
		})
	}
}

func TestEncryptionUniqueness(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	plaintext := "testing123"

	// Encrypt same plaintext twice
	encrypted1, err := EncryptPassword(plaintext, masterKey)
	if err != nil {
		t.Fatalf("First encryption failed: %v", err)
	}

	encrypted2, err := EncryptPassword(plaintext, masterKey)
	if err != nil {
		t.Fatalf("Second encryption failed: %v", err)
	}

	// Should be different (due to random nonce)
	if encrypted1 == encrypted2 {
		t.Fatal("Same plaintext encrypted twice should produce different ciphertexts")
	}

	// But both should decrypt to same plaintext
	decrypted1, err := DecryptPassword(encrypted1, masterKey)
	if err != nil {
		t.Fatalf("First decryption failed: %v", err)
	}

	decrypted2, err := DecryptPassword(encrypted2, masterKey)
	if err != nil {
		t.Fatalf("Second decryption failed: %v", err)
	}

	if decrypted1 != plaintext || decrypted2 != plaintext {
		t.Fatal("Both ciphertexts should decrypt to original plaintext")
	}
}

func TestWrongKey(t *testing.T) {
	masterKey1, _ := GenerateMasterKey()
	masterKey2, _ := GenerateMasterKey()

	plaintext := "testing123"

	// Encrypt with key1
	encrypted, err := EncryptPassword(plaintext, masterKey1)
	if err != nil {
		t.Fatalf("Encryption failed: %v", err)
	}

	// Try to decrypt with key2
	_, err = DecryptPassword(encrypted, masterKey2)
	if err == nil {
		t.Fatal("Decryption with wrong key should fail")
	}
}

func TestIsEncrypted(t *testing.T) {
	masterKey, _ := GenerateMasterKey()

	testCases := []struct {
		name     string
		input    string
		expected bool
	}{
		{"Empty string", "", false},
		{"Plain password", "testing123", false},
		{"Short base64", "YWJjZA==", false}, // "abcd" encoded - too short
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := IsEncrypted(tc.input)
			if result != tc.expected {
				t.Fatalf("IsEncrypted(%q) = %v, expected %v", tc.input, result, tc.expected)
			}
		})
	}

	// Test with actual encrypted data
	encrypted, _ := EncryptPassword("testing123", masterKey)
	if !IsEncrypted(encrypted) {
		t.Fatal("IsEncrypted should return true for encrypted data")
	}
}
