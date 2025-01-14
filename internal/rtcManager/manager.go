package rtcManager

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"

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
	"github.com/pion/mediadevices/pkg/frame"
	"github.com/pion/mediadevices/pkg/prop"

	"github.com/pion/rtp"
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
	config                  *config.Config // from config package
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
	connectionStateHandlers map[int64]func(webrtc.PeerConnectionState)
	handlersLock            sync.RWMutex
	nextHandlerID           int64
	connectionAttempts      int
	isNegotiating           atomic.Bool
	needsRenegotiation      atomic.Bool
	pendingOperations       []func() error
	lastCodecSelector       *mediadevices.CodecSelector
	turnServer              *TURNServer
	rpcConn                 *jsonrpc2.Conn
	handler                 *rtcHandler
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
		var candidate webrtc.ICECandidateInit
		if err := json.Unmarshal(*req.Params, &candidate); err != nil {
			log.Printf("Failed to parse ICE candidate: %v", err)
			return
		}
		h.manager.PeerConnection.AddICECandidate(candidate)
	}
}

//================

// Initialize both TURN server and RPC connection
func NewManager(myconfig *config.Config, wsConn *websocket.Conn, recorder *video.Recorder) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize TURN server first
	turnServer := CreateTURNServer(ctx)

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
	}

	// Create RPC handler and connection
	m.handler = &rtcHandler{manager: m}
	stream := jsonrpc2.NewBufferedStream(wsConn.UnderlyingConn(), jsonrpc2.VSCodeObjectCodec{})
	m.rpcConn = jsonrpc2.NewConn(ctx, stream, m.handler)

	return m, nil
}

func (m *Manager) Initialize() (*mediadevices.CodecSelector, error) {
	// Start TURN server
	if err := m.turnServer.Start(); err != nil {
		return nil, fmt.Errorf("failed to start TURN server: %v", err)
	}

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
			m.handleConnectionFailure()
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
						} else {
							log.Println("No SCTP transport available because no negotiation has occurred")
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

	m.mu.Lock()
	defer m.mu.Unlock()

	offer, err := m.PeerConnection.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("[handleNegotiationNeeded] failed to create offer: %v", err)
	}

	if err := m.PeerConnection.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("[handleNegotiationNeeded] failed to set local description: %v", err)
	}

	log.Println("[handleNegotiationNeeded] Negotiation offer created and local description set")
	return m.rpcConn.Call(m.ctx, "offer", offer, nil)
}

func (m *Manager) SendICECandidate(candidate *webrtc.ICECandidate) error {
	return m.rpcConn.Notify(m.ctx, "trickle", candidate)
}

func (m *Manager) SetupSignaling() error {
	params := struct {
		SID   string                     `json:"sid"`
		Offer *webrtc.SessionDescription `json:"offer"`
	}{
		SID:   "defaultroom",
		Offer: m.PeerConnection.LocalDescription(),
	}

	return m.rpcConn.Call(m.ctx, "join", params, nil)
}

func (m *Manager) handleOffer(ctx context.Context, offer *webrtc.SessionDescription) error {
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

func (m *Manager) handleMediaPackets(srcTrack mediadevices.Track, localTrack *webrtc.TrackLocalStaticRTP, ssrc uint32, mtu int, isVideo bool) {
	const maxBufferSize = 25 // Maximum number of packets to buffer
	pktBuffer := make(chan []*rtp.Packet, maxBufferSize)
	// We need the "codec name" for calling NewRTPReader, which generally is safe to assume is the second part of the MIME type
	mimeParts := strings.SplitN(localTrack.Codec().MimeType, "/", 2)
	if len(mimeParts) != 2 {
		// Log or handle error: invalid MIME type format
		return
	}
	codec := mimeParts[1]
	log.Printf("Codec name is %s", codec)

	rtpReader, err := srcTrack.NewRTPReader(codec, ssrc, mtu)
	if err != nil {
		log.Panicf("failed to create RTP reader: %v", err)
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
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				log.Printf("[readRTCP] %s RTCP read error: %v", mediaKind, rtcpErr)
				return
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

	m.mu.Lock()
	// Clean up old m.mediaStream
	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
	}
	m.camera = camera
	m.microphone = microphone
	m.mu.Unlock()

	// Generate new media stream
	if err := m.GenerateANDSetStream(codecSelector); err != nil {
		return fmt.Errorf("failed to generate media stream: %v", err)
	}

	// Setup local static video track
	videoTrack, videoSender, err := m.setupVideoTrack()
	if err != nil {
		return fmt.Errorf("setupVideoTrack failed: %v", err)
	}

	// Setup local static audio track
	audioTrack, audioSender, err := m.setupAudioTrack()
	if err != nil {
		return fmt.Errorf("setupAudioTrack failed: %v", err)
	}

	// Attach the newly captured media to those local tracks
	go m.attachMediaKind(videoTrack, videoSender, true /* isVideo */)
	go m.attachMediaKind(audioTrack, audioSender, false /* isAudio */)

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
		go m.handleMediaPackets(track, localTrack, ssrc, mtu, isVideo)
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
			if _, _, err := rtpSender.Read(rtcpBuf); err != nil {
				log.Printf("[handleRTCP] %s RTCP read error: %v", mediaKind, err)
				return
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

// handleConnectionFailure attempts to recover from a connection failure using an ICE restart.
func (m *Manager) handleConnectionFailure() error {
	m.mu.Lock()
	defer m.mu.Unlock()

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

	// Wait for ICE gathering to complete (with timeout).
	gatherComplete := webrtc.GatheringCompletePromise(m.PeerConnection)
	select {
	case <-gatherComplete:
		log.Println("ICE gathering completed for restart")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("ICE gathering timed out during restart")
	}

	// Use jsonrpc2 to send the offer; note that we assume the remote side will handle it.
	if m.rpcConn == nil {
		return fmt.Errorf("rpcConn is not initialized")
	}

	// Instead of using sendSignalingMessage, we directly call Notify on rpcConn.
	// We marshal the local description into JSON.
	if err := m.rpcConn.Notify(m.ctx, "offer", m.PeerConnection.LocalDescription()); err != nil {
		return fmt.Errorf("failed to notify remote peer with restart offer: %v", err)
	}

	fmt.Println("ICE restart offer sent successfully")

	return nil
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
	// For example, assume m.Initialize() sets up a new PeerConnection and returns a codecSelector.
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
		if err := m.handleDisconnection(); err != nil {
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

func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

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
