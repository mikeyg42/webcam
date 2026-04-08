package framestream

import (
	"sync"
	"testing"
	"time"
)

func TestBroadcasterBasic(t *testing.T) {
	b := NewBroadcaster[int]("Test", 5)

	// Subscribe
	sub := b.Subscribe()
	if sub == nil {
		t.Fatal("Subscribe returned nil")
	}

	// Broadcast
	b.Broadcast(42)

	// Receive
	select {
	case val := <-sub.Frames():
		if val != 42 {
			t.Errorf("Expected 42, got %d", val)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for broadcast")
	}

	// Close
	sub.Close()
	if !sub.IsClosed() {
		t.Error("Subscription should be closed")
	}
}

func TestBroadcasterMultipleSubscribers(t *testing.T) {
	b := NewBroadcaster[int]("Test", 5)

	sub1 := b.Subscribe()
	sub2 := b.Subscribe()
	sub3 := b.Subscribe()

	if b.SubscriberCount() != 3 {
		t.Errorf("Expected 3 subscribers, got %d", b.SubscriberCount())
	}

	// Broadcast to all
	b.Broadcast(100)

	// All should receive
	for i, sub := range []*Subscription[int]{sub1, sub2, sub3} {
		select {
		case val := <-sub.Frames():
			if val != 100 {
				t.Errorf("Sub %d: Expected 100, got %d", i+1, val)
			}
		case <-time.After(time.Second):
			t.Fatalf("Sub %d: Timeout waiting for broadcast", i+1)
		}
	}

	// Close one
	sub2.Close()
	if b.SubscriberCount() != 2 {
		t.Errorf("Expected 2 subscribers after close, got %d", b.SubscriberCount())
	}

	sub1.Close()
	sub3.Close()
}

func TestBroadcasterPauseResume(t *testing.T) {
	b := NewBroadcaster[int]("Test", 5)
	sub := b.Subscribe()
	defer sub.Close()

	// Pause
	b.Pause()

	// Broadcast while paused - should not deliver
	b.Broadcast(1)

	select {
	case <-sub.Frames():
		t.Error("Should not receive while paused")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}

	// Resume
	b.Resume()

	// Now broadcast should work
	b.Broadcast(2)

	select {
	case val := <-sub.Frames():
		if val != 2 {
			t.Errorf("Expected 2, got %d", val)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout after resume")
	}
}

func TestBroadcasterNonBlocking(t *testing.T) {
	// Small buffer to test non-blocking behavior
	b := NewBroadcaster[int]("Test", 2)
	sub := b.Subscribe()
	defer sub.Close()

	// Fill the buffer
	b.Broadcast(1)
	b.Broadcast(2)

	// This should not block even though buffer is full
	done := make(chan bool)
	go func() {
		b.Broadcast(3) // Should drop, not block
		done <- true
	}()

	select {
	case <-done:
		// Good, didn't block
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Broadcast blocked when buffer full")
	}

	// Drain and verify first two values
	val1 := <-sub.Frames()
	val2 := <-sub.Frames()
	if val1 != 1 || val2 != 2 {
		t.Errorf("Expected 1, 2 but got %d, %d", val1, val2)
	}
}

func TestSubscriptionSurvivesRestart(t *testing.T) {
	b := NewBroadcaster[int]("Test", 5)
	sub := b.Subscribe()
	defer sub.Close()

	// Simulate distributor restart cycle
	b.Broadcast(1)
	<-sub.Frames()

	b.Pause()  // Stop
	b.Resume() // Start

	// Subscription should still work
	b.Broadcast(2)

	select {
	case val := <-sub.Frames():
		if val != 2 {
			t.Errorf("Expected 2, got %d", val)
		}
	case <-time.After(time.Second):
		t.Fatal("Subscription dead after restart")
	}
}

func TestConcurrentSubscribeUnsubscribe(t *testing.T) {
	b := NewBroadcaster[int]("Test", 5)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := b.Subscribe()
			time.Sleep(time.Millisecond)
			sub.Close()
		}()
	}

	// Broadcast while subscribing/unsubscribing
	go func() {
		for i := 0; i < 100; i++ {
			b.Broadcast(i)
			time.Sleep(time.Microsecond * 100)
		}
	}()

	wg.Wait()

	if b.SubscriberCount() != 0 {
		t.Errorf("Expected 0 subscribers, got %d", b.SubscriberCount())
	}
}

func TestDoubleClose(t *testing.T) {
	b := NewBroadcaster[int]("Test", 5)
	sub := b.Subscribe()

	// First close
	sub.Close()

	// Second close should not panic
	sub.Close()

	if !sub.IsClosed() {
		t.Error("Should still be closed")
	}
}
