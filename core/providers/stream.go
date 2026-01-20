package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamChunk struct {
	Index      int             `json:"index"`
	Text       string          `json:"text"`
	Type       StreamChunkType `json:"type"`
	ToolCall   *ToolCallChunk  `json:"tool_call,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
	StopReason StopReason      `json:"stop_reason,omitempty"`
	Timestamp  time.Time       `json:"timestamp"`
}

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

type ToolCallChunk struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

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

func NewStreamAccumulator() *StreamAccumulator {
	return &StreamAccumulator{
		chunks:    make([]StreamChunk, 0, 100),
		toolCalls: make(map[string]*accumulatedToolCall),
		startTime: time.Now(),
	}
}

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

func (a *StreamAccumulator) Text() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.text.String()
}

func (a *StreamAccumulator) ChunkCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.chunks)
}

type StreamCollector struct {
	accumulator *StreamAccumulator
	onChunk     func(chunk *StreamChunk)
}

func NewStreamCollector(onChunk func(chunk *StreamChunk)) *StreamCollector {
	return &StreamCollector{
		accumulator: NewStreamAccumulator(),
		onChunk:     onChunk,
	}
}

func (c *StreamCollector) Add(chunk *StreamChunk) {
	c.accumulator.Add(chunk)
	if c.onChunk != nil {
		c.onChunk(chunk)
	}
}

func (c *StreamCollector) Response() *Response {
	return c.accumulator.Response()
}

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
	})

	chunks, err := provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	for chunk := range chunks {
		collector.Add(chunk)
	}

	return collector.Response(), nil
}

// StreamToChannel converts a streaming provider into channels for chunks and errors.
// Includes panic recovery to prevent crashes from propagating (W12.45 fix).
func StreamToChannel(
	ctx context.Context,
	provider Provider,
	req *Request,
) (<-chan *StreamChunk, <-chan error) {
	chunks := make(chan *StreamChunk, 100)
	errs := make(chan error, 1)

	go func() {
		// Note: defers execute in LIFO order
		// 1. recoverStreamPanic runs first (sends error to errs channel)
		// 2. close(errs) runs second
		// 3. close(chunks) runs last
		defer close(chunks)
		defer close(errs)
		defer recoverStreamPanic(errs)

		if handlerProvider, ok := provider.(StreamHandlerProvider); ok {
			err := handlerProvider.StreamWithHandler(ctx, req, func(chunk *StreamChunk) error {
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
			return
		}

		stream, err := provider.Stream(ctx, req)
		if err != nil {
			errs <- err
			return
		}
		for chunk := range stream {
			select {
			case chunks <- chunk:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
	}()

	return chunks, errs
}

// recoverStreamPanic recovers from panics in stream goroutines and sends an error.
// This ensures that panics in streaming code don't crash the application (W12.45 fix).
func recoverStreamPanic(errs chan<- error) {
	if r := recover(); r != nil {
		err := fmt.Errorf("panic in stream: %v", r)
		select {
		case errs <- err:
		default:
			// Error channel full or closed, panic was still recovered
		}
	}
}
