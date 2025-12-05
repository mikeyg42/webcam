package rtcManager

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/mikeyg42/webcam/internal/quality"
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

	// Quality management for adaptive streaming
	qualityManager *quality.QualityManager

	// Shutdown synchronization
	shutdownOnce sync.Once
	isShutdown   atomic.Bool
}

// GetQualityManager returns the quality manager for callback wiring
func (cd *ConnectionDoctor) GetQualityManager() *quality.QualityManager {
	return cd.qualityManager
}

type Warning struct {
	Level       WarningLevel
	Type        WarningType
	Message     string
	Timestamp   time.Time // Changed from string to time.Time for efficiency
	Measurement float64   // Optional measurement associated with warning
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

	// Tailscale specific metrics (could add ping RTT here later)
	TailscaleConnected bool
	TailscaleIP        string
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
				// OBSOLETE: mediadevices constraint adjustment removed
				// Quality adaptation is now handled by QualityManager via GStreamer SetBitrate()
				log.Printf("[ConnectionDoctor] Critical %s warning (frequency: %d) - handled by QualityManager",
					warning.Type, warningFrequency)
			case SignalingWarning:
				if warningFrequency >= 2 {
					m.handleDisconnection()
				}
			}

		case SuggestionLevel:
			switch warning.Type {
			case PacketLossWarning, BitrateWarning, LatencyWarning:
				// OBSOLETE: mediadevices constraint adjustment removed
				// Quality adaptation is now handled by QualityManager via GStreamer SetBitrate()
				log.Printf("[ConnectionDoctor] Suggestion %s warning (frequency: %d) - handled by QualityManager",
					warning.Type, warningFrequency)
			default:
				// Log for monitoring but don't take action yet
				log.Printf("Monitoring suggestion: %s (count: %d)",
					warning.Message, warningFrequency)
			}

		case InfoLevel:
			// OBSOLETE: mediadevices constraint "relaxing" removed
			// Quality adaptation is bidirectional in QualityManager (can upgrade when conditions improve)
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
			if w.Type == warningType && w.Timestamp.After(cutoff) {
				count++
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

// OBSOLETE FUNCTIONS REMOVED: resetMediaConstraints() and adjustMediaConstraints()
// These used mediadevices VP9 encoding which has been replaced by GStreamer H.264/H.265 encoding.
// Quality adaptation is now handled by QualityManager calling GStreamer's SetBitrate() method.
// See internal/quality/manager.go for the new adaptive streaming implementation.

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
			if w.Level == CriticalLevel && w.Timestamp.After(lastCritical) {
				lastCritical = w.Timestamp
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
		if w.Timestamp.After(cutoff) {
			filtered = append(filtered, w)
		}
	}
	wt.warnings[key] = filtered
}

//----------

func (m *Manager) NewConnectionDoctor(ctx context.Context) *ConnectionDoctor {
	CDctx, cancel := context.WithCancel(ctx)

	// Initialize QualityManager with detected device capability
	// Note: Hardware encoder detection will be updated after GStreamer pipeline is created
	hasHWEncoder := quality.DetectHardwareEncoder() // OS-based heuristic
	deviceCap := quality.DetectDeviceCapability(
		m.config.Video.Width,
		m.config.Video.Height,
		m.config.Video.FrameRate,
		hasHWEncoder,
	)

	// Parse priority from config, default to maximize quality if invalid
	priority := quality.PriorityMaximizeQuality
	if m.config.WebRTC.QualityPriority != "" {
		if parsed, err := quality.ParsePriority(m.config.WebRTC.QualityPriority); err == nil {
			priority = parsed
		} else {
			log.Printf("[ConnectionDoctor] Invalid quality priority '%s', using default 'maximize_quality': %v",
				m.config.WebRTC.QualityPriority, err)
		}
	}

	qm := quality.NewQualityManager(priority, deviceCap)

	log.Printf("[ConnectionDoctor] Quality manager initialized: priority=%s, resolution=%dx%d@%dfps",
		priority.String(), deviceCap.MaxResolution.Width, deviceCap.MaxResolution.Height, deviceCap.MaxFrameRate)

	return &ConnectionDoctor{
		manager:        m,
		ctx:            CDctx,
		cancel:         cancel,
		metrics:        NewCircularMetricsBuffer(circularBufferCapacity),
		done:           make(chan struct{}),
		warnings:       make(chan Warning, 100), // Buffered to prevent blocking during shutdown
		qualityManager: qm,
	}
}

// Shutdown gracefully shuts down the ConnectionDoctor and releases resources
func (cd *ConnectionDoctor) Shutdown() {
	cd.shutdownOnce.Do(func() {
		log.Println("[ConnectionDoctor] Initiating shutdown...")
		cd.isShutdown.Store(true)

		// Cancel context first to signal all goroutines to stop
		cd.cancel()

		// Wait a brief moment for goroutines to drain
		time.Sleep(100 * time.Millisecond)

		// Now safe to close channels
		close(cd.warnings)
		close(cd.done)

		log.Println("[ConnectionDoctor] Shutdown complete")
	})
}

// sendWarning safely sends a warning, checking shutdown status first
func (cd *ConnectionDoctor) sendWarning(w Warning) {
	// Don't send if shutdown is in progress
	if cd.isShutdown.Load() {
		return
	}

	// Use non-blocking send with timeout
	select {
	case cd.warnings <- w:
		// Successfully sent
	case <-cd.ctx.Done():
		// Context cancelled, don't block
		return
	case <-time.After(10 * time.Millisecond):
		// Channel blocked for too long, drop the warning
		log.Printf("[ConnectionDoctor] Warning channel blocked, dropping: %s", w.Message)
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
	// Check if PeerConnection exists first
	if cd.manager.PeerConnection == nil {
		return QualityMetrics{
			Timestamp: time.Now(),
		}
	}

	// Safely get DTLS state with nil checks
	var dtlsState webrtc.DTLSTransportState
	if sctp := cd.manager.PeerConnection.SCTP(); sctp != nil {
		if transport := sctp.Transport(); transport != nil {
			dtlsState = transport.State()
		}
	}

	metrics := QualityMetrics{
		Timestamp:      time.Now(),
		ICEState:       cd.manager.PeerConnection.ICEConnectionState(),
		DTLSState:      dtlsState,
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
				log.Printf("[@%s] Local ICE Candidate: %s (%s)", metrics.Timestamp.Format(time.TimeOnly), stat.Protocol, stat.CandidateType)
			} else {
				log.Printf("[@%s] Remote ICE Candidate: %s (%s)", metrics.Timestamp.Format(time.TimeOnly), stat.Protocol, stat.CandidateType)
			}

		case *webrtc.DataChannelStats:
			// Monitor data channel health
			if stat.State != webrtc.DataChannelStateOpen {
				select {
				case cd.warnings <- Warning{
					Timestamp: metrics.Timestamp,
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
	if len(recent) == 0 {
		// No previous stats, skip rate calculation
		return
	}
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
			Timestamp:   time.Now(),
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
			Timestamp:   time.Now(),
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
			Timestamp:   time.Now(),
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
					Timestamp: time.Now(),
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
	// Early exit if shutdown is in progress to prevent sending on closed channels
	if cd.isShutdown.Load() {
		return
	}

	if cd.manager.wsConnection == nil {
		return
	}

	// Measure WebSocket latency with ping
	start := time.Now()
	err := cd.manager.wsConnection.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second))
	if err != nil {
		metrics.WSState = "Error"
		// Check shutdown again before trying to send
		if cd.isShutdown.Load() {
			return
		}
		select {
		case cd.warnings <- Warning{
			Timestamp: time.Now(),
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
	// NOTE: Disabled because ion-sfu doesn't implement a standard "ping" method
	// The connection is monitored through WebSocket health checks instead
	if cd.manager.rpcConn != nil {
		// Skip JSON-RPC health check - not supported by ion-sfu
		// WebSocket health is sufficient for monitoring
		metrics.JSONRPCLatency = 0
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

func (cd *ConnectionDoctor) checkPackageLostRate(metrics QualityMetrics) {

	// Packet Loss
	if metrics.PacketLossRate >= criticalPacketLoss {
		cd.sendWarning(Warning{
			Timestamp:   time.Now(),
			Level:       CriticalLevel,
			Type:        PacketLossWarning,
			Message:     fmt.Sprintf("Critical packet loss: %.2f%%", metrics.PacketLossRate*100),
			Measurement: metrics.PacketLossRate,
		})
	} else if metrics.PacketLossRate >= warningPacketLoss {
		cd.sendWarning(Warning{
			Timestamp:   time.Now(),
			Level:       SuggestionLevel,
			Type:        PacketLossWarning,
			Message:     fmt.Sprintf("High packet loss: %.2f%%", metrics.PacketLossRate*100),
			Measurement: metrics.PacketLossRate,
		})
	}
}

func (cd *ConnectionDoctor) checkConnectionStates(metrics QualityMetrics) {
	// Check ICE State
	if metrics.ICEState == webrtc.ICEConnectionStateFailed {
		select {
		case cd.warnings <- Warning{
			Timestamp: time.Now(),
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
		cd.sendWarning(Warning{
			Timestamp: time.Now(),
			Level:     CriticalLevel,
			Type:      SignalingWarning,
			Message:   "DTLS transport failed",
		})
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
		cd.sendWarning(Warning{
			Timestamp:   time.Now(),
			Level:       SuggestionLevel,
			Type:        LatencyWarning,
			Message:     "Detected periodic network issues",
			Measurement: packetLossPattern.period,
		})
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
		cd.sendWarning(Warning{
			Timestamp:   time.Now(),
			Level:       SuggestionLevel,
			Type:        BitrateWarning,
			Message:     fmt.Sprintf("Network instability detected (score: %.2f)", stabilityScore),
			Measurement: stabilityScore,
		})
	}
}
func (cd *ConnectionDoctor) analyzeInfrastructureHealth(metrics QualityMetrics) {
	// Check WebSocket connection health
	if metrics.WSLatency > criticalWSLatency {
		cd.sendWarning(Warning{
			Timestamp:   time.Now(),
			Level:       CriticalLevel,
			Type:        SignalingWarning,
			Message:     fmt.Sprintf("WebSocket latency critical: %v", metrics.WSLatency),
			Measurement: metrics.WSLatency.Seconds(),
		})
	}
}

// estimateBandwidth calculates available bandwidth based on recent throughput
// Does NOT factor in loss - that's handled by the PID controller to avoid double-counting
func (cd *ConnectionDoctor) estimateBandwidth(recent []QualityMetrics) int {
	if len(recent) == 0 {
		return 0
	}

	// Calculate average bitrate from recent measurements
	var totalBitrate uint64
	for _, m := range recent {
		totalBitrate += m.Bitrate
	}
	avgBitrate := totalBitrate / uint64(len(recent))

	// Simple throughput-based estimate with conservative headroom
	// The PID controller will handle loss/congestion signals separately
	availableBandwidth := float64(avgBitrate)

	// Only add headroom if we're consistently achieving good throughput
	if len(recent) >= 3 {
		// Check if bitrate is stable/growing (not dropping)
		stable := true
		for i := 1; i < len(recent); i++ {
			if recent[i].Bitrate < recent[i-1].Bitrate*9/10 { // >10% drop
				stable = false
				break
			}
		}
		if stable {
			availableBandwidth *= 1.1 // Add 10% headroom when stable
		}
	}

	// Convert from bps to Kbps
	return int(availableBandwidth / 1000)
}

// calculateBufferHealth computes overall buffer health score (0.0-1.0)
func (cd *ConnectionDoctor) calculateBufferHealth(recent []QualityMetrics) float64 {
	if len(recent) == 0 {
		return 1.0 // Assume healthy if no data
	}

	latest := recent[0]

	// Jitter score (40% weight)
	// Ideal jitter is 0, critical is 100ms
	jitterScore := 1.0 - math.Min(latest.JitterBuffer/0.1, 1.0)

	// Packet loss score (40% weight)
	// Ideal is 0%, critical is 5%
	lossScore := 1.0 - math.Min(latest.PacketLossRate/0.05, 1.0)

	// Framerate stability score (20% weight)
	// Calculate variance of recent framerates
	framerateVariance := 0.0
	if len(recent) >= 3 {
		var sum float64
		for _, m := range recent {
			sum += m.Framerate
		}
		mean := sum / float64(len(recent))

		var variance float64
		for _, m := range recent {
			diff := m.Framerate - mean
			variance += diff * diff
		}
		variance /= float64(len(recent))
		framerateVariance = math.Sqrt(variance)
	}

	// Ideal variance is 0, critical is 5fps deviation
	framerateScore := 1.0 - math.Min(framerateVariance/5.0, 1.0)

	// Weighted combination
	bufferHealth := (jitterScore * 0.4) + (lossScore * 0.4) + (framerateScore * 0.2)

	return math.Max(0.0, math.Min(1.0, bufferHealth))
}

func (cd *ConnectionDoctor) AnalyzeStats() {

	recent := cd.metrics.GetRecent(5) // Get last 5 measurements
	if len(recent) == 0 {
		return
	}

	latest := recent[0]

	// Update quality manager with current network state
	if cd.qualityManager != nil {
		networkState := quality.NetworkState{
			AvailableBandwidth: cd.estimateBandwidth(recent),
			PacketLoss:         latest.PacketLossRate,
			RTT:                latest.RTT,
			JitterBuffer:       latest.JitterBuffer,
			BurstLossRate:      latest.BurstLossRate,
			Framerate:          latest.Framerate,
			FramerateVariance:  cd.calculateFramerateVariance(recent),
			NACKCount:          latest.Network.Nacks,
			PLICount:           latest.Network.Plis,
			FIRCount:           latest.Network.Firs,
			WSLatency:          latest.WSLatency,
			CurrentBitrate:     int(latest.Bitrate / 1000), // Convert to Kbps
			CurrentResolution: quality.Resolution{
				Width:  cd.manager.config.Video.Width,
				Height: cd.manager.config.Video.Height,
			},
			CurrentFramerate: cd.manager.config.Video.FrameRate,
			Timestamp:        time.Now(),
		}

		cd.qualityManager.UpdateNetworkState(networkState)
	}

	// Check package loss rate issues
	cd.checkPackageLostRate(latest)

	// Check trends if we have enough data
	if len(recent) >= 3 {
		cd.analyzeTrends(recent)
	}

	// Check connection states
	cd.checkConnectionStates(latest)
}

// calculateFramerateVariance computes the variance in framerate
func (cd *ConnectionDoctor) calculateFramerateVariance(recent []QualityMetrics) float64 {
	if len(recent) < 2 {
		return 0.0
	}

	var sum float64
	for _, m := range recent {
		sum += m.Framerate
	}
	mean := sum / float64(len(recent))

	var variance float64
	for _, m := range recent {
		diff := m.Framerate - mean
		variance += diff * diff
	}
	variance /= float64(len(recent))

	return math.Sqrt(variance)
}

func (cd *ConnectionDoctor) analyzeTrends(recent []QualityMetrics) {
	if len(recent) < 3 {
		return
	}

	// Calculate trends using exponential moving averages
	var (
		packetLossEMA = calculateEMA(recent, func(m QualityMetrics) float64 { return m.PacketLossRate })
		rttEMA        = calculateEMA(recent, func(m QualityMetrics) float64 { return m.RTT.Seconds() })
		bitrateEMA    = calculateEMA(recent, func(m QualityMetrics) float64 { return float64(m.Bitrate) })
		jitterEMA     = calculateEMA(recent, func(m QualityMetrics) float64 { return m.JitterBuffer })
		wsLatencyEMA  = calculateEMA(recent, func(m QualityMetrics) float64 { return m.WSLatency.Seconds() })
	)

	if bitrateEMA < float64(warningBitrate) {
		if bitrateEMA < float64(criticalBitrate) {
			cd.warnings <- Warning{
				Timestamp:   time.Now(),
				Level:       CriticalLevel,
				Type:        BitrateWarning,
				Message:     fmt.Sprintf("Bitrate warning -- way too slow!: %.3f Kbps", bitrateEMA),
				Measurement: bitrateEMA,
			}
		} else {
			cd.warnings <- Warning{
				Timestamp:   time.Now(),
				Level:       SuggestionLevel,
				Type:        BitrateWarning,
				Message:     fmt.Sprintf(" Bitrate is getting slow... dangerzone: %.3f Kbps", bitrateEMA),
				Measurement: bitrateEMA,
			}
		}
	}

	if jitterEMA > 0.1 { // 100ms jitter threshold
		cd.sendWarning(Warning{
			Timestamp:   time.Now(),
			Level:       SuggestionLevel,
			Type:        LatencyWarning,
			Message:     fmt.Sprintf("Sustained high jitter: %.2fms", jitterEMA*1000),
			Measurement: jitterEMA,
		})
	}

	if wsLatencyEMA > warningWSLatency {
		if wsLatencyEMA > criticalWSLatency {
			cd.sendWarning(Warning{
				Timestamp:   time.Now(),
				Level:       CriticalLevel,
				Type:        LatencyWarning,
				Message:     fmt.Sprintf("Sustained critically high latency: %.3fms", wsLatencyEMA*1000),
				Measurement: wsLatencyEMA,
			})
		} else {
			cd.sendWarning(Warning{
				Timestamp:   time.Now(),
				Level:       SuggestionLevel,
				Type:        LatencyWarning,
				Message:     fmt.Sprintf("Sustained borderline dangerous latency: %.3fms", wsLatencyEMA*1000),
				Measurement: wsLatencyEMA,
			})
		}
	}

	vars, err := getVariances(recent, []string{"Bitrate", "PacketLoss", "RTT", "WSLatency"})
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
				Timestamp: time.Now(),
				Level:     SuggestionLevel,
				Type:      PacketLossWarning,
				Message:   fmt.Sprintf("Anomalous packet loss detected: %.2f%%", m.PacketLossRate*100),
			}
		}

		if rttZScore > zScoreThreshold {
			cd.warnings <- Warning{
				Timestamp: time.Now(),
				Level:     SuggestionLevel,
				Type:      LatencyWarning,
				Message:   fmt.Sprintf("Anomalous RTT detected: %v", m.RTT),
			}
		}

		if wsLatencyZScore > zScoreThreshold {
			cd.warnings <- Warning{
				Timestamp: time.Now(),
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
		"Bitrate":    func(m QualityMetrics) float64 { return float64(m.Bitrate) },
		"PacketLoss": func(m QualityMetrics) float64 { return m.PacketLossRate },
		"RTT":        func(m QualityMetrics) float64 { return m.RTT.Seconds() },
		"WSLatency":  func(m QualityMetrics) float64 { return m.WSLatency.Seconds() },
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
