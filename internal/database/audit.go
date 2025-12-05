package database

import (
	"log"
	"time"
)

// AuditAction represents types of credential operations
type AuditAction string

const (
	AuditActionCreate   AuditAction = "CREATE"
	AuditActionRead     AuditAction = "READ"
	AuditActionUpdate   AuditAction = "UPDATE"
	AuditActionDelete   AuditAction = "DELETE"
	AuditActionValidate AuditAction = "VALIDATE"
)

// AuditResult represents the outcome of an operation
type AuditResult string

const (
	AuditResultSuccess AuditResult = "SUCCESS"
	AuditResultFailure AuditResult = "FAILURE"
)

// AuditLog logs credential-related operations for security monitoring
func AuditLog(action AuditAction, userEmail, remoteIP string, result AuditResult, details string) {
	log.Printf("[AUDIT] timestamp=%s action=%s user=%s ip=%s result=%s details=%s",
		time.Now().UTC().Format(time.RFC3339),
		action,
		maskEmail(userEmail),
		maskIP(remoteIP),
		result,
		details,
	)
}

// maskEmail partially masks email for privacy
// example@domain.com -> e*****e@domain.com
func maskEmail(email string) string {
	if email == "" {
		return "unknown"
	}

	// Find @ symbol
	atIndex := -1
	for i, char := range email {
		if char == '@' {
			atIndex = i
			break
		}
	}

	if atIndex <= 0 {
		return "invalid-email"
	}

	localPart := email[:atIndex]
	domain := email[atIndex:]

	// Mask middle characters of local part
	if len(localPart) <= 2 {
		return "*" + domain
	}

	masked := string(localPart[0]) + "***" + string(localPart[len(localPart)-1]) + domain
	return masked
}

// maskIP partially masks IP address for privacy
// 192.168.1.100 -> 192.168.*.***
func maskIP(ip string) string {
	if ip == "" {
		return "unknown"
	}

	// Simple IPv4 masking
	count := 0
	result := ""
	for i, char := range ip {
		if char == '.' {
			count++
		}
		if count >= 2 {
			result += ".***.***"
			break
		}
		result += string(ip[i])
	}

	if count < 2 {
		return ip // Return original if not IPv4
	}

	return result
}
