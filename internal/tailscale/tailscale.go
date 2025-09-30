package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"time"

	"github.com/mikeyg42/webcam/internal/config"
)

// TailscaleManager handles Tailscale integration for WebRTC
type TailscaleManager struct {
	config   *config.TailscaleConfig
	hostname string
	localIP  string
	ctx      context.Context
	cancel   context.CancelFunc
}

// TailscaleStatus represents the status information from Tailscale
type TailscaleStatus struct {
	BackendState string                       `json:"BackendState"`
	Self         TailscaleDevice             `json:"Self"`
	Peer         map[string]TailscaleDevice  `json:"Peer"`
}

type TailscaleDevice struct {
	ID       string   `json:"ID"`
	HostName string   `json:"HostName"`
	DNSName  string   `json:"DNSName"`
	TailAddr string   `json:"TailAddr"`
	Online   bool     `json:"Online"`
}

// NewTailscaleManager creates a new Tailscale manager
func NewTailscaleManager(ctx context.Context, tsConfig *config.TailscaleConfig) (*TailscaleManager, error) {
	if !tsConfig.Enabled {
		return nil, fmt.Errorf("Tailscale is not enabled in configuration")
	}

	tsCtx, cancel := context.WithCancel(ctx)
	
	tm := &TailscaleManager{
		config: tsConfig,
		ctx:    tsCtx,
		cancel: cancel,
	}

	// Initialize Tailscale connection
	if err := tm.initialize(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize Tailscale: %w", err)
	}

	return tm, nil
}

// initialize sets up the Tailscale connection
func (tm *TailscaleManager) initialize() error {
	// Check if Tailscale is already connected
	status, err := tm.getStatus()
	if err != nil {
		return fmt.Errorf("failed to get Tailscale status: %w", err)
	}

	if status.BackendState != "Running" {
		log.Println("Tailscale not running, attempting to start...")
		
		if err := tm.startTailscale(); err != nil {
			return fmt.Errorf("failed to start Tailscale: %w", err)
		}

		// Wait for connection
		if err := tm.waitForConnection(30 * time.Second); err != nil {
			return fmt.Errorf("failed to connect to Tailscale: %w", err)
		}
	}

	// Get our Tailscale info
	status, err = tm.getStatus()
	if err != nil {
		return fmt.Errorf("failed to get updated status: %w", err)
	}

	tm.hostname = status.Self.HostName
	tm.localIP = status.Self.TailAddr
	
	log.Printf("Tailscale initialized - Hostname: %s, IP: %s", tm.hostname, tm.localIP)
	return nil
}

// startTailscale starts the Tailscale daemon and connects
func (tm *TailscaleManager) startTailscale() error {
	var cmd *exec.Cmd
	
	if tm.config.AuthKey != "" {
		// Use auth key for unattended setup
		cmd = exec.CommandContext(tm.ctx, "tailscale", "up", 
			"--authkey", tm.config.AuthKey,
			"--hostname", tm.config.NodeName,
		)
	} else {
		// Interactive setup (user needs to authenticate via browser)
		cmd = exec.CommandContext(tm.ctx, "tailscale", "up",
			"--hostname", tm.config.NodeName,
		)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale up failed: %w, output: %s", err, string(output))
	}

	log.Printf("Tailscale started: %s", string(output))
	return nil
}

// getStatus retrieves the current Tailscale status
func (tm *TailscaleManager) getStatus() (*TailscaleStatus, error) {
	cmd := exec.CommandContext(tm.ctx, "tailscale", "status", "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	var status TailscaleStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, fmt.Errorf("failed to parse status JSON: %w", err)
	}

	return &status, nil
}

// waitForConnection waits for Tailscale to be fully connected
func (tm *TailscaleManager) waitForConnection(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(tm.ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Tailscale connection")
		case <-ticker.C:
			status, err := tm.getStatus()
			if err != nil {
				log.Printf("Error checking status: %v", err)
				continue
			}
			
			if status.BackendState == "Running" && status.Self.Online {
				return nil
			}
			
			log.Printf("Waiting for Tailscale connection... Status: %s", status.BackendState)
		}
	}
}

// GetLocalTailscaleIP returns this device's Tailscale IP address
func (tm *TailscaleManager) GetLocalTailscaleIP() string {
	return tm.localIP
}

// GetTailscaleHostname returns this device's Tailscale hostname
func (tm *TailscaleManager) GetTailscaleHostname() string {
	return tm.hostname
}

// GetICEServers returns ICE server configuration for WebRTC using Tailscale
func (tm *TailscaleManager) GetICEServers() ([]map[string]interface{}, error) {
	// Instead of traditional STUN/TURN servers, we can use Tailscale's coordination
	// For WebRTC over Tailscale, we primarily rely on direct connections
	// but may still need STUN for discovering our public endpoint
	
	iceServers := []map[string]interface{}{
		{
			"urls": []string{"stun:stun.l.google.com:19302"},
		},
	}

	// If we have peers, we could potentially set up TURN-like functionality
	// through Tailscale's DERP servers, but this requires custom implementation
	
	return iceServers, nil
}

// GetPeerAddresses returns addresses of other devices on the Tailscale network
func (tm *TailscaleManager) GetPeerAddresses() ([]string, error) {
	status, err := tm.getStatus()
	if err != nil {
		return nil, err
	}

	var addresses []string
	for _, peer := range status.Peer {
		if peer.Online && peer.TailAddr != "" {
			addresses = append(addresses, peer.TailAddr)
		}
	}

	return addresses, nil
}

// IsDirectConnectionPossible checks if we can connect directly to a peer
func (tm *TailscaleManager) IsDirectConnectionPossible(peerAddr string) bool {
	// Try to establish a test connection to the peer
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(peerAddr, "80"), 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetWebRTCConfigForTailscale returns WebRTC configuration optimized for Tailscale
func (tm *TailscaleManager) GetWebRTCConfigForTailscale() map[string]interface{} {
	config := map[string]interface{}{
		"iceServers": []map[string]interface{}{
			{
				// Use Google's STUN server for initial connectivity detection
				"urls": []string{"stun:stun.l.google.com:19302"},
			},
		},
		// Prefer direct connections over relays since Tailscale handles NAT traversal
		"iceTransportPolicy": "all",
		// Faster connection establishment for local network
		"iceCandidatePoolSize": 10,
	}

	return config
}

// Shutdown cleanly stops the Tailscale manager
func (tm *TailscaleManager) Shutdown() error {
	if tm.cancel != nil {
		tm.cancel()
	}
	
	log.Println("Tailscale manager shutdown complete")
	return nil
}

// HTTP handler for Tailscale status endpoint
func (tm *TailscaleManager) StatusHandler(w http.ResponseWriter, r *http.Request) {
	status, err := tm.getStatus()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get status: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// Helper function to check if Tailscale is installed
func IsTailscaleInstalled() bool {
	cmd := exec.Command("which", "tailscale")
	return cmd.Run() == nil
}

// ValidateTailscaleConfig validates the Tailscale configuration
func ValidateTailscaleConfig(config *config.TailscaleConfig) error {
	if !config.Enabled {
		return nil
	}

	if !IsTailscaleInstalled() {
		return fmt.Errorf("Tailscale is not installed. Please install it first: https://tailscale.com/download")
	}

	if config.NodeName == "" {
		return fmt.Errorf("Tailscale node name cannot be empty")
	}

	if config.ListenPort < 1024 || config.ListenPort > 65535 {
		return fmt.Errorf("Tailscale listen port must be between 1024 and 65535")
	}

	return nil
}