package providers

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/messaging"
)

type StreamPublisher interface {
	Publish(msg *messaging.Message[StreamChunk]) error
	PublishControl(msg *StreamControlMessage) error
}

type StreamBridgeConfig struct {
	ChunkBufferSize int
	PublishTimeout  time.Duration
}

func DefaultStreamBridgeConfig() StreamBridgeConfig {
	return StreamBridgeConfig{
		ChunkBufferSize: 100,
		PublishTimeout:  5 * time.Second,
	}
}

// activeStream tracks a single active stream.
type activeStream struct {
	ctx           context.Context
	cancel        context.CancelFunc
	correlationID string
	startedAt     time.Time
}

// StreamBridge connects the provider streaming system to the event bus.
type StreamBridge struct {
	mu            sync.RWMutex
	activeStreams map[string]*activeStream
	publisher     StreamPublisher
	config        StreamBridgeConfig
}

// NewStreamBridge creates a new StreamBridge with the given publisher and config.
func NewStreamBridge(publisher StreamPublisher, config StreamBridgeConfig) *StreamBridge {
	return &StreamBridge{
		activeStreams: make(map[string]*activeStream),
		publisher:     publisher,
		config:        config,
	}
}

// StartStream begins streaming from a provider and publishes chunks to the event bus.
func (b *StreamBridge) StartStream(
	ctx context.Context,
	provider StreamProvider,
	req *Request,
	streamCtx *StreamContext,
) error {
	streamCtx2, cancel := context.WithCancel(ctx)

	stream := &activeStream{
		ctx:           streamCtx2,
		cancel:        cancel,
		correlationID: streamCtx.CorrelationID,
		startedAt:     time.Now(),
	}

	b.trackStream(streamCtx.CorrelationID, stream)

	go b.runStreamLoop(streamCtx2, provider, req, streamCtx)

	return nil
}

// runStreamLoop reads chunks from the provider and publishes them.
// Includes panic recovery to prevent crashes from propagating (W12.45 fix).
func (b *StreamBridge) runStreamLoop(
	ctx context.Context,
	provider StreamProvider,
	req *Request,
	streamCtx *StreamContext,
) {
	defer b.untrackStream(streamCtx.CorrelationID)
	defer b.recoverAndPublishError(streamCtx)

	chunks, errs := StreamToChannel(ctx, provider, req)

	b.processChunks(ctx, chunks, errs, streamCtx)
}

// recoverAndPublishError recovers from panics and publishes an error chunk.
// This ensures that panics in handlers don't crash the entire application (W12.45 fix).
func (b *StreamBridge) recoverAndPublishError(streamCtx *StreamContext) {
	if r := recover(); r != nil {
		err := fmt.Errorf("panic in stream handler: %v\n%s", r, captureStackTrace())
		b.publishErrorChunk(err, streamCtx)
	}
}

// captureStackTrace captures the current stack trace for panic reporting.
func captureStackTrace() string {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

// processChunks handles the chunk and error channels from streaming.
func (b *StreamBridge) processChunks(
	ctx context.Context,
	chunks <-chan *StreamChunk,
	errs <-chan error,
	streamCtx *StreamContext,
) {
	for b.processNextEvent(ctx, chunks, errs, streamCtx) {
	}
}

// processNextEvent waits for and processes a single event. Returns true to continue.
func (b *StreamBridge) processNextEvent(
	ctx context.Context,
	chunks <-chan *StreamChunk,
	errs <-chan error,
	streamCtx *StreamContext,
) bool {
	select {
	case <-ctx.Done():
		return false
	case err := <-errs:
		b.handleStreamError(err, streamCtx)
		return false
	case chunk, ok := <-chunks:
		return b.handleChunk(chunk, ok, streamCtx)
	}
}

// handleStreamError publishes an error if one occurred.
func (b *StreamBridge) handleStreamError(err error, streamCtx *StreamContext) {
	if err == nil {
		return
	}
	b.publishErrorChunk(err, streamCtx)
}

// handleChunk processes a single chunk. Returns false when stream is closed.
func (b *StreamBridge) handleChunk(chunk *StreamChunk, ok bool, streamCtx *StreamContext) bool {
	if !ok {
		return false
	}
	b.publishChunk(chunk, streamCtx)
	return true
}

// publishChunk wraps and publishes a single chunk.
func (b *StreamBridge) publishChunk(chunk *StreamChunk, streamCtx *StreamContext) {
	msg := WrapStreamChunk(chunk, streamCtx)
	_ = b.publisher.Publish(msg)
}

// publishErrorChunk creates and publishes an error chunk.
func (b *StreamBridge) publishErrorChunk(err error, streamCtx *StreamContext) {
	errChunk := &StreamChunk{
		Type:      ChunkTypeError,
		Text:      err.Error(),
		Timestamp: time.Now(),
	}
	msg := WrapStreamChunk(errChunk, streamCtx)
	_ = b.publisher.Publish(msg)
}

// CancelStream cancels an active stream and publishes a control message.
func (b *StreamBridge) CancelStream(correlationID string, reason string) error {
	b.mu.RLock()
	stream, exists := b.activeStreams[correlationID]
	b.mu.RUnlock()

	if !exists {
		return nil
	}

	controlMsg := NewStreamControlMessage(correlationID, "cancel", reason)
	_ = b.publisher.PublishControl(controlMsg)

	stream.cancel()

	return nil
}

// GetActiveStreams returns a list of active stream correlation IDs.
func (b *StreamBridge) GetActiveStreams() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	ids := make([]string, 0, len(b.activeStreams))
	for id := range b.activeStreams {
		ids = append(ids, id)
	}
	return ids
}

// Close cancels all active streams and cleans up resources.
func (b *StreamBridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, stream := range b.activeStreams {
		stream.cancel()
	}

	b.activeStreams = make(map[string]*activeStream)
	return nil
}

// trackStream adds a stream to the active streams map.
func (b *StreamBridge) trackStream(correlationID string, stream *activeStream) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeStreams[correlationID] = stream
}

// untrackStream removes a stream from the active streams map.
func (b *StreamBridge) untrackStream(correlationID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.activeStreams, correlationID)
}

// isStreamActive checks if a stream is currently active.
func (b *StreamBridge) isStreamActive(correlationID string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, exists := b.activeStreams[correlationID]
	return exists
}
