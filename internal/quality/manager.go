// Package quality provides intelligent video quality management with adaptive control
package quality

import (
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"
)

// QualityPriority represents user's preference for video quality behavior
type QualityPriority int

const (
	PriorityMaximizeQuality QualityPriority = iota
	PriorityMinimizeLatency
	PriorityMinimizeDeviceStrain
)

func (p QualityPriority) String() string {
	switch p {
	case PriorityMaximizeQuality:
		return "maximize-quality"
	case PriorityMinimizeLatency:
		return "minimize-latency"
	case PriorityMinimizeDeviceStrain:
		return "minimize-device-strain"
	default:
		return "unknown"
	}
}

// ParsePriority converts string to QualityPriority
// Accepts both hyphenated and underscore formats for compatibility
func ParsePriority(s string) (QualityPriority, error) {
	switch s {
	case "maximize-quality", "maximize_quality":
		return PriorityMaximizeQuality, nil
	case "minimize-latency", "minimize_latency":
		return PriorityMinimizeLatency, nil
	case "minimize-device-strain", "minimize_device_strain":
		return PriorityMinimizeDeviceStrain, nil
	default:
		return 0, fmt.Errorf("invalid priority: %s", s)
	}
}

// Resolution represents video dimensions
type Resolution struct {
	Width  int
	Height int
}

func (r Resolution) String() string {
	return fmt.Sprintf("%dx%d", r.Width, r.Height)
}

func (r Resolution) Pixels() int {
	return r.Width * r.Height
}

// BitrateRange defines min/max bitrate for a quality profile
type BitrateRange struct {
	Min int // Kbps
	Max int // Kbps
}

// DeviceTier estimates device processing capability
type DeviceTier int

const (
	DeviceTierLow DeviceTier = iota
	DeviceTierMedium
	DeviceTierHigh
)

func (d DeviceTier) String() string {
	switch d {
	case DeviceTierLow:
		return "low"
	case DeviceTierMedium:
		return "medium"
	case DeviceTierHigh:
		return "high"
	default:
		return "unknown"
	}
}

// QualityProfile defines resolution-bitrate-framerate combinations
type QualityProfile struct {
	Name         string
	Resolution   Resolution
	FrameRate    int
	BitrateRange BitrateRange
	MinDeviceTier DeviceTier
}

// NetworkState captures real-time network conditions
// Every metric feeds into negative feedback control loops
type NetworkState struct {
	// Primary control metrics
	AvailableBandwidth int     // Kbps - estimated from current bitrate + headroom
	PacketLoss         float64 // 0.0-1.0 - packet loss rate
	RTT                time.Duration // Round-trip time

	// Buffer and jitter metrics
	JitterBuffer  float64 // Seconds - receiver buffer depth
	BurstLossRate uint64  // Packets lost in bursts (indicates congestion)

	// Framerate stability
	Framerate        float64 // Current fps
	FramerateVariance float64 // Variance in fps (instability indicator)

	// RTCP feedback metrics (control signals from receiver)
	NACKCount uint32 // Negative acknowledgments (retransmission requests)
	PLICount  uint32 // Picture Loss Indication (keyframe requests)
	FIRCount  uint32 // Full Intra Request (decoder errors)

	// WebSocket health (signaling path)
	WSLatency time.Duration // Signaling latency

	// Encoder feedback
	CurrentBitrate     int // Kbps - actual encoder output
	CurrentResolution  Resolution
	CurrentFramerate   int

	Timestamp time.Time
}

// ControlSignals represent the output of feedback control system
type ControlSignals struct {
	// Control outputs
	TargetBitrate    int // Kbps
	TargetResolution Resolution
	TargetFramerate  int

	// Control state
	Reason           string  // Why this adjustment was made
	ConfidenceScore  float64 // 0.0-1.0 - confidence in this decision
	UrgencyLevel     int     // 0-3 (higher = more urgent)
}

// DeviceCapability describes device encoding capabilities
type DeviceCapability struct {
	Tier             DeviceTier
	HardwareEncoder  bool
	MaxResolution    Resolution
	MaxFrameRate     int
	SupportsDynamicBitrate bool
}

// AdjustmentRecord tracks quality changes for oscillation detection
type AdjustmentRecord struct {
	Timestamp       time.Time
	FromBitrate     int
	ToBitrate       int
	FromResolution  Resolution
	ToResolution    Resolution
	Reason          string
	NetworkState    NetworkState
}

// FeedbackController implements PID-like control for quality adaptation
type FeedbackController struct {
	// Proportional: react to current error
	// Integral: correct for sustained errors
	// Derivative: predict future errors

	targetBandwidthUtilization float64 // Target % of available bandwidth to use

	// Error accumulators (integral terms)
	bandwidthErrorIntegral float64
	lossErrorIntegral      float64
	latencyErrorIntegral   float64

	// Previous errors (derivative terms)
	prevBandwidthError float64
	prevLossError      float64
	prevLatencyError   float64

	// Control gains (tuning parameters)
	kpBandwidth float64 // Proportional gain for bandwidth
	kiBandwidth float64 // Integral gain for bandwidth
	kdBandwidth float64 // Derivative gain for bandwidth

	kpLoss float64
	kiLoss float64
	kdLoss float64

	kpLatency float64
	kiLatency float64
	kdLatency float64

	lastUpdate time.Time
}

func NewFeedbackController() *FeedbackController {
	return &FeedbackController{
		targetBandwidthUtilization: 0.80, // Use 80% of available bandwidth

		// Bandwidth control (aggressive response to capacity)
		kpBandwidth: 0.5,
		kiBandwidth: 0.1,
		kdBandwidth: 0.3,

		// Loss control (very aggressive - packet loss is critical)
		kpLoss: 1.0,
		kiLoss: 0.5,
		kdLoss: 0.8,

		// Latency control (moderate response)
		kpLatency: 0.4,
		kiLatency: 0.2,
		kdLatency: 0.3,

		lastUpdate: time.Now(),
	}
}

// Reset clears integral/derivative state to prevent carrying stale errors across operating point changes
func (fc *FeedbackController) Reset() {
	fc.bandwidthErrorIntegral = 0
	fc.lossErrorIntegral = 0
	fc.latencyErrorIntegral = 0
	fc.prevBandwidthError = 0
	fc.prevLossError = 0
	fc.prevLatencyError = 0
	fc.lastUpdate = time.Now()
}

// CalculateControl computes control output based on multiple feedback loops
// Uses fixed dt=1.0 for consistent tuning regardless of call frequency
func (fc *FeedbackController) CalculateControl(state NetworkState, currentBitrate int) float64 {
	// Fixed time step for consistent tuning
	// Actual call frequency is gated by cooldown logic, so using wall-clock time
	// would make integral/derivative terms unpredictable
	const dt = 1.0
	fc.lastUpdate = time.Now()

	// ===== BANDWIDTH FEEDBACK LOOP =====
	// Error: how much bandwidth are we under/over-using?
	targetBandwidthUsage := float64(state.AvailableBandwidth) * fc.targetBandwidthUtilization
	bandwidthError := targetBandwidthUsage - float64(currentBitrate)

	// Integral term (accumulated error)
	fc.bandwidthErrorIntegral += bandwidthError * dt

	// Anti-windup: clamp integral
	fc.bandwidthErrorIntegral = clamp(fc.bandwidthErrorIntegral, -5000, 5000)

	// Derivative term (rate of change)
	bandwidthDerivative := (bandwidthError - fc.prevBandwidthError) / dt
	fc.prevBandwidthError = bandwidthError

	// PID output for bandwidth
	bandwidthControl := fc.kpBandwidth*bandwidthError +
		fc.kiBandwidth*fc.bandwidthErrorIntegral +
		fc.kdBandwidth*bandwidthDerivative

	// ===== PACKET LOSS FEEDBACK LOOP =====
	// Target: 0% packet loss
	// Error: current packet loss (positive value means reduce bitrate)
	lossError := state.PacketLoss * 10000 // Scale to make significant

	// If loss is high, integral builds up quickly
	if state.PacketLoss > 0.05 {
		fc.lossErrorIntegral += lossError * dt * 2 // Faster accumulation
	} else {
		fc.lossErrorIntegral += lossError * dt
	}

	fc.lossErrorIntegral = clamp(fc.lossErrorIntegral, -10000, 10000)

	lossDerivative := (lossError - fc.prevLossError) / dt
	fc.prevLossError = lossError

	// Loss control output (negative - reduces bitrate)
	lossControl := -(fc.kpLoss*lossError +
		fc.kiLoss*fc.lossErrorIntegral +
		fc.kdLoss*lossDerivative)

	// ===== LATENCY FEEDBACK LOOP =====
	// Target RTT: 100ms
	// Error: excess latency
	targetRTT := 100.0 // ms
	currentRTT := float64(state.RTT.Milliseconds())
	latencyError := currentRTT - targetRTT

	// Only accumulate if latency is high
	if latencyError > 0 {
		fc.latencyErrorIntegral += latencyError * dt
	} else {
		// Decay integral when latency is good
		fc.latencyErrorIntegral *= 0.95
	}

	fc.latencyErrorIntegral = clamp(fc.latencyErrorIntegral, -1000, 1000)

	latencyDerivative := (latencyError - fc.prevLatencyError) / dt
	fc.prevLatencyError = latencyError

	// Latency control output (negative - reduces bitrate if high latency)
	latencyControl := -(fc.kpLatency*latencyError +
		fc.kiLatency*fc.latencyErrorIntegral +
		fc.kdLatency*latencyDerivative)

	// ===== COMBINE CONTROL SIGNALS =====
	// Weighted sum of all feedback loops
	totalControl := bandwidthControl +
		lossControl*2.0 + // Loss is critical, weight heavily
		latencyControl*0.5 // Latency is secondary

	return totalControl
}

// QualityManager coordinates video quality with comprehensive feedback control
type QualityManager struct {
	mu sync.RWMutex

	// User configuration
	priority QualityPriority
	deviceCapability DeviceCapability

	// Current state
	currentProfile   *QualityProfile
	targetBitrate    int // Kbps

	// Feedback control system
	controller *FeedbackController

	// Network state tracking
	networkState     NetworkState
	stateHistory     []NetworkState // Last N states for trend analysis
	maxHistorySize   int

	// Adaptation state
	lastAdjustment     time.Time
	adjustmentHistory  []AdjustmentRecord
	baseCooldownPeriod time.Duration

	// Oscillation detection
	recentUpgrades   int
	recentDowngrades int
	oscillationCount int

	// Callbacks for applying changes
	onBitrateChange  func(newBitrate int) error
	onProfileChange  func(newProfile *QualityProfile) error

	// Metrics
	totalAdjustments     int
	successfulAdjustments int
	failedAdjustments    int
}

// NewQualityManager creates a quality manager with specified priority
func NewQualityManager(priority QualityPriority, deviceCap DeviceCapability) *QualityManager {
	qm := &QualityManager{
		priority:           priority,
		deviceCapability:   deviceCap,
		baseCooldownPeriod: 5 * time.Second,
		adjustmentHistory:  make([]AdjustmentRecord, 0, 20),
		stateHistory:       make([]NetworkState, 0, 60), // 1 minute at 1 sample/sec
		maxHistorySize:     60,
		controller:         NewFeedbackController(),
	}

	// Select initial profile based on priority
	qm.currentProfile = qm.selectInitialProfile()
	qm.targetBitrate = qm.currentProfile.BitrateRange.Max

	log.Printf("[QualityManager] Initialized with priority=%s, profile=%s, bitrate=%d Kbps",
		priority.String(), qm.currentProfile.Name, qm.targetBitrate)

	return qm
}

// selectInitialProfile chooses starting profile based on priority and device capability
func (qm *QualityManager) selectInitialProfile() *QualityProfile {
	availableProfiles := GetQualityProfiles()

	// Filter by device capability
	compatible := make([]*QualityProfile, 0)
	for i := range availableProfiles {
		profile := &availableProfiles[i]
		if profile.MinDeviceTier <= qm.deviceCapability.Tier &&
			profile.Resolution.Pixels() <= qm.deviceCapability.MaxResolution.Pixels() &&
			profile.FrameRate <= qm.deviceCapability.MaxFrameRate {
			compatible = append(compatible, profile)
		}
	}

	if len(compatible) == 0 {
		// Fallback to lowest profile
		return &availableProfiles[len(availableProfiles)-1]
	}

	switch qm.priority {
	case PriorityMaximizeQuality:
		// Start with highest quality available
		return compatible[0]

	case PriorityMinimizeLatency:
		// Prefer 720p@30 for responsiveness
		for _, p := range compatible {
			if p.Resolution.Width == 1280 && p.Resolution.Height == 720 && p.FrameRate == 30 {
				return p
			}
		}
		// Fallback to middle profile
		return compatible[len(compatible)/2]

	case PriorityMinimizeDeviceStrain:
		// Start conservatively - 720p@24 or lower
		for _, p := range compatible {
			if p.Resolution.Width <= 1280 && p.FrameRate <= 24 {
				return p
			}
		}
		return compatible[len(compatible)-1]

	default:
		return compatible[len(compatible)/2]
	}
}

// SetCustomProfile allows setting a custom resolution/framerate
// Used when user selects "custom" in config UI
func (qm *QualityManager) SetCustomProfile(width, height, framerate int) error {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	// Validate custom resolution
	if err := ValidateCustomResolution(width, height, framerate); err != nil {
		return fmt.Errorf("invalid custom resolution: %w", err)
	}

	// Create custom profile based on closest standard profile
	customProfile := CreateCustomProfile(width, height, framerate)

	log.Printf("[QualityManager] Custom profile set: %s, bitrate range %d-%d Kbps",
		customProfile.Name, customProfile.BitrateRange.Min, customProfile.BitrateRange.Max)

	qm.currentProfile = customProfile
	qm.targetBitrate = customProfile.BitrateRange.Max

	// Trigger immediate adaptation with new profile
	if qm.onProfileChange != nil {
		if err := qm.onProfileChange(customProfile); err != nil {
			return fmt.Errorf("failed to apply custom profile: %w", err)
		}
	}

	return nil
}

// UpdateNetworkState receives metrics from ConnectionDoctor and triggers adaptation
// This is the primary input to the negative feedback control system
func (qm *QualityManager) UpdateNetworkState(state NetworkState) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	// Store network state
	qm.networkState = state

	// Add to history for trend analysis
	qm.stateHistory = append(qm.stateHistory, state)
	if len(qm.stateHistory) > qm.maxHistorySize {
		qm.stateHistory = qm.stateHistory[1:]
	}

	// Trigger adaptation
	qm.adaptToConditions()
}

// adaptToConditions adjusts quality based on comprehensive feedback control
func (qm *QualityManager) adaptToConditions() {
	// Check cooldown period (with exponential backoff for oscillation)
	cooldown := qm.calculateDynamicCooldown()
	if time.Since(qm.lastAdjustment) < cooldown {
		return
	}

	// Need at least 5 samples for stable control
	if len(qm.stateHistory) < 5 {
		return
	}

	// Calculate control signal from feedback controller
	controlSignal := qm.controller.CalculateControl(qm.networkState, qm.targetBitrate)

	// Compute target bitrate from control signal
	newTargetBitrate := qm.targetBitrate + int(controlSignal)

	// Limit step size to prevent aggressive swings (max 25% per adjustment)
	maxDelta := qm.targetBitrate / 4
	if newTargetBitrate > qm.targetBitrate+maxDelta {
		newTargetBitrate = qm.targetBitrate + maxDelta
	} else if newTargetBitrate < qm.targetBitrate-maxDelta {
		newTargetBitrate = qm.targetBitrate - maxDelta
	}

	// Apply priority-specific adjustments
	newTargetBitrate = qm.applyPriorityModulation(newTargetBitrate)

	// Apply additional negative feedback from RTCP signals
	newTargetBitrate = qm.applyRTCPFeedback(newTargetBitrate)

	// Apply hysteresis (prevent small oscillations)
	threshold := float64(qm.targetBitrate) * 0.15 // 15% threshold
	if math.Abs(float64(newTargetBitrate-qm.targetBitrate)) < threshold {
		return // Change too small
	}

	// Clamp to profile range
	profileMin := qm.currentProfile.BitrateRange.Min
	profileMax := qm.currentProfile.BitrateRange.Max
	newTargetBitrate = clampInt(newTargetBitrate, profileMin, profileMax)

	// Absolute bounds check (sanity limits)
	const absoluteMin = 100  // 100 Kbps minimum
	const absoluteMax = 15000 // 15 Mbps maximum
	newTargetBitrate = clampInt(newTargetBitrate, absoluteMin, absoluteMax)

	// Check if profile change needed (resolution/fps change)
	needsProfileChange := qm.shouldChangeProfile(newTargetBitrate)

	reason := qm.determineAdjustmentReason()

	if needsProfileChange {
		newProfile := qm.selectProfileForBitrate(newTargetBitrate)
		// Guard: Skip if same profile selected (prevents spurious resets)
		if newProfile.Name == qm.currentProfile.Name {
			needsProfileChange = false
			// Fall through to bitrate-only adjustment
		} else if qm.applyProfileChange(newProfile, newTargetBitrate, reason) {
			qm.recordAdjustment(qm.targetBitrate, newTargetBitrate, qm.currentProfile.Resolution, newProfile.Resolution, reason)
			qm.targetBitrate = newTargetBitrate
			qm.lastAdjustment = time.Now()
			return // Profile change applied, done
		}
	}

	// Bitrate-only adjustment (either needsProfileChange was false, or same profile guard triggered)
	if !needsProfileChange {
		// Bitrate-only adjustment
		if qm.applyBitrateChange(newTargetBitrate, reason) {
			qm.recordAdjustment(qm.targetBitrate, newTargetBitrate, qm.currentProfile.Resolution, qm.currentProfile.Resolution, reason)
			qm.targetBitrate = newTargetBitrate
			qm.lastAdjustment = time.Now()
		}
	}
}

// applyPriorityModulation adjusts target based on user priority
func (qm *QualityManager) applyPriorityModulation(targetBitrate int) int {
	state := qm.networkState

	switch qm.priority {
	case PriorityMaximizeQuality:
		// Aggressive bandwidth usage, tolerate some latency
		if state.PacketLoss < 0.03 && state.RTT < 200*time.Millisecond {
			// Network is good - push higher
			return int(float64(targetBitrate) * 1.1)
		}
		return targetBitrate

	case PriorityMinimizeLatency:
		// React strongly to latency and jitter
		if state.RTT > 150*time.Millisecond || state.JitterBuffer > 0.05 {
			// High latency - reduce aggressively
			return int(float64(targetBitrate) * 0.8)
		}
		// Conservative bandwidth usage
		return int(float64(targetBitrate) * 0.85)

	case PriorityMinimizeDeviceStrain:
		// Use lower bitrates to reduce encoding load
		return int(float64(targetBitrate) * 0.75)

	default:
		return targetBitrate
	}
}

// applyRTCPFeedback uses receiver feedback signals for additional control
// applyRTCPFeedback applies small corrections based on RTCP signals
// These are nudges, not aggressive cuts - the PID controller handles most adaptation
func (qm *QualityManager) applyRTCPFeedback(targetBitrate int) int {
	state := qm.networkState

	// NACKs indicate retransmissions - small reduction
	if state.NACKCount > 20 {
		reduction := math.Min(float64(state.NACKCount)/200.0, 0.10) // Max 10% reduction
		targetBitrate = int(float64(targetBitrate) * (1.0 - reduction))
		log.Printf("[QualityManager] RTCP: High NACKs (%d), nudging bitrate down", state.NACKCount)
	}

	// PLIs/FIRs indicate decoder issues - small correction
	if state.PLICount > 5 || state.FIRCount > 2 {
		targetBitrate = int(float64(targetBitrate) * 0.95) // 5% reduction
		log.Printf("[QualityManager] RTCP: Decoder stress (PLI=%d, FIR=%d), small reduction",
			state.PLICount, state.FIRCount)
	}

	// Burst loss indicates severe congestion - moderate reduction
	if state.BurstLossRate > 100 {
		targetBitrate = int(float64(targetBitrate) * 0.85) // 15% reduction
		log.Printf("[QualityManager] RTCP: Burst loss detected (%d), emergency bitrate reduction",
			state.BurstLossRate)
	}

	return targetBitrate
}

// shouldChangeProfile determines if resolution/framerate change is needed
func (qm *QualityManager) shouldChangeProfile(targetBitrate int) bool {
	// If target bitrate is far outside current profile's range, change profile
	profileMin := qm.currentProfile.BitrateRange.Min
	profileMax := qm.currentProfile.BitrateRange.Max

	// Need to downgrade profile
	if targetBitrate < int(float64(profileMin)*0.8) {
		return true
	}

	// Can upgrade profile
	if targetBitrate > int(float64(profileMax)*1.2) {
		return true
	}

	return false
}

// selectProfileForBitrate chooses best profile for target bitrate
func (qm *QualityManager) selectProfileForBitrate(targetBitrate int) *QualityProfile {
	profiles := GetQualityProfiles()

	// Filter compatible profiles
	compatible := make([]*QualityProfile, 0)
	for i := range profiles {
		p := &profiles[i]
		if p.MinDeviceTier <= qm.deviceCapability.Tier &&
			p.Resolution.Pixels() <= qm.deviceCapability.MaxResolution.Pixels() {
			compatible = append(compatible, p)
		}
	}

	// Find best match for target bitrate
	var best *QualityProfile
	minDiff := math.MaxInt32

	for _, p := range compatible {
		// Prefer profiles where target is in the middle of range
		midpoint := (p.BitrateRange.Min + p.BitrateRange.Max) / 2
		diff := abs(targetBitrate - midpoint)

		if diff < minDiff {
			minDiff = diff
			best = p
		}
	}

	if best != nil {
		return best
	}

	// Fallback to current profile
	return qm.currentProfile
}

// determineAdjustmentReason generates human-readable reason for adjustment
func (qm *QualityManager) determineAdjustmentReason() string {
	state := qm.networkState
	reasons := make([]string, 0)

	if state.PacketLoss > 0.05 {
		reasons = append(reasons, fmt.Sprintf("packet loss %.1f%%", state.PacketLoss*100))
	}
	if state.RTT > 150*time.Millisecond {
		reasons = append(reasons, fmt.Sprintf("high RTT %dms", state.RTT.Milliseconds()))
	}
	if state.JitterBuffer > 0.05 {
		reasons = append(reasons, fmt.Sprintf("jitter %.0fms", state.JitterBuffer*1000))
	}
	if state.NACKCount > 10 {
		reasons = append(reasons, fmt.Sprintf("NACKs %d", state.NACKCount))
	}
	if state.BurstLossRate > 50 {
		reasons = append(reasons, fmt.Sprintf("burst loss %d", state.BurstLossRate))
	}

	// Check for positive conditions (upgrade)
	if state.PacketLoss < 0.01 && state.RTT < 100*time.Millisecond &&
		float64(state.CurrentBitrate) < float64(qm.networkState.AvailableBandwidth)*0.7 {
		reasons = append(reasons, "network capacity available")
	}

	if len(reasons) == 0 {
		return "routine optimization"
	}

	return strings.Join(reasons, ", ")
}

// applyProfileChange applies resolution/framerate change
func (qm *QualityManager) applyProfileChange(newProfile *QualityProfile, newBitrate int, reason string) bool {
	if qm.onProfileChange == nil {
		log.Printf("[QualityManager] WARNING: Profile change callback not set")
		return false
	}

	// Store old profile before updating for oscillation detection
	oldProfile := qm.currentProfile

	log.Printf("[QualityManager] Profile change: %s -> %s (%d Kbps), reason: %s",
		oldProfile.Name, newProfile.Name, newBitrate, reason)

	if err := qm.onProfileChange(newProfile); err != nil {
		log.Printf("[QualityManager] Failed to apply profile change: %v", err)
		qm.failedAdjustments++
		return false
	}

	qm.currentProfile = newProfile
	qm.successfulAdjustments++
	qm.totalAdjustments++

	// Track upgrade/downgrade for oscillation detection
	if newProfile.Resolution.Pixels() > oldProfile.Resolution.Pixels() {
		qm.recentUpgrades++
	} else if newProfile.Resolution.Pixels() < oldProfile.Resolution.Pixels() {
		qm.recentDowngrades++
	}
	// Equal pixels = no change to counters

	// Reset controller state on profile change to avoid carrying stale errors
	qm.controller.Reset()

	return true
}

// applyBitrateChange applies bitrate-only adjustment
func (qm *QualityManager) applyBitrateChange(newBitrate int, reason string) bool {
	if qm.onBitrateChange == nil {
		log.Printf("[QualityManager] WARNING: Bitrate change callback not set")
		return false
	}

	direction := "increase"
	if newBitrate < qm.targetBitrate {
		direction = "decrease"
	}

	log.Printf("[QualityManager] Bitrate %s: %d -> %d Kbps, reason: %s",
		direction, qm.targetBitrate, newBitrate, reason)

	if err := qm.onBitrateChange(newBitrate); err != nil {
		log.Printf("[QualityManager] Failed to apply bitrate change: %v", err)
		qm.failedAdjustments++
		return false
	}

	qm.successfulAdjustments++
	qm.totalAdjustments++
	return true
}

// calculateDynamicCooldown implements exponential backoff for oscillation prevention
func (qm *QualityManager) calculateDynamicCooldown() time.Duration {
	// Base cooldown
	cooldown := qm.baseCooldownPeriod

	// Check for oscillation in last minute
	recentChanges := qm.countRecentAdjustments(1 * time.Minute)

	if recentChanges > 5 {
		// Oscillating - apply moderate backoff
		// Use linear increase instead of exponential to avoid getting stuck
		multiplier := 1 + (recentChanges-5)/3 // Gentler than 2^n
		if multiplier > 6 {
			multiplier = 6 // Cap at 6x (30 seconds with 5s base)
		}
		cooldown = cooldown * time.Duration(multiplier)
		qm.oscillationCount++
	}

	// Check for upgrade/downgrade ping-pong
	if qm.recentUpgrades > 2 && qm.recentDowngrades > 2 {
		cooldown = cooldown * 2 // Reduced from 4x
	}

	// Absolute maximum cooldown to prevent getting stuck
	const maxCooldown = 20 * time.Second
	if cooldown > maxCooldown {
		cooldown = maxCooldown
	}

	return cooldown
}

// countRecentAdjustments counts adjustments within time window
func (qm *QualityManager) countRecentAdjustments(window time.Duration) int {
	cutoff := time.Now().Add(-window)
	count := 0
	for i := len(qm.adjustmentHistory) - 1; i >= 0; i-- {
		if qm.adjustmentHistory[i].Timestamp.After(cutoff) {
			count++
		} else {
			break
		}
	}
	return count
}

// recordAdjustment logs an adjustment for history and oscillation detection
func (qm *QualityManager) recordAdjustment(fromBitrate, toBitrate int, fromRes, toRes Resolution, reason string) {
	record := AdjustmentRecord{
		Timestamp:      time.Now(),
		FromBitrate:    fromBitrate,
		ToBitrate:      toBitrate,
		FromResolution: fromRes,
		ToResolution:   toRes,
		Reason:         reason,
		NetworkState:   qm.networkState,
	}

	qm.adjustmentHistory = append(qm.adjustmentHistory, record)

	// Keep last 20 adjustments
	if len(qm.adjustmentHistory) > 20 {
		qm.adjustmentHistory = qm.adjustmentHistory[1:]
	}

	// Reset upgrade/downgrade counters periodically
	if len(qm.adjustmentHistory) > 10 {
		qm.recentUpgrades = 0
		qm.recentDowngrades = 0
	}
}

// SetBitrateCallback registers callback for encoder bitrate changes
func (qm *QualityManager) SetBitrateCallback(cb func(int) error) {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.onBitrateChange = cb
}

// SetProfileCallback registers callback for profile changes
func (qm *QualityManager) SetProfileCallback(cb func(*QualityProfile) error) {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.onProfileChange = cb
}

// UpdateDeviceCapability updates device capability (called after encoder initialization)
func (qm *QualityManager) UpdateDeviceCapability(cap DeviceCapability) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	oldTier := qm.deviceCapability.Tier
	qm.deviceCapability = cap

	log.Printf("[QualityManager] Device capability updated: %s -> %s (HW encoder: %v)",
		oldTier.String(), cap.Tier.String(), cap.HardwareEncoder)
}

// SetPriority updates the quality priority mode at runtime
// The priority affects bandwidth utilization targets and adaptation behavior
func (qm *QualityManager) SetPriority(priority QualityPriority) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	oldPriority := qm.priority
	qm.priority = priority

	// Reset controller state when operating point changes
	qm.controller.Reset()

	log.Printf("[QualityManager] Priority changed: %s -> %s (controller reset, will affect next adaptation cycle)",
		oldPriority.String(), priority.String())
}

// GetPriority returns the current quality priority mode
func (qm *QualityManager) GetPriority() QualityPriority {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	return qm.priority
}

// GetCurrentState returns current quality state
func (qm *QualityManager) GetCurrentState() (profile *QualityProfile, bitrate int, adjustments int) {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	return qm.currentProfile, qm.targetBitrate, qm.totalAdjustments
}


// Utility functions

func clamp(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func clampInt(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// QualityMetrics represents the current state of quality management for API export
type QualityMetrics struct {
	// Current operating state
	Priority          string    `json:"priority"`
	CurrentProfile    string    `json:"currentProfile"`
	TargetBitrate     int       `json:"targetBitrate"`     // kbps
	CurrentResolution string    `json:"currentResolution"` // e.g., "1920x1080@30"

	// Network state
	AvailableBandwidth int     `json:"availableBandwidth"` // kbps
	PacketLoss         float64 `json:"packetLoss"`         // 0-1
	RTT                int     `json:"rtt"`                // milliseconds
	Jitter             int     `json:"jitter"`             // milliseconds

	// Adaptation statistics
	TotalAdjustments   int       `json:"totalAdjustments"`
	RecentUpgrades     int       `json:"recentUpgrades"`
	RecentDowngrades   int       `json:"recentDowngrades"`
	LastAdjustment     time.Time `json:"lastAdjustment"`
	NextCheckAvailable time.Time `json:"nextCheckAvailable"` // When cooldown expires

	// Controller state (for debugging)
	BandwidthError float64 `json:"bandwidthError"`
	LossError      float64 `json:"lossError"`
	LatencyError   float64 `json:"latencyError"`
}

// GetMetrics returns current quality management metrics for monitoring/debugging
func (qm *QualityManager) GetMetrics() QualityMetrics {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	nextCheck := qm.lastAdjustment.Add(qm.calculateDynamicCooldown())

	return QualityMetrics{
		Priority:           qm.priority.String(),
		CurrentProfile:     qm.currentProfile.Name,
		TargetBitrate:      qm.targetBitrate,
		CurrentResolution:  fmt.Sprintf("%dx%d@%d", qm.currentProfile.Resolution.Width, qm.currentProfile.Resolution.Height, qm.currentProfile.FrameRate),
		AvailableBandwidth: qm.networkState.AvailableBandwidth,
		PacketLoss:         qm.networkState.PacketLoss,
		RTT:                int(qm.networkState.RTT.Milliseconds()),
		Jitter:             int(qm.networkState.JitterBuffer * 1000), // Convert seconds to ms
		TotalAdjustments:   len(qm.adjustmentHistory),
		RecentUpgrades:     qm.recentUpgrades,
		RecentDowngrades:   qm.recentDowngrades,
		LastAdjustment:     qm.lastAdjustment,
		NextCheckAvailable: nextCheck,
		BandwidthError:     qm.controller.prevBandwidthError,
		LossError:          qm.controller.prevLossError,
		LatencyError:       qm.controller.prevLatencyError,
	}
}
