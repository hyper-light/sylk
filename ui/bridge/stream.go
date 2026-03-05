package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	streamBridgeName    = "bridge.stream"
	streamWatcherPrefix = "bridge.stream.watcher."
	streamWatchTimeout  = 5 * time.Minute
)

// streamDispatcher maps a StreamEventType to a function that produces the
// corresponding tea.Msg. Table-driven dispatch keeps cyclomatic complexity
// below 4 in the hot path.
type streamDispatcher struct {
	sessionID     string
	correlationID string
}

// dispatchEntry pairs a StreamEventType with its msg constructor.
type dispatchEntry struct {
	eventType guide.StreamEventType
	convert   func(d *streamDispatcher, event *guide.StreamEvent) any
}

// dispatchTable is the table-driven mapping from StreamEventType to msg constructor.
// Looked up linearly; the table is small (5 entries) so this is faster than a map.
var dispatchTable = [...]dispatchEntry{
	{guide.StreamEventStart, convertStart},
	{guide.StreamEventData, convertData},
	{guide.StreamEventProgress, convertProgress},
	{guide.StreamEventComplete, convertComplete},
	{guide.StreamEventError, convertError},
}

func convertStart(d *streamDispatcher, _ *guide.StreamEvent) any {
	return msg.StreamStartMsg{
		SessionID:     d.sessionID,
		CorrelationID: d.correlationID,
	}
}

func convertData(d *streamDispatcher, event *guide.StreamEvent) any {
	return msg.StreamChunkMsg{
		SessionID:     d.sessionID,
		CorrelationID: d.correlationID,
		Text:          event.Text,
	}
}

func convertProgress(d *streamDispatcher, event *guide.StreamEvent) any {
	progress, _ := event.Data.(*guide.ProgressData)
	m := msg.StreamProgressMsg{
		SessionID:     d.sessionID,
		CorrelationID: d.correlationID,
	}
	if progress != nil {
		m.Current = progress.Current
		m.Total = progress.Total
		m.Message = progress.Message
	}
	if event != nil {
		m.Visibility = event.Visibility
	}
	return m
}

func convertComplete(d *streamDispatcher, event *guide.StreamEvent) any {
	complete := msg.StreamCompleteMsg{
		SessionID:         d.sessionID,
		CorrelationID:     d.correlationID,
		Result:            event.Data,
		AuthoritativeText: strings.TrimSpace(event.Text),
	}
	if event.Usage != nil {
		complete.InputTokens = event.Usage.InputTokens
		complete.OutputTokens = event.Usage.OutputTokens
		complete.ReasoningTokens = event.Usage.ReasoningTokens
		complete.CacheReadTokens = event.Usage.CacheReadTokens
		complete.CacheWriteTokens = event.Usage.CacheWriteTokens
	}
	return complete
}

func convertError(d *streamDispatcher, event *guide.StreamEvent) any {
	return msg.StreamErrorMsg{
		SessionID:     d.sessionID,
		CorrelationID: d.correlationID,
		Err:           extractError(event),
	}
}

// extractError pulls an error from StreamEvent.Data, creating one from the
// string representation if the data is not already an error.
func extractError(event *guide.StreamEvent) error {
	if errMap, ok := event.Data.(map[string]string); ok {
		return errors.New(errMap["error"])
	}
	return fmt.Errorf("stream error: %v", event.Data)
}

// lookup returns the msg constructor for the given event type, or nil.
func lookup(eventType guide.StreamEventType) func(d *streamDispatcher, event *guide.StreamEvent) any {
	for i := range dispatchTable {
		if dispatchTable[i].eventType == eventType {
			return dispatchTable[i].convert
		}
	}
	return nil
}

// StreamBridge manages per-stream goroutines that read from ResponseStream
// channels and convert events into Bubble Tea messages.
type StreamBridge struct {
	scope   *concurrency.GoroutineScope
	mu      sync.Mutex
	streams map[string]context.CancelFunc // correlationID -> cancel
	dropped atomic.Int64
}

// NewStreamBridge creates a bridge that converts ResponseStream events
// into Bubble Tea messages.
func NewStreamBridge(scope *concurrency.GoroutineScope) *StreamBridge {
	return &StreamBridge{
		scope:   scope,
		streams: make(map[string]context.CancelFunc),
	}
}

// -- Bridge implementation --

// Start is a no-op for StreamBridge. Individual streams are started via WatchStream.
func (b *StreamBridge) Start(_ TeaProgram) error { return nil }

// Stop cancels all active stream watchers.
func (b *StreamBridge) Stop() {
	b.mu.Lock()
	streams := b.streams
	b.streams = make(map[string]context.CancelFunc)
	b.mu.Unlock()

	for _, cancel := range streams {
		cancel()
	}
}

// Name returns the bridge identifier.
func (b *StreamBridge) Name() string { return streamBridgeName }

// DroppedCount returns the total number of events dropped due to backpressure.
func (b *StreamBridge) DroppedCount() int64 { return b.dropped.Load() }

// WatchStream starts a tracked goroutine that reads from the given stream and
// sends converted messages to the Bubble Tea program.
func (b *StreamBridge) WatchStream(correlationID, sessionID string, stream *guide.ResponseStream, program TeaProgram) error {
	ctx, cancel := context.WithCancel(context.Background())

	b.mu.Lock()
	b.streams[correlationID] = cancel
	b.mu.Unlock()

	desc := streamWatcherPrefix + correlationID
	return b.scope.Go(desc, streamWatchTimeout, b.watchFunc(ctx, correlationID, sessionID, stream, program))
}

// watchFunc returns the WorkFunc that reads events from a single stream.
func (b *StreamBridge) watchFunc(
	watchCtx context.Context,
	correlationID, sessionID string,
	stream *guide.ResponseStream,
	program TeaProgram,
) concurrency.WorkFunc {
	return func(scopeCtx context.Context) error {
		defer b.removeStream(correlationID)

		d := &streamDispatcher{
			sessionID:     sessionID,
			correlationID: correlationID,
		}

		return b.drainStream(watchCtx, scopeCtx, stream, d, program)
	}
}

// drainStream reads events from the stream channel until it closes or context
// cancels. Each event is dispatched through the table-driven converter.
func (b *StreamBridge) drainStream(
	watchCtx, scopeCtx context.Context,
	stream *guide.ResponseStream,
	d *streamDispatcher,
	program TeaProgram,
) error {
	events := stream.Events()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return nil
			}
			b.dispatchEvent(d, event, program)
		case <-watchCtx.Done():
			return watchCtx.Err()
		case <-scopeCtx.Done():
			return scopeCtx.Err()
		}
	}
}

// dispatchEvent uses the dispatch table to convert and send a single event.
func (b *StreamBridge) dispatchEvent(d *streamDispatcher, event *guide.StreamEvent, program TeaProgram) {
	converter := lookup(event.Type)
	if converter == nil {
		return
	}
	program.Send(converter(d, event))
}

// removeStream cleans up the cancel function for a completed stream.
func (b *StreamBridge) removeStream(correlationID string) {
	b.mu.Lock()
	delete(b.streams, correlationID)
	b.mu.Unlock()
}
