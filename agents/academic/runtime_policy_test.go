package academic

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/versioning"
)

func TestApplyLLMRuntimeProfileUsesContextSessionMetadata(t *testing.T) {
	a, err := New(Config{ID: "academic", SessionID: "sess-config"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Model:     "gpt-5.4-pro",
		MaxTokens: 2048,
	}
	ctx := versioning.WithSessionID(context.Background(), "sess-live")

	a.applyLLMRuntimeProfile(ctx, req, "conversation")

	if got := req.Metadata["agent_id"]; got != "academic" {
		t.Fatalf("Metadata[agent_id] = %#v, want academic", got)
	}
	if got := req.Metadata["session_id"]; got != "sess-live" {
		t.Fatalf("Metadata[session_id] = %#v, want sess-live", got)
	}
}
