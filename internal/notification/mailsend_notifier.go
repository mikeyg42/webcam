package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// MailSendConfig holds configuration for MailSend API
type MailSendConfig struct {
	APIToken string
	ToEmail  string
	FromEmail string
	Debug    bool
}

// MailSendNotifier implements the Notifier interface using MailSend API
type MailSendNotifier struct {
	config      *MailSendConfig
	httpClient  *http.Client
	startupTime time.Time
	systemName  string
}

// MailSendEmailRequest represents the raw MIME message for MailerSend API
type MailSendEmailRequest struct {
	From    EmailRecipient   `json:"from"`
	To      []EmailRecipient `json:"to"`
	Subject string           `json:"subject,omitempty"`
	RawMessage string        `json:"raw,omitempty"`
}

// EmailRecipient represents an email recipient with name and address
type EmailRecipient struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// MailSendResponse represents the response from MailSend API
type MailSendResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	ID      string `json:"id,omitempty"`
}

const (
	mailSendAPIURL = "https://api.mailersend.com/v1/email"
)

// NewMailSendNotifier creates a new notifier that uses MailSend for sending emails
func NewMailSendNotifier(config *MailSendConfig, systemName string) (*MailSendNotifier, error) {
	if config.APIToken == "" {
		return nil, fmt.Errorf("MailSend API token is required")
	}

	if config.ToEmail == "" {
		return nil, fmt.Errorf("notification email address is required")
	}

	// Set default from email if not provided
	if config.FromEmail == "" {
		config.FromEmail = "security@webcam-system.local"
	}

	// Set default system name if not provided
	if systemName == "" {
		systemName = "WebCam Security System"
	}

	notifier := &MailSendNotifier{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		startupTime: time.Now(),
		systemName:  systemName,
	}

	if config.Debug {
		log.Printf("MailSend notifier initialized with token: %s...",
			config.APIToken[:10])
	}

	return notifier, nil
}

// SendNotification sends a notification via MailSend API
func (n *MailSendNotifier) SendNotification() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return n.SendNotificationWithContext(ctx)
}

// SendNotificationWithContext sends notification with context for cancellation
func (n *MailSendNotifier) SendNotificationWithContext(ctx context.Context) error {
	// Create enhanced email data
	emailData := GenerateEnhancedEmailData(time.Now(), n.systemName)

	// Create professional motion alert email
	professionalEmail, err := CreateProfessionalMotionAlert(emailData, n.config.FromEmail, n.config.ToEmail, n.systemName)
	if err != nil {
		return fmt.Errorf("failed to create professional email: %v", err)
	}

	// Build MIME message with all professional headers
	mimeMessage, err := BuildMIMEMessage(professionalEmail)
	if err != nil {
		return fmt.Errorf("failed to build MIME message: %v", err)
	}

	// Create email request using raw MIME content
	emailRequest := MailSendEmailRequest{
		From: EmailRecipient{
			Email: n.config.FromEmail,
			Name:  "Security Cam Motion Detector",
		},
		To: []EmailRecipient{
			{
				Email: n.config.ToEmail,
				Name:  "",
			},
		},
		RawMessage: string(mimeMessage),
	}

	// Send email with retry mechanism
	return n.sendWithRetry(ctx, emailRequest)
}


// sendWithRetry sends email with retry mechanism using shared utility
func (n *MailSendNotifier) sendWithRetry(ctx context.Context, emailRequest MailSendEmailRequest) error {
	retryConfig := RetryConfig{
		MaxAttempts: 3,
		Delay:       time.Second,
		MaxDelay:    5 * time.Second,
	}

	return SendWithRetry(ctx, retryConfig, func(ctx context.Context) error {
		return n.sendEmail(ctx, emailRequest)
	})
}

// sendEmail sends the email via MailSend API
func (n *MailSendNotifier) sendEmail(ctx context.Context, emailRequest MailSendEmailRequest) error {
	// Prepare JSON payload
	jsonData, err := json.Marshal(emailRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal email request: %v", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", mailSendAPIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %v", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+n.config.APIToken)

	// Send request
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer resp.Body.Close()

	// Check response status (MailerSend returns 202 Accepted on success)
	if resp.StatusCode != http.StatusAccepted {
		// Try to read error response
		var errorBody []byte
		if resp.Body != nil {
			errorBody, _ = io.ReadAll(resp.Body)
		}
		return fmt.Errorf("MailerSend API error (status %d): %s", resp.StatusCode, string(errorBody))
	}

	// Get message ID from response headers
	messageID := resp.Header.Get("x-message-id")

	if n.config.Debug {
		log.Printf("MailerSend API success (Message ID: %s)", messageID)
	}

	return nil
}

// SendDebugEmail sends a debug email to test the MailSend configuration
func (n *MailSendNotifier) SendDebugEmail(ctx context.Context) error {
	// Create enhanced email data for the test email
	emailData := GenerateEnhancedEmailData(time.Now(), n.systemName)

	// Create professional test email
	professionalEmail, err := CreateProfessionalTestEmail(emailData, n.config.FromEmail, n.config.ToEmail, n.systemName)
	if err != nil {
		return fmt.Errorf("failed to create professional test email: %v", err)
	}

	// Build MIME message with all professional headers
	mimeMessage, err := BuildMIMEMessage(professionalEmail)
	if err != nil {
		return fmt.Errorf("failed to build MIME message: %v", err)
	}

	// Create email request using raw MIME content
	emailRequest := MailSendEmailRequest{
		From: EmailRecipient{
			Email: n.config.FromEmail,
			Name:  "Security Cam Motion Detector",
		},
		To: []EmailRecipient{
			{
				Email: n.config.ToEmail,
				Name:  "",
			},
		},
		RawMessage: string(mimeMessage),
	}

	return n.sendWithRetry(ctx, emailRequest)
}

// Close closes any resources used by the notifier
func (n *MailSendNotifier) Close() error {
	// No persistent connections to close for HTTP-based API
	return nil
}