package framestream

import (
	"log"
	"sync"
	"sync/atomic"
)

// Broadcaster manages subscriptions for a single stream type.
// Consumers subscribe once and receive frames even after distributor restarts.
// This solves the problem where Stop/Start recreates channels but consumers
// hold stale references to closed channels.
type Broadcaster[T any] struct {
	mu            sync.RWMutex
	subscriptions map[uint64]*Subscription[T]
	nextID        uint64
	bufferSize    int
	paused        atomic.Bool
	name          string // for logging
}

// Subscription provides a stable channel that survives distributor restarts.
// Consumers read from Frames() and call Close() when done.
type Subscription[T any] struct {
	id         uint64
	frames     chan T
	closed     atomic.Bool
	broadcaster *Broadcaster[T]
}

// NewBroadcaster creates a new broadcaster for a stream type.
// bufferSize controls how many frames can be queued per subscriber.
func NewBroadcaster[T any](name string, bufferSize int) *Broadcaster[T] {
	return &Broadcaster[T]{
		subscriptions: make(map[uint64]*Subscription[T]),
		bufferSize:    bufferSize,
		name:          name,
	}
}

// Subscribe creates a new subscription. The returned subscription's Frames()
// channel will receive broadcasts until Close() is called.
func (b *Broadcaster[T]) Subscribe() *Subscription[T] {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++

	sub := &Subscription[T]{
		id:          id,
		frames:      make(chan T, b.bufferSize),
		broadcaster: b,
	}

	b.subscriptions[id] = sub
	log.Printf("[Broadcaster:%s] New subscription id=%d, total=%d", b.name, id, len(b.subscriptions))

	return sub
}

// Unsubscribe removes a subscription. Called by Subscription.Close().
// Safe to call multiple times - only the first call closes the channel.
func (b *Broadcaster[T]) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if sub, ok := b.subscriptions[id]; ok {
		// Use CompareAndSwap to ensure we only close the channel once,
		// even if Unsubscribe is called concurrently or multiple times
		if sub.closed.CompareAndSwap(false, true) {
			close(sub.frames)
		}
		delete(b.subscriptions, id)
		log.Printf("[Broadcaster:%s] Unsubscribed id=%d, remaining=%d", b.name, id, len(b.subscriptions))
	}
}

// Broadcast sends a frame to all subscribers (non-blocking).
// Slow consumers will have frames dropped rather than blocking the producer.
func (b *Broadcaster[T]) Broadcast(frame T) {
	if b.paused.Load() {
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subscriptions {
		if sub.closed.Load() {
			continue
		}
		b.safeSend(sub, frame)
	}
}

// safeSend attempts to send a frame to a subscription with panic recovery.
// This handles the rare race condition where a subscription closes between
// the closed check and the actual send operation.
func (b *Broadcaster[T]) safeSend(sub *Subscription[T], frame T) {
	defer func() {
		if r := recover(); r != nil {
			// Channel was closed between our check and send - mark subscription as closed
			sub.closed.Store(true)
		}
	}()

	select {
	case sub.frames <- frame:
		// Frame sent successfully
	default:
		// Subscriber's buffer is full, drop frame
	}
}

// Pause stops broadcasting (used during distributor Stop).
// Subscriptions remain valid but won't receive frames.
func (b *Broadcaster[T]) Pause() {
	b.paused.Store(true)
	log.Printf("[Broadcaster:%s] Paused", b.name)
}

// Resume starts broadcasting again (used during distributor Start).
func (b *Broadcaster[T]) Resume() {
	b.paused.Store(false)
	log.Printf("[Broadcaster:%s] Resumed", b.name)
}

// SubscriberCount returns the number of active subscribers.
func (b *Broadcaster[T]) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscriptions)
}

// Frames returns the channel to read frames from.
// This channel remains valid for the lifetime of the subscription.
func (s *Subscription[T]) Frames() <-chan T {
	return s.frames
}

// Close unsubscribes and closes the subscription's channel.
// Safe to call multiple times.
func (s *Subscription[T]) Close() {
	if s.closed.CompareAndSwap(false, true) {
		s.broadcaster.Unsubscribe(s.id)
	}
}

// IsClosed returns whether the subscription has been closed.
func (s *Subscription[T]) IsClosed() bool {
	return s.closed.Load()
}
