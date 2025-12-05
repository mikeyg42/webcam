package database

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	MaxCredentialLength = 1024 // Max length for any credential field
	MinPasswordLength   = 12   // Minimum password length (increased from 8)
	MaxEmailLength      = 254  // RFC 5321
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// ValidateUserCredentials performs comprehensive validation on user credentials
func ValidateUserCredentials(creds *UserCredentials) error {
	if creds == nil {
		return fmt.Errorf("credentials cannot be nil")
	}

	// Validate email
	if err := validateEmail(creds.UserEmail); err != nil {
		return fmt.Errorf("invalid user email: %w", err)
	}

	// Validate WebRTC credentials
	if err := validateField("WebRTC username", creds.WebRTCUsername, 3, MaxCredentialLength, false); err != nil {
		return err
	}
	if err := validateField("WebRTC password", creds.WebRTCPassword, MinPasswordLength, MaxCredentialLength, true); err != nil {
		return err
	}

	// Validate MinIO credentials
	if err := validateField("MinIO access key", creds.MinIOAccessKey, 3, MaxCredentialLength, false); err != nil {
		return err
	}
	if err := validateField("MinIO secret key", creds.MinIOSecretKey, MinPasswordLength, MaxCredentialLength, true); err != nil {
		return err
	}

	// Validate PostgreSQL credentials
	if err := validateField("PostgreSQL username", creds.PostgresUsername, 1, MaxCredentialLength, false); err != nil {
		return err
	}
	if err := validateField("PostgreSQL password", creds.PostgresPassword, 1, MaxCredentialLength, true); err != nil {
		return err
	}

	return nil
}

// validateEmail validates an email address
func validateEmail(email string) error {
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}

	if len(email) > MaxEmailLength {
		return fmt.Errorf("email too long (max %d characters)", MaxEmailLength)
	}

	if !emailRegex.MatchString(email) {
		return fmt.Errorf("invalid email format")
	}

	return nil
}

// validateField validates a credential field
func validateField(fieldName, value string, minLen, maxLen int, isPassword bool) error {
	if value == "" {
		return fmt.Errorf("%s cannot be empty", fieldName)
	}

	if len(value) < minLen {
		return fmt.Errorf("%s too short (minimum %d characters)", fieldName, minLen)
	}

	if len(value) > maxLen {
		return fmt.Errorf("%s too long (maximum %d characters)", fieldName, maxLen)
	}

	// Check for null bytes (security issue)
	if strings.Contains(value, "\x00") {
		return fmt.Errorf("%s contains invalid characters", fieldName)
	}

	// Additional password-specific checks
	if isPassword {
		if err := validatePasswordStrength(fieldName, value); err != nil {
			return err
		}
	}

	return nil
}

// validatePasswordStrength performs comprehensive password strength validation
func validatePasswordStrength(fieldName, password string) error {
	// Expanded list of common weak patterns and passwords
	weakPasswords := []string{
		"password", "12345678", "123456789", "1234567890",
		"qwerty", "qwertyuiop", "abc123", "abc123456",
		"letmein", "monkey", "dragon", "iloveyou",
		"welcome", "admin", "root", "test", "user",
		"passw0rd", "p@ssword", "password1", "password123",
		"sunshine", "princess", "football", "baseball",
		"starwars", "master", "superman", "batman",
		"trustno1", "111111", "000000", "login",
		"access", "shadow", "michael", "jennifer",
	}

	lowerPassword := strings.ToLower(password)
	for _, weak := range weakPasswords {
		if strings.Contains(lowerPassword, weak) {
			return fmt.Errorf("%s is too weak (contains common pattern)", fieldName)
		}
	}

	// Check for sequential characters
	if containsSequential(password) {
		return fmt.Errorf("%s is too weak (contains sequential characters)", fieldName)
	}

	// Require at least one uppercase, one lowercase, one number, and one special character
	hasUpper := false
	hasLower := false
	hasNumber := false
	hasSpecial := false

	for _, char := range password {
		if char >= 'A' && char <= 'Z' {
			hasUpper = true
		}
		if char >= 'a' && char <= 'z' {
			hasLower = true
		}
		if char >= '0' && char <= '9' {
			hasNumber = true
		}
		// Common special characters
		if strings.ContainsRune("!@#$%^&*()_+-=[]{}|;:,.<>?/~`", char) {
			hasSpecial = true
		}
	}

	if !hasUpper {
		return fmt.Errorf("%s must contain at least one uppercase letter", fieldName)
	}
	if !hasLower {
		return fmt.Errorf("%s must contain at least one lowercase letter", fieldName)
	}
	if !hasNumber {
		return fmt.Errorf("%s must contain at least one number", fieldName)
	}
	if !hasSpecial {
		return fmt.Errorf("%s must contain at least one special character (!@#$%%^&*...)", fieldName)
	}

	return nil
}

// containsSequential checks for sequential characters (abc, 123, etc.)
func containsSequential(s string) bool {
	s = strings.ToLower(s)
	sequences := []string{
		"abc", "bcd", "cde", "def", "efg", "fgh", "ghi", "hij", "ijk", "jkl", "klm",
		"lmn", "mno", "nop", "opq", "pqr", "qrs", "rst", "stu", "tuv", "uvw", "vwx",
		"wxy", "xyz", "012", "123", "234", "345", "456", "567", "678", "789",
	}
	for _, seq := range sequences {
		if strings.Contains(s, seq) {
			return true
		}
	}
	return false
}
