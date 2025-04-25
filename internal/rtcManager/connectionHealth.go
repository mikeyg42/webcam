package rtcManager

import (
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/opus"
	"github.com/pion/mediadevices/pkg/codec/vpx"
	"github.com/pion/mediadevices/pkg/frame"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v4"
)

const (
	warningPacketLoss  = 0.05 // 5%
	criticalPacketLoss = 0.15 // 15%

	minAcceptableFramerate = 10.0 // fps

	metricsCollectionInterval = 1 * time.Second
	circularBufferCapacity    = 21
	statsHistoryDuration      = 20 * time.Second

	criticalWSLatency = 500 // Milliseconds
	warningWSLatency  = 200 // Milliseconds

	criticalTURNLatency = 300 // Milliseconds
	warningTURNLatency  = 150 // Milliseconds

	// Bitrate thresholds
	criticalBitrate = 50_000  // 50 Kbps
	warningBitrate  = 100_000 // 100 Kbps
	targetBitrate   = 500_000 // 500 Kbps

	maxExpectedVariance = 0.25 // i.e. up to 25% variance
	maxExpectedTrend    = 0.1  // Maximum expected rate of change per sample
	maxExpectedJitter   = 0.05 // Maximum expected jitter (50ms)
)

// ConnectionDoctor handles monitoring and diagnosis of WebRTC connections
type ConnectionDoctor struct {
	manager  *Manager
	ctx      context.Context
	cancel   context.CancelFunc
	metrics  *CircularMetricsBuffer
	done     chan struct{}
	warnings chan Warning // Channel for communicating issues
}

type Warning struct {
	Level       WarningLevel
	Type        WarningType
	Message     string
	Timestamp   string
	Measurement float64 // Optional measurement associated with warning
}

type WarningLevel int

const (
	InfoLevel WarningLevel = iota
	SuggestionLevel
	CriticalLevel
)

type WarningType int

const (
	PacketLossWarning WarningType = iota
	LatencyWarning
	BitrateWarning
	FramerateWarning
	ICEWarning
	TURNWarning
	SignalingWarning
	ConnWarning
)

type RTCPFeedbackStats struct {
	NACKCount uint32
	PLICount  uint32
	FIRCount  uint32
}

type NetworkMetrics struct {
	BytesSent     uint64
	BytesReceived uint64
	RTCPSent      uint64
	RTCPReceived  uint64
	RTCPLost      uint64
	Nacks         uint32
	Plis          uint32
	Firs          uint32
	BurstLossRate uint64
	FrameWidth    uint32
	FrameHeight   uint32
}

type TransportMetrics struct {
	BytesSent         uint64
	BytesReceived     uint64
	PacketsSent       uint64
	PacketsReceived   uint64
	DTLSState         webrtc.DTLSTransportState
	SelectedCandidate struct {
		Local  string
		Remote string
	}
	CurrentRoundTripTime float64 // in seconds
	LocalCandidateID     string
	RemoteCandidateID    string
}

// Enhanced QualityMetrics
type QualityMetrics struct {
	TrackID        string
	Timestamp      time.Time
	PacketLossRate float64
	RTT            time.Duration
	Framerate      float64
	Resolution     string
	Bitrate        uint64
	JitterBuffer   float64
	BurstLossRate  uint64
	ICEState       webrtc.ICEConnectionState
	DTLSState      webrtc.DTLSTransportState
	SignalingState webrtc.SignalingState

	// Network specific metrics
	Network   NetworkMetrics
	Transport TransportMetrics

	// WebSocket/JSON-RPC metrics
	WSLatency      time.Duration
	JSONRPCLatency time.Duration
	WSState        string

	// TURN specific metrics
	TURNLatency          time.Duration
	TURNState            string
	RelayedBytes         uint64
	NumActiveConnections int
}

// ---------

func (m *Manager) RespondToDoctorsWarnings() {
	tracker := NewWarningTracker()

	for warning := range m.ConnectionDoctor.warnings {
		// Track and potentially upgrade warning
		warning = tracker.Track(warning)

		log.Printf("[ConnectionDoctor] %v: %s", warning.Level, warning.Message)

		// Check connection state first
		switch m.PeerConnection.ConnectionState() {
		case webrtc.PeerConnectionStateFailed:
			log.Println("Connection failed, attempting ICE restart")
			if err := m.handleConnectionFailure(); err != nil {
				log.Printf("Failed to handle connection failure: %v", err)
			}
			continue // Skip normal warning processing
		case webrtc.PeerConnectionStateDisconnected:
			log.Println("Connection disconnected, attempting reconnection")
			if err := m.handleDisconnection(); err != nil {
				log.Printf("Failed to handle disconnection: %v", err)
			}
			continue // Skip normal warning processing
		}

		// Check warning frequency
		warningFrequency := tracker.GetWarningCount(warning.Type, 5*time.Minute)

		switch warning.Level {
		case CriticalLevel:
			switch warning.Type {
			case ICEWarning:
				m.handleConnectionFailure()
			case PacketLossWarning, LatencyWarning:
				if warningFrequency >= 3 {
					// If we've seen multiple critical warnings, be more aggressive
					m.adjustMediaConstraints(true) // aggressive mode
				}
			case SignalingWarning:
				if warningFrequency >= 2 {
					m.handleDisconnection()
				}
			case TURNWarning:
				if warningFrequency >= 3 {
					// Consider restarting TURN server or switching to backup
					log.Printf("Multiple TURN issues detected, considering infrastructure changes")
				}
			}

		case SuggestionLevel:
			switch warning.Type {
			case PacketLossWarning, BitrateWarning:
				if warningFrequency >= 3 {
					m.adjustMediaConstraints(false) // moderate adjustments
				}
			case LatencyWarning:
				if warningFrequency >= 4 {
					// If latency issues persist, try moderate adjustments
					m.adjustMediaConstraints(false)
				}
			default:
				// Log for monitoring but don't take action yet
				log.Printf("Monitoring suggestion: %s (count: %d)",
					warning.Message, warningFrequency)
			}

		case InfoLevel:
			// Check if we can relax constraints
			if warningFrequency < 2 &&
				tracker.GetWarningCount(PacketLossWarning, 5*time.Minute) == 0 &&
				tracker.GetWarningCount(LatencyWarning, 5*time.Minute) == 0 {

				currentMode := m.getCurrentMediaMode()
				timeSinceLastCritical := time.Since(tracker.LastCriticalWarning())
				timeSinceLastChange := time.Since(m.LastConstraintChange())

				switch currentMode {
				case "aggressive":
					if timeSinceLastCritical > 5*time.Minute {
						m.adjustMediaConstraints(false) // Switch to moderate
					}
				case "moderate":
					if timeSinceLastCritical > 8*time.Minute &&
						timeSinceLastChange > 2*time.Minute {
						m.resetMediaConstraints() // Return to default
					}
				}
			}

			// Log info for monitoring
			log.Printf("[ConnectionDoctor Info] %s (Recent count: %d)",
				warning.Message, warningFrequency)
		}
	}
}

func (wt *WarningTracker) GetWarningCount(warningType WarningType, duration time.Duration) int {
	wt.mu.RLock()
	defer wt.mu.RUnlock()

	count := 0
	cutoff := time.Now().Add(-duration)

	for _, warnings := range wt.warnings {
		for _, w := range warnings {
			if w.Type == warningType {
				if t, err := time.Parse("15:04:05.000", w.Timestamp); err == nil {
					if t.After(cutoff) {
						count++
					}
				}
			}
		}
	}

	return count
}

func (m *Manager) getCurrentMediaMode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.MediaMode
}

func (m *Manager) LastConstraintChange() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.LastConstraintChangeTime
}

func (m *Manager) resetMediaConstraints() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize default constraints if not already set
	if m.MediaConstraints.base.Video.Width == 0 {
		m.MediaConstraints.base = MediaConstraints{
			Video: struct {
				Width     int
				Height    int
				FrameRate float32
				BitRate   uint
			}{
				Width:     640,
				Height:    480,
				FrameRate: 30,
				BitRate:   800_000,
			},
			Audio: struct {
				SampleRate   int
				ChannelCount int
				BitRate      uint
			}{
				SampleRate:   48000,
				ChannelCount: 1,
				BitRate:      64000,
			},
		}
	}

	// Apply default constraints
	constraints := mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(m.camera.DeviceID)
			c.FrameFormat = prop.FrameFormat(frame.FormatYUY2)
			c.Width = prop.Int(m.MediaConstraints.base.Video.Width)
			c.Height = prop.Int(m.MediaConstraints.base.Video.Height)
			c.FrameRate = prop.Float(m.MediaConstraints.base.Video.FrameRate)
			c.DiscardFramesOlderThan = 500 * time.Millisecond
		},
		Audio: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(m.microphone.DeviceID)
			c.SampleRate = prop.Int(m.MediaConstraints.base.Audio.SampleRate)
			c.ChannelCount = prop.Int(m.MediaConstraints.base.Audio.ChannelCount)
			c.Latency = prop.Duration(time.Millisecond * 50)
		},
	}

	// Apply new stream with default constraints
	stream, err := mediadevices.GetUserMedia(constraints)
	if err != nil {
		log.Printf("Failed to reset media constraints: %v", err)
		return
	}

	// Clean up old stream
	if m.mediaStream != nil {
		for _, track := range m.mediaStream.GetTracks() {
			track.Close()
		}
	}

	m.mediaStream = stream
	m.MediaMode = "default"
	m.LastConstraintChangeTime = time.Now()

	log.Printf("Media constraints reset to default values")
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
		videoConstraints.frameRate = minAcceptableFramerate
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

	// Update mode and timestamp
	if aggressive {
		m.MediaMode = "aggressive"
	} else {
		m.MediaMode = "moderate"
	}
	m.LastConstraintChangeTime = time.Now()
}

// ------------------
type WarningTracker struct {
	warnings     map[string][]Warning // key is warning type + message
	mu           sync.RWMutex
	upgradeAfter int // in minutes
}

func NewWarningTracker() *WarningTracker {
	return &WarningTracker{
		warnings:     make(map[string][]Warning),
		upgradeAfter: 5, //minutes
	}
}

func (wt *WarningTracker) Track(warning Warning) Warning {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	key := fmt.Sprintf("%d:%s", warning.Type, warning.Message)
	wt.warnings[key] = append(wt.warnings[key], warning)

	// Clean up old warnings (older than 10 minutes)
	wt.cleanup(key, wt.upgradeAfter)

	// Check if warning should be upgraded
	if warning.Level == SuggestionLevel {
		recentWarnings := wt.warnings[key]
		if len(recentWarnings) >= 3 {
			warning.Level = CriticalLevel
			warning.Message = fmt.Sprintf("Persistent issue: %s", warning.Message)
		}
	}

	return warning
}

func (wt *WarningTracker) LastCriticalWarning() time.Time {
	wt.mu.RLock()
	defer wt.mu.RUnlock()

	var lastCritical time.Time
	for _, warnings := range wt.warnings {
		for _, w := range warnings {
			if w.Level == CriticalLevel {
				if t, err := time.Parse("15:04:05.000", w.Timestamp); err == nil {
					if t.After(lastCritical) {
						lastCritical = t
					}
				}
			}
		}
	}
	return lastCritical
}

func (wt *WarningTracker) cleanup(key string, timeSince int) {
	if timeSince > 0 {
		timeSince = 0 - timeSince
	}
	cutoff := time.Now().Add(time.Duration(timeSince) * time.Minute)
	filtered := make([]Warning, 0)
	for _, w := range wt.warnings[key] {
		if t, err := time.Parse("15:04:05.000", w.Timestamp); err == nil && t.After(cutoff) {
			filtered = append(filtered, w)
		}
	}
	wt.warnings[key] = filtered
}

//----------

func (m *Manager) NewConnectionDoctor(ctx context.Context) *ConnectionDoctor {
	CDctx, cancel := context.WithCancel(ctx)
	return &ConnectionDoctor{
		manager:  m,
		ctx:      CDctx,
		cancel:   cancel,
		metrics:  NewCircularMetricsBuffer(circularBufferCapacity),
		done:     make(chan struct{}),
		warnings: make(chan Warning),
	}
}

// StartStatsCollection begins periodic stats collection
func (cd *ConnectionDoctor) StartStatsCollection(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-cd.ctx.Done():
				return
			case <-ticker.C:
				qualityMetrics := cd.AccumulateNewStats()
				cd.metrics.Add(qualityMetrics)
			}
		}
	}()
}
func (cd *ConnectionDoctor) AccumulateNewStats() QualityMetrics {
	metrics := QualityMetrics{
		Timestamp:      time.Now(),
		ICEState:       cd.manager.PeerConnection.ICEConnectionState(),
		DTLSState:      cd.manager.PeerConnection.SCTP().Transport().State(),
		SignalingState: cd.manager.PeerConnection.SignalingState(),
	}

	// Get all stats
	stats := cd.manager.PeerConnection.GetStats()

	// Process each stat type
	for _, s := range stats {
		switch stat := s.(type) {
		case *webrtc.InboundRTPStreamStats:
			cd.processInboundStats(&metrics, stat)

		case *webrtc.OutboundRTPStreamStats:
			metrics.Network.BytesSent += stat.BytesSent
			if stat.Kind == "video" {
				metrics.Network.FrameWidth = stat.FrameWidth
				metrics.Network.FrameHeight = stat.FrameHeight
			}

		case *webrtc.RemoteInboundRTPStreamStats:
			metrics.RTT = time.Duration(stat.RoundTripTime * float64(time.Second))
			metrics.Network.RTCPReceived++

		case *webrtc.TransportStats:
			cd.processTransportStats(&metrics, stat)

		case *webrtc.ICECandidatePairStats:
			if stat.State == "succeeded" {
				metrics.Transport.CurrentRoundTripTime = stat.CurrentRoundTripTime
				metrics.Transport.SelectedCandidate.Local = stat.LocalCandidateID
				metrics.Transport.SelectedCandidate.Remote = stat.RemoteCandidateID
			}

		case *webrtc.ICECandidateStats:
			// Log candidate information
			if stat.Type == "local" {
				log.Printf("[@%s] Local ICE Candidate: %s (%s)", metrics.Timestamp.Format("15:04:05.000"), stat.Protocol, stat.CandidateType)
			} else {
				log.Printf("[@%s] Remote ICE Candidate: %s (%s)", metrics.Timestamp.Format("15:04:05.000"), stat.Protocol, stat.CandidateType)
			}

		case *webrtc.DataChannelStats:
			// Monitor data channel health
			if stat.State != webrtc.DataChannelStateOpen {
				select {
				case cd.warnings <- Warning{
					Timestamp: metrics.Timestamp.Format("15:04:05.000"),
					Level:     InfoLevel,
					Type:      SignalingWarning,
					Message:   fmt.Sprintf("Data channel %s in state: %s", stat.Label, stat.State),
				}:
				case <-cd.ctx.Done():
					return metrics
				}
			}
		}
	}
	// assess TURN Server health\
	cd.collectTURNMetrics(&metrics)

	cd.collectWSMetrics(&metrics)

	// Calculate bitrate
	if recent := cd.metrics.GetRecent(1); len(recent) > 0 {
		lastMetric := recent[0]
		timeDiff := metrics.Timestamp.Sub(lastMetric.Timestamp).Seconds()
		if timeDiff > 0 {
			bytesDiff := metrics.Network.BytesReceived - lastMetric.Network.BytesReceived
			metrics.Bitrate = uint64(float64(bytesDiff*8) / timeDiff)
		}
	}

	// Process RTCP feedback
	if metrics.Network.Nacks > 0 || metrics.Network.Plis > 0 || metrics.Network.Firs > 0 {
		cd.processRTCPStats(&RTCPFeedbackStats{
			NACKCount: metrics.Network.Nacks,
			PLICount:  metrics.Network.Plis,
			FIRCount:  metrics.Network.Firs,
		})
	}

	return metrics
}

func (cd *ConnectionDoctor) processRTCPStats(stat *RTCPFeedbackStats) {
	const (
		nackThreshold = 10 // NACKs per second
		pliThreshold  = 2  // PLIs per second
		firThreshold  = 1  // FIRs per second
	)

	// Get time period since last stats
	recent := cd.metrics.GetRecent(1)
	lastTime := recent[0].Timestamp
	timePeriod := time.Now().Sub(lastTime).Seconds()

	// Calculate rates
	nackRate := float64(stat.NACKCount) / timePeriod
	pliRate := float64(stat.PLICount) / timePeriod
	firRate := float64(stat.FIRCount) / timePeriod

	// Check for excessive feedback
	if nackRate > nackThreshold {
		select {
		case cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       CriticalLevel,
			Type:        PacketLossWarning,
			Message:     fmt.Sprintf("High NACK rate: %.2f/s", nackRate),
			Measurement: nackRate,
		}:
		case <-cd.ctx.Done():
			return
		}
	}

	if pliRate > pliThreshold {
		select {
		case cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       CriticalLevel,
			Type:        FramerateWarning,
			Message:     fmt.Sprintf("High PLI rate: %.2f/s", pliRate),
			Measurement: pliRate,
		}:
		case <-cd.ctx.Done():
			return
		}
	}

	if firRate > firThreshold {
		select {
		case cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       CriticalLevel,
			Type:        FramerateWarning,
			Message:     fmt.Sprintf("High FIR rate: %.2f/s", firRate),
			Measurement: firRate,
		}:
		case <-cd.ctx.Done():
			return
		}
	}
}

func (cd *ConnectionDoctor) processInboundStats(metrics *QualityMetrics, stat *webrtc.InboundRTPStreamStats) {
	if stat.Kind == "video" {
		metrics.Framerate = float64(stat.FramesDecoded)
		metrics.Resolution = fmt.Sprintf("%dx%d", stat.FrameWidth, stat.FrameHeight)

		// Track frame decode times
		if stat.FramesDecoded > 0 {
			avgDecodeTime := stat.TotalDecodeTime / float64(stat.FramesDecoded)
			if avgDecodeTime > 0.033 { // More than 33ms per frame
				select {
				case cd.warnings <- Warning{
					Timestamp: time.Now().Format("15:04:05.000"),
					Level:     SuggestionLevel,
					Type:      FramerateWarning,
					Message:   fmt.Sprintf("High decode time: %.2fms", avgDecodeTime*1000),
				}:
				case <-cd.ctx.Done():
					return
				}
			}
		}
	}
	metrics.TrackID = stat.TrackID
	metrics.BurstLossRate = uint64(stat.BurstLossRate)
	metrics.PacketLossRate = float64(stat.PacketsLost) / float64(stat.PacketsReceived+uint32(stat.PacketsLost))
	metrics.JitterBuffer = stat.Jitter
	metrics.Network.BytesReceived += stat.BytesReceived
	metrics.Network.Nacks += stat.NACKCount
	metrics.Network.Plis += stat.PLICount
	metrics.Network.Firs += stat.FIRCount
	metrics.Network.FrameWidth = stat.FrameWidth
	metrics.Network.FrameHeight = stat.FrameHeight
}

func (cd *ConnectionDoctor) processTransportStats(metrics *QualityMetrics, stat *webrtc.TransportStats) {
	metrics.Transport.BytesSent = stat.BytesSent
	metrics.Transport.BytesReceived = stat.BytesReceived
	metrics.Transport.PacketsSent = uint64(stat.PacketsSent)
	metrics.Transport.PacketsReceived = uint64(stat.PacketsReceived)
}

func (cd *ConnectionDoctor) collectWSMetrics(metrics *QualityMetrics) {
	if cd.manager.wsConnection == nil {
		return
	}

	// Measure WebSocket latency with ping
	start := time.Now()
	err := cd.manager.wsConnection.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second))
	if err != nil {
		metrics.WSState = "Error"
		select {
		case cd.warnings <- Warning{
			Timestamp: time.Now().Format("15:04:05.000"),
			Level:     CriticalLevel,
			Type:      SignalingWarning,
			Message:   fmt.Sprintf("WebSocket ping failed: %v", err),
		}:
		case <-cd.ctx.Done():
			return
		}
		return
	}

	metrics.WSLatency = time.Since(start)
	metrics.WSState = "Connected"

	// Check JSON-RPC health
	if cd.manager.rpcConn != nil {
		jsonrpc_latency, err := cd.checkJSONRPCHealth()
		if err != nil {
			select {
			case cd.warnings <- Warning{
				Timestamp: time.Now().Format("15:04:05.000"),
				Level:     CriticalLevel,
				Type:      SignalingWarning,
				Message:   fmt.Sprintf("JSON-RPC health check failed: %v", err),
			}:
			case <-cd.ctx.Done():
				return
			}
		} else {
			metrics.JSONRPCLatency = jsonrpc_latency
		}
	}
}

func (cd *ConnectionDoctor) checkJSONRPCHealth() (time.Duration, error) {
	for attempt := 1; attempt <= 3; attempt++ {
		latency, err := cd.pingRPC()
		if err == nil {
			return latency, nil
		}
		log.Printf("[checkJSONRPCHealth] Attempt %d failed: %v", attempt, err)
		time.Sleep(500 * time.Millisecond)
	}
	return 0, fmt.Errorf("[checkJSONRPCHealth] RPC health check failed after 3 attempts")
}

func (cd *ConnectionDoctor) pingRPC() (time.Duration, error) {
	// Create a context with a timeout for the health check
	ctx, cancel := context.WithTimeout(cd.ctx, 2*time.Second)
	defer cancel()

	// Start measuring time
	start := time.Now()

	// Call a health-check endpoint (e.g., "ping") on the rpcConn
	var result string
	err := cd.manager.rpcConn.Call(ctx, "ping", nil, &result)
	if err != nil {
		return 0, fmt.Errorf("[pingRPC] RPC call failed: %w", err)
	}

	// Ensure the response is what you expect
	if result != "pong" {
		return 0, fmt.Errorf("[pingRPC] Unexpected ping response: %s", result)
	}

	// Calculate and return latency
	latency := time.Since(start)
	log.Printf("[pingRPC] Ping successful, latency: %v", latency)
	return latency, nil
}

func (cd *ConnectionDoctor) collectTURNMetrics(metrics *QualityMetrics) {
	if cd.manager.turnServer == nil {
		return
	}

	// Check allocation health
	TURNstats := cd.manager.turnServer.GetStats()
	metrics.NumActiveConnections = TURNstats.ActiveAllocations
	if TURNstats.CurrentState != "running" {
		cd.warnings <- Warning{
			Timestamp: time.Now().Format("15:04:05.000"),
			Level:     InfoLevel,
			Type:      TURNWarning,
			Message:   fmt.Sprintf("[turn server] TURN server not running. state: %s", TURNstats.CurrentState),
		}
		return
	}

	// Perform TURN latency test
	start := time.Now()
	testAllocation := func() error {
		// Create a temporary allocation to test TURN server responsiveness
		conn, err := net.DialUDP("udp", nil, &net.UDPAddr{
			IP:   net.ParseIP(cd.manager.turnServer.PublicIP),
			Port: cd.manager.turnServer.Port,
		})
		if err != nil {
			return err
		}
		defer conn.Close()
		return nil
	}

	if err := testAllocation(); err != nil {
		cd.warnings <- Warning{
			Timestamp: time.Now().Format("15:04:05.000"),
			Level:     CriticalLevel,
			Type:      TURNWarning,
			Message:   fmt.Sprintf("TURN allocation test failed: %v", err),
		}
	}

	metrics.TURNLatency = time.Since(start)
	metrics.TURNState = cd.manager.turnServer.GetState()
}

func (cd *ConnectionDoctor) checkPackageLostRate(metrics QualityMetrics) {

	// Packet Loss
	if metrics.PacketLossRate >= criticalPacketLoss {
		cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       CriticalLevel,
			Type:        PacketLossWarning,
			Message:     fmt.Sprintf("Critical packet loss: %.2f%%", metrics.PacketLossRate*100),
			Measurement: metrics.PacketLossRate,
		}
	} else if metrics.PacketLossRate >= warningPacketLoss {
		cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       SuggestionLevel,
			Type:        PacketLossWarning,
			Message:     fmt.Sprintf("High packet loss: %.2f%%", metrics.PacketLossRate*100),
			Measurement: metrics.PacketLossRate,
		}
	}
}

func (cd *ConnectionDoctor) checkConnectionStates(metrics QualityMetrics) {
	// Check ICE State
	if metrics.ICEState == webrtc.ICEConnectionStateFailed {
		select {
		case cd.warnings <- Warning{
			Timestamp: time.Now().Format("15:04:05.000"),
			Level:     CriticalLevel,
			Type:      ICEWarning,
			Message:   "ICE connection failed",
		}:
		default:
			log.Printf("Warning channel blocked, dropping ICE failure warning")
		}
	}

	// Check DTLS State
	if metrics.DTLSState == webrtc.DTLSTransportStateFailed {
		cd.warnings <- Warning{
			Timestamp: time.Now().Format("15:04:05.000"),
			Level:     CriticalLevel,
			Type:      SignalingWarning,
			Message:   "DTLS transport failed",
		}
	}
}

func (cd *ConnectionDoctor) detectPeriodicIssues() {
	recent := cd.metrics.GetRecent(30) // Get last 30 samples
	if len(recent) < 30 {
		return
	}

	// Look for patterns in packet loss
	packetLossPattern := cd.analyzePattern(recent, func(m QualityMetrics) float64 {
		return m.PacketLossRate
	})

	// Look for patterns in RTT
	rttPattern := cd.analyzePattern(recent, func(m QualityMetrics) float64 {
		return m.RTT.Seconds()
	})

	if packetLossPattern.isPeriodic || rttPattern.isPeriodic {
		cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       SuggestionLevel,
			Type:        LatencyWarning,
			Message:     "Detected periodic network issues",
			Measurement: packetLossPattern.period,
		}
	}
}

type Pattern struct {
	isPeriodic bool
	period     float64
	amplitude  float64
	trend      string // "increasing", "decreasing", "cyclic", "stable"
}

func (cd *ConnectionDoctor) analyzePattern(metrics []QualityMetrics, getValue func(QualityMetrics) float64) Pattern {
	if len(metrics) < 30 {
		return Pattern{isPeriodic: false}
	}

	values := make([]float64, len(metrics))
	for i, m := range metrics {
		values[i] = getValue(m)
	}

	// Check for cyclical patterns using autocorrelation
	autocorr := calculateAutocorrelation(values)

	// Check for periodic spikes
	peaks := findPeaks(autocorr)
	if len(peaks) >= 2 {
		avgPeriod := calculateAveragePeriod(peaks)
		amplitude := calculateAmplitude(values)

		return Pattern{
			isPeriodic: true,
			period:     avgPeriod,
			amplitude:  amplitude,
			trend:      detectTrend(values),
		}
	}

	return Pattern{isPeriodic: false}
}

func calculateAveragePeriod(peaks []int) float64 {
	if len(peaks) < 2 {
		return 0
	}

	// Calculate differences between consecutive peaks
	periods := make([]float64, len(peaks)-1)
	for i := 1; i < len(peaks); i++ {
		periods[i-1] = float64(peaks[i] - peaks[i-1])
	}

	// Calculate mean period
	sum := 0.0
	for _, period := range periods {
		sum += period
	}
	return sum / float64(len(periods))
}

func calculateAmplitude(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	min, max := values[0], values[0] // preallocate
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	return (max - min) / 2 // Peak-to-peak amplitude divided by 2
}

func detectTrend(values []float64) string {
	if len(values) < 3 {
		return "stable"
	}

	// Calculate linear regression
	var sumX, sumY, sumXY, sumXX float64
	n := float64(len(values))

	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}

	slope := (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)

	// Calculate R-squared to determine if trend is significant
	meanY := sumY / n
	var totalSS, residualSS float64

	for i, y := range values {
		predicted := slope*float64(i) + (sumY-slope*sumX)/n
		residualSS += (y - predicted) * (y - predicted)
		totalSS += (y - meanY) * (y - meanY)
	}

	rSquared := 1 - (residualSS / totalSS)

	// Determine trend type
	if rSquared < 0.5 {
		// Check for cyclical pattern
		autocorr := calculateAutocorrelation(values)
		if hasCyclicalPattern(autocorr) {
			return "cyclic"
		}
		return "stable"
	}

	if math.Abs(slope) < 0.01 {
		return "stable"
	}
	if slope > 0 {
		return "increasing"
	}
	return "decreasing"
}

func hasCyclicalPattern(autocorr []float64) bool {
	// Look for secondary peaks in autocorrelation
	peaks := findPeaks(autocorr)
	if len(peaks) < 2 {
		return false
	}

	// Calculate average peak height (excluding first peak which is always 1.0)
	var avgPeakHeight float64
	for i := 1; i < len(peaks); i++ {
		avgPeakHeight += autocorr[peaks[i]]
	}
	avgPeakHeight /= float64(len(peaks) - 1)

	// If average peak height is significant, consider it cyclic
	return avgPeakHeight > 0.3
}

func calculateAutocorrelation(values []float64) []float64 {
	n := len(values)
	autocorr := make([]float64, n/2)

	// Calculate mean
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(n)

	// Calculate variance for normalization
	var variance float64
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(n)

	// Calculate autocorrelation for each lag
	for lag := 0; lag < n/2; lag++ {
		var crossSum float64
		for i := 0; i < n-lag; i++ {
			crossSum += (values[i] - mean) * (values[i+lag] - mean)
		}
		autocorr[lag] = crossSum / (float64(n-lag) * variance)
	}

	return autocorr
}

func findPeaks(values []float64) []int {
	peaks := []int{}
	for i := 1; i < len(values)-1; i++ {
		if values[i] > values[i-1] && values[i] > values[i+1] {
			peaks = append(peaks, i)
		}
	}
	return peaks
}

func calculateStabilityScore(metrics []QualityMetrics, getValue func(QualityMetrics) float64) float64 {
	values := make([]float64, len(metrics))
	for i, m := range metrics {
		values[i] = getValue(m)
	}

	variance := calculateVariance(values)
	trend := calculateTrendStrength(values)
	jitter := calculateJitter(values)

	// Normalize each component (0-1 scale)
	normalizedVariance := math.Exp(-variance / maxExpectedVariance)
	normalizedTrend := math.Exp(-math.Abs(trend) / maxExpectedTrend)
	normalizedJitter := math.Exp(-jitter / maxExpectedJitter)

	// Weighted combination
	return 0.4*normalizedVariance + 0.3*normalizedTrend + 0.3*normalizedJitter
}

func calculateTrendStrength(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}

	// Calculate linear regression slope
	var sumX, sumY, sumXY, sumXX float64
	n := float64(len(values))

	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}

	slope := (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
	return math.Abs(slope)
}

func calculateJitter(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}

	var sumJitter float64
	for i := 1; i < len(values); i++ {
		sumJitter += math.Abs(values[i] - values[i-1])
	}

	return sumJitter / float64(len(values)-1)
}

func (cd *ConnectionDoctor) analyzeNetworkStability() {
	recent := cd.metrics.GetRecent(10)
	if len(recent) < 10 {
		return
	}

	packetLossStability := calculateStabilityScore(recent, func(m QualityMetrics) float64 {
		return m.PacketLossRate
	})

	rttStability := calculateStabilityScore(recent, func(m QualityMetrics) float64 {
		return m.RTT.Seconds()
	})

	bitrateStability := calculateStabilityScore(recent, func(m QualityMetrics) float64 {
		return float64(m.Bitrate)
	})

	// Combined stability score (0-1, where 1 is most stable)
	stabilityScore := (packetLossStability + rttStability + bitrateStability) / 3.0

	if stabilityScore < 0.5 {
		cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       SuggestionLevel,
			Type:        BitrateWarning,
			Message:     fmt.Sprintf("Network instability detected (score: %.2f)", stabilityScore),
			Measurement: stabilityScore,
		}
	}
}
func (cd *ConnectionDoctor) analyzeInfrastructureHealth(metrics QualityMetrics) {
	// Check TURN server health
	if metrics.TURNLatency > criticalTURNLatency {
		cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       CriticalLevel,
			Type:        TURNWarning,
			Message:     fmt.Sprintf("TURN server latency critical: %v", metrics.TURNLatency),
			Measurement: metrics.TURNLatency.Seconds(),
		}
	}

	// Check WebSocket connection health
	if metrics.WSLatency > criticalWSLatency {
		cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       CriticalLevel,
			Type:        SignalingWarning,
			Message:     fmt.Sprintf("WebSocket latency critical: %v", metrics.WSLatency),
			Measurement: metrics.WSLatency.Seconds(),
		}
	}

	// Check for infrastructure capacity issues
	if metrics.NumActiveConnections > 0 {
		utilizationRate := float64(metrics.RelayedBytes) / float64(metrics.NumActiveConnections)
		if utilizationRate > 1_000_000 { // 1 MB per connection
			cd.warnings <- Warning{
				Timestamp:   time.Now().Format("15:04:05.000"),
				Level:       SuggestionLevel,
				Type:        TURNWarning,
				Message:     "High TURN server utilization",
				Measurement: utilizationRate,
			}
		}
	}
}

func (cd *ConnectionDoctor) AnalyzeStats() {

	recent := cd.metrics.GetRecent(5) // Get last 5 measurements
	if len(recent) == 0 {
		return
	}

	latest := recent[0]

	// Check package loss rate issues
	cd.checkPackageLostRate(latest)

	// Check trends if we have enough data
	if len(recent) >= 3 {
		cd.analyzeTrends(recent)
	}

	// Check connection states
	cd.checkConnectionStates(latest)
}

func (cd *ConnectionDoctor) analyzeTrends(recent []QualityMetrics) {
	if len(recent) < 3 {
		return
	}

	// Calculate trends using exponential moving averages
	var (
		packetLossEMA  = calculateEMA(recent, func(m QualityMetrics) float64 { return m.PacketLossRate })
		rttEMA         = calculateEMA(recent, func(m QualityMetrics) float64 { return m.RTT.Seconds() })
		bitrateEMA     = calculateEMA(recent, func(m QualityMetrics) float64 { return float64(m.Bitrate) })
		jitterEMA      = calculateEMA(recent, func(m QualityMetrics) float64 { return m.JitterBuffer })
		wsLatencyEMA   = calculateEMA(recent, func(m QualityMetrics) float64 { return m.WSLatency.Seconds() })
		turnLatencyEMA = calculateEMA(recent, func(m QualityMetrics) float64 { return m.TURNLatency.Seconds() })
	)

	if bitrateEMA < float64(warningBitrate) {
		if bitrateEMA < float64(criticalBitrate) {
			cd.warnings <- Warning{
				Timestamp:   time.Now().Format("15:04:05.000"),
				Level:       CriticalLevel,
				Type:        BitrateWarning,
				Message:     fmt.Sprintf("Bitrate warning -- way too slow!: %.3f Kbps", bitrateEMA),
				Measurement: bitrateEMA,
			}
		} else {
			cd.warnings <- Warning{
				Timestamp:   time.Now().Format("15:04:05.000"),
				Level:       SuggestionLevel,
				Type:        BitrateWarning,
				Message:     fmt.Sprintf(" Bitrate is getting slow... dangerzone: %.3f Kbps", bitrateEMA),
				Measurement: bitrateEMA,
			}
		}
	}

	if jitterEMA > 0.1 { // 100ms jitter threshold
		cd.warnings <- Warning{
			Timestamp:   time.Now().Format("15:04:05.000"),
			Level:       SuggestionLevel,
			Type:        LatencyWarning,
			Message:     fmt.Sprintf("Sustained high jitter: %.2fms", jitterEMA*1000),
			Measurement: jitterEMA,
		}
	}

	if wsLatencyEMA > warningWSLatency {
		if wsLatencyEMA > criticalWSLatency {
			cd.warnings <- Warning{
				Timestamp:   time.Now().Format("15:04:05.000"),
				Level:       CriticalLevel,
				Type:        LatencyWarning,
				Message:     fmt.Sprintf("Sustained critically high latency: %.3fms", wsLatencyEMA*1000),
				Measurement: wsLatencyEMA,
			}
		} else {
			cd.warnings <- Warning{
				Timestamp:   time.Now().Format("15:04:05.000"),
				Level:       SuggestionLevel,
				Type:        LatencyWarning,
				Message:     fmt.Sprintf("Sustained borderline dangerous latency: %.3fms", wsLatencyEMA*1000),
				Measurement: wsLatencyEMA,
			}
		}
	}

	if turnLatencyEMA > warningTURNLatency {
		if turnLatencyEMA > criticalTURNLatency {
			cd.warnings <- Warning{
				Timestamp:   time.Now().Format("15:04:05.000"),
				Level:       CriticalLevel,
				Type:        TURNWarning,
				Message:     fmt.Sprintf("Sustained critically high TURN latency: %.3fms", turnLatencyEMA*1000),
				Measurement: turnLatencyEMA,
			}
		} else {
			cd.warnings <- Warning{
				Timestamp:   time.Now().Format("15:04:05.000"),
				Level:       SuggestionLevel,
				Type:        TURNWarning,
				Message:     fmt.Sprintf("Sustained borderline dangerous TURN latency: %.3fms", turnLatencyEMA*1000),
				Measurement: turnLatencyEMA,
			}
		}
	}

	vars, err := getVariances(recent, []string{"TURNLatency", "Bitrate", "PacketLoss", "RTT", "WSLatency"})
	if err != nil {
		log.Printf("Failed to calculate variances: %v", err)
	}

	// Detect trends and patterns
	cd.detectAnomalies(recent, packetLossEMA, rttEMA, wsLatencyEMA, vars)
	cd.detectPeriodicIssues()
	cd.analyzeNetworkStability()
	cd.analyzeInfrastructureHealth(recent[0])
}

func calculateEMA(metrics []QualityMetrics, getValue func(QualityMetrics) float64) float64 {
	const alpha = 0.2
	ema := getValue(metrics[0])

	for i := 1; i < len(metrics); i++ {
		value := getValue(metrics[i])
		ema = alpha*value + (1-alpha)*ema
	}

	return ema
}

func (cd *ConnectionDoctor) detectAnomalies(metrics []QualityMetrics,
	packetLossEMA, rttEMA, wsLatencyEMA float64, vars map[string]float64) {

	// Z-score threshold for anomaly detection
	const zScoreThreshold = 2.5

	for _, m := range metrics {
		// Calculate z-scores
		packetLossZScore := math.Abs((m.PacketLossRate - packetLossEMA) / math.Sqrt(vars["PacketLoss"]))
		rttZScore := math.Abs((m.RTT.Seconds() - rttEMA) / math.Sqrt(vars["RTT"]))
		wsLatencyZScore := math.Abs((m.WSLatency.Seconds() - wsLatencyEMA) / math.Sqrt(vars["WSLatency"]))

		if packetLossZScore > zScoreThreshold {
			cd.warnings <- Warning{
				Timestamp: time.Now().Format("15:04:05.000"),
				Level:     SuggestionLevel,
				Type:      PacketLossWarning,
				Message:   fmt.Sprintf("Anomalous packet loss detected: %.2f%%", m.PacketLossRate*100),
			}
		}

		if rttZScore > zScoreThreshold {
			cd.warnings <- Warning{
				Timestamp: time.Now().Format("15:04:05.000"),
				Level:     SuggestionLevel,
				Type:      LatencyWarning,
				Message:   fmt.Sprintf("Anomalous RTT detected: %v", m.RTT),
			}
		}

		if wsLatencyZScore > zScoreThreshold {
			cd.warnings <- Warning{
				Timestamp: time.Now().Format("15:04:05.000"),
				Level:     SuggestionLevel,
				Type:      LatencyWarning,
				Message:   fmt.Sprintf("Anomalous websocket latency detected: %v", m.WSLatency.Seconds()),
			}
		}
	}
}

// calculateVariance calculates the variance for a slice of float64 values.
func calculateVariance(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Calculate mean
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	// Calculate variance
	var variance float64
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	return variance / float64(len(values))
}

// getVariances calculates variances for multiple fields.
func getVariances(m []QualityMetrics, fields []string) (map[string]float64, error) {
	// Define a mapping of field names to accessor functions.
	fieldAccessors := map[string]func(QualityMetrics) float64{
		"TURNLatency": func(m QualityMetrics) float64 { return float64(m.TURNLatency.Milliseconds()) },
		"Bitrate":     func(m QualityMetrics) float64 { return float64(m.Bitrate) },
		"PacketLoss":  func(m QualityMetrics) float64 { return m.PacketLossRate },
		"RTT":         func(m QualityMetrics) float64 { return m.RTT.Seconds() },
		"WSLatency":   func(m QualityMetrics) float64 { return m.WSLatency.Seconds() },
	}

	variances := make(map[string]float64)

	// Iterate over requested fields.
	for _, field := range fields {
		accessor, ok := fieldAccessors[field]
		if !ok {
			return nil, fmt.Errorf("unknown field: %s", field)
		}

		// Extract values using the accessor function.
		values := make([]float64, len(m))
		for i, metric := range m {
			values[i] = accessor(metric)
		}

		// Calculate and store the variance.
		variances[field] = calculateVariance(values)
	}

	return variances, nil
}
