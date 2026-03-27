package shared

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestAutomaticHandoffEnabled_DefaultsTrue(t *testing.T) {
	if !AutomaticHandoffEnabled(context.Background()) {
		t.Fatal("expected automatic handoff to default to enabled")
	}
}

func TestAutomaticHandoffEnabled_UsesContextOverride(t *testing.T) {
	ctx := WithAutomaticHandoffEnabled(context.Background(), false)
	if AutomaticHandoffEnabled(ctx) {
		t.Fatal("expected context override to disable automatic handoff")
	}
}

func TestAutomaticHandoffAllowedForForwardedRequest_DisablesNestedChildBranches(t *testing.T) {
	fwd := &guide.ForwardedRequest{
		CorrelationID: "corr-child",
		Metadata: map[string]any{
			streamMetadataParentCorrelation: "corr-parent",
			streamMetadataInterAgentKind:    InterAgentToolEventKindConsult,
			streamMetadataParentToolCallKey: "consult_librarian_1",
		},
	}

	if AutomaticHandoffAllowedForForwardedRequest(fwd) {
		t.Fatalf("expected nested child forwarded request to disable automatic handoff: %+v", fwd.Metadata)
	}
}

func TestAutomaticHandoffAllowedForForwardedRequest_AllowsTopLevelRequests(t *testing.T) {
	fwd := &guide.ForwardedRequest{
		CorrelationID: "corr-top-level",
		Metadata: map[string]any{
			"agent_type": "academic",
		},
	}

	if !AutomaticHandoffAllowedForForwardedRequest(fwd) {
		t.Fatalf("expected top-level request to keep automatic handoff enabled: %+v", fwd.Metadata)
	}
}
