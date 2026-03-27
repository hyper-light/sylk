package global

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	inspectorshared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func TestWaitForPendingResponse_UsesInactivityTimeout(t *testing.T) {
	wait := &pendingWait{
		response: make(chan *guide.Message, 1),
		activity: make(chan struct{}, 1),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := waitForPendingResponse(ctx, `consultation to "academic"`, 40*time.Millisecond, wait)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	wait.activity <- struct{}{}
	time.Sleep(20 * time.Millisecond)
	wait.activity <- struct{}{}
	time.Sleep(20 * time.Millisecond)
	wait.response <- &guide.Message{Type: guide.MessageTypeResponse, CorrelationID: "corr-1"}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitForPendingResponse() error = %v, want nil", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for response")
	}
}

func TestWaitForPendingResponse_TimesOutAfterSilence(t *testing.T) {
	wait := &pendingWait{
		response: make(chan *guide.Message, 1),
		activity: make(chan struct{}, 1),
	}

	_, err := waitForPendingResponse(context.Background(), `consultation to "academic"`, 15*time.Millisecond, wait)
	if err == nil {
		t.Fatal("expected inactivity timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), `consultation to "academic"`) {
		t.Fatalf("error = %q, want consultation subject", err)
	}
}

func TestRequestRouteSync_PreservesChildSourceStreamTarget(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	gi := &GlobalInspector{
		id:         "inspector-id",
		bus:        bus,
		channels:   guide.NewAgentChannels("inspector", "inspector-id"),
		pendingBus: make(map[string]*pendingWait),
		config:     inspectorshared.GlobalInspectorConfig{SessionID: "sess-1"},
	}

	respSub, err := bus.SubscribeAsync(gi.channels.Responses, gi.handleBusResponse)
	if err != nil {
		t.Fatalf("subscribe inspector responses: %v", err)
	}
	defer respSub.Unsubscribe()

	requests := make(chan *guide.RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requests <- req:
		default:
		}
		_ = bus.Publish(gi.channels.Responses, &guide.Message{
			ID:            "stream-msg",
			CorrelationID: req.CorrelationID,
			Type:          guide.MessageTypeStream,
			Payload: &guide.StreamResponse{
				CorrelationID:     req.CorrelationID,
				RespondingAgentID: "librarian",
				Event:             &guide.StreamEvent{Type: guide.StreamEventData, Text: "working"},
			},
		})
		return bus.Publish(gi.channels.Responses, guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "librarian",
			Data:              "done",
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	ctx, cancel := context.WithTimeout(agentshared.WithStreamContext(context.Background(), "corr-parent", "orchestrator"), 2*time.Second)
	defer cancel()

	if _, err := gi.requestRouteSync(ctx, "librarian", "check style", nil); err != nil {
		t.Fatalf("requestRouteSync: %v", err)
	}

	select {
	case req := <-requests:
		if req.ParentCorrelationID != "corr-parent" {
			t.Fatalf("parent_correlation_id = %q, want %q", req.ParentCorrelationID, "corr-parent")
		}
		value, ok := req.Metadata["chat_preserve_source_stream_target"].(bool)
		if !ok || !value {
			t.Fatalf("chat_preserve_source_stream_target = %#v, want true", req.Metadata["chat_preserve_source_stream_target"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for routed request")
	}
}

func TestDeliverPendingMessage_UsesNestedChildStreamActivityForParentWait(t *testing.T) {
	gi := &GlobalInspector{
		pendingBus: make(map[string]*pendingWait),
	}
	wait := gi.registerPendingWait("corr-parent")
	defer gi.clearPendingWait("corr-parent")

	gi.deliverPendingMessage(&guide.Message{
		CorrelationID: "corr-child",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID: "corr-child",
			Metadata: map[string]any{
				"chat_nested_branch":         true,
				"chat_parent_correlation_id": "corr-parent",
				"chat_parent_tool_call_key":  "consult-1",
				"chat_inter_agent_kind":      agentshared.InterAgentToolEventKindConsult,
			},
			Event: &guide.StreamEvent{Type: guide.StreamEventProgress},
		},
	})

	select {
	case <-wait.activity:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected parent wait activity from nested child stream")
	}
}
