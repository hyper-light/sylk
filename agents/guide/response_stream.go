package guide

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// ErrStreamClosed is returned when attempting to send to a closed stream.
var ErrStreamClosed = &streamError{msg: "stream is closed"}

// =============================================================================
// Response Streaming
// =============================================================================

// StreamManager manages response streams for clients
type StreamManager struct {
	mu sync.RWMutex

	// Active streams by correlation ID
	streams map[string]*ResponseStream

	// Configuration
	config StreamConfig

	// Statistics (using atomic operations)
	statsInternal streamStatsInternal
}

// StreamConfig configures the stream manager
type StreamConfig struct {
	// Maximum buffer size per stream
	MaxBufferSize int `json:"max_buffer_size"`

	// Stream timeout (how long to keep idle streams)
	StreamTimeout time.Duration `json:"stream_timeout"`

	// Heartbeat interval for keep-alive
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`

	// Maximum streams per session
	MaxStreamsPerSession int `json:"max_streams_per_session"`
}

// DefaultStreamConfig returns sensible defaults
func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		MaxBufferSize:        100,
		StreamTimeout:        5 * time.Minute,
		HeartbeatInterval:    30 * time.Second,
		MaxStreamsPerSession: 50,
	}
}

// ResponseStream represents a streaming response channel
type ResponseStream struct {
	mu sync.Mutex

	// Stream identification
	ID            string `json:"id"`
	CorrelationID string `json:"correlation_id"`
	SessionID     string `json:"session_id"`

	// Stream channel
	events chan *StreamEvent

	// State
	started   time.Time
	lastSend  time.Time
	closed    atomic.Bool
	closeOnce sync.Once // Ensures channel is closed exactly once

	// Statistics
	eventCount int64
	bytesSent  int64
}

// StreamEvent represents an event in a response stream
type StreamEvent struct {
	// Event type
	Type StreamEventType `json:"type"`

	// Event data
	Data any `json:"data,omitempty"`

	// Serialized data (for text events)
	Text string `json:"text,omitempty"`

	// Metadata
	Timestamp time.Time `json:"timestamp"`
	Sequence  int64     `json:"sequence"`
}

// StreamEventType indicates the type of stream event
type StreamEventType string

const (
	// StreamEventStart indicates stream started
	StreamEventStart StreamEventType = "start"

	// StreamEventData indicates a data chunk
	StreamEventData StreamEventType = "data"

	// StreamEventProgress indicates progress update
	StreamEventProgress StreamEventType = "progress"

	// StreamEventHeartbeat indicates keep-alive heartbeat
	StreamEventHeartbeat StreamEventType = "heartbeat"

	// StreamEventComplete indicates successful completion
	StreamEventComplete StreamEventType = "complete"

	// StreamEventError indicates an error
	StreamEventError StreamEventType = "error"

	// StreamEventEnd indicates stream ended
	StreamEventEnd StreamEventType = "end"
)

// StreamStats contains stream manager statistics
type StreamStats struct {
	TotalStreams   int64 `json:"total_streams"`
	ActiveStreams  int   `json:"active_streams"`
	CompletedOK    int64 `json:"completed_ok"`
	CompletedError int64 `json:"completed_error"`
	TotalEvents    int64 `json:"total_events"`
	TotalBytes     int64 `json:"total_bytes"`
	TimeoutsClosed int64 `json:"timeouts_closed"`
}

// streamStatsInternal holds atomic counters for thread-safe stats
type streamStatsInternal struct {
	totalStreams   int64
	completedOK    int64
	completedError int64
	timeoutsClosed int64
}

// NewStreamManager creates a new stream manager
func NewStreamManager(config StreamConfig) *StreamManager {
	if config.MaxBufferSize <= 0 {
		config = DefaultStreamConfig()
	}

	return &StreamManager{
		streams: make(map[string]*ResponseStream),
		config:  config,
	}
}

// =============================================================================
// Stream Creation
// =============================================================================

// CreateStream creates a new response stream
func (sm *StreamManager) CreateStream(correlationID, sessionID string) (*ResponseStream, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if stream already exists
	if _, exists := sm.streams[correlationID]; exists {
		// Return existing stream
		return sm.streams[correlationID], nil
	}

	// Check session limits
	sessionCount := 0
	for _, s := range sm.streams {
		if s.SessionID == sessionID {
			sessionCount++
		}
	}
	if sessionCount >= sm.config.MaxStreamsPerSession {
		return nil, ErrTooManyStreams
	}

	stream := &ResponseStream{
		ID:            generateStreamID(),
		CorrelationID: correlationID,
		SessionID:     sessionID,
		events:        make(chan *StreamEvent, sm.config.MaxBufferSize),
		started:       time.Now(),
		lastSend:      time.Now(),
	}

	sm.streams[correlationID] = stream
	atomic.AddInt64(&sm.statsInternal.totalStreams, 1)

	// Send start event
	stream.sendEvent(&StreamEvent{
		Type:      StreamEventStart,
		Timestamp: time.Now(),
		Sequence:  0,
	})

	return stream, nil
}

// GetStream retrieves an existing stream
func (sm *StreamManager) GetStream(correlationID string) *ResponseStream {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.streams[correlationID]
}

// CloseStream closes and removes a stream
func (sm *StreamManager) CloseStream(correlationID string) {
	sm.mu.Lock()
	stream, exists := sm.streams[correlationID]
	if exists {
		delete(sm.streams, correlationID)
	}
	sm.mu.Unlock()

	if exists && stream != nil {
		stream.Close()
	}
}

// =============================================================================
// Stream Operations
// =============================================================================

// SendData sends a data event to a stream
func (rs *ResponseStream) SendData(data any) bool {
	if rs.closed.Load() {
		return false
	}

	event := &StreamEvent{
		Type:      StreamEventData,
		Data:      data,
		Timestamp: time.Now(),
		Sequence:  atomic.AddInt64(&rs.eventCount, 1),
	}

	// Calculate bytes
	if jsonData, err := json.Marshal(data); err == nil {
		atomic.AddInt64(&rs.bytesSent, int64(len(jsonData)))
	}

	return rs.sendEvent(event)
}

// SendText sends a text data event to a stream
func (rs *ResponseStream) SendText(text string) bool {
	if rs.closed.Load() {
		return false
	}

	event := &StreamEvent{
		Type:      StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
		Sequence:  atomic.AddInt64(&rs.eventCount, 1),
	}

	atomic.AddInt64(&rs.bytesSent, int64(len(text)))

	return rs.sendEvent(event)
}

// SendProgress sends a progress update
func (rs *ResponseStream) SendProgress(current, total int, message string) bool {
	if rs.closed.Load() {
		return false
	}

	progress := &ProgressData{
		Current: current,
		Total:   total,
		Percent: 0,
		Message: message,
	}
	if total > 0 {
		progress.Percent = float64(current) / float64(total) * 100
	}

	event := &StreamEvent{
		Type:      StreamEventProgress,
		Data:      progress,
		Timestamp: time.Now(),
		Sequence:  atomic.AddInt64(&rs.eventCount, 1),
	}

	return rs.sendEvent(event)
}

// ProgressData contains progress information
type ProgressData struct {
	Current int     `json:"current"`
	Total   int     `json:"total"`
	Percent float64 `json:"percent"`
	Message string  `json:"message,omitempty"`
}

// SendHeartbeat sends a keep-alive heartbeat
func (rs *ResponseStream) SendHeartbeat() bool {
	if rs.closed.Load() {
		return false
	}

	event := &StreamEvent{
		Type:      StreamEventHeartbeat,
		Timestamp: time.Now(),
		Sequence:  atomic.AddInt64(&rs.eventCount, 1),
	}

	return rs.sendEvent(event)
}

// SendComplete sends a completion event with final result
func (rs *ResponseStream) SendComplete(result any) bool {
	if rs.closed.Load() {
		return false
	}

	event := &StreamEvent{
		Type:      StreamEventComplete,
		Data:      result,
		Timestamp: time.Now(),
		Sequence:  atomic.AddInt64(&rs.eventCount, 1),
	}

	return rs.sendEvent(event)
}

// SendError sends an error event
func (rs *ResponseStream) SendError(err error) bool {
	if rs.closed.Load() {
		return false
	}

	event := &StreamEvent{
		Type:      StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
		Sequence:  atomic.AddInt64(&rs.eventCount, 1),
	}

	return rs.sendEvent(event)
}

// sendEvent sends an event to the stream
func (rs *ResponseStream) sendEvent(event *StreamEvent) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if rs.closed.Load() {
		return false
	}

	select {
	case rs.events <- event:
		rs.lastSend = time.Now()
		return true
	default:
		// Buffer full
		return false
	}
}

// Close closes the stream safely. Multiple calls to Close are safe
// and will not panic. Uses sync.Once to ensure the channel is closed
// exactly once, preventing send-on-closed-channel panics.
// The close operation is synchronized with sendEvent to prevent races.
func (rs *ResponseStream) Close() {
	rs.closeOnce.Do(func() {
		rs.closeInternal()
	})
}

// closeInternal performs the actual close operation. Must only be called
// via closeOnce.Do to ensure it executes exactly once. Acquires the lock
// to synchronize with sendEvent and prevent send-on-closed-channel races.
func (rs *ResponseStream) closeInternal() {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Mark as closed under lock so sendEvent sees consistent state
	rs.closed.Store(true)

	// Send end event (non-blocking since buffer might be full)
	event := &StreamEvent{
		Type:      StreamEventEnd,
		Timestamp: time.Now(),
		Sequence:  atomic.AddInt64(&rs.eventCount, 1),
	}

	select {
	case rs.events <- event:
	default:
		// Buffer full, skip end event
	}

	close(rs.events)
}

// Events returns the event channel for reading
func (rs *ResponseStream) Events() <-chan *StreamEvent {
	return rs.events
}

// IsClosed returns true if the stream is closed
func (rs *ResponseStream) IsClosed() bool {
	return rs.closed.Load()
}

// Stats returns stream statistics
func (rs *ResponseStream) Stats() StreamEventStats {
	rs.mu.Lock()
	lastSend := rs.lastSend
	rs.mu.Unlock()

	return StreamEventStats{
		ID:            rs.ID,
		CorrelationID: rs.CorrelationID,
		Started:       rs.started,
		LastSend:      lastSend,
		EventCount:    atomic.LoadInt64(&rs.eventCount),
		BytesSent:     atomic.LoadInt64(&rs.bytesSent),
		Closed:        rs.closed.Load(),
	}
}

// StreamEventStats contains per-stream statistics
type StreamEventStats struct {
	ID            string    `json:"id"`
	CorrelationID string    `json:"correlation_id"`
	Started       time.Time `json:"started"`
	LastSend      time.Time `json:"last_send"`
	EventCount    int64     `json:"event_count"`
	BytesSent     int64     `json:"bytes_sent"`
	Closed        bool      `json:"closed"`
}

// =============================================================================
// Stream Consumer
// =============================================================================

// StreamConsumer provides methods for consuming stream events
type StreamConsumer struct {
	stream *ResponseStream
	ctx    context.Context
}

// NewStreamConsumer creates a new stream consumer
func NewStreamConsumer(ctx context.Context, stream *ResponseStream) *StreamConsumer {
	return &StreamConsumer{
		stream: stream,
		ctx:    ctx,
	}
}

// Next returns the next event or blocks until available
func (sc *StreamConsumer) Next() (*StreamEvent, bool) {
	select {
	case event, ok := <-sc.stream.events:
		return event, ok
	case <-sc.ctx.Done():
		return nil, false
	}
}

// Collect collects all events until stream ends
func (sc *StreamConsumer) Collect() []*StreamEvent {
	var events []*StreamEvent
	for {
		event, ok := sc.Next()
		if !ok {
			break
		}
		events = append(events, event)
		if event.Type == StreamEventEnd || event.Type == StreamEventError {
			break
		}
	}
	return events
}

// CollectData collects only data events
func (sc *StreamConsumer) CollectData() []any {
	var data []any
	for {
		event, ok := sc.Next()
		if !ok {
			break
		}

		if sc.isDataEvent(event) {
			data = sc.appendEventData(data, event)
		}
		if sc.isTerminalEvent(event) {
			break
		}
	}
	return data
}

func (sc *StreamConsumer) isDataEvent(event *StreamEvent) bool {
	return event.Type == StreamEventData
}

func (sc *StreamConsumer) isTerminalEvent(event *StreamEvent) bool {
	return event.Type == StreamEventEnd || event.Type == StreamEventError
}

func (sc *StreamConsumer) appendEventData(data []any, event *StreamEvent) []any {
	if event.Data != nil {
		return append(data, event.Data)
	}
	if event.Text != "" {
		return append(data, event.Text)
	}
	return data
}

// CollectText collects text from all data events
func (sc *StreamConsumer) CollectText() string {
	var text string
	for {
		event, ok := sc.Next()
		if !ok {
			break
		}
		if sc.isTextEvent(event) {
			text += event.Text
		}
		if sc.isTerminalEvent(event) {
			break
		}
	}
	return text
}

func (sc *StreamConsumer) isTextEvent(event *StreamEvent) bool {
	return event.Type == StreamEventData && event.Text != ""
}

// =============================================================================
// Cleanup
// =============================================================================

// CleanupIdle closes streams that have been idle
func (sm *StreamManager) CleanupIdle() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	closed := 0
	threshold := time.Now().Add(-sm.config.StreamTimeout)

	for corrID, stream := range sm.streams {
		stream.mu.Lock()
		isIdle := stream.lastSend.Before(threshold)
		stream.mu.Unlock()

		if isIdle {
			delete(sm.streams, corrID)
			stream.Close()
			closed++
			atomic.AddInt64(&sm.statsInternal.timeoutsClosed, 1)
		}
	}

	return closed
}

// Stats returns stream manager statistics
func (sm *StreamManager) Stats() StreamStats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	stats := StreamStats{
		TotalStreams:   atomic.LoadInt64(&sm.statsInternal.totalStreams),
		ActiveStreams:  len(sm.streams),
		CompletedOK:    atomic.LoadInt64(&sm.statsInternal.completedOK),
		CompletedError: atomic.LoadInt64(&sm.statsInternal.completedError),
		TimeoutsClosed: atomic.LoadInt64(&sm.statsInternal.timeoutsClosed),
	}

	// Aggregate from active streams
	for _, stream := range sm.streams {
		stats.TotalEvents += atomic.LoadInt64(&stream.eventCount)
		stats.TotalBytes += atomic.LoadInt64(&stream.bytesSent)
	}

	return stats
}

// generateStreamID creates a unique stream ID
func generateStreamID() string {
	return "stream_" + time.Now().Format("20060102150405.000000000")
}

// =============================================================================
// Errors
// =============================================================================

// ErrTooManyStreams indicates the session has too many active streams
var ErrTooManyStreams = &streamError{msg: "too many active streams for session"}

type streamError struct {
	msg string
}

func (e *streamError) Error() string {
	return e.msg
}
