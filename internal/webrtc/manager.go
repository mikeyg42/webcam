package webrtc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/dtls"

	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/opus"
	"github.com/pion/mediadevices/pkg/codec/vpx"

	"github.com/pion/mediadevices/pkg/frame"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v3"
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

// Manager handles WebRTC connection and signaling
type Manager struct {
	config          *config.Config
	peerConnection  *webrtc.PeerConnection
	connectionID    uint64
	wsConnection    *websocket.Conn
	mediaStream     mediadevices.MediaStream
	camera          mediadevices.MediaDeviceInfo
	microphone      mediadevices.MediaDeviceInfo
	recorder        *video.Recorder
	pcConfiguration webrtc.Configuration
	mu              *sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	dtlsFingerprint string
	certificates    []webrtc.Certificate
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
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
		
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		config:          myconfig,
		wsConnection:    wsConn,
		camera:          mediadevices.MediaDeviceInfo{},
		microphone:      mediadevices.MediaDeviceInfo{},
		recorder:        recorder,
		pcConfiguration: pcConfig,
		mu:              &sync.RWMutex{},
		ctx:             ctx,
		cancel:          cancel,
		done:            make(chan struct{}),
	}, nil
}

func (m *Manager) Initialize() (*mediadevices.CodecSelector, error) {

	// Generate certificates for DTLS
	config := &dtls.Config{
		Certificate:         certificate,
		PrivateKey:		  	  privKey,
		InsecureSkipVerify:   false,
		ExtendedMasterSecret: dtls.RequireExtendedMasterSecret,
	}

	// Update peer connection configuration with DTLS settings
	m.pcConfiguration.Certificates = []webrtc.Certificate{*certificate}
	m.pcConfiguration.DTLSTransport = webrtc.DTLSTransportParameters{
		Role:         webrtc.DTLSRoleAuto,
		Fingerprints: []string{"sha-256"},
	}
	// Create MediaEngine
	mediaEngine := webrtc.MediaEngine{}
	// Register default codecs first
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("failed to register default codecs: %v", err)
	}

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

	log.Printf("Using video constraints: BitRate=%d, KeyFrameInterval=%d, Deadline=%d, RateControlMode = VBR\n", vpxParams.BitRate, vpxParams.KeyFrameInterval, vpxParams.Deadline)
	log.Printf("Using audio constraints: BitRate=%d, Latency=%d\n", opusParams.BitRate, opusParams.Latency)

	// Create and store codec selector
	codecSelector := mediadevices.NewCodecSelector(
		mediadevices.WithVideoEncoders(&vpxParams),
		mediadevices.WithAudioEncoders(&opusParams),
	)
	log.Printf("Codec Selector Configured: %v", codecSelector)

	// Register codecs with the MediaEngine
	codecSelector.Populate(&mediaEngine)

	// Create API with MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(&mediaEngine))

	// Create peer connection
	peerConnection, err := api.NewPeerConnection(m.pcConfiguration)
	if err != nil || peerConnection == nil {
		return nil, fmt.Errorf("failed to create peer connection: %v", err)
	}
	// Store peer connection in Manager
	m.peerConnection = peerConnection

	// Set up ICE connection state change handler
	m.peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state changed to: %v", state)
	})

	// Set up ICE candidate handler
	m.peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			log.Printf("Gathered ICE candidate: %v", candidate.ToJSON())
			m.handleICECandidate(candidate)
		}
	})

	// Set up track handler
	m.peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Received track: ID=%s, Kind=%s, SSRC=%d", track.ID(), track.Kind(), track.SSRC())
		if m.recorder != nil {
			if err := m.recorder.HandleTrack(track); err != nil {
				log.Printf("Error handling track: %v", err)
			}
		}
	})
	m.peerConnection.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
		log.Printf("ICE Gathering State changed to: %s", state.String())
	})

	// Add renegotiation handling
	m.peerConnection.OnNegotiationNeeded(func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		log.Println("Negotiation needed, creating new offer")

		// Create a new offer
		offer, err := m.peerConnection.CreateOffer(nil)
		if err != nil {
			log.Printf("Failed to create offer during renegotiation: %v", err)
			return
		}

		// Set local description
		err = m.peerConnection.SetLocalDescription(offer)
		if err != nil {
			log.Printf("Failed to set local description during renegotiation: %v", err)
			return
		}

		// Send the offer through signaling
		offerJSON, err := json.Marshal(&SendOffer{
			Offer: m.peerConnection.LocalDescription(),
			SID:   "test room",
		})
		if err != nil {
			log.Printf("Failed to marshal offer during renegotiation: %v", err)
			return
		}

		params := (*json.RawMessage)(&offerJSON)
		connectionUUID := uuid.New()
		m.connectionID = uint64(connectionUUID.ID())

		offerMessage := &jsonrpc2.Request{
			Method: "offer",
			Params: params,
			ID: jsonrpc2.ID{
				IsString: false,
				Str:      "",
				Num:      m.connectionID,
			},
		}

		// Send the offer through websocket
		if err := m.sendMessage(offerMessage); err != nil {
			log.Printf("Failed to send offer during renegotiation: %v", err)
			return
		}

		log.Println("Renegotiation offer sent successfully")
	})

	// Add state change handlers for debugging renegotiation
	m.peerConnection.OnSignalingStateChange(func(state webrtc.SignalingState) {
		log.Printf("Signaling state changed to: %s", state.String())
	})

	m.peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Connection state changed to: %s", state.String())

		switch state {
		case webrtc.PeerConnectionStateFailed:
			// Handle connection failure
			if err := m.handleConnectionFailure(); err != nil {
				log.Printf("Error handling connection failure: %v", err)
			}
		case webrtc.PeerConnectionStateDisconnected:
			// Handle disconnection
			if err := m.handleDisconnection(); err != nil {
				log.Printf("Error handling disconnection: %v", err)
			}
		}
	})

	m.peerConnection.OnDTLSTransportSetup(func(dtlsTransport *webrtc.DTLSTransport) {
		fingerprint, err := dtlsTransport.GetRemoteParameters()
		if err != nil {
			log.Printf("Failed to get remote DTLS parameters: %v", err)
			return
		}
		m.dtlsFingerprint = fingerprint.Fingerprints[0]
		log.Printf("DTLS fingerprint verified: %s", m.dtlsFingerprint)
	})

	return codecSelector, nil
}

func (m *Manager) SetupSignaling() error {
	// Create and set local description
	offer, err := m.peerConnection.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("failed to create offer: %v", err)
	}

	// Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(m.peerConnection)

	// Sets the LocalDescription, and starts our UDP listeners
	err = m.peerConnection.SetLocalDescription(offer)
	if err != nil {
		return fmt.Errorf("failed to set local description: %v", err)
	}

	// Setup ICE candidate handling
	m.peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			m.handleICECandidate(candidate)
		}
	})

	log.Println("Waiting for ICE gathering to complete...")
	<-gatherComplete
	log.Println("ICE gathering completed")

	// Send the offer to ION-SFU
	return m.sendOffer()
}

func (m *Manager) GenerateStream(codecSelector *mediadevices.CodecSelector) error {
	if codecSelector == nil {
		return fmt.Errorf("codec selector cannot be nil")
	}

	stream, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(m.camera.DeviceID)

			// Video format and basic parameters
			c.FrameFormat = prop.FrameFormat(frame.FormatYUY2)
			c.Width = prop.Int(320) // Start with lower resolution
			c.Height = prop.Int(240)
			c.FrameRate = prop.Float(20)

			// Frame handling
			c.DiscardFramesOlderThan = 500 * time.Millisecond // Drop frames older than 500ms
		},

		Audio: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(m.microphone.DeviceID)

			// Audio quality parameters
			c.SampleRate = prop.Int(48000)
			c.SampleSize = prop.Int(16)  // 16-bit audio
			c.ChannelCount = prop.Int(1) // Mono

			// Audio format settings
			c.IsFloat = prop.BoolExact(false)      // Use integer samples
			c.IsBigEndian = prop.BoolExact(false)  // Use little-endian
			c.IsInterleaved = prop.BoolExact(true) // Use interleaved format

			// Latency handling
			c.Latency = prop.Duration(time.Millisecond * 50)
		},
		Codec: codecSelector,
	})

	if err != nil {
		return fmt.Errorf("failed to get user media: %v", err)
	}
	m.mu.Lock()
	m.mediaStream = stream
	m.mu.Unlock()

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

	videoRtpSender, err := m.peerConnection.AddTrack(videoTrack)
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

	audioRtpSender, err := m.peerConnection.AddTrack(audioTrack)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add audio track: %v", err)
	}

	return audioTrack, audioRtpSender, nil
}

func (m *Manager) handleMediaPackets(track mediadevices.Track, localTrack *webrtc.TrackLocalStaticRTP, ssrc uint32, mtu int, isVideo bool) {
	select {
	case <-m.ctx.Done():
		return
	default:
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
				return
			default:
				packets, _, err := rtpReader.Read()
				if err != nil {
					log.Printf("Error reading %s RTP packet: %v", mediaType, err)
					return
				}

				for _, packet := range packets {
					if err := localTrack.WriteRTP(packet); err != nil {
						log.Printf("Error writing %s RTP packet: %v", mediaType, err)
						return
					}
				}
			}
		}
	}
}

func (m *Manager) SetupMediaTracks(camera, microphone mediadevices.MediaDeviceInfo, codecSelector *mediadevices.CodecSelector) error {
	if m.peerConnection == nil {
		return fmt.Errorf("peer connection not initialized")
	}

	m.camera = camera
	m.microphone = microphone

	if err := m.GenerateStream(codecSelector); err != nil {
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

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cancel() // Stop all goroutines

	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
	}

	if m.peerConnection != nil {
		return m.peerConnection.Close()
	}

	close(m.done)
	return nil
}

func (m *Manager) handleICECandidate(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		return
	}

	candidateJSON, err := json.Marshal(&Candidate{
		Candidate: candidate,
		Target:    0,
	})
	if err != nil {
		log.Printf("Failed to marshal ICE candidate: %v", err)
		return
	}

	params := (*json.RawMessage)(&candidateJSON)
	message := &jsonrpc2.Request{
		Method: "trickle",
		Params: params,
	}

	if err := m.sendMessage(message); err != nil {
		log.Printf("Failed to send ICE candidate: %v", err)
	}
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

func (m *Manager) sendOffer() error {
	offerJSON, err := json.Marshal(&SendOffer{
		Offer: m.peerConnection.LocalDescription(),
		SID:   "test room", // Consider making this configurable
	})
	if err != nil {
		return fmt.Errorf("failed to marshal offer: %v", err)
	}

	params := (*json.RawMessage)(&offerJSON)
	connectionUUID := uuid.New()
	m.connectionID = uint64(connectionUUID.ID())

	offerMessage := &jsonrpc2.Request{
		Method: "join",
		Params: params,
		ID: jsonrpc2.ID{
			IsString: false,
			Str:      "",
			Num:      m.connectionID,
		},
	}

	if err := m.sendMessage(offerMessage); err != nil {
		return fmt.Errorf("failed to send offer: %v", err)
	}
	return nil
}

func (m *Manager) HandleIncomingMessage(message []byte) error {
	var response Response
	if err := json.Unmarshal(message, &response); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	log.Printf("Received message with method: %s", response.Method)

	switch {
	case response.Id == m.connectionID:
		return m.handleConnectionResponse(&response)
	case response.Id != 0 && response.Method == "offer":
		return m.handleOffer(&response)
	case response.Method == "trickle":
		return m.handleTrickle(message)
	}
	return nil
}

func (m *Manager) handleConnectionResponse(response *Response) error {
	if response.Result == nil {
		return fmt.Errorf("received nil result in connection response")
	}
	log.Printf("Remote SDP: %v", response.Result)
	if err := m.peerConnection.SetRemoteDescription(*response.Result); err != nil {
		log.Printf("Remote description error: %v", err)
		return fmt.Errorf("failed to set remote description: %v", err)
	}
	return nil
}

func (m *Manager) handleOffer(response *Response) error {
	if response.Params == nil {
		return fmt.Errorf("received nil params in offer")
	}

	// Validate SDP before processing
	if err := validateSDP(response.Params); err != nil {
		return fmt.Errorf("invalid offer SDP: %v", err)
	}

	if err := m.peerConnection.SetRemoteDescription(*response.Params); err != nil {
		return fmt.Errorf("failed to set remote description from offer: %v", err)
	}

	answer, err := m.peerConnection.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("failed to create answer: %v", err)
	}

	if err := m.peerConnection.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("failed to set local description: %v", err)
	}

	return m.sendAnswer()
}

func (m *Manager) sendAnswer() error {
	connectionUUID := uuid.New()
	m.connectionID = uint64(connectionUUID.ID())

	answerJSON, err := json.Marshal(&SendAnswer{
		Answer: m.peerConnection.LocalDescription(),
		SID:    "test room",
	})
	if err != nil {
		return fmt.Errorf("failed to marshal answer: %v", err)
	}

	params := (*json.RawMessage)(&answerJSON)
	answerMessage := &jsonrpc2.Request{
		Method: "answer",
		Params: params,
		ID: jsonrpc2.ID{
			IsString: false,
			Str:      "",
			Num:      m.connectionID,
		},
	}

	if err := m.sendMessage(answerMessage); err != nil {
		return fmt.Errorf("failed to send answer: %v", err)
	}
	return nil
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

	if err := m.peerConnection.AddICECandidate(*trickleResponse.Params.Candidate); err != nil {
		return fmt.Errorf("failed to add ICE candidate: %v", err)
	}
	return nil
}

// Add these helper methods for connection state handling
func (m *Manager) handleConnectionFailure() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Attempt to restart ICE
	if err := m.peerConnection.RestartICE(); err != nil {
		return fmt.Errorf("failed to restart ICE: %v", err)
	}

	// Create a new offer after ICE restart
	offer, err := m.peerConnection.CreateOffer(&webrtc.OfferOptions{
		ICERestart: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create offer after ICE restart: %v", err)
	}

	if err := m.peerConnection.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("failed to set local description after ICE restart: %v", err)
	}

	// Send the new offer through signaling
	return m.sendOffer()
}

func (m *Manager) handleDisconnection() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Wait for a short period before attempting reconnection
	time.Sleep(2 * time.Second)

	// Re-initialize the application
	if err := app.Initialize(); err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
		return err
	}

	// Restart the main processing loop
	if err := app.startProcessing(); err != nil {
		log.Fatalf("Error during processing: %v", err)
		return err
	}
	return nil
}

// SDP VALIDATION and DTLS Configuration
type SDPValidationError struct {
	Field   string
	Message string
}

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

	return nil
}
