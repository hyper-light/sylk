package providers

import (
	"context"
	"sync"
	"time"
)

// Default watchdog configuration values
const (
	DefaultChunkTimeout  = 60 * time.Second
	DefaultMaxStreamTime = 10 * time.Minute
)

// WatchdogEventType identifies the type of watchdog event
type WatchdogEventType string

const (
	WatchdogChunkTimeout WatchdogEventType = "chunk_timeout"
	WatchdogMaxDuration  WatchdogEventType = "max_duration"
)

// WatchdogEvent represents a timeout or other watchdog event
type WatchdogEvent struct {
	StreamID       string            `json:"stream_id"`
	EventType      WatchdogEventType `json:"event_type"`
	Timestamp      time.Time         `json:"timestamp"`
	LastChunkAt    time.Time         `json:"last_chunk_at"`
	ChunksReceived int               `json:"chunks_received"`
}

// StreamWatchdogConfig holds configuration for the watchdog
type StreamWatchdogConfig struct {
	ChunkTimeout  time.Duration // Max time between chunks (default: 60s)
	MaxStreamTime time.Duration // Max total stream duration (default: 10m)
}

// DefaultStreamWatchdogConfig returns a config with default values
func DefaultStreamWatchdogConfig() StreamWatchdogConfig {
	return StreamWatchdogConfig{
		ChunkTimeout:  DefaultChunkTimeout,
		MaxStreamTime: DefaultMaxStreamTime,
	}
}

// WatchedStream represents an actively monitored stream
type WatchedStream struct {
	streamID       string
	cancel         context.CancelFunc
	lastChunkAt    time.Time
	chunksReceived int
	startedAt      time.Time
	mu             sync.Mutex
	stopCh         chan struct{}
	stopped        bool
}

// ChunksReceived returns the number of chunks received for this stream
func (w *WatchedStream) ChunksReceived() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.chunksReceived
}

// LastChunkAt returns the timestamp of the last received chunk
func (w *WatchedStream) LastChunkAt() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastChunkAt
}

// StreamWatchdog monitors streams for timeout conditions
type StreamWatchdog struct {
	config    StreamWatchdogConfig
	streams   map[string]*WatchedStream
	callbacks []func(WatchdogEvent)
	mu        sync.RWMutex
	closed    bool
}

// NewStreamWatchdog creates a new StreamWatchdog with the given config
func NewStreamWatchdog(config StreamWatchdogConfig) *StreamWatchdog {
	return &StreamWatchdog{
		config:    config,
		streams:   make(map[string]*WatchedStream),
		callbacks: make([]func(WatchdogEvent), 0),
	}
}

// OnTimeout registers a callback to be invoked when a timeout event occurs
func (w *StreamWatchdog) OnTimeout(callback func(WatchdogEvent)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.callbacks = append(w.callbacks, callback)
}

// Watch starts monitoring a stream for timeout conditions.
// Returns a WatchedStream that can be used to report chunks.
func (w *StreamWatchdog) Watch(ctx context.Context, streamID string, cancel context.CancelFunc) *WatchedStream {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}

	now := time.Now()
	watched := &WatchedStream{
		streamID:    streamID,
		cancel:      cancel,
		lastChunkAt: now,
		startedAt:   now,
		stopCh:      make(chan struct{}),
	}

	w.streams[streamID] = watched
	go w.runWatcher(watched)

	return watched
}

// runWatcher runs the timeout monitoring loop for a single stream
func (w *StreamWatchdog) runWatcher(watched *WatchedStream) {
	chunkTicker := time.NewTicker(w.config.ChunkTimeout / 4)
	defer chunkTicker.Stop()

	maxTimer := time.NewTimer(w.config.MaxStreamTime)
	defer maxTimer.Stop()

	for {
		select {
		case <-watched.stopCh:
			return
		case <-maxTimer.C:
			w.handleMaxDuration(watched)
			return
		case <-chunkTicker.C:
			if w.checkChunkTimeout(watched) {
				return
			}
		}
	}
}

// checkChunkTimeout checks if chunk timeout has been exceeded.
// Returns true if timeout occurred and stream was cancelled.
func (w *StreamWatchdog) checkChunkTimeout(watched *WatchedStream) bool {
	watched.mu.Lock()
	elapsed := time.Since(watched.lastChunkAt)
	stopped := watched.stopped
	watched.mu.Unlock()

	if stopped {
		return true
	}

	if elapsed <= w.config.ChunkTimeout {
		return false
	}

	w.emitTimeout(watched, WatchdogChunkTimeout)
	return true
}

// handleMaxDuration handles max stream duration exceeded
func (w *StreamWatchdog) handleMaxDuration(watched *WatchedStream) {
	watched.mu.Lock()
	stopped := watched.stopped
	watched.mu.Unlock()

	if stopped {
		return
	}

	w.emitTimeout(watched, WatchdogMaxDuration)
}

// emitTimeout creates and emits a timeout event
func (w *StreamWatchdog) emitTimeout(watched *WatchedStream, eventType WatchdogEventType) {
	watched.mu.Lock()
	event := WatchdogEvent{
		StreamID:       watched.streamID,
		EventType:      eventType,
		Timestamp:      time.Now(),
		LastChunkAt:    watched.lastChunkAt,
		ChunksReceived: watched.chunksReceived,
	}
	watched.stopped = true
	watched.mu.Unlock()

	w.mu.Lock()
	delete(w.streams, watched.streamID)
	w.mu.Unlock()

	watched.cancel()
	w.notifyCallbacks(event)
}

// notifyCallbacks invokes all registered callbacks with the event
func (w *StreamWatchdog) notifyCallbacks(event WatchdogEvent) {
	w.mu.RLock()
	callbacks := make([]func(WatchdogEvent), len(w.callbacks))
	copy(callbacks, w.callbacks)
	w.mu.RUnlock()

	for _, cb := range callbacks {
		cb(event)
	}
}

// ReportChunk should be called when a chunk is received to reset the timeout
func (w *StreamWatchdog) ReportChunk(streamID string) {
	w.mu.RLock()
	watched, exists := w.streams[streamID]
	w.mu.RUnlock()

	if !exists {
		return
	}

	watched.mu.Lock()
	watched.lastChunkAt = time.Now()
	watched.chunksReceived++
	watched.mu.Unlock()
}

// Stop stops watching a specific stream without triggering a timeout event
func (w *StreamWatchdog) Stop(streamID string) {
	w.mu.Lock()
	watched, exists := w.streams[streamID]
	if exists {
		delete(w.streams, streamID)
	}
	w.mu.Unlock()

	if !exists {
		return
	}

	watched.mu.Lock()
	if !watched.stopped {
		watched.stopped = true
		close(watched.stopCh)
	}
	watched.mu.Unlock()
}

// Close stops all watchers and cleans up resources
func (w *StreamWatchdog) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true

	// Collect all streams to stop
	streams := make([]*WatchedStream, 0, len(w.streams))
	for _, watched := range w.streams {
		streams = append(streams, watched)
	}
	w.streams = make(map[string]*WatchedStream)
	w.mu.Unlock()

	// Stop all watchers outside the lock
	for _, watched := range streams {
		watched.mu.Lock()
		if !watched.stopped {
			watched.stopped = true
			close(watched.stopCh)
		}
		watched.mu.Unlock()
	}
}

// GetStreamInfo returns information about a watched stream.
// Returns nil if the stream is not being watched.
func (w *StreamWatchdog) GetStreamInfo(streamID string) *WatchedStream {
	w.mu.RLock()
	watched := w.streams[streamID]
	w.mu.RUnlock()
	return watched
}

// ActiveStreamCount returns the number of streams currently being watched
func (w *StreamWatchdog) ActiveStreamCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.streams)
}
