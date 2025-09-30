package notification

import (
	"bytes"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
	"time"
)

// ProfessionalEmail represents a complete email with enhanced headers and MIME structure
type ProfessionalEmail struct {
	From         string
	FromName     string
	To           string
	Subject      string
	TextBody     string
	HTMLBody     string
	PreheaderText string

	// Threading support
	MessageID    string
	InReplyTo    string
	References   string

	// Professional headers
	AutoSubmitted           bool
	AutoResponseSuppress    bool
	BulkPrecedence         bool
	ListUnsubscribe        string

	// System information
	UserAgent   string
	AlertID     string
	SystemName  string
}

// BuildMIMEMessage creates a professional multipart MIME email with all headers
func BuildMIMEMessage(email *ProfessionalEmail) ([]byte, error) {
	var buf bytes.Buffer

	// Create multipart writer for mixed content
	writer := multipart.NewWriter(&buf)
	boundary := writer.Boundary()

	// Write main headers
	if err := writeEmailHeaders(&buf, email, boundary); err != nil {
		return nil, fmt.Errorf("failed to write headers: %v", err)
	}

	// Create multipart alternative for text/html
	altWriter := multipart.NewWriter(&buf)
	altBoundary := altWriter.Boundary()

	// Start alternative section
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%s\r\n", altBoundary)
	fmt.Fprintf(&buf, "\r\n")

	// Add text version
	if err := writeTextPart(&buf, email.TextBody, altBoundary); err != nil {
		return nil, fmt.Errorf("failed to write text part: %v", err)
	}

	// Add HTML version
	if err := writeHTMLPart(&buf, email.HTMLBody, altBoundary); err != nil {
		return nil, fmt.Errorf("failed to write HTML part: %v", err)
	}

	// Close alternative section
	fmt.Fprintf(&buf, "--%s--\r\n", altBoundary)

	// Future: Add attachment section for motion capture images
	// This is where we'll add the motion detection image when that feature is implemented
	/*
	if email.AttachmentPath != "" {
		if err := writeAttachmentPart(&buf, email.AttachmentPath, boundary); err != nil {
			return nil, fmt.Errorf("failed to write attachment: %v", err)
		}
	}
	*/

	// Close main multipart
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)

	return buf.Bytes(), nil
}

// writeEmailHeaders writes all the professional email headers
func writeEmailHeaders(buf *bytes.Buffer, email *ProfessionalEmail, boundary string) error {
	headers := make(textproto.MIMEHeader)

	// Basic headers
	if email.FromName != "" {
		headers.Set("From", CreateDisplayName(email.FromName, email.From))
	} else {
		headers.Set("From", email.From)
	}
	headers.Set("To", email.To)
	headers.Set("Subject", mime.QEncoding.Encode("utf-8", email.Subject))
	headers.Set("Date", time.Now().Format(time.RFC1123Z))

	// MIME headers
	headers.Set("MIME-Version", "1.0")
	headers.Set("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%s", boundary))

	// Threading headers for grouped alerts
	if email.MessageID != "" {
		headers.Set("Message-ID", fmt.Sprintf("<%s>", email.MessageID))
	}
	if email.InReplyTo != "" {
		headers.Set("In-Reply-To", fmt.Sprintf("<%s>", email.InReplyTo))
	}
	if email.References != "" {
		headers.Set("References", fmt.Sprintf("<%s>", email.References))
	}

	// Professional auto-reply suppression headers
	if email.AutoSubmitted {
		headers.Set("Auto-Submitted", "auto-generated")
	}
	if email.AutoResponseSuppress {
		headers.Set("X-Auto-Response-Suppress", "All")
	}
	if email.BulkPrecedence {
		headers.Set("Precedence", "bulk")
	}

	// System identification headers
	if email.UserAgent != "" {
		headers.Set("User-Agent", email.UserAgent)
	}
	if email.SystemName != "" {
		headers.Set("X-Webcam-System", email.SystemName)
	}
	if email.AlertID != "" {
		headers.Set("X-Alert-ID", email.AlertID)
	}

	// Security/spam prevention headers
	headers.Set("X-Mailer", "WebCam Security System v1.0")
	headers.Set("X-Priority", "2") // High priority for security alerts

	// Future: List-Unsubscribe header for compliance
	if email.ListUnsubscribe != "" {
		headers.Set("List-Unsubscribe", email.ListUnsubscribe)
	}

	// Write all headers
	for key, values := range headers {
		for _, value := range values {
			fmt.Fprintf(buf, "%s: %s\r\n", key, value)
		}
	}
	fmt.Fprintf(buf, "\r\n")

	return nil
}

// writeTextPart writes the plain text version of the email
func writeTextPart(buf *bytes.Buffer, textBody, boundary string) error {
	fmt.Fprintf(buf, "--%s\r\n", boundary)
	fmt.Fprintf(buf, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(buf, "Content-Transfer-Encoding: quoted-printable\r\n")
	fmt.Fprintf(buf, "\r\n")

	// Convert to quoted-printable encoding for better compatibility
	encoded := quotedPrintableEncode(textBody)
	fmt.Fprintf(buf, "%s\r\n", encoded)

	return nil
}

// writeHTMLPart writes the HTML version of the email
func writeHTMLPart(buf *bytes.Buffer, htmlBody, boundary string) error {
	fmt.Fprintf(buf, "--%s\r\n", boundary)
	fmt.Fprintf(buf, "Content-Type: text/html; charset=utf-8\r\n")
	fmt.Fprintf(buf, "Content-Transfer-Encoding: quoted-printable\r\n")
	fmt.Fprintf(buf, "\r\n")

	// Convert to quoted-printable encoding
	encoded := quotedPrintableEncode(htmlBody)
	fmt.Fprintf(buf, "%s\r\n", encoded)

	return nil
}

// CreateProfessionalMotionAlert creates a complete professional motion detection email
func CreateProfessionalMotionAlert(data *EnhancedEmailData, fromAddr, toAddr, systemName string) (*ProfessionalEmail, error) {
	template := GetMotionAlertTemplate()

	htmlBody, textBody, err := RenderEmailTemplate(template, data)
	if err != nil {
		return nil, fmt.Errorf("failed to render template: %v", err)
	}

	return &ProfessionalEmail{
		From:         fromAddr,
		FromName:     "Security Cam Motion Detector",
		To:           toAddr,
		Subject:      template.Subject,
		TextBody:     textBody,
		HTMLBody:     htmlBody,
		PreheaderText: template.PreheaderText,

		// Threading - all alerts in same thread
		MessageID:    generateMessageID(data.AlertID),
		InReplyTo:    data.ThreadID,
		References:   data.ThreadID,

		// Professional headers
		AutoSubmitted:        true,
		AutoResponseSuppress: true,
		BulkPrecedence:      true,

		// System info
		UserAgent:   "WebCam Security System v1.0",
		AlertID:     data.AlertID,
		SystemName:  systemName,
	}, nil
}

// CreateProfessionalTestEmail creates a professional test/debug email
func CreateProfessionalTestEmail(data *EnhancedEmailData, fromAddr, toAddr, systemName string) (*ProfessionalEmail, error) {
	template := GetDebugTestTemplate()

	htmlBody, textBody, err := RenderEmailTemplate(template, data)
	if err != nil {
		return nil, fmt.Errorf("failed to render template: %v", err)
	}

	return &ProfessionalEmail{
		From:         fromAddr,
		FromName:     "Security Cam Motion Detector",
		To:           toAddr,
		Subject:      template.Subject,
		TextBody:     textBody,
		HTMLBody:     htmlBody,
		PreheaderText: template.PreheaderText,

		// Unique message for test emails (no threading)
		MessageID:    generateMessageID(fmt.Sprintf("test-%s", data.AlertID)),

		// Professional headers
		AutoSubmitted:        true,
		AutoResponseSuppress: true,
		BulkPrecedence:      false, // Test emails are not bulk

		// System info
		UserAgent:   "WebCam Security System v1.0",
		AlertID:     data.AlertID,
		SystemName:  systemName,
	}, nil
}

// generateMessageID creates a unique Message-ID for email threading
func generateMessageID(alertID string) string {
	return fmt.Sprintf("%s@webcam-system.local", alertID)
}

// quotedPrintableEncode performs basic quoted-printable encoding
// For production, consider using mime/quotedprintable package
func quotedPrintableEncode(text string) string {
	// Simple implementation - for production you might want to use
	// the mime/quotedprintable package for full compliance
	encoded := strings.ReplaceAll(text, "\r\n", "\n")
	encoded = strings.ReplaceAll(encoded, "\n", "\r\n")

	// Handle lines longer than 76 characters
	lines := strings.Split(encoded, "\r\n")
	var result []string

	for _, line := range lines {
		if len(line) <= 76 {
			result = append(result, line)
		} else {
			// Simple line wrapping - break at word boundaries when possible
			for len(line) > 76 {
				breakPoint := 75
				if spaceIdx := strings.LastIndex(line[:76], " "); spaceIdx > 50 {
					breakPoint = spaceIdx
				}
				result = append(result, line[:breakPoint]+"=")
				line = line[breakPoint:]
			}
			if len(line) > 0 {
				result = append(result, line)
			}
		}
	}

	return strings.Join(result, "\r\n")
}

// Future: writeAttachmentPart for motion capture images
/*
func writeAttachmentPart(buf *bytes.Buffer, imagePath, boundary string) error {
	// This will be implemented when we add motion capture image support
	// It will:
	// 1. Read the image file
	// 2. Encode it in base64
	// 3. Add proper MIME headers for the attachment
	// 4. Include it as an inline image that displays in the HTML

	fmt.Fprintf(buf, "--%s\r\n", boundary)
	fmt.Fprintf(buf, "Content-Type: image/jpeg; name=\"motion-capture.jpg\"\r\n")
	fmt.Fprintf(buf, "Content-Transfer-Encoding: base64\r\n")
	fmt.Fprintf(buf, "Content-ID: <motion-capture>\r\n")
	fmt.Fprintf(buf, "Content-Disposition: inline; filename=\"motion-capture.jpg\"\r\n")
	fmt.Fprintf(buf, "\r\n")

	// Base64 encoded image data would go here

	fmt.Fprintf(buf, "\r\n")
	return nil
}
*/