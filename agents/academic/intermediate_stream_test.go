package academic

import (
	"context"
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

type delayedToolEndProvider struct {
	calls   int
	release chan struct{}
}

func (p *delayedToolEndProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{
		Content: "Final synthesized packaging guidance.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func (p *delayedToolEndProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	p.calls++
	ch := make(chan *providers.StreamChunk, 8)
	go func() {
		defer close(ch)
		ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
		if p.calls == 1 {
			ch <- &providers.StreamChunk{
				Type: providers.ChunkTypeToolStart,
				ToolCall: &providers.ToolCallChunk{
					ID:   "tool-delayed-1",
					Name: "search_skills",
				},
			}
			<-p.release
			ch <- &providers.StreamChunk{
				Type: providers.ChunkTypeToolDelta,
				ToolCall: &providers.ToolCallChunk{
					ID:             "tool-delayed-1",
					ArgumentsDelta: `{"query":"packaging"}`,
				},
			}
			ch <- &providers.StreamChunk{Type: providers.ChunkTypeToolEnd, ToolCall: &providers.ToolCallChunk{ID: "tool-delayed-1"}}
			ch <- &providers.StreamChunk{
				Type:       providers.ChunkTypeEnd,
				Usage:      &providers.Usage{InputTokens: 10, OutputTokens: 5},
				StopReason: providers.StopReasonToolUse,
			}
			return
		}
		ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "Final synthesized packaging guidance."}
		ch <- &providers.StreamChunk{
			Type:       providers.ChunkTypeEnd,
			Usage:      &providers.Usage{InputTokens: 8, OutputTokens: 6},
			StopReason: providers.StopReasonEndTurn,
		}
	}()
	return ch, nil
}

func TestAcademicExecuteToolLoop_PublishesIntermediateTurnChunks(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, &intermediateTurnProvider{})
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
			if stream.Event.Type != guide.StreamEventProgress {
				continue
			}
			progress, ok := stream.Event.Data.(*guide.ProgressData)
			if !ok || progress == nil {
				t.Fatalf("expected progress payload, got %+v", stream.Event.Data)
			}
			if got := progress.Message; got == "Searching documentation and package guidance before answering." {
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

	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, &thinkingFallbackIntermediateTurnProvider{})
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
			if stream.Event.Type != guide.StreamEventProgress {
				continue
			}
			progress, ok := stream.Event.Data.(*guide.ProgressData)
			if !ok || progress == nil {
				t.Fatalf("expected progress payload, got %+v", stream.Event.Data)
			}
			if got := progress.Message; got == "I’m checking packaging conventions and current recommendations before I answer." {
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

	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, &streamingIntermediateTurnProvider{})
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
	a.applyLLMRuntimeProfile(context.Background(), req, "conversation")
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
	var sawThought, sawIntermediateProgress, sawFinalText bool
	for !(sawThought && sawIntermediateProgress && sawFinalText) {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != "corr-streaming" || stream.Event == nil {
				continue
			}
			switch stream.Event.Type {
			case guide.StreamEventProgress:
				if data, ok := stream.Event.Data.(*guide.ProgressData); ok {
					switch data.Message {
					case "Inspecting packaging standards.":
						sawThought = true
					case "I’m checking current packaging guidance before answering.":
						sawIntermediateProgress = true
					}
				}
			case guide.StreamEventData:
				if stream.Event.Text == "I’m checking current packaging guidance before answering." {
					t.Fatalf("provisional streamed text should not be published as final data: %q", stream.Event.Text)
				}
				if stream.Event.Text == "Final synthesized packaging guidance." {
					sawFinalText = true
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for streamed academic lifecycle; sawThought=%v sawIntermediateProgress=%v sawFinalText=%v", sawThought, sawIntermediateProgress, sawFinalText)
		}
	}
}

func TestAcademicExecuteToolLoop_StreamsToolCallsDuringTurn(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, &streamingIntermediateTurnProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 32)
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
	a.applyLLMRuntimeProfile(context.Background(), req, "conversation")
	ctx := shared.WithStreamContext(context.Background(), "corr-streaming-tools", "tui")
	ctx = shared.WithStreamContextMetadata(ctx, map[string]any{
		"agent_type": "academic",
	})
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus:           bus,
		Channels:      a.channels,
		AgentID:       a.id,
		CorrelationID: "corr-streaming-tools",
		SourceAgentID: "tui",
	})
	ctx = shared.WithToolCallEmitter(ctx, shared.NewToolCallEmitter(bus, a.channels, a.id, "corr-streaming-tools", "tui"))

	if _, err := a.executeToolLoop(ctx, req, nil, nil); err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}

	deadline := time.After(2 * time.Second)
	var sawStart, sawComplete bool
	for !(sawStart && sawComplete) {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != "corr-streaming-tools" || stream.Event == nil {
				continue
			}
			if stream.Event.Type != guide.StreamEventToolCall {
				continue
			}
			data, ok := stream.Event.Data.(map[string]any)
			if !ok {
				continue
			}
			if got, _ := data["tool_name"].(string); got != "search_skills" {
				continue
			}
			switch phase := data["phase"].(type) {
			case int:
				if phase == 0 {
					sawStart = true
				}
				if phase == 1 {
					sawComplete = true
				}
			case float64:
				if int(phase) == 0 {
					sawStart = true
				}
				if int(phase) == 1 {
					sawComplete = true
				}
			}
			if got, _ := stream.Metadata["agent_type"].(string); got != "academic" {
				t.Fatalf("tool-call stream metadata agent_type = %#v, want academic", stream.Metadata["agent_type"])
			}
		case <-deadline:
			t.Fatalf("timed out waiting for tool-call events; sawStart=%v sawComplete=%v", sawStart, sawComplete)
		}
	}
}

func TestAcademicExecuteToolLoop_StreamsToolCallStartBeforeToolEnd(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	provider := &delayedToolEndProvider{release: make(chan struct{})}
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 32)
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
	a.applyLLMRuntimeProfile(context.Background(), req, "conversation")
	ctx := shared.WithStreamContext(context.Background(), "corr-streaming-tool-start", "tui")
	ctx = shared.WithStreamContextMetadata(ctx, map[string]any{"agent_type": "academic"})
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus:           bus,
		Channels:      a.channels,
		AgentID:       a.id,
		CorrelationID: "corr-streaming-tool-start",
		SourceAgentID: "tui",
	})
	ctx = shared.WithToolCallEmitter(ctx, shared.NewToolCallEmitter(bus, a.channels, a.id, "corr-streaming-tool-start", "tui"))

	done := make(chan error, 1)
	go func() {
		_, err := a.executeToolLoop(ctx, req, nil, nil)
		done <- err
	}()

	deadline := time.After(2 * time.Second)
	sawStart := false
	for !sawStart {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != "corr-streaming-tool-start" || stream.Event == nil || stream.Event.Type != guide.StreamEventToolCall {
				continue
			}
			data, ok := stream.Event.Data.(map[string]any)
			if !ok {
				continue
			}
			if got, _ := data["tool_name"].(string); got != "search_skills" {
				continue
			}
			switch phase := data["phase"].(type) {
			case int:
				sawStart = phase == 0
			case float64:
				sawStart = int(phase) == 0
			}
		case err := <-done:
			t.Fatalf("tool loop returned before streamed tool start was observed: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for streamed tool start before tool end")
		}
	}

	close(provider.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeToolLoop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tool loop completion after releasing delayed tool end")
	}
}

func TestAcademicExecuteToolLoop_HidesThoughtsForWorkerResearchTurns(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, &streamingIntermediateTurnProvider{})
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
	a.applyLLMRuntimeProfile(context.Background(), req, "research")
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
				if data, ok := stream.Event.Data.(*guide.ProgressData); ok {
					if data.Message == "Inspecting packaging standards." {
						t.Fatalf("unexpected hidden thought progress for worker research turn: %q", data.Message)
					}
				}
			}
		case <-deadline:
			return
		}
	}
}
