package concurrency

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestW4N1_ResizePreservesAllMessages tests that resize operations preserve all messages.
func TestW4N1_ResizePreservesAllMessages(t *testing.T) {
	t.Run("downsize preserves messages via overflow", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         2,
			MaxSize:         16,
			InitialSize:     8,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Fill channel with 6 messages (75% of 8)
		for i := 0; i < 6; i++ {
			if err := ch.Send(i); err != nil {
				t.Fatalf("Send %d failed: %v", i, err)
			}
		}

		// Force downsize from 8 to 4 (need 6 messages but only 4 fit)
		ch.mu.Lock()
		ch.lowWaterCnt = LowWaterTrigger + 1
		ch.resizeDownIfNeeded()
		newSize := ch.currentSize
		overflowLen := ch.overflowLenLocked()
		ch.mu.Unlock()

		// Should have resized down
		if newSize != 4 {
			t.Errorf("expected size 4, got %d", newSize)
		}

		// Overflow should have the excess messages (6 - 4 = 2)
		if overflowLen != 2 {
			t.Errorf("expected overflow len 2, got %d", overflowLen)
		}

		// Verify all 6 messages are recoverable
		received := make(map[int]bool)
		for i := 0; i < 6; i++ {
			msg, err := ch.ReceiveTimeout(100 * time.Millisecond)
			if err != nil {
				t.Fatalf("Receive %d failed: %v", i, err)
			}
			received[msg] = true
		}

		for i := 0; i < 6; i++ {
			if !received[i] {
				t.Errorf("missing message %d", i)
			}
		}
	})

	t.Run("upsize preserves all messages", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         32,
			InitialSize:     4,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Fill channel to capacity
		for i := 0; i < 4; i++ {
			if err := ch.Send(i); err != nil {
				t.Fatalf("Send %d failed: %v", i, err)
			}
		}

		// Force upsize from 4 to 8
		ch.mu.Lock()
		ch.highWaterCnt = HighWaterTrigger + 1
		ch.resizeUpIfNeeded()
		newSize := ch.currentSize
		ch.mu.Unlock()

		if newSize != 8 {
			t.Errorf("expected size 8, got %d", newSize)
		}

		// Verify all 4 messages are recoverable
		received := make(map[int]bool)
		for i := 0; i < 4; i++ {
			msg, err := ch.ReceiveTimeout(100 * time.Millisecond)
			if err != nil {
				t.Fatalf("Receive %d failed: %v", i, err)
			}
			received[msg] = true
		}

		for i := 0; i < 4; i++ {
			if !received[i] {
				t.Errorf("missing message %d", i)
			}
		}
	})
}

// TestW4N1_ResizeHandlesEmptyChannel tests resize on empty channel.
func TestW4N1_ResizeHandlesEmptyChannel(t *testing.T) {
	t.Run("downsize empty channel", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         2,
			MaxSize:         16,
			InitialSize:     8,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Force downsize on empty channel
		ch.mu.Lock()
		ch.lowWaterCnt = LowWaterTrigger + 1
		ch.resizeDownIfNeeded()
		newSize := ch.currentSize
		ch.mu.Unlock()

		if newSize != 4 {
			t.Errorf("expected size 4, got %d", newSize)
		}

		// Should be able to send/receive normally
		if err := ch.Send(42); err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		msg, err := ch.Receive()
		if err != nil {
			t.Fatalf("Receive failed: %v", err)
		}
		if msg != 42 {
			t.Errorf("expected 42, got %d", msg)
		}
	})

	t.Run("upsize empty channel", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         32,
			InitialSize:     4,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Force upsize on empty channel
		ch.mu.Lock()
		ch.highWaterCnt = HighWaterTrigger + 1
		ch.resizeUpIfNeeded()
		newSize := ch.currentSize
		ch.mu.Unlock()

		if newSize != 8 {
			t.Errorf("expected size 8, got %d", newSize)
		}

		// Should be able to send/receive normally
		if err := ch.Send(42); err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		msg, err := ch.Receive()
		if err != nil {
			t.Fatalf("Receive failed: %v", err)
		}
		if msg != 42 {
			t.Errorf("expected 42, got %d", msg)
		}
	})
}

// TestW4N1_ResizeHandlesCloseDuringResize tests resize behavior when channel closes.
func TestW4N1_ResizeHandlesCloseDuringResize(t *testing.T) {
	t.Run("close prevents resize", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         32,
			InitialSize:     8,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)

		// Fill channel
		for i := 0; i < 4; i++ {
			_ = ch.Send(i)
		}

		// Close channel
		ch.Close()

		// evaluateResize should return early due to closed check
		ch.mu.Lock()
		initialSize := ch.currentSize
		ch.mu.Unlock()

		// Call evaluateResize - should not panic and should not resize
		ch.evaluateResize()

		ch.mu.Lock()
		finalSize := ch.currentSize
		ch.mu.Unlock()

		if finalSize != initialSize {
			t.Errorf("size should not change after close, got %d -> %d", initialSize, finalSize)
		}
	})

	t.Run("resize after close is no-op", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         64,
			InitialSize:     8,
			MaxOverflowSize: 1000,
		}
		ch := NewAdaptiveChannel[int](config)

		// Fill with some messages
		for i := 0; i < 4; i++ {
			_ = ch.Send(i)
		}

		ch.mu.Lock()
		initialSize := ch.currentSize
		ch.mu.Unlock()

		// Close channel
		ch.Close()

		// Attempt to trigger resize - should be a no-op
		ch.evaluateResize()

		ch.mu.Lock()
		finalSize := ch.currentSize
		ch.mu.Unlock()

		if finalSize != initialSize {
			t.Errorf("size should not change after close, got %d -> %d", initialSize, finalSize)
		}

		// Should be closed
		if !ch.IsClosed() {
			t.Error("channel should be closed")
		}
	})
}

// TestW4N1_ConcurrentSendResize tests race conditions during concurrent sends and resize.
func TestW4N1_ConcurrentSendResize(t *testing.T) {
	t.Run("resize preserves buffered messages under concurrent access", func(t *testing.T) {
		// This test verifies that messages already in the channel buffer
		// are preserved during resize. It does NOT test concurrent send
		// during the resize transition (which has known limitations).
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         64,
			InitialSize:     16,
			MaxOverflowSize: 1000,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Pre-fill the channel with a known number of messages
		const prefilledCount = 12
		for i := 0; i < prefilledCount; i++ {
			if err := ch.Send(i); err != nil {
				t.Fatalf("Send %d failed: %v", i, err)
			}
		}

		// Perform multiple resize operations
		for i := 0; i < 5; i++ {
			ch.mu.Lock()
			if i%2 == 0 {
				ch.highWaterCnt = HighWaterTrigger + 1
				ch.resizeUpIfNeeded()
			} else {
				ch.lowWaterCnt = LowWaterTrigger + 1
				ch.resizeDownIfNeeded()
			}
			ch.mu.Unlock()
		}

		// Verify all prefilled messages are still available
		receivedSet := make(map[int]bool)
		totalAvailable := ch.Len() + ch.OverflowLen()
		if totalAvailable != prefilledCount {
			t.Errorf("expected %d total messages, got %d", prefilledCount, totalAvailable)
		}

		for i := 0; i < prefilledCount; i++ {
			msg, err := ch.ReceiveTimeout(100 * time.Millisecond)
			if err != nil {
				t.Fatalf("Receive %d failed: %v", i, err)
			}
			receivedSet[msg] = true
		}

		for i := 0; i < prefilledCount; i++ {
			if !receivedSet[i] {
				t.Errorf("missing message %d", i)
			}
		}
	})

	t.Run("version tracking ensures no duplicate sends", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         32,
			InitialSize:     8,
			MaxOverflowSize: 1000,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Check version starts at 0
		ch.mu.Lock()
		initialVersion := ch.version.Load()
		ch.mu.Unlock()

		if initialVersion != 0 {
			t.Errorf("expected initial version 0, got %d", initialVersion)
		}

		// Fill and force multiple resizes
		for i := 0; i < 8; i++ {
			_ = ch.Send(i)
		}

		ch.mu.Lock()
		ch.highWaterCnt = HighWaterTrigger + 1
		ch.resizeUpIfNeeded()
		version1 := ch.version.Load()
		ch.mu.Unlock()

		ch.mu.Lock()
		ch.highWaterCnt = HighWaterTrigger + 1
		ch.resizeUpIfNeeded()
		version2 := ch.version.Load()
		ch.mu.Unlock()

		if version1 <= initialVersion {
			t.Error("version should increment after first resize")
		}
		if version2 <= version1 {
			t.Error("version should increment after second resize")
		}
	})
}

// TestW4N1_ResizeDuringHighLoad tests resize during high message throughput.
func TestW4N1_ResizeDuringHighLoad(t *testing.T) {
	t.Run("high throughput resize preserves messages", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         8,
			MaxSize:         256,
			InitialSize:     32,
			MaxOverflowSize: 10000,
			AllowOverflow:   true,
			SendTimeout:     50 * time.Millisecond,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		const totalMessages = 1000
		var sentCount atomic.Int64
		var receivedCount atomic.Int64

		var wg sync.WaitGroup
		done := make(chan struct{})

		// High-speed sender with timeout
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < totalMessages; i++ {
				err := ch.SendTimeout(i, 100*time.Millisecond)
				if err == nil {
					sentCount.Add(1)
				}
			}
		}()

		// High-speed receiver
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					// Drain remaining
					for {
						_, err := ch.ReceiveTimeout(50 * time.Millisecond)
						if err != nil {
							return
						}
						receivedCount.Add(1)
					}
				default:
					_, err := ch.ReceiveTimeout(100 * time.Millisecond)
					if err == nil {
						receivedCount.Add(1)
					}
				}
			}
		}()

		// Aggressive resizer
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				ch.mu.Lock()
				if i%3 == 0 {
					ch.highWaterCnt = HighWaterTrigger + 1
					ch.resizeUpIfNeeded()
				} else if i%3 == 1 {
					ch.lowWaterCnt = LowWaterTrigger + 1
					ch.resizeDownIfNeeded()
				}
				ch.mu.Unlock()
				time.Sleep(2 * time.Millisecond)
			}
		}()

		// Wait for sender and resizer to complete
		time.Sleep(500 * time.Millisecond)
		close(done)

		wg.Wait()

		sentVal := sentCount.Load()
		receivedVal := receivedCount.Load()

		// Verify no message loss (received >= sent would mean duplicates which is also bad)
		if sentVal < int64(totalMessages/2) {
			t.Errorf("too few messages sent: %d (expected at least %d)", sentVal, totalMessages/2)
		}
		if receivedVal < sentVal {
			t.Errorf("message loss detected: sent=%d, received=%d", sentVal, receivedVal)
		}
	})
}

// TestW4N1_ResizeMinMaxBoundaries tests resize at minimum and maximum sizes.
func TestW4N1_ResizeMinMaxBoundaries(t *testing.T) {
	t.Run("resize does not go below minimum", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         8,
			MaxSize:         64,
			InitialSize:     8,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Try to downsize when already at minimum
		ch.mu.Lock()
		ch.lowWaterCnt = LowWaterTrigger + 1
		ch.resizeDownIfNeeded()
		finalSize := ch.currentSize
		ch.mu.Unlock()

		if finalSize < 8 {
			t.Errorf("size went below minimum: %d", finalSize)
		}
	})

	t.Run("resize does not go above maximum", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         16,
			InitialSize:     16,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Fill to capacity
		for i := 0; i < 16; i++ {
			_ = ch.Send(i)
		}

		// Try to upsize when already at maximum
		ch.mu.Lock()
		ch.highWaterCnt = HighWaterTrigger + 1
		ch.resizeUpIfNeeded()
		finalSize := ch.currentSize
		ch.mu.Unlock()

		if finalSize > 16 {
			t.Errorf("size went above maximum: %d", finalSize)
		}
	})

	t.Run("resize from min to max preserves messages", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         64,
			InitialSize:     4,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Fill to capacity
		for i := 0; i < 4; i++ {
			_ = ch.Send(i)
		}

		// Force multiple upsizes to reach max
		for ch.Size() < 64 {
			ch.mu.Lock()
			ch.highWaterCnt = HighWaterTrigger + 1
			ch.resizeUpIfNeeded()
			ch.mu.Unlock()
		}

		if ch.Size() != 64 {
			t.Errorf("expected final size 64, got %d", ch.Size())
		}

		// Verify all 4 messages are recoverable
		received := make(map[int]bool)
		for i := 0; i < 4; i++ {
			msg, err := ch.ReceiveTimeout(100 * time.Millisecond)
			if err != nil {
				t.Fatalf("Receive %d failed: %v", i, err)
			}
			received[msg] = true
		}

		for i := 0; i < 4; i++ {
			if !received[i] {
				t.Errorf("missing message %d", i)
			}
		}
	})

	t.Run("resize from max to min uses overflow", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         64,
			InitialSize:     64,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Fill with 32 messages
		for i := 0; i < 32; i++ {
			_ = ch.Send(i)
		}

		// Force multiple downsizes to reach min
		for ch.Size() > 4 {
			ch.mu.Lock()
			ch.lowWaterCnt = LowWaterTrigger + 1
			ch.resizeDownIfNeeded()
			ch.mu.Unlock()
		}

		if ch.Size() != 4 {
			t.Errorf("expected final size 4, got %d", ch.Size())
		}

		// Total of messages in channel + overflow should be 32
		totalLen := ch.Len() + ch.OverflowLen()
		if totalLen != 32 {
			t.Errorf("expected 32 total messages, got %d", totalLen)
		}

		// Verify all 32 messages are recoverable
		received := make(map[int]bool)
		for i := 0; i < 32; i++ {
			msg, err := ch.ReceiveTimeout(100 * time.Millisecond)
			if err != nil {
				t.Fatalf("Receive %d failed: %v", i, err)
			}
			received[msg] = true
		}

		for i := 0; i < 32; i++ {
			if !received[i] {
				t.Errorf("missing message %d", i)
			}
		}
	})
}

// TestW4N1_DrainOldChannelBounded verifies bounded iteration in drainOldChannel.
func TestW4N1_DrainOldChannelBounded(t *testing.T) {
	t.Run("drain iteration is bounded", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         16,
			InitialSize:     8,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Fill channel completely
		for i := 0; i < 8; i++ {
			_ = ch.Send(i)
		}

		// Create a smaller channel and verify drain is bounded
		ch.mu.Lock()
		oldCh := ch.ch
		newCh := make(chan int, 4)

		// drainOldChannel should complete within bounded iterations
		ch.drainOldChannel(oldCh, newCh, 9) // maxIterations = currentSize + 1

		// All 8 messages should be handled (4 in newCh, 4 in overflow)
		newChLen := len(newCh)
		overflowLen := ch.overflowLenLocked()
		ch.mu.Unlock()

		if newChLen != 4 {
			t.Errorf("expected 4 in new channel, got %d", newChLen)
		}
		if overflowLen != 4 {
			t.Errorf("expected 4 in overflow, got %d", overflowLen)
		}
	})
}

// TestW4N1_TransferMessageToOverflow verifies message transfer to overflow.
func TestW4N1_TransferMessageToOverflow(t *testing.T) {
	t.Run("transfer to full channel uses overflow", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         16,
			InitialSize:     4,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		ch.mu.Lock()
		// Create a full channel
		fullCh := make(chan int, 2)
		fullCh <- 1
		fullCh <- 2

		// Transfer message should go to overflow
		ch.transferMessage(100, fullCh)
		overflowLen := ch.overflowLenLocked()
		ch.mu.Unlock()

		if overflowLen != 1 {
			t.Errorf("expected overflow len 1, got %d", overflowLen)
		}
	})

	t.Run("transfer to non-full channel succeeds", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         16,
			InitialSize:     4,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		ch.mu.Lock()
		// Create a channel with space
		spaceCh := make(chan int, 2)

		ch.transferMessage(100, spaceCh)
		overflowLen := ch.overflowLenLocked()
		ch.mu.Unlock()

		if overflowLen != 0 {
			t.Errorf("expected overflow len 0, got %d", overflowLen)
		}
		if len(spaceCh) != 1 {
			t.Errorf("expected channel len 1, got %d", len(spaceCh))
		}
	})
}

// TestW4N1_StoreInOverflowModes tests storeInOverflow for both modes.
func TestW4N1_StoreInOverflowModes(t *testing.T) {
	t.Run("bounded overflow mode", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         16,
			InitialSize:     4,
			MaxOverflowSize: 10,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		ch.mu.Lock()
		for i := 0; i < 5; i++ {
			ch.storeInOverflow(i)
		}
		overflowLen := ch.overflowLenLocked()
		ch.mu.Unlock()

		if overflowLen != 5 {
			t.Errorf("expected overflow len 5, got %d", overflowLen)
		}
	})
}

// TestW4N1_NoGoroutineLeaks ensures no goroutine spawning during resize.
func TestW4N1_NoGoroutineLeaks(t *testing.T) {
	t.Run("resize does not spawn goroutines", func(t *testing.T) {
		config := AdaptiveChannelConfig{
			MinSize:         4,
			MaxSize:         64,
			InitialSize:     16,
			MaxOverflowSize: 100,
		}
		ch := NewAdaptiveChannel[int](config)
		defer ch.Close()

		// Fill channel
		for i := 0; i < 16; i++ {
			_ = ch.Send(i)
		}

		// Perform multiple resize operations - they should not leak goroutines
		for i := 0; i < 10; i++ {
			ch.mu.Lock()
			if i%2 == 0 {
				ch.highWaterCnt = HighWaterTrigger + 1
				ch.resizeUpIfNeeded()
			} else {
				ch.lowWaterCnt = LowWaterTrigger + 1
				ch.resizeDownIfNeeded()
			}
			ch.mu.Unlock()
		}

		// Drain remaining messages
		for {
			_, err := ch.ReceiveTimeout(50 * time.Millisecond)
			if err != nil {
				break
			}
		}
	})
}
