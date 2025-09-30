package rtcManager

import (
	"fmt"
	"sync"
	"time"
)

// CircularMetricsBuffer provides thread-safe storage for metrics with a fixed capacity
type CircularMetricsBuffer struct {
	mu       sync.RWMutex
	data     []QualityMetrics
	capacity int
	size     int
	head     int // Points to the next write position
	tail     int // Points to the oldest element
}

// NewCircularMetricsBuffer creates a new circular buffer with specified capacity
func NewCircularMetricsBuffer(capacity int) *CircularMetricsBuffer {
	return &CircularMetricsBuffer{
		data:     make([]QualityMetrics, capacity),
		capacity: capacity,
		size:     0,
		head:     0,
		tail:     0,
	}
}

// Add adds a new metric to the buffer
func (cb *CircularMetricsBuffer) Add(metric QualityMetrics) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.data[cb.head] = metric

	// Update head position
	cb.head = (cb.head + 1) % cb.capacity

	// Update size and tail
	if cb.size < cb.capacity {
		cb.size++
	} else {
		// Buffer is full, move tail
		cb.tail = (cb.tail + 1) % cb.capacity
	}
}

// GetRecent returns the most recent n metrics
func (cb *CircularMetricsBuffer) GetRecent(n int) []QualityMetrics {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if n > cb.size {
		n = cb.size
	}

	result := make([]QualityMetrics, n)
	idx := 0
	pos := (cb.head - 1 + cb.capacity) % cb.capacity // Start from most recent

	for i := 0; i < n; i++ {
		if pos < 0 {
			pos = cb.capacity - 1
		}
		result[idx] = cb.data[pos]
		idx++
		pos = (pos - 1 + cb.capacity) % cb.capacity
	}

	return result
}

// GetAll returns all metrics in chronological order
func (cb *CircularMetricsBuffer) GetAll() []QualityMetrics {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.size == 0 {
		return nil
	}

	result := make([]QualityMetrics, cb.size)
	idx := 0
	current := cb.tail

	for i := 0; i < cb.size; i++ {
		result[idx] = cb.data[current]
		idx++
		current = (current + 1) % cb.capacity
	}

	return result
}

// Size returns the current number of metrics in the buffer
func (cb *CircularMetricsBuffer) Size() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.size
}

// Clear empties the buffer
func (cb *CircularMetricsBuffer) Clear() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.size = 0
	cb.head = 0
	cb.tail = 0
}

// RTCPFeedbackEvent represents an RTCP feedback event
type RTCPFeedbackEvent struct {
	Timestamp   time.Time
	MediaKind   string // "video" or "audio"
	PacketType  uint8  // RTCP packet type (200=SR, 201=RR, 205=TWCC, 206=PSF, etc.)
	FeedbackType uint8 // For PSF packets: 1=NACK, 2=PLI, 4=FIR
	SSRC        uint32 // Source SSRC
	Data        []byte // Raw packet data for analysis
}

// RTCPFeedbackBuffer provides thread-safe RTCP feedback tracking with circular storage
type RTCPFeedbackBuffer struct {
	mu           sync.RWMutex
	data         []RTCPFeedbackEvent
	capacity     int
	size         int
	head         int // Points to the next write position
	tail         int // Points to the oldest element

	// Feedback loop prevention
	recentCounts map[string]int // key: "packetType:mediaKind", value: count in last second
	lastCleanup  time.Time
}

// NewRTCPFeedbackBuffer creates a new RTCP feedback buffer
func NewRTCPFeedbackBuffer(capacity int) *RTCPFeedbackBuffer {
	return &RTCPFeedbackBuffer{
		data:         make([]RTCPFeedbackEvent, capacity),
		capacity:     capacity,
		size:         0,
		head:         0,
		tail:         0,
		recentCounts: make(map[string]int),
		lastCleanup:  time.Now(),
	}
}

// Add adds a new RTCP feedback event to the buffer
func (rb *RTCPFeedbackBuffer) Add(event RTCPFeedbackEvent) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Clean up old counts periodically
	if time.Since(rb.lastCleanup) > time.Second {
		rb.recentCounts = make(map[string]int)
		rb.lastCleanup = time.Now()
	}

	rb.data[rb.head] = event

	// Update head position
	rb.head = (rb.head + 1) % rb.capacity

	// Update size and tail
	if rb.size < rb.capacity {
		rb.size++
	} else {
		// Buffer is full, move tail
		rb.tail = (rb.tail + 1) % rb.capacity
	}
}

// ShouldProcessPacket checks if we should process this RTCP packet to prevent feedback loops
func (rb *RTCPFeedbackBuffer) ShouldProcessPacket(packetType uint8, mediaKind string) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Clean up old counts if needed
	if time.Since(rb.lastCleanup) > time.Second {
		rb.recentCounts = make(map[string]int)
		rb.lastCleanup = time.Now()
	}

	key := formatCountKey(packetType, mediaKind)
	currentCount := rb.recentCounts[key]

	// Define limits per packet type to prevent feedback loops
	var limit int
	switch packetType {
	case 200, 201: // SR, RR - these are regular, allow more
		limit = 50
	case 205: // TWCC feedback
		limit = 20
	case 206: // PSF (NACK, PLI, FIR) - these can cause loops, be more restrictive
		limit = 10
	default:
		limit = 25
	}

	if currentCount >= limit {
		return false // Skip processing to prevent feedback loop
	}

	// Increment count
	rb.recentCounts[key] = currentCount + 1
	return true
}

// GetRecentEvents returns the most recent n RTCP events
func (rb *RTCPFeedbackBuffer) GetRecentEvents(n int) []RTCPFeedbackEvent {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if n > rb.size {
		n = rb.size
	}

	result := make([]RTCPFeedbackEvent, n)
	idx := 0
	pos := (rb.head - 1 + rb.capacity) % rb.capacity // Start from most recent

	for i := 0; i < n; i++ {
		if pos < 0 {
			pos = rb.capacity - 1
		}
		result[idx] = rb.data[pos]
		idx++
		pos = (pos - 1 + rb.capacity) % rb.capacity
	}

	return result
}

// GetEventsByType returns recent events of a specific packet type
func (rb *RTCPFeedbackBuffer) GetEventsByType(packetType uint8, mediaKind string, maxAge time.Duration) []RTCPFeedbackEvent {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	var result []RTCPFeedbackEvent
	cutoff := time.Now().Add(-maxAge)

	current := rb.tail
	for i := 0; i < rb.size; i++ {
		event := rb.data[current]
		if event.PacketType == packetType &&
		   event.MediaKind == mediaKind &&
		   event.Timestamp.After(cutoff) {
			result = append(result, event)
		}
		current = (current + 1) % rb.capacity
	}

	return result
}

// GetFeedbackStats returns statistics about recent feedback
func (rb *RTCPFeedbackBuffer) GetFeedbackStats(maxAge time.Duration) map[string]int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	stats := make(map[string]int)
	cutoff := time.Now().Add(-maxAge)

	current := rb.tail
	for i := 0; i < rb.size; i++ {
		event := rb.data[current]
		if event.Timestamp.After(cutoff) {
			key := formatCountKey(event.PacketType, event.MediaKind)
			stats[key]++
		}
		current = (current + 1) % rb.capacity
	}

	return stats
}

// Size returns the current number of events in the buffer
func (rb *RTCPFeedbackBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

func formatCountKey(packetType uint8, mediaKind string) string {
	return fmt.Sprintf("%d:%s", packetType, mediaKind)
}
