package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"time"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/validate"
)

// TailscaleManager handles Tailscale integration for WebRTC.
type TailscaleManager struct {
	config   *config.TailscaleConfig
	hostname string
	localIP  string
	ctx      context.Context
	cancel   context.CancelFunc
}

// TailscaleStatus represents the status information from Tailscale.
type TailscaleStatus struct {
	BackendState string                     `json:"BackendState"`
	Self         TailscaleDevice            `json:"Self"`
	Peer         map[string]TailscaleDevice `json:"Peer"`
}

type TailscaleDevice struct {
	ID       string `json:"ID"`
	HostName string `json:"HostName"`
	DNSName  string `json:"DNSName"`
	TailAddr string `json:"TailAddr"`
	Online   bool   `json:"Online"`
}

// NewTailscaleManager creates a new Tailscale manager.
// It runs the minimal validation (installed, hostname set, writable state dir) before starting.
func NewTailscaleManager(parent context.Context, tsConfig *config.TailscaleConfig) (*TailscaleManager, error) {
	if tsConfig == nil {
		return nil, errors.New("tailscale: nil config")
	}
	if err := validate.ValidateTailscaleConfig(tsConfig); err != nil {
		return nil, err
	}

	tsCtx, cancel := context.WithCancel(parent)
	tm := &TailscaleManager{
		config: tsConfig,
		ctx:    tsCtx,
		cancel: cancel,
	}

	// Initialize (read status, confirm running, capture node info).
	if err := tm.initialize(); err != nil {
		cancel()
		return nil, fmt.Errorf("tailscale init: %w", err)
	}
	return tm, nil
}

// initialize sets up the Tailscale connection metadata from `tailscale status --json`.
func (tm *TailscaleManager) initialize() error {
	status, err := tm.getStatus()
	if err != nil {
		return fmt.Errorf("get status: %w", err)
	}
	if status.BackendState != "Running" {
		return fmt.Errorf("tailscale is not running (state: %s); start tailscale and connect to your tailnet", status.BackendState)
	}

	// Fill locals from the same status object (no need to re-query).
	tm.hostname = status.Self.HostName
	tm.localIP = status.Self.TailAddr

	log.Printf("tailscale: initialized — Hostname=%s TailIP=%s", tm.hostname, tm.localIP)
	return nil
}

// getStatus retrieves the current Tailscale status (JSON) via the `tailscale` CLI.
func (tm *TailscaleManager) getStatus() (*TailscaleStatus, error) {
	// keep this fast but bounded
	ctx, cancel := context.WithTimeout(tm.ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tailscale status: %w", err)
	}

	var status TailscaleStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, fmt.Errorf("parse status json: %w", err)
	}

	if status.Self.HostName == "" || status.Self.DNSName == "" {
		return &status, fmt.Errorf("TailscaleStatus lacks a hostname and/or a DNS name: %+v", status)
	}

	return &status, nil
}

// waitForConnection polls until Tailscale backend is Running (or times out).
func (tm *TailscaleManager) waitForConnection(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(tm.ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for tailscale connection")
		case <-ticker.C:
			status, err := tm.getStatus()
			if err != nil {
				log.Printf("tailscale: status check error: %v", err)
				continue
			}
			if status.BackendState == "Running" && status.Self.Online {
				return nil
			}
			log.Printf("tailscale: waiting… state=%s online=%v", status.BackendState, status.Self.Online)
		}
	}
}

// GetLocalTailscaleIP returns this device's Tailscale IP address.
func (tm *TailscaleManager) GetLocalTailscaleIP() string { return tm.localIP }

// GetTailscaleHostname returns this device's Tailscale hostname.
func (tm *TailscaleManager) GetTailscaleHostname() string { return tm.hostname }

// GetICEServers returns ICE server configuration for WebRTC.
// We keep a single public STUN for initial ICE; Tailscale itself handles NAT traversal and DERP.
func (tm *TailscaleManager) GetICEServers() ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{"urls": []string{"stun:stun.l.google.com:19302"}},
	}, nil
}

// GetPeerAddresses returns Tail IPs of online peers.
func (tm *TailscaleManager) GetPeerAddresses() ([]string, error) {
	status, err := tm.getStatus()
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(status.Peer))
	for _, p := range status.Peer {
		if p.Online && p.TailAddr != "" {
			addrs = append(addrs, p.TailAddr)
		}
	}
	return addrs, nil
}

// IsDirectConnectionPossible does a quick TCP probe to a peer on port 80.
// This is a *best-effort* reachability hint only.
func (tm *TailscaleManager) IsDirectConnectionPossible(peerAddr string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(peerAddr, "80"), 5*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// GetWebRTCConfigForTailscale returns a minimal WebRTC config map tuned for Tailscale.
func (tm *TailscaleManager) GetWebRTCConfigForTailscale() map[string]interface{} {
	return map[string]interface{}{
		"iceServers": []map[string]interface{}{
			{"urls": []string{"stun:stun.l.google.com:19302"}},
		},
		"iceTransportPolicy":   "all",
		"iceCandidatePoolSize": 10,
	}
}

// Shutdown cleanly stops the Tailscale manager.
func (tm *TailscaleManager) Shutdown() error {
	if tm.cancel != nil {
		tm.cancel()
	}
	log.Println("tailscale: manager shutdown complete")
	return nil
}

// StatusHandler is an HTTP handler that returns the current tailscale status as JSON.
func (tm *TailscaleManager) StatusHandler(w http.ResponseWriter, r *http.Request) {
	status, err := tm.getStatus()
	if err != nil {
		http.Error(w, fmt.Sprintf("tailscale status error: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}
