package livekitPublisher

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/quality"
)

// Publisher connects to a LiveKit room and publishes camera tracks.
// Uses webrtc.TrackLocalStaticRTP for zero-copy RTP forwarding — GStreamer
// already produces correctly packetized H.264 RTP, so we forward as-is.
type Publisher struct {
	room       *lksdk.Room
	videoTrack *webrtc.TrackLocalStaticRTP
	audioTrack *webrtc.TrackLocalStaticRTP
	config     *config.LiveKitConfig
	debugMode  bool

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
}

func NewPublisher(ctx context.Context, cfg *config.Config, debugMode bool) (*Publisher, error) {
	pubCtx, cancel := context.WithCancel(ctx)
	return &Publisher{
		config:    &cfg.LiveKit,
		debugMode: debugMode,
		ctx:       pubCtx,
		cancel:    cancel,
	}, nil
}

// Connect joins the LiveKit room as a publisher.
func (p *Publisher) Connect() error {
	log.Printf("[LiveKit] Connecting to %s, room=%s", p.config.URL(), p.config.RoomName)

	cb := lksdk.NewRoomCallback()
	cb.OnDisconnected = func() {
		log.Println("[LiveKit] Disconnected from room")
	}
	cb.OnReconnecting = func() {
		log.Println("[LiveKit] Reconnecting...")
	}
	cb.OnReconnected = func() {
		log.Println("[LiveKit] Reconnected")
	}

	room, err := lksdk.ConnectToRoom(p.config.URL(), lksdk.ConnectInfo{
		APIKey:              p.config.APIKey,
		APISecret:           p.config.APISecret,
		RoomName:            p.config.RoomName,
		ParticipantIdentity: "camera-backend",
		ParticipantName:     "Security Camera",
	}, cb)
	if err != nil {
		return fmt.Errorf("failed to connect to LiveKit room: %w", err)
	}

	p.mu.Lock()
	p.room = room
	p.mu.Unlock()

	log.Printf("[LiveKit] Connected to room %q (SID: %s)", room.Name(), room.SID())
	return nil
}

// PublishTracks creates and publishes H.264 video and Opus audio tracks.
// Uses TrackLocalStaticRTP so pre-packetized RTP from GStreamer is forwarded as-is.
func (p *Publisher) PublishTracks(videoCodecMimeType string, width, height int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.room == nil {
		return fmt.Errorf("not connected to room")
	}

	// Video track — static RTP (no re-packetization)
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: videoCodecMimeType},
		"video",
		"gstreamer-video",
	)
	if err != nil {
		return fmt.Errorf("failed to create video track: %w", err)
	}

	_, err = p.room.LocalParticipant.PublishTrack(videoTrack, &lksdk.TrackPublicationOptions{
		Name:        "camera-video",
		VideoWidth:  width,
		VideoHeight: height,
	})
	if err != nil {
		return fmt.Errorf("failed to publish video track: %w", err)
	}
	p.videoTrack = videoTrack
	log.Printf("[LiveKit] Published video track (%s, %dx%d)", videoCodecMimeType, width, height)

	// Audio track — static RTP
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio",
		"gstreamer-audio",
	)
	if err != nil {
		return fmt.Errorf("failed to create audio track: %w", err)
	}

	_, err = p.room.LocalParticipant.PublishTrack(audioTrack, &lksdk.TrackPublicationOptions{
		Name: "camera-audio",
	})
	if err != nil {
		return fmt.Errorf("failed to publish audio track: %w", err)
	}
	p.audioTrack = audioTrack
	log.Println("[LiveKit] Published audio track (opus)")

	// Brief pause for SDP negotiation to complete and track bindings to establish.
	// Without this, the first RTP packets (including the initial keyframe) may be
	// silently dropped because TrackLocalStaticRTP.WriteRTP returns nil when unbound.
	time.Sleep(500 * time.Millisecond)

	return nil
}

// AttachRTPSource starts forwarding RTP packets from GStreamer to the published tracks.
func (p *Publisher) AttachRTPSource(videoRTP <-chan *rtp.Packet, audioRTP <-chan *rtp.Packet) error {
	p.mu.RLock()
	vt := p.videoTrack
	at := p.audioTrack
	p.mu.RUnlock()

	if vt == nil && at == nil {
		return fmt.Errorf("no tracks published — call PublishTracks first")
	}

	if videoRTP != nil && vt != nil {
		go p.forwardRTP(videoRTP, vt, "video")
		log.Println("[LiveKit] Video RTP forwarding started")
	}
	if audioRTP != nil && at != nil {
		go p.forwardRTP(audioRTP, at, "audio")
		log.Println("[LiveKit] Audio RTP forwarding started")
	}

	return nil
}

func (p *Publisher) forwardRTP(ch <-chan *rtp.Packet, track *webrtc.TrackLocalStaticRTP, kind string) {
	var sent, dropped uint64
	lastLog := time.Now()

	for {
		select {
		case <-p.ctx.Done():
			log.Printf("[LiveKit] %s RTP forwarding stopped (context cancelled)", kind)
			return
		case pkt, ok := <-ch:
			if !ok {
				log.Printf("[LiveKit] %s RTP channel closed", kind)
				return
			}
			if pkt == nil {
				continue
			}

			if err := track.WriteRTP(pkt); err != nil {
				dropped++
				if p.debugMode && dropped%100 == 1 {
					log.Printf("[LiveKit] %s RTP write error (dropped=%d): %v", kind, dropped, err)
				}
			} else {
				sent++
			}

			if p.debugMode && time.Since(lastLog) > 10*time.Second {
				log.Printf("[LiveKit] %s RTP stats: sent=%d, dropped=%d", kind, sent, dropped)
				lastLog = time.Now()
			}
		}
	}
}

// Disconnect leaves the LiveKit room and cleans up.
func (p *Publisher) Disconnect() {
	p.cancel()

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.room != nil {
		p.room.Disconnect()
		p.room = nil
		log.Println("[LiveKit] Room disconnected")
	}
}

// GetQualityManager satisfies the api.QualityManagerProvider interface.
// Returns nil until we wire LiveKit room stats into the quality system.
func (p *Publisher) GetQualityManager() *quality.QualityManager {
	return nil
}
