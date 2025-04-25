package notification

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"github.com/antihax/optional"
	mailslurp "github.com/mailslurp/mailslurp-client-go"
)

// Notifier interface for sending notifications
type Notifier interface {
	SendNotification() error
}

type MailSlurpConfig struct {
	APIKey   string
	InboxID  string
	SMTPHost string
	SMTPPort int
	ToEmail  string
	Debug    bool 
	NoAPIMode bool
}

// MailSlurpNotifier implements the Notifier interface using MailSlurp email service
type MailSlurpNotifier struct {
	config        *MailSlurpConfig
	client        *mailslurp.APIClient
	ctx           context.Context
	inbox         *mailslurp.InboxDto
	smtpAccess    *mailslurp.ImapSmtpAccessDetails
	auth          smtp.Auth // Changed from sasl.Client to smtp.Auth
	startupTime   time.Time
	emailTemplate string
	debug         bool
	noAPIMode     bool
}

type EmailTemplate struct {
	Subject string
	Body    string
}

type EmailData struct {
	Time     string
	Location string
	Details  string
}

type RetryConfig struct {
	MaxAttempts int
	Delay       time.Duration
	MaxDelay    time.Duration
}

const (
	defaultEmailSubject = "Security Alert: Motion Detected"
	defaultEmailBody    = `Motion detected at {{.Time}}
	Location: {{.Location}}
	Details: {{.Details}}
	Please check your security system.`
)

func NewEmailTemplate() *EmailTemplate {
	return &EmailTemplate{
		Subject: defaultEmailSubject,
		Body:    defaultEmailBody,
	}
}

func (t *EmailTemplate) Execute(data EmailData) (string, error) {
	tmpl, err := template.New("email").Parse(t.Body)
	if err != nil {
		return "", fmt.Errorf("failed to parse email template: %v", err)
	}

	var bodyBuffer bytes.Buffer
	if err := tmpl.Execute(&bodyBuffer, data); err != nil {
		return "", fmt.Errorf("failed to execute email template: %v", err)
	}

	return bodyBuffer.String(), nil
}

// NewMailSlurpNotifier creates a new notifier that uses MailSlurp for sending emails
func NewMailSlurpNotifier(config *MailSlurpConfig) (*MailSlurpNotifier, error) {
	ctx := context.WithValue(context.Background(), mailslurp.ContextAPIKey,
		mailslurp.APIKey{Key: config.APIKey})

	// Create default email template
	defaultTemplate := defaultEmailBody

	notifier := &MailSlurpNotifier{
		config:        config,
		client:        mailslurp.NewAPIClient(mailslurp.NewConfiguration()),
		ctx:           ctx,
		debug:         config.Debug,
		startupTime:   time.Now(),
		emailTemplate: defaultTemplate, // Initialize the email template
	}

	if !notifier.noAPIMode {
		if err := notifier.initialize(); err != nil {
			return nil, err
		}
	}

	return notifier, nil
}

func (n *MailSlurpNotifier) initialize() error {
	var err error
	var inbox mailslurp.InboxDto

	if strings.TrimSpace(n.config.InboxID) != "" {
		inbox, _, err = n.client.InboxControllerApi.GetInbox(n.ctx, n.config.InboxID)
		if err != nil {
			return fmt.Errorf("failed to get inbox: %v", err)
		}
	} else {
		inboxOpts := &mailslurp.CreateInboxOpts{
			InboxType: optional.NewString("SMTP_INBOX"),
		}
		inbox, _, err = n.client.InboxControllerApi.CreateInbox(n.ctx, inboxOpts)
		if err != nil {
			return fmt.Errorf("failed to create inbox: %v", err)
		}
		n.config.InboxID = inbox.Id
	}
	n.inbox = &inbox

	// Get SMTP access details
	accessOpts := &mailslurp.GetImapSmtpAccessOpts{
		InboxId: optional.NewInterface(n.config.InboxID),
	}
	access, _, err := n.client.InboxControllerApi.GetImapSmtpAccess(n.ctx, accessOpts)
	if err != nil {
		return fmt.Errorf("failed to get SMTP access: %v", err)
	}
	n.smtpAccess = &access

	// Create SMTP auth using PlainAuth
	n.auth = smtp.PlainAuth("", access.SmtpUsername, access.SmtpPassword, n.config.SMTPHost)

	log.Printf("Initialized MailSlurp inbox with ID: %s and email address: %s\n",
		inbox.Id, inbox.EmailAddress)
	return nil
}

// SendNotification sends a notification via email
func (n *MailSlurpNotifier) SendNotification() error {
	if n.noAPIMode {
		log.Printf("MOTION DETECTED at %s (email notifications disabled)",
			time.Now().Format(time.RFC1123))
		return nil
	}

	ctx, cancel := context.WithTimeout(n.ctx, 30*time.Second)
	defer cancel()

	return n.SendNotificationWithContext(ctx)
}

func (n *MailSlurpNotifier) SendNotificationWithContext(ctx context.Context) error {
	if n.client == nil {
		return fmt.Errorf("MailSlurp client not initialized")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context invalid before sending notification: %v", err)
	}

	return n.sendEmail(ctx)
}

func formatEmail(to, subject, body string) []byte {
	return []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: %s\r\n"+
		"Content-Type: text/plain; charset=UTF-8\r\n"+
		"\r\n"+
		"%s", to, subject, body))
}

func (n *MailSlurpNotifier) prepareEmailBody(ctx context.Context) (string, error) {
	// Create template with context awareness
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
		tmpl, err := template.New("email").Parse(n.emailTemplate)
		if err != nil {
			return "", fmt.Errorf("failed to parse template: %v", err)
		}

		var bodyBuffer bytes.Buffer
		data := map[string]string{
			"Time": time.Now().Format(time.RFC1123),
		}

		if err := tmpl.Execute(&bodyBuffer, data); err != nil {
			return "", fmt.Errorf("failed to execute template: %v", err)
		}

		return bodyBuffer.String(), nil
	}
}

func (n *MailSlurpNotifier) sendEmail(ctx context.Context) error {
	// Check context before proceeding
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context error before sending email: %v", err)
	}

	// Parse email template with timeout
	tmplCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var emailBody string
	done := make(chan error, 1)
	go func() {
		var err error
		emailBody, err = n.prepareEmailBody(tmplCtx)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("failed to prepare email: %v", err)
		}
	case <-tmplCtx.Done():
		return fmt.Errorf("template preparation timed out: %v", tmplCtx.Err())
	}

	// Prepare email message
	msg := formatEmail(
		n.config.ToEmail,
		"Security Alert: Motion Detected",
		emailBody,
	)

	// Send email with retry mechanism
	return n.sendWithRetry(ctx, msg)
}

// Close closes any resources used by the notifier
func (n *MailSlurpNotifier) Close() error {
	// wait for pending notifications to complete
	// Close any open connections
	return nil
}

func (n *MailSlurpNotifier) sendWithRetry(ctx context.Context, msg []byte) error {
	retryConfig := RetryConfig{
		MaxAttempts: 3,
		Delay:       time.Second,
		MaxDelay:    5 * time.Second,
	}

	var lastErr error
	for attempt := 0; attempt < retryConfig.MaxAttempts; attempt++ {
		// Check context before each attempt
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during retry: %v", err)
		}

		if attempt > 0 {
			delay := time.Duration(attempt) * retryConfig.Delay
			if delay > retryConfig.MaxDelay {
				delay = retryConfig.MaxDelay
			}

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry delay: %v", ctx.Err())
			}
		}

		// Create smtp client with timeout
		smtpCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := n.sendMailWithContext(smtpCtx, msg)
		cancel()

		if err == nil {
			if n.debug {
				log.Printf("Email sent successfully on attempt %d", attempt+1)
			}
			return nil
		}

		lastErr = err
		log.Printf("Attempt %d failed: %v", attempt+1, err)
	}

	return fmt.Errorf("all retry attempts failed, last error: %v", lastErr)
}

func (n *MailSlurpNotifier) sendMailWithContext(ctx context.Context, msg []byte) error {
	// Create a channel for the result
	done := make(chan error, 1)

	go func() {
		smtpAddr := fmt.Sprintf("%s:%d", n.config.SMTPHost, n.config.SMTPPort)
		err := smtp.SendMail(
			smtpAddr,
			n.auth,
			n.inbox.EmailAddress,
			[]string{n.config.ToEmail},
			msg,
		)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("smtp operation cancelled: %v", ctx.Err())
	}
}

func (n *MailSlurpNotifier) SendDebugEmail(ctx context.Context) error {
	debugData := EmailData{
		Time:     n.startupTime.Format(time.RFC3339),
		Location: "System Startup",
		Details: fmt.Sprintf(`
Configuration Details:
---------------------
SMTP Host: %s
SMTP Port: %d
Inbox ID: %s
Email Address: %s
API Status: %s

Connection Test Results:
----------------------
SMTP Auth: %v
Client Initialized: %v
Template Parsed: %v

Additional Information:
---------------------
Startup Time: %s
Debug Mode: Enabled
`,
			n.config.SMTPHost,
			n.config.SMTPPort,
			n.config.InboxID,
			n.inbox.EmailAddress,
			"Connected",
			n.auth != nil,
			n.client != nil,
			true,
			n.startupTime.Format(time.RFC3339)),
	}

	template := NewEmailTemplate()
	template.Subject = "Security System Debug Report"
	template.Body = `Debug Report Generated at {{.Time}}
Location: {{.Location}}
{{.Details}}`

	body, err := template.Execute(debugData)
	if err != nil {
		return fmt.Errorf("failed to generate debug email: %v", err)
	}

	msg := formatEmail(n.config.ToEmail, template.Subject, body)
	return n.sendWithRetry(ctx, msg)
}
