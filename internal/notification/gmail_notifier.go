// gmail_notifier.go
package notification

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gmail "google.golang.org/api/gmail/v1"
)

const (
	// Network timeouts - adjust based on your network conditions
	defaultOAuthTimeout = 5 * time.Minute
	defaultSendTimeout  = 30 * time.Second

	// OAuth callback server settings
	defaultCallbackPath = "/oauth2/callback"
	defaultCallbackPort = "8787"

	// Token file permissions (owner read/write only)
	tokenFilePerms = 0600
)

// GmailConfig holds configuration for Gmail OAuth2 sending
type GmailConfig struct {
	// OAuth2 app credentials from Google Cloud Console
	// REQUIRED: Create a "Desktop app" OAuth client in your GCP project
	ClientID     string
	ClientSecret string

	// Local redirect URL for OAuth callback (modifiable if port conflicts)
	RedirectURL    string // defaults to "http://127.0.0.1:8787/oauth2/callback"
	TokenStorePath string // defaults to "./gmail_token.json"

	// Encryption key for token storage (auto-generated if empty)
	// Store this securely if you need token portability across instances
	TokenEncryptionKey string

	// Message addressing
	ToEmail   string // REQUIRED: recipient email
	FromEmail string // Optional: defaults to authenticated Gmail account

	Debug bool // Enable verbose logging
}

// GmailNotifier implements email notifications via Gmail API with OAuth2
// Handles authentication flow, token persistence, and email sending
type GmailNotifier struct {
	cfg        *GmailConfig
	oauthCfg   *oauth2.Config
	svc        *gmail.Service
	startedAt  time.Time
	systemName string
}

// NewGmailNotifier initializes Gmail OAuth2 and API client
// First run triggers interactive browser-based authentication
func NewGmailNotifier(ctx context.Context, cfg *GmailConfig, systemName string) (*GmailNotifier, error) {
	// Validate required configuration
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("Gmail OAuth2 ClientID/ClientSecret are required")
	}
	if cfg.ToEmail == "" {
		return nil, fmt.Errorf("notification email address (ToEmail) is required")
	}

	// Set defaults for optional fields
	if cfg.RedirectURL == "" {
		cfg.RedirectURL = fmt.Sprintf("http://127.0.0.1:%s%s", defaultCallbackPort, defaultCallbackPath)
	}
	if cfg.TokenStorePath == "" {
		cfg.TokenStorePath = "./gmail_token.json"
	}

	// Set default system name if not provided
	if systemName == "" {
		systemName = "WebCam Security System"
	}

	// Generate encryption key if not provided
	if cfg.TokenEncryptionKey == "" {
		cfg.TokenEncryptionKey = generateEncryptionKey()
		if cfg.Debug {
			log.Printf("Generated token encryption key (save for token portability): %s", cfg.TokenEncryptionKey)
		}
	}

	// Configure OAuth2
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       []string{gmail.GmailSendScope}, // Minimal scope: send-only
	}

	// Load or obtain OAuth token
	token, err := loadEncryptedToken(cfg.TokenStorePath, cfg.TokenEncryptionKey)
	if err != nil {
		if cfg.Debug {
			log.Printf("No valid existing token, starting interactive OAuth: %v", err)
		}

		// Run interactive OAuth flow
		token, err = runInteractiveOAuth(ctx, oauthCfg, cfg.RedirectURL, cfg.Debug)
		if err != nil {
			return nil, fmt.Errorf("failed OAuth2 flow: %w", err)
		}

		// Persist token for future use
		if err := saveEncryptedToken(cfg.TokenStorePath, token, cfg.TokenEncryptionKey); err != nil {
			return nil, fmt.Errorf("failed to persist token: %w", err)
		}
	}

	// Validate token has required fields
	if token.AccessToken == "" && token.RefreshToken == "" {
		return nil, fmt.Errorf("invalid token: missing access and refresh tokens")
	}

	// Create HTTP client with token auto-refresh
	httpClient := oauthCfg.Client(ctx, token)
	httpClient.Timeout = defaultSendTimeout // Set reasonable timeout

	// Initialize Gmail service
	svc, err := gmail.New(httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to init Gmail service: %w", err)
	}

	return &GmailNotifier{
		cfg:        cfg,
		oauthCfg:   oauthCfg,
		svc:        svc,
		startedAt:  time.Now(),
		systemName: systemName,
	}, nil
}

// SendNotification sends a notification email with default timeout
func (n *GmailNotifier) SendNotification() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultSendTimeout)
	defer cancel()
	return n.SendNotificationWithContext(ctx)
}

// SendNotificationWithContext sends notification with custom context
func (n *GmailNotifier) SendNotificationWithContext(ctx context.Context) error {
	// Create enhanced email data
	emailData := GenerateEnhancedEmailData(time.Now(), n.systemName)

	// Create professional motion alert email
	professionalEmail, err := CreateProfessionalMotionAlert(emailData, n.fromAddr(), n.cfg.ToEmail, n.systemName)
	if err != nil {
		return fmt.Errorf("failed to create professional email: %w", err)
	}

	// Build MIME message with all professional headers
	mimeMessage, err := BuildMIMEMessage(professionalEmail)
	if err != nil {
		return fmt.Errorf("failed to build MIME message: %w", err)
	}

	// Send with retry logic
	return SendWithRetry(ctx, RetryConfig{
		MaxAttempts: 3,
		Delay:       time.Second,
		MaxDelay:    5 * time.Second,
	}, func(ctx context.Context) error {
		return n.sendRaw(ctx, mimeMessage)
	})
}

// SendDebugEmail sends a test email to verify configuration
// Useful for initial setup and troubleshooting
func (n *GmailNotifier) SendDebugEmail(ctx context.Context) error {
	// Create enhanced email data for the test email
	emailData := GenerateEnhancedEmailData(time.Now(), n.systemName)

	// Create professional test email
	professionalEmail, err := CreateProfessionalTestEmail(emailData, n.fromAddr(), n.cfg.ToEmail, n.systemName)
	if err != nil {
		return fmt.Errorf("failed to create professional test email: %w", err)
	}

	// Build MIME message with all professional headers
	mimeMessage, err := BuildMIMEMessage(professionalEmail)
	if err != nil {
		return fmt.Errorf("failed to build MIME message: %w", err)
	}

	return n.sendRaw(ctx, mimeMessage)
}

// Close performs cleanup (currently no-op, reserved for future use)
func (n *GmailNotifier) Close() error {
	return nil
}

// --- Helper Functions ---

// fromAddr returns the sender email address
// Falls back to authenticated account if not specified
func (n *GmailNotifier) fromAddr() string {
	if addr := strings.TrimSpace(n.cfg.FromEmail); addr != "" {
		return addr
	}
	// Gmail automatically uses authenticated account
	return "me"
}


// sendRaw sends raw email message via Gmail API
func (n *GmailNotifier) sendRaw(ctx context.Context, raw []byte) error {
	// Encode message for Gmail API (base64url without padding)
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw)

	// Send via Gmail API
	_, err := n.svc.Users.Messages.Send("me", &gmail.Message{
		Raw: encoded,
	}).Context(ctx).Do()

	if err != nil {
		return fmt.Errorf("gmail send failed: %w", err)
	}

	if n.cfg.Debug {
		log.Printf("Gmail send: OK → %s", n.cfg.ToEmail)
	}
	return nil
}

// --- Token Storage with Encryption ---

// tokenData wraps OAuth token with validation fields
type tokenData struct {
	Token     *oauth2.Token `json:"token"`
	CreatedAt time.Time     `json:"created_at"`
	Checksum  string        `json:"checksum"` // For integrity verification
}

// loadEncryptedToken loads and decrypts stored OAuth token
func loadEncryptedToken(path, key string) (*oauth2.Token, error) {
	// Read encrypted file
	encrypted, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read token file: %w", err)
	}

	// Decrypt data
	decrypted, err := decrypt(encrypted, key)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt token: %w", err)
	}

	// Parse JSON
	var data tokenData
	if err := json.Unmarshal(decrypted, &data); err != nil {
		return nil, fmt.Errorf("failed to parse token data: %w", err)
	}

	// Verify integrity
	checksum := calculateChecksum(data.Token)
	if checksum != data.Checksum {
		return nil, fmt.Errorf("token integrity check failed")
	}

	return data.Token, nil
}

// saveEncryptedToken encrypts and stores OAuth token
func saveEncryptedToken(path string, token *oauth2.Token, key string) error {
	// Prepare token data with metadata
	data := tokenData{
		Token:     token,
		CreatedAt: time.Now(),
		Checksum:  calculateChecksum(token),
	}

	// Marshal to JSON
	plaintext, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal token: %w", err)
	}

	// Encrypt data
	encrypted, err := encrypt(plaintext, key)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	// Write with restricted permissions
	if err := os.WriteFile(path, encrypted, tokenFilePerms); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}

	return nil
}

// --- Encryption Utilities ---

// generateEncryptionKey creates a secure random key
func generateEncryptionKey() string {
	key := make([]byte, 32) // 256-bit key
	if _, err := rand.Read(key); err != nil {
		// Fallback to less secure but functional key
		log.Printf("Warning: using fallback encryption key generation")
		return fmt.Sprintf("%x", sha256.Sum256([]byte(time.Now().String())))
	}
	return hex.EncodeToString(key)
}

// encrypt performs AES-GCM encryption
func encrypt(plaintext []byte, keyHex string) ([]byte, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid encryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher creation failed: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("GCM creation failed: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce generation failed: %w", err)
	}

	// Prepend nonce to ciphertext for storage
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt performs AES-GCM decryption
func decrypt(ciphertext []byte, keyHex string) ([]byte, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid decryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher creation failed: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("GCM creation failed: %w", err)
	}

	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}

	// Extract nonce and decrypt
	nonce, ciphertext := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// calculateChecksum generates integrity checksum for token
func calculateChecksum(token *oauth2.Token) string {
	data := fmt.Sprintf("%s:%s:%s:%v",
		token.AccessToken, token.RefreshToken, token.TokenType, token.Expiry.Unix())
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// --- OAuth Interactive Flow ---

// runInteractiveOAuth performs browser-based OAuth authentication
func runInteractiveOAuth(ctx context.Context, cfg *oauth2.Config, redirectURL string, debug bool) (*oauth2.Token, error) {
	// Parse redirect URL to extract listen address
	parsedURL, err := url.Parse(redirectURL)
	if err != nil {
		return nil, fmt.Errorf("invalid redirect URL: %w", err)
	}

	// Try to bind to the specified port, with fallback to random port
	listener, err := net.Listen("tcp", parsedURL.Host)
	if err != nil {
		// Port in use, try random port
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("failed to bind OAuth callback listener: %w", err)
		}

		// Update redirect URL with actual port
		actualAddr := listener.Addr().String()
		parsedURL.Host = actualAddr
		cfg.RedirectURL = parsedURL.String()

		if debug {
			log.Printf("OAuth: Using fallback port %s (original port was in use)", actualAddr)
		}
	}
	defer listener.Close()

	// Generate cryptographically secure state token
	state, err := generateSecureState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state token: %w", err)
	}

	// Build authorization URL
	authURL := cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline, // Request refresh token
		oauth2.ApprovalForce)     // Force approval screen

	// Open browser (best effort)
	if err := openBrowser(authURL); err != nil && debug {
		log.Printf("Failed to open browser: %v", err)
	}

	// Always show URL for manual access
	fmt.Printf("\n=== Gmail OAuth Setup ===\n")
	fmt.Printf("Please visit this URL to authorize the application:\n\n%s\n\n", authURL)
	fmt.Printf("Waiting for authorization...\n")

	// Setup callback server with timeout
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify callback path
			if r.URL.Path != defaultCallbackPath {
				http.NotFound(w, r)
				return
			}

			// Validate state token (CSRF protection)
			if r.FormValue("state") != state {
				http.Error(w, "Invalid state parameter", http.StatusBadRequest)
				errCh <- fmt.Errorf("OAuth state mismatch (possible CSRF)")
				return
			}

			// Check for errors from OAuth provider
			if errMsg := r.FormValue("error"); errMsg != "" {
				http.Error(w, fmt.Sprintf("Authorization failed: %s", errMsg), http.StatusBadRequest)
				errCh <- fmt.Errorf("OAuth provider error: %s", errMsg)
				return
			}

			// Extract authorization code
			code := r.FormValue("code")
			if code == "" {
				http.Error(w, "Missing authorization code", http.StatusBadRequest)
				errCh <- fmt.Errorf("missing OAuth authorization code")
				return
			}

			// Send success response
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body>
				<h1>Authorization Successful!</h1>
				<p>You can close this window and return to your application.</p>
				<script>window.close();</script>
			</body></html>`)

			// Signal success
			codeCh <- code
		}),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	// Start callback server
	go func() {
		if err := srv.Serve(listener); err != http.ErrServerClosed {
			if debug {
				log.Printf("OAuth callback server error: %v", err)
			}
		}
	}()

	// Wait for authorization with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, defaultOAuthTimeout)
	defer cancel()

	var code string
	select {
	case <-timeoutCtx.Done():
		srv.Close()
		return nil, fmt.Errorf("OAuth authorization timeout")
	case err := <-errCh:
		srv.Close()
		return nil, err
	case code = <-codeCh:
		// Gracefully shutdown server
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}

	// Exchange authorization code for tokens
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	fmt.Printf("\n=== Authorization Complete ===\n\n")
	return tok, nil
}

// generateSecureState creates cryptographically secure state token
func generateSecureState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random state: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// openBrowser attempts to open URL in default browser
// Platform-specific implementation, best-effort only
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux, freebsd, openbsd, netbsd
		cmd = exec.Command("xdg-open", url)
	}

	// Run without waiting (fire and forget)
	return cmd.Start()
}
