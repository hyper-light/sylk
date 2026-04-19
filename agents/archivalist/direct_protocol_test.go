package archivalist

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
)

type captureArchivalistActivityPublisher struct {
	mu     sync.Mutex
	events []*events.ActivityEvent
}

func (p *captureArchivalistActivityPublisher) PublishActivity(event *events.ActivityEvent) error {
	if event == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	clone := *event
	if event.Data != nil {
		clone.Data = make(map[string]any, len(event.Data))
		for key, value := range event.Data {
			clone.Data[key] = value
		}
	}
	p.events = append(p.events, &clone)
	return nil
}

func (p *captureArchivalistActivityPublisher) snapshot() []*events.ActivityEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*events.ActivityEvent, len(p.events))
	copy(out, p.events)
	return out
}

func TestProcessForwardedRequest_StoreBypassesLLMAndUsesStructuredContent(t *testing.T) {
	a := newTestArchivalist(t)
	provider := NewMockProvider()
	provider.SetResponse("unexpected llm call", 1, 1)
	a.provider = provider
	a.config.EnableLLM = true

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		Input:  "@archivalist:store:history{\"content\":\"stored child event\"}",
		Intent: guide.IntentStore,
		Domain: guide.DomainHistory,
		Entities: &guide.ExtractedEntities{
			Data: map[string]any{"content": "stored child event"},
		},
		Metadata: map[string]any{
			"event_type": "child_store",
		},
	})
	if err != nil {
		t.Fatalf("processForwardedRequest returned error: %v", err)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider call count = %d, want 0", provider.CallCount())
	}
	if result == nil {
		t.Fatal("expected store result")
	}
}

func TestHandleBusRequest_NestedFireAndForgetStorePublishesStreamLifecycle(t *testing.T) {
	a := newTestArchivalist(t)
	pub := &captureArchivalistActivityPublisher{}
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })
	a.bus = bus
	a.channels = guide.NewAgentChannels("archivalist", a.id)
	a.runCtx = context.Background()
	a.running = true
	a.activityPub = pub
	// The readiness gate (added in Phase 5) refuses requests when the
	// archivalist has no live provider. Store operations don't actually
	// invoke the LLM, but the gate runs at the routing boundary before
	// per-request intent classification — this stub keeps the agent
	// "ready" so the store path can be exercised.
	a.provider = NewMockProvider()

	streamCh := make(chan *guide.StreamResponse, 4)
	responseCh := make(chan *guide.RouteResponse, 1)
	sub, err := a.bus.Subscribe(a.channels.Responses, func(msg *guide.Message) error {
		if msg == nil || msg.CorrelationID != "corr-nested-store" {
			return nil
		}
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			select {
			case streamCh <- stream:
			default:
			}
		}
		if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
			select {
			case responseCh <- resp:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	err = a.handleBusRequest(guide.NewForwardMessage("msg-nested-store", &guide.ForwardedRequest{
		CorrelationID: "corr-nested-store",
		Input:         "@archivalist:store:history{\"content\":\"stored child event\"}",
		Intent:        guide.IntentStore,
		Domain:        guide.DomainHistory,
		Entities: &guide.ExtractedEntities{
			Data: map[string]any{"content": "stored child event"},
		},
		SourceAgentID: "architect",
		FireAndForget: true,
		SessionID:     "sess-nested-store",
		Metadata: map[string]any{
			"chat_nested_branch":          true,
			"chat_parent_correlation_id":  "corr-parent-store",
			"chat_parent_tool_call_key":   "store-1",
			"chat_inter_agent_kind":       "store",
			"chat_inter_agent_thread_key": "",
			"event_type":                  "child_store",
		},
	}))
	if err != nil {
		t.Fatalf("handleBusRequest returned error: %v", err)
	}

	var sawStart, sawComplete bool
	deadline := time.After(2 * time.Second)
	for !sawStart || !sawComplete {
		select {
		case resp := <-responseCh:
			t.Fatalf("unexpected route response for nested fire-and-forget store: %+v", resp)
		case stream := <-streamCh:
			if stream == nil || stream.Event == nil {
				continue
			}
			if got, _ := stream.Metadata["chat_nested_branch"].(bool); !got {
				t.Fatalf("chat_nested_branch = %#v, want true", stream.Metadata["chat_nested_branch"])
			}
			if got, _ := stream.Metadata["chat_inter_agent_kind"].(string); got != "store" {
				t.Fatalf("chat_inter_agent_kind = %q, want store", got)
			}
			if stream.Event.Visibility != events.VisibilitySystem {
				t.Fatalf("stream visibility = %v, want %v", stream.Event.Visibility, events.VisibilitySystem)
			}
			switch stream.Event.Type {
			case guide.StreamEventStart:
				sawStart = true
			case guide.StreamEventComplete:
				sawComplete = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for nested fire-and-forget stream lifecycle (start=%v complete=%v)", sawStart, sawComplete)
		}
	}

	for _, event := range pub.snapshot() {
		if event == nil || event.CorrelationID != "corr-nested-store" {
			continue
		}
		if event.Visibility != events.VisibilitySystem {
			t.Fatalf("activity visibility = %v, want %v for nested store", event.Visibility, events.VisibilitySystem)
		}
	}
}

func TestProcessForwardedRequest_DeclareAndCompleteBypassLLM(t *testing.T) {
	a := newTestArchivalist(t)
	provider := NewMockProvider()
	provider.SetResponse("unexpected llm call", 1, 1)
	a.provider = provider
	a.config.EnableLLM = true

	tests := []guide.Intent{guide.IntentDeclare, guide.IntentComplete}
	for _, intent := range tests {
		t.Run(string(intent), func(t *testing.T) {
			provider.Reset()
			_, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
				Input:  "structured control intent",
				Intent: intent,
				Domain: guide.DomainHistory,
			})
			if err != nil {
				t.Fatalf("processForwardedRequest returned error: %v", err)
			}
			if provider.CallCount() != 0 {
				t.Fatalf("provider call count = %d, want 0", provider.CallCount())
			}
		})
	}
}

func TestProcessForwardedRequest_BriefingRequestBypassesLLMAndReturnsContextBrief(t *testing.T) {
	a := newTestArchivalist(t)
	provider := NewMockProvider()
	provider.SetResponse("unexpected llm call", 1, 1)
	a.provider = provider
	a.config.EnableLLM = true
	a.SetCurrentTask("Implement handoff persistence", "keep scribe continuity", SourceModelClaudeOpus)
	a.SetCurrentStep("Restore archivalist briefing contract")
	a.SetNextSteps([]string{"Wire shared brief source", "Verify handoff tests"})
	a.AddBlocker("Need canonical briefing surface")

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		Input:  "archivalist_get_briefing",
		Intent: guide.IntentRecall,
		Metadata: map[string]any{
			"request_type": "briefing",
			"tool_name":    ToolGetBriefing,
			"brief_format": "context_brief",
			"brief_tier":   "standard",
			"agent_type":   "engineer",
			"context_size": 4096,
			"turn_number":  7,
		},
	})
	if err != nil {
		t.Fatalf("processForwardedRequest returned error: %v", err)
	}
	if provider.CallCount() != 0 {
		t.Fatalf("provider call count = %d, want 0", provider.CallCount())
	}
	brief, ok := result.(*handoff.ContextBrief)
	if !ok || brief == nil {
		t.Fatalf("briefing result = %#v, want *handoff.ContextBrief", result)
	}
	if brief.TaskSummary == "" {
		t.Fatal("expected task summary")
	}
	if brief.GeneratedAt.Before(time.Now().Add(-5 * time.Second)) {
		t.Fatalf("generated_at = %v, want recent timestamp", brief.GeneratedAt)
	}
	if brief.ContextSize != 4096 || brief.TurnNumber != 7 {
		t.Fatalf("brief metadata = (%d,%d), want (4096,7)", brief.ContextSize, brief.TurnNumber)
	}
}
