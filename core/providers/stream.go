package providers

import (
	"context"
	"strings"
	"sync"
	"time"
)

// StreamChunk represents a single chunk from a streaming response
type StreamChunk struct {
	// Index is the sequence number of this chunk
	Index int `json:"index"`

	// Text is the content delta
	Text string `json:"text"`

	// Type indicates the chunk type
	Type StreamChunkType `json:"type"`

	// ToolCall if this chunk contains tool use data
	ToolCall *ToolCallChunk `json:"tool_call,omitempty"`

	// Usage is populated on the final chunk (if available)
	Usage *Usage `json:"usage,omitempty"`

	// StopReason is populated on the final chunk
	StopReason StopReason `json:"stop_reason,omitempty"`

	// Timestamp when this chunk was received
	Timestamp time.Time `json:"timestamp"`
}

// StreamChunkType identifies what kind of content the chunk contains
type StreamChunkType string

const (
	ChunkTypeText      StreamChunkType = "text"
	ChunkTypeToolStart StreamChunkType = "tool_start"
	ChunkTypeToolDelta StreamChunkType = "tool_delta"
	ChunkTypeToolEnd   StreamChunkType = "tool_end"
	ChunkTypeStart     StreamChunkType = "start"
	ChunkTypeEnd       StreamChunkType = "end"
	ChunkTypeError     StreamChunkType = "error"
)

// ToolCallChunk contains incremental tool call data
type ToolCallChunk struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

// StreamAccumulator collects streaming chunks into a complete response
type StreamAccumulator struct {
	mu sync.Mutex

	chunks     []StreamChunk
	text       strings.Builder
	toolCalls  map[string]*accumulatedToolCall
	usage      Usage
	stopReason StopReason
	model      string

	startTime time.Time
	endTime   time.Time
}

type accumulatedToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// NewStreamAccumulator creates a new accumulator
func NewStreamAccumulator() *StreamAccumulator {
	return &StreamAccumulator{
		chunks:    make([]StreamChunk, 0, 100),
		toolCalls: make(map[string]*accumulatedToolCall),
		startTime: time.Now(),
	}
}

// Add accumulates a chunk
func (a *StreamAccumulator) Add(chunk *StreamChunk) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.chunks = append(a.chunks, *chunk)

	switch chunk.Type {
	case ChunkTypeText:
		a.text.WriteString(chunk.Text)

	case ChunkTypeToolStart:
		if chunk.ToolCall != nil {
			a.toolCalls[chunk.ToolCall.ID] = &accumulatedToolCall{
				ID:   chunk.ToolCall.ID,
				Name: chunk.ToolCall.Name,
			}
		}

	case ChunkTypeToolDelta:
		if chunk.ToolCall != nil {
			if tc, ok := a.toolCalls[chunk.ToolCall.ID]; ok {
				tc.Arguments.WriteString(chunk.ToolCall.ArgumentsDelta)
			}
		}

	case ChunkTypeEnd:
		a.endTime = time.Now()
		if chunk.Usage != nil {
			a.usage = *chunk.Usage
		}
		if chunk.StopReason != "" {
			a.stopReason = chunk.StopReason
		}
	}
}

// Response builds the final response from accumulated chunks
func (a *StreamAccumulator) Response() *Response {
	a.mu.Lock()
	defer a.mu.Unlock()

	var toolCalls []ToolCall
	for _, tc := range a.toolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments.String(),
		})
	}

	return &Response{
		Content:    a.text.String(),
		Model:      a.model,
		StopReason: a.stopReason,
		Usage:      a.usage,
		ToolCalls:  toolCalls,
		ProviderMetadata: map[string]any{
			"chunk_count":     len(a.chunks),
			"stream_duration": a.endTime.Sub(a.startTime).String(),
		},
	}
}

// Text returns the accumulated text so far
func (a *StreamAccumulator) Text() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.text.String()
}

// ChunkCount returns the number of chunks received
func (a *StreamAccumulator) ChunkCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.chunks)
}

// StreamCollector wraps StreamHandler with automatic accumulation
type StreamCollector struct {
	accumulator *StreamAccumulator
	onChunk     func(chunk *StreamChunk)
	onError     func(err error)
}

// NewStreamCollector creates a collector with optional callbacks
func NewStreamCollector(onChunk func(chunk *StreamChunk), onError func(err error)) *StreamCollector {
	return &StreamCollector{
		accumulator: NewStreamAccumulator(),
		onChunk:     onChunk,
		onError:     onError,
	}
}

// Handler returns a StreamHandler that accumulates and optionally forwards chunks
func (c *StreamCollector) Handler() StreamHandler {
	return func(chunk *StreamChunk) error {
		c.accumulator.Add(chunk)
		if c.onChunk != nil {
			c.onChunk(chunk)
		}
		return nil
	}
}

// Response returns the accumulated response
func (c *StreamCollector) Response() *Response {
	return c.accumulator.Response()
}

// StreamWithCallback is a convenience function to stream with a simple text callback
func StreamWithCallback(
	ctx context.Context,
	provider Provider,
	req *Request,
	onText func(text string),
) (*Response, error) {
	collector := NewStreamCollector(func(chunk *StreamChunk) {
		if chunk.Type == ChunkTypeText && onText != nil {
			onText(chunk.Text)
		}
	}, nil)

	if err := provider.Stream(ctx, req, collector.Handler()); err != nil {
		return nil, err
	}

	return collector.Response(), nil
}

// StreamToChannel streams chunks to a channel
func StreamToChannel(
	ctx context.Context,
	provider Provider,
	req *Request,
) (<-chan *StreamChunk, <-chan error) {
	chunks := make(chan *StreamChunk, 100)
	errs := make(chan error, 1)

	go func() {
		defer close(chunks)
		defer close(errs)

		err := provider.Stream(ctx, req, func(chunk *StreamChunk) error {
			select {
			case chunks <- chunk:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})

		if err != nil {
			errs <- err
		}
	}()

	return chunks, errs
}
