package shared

import (
	"context"
	"testing"
)

func TestBeginAutoInterAgentRouteBranch_DoesNotSynthesizeWithoutExistingBranch(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-parent", "tui")
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) {
		events = append(events, ev)
	})

	branchCtx, branch := BeginAutoInterAgentRouteBranch(ctx, "guardian", `{"action":"preflight_task","task_id":"task-1"}`, nil)
	metadata := branch.ApplyMetadata(branchCtx, nil)

	if len(metadata) != 0 {
		t.Fatalf("metadata = %#v, want empty", metadata)
	}
	branch.Complete(branchCtx, "ignored", "", nil)
	if len(events) != 0 {
		t.Fatalf("emitted events = %d, want 0", len(events))
	}
}

func TestBeginAutoInterAgentRouteBranch_ReusesExplicitBranchMetadata(t *testing.T) {
	var events []ToolCallEvent
	ctx := WithStreamContext(context.Background(), "corr-parent", "tui")
	ctx = WithToolCallEmitter(ctx, func(ev ToolCallEvent) {
		events = append(events, ev)
	})

	existing := map[string]any{
		streamMetadataNestedBranch:      true,
		streamMetadataParentCorrelation: "corr-root",
		streamMetadataParentToolCallKey: "challenge-root",
		streamMetadataInterAgentThread:  "pipeline:challenge-1",
		streamMetadataInterAgentKind:    InterAgentToolEventKindChallenge,
	}

	branchCtx, branch := BeginAutoInterAgentRouteBranch(ctx, "tester-pipeline", "ignored", existing)
	metadata := branch.ApplyMetadata(branchCtx, nil)

	if got, _ := metadata[streamMetadataParentCorrelation].(string); got != "corr-root" {
		t.Fatalf("parent correlation = %q, want %q", got, "corr-root")
	}
	if got, _ := metadata[streamMetadataParentToolCallKey].(string); got != "challenge-root" {
		t.Fatalf("parent tool call key = %q, want %q", got, "challenge-root")
	}
	if got, _ := metadata[streamMetadataInterAgentThread].(string); got != "pipeline:challenge-1" {
		t.Fatalf("thread key = %q, want %q", got, "pipeline:challenge-1")
	}

	branch.Complete(branchCtx, "done", "", nil)
	if len(events) != 0 {
		t.Fatalf("emitted events = %d, want 0 for reused branch metadata", len(events))
	}
}

func TestBeginAutoInterAgentRouteBranch_ReusesInheritedStreamBranchMetadata(t *testing.T) {
	existing := map[string]any{
		streamMetadataNestedBranch:      true,
		streamMetadataParentCorrelation: "corr-root",
		streamMetadataParentToolCallKey: "consult-root",
		streamMetadataInterAgentThread:  "global_review:challenge-1",
		streamMetadataInterAgentKind:    InterAgentToolEventKindConsult,
	}
	ctx := WithForwardedStreamContext(context.Background(), "corr-child", "librarian", "corr-root", existing)

	branchCtx, branch := BeginAutoInterAgentRouteBranch(ctx, "academic", "ignored", nil)
	metadata := branch.ApplyMetadata(branchCtx, nil)

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
