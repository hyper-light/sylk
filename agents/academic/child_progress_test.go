package academic

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
)

type delayedAcademicChildProvider struct {
	delay time.Duration
}

func (p *delayedAcademicChildProvider) Complete(ctx context.Context, _ *providers.Request) (*providers.Response, error) {
	if p.delay > 0 {
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return &providers.Response{
		Content: "Use the official packaging guidance as the primary source.",
		Model:   "gpt-5.4-pro",
		ProviderMetadata: map[string]any{
			providers.ProviderMetadataNativeWebSearchCallsKey: []providers.NativeWebSearchCall{
				{ID: "ws_1", Provider: "openai", Action: "search", Query: "python packaging official guide"},
			},
		},
	}, nil
}

type delayedAcademicTerminalProvider struct {
	delay time.Duration
}

func (p *delayedAcademicTerminalProvider) Complete(ctx context.Context, _ *providers.Request) (*providers.Response, error) {
	if p.delay > 0 {
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return &providers.Response{
		Content: "Fetched and summarized the official packaging guidance.",
		Model:   "gpt-5.4-pro",
		ProviderMetadata: map[string]any{
			providers.ProviderMetadataNativeWebSearchCallsKey: []providers.NativeWebSearchCall{
				{
					ID:       "ws_open_1",
					Provider: "openai",
					Action:   "open_page",
					Query:    "python packaging pyproject guide",
					URL:      "https://packaging.python.org/en/latest/guides/writing-pyproject-toml/",
				},
			},
		},
	}, nil
}

func TestAcademicChildPublishesSearchingProgressWithMetadata(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{Factory: newTestFactory(t), ID: "academic", SessionID: "sess-1"}, &delayedAcademicChildProvider{delay: 40 * time.Millisecond})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 16)
	sub, err := bus.SubscribeAsync(a.channels.Responses, func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	correlationID := "corr-academic-child-progress"
	if err := bus.Publish(a.channels.Requests, guide.NewForwardMessage("", &guide.ForwardedRequest{
		CorrelationID: correlationID,
		Input:         "Research Python packaging guidance.",
		Intent:        guide.IntentRecall,
		SessionID:     "sess-1",
		SourceAgentID: "engineer",
		TargetAgentID: a.id,
	})); err != nil {
		t.Fatalf("publish forward: %v", err)
	}

	deadline := time.After(2 * time.Second)
	var sawStart, sawProgress bool
	for !(sawStart && sawProgress) {
		select {
		case stream := <-streamCh:
			if stream == nil || stream.CorrelationID != correlationID || stream.Event == nil {
				continue
			}
			switch stream.Event.Type {
			case guide.StreamEventStart:
				sawStart = true
				if got, _ := stream.Metadata["agent_type"].(string); got != "academic" {
					t.Fatalf("start metadata agent_type = %#v, want academic", stream.Metadata["agent_type"])
				}
				if got, _ := stream.Metadata["agent_name"].(string); got != "Academic" {
					t.Fatalf("start metadata agent_name = %#v, want Academic", stream.Metadata["agent_name"])
				}
			case guide.StreamEventProgress:
				progress, ok := stream.Event.Data.(*guide.ProgressData)
				if !ok || progress == nil {
					t.Fatalf("expected progress payload, got %+v", stream.Event.Data)
				}
				if progress.Message != academicChildProgressMessage {
					continue
				}
				sawProgress = true
				if progress.UIState != events.AgentUIStateSearching {
					t.Fatalf("progress UI state = %q, want %q", progress.UIState, events.AgentUIStateSearching)
				}
				if got, _ := stream.Metadata["agent_type"].(string); got != "academic" {
					t.Fatalf("progress metadata agent_type = %#v, want academic", stream.Metadata["agent_type"])
				}
				if got, _ := stream.Metadata["agent_name"].(string); got != "Academic" {
					t.Fatalf("progress metadata agent_name = %#v, want Academic", stream.Metadata["agent_name"])
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for child academic progress; start=%v progress=%v", sawStart, sawProgress)
		}
	}
}

func TestAcademicChildKeepalivePublishesPeriodicSearchingProgress(t *testing.T) {
	prevKeepalive := academicChildProgressKeepaliveInterval
	academicChildProgressKeepaliveInterval = 10 * time.Millisecond
	defer func() { academicChildProgressKeepaliveInterval = prevKeepalive }()

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{Factory: newTestFactory(t), ID: "academic", SessionID: "sess-1"}, &delayedAcademicTerminalProvider{delay: 55 * time.Millisecond})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	const correlationID = "corr-academic-child-keepalive"
	streamCh := make(chan *guide.StreamResponse, 32)
	responseCh := make(chan *guide.Message, 4)
	sub, err := bus.SubscribeAsync(a.channels.Responses, func(msg *guide.Message) error {
		if msg == nil {
			return nil
		}
		switch msg.Type {
		case guide.MessageTypeStream:
			if stream, ok := msg.GetStreamResponse(); ok && stream != nil && stream.CorrelationID == correlationID {
				select {
				case streamCh <- stream:
				default:
				}
			}
		case guide.MessageTypeResponse, guide.MessageTypeError:
			if msg.CorrelationID != correlationID {
				return nil
			}
			select {
			case responseCh <- msg:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	if err := bus.Publish(a.channels.Requests, guide.NewForwardMessage("", &guide.ForwardedRequest{
		CorrelationID: correlationID,
		Input:         "Fetch https://packaging.python.org/en/latest/guides/writing-pyproject-toml/ and summarize it.",
		Intent:        guide.IntentFetch,
		SessionID:     "sess-1",
		SourceAgentID: "engineer",
		TargetAgentID: a.id,
	})); err != nil {
		t.Fatalf("publish forward: %v", err)
	}

	deadline := time.After(2 * time.Second)
	progressCount := 0
	sawTerminal := false
	for !sawTerminal {
		select {
		case stream := <-streamCh:
			if stream == nil || stream.Event == nil {
				continue
			}
			if stream.Event.Type == guide.StreamEventProgress {
				progressCount++
			}
		case msg := <-responseCh:
			if msg != nil {
				sawTerminal = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for periodic academic progress; progress_count=%d terminal=%v", progressCount, sawTerminal)
		}
	}
	if progressCount < 2 {
		t.Fatalf("expected initial progress plus at least one keepalive before terminal response, got %d progress events", progressCount)
	}
}
