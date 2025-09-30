package rtcManager

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/gorilla/websocket"

	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/opus"
	"github.com/pion/mediadevices/pkg/codec/vpx"
	_ "github.com/pion/mediadevices/pkg/driver/camera"     // This is required to register camera adapter - DON'T REMOVE
	_ "github.com/pion/mediadevices/pkg/driver/microphone" // This is required to register microphone adapter  - DON'T REMOVE
	"github.com/pion/mediadevices/pkg/prop"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/tailscale"
	"github.com/mikeyg42/webcam/internal/video"
)

// websocketAdapter adapts a WebSocket connection to io.ReadWriteCloser
type websocketAdapter struct {
	conn      *websocket.Conn
	readBuf   bytes.Buffer
	readMutex sync.Mutex
}

func (w *websocketAdapter) Read(p []byte) (n int, err error) {
	w.readMutex.Lock()
	defer w.readMutex.Unlock()

	// If we have data buffered, read from buffer first
	if w.readBuf.Len() > 0 {
		return w.readBuf.Read(p)
	}

	// Read the next WebSocket message
	messageType, data, err := w.conn.ReadMessage()
	if err != nil {
		return 0, err
	}

	// Handle both text and binary messages from ion-sfu
	// Both message types should contain JSON data that jsonrpc2 can process
	switch messageType {
	case websocket.TextMessage, websocket.BinaryMessage:
		// Both text and binary messages are treated as JSON data
		// ion-sfu may send JSON as binary messages, which is valid
		log.Printf("WebSocket received message type %d, length %d", messageType, len(data))
	default:
		return 0, fmt.Errorf("unsupported WebSocket message type: %d", messageType)
	}

	// Write to buffer with newline (jsonrpc2 expects line-delimited JSON)
	w.readBuf.Write(data)
	w.readBuf.WriteString("\n")
	return w.readBuf.Read(p)
}

func (w *websocketAdapter) Write(p []byte) (n int, err error) {
	// Remove trailing newlines as WebSocket messages are naturally message-delimited
	data := bytes.TrimRight(p, "\n")
	err = w.conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *websocketAdapter) Close() error {
	return w.conn.Close()
}

// WebRTC related structs
type Candidate struct {
	Target    int                  `json:"target"`
	Candidate *webrtc.ICECandidate `json:"candidate"`
}

type ResponseCandidate struct {
	Target    int                      `json:"target"`
	Candidate *webrtc.ICECandidateInit `json:"candidate"`
}

type SendAnswer struct {
	SID    string                     `json:"sid"`
	Answer *webrtc.SessionDescription `json:"answer"`
}

type TrickleResponse struct {
	Params ResponseCandidate `json:"params"`
	Method string            `json:"method"`
}

type Response struct {
	Params *webrtc.SessionDescription `json:"params"`
	Result *webrtc.SessionDescription `json:"result"`
	Method string                     `json:"method"`
	Id     uint64                     `json:"id"`
}

// SignalingState tracks the current state of the signaling process
type SignalingState int

const (
	SignalingStateNew SignalingState = iota
	SignalingStateOffering
	SignalingStateAnswering
	SignalingStateStable
)

type SDPValidationError struct {
	Field   string
	Message string
}
type DTLSConfig struct {
	fingerprint    string
	certificate    *tls.Certificate
	verifyPeerCert bool
}

// Manager handles WebRTC connection and signaling
type Manager struct {
	config                   *config.Config // from config package
	PeerConnection           *webrtc.PeerConnection
	connectionID             uint64
	wsConnection             *websocket.Conn
	mediaStream              mediadevices.MediaStream
	camera                   mediadevices.MediaDeviceInfo
	microphone               mediadevices.MediaDeviceInfo
	recorder                 *video.Recorder
	pcConfiguration          webrtc.Configuration
	mu                       *sync.RWMutex
	ctx                      context.Context
	cancel                   context.CancelFunc
	done                     chan struct{}
	closed                   bool // Track if cleanup has been called
	dtlsConfig               *DTLSConfig
	dtlsStateChan            chan webrtc.DTLSTransportState
	connectionStateHandlers  map[int64]func(webrtc.PeerConnectionState)
	handlersLock             sync.RWMutex
	nextHandlerID            int64
	connectionAttempts       int
	isNegotiating            atomic.Bool
	needsRenegotiation       atomic.Bool
	pendingOperations        []func() error
	lastCodecSelector        *mediadevices.CodecSelector
	turnServer               *TURNServer
	tailscaleManager         *tailscale.TailscaleManager
	rpcConn                  *jsonrpc2.Conn
	handler                  *rtcHandler
	ConnectionDoctor         *ConnectionDoctor
	MediaMode                string // "base", "moderate", or "aggressive"
	LastConstraintChangeTime time.Time
	MediaConstraints         struct {
		base       MediaConstraints
		moderate   MediaConstraints
		aggressive MediaConstraints
	}
	rtcpFeedbackBuffer *RTCPFeedbackBuffer
	signalingReady     atomic.Bool // Track when initial signaling setup is complete
}
type MediaConstraints struct {
	Video struct {
		Width     int
		Height    int
		FrameRate float32
		BitRate   uint
	}
	Audio struct {
		SampleRate   int
		ChannelCount int
		BitRate      uint
	}
}

// ====================
// rtcHandler implements jsonrpc2.Handler
type rtcHandler struct {
	manager *Manager
}

func (h *rtcHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	if req == nil {
		return
	}

	switch req.Method {
	case "offer":
		var offer webrtc.SessionDescription
		if err := json.Unmarshal(*req.Params, &offer); err != nil {
			conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    jsonrpc2.CodeParseError,
				Message: fmt.Sprintf("failed to parse offer: %v", err),
			})
			return
		}

		if err := h.manager.handleOffer(ctx, &offer); err != nil {
			conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    jsonrpc2.CodeInternalError,
				Message: err.Error(),
			})
			return
		}

		conn.Reply(ctx, req.ID, nil)

	case "answer":
		var answer webrtc.SessionDescription
		if err := json.Unmarshal(*req.Params, &answer); err != nil {
			log.Printf("Failed to parse answer: %v", err)
			return
		}
		h.manager.PeerConnection.SetRemoteDescription(answer)

	case "trickle":
		var trickleMsg ResponseCandidate
		if err := json.Unmarshal(*req.Params, &trickleMsg); err != nil {
			log.Printf("Failed to parse ICE candidate structure: %v", err)
			return
		}
		if trickleMsg.Candidate != nil {
			if err := h.manager.PeerConnection.AddICECandidate(*trickleMsg.Candidate); err != nil {
				log.Printf("Failed to add ICE candidate: %v", err)
			} else {
				log.Printf("Successfully added ICE candidate from target %d", trickleMsg.Target)
			}
		}
	default:
		conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeMethodNotFound,
			Message: fmt.Sprintf("method %s not found", req.Method),
		})
	}

}

//================

// Initialize both TURN server and RPC connection
func NewManager(appCtx context.Context, myconfig *config.Config, wsConn *websocket.Conn, recorder *video.Recorder) (*Manager, error) {
	ctx, cancel := context.WithCancel(appCtx)

	// Initialize TURN server first
	// Check if Tailscale is enabled and initialize accordingly
	var tailscaleManager *tailscale.TailscaleManager
	var turnServer *TURNServer

	if myconfig.TailscaleConfig.Enabled {
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

	if !myconfig.TailscaleConfig.Enabled {
		turnServer = CreateTURNServer(ctx)
	}

	// Create ICE configuration with TURN server
	pcConfig := webrtc.Configuration{
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

	m := &Manager{
		config:                  myconfig,
		wsConnection:            wsConn,
		camera:                  mediadevices.MediaDeviceInfo{},
		microphone:              mediadevices.MediaDeviceInfo{},
		recorder:                recorder,
		pcConfiguration:         pcConfig,
		connectionID:            0,
		mu:                      &sync.RWMutex{},
		ctx:                     ctx,
		cancel:                  cancel,
		done:                    make(chan struct{}),
		connectionStateHandlers: make(map[int64]func(webrtc.PeerConnectionState)),
		nextHandlerID:           0,
		pendingOperations:       make([]func() error, 0),
		handlersLock:            sync.RWMutex{},
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

	// Initialize RTCP feedback buffer for preventing feedback loops
	m.rtcpFeedbackBuffer = NewRTCPFeedbackBuffer(1000) // Store up to 1000 RTCP events

	return m, nil
}

func (m *Manager) Initialize() (*mediadevices.CodecSelector, error) {
	log.Println("DEBUGGING: WebRTC Manager Initialize() started")

	// Skip TURN server when streaming to ion-sfu (ion-sfu handles NAT traversal)
	log.Println("DEBUGGING: Skipping TURN server - using ion-sfu for media forwarding")

	// Create MediaEngine
	mediaEngine := webrtc.MediaEngine{}

	// Register default codecs first
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("failed to register default codecs: %v", err)
	} else {
		fmt.Println("Default codecs registered successfully")
	}

	// Enable TWCC for video
	mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{
		Type: "transport-cc",
	}, webrtc.RTPCodecTypeVideo)

	// Register RTCP feedback for audio (Opus)
	mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{
		Type: "transport-cc",
	}, webrtc.RTPCodecTypeAudio)

	// Additional Opus-specific feedback mechanisms
	mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{
		Type: "nack",
	}, webrtc.RTPCodecTypeAudio)

	// Create VP9 parameters with bandwidth-based optimization
	vpxParams, err := vpx.NewVP9Params()
	if err != nil {
		return nil, fmt.Errorf("failed to create VP9 params: %v", err)
	}

	// Bandwidth-based optimization (estimate ~1-5 Mbps typical)
	// Conservative VP9 settings to avoid encoder errors
	estimatedBandwidth := m.estimateBandwidth()               // In bps
	optimalBitrate := uint(float64(estimatedBandwidth) * 0.4) // Use only 40% of estimated bandwidth (ultra-conservative)
	if optimalBitrate > 500_000 {
		optimalBitrate = 500_000 // Cap at 500kbps for VP9 encoder stability
	}
	if optimalBitrate < 150_000 {
		optimalBitrate = 150_000 // Reduced minimum threshold
	}

	// Use minimal VP9 parameters to avoid vpx_codec_encode error (8)
	vpxParams.BitRate = int(optimalBitrate)
	vpxParams.KeyFrameInterval = 60 // More conservative: every 4 seconds at 15fps
	// Use VBR for VP9 stability (CBR can cause encoding issues)
	vpxParams.RateControlEndUsage = vpx.RateControlVBR
	// Remove Deadline and ErrorResilient to prevent invalid parameter errors

	// Create Opus parameters
	opusParams, err := opus.NewParams()
	if err != nil {
		return nil, fmt.Errorf("failed to create Opus params: %v", err)
	}
	opusParams.BitRate = 32_000
	opusParams.Latency = opus.Latency20ms // 20 ms frame size for real-time communication

	log.Printf("Using VP9 video constraints: BitRate=%d (from %d bps bandwidth), KeyFrameInterval=%d, RateControl=VBR (conservative mode)\n",
		vpxParams.BitRate, m.estimateBandwidth(), vpxParams.KeyFrameInterval)
	log.Printf("Using audio constraints: BitRate=%d, Latency=%d\n", opusParams.BitRate, opusParams.Latency)

	// Create VP9-only codec selector to prevent VP8 encoder creation
	// This forces mediadevices to only create VP9 encoders
	codecSelector := mediadevices.NewCodecSelector(
		mediadevices.WithVideoEncoders(&vpxParams), // VP9 only
		mediadevices.WithAudioEncoders(&opusParams),
	)
	log.Printf("VP9-only Codec Selector Configured: %v", codecSelector)

	// Register VP9-only codec with the MediaEngine to prevent VP8 registration
	// This ensures only VP9 is available during negotiation
	err = mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeVP9,
			ClockRate: 90000,
			RTCPFeedback: []webrtc.RTCPFeedback{
				{Type: "nack"}, {Type: "nack", Parameter: "pli"},
				{Type: "ccm", Parameter: "fir"}, {Type: "transport-cc"},
			},
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo)
	if err != nil {
		return nil, fmt.Errorf("failed to register VP9 codec: %v", err)
	}

	// Register Opus for audio
	err = mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
			RTCPFeedback: []webrtc.RTCPFeedback{
				{Type: "transport-cc"},
			},
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio)
	if err != nil {
		return nil, fmt.Errorf("failed to register Opus codec: %v", err)
	}

	log.Println("VP9-only codecs registered successfully")

	// Create SettingEngine for DTLS configuration
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetDTLSDisableInsecureSkipVerify(true)

	// Create API with MediaEngine
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(&mediaEngine),
		webrtc.WithSettingEngine(settingEngine),
	)

	// Add ICE transport policy
	m.pcConfiguration.ICETransportPolicy = webrtc.ICETransportPolicyAll

	// Enable ICE-lite if your SFU supports it
	settingEngine.SetLite(false)

	// Set ICE candidate timeout
	settingEngine.SetICETimeouts(
		5*time.Second,  // disconnected timeout
		25*time.Second, // failed timeout
		4*time.Second,  // keep-alive interval
	)
	// Create peer connection
	peerConnection, err := api.NewPeerConnection(m.pcConfiguration)
	if err != nil || peerConnection == nil {
		return nil, fmt.Errorf("failed to create peer connection: %v", err)
	}

	// Add transceivers with VP9-only codec preferences (proper Pion approach)
	videoTransceiver, err := peerConnection.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionSendonly,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to add video transceiver: %v", err)
	}

	// Set codec preferences to VP9-only using the recommended Pion approach
	// Create VP9 codec parameters manually since GetCodecsByKind is unexported
	vp9Codecs := []webrtc.RTPCodecParameters{
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypeVP9,
				ClockRate: 90000,
				RTCPFeedback: []webrtc.RTCPFeedback{
					{Type: "nack"},
					{Type: "nack", Parameter: "pli"},
					{Type: "ccm", Parameter: "fir"},
					{Type: "transport-cc"},
				},
			},
			PayloadType: 96,
		},
	}

	if err := videoTransceiver.SetCodecPreferences(vp9Codecs); err != nil {
		log.Printf("Warning: Failed to set VP9 codec preferences: %v", err)
		// Continue anyway - fallback to default negotiation
	} else {
		log.Printf("Successfully set VP9-only codec preferences (%d codecs)", len(vp9Codecs))
	}

	// Add audio transceiver
	if _, err := peerConnection.AddTransceiverFromKind(
		webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionSendonly,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to add audio transceiver: %v", err)
	}

	// Store peer connection in Manager
	m.PeerConnection = peerConnection
	m.connectionID = uint64(uuid.New().ID())

	// Set up all callbacks in one place
	m.setupCallbacks()

	return codecSelector, nil
}

// AddConnectionStateHandler adds a new connection state handler and returns its ID
func (m *Manager) AddConnectionStateHandler(handler func(webrtc.PeerConnectionState)) int64 {
	m.handlersLock.Lock()
	defer m.handlersLock.Unlock()

	// Generate unique ID for this handler
	handlerID := atomic.AddInt64(&m.nextHandlerID, 1)

	// Store the handler
	m.connectionStateHandlers[handlerID] = handler

	// If PeerConnection already exists, update its callback
	if m.PeerConnection != nil {
		currentHandlers := m.connectionStateHandlers
		m.PeerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
			m.handlersLock.RLock()
			defer m.handlersLock.RUnlock()

			// Call all registered handlers
			for _, h := range currentHandlers {
				h(state)
			}
		})
	}

	return handlerID
}

// RemoveConnectionStateHandler removes a connection state handler by its ID
func (m *Manager) RemoveConnectionStateHandler(handlerID int64) {
	m.handlersLock.Lock()
	defer m.handlersLock.Unlock()

	// Remove the handler
	delete(m.connectionStateHandlers, handlerID)

	// If PeerConnection exists, update its callback
	if m.PeerConnection != nil {
		currentHandlers := m.connectionStateHandlers
		m.PeerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
			m.handlersLock.RLock()
			defer m.handlersLock.RUnlock()

			// Call remaining handlers
			for _, h := range currentHandlers {
				h(state)
			}
		})
	}
}

// register all callbacks in one place
func (m *Manager) setupCallbacks() {
	// Connection State
	m.PeerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("PeerConnection State changed to: %s", state)

		// Call all registered handlers
		m.handlersLock.RLock()
		handlers := make([]func(webrtc.PeerConnectionState), 0, len(m.connectionStateHandlers))
		for _, handler := range m.connectionStateHandlers {
			handlers = append(handlers, handler)
		}
		m.handlersLock.RUnlock()

		// Call handlers outside the lock
		for _, handler := range handlers {
			handler(state)
		}

		// Handle state changes in general peer connection
		switch state {
		case webrtc.PeerConnectionStateNew:
			log.Println("PeerConnection is new")
		case webrtc.PeerConnectionStateConnecting:
			log.Println("PeerConnection is establishing...")
		case webrtc.PeerConnectionStateConnected:
			log.Println("PeerConnection established successfully!")
			m.handleConnectionEstablished()
		case webrtc.PeerConnectionStateDisconnected:
			log.Println("PeerConnection disconnected")
			m.handleDisconnection()
		case webrtc.PeerConnectionStateFailed:
			log.Println("PeerConnection failed")
			select {
			case m.ConnectionDoctor.warnings <- Warning{
				Timestamp: time.Now().Format("15:04:05.000"),
				Level:     CriticalLevel,
				Type:      ConnWarning,
				Message:   fmt.Sprintln("Peer Connection Failed! "),
			}:
			default:
				log.Println("Warning channel ful")
				m.handleDisconnection()
			}

		case webrtc.PeerConnectionStateClosed:
			log.Println("PeerConnection closed")
			m.Cleanup()
		}
	})

	// ICE Connection State
	m.PeerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State changed to: %s", state)

		switch state {
		case webrtc.ICEConnectionStateNew:
			log.Println("ICE new - waiting for candidates")
			// Extend timeout to prevent premature shutdowns
			go func() {
				select {
				case <-time.After(30 * time.Second): // Increased from 10s to 30s
					if m.PeerConnection.ICEConnectionState() == webrtc.ICEConnectionStateNew {
						log.Println("WARNING: ICE stuck in 'new' state for 30 seconds")
						// Log current ICE candidates
						if m.PeerConnection.SCTP() != nil {
							transport := m.PeerConnection.SCTP().Transport()
							if transport != nil {
								x, _ := transport.GetLocalParameters()
								log.Printf("Current ICE candidates: %+v", x)
							}
						} else {
							log.Println("No SCTP transport available because no negotiation has occurred")
						}

						// Send warning to connection doctor instead of immediate failure
						if m.ConnectionDoctor != nil {
							select {
							case m.ConnectionDoctor.warnings <- Warning{
								Timestamp: time.Now().Format("15:04:05.000"),
								Level:     SuggestionLevel,
								Type:      ICEWarning,
								Message:   "ICE connection stuck in 'new' state for 30 seconds",
							}:
							default:
							}
						}
					}
				case <-m.ctx.Done():
					return
				}
			}()

		case webrtc.ICEConnectionStateChecking:
			log.Println("ICE checking - candidates being tested")
			if m.PeerConnection.SCTP() != nil && m.PeerConnection.SCTP().Transport() != nil {
				dtlsparams, _ := m.PeerConnection.SCTP().Transport().GetLocalParameters()
				log.Printf("ICE checking - DTLS role: %s", dtlsparams.Role.String())
			}

		case webrtc.ICEConnectionStateConnected:
			log.Println("ICE connected - connection established")

		case webrtc.ICEConnectionStateCompleted:
			log.Println("ICE completed - connection fully established")

		case webrtc.ICEConnectionStateDisconnected:
			log.Println("ICE disconnected - but not necessarily failed, waiting for recovery...")
			// Don't immediately restart, wait for potential recovery
			go func() {
				select {
				case <-time.After(15 * time.Second): // Wait longer before considering it failed
					if m.PeerConnection.ICEConnectionState() == webrtc.ICEConnectionStateDisconnected {
						log.Println("ICE still disconnected after 15 seconds, attempting recovery")
						if err := m.handleConnectionFailure(); err != nil {
							log.Printf("Failed to handle ICE disconnection recovery: %v", err)
						}
					}
				case <-m.ctx.Done():
					return
				}
			}()

		case webrtc.ICEConnectionStateFailed:
			log.Println("ICE failed - attempting restart")
			if err := m.handleConnectionFailure(); err != nil {
				log.Printf("Failed to handle ICE failure: %v", err)
			}
		}
	})

	// Signaling State
	m.PeerConnection.OnSignalingStateChange(func(state webrtc.SignalingState) {
		log.Printf("Signaling state changed to: %s", state.String())
		if state == webrtc.SignalingStateStable {
			m.handleStableState()
		}
	})

	// Track handling
	m.PeerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Received track: ID=%s, Kind=%s, SSRC=%d, codec=%s",
			track.ID(), track.Kind(), track.SSRC(), track.Codec().MimeType)
		m.handleTrack(track)
	})

	// Negotiation
	var negotiating atomic.Bool
	m.PeerConnection.OnNegotiationNeeded(func() {
		if !negotiating.CompareAndSwap(false, true) {
			log.Println("Skipping negotiation, already in progress")
			return
		}
		defer negotiating.Store(false)

		// Add delay to prevent rapid renegotiation cycles
		time.Sleep(100 * time.Millisecond)

		if err := m.handleNegotiationNeeded(); err != nil {
			log.Printf("Negotiation failed: %v", err)
			// Send warning to connection doctor instead of immediate failure
			if m.ConnectionDoctor != nil {
				select {
				case m.ConnectionDoctor.warnings <- Warning{
					Timestamp: time.Now().Format("15:04:05.000"),
					Level:     SuggestionLevel,
					Type:      SignalingWarning,
					Message:   fmt.Sprintf("Negotiation failed: %v", err),
				}:
				default:
					// Channel full
				}
			}
		}
	})

	// ICE Candidate handling
	m.PeerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			if err := m.SendICECandidate(candidate); err != nil {
				log.Printf("Failed to send ICE candidate: %v", err)
			}
		}
	})
}

// handleConnectionEstablished is called when the PeerConnection state transitions to "Connected".
// It resets failure counters, ensures that the media stream is set up, and verifies the DTLS transport state.
func (m *Manager) handleConnectionEstablished() {
	m.mu.Lock()
	// Reset connection failure counters as the connection is now established.
	m.connectionAttempts = 0
	m.mu.Unlock()

	// Ensure media stream is available.
	if m.mediaStream == nil {
		log.Println("[handleConnectionEstablished] Warning: No media stream available after connection established")
		if err := m.GenerateANDSetStream(m.lastCodecSelector); err != nil {
			log.Printf("[handleConnectionEstablished] Failed to regenerate media stream: %v", err)
		}
	}

	// Check DTLS transport state.
	if sctp := m.PeerConnection.SCTP(); sctp != nil {
		if transport := sctp.Transport(); transport != nil {
			state := transport.State()
			log.Printf("[handleConnectionEstablished] DTLS Transport state after connection: %s", state)
			if state != webrtc.DTLSTransportStateConnected {
				log.Println("[handleConnectionEstablished] Warning: DTLS transport not in connected state")
			}
		}
	}
}

// handleStableState is called when the signaling state transitions to "stable".
// It clears negotiation flags, processes queued operations, and verifies the SDP descriptions.
func (m *Manager) handleStableState() {
	var pendingOps []func() error

	m.mu.Lock()
	// Clear the negotiation flag.
	m.isNegotiating.Store(false)

	// Copy pending operations and clear the slice.
	if len(m.pendingOperations) > 0 {
		log.Printf("[handleStableState] Processing %d pending operations", len(m.pendingOperations))
		pendingOps = make([]func() error, len(m.pendingOperations))
		copy(pendingOps, m.pendingOperations)
		m.pendingOperations = m.pendingOperations[:0]
	}
	m.mu.Unlock()

	// Process pending operations outside of the lock.
	for _, op := range pendingOps {
		if err := op(); err != nil {
			log.Printf("[handleStableState] Failed to process pending operation: %v", err)
		}
	}

	// Verify that both local and remote descriptions are available.
	if m.PeerConnection.LocalDescription() == nil {
		log.Println("[handleStableState] Warning: No local description in stable state")
	}
	if m.PeerConnection.RemoteDescription() == nil {
		log.Println("[handleStableState] Warning: No remote description in stable state")
	}

	// Log negotiated codecs after reaching stable state
	m.logNegotiatedCodecs("handleStableState")

	// Check whether a renegotiation was requested.
	if m.needsRenegotiation.Load() {
		log.Println("[handleStableState] Stable state reached, processing pending renegotiation")
		m.needsRenegotiation.Store(false)
		// Process renegotiation concurrently.
		go func() {
			if err := m.handleNegotiationNeeded(); err != nil {
				log.Printf("[handleStableState] Failed to handle pending renegotiation: %v", err)
			}
		}()
	}
}

// handleNegotiationNeeded creates a new offer and initiates a negotiation using JSON-RPC.
func (m *Manager) handleNegotiationNeeded() error {
	// Quick state check outside the critical section.
	if m.PeerConnection.SignalingState() != webrtc.SignalingStateStable {
		return fmt.Errorf("cannot renegotiate because signaling state is not stable")
	}

	// Check if initial signaling setup is complete
	if !m.signalingReady.Load() {
		log.Println("[handleNegotiationNeeded] Initial signaling not complete yet, deferring negotiation")
		return nil // Defer negotiation until initial signaling is complete
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	offer, err := m.PeerConnection.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("[handleNegotiationNeeded] failed to create offer: %v", err)
	}

	// Prioritize VP9 in the offer SDP BEFORE setting local description
	offer.SDP = prioritizeVP9InSDP(offer.SDP)

	// Don't validate our own local offer - only validate remote SDPs

	if err := m.PeerConnection.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("[handleNegotiationNeeded] failed to set local description: %v", err)
	}

	log.Println("[handleNegotiationNeeded] Negotiation offer created and local description set")
	return m.SendOffer(&offer)
}

func (m *Manager) SendICECandidate(candidate *webrtc.ICECandidate) error {
	return m.rpcConn.Notify(m.ctx, "trickle", candidate)
}

func (m *Manager) SetupSignaling() error {
	log.Println("[SetupSignaling] Setting up signaling connection to ion-sfu")

	// Create an offer to send with the join request
	offer, err := m.PeerConnection.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("[SetupSignaling] failed to create offer: %v", err)
	}

	// Prioritize VP9 in SDP BEFORE setting local description
	offer.SDP = prioritizeVP9InSDP(offer.SDP)

	// Set local description
	if err := m.PeerConnection.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("[SetupSignaling] failed to set local description: %v", err)
	}

	// Send join request with offer
	params := struct {
		SID   string `json:"sid"`
		Offer struct {
			SDP  string `json:"sdp"`
			Type string `json:"type"`
		} `json:"offer"`
	}{
		SID: "cameraRoom",
		Offer: struct {
			SDP  string `json:"sdp"`
			Type string `json:"type"`
		}{
			SDP:  offer.SDP,
			Type: offer.Type.String(),
		},
	}

	var answer webrtc.SessionDescription
	err = m.rpcConn.Call(m.ctx, "join", params, &answer)
	if err != nil {
		return fmt.Errorf("[SetupSignaling] join request failed: %v", err)
	}

	// Set remote description with the answer
	if err := m.PeerConnection.SetRemoteDescription(answer); err != nil {
		// Log the error but don't fail completely if it's a codec issue
		if strings.Contains(err.Error(), "codec is not supported") || strings.Contains(err.Error(), "unable to start track") {
			log.Printf("[SetupSignaling] Codec negotiation warning (continuing): %v", err)
			// Continue anyway - the connection might still work for compatible tracks
		} else {
			return fmt.Errorf("[SetupSignaling] failed to set remote description: %v", err)
		}
	}
	// Log the negotiated codecs
	m.logNegotiatedCodecs("SetupSignaling")

	// Mark signaling as ready for future negotiations
	m.signalingReady.Store(true)
	log.Println("[SetupSignaling] Successfully completed signaling handshake")
	return nil
}

func (m *Manager) handleOffer(ctx context.Context, offer *webrtc.SessionDescription) error {
	// is this right?
	if err := validateSDP(offer); err != nil {
		return fmt.Errorf("remote SDP validation failed: %w", err)
	}

	if err := m.PeerConnection.SetRemoteDescription(*offer); err != nil {
		return err
	}

	answer, err := m.PeerConnection.CreateAnswer(nil)
	if err != nil {
		return err
	}

	if err := m.PeerConnection.SetLocalDescription(answer); err != nil {
		return err
	}
	// Log the negotiated codecs
	m.logNegotiatedCodecs("handleOffer")

	return m.rpcConn.Call(ctx, "answer", answer, nil)
}

// handleTrack is called when a remote track arrives from the PeerConnection.
func (m *Manager) handleTrack(track *webrtc.TrackRemote) {
	if track == nil {
		log.Println("[handleTrack] Received a nil track")
		return
	}

	log.Printf("[handleTrack] Received track: ID=%s, Kind=%s, SSRC=%d",
		track.ID(), track.Kind(), track.SSRC())

	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		go m.handleIncomingRTP(track, "video", "webcam-video")
	case webrtc.RTPCodecTypeAudio:
		go m.handleIncomingRTP(track, "audio", "webcam-audio")
	default:
		log.Printf("[handleTrack] Unknown track kind: %s", track.Kind())
	}
}
func (m *Manager) GenerateStream(codecSelector *mediadevices.CodecSelector) (mediadevices.MediaStream, error) {
	var stream mediadevices.MediaStream
	if codecSelector == nil {
		return stream, fmt.Errorf("codec selector cannot be nil")
	}

	// Calculate optimal resolution based on bandwidth
	width, height := m.calculateOptimalResolution()
	if width%64 != 0 || height%64 != 0 {
		return stream, fmt.Errorf("width and height must be divisible by 16, as must width/4 and height/4 (our falllback)")
	}
	if width == 0 || height == 0 {
		width = 640
		height = 512  // Must be divisible by 64
	}

	log.Printf("[GenerateStream] Attempting to generate media stream with camera: %s, microphone: %s", m.camera.Label, m.microphone.Label)
	log.Printf("[GenerateStream] Using optimal resolution: %dx%d (bandwidth: %d bps)", width, height, m.estimateBandwidth())

	// Fallback: Use camera capture directly (original implementation)
	log.Printf("[GenerateStream] Using direct camera capture fallback")

	// First attempt with bandwidth-optimized constraints
	constraints := mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			// Set device ID as string property
			c.DeviceID = prop.String(m.camera.DeviceID)

			// Use I420 format for direct camera capture
			//c.FrameFormat = prop.FrameFormat(frame.FormatI420)

			// Use calculated optimal resolution for bandwidth efficiency
			c.Width = prop.IntExact(width)    // Bandwidth-optimized width
			c.Height = prop.IntExact(height)  // Bandwidth-optimized height
			c.FrameRate = prop.FloatExact(15) // Fixed 15fps as requested
		},
		Audio: func(c *mediadevices.MediaTrackConstraints) {
			// Set device ID as string property
			c.DeviceID = prop.String(m.microphone.DeviceID)

			// Set audio format constraints with explicit types
			c.SampleRate = prop.IntExact(48000)
			c.ChannelCount = prop.IntExact(1)
			c.SampleSize = prop.IntExact(16)

			// Use boolean constraints with explicit types
			c.IsFloat = prop.BoolExact(false)
			c.IsBigEndian = prop.BoolExact(false)
			c.IsInterleaved = prop.BoolExact(true)

			// Set latency with proper duration type
			c.Latency = prop.Duration(20 * time.Millisecond)
		},
		Codec: codecSelector,
	}

	stream, err := mediadevices.GetUserMedia(constraints)
	if err != nil {
		log.Printf("[GenerateStream] First attempt failed: %v, trying fallback constraints", err)

		// Fallback: Try with moderate resolution but still WebRTC-compatible
		fallbackConstraints := mediadevices.MediaStreamConstraints{
			Video: func(c *mediadevices.MediaTrackConstraints) {
				c.DeviceID = prop.String(m.camera.DeviceID)
				//c.FrameFormat = prop.FrameFormat(frame.FormatI420) // Use I420 for camera capture
				c.Width = prop.IntExact(320)   // Fixed 320x256 (64-divisible)
				c.Height = prop.IntExact(256)  // Fixed 320x256 (64-divisible)
				c.FrameRate = prop.FloatExact(15)    // same framerate
			},
			Audio: func(c *mediadevices.MediaTrackConstraints) {
				c.DeviceID = prop.String(m.microphone.DeviceID)
				c.SampleRate = prop.IntExact(16000) // Lower sample rate
				c.ChannelCount = prop.IntExact(1)
				c.SampleSize = prop.IntExact(16)
				c.Latency = prop.Duration(50 * time.Millisecond) // Higher latency
			},
			Codec: codecSelector,
		}

		stream, err = mediadevices.GetUserMedia(fallbackConstraints)
		if err != nil {
			log.Printf("[GenerateStream] Fallback attempt failed: %v, trying video-only", err)

			// Final fallback: Video-only with minimal WebRTC-compatible resolution
			videoOnlyConstraints := mediadevices.MediaStreamConstraints{
				Video: func(c *mediadevices.MediaTrackConstraints) {
					c.DeviceID = prop.String(m.camera.DeviceID)
					//c.FrameFormat = prop.FrameFormat(frame.FormatI420) // Use I420 for camera capture
					c.Width = prop.Int(320)    // Fixed 320x256 (64-divisible)
					c.Height = prop.Int(256)   // Fixed 320x256 (64-divisible)
					c.FrameRate = prop.Float(5)
				},
				Codec: codecSelector,
			}

			stream, err = mediadevices.GetUserMedia(videoOnlyConstraints)
			if err != nil {
				return stream, fmt.Errorf("failed to get user media with all fallback constraints: %v", err)
			}
			log.Println("[GenerateStream] Video-only stream created successfully")
		} else {
			log.Println("[GenerateStream] Fallback constraints succeeded")
		}
	} else {
		log.Println("[GenerateStream] Primary constraints succeeded")
	}

	// detailed debugging of what we actually got
	log.Printf("[GenerateStream] Stream created with %d video tracks and %d audio tracks",
		len(stream.GetVideoTracks()), len(stream.GetAudioTracks()))

	// Debug video tracks with available methods
	for i, track := range stream.GetVideoTracks() {
		log.Printf("[GenerateStream] Video Track %d:", i)
		log.Printf("  - ID: %s", track.ID())
		log.Printf("  - Kind: %s", track.Kind())
	}

	// Debug audio tracks
	for i, track := range stream.GetAudioTracks() {
		log.Printf("[GenerateStream] Audio Track %d:", i)
		log.Printf("  - ID: %s", track.ID())
		log.Printf("  - Kind: %s", track.Kind())
	}

	return stream, nil
}

func (m *Manager) GenerateANDSetStream(codecSelector *mediadevices.CodecSelector) error {
	if codecSelector == nil {
		return fmt.Errorf("GenerateANDSetStream: codecSelector cannot be nil")
	}
	stream, err := m.GenerateStream(codecSelector)
	if err != nil {
		return fmt.Errorf("GenerateANDSetStream: failed to generate stream: %w", err)
	}

	m.mu.Lock()
	m.mediaStream = stream
	m.mu.Unlock()

	fmt.Println("Media stream generated successfully and saved to Manager")
	return nil
}

func (m *Manager) setupVideoTrack() (*webrtc.TrackLocalStaticRTP, *webrtc.RTPSender, error) {
	videoCodec := webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeVP9,
		ClockRate:   90000,
		SDPFmtpLine: "profile-id=0",
		RTCPFeedback: []webrtc.RTCPFeedback{
			{Type: "nack"}, {Type: "nack", Parameter: "pli"},
			{Type: "ccm", Parameter: "fir"}, {Type: "transport-cc"},
		},
	}

	videoTrack, err := webrtc.NewTrackLocalStaticRTP(videoCodec, "video", "webcam-video")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create video track: %v", err)
	}

	videoRtpSender, err := m.PeerConnection.AddTrack(videoTrack)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add video track: %v", err)
	}

	return videoTrack, videoRtpSender, nil
}

func (m *Manager) setupAudioTrack() (*webrtc.TrackLocalStaticRTP, *webrtc.RTPSender, error) {
	audioCodec := webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   48000,
		Channels:    1,
		SDPFmtpLine: "minptime=10;useinbandfec=1",
	}

	audioTrack, err := webrtc.NewTrackLocalStaticRTP(audioCodec, "audio", "webcam-audio")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create audio track: %v", err)
	}

	audioRtpSender, err := m.PeerConnection.AddTrack(audioTrack)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add audio track: %v", err)
	}

	return audioTrack, audioRtpSender, nil
}

func (m *Manager) handleMediaPackets(srcTrack mediadevices.Track, localTrack *webrtc.TrackLocalStaticRTP, ssrc uint32, mtu int) {
	const maxBufferSize = 25 // Maximum number of packets to buffer
	pktBuffer := make(chan []*rtp.Packet, maxBufferSize)

	// OPUS 4 DEBUGGING: Track type analysis
	log.Printf("[TRACK_DEBUG] Track type: %T", srcTrack)
	log.Printf("[TRACK_DEBUG] Track ID: %s", srcTrack.ID())
	log.Printf("[TRACK_DEBUG] Track Kind: %s", srcTrack.Kind().String())

	// OPUS 4 DEBUGGING: LocalTrack analysis
	mimeType := localTrack.Codec().MimeType
	log.Printf("[TRACK_DEBUG] LocalTrack codec: %s", mimeType)
	log.Printf("[TRACK_DEBUG] LocalTrack Number of Channels: %v", localTrack.Codec().Channels)
	log.Printf("[TRACK_DEBUG] LocalTrack SSRC provided: %d", ssrc)

	// We need the "codec name" for calling NewRTPReader, which generally is safe to assume is the second part of the MIME type
	mimeParts := strings.SplitN(mimeType, "/", 2)
	if len(mimeParts) != 2 {
		log.Printf("[TRACK_DEBUG] ERROR: Invalid MIME type format: %s", mimeType)
		return
	}
	codec := mimeParts[1]
	log.Printf("[TRACK_DEBUG] Extracted codec name: %s", codec)

	// OPUS 4 DEBUGGING: Try multiple codec variations
	log.Printf("[TRACK_DEBUG] Attempting to create RTP reader with codec: '%s', SSRC: %d, MTU: %d", codec, ssrc, mtu)

	rtpReader, err := srcTrack.NewRTPReader(codec, ssrc, mtu)
	if err != nil {
		log.Printf("[TRACK_DEBUG] FAILED with codec '%s': %v", codec, err)

		// OPUS 4 SUGGESTION: Try lowercase codec
		lowerCodec := strings.ToLower(codec)
		log.Printf("[TRACK_DEBUG] Trying lowercase codec: '%s'", lowerCodec)
		rtpReader, err = srcTrack.NewRTPReader(lowerCodec, ssrc, mtu)
		if err != nil {
			log.Printf("[TRACK_DEBUG] FAILED with lowercase codec '%s': %v", lowerCodec, err)

			// OPUS 4 SUGGESTION: Try empty codec
			log.Printf("[TRACK_DEBUG] Trying empty codec string")
			rtpReader, err = srcTrack.NewRTPReader("", ssrc, mtu)
			if err != nil {
				log.Printf("[TRACK_DEBUG] FAILED with empty codec: %v", err)

				// OPUS 4 SUGGESTION: Check if srcTrack is nil or invalid
				if srcTrack == nil {
					log.Printf("[TRACK_DEBUG] ERROR: srcTrack is nil!")
				} else {
					log.Printf("[TRACK_DEBUG] srcTrack is valid, but NewRTPReader consistently fails")
				}

				log.Printf("RTP reader failed with all attempts: original='%s', lowercase='%s', empty=''", codec, lowerCodec)
				return
			}
			log.Printf("[TRACK_DEBUG] SUCCESS with empty codec!")
		} else {
			log.Printf("[TRACK_DEBUG] SUCCESS with lowercase codec: '%s'", lowerCodec)
		}
	} else {
		log.Printf("[TRACK_DEBUG] SUCCESS with original codec: '%s'", codec)
	}

	// Producer
	go func() {
		defer func() {
			close(pktBuffer)
			_ = rtpReader.Close() // Clean up when done
			log.Printf("[handleMediaPacketsWithRTPReader] Done forwarding track: %s", srcTrack.ID())
		}()
		for {
			select {
			case <-m.ctx.Done():
				return
			default:
				pkts, release, err := rtpReader.Read()
				if err != nil {
					if err != io.EOF {
						log.Printf("[handleMediaPacketsWithRTPReader]RTP read error: %v", err)
						// For VP9 encoder errors, try to continue after a brief pause
						if strings.Contains(err.Error(), "vpx_codec_encode failed") {
							log.Println("[handleMediaPacketsWithRTPReader]VP9 encoder error, attempting to continue...")
							time.Sleep(100 * time.Millisecond)
							continue
						}
					}
					return
				}
				if release != nil {
					defer release()
				}
				if pkts == nil || len(pkts) == 0 {
					continue
				}

				select {
				case pktBuffer <- pkts:
				default:
					// Buffer full, drop oldest packet
					<-pktBuffer
					pktBuffer <- pkts
				}
			}
		}
	}()

	// Consumer reads slices of packets from the channel
	for pkts := range pktBuffer {
		for _, pkt := range pkts {
			if pkt == nil {
				continue
			}
			// localTrack.WriteRTP(...) needs a single packet
			if err := localTrack.WriteRTP(pkt); err != nil {
				log.Printf("[handleMediaPackets] WriteRTP error: %v", err)
			}
		}
	}
}

func (m *Manager) readRTCP(rtpSender *webrtc.RTPSender, mediaKind string) {
	rtcpBuf := make([]byte, 1500)

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
			n, attributes, rtcpErr := rtpSender.Read(rtcpBuf)
			if rtcpErr != nil {
				log.Printf("[readRTCP] %s RTCP read error: %v", mediaKind, rtcpErr)
				// Send error to connection doctor if available
				if m.ConnectionDoctor != nil {
					select {
					case m.ConnectionDoctor.warnings <- Warning{
						Timestamp: time.Now().Format("15:04:05.000"),
						Level:     SuggestionLevel,
						Type:      ConnWarning,
						Message:   fmt.Sprintf("RTCP read error for %s: %v", mediaKind, rtcpErr),
					}:
					default:
						// Channel full, continue
					}
				}
				return
			}

			// Analyze RTCP packets if we have data, using circular buffer for feedback loop prevention
			if n > 0 {
				m.processRTCPWithBuffer(rtcpBuf[:n], attributes, mediaKind)
			}
		}
	}
}

func (m *Manager) SetupMediaTracks(
	camera, microphone mediadevices.MediaDeviceInfo,
	codecSelector *mediadevices.CodecSelector,
) error {
	if m.PeerConnection == nil {
		return fmt.Errorf("peer connection not initialized")
	}

	log.Printf("[SetupMediaTracks] Starting media track setup for camera: %s, microphone: %s", camera.Label, microphone.Label)

	m.mu.Lock()
	// Clean up old m.mediaStream
	if m.mediaStream != nil {
		log.Println("[SetupMediaTracks] Cleaning up existing media stream")
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
	}
	m.camera = camera
	m.microphone = microphone
	m.mu.Unlock()

	// Generate new media stream with retry logic
	const maxRetries = 3
	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err = m.GenerateANDSetStream(codecSelector)
		if err == nil {
			break
		}
		log.Printf("[SetupMediaTracks] Media stream generation attempt %d failed: %v", attempt, err)
		if attempt < maxRetries {
			// Wait before retry with exponential backoff
			waitTime := time.Duration(attempt*attempt) * time.Second
			log.Printf("[SetupMediaTracks] Retrying in %v...", waitTime)
			time.Sleep(waitTime)
		}
	}
	if err != nil {
		return fmt.Errorf("failed to generate media stream after %d attempts: %v", maxRetries, err)
	}

	log.Println("[SetupMediaTracks] Media stream generated successfully")

	// Setup local static video track
	videoTrack, videoSender, err := m.setupVideoTrack()
	if err != nil {
		// Try to continue with audio only if video fails
		log.Printf("[SetupMediaTracks] Video track setup failed: %v, attempting audio-only mode", err)

		// Setup audio track only
		audioTrack, audioSender, err := m.setupAudioTrack()
		if err != nil {
			return fmt.Errorf("both video and audio track setup failed: %v", err)
		}

		go m.readRTCP(audioSender, "audio")
		go m.attachMediaKind(audioTrack, audioSender, false /* isAudio */)

		log.Println("[SetupMediaTracks] Successfully set up audio-only mode")
		return nil
	}

	// Setup local static audio track
	audioTrack, audioSender, err := m.setupAudioTrack()
	if err != nil {
		log.Printf("[SetupMediaTracks] Audio track setup failed: %v, continuing with video-only mode", err)

		// Continue with video only
		go m.readRTCP(videoSender, "video")
		go m.attachMediaKind(videoTrack, videoSender, true /* isVideo */)

		log.Println("[SetupMediaTracks] Successfully set up video-only mode")
		return nil
	}

	// Both tracks successful
	go m.readRTCP(videoSender, "video")
	go m.readRTCP(audioSender, "audio")

	// Attach the newly captured media to those local tracks
	go m.attachMediaKind(videoTrack, videoSender, true /* isVideo */)
	go m.attachMediaKind(audioTrack, audioSender, false /* isAudio */)

	log.Println("[SetupMediaTracks] Successfully set up both video and audio tracks")
	return nil
}

// attachMediaKind is a helper that takes either "video" or "audio" tracks
// from m.mediaStream, obtains the SSRC from rtpSender, and spawns goroutines
// to handle each track’s RTP packets.
func (m *Manager) attachMediaKind(
	localTrack *webrtc.TrackLocalStaticRTP,
	rtpSender *webrtc.RTPSender,
	isVideo bool,
) {
	var kindStr string
	var mediaTracks []mediadevices.Track

	if isVideo {
		kindStr = "video"
		// Read-lock while accessing m.mediaStream
		m.mu.RLock()
		mediaTracks = m.mediaStream.GetVideoTracks()
		m.mu.RUnlock()
	} else {
		kindStr = "audio"
		m.mu.RLock()
		mediaTracks = m.mediaStream.GetAudioTracks()
		m.mu.RUnlock()
	}

	if len(mediaTracks) == 0 {
		log.Printf("[attachMediaKind] No %s tracks available", kindStr)
		return
	}

	params := rtpSender.GetParameters()
	if len(params.Encodings) == 0 || params.Encodings[0].SSRC == 0 {
		log.Printf("[attachMediaKind] No valid SSRC found for %s", kindStr)
		return
	}
	ssrc := uint32(params.Encodings[0].SSRC)
	log.Printf("[attachMediaKind] Expected %s SSRC: %v", kindStr, ssrc)

	// For each captured track from mediadevices, spawn a goroutine to handle its packets.
	const mtu = 1200
	for _, track := range mediaTracks {
		go m.handleMediaPackets(track, localTrack, ssrc, mtu)
	}
}

// handleIncomingRTP generalizes the creation of a local static track and reading remote RTP packets.
func (m *Manager) handleIncomingRTP(
	remoteTrack *webrtc.TrackRemote,
	mediaKind, streamID string,
) {
	if remoteTrack == nil {
		log.Printf("[handleIncomingRTP] Remote track for %s is nil", mediaKind)
		return
	}

	// Create a local track to write incoming RTP to.
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		remoteTrack.Codec().RTPCodecCapability,
		mediaKind,
		streamID,
	)
	if err != nil {
		log.Printf("[handleIncomingRTP] Failed creating local track (%s): %v", mediaKind, err)
		return
	}

	// Add track to the PeerConnection (thus re-broadcasting the incoming media?).
	rtpSender, err := m.PeerConnection.AddTrack(localTrack)
	if err != nil {
		log.Printf("[handleIncomingRTP] Failed adding %s track to PeerConnection: %v", mediaKind, err)
		return
	}

	// Start a goroutine to handle RTCP from the rtpSender.
	go m.handleRTCP(rtpSender, mediaKind)

	// Start a goroutine that reads RTP packets from the remote track and writes them to localTrack.
	go m.forwardRTPPackets(remoteTrack, localTrack, rtpSender, mediaKind)
}

// handleRTCP reads RTCP packets from the given RTPSender (e.g., feedback about the sent stream).
func (m *Manager) handleRTCP(rtpSender *webrtc.RTPSender, mediaKind string) {
	rtcpBuf := make([]byte, 1500)

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
			n, attributes, err := rtpSender.Read(rtcpBuf)
			if err != nil {
				log.Printf("[handleRTCP] %s RTCP read error: %v", mediaKind, err)
				// Send error to connection doctor if available
				if m.ConnectionDoctor != nil {
					select {
					case m.ConnectionDoctor.warnings <- Warning{
						Timestamp: time.Now().Format("15:04:05.000"),
						Level:     SuggestionLevel,
						Type:      ConnWarning,
						Message:   fmt.Sprintf("RTCP read error in handleRTCP for %s: %v", mediaKind, err),
					}:
					default:
						// Channel full, continue
					}
				}
				return
			}

			// Analyze RTCP packets if we have data, using circular buffer for feedback loop prevention
			if n > 0 {
				m.processRTCPWithBuffer(rtcpBuf[:n], attributes, mediaKind)
			}
		}
	}
}

// forwardRTPPackets reads incoming RTP from the remote track and writes them to the local track.
func (m *Manager) forwardRTPPackets(
	remoteTrack *webrtc.TrackRemote,
	localTrack *webrtc.TrackLocalStaticRTP,
	rtpSender *webrtc.RTPSender,
	mediaKind string,
) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[forwardRTPPackets] Recovered panic in %s handler: %v", mediaKind, r)
			debug.PrintStack()
		}
		// Clean-up logic
		if err := rtpSender.Stop(); err != nil {
			log.Printf("[forwardRTPPackets] Error stopping %s RTP sender: %v", mediaKind, err)
		}
		log.Printf("[forwardRTPPackets] %s RTP forwarding stopped.", mediaKind)
	}()

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
			pkt, _, err := remoteTrack.ReadRTP()
			if err != nil {
				if err == io.EOF {
					log.Printf("[forwardRTPPackets] %s track ended", mediaKind)
				} else {
					log.Printf("[forwardRTPPackets] %s RTP read error: %v", mediaKind, err)
				}
				return
			}

			if pkt == nil {
				continue
			}

			// Write packet to the local track.
			if writeErr := localTrack.WriteRTP(pkt); writeErr != nil {
				log.Printf("[forwardRTPPackets] Error writing %s RTP: %v", mediaKind, writeErr)
			}
		}
	}
}

// analyzeRTCPPacket parses and analyzes RTCP packets for connection health metrics
func (m *Manager) analyzeRTCPPacket(data []byte, attributes interface{}, mediaKind string) {
	// Import the necessary rtcp package for parsing
	// For now, let's do basic analysis on the raw bytes

	if len(data) < 4 {
		return // Invalid RTCP packet
	}

	// Basic RTCP packet structure: Version(2bits) + Padding(1bit) + ReceptionReport count(5bits) + PacketType(8bits) + Length(16bits)
	version := (data[0] >> 6) & 0x3
	packetType := data[1]

	if version != 2 {
		log.Printf("[analyzeRTCPPacket] Invalid RTCP version for %s: %d", mediaKind, version)
		return
	}

	// Send analysis to connection doctor
	if m.ConnectionDoctor != nil {
		switch packetType {
		case 200: // Sender Report (SR)
			m.handleSenderReport(data, mediaKind)
		case 201: // Receiver Report (RR)
			m.handleReceiverReport(data, mediaKind)
		case 202: // Source Description (SDES)
			// Log for debugging
			log.Printf("[analyzeRTCPPacket] Received SDES packet for %s", mediaKind)
		case 203: // Goodbye (BYE)
			select {
			case m.ConnectionDoctor.warnings <- Warning{
				Timestamp: time.Now().Format("15:04:05.000"),
				Level:     InfoLevel,
				Type:      ConnWarning,
				Message:   fmt.Sprintf("Received RTCP BYE packet for %s", mediaKind),
			}:
			default:
				// Channel full
			}
		case 205: // Transport-wide Congestion Control (TWCC) feedback
			m.handleTWCCFeedback(data, mediaKind)
		case 206: // Payload Specific Feedback (PSF) - includes NACK, PLI, FIR
			m.handlePayloadSpecificFeedback(data, mediaKind)
		default:
			log.Printf("[analyzeRTCPPacket] Unknown RTCP packet type %d for %s", packetType, mediaKind)
		}
	}
}

// handleSenderReport analyzes SR packets for timing and statistics
func (m *Manager) handleSenderReport(data []byte, mediaKind string) {
	if len(data) < 28 { // Minimum SR packet size
		return
	}

	// Extract packet count and octet count (bytes 16-23)
	packetCount := uint32(data[16])<<24 | uint32(data[17])<<16 | uint32(data[18])<<8 | uint32(data[19])
	octetCount := uint32(data[20])<<24 | uint32(data[21])<<16 | uint32(data[22])<<8 | uint32(data[23])

	log.Printf("[handleSenderReport] %s - Packets: %d, Octets: %d", mediaKind, packetCount, octetCount)
}

// handleReceiverReport analyzes RR packets for reception statistics
func (m *Manager) handleReceiverReport(data []byte, mediaKind string) {
	if len(data) < 24 { // Minimum RR packet size
		return
	}

	reportCount := data[0] & 0x1F
	if reportCount > 0 && len(data) >= 32 {
		// Extract loss statistics from first reception report block
		fractionLost := data[12]
		totalLost := uint32(data[13])<<16 | uint32(data[14])<<8 | uint32(data[15])

		lossRate := float64(fractionLost) / 256.0

		if lossRate > 0.05 { // 5% loss threshold
			select {
			case m.ConnectionDoctor.warnings <- Warning{
				Timestamp:   time.Now().Format("15:04:05.000"),
				Level:       SuggestionLevel,
				Type:        PacketLossWarning,
				Message:     fmt.Sprintf("RTCP RR indicates %s packet loss: %.2f%% (%d total)", mediaKind, lossRate*100, totalLost),
				Measurement: lossRate,
			}:
			default:
				// Channel full
			}
		}

		log.Printf("[handleReceiverReport] %s - Loss rate: %.2f%%, Total lost: %d", mediaKind, lossRate*100, totalLost)
	}
}

// handleTWCCFeedback processes Transport-wide Congestion Control feedback
func (m *Manager) handleTWCCFeedback(data []byte, mediaKind string) {
	// TWCC feedback packets indicate network congestion
	log.Printf("[handleTWCCFeedback] Received TWCC feedback for %s", mediaKind)

	// Send info to connection doctor
	select {
	case m.ConnectionDoctor.warnings <- Warning{
		Timestamp: time.Now().Format("15:04:05.000"),
		Level:     InfoLevel,
		Type:      BitrateWarning,
		Message:   fmt.Sprintf("Received TWCC congestion feedback for %s", mediaKind),
	}:
	default:
		// Channel full
	}
}

// handlePayloadSpecificFeedback processes PSF packets (NACK, PLI, FIR)
func (m *Manager) handlePayloadSpecificFeedback(data []byte, mediaKind string) {
	if len(data) < 12 {
		return
	}

	feedbackType := data[0] & 0x1F

	switch feedbackType {
	case 1: // NACK - Negative Acknowledgment
		select {
		case m.ConnectionDoctor.warnings <- Warning{
			Timestamp: time.Now().Format("15:04:05.000"),
			Level:     SuggestionLevel,
			Type:      PacketLossWarning,
			Message:   fmt.Sprintf("Received NACK feedback for %s - requesting packet retransmission", mediaKind),
		}:
		default:
		}
		log.Printf("[handlePayloadSpecificFeedback] Received NACK for %s", mediaKind)

	case 2: // PLI - Picture Loss Indication
		select {
		case m.ConnectionDoctor.warnings <- Warning{
			Timestamp: time.Now().Format("15:04:05.000"),
			Level:     SuggestionLevel,
			Type:      FramerateWarning,
			Message:   fmt.Sprintf("Received PLI feedback for %s - picture corruption detected", mediaKind),
		}:
		default:
		}
		log.Printf("[handlePayloadSpecificFeedback] Received PLI for %s", mediaKind)

	case 4: // FIR - Full Intra Request
		select {
		case m.ConnectionDoctor.warnings <- Warning{
			Timestamp: time.Now().Format("15:04:05.000"),
			Level:     SuggestionLevel,
			Type:      FramerateWarning,
			Message:   fmt.Sprintf("Received FIR feedback for %s - requesting keyframe", mediaKind),
		}:
		default:
		}
		log.Printf("[handlePayloadSpecificFeedback] Received FIR for %s", mediaKind)

	default:
		log.Printf("[handlePayloadSpecificFeedback] Unknown PSF type %d for %s", feedbackType, mediaKind)
	}
}

// processRTCPWithBuffer processes RTCP packets using the circular buffer for intelligent feedback loop prevention
func (m *Manager) processRTCPWithBuffer(data []byte, attributes interface{}, mediaKind string) {
	if len(data) < 4 {
		return // Invalid RTCP packet
	}

	// Parse basic RTCP packet structure
	version := (data[0] >> 6) & 0x3
	packetType := data[1]

	if version != 2 {
		log.Printf("[processRTCPWithBuffer] Invalid RTCP version for %s: %d", mediaKind, version)
		return
	}

	// Check if we should process this packet type to prevent feedback loops
	if !m.rtcpFeedbackBuffer.ShouldProcessPacket(packetType, mediaKind) {
		log.Printf("[processRTCPWithBuffer] Skipping %s packet type %d for %s to prevent feedback loop",
			mediaKind, packetType, mediaKind)
		return
	}

	// Extract SSRC for tracking
	var ssrc uint32
	if len(data) >= 8 {
		ssrc = uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])
	}

	// Determine feedback type for PSF packets
	var feedbackType uint8
	if packetType == 206 && len(data) >= 12 { // PSF packet
		feedbackType = data[0] & 0x1F
	}

	// Create feedback event and add to circular buffer
	event := RTCPFeedbackEvent{
		Timestamp:    time.Now(),
		MediaKind:    mediaKind,
		PacketType:   packetType,
		FeedbackType: feedbackType,
		SSRC:         ssrc,
		Data:         make([]byte, len(data)),
	}
	copy(event.Data, data)
	m.rtcpFeedbackBuffer.Add(event)

	// Process the packet using existing analysis logic
	if m.ConnectionDoctor != nil {
		switch packetType {
		case 200: // Sender Report (SR)
			m.handleSenderReport(data, mediaKind)
		case 201: // Receiver Report (RR)
			m.handleReceiverReport(data, mediaKind)
		case 202: // Source Description (SDES)
			log.Printf("[processRTCPWithBuffer] Received SDES packet for %s (SSRC: %d)", mediaKind, ssrc)
		case 203: // Goodbye (BYE)
			select {
			case m.ConnectionDoctor.warnings <- Warning{
				Timestamp: time.Now().Format("15:04:05.000"),
				Level:     InfoLevel,
				Type:      ConnWarning,
				Message:   fmt.Sprintf("Received RTCP BYE packet for %s (SSRC: %d)", mediaKind, ssrc),
			}:
			default:
				// Channel full
			}
		case 205: // Transport-wide Congestion Control (TWCC) feedback
			m.handleTWCCFeedback(data, mediaKind)
		case 206: // Payload Specific Feedback (PSF) - includes NACK, PLI, FIR
			m.handlePayloadSpecificFeedback(data, mediaKind)
		default:
			log.Printf("[processRTCPWithBuffer] Unknown RTCP packet type %d for %s (SSRC: %d)",
				packetType, mediaKind, ssrc)
		}
	}

	// Log buffer statistics periodically (every 100 packets)
	if m.rtcpFeedbackBuffer.Size() > 0 && m.rtcpFeedbackBuffer.Size()%100 == 0 {
		stats := m.rtcpFeedbackBuffer.GetFeedbackStats(30 * time.Second)
		log.Printf("[processRTCPWithBuffer] RTCP feedback stats (last 30s): %+v", stats)
	}
}

// handleConnectionFailure attempts to recover from a connection failure using an ICE restart.
func (m *Manager) handleConnectionFailure() error {
	log.Println("[handleConnectionFailure] Starting connection failure recovery process")

	m.mu.Lock()
	defer m.mu.Unlock()

	// Increment connection attempts and add backoff to prevent rapid retries
	m.connectionAttempts++
	if m.connectionAttempts > 5 {
		return fmt.Errorf("too many connection attempts (%d), aborting restart", m.connectionAttempts)
	}

	// Add exponential backoff
	backoffDuration := time.Duration(m.connectionAttempts*m.connectionAttempts) * time.Second
	log.Printf("[handleConnectionFailure] Attempt %d, waiting %v before restart", m.connectionAttempts, backoffDuration)
	time.Sleep(backoffDuration)

	// Check if we're still in a state that requires restart
	currentState := m.PeerConnection.ConnectionState()
	if currentState == webrtc.PeerConnectionStateConnected || currentState == webrtc.PeerConnectionStateConnecting {
		log.Println("[handleConnectionFailure] Connection recovered during backoff, canceling restart")
		m.connectionAttempts = 0 // Reset on recovery
		return nil
	}

	// Create an offer with ICE restart enabled.
	offer, err := m.PeerConnection.CreateOffer(&webrtc.OfferOptions{
		ICERestart: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create restart offer: %v", err)
	}

	// Set the local description.
	if err := m.PeerConnection.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("failed to set local description: %v", err)
	}

	// Wait for ICE gathering to complete (with extended timeout for stability).
	gatherComplete := webrtc.GatheringCompletePromise(m.PeerConnection)
	select {
	case <-gatherComplete:
		log.Println("[handleConnectionFailure] ICE gathering completed for restart")
	case <-time.After(30 * time.Second): // Increased timeout from 10s to 30s
		log.Println("[handleConnectionFailure] ICE gathering timed out, but continuing...")
		// Don't return error here - gathering might still complete later
	case <-m.ctx.Done():
		return fmt.Errorf("context canceled during ICE gathering")
	}

	// Check RPC connection health before sending restart offer
	if m.rpcConn == nil {
		return fmt.Errorf("rpcConn is not initialized")
	}

	// Send restart offer using jsonrpc2
	if err := m.rpcConn.Notify(m.ctx, "offer", m.PeerConnection.LocalDescription()); err != nil {
		return fmt.Errorf("failed to notify remote peer with restart offer: %v", err)
	}

	log.Println("[handleConnectionFailure] ICE restart offer sent successfully")

	// Start monitoring for restart success
	go m.monitorRestartProgress()

	return nil
}

// monitorRestartProgress monitors the connection after a restart attempt
func (m *Manager) monitorRestartProgress() {
	timeout := time.After(60 * time.Second) // Give restart 60 seconds to succeed
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			log.Println("[monitorRestartProgress] Restart timeout reached")
			return
		case <-ticker.C:
			state := m.PeerConnection.ConnectionState()
			log.Printf("[monitorRestartProgress] Connection state during restart: %s", state)

			if state == webrtc.PeerConnectionStateConnected {
				log.Println("[monitorRestartProgress] Restart successful!")
				m.mu.Lock()
				m.connectionAttempts = 0 // Reset on successful restart
				m.mu.Unlock()
				return
			} else if state == webrtc.PeerConnectionStateFailed {
				log.Println("[monitorRestartProgress] Restart failed, connection still in failed state")
				// Don't immediately retry - let the normal failure handler deal with it
				return
			}
		case <-m.ctx.Done():
			return
		}
	}
}

// handleDisconnection cleans up resources and attempts reconnection.
func (m *Manager) handleDisconnection() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Cleanup media resources.
	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
		m.mediaStream = nil
	}

	// Close the current PeerConnection.
	if m.PeerConnection != nil {
		m.PeerConnection.Close()
		m.PeerConnection = nil
	}

	// Wait briefly before reconnecting.
	time.Sleep(2 * time.Second)

	// Reinitialize your connection.

	codecSelector, err := m.Initialize()
	if err != nil {
		return fmt.Errorf("failed to reinitialize: %v", err)
	}

	// Setup media tracks again (using stored camera and microphone info).
	if err := m.SetupMediaTracks(m.camera, m.microphone, codecSelector); err != nil {
		return fmt.Errorf("failed to setup media tracks: %v", err)
	}

	// Setup your signaling once again. This might include re-establishing
	// the JSON-RPC connection if necessary.
	return m.SetupSignaling()
}

func validateSDP(sd *webrtc.SessionDescription) error {
	if sd == nil {
		return fmt.Errorf("SessionDescription is nil")
	}

	lines := strings.Split(sd.SDP, "\n")
	var (
		hasAudio, hasVideo                       bool
		hasICE, hasDTLS                          bool
		mediaCount                               int
		fingerprint                              string
		hasVideoTWCC, hasAudioTWCC, hasAudioNACK bool
	)

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "m="):
			mediaCount++
			if strings.HasPrefix(line, "m=audio") {
				hasAudio = true
			}
			if strings.HasPrefix(line, "m=video") {
				hasVideo = true
			}

		case strings.HasPrefix(line, "a=ice-ufrag:"):
			hasICE = true

		case strings.HasPrefix(line, "a=fingerprint:"):
			hasDTLS = true
			fingerprint = strings.TrimPrefix(line, "a=fingerprint:")

		case strings.HasPrefix(line, "b=AS:"):
			bitrateStr := strings.TrimPrefix(line, "b=AS:")
			bitrate, err := strconv.Atoi(bitrateStr)
			if err != nil {
				return fmt.Errorf("bitrate invalid: %v", err)
			}
			if bitrate < config.MinBitrate || bitrate > config.MaxBitrate {
				return fmt.Errorf("bitrate is outside allowed range (%d-%d)",
					config.MinBitrate, config.MaxBitrate)
			}
		}

		// Also in the same pass, check for feedback lines:
		if strings.Contains(line, "transport-cc") && strings.Contains(line, "video") {
			hasVideoTWCC = true
		}
		if strings.Contains(line, "transport-cc") && strings.Contains(line, "audio") {
			hasAudioTWCC = true
		}
		if strings.Contains(line, "nack") && strings.Contains(line, "audio") {
			hasAudioNACK = true
		}
	}

	if mediaCount == 0 {
		return fmt.Errorf("no media sections found")
	}
	if !hasICE {
		return fmt.Errorf("no ICE credentials found")
	}
	if !hasDTLS {
		return fmt.Errorf("no DTLS fingerprint found")
	}
	if fingerprint == "" {
		return fmt.Errorf("fingerprint for DTLS is empty")
	}
	if !hasAudio && !hasVideo {
		return fmt.Errorf("no audio or video tracks found in SDP")
	}

	log.Printf("SDP Feedback - Video TWCC: %v, Audio TWCC: %v, Audio NACK: %v",
		hasVideoTWCC, hasAudioTWCC, hasAudioNACK)

	return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	if m.turnServer != nil {
		if err := m.turnServer.Stop(); err != nil {
			log.Printf("Failed to stop TURN server: %v", err)
		}
	}

	// Create a channel to signal completion
	shutdownComplete := make(chan struct{})

	go func() {
		// Close all tracks
		if m.mediaStream != nil {
			for _, track := range m.mediaStream.GetTracks() {
				track.Close()
			}
		}

		// Close peer connection
		if m.PeerConnection != nil {
			m.PeerConnection.Close()
		}

		// Cancel context and close channels
		m.cancel()

		// Only close if not already closed by Cleanup
		select {
		case <-m.done:
			// Channel already closed
		default:
			close(m.done)
		}
		close(shutdownComplete)
	}()

	// Wait for shutdown to complete or context to timeout
	select {
	case <-shutdownComplete:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown timed out: %v", ctx.Err())
	}
}

func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return // Already cleaned up
	}
	m.closed = true

	if m.rpcConn != nil {
		m.rpcConn.Close()
	}

	// Close media stream
	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
	}

	// Close peer connection
	if m.PeerConnection != nil {
		m.PeerConnection.Close()
	}

	// Cancel context
	m.cancel()

	// Close channels
	close(m.done)
	if m.dtlsStateChan != nil {
		close(m.dtlsStateChan)
	}

	log.Println("Cleanup completed")
}

// these helper methods to Manager are mostly for the testDevices method of selectDervices in main.go
func (m *Manager) SetCamera(device mediadevices.MediaDeviceInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.camera = device
}

func (m *Manager) SetMicrophone(device mediadevices.MediaDeviceInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.microphone = device
}

func (m *Manager) SendOffer(offer *webrtc.SessionDescription) error {
	params := struct {
		SDP  string `json:"sdp"`
		Type string `json:"type"`
	}{
		SDP:  offer.SDP,
		Type: offer.Type.String(),
	}

	var result interface{}
	if err := m.rpcConn.Call(m.ctx, "offer", params, &result); err != nil {
		return fmt.Errorf("offer request failed: %v", err)
	}

	return nil
}

// prioritizeVP9InSDP modifies SDP to prioritize VP9 codec over VP8
func prioritizeVP9InSDP(sdp string) string {
	// Find VP9 and VP8 payload types in the SDP
	vp9Pattern := regexp.MustCompile(`a=rtpmap:(\d+) VP9/90000`)
	vp8Pattern := regexp.MustCompile(`a=rtpmap:(\d+) VP8/90000`)

	vp9Matches := vp9Pattern.FindStringSubmatch(sdp)
	vp8Matches := vp8Pattern.FindStringSubmatch(sdp)

	if len(vp9Matches) < 2 || len(vp8Matches) < 2 {
		log.Printf("[prioritizeVP9InSDP] Could not find both VP9 and VP8 codecs in SDP")
		return sdp
	}

	vp9PayloadType := vp9Matches[1]
	vp8PayloadType := vp8Matches[1]

	log.Printf("[prioritizeVP9InSDP] Found VP9 payload type: %s, VP8 payload type: %s", vp9PayloadType, vp8PayloadType)

	// In the m= video line, move VP9 payload type to the front
	videoLinePattern := regexp.MustCompile(`(m=video \d+ \w+/\w+ )([^\r\n]+)`)
	sdp = videoLinePattern.ReplaceAllStringFunc(sdp, func(match string) string {
		parts := videoLinePattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}

		prefix := parts[1]
		payloadTypes := parts[2]

		// Split payload types and reorder to put VP9 first
		types := strings.Fields(payloadTypes)
		var reordered []string

		// Add VP9 first if found
		for _, pt := range types {
			if pt == vp9PayloadType {
				reordered = append(reordered, pt)
				break
			}
		}

		// Add all other types except VP9 (already added)
		for _, pt := range types {
			if pt != vp9PayloadType {
				reordered = append(reordered, pt)
			}
		}

		result := prefix + strings.Join(reordered, " ")
		log.Printf("[prioritizeVP9InSDP] Modified video line: %s", result)
		return result
	})

	return sdp
}

// logNegotiatedCodecs inspects all transceivers and logs the negotiated codecs.
// Safe to call after SetRemoteDescription (offer or answer).
func (m *Manager) logNegotiatedCodecs(context string) {
	if m.PeerConnection == nil {
		log.Printf("[logNegotiatedCodecs:%s] PeerConnection is nil", context)
		return
	}

	transceivers := m.PeerConnection.GetTransceivers()
	if len(transceivers) == 0 {
		log.Printf("[logNegotiatedCodecs:%s] No transceivers found", context)
		return
	}

	for i, t := range transceivers {
		// Skip transceivers without a sender (may be recvonly)
		if t.Sender() == nil {
			log.Printf("[logNegotiatedCodecs:%s] Transceiver[%d] kind=%s has no sender", context, i, t.Kind())
			continue
		}

		params := t.Sender().GetParameters()
		if len(params.Codecs) == 0 {
			log.Printf("[logNegotiatedCodecs:%s] Transceiver[%d] kind=%s has no negotiated codecs", context, i, t.Kind())
			continue
		}

		for _, cp := range params.Codecs {
			log.Printf("[logNegotiatedCodecs:%s] Transceiver[%d] kind=%s NEGOTIATED CODEC: %s (pt=%d fmtp=%q)",
				context, i, t.Kind().String(), cp.MimeType, cp.PayloadType, cp.SDPFmtpLine)
		}
	}
}

// estimateBandwidth estimates available bandwidth for video transmission
// Returns estimated bandwidth in bits per second
func (m *Manager) estimateBandwidth() int {
	// Simple bandwidth estimation - in production this would use:
	// 1. Network interface speed detection
	// 2. Historical throughput measurements
	// 3. RTT measurements to estimate network conditions
	// 4. RTCP feedback analysis for adaptive estimation

	// For now, use conservative estimates based on common scenarios:
	// - Local network: 10+ Mbps
	// - Remote/VPN: 1-5 Mbps
	// - Mobile/limited: 0.5-2 Mbps

	// Simple heuristic: check if we're on Tailscale (indicates remote access)
	if m.config != nil && m.config.TailscaleConfig.Enabled {
		// Remote access through Tailscale - be conservative
		return 2_000_000 // 2 Mbps
	}

	// Local network - can be more aggressive
	return 5_000_000 // 5 Mbps
}

// calculateOptimalResolution determines the best resolution based on bandwidth
// Returns width, height as integers
func (m *Manager) calculateOptimalResolution() (int, int) {
	bandwidth := m.estimateBandwidth()

	// Ultra-conservative resolution tiers for VP9 encoder stability
	// Using lower resolutions to prevent vpx_codec_encode errors
	// All dimensions must be divisible by 64
	switch {
	case bandwidth >= 8_000_000: // 8+ Mbps - Use high resolution (720p equivalent, 64-divisible)
		return 1280, 768  // 768 = 720 + 48 to make it divisible by 64

	case bandwidth >= 3_000_000: // 3-8 Mbps - Use VGA-equivalent (64-divisible)
		return 640, 512  // 512 = 480 + 32 to make it divisible by 64

	case bandwidth >= 1_500_000: // 1.5-3 Mbps - 512x384 (divisible by 64)
		return 512, 384

	default: // <1.5 Mbps - Very low resolution for encoder stability
		return 320, 256 // Both divisible by 64
	}
}
