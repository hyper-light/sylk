package academic

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

type intermediateTurnProvider struct {
	calls int
}

func (p *intermediateTurnProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.Response{
			Content: "Searching documentation and package guidance before answering.",
			Model:   "gpt-5.4-pro",
			ToolCalls: []providers.ToolCall{{
				ID:        "tool-1",
				Name:      "search_skills",
				Arguments: `{"query":"fetch"}`,
			}},
		}, nil
	}
	return &providers.Response{
		Content: "Use the fetched guidance to answer with the final recommendation.",
		Model:   "gpt-5.4-pro",
	}, nil
}

type thinkingFallbackIntermediateTurnProvider struct {
	calls int
}

func (p *thinkingFallbackIntermediateTurnProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.Response{
			Thinking: "I’m checking packaging conventions and current recommendations before I answer.",
			Model:    "gpt-5.4-pro",
			ToolCalls: []providers.ToolCall{{
				ID:        "tool-1",
				Name:      "research_topic",
				Arguments: `{"topic":"python packaging"}`,
			}},
		}, nil
	}
	return &providers.Response{
		Content: "Use the final packaging recommendation.",
		Model:   "gpt-5.4-pro",
	}, nil
}

type streamingIntermediateTurnProvider struct {
	calls int
}

func (p *streamingIntermediateTurnProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{
		Content: "Final synthesized packaging guidance.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func (p *streamingIntermediateTurnProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	p.calls++
	ch := make(chan *providers.StreamChunk, 8)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
	if p.calls == 1 {
		ch <- &providers.StreamChunk{Type: providers.ChunkTypeThought, Text: "Inspecting packaging standards."}
		ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "I’m checking current packaging guidance before answering."}
		ch <- &providers.StreamChunk{
			Type: providers.ChunkTypeToolStart,
			ToolCall: &providers.ToolCallChunk{
				ID:   "tool-1",
				Name: "search_skills",
			},
		}
		ch <- &providers.StreamChunk{
			Type: providers.ChunkTypeToolDelta,
			ToolCall: &providers.ToolCallChunk{
				ID:             "tool-1",
				ArgumentsDelta: `{"query":"packaging"}`,
			},
		}
		ch <- &providers.StreamChunk{Type: providers.ChunkTypeToolEnd, ToolCall: &providers.ToolCallChunk{ID: "tool-1"}}
		ch <- &providers.StreamChunk{
			Type:       providers.ChunkTypeEnd,
			Usage:      &providers.Usage{InputTokens: 20, OutputTokens: 10},
			StopReason: providers.StopReasonToolUse,
		}
	} else {
		ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "Final synthesized packaging guidance."}
		ch <- &providers.StreamChunk{
			Type:       providers.ChunkTypeEnd,
			Usage:      &providers.Usage{InputTokens: 12, OutputTokens: 8},
			StopReason: providers.StopReasonEndTurn,
		}
	}
	close(ch)
	return ch, nil
}

func TestAcademicExecuteToolLoop_PublishesIntermediateTurnChunks(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{ID: "academic"}, &intermediateTurnProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 8)
	sub, err := bus.SubscribeAsync(a.channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research Python packaging guidance."}},
		Tools:    a.buildToolDefinitions(),
		Model:    "gpt-5.4-pro",
	}
	ctx := shared.WithStreamContext(context.Background(), "corr-intermediate", "tui")

	if _, err := a.executeToolLoop(ctx, req, nil, nil); err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != "corr-intermediate" || stream.Event == nil {
				continue
			}
			if stream.Event.Type != guide.StreamEventData {
				continue
			}
			if got := stream.Event.Text; got == "Searching documentation and package guidance before answering.\n\n" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for intermediate stream chunk")
		}
	}
}

func TestAcademicExecuteToolLoop_PublishesThinkingFallbackIntermediateTurn(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{ID: "academic"}, &thinkingFallbackIntermediateTurnProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 8)
	sub, err := bus.SubscribeAsync(a.channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research Python packaging guidance."}},
		Tools:    a.buildToolDefinitions(),
		Model:    "gpt-5.4-pro",
	}
	ctx := shared.WithStreamContext(context.Background(), "corr-thinking-fallback", "tui")

	if _, err := a.executeToolLoop(ctx, req, nil, nil); err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != "corr-thinking-fallback" || stream.Event == nil {
				continue
			}
			if stream.Event.Type != guide.StreamEventData {
				continue
			}
			if got := stream.Event.Text; got == "I’m checking packaging conventions and current recommendations before I answer.\n\n" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for thinking fallback intermediate stream chunk")
		}
	}
}

func TestAcademicExecuteToolLoop_StreamsThoughtsAndTextDuringTurn(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{ID: "academic"}, &streamingIntermediateTurnProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 16)
	sub, err := bus.SubscribeAsync(a.channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research Python packaging guidance."}},
		Tools:    a.buildToolDefinitions(),
		Model:    "gpt-5.4-pro",
	}
	a.applyLLMRuntimeProfile(req, "conversation")
	ctx := shared.WithStreamContext(context.Background(), "corr-streaming", "tui")
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus:           bus,
		Channels:      a.channels,
		AgentID:       a.id,
		CorrelationID: "corr-streaming",
		SourceAgentID: "tui",
	})

	if _, err := a.executeToolLoop(ctx, req, nil, nil); err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}

	deadline := time.After(2 * time.Second)
	var sawThought, sawText bool
	for !(sawThought && sawText) {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != "corr-streaming" || stream.Event == nil {
				continue
			}
			switch stream.Event.Type {
			case guide.StreamEventProgress:
				if data, ok := stream.Event.Data.(*guide.ProgressData); ok && data.Message == "Inspecting packaging standards." {
					sawThought = true
				}
			case guide.StreamEventData:
				if stream.Event.Text == "I’m checking current packaging guidance before answering." {
					sawText = true
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for streamed thought/text; sawThought=%v sawText=%v", sawThought, sawText)
		}
	}
}

func TestAcademicExecuteToolLoop_HidesThoughtsForWorkerResearchTurns(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{ID: "academic"}, &streamingIntermediateTurnProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 16)
	sub, err := bus.SubscribeAsync(a.channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research Python packaging guidance."}},
		Tools:    a.buildToolDefinitions(),
		Model:    "gpt-5.4-pro",
	}
	a.applyLLMRuntimeProfile(req, "research")
	ctx := shared.WithStreamContext(context.Background(), "corr-worker-streaming", "architect")
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus:           bus,
		Channels:      a.channels,
		AgentID:       a.id,
		CorrelationID: "corr-worker-streaming",
		SourceAgentID: "architect",
	})

	if _, err := a.executeToolLoop(ctx, req, nil, nil); err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}

	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != "corr-worker-streaming" || stream.Event == nil {
				continue
			}
			if stream.Event.Type == guide.StreamEventProgress {
				if data, ok := stream.Event.Data.(*guide.ProgressData); ok && strings.TrimSpace(data.Message) != "" {
					t.Fatalf("unexpected thought progress for worker research turn: %q", data.Message)
				}
			}
		case <-deadline:
			return
		}
	}
}
