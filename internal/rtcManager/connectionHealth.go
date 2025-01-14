package rtcManager

import (
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	warningPacketLoss         = 0.05 // 5%
	criticalPacketLoss        = 0.15 // 15%
	warningRTT                = 200 * time.Millisecond
	criticalRTT               = 500 * time.Millisecond
	minAcceptableFramerate    = 10.0 // fps
	metricsCollectionInterval = 3 * time.Second
	circularBufferCapacity    = 10
	statsHistoryDuration      = 30 * time.Second
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
	Timestamp   time.Time
	Measurement float64 // Optional measurement associated with warning
}

type WarningLevel int

const (
	InfoLevel       WarningLevel = iota
	SuggestionLevel              // changed warningLevel to suggestion level because we already have type warningLevel - compiler things we are redeclarinkg
	CriticalLevel
)

type WarningType int

const (
	PacketLossWarning WarningType = iota
	LatencyWarning
	BitrateWarning
	FramerateWarning
	ICEWarning
	STUNWarning
	TURNWarning
	SignalingWarning
)

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
	SelectedCandidate *webrtc.ICECandidatePair
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
	WSLatency   time.Duration
	WSState     string
	RPCFailures uint32

	// TURN specific metrics
	TURNLatency          time.Duration
	TURNState            string
	RelayedBytes         uint64
	NumActiveConnections int
}

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
				qualityMetrics := cd.AccumulateStats()
				cd.metrics.Add(qualityMetrics)
			}
		}
	}()
}

func (cd *ConnectionDoctor) AccumulateStats() {
	metrics := QualityMetrics{
		Timestamp:      time.Now(),
		ICEState:       cd.manager.PeerConnection.ICEConnectionState(),
		SignalingState: cd.manager.PeerConnection.SignalingState(),
	}

	// Collect WebRTC stats
	stats := cd.manager.PeerConnection.GetStats()
	for _, s := range stats {
		switch stat := s.(type) {
		case *webrtc.InboundRTPStreamStats:
			cd.processInboundStats(&metrics, stat)

		case *webrtc.RemoteInboundRTPStreamStats:
			metrics.RTT = time.Duration(stat.RoundTripTime * float64(time.Second))
			metrics.Network.RTCPReceived++

		case *webrtc.TransportStats:
			cd.processTransportStats(&metrics, stat)

		case *webrtc.ICECandidatePairStats:
			if stat.State == webrtc.ICECandidatePairStateSucceeded {
				metrics.Transport.SelectedCandidate = &webrtc.ICECandidatePair{
					Local:  &webrtc.ICECandidate{},
					Remote: &webrtc.ICECandidate{},
				}
				// Fill in candidate details...
			}

		case *webrtc.RTCPFeedbackStats:
			cd.processRTCPStats(&metrics, stat)
		}
	}

	// Collect TURN metrics
	if cd.manager.turnServer != nil {
		cd.collectTURNMetrics(&metrics)
		metrics.NumActiveConnections := cd.manager.turnServer.GetActiveConnections()
	}

	// Collect WebSocket/JSON-RPC metrics
	cd.collectWSMetrics(&metrics)

	cd.metrics.Add(metrics)
	return
}

func (cd *ConnectionDoctor) processInboundStats(metrics *QualityMetrics, stat *webrtc.InboundRTPStreamStats) {
	if stat.Kind == "video" {
		metrics.Framerate = float64(stat.FramesDecoded)
		metrics.Resolution = fmt.Sprintf("%dx%d", stat.FrameWidth, stat.FrameHeight)

		// Track frame decode times
		if stat.FramesDecoded > 0 {
			avgDecodeTime := stat.TotalDecodeTime / float64(stat.FramesDecoded)
			if avgDecodeTime > 0.033 { // More than 33ms per frame
				cd.warnings <- Warning{
					Level:   WarningLevel,
					Type:    FramerateWarning,
					Message: fmt.Sprintf("High decode time: %.2fms", avgDecodeTime*1000),
				}
			}
		}
	}
	metrics.TrackID = stat.TrackID
	metrics.BurstLossRate = stat.BurstLossRate
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
	metrics.Transport.PacketsSent = stat.PacketsSent
	metrics.Transport.PacketsReceived = stat.PacketsReceived

	// Check for congestion
	if stat.BytesSent > 0 {
		congestionWindow := float64(stat.CongestionWindow)
		bytesInFlight := float64(stat.BytesInFlight)
		if bytesInFlight/congestionWindow > 0.9 {
			cd.warnings <- Warning{
				Level:   WarningLevel,
				Type:    BitrateWarning,
				Message: "Network congestion detected",
			}
		}
	}
}

func (cd *ConnectionDoctor) AnalyzeStats() {
	const (
		criticalPacketLoss = 0.15 // 15%
		warningPacketLoss  = 0.05 // 5%
		criticalRTT        = 500 * time.Millisecond
		warningRTT         = 200 * time.Millisecond
		minFramerate       = 15.0
		minBitrate         = 100000 // 100 Kbps
	)

	recent := cd.metrics.GetRecent(5) // Get last 5 measurements
	if len(recent) == 0 {
		return
	}

	latest := recent[0]

	// Check immediate issues
	cd.checkImmediateIssues(latest, criticalPacketLoss, warningPacketLoss,
		criticalRTT, warningRTT, minFramerate, minBitrate)

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

	// Calculate variances
	packetLossVariance := calculateVariance(recent, func(m QualityMetrics) float64 { return m.PacketLossRate })
	rttVariance := calculateVariance(recent, func(m QualityMetrics) float64 { return m.RTT.Seconds() })

	// Detect trends and patterns
	cd.detectAnomalies(recent, packetLossEMA, rttEMA, packetLossVariance, rttVariance)
	cd.detectPeriodicIssues(recent)
	cd.analyzeNetworkStability(recent, bitrateEMA, jitterEMA)
	cd.analyzeInfrastructureHealth(recent, wsLatencyEMA, turnLatencyEMA)
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
	packetLossEMA, rttEMA, packetLossVar, rttVar float64) {

	// Z-score threshold for anomaly detection
	const zScoreThreshold = 2.5

	for _, m := range metrics {
		// Calculate z-scores
		packetLossZScore := math.Abs((m.PacketLossRate - packetLossEMA) / math.Sqrt(packetLossVar))
		rttZScore := math.Abs((m.RTT.Seconds() - rttEMA) / math.Sqrt(rttVar))

		if packetLossZScore > zScoreThreshold {
			cd.warnings <- Warning{
				Level:   WarningLevel,
				Type:    PacketLossWarning,
				Message: fmt.Sprintf("Anomalous packet loss detected: %.2f%%", m.PacketLossRate*100),
			}
		}

		if rttZScore > zScoreThreshold {
			cd.warnings <- Warning{
				Level:   WarningLevel,
				Type:    LatencyWarning,
				Message: fmt.Sprintf("Anomalous RTT detected: %v", m.RTT),
			}
		}
	}
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
		cd.warnings <- Warning{
			Level:   WarningLevel,
			Type:    SignalingWarning,
			Message: fmt.Sprintf("WebSocket ping failed: %v", err),
		}
		return
	}

	metrics.WSLatency = time.Since(start)
	metrics.WSState = "Connected"

	// Check JSON-RPC health
	if cd.manager.rpcConn != nil {
		// Implement JSON-RPC health check
		// This could involve sending a ping method or checking recent request success rates
	}
}

func (cd *ConnectionDoctor) collectTURNMetrics(metrics *QualityMetrics) {
	if cd.manager.turnServer == nil {
		return
	}

	// Measure TURN server latency
	start := time.Now()
	// Implement TURN server health check
	// This could involve sending an Allocation request or checking existing allocation
	metrics.TURNLatency = time.Since(start)

	// Collect additional TURN metrics
	metrics.RelayedBytes = cd.manager.turnServer.GetRelayedBytes()
	metrics.TURNState = cd.manager.turnServer.GetState()
}

func (cd *ConnectionDoctor) checkImmediateIssues(metrics QualityMetrics,
	criticalPacketLoss, warningPacketLoss float64,
	criticalRTT, warningRTT time.Duration,
	minFramerate float64, minBitrate uint64) {

	// Packet Loss
	if metrics.PacketLossRate >= criticalPacketLoss {
		cd.warnings <- Warning{
			Level:       CriticalLevel,
			Type:        PacketLossWarning,
			Message:     fmt.Sprintf("Critical packet loss: %.2f%%", metrics.PacketLossRate*100),
			Timestamp:   metrics.Timestamp,
			Measurement: metrics.PacketLossRate,
		}
	} else if metrics.PacketLossRate >= warningPacketLoss {
		cd.warnings <- Warning{
			Level:       WarningLevel,
			Type:        PacketLossWarning,
			Message:     fmt.Sprintf("High packet loss: %.2f%%", metrics.PacketLossRate*100),
			Timestamp:   metrics.Timestamp,
			Measurement: metrics.PacketLossRate,
		}
	}

	// Similar checks for RTT, Framerate, and Bitrate...
}

func (cd *ConnectionDoctor) analyzeTrends(recent []QualityMetrics) {
	// Calculate trends using your existing trend calculation methods
	trend := calculateStatsTrend(recent)

	if trend.PacketLossIncreasing {
		cd.warnings <- Warning{
			Level:   WarningLevel,
			Type:    PacketLossWarning,
			Message: "Increasing packet loss trend detected",
		}
	}
	// Similar checks for other trends...
}

func (cd *ConnectionDoctor) checkConnectionStates(metrics QualityMetrics) {
	// Check ICE State
	if metrics.ICEState == webrtc.ICEConnectionStateFailed {
		cd.warnings <- Warning{
			Level:   CriticalLevel,
			Type:    ICEWarning,
			Message: "ICE connection failed",
		}
	}

	// Check DTLS State
	if metrics.DTLSState == webrtc.DTLSTransportStateFailed {
		cd.warnings <- Warning{
			Level:   CriticalLevel,
			Type:    SignalingWarning,
			Message: "DTLS transport failed",
		}
	}
}

func (m *Manager) RespondToDoctorsWarnings() {
	for warning := range m.ConnectionDoctor.warnings {
		log.Printf("[ConnectionDoctor] %s: %s", warning.Level, warning.Message)

		switch warning.Level {
		case CriticalLevel:
			switch warning.Type {
			case ICEWarning:
				m.handleConnectionFailure()
			case PacketLossWarning, LatencyWarning:
				m.adjustMediaConstraints(true) // aggressive mode
			case SignalingWarning:
				m.handleDisconnection()
			}

		case WarningLevel:
			switch warning.Type {
			case PacketLossWarning, BitrateWarning:
				m.adjustMediaConstraints(false) // moderate adjustments
			case STUNWarning, TURNWarning:
				// do stuff
			}
		}
	}
}
