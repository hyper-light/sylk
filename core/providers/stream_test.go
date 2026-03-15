package providers

import (
	"context"
	"strings"
	"testing"
)

type panicHandlerStreamProvider struct {
	panicMessage string
}

func (p *panicHandlerStreamProvider) Name() string {
	return "panic-handler-provider"
}

func (p *panicHandlerStreamProvider) SupportedModels() []ModelInfo {
	return nil
}

func (p *panicHandlerStreamProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	return nil, nil
}

func (p *panicHandlerStreamProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	return nil, nil
}

func (p *panicHandlerStreamProvider) CountTokens(messages []Message) (int, error) {
	return 0, nil
}

func (p *panicHandlerStreamProvider) MaxContextTokens(model string) int {
	return 0
}

func (p *panicHandlerStreamProvider) HealthCheck(ctx context.Context) error {
	return nil
}

func (p *panicHandlerStreamProvider) StreamWithHandler(ctx context.Context, req *Request, handler StreamHandler) error {
	panic(p.panicMessage)
}

func TestStreamViaHandler_RecoversPanic(t *testing.T) {
	stream := streamViaHandler(
		context.Background(),
		&panicHandlerStreamProvider{panicMessage: "handler boom"},
		&Request{},
	)

	var chunks []*StreamChunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	if got := chunks[0].Type; got != ChunkTypeError {
		t.Fatalf("chunk type = %q, want %q", got, ChunkTypeError)
	}

	if !strings.Contains(chunks[0].Text, "panic in stream") {
		t.Fatalf("chunk text should contain panic marker, got %q", chunks[0].Text)
	}

	if !strings.Contains(chunks[0].Text, "handler boom") {
		t.Fatalf("chunk text should contain panic message, got %q", chunks[0].Text)
	}
}
