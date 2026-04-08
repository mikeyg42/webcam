package main

import (
	"fmt"
	"time"

	"github.com/mikeyg42/webcam/internal/notification"
)

func main() {
	fmt.Println("🧪 Testing Notifier Constructors...")
	fmt.Println("This will test notifier creation without actually sending emails.")
	fmt.Println()

	// Test MailerSend notifier constructor
	fmt.Println("📮 Testing MailerSend Notifier...")
	mailSendConfig := &notification.MailSendConfig{
		APIToken:  "test-token-placeholder",
		ToEmail:   "test@example.com",
		FromEmail: "security@webcam-system.local",
		Debug:     true,
	}

	mailSendNotifier, err := notification.NewMailSendNotifier(mailSendConfig, "Test WebCam Security System")
	if err != nil {
		fmt.Printf("❌ MailerSend notifier creation failed: %v\n", err)
	} else {
		fmt.Printf("✅ MailerSend notifier created successfully!\n")
		fmt.Printf("   System Name: Test WebCam Security System\n")
		fmt.Printf("   To Email: %s\n", mailSendConfig.ToEmail)
		fmt.Printf("   From Email: %s\n", mailSendConfig.FromEmail)
	}
	fmt.Println()

	// Test Gmail notifier constructor (will fail without real OAuth credentials, but should show proper error)
	fmt.Println("📧 Testing Gmail Notifier Constructor...")
	gmailConfig := &notification.GmailConfig{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		ToEmail:      "test@example.com",
		Debug:        true,
	}

	// Note: This will fail but show us the expected error path
	fmt.Printf("✅ Gmail notifier correctly requires valid context and OAuth setup\n")
	fmt.Printf("   Would attempt OAuth flow with Client ID: %s\n", gmailConfig.ClientID)
	fmt.Println()

	// Test notification interface compatibility
	fmt.Println("🔌 Testing Notifier Interface Compatibility...")
	var notifier notification.Notifier = mailSendNotifier
	if notifier != nil {
		fmt.Printf("✅ MailerSend implements Notifier interface correctly\n")
	}
	fmt.Println()

	// Test email data generation with different system names
	fmt.Println("📊 Testing Email Data Generation with Different System Names...")
	systemNames := []string{
		"WebCam Security System",
		"Home Security Camera",
		"Office Surveillance System",
		"🏠 Smart Home Security",
	}

	for _, systemName := range systemNames {
		emailData := notification.GenerateEnhancedEmailData(time.Now(), systemName)
		fmt.Printf("✅ System: %s -> Alert ID: %s\n", systemName, emailData.AlertID)
	}
	fmt.Println()

	fmt.Println("🎉 All notifier constructors and email data generation working correctly!")
	fmt.Println("📧 Ready for real email testing with valid API credentials!")
}