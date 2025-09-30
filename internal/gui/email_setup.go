package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/zserge/lorca"

	"github.com/mikeyg42/webcam/internal/notification"
)

// EmailMethod represents the chosen email notification method
type EmailMethod string

const (
	EmailMethodMailerSend EmailMethod = "mailersend"
	EmailMethodGmail      EmailMethod = "gmail"
	EmailMethodDisabled   EmailMethod = "disabled"
)

// EmailSetupResult contains the user's email configuration choice
type EmailSetupResult struct {
	Method           EmailMethod                    `json:"method"`
	MailerSendConfig *notification.MailSendConfig   `json:"mailersend_config,omitempty"`
	GmailConfig      *notification.GmailConfig      `json:"gmail_config,omitempty"`
	Error            string                         `json:"error,omitempty"`
}

// EmailSetupGUI manages the email configuration dialog
type EmailSetupGUI struct {
	ui         lorca.UI
	resultChan chan EmailSetupResult
	ctx        context.Context
	cancel     context.CancelFunc
}

// ShowEmailSetupDialog displays an interactive GUI for email configuration
// Returns the chosen email method and configuration, or error if user cancels
func ShowEmailSetupDialog(ctx context.Context) (*EmailSetupResult, error) {
	gui := &EmailSetupGUI{
		resultChan: make(chan EmailSetupResult, 1),
	}
	gui.ctx, gui.cancel = context.WithCancel(ctx)
	defer gui.cancel()

	// Create Lorca UI with Chrome
	ui, err := lorca.New("", "", 500, 400)
	if err != nil {
		return nil, fmt.Errorf("failed to create GUI window: %v", err)
	}
	defer ui.Close()
	gui.ui = ui

	// Bind Go functions to JavaScript
	if err := gui.bindFunctions(); err != nil {
		return nil, fmt.Errorf("failed to bind GUI functions: %v", err)
	}

	// Load the email setup HTML
	if err := gui.loadHTML(); err != nil {
		return nil, fmt.Errorf("failed to load GUI HTML: %v", err)
	}

	// Wait for user interaction or context cancellation
	select {
	case result := <-gui.resultChan:
		return &result, nil
	case <-gui.ctx.Done():
		return nil, gui.ctx.Err()
	case <-ui.Done():
		return &EmailSetupResult{Method: EmailMethodDisabled}, nil
	}
}

// bindFunctions exposes Go functions to the JavaScript frontend
func (g *EmailSetupGUI) bindFunctions() error {
	// Bind function for MailerSend token validation
	g.ui.Bind("testMailerSend", g.testMailerSend)

	// Bind function for Gmail OAuth2 setup
	g.ui.Bind("setupGmail", g.setupGmail)

	// Bind function to skip email notifications
	g.ui.Bind("skipEmail", g.skipEmail)

	// Bind function to close/cancel dialog
	g.ui.Bind("closeDialog", g.closeDialog)

	return nil
}

// testMailerSend validates a MailerSend API token by sending a test email
func (g *EmailSetupGUI) testMailerSend(token, email string) {
	go func() {
		result := EmailSetupResult{Method: EmailMethodMailerSend}

		// Validate inputs
		if token == "" {
			result.Error = "Please enter your MailerSend API token"
			g.sendResult(result)
			return
		}
		if email == "" {
			result.Error = "Please enter your email address"
			g.sendResult(result)
			return
		}

		// Create MailerSend notifier
		config := &notification.MailSendConfig{
			APIToken:  token,
			ToEmail:   email,
			FromEmail: "security@webcam-system.local",
			Debug:     true,
		}

		notifier, err := notification.NewMailSendNotifier(config, "WebCam Security System")
		if err != nil {
			result.Error = fmt.Sprintf("Failed to initialize MailerSend: %v", err)
			g.sendResult(result)
			return
		}

		// Send test email
		ctx, cancel := context.WithTimeout(g.ctx, 30*time.Second)
		defer cancel()

		if err := notifier.SendDebugEmail(ctx); err != nil {
			result.Error = fmt.Sprintf("Failed to send test email: %v", err)
			g.sendResult(result)
			return
		}

		// Success!
		result.MailerSendConfig = config
		g.resultChan <- result
	}()
}

// setupGmail initiates Gmail OAuth2 flow
func (g *EmailSetupGUI) setupGmail(clientID, clientSecret, email string) {
	go func() {
		result := EmailSetupResult{Method: EmailMethodGmail}

		// Validate inputs
		if clientID == "" || clientSecret == "" {
			result.Error = "Please enter both Gmail Client ID and Client Secret"
			g.sendResult(result)
			return
		}
		if email == "" {
			result.Error = "Please enter your email address"
			g.sendResult(result)
			return
		}

		// Create Gmail notifier (this will trigger OAuth2 flow)
		config := &notification.GmailConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			ToEmail:      email,
			Debug:        true,
		}

		notifier, err := notification.NewGmailNotifier(g.ctx, config, "WebCam Security System")
		if err != nil {
			result.Error = fmt.Sprintf("Gmail OAuth2 setup failed: %v", err)
			g.sendResult(result)
			return
		}

		// Send test email
		if err := notifier.SendDebugEmail(g.ctx); err != nil {
			result.Error = fmt.Sprintf("Failed to send Gmail test email: %v", err)
			g.sendResult(result)
			return
		}

		// Success!
		result.GmailConfig = config
		g.resultChan <- result
	}()
}

// skipEmail configures the system to work without email notifications
func (g *EmailSetupGUI) skipEmail() {
	g.resultChan <- EmailSetupResult{Method: EmailMethodDisabled}
}

// closeDialog closes the setup dialog (equivalent to skip)
func (g *EmailSetupGUI) closeDialog() {
	g.cancel()
}

// sendResult sends a result back to the JavaScript frontend
func (g *EmailSetupGUI) sendResult(result EmailSetupResult) {
	if result.Error != "" {
		// Send error back to frontend for display
		if err := g.ui.Eval(fmt.Sprintf(`handleError(%s)`, jsonEscape(result.Error))); err != nil {
			log.Printf("Failed to send error to frontend: %v", err)
		}
	}
}

// loadHTML loads the email setup interface HTML into the browser
func (g *EmailSetupGUI) loadHTML() error {
	html := g.generateHTML()
	return g.ui.Load("data:text/html," + html)
}

// generateHTML creates the email setup dialog HTML
func (g *EmailSetupGUI) generateHTML() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Email Notification Setup</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            margin: 0;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            max-width: 450px;
            margin: 0 auto;
            background: white;
            border-radius: 8px;
            padding: 30px;
            box-shadow: 0 2px 20px rgba(0,0,0,0.1);
        }
        h1 {
            text-align: center;
            color: #333;
            margin-bottom: 10px;
        }
        .subtitle {
            text-align: center;
            color: #666;
            margin-bottom: 30px;
        }
        .option {
            margin-bottom: 20px;
            padding: 20px;
            border: 2px solid #e0e0e0;
            border-radius: 6px;
            transition: all 0.2s;
        }
        .option:hover {
            border-color: #2196F3;
            box-shadow: 0 2px 8px rgba(33,150,243,0.2);
        }
        .option h3 {
            margin: 0 0 10px 0;
            color: #333;
        }
        .option p {
            margin: 0 0 15px 0;
            color: #666;
            font-size: 14px;
        }
        input[type="text"], input[type="password"], input[type="email"] {
            width: 100%;
            padding: 10px;
            border: 1px solid #ddd;
            border-radius: 4px;
            margin-bottom: 10px;
            box-sizing: border-box;
        }
        button {
            background: #2196F3;
            color: white;
            border: none;
            padding: 12px 20px;
            border-radius: 4px;
            cursor: pointer;
            font-size: 14px;
            transition: background 0.2s;
        }
        button:hover {
            background: #1976D2;
        }
        button:disabled {
            background: #ccc;
            cursor: not-allowed;
        }
        .skip-button {
            background: #757575;
            width: 100%;
            margin-top: 20px;
        }
        .skip-button:hover {
            background: #616161;
        }
        .error {
            color: #f44336;
            background: #ffebee;
            padding: 10px;
            border-radius: 4px;
            margin: 10px 0;
            display: none;
        }
        .loading {
            display: none;
            text-align: center;
            padding: 10px;
            color: #666;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>📧 Email Notifications Setup</h1>
        <p class="subtitle">Choose how you'd like to receive motion alerts</p>

        <div class="error" id="errorMessage"></div>
        <div class="loading" id="loadingMessage">Testing configuration...</div>

        <!-- MailerSend Option -->
        <div class="option">
            <h3>🚀 MailerSend API</h3>
            <p>Use your MailerSend API token for reliable email delivery</p>
            <input type="password" id="mailersendToken" placeholder="Enter MailerSend API Token" />
            <input type="email" id="mailersendEmail" placeholder="Your email address" />
            <button onclick="testMailerSend()">Test MailerSend</button>
        </div>

        <!-- Gmail Option -->
        <div class="option">
            <h3>📨 Gmail OAuth2</h3>
            <p>Send emails through your Gmail account using OAuth2</p>
            <input type="text" id="gmailClientId" placeholder="Gmail Client ID" />
            <input type="password" id="gmailClientSecret" placeholder="Gmail Client Secret" />
            <input type="email" id="gmailEmail" placeholder="Your email address" />
            <button onclick="setupGmail()">Setup Gmail</button>
        </div>

        <!-- Skip Option -->
        <button class="skip-button" onclick="skipEmail()">
            ⏭️ Skip Email Notifications (Motion detection will still work)
        </button>
    </div>

    <script>
        function testMailerSend() {
            const token = document.getElementById('mailersendToken').value;
            const email = document.getElementById('mailersendEmail').value;

            showLoading();
            testMailerSend(token, email);
        }

        function setupGmail() {
            const clientId = document.getElementById('gmailClientId').value;
            const clientSecret = document.getElementById('gmailClientSecret').value;
            const email = document.getElementById('gmailEmail').value;

            showLoading();
            setupGmail(clientId, clientSecret, email);
        }

        function skipEmail() {
            skipEmail();
        }

        function showLoading() {
            document.getElementById('loadingMessage').style.display = 'block';
            document.getElementById('errorMessage').style.display = 'none';

            // Disable all buttons during testing
            const buttons = document.querySelectorAll('button');
            buttons.forEach(btn => btn.disabled = true);
        }

        function handleError(error) {
            document.getElementById('errorMessage').textContent = error;
            document.getElementById('errorMessage').style.display = 'block';
            document.getElementById('loadingMessage').style.display = 'none';

            // Re-enable buttons
            const buttons = document.querySelectorAll('button');
            buttons.forEach(btn => btn.disabled = false);
        }

        // Handle window close
        window.addEventListener('beforeunload', function() {
            closeDialog();
        });
    </script>
</body>
</html>`
}

// jsonEscape properly escapes a string for use in JavaScript
func jsonEscape(str string) string {
	data, _ := json.Marshal(str)
	return string(data)
}