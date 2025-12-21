package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
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

	// WhoIs cache to avoid spawning processes per request
	whoisCache     map[string]*cachedWhoIs
	whoisCacheLock sync.RWMutex
}

type cachedWhoIs struct {
	email      string
	cachedAt   time.Time
	ttl        time.Duration
}

// TailscaleStatus represents the status information from Tailscale.
type TailscaleStatus struct {
	BackendState string                     `json:"BackendState"`
	Self         TailscaleDevice            `json:"Self"`
	Peer         map[string]TailscaleDevice `json:"Peer"`
}

type TailscaleDevice struct {
	ID            string   `json:"ID"`
	HostName      string   `json:"HostName"`
	DNSName       string   `json:"DNSName"`
	TailAddr      string   `json:"TailAddr"`       // Deprecated, may be null
	TailscaleIPs  []string `json:"TailscaleIPs"`  // Use this for IP addresses
	Online        bool     `json:"Online"`
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
		config:     tsConfig,
		ctx:        tsCtx,
		cancel:     cancel,
		whoisCache: make(map[string]*cachedWhoIs),
	}

	// Initialize (read status, confirm running, capture node info).
	if err := tm.initialize(); err != nil {
		cancel()
		return nil, fmt.Errorf("tailscale init: %w", err)
	}

	// Start cache cleanup goroutine
	go tm.cleanupCacheLoop()

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

	// Get Tailscale IP from TailscaleIPs array (prefer IPv4)
	if len(status.Self.TailscaleIPs) > 0 {
		// Find IPv4 address (starts with 100.)
		for _, ip := range status.Self.TailscaleIPs {
			if len(ip) > 0 && ip[0] == '1' {  // Quick check for 100.x.x.x
				tm.localIP = ip
				break
			}
		}
		// Fallback to first IP if no IPv4 found
		if tm.localIP == "" {
			tm.localIP = status.Self.TailscaleIPs[0]
		}
	}

	log.Printf("tailscale: initialized — Hostname=%s TailIP=%s", tm.hostname, tm.localIP)
	return nil
}

// getStatus retrieves the current Tailscale status (JSON) via the `tailscale` CLI.
func (tm *TailscaleManager) getStatus() (*TailscaleStatus, error) {
	// keep this fast but bounded
	ctx, cancel := context.WithTimeout(tm.ctx, 5*time.Second)
	defer cancel()

	// Use full path to avoid bundleIdentifier issues with /usr/local/bin/tailscale symlink
	tailscaleBin := getTailscaleBinary()
	cmd := exec.CommandContext(ctx, tailscaleBin, "status", "--json")
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

// WhoIsInfo represents user information from Tailscale WhoIs
type WhoIsInfo struct {
	UserProfile UserProfile `json:"UserProfile"`
	Node        NodeInfo    `json:"Node"`
}

type UserProfile struct {
	LoginName      string `json:"LoginName"`      // User's email
	DisplayName    string `json:"DisplayName"`
	ProfilePicURL  string `json:"ProfilePicURL"`
	ID             int64  `json:"ID"`
}

type NodeInfo struct {
	ID       int64  `json:"ID"`
	Name     string `json:"Name"`
	User     int64  `json:"User"`
}

// GetUserEmailFromIP uses Tailscale WhoIs to get the email of a connected user
// Results are cached for 5 minutes to avoid spawning processes per request
func (tm *TailscaleManager) GetUserEmailFromIP(ipAddr string) (string, error) {
	// Validate IP address format before using
	ip := net.ParseIP(ipAddr)
	if ip == nil {
		return "", fmt.Errorf("invalid IP address format: %s", ipAddr)
	}

	// Check cache first
	tm.whoisCacheLock.RLock()
	if cached, exists := tm.whoisCache[ipAddr]; exists {
		if time.Since(cached.cachedAt) < cached.ttl {
			email := cached.email
			tm.whoisCacheLock.RUnlock()
			return email, nil
		}
	}
	tm.whoisCacheLock.RUnlock()

	// Cache miss or expired - fetch from tailscale
	ctx, cancel := context.WithTimeout(tm.ctx, 5*time.Second)
	defer cancel()

	// Call tailscale whois with validated IP (use full path to avoid bundleIdentifier issues)
	tailscaleBin := getTailscaleBinary()
	cmd := exec.CommandContext(ctx, tailscaleBin, "whois", "--json", ip.String())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tailscale whois: %w", err)
	}

	var whoIs WhoIsInfo
	if err := json.Unmarshal(out, &whoIs); err != nil {
		return "", fmt.Errorf("parse whois json: %w", err)
	}

	if whoIs.UserProfile.LoginName == "" {
		return "", fmt.Errorf("no user email found in whois response")
	}

	email := whoIs.UserProfile.LoginName

	// Cache the result for 5 minutes
	// User-to-IP mappings rarely change, so longer TTL reduces subprocess overhead
	tm.whoisCacheLock.Lock()
	tm.whoisCache[ipAddr] = &cachedWhoIs{
		email:    email,
		cachedAt: time.Now(),
		ttl:      5 * time.Minute,
	}
	tm.whoisCacheLock.Unlock()

	return email, nil
}

// GetUserEmailFromRequest extracts user email from HTTP request using Tailscale WhoIs
//
// CRITICAL ASSUMPTION: This function assumes the API server is listening DIRECTLY on a
// Tailscale network interface and clients connect over the tailnet. It uses r.RemoteAddr
// to get the client's IP address.
//
// If you put an HTTP reverse proxy (nginx, Caddy, etc.) in front of this server:
// - r.RemoteAddr will be 127.0.0.1 or the proxy's LAN IP, NOT the client's Tailscale IP
// - Authentication will fail with "Tailscale authentication required"
// - You would need to trust X-Forwarded-For from the proxy (which we explicitly don't do)
//
// For production deployments behind a proxy:
// - Run the proxy on the same node and join it to the tailnet
// - Configure secure forwarding of the original client IP
// - Modify this function to trust X-Forwarded-For only from specific trusted sources
func (tm *TailscaleManager) GetUserEmailFromRequest(r *http.Request) (string, error) {
	// Get client IP - trust X-Forwarded-For ONLY from localhost (our trusted proxy)
	clientIP := getClientIP(r)
	if clientIP == "" {
		return "", fmt.Errorf("no client address in request")
	}

	// Check if this is a local/non-Tailscale IP
	if isLocalIP(clientIP) {
		// Development mode: Allow localhost access without Tailscale authentication
		// Set TAILSCALE_DEV_MODE=true to enable (WARNING: Bypasses all authentication!)
		if os.Getenv("TAILSCALE_DEV_MODE") == "true" {
			log.Printf("Tailscale DEV MODE: bypassing auth for localhost IP %s", clientIP)
			return "dev-user@localhost", nil
		}

		// Production: Reject non-Tailscale IPs
		log.Printf("Tailscale auth unavailable: request from non-Tailscale IP %s", clientIP)
		return "", fmt.Errorf("Tailscale authentication required")
	}

	return tm.GetUserEmailFromIP(clientIP)
}

// isLocalIP checks if the IP is localhost or private network (but NOT Tailscale CGNAT)
// Returns true for IPs that should be rejected (local/RFC1918), false for allowed IPs (Tailscale/public)
func isLocalIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	// Check for localhost
	if ip.IsLoopback() {
		return true
	}

	// Tailscale CGNAT range: 100.64.0.0/10 (100.64.0.0 - 100.127.255.255)
	// This is NOT in RFC1918, so check explicitly first
	ipv4 := ip.To4()
	if ipv4 != nil && ipv4[0] == 100 && ipv4[1] >= 64 && ipv4[1] < 128 {
		return false // This IS a Tailscale IP - allow it
	}

	// Check for RFC1918 private networks (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
	// Note: ip.IsPrivate() returns false for 100.64.0.0/10, so this doesn't conflict
	if ip.IsPrivate() {
		return true // Private IP - reject it
	}

	// Public IPs will reach here - allow them (they'll fail at whois if not in tailnet)
	return false
}

// cleanupCacheLoop periodically removes expired entries from the WhoIs cache
func (tm *TailscaleManager) cleanupCacheLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-tm.ctx.Done():
			return
		case <-ticker.C:
			tm.cleanupExpiredCache()
		}
	}
}

// cleanupExpiredCache removes expired entries from the WhoIs cache
func (tm *TailscaleManager) cleanupExpiredCache() {
	tm.whoisCacheLock.Lock()
	defer tm.whoisCacheLock.Unlock()

	now := time.Now()
	for ip, cached := range tm.whoisCache {
		if now.Sub(cached.cachedAt) > cached.ttl {
			delete(tm.whoisCache, ip)
		}
	}
}

// getTailscaleBinary returns the path to a working Tailscale binary
// Prefers the app binary which doesn't have bundleIdentifier issues
func getTailscaleBinary() string {
	// Try app binary first (works on macOS without bundleIdentifier issues)
	appBinary := "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	if _, err := os.Stat(appBinary); err == nil {
		return appBinary
	}
	// Fallback to system binary
	return "tailscale"
}

// isLocalhost checks if an IP is localhost
func isLocalhost(ipStr string) bool {
	return ipStr == "127.0.0.1" || ipStr == "::1" || ipStr == "localhost"
}

// getClientIP extracts the real client IP, trusting X-Forwarded-For ONLY from localhost
// This allows a trusted proxy (Node.js on same machine) to forward the real client IP
// while maintaining security against X-Forwarded-For spoofing from external clients
func getClientIP(r *http.Request) string {
	// Get the proxy IP (who made the request to us)
	remoteAddr := r.RemoteAddr
	proxyIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		proxyIP = remoteAddr
	}

	// If request comes from localhost (our trusted Node.js proxy), trust X-Forwarded-For
	if isLocalhost(proxyIP) {
		// Check X-Forwarded-For header for the real client IP
		forwardedFor := r.Header.Get("X-Forwarded-For")
		if forwardedFor != "" {
			// X-Forwarded-For can be a comma-separated list; take the first IP
			// This is the original client IP before any proxies
			ips := strings.Split(forwardedFor, ",")
			clientIP := strings.TrimSpace(ips[0])
			if clientIP != "" {
				log.Printf("Tailscale auth: using X-Forwarded-For IP %s from trusted proxy %s", clientIP, proxyIP)
				return clientIP
			}
		}
	}

	// Either not from localhost proxy, or no X-Forwarded-For header
	// Use the direct connection IP
	return proxyIP
}
