package webrtc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/opus"
	"github.com/pion/mediadevices/pkg/codec/vpx"
	"github.com/pion/rtcp"

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

	return &Manager{
		config:          myconfig,
		wsConnection:    wsConn,
		camera:          mediadevices.MediaDeviceInfo{},
		microphone:      mediadevices.MediaDeviceInfo{},
		recorder:        recorder,
		pcConfiguration: pcConfig,
	}, nil
}

func (m *Manager) Initialize() (*mediadevices.CodecSelector, error) {

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
	vpxParams.BitRate = 1_000_000
	vpxParams.KeyFrameInterval = 30

	// Create Opus parameters
	opusParams, err := opus.NewParams()
	if err != nil {
		return nil, fmt.Errorf("failed to create Opus params: %v", err)
	}
	opusParams.BitRate = 96_000

	// Create and store codec selector
	codecSelector := mediadevices.NewCodecSelector(
		mediadevices.WithVideoEncoders(&vpxParams),
		mediadevices.WithAudioEncoders(&opusParams),
	)

	// Register codecs with the MediaEngine
	codecSelector.Populate(&mediaEngine)

	// Create API with MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(&mediaEngine))

	// Create peer connection
	peerConnection, err := api.NewPeerConnection(m.pcConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %v", err)
	}

	m.peerConnection = peerConnection

	// Set up handlers
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Printf("Connection State has changed to %s\n", connectionState.String())
	})

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Got track: %s (%s)", track.ID(), track.Kind())
		if m.recorder != nil {
			if err := m.recorder.HandleTrack(track); err != nil {
				log.Printf("Error handling track: %v", err)
			}
		}
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

func (m *Manager) SetupMediaTracks(camera, microphone mediadevices.MediaDeviceInfo, codecSelector *mediadevices.CodecSelector) error {

	// Set up the media stream with the same codec selector
	stream, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(camera.DeviceID)
			c.FrameFormat = prop.FrameFormat(frame.FormatI420)
			c.Width = prop.Int(640)
			c.Height = prop.Int(480)
			c.FrameRate = prop.Float(30)
		},
		Audio: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(microphone.DeviceID)
			c.SampleRate = prop.Int(48000)
			c.ChannelCount = prop.Int(2)
			c.Latency = prop.Duration(20 * time.Millisecond)
		},
		Codec: codecSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to get user media: %v", err)
	}

	m.camera = camera
	m.microphone = microphone
	m.mediaStream = stream

	// Add tracks to peer connection
	for _, track := range stream.GetTracks() {
		log.Printf("Adding track: %s", track.ID())
		track.OnEnded(func(err error) {
			log.Printf("Track (ID: %s) ended with error: %v\n", track.ID(), err)
		})

		transceiver, err := m.peerConnection.AddTransceiverFromTrack(track,
			webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendonly,
			},
		)
		if err != nil {
			return fmt.Errorf("failed to add track %s: %v", track.ID(), err)
		}
		log.Printf("Successfully added track %s with transceiver %s\n", track.ID(), transceiver.Mid())

		// Add PLI handler for video tracks
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			sender := transceiver.Sender()
			if sender == nil {
				continue
			}

			parameters := sender.GetParameters()
			if len(parameters.Encodings) == 0 {
				continue
			}

			ssrc := parameters.Encodings[0].SSRC
			go func(ssrc webrtc.SSRC) {
				ticker := time.NewTicker(3 * time.Second)
				for range ticker.C {
					if err := m.peerConnection.WriteRTCP([]rtcp.Packet{
						&rtcp.PictureLossIndication{
							MediaSSRC: uint32(ssrc),
						},
					}); err != nil {
						log.Printf("Failed to send PLI: %v", err)
					}
				}
			}(ssrc)
		}
	}
	return nil
}

// Make sure Initialize is called before SetupMediaTracks
func (m *Manager) Close() error {
	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
	}

	if m.peerConnection != nil {
		return m.peerConnection.Close()
	}
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

	if err := m.peerConnection.SetRemoteDescription(*response.Result); err != nil {
		return fmt.Errorf("failed to set remote description: %v", err)
	}
	return nil
}

func (m *Manager) handleOffer(response *Response) error {
	if response.Params == nil {
		return fmt.Errorf("received nil params in offer")
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

	if err := m.peerConnection.AddICECandidate(*trickleResponse.Params.Candidate); err != nil {
		return fmt.Errorf("failed to add ICE candidate: %v", err)
	}
	return nil
}

func (m *Manager) GetCamera() mediadevices.MediaDeviceInfo {
	return m.camera
}

func (m *Manager) GetMicrophone() mediadevices.MediaDeviceInfo {
	return m.microphone
}
