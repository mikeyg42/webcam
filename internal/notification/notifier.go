package notification

import (
	"time"
)

// Notifier interface for sending notifications
type Notifier interface {
	SendNotification() error
}

// Shared types for notification system
type RetryConfig struct {
	MaxAttempts int
	Delay       time.Duration
	MaxDelay    time.Duration
}