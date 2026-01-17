package providers

import (
	"sync"
	"sync/atomic"
	"time"
)

// StreamMetrics collects telemetry from streaming responses.
// All fields are thread-safe for concurrent access.
type StreamMetrics struct {
	// Timing
	TimeToFirstToken    time.Duration // Latency until first content
	TimeToFirstToolCall time.Duration // Latency until tool detected
	TotalDuration       time.Duration // Total stream duration

	// Throughput
	TokensPerSecond float64 // Average generation speed
	ChunksReceived  int     // Total chunks processed

	// Efficiency
	EarlyAborts        int // Streams cancelled before completion
	TokensSaved        int // Estimated tokens saved from early abort
	BackpressureEvents int // Times producer blocked on full buffer
}

// StreamMetricsCollector manages metrics collection for a single stream.
// Thread-safe for concurrent chunk processing.
type StreamMetricsCollector struct {
	mu sync.RWMutex

	// Internal state
	startTime      time.Time
	firstTokenTime time.Time
	firstToolTime  time.Time
	chunksReceived int
	lastUsage      *Usage
	earlyAbort     bool

	// Backpressure tracking
	backpressureEvents int32 // atomic for lock-free increment

	// Lifecycle
	started atomic.Bool
	stopped atomic.Bool
}

// NewStreamMetricsCollector creates a new collector.
func NewStreamMetricsCollector() *StreamMetricsCollector {
	return &StreamMetricsCollector{}
}

// Start begins metrics collection. Must be called before processing chunks.
func (c *StreamMetricsCollector) Start() {
	if c.started.Swap(true) {
		return // Already started
	}
	c.mu.Lock()
	c.startTime = time.Now()
	c.mu.Unlock()
}

// Stop marks the stream as completed or aborted.
// Pass earlyAbort=true if stream was cancelled before ChunkTypeEnd.
func (c *StreamMetricsCollector) Stop(earlyAbort bool) {
	if c.stopped.Swap(true) {
		return // Already stopped
	}
	c.mu.Lock()
	c.earlyAbort = earlyAbort
	c.mu.Unlock()
}

// RecordChunk processes a chunk and updates metrics.
// Thread-safe for concurrent calls.
func (c *StreamMetricsCollector) RecordChunk(chunk *StreamChunk) {
	if chunk == nil || !c.started.Load() || c.stopped.Load() {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.chunksReceived++
	c.recordChunkType(chunk)
}

// recordChunkType handles type-specific metric updates.
// Caller must hold the mutex.
func (c *StreamMetricsCollector) recordChunkType(chunk *StreamChunk) {
	switch chunk.Type {
	case ChunkTypeText:
		c.recordFirstToken()
	case ChunkTypeToolStart:
		c.recordFirstToolCall()
	case ChunkTypeEnd:
		c.recordEndChunk(chunk)
	}
}

// recordFirstToken records time to first token if not already set.
// Caller must hold the mutex.
func (c *StreamMetricsCollector) recordFirstToken() {
	if c.firstTokenTime.IsZero() {
		c.firstTokenTime = time.Now()
	}
}

// recordFirstToolCall records time to first tool call if not already set.
// Caller must hold the mutex.
func (c *StreamMetricsCollector) recordFirstToolCall() {
	if c.firstToolTime.IsZero() {
		c.firstToolTime = time.Now()
	}
}

// recordEndChunk captures final usage information.
// Caller must hold the mutex.
func (c *StreamMetricsCollector) recordEndChunk(chunk *StreamChunk) {
	if chunk.Usage != nil {
		c.lastUsage = chunk.Usage
	}
}

// RecordBackpressure increments the backpressure event counter.
// Lock-free for minimal contention during high-throughput scenarios.
func (c *StreamMetricsCollector) RecordBackpressure() {
	atomic.AddInt32(&c.backpressureEvents, 1)
}

// GetMetrics returns a snapshot of current metrics.
// Safe to call at any time, even during active collection.
func (c *StreamMetricsCollector) GetMetrics() *StreamMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	metrics := &StreamMetrics{
		ChunksReceived:     c.chunksReceived,
		BackpressureEvents: int(atomic.LoadInt32(&c.backpressureEvents)),
	}

	c.populateTimingMetrics(metrics)
	c.populateEfficiencyMetrics(metrics)

	return metrics
}

// populateTimingMetrics fills timing-related fields.
// Caller must hold at least a read lock.
func (c *StreamMetricsCollector) populateTimingMetrics(metrics *StreamMetrics) {
	if c.startTime.IsZero() {
		return
	}

	// Time to first token
	if !c.firstTokenTime.IsZero() {
		metrics.TimeToFirstToken = c.firstTokenTime.Sub(c.startTime)
	}

	// Time to first tool call
	if !c.firstToolTime.IsZero() {
		metrics.TimeToFirstToolCall = c.firstToolTime.Sub(c.startTime)
	}

	// Total duration and tokens per second
	metrics.TotalDuration = c.calculateDuration()
	metrics.TokensPerSecond = c.calculateTokensPerSecond(metrics.TotalDuration)
}

// calculateDuration returns stream duration.
// Caller must hold at least a read lock.
func (c *StreamMetricsCollector) calculateDuration() time.Duration {
	if c.startTime.IsZero() {
		return 0
	}
	return time.Since(c.startTime)
}

// calculateTokensPerSecond computes generation speed.
// Caller must hold at least a read lock.
func (c *StreamMetricsCollector) calculateTokensPerSecond(duration time.Duration) float64 {
	if c.lastUsage == nil || duration <= 0 {
		return 0
	}
	seconds := duration.Seconds()
	if seconds <= 0 {
		return 0
	}
	return float64(c.lastUsage.OutputTokens) / seconds
}

// populateEfficiencyMetrics fills efficiency-related fields.
// Caller must hold at least a read lock.
func (c *StreamMetricsCollector) populateEfficiencyMetrics(metrics *StreamMetrics) {
	if !c.earlyAbort {
		return
	}

	metrics.EarlyAborts = 1
	metrics.TokensSaved = c.estimateTokensSaved()
}

// estimateTokensSaved estimates tokens that would have been generated.
// Uses average tokens per chunk as a rough heuristic.
// Caller must hold at least a read lock.
func (c *StreamMetricsCollector) estimateTokensSaved() int {
	if c.lastUsage == nil || c.chunksReceived == 0 {
		return 0
	}
	// Estimate: assume ~20% more tokens would have been generated
	// This is a conservative estimate based on typical abort points
	return c.lastUsage.OutputTokens / 5
}

// CollectStreamMetrics collects metrics from a channel of chunks.
// Blocks until the chunks channel is closed or done signal is received.
// Returns metrics snapshot when collection completes.
func CollectStreamMetrics(
	chunks <-chan *StreamChunk,
	done <-chan struct{},
) *StreamMetrics {
	collector := NewStreamMetricsCollector()
	collector.Start()

	earlyAbort := processChunks(collector, chunks, done)
	collector.Stop(earlyAbort)

	return collector.GetMetrics()
}

// processChunks reads from chunks channel until closed or done.
// Returns true if terminated by done signal (early abort).
func processChunks(
	collector *StreamMetricsCollector,
	chunks <-chan *StreamChunk,
	done <-chan struct{},
) bool {
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return false // Normal completion
			}
			collector.RecordChunk(chunk)

		case <-done:
			return true // Early abort
		}
	}
}

// CollectStreamMetricsWithBackpressure is like CollectStreamMetrics but
// also tracks backpressure from a secondary signal channel.
func CollectStreamMetricsWithBackpressure(
	chunks <-chan *StreamChunk,
	done <-chan struct{},
	backpressure <-chan struct{},
) *StreamMetrics {
	collector := NewStreamMetricsCollector()
	collector.Start()

	earlyAbort := processChunksWithBackpressure(collector, chunks, done, backpressure)
	collector.Stop(earlyAbort)

	return collector.GetMetrics()
}

// processChunksWithBackpressure handles chunks with backpressure monitoring.
func processChunksWithBackpressure(
	collector *StreamMetricsCollector,
	chunks <-chan *StreamChunk,
	done <-chan struct{},
	backpressure <-chan struct{},
) bool {
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return false
			}
			collector.RecordChunk(chunk)

		case <-backpressure:
			collector.RecordBackpressure()

		case <-done:
			return true
		}
	}
}

// AggregateStreamMetrics combines multiple StreamMetrics into one.
// Useful for session-level or global statistics.
type AggregateStreamMetrics struct {
	mu sync.RWMutex

	totalStreams       int
	totalChunks        int
	totalEarlyAborts   int
	totalTokensSaved   int
	totalBackpressure  int
	sumTimeToFirst     time.Duration
	sumTimeToFirstTool time.Duration
	sumDuration        time.Duration
	sumTokensPerSecond float64
}

// NewAggregateStreamMetrics creates a new aggregate collector.
func NewAggregateStreamMetrics() *AggregateStreamMetrics {
	return &AggregateStreamMetrics{}
}

// Add incorporates metrics from a completed stream.
func (a *AggregateStreamMetrics) Add(m *StreamMetrics) {
	if m == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.totalStreams++
	a.totalChunks += m.ChunksReceived
	a.totalEarlyAborts += m.EarlyAborts
	a.totalTokensSaved += m.TokensSaved
	a.totalBackpressure += m.BackpressureEvents
	a.sumTimeToFirst += m.TimeToFirstToken
	a.sumTimeToFirstTool += m.TimeToFirstToolCall
	a.sumDuration += m.TotalDuration
	a.sumTokensPerSecond += m.TokensPerSecond
}

// Summary returns aggregate statistics.
func (a *AggregateStreamMetrics) Summary() *StreamMetricsSummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.totalStreams == 0 {
		return &StreamMetricsSummary{}
	}

	return &StreamMetricsSummary{
		TotalStreams:         a.totalStreams,
		TotalChunks:          a.totalChunks,
		TotalEarlyAborts:     a.totalEarlyAborts,
		TotalTokensSaved:     a.totalTokensSaved,
		TotalBackpressure:    a.totalBackpressure,
		AvgTimeToFirstToken:  a.sumTimeToFirst / time.Duration(a.totalStreams),
		AvgTimeToFirstTool:   a.sumTimeToFirstTool / time.Duration(a.totalStreams),
		AvgDuration:          a.sumDuration / time.Duration(a.totalStreams),
		AvgTokensPerSecond:   a.sumTokensPerSecond / float64(a.totalStreams),
		EarlyAbortPercentage: float64(a.totalEarlyAborts) / float64(a.totalStreams) * 100,
	}
}

// StreamMetricsSummary provides aggregate statistics across multiple streams.
type StreamMetricsSummary struct {
	TotalStreams         int
	TotalChunks          int
	TotalEarlyAborts     int
	TotalTokensSaved     int
	TotalBackpressure    int
	AvgTimeToFirstToken  time.Duration
	AvgTimeToFirstTool   time.Duration
	AvgDuration          time.Duration
	AvgTokensPerSecond   float64
	EarlyAbortPercentage float64
}

// Reset clears all aggregate metrics.
func (a *AggregateStreamMetrics) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.totalStreams = 0
	a.totalChunks = 0
	a.totalEarlyAborts = 0
	a.totalTokensSaved = 0
	a.totalBackpressure = 0
	a.sumTimeToFirst = 0
	a.sumTimeToFirstTool = 0
	a.sumDuration = 0
	a.sumTokensPerSecond = 0
}
