package rtcManager

import (
	"sync"
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
