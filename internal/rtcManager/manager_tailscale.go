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

// NewManagerWithTailscale creates a new WebRTC manager with Tailscale support
func NewManagerWithTailscale(appCtx context.Context, myconfig *config.Config, wsConn *websocket.Conn, recorder *video.Recorder) (*Manager, error) {
	ctx, cancel := context.WithCancel(appCtx)

	// Check if Tailscale is enabled and initialize accordingly
	var tailscaleManager *tailscale.TailscaleManager
	var turnServer *TURNServer
	var pcConfig webrtc.Configuration

	if myconfig.TailscaleConfig.Enabled {
		// Validate Tailscale configuration
		if err := tailscale.ValidateTailscaleConfig(&myconfig.TailscaleConfig); err != nil {
			log.Printf("Tailscale configuration invalid, falling back to TURN: %v", err)
			myconfig.TailscaleConfig.Enabled = false
		} else {
			var err error
			tailscaleManager, err = tailscale.NewTailscaleManager(ctx, &myconfig.TailscaleConfig)
			if err != nil {
				log.Printf("Failed to initialize Tailscale, falling back to TURN: %v", err)
				myconfig.TailscaleConfig.Enabled = false
			} else {
				log.Println("Tailscale networking initialized for WebRTC")
			}
		}
	}

	if myconfig.TailscaleConfig.Enabled && tailscaleManager != nil {
		// Configure WebRTC to use Tailscale networking
		pcConfig = webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{
				{
					// Use Google STUN server for initial connectivity
					URLs: []string{"stun:stun.l.google.com:19302"},
				},
			},
			// Prefer direct connections since Tailscale handles NAT traversal
			ICETransportPolicy: webrtc.ICETransportPolicyAll,
		}
		log.Printf("Using Tailscale WebRTC configuration - Local IP: %s", 
			tailscaleManager.GetLocalTailscaleIP())
	} else {
		// Fall back to traditional TURN server
		turnServer = CreateTURNServer(ctx)
		pcConfig = webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{
				{
					URLs: []string{
						fmt.Sprintf("turn:%s:%d", turnServer.PublicIP, turnServer.Port),
					},
					Username:   myconfig.WebrtcAuth.Username,
					Credential: myconfig.WebrtcAuth.Password,
				},
			},
			ICETransportPolicy: webrtc.ICETransportPolicyAll,
		}
		log.Println("Using traditional TURN server configuration")
	}

	// Create the manager with Tailscale support
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
		turnServer:              turnServer,
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

	// Stop traditional TURN server if running
	if m.turnServer != nil {
		if err := m.turnServer.Stop(); err != nil {
			log.Printf("Warning: failed to stop TURN server: %v", err)
		}
		m.turnServer = nil
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