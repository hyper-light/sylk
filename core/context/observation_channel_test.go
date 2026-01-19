package context

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency/safechan"
	"github.com/stretchr/testify/assert"
)

// TestNewObservationChannel tests the constructor with various buffer sizes.
func TestNewObservationChannel(t *testing.T) {
	t.Run("positive buffer size", func(t *testing.T) {
		ch := NewObservationChannel(50)
		assert.NotNil(t, ch)
		assert.Equal(t, 50, ch.bufferSize)
		assert.False(t, ch.IsClosed())
		assert.Equal(t, 0, ch.Len())
	})

	t.Run("default buffer size when zero", func(t *testing.T) {
		ch := NewObservationChannel(0)
		assert.NotNil(t, ch)
		assert.Equal(t, defaultBufferSize, ch.bufferSize)
		assert.Equal(t, 100, ch.bufferSize)
	})

	t.Run("default buffer size when negative", func(t *testing.T) {
		ch := NewObservationChannel(-10)
		assert.NotNil(t, ch)
		assert.Equal(t, defaultBufferSize, ch.bufferSize)
	})

	t.Run("large buffer size", func(t *testing.T) {
		ch := NewObservationChannel(10000)
		assert.NotNil(t, ch)
		assert.Equal(t, 10000, ch.bufferSize)
	})
}

// TestObservationChannelSend tests the Send method.
func TestObservationChannelSend(t *testing.T) {
	t.Run("successful send", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx := context.Background()
		obs := EpisodeObservation{TaskCompleted: true}

		err := ch.Send(ctx, obs)
		assert.NoError(t, err)
		assert.Equal(t, 1, ch.Len())
	})

	t.Run("multiple sends", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx := context.Background()

		for i := 0; i < 5; i++ {
			obs := EpisodeObservation{FollowUpCount: i}
			err := ch.Send(ctx, obs)
			assert.NoError(t, err)
		}
		assert.Equal(t, 5, ch.Len())
	})

	t.Run("send on closed channel", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx := context.Background()
		obs := EpisodeObservation{TaskCompleted: true}

		err := ch.Close()
		assert.NoError(t, err)

		err = ch.Send(ctx, obs)
		assert.Error(t, err)
		assert.Equal(t, safechan.ErrChannelClosed, err)
	})

	t.Run("send with cancelled context on full buffer", func(t *testing.T) {
		ch := NewObservationChannel(1) // Small buffer
		ctx := context.Background()

		// Fill the buffer first
		obs := EpisodeObservation{TaskCompleted: true}
		err := ch.Send(ctx, obs)
		assert.NoError(t, err)

		// Now use cancelled context - send should fail since buffer is full
		ctx2, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		err = ch.Send(ctx2, obs)
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, err)
	})

	t.Run("send with deadline exceeded", func(t *testing.T) {
		ch := NewObservationChannel(1) // Small buffer
		ctx := context.Background()

		// Fill the buffer
		obs := EpisodeObservation{TaskCompleted: true}
		err := ch.Send(ctx, obs)
		assert.NoError(t, err)

		// Now send with a timeout context to a full channel
		ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		err = ch.Send(ctx2, obs)
		assert.Error(t, err)
		assert.Equal(t, context.DeadlineExceeded, err)
	})
}

// TestObservationChannelReceive tests the Receive method.
func TestObservationChannelReceive(t *testing.T) {
	t.Run("successful receive", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx := context.Background()
		sent := EpisodeObservation{TaskCompleted: true, FollowUpCount: 42}

		err := ch.Send(ctx, sent)
		assert.NoError(t, err)

		received, err := ch.Receive(ctx)
		assert.NoError(t, err)
		assert.Equal(t, sent.TaskCompleted, received.TaskCompleted)
		assert.Equal(t, sent.FollowUpCount, received.FollowUpCount)
		assert.Equal(t, 0, ch.Len())
	})

	t.Run("receive from closed channel", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx := context.Background()

		err := ch.Close()
		assert.NoError(t, err)

		_, err = ch.Receive(ctx)
		assert.Error(t, err)
		assert.Equal(t, safechan.ErrChannelClosed, err)
	})

	t.Run("receive from closed channel with remaining items", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx := context.Background()

		// Send some items
		obs1 := EpisodeObservation{FollowUpCount: 1}
		obs2 := EpisodeObservation{FollowUpCount: 2}
		ch.Send(ctx, obs1)
		ch.Send(ctx, obs2)

		// Close the channel
		err := ch.Close()
		assert.NoError(t, err)

		// Should still be able to receive remaining items
		received1, err := ch.Receive(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, received1.FollowUpCount)

		received2, err := ch.Receive(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 2, received2.FollowUpCount)

		// Now should get channel closed error
		_, err = ch.Receive(ctx)
		assert.Error(t, err)
		assert.Equal(t, safechan.ErrChannelClosed, err)
	})

	t.Run("receive with cancelled context", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := ch.Receive(ctx)
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, err)
	})

	t.Run("receive with deadline exceeded", func(t *testing.T) {
		ch := NewObservationChannel(10) // Empty channel
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err := ch.Receive(ctx)
		assert.Error(t, err)
		assert.Equal(t, context.DeadlineExceeded, err)
	})
}

// TestObservationChannelTrySend tests the TrySend method.
func TestObservationChannelTrySend(t *testing.T) {
	t.Run("successful try send", func(t *testing.T) {
		ch := NewObservationChannel(10)
		obs := EpisodeObservation{TaskCompleted: true}

		ok := ch.TrySend(obs)
		assert.True(t, ok)
		assert.Equal(t, 1, ch.Len())
	})

	t.Run("try send to full channel", func(t *testing.T) {
		ch := NewObservationChannel(2) // Small buffer
		obs := EpisodeObservation{TaskCompleted: true}

		// Fill the buffer
		ok := ch.TrySend(obs)
		assert.True(t, ok)
		ok = ch.TrySend(obs)
		assert.True(t, ok)

		// Buffer is now full
		ok = ch.TrySend(obs)
		assert.False(t, ok)
		assert.Equal(t, 2, ch.Len())
	})

	t.Run("try send to closed channel", func(t *testing.T) {
		ch := NewObservationChannel(10)
		obs := EpisodeObservation{TaskCompleted: true}

		err := ch.Close()
		assert.NoError(t, err)

		ok := ch.TrySend(obs)
		assert.False(t, ok)
	})
}

// TestObservationChannelClose tests the Close method.
func TestObservationChannelClose(t *testing.T) {
	t.Run("successful close", func(t *testing.T) {
		ch := NewObservationChannel(10)
		assert.False(t, ch.IsClosed())

		err := ch.Close()
		assert.NoError(t, err)
		assert.True(t, ch.IsClosed())
	})

	t.Run("double close returns error", func(t *testing.T) {
		ch := NewObservationChannel(10)

		err := ch.Close()
		assert.NoError(t, err)

		err = ch.Close()
		assert.Error(t, err)
		assert.Equal(t, ErrChannelAlreadyClosed, err)
	})

	t.Run("multiple close returns error consistently", func(t *testing.T) {
		ch := NewObservationChannel(10)

		err := ch.Close()
		assert.NoError(t, err)

		for i := 0; i < 5; i++ {
			err = ch.Close()
			assert.Error(t, err)
			assert.Equal(t, ErrChannelAlreadyClosed, err)
		}
	})
}

// TestObservationChannelIsClosed tests the IsClosed method.
func TestObservationChannelIsClosed(t *testing.T) {
	t.Run("initially not closed", func(t *testing.T) {
		ch := NewObservationChannel(10)
		assert.False(t, ch.IsClosed())
	})

	t.Run("closed after Close()", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ch.Close()
		assert.True(t, ch.IsClosed())
	})

	t.Run("remains closed after operations", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ch.Close()

		// Try various operations
		ch.TrySend(EpisodeObservation{})
		ch.Send(context.Background(), EpisodeObservation{})
		ch.Receive(context.Background())

		assert.True(t, ch.IsClosed())
	})
}

// TestObservationChannelLen tests the Len method.
func TestObservationChannelLen(t *testing.T) {
	t.Run("empty channel", func(t *testing.T) {
		ch := NewObservationChannel(10)
		assert.Equal(t, 0, ch.Len())
	})

	t.Run("after send", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx := context.Background()

		for i := 1; i <= 5; i++ {
			ch.Send(ctx, EpisodeObservation{})
			assert.Equal(t, i, ch.Len())
		}
	})

	t.Run("after receive", func(t *testing.T) {
		ch := NewObservationChannel(10)
		ctx := context.Background()

		// Send 5 items
		for i := 0; i < 5; i++ {
			ch.Send(ctx, EpisodeObservation{})
		}
		assert.Equal(t, 5, ch.Len())

		// Receive 3 items
		for i := 0; i < 3; i++ {
			ch.Receive(ctx)
		}
		assert.Equal(t, 2, ch.Len())
	})
}

// TestObservationChannelContextCancellation tests context cancellation mid-operation.
func TestObservationChannelContextCancellation(t *testing.T) {
	t.Run("context cancelled during blocking send", func(t *testing.T) {
		ch := NewObservationChannel(1)
		ctx := context.Background()

		// Fill the buffer
		ch.Send(ctx, EpisodeObservation{})

		// Start a goroutine that cancels context after a delay
		ctx2, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		// This send should block then fail due to cancellation
		err := ch.Send(ctx2, EpisodeObservation{})
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, err)
	})

	t.Run("context cancelled during blocking receive", func(t *testing.T) {
		ch := NewObservationChannel(10) // Empty channel

		// Start a goroutine that cancels context after a delay
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		// This receive should block then fail due to cancellation
		_, err := ch.Receive(ctx)
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, err)
	})
}

// TestObservationChannelConcurrentSend tests concurrent sends from multiple goroutines.
func TestObservationChannelConcurrentSend(t *testing.T) {
	ch := NewObservationChannel(1000)
	ctx := context.Background()
	numGoroutines := 100
	sendsPerGoroutine := 10

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < sendsPerGoroutine; j++ {
				obs := EpisodeObservation{FollowUpCount: id*sendsPerGoroutine + j}
				if err := ch.Send(ctx, obs); err == nil {
					successCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()
	expected := int32(numGoroutines * sendsPerGoroutine)
	assert.Equal(t, expected, successCount.Load())
	assert.Equal(t, int(expected), ch.Len())
}

// TestObservationChannelConcurrentReceive tests concurrent receives from multiple goroutines.
func TestObservationChannelConcurrentReceive(t *testing.T) {
	ch := NewObservationChannel(1000)
	ctx := context.Background()
	numItems := 1000

	// Pre-fill the channel
	for i := 0; i < numItems; i++ {
		ch.Send(ctx, EpisodeObservation{FollowUpCount: i})
	}
	assert.Equal(t, numItems, ch.Len())

	numGoroutines := 10
	var wg sync.WaitGroup
	var receiveCount atomic.Int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				_, err := ch.Receive(ctx)
				if err != nil {
					return
				}
				receiveCount.Add(1)
			}
		}()
	}

	// Wait a bit then close to signal goroutines to stop
	time.Sleep(100 * time.Millisecond)
	ch.Close()

	wg.Wait()
	assert.Equal(t, int32(numItems), receiveCount.Load())
	assert.Equal(t, 0, ch.Len())
}

// TestObservationChannelConcurrentSendReceive tests concurrent send and receive operations.
func TestObservationChannelConcurrentSendReceive(t *testing.T) {
	ch := NewObservationChannel(100)
	ctx := context.Background()
	numItems := 1000

	var sendWg sync.WaitGroup
	var receiveWg sync.WaitGroup
	var sendCount atomic.Int32
	var receiveCount atomic.Int32

	// Start receivers
	for i := 0; i < 5; i++ {
		receiveWg.Add(1)
		go func() {
			defer receiveWg.Done()
			for {
				_, err := ch.Receive(ctx)
				if err != nil {
					return
				}
				receiveCount.Add(1)
			}
		}()
	}

	// Start senders
	for i := 0; i < 5; i++ {
		sendWg.Add(1)
		go func() {
			defer sendWg.Done()
			for j := 0; j < numItems/5; j++ {
				if err := ch.Send(ctx, EpisodeObservation{}); err == nil {
					sendCount.Add(1)
				}
			}
		}()
	}

	// Wait for all sends to complete
	sendWg.Wait()

	// Give receivers time to drain
	time.Sleep(100 * time.Millisecond)

	// Close to signal receivers to stop
	ch.Close()
	receiveWg.Wait()

	assert.Equal(t, int32(numItems), sendCount.Load())
	assert.Equal(t, int32(numItems), receiveCount.Load())
}

// TestObservationChannelConcurrentClose tests concurrent close operations.
func TestObservationChannelConcurrentClose(t *testing.T) {
	ch := NewObservationChannel(10)
	numGoroutines := 100

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var errorCount atomic.Int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := ch.Close()
			if err == nil {
				successCount.Add(1)
			} else {
				errorCount.Add(1)
			}
		}()
	}

	wg.Wait()

	// Exactly one close should succeed
	assert.Equal(t, int32(1), successCount.Load())
	assert.Equal(t, int32(numGoroutines-1), errorCount.Load())
	assert.True(t, ch.IsClosed())
}

// TestObservationChannelConcurrentTrySend tests concurrent TrySend operations.
func TestObservationChannelConcurrentTrySend(t *testing.T) {
	ch := NewObservationChannel(50)
	numGoroutines := 100
	trySendsPerGoroutine := 10

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < trySendsPerGoroutine; j++ {
				if ch.TrySend(EpisodeObservation{}) {
					successCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// All successful sends should be in the channel
	assert.Equal(t, int32(ch.Len()), successCount.Load())
	// Buffer is 50, so at most 50 should succeed
	assert.LessOrEqual(t, ch.Len(), 50)
}

// TestObservationChannelConcurrentIsClosedCheck tests concurrent IsClosed checks.
func TestObservationChannelConcurrentIsClosedCheck(t *testing.T) {
	ch := NewObservationChannel(10)
	numGoroutines := 100

	var wg sync.WaitGroup
	var closedBeforeClose atomic.Int32
	var closedAfterClose atomic.Int32

	// Check IsClosed before close
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ch.IsClosed() {
				closedBeforeClose.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(0), closedBeforeClose.Load())

	// Close the channel
	ch.Close()

	// Check IsClosed after close
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ch.IsClosed() {
				closedAfterClose.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(numGoroutines), closedAfterClose.Load())
}

// TestObservationChannelZeroBufferSize tests behavior with zero buffer size (should use default).
func TestObservationChannelZeroBufferSize(t *testing.T) {
	ch := NewObservationChannel(0)
	ctx := context.Background()

	// Should work normally with default buffer size
	for i := 0; i < 50; i++ {
		err := ch.Send(ctx, EpisodeObservation{})
		assert.NoError(t, err)
	}
	assert.Equal(t, 50, ch.Len())

	// TrySend should also work
	ok := ch.TrySend(EpisodeObservation{})
	assert.True(t, ok)
	assert.Equal(t, 51, ch.Len())
}

// TestObservationChannelSendReceiveOrder tests that FIFO order is maintained.
func TestObservationChannelSendReceiveOrder(t *testing.T) {
	ch := NewObservationChannel(100)
	ctx := context.Background()
	numItems := 50

	// Send items with sequential FollowUpCount
	for i := 0; i < numItems; i++ {
		ch.Send(ctx, EpisodeObservation{FollowUpCount: i})
	}

	// Receive and verify order
	for i := 0; i < numItems; i++ {
		obs, err := ch.Receive(ctx)
		assert.NoError(t, err)
		assert.Equal(t, i, obs.FollowUpCount)
	}
}

// TestObservationChannelDataIntegrity tests that sent data is received correctly.
func TestObservationChannelDataIntegrity(t *testing.T) {
	ch := NewObservationChannel(10)
	ctx := context.Background()

	sent := EpisodeObservation{
		Timestamp:       time.Now(),
		Position:        12345,
		TaskContext:     "test-context",
		TaskCompleted:   true,
		FollowUpCount:   5,
		ToolCallCount:   10,
		UserEdits:       3,
		HedgingDetected: true,
		SessionDuration: 5 * time.Minute,
		ExplicitSignals: []string{"good", "helpful"},
		PrefetchedIDs:   []string{"id1", "id2"},
		UsedIDs:         []string{"id1"},
		SearchedAfter:   []string{"query1"},
	}

	err := ch.Send(ctx, sent)
	assert.NoError(t, err)

	received, err := ch.Receive(ctx)
	assert.NoError(t, err)

	assert.Equal(t, sent.Position, received.Position)
	assert.Equal(t, sent.TaskContext, received.TaskContext)
	assert.Equal(t, sent.TaskCompleted, received.TaskCompleted)
	assert.Equal(t, sent.FollowUpCount, received.FollowUpCount)
	assert.Equal(t, sent.ToolCallCount, received.ToolCallCount)
	assert.Equal(t, sent.UserEdits, received.UserEdits)
	assert.Equal(t, sent.HedgingDetected, received.HedgingDetected)
	assert.Equal(t, sent.SessionDuration, received.SessionDuration)
	assert.Equal(t, sent.ExplicitSignals, received.ExplicitSignals)
	assert.Equal(t, sent.PrefetchedIDs, received.PrefetchedIDs)
	assert.Equal(t, sent.UsedIDs, received.UsedIDs)
	assert.Equal(t, sent.SearchedAfter, received.SearchedAfter)
}

// TestObservationChannelDefaultBufferSize tests that the default buffer size constant is correct.
func TestObservationChannelDefaultBufferSize(t *testing.T) {
	assert.Equal(t, 100, defaultBufferSize)
}

// TestErrChannelAlreadyClosed tests the error value.
func TestErrChannelAlreadyClosed(t *testing.T) {
	assert.NotNil(t, ErrChannelAlreadyClosed)
	assert.Equal(t, "channel already closed", ErrChannelAlreadyClosed.Error())
}
