package shared

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestInheritedBranchMetadata_ReturnsOriginalMetadataWithoutBranch(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-parent", "tui")
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error {
		events = append(events, ev)
		return nil
	})

	metadata := InheritedBranchMetadata(ctx, nil)

	if len(metadata) != 0 {
		t.Fatalf("metadata = %#v, want empty", metadata)
	}
	if len(events) != 0 {
		t.Fatalf("emitted events = %d, want 0 (InheritedBranchMetadata must never emit)", len(events))
	}
}

func TestInheritedBranchMetadata_PropagatesExplicitBranchMetadata(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-parent", "tui")
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error {
		events = append(events, ev)
		return nil
	})

	existing := map[string]any{
		streamMetadataNestedBranch:      true,
		streamMetadataParentCorrelation: "corr-root",
		streamMetadataParentToolCallKey: "challenge-root",
		streamMetadataInterAgentThread:  "pipeline:challenge-1",
		streamMetadataInterAgentKind:    InterAgentToolEventKindChallenge,
	}

	metadata := InheritedBranchMetadata(ctx, existing)

	if got, _ := metadata[streamMetadataParentCorrelation].(string); got != "corr-root" {
		t.Fatalf("parent correlation = %q, want %q", got, "corr-root")
	}
	if got, _ := metadata[streamMetadataParentToolCallKey].(string); got != "challenge-root" {
		t.Fatalf("parent tool call key = %q, want %q", got, "challenge-root")
	}
	if got, _ := metadata[streamMetadataInterAgentThread].(string); got != "pipeline:challenge-1" {
		t.Fatalf("thread key = %q, want %q", got, "pipeline:challenge-1")
	}
	if len(events) != 0 {
		t.Fatalf("emitted events = %d, want 0 for metadata-only propagation", len(events))
	}
}

func TestInheritedBranchMetadata_PropagatesInheritedStreamBranch(t *testing.T) {
	existing := map[string]any{
		streamMetadataNestedBranch:      true,
		streamMetadataParentCorrelation: "corr-root",
		streamMetadataParentToolCallKey: "consult-root",
		streamMetadataInterAgentThread:  "global_review:challenge-1",
		streamMetadataInterAgentKind:    InterAgentToolEventKindConsult,
	}
	ctx := WithForwardedStreamContext(context.Background(), "corr-child", "librarian", "corr-root", existing)

	metadata := InheritedBranchMetadata(ctx, nil)

	if got, _ := metadata[streamMetadataParentCorrelation].(string); got != "corr-root" {
		t.Fatalf("parent correlation = %q, want %q", got, "corr-root")
	}
	if got, _ := metadata[streamMetadataParentToolCallKey].(string); got != "consult-root" {
		t.Fatalf("parent tool call key = %q, want %q", got, "consult-root")
	}
	if got, _ := metadata[streamMetadataInterAgentKind].(string); got != InterAgentToolEventKindConsult {
		t.Fatalf("kind = %q, want %q", got, InterAgentToolEventKindConsult)
	}
}

func TestRouteResponseSummary_GuardianPayloadPrefersHumanMessage(t *testing.T) {
	summary := routeResponseSummary(map[string]any{
		"target": "guardian",
		"data": map[string]any{
			"user_message": "Proceed with caution around infra changes.",
			"reason":       "guardian-approved deterministic control-plane grant",
		},
	})
	if summary != "Proceed with caution around infra changes." {
		t.Fatalf("route response summary = %q", summary)
	}
}

func TestInterAgentBranchCompleteFromMessage_TreatsTerminalGuideErrorAsFailure(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-parent", "inspector")
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) error {
		events = append(events, ev)
		return nil
	})

	branchCtx, branch := BeginInterAgentBranch(ctx, InterAgentBranchSpec{
		Kind:       InterAgentToolEventKindConsult,
		ToolName:   "consult_academic",
		AgentTypes: []string{"academic"},
		Summary:    "Assess whether the current approach is sound.",
	})
	branch.CompleteFromMessage(branchCtx, guide.NewErrorMessage("err-1", "corr-parent", "academic", "academic consultation failed: provider unavailable"), nil)

	if len(events) != 0 {
		t.Fatalf("consult branch emitted %d events; consult rows must be projected from claims deltas", len(events))
	}
}
