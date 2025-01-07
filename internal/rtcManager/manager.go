package rtcManager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
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
	"github.com/pion/mediadevices/pkg/frame"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/stun/v3"

	_ "github.com/pion/mediadevices/pkg/driver/camera"     // This is required to register camera adapter - DON'T REMOVE
	_ "github.com/pion/mediadevices/pkg/driver/microphone" // This is required to register microphone adapter  - DON'T REMOVE

	"github.com/pion/dtls"
	"github.com/pion/webrtc/v4"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/video"
)

// WebRTC related structs
type Candidate struct {
	Target    int                  `json:"target"`
	Candidate *webrtc.ICECandidate `json:"candidate"`
}

type ResponseCandidate struct {
	Target    int                      `json:"target"`
	Candidate *webrtc.ICECandidateInit `json:"candidate"`
}

type SendOffer struct {
	SID   string                     `json:"sid"`
	Offer *webrtc.SessionDescription `json:"offer"`
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

// StatsCollector provides a unified interface for WebRTC statistics
type StatsCollector struct {
	mu        sync.RWMutex
	pc        *webrtc.PeerConnection
	lastStats *ConnectionStats
	metrics   []QualityMetrics
	callback  func(*ConnectionStats)
}

// ConnectionStats represents the current state of the connection
type ConnectionStats struct {
	Timestamp time.Time
	// Network metrics
	PacketsLost     uint32
	PacketsReceived uint32
	PacketsSent     uint32
	BytesSent       uint64
	BytesReceived   uint64
	RoundTripTime   float64
	Jitter          float64

	// Video metrics
	FramesReceived uint32
	FramesDropped  uint32
	FramerateRecv  float64
	FramerateSent  float64
	VideoWidth     uint32
	VideoHeight    uint32

	// Audio metrics
	AudioLevel float64
}

type QualityMetrics struct {
	Timestamp      time.Time
	PacketLossRate float64
	RTT            time.Duration
	Framerate      float64
	Resolution     string
	Bitrate        uint64
	JitterBuffer   float64
}

const (
	monitoringInterval     = 5 * time.Second
	criticalPacketLoss     = 0.05 // 5%
	warningPacketLoss      = 0.02 // 2%
	criticalRTT            = 300 * time.Millisecond
	warningRTT             = 150 * time.Millisecond
	minAcceptableFramerate = 15.0

	// Metrics collection constants
	metricsHistoryDuration    = 5 * time.Minute
	metricsCollectionInterval = time.Second
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
	stats                   *StatsCollector
	config                  *config.Config
	PeerConnection          *webrtc.PeerConnection
	connectionID            uint64
	wsConnection            *websocket.Conn
	mediaStream             mediadevices.MediaStream
	camera                  mediadevices.MediaDeviceInfo
	microphone              mediadevices.MediaDeviceInfo
	recorder                *video.Recorder
	pcConfiguration         webrtc.Configuration
	mu                      *sync.RWMutex
	ctx                     context.Context
	cancel                  context.CancelFunc
	done                    chan struct{}
	dtlsConfig              *DTLSConfig
	dtlsStateChan           chan webrtc.DTLSTransportState
	metrics                 *CircularMetricsBuffer
	metricsMutex            sync.RWMutex
	connectionStateHandlers map[int64]func(webrtc.PeerConnectionState)
	handlersLock            sync.RWMutex
	nextHandlerID           int64
	connectionAttempts      int
	isNegotiating           atomic.Bool
	needsRenegotiation      atomic.Bool
	pendingOperations       []func() error
	lastCodecSelector       *mediadevices.CodecSelector
	stunServer              *STUNServer
}

func NewManager(myconfig *config.Config, wsConn *websocket.Conn, recorder *video.Recorder) (*Manager, error) {
	if myconfig == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if wsConn == nil {
		return nil, fmt.Errorf("websocket connection cannot be nil")
	}

	pcConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{
					"stun:stun.l.google.com:19302",
					"stun:stun1.l.google.com:19302",
					"stun:stun2.l.google.com:19302",
					"stun:stun3.l.google.com:19302",
					"stun:stun4.l.google.com:19302",
				},
			},
		},
		ICETransportPolicy: webrtc.ICETransportPolicyAll,
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &Manager{
		config:                  myconfig,
		wsConnection:            wsConn,
		camera:                  mediadevices.MediaDeviceInfo{},
		microphone:              mediadevices.MediaDeviceInfo{},
		recorder:                recorder,
		pcConfiguration:         pcConfig,
		connectionID:            1,
		mu:                      &sync.RWMutex{},
		ctx:                     ctx,
		cancel:                  cancel,
		done:                    make(chan struct{}),
		metrics:                 NewCircularMetricsBuffer(3600),
		metricsMutex:            sync.RWMutex{},
		connectionStateHandlers: make(map[int64]func(webrtc.PeerConnectionState)),
		nextHandlerID:           0,
		pendingOperations:       make([]func() error, 0),
		handlersLock:            sync.RWMutex{},
	}

	return m, nil
}

func (m *Manager) Initialize() (*mediadevices.CodecSelector, error) {
	// Start STUN server
	stunServer, err := NewSTUNServer(3478) // Standard STUN port
	if err != nil {
		return nil, fmt.Errorf("failed to create STUN server: %v", err)
	}

	if err := stunServer.Start(); err != nil {
		return nil, fmt.Errorf("failed to start STUN server: %v", err)
	}

	m.stunServer = stunServer

	// Update ICE servers configuration to use your STUN server
	m.pcConfiguration.ICEServers = []webrtc.ICEServer{
		{
			URLs: []string{
				fmt.Sprintf("stun:%s:%d", m.stunServer.publicIP, m.stunServer.port),
			},
		},
	}

	// Create MediaEngine
	mediaEngine := webrtc.MediaEngine{}

	// Register default codecs first
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("failed to register default codecs: %v", err)
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

	// Create VP8 parameters
	vpxParams, err := vpx.NewVP8Params()
	if err != nil {
		return nil, fmt.Errorf("failed to create VP8 params: %v", err)
	}
	vpxParams.BitRate = 100_000
	vpxParams.KeyFrameInterval = 15
	vpxParams.RateControlEndUsage = vpx.RateControlVBR
	vpxParams.Deadline = time.Millisecond * 200

	// Create Opus parameters
	opusParams, err := opus.NewParams()
	if err != nil {
		return nil, fmt.Errorf("failed to create Opus params: %v", err)
	}
	opusParams.BitRate = 32_000
	opusParams.Latency = opus.Latency20ms // 20 ms frame size for real-time communication

	log.Printf("Using video constraints: BitRate=%d, KeyFrameInterval=%d, Deadline=%d, RateControlMode = VBR\n",
		vpxParams.BitRate, vpxParams.KeyFrameInterval, vpxParams.Deadline)
	log.Printf("Using audio constraints: BitRate=%d, Latency=%d\n", opusParams.BitRate, opusParams.Latency)

	// Create and store codec selector
	codecSelector := mediadevices.NewCodecSelector(
		mediadevices.WithVideoEncoders(&vpxParams),
		mediadevices.WithAudioEncoders(&opusParams),
	)
	log.Printf("Codec Selector Configured: %v", codecSelector)

	// Register codecs with the MediaEngine
	codecSelector.Populate(&mediaEngine)

	// Setup DTLS
	dtlsConfig, err := newDTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create DTLS config: %v", err)
	}
	m.dtlsConfig = dtlsConfig
	m.dtlsStateChan = make(chan webrtc.DTLSTransportState, 1)

	// Create SettingEngine for DTLS configuration
	settingEngine := webrtc.SettingEngine{}
	//settingEngine.SetDTLSInsecureSkipVerify(!m.dtlsConfig.verifyPeerCert)

	// Create API with MediaEngine
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(&mediaEngine),
		webrtc.WithSettingEngine(settingEngine),
	)

	if err := m.validateICEConfiguration(); err != nil {
		return nil, fmt.Errorf("ICE configuration validation failed: %v", err)
	}

	// Add ICE transport policy
	m.pcConfiguration.ICETransportPolicy = webrtc.ICETransportPolicyAll

	// Enable ICE-lite if your SFU supports it
	settingEngine.SetLite(true)

	// Set ICE candidate timeout
	settingEngine.SetICETimeouts(
		5*time.Second,  // disconnected timeout
		10*time.Second, // failed timeout
		30*time.Second, // keep-alive interval
	)
	// Create peer connection
	peerConnection, err := api.NewPeerConnection(m.pcConfiguration)
	if err != nil || peerConnection == nil {
		return nil, fmt.Errorf("failed to create peer connection: %v", err)
	}

	// Add transceivers (TWCC will be automatically negotiated)
	if _, err := peerConnection.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionSendonly,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to add video transceiver: %v", err)
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

	// Initialize stats collector with error handling
	if m.stats == nil {
		m.stats = NewStatsCollector(m.PeerConnection)
		if m.stats == nil {
			return nil, fmt.Errorf("failed to initialize stats collector")
		}
	}

	// Start monitoring
	go func() {
		if err := m.monitorConnection(m.stats); err != nil {
			log.Printf("Failed to start connection monitoring: %v", err)
		}
	}()

	go m.monitorSTUNHealth()

	return codecSelector, nil
}

func (m *Manager) validateICEConfiguration() error {
	if len(m.pcConfiguration.ICEServers) == 0 {
		return fmt.Errorf("no ICE servers configured")
	}

	// Test STUN server connectivity
	for _, server := range m.pcConfiguration.ICEServers {
		for _, url := range server.URLs {
			if strings.HasPrefix(url, "stun:") {
				log.Printf("Testing STUN server: %s", url)
				conn, err := net.Dial("udp", strings.TrimPrefix(url, "stun:"))
				if err != nil {
					log.Printf("WARNING: Failed to connect to STUN server %s: %v", url, err)
				} else {
					conn.Close()
					log.Printf("Successfully connected to STUN server: %s", url)
				}
			}
		}
	}
	return nil
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

		// Handle state changes
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
			m.handleTemporaryDisconnection()
		case webrtc.PeerConnectionStateFailed:
			log.Println("PeerConnection failed")
			m.handleConnectionFailure()
		case webrtc.PeerConnectionStateClosed:
			log.Println("PeerConnection closed")
			m.Cleanup()
		}
	})

	// ICE Connection State
	// Add this to setupCallbacks()
	m.PeerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State changed to: %s", state)

		// Add detailed logging for ICE state transitions
		switch state {
		case webrtc.ICEConnectionStateNew:
			log.Println("ICE new - waiting for candidates")
			// Start a timeout monitor
			go func() {
				select {
				case <-time.After(10 * time.Second):
					if m.PeerConnection.ICEConnectionState() == webrtc.ICEConnectionStateNew {
						log.Println("WARNING: ICE stuck in 'new' state for 10 seconds")
						// Log current ICE candidates
						if m.PeerConnection.SCTP() != nil {
							transport := m.PeerConnection.SCTP().Transport()
							if transport != nil {
								x, _ := transport.GetLocalParameters()
								log.Printf("Current ICE candidates: %+v", x)
							}
						}
					}
				case <-m.ctx.Done():
					return
				}
			}()

		case webrtc.ICEConnectionStateChecking:
			dtlsparams, _ := m.PeerConnection.SCTP().Transport().GetLocalParameters()
			log.Printf("ICE checking - gathered candidates: %s", dtlsparams.Role.String())

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
		m.handleNegotiationNeeded()
	})

	// ICE Candidate handling
	m.PeerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		m.handleICECandidate(candidate)
	})
}

func (m *Manager) handleConnectionEstablished() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Reset failure counters
	m.connectionAttempts = 0

	// Start stats collection if not already running
	if m.stats != nil {
		go m.StartMetricsCollection(m.stats)
	}

	// Verify media streams are properly set up
	if m.mediaStream == nil {
		log.Println("Warning: No media stream available after connection established")
		if err := m.GenerateANDSetStream(m.lastCodecSelector); err != nil {
			log.Printf("Failed to regenerate media stream: %v", err)
		}
	}

	// Verify DTLS state
	if sctp := m.PeerConnection.SCTP(); sctp != nil {
		if transport := sctp.Transport(); transport != nil {
			state := transport.State()
			log.Printf("DTLS Transport state after connection: %s", state)
			if state != webrtc.DTLSTransportStateConnected {
				log.Println("Warning: DTLS transport not in connected state")
			}
		}
	}

	// Log connection quality baseline
	if stats := m.stats.GetLastStats(); stats != nil {
		log.Printf("Initial connection stats - RTT: %v, Jitter: %v",
			stats.RoundTripTime, stats.Jitter)
	}
}

func (m *Manager) handleStableState() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear any pending negotiation flags
	m.isNegotiating.Store(false)

	// Check if we need to process any pending operations
	if len(m.pendingOperations) > 0 {
		log.Printf("Processing %d pending operations", len(m.pendingOperations))
		for _, op := range m.pendingOperations {
			if err := op(); err != nil {
				log.Printf("Failed to process pending operation: %v", err)
			}
		}
		m.pendingOperations = m.pendingOperations[:0]
	}

	// Verify local and remote descriptions are properly set
	if m.PeerConnection.LocalDescription() == nil {
		log.Println("Warning: No local description in stable state")
	}
	if m.PeerConnection.RemoteDescription() == nil {
		log.Println("Warning: No remote description in stable state")
	}

	// Check if we need to renegotiate
	if m.needsRenegotiation.Load() {
		log.Println("Stable state reached, processing pending renegotiation")
		m.needsRenegotiation.Store(false)
		go func() {
			if err := m.handleNegotiationNeeded(); err != nil {
				log.Printf("Failed to handle pending renegotiation: %v", err)
			}
		}()
	}
}

func (m *Manager) handleNegotiationNeeded() error {

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.PeerConnection.SignalingState() != webrtc.SignalingStateStable {
		log.Println("Skipping negotiation, signaling state not stable")
		return fmt.Errorf("signaling state not stable -- skipping negotiation")
	}

	offer, err := m.PeerConnection.CreateOffer(nil)
	if err != nil {
		log.Printf("Failed to create offer: %v", err)
		return err
	}

	if err := m.PeerConnection.SetLocalDescription(offer); err != nil {
		log.Printf("Failed to set local description: %v", err)
		return err
	}

	if err := m.sendSignalingMessage("offer", m.PeerConnection.LocalDescription()); err != nil {
		log.Printf("Failed to send offer: %v", err)
		return err
	}
	return nil
}

func (m *Manager) handleICECandidate(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		log.Println("Received nil ICE candidate - this is normal at gathering completion")
		return
	}

	log.Printf("New ICE candidate: %s", candidate.String())

	candidateJSON, err := json.Marshal(&Candidate{
		Candidate: candidate,
		Target:    0, // Make sure this matches what your SFU expects
	})
	if err != nil {
		log.Printf("Failed to marshal ICE candidate: %v", err)
		return
	}

	// Add retry logic for sending candidates
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		err := m.sendMessage(&jsonrpc2.Request{
			Method: "trickle",
			Params: (*json.RawMessage)(&candidateJSON),
		})
		if err == nil {
			log.Printf("Successfully sent ICE candidate: %s", candidate.String())
			return
		}
		log.Printf("Failed to send ICE candidate (attempt %d/%d): %v", i+1, maxRetries, err)
		time.Sleep(time.Second * time.Duration(i+1))
	}
}

// SETUP SIGNALING WITH THE SELECTIVE FORWARDING UNIT
func (m *Manager) SetupSignaling() error {
	log.Println("Starting signaling setup...")

	// Create join message with proper room ID
	joinMsg := &SendOffer{
		SID:   "test room", // This should come from config
		Offer: m.PeerConnection.LocalDescription(),
	}

	// Send join message
	params, err := json.Marshal(joinMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal join message: %v", err)
	}

	// Send via JSON-RPC
	rpcMsg := &jsonrpc2.Request{
		Method: "join",
		Params: (*json.RawMessage)(&params),
		ID:     jsonrpc2.ID{Num: uint64(uuid.New().ID())},
	}

	if err := m.sendMessage(rpcMsg); err != nil {
		return fmt.Errorf("failed to send join message: %v", err)
	}

	log.Println("Join message sent, waiting for response...")
	return nil
}

// ---- Unified method for sending signaling messages
func (m *Manager) sendSignalingMessage(method string, sd *webrtc.SessionDescription) error {
	var message interface{}

	switch method {
	case "join", "offer":
		message = &SendOffer{
			SID:   "test room",
			Offer: sd,
		}
	case "answer":
		message = &SendAnswer{
			SID:    "test room",
			Answer: sd,
		}
	default:
		return fmt.Errorf("unknown signaling method: %s", method)
	}

	params, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	rpcMsg := &jsonrpc2.Request{
		Method: method,
		Params: (*json.RawMessage)(&params),
		ID:     jsonrpc2.ID{Num: uint64(uuid.New().ID())},
	}

	return m.sendMessage(rpcMsg)
}

// ---- INCOMING MESSAGE HANDLING
// Simplified incoming message handler
func (m *Manager) HandleIncomingMessage(message []byte) error {
	log.Printf("Received message from SFU: %s", string(message))

	var response Response
	if err := json.Unmarshal(message, &response); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	switch response.Method {
	case "offer":
		// SFU is sending us an offer (usually after join)
		return m.handleIncomingOffer(&response)
	case "answer":
		// SFU is responding to our offer
		return m.handleIncomingAnswer(&response)
	case "trickle":
		// SFU is sending ICE candidates
		return m.handleTrickle(message)
	case "error":
		log.Printf("Received error from SFU: %v", response)
		return fmt.Errorf("SFU error: %v", response)
	default:
		return fmt.Errorf("unknown message method: %s", response.Method)
	}
}

func (m *Manager) handleIncomingAnswer(response *Response) error {
	if response.Result == nil {
		return fmt.Errorf("received nil answer")
	}

	// Validate SDP before processing
	if err := validateSDP(response.Result); err != nil {
		return fmt.Errorf("invalid answer SDP: %v", err)
	}

	return m.PeerConnection.SetRemoteDescription(*response.Result)
}

// Simplified offer handling
func (m *Manager) handleIncomingOffer(response *Response) error {
	if err := m.PeerConnection.SetRemoteDescription(*response.Params); err != nil {
		return fmt.Errorf("failed to set remote description: %v", err)
	}

	answer, err := m.PeerConnection.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("failed to create answer: %v", err)
	}

	if err := m.PeerConnection.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("failed to set local description: %v", err)
	}

	return m.sendSignalingMessage("answer", m.PeerConnection.LocalDescription())
}

func (m *Manager) handleTrickle(message []byte) error {
	var trickleResponse TrickleResponse
	if err := json.Unmarshal(message, &trickleResponse); err != nil {
		return fmt.Errorf("failed to unmarshal trickle: %v", err)
	}

	if trickleResponse.Params.Candidate == nil {
		return fmt.Errorf("received nil ICE candidate")
	}

	log.Printf("Adding ICE candidate: %v", trickleResponse.Params.Candidate)

	if err := m.PeerConnection.AddICECandidate(*trickleResponse.Params.Candidate); err != nil {
		return fmt.Errorf("failed to add ICE candidate: %v", err)
	} else {
		fmt.Println("ICE Candidate added successfully")
	}
	return nil
}

// END incoming message handling

// TRACK AND STREAM LOGIC
func (m *Manager) handleTrack(track *webrtc.TrackRemote) {
	log.Printf("Received track: ID=%s, Kind=%s, SSRC=%d", track.ID(), track.Kind(), track.SSRC())

	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		m.handleVideoStream(track)
	case webrtc.RTPCodecTypeAudio:
		m.handleAudioStream(track)
	default:
		log.Printf("Received unknown track type: %s", track.Kind())
	}

	// Start stats monitoring
	go m.monitorTrackStats()
}

// Private helper methods for track handling
func (m *Manager) handleVideoStream(track *webrtc.TrackRemote) {
	if track == nil {
		log.Println("Received nil video track")
		return
	}

	log.Printf("Handling video track: ID=%s, SSRC=%d", track.ID(), track.SSRC())

	// Create a local track to write to
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		track.Codec().RTPCodecCapability,
		"video",
		"webcam-video",
	)
	if err != nil {
		log.Printf("Failed to create local track: %v", err)
		return
	}

	// Add the track to the PeerConnection
	rtpSender, err := m.PeerConnection.AddTrack(localTrack)
	if err != nil {
		log.Printf("Failed to add local track: %v", err)
		return
	}

	// Handle RTCP packets
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				return
			}
		}
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Recovered from panic in video handler: %v", r)
			}
		}()

		for {
			select {
			case <-m.ctx.Done():
				return
			default:
				rtp, _, err := track.ReadRTP()
				if err != nil {
					if err == io.EOF {
						log.Println("Video track ended")
						return
					}
					log.Printf("Error reading video RTP: %v", err)
					continue
				}

				// Write to the local track
				if err := localTrack.WriteRTP(rtp); err != nil {
					log.Printf("Error writing to local track: %v", err)
				}
			}
		}
	}()
}

func (m *Manager) handleAudioStream(track *webrtc.TrackRemote) {
	if track == nil {
		log.Println("Received nil audio track")
		return
	}

	log.Printf("Handling audio track: ID=%s, SSRC=%d", track.ID(), track.SSRC())

	// Create a local track to write to
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		track.Codec().RTPCodecCapability,
		"audio",
		"webcam-audio",
	)
	if err != nil {
		log.Printf("Failed to create local audio track: %v", err)
		return
	}

	// Add the track to the PeerConnection
	rtpSender, err := m.PeerConnection.AddTrack(localTrack)
	if err != nil {
		log.Printf("Failed to add local audio track: %v", err)
		return
	}

	// Handle RTCP packets
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				return
			}
		}
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Recovered from panic in audio handler: %v", r)
			}
		}()

		for {
			select {
			case <-m.ctx.Done():
				return
			default:
				rtp, _, err := track.ReadRTP()
				if err != nil {
					if err == io.EOF {
						log.Println("Audio track ended")
						return
					}
					log.Printf("Error reading audio RTP: %v", err)
					continue
				}

				if err := localTrack.WriteRTP(rtp); err != nil {
					log.Printf("Error writing to local audio track: %v", err)
				}
			}
		}
	}()
}

func (m *Manager) monitorTrackStats() {
	ticker := time.NewTicker(time.Second * 3)
	defer ticker.Stop()

	var warnings []string
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			stats := m.PeerConnection.GetStats()
			for _, s := range stats {
				if inboundStats, ok := s.(*webrtc.InboundRTPStreamStats); ok {
					// Check for warning conditions
					if inboundStats.PacketsLost > 0 {
						packetLossRate := float32(inboundStats.PacketsLost) / float32(inboundStats.PacketsReceived+uint32(inboundStats.PacketsLost))
						if packetLossRate >= warningPacketLoss {
							warnings = append(warnings, fmt.Sprintf("High packet loss rate: %.2f%%", packetLossRate*100))
						}
					}
					if inboundStats.Jitter > float64(warningRTT.Seconds()) {
						warnings = append(warnings, fmt.Sprintf("High jitter: %v", inboundStats.Jitter))
					}
				}
			}

			// Handle any warnings
			if len(warnings) > 0 {
				m.handleWarnings(warnings)
				warnings = warnings[:0] // Clear the warnings slice
			}
		}
	}
}

func (m *Manager) GenerateStream(codecSelector *mediadevices.CodecSelector) (mediadevices.MediaStream, error) {
	var stream mediadevices.MediaStream
	if codecSelector == nil {
		return stream, fmt.Errorf("codec selector cannot be nil")
	}

	stream, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			// Set device ID as string property
			c.DeviceID = prop.String(m.camera.DeviceID)

			// Set video format constraints
			c.Width = prop.Int(640)      // Safe default width
			c.Height = prop.Int(480)     // Safe default height
			c.FrameRate = prop.Float(30) // Safe default framerate

			// Set format explicitly
			c.FrameFormat = prop.FrameFormat(frame.FormatYUY2)

		},
		Audio: func(c *mediadevices.MediaTrackConstraints) {
			// Set device ID as string property
			c.DeviceID = prop.String(m.microphone.DeviceID)

			// Set audio format constraints with explicit types
			c.SampleRate = prop.Int(48000)
			c.ChannelCount = prop.Int(1)
			c.SampleSize = prop.Int(16)

			// Use boolean constraints with explicit types
			c.IsFloat = prop.BoolExact(false)
			c.IsBigEndian = prop.BoolExact(false)
			c.IsInterleaved = prop.BoolExact(true)

			// Set latency with proper duration type
			c.Latency = prop.Duration(20 * time.Millisecond)
		},
		Codec: codecSelector,
	})

	if err != nil {
		return stream, fmt.Errorf("failed to get user media: %v", err)
	}
	return stream, nil
}

func (m *Manager) GenerateANDSetStream(codecSelector *mediadevices.CodecSelector) error {
	stream, err := m.GenerateStream(codecSelector)
	if err != nil {
		log.Printf("Failed to generate media stream: %v", err)
		return err
	} else {
		m.mu.Lock()
		m.mediaStream = stream
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) setupVideoTrack() (*webrtc.TrackLocalStaticRTP, *webrtc.RTPSender, error) {
	videoCodec := webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeVP8,
		ClockRate:   90000,
		Channels:    0,
		SDPFmtpLine: "",
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

func (m *Manager) handleMediaPackets(track mediadevices.Track, localTrack *webrtc.TrackLocalStaticRTP, ssrc uint32, mtu int, isVideo bool) {
	if track == nil || localTrack == nil {
		log.Printf("Nil track provided to handleMediaPackets")
		return
	}

	mediaType := "audio"
	if isVideo {
		mediaType = "video"
	}

	rtpReader, err := track.NewRTPReader(localTrack.Codec().MimeType, ssrc, mtu)
	if err != nil {
		log.Printf("Failed to create %s RTP reader: %v", mediaType, err)
		return
	}
	defer rtpReader.Close()

	for {
		select {
		case <-m.ctx.Done():
			log.Printf("Stopping %s packet handler due to context cancellation", mediaType)
			return
		default:
			packets, _, err := rtpReader.Read()
			if err != nil {
				if err == io.EOF {
					log.Printf("%s track ended", mediaType)
					return
				}
				log.Printf("Error reading %s RTP packet: %v", mediaType, err)
				continue
			}

			for _, packet := range packets {
				select {
				case <-m.ctx.Done():
					return
				default:
					if err := localTrack.WriteRTP(packet); err != nil {
						log.Printf("Error writing %s RTP packet: %v", mediaType, err)
						// Consider if this error should trigger a reconnection
						if strings.Contains(err.Error(), "closed") {
							return
						}
					}
				}
			}
		}
	}
}

func (m *Manager) SetupMediaTracks(camera, microphone mediadevices.MediaDeviceInfo, codecSelector *mediadevices.CodecSelector) error {
	if m.PeerConnection == nil {
		return fmt.Errorf("peer connection not initialized")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Clean up any existing tracks
	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
	}

	m.camera = camera
	m.microphone = microphone

	err := m.GenerateANDSetStream(codecSelector)
	if err != nil {
		return fmt.Errorf("failed to generate media stream: %v", err)
	}

	// Setup video track
	videoTrack, videoRtpSender, err := m.setupVideoTrack()
	if err != nil {
		return err
	}

	// Setup audio track
	audioTrack, audioRtpSender, err := m.setupAudioTrack()
	if err != nil {
		return err
	}

	const mtu = 1200

	// Handle video packets
	go func() {
		m.mu.RLock()
		videoTracks := m.mediaStream.GetVideoTracks()
		m.mu.RUnlock()
		if len(videoTracks) == 0 {
			log.Println("No video tracks available")
			return
		}

		params := videoRtpSender.GetParameters()
		if len(params.Encodings) > 0 && params.Encodings[0].SSRC > 0 {
			log.Printf("Expected video SSRC: %v", params.Encodings[0].SSRC)
		} else if len(params.Encodings) == 0 || params.Encodings[0].SSRC == 0 {
			log.Println("No valid SSRC found for video")
			return
		}

		ssrc := uint32(params.Encodings[0].SSRC)
		for _, track := range videoTracks {
			go m.handleMediaPackets(track, videoTrack, ssrc, mtu, true)
		}
	}()

	// Handle audio packets
	go func() {
		m.mu.RLock()
		audioTracks := m.mediaStream.GetAudioTracks()
		m.mu.RUnlock()
		if len(audioTracks) == 0 {
			log.Println("No audio tracks available")
			return
		}

		params := audioRtpSender.GetParameters()
		if len(params.Encodings) > 0 && params.Encodings[0].SSRC > 0 {
			log.Printf("Expected audio SSRC: %v", params.Encodings[0].SSRC)
		} else if len(params.Encodings) == 0 || params.Encodings[0].SSRC == 0 {
			log.Println("No valid SSRC found for audio")
			return
		}

		ssrc := uint32(params.Encodings[0].SSRC)
		for _, track := range audioTracks {
			go m.handleMediaPackets(track, audioTrack, ssrc, mtu, false)
		}
	}()

	return nil
}

// Helper method to send websocket messages
func (m *Manager) sendMessage(message interface{}) error {
	reqBodyBytes := new(bytes.Buffer)
	if err := json.NewEncoder(reqBodyBytes).Encode(message); err != nil {
		return fmt.Errorf("failed to encode message: %v", err)
	}

	if err := m.wsConnection.WriteMessage(
		websocket.TextMessage,
		reqBodyBytes.Bytes(),
	); err != nil {
		return fmt.Errorf("failed to write websocket message: %v", err)
	}
	return nil
}

func (m *Manager) handleConnectionFailure() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create offer with ICE restart
	offer, err := m.PeerConnection.CreateOffer(&webrtc.OfferOptions{
		ICERestart: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create restart offer: %v", err)
	}

	// Set local description and gather ICE candidates
	gatherComplete := webrtc.GatheringCompletePromise(m.PeerConnection)
	if err = m.PeerConnection.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("failed to set local description: %v", err)
	}

	// Wait for ICE gathering with timeout
	select {
	case <-gatherComplete:
		log.Println("ICE gathering completed for restart")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("ICE gathering timed out during restart")
	}

	return m.sendSignalingMessage("offer", m.PeerConnection.LocalDescription())
}

func (m *Manager) handleDisconnection() error {
	// Log metrics before attempting reconnection
	m.logRecentMetrics("Disconnection")

	m.mu.Lock()
	defer m.mu.Unlock()

	// Cleanup old resources
	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
		m.mediaStream = nil
	}

	if m.PeerConnection != nil {
		m.PeerConnection.Close()
		m.PeerConnection = nil
	}

	// Wait before reconnecting
	time.Sleep(2 * time.Second)

	// Re-initialize
	codecSelector, err := m.Initialize()
	if err != nil {
		return fmt.Errorf("failed to reinitialize: %v", err)
	}

	// Reconnect media
	if err := m.SetupMediaTracks(m.camera, m.microphone, codecSelector); err != nil {
		return fmt.Errorf("failed to setup media tracks: %v", err)
	}

	return m.SetupSignaling()
}

//----------------------
// SDP VALIDATION and DTLS Configuration

func (e *SDPValidationError) Error() string {
	return fmt.Sprintf("SDP validation error in %s: %s", e.Field, e.Message)
}

const (
	minBitrate = 100000  // 100 kbps
	maxBitrate = 2000000 // 2 Mbps
)

func validateSDP(sd *webrtc.SessionDescription) error {
	if sd == nil {
		return &SDPValidationError{Field: "SessionDescription", Message: "is nil"}
	}

	// Split SDP into lines
	lines := strings.Split(sd.SDP, "\n")

	var (
		hasAudio    bool
		hasVideo    bool
		hasICE      bool
		hasDTLS     bool
		mediaCount  int
		fingerprint string
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
				return &SDPValidationError{Field: "Bitrate", Message: "invalid format"}
			}
			if bitrate < minBitrate || bitrate > maxBitrate {
				return &SDPValidationError{
					Field:   "Bitrate",
					Message: fmt.Sprintf("outside allowed range (%d-%d)", minBitrate, maxBitrate),
				}
			}
		}
	}

	// Validate required components
	if mediaCount == 0 {
		return &SDPValidationError{Field: "Media", Message: "no media sections found"}
	}
	if !hasICE {
		return &SDPValidationError{Field: "ICE", Message: "no ICE credentials found"}
	}
	if !hasDTLS {
		return &SDPValidationError{Field: "DTLS", Message: "no DTLS fingerprint found"}
	}
	if len(fingerprint) == 0 {
		return &SDPValidationError{Field: "Fingerprint", Message: "empty DTLS fingerprint"}
	}

	// Validate media requirements based on your application needs
	if !hasAudio && !hasVideo {
		return &SDPValidationError{Field: "Media", Message: "neither audio nor video tracks found"}
	}

	// Check for RTCP feedback mechanisms
	var (
		hasVideoTWCC bool
		hasAudioTWCC bool
		hasAudioNACK bool
	)

	for _, line := range strings.Split(sd.SDP, "\n") {
		switch {
		case strings.Contains(line, "transport-cc") && strings.Contains(line, "video"):
			hasVideoTWCC = true
		case strings.Contains(line, "transport-cc") && strings.Contains(line, "audio"):
			hasAudioTWCC = true
		case strings.Contains(line, "nack") && strings.Contains(line, "audio"):
			hasAudioNACK = true
		}
	}

	// Log feedback mechanism status
	log.Printf("SDP Feedback mechanisms - Video TWCC: %v, Audio TWCC: %v, Audio NACK: %v",
		hasVideoTWCC, hasAudioTWCC, hasAudioNACK)

	return nil
}

func newDTLSConfig() (*DTLSConfig, error) {
	// Generate certificate
	cert, err := generateCertificate()
	if err != nil {
		return nil, fmt.Errorf("failed to generate certificate: %v", err)
	}

	// Calculate fingerprint
	fingerprint, err := calculateFingerprint(cert)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate fingerprint: %v", err)
	}

	return &DTLSConfig{
		certificate:    cert,
		fingerprint:    fingerprint,
		verifyPeerCert: true,
	}, nil
}

func generateCertificate() (*tls.Certificate, error) {
	certificate, privateKey, err := dtls.GenerateSelfSigned()
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certificate.Raw},
		PrivateKey:  privateKey,
		Leaf:        certificate,
	}

	return tlsCert, nil
}

func calculateFingerprint(cert *tls.Certificate) (string, error) {
	if cert == nil || len(cert.Certificate) == 0 {
		return "", fmt.Errorf("invalid certificate")
	}

	// Calculate SHA-256 fingerprint
	h := sha256.New()
	h.Write(cert.Certificate[0])
	fingerprint := hex.EncodeToString(h.Sum(nil))

	// Format fingerprint with colons
	var formatted strings.Builder
	for i := 0; i < len(fingerprint); i += 2 {
		if i > 0 {
			formatted.WriteString(":")
		}
		formatted.WriteString(fingerprint[i : i+2])
	}

	return fmt.Sprintf("sha-256 %s", formatted.String()), nil
}

func (m *Manager) handleCriticalIssues(issues []string) {
	// Log all issues
	for _, issue := range issues {
		log.Printf("Critical issue: %s", issue)
	}

	// Check connection state and attempt recovery
	switch m.PeerConnection.ConnectionState() {
	case webrtc.PeerConnectionStateFailed:
		log.Println("Attempting ICE restart due to connection failure")
		if err := m.handleConnectionFailure(); err != nil {
			log.Printf("Failed to handle connection failure: %v", err)
		}
	case webrtc.PeerConnectionStateDisconnected:
		log.Println("Connection disconnected, attempting to reconnect")
		if err := m.handleTemporaryDisconnection(); err != nil {
			log.Printf("Failed to handle disconnection: %v", err)
		}
	default:
		// If connection is still alive, adjust media constraints
		m.adjustMediaConstraints(true)
	}
}
func (m *Manager) adjustMediaConstraints(aggressive bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Define constraint values based on aggressive mode
	videoConstraints := struct {
		width     int
		height    int
		frameRate float32
		bitRate   uint
	}{}
	// Set values based on aggressive mode
	if aggressive {
		videoConstraints.width = 320
		videoConstraints.height = 240
		videoConstraints.frameRate = 15
		videoConstraints.bitRate = 150_000
	} else {
		videoConstraints.width = 480
		videoConstraints.height = 360
		videoConstraints.frameRate = 20
		videoConstraints.bitRate = 500_000
	}

	audioConstraints := struct {
		sampleRate   int
		channelCount int
		bitRate      uint
	}{}

	// Set audio constraints based on aggressive mode
	if aggressive {
		audioConstraints.sampleRate = 8000
		audioConstraints.bitRate = 16000
	} else {
		audioConstraints.sampleRate = 16000
		audioConstraints.bitRate = 32000
	}
	audioConstraints.channelCount = 1 // Always mono

	// Create VP8 parameters
	vpxParams, err := vpx.NewVP8Params()
	if err != nil {
		log.Printf("Failed to create VP8 params: %v", err)
		return
	}
	vpxParams.BitRate = int(videoConstraints.bitRate)
	if aggressive {
		vpxParams.KeyFrameInterval = 30
	} else {
		vpxParams.KeyFrameInterval = 15
	}

	vpxParams.RateControlEndUsage = vpx.RateControlVBR

	// Create Opus parameters
	opusParams, err := opus.NewParams()
	if err != nil {
		log.Printf("Failed to create Opus params: %v", err)
		return
	}
	opusParams.BitRate = int(audioConstraints.bitRate)
	opusParams.Latency = opus.Latency20ms

	// Create codec selector
	codecSelector := mediadevices.NewCodecSelector(
		mediadevices.WithVideoEncoders(&vpxParams),
		mediadevices.WithAudioEncoders(&opusParams),
	)

	constraints := mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(m.camera.DeviceID)
			c.FrameFormat = prop.FrameFormat(frame.FormatYUY2)
			c.Width = prop.Int(videoConstraints.width)
			c.Height = prop.Int(videoConstraints.height)
			c.FrameRate = prop.Float(videoConstraints.frameRate)
			c.DiscardFramesOlderThan = 500 * time.Millisecond
		},
		Audio: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(m.microphone.DeviceID)
			c.SampleRate = prop.Int(audioConstraints.sampleRate)
			c.ChannelCount = prop.Int(audioConstraints.channelCount)
			c.Latency = prop.Duration(time.Millisecond * 50)
		},
		Codec: codecSelector,
	}

	// Get new media stream
	stream, err := mediadevices.GetUserMedia(constraints)
	if err != nil {
		log.Printf("Failed to get user media with adjusted constraints: %v", err)
		return
	}

	// Gracefully close old stream
	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
	}

	// Update stream and log changes
	m.mediaStream = stream
	log.Printf("Media constraints adjusted - Aggressive mode: %v", aggressive)
	log.Printf("Video: %dx%d @%dfps, BitRate: %d",
		videoConstraints.width,
		videoConstraints.height,
		int(videoConstraints.frameRate),
		videoConstraints.bitRate)
	log.Printf("Audio: %dHz, Channels: %d, BitRate: %d",
		audioConstraints.sampleRate,
		audioConstraints.channelCount,
		audioConstraints.bitRate)
}

func (m *Manager) handleWarnings(warnings []string) {
	// Log all warnings
	for _, warning := range warnings {
		log.Printf("Warning: %s", warning)
	}

	// Only adjust constraints if connection is stable
	if m.PeerConnection.ConnectionState() == webrtc.PeerConnectionStateConnected {
		m.adjustMediaConstraints(false)
	}
}

func (m *Manager) Shutdown(ctx context.Context) error {
	if m.stunServer != nil {
		if err := m.stunServer.Stop(); err != nil {
			log.Printf("Failed to stop STUN server: %v", err)
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
		close(m.done)
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

func NewStatsCollector(pc *webrtc.PeerConnection) *StatsCollector {
	sc := &StatsCollector{
		pc:      pc,
		metrics: make([]QualityMetrics, 0),
	}
	go sc.CollectStats()
	return sc
}

// CollectStats is the main stats collection loop
func (sc *StatsCollector) CollectStats() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		currentStats := sc.gatherStats()

		sc.mu.Lock()
		sc.lastStats = currentStats
		sc.mu.Unlock()

		if sc.callback != nil {
			sc.callback(currentStats)
		}
	}
}

// gatherStats collects and processes raw WebRTC stats
func (sc *StatsCollector) gatherStats() *ConnectionStats {
	stats := sc.pc.GetStats()
	currentStats := &ConnectionStats{
		Timestamp: time.Now(),
	}

	sc.mu.RLock() // Use StatsCollector's mutex to access lastStats if needed
	lastStats := sc.lastStats
	sc.mu.RUnlock()

	for _, s := range stats {
		switch stat := s.(type) {
		case *webrtc.InboundRTPStreamStats:
			// Process inbound stats
			if stat.Kind == "video" {
				currentStats.FramesReceived = stat.FramesDecoded
				currentStats.VideoWidth = stat.FrameWidth
				currentStats.VideoHeight = stat.FrameHeight

				// Calculate framerate if we have previous stats
				if lastStats != nil && time.Since(lastStats.Timestamp) > 0 {
					frameDiff := currentStats.FramesReceived - lastStats.FramesReceived
					timeDiff := time.Since(lastStats.Timestamp).Seconds()
					if timeDiff > 0 {
						currentStats.FramerateRecv = float64(frameDiff) / timeDiff
					}
				}
			}

			currentStats.PacketsReceived += stat.PacketsReceived
			currentStats.PacketsLost += uint32(stat.PacketsLost)
			currentStats.Jitter = stat.Jitter
			currentStats.BytesReceived += stat.BytesReceived

		case *webrtc.OutboundRTPStreamStats:
			// Process outbound stats
			currentStats.PacketsSent += stat.PacketsSent
			currentStats.BytesSent += stat.BytesSent

			if stat.Kind == "video" {
				// Calculate send framerate if we have previous stats
				if lastStats != nil && time.Since(lastStats.Timestamp) > 0 {
					bytesDiff := currentStats.BytesSent - lastStats.BytesSent
					timeDiff := time.Since(lastStats.Timestamp).Seconds()
					if timeDiff > 0 {
						currentStats.FramerateSent = float64(bytesDiff*8) / timeDiff // bits per second
					}
				}
			}

		case *webrtc.RemoteInboundRTPStreamStats:
			currentStats.RoundTripTime = stat.RoundTripTime
		}
	}

	return currentStats
}
func (m *Manager) monitorConnection(sc *StatsCollector) error {
	if sc == nil {
		return fmt.Errorf("stats collector cannot be nil")
	}

	type connectionState struct {
		consecutiveFailures int
		timeInNewState      time.Time
	}

	state := &connectionState{}
	stateMutex := &sync.Mutex{}

	// Calculate delays for staggered monitoring
	now := time.Now()
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	delay1 := time.Until(nextMinute)
	delay2 := delay1 + 20*time.Second // 20 seconds after first check
	delay3 := delay2 + 20*time.Second // 20 seconds after second check

	// Monitor ICE Connection State
	time.AfterFunc(delay1, func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		go func() {
			for {
				select {
				case <-m.ctx.Done():
					return
				case <-ticker.C:
					stateMutex.Lock()
					m.checkICEConnectionState(&state.consecutiveFailures, &state.timeInNewState)
					stateMutex.Unlock()
				}
			}
		}()
	})

	// Monitor Peer Connection State
	time.AfterFunc(delay2, func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		go func() {
			for {
				select {
				case <-m.ctx.Done():
					return
				case <-ticker.C:
					stateMutex.Lock()
					m.checkPeerConnectionState(&state.consecutiveFailures, &state.timeInNewState)
					stateMutex.Unlock()
				}
			}
		}()
	})

	// Monitor DTLS Transport State
	time.AfterFunc(delay3, func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		go func() {
			for {
				select {
				case <-m.ctx.Done():
					return
				case <-ticker.C:
					m.checkDTLSTransportState()
				}
			}
		}()
	})

	return nil
}

// Separate method for DTLS state checking
func (m *Manager) checkDTLSTransportState() {
	if m.PeerConnection == nil {
		log.Println("Warning: PeerConnection is nil during DTLS check")
		return
	}

	sctp := m.PeerConnection.SCTP()
	if sctp == nil {
		log.Println("Warning: SCTP association not available")
		return
	}

	pcTransport := sctp.Transport()
	if pcTransport == nil {
		log.Println("Warning: PC transport is nil")
		return
	}

	// Get and log ICE parameters
	params, err := pcTransport.GetLocalParameters()
	if err == nil {
		log.Printf("Local ICE parameters: %+v", params)
	} else {
		log.Printf("Failed to get local ICE parameters: %v", err)
	}

	dtlsTransport := sctp.Transport()
	if dtlsTransport == nil {
		log.Println("Warning: DTLS transport is nil")
		return
	}

	// Set up DTLS state change handler
	currentState := dtlsTransport.State()
	log.Printf("Current DTLS Transport State: %s", currentState)

	dtlsTransport.OnStateChange(func(state webrtc.DTLSTransportState) {
		log.Printf("DTLS Transport State changed to: %s", state)

		switch state {
		case webrtc.DTLSTransportStateConnected:
			select {
			case m.dtlsStateChan <- state:
				log.Println("DTLS connection secured successfully")
			default:
				log.Printf("Could not send DTLS state update: channel full or closed")
			}

		case webrtc.DTLSTransportStateFailed:
			log.Printf("DTLS connection failed, attempting restart...")
			// Trigger connection restart
			if err := m.handleConnectionFailure(); err != nil {
				log.Printf("Failed to handle DTLS failure: %v", err)
			}

		case webrtc.DTLSTransportStateClosed:
			log.Printf("DTLS connection closed")
		}
	})
}

func (m *Manager) checkICEConnectionState(consecutiveFailures *int, timeInNewState *time.Time) {
	iceState := m.PeerConnection.ICEConnectionState()
	log.Printf("Checking ICE Connection State: %s", iceState)

	switch iceState {
	case webrtc.ICEConnectionStateNew:
		if timeInNewState.IsZero() {
			*timeInNewState = time.Now()
		} else if time.Since(*timeInNewState) > time.Second*30 {
			log.Printf("ICE stuck in new state for too long, attempting restart")
			m.handleConnectionFailure()
		}

	case webrtc.ICEConnectionStateChecking:
		*timeInNewState = time.Time{} // Reset new state timer

	case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
		*consecutiveFailures = 0
		*timeInNewState = time.Time{}
		log.Printf("ICE Connection stable in %s state", iceState)

	case webrtc.ICEConnectionStateFailed, webrtc.ICEConnectionStateDisconnected:
		*consecutiveFailures++
		if *consecutiveFailures < 3 {
			log.Printf("ICE connection issue (attempt %d/3), attempting recovery", *consecutiveFailures)
			m.handleConnectionFailure()
		} else {
			log.Printf("ICE connection failed after 3 attempts, initiating full restart")
			m.handleDisconnection()
			*consecutiveFailures = 0
		}

	case webrtc.ICEConnectionStateClosed:
		log.Printf("ICE connection closed, cleaning up")
		m.Cleanup()
	}
}

func (m *Manager) checkPeerConnectionState(consecutiveFailures *int, timeInNewState *time.Time) {
	peerState := m.PeerConnection.ConnectionState()
	log.Printf("Checking Peer Connection State: %s", peerState)

	switch peerState {
	case webrtc.PeerConnectionStateNew:
		if timeInNewState.IsZero() {
			*timeInNewState = time.Now()
		} else if time.Since(*timeInNewState) > time.Second*30 {
			log.Printf("Peer connection stuck in new state, checking WebSocket connection")
			if err := m.checkSignalingConnection(); err != nil {
				log.Printf("Signaling connection issue: %v, attempting reconnection", err)
				m.handleDisconnection()
			}
		}

	case webrtc.PeerConnectionStateConnecting:
		*timeInNewState = time.Time{}
		log.Printf("Peer connection is establishing...")

	case webrtc.PeerConnectionStateConnected:
		*consecutiveFailures = 0
		*timeInNewState = time.Time{}
		m.analyzeConnectionQuality(m.stats.lastStats, consecutiveFailures)
		log.Printf("Peer connection stable")

	case webrtc.PeerConnectionStateDisconnected:
		log.Printf("Peer connection disconnected, attempting recovery")
		m.handleTemporaryDisconnection()

	case webrtc.PeerConnectionStateFailed:
		*consecutiveFailures++
		*timeInNewState = time.Time{}
		if *consecutiveFailures < 3 {
			log.Printf("Peer connection failed (attempt %d/3), attempting recovery", *consecutiveFailures)
			m.handleConnectionFailure()
		} else {
			log.Printf("Peer connection failed after 3 attempts, initiating full restart")
			m.handleDisconnection()
			*consecutiveFailures = 0
		}

	case webrtc.PeerConnectionStateClosed:
		log.Printf("Peer connection closed, cleaning up")
		m.Cleanup()
	}
}

func (m *Manager) checkSignalingConnection() error {
	pingMsg := &jsonrpc2.Request{
		Method: "ping",
	}
	return m.sendMessage(pingMsg)
}

func (m *Manager) analyzeConnectionQuality(stats *ConnectionStats, consecutiveFailures *int) {
	if stats == nil {
		return
	}

	// Create current metrics from stats
	currentMetrics := QualityMetrics{
		Timestamp:      stats.Timestamp,
		PacketLossRate: float64(stats.PacketsLost) / float64(stats.PacketsReceived+stats.PacketsLost),
		RTT:            time.Duration(stats.RoundTripTime * float64(time.Second)),
		Framerate:      stats.FramerateRecv,
		Resolution:     fmt.Sprintf("%dx%d", stats.VideoWidth, stats.VideoHeight),
		Bitrate:        uint64(stats.BytesSent * 8),
		JitterBuffer:   stats.Jitter,
	}

	// Get previous metrics if available
	recent := m.metrics.GetRecent(2)
	var previousMetrics QualityMetrics
	if len(recent) > 1 {
		previousMetrics = recent[1]
	}

	var issues []string

	// Check packet loss
	if currentMetrics.PacketLossRate >= criticalPacketLoss {
		issues = append(issues, fmt.Sprintf("Critical packet loss: %.2f%%", currentMetrics.PacketLossRate*100))
		*consecutiveFailures++
	} else if currentMetrics.PacketLossRate >= warningPacketLoss {
		issues = append(issues, fmt.Sprintf("High packet loss: %.2f%%", currentMetrics.PacketLossRate*100))
	}

	// Check RTT
	if currentMetrics.RTT >= criticalRTT {
		issues = append(issues, fmt.Sprintf("Critical RTT: %v", currentMetrics.RTT))
		*consecutiveFailures++
	} else if currentMetrics.RTT >= warningRTT {
		issues = append(issues, fmt.Sprintf("High RTT: %v", currentMetrics.RTT))
	}

	// Check framerate
	if currentMetrics.Framerate > 0 && currentMetrics.Framerate < minAcceptableFramerate {
		issues = append(issues, fmt.Sprintf("Low framerate: %.2f fps", currentMetrics.Framerate))
	}

	// Log issues and take action
	if len(issues) > 0 {
		log.Printf("Connection quality issues detected: %v", issues)

		if *consecutiveFailures >= 3 {
			log.Printf("Multiple consecutive failures detected, attempting recovery...")
			m.handleQualityChange(previousMetrics, currentMetrics)
			*consecutiveFailures = 0
		}
	}

	// Store current metrics
	m.metrics.Add(currentMetrics)
}

// handleQualityChange handled both metrics
func (m *Manager) handleQualityChange(old, new QualityMetrics) {
	log.Printf("Quality change detected:")
	log.Printf("  Framerate: %.2f -> %.2f", old.Framerate, new.Framerate)
	log.Printf("  Packet Loss: %.2f%% -> %.2f%%", old.PacketLossRate*100, new.PacketLossRate*100)
	log.Printf("  RTT: %v -> %v", old.RTT, new.RTT)
	log.Printf("  Bitrate: %d -> %d", old.Bitrate, new.Bitrate)

	// Determine if we need aggressive adaptation
	needsAggressive := new.PacketLossRate > criticalPacketLoss ||
		new.RTT > criticalRTT ||
		new.Framerate < minAcceptableFramerate

	// Adjust media constraints based on severity
	m.adjustMediaConstraints(needsAggressive)

	// If conditions are critical, attempt connection recovery
	if needsAggressive {
		m.handleCriticalIssues([]string{
			fmt.Sprintf("Critical packet loss: %.2f%%", new.PacketLossRate*100),
			fmt.Sprintf("Critical RTT: %v", new.RTT),
		})
	}
}

func (m *Manager) handleTemporaryDisconnection() error {
	// Wait a short period before attempting reconnection
	time.Sleep(2 * time.Second)

	// Check if we're still disconnected
	if m.PeerConnection.ConnectionState() == webrtc.PeerConnectionStateDisconnected {
		log.Println("Connection still disconnected, attempting to restore...")

		// Try to restore ICE connection
		offer, err := m.PeerConnection.CreateOffer(&webrtc.OfferOptions{
			ICERestart: true,
		})
		if err != nil {
			return fmt.Errorf("failed to create offer for reconnection: %v", err)
		}

		if err := m.PeerConnection.SetLocalDescription(offer); err != nil {
			return fmt.Errorf("failed to set local description for reconnection: %v", err)
		}

		return m.sendSignalingMessage("offer", m.PeerConnection.LocalDescription())
	}

	return nil
}

// Add method to get the most recent stats
func (sc *StatsCollector) GetLastStats() *ConnectionStats {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.lastStats == nil {
		return nil
	}

	// Return a copy to prevent concurrent modification
	statsCopy := *sc.lastStats
	return &statsCopy
}

// Add method to log recent metrics
func (m *Manager) logRecentMetrics(event string) {
	metrics := m.metrics.GetAll()

	if len(metrics) == 0 {
		log.Printf("%s: No metrics available", event)
		return
	}

	log.Printf("=== %s: Last %v of metrics ===", event, metricsHistoryDuration)
	for i, metric := range metrics {
		log.Printf("Metric %d/%d - Time: %s", i+1, len(metrics), metric.Timestamp.Format(time.RFC3339))
		log.Printf("  Packet Loss Rate: %.2f%%", metric.PacketLossRate*100)
		log.Printf("  RTT: %v", metric.RTT)
		log.Printf("  Framerate: %.2f", metric.Framerate)
		log.Printf("  Resolution: %s", metric.Resolution)
		log.Printf("  Bitrate: %d bps", metric.Bitrate)
	}
	log.Printf("=== End of metrics dump ===")
}

// Manager's metrics collection focuses on analysis and monitoring
func (m *Manager) StartMetricsCollection(sc *StatsCollector) {
	go func() {
		metricsTicker := time.NewTicker(metricsCollectionInterval)
		defer metricsTicker.Stop()

		for {
			select {
			case <-m.ctx.Done():
				return

			case <-metricsTicker.C:
				if m.PeerConnection != nil {
					// Get the latest stats from the StatsCollector
					currentStats := sc.lastStats
					if currentStats == nil {
						continue
					}

					// Create new metrics based on the stats
					newMetrics := QualityMetrics{
						Timestamp:      currentStats.Timestamp,
						PacketLossRate: calculatePacketLossRate(currentStats),
						RTT:            time.Duration(currentStats.RoundTripTime * float64(time.Second)),
						Framerate:      currentStats.FramerateRecv,
						Resolution:     fmt.Sprintf("%dx%d", currentStats.VideoWidth, currentStats.VideoHeight),
						Bitrate:        calculateBitrate(currentStats),
						JitterBuffer:   currentStats.Jitter,
					}

					// Store metrics
					m.metrics.Add(newMetrics)

					// Check for significant changes and handle quality issues

					recent := m.metrics.GetRecent(2)
					if len(recent) > 1 {
						if m.isSignificantChange(recent[1], recent[0]) {
							m.handleQualityChange(recent[1], recent[0])
						}
					}
				}
			}
		}
	}()
}

// Helper functions for the Manager
func calculatePacketLossRate(stats *ConnectionStats) float64 {
	total := stats.PacketsReceived + stats.PacketsLost
	if total == 0 {
		return 0
	}
	return float64(stats.PacketsLost) / float64(total)
}

func calculateBitrate(stats *ConnectionStats) uint64 {
	return stats.BytesSent * 8 // Convert bytes to bits
}

// Helper method to determine if there's a significant change in metrics
func (m *Manager) isSignificantChange(previous, current QualityMetrics) bool {
	const (
		framerateDiffThreshold  = 5.0  // fps
		packetLossRateThreshold = 0.05 // 5%
		rttDiffThreshold        = 100 * time.Millisecond
		bitrateDiffThreshold    = 0.20 // 20% change
	)

	// Check framerate change
	if math.Abs(current.Framerate-previous.Framerate) > framerateDiffThreshold {
		return true
	}

	// Check packet loss rate change
	if math.Abs(current.PacketLossRate-previous.PacketLossRate) > packetLossRateThreshold {
		return true
	}

	// Check RTT change
	if current.RTT-previous.RTT > rttDiffThreshold {
		return true
	}

	// Check bitrate change (as a percentage)
	if previous.Bitrate > 0 {
		bitrateChange := math.Abs(float64(current.Bitrate-previous.Bitrate)) / float64(previous.Bitrate)
		if bitrateChange > bitrateDiffThreshold {
			return true
		}
	}

	return false
}

func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Log metrics before cleanup
	m.logRecentMetrics("Final Metrics Before Cleanup")

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

// these helper methods to Manager are mostly for the testDevices method of selectDervices
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

func (m *Manager) monitorSTUNHealth() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			for _, server := range m.pcConfiguration.ICEServers {
				for _, url := range server.URLs {
					if strings.HasPrefix(url, "stun:") {
						addr := strings.TrimPrefix(url, "stun:")
						conn, err := net.Dial("udp", addr)
						if err != nil {
							log.Printf("STUN server %s is unreachable: %v", addr, err)
							continue
						}
						conn.Close()

						// Test STUN binding request
						c, err := stun.Dial("udp", addr)
						if err != nil {
							log.Printf("Failed to connect to STUN server %s: %v", addr, err)
							continue
						}

						message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
						if err := c.Do(message, func(res stun.Event) {
							if res.Error != nil {
								log.Printf("Failed STUN binding request to %s: %v", addr, res.Error)
								return
							}
							var xorAddr stun.XORMappedAddress
							if err := xorAddr.GetFrom(res.Message); err != nil {
								log.Printf("Failed to get address from STUN response: %v", err)
								return
							}
							log.Printf("STUN server %s responded with mapped address: %s", addr, xorAddr.String())
						}); err != nil {
							log.Printf("STUN binding request failed: %v", err)
						}
						c.Close()
					}
				}
			}
		}
	}
}
