package providers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// StreamWatchdog Configuration Tests
// =============================================================================

func TestDefaultStreamWatchdogConfig(t *testing.T) {
	config := DefaultStreamWatchdogConfig()

	if config.ChunkTimeout != 60*time.Second {
		t.Errorf("expected ChunkTimeout=60s, got %v", config.ChunkTimeout)
	}
	if config.MaxStreamTime != 10*time.Minute {
		t.Errorf("expected MaxStreamTime=10m, got %v", config.MaxStreamTime)
	}
}

func TestNewStreamWatchdog(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  5 * time.Second,
		MaxStreamTime: 30 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)

	if watchdog == nil {
		t.Fatal("expected non-nil watchdog")
	}
	if len(watchdog.streams) != 0 {
		t.Error("expected empty streams map")
	}
	if len(watchdog.callbacks) != 0 {
		t.Error("expected empty callbacks slice")
	}
}

// =============================================================================
// Watch/Stop Tests
// =============================================================================

func TestStreamWatchdog_Watch(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watched := watchdog.Watch(ctx, "stream-1", cancel)

	if watched == nil {
		t.Fatal("expected non-nil watched stream")
	}
	if watchdog.ActiveStreamCount() != 1 {
		t.Errorf("expected 1 active stream, got %d", watchdog.ActiveStreamCount())
	}
}

func TestStreamWatchdog_Watch_AfterClose(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	watchdog.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watched := watchdog.Watch(ctx, "stream-1", cancel)

	if watched != nil {
		t.Error("expected nil watched stream after close")
	}
}

func TestStreamWatchdog_Stop(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	if watchdog.ActiveStreamCount() != 1 {
		t.Fatal("expected 1 active stream")
	}

	watchdog.Stop("stream-1")

	if watchdog.ActiveStreamCount() != 0 {
		t.Errorf("expected 0 active streams after stop, got %d", watchdog.ActiveStreamCount())
	}
}

func TestStreamWatchdog_Stop_NonExistent(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	// Should not panic
	watchdog.Stop("non-existent")
}

func TestStreamWatchdog_Close(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)

	// Watch multiple streams
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		watchdog.Watch(ctx, "stream-"+string(rune('a'+i)), cancel)
	}

	if watchdog.ActiveStreamCount() != 5 {
		t.Fatalf("expected 5 active streams, got %d", watchdog.ActiveStreamCount())
	}

	watchdog.Close()

	if watchdog.ActiveStreamCount() != 0 {
		t.Errorf("expected 0 active streams after close, got %d", watchdog.ActiveStreamCount())
	}

	// Double close should not panic
	watchdog.Close()
}

// =============================================================================
// ReportChunk Tests
// =============================================================================

func TestStreamWatchdog_ReportChunk(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	watchdog.ReportChunk("stream-1")
	watchdog.ReportChunk("stream-1")
	watchdog.ReportChunk("stream-1")

	info := watchdog.GetStreamInfo("stream-1")
	if info == nil {
		t.Fatal("expected stream info")
	}
	if info.ChunksReceived() != 3 {
		t.Errorf("expected 3 chunks, got %d", info.ChunksReceived())
	}
}

func TestStreamWatchdog_ReportChunk_NonExistent(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	// Should not panic
	watchdog.ReportChunk("non-existent")
}

// =============================================================================
// Timeout Tests
// =============================================================================

func TestStreamWatchdog_ChunkTimeout(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  50 * time.Millisecond,
		MaxStreamTime: 10 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	var timeoutEvent atomic.Value
	watchdog.OnTimeout(func(event WatchdogEvent) {
		timeoutEvent.Store(event)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	// Wait for chunk timeout (check interval is ChunkTimeout/4 = 12.5ms)
	time.Sleep(100 * time.Millisecond)

	event, ok := timeoutEvent.Load().(WatchdogEvent)
	if !ok {
		t.Fatal("expected timeout event")
	}
	if event.StreamID != "stream-1" {
		t.Errorf("expected stream-1, got %s", event.StreamID)
	}
	if event.EventType != WatchdogChunkTimeout {
		t.Errorf("expected chunk_timeout, got %s", event.EventType)
	}
}

func TestStreamWatchdog_ChunkTimeout_ResetByReport(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  50 * time.Millisecond,
		MaxStreamTime: 10 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	var timeoutCount atomic.Int32
	watchdog.OnTimeout(func(event WatchdogEvent) {
		timeoutCount.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	// Keep reporting chunks to prevent timeout
	for i := 0; i < 5; i++ {
		time.Sleep(30 * time.Millisecond)
		watchdog.ReportChunk("stream-1")
	}

	// Should not have timed out
	if timeoutCount.Load() != 0 {
		t.Errorf("expected 0 timeouts, got %d", timeoutCount.Load())
	}

	watchdog.Stop("stream-1")
}

func TestStreamWatchdog_MaxDuration(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  10 * time.Second, // High to avoid chunk timeout
		MaxStreamTime: 50 * time.Millisecond,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	var timeoutEvent atomic.Value
	watchdog.OnTimeout(func(event WatchdogEvent) {
		timeoutEvent.Store(event)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	// Keep reporting to prevent chunk timeout, but max duration will hit
	go func() {
		for i := 0; i < 10; i++ {
			time.Sleep(10 * time.Millisecond)
			watchdog.ReportChunk("stream-1")
		}
	}()

	// Wait for max duration
	time.Sleep(100 * time.Millisecond)

	event, ok := timeoutEvent.Load().(WatchdogEvent)
	if !ok {
		t.Fatal("expected timeout event")
	}
	if event.EventType != WatchdogMaxDuration {
		t.Errorf("expected max_duration, got %s", event.EventType)
	}
}

// =============================================================================
// Callback Tests
// =============================================================================

func TestStreamWatchdog_OnTimeout_MultipleCallbacks(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  30 * time.Millisecond,
		MaxStreamTime: 10 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	var count1, count2 atomic.Int32
	watchdog.OnTimeout(func(event WatchdogEvent) {
		count1.Add(1)
	})
	watchdog.OnTimeout(func(event WatchdogEvent) {
		count2.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	// Wait for timeout
	time.Sleep(100 * time.Millisecond)

	if count1.Load() != 1 {
		t.Errorf("expected callback 1 called once, got %d", count1.Load())
	}
	if count2.Load() != 1 {
		t.Errorf("expected callback 2 called once, got %d", count2.Load())
	}
}

// =============================================================================
// GetStreamInfo Tests
// =============================================================================

func TestStreamWatchdog_GetStreamInfo(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	info := watchdog.GetStreamInfo("stream-1")
	if info == nil {
		t.Fatal("expected stream info")
	}

	// Non-existent stream
	info = watchdog.GetStreamInfo("non-existent")
	if info != nil {
		t.Error("expected nil for non-existent stream")
	}
}

// =============================================================================
// WatchedStream Tests
// =============================================================================

func TestWatchedStream_ChunksReceived(t *testing.T) {
	watched := &WatchedStream{}

	if watched.ChunksReceived() != 0 {
		t.Error("expected 0 initial chunks")
	}

	watched.mu.Lock()
	watched.chunksReceived = 5
	watched.mu.Unlock()

	if watched.ChunksReceived() != 5 {
		t.Errorf("expected 5 chunks, got %d", watched.ChunksReceived())
	}
}

func TestWatchedStream_LastChunkAt(t *testing.T) {
	now := time.Now()
	watched := &WatchedStream{lastChunkAt: now}

	if !watched.LastChunkAt().Equal(now) {
		t.Error("expected matching timestamp")
	}
}

// =============================================================================
// Concurrent Tests
// =============================================================================

func TestStreamWatchdog_Concurrent(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  500 * time.Millisecond,
		MaxStreamTime: 5 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	var wg sync.WaitGroup

	// Concurrent watch/stop operations
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			streamID := "stream-" + string(rune('a'+n%26))

			ctx, cancel := context.WithCancel(context.Background())
			watchdog.Watch(ctx, streamID, cancel)

			// Report some chunks
			for j := 0; j < 5; j++ {
				watchdog.ReportChunk(streamID)
				time.Sleep(time.Millisecond)
			}

			// Get info
			_ = watchdog.GetStreamInfo(streamID)
			_ = watchdog.ActiveStreamCount()

			watchdog.Stop(streamID)
		}(i)
	}

	wg.Wait()
}

func TestStreamWatchdog_Concurrent_Callbacks(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  20 * time.Millisecond,
		MaxStreamTime: 5 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	var eventCount atomic.Int32
	watchdog.OnTimeout(func(event WatchdogEvent) {
		eventCount.Add(1)
	})

	var wg sync.WaitGroup

	// Start multiple streams that will timeout
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			streamID := "timeout-stream-" + string(rune('0'+n))
			watchdog.Watch(ctx, streamID, cancel)
		}(i)
	}

	wg.Wait()

	// Wait for timeouts
	time.Sleep(100 * time.Millisecond)

	// All 10 should have timed out
	if eventCount.Load() != 10 {
		t.Errorf("expected 10 timeout events, got %d", eventCount.Load())
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestStreamWatchdog_DoubleStop(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  100 * time.Millisecond,
		MaxStreamTime: 1 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	watchdog.Stop("stream-1")
	watchdog.Stop("stream-1") // Should not panic
}

func TestStreamWatchdog_StopAfterTimeout(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  20 * time.Millisecond,
		MaxStreamTime: 10 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchdog.Watch(ctx, "stream-1", cancel)

	// Wait for timeout
	time.Sleep(100 * time.Millisecond)

	// Stop after timeout should not panic
	watchdog.Stop("stream-1")
}

func TestStreamWatchdog_CancelCalledOnTimeout(t *testing.T) {
	config := StreamWatchdogConfig{
		ChunkTimeout:  30 * time.Millisecond,
		MaxStreamTime: 10 * time.Second,
	}
	watchdog := NewStreamWatchdog(config)
	defer watchdog.Close()

	ctx, cancel := context.WithCancel(context.Background())

	watchdog.Watch(ctx, "stream-1", cancel)

	// Wait for timeout
	time.Sleep(100 * time.Millisecond)

	// Context should be cancelled
	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Error("expected context to be cancelled")
	}
}

func TestWatchdogEventType_Values(t *testing.T) {
	if WatchdogChunkTimeout != "chunk_timeout" {
		t.Error("unexpected WatchdogChunkTimeout value")
	}
	if WatchdogMaxDuration != "max_duration" {
		t.Error("unexpected WatchdogMaxDuration value")
	}
}
