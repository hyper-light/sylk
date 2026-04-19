package bridge

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	uimsg "github.com/adalundhe/sylk/ui/msg"
)

// TestActivityBridge_StopIsIdempotent locks the UI-12 invariant that Stop
// can be called multiple times without a panic. Before UI-12, sync.Once
// served this role; after, the atomic stopped flag does.
func TestActivityBridge_StopIsIdempotent(t *testing.T) {
	b := NewActivityBridge("test", nil, nil)
	// Call Stop twice. The second must be a no-op; if the atomic-guard
	// regressed to a naive close(done), the double-close would panic.
	b.Stop()
	b.Stop()
}

func TestActivityBridgeForwardHandler_CanonicalizesReplicaIdentityForUI(t *testing.T) {
	b := NewActivityBridge("test", nil, nil)
	program := &recordingProgram{}
	handler := b.forwardHandler(program)

	evt := events.NewActivityEvent(events.EventTypeAgentAction, "default", "working")
	evt.AgentID = "librarian#replica-corr-1"
	evt.Data["agent_type"] = "librarian"
	evt.Data["agent_name"] = "Librarian"
	evt.Data["runtime_agent_id"] = "librarian#replica-corr-1"
	evt.Data["canonical_agent_id"] = "librarian"

	if err := handler(guide.NewActivityMessage("librarian#replica-corr-1", evt)); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(program.messages) != 1 {
		t.Fatalf("sent %d messages, want 1", len(program.messages))
	}

	forwarded, ok := program.messages[0].(uimsg.ActivityEventMsg)
	if !ok {
		t.Fatalf("sent message type %T, want uimsg.ActivityEventMsg", program.messages[0])
	}
	if forwarded.Event == nil {
		t.Fatal("expected forwarded event")
	}
	if forwarded.Event.AgentID != "librarian" {
		t.Fatalf("forwarded Event.AgentID = %q, want librarian", forwarded.Event.AgentID)
	}
	if got, _ := forwarded.Event.Data["runtime_agent_id"].(string); got != "librarian#replica-corr-1" {
		t.Fatalf("runtime_agent_id = %q, want librarian#replica-corr-1", got)
	}
}
