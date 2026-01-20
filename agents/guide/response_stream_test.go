package guide_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ResponseStream Tests - W12.1 Fix Verification
// =============================================================================

func TestResponseStream_Close_MultipleCallsSafe(t *testing.T) {
	// Test that multiple Close() calls do not panic
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, err := sm.CreateStream("corr-1", "sess-1")
	require.NoError(t, err)
	require.NotNil(t, stream)

	// Close multiple times - should not panic
	stream.Close()
	stream.Close()
	stream.Close()

	assert.True(t, stream.IsClosed())
}

func TestResponseStream_Close_ConcurrentCallsSafe(t *testing.T) {
	// Test that concurrent Close() calls do not cause race or panic
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, err := sm.CreateStream("corr-concurrent", "sess-concurrent")
	require.NoError(t, err)

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Concurrent Close() calls
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			stream.Close()
		}()
	}

	wg.Wait()
	assert.True(t, stream.IsClosed())
}

func TestResponseStream_SendAfterClose_ReturnsFalse(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, err := sm.CreateStream("corr-send-after", "sess-send-after")
	require.NoError(t, err)

	// Drain the start event
	select {
	case <-stream.Events():
	case <-time.After(100 * time.Millisecond):
	}

	stream.Close()

	// All send methods should return false after close
	assert.False(t, stream.SendData("test"))
	assert.False(t, stream.SendText("test"))
	assert.False(t, stream.SendProgress(1, 10, "progress"))
	assert.False(t, stream.SendHeartbeat())
	assert.False(t, stream.SendComplete("result"))
	assert.False(t, stream.SendError(errors.New("test error")))
}

func TestResponseStream_ConcurrentSendAndClose(t *testing.T) {
	// Test concurrent sends during close do not panic
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        1000,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
	})
	stream, err := sm.CreateStream("corr-race", "sess-race")
	require.NoError(t, err)

	const senders = 50
	var wg sync.WaitGroup
	wg.Add(senders + 1)

	// Start consumer to drain events
	done := make(chan struct{})
	go func() {
		for range stream.Events() {
			// Drain events
		}
		close(done)
	}()

	// Concurrent senders
	for i := 0; i < senders; i++ {
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				stream.SendText("message")
				stream.SendProgress(j, 10, "progress")
			}
		}(i)
	}

	// Closer goroutine
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond * 5)
		stream.Close()
	}()

	wg.Wait()

	// Wait for consumer to finish
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not finish")
	}

	assert.True(t, stream.IsClosed())
}

func TestResponseStream_CloseOnce_ChannelClosedExactlyOnce(t *testing.T) {
	// Verify that the events channel is closed exactly once
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, err := sm.CreateStream("corr-once", "sess-once")
	require.NoError(t, err)

	var closeCount int32

	// Spawn goroutine to count channel closes by reading until closed
	go func() {
		for range stream.Events() {
			// Consume events
		}
		atomic.AddInt32(&closeCount, 1)
	}()

	// Multiple close attempts
	stream.Close()
	stream.Close()
	stream.Close()

	// Give time for consumer to detect close
	time.Sleep(50 * time.Millisecond)

	// Channel should have been closed exactly once
	assert.Equal(t, int32(1), atomic.LoadInt32(&closeCount))
}

func TestResponseStream_RaceCondition_CloseAndSend(t *testing.T) {
	// Stress test for race conditions
	for iteration := 0; iteration < 100; iteration++ {
		sm := guide.NewStreamManager(guide.StreamConfig{
			MaxBufferSize:        10,
			StreamTimeout:        5 * time.Minute,
			HeartbeatInterval:    30 * time.Second,
			MaxStreamsPerSession: 100,
		})
		stream, err := sm.CreateStream("corr-stress", "sess-stress")
		require.NoError(t, err)

		var wg sync.WaitGroup
		const workers = 10
		wg.Add(workers * 2)

		// Senders
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < 5; j++ {
					stream.SendText("data")
				}
			}()
		}

		// Closers
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				stream.Close()
			}()
		}

		wg.Wait()
		assert.True(t, stream.IsClosed())
	}
}

// =============================================================================
// StreamManager Tests
// =============================================================================

func TestStreamManager_CreateStream_W12(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	stream, err := sm.CreateStream("corr-1", "sess-1")
	require.NoError(t, err)
	require.NotNil(t, stream)

	assert.Equal(t, "corr-1", stream.CorrelationID)
	assert.Equal(t, "sess-1", stream.SessionID)
	assert.False(t, stream.IsClosed())
}

func TestStreamManager_CreateStream_Duplicate(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	stream1, err := sm.CreateStream("corr-dup", "sess-1")
	require.NoError(t, err)

	stream2, err := sm.CreateStream("corr-dup", "sess-1")
	require.NoError(t, err)

	// Should return same stream
	assert.Equal(t, stream1.ID, stream2.ID)
}

func TestStreamManager_GetStream_W12(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	stream, err := sm.CreateStream("corr-get", "sess-1")
	require.NoError(t, err)

	retrieved := sm.GetStream("corr-get")
	assert.Equal(t, stream.ID, retrieved.ID)

	// Non-existent stream
	assert.Nil(t, sm.GetStream("non-existent"))
}

func TestStreamManager_CloseStream_W12(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	stream, err := sm.CreateStream("corr-close", "sess-1")
	require.NoError(t, err)

	sm.CloseStream("corr-close")

	assert.True(t, stream.IsClosed())
	assert.Nil(t, sm.GetStream("corr-close"))
}

func TestStreamManager_MaxStreamsPerSession(t *testing.T) {
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        10,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 2,
	})

	_, err := sm.CreateStream("corr-1", "sess-limited")
	require.NoError(t, err)

	_, err = sm.CreateStream("corr-2", "sess-limited")
	require.NoError(t, err)

	_, err = sm.CreateStream("corr-3", "sess-limited")
	assert.Equal(t, guide.ErrTooManyStreams, err)
}

func TestStreamManager_Stats_W12Fix(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())

	stream1, _ := sm.CreateStream("corr-1", "sess-1")
	stream2, _ := sm.CreateStream("corr-2", "sess-1")

	stats := sm.Stats()
	assert.Equal(t, int64(2), stats.TotalStreams)
	assert.Equal(t, 2, stats.ActiveStreams)

	sm.CloseStream("corr-1")
	stats = sm.Stats()
	assert.Equal(t, 1, stats.ActiveStreams)

	_ = stream1
	_ = stream2
}

// =============================================================================
// StreamConsumer Tests
// =============================================================================

func TestStreamConsumer_Next(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, err := sm.CreateStream("corr-consumer", "sess-1")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	consumer := guide.NewStreamConsumer(ctx, stream)

	// First event should be start event
	event, ok := consumer.Next()
	require.True(t, ok)
	assert.Equal(t, guide.StreamEventStart, event.Type)

	// Send a data event
	stream.SendText("hello")

	event, ok = consumer.Next()
	require.True(t, ok)
	assert.Equal(t, guide.StreamEventData, event.Type)
	assert.Equal(t, "hello", event.Text)

	stream.Close()
}

func TestStreamConsumer_ContextCancellation(t *testing.T) {
	sm := guide.NewStreamManager(guide.DefaultStreamConfig())
	stream, err := sm.CreateStream("corr-ctx-cancel", "sess-1")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	consumer := guide.NewStreamConsumer(ctx, stream)

	// Drain start event
	consumer.Next()

	// Cancel context
	cancel()

	// Next should return false
	_, ok := consumer.Next()
	assert.False(t, ok)

	stream.Close()
}

func TestStreamConsumer_Collect(t *testing.T) {
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        100,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
	})
	stream, err := sm.CreateStream("corr-collect", "sess-1")
	require.NoError(t, err)

	// Send some events
	stream.SendText("msg1")
	stream.SendText("msg2")
	stream.SendProgress(50, 100, "halfway")
	stream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	consumer := guide.NewStreamConsumer(ctx, stream)
	events := consumer.Collect()

	// Should have: start, data, data, progress, end
	assert.GreaterOrEqual(t, len(events), 4)
}

func TestStreamConsumer_CollectText_W12Fix(t *testing.T) {
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        100,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
	})
	stream, err := sm.CreateStream("corr-collect-text", "sess-1")
	require.NoError(t, err)

	stream.SendText("Hello ")
	stream.SendText("World")
	stream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	consumer := guide.NewStreamConsumer(ctx, stream)
	text := consumer.CollectText()

	assert.Equal(t, "Hello World", text)
}

// =============================================================================
// Edge Cases and Error Conditions
// =============================================================================

func TestResponseStream_SendEvent_BufferFull(t *testing.T) {
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        1, // Very small buffer
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
	})
	stream, err := sm.CreateStream("corr-buffer-full", "sess-1")
	require.NoError(t, err)

	// Buffer already has start event, so next sends should fail
	result := stream.SendText("msg1")
	// May or may not succeed depending on timing
	_ = result

	// Force buffer full
	for i := 0; i < 10; i++ {
		stream.SendText("overflow")
	}

	stream.Close()
	assert.True(t, stream.IsClosed())
}

func TestResponseStream_Stats(t *testing.T) {
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        100,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
	})
	stream, err := sm.CreateStream("corr-stats", "sess-1")
	require.NoError(t, err)

	// Drain start event
	<-stream.Events()

	stream.SendText("hello")
	stream.SendText("world")

	stats := stream.Stats()
	assert.Equal(t, "corr-stats", stats.CorrelationID)
	assert.GreaterOrEqual(t, stats.EventCount, int64(2))
	assert.False(t, stats.Closed)

	stream.Close()

	stats = stream.Stats()
	assert.True(t, stats.Closed)
}

func TestStreamManager_CleanupIdle(t *testing.T) {
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        10,
		StreamTimeout:        time.Millisecond, // Very short timeout
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
	})

	_, err := sm.CreateStream("corr-idle", "sess-1")
	require.NoError(t, err)

	// Wait for stream to become idle
	time.Sleep(10 * time.Millisecond)

	closed := sm.CleanupIdle()
	assert.Equal(t, 1, closed)
	assert.Nil(t, sm.GetStream("corr-idle"))
}

// =============================================================================
// W12.28 - Backpressure Tests
// =============================================================================

func TestResponseStream_Backpressure_WithTimeout(t *testing.T) {
	// W12.28: Test that backpressure timeout allows buffer to drain
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        2,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
		BackpressureTimeout:  50 * time.Millisecond,
	})
	stream, err := sm.CreateStream("corr-backpressure", "sess-1")
	require.NoError(t, err)

	// Start consumer that drains slowly
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range stream.Events() {
			time.Sleep(5 * time.Millisecond) // Slow consumer
		}
	}()

	// Send multiple events - backpressure should allow some to succeed
	successCount := 0
	for i := 0; i < 10; i++ {
		if stream.SendText("msg") {
			successCount++
		}
	}

	stream.Close()
	<-done

	// Should have succeeded in sending some messages due to backpressure
	assert.Greater(t, successCount, 0, "backpressure should allow some sends to succeed")
}

func TestResponseStream_Backpressure_NoTimeout(t *testing.T) {
	// W12.28: Test that zero backpressure timeout returns immediately on full buffer
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        1,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
		BackpressureTimeout:  0, // No backpressure
	})
	stream, err := sm.CreateStream("corr-no-backpressure", "sess-1")
	require.NoError(t, err)

	// Fill buffer (start event already in buffer)
	// Next send should fail immediately with no wait
	start := time.Now()
	result := stream.SendText("overflow")
	elapsed := time.Since(start)

	// Should fail quickly (no backpressure delay)
	assert.False(t, result, "send should fail when buffer full with no backpressure")
	assert.Less(t, elapsed, 10*time.Millisecond, "should return immediately without delay")

	stream.Close()
}

func TestResponseStream_Backpressure_SlowConsumer(t *testing.T) {
	// W12.28: Test that slow consumer doesn't cause OOM - sends eventually fail
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        3,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
		BackpressureTimeout:  10 * time.Millisecond, // Short timeout
	})
	stream, err := sm.CreateStream("corr-slow-consumer", "sess-1")
	require.NoError(t, err)

	// No consumer - buffer will fill up
	failCount := 0
	for i := 0; i < 20; i++ {
		if !stream.SendText("msg") {
			failCount++
		}
	}

	// Should have failures due to buffer full + timeout
	assert.Greater(t, failCount, 0, "sends should fail when buffer full and timeout expires")

	stream.Close()
}

func TestResponseStream_Backpressure_Concurrent(t *testing.T) {
	// W12.28: Test concurrent sends with backpressure don't cause races
	sm := guide.NewStreamManager(guide.StreamConfig{
		MaxBufferSize:        5,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
		BackpressureTimeout:  20 * time.Millisecond,
	})
	stream, err := sm.CreateStream("corr-concurrent-backpressure", "sess-1")
	require.NoError(t, err)

	// Start consumer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range stream.Events() {
			time.Sleep(time.Millisecond)
		}
	}()

	// Concurrent senders
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				stream.SendText("concurrent")
			}
		}()
	}

	wg.Wait()
	stream.Close()
	<-done

	assert.True(t, stream.IsClosed())
}
