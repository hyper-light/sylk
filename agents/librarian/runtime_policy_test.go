package librarian

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestBuildLLMRequestUsesForwardedSessionMetadata(t *testing.T) {
	l, err := New(Config{Factory: newTestFactory(t), ID: "librarian", SessionID: "sess-config", EnableLLM: true}, nil)
	if err != nil {
		t.Fatalf("new librarian: %v", err)
	}

	req := l.buildLLMRequest(&guide.ForwardedRequest{
		Input:     "find packaging patterns",
		SessionID: "sess-live",
	})

	if got := req.Metadata["agent_id"]; got != "librarian" {
		t.Fatalf("Metadata[agent_id] = %#v, want librarian", got)
	}
	if got := req.Metadata["session_id"]; got != "sess-live" {
		t.Fatalf("Metadata[session_id] = %#v, want sess-live", got)
	}
}
