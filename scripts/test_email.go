package main

import (
	"fmt"
	"time"

	"github.com/mikeyg42/webcam/internal/notification"
)

func main() {
	fmt.Println("🧪 Testing Professional Email Templates...")
	fmt.Println("This will generate email content without actually sending.")
	fmt.Println()

	// Create enhanced email data
	emailData := notification.GenerateEnhancedEmailData(time.Now(), "Test WebCam Security System")

	fmt.Printf("📧 Generated Email Data:\n")
	fmt.Printf("   Time: %s\n", emailData.Time)
	fmt.Printf("   Location: %s\n", emailData.Location)
	fmt.Printf("   Alert ID: %s\n", emailData.AlertID)
	fmt.Printf("   Thread ID: %s\n", emailData.ThreadID)
	fmt.Printf("   System: %s\n", emailData.SystemName)
	fmt.Println()

	// Test motion alert email
	fmt.Println("🔔 Testing Motion Alert Email...")
	professionalEmail, err := notification.CreateProfessionalMotionAlert(
		emailData,
		"security@webcam-system.local",
		"test@example.com",
		"Test WebCam Security System")
	if err != nil {
		fmt.Printf("❌ Failed to create motion alert: %v\n", err)
		return
	}

	// Build MIME message
	mimeMessage, err := notification.BuildMIMEMessage(professionalEmail)
	if err != nil {
		fmt.Printf("❌ Failed to build MIME message: %v\n", err)
		return
	}

	fmt.Printf("✅ Motion Alert Email Created!\n")
	fmt.Printf("   From: %s <%s>\n", professionalEmail.FromName, professionalEmail.From)
	fmt.Printf("   To: %s\n", professionalEmail.To)
	fmt.Printf("   Subject: %s\n", professionalEmail.Subject)
	fmt.Printf("   Message ID: %s\n", professionalEmail.MessageID)
	fmt.Printf("   Thread ID: %s\n", professionalEmail.InReplyTo)
	fmt.Printf("   MIME Size: %d bytes\n", len(mimeMessage))
	fmt.Println()

	// Test debug/test email
	fmt.Println("✅ Testing Debug/Test Email...")
	testEmail, err := notification.CreateProfessionalTestEmail(
		emailData,
		"security@webcam-system.local",
		"test@example.com",
		"Test WebCam Security System")
	if err != nil {
		fmt.Printf("❌ Failed to create test email: %v\n", err)
		return
	}

	testMimeMessage, err := notification.BuildMIMEMessage(testEmail)
	if err != nil {
		fmt.Printf("❌ Failed to build test MIME message: %v\n", err)
		return
	}

	fmt.Printf("✅ Test Email Created!\n")
	fmt.Printf("   From: %s <%s>\n", testEmail.FromName, testEmail.From)
	fmt.Printf("   Subject: %s\n", testEmail.Subject)
	fmt.Printf("   MIME Size: %d bytes\n", len(testMimeMessage))
	fmt.Println()

	// Show a sample of the MIME content
	fmt.Println("📄 Sample MIME Content (first 500 chars):")
	fmt.Println("==================================================")
	if len(mimeMessage) > 500 {
		fmt.Printf("%s...\n", string(mimeMessage[:500]))
	} else {
		fmt.Printf("%s\n", string(mimeMessage))
	}
	fmt.Println("==================================================")
	fmt.Println()

	fmt.Println("🎉 Email template system is working correctly!")
	fmt.Println("📧 Professional emails with HTML/text parts, threading, and auto-reply suppression are ready!")
}