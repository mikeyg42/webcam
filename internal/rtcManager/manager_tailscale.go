package rtcManager

import (
	"context"
	"fmt"
	"log"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/sourcegraph/jsonrpc2"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/tailscale"
	"github.com/mikeyg42/webcam/internal/video"
)

// NewManagerWithTailscale creates a new WebRTC manager (Tailscale-only)
// This is now identical to NewManager - kept for API compatibility
func NewManagerWithTailscale(appCtx context.Context, myconfig *config.Config, wsConn *websocket.Conn, recorder *video.Recorder) (*Manager, error) {
	// Redirect to the main NewManager - both are Tailscale-only now
	return NewManager(appCtx, myconfig, wsConn, recorder)
}

// legacyNewManagerWithTailscale is the old implementation (kept for reference but unused)
func legacyNewManagerWithTailscale(appCtx context.Context, myconfig *config.Config, wsConn *websocket.Conn, recorder *video.Recorder) (*Manager, error) {
	ctx, cancel := context.WithCancel(appCtx)

	// Require Tailscale - no fallback
	if !myconfig.TailscaleConfig.Enabled {
		cancel()
		return nil, fmt.Errorf("tailscale is required (TailscaleConfig.Enabled must be true)")
	}

	if err := tailscale.ValidateTailscaleConfig(&myconfig.TailscaleConfig); err != nil {
		cancel()
		return nil, fmt.Errorf("tailscale configuration invalid: %w", err)
	}

	tailscaleManager, err := tailscale.NewTailscaleManager(ctx, &myconfig.TailscaleConfig)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize tailscale: %w", err)
	}
	log.Println("Tailscale networking initialized for WebRTC")

	tsIP := tailscaleManager.GetLocalTailscaleIP()
	if tsIP == "" {
		cancel()
		return nil, fmt.Errorf("tailscale not connected (no tailnet IP)")
	}

	// Configure WebRTC for Tailscale-only
	pcConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
		ICETransportPolicy: webrtc.ICETransportPolicyAll,
	}
	log.Printf("Using Tailscale WebRTC configuration - Local IP: %s", tsIP)

	// Create the manager
	m := &Manager{
		config:                  myconfig,
		wsConnection:            wsConn,
		recorder:                recorder,
		pcConfiguration:         pcConfig,
		ctx:                     ctx,
		cancel:                  cancel,
		done:                    make(chan struct{}),
		connectionStateHandlers: make(map[int64]func(webrtc.PeerConnectionState)),
		pendingOperations:       make([]func() error, 0),
		tailscaleManager:        tailscaleManager,
	}

	// Create RPC handler and connection
	m.handler = &rtcHandler{manager: m}

	// Create a WebSocket adapter for jsonrpc2
	wsAdapter := &websocketAdapter{conn: wsConn}
	stream := jsonrpc2.NewBufferedStream(wsAdapter, jsonrpc2.PlainObjectCodec{})
	m.rpcConn = jsonrpc2.NewConn(ctx, stream, m.handler)

	m.ConnectionDoctor = m.NewConnectionDoctor(ctx)

	return m, nil
}

// GetICEServersForTailscale returns optimized ICE servers when using Tailscale
func (m *Manager) GetICEServersForTailscale() []webrtc.ICEServer {
	if m.tailscaleManager != nil {
		// For Tailscale networks, we primarily rely on direct connections
		// but keep STUN for fallback scenarios
		return []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		}
	}

	// Return original ICE servers if not using Tailscale
	return m.pcConfiguration.ICEServers
}

// IsTailscaleEnabled returns whether Tailscale is being used for networking
func (m *Manager) IsTailscaleEnabled() bool {
	return m.tailscaleManager != nil
}

// GetTailscaleIP returns the local Tailscale IP address if available
func (m *Manager) GetTailscaleIP() string {
	if m.tailscaleManager != nil {
		return m.tailscaleManager.GetLocalTailscaleIP()
	}
	return ""
}

// GetTailscalePeers returns addresses of other devices on the Tailscale network
func (m *Manager) GetTailscalePeers() ([]string, error) {
	if m.tailscaleManager != nil {
		return m.tailscaleManager.GetPeerAddresses()
	}
	return []string{}, fmt.Errorf("Tailscale not enabled")
}

// UpdateManagerForTailscale modifies an existing manager to support Tailscale
func (m *Manager) UpdateManagerForTailscale() error {
	if !m.config.TailscaleConfig.Enabled {
		return fmt.Errorf("Tailscale not enabled in configuration")
	}

	// Initialize Tailscale if not already done
	if m.tailscaleManager == nil {
		var err error
		m.tailscaleManager, err = tailscale.NewTailscaleManager(m.ctx, &m.config.TailscaleConfig)
		if err != nil {
			return fmt.Errorf("failed to initialize Tailscale: %w", err)
		}
	}

	// Update peer connection configuration for Tailscale
	m.pcConfiguration.ICEServers = []webrtc.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	}

	log.Printf("Manager updated for Tailscale networking - Local IP: %s", 
		m.tailscaleManager.GetLocalTailscaleIP())
	return nil
}

// Shutdown cleans up both traditional and Tailscale resources
func (m *Manager) ShutdownWithTailscale(ctx context.Context) error {
	// First call the original Shutdown
	if err := m.Shutdown(ctx); err != nil {
		log.Printf("Warning during standard shutdown: %v", err)
	}

	// Clean up Tailscale resources
	if m.tailscaleManager != nil {
		if err := m.tailscaleManager.Shutdown(); err != nil {
			log.Printf("Warning during Tailscale shutdown: %v", err)
		}
	}

	log.Println("Manager shutdown complete (with Tailscale cleanup)")
	return nil
}