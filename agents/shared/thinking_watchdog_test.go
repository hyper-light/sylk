package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/steering"
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
	sub, err := bus.Subscribe(channels.Responses, func(msg *guide.Message) error {
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

func TestCompleteWithWatchdog_SuppressesTesterThoughtProgress(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("tester", "tester")
	streams := make(chan *guide.StreamResponse, 8)
	sub, err := bus.Subscribe(channels.Responses, func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			streams <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithStreamContext(context.Background(), "corr-tester-live", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{"agent_type": "tester"})
	ctx = WithProgressPublisher(ctx, &ProgressPublisher{
		Bus:           bus,
		Channels:      channels,
		AgentID:       "tester",
		CorrelationID: "corr-tester-live",
		SourceAgentID: "tui",
	})

	provider := &streamingWatchdogProvider{}
	resp, err := CompleteWithWatchdog(ctx, provider, &providers.Request{
		Model: "gpt-5.4-pro",
	}, AgentDisplayName("tester"))
	if err != nil {
		t.Fatalf("CompleteWithWatchdog: %v", err)
	}
	if !ResponseStreamedText(resp) {
		t.Fatal("expected streamed response to be marked as live-text streamed")
	}

	deadline := time.After(2 * time.Second)
	var sawThought, sawText bool
	for !sawText {
		select {
		case stream := <-streams:
			if stream == nil || stream.Event == nil {
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
			t.Fatalf("timed out waiting for tester live stream; sawThought=%v sawText=%v", sawThought, sawText)
		}
	}
	if sawThought {
		t.Fatal("expected tester thought summary progress to be suppressed")
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

type gatedWatchdogProvider struct {
	started chan struct{}
}

func (p *gatedWatchdogProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.started <- struct{}{}
	return &providers.Response{Content: "ok"}, nil
}

func TestCompleteWithWatchdog_WaitsWhilePaused(t *testing.T) {
	ledger := steering.NewSteeringLedger("corr-paused-llm", "engineer", "sess", nil, nil)
	ledger.SetPace(steering.PacePaused)
	ctx := WithSteeringLedger(context.Background(), ledger)

	provider := &gatedWatchdogProvider{started: make(chan struct{}, 1)}
	done := make(chan error, 1)

	go func() {
		_, err := CompleteWithWatchdog(ctx, provider, &providers.Request{Model: "gpt-5.4-pro"}, AgentDisplayName("engineer"))
		done <- err
	}()

	select {
	case <-provider.started:
		t.Fatal("provider started while paused")
	case <-time.After(100 * time.Millisecond):
	}

	ledger.SetPace(steering.PaceAuto)

	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start after resume")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CompleteWithWatchdog returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CompleteWithWatchdog did not finish after resume")
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

func TestProgressPublisher_SuppressesLateProgressAfterStreamComplete(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("academic", "academic")
	streams := make(chan *guide.StreamResponse, 8)
	sub, err := bus.Subscribe(channels.Responses, func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			streams <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithForwardedStreamContext(context.Background(), "corr-complete", "tui", "", nil)
	ctx = WithProgressPublisher(ctx, &ProgressPublisher{
		Bus:           bus,
		Channels:      channels,
		AgentID:       "academic",
		CorrelationID: "corr-complete",
		SourceAgentID: "tui",
	})

	PublishStreamComplete(bus, channels, ctx, "academic", "", nil)
	if pp := ProgressPublisherFromContext(ctx); pp != nil {
		pp.Publish("Academic is reasoning deeply...")
	}

	time.Sleep(50 * time.Millisecond)

	var got []guide.StreamEventType
drain:
	for {
		select {
		case stream := <-streams:
			if stream != nil && stream.Event != nil {
				got = append(got, stream.Event.Type)
			}
		default:
			break drain
		}
	}

	if len(got) != 1 || got[0] != guide.StreamEventComplete {
		t.Fatalf("expected only stream complete, got %#v", got)
	}
}

func TestProgressPublisher_AllowsCompleteAfterErrorButSuppressesLateProgress(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("academic", "academic")
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

	ctx := WithForwardedStreamContext(context.Background(), "corr-error", "tui", "", nil)
	ctx = WithProgressPublisher(ctx, &ProgressPublisher{
		Bus:           bus,
		Channels:      channels,
		AgentID:       "academic",
		CorrelationID: "corr-error",
		SourceAgentID: "tui",
	})

	PublishStreamError(bus, channels, ctx, "academic", context.DeadlineExceeded)
	if pp := ProgressPublisherFromContext(ctx); pp != nil {
		pp.Publish("late progress")
	}
	PublishStreamComplete(bus, channels, ctx, "academic", "", nil)

	time.Sleep(50 * time.Millisecond)

	var got []guide.StreamEventType
drain:
	for {
		select {
		case stream := <-streams:
			if stream != nil && stream.Event != nil {
				got = append(got, stream.Event.Type)
			}
		default:
			break drain
		}
	}

	if len(got) != 2 {
		t.Fatalf("event count = %d, want 2 (%#v)", len(got), got)
	}
	var sawError, sawComplete bool
	for _, eventType := range got {
		if eventType == guide.StreamEventError {
			sawError = true
		}
		if eventType == guide.StreamEventComplete {
			sawComplete = true
		}
	}
	if !sawError || !sawComplete {
		t.Fatalf("expected error + complete only, got %#v", got)
	}
}

type retryResetStreamingWatchdogProvider struct{}

func (p *retryResetStreamingWatchdogProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{Content: "should-not-run"}, nil
}

func (p *retryResetStreamingWatchdogProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk, 5)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeError, Text: "retry me"}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart, RetryReset: true}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "recovered"}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeEnd}
	close(ch)
	return ch, nil
}

func TestCompleteWithWatchdog_ClearsErrorOnRetryReset(t *testing.T) {
	ctx := WithProgressPublisher(context.Background(), &ProgressPublisher{
		SourceAgentID: "tui",
	})

	resp, err := CompleteWithWatchdog(ctx, &retryResetStreamingWatchdogProvider{}, &providers.Request{Model: "gpt-5.4-pro"}, AgentDisplayName("engineer"))
	if err != nil {
		t.Fatalf("CompleteWithWatchdog: %v", err)
	}
	if resp == nil || resp.Content != "recovered" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

type toolStreamingWatchdogProvider struct{}

func (p *toolStreamingWatchdogProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{Content: "should-not-run"}, nil
}

func (p *toolStreamingWatchdogProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk, 6)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart}
	ch <- &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:   "tool-1",
			Name: "read_file",
		},
	}
	ch <- &providers.StreamChunk{
		Type: providers.ChunkTypeToolDelta,
		ToolCall: &providers.ToolCallChunk{
			ID:             "tool-1",
			ArgumentsDelta: `{"path":"README.md"}`,
		},
	}
	ch <- &providers.StreamChunk{
		Type: providers.ChunkTypeToolEnd,
		ToolCall: &providers.ToolCallChunk{
			ID: "tool-1",
		},
	}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "Working on it."}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeEnd}
	close(ch)
	return ch, nil
}

func TestCompleteWithWatchdog_EmitsToolCallStartFromStreamingProvider(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithProgressPublisher(context.Background(), &ProgressPublisher{
		SourceAgentID: "tui",
	})
	ctx = WithStreamContext(ctx, "corr-live-tool", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{"agent_type": "engineer"})
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) { events = append(events, ev) })

	resp, err := CompleteWithWatchdog(ctx, &toolStreamingWatchdogProvider{}, &providers.Request{
		Model: "gpt-5.4-pro",
	}, AgentDisplayName("engineer"))
	if err != nil {
		t.Fatalf("CompleteWithWatchdog: %v", err)
	}
	if resp == nil || len(resp.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(resp.ToolCalls))
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 streamed tool start event, got %d", len(events))
	}
	if events[0].Phase != ToolCallStart {
		t.Fatalf("phase = %d, want start", events[0].Phase)
	}
	if events[0].ToolName != "read_file" {
		t.Fatalf("tool name = %q, want read_file", events[0].ToolName)
	}
	if got, ok := events[0].StreamMetadata["agent_type"].(string); !ok || got != "engineer" {
		t.Fatalf("agent_type = %#v, want engineer", events[0].StreamMetadata["agent_type"])
	}
}

func TestWithProgressPublisher_PublishesRetryStatus(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("engineer", "engineer")
	streams := make(chan *guide.StreamResponse, 4)
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
		CorrelationID: "corr-retry",
		SourceAgentID: "tui",
	})

	obs := providers.RetryObserverFromContext(ctx)
	if obs == nil {
		t.Fatal("expected retry observer")
	}
	obs(providers.RetryEvent{
		Attempt:     2,
		MaxAttempts: 3,
		Delay:       2 * time.Second,
		Err:         context.DeadlineExceeded,
	})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case stream := <-streams:
			if stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventRetry {
				continue
			}
			status, ok := stream.Event.Data.(guide.RetryStatus)
			if !ok {
				t.Fatalf("retry status type = %T", stream.Event.Data)
			}
			if status.Attempt != 2 || status.MaxAttempts != 3 || status.Delay != 2*time.Second {
				t.Fatalf("unexpected retry status: %#v", status)
			}
			if status.Err == nil || status.Err.Error() != context.DeadlineExceeded.Error() {
				t.Fatalf("unexpected retry error: %v", status.Err)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for retry status")
		}
	}
}

func TestWithProgressPublisher_PreservesStreamMetadataOnStartAndProgress(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels("librarian", "librarian")
	streams := make(chan *guide.StreamResponse, 4)
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

	ctx := WithStreamContext(context.Background(), "corr-child", "tui")
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		streamMetadataNestedBranch:      true,
		streamMetadataParentCorrelation: "corr-parent",
		streamMetadataParentToolCallKey: "consult-1",
		streamMetadataInterAgentKind:    InterAgentToolEventKindConsult,
		"agent_type":                    "librarian",
	})
	ctx = WithProgressPublisher(ctx, &ProgressPublisher{
		Bus:           bus,
		Channels:      channels,
		AgentID:       "librarian",
		CorrelationID: "corr-child",
		SourceAgentID: "tui",
	})

	pp := ProgressPublisherFromContext(ctx)
	if pp == nil {
		t.Fatal("expected progress publisher on context")
	}
	pp.PublishStart()
	pp.Publish("Searching the codebase.")

	for i := 0; i < 2; i++ {
		select {
		case stream := <-streams:
			if stream == nil {
				t.Fatal("expected stream payload")
			}
			if got, _ := stream.Metadata[streamMetadataParentCorrelation].(string); got != "corr-parent" {
				t.Fatalf("parent correlation = %q, want corr-parent", got)
			}
			if got, _ := stream.Metadata[streamMetadataParentToolCallKey].(string); got != "consult-1" {
				t.Fatalf("parent tool call key = %q, want consult-1", got)
			}
			if got, _ := stream.Metadata[streamMetadataNestedBranch].(bool); !got {
				t.Fatalf("nested branch flag = %#v, want true", stream.Metadata[streamMetadataNestedBranch])
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for progress publisher events")
		}
	}
}
