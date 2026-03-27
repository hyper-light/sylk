package architect

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
)

func TestPublishDeclaration_NestsArchivalistStoreUnderParentTurn(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a := &Architect{
		id:      "architect",
		bus:     bus,
		running: true,
	}

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
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	var events []shared.ToolCallEvent
	ctx := shared.WithStreamContext(context.Background(), "corr-parent", "tui")
	ctx = shared.WithToolCallEmitter(ctx, func(ev shared.ToolCallEvent) {
		events = append(events, ev)
	})

	a.publishDeclaration(ctx, &PreDelegationDeclaration{ID: "decl-1"}, "sess-1")

	select {
	case req := <-requests:
		if req.ParentCorrelationID != "corr-parent" {
			t.Fatalf("parent_correlation_id = %q, want corr-parent", req.ParentCorrelationID)
		}
		if got := req.Input; !strings.HasPrefix(got, "@archivalist:store:history") {
			t.Fatalf("input = %q, want archivalist store DSL", got)
		}
		if got, _ := req.Metadata["chat_nested_branch"].(bool); !got {
			t.Fatalf("chat_nested_branch = %#v, want true", req.Metadata["chat_nested_branch"])
		}
		if got, _ := req.Metadata["chat_inter_agent_kind"].(string); got != shared.InterAgentToolEventKindStore {
			t.Fatalf("chat_inter_agent_kind = %q, want %q", got, shared.InterAgentToolEventKindStore)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for archivalist store request")
	}

	if len(events) != 2 {
		t.Fatalf("emitted events = %d, want 2", len(events))
	}
	if events[0].InterAgent == nil || events[0].InterAgent.Kind != shared.InterAgentToolEventKindStore {
		t.Fatalf("start inter-agent metadata = %+v, want store", events[0].InterAgent)
	}
	if events[1].InterAgent == nil || events[1].InterAgent.Status != shared.InterAgentToolEventStatusDone {
		t.Fatalf("complete inter-agent metadata = %+v, want done", events[1].InterAgent)
	}
}
