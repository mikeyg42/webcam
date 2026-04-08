package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mikeyg42/webcam/internal/gui"
)

func main() {
	fmt.Println("🧪 Testing Email Setup GUI...")
	fmt.Println("This will open a browser window with the email configuration dialog.")
	fmt.Println("Try different options to test the interface!")
	fmt.Println()

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Show the email setup dialog
	result, err := gui.ShowEmailSetupDialog(ctx)
	if err != nil {
		log.Fatalf("❌ GUI test failed: %v", err)
	}

	// Display the result
	fmt.Printf("\n✅ GUI test completed successfully!\n")
	fmt.Printf("📧 User selected: %s\n", result.Method)

	switch result.Method {
	case gui.EmailMethodMailerSend:
		if result.MailerSendConfig != nil {
			fmt.Printf("📮 MailerSend configured for: %s\n", result.MailerSendConfig.ToEmail)
		}
	case gui.EmailMethodGmail:
		if result.GmailConfig != nil {
			fmt.Printf("📧 Gmail configured for: %s\n", result.GmailConfig.ToEmail)
		}
	case gui.EmailMethodDisabled:
		fmt.Println("📵 Email notifications disabled")
	}

	if result.Error != "" {
		fmt.Printf("⚠️  Error occurred: %s\n", result.Error)
	}

	fmt.Println("\n🎉 GUI system is working correctly!")
}