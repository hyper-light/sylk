package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

type streamingWatchdogProvider struct {
	completeCalls int
	streamCalls   int
}

func (p *streamingWatchdogProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.completeCalls++
	return &providers.Response{Content: "sync"}, nil
}

func (p *streamingWatchdogProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	p.streamCalls++
	ch := make(chan *providers.StreamChunk, 4)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeThought, Text: "Planning the response."}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "Live streamed reply."}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeEnd, Usage: &providers.Usage{InputTokens: 10, OutputTokens: 5}}
	close(ch)
	return ch, nil
}

func TestCompleteWithWatchdog_StreamsLiveTurnForTUI(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("engineer", "engineer")
	streams := make(chan *guide.StreamResponse, 8)
	sub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			streams <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithProgressPublisher(context.Background(), &ProgressPublisher{
		Bus:           bus,
		Channels:      channels,
		AgentID:       "engineer",
		CorrelationID: "corr-live",
		SourceAgentID: "tui",
	})

	req := &providers.Request{
		Model: "gpt-5.4-pro",
		Metadata: map[string]any{
			"llm_thought_visibility": "hidden",
			"llm_emit_thoughts":      false,
		},
	}
	provider := &streamingWatchdogProvider{}
	resp, err := CompleteWithWatchdog(ctx, provider, req, AgentDisplayName("engineer"))
	if err != nil {
		t.Fatalf("CompleteWithWatchdog: %v", err)
	}
	if provider.streamCalls != 1 || provider.completeCalls != 0 {
		t.Fatalf("stream=%d complete=%d, want stream=1 complete=0", provider.streamCalls, provider.completeCalls)
	}
	if !ResponseStreamedText(resp) {
		t.Fatal("expected streamed response to be marked as live-text streamed")
	}

	deadline := time.After(2 * time.Second)
	var sawThought, sawText bool
	for !(sawThought && sawText) {
		select {
		case stream := <-streams:
			if stream.CorrelationID != "corr-live" || stream.Event == nil {
				continue
			}
			switch stream.Event.Type {
			case guide.StreamEventProgress:
				if data, ok := stream.Event.Data.(*guide.ProgressData); ok && data.Message == "Planning the response." {
					sawThought = true
				}
			case guide.StreamEventData:
				if stream.Event.Text == "Live streamed reply." {
					sawText = true
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for live stream events; sawThought=%v sawText=%v", sawThought, sawText)
		}
	}
}

func TestCompleteWithWatchdog_KeepsSyncPathForInternalTurns(t *testing.T) {
	req := &providers.Request{Model: "gpt-5.4-pro"}
	provider := &streamingWatchdogProvider{}
	ctx := WithProgressPublisher(context.Background(), &ProgressPublisher{
		SourceAgentID: "architect",
	})

	resp, err := CompleteWithWatchdog(ctx, provider, req, AgentDisplayName("engineer"))
	if err != nil {
		t.Fatalf("CompleteWithWatchdog: %v", err)
	}
	if provider.completeCalls != 1 || provider.streamCalls != 0 {
		t.Fatalf("stream=%d complete=%d, want stream=0 complete=1", provider.streamCalls, provider.completeCalls)
	}
	if resp == nil || resp.Content != "sync" {
		t.Fatalf("unexpected sync response: %#v", resp)
	}
}

type noOpStreamingWatchdogProvider struct {
	completeCalls int
	streamCalls   int
}

func (p *noOpStreamingWatchdogProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.completeCalls++
	return &providers.Response{Content: "fallback"}, nil
}

func (p *noOpStreamingWatchdogProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	p.streamCalls++
	ch := make(chan *providers.StreamChunk)
	close(ch)
	return ch, nil
}

func TestCompleteWithWatchdog_FallsBackOnlyWhenStreamNeverStarts(t *testing.T) {
	provider := &noOpStreamingWatchdogProvider{}
	ctx := WithProgressPublisher(context.Background(), &ProgressPublisher{
		SourceAgentID: "tui",
	})

	resp, err := CompleteWithWatchdog(ctx, provider, &providers.Request{Model: "gpt-5.4-pro"}, AgentDisplayName("engineer"))
	if err != nil {
		t.Fatalf("CompleteWithWatchdog: %v", err)
	}
	if provider.streamCalls != 1 || provider.completeCalls != 1 {
		t.Fatalf("stream=%d complete=%d, want stream=1 complete=1", provider.streamCalls, provider.completeCalls)
	}
	if resp == nil || resp.Content != "fallback" {
		t.Fatalf("unexpected fallback response: %#v", resp)
	}
}

type partialErrorStreamingWatchdogProvider struct {
	completeCalls int
	streamCalls   int
}

func (p *partialErrorStreamingWatchdogProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.completeCalls++
	return &providers.Response{Content: "should-not-run"}, nil
}

func (p *partialErrorStreamingWatchdogProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	p.streamCalls++
	ch := make(chan *providers.StreamChunk, 3)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "partial"}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeError, Text: "boom"}
	close(ch)
	return ch, nil
}

func TestCompleteWithWatchdog_DoesNotReplayAfterPartialStream(t *testing.T) {
	provider := &partialErrorStreamingWatchdogProvider{}
	ctx := WithProgressPublisher(context.Background(), &ProgressPublisher{
		SourceAgentID: "tui",
	})

	resp, err := CompleteWithWatchdog(ctx, provider, &providers.Request{Model: "gpt-5.4-pro"}, AgentDisplayName("engineer"))
	if err == nil {
		t.Fatal("expected stream error")
	}
	if provider.streamCalls != 1 || provider.completeCalls != 0 {
		t.Fatalf("stream=%d complete=%d, want stream=1 complete=0", provider.streamCalls, provider.completeCalls)
	}
	if resp == nil || resp.Content != "partial" {
		t.Fatalf("unexpected partial response: %#v", resp)
	}
}
