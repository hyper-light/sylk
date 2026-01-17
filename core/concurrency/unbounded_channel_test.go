package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewUnboundedChannel(t *testing.T) {
	uc := NewUnboundedChannel[int]()
	if uc == nil {
		t.Fatal("expected non-nil channel")
	}
	if uc.IsClosed() {
		t.Error("new channel should not be closed")
	}
	if uc.Len() != 0 {
		t.Errorf("expected len 0, got %d", uc.Len())
	}
}

func TestNewUnboundedChannelWithCapacity(t *testing.T) {
	uc := NewUnboundedChannelWithCapacity[int](128)
	if uc == nil {
		t.Fatal("expected non-nil channel")
	}

	// Negative capacity should default
	uc2 := NewUnboundedChannelWithCapacity[int](-1)
	if uc2 == nil {
		t.Fatal("expected non-nil channel with default capacity")
	}
}

func TestUnboundedChannel_SendReceive(t *testing.T) {
	uc := NewUnboundedChannel[string]()
	defer uc.Close()

	// Send message
	err := uc.Send("hello")
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Receive message
	msg, err := uc.Receive()
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}
	if msg != "hello" {
		t.Errorf("expected 'hello', got '%s'", msg)
	}
}

func TestUnboundedChannel_NeverBlocks(t *testing.T) {
	// Small capacity to force overflow
	uc := NewUnboundedChannelWithCapacity[int](2)
	defer uc.Close()

	// Send more messages than capacity - should never block
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			if err := uc.Send(i); err != nil {
				t.Errorf("send %d failed: %v", i, err)
			}
		}
		close(done)
	}()

	// Should complete immediately without blocking
	select {
	case <-done:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send blocked - should never block")
	}

	// Verify overflow was used
	if uc.OverflowLen() == 0 {
		t.Error("expected overflow to be used")
	}

	// Verify total length
	if uc.Len() != 100 {
		t.Errorf("expected len 100, got %d", uc.Len())
	}
}

func TestUnboundedChannel_OverflowDrainOrdering(t *testing.T) {
	// Small capacity to force overflow
	uc := NewUnboundedChannelWithCapacity[int](2)
	defer uc.Close()

	// Send messages
	for i := 0; i < 10; i++ {
		if err := uc.Send(i); err != nil {
			t.Fatalf("send %d failed: %v", i, err)
		}
	}

	// Receive should maintain FIFO order
	for i := 0; i < 10; i++ {
		msg, err := uc.Receive()
		if err != nil {
			t.Fatalf("receive %d failed: %v", i, err)
		}
		if msg != i {
			t.Errorf("expected %d, got %d - ordering violated", i, msg)
		}
	}
}

func TestUnboundedChannel_ReceiveContextCancellation(t *testing.T) {
	uc := NewUnboundedChannel[int]()
	defer uc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Receive on empty channel should block until context cancels
	_, err := uc.ReceiveWithContext(ctx)
	if err == nil {
		t.Error("expected error on cancelled context")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestUnboundedChannel_TryReceive(t *testing.T) {
	uc := NewUnboundedChannel[int]()
	defer uc.Close()

	// Empty channel
	_, ok := uc.TryReceive()
	if ok {
		t.Error("TryReceive should return false on empty channel")
	}

	// Send and try receive
	uc.Send(42)
	msg, ok := uc.TryReceive()
	if !ok {
		t.Error("TryReceive should return true when message available")
	}
	if msg != 42 {
		t.Errorf("expected 42, got %d", msg)
	}
}

func TestUnboundedChannel_Close(t *testing.T) {
	uc := NewUnboundedChannel[int]()

	// Send before close
	uc.Send(1)
	uc.Send(2)

	// Close
	uc.Close()

	if !uc.IsClosed() {
		t.Error("channel should be closed")
	}

	// Send after close should fail
	err := uc.Send(3)
	if err != ErrChannelClosed {
		t.Errorf("expected ErrChannelClosed, got %v", err)
	}

	// Can still receive pending messages
	msg, err := uc.Receive()
	if err != nil {
		t.Fatalf("should receive pending message: %v", err)
	}
	if msg != 1 {
		t.Errorf("expected 1, got %d", msg)
	}

	msg, err = uc.Receive()
	if err != nil {
		t.Fatalf("should receive pending message: %v", err)
	}
	if msg != 2 {
		t.Errorf("expected 2, got %d", msg)
	}

	// After draining, receive should fail
	_, err = uc.Receive()
	if err != ErrChannelClosed {
		t.Errorf("expected ErrChannelClosed after drain, got %v", err)
	}
}

func TestUnboundedChannel_DoubleClose(t *testing.T) {
	uc := NewUnboundedChannel[int]()

	// Double close should not panic
	uc.Close()
	uc.Close()

	if !uc.IsClosed() {
		t.Error("channel should be closed")
	}
}

func TestUnboundedChannel_Stats(t *testing.T) {
	uc := NewUnboundedChannelWithCapacity[int](2)
	defer uc.Close()

	// Send messages
	for i := 0; i < 5; i++ {
		uc.Send(i)
	}

	stats := uc.Stats()
	if stats.SendCount != 5 {
		t.Errorf("expected SendCount 5, got %d", stats.SendCount)
	}
	if stats.OverflowLen == 0 {
		t.Error("expected overflow to be used")
	}
	if stats.IsClosed {
		t.Error("channel should not be closed")
	}

	// Receive some
	uc.Receive()
	uc.Receive()

	stats = uc.Stats()
	if stats.ReceiveCount != 2 {
		t.Errorf("expected ReceiveCount 2, got %d", stats.ReceiveCount)
	}
}

func TestUnboundedChannel_ConcurrentSendReceive(t *testing.T) {
	uc := NewUnboundedChannel[int]()
	defer uc.Close()

	const numSenders = 10
	const numMessages = 100
	const totalMessages = numSenders * numMessages

	var wg sync.WaitGroup
	var sendCount, recvCount atomic.Int64

	// Start senders
	for s := 0; s < numSenders; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < numMessages; i++ {
				if err := uc.Send(i); err != nil {
					return
				}
				sendCount.Add(1)
			}
		}()
	}

	// Start receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		for recvCount.Load() < totalMessages {
			_, ok := uc.TryReceive()
			if ok {
				recvCount.Add(1)
			}
			// Yield to other goroutines
			time.Sleep(time.Microsecond)
		}
	}()

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent test timed out")
	}

	if sendCount.Load() != totalMessages {
		t.Errorf("expected %d sends, got %d", totalMessages, sendCount.Load())
	}
}

func TestUnboundedChannel_DrainOverflowFirst(t *testing.T) {
	// Force overflow by using capacity 1
	uc := NewUnboundedChannelWithCapacity[int](1)
	defer uc.Close()

	// Send 3 messages: 1 in channel, 2 in overflow
	uc.Send(1)
	uc.Send(2)
	uc.Send(3)

	// First message should be from channel (1)
	msg, _ := uc.Receive()
	if msg != 1 {
		t.Errorf("expected 1, got %d", msg)
	}

	// Overflow should be drained in order (2, then 3)
	msg, _ = uc.Receive()
	if msg != 2 {
		t.Errorf("expected 2, got %d", msg)
	}

	msg, _ = uc.Receive()
	if msg != 3 {
		t.Errorf("expected 3, got %d", msg)
	}
}

func TestUnboundedChannel_ReceiveBlocksUntilSend(t *testing.T) {
	uc := NewUnboundedChannel[int]()
	defer uc.Close()

	received := make(chan int, 1)

	// Start receiver
	go func() {
		msg, err := uc.Receive()
		if err == nil {
			received <- msg
		}
	}()

	// Small delay then send
	time.Sleep(10 * time.Millisecond)
	uc.Send(42)

	select {
	case msg := <-received:
		if msg != 42 {
			t.Errorf("expected 42, got %d", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("receiver did not get message")
	}
}

func TestUnboundedChannel_TryReceiveFromOverflow(t *testing.T) {
	uc := NewUnboundedChannelWithCapacity[int](1)
	defer uc.Close()

	// Fill channel and overflow
	uc.Send(1)
	uc.Send(2) // Goes to overflow

	// Drain channel first
	msg, ok := uc.TryReceive()
	if !ok || msg != 1 {
		t.Errorf("expected 1 from channel, got %d", msg)
	}

	// Then overflow
	msg, ok = uc.TryReceive()
	if !ok || msg != 2 {
		t.Errorf("expected 2 from overflow, got %d", msg)
	}

	// Empty
	_, ok = uc.TryReceive()
	if ok {
		t.Error("expected empty channel")
	}
}

func TestUnboundedChannel_ChannelLen(t *testing.T) {
	uc := NewUnboundedChannelWithCapacity[int](10)
	defer uc.Close()

	// Initially empty
	if uc.ChannelLen() != 0 {
		t.Errorf("expected 0, got %d", uc.ChannelLen())
	}

	// Add messages
	for i := 0; i < 5; i++ {
		uc.Send(i)
	}

	if uc.ChannelLen() != 5 {
		t.Errorf("expected 5, got %d", uc.ChannelLen())
	}
}
