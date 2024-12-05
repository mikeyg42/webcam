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

type MailSlurpConfig struct {
	APIKey   string
	InboxID  string
	SMTPHost string
	SMTPPort int
	ToEmail  string
}

type Notifier struct {
	config        *MailSlurpConfig
	client        *mailslurp.APIClient
	ctx           context.Context
	inbox         *mailslurp.InboxDto
	smtpAccess    *mailslurp.ImapSmtpAccessDetails
	auth          smtp.Auth // Changed from sasl.Client to smtp.Auth
	emailTemplate string
}

const defaultEmailTemplate = `Motion detected at {{.Time}}
Please check your security system.`

func NewNotifier(config *MailSlurpConfig) (*Notifier, error) {
	ctx := context.WithValue(context.Background(), mailslurp.ContextAPIKey,
		mailslurp.APIKey{Key: config.APIKey})
	client := mailslurp.NewAPIClient(mailslurp.NewConfiguration())

	notifier := &Notifier{
		config:        config,
		client:        client,
		ctx:           ctx,
		emailTemplate: defaultEmailTemplate,
	}

	if err := notifier.initialize(); err != nil {
		return nil, err
	}

	return notifier, nil
}

func (n *Notifier) initialize() error {
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

func (n *Notifier) SendNotification() error {
	if n.client == nil {
		return fmt.Errorf("MailSlurp client not initialized")
	}

	// Parse email template
	tmpl, err := template.New("email").Parse(n.emailTemplate)
	if err != nil {
		return fmt.Errorf("Failed to parse email template: %v", err)
	}

	var bodyBuffer bytes.Buffer
	err = tmpl.Execute(&bodyBuffer, map[string]string{
		"Time": time.Now().Format(time.RFC1123),
	})
	if err != nil {
		return fmt.Errorf("Failed to execute email template: %v", err)
	}

	emailBody := bodyBuffer.String()
	emailSubject := "Security Alert: Motion Detected"

	msg := []byte("To: " + n.config.ToEmail + "\r\n" +
		"Subject: " + emailSubject + "\r\n" +
		"Body: " + emailBody + "\r\n")

	// Connect to SMTP server
	smtpAddr := fmt.Sprintf("%s:%d", n.config.SMTPHost, n.config.SMTPPort)
	err = smtp.SendMail(
		smtpAddr,
		n.auth,
		n.inbox.EmailAddress,
		[]string{n.config.ToEmail}, // recipients
		msg,
	)
	if err != nil {
		return fmt.Errorf("Failed to send email via MailSlurp: %v", err)
	}

	log.Println("Security notification email sent successfully via MailSlurp")
	return nil
}

func (n *Notifier) Close() error {
	// Add any cleanup if needed
	return nil
}
