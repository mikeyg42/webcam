package notification

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"html/template"
	"mime"
	"time"
)

// EnhancedEmailData contains all data for professional email templates
type EnhancedEmailData struct {
	// Basic alert information
	Time        string
	Location    string
	Details     string

	// Enhanced metadata
	Timestamp   time.Time
	AlertID     string
	ThreadID    string // For email threading
	SystemName  string

	// Future: Motion detection data
	MotionArea     int     // Area of detected motion
	ConfidenceLevel float32 // Detection confidence (0-1)
	ImagePath      string  // Path to captured image (future feature)
}

// EmailTemplate represents a complete email with both HTML and text versions
type EmailTemplate struct {
	Subject     string
	PreheaderText string
	HTMLBody    string
	TextBody    string
}

// GenerateEnhancedEmailData creates comprehensive email template data
func GenerateEnhancedEmailData(detectionTime time.Time, systemName string) *EnhancedEmailData {
	alertID := generateAlertID(detectionTime)

	return &EnhancedEmailData{
		Time:        detectionTime.Format("Monday, January 2, 2006 at 3:04 PM"),
		Location:    systemName,
		Details:     "Motion was detected by your security camera system. Please check the live feed for more details.",
		Timestamp:   detectionTime,
		AlertID:     alertID,
		ThreadID:    generateThreadID(), // Consistent thread for all alerts
		SystemName:  systemName,
		MotionArea:  0,    // Future: will be populated from motion detector
		ConfidenceLevel: 0.0, // Future: detection confidence
		ImagePath:   "",   // Future: captured motion image
	}
}

// GetMotionAlertTemplate returns a professional motion detection email template
func GetMotionAlertTemplate() *EmailTemplate {
	return &EmailTemplate{
		Subject:       "🔔 Motion Detected - Security Alert",
		PreheaderText: "Motion detected by your security camera system",
		HTMLBody:      motionAlertHTMLTemplate,
		TextBody:      motionAlertTextTemplate,
	}
}

// GetDebugTestTemplate returns a template for debug/test emails
func GetDebugTestTemplate() *EmailTemplate {
	return &EmailTemplate{
		Subject:       "✅ Security System Test - Email Configuration Successful",
		PreheaderText: "Your security camera email notifications are working correctly",
		HTMLBody:      debugTestHTMLTemplate,
		TextBody:      debugTestTextTemplate,
	}
}

// RenderEmailTemplate renders both HTML and text versions of an email template
func RenderEmailTemplate(tmpl *EmailTemplate, data *EnhancedEmailData) (htmlBody, textBody string, err error) {
	// Render HTML version
	htmlTmpl, err := template.New("html").Parse(tmpl.HTMLBody)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse HTML template: %v", err)
	}

	var htmlBuf bytes.Buffer
	if err := htmlTmpl.Execute(&htmlBuf, data); err != nil {
		return "", "", fmt.Errorf("failed to execute HTML template: %v", err)
	}

	// Render text version
	textTmpl, err := template.New("text").Parse(tmpl.TextBody)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse text template: %v", err)
	}

	var textBuf bytes.Buffer
	if err := textTmpl.Execute(&textBuf, data); err != nil {
		return "", "", fmt.Errorf("failed to execute text template: %v", err)
	}

	return htmlBuf.String(), textBuf.String(), nil
}

// CreateDisplayName creates a properly encoded display name for email headers
func CreateDisplayName(name, address string) string {
	if name == "" {
		return address
	}

	// Use MIME Q-encoding for the display name to handle special characters
	encodedName := mime.QEncoding.Encode("utf-8", name)
	return fmt.Sprintf("%s <%s>", encodedName, address)
}

// generateAlertID creates a unique identifier for this specific alert
func generateAlertID(detectionTime time.Time) string {
	// Create a unique ID based on timestamp and a hash
	timeStr := detectionTime.Format("20060102-150405")
	hash := md5.Sum([]byte(detectionTime.String()))
	return fmt.Sprintf("%s-%x", timeStr, hash[:4])
}

// generateThreadID creates a consistent thread ID for all security alerts
func generateThreadID() string {
	// Use a consistent thread ID so all alerts appear in the same thread
	return "security-motion-alerts@webcam-system.local"
}

// HTML Email Template for Motion Alerts
const motionAlertHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Motion Detected - Security Alert</title>
    <!--[if mso]>
    <noscript>
        <xml>
            <o:OfficeDocumentSettings>
                <o:PixelsPerInch>96</o:PixelsPerInch>
            </o:OfficeDocumentSettings>
        </xml>
    </noscript>
    <![endif]-->
    <style>
        /* Reset and base styles */
        body, table, td, p, a, li, blockquote {
            -webkit-text-size-adjust: 100%;
            -ms-text-size-adjust: 100%;
        }
        table, td {
            mso-table-lspace: 0pt;
            mso-table-rspace: 0pt;
        }
        img {
            -ms-interpolation-mode: bicubic;
            border: 0;
            outline: none;
            text-decoration: none;
        }

        /* Email-specific styles */
        .email-container {
            max-width: 600px;
            margin: 0 auto;
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Arial, sans-serif;
        }
        .header {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 30px 20px;
            text-align: center;
        }
        .content {
            padding: 30px 20px;
            background: white;
        }
        .alert-box {
            background: #fff3cd;
            border: 1px solid #ffeaa7;
            border-radius: 8px;
            padding: 20px;
            margin: 20px 0;
        }
        .alert-icon {
            font-size: 48px;
            margin-bottom: 10px;
        }
        .motion-image-placeholder {
            border: 2px dashed #ddd;
            border-radius: 8px;
            padding: 40px;
            text-align: center;
            color: #666;
            margin: 20px 0;
            background: #f9f9f9;
        }
        .footer {
            background: #f8f9fa;
            color: #666;
            padding: 20px;
            text-align: center;
            font-size: 14px;
        }
        .button {
            display: inline-block;
            background: #007bff;
            color: white;
            text-decoration: none;
            padding: 12px 24px;
            border-radius: 6px;
            margin: 15px 0;
        }

        /* Mobile responsive */
        @media only screen and (max-width: 480px) {
            .email-container {
                width: 100% !important;
            }
            .header, .content, .footer {
                padding: 20px 15px !important;
            }
        }
    </style>
</head>
<body style="margin: 0; padding: 0; background-color: #f4f4f7;">
    <!-- Preheader text (hidden but shows in email previews) -->
    <div style="display: none; font-size: 1px; color: #fefefe; line-height: 1px; max-height: 0px; max-width: 0px; opacity: 0; overflow: hidden;">
        Motion detected by your security camera system at {{.Time}}
    </div>

    <div class="email-container">
        <!-- Header -->
        <div class="header">
            <div class="alert-icon">🔔</div>
            <h1 style="margin: 0; font-size: 28px; font-weight: 300;">Security Alert</h1>
            <p style="margin: 10px 0 0 0; opacity: 0.9;">Motion Detection System</p>
        </div>

        <!-- Main Content -->
        <div class="content">
            <div class="alert-box">
                <h2 style="color: #856404; margin: 0 0 15px 0;">🎯 Motion Detected</h2>
                <p style="margin: 0; font-size: 16px; line-height: 1.5;">
                    <strong>When:</strong> {{.Time}}<br>
                    <strong>Location:</strong> {{.Location}}<br>
                    <strong>Alert ID:</strong> {{.AlertID}}
                </p>
            </div>

            <p style="font-size: 16px; line-height: 1.6; color: #333;">
                {{.Details}}
            </p>

            <!-- Placeholder for future motion capture image -->
            <div class="motion-image-placeholder">
                <div style="font-size: 32px; margin-bottom: 10px;">📸</div>
                <p style="margin: 0; font-weight: 500;">Motion Capture Image</p>
                <p style="margin: 5px 0 0 0; font-size: 14px; color: #999;">
                    Coming Soon: Automated snapshots of detected motion
                </p>
            </div>

            <div style="text-align: center;">
                <a href="http://localhost:3000" class="button">
                    🔴 View Live Feed
                </a>
            </div>
        </div>

        <!-- Footer -->
        <div class="footer">
            <p style="margin: 0 0 10px 0;">
                <strong>{{.SystemName}} Security System</strong>
            </p>
            <p style="margin: 0; font-size: 12px;">
                This is an automated message from your security camera system.<br>
                Alert generated at {{.Timestamp.Format "2006-01-02 15:04:05 UTC"}}
            </p>
        </div>
    </div>
</body>
</html>`

// Text Email Template for Motion Alerts
const motionAlertTextTemplate = `🔔 SECURITY ALERT - Motion Detected

Motion Detection System - {{.SystemName}}

ALERT DETAILS:
==============
When: {{.Time}}
Location: {{.Location}}
Alert ID: {{.AlertID}}

{{.Details}}

📸 Motion Capture Image: Coming Soon
🔴 View Live Feed: http://localhost:3000

This is an automated message from your security camera system.
Alert generated at {{.Timestamp.Format "2006-01-02 15:04:05 UTC"}}

---
{{.SystemName}} Security System`

// HTML Template for Debug/Test Emails
const debugTestHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Security System Test - Email Working</title>
    <style>
        .email-container {
            max-width: 600px;
            margin: 0 auto;
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Arial, sans-serif;
        }
        .header {
            background: linear-gradient(135deg, #48c78e 0%, #06d6a0 100%);
            color: white;
            padding: 30px 20px;
            text-align: center;
        }
        .content {
            padding: 30px 20px;
            background: white;
        }
        .success-box {
            background: #d1f2eb;
            border: 1px solid #7dcea0;
            border-radius: 8px;
            padding: 20px;
            margin: 20px 0;
        }
        .footer {
            background: #f8f9fa;
            color: #666;
            padding: 20px;
            text-align: center;
            font-size: 14px;
        }
    </style>
</head>
<body style="margin: 0; padding: 0; background-color: #f4f4f7;">
    <!-- Preheader text -->
    <div style="display: none; font-size: 1px; color: #fefefe; line-height: 1px; max-height: 0px; max-width: 0px; opacity: 0; overflow: hidden;">
        Your security camera email notifications are working correctly
    </div>

    <div class="email-container">
        <div class="header">
            <div style="font-size: 48px; margin-bottom: 10px;">✅</div>
            <h1 style="margin: 0; font-size: 28px; font-weight: 300;">Test Successful</h1>
            <p style="margin: 10px 0 0 0; opacity: 0.9;">Email Configuration Complete</p>
        </div>

        <div class="content">
            <div class="success-box">
                <h2 style="color: #27ae60; margin: 0 0 15px 0;">🎉 Email Notifications Working!</h2>
                <p style="margin: 0; font-size: 16px; line-height: 1.5;">
                    Your security camera system can now send email alerts when motion is detected.
                </p>
            </div>

            <p style="font-size: 16px; line-height: 1.6; color: #333;">
                This test email confirms that your email notification system is properly configured and ready to alert you of any motion detection events.
            </p>

            <h3>System Information:</h3>
            <ul style="line-height: 1.6;">
                <li><strong>System:</strong> {{.SystemName}}</li>
                <li><strong>Test Time:</strong> {{.Time}}</li>
                <li><strong>Status:</strong> Email notifications active</li>
            </ul>
        </div>

        <div class="footer">
            <p style="margin: 0 0 10px 0;">
                <strong>{{.SystemName}} Security System</strong>
            </p>
            <p style="margin: 0; font-size: 12px;">
                This is an automated test message.<br>
                Generated at {{.Timestamp.Format "2006-01-02 15:04:05 UTC"}}
            </p>
        </div>
    </div>
</body>
</html>`

// Text Template for Debug/Test Emails
const debugTestTextTemplate = `✅ SECURITY SYSTEM TEST - Email Configuration Successful

{{.SystemName}} - Email Notification System

🎉 SUCCESS: Email notifications are now working correctly!

SYSTEM INFORMATION:
==================
System: {{.SystemName}}
Test Time: {{.Time}}
Status: Email notifications active

Your security camera system can now send email alerts when motion is detected.

This test email confirms that your email notification system is properly
configured and ready to alert you of any motion detection events.

---
This is an automated test message.
Generated at {{.Timestamp.Format "2006-01-02 15:04:05 UTC"}}

{{.SystemName}} Security System`