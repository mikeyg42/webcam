package webrtc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/vpx"
	"github.com/pion/mediadevices/pkg/frame"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v3"
	"github.com/sourcegraph/jsonrpc2"

	"github.com/mikey42/webcam/internal/config"
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
	config         *config.Config
	peerConnection *webrtc.PeerConnection
	connectionID   uint64
	wsConnection   *websocket.Conn
	mediaStream    *mediadevices.MediaStream
}

func NewManager(config *config.Config, wsConn *websocket.Conn) (*Manager, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if wsConn == nil {
		return nil, fmt.Errorf("websocket connection cannot be nil")
	}

	return &Manager{
		config:       config,
		wsConnection: wsConn,
	}, nil
}

// Close handles cleanup of WebRTC resources
func (m *Manager) Close() error {
	if m.mediaStream != nil {
		m.mediaStream.Close()
	}

	if m.peerConnection != nil {
		if err := m.peerConnection.Close(); err != nil {
			return fmt.Errorf("failed to close peer connection: %v", err)
		}
	}
	return nil
}

// [Previous Initialize() method remains the same]

func (m *Manager) SetupMediaTracks(deviceIDs []string) error {
	if len(deviceIDs) < 2 {
		return fmt.Errorf("not enough device IDs provided, need both video and audio")
	}

	s, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(deviceIDs[0])
			c.FrameFormat = prop.FrameFormat(frame.FormatYUY2)
			c.Width = prop.Int(m.config.VideoConfig.Width)
			c.Height = prop.Int(m.config.VideoConfig.Height)
		},
		Audio: func(a *mediadevices.MediaTrackConstraints) {
			a.DeviceID = prop.String(deviceIDs[1])
		},
		Codec: mediadevices.NewCodecSelector(
			mediadevices.WithVideoEncoders(&vpx.VP8Params{
				BitRate: m.config.VideoConfig.BitRate,
			}),
		),
	})
	if err != nil {
		return fmt.Errorf("failed to get user media: %v", err)
	}

	m.mediaStream = s // Save reference for cleanup

	for _, track := range s.GetTracks() {
		track.OnEnded(func(err error) {
			log.Printf("Track (ID: %s) ended with error: %v\n", track.ID(), err)
		})
		_, err = m.peerConnection.AddTransceiverFromTrack(track,
			webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendonly,
			},
		)
		if err != nil {
			return fmt.Errorf("failed to add track: %v", err)
		}
	}
	return nil
}

// [Previous SetupSignaling() method remains the same]

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

	return m.wsConnection.WriteMessage(
		websocket.TextMessage,
		reqBodyBytes.Bytes(),
	)
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
