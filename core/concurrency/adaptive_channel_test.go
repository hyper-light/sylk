package concurrency

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewAdaptiveChannel(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())

	if ch.Size() != DefaultMinSize {
		t.Errorf("got size %d, want %d", ch.Size(), DefaultMinSize)
	}
	if ch.Len() != 0 {
		t.Errorf("got len %d, want 0", ch.Len())
	}
	if ch.OverflowLen() != 0 {
		t.Errorf("got overflow len %d, want 0", ch.OverflowLen())
	}
}

func TestAdaptiveChannel_SendReceive(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	if err := ch.Send(42); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	msg, err := ch.Receive()
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}
	if msg != 42 {
		t.Errorf("got %d, want 42", msg)
	}
}

func TestAdaptiveChannel_SendTimeout(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 1, MaxSize: 1, SendTimeout: 50 * time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	_ = ch.Send(1)

	err := ch.SendTimeout(2, 50*time.Millisecond)
	if err != ErrSendTimeout {
		t.Errorf("got error %v, want %v", err, ErrSendTimeout)
	}
}

func TestAdaptiveChannel_SendTimeout_OverflowAllowed(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 1, MaxSize: 1, AllowOverflow: true, SendTimeout: 50 * time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	_ = ch.Send(1)

	err := ch.SendTimeout(2, 50*time.Millisecond)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if ch.OverflowLen() == 0 {
		t.Error("expected overflow to be used")
	}
}

func TestAdaptiveChannel_ReceiveTimeout(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	_, err := ch.ReceiveTimeout(50 * time.Millisecond)
	if err != ErrReceiveTimeout {
		t.Errorf("got error %v, want %v", err, ErrReceiveTimeout)
	}
}

func TestAdaptiveChannel_TryReceive(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	_, ok := ch.TryReceive()
	if ok {
		t.Error("TryReceive should return false on empty channel")
	}

	_ = ch.Send(42)
	msg, ok := ch.TryReceive()
	if !ok {
		t.Error("TryReceive should return true after send")
	}
	if msg != 42 {
		t.Errorf("got %d, want 42", msg)
	}
}

func TestAdaptiveChannel_Close(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	ch.Close()

	if !ch.IsClosed() {
		t.Error("channel should be closed")
	}

	err := ch.Send(1)
	if err != ErrChannelClosed {
		t.Errorf("got error %v, want %v", err, ErrChannelClosed)
	}
}

func TestAdaptiveChannel_ResizeUp(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 4, MaxSize: 32, AllowOverflow: true, SendTimeout: 10 * time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	for i := 0; i < 10; i++ {
		_ = ch.SendTimeout(i, 10*time.Millisecond)
	}

	time.Sleep(AdaptInterval * 4)

	stats := ch.Stats()
	if stats.ResizeUpCount == 0 {
		t.Error("expected at least one resize up")
	}
	if ch.Size() <= 4 {
		t.Errorf("expected size > 4, got %d", ch.Size())
	}
}

func TestAdaptiveChannel_Overflow(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 2, MaxSize: 2, AllowOverflow: true, SendTimeout: 10 * time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	for i := 0; i < 5; i++ {
		_ = ch.SendTimeout(i, 10*time.Millisecond)
	}

	if ch.OverflowLen() == 0 {
		t.Error("expected overflow to be used")
	}

	received := make(map[int]bool)
	for i := 0; i < 5; i++ {
		msg, err := ch.ReceiveTimeout(100 * time.Millisecond)
		if err != nil {
			t.Fatalf("Receive %d failed: %v", i, err)
		}
		received[msg] = true
	}

	for i := 0; i < 5; i++ {
		if !received[i] {
			t.Errorf("missing value %d", i)
		}
	}
}

func TestAdaptiveChannel_Stats(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	_ = ch.Send(1)
	_ = ch.Send(2)
	_, _ = ch.Receive()

	stats := ch.Stats()
	if stats.SendCount != 2 {
		t.Errorf("got SendCount %d, want 2", stats.SendCount)
	}
	if stats.ReceiveCount != 1 {
		t.Errorf("got ReceiveCount %d, want 1", stats.ReceiveCount)
	}
	if stats.MessageCount != 1 {
		t.Errorf("got MessageCount %d, want 1", stats.MessageCount)
	}
}

func TestAdaptiveChannel_EnableDisableOverflow(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	if err := ch.EnableOverflow(); err != nil {
		t.Errorf("EnableOverflow failed: %v", err)
	}

	err := ch.EnableOverflow()
	if err != ErrOverflowEnabled {
		t.Errorf("got error %v, want %v", err, ErrOverflowEnabled)
	}

	ch.DisableOverflow()
}

func TestAdaptiveChannel_ConcurrentSendReceive(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	var wg sync.WaitGroup
	count := 100

	wg.Add(2)
	go sendN(ch, count, &wg)
	go receiveN(ch, count, &wg)
	wg.Wait()

	stats := ch.Stats()
	if stats.SendCount != int64(count) {
		t.Errorf("got SendCount %d, want %d", stats.SendCount, count)
	}
}

func sendN(ch *AdaptiveChannel[int], n int, wg *sync.WaitGroup) {
	defer wg.Done()
	for i := 0; i < n; i++ {
		ch.Send(i)
	}
}

func receiveN(ch *AdaptiveChannel[int], n int, wg *sync.WaitGroup) {
	defer wg.Done()
	for i := 0; i < n; i++ {
		ch.ReceiveTimeout(time.Second)
	}
}

func TestAdaptiveChannel_MaxSizeRespected(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 4, MaxSize: 8, SendTimeout: time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	for i := 0; i < 20; i++ {
		ch.SendTimeout(i, time.Millisecond)
	}

	time.Sleep(AdaptInterval * 4)

	if ch.Size() > 8 {
		t.Errorf("got size %d, want <= 8", ch.Size())
	}
}

func TestDefaultAdaptiveChannelConfig(t *testing.T) {
	config := DefaultAdaptiveChannelConfig()

	if config.MinSize != DefaultMinSize {
		t.Errorf("got MinSize %d, want %d", config.MinSize, DefaultMinSize)
	}
	if config.MaxSize != DefaultMaxSize {
		t.Errorf("got MaxSize %d, want %d", config.MaxSize, DefaultMaxSize)
	}
	if config.AllowOverflow {
		t.Error("AllowOverflow should be false by default")
	}
}

func TestNewAdaptiveChannelWithContext(t *testing.T) {
	ctx := context.Background()
	ch := NewAdaptiveChannelWithContext[int](ctx, DefaultAdaptiveChannelConfig())
	defer ch.Close()

	if ch.Size() != DefaultMinSize {
		t.Errorf("got size %d, want %d", ch.Size(), DefaultMinSize)
	}
	if ch.ctx != ctx {
		t.Error("context was not stored")
	}
}

func TestAdaptiveChannel_ResizeRaceNoMessageLoss(t *testing.T) {
	// Test for PF.5.9: Verify that no messages are lost during channel resize
	// This tests the blockSend version tracking mechanism that prevents lost messages
	// when resize replaces the channel while a send is in progress.

	t.Run("concurrent sends during resize", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:     4,
			MaxSize:     128,
			InitialSize: 4,
			SendTimeout: 0, // Use blocking send
		}
		ch := NewAdaptiveChannelWithContext[int](context.Background(), config)
		defer ch.Close()

		const numSenders = 10
		const messagesPerSender = 100
		totalExpected := numSenders * messagesPerSender

		var wg sync.WaitGroup
		received := make(chan int, totalExpected*2)

		// Start receiver
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < totalExpected; i++ {
				msg, err := ch.ReceiveTimeout(5 * time.Second)
				if err != nil {
					t.Errorf("receive failed: %v (received %d so far)", err, i)
					return
				}
				received <- msg
			}
		}()

		// Start multiple senders concurrently
		for i := 0; i < numSenders; i++ {
			wg.Add(1)
			go func(senderID int) {
				defer wg.Done()
				for j := 0; j < messagesPerSender; j++ {
					msg := senderID*messagesPerSender + j
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err := ch.SendWithContext(ctx, msg)
					cancel()
					if err != nil {
						t.Errorf("send failed for sender %d, msg %d: %v", senderID, j, err)
						return
					}
				}
			}(i)
		}

		// Wait for all goroutines to complete
		wg.Wait()
		close(received)

		// Verify all messages were received
		receivedSet := make(map[int]bool)
		for msg := range received {
			receivedSet[msg] = true
		}

		if len(receivedSet) != totalExpected {
			t.Errorf("expected %d unique messages, got %d", totalExpected, len(receivedSet))
		}

		// Check for any missing messages
		var missing []int
		for i := 0; i < totalExpected; i++ {
			if !receivedSet[i] {
				missing = append(missing, i)
			}
		}
		if len(missing) > 0 {
			t.Errorf("missing %d messages: %v (first 10)", len(missing), missing[:min(10, len(missing))])
		}
	})

	t.Run("force resize during blocked send", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:     2,
			MaxSize:     64,
			InitialSize: 2,
			SendTimeout: 0,
		}
		ch := NewAdaptiveChannelWithContext[int](context.Background(), config)
		defer ch.Close()

		// Fill the channel
		for i := 0; i < 2; i++ {
			_ = ch.Send(i)
		}

		const numSenders = 5
		const messagesPerSender = 50
		totalMessages := 2 + numSenders*messagesPerSender

		var wg sync.WaitGroup
		receivedCount := make(chan int, totalMessages)

		// Start senders that will block and trigger resize
		for i := 0; i < numSenders; i++ {
			wg.Add(1)
			go func(senderID int) {
				defer wg.Done()
				for j := 0; j < messagesPerSender; j++ {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					err := ch.SendWithContext(ctx, 100+senderID*100+j)
					cancel()
					if err != nil {
						t.Errorf("blocked send failed: %v", err)
						return
					}
				}
			}(i)
		}

		// Start receiver to drain and allow sends to complete
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < totalMessages; i++ {
				msg, err := ch.ReceiveTimeout(5 * time.Second)
				if err != nil {
					t.Errorf("receive failed: %v", err)
					return
				}
				receivedCount <- msg
			}
		}()

		wg.Wait()
		close(receivedCount)

		count := 0
		for range receivedCount {
			count++
		}

		if count != totalMessages {
			t.Errorf("expected %d messages, received %d", totalMessages, count)
		}
	})

	t.Run("version tracking prevents stale channel sends", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:     4,
			MaxSize:     32,
			InitialSize: 4,
			SendTimeout: 0,
		}
		ch := NewAdaptiveChannelWithContext[int](context.Background(), config)
		defer ch.Close()

		// Verify version starts at 0
		ch.mu.Lock()
		initialVersion := ch.version.Load()
		ch.mu.Unlock()

		if initialVersion != 0 {
			t.Errorf("expected initial version 0, got %d", initialVersion)
		}

		// Fill channel to trigger resize on next check
		for i := 0; i < 4; i++ {
			_ = ch.Send(i)
		}

		// Force resize by calling evaluateResize multiple times with high utilization
		for i := 0; i < HighWaterTrigger+1; i++ {
			ch.mu.Lock()
			ch.highWaterCnt = HighWaterTrigger + 1
			ch.resizeUpIfNeeded()
			ch.mu.Unlock()
		}

		// Check version incremented
		ch.mu.Lock()
		newVersion := ch.version.Load()
		ch.mu.Unlock()

		if newVersion <= initialVersion {
			t.Error("expected version to increment after resize")
		}
	})
}

func TestAdaptiveChannel_ContextCancellation(t *testing.T) {
	t.Run("adapt loop stops on context cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ch := NewAdaptiveChannelWithContext[int](ctx, DefaultAdaptiveChannelConfig())
		defer ch.Close()

		// Send some messages
		for i := 0; i < 5; i++ {
			err := ch.Send(i)
			if err != nil {
				t.Fatalf("Send failed: %v", err)
			}
		}

		// Cancel context - this should trigger the adapt loop to stop
		// Note: The channel is not automatically closed to avoid racing with ongoing operations
		cancel()

		// Give the goroutine time to respond to cancellation
		time.Sleep(100 * time.Millisecond)

		// Channel should still be usable (not closed) - caller is responsible for closing
		if ch.IsClosed() {
			t.Error("channel should not be automatically closed after context cancellation")
		}

		// Receive the messages we sent
		for i := 0; i < 5; i++ {
			_, err := ch.Receive()
			if err != nil {
				t.Fatalf("Receive failed: %v", err)
			}
		}
	})

	t.Run("operations work before context cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ch := NewAdaptiveChannelWithContext[int](ctx, DefaultAdaptiveChannelConfig())
		defer ch.Close()

		// Operations should work normally
		err := ch.Send(42)
		if err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		msg, err := ch.Receive()
		if err != nil {
			t.Fatalf("Receive failed: %v", err)
		}
		if msg != 42 {
			t.Errorf("got %d, want 42", msg)
		}
	})

	t.Run("context cancellation stops adapt loop without closing channel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ch := NewAdaptiveChannelWithContext[int](ctx, DefaultAdaptiveChannelConfig())
		defer ch.Close()

		// Send some messages
		sentCount := 0
		for i := 0; i < 10; i++ {
			err := ch.SendTimeout(i, 10*time.Millisecond)
			if err == nil {
				sentCount++
			}
		}

		// Cancel context
		cancel()

		// Give the goroutine time to respond to cancellation
		time.Sleep(100 * time.Millisecond)

		// Channel should still be usable - operations should work
		if ch.IsClosed() {
			t.Error("channel should not be automatically closed after context cancellation")
		}

		// Should be able to receive all sent messages
		for i := 0; i < sentCount; i++ {
			_, err := ch.ReceiveTimeout(100 * time.Millisecond)
			if err != nil {
				t.Fatalf("Receive %d failed: %v", i, err)
			}
		}
	})

	t.Run("backward compatible constructor uses background context", func(t *testing.T) {
		ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
		defer ch.Close()

		// Should work normally
		err := ch.Send(42)
		if err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		msg, err := ch.Receive()
		if err != nil {
			t.Fatalf("Receive failed: %v", err)
		}
		if msg != 42 {
			t.Errorf("got %d, want 42", msg)
		}
	})
}
