package buffer

import (
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"time"
)

// Frame represents a video frame with metadata
type Frame struct {
	Image     image.Image
	Timestamp time.Time
	PTS       time.Duration // Presentation timestamp
	Size      int           // Size in bytes
	Sequence  uint64        // Frame sequence number
	Keyframe  bool          // Is this a keyframe
	pooled    bool          // Whether this frame came from pool
}

// RingBuffer implements a circular buffer for frames.
// Semantics:
//   - Write appends the newest frame; if full, the oldest is overwritten.
//   - Read returns the next unread frame and ADVANCES the reader cursor (destructive in the cursor sense).
//   - Peek(offset) does not advance the reader cursor.
//   - WaitForFrame blocks until at least one unread frame exists, then returns the oldest unread (does not advance).
type RingBuffer struct {
	frames    []*Frame
	capacity  int
	writePos  int64           // total frames ever written
	readPos   int64           // consumer cursor (frames read/advanced)
	size      atomic.Int64    // current number of frames retained (<= capacity)
	sequence  atomic.Uint64

	mu       sync.RWMutex
	notEmpty *sync.Cond
	pool     *FramePool

	// Metrics
	totalWrites   atomic.Uint64
	totalReads    atomic.Uint64
	droppedFrames atomic.Uint64 // frames skipped because reader fell behind / overwrites
	overflows     atomic.Uint64 // number of overwrites performed (buffer was full)
}

// NewRingBuffer creates a new ring buffer with the specified capacity
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 1
	}
	rb := &RingBuffer{
		frames:   make([]*Frame, capacity),
		capacity: capacity,
		pool:     NewFramePool(capacity / 2), // Pool size is half of buffer
	}
	rb.notEmpty = sync.NewCond(&rb.mu)
	return rb
}

// NewRingBufferWithPool creates a ring buffer with a custom frame pool
func NewRingBufferWithPool(capacity int, pool *FramePool) *RingBuffer {
	if capacity <= 0 {
		capacity = 1
	}
	rb := &RingBuffer{
		frames:   make([]*Frame, capacity),
		capacity: capacity,
		pool:     pool,
	}
	rb.notEmpty = sync.NewCond(&rb.mu)
	return rb
}

// Write adds a frame to the buffer. If the buffer is full, the oldest frame is overwritten.
func (rb *RingBuffer) Write(frame *Frame) error {
	if frame == nil {
		return fmt.Errorf("cannot write nil frame")
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Assign sequence number
	frame.Sequence = rb.sequence.Add(1)

	writePos := atomic.LoadInt64(&rb.writePos)
	pos := int(writePos % int64(rb.capacity))

	// If overwriting an existing frame, the buffer was full at this slot.
	if old := rb.frames[pos]; old != nil {
		rb.overflows.Add(1)

		// If the consumer hasn't caught up (unread >= capacity), we drop one.
		unread := writePos - atomic.LoadInt64(&rb.readPos)
		if unread >= int64(rb.capacity) {
			rb.droppedFrames.Add(1)
			// We don't move readPos here; we let Read() clamp and count precisely.
		}

		// Return old frame to pool if it came from there
		if old.pooled && rb.pool != nil {
			rb.pool.Put(old)
		}
	}

	// Store frame and advance writer
	rb.frames[pos] = frame
	atomic.AddInt64(&rb.writePos, 1)

	// Update retained size (up to capacity)
	if rb.size.Load() < int64(rb.capacity) {
		rb.size.Add(1)
	}

	rb.totalWrites.Add(1)
	rb.notEmpty.Signal()
	return nil
}

// Read returns the next unread frame and advances the reader cursor.
// If there are no unread frames, returns an error. It clamps the reader cursor
// to the oldest retained frame if it has fallen behind (counts dropped frames).
func (rb *RingBuffer) Read() (*Frame, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	currentSize := rb.size.Load()
	if currentSize == 0 {
		return nil, fmt.Errorf("buffer is empty")
	}

	writePos := atomic.LoadInt64(&rb.writePos)
	readPos := atomic.LoadInt64(&rb.readPos)

	// Oldest retained index
	validStart := writePos - currentSize
	if validStart < 0 {
		validStart = 0
	}

	// If reader fell behind the retention window, clamp and count drops.
	if readPos < validStart {
		dropped := validStart - readPos
		if dropped > 0 {
			rb.droppedFrames.Add(uint64(dropped))
			readPos = validStart
			atomic.StoreInt64(&rb.readPos, readPos)
		}
	}

	// If no unread frames remain, bail out.
	if readPos >= writePos {
		return nil, fmt.Errorf("no unread frames")
	}

	pos := int(readPos % int64(rb.capacity))
	frame := rb.frames[pos]
	atomic.StoreInt64(&rb.readPos, readPos+1)
	rb.totalReads.Add(1)

	if frame == nil {
		// Should not happen after clamping; treat as transient.
		return nil, fmt.Errorf("no frame at position %d", pos)
	}

	return frame, nil
}

// Peek returns the frame at the specified offset from the current reader cursor
// without advancing it. Offset 0 is the next unread frame. It clamps the
// reader cursor conceptually for bounds checking but does not modify it.
func (rb *RingBuffer) Peek(offset int) (*Frame, error) {
	if offset < 0 {
		return nil, fmt.Errorf("offset %d out of range", offset)
	}

	rb.mu.RLock()
	defer rb.mu.RUnlock()

	currentSize := rb.size.Load()
	if currentSize == 0 {
		return nil, fmt.Errorf("buffer is empty")
	}

	writePos := atomic.LoadInt64(&rb.writePos)
	readPos := atomic.LoadInt64(&rb.readPos)
	validStart := writePos - currentSize
	if validStart < 0 {
		validStart = 0
	}
	// Effective reader position after clamping (do not store it)
	if readPos < validStart {
		readPos = validStart
	}

	unread := writePos - readPos
	if unread <= 0 {
		return nil, fmt.Errorf("no unread frames")
	}
	if int64(offset) >= unread {
		return nil, fmt.Errorf("offset %d out of range (unread=%d)", offset, unread)
	}

	pos := int((readPos + int64(offset)) % int64(rb.capacity))
	frame := rb.frames[pos]
	if frame == nil {
		return nil, fmt.Errorf("no frame at offset %d", offset)
	}
	return frame, nil
}

// Dump returns all frames currently retained (from oldest to newest) without
// modifying the buffer. It returns shallow copies of the metadata (Frame struct),
// not deep copies of the image data.
func (rb *RingBuffer) Dump() ([]*Frame, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	currentSize := int(rb.size.Load())
	if currentSize == 0 {
		return []*Frame{}, nil
	}

	writePos := atomic.LoadInt64(&rb.writePos)
	startPos := writePos - int64(currentSize)
	if startPos < 0 {
		startPos = 0
	}

	frames := make([]*Frame, 0, currentSize)
	for i := 0; i < currentSize; i++ {
		pos := int((startPos + int64(i)) % int64(rb.capacity))
		if f := rb.frames[pos]; f != nil {
			frameCopy := &Frame{
				Image:     f.Image,
				Timestamp: f.Timestamp,
				PTS:       f.PTS,
				Size:      f.Size,
				Sequence:  f.Sequence,
				Keyframe:  f.Keyframe,
			}
			frames = append(frames, frameCopy)
		}
	}
	return frames, nil
}

// DumpAndReset returns all frames and resets the buffer
func (rb *RingBuffer) DumpAndReset() ([]*Frame, error) {
	frames, err := rb.Dump()
	if err != nil {
		return nil, err
	}
	return frames, rb.Reset()
}

// Reset clears the buffer (returns pooled frames to the pool)
func (rb *RingBuffer) Reset() error {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Return pooled frames to pool
	if rb.pool != nil {
		for i := 0; i < rb.capacity; i++ {
			if rb.frames[i] != nil && rb.frames[i].pooled {
				rb.pool.Put(rb.frames[i])
			}
		}
	}

	// Clear all frames
	for i := 0; i < rb.capacity; i++ {
		rb.frames[i] = nil
	}

	atomic.StoreInt64(&rb.writePos, 0)
	atomic.StoreInt64(&rb.readPos, 0)
	rb.size.Store(0)

	return nil
}

// Size returns the current number of frames retained in the buffer (<= capacity).
func (rb *RingBuffer) Size() int {
	return int(rb.size.Load())
}

// Capacity returns the maximum capacity of the buffer
func (rb *RingBuffer) Capacity() int {
	return rb.capacity
}

// IsFull returns true if the buffer is at capacity
func (rb *RingBuffer) IsFull() bool {
	return rb.size.Load() >= int64(rb.capacity)
}

// IsEmpty returns true if the buffer retains no frames
func (rb *RingBuffer) IsEmpty() bool {
	return rb.size.Load() == 0
}

// GetOldest returns the oldest retained frame without removing it.
func (rb *RingBuffer) GetOldest() (*Frame, error) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size.Load() == 0 {
		return nil, fmt.Errorf("buffer is empty")
	}

	writePos := atomic.LoadInt64(&rb.writePos)
	startPos := writePos - rb.size.Load()
	if startPos < 0 {
		startPos = 0
	}
	pos := int(startPos % int64(rb.capacity))
	return rb.frames[pos], nil
}

// GetNewest returns the newest retained frame without removing it.
func (rb *RingBuffer) GetNewest() (*Frame, error) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size.Load() == 0 {
		return nil, fmt.Errorf("buffer is empty")
	}

	writePos := atomic.LoadInt64(&rb.writePos)
	pos := int((writePos - 1) % int64(rb.capacity))
	if pos < 0 {
		pos += rb.capacity
	}
	return rb.frames[pos], nil
}

// GetFramesInTimeRange returns retained frames whose timestamps are in [start, end].
func (rb *RingBuffer) GetFramesInTimeRange(start, end time.Time) ([]*Frame, error) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	currentSize := int(rb.size.Load())
	if currentSize == 0 {
		return []*Frame{}, nil
	}

	writePos := atomic.LoadInt64(&rb.writePos)
	startPos := writePos - int64(currentSize)

	frames := make([]*Frame, 0)
	for i := 0; i < currentSize; i++ {
		pos := int((startPos + int64(i)) % int64(rb.capacity))
		if pos < 0 {
			pos += rb.capacity
		}
		if frame := rb.frames[pos]; frame != nil && !frame.Timestamp.Before(start) && !frame.Timestamp.After(end) {
			frames = append(frames, frame)
		}
	}
	return frames, nil
}

// GetDuration returns the time span covered by retained frames
func (rb *RingBuffer) GetDuration() (time.Duration, error) {
	oldest, err := rb.GetOldest()
	if err != nil {
		return 0, err
	}
	newest, err := rb.GetNewest()
	if err != nil {
		return 0, err
	}
	return newest.Timestamp.Sub(oldest.Timestamp), nil
}

// Metrics returns buffer statistics
func (rb *RingBuffer) Metrics() map[string]interface{} {
	writePos := atomic.LoadInt64(&rb.writePos)
	readPos := atomic.LoadInt64(&rb.readPos)
	unread := int64(0)
	if writePos > readPos {
		unread = writePos - readPos
	}
	return map[string]interface{}{
		"capacity":        rb.capacity,
		"current_size":    rb.Size(),
		"total_writes":    rb.totalWrites.Load(),
		"total_reads":     rb.totalReads.Load(),
		"dropped_frames":  rb.droppedFrames.Load(),
		"overflows":       rb.overflows.Load(),
		"write_position":  writePos,
		"read_position":   readPos,
		"unread_estimate": unread, // unread frames based on the cursor
	}
}

// WaitForFrame blocks until at least one unread frame exists, then returns
// the oldest unread frame (does not advance the reader cursor).
func (rb *RingBuffer) WaitForFrame() *Frame {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	for {
		size := rb.size.Load()
		writePos := atomic.LoadInt64(&rb.writePos)
		readPos := atomic.LoadInt64(&rb.readPos)

		// Unread exists when writer advanced past the reader.
		if size > 0 && writePos > readPos {
			validStart := writePos - size
			if validStart < 0 {
				validStart = 0
			}
			if readPos < validStart {
				readPos = validStart
			}
			pos := int(readPos % int64(rb.capacity))
			return rb.frames[pos]
		}
		rb.notEmpty.Wait()
	}
}
