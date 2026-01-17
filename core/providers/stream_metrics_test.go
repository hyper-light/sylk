package providers

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// StreamMetricsCollector Tests
// =============================================================================

func TestNewStreamMetricsCollector(t *testing.T) {
	collector := NewStreamMetricsCollector()
	if collector == nil {
		t.Fatal("expected non-nil collector")
	}
}

func TestStreamMetricsCollector_StartStop(t *testing.T) {
	collector := NewStreamMetricsCollector()

	collector.Start()

	chunk := &StreamChunk{Type: ChunkTypeText, Text: "test"}
	collector.RecordChunk(chunk)

	metrics := collector.GetMetrics()
	if metrics.ChunksReceived != 1 {
		t.Errorf("expected 1 chunk after start, got %d", metrics.ChunksReceived)
	}

	collector.Start()
	collector.RecordChunk(chunk)
	metrics = collector.GetMetrics()
	if metrics.ChunksReceived != 2 {
		t.Errorf("expected 2 chunks after double start, got %d", metrics.ChunksReceived)
	}

	collector.Stop(false)
	collector.RecordChunk(chunk)
	metrics = collector.GetMetrics()
	if metrics.ChunksReceived != 2 {
		t.Errorf("expected 2 chunks after stop (no new chunks), got %d", metrics.ChunksReceived)
	}

	collector.Stop(true)
	metrics = collector.GetMetrics()
	if metrics.EarlyAborts != 0 {
		t.Error("expected no early aborts after first stop")
	}
}

func TestStreamMetricsCollector_RecordChunk_BeforeStart(t *testing.T) {
	collector := NewStreamMetricsCollector()

	chunk := &StreamChunk{Type: ChunkTypeText, Text: "test"}
	collector.RecordChunk(chunk)

	metrics := collector.GetMetrics()
	if metrics.ChunksReceived != 0 {
		t.Errorf("expected 0 chunks before start, got %d", metrics.ChunksReceived)
	}
}

func TestStreamMetricsCollector_RecordChunk_AfterStop(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()
	collector.Stop(false)

	chunk := &StreamChunk{Type: ChunkTypeText, Text: "test"}
	collector.RecordChunk(chunk)

	metrics := collector.GetMetrics()
	if metrics.ChunksReceived != 0 {
		t.Errorf("expected 0 chunks after stop, got %d", metrics.ChunksReceived)
	}
}

func TestStreamMetricsCollector_RecordChunk_Nil(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	// Should not panic
	collector.RecordChunk(nil)

	metrics := collector.GetMetrics()
	if metrics.ChunksReceived != 0 {
		t.Errorf("expected 0 chunks for nil, got %d", metrics.ChunksReceived)
	}
}

func TestStreamMetricsCollector_TextChunk(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	time.Sleep(10 * time.Millisecond)

	chunk := &StreamChunk{Type: ChunkTypeText, Text: "hello"}
	collector.RecordChunk(chunk)

	metrics := collector.GetMetrics()
	if metrics.ChunksReceived != 1 {
		t.Errorf("expected 1 chunk, got %d", metrics.ChunksReceived)
	}
	if metrics.TimeToFirstToken == 0 {
		t.Error("expected non-zero time to first token")
	}
	if metrics.TimeToFirstToken < 10*time.Millisecond {
		t.Errorf("expected at least 10ms, got %v", metrics.TimeToFirstToken)
	}
}

func TestStreamMetricsCollector_ToolStartChunk(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	time.Sleep(5 * time.Millisecond)

	chunk := &StreamChunk{Type: ChunkTypeToolStart}
	collector.RecordChunk(chunk)

	metrics := collector.GetMetrics()
	if metrics.TimeToFirstToolCall == 0 {
		t.Error("expected non-zero time to first tool call")
	}
}

func TestStreamMetricsCollector_EndChunk(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	// Record usage in end chunk
	usage := &Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}
	chunk := &StreamChunk{Type: ChunkTypeEnd, Usage: usage}
	collector.RecordChunk(chunk)

	metrics := collector.GetMetrics()
	if metrics.ChunksReceived != 1 {
		t.Errorf("expected 1 chunk, got %d", metrics.ChunksReceived)
	}
}

func TestStreamMetricsCollector_TokensPerSecond(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	// Simulate some work
	time.Sleep(100 * time.Millisecond)

	// Record end with usage
	usage := &Usage{OutputTokens: 100}
	chunk := &StreamChunk{Type: ChunkTypeEnd, Usage: usage}
	collector.RecordChunk(chunk)

	metrics := collector.GetMetrics()
	// Should be approximately 100 tokens / 0.1 seconds = 1000 tokens/sec
	// But allow for timing variance
	if metrics.TokensPerSecond < 500 || metrics.TokensPerSecond > 2000 {
		t.Errorf("expected tokens/sec around 1000, got %f", metrics.TokensPerSecond)
	}
}

func TestStreamMetricsCollector_EarlyAbort(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	// Record some usage
	collector.mu.Lock()
	collector.lastUsage = &Usage{OutputTokens: 100}
	collector.chunksReceived = 5
	collector.mu.Unlock()

	collector.Stop(true) // early abort

	metrics := collector.GetMetrics()
	if metrics.EarlyAborts != 1 {
		t.Errorf("expected 1 early abort, got %d", metrics.EarlyAborts)
	}
	if metrics.TokensSaved == 0 {
		t.Error("expected non-zero tokens saved")
	}
}

func TestStreamMetricsCollector_Backpressure(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	// Record backpressure events
	collector.RecordBackpressure()
	collector.RecordBackpressure()
	collector.RecordBackpressure()

	metrics := collector.GetMetrics()
	if metrics.BackpressureEvents != 3 {
		t.Errorf("expected 3 backpressure events, got %d", metrics.BackpressureEvents)
	}
}

func TestStreamMetricsCollector_Concurrent(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chunk := &StreamChunk{Type: ChunkTypeText, Text: "test"}
			collector.RecordChunk(chunk)
			collector.RecordBackpressure()
			_ = collector.GetMetrics()
		}()
	}
	wg.Wait()

	metrics := collector.GetMetrics()
	if metrics.ChunksReceived != 100 {
		t.Errorf("expected 100 chunks, got %d", metrics.ChunksReceived)
	}
	if metrics.BackpressureEvents != 100 {
		t.Errorf("expected 100 backpressure events, got %d", metrics.BackpressureEvents)
	}
}

// =============================================================================
// CollectStreamMetrics Tests
// =============================================================================

func TestCollectStreamMetrics_NormalCompletion(t *testing.T) {
	chunks := make(chan *StreamChunk, 10)
	done := make(chan struct{})

	// Send chunks
	go func() {
		chunks <- &StreamChunk{Type: ChunkTypeText, Text: "Hello"}
		chunks <- &StreamChunk{Type: ChunkTypeText, Text: " world"}
		chunks <- &StreamChunk{Type: ChunkTypeEnd, Usage: &Usage{OutputTokens: 10}}
		close(chunks)
	}()

	metrics := CollectStreamMetrics(chunks, done)

	if metrics.ChunksReceived != 3 {
		t.Errorf("expected 3 chunks, got %d", metrics.ChunksReceived)
	}
	if metrics.EarlyAborts != 0 {
		t.Error("expected no early aborts")
	}
}

func TestCollectStreamMetrics_EarlyAbort(t *testing.T) {
	chunks := make(chan *StreamChunk, 10)
	done := make(chan struct{})

	// Send one chunk then abort
	go func() {
		chunks <- &StreamChunk{Type: ChunkTypeText, Text: "Hello"}
		time.Sleep(10 * time.Millisecond)
		close(done)
	}()

	metrics := CollectStreamMetrics(chunks, done)

	if metrics.ChunksReceived != 1 {
		t.Errorf("expected 1 chunk, got %d", metrics.ChunksReceived)
	}
	if metrics.EarlyAborts != 1 {
		t.Error("expected early abort")
	}
}

func TestCollectStreamMetricsWithBackpressure(t *testing.T) {
	chunks := make(chan *StreamChunk, 10)
	done := make(chan struct{})
	backpressure := make(chan struct{}, 10)

	go func() {
		chunks <- &StreamChunk{Type: ChunkTypeText, Text: "Hello"}
		time.Sleep(10 * time.Millisecond)
		backpressure <- struct{}{}
		time.Sleep(10 * time.Millisecond)
		backpressure <- struct{}{}
		time.Sleep(10 * time.Millisecond)
		chunks <- &StreamChunk{Type: ChunkTypeEnd}
		close(chunks)
	}()

	metrics := CollectStreamMetricsWithBackpressure(chunks, done, backpressure)

	if metrics.ChunksReceived != 2 {
		t.Errorf("expected 2 chunks, got %d", metrics.ChunksReceived)
	}
	if metrics.BackpressureEvents != 2 {
		t.Errorf("expected 2 backpressure events, got %d", metrics.BackpressureEvents)
	}
}

// =============================================================================
// AggregateStreamMetrics Tests
// =============================================================================

func TestNewAggregateStreamMetrics(t *testing.T) {
	agg := NewAggregateStreamMetrics()
	if agg == nil {
		t.Fatal("expected non-nil aggregate")
	}
}

func TestAggregateStreamMetrics_Add(t *testing.T) {
	agg := NewAggregateStreamMetrics()

	// Add nil should not panic
	agg.Add(nil)
	summary := agg.Summary()
	if summary.TotalStreams != 0 {
		t.Error("expected 0 streams after adding nil")
	}

	// Add real metrics
	m1 := &StreamMetrics{
		TimeToFirstToken:   100 * time.Millisecond,
		TotalDuration:      1 * time.Second,
		TokensPerSecond:    50,
		ChunksReceived:     10,
		EarlyAborts:        0,
		BackpressureEvents: 1,
	}
	agg.Add(m1)

	m2 := &StreamMetrics{
		TimeToFirstToken:   200 * time.Millisecond,
		TotalDuration:      2 * time.Second,
		TokensPerSecond:    100,
		ChunksReceived:     20,
		EarlyAborts:        1,
		TokensSaved:        50,
		BackpressureEvents: 2,
	}
	agg.Add(m2)

	summary = agg.Summary()

	if summary.TotalStreams != 2 {
		t.Errorf("expected 2 streams, got %d", summary.TotalStreams)
	}
	if summary.TotalChunks != 30 {
		t.Errorf("expected 30 chunks, got %d", summary.TotalChunks)
	}
	if summary.TotalEarlyAborts != 1 {
		t.Errorf("expected 1 early abort, got %d", summary.TotalEarlyAborts)
	}
	if summary.TotalTokensSaved != 50 {
		t.Errorf("expected 50 tokens saved, got %d", summary.TotalTokensSaved)
	}
	if summary.TotalBackpressure != 3 {
		t.Errorf("expected 3 backpressure events, got %d", summary.TotalBackpressure)
	}

	// Check averages
	expectedAvgTTFT := 150 * time.Millisecond
	if summary.AvgTimeToFirstToken != expectedAvgTTFT {
		t.Errorf("expected avg TTFT %v, got %v", expectedAvgTTFT, summary.AvgTimeToFirstToken)
	}

	expectedAvgDuration := 1500 * time.Millisecond
	if summary.AvgDuration != expectedAvgDuration {
		t.Errorf("expected avg duration %v, got %v", expectedAvgDuration, summary.AvgDuration)
	}

	if summary.AvgTokensPerSecond != 75 {
		t.Errorf("expected avg tokens/sec 75, got %f", summary.AvgTokensPerSecond)
	}

	if summary.EarlyAbortPercentage != 50 {
		t.Errorf("expected 50%% early aborts, got %f", summary.EarlyAbortPercentage)
	}
}

func TestAggregateStreamMetrics_Summary_Empty(t *testing.T) {
	agg := NewAggregateStreamMetrics()
	summary := agg.Summary()

	if summary.TotalStreams != 0 {
		t.Error("expected 0 streams")
	}
	if summary.AvgTokensPerSecond != 0 {
		t.Error("expected 0 tokens/sec for empty")
	}
}

func TestAggregateStreamMetrics_Reset(t *testing.T) {
	agg := NewAggregateStreamMetrics()

	agg.Add(&StreamMetrics{ChunksReceived: 10})
	agg.Add(&StreamMetrics{ChunksReceived: 20})

	agg.Reset()

	summary := agg.Summary()
	if summary.TotalStreams != 0 {
		t.Errorf("expected 0 streams after reset, got %d", summary.TotalStreams)
	}
	if summary.TotalChunks != 0 {
		t.Errorf("expected 0 chunks after reset, got %d", summary.TotalChunks)
	}
}

func TestAggregateStreamMetrics_Concurrent(t *testing.T) {
	agg := NewAggregateStreamMetrics()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agg.Add(&StreamMetrics{
				ChunksReceived:  10,
				TokensPerSecond: 100,
				TotalDuration:   time.Second,
			})
			_ = agg.Summary()
		}()
	}
	wg.Wait()

	summary := agg.Summary()
	if summary.TotalStreams != 100 {
		t.Errorf("expected 100 streams, got %d", summary.TotalStreams)
	}
	if summary.TotalChunks != 1000 {
		t.Errorf("expected 1000 chunks, got %d", summary.TotalChunks)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestStreamMetricsCollector_MultipleTextChunks_OnlyFirstRecorded(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	time.Sleep(10 * time.Millisecond)
	collector.RecordChunk(&StreamChunk{Type: ChunkTypeText, Text: "first"})

	time.Sleep(50 * time.Millisecond)
	collector.RecordChunk(&StreamChunk{Type: ChunkTypeText, Text: "second"})

	metrics := collector.GetMetrics()

	// TTFT should be for first chunk only
	if metrics.TimeToFirstToken < 10*time.Millisecond || metrics.TimeToFirstToken > 30*time.Millisecond {
		t.Errorf("expected TTFT around 10ms, got %v", metrics.TimeToFirstToken)
	}
}

func TestStreamMetricsCollector_NoUsage_ZeroTokensPerSecond(t *testing.T) {
	collector := NewStreamMetricsCollector()
	collector.Start()

	time.Sleep(50 * time.Millisecond)

	// End without usage
	collector.RecordChunk(&StreamChunk{Type: ChunkTypeEnd})

	metrics := collector.GetMetrics()
	if metrics.TokensPerSecond != 0 {
		t.Errorf("expected 0 tokens/sec without usage, got %f", metrics.TokensPerSecond)
	}
}

func TestStreamMetricsCollector_GetMetrics_BeforeStart(t *testing.T) {
	collector := NewStreamMetricsCollector()

	metrics := collector.GetMetrics()

	if metrics.TotalDuration != 0 {
		t.Errorf("expected 0 duration before start, got %v", metrics.TotalDuration)
	}
	if metrics.TimeToFirstToken != 0 {
		t.Error("expected 0 TTFT before start")
	}
}
