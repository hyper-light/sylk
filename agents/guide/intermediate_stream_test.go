package guide

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

type toolLoopGuideProvider struct {
	responses []*providers.Response
	calls     int
}

func (p *toolLoopGuideProvider) Name() string { return "test-guide-provider" }

func (p *toolLoopGuideProvider) SupportedModels() []providers.ModelInfo {
	return []providers.ModelInfo{{ID: "gpt-5.4-pro", Name: "gpt-5.4-pro", MaxContext: 200000}}
}

func (p *toolLoopGuideProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	resp := p.responses[p.calls]
	p.calls++
	return resp, nil
}

func (p *toolLoopGuideProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk)
	close(ch)
	return ch, nil
}

func (p *toolLoopGuideProvider) CountTokens(_ []providers.Message) (int, error) { return 0, nil }

func (p *toolLoopGuideProvider) MaxContextTokens(_ string) int { return 200000 }

func (p *toolLoopGuideProvider) HealthCheck(_ context.Context) error { return nil }

func TestGuideResponderRespondStream_SurfacesIntermediateToolTurns(t *testing.T) {
	provider := &toolLoopGuideProvider{
		responses: []*providers.Response{
			{
				Content: "Investigating routing policy before I answer.",
				ToolCalls: []providers.ToolCall{{
					ID:        "tool-1",
					Name:      "lookup",
					Arguments: `{"topic":"routing"}`,
				}},
			},
			{
				Content: "Final guide answer.",
			},
		},
	}

	responder := NewGuideResponderWithTools(
		provider,
		"gpt-5.4-pro",
		RouterConfig{},
		func(string) []providers.Tool {
			return []providers.Tool{{
				Name:        "lookup",
				Description: "lookup guidance",
				Parameters:  map[string]any{"type": "object"},
			}}
		},
		func(context.Context, string, string) (string, error) {
			return `{"ok":true}`, nil
		},
	)

	var chunks []string
	text, _, err := responder.(GuideSelfStreamResponder).RespondStream(
		context.Background(),
		GuideSelfResponseRequest{
			Input:     "How should Guide route direct requests?",
			SessionID: "session-1",
		},
		func(chunk string) error {
			chunks = append(chunks, chunk)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RespondStream: %v", err)
	}
	if text != "Final guide answer." {
		t.Fatalf("final text = %q", text)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d, want 2", len(chunks))
	}
	if chunks[0] != "Investigating routing policy before I answer.\n\n" {
		t.Fatalf("first chunk = %q", chunks[0])
	}
	if chunks[1] != "Final guide answer." {
		t.Fatalf("second chunk = %q", chunks[1])
	}
}

type streamingFinalGuideProvider struct{}

func (p *streamingFinalGuideProvider) Name() string { return "streaming-final-guide-provider" }
func (p *streamingFinalGuideProvider) SupportedModels() []providers.ModelInfo {
	return []providers.ModelInfo{{ID: "gpt-5.4-pro", Name: "gpt-5.4-pro", MaxContext: 200000}}
}
func (p *streamingFinalGuideProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{Content: "should-not-complete"}, nil
}
func (p *streamingFinalGuideProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk, 3)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "Final guide answer."}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeEnd, Usage: &providers.Usage{InputTokens: 10, OutputTokens: 5}}
	close(ch)
	return ch, nil
}
func (p *streamingFinalGuideProvider) CountTokens(_ []providers.Message) (int, error) { return 0, nil }
func (p *streamingFinalGuideProvider) MaxContextTokens(_ string) int                   { return 200000 }
func (p *streamingFinalGuideProvider) HealthCheck(_ context.Context) error              { return nil }

func TestGuideResponderRespondStream_DoesNotDuplicateFinalStreamedText(t *testing.T) {
	responder := NewGuideResponderWithTools(
		&streamingFinalGuideProvider{},
		"gpt-5.4-pro",
		RouterConfig{},
		nil,
		nil,
	)

	var chunks []string
	text, _, err := responder.(GuideSelfStreamResponder).RespondStream(
		context.Background(),
		GuideSelfResponseRequest{Input: "hello", SessionID: "session-1"},
		func(chunk string) error {
			chunks = append(chunks, chunk)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RespondStream: %v", err)
	}
	if text != "Final guide answer." {
		t.Fatalf("final text = %q", text)
	}
	if len(chunks) != 1 || chunks[0] != "Final guide answer." {
		t.Fatalf("chunks = %#v, want single streamed final chunk", chunks)
	}
}

type partialErrorGuideProvider struct {
	completeCalls int
}

func (p *partialErrorGuideProvider) Name() string { return "partial-error-guide-provider" }
func (p *partialErrorGuideProvider) SupportedModels() []providers.ModelInfo {
	return []providers.ModelInfo{{ID: "gpt-5.4-pro", Name: "gpt-5.4-pro", MaxContext: 200000}}
}
func (p *partialErrorGuideProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.completeCalls++
	return &providers.Response{Content: "should-not-replay"}, nil
}
func (p *partialErrorGuideProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk, 3)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "partial"}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeError, Text: "boom"}
	close(ch)
	return ch, nil
}
func (p *partialErrorGuideProvider) CountTokens(_ []providers.Message) (int, error) { return 0, nil }
func (p *partialErrorGuideProvider) MaxContextTokens(_ string) int                   { return 200000 }
func (p *partialErrorGuideProvider) HealthCheck(_ context.Context) error              { return nil }

func TestGuideResponderRespondStream_DoesNotReplayAfterPartialStream(t *testing.T) {
	provider := &partialErrorGuideProvider{}
	responder := NewGuideResponderWithTools(provider, "gpt-5.4-pro", RouterConfig{}, nil, nil)

	_, _, err := responder.(GuideSelfStreamResponder).RespondStream(
		context.Background(),
		GuideSelfResponseRequest{Input: "hello", SessionID: "session-1"},
		func(string) error { return nil },
	)
	if err == nil {
		t.Fatal("expected stream error")
	}
	if !strings.Contains(err.Error(), "stream error: boom") {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.completeCalls != 0 {
		t.Fatalf("Complete() calls = %d, want 0", provider.completeCalls)
	}
}
