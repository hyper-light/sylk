package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestRequestGuideRouteSyncWaitsForMatchingTerminalResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		_ = bus.Publish(guide.TopicResponses("tui", "tui"), guide.NewResponseMessage("ignore", &guide.RouteResponse{
			CorrelationID:     "other",
			Success:           true,
			RespondingAgentID: "tester-pipeline",
		}))
		return bus.Publish(guide.TopicResponses("tui", "tui"), guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			Data:              map[string]any{"ok": true},
			RespondingAgentID: "tester-pipeline",
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msg, err := RequestGuideRouteSync(ctx, GuideRouteSyncRequest{
		Bus:           bus,
		ResponseTopic: guide.TopicResponses("tui", "tui"),
		Request: &guide.RouteRequest{
			SourceAgentID:  "tui",
			TargetAgentID:  "tester-pipeline",
			ExplicitTarget: true,
			Input:          `{"task_id":"task_1"}`,
		},
	})
	if err != nil {
		t.Fatalf("RequestGuideRouteSync: %v", err)
	}

	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		t.Fatalf("expected route response, got %#v", msg)
	}
	if resp.CorrelationID == "other" {
		t.Fatal("received mismatched response")
	}
}

func TestRequestGuideRouteSyncInheritsParentCorrelationWithoutSynthesizingNestedBranch(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requests := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requests <- req:
		default:
		}
		return bus.Publish(guide.TopicResponses("tui", "tui"), guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "guardian",
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(WithStreamContext(context.Background(), "corr-parent", "tui"), 2*time.Second)
	defer cancel()

	if _, err := RequestGuideRouteSync(ctx, GuideRouteSyncRequest{
		Bus:           bus,
		ResponseTopic: guide.TopicResponses("tui", "tui"),
		Request: &guide.RouteRequest{
			SourceAgentID:  "tui",
			TargetAgentID:  "guardian",
			ExplicitTarget: true,
			Input:          "check",
		},
	}); err != nil {
		t.Fatalf("RequestGuideRouteSync: %v", err)
	}

	select {
	case req := <-requests:
		if req.ParentCorrelationID != "corr-parent" {
			t.Fatalf("parent_correlation_id = %q, want %q", req.ParentCorrelationID, "corr-parent")
		}
		if got, _ := req.Metadata["chat_preserve_source_stream_target"].(bool); !got {
			t.Fatalf("chat_preserve_source_stream_target = %#v, want true", req.Metadata["chat_preserve_source_stream_target"])
		}
		if _, ok := req.Metadata[streamMetadataNestedBranch]; ok {
			t.Fatalf("chat_nested_branch = %#v, want absent", req.Metadata[streamMetadataNestedBranch])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for routed request")
	}
}

func TestRequestGuideRouteSyncPreservesExplicitNestedBranchMetadata(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requests := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requests <- req:
		default:
		}
		return bus.Publish(guide.TopicResponses("tui", "tui"), guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "academic",
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := RequestGuideRouteSync(ctx, GuideRouteSyncRequest{
		Bus:           bus,
		ResponseTopic: guide.TopicResponses("tui", "tui"),
		Request: &guide.RouteRequest{
			SourceAgentID:  "tui",
			TargetAgentID:  "academic",
			ExplicitTarget: true,
			Input:          "check",
			Metadata: map[string]any{
				streamMetadataNestedBranch:      true,
				streamMetadataParentCorrelation: "corr-parent",
				streamMetadataParentToolCallKey: "consult-1",
				streamMetadataInterAgentKind:    InterAgentToolEventKindConsult,
			},
		},
	}); err != nil {
		t.Fatalf("RequestGuideRouteSync: %v", err)
	}

	select {
	case req := <-requests:
		if got, _ := req.Metadata["chat_preserve_source_stream_target"].(bool); !got {
			t.Fatalf("chat_preserve_source_stream_target = %#v, want true", req.Metadata["chat_preserve_source_stream_target"])
		}
		if got, _ := req.Metadata[streamMetadataNestedBranch].(bool); !got {
			t.Fatalf("chat_nested_branch = %#v, want true", req.Metadata[streamMetadataNestedBranch])
		}
		if got, _ := req.Metadata[streamMetadataInterAgentKind].(string); got != InterAgentToolEventKindConsult {
			t.Fatalf("chat_inter_agent_kind = %q, want %q", got, InterAgentToolEventKindConsult)
		}
		if got, _ := req.Metadata[streamMetadataParentToolCallKey].(string); got != "consult-1" {
			t.Fatalf("chat_parent_tool_call_key = %q, want consult-1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for routed request")
	}
}

func TestInactivityTimeoutOrDefault_UsesTargetAwareDefault(t *testing.T) {
	if got := inactivityTimeoutOrDefault(0, "academic"); got != DefaultResearchConsultationTimeout {
		t.Fatalf("inactivityTimeoutOrDefault(academic) = %v, want %v", got, DefaultResearchConsultationTimeout)
	}
	if got := inactivityTimeoutOrDefault(0, "librarian"); got != DefaultConsultationTimeout {
		t.Fatalf("inactivityTimeoutOrDefault(librarian) = %v, want %v", got, DefaultConsultationTimeout)
	}
	if got := inactivityTimeoutOrDefault(5*time.Second, "academic"); got != 5*time.Second {
		t.Fatalf("inactivityTimeoutOrDefault(explicit) = %v, want %v", got, 5*time.Second)
	}
}

func TestRequestGuideRouteSync_CancelsTargetWhenWaitFails(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requests := make(chan *guide.RouteRequest, 1)
	actions := make(chan *guide.ActionRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok && req != nil {
			select {
			case requests <- req:
			default:
			}
			return nil
		}
		if action, ok := msg.GetActionRequest(); ok && action != nil {
			select {
			case actions <- action:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = RequestGuideRouteSync(ctx, GuideRouteSyncRequest{
		Bus:               bus,
		ResponseTopic:     guide.TopicResponses("tui", "tui"),
		InactivityTimeout: 20 * time.Millisecond,
		Request: &guide.RouteRequest{
			SourceAgentID:  "tui",
			TargetAgentID:  "academic",
			ExplicitTarget: true,
			Input:          "research",
		},
	})
	if err == nil {
		t.Fatal("expected inactivity timeout")
	}

	select {
	case req := <-requests:
		select {
		case action := <-actions:
			if action.CorrelationID != req.CorrelationID {
				t.Fatalf("cancel correlation_id = %q, want %q", action.CorrelationID, req.CorrelationID)
			}
			if action.TargetAgentID != "academic" {
				t.Fatalf("cancel target_agent_id = %q, want academic", action.TargetAgentID)
			}
			if action.Action != "cancel" {
				t.Fatalf("cancel action = %q, want cancel", action.Action)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for cancel action")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for route request")
	}
}
