package shared

import (
	"context"
	"testing"
)

func TestPendingRequiredProtocolAction_Pipeline(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{})
	ctx := WithPipelineProtocolState(context.Background(), state)

	if PendingRequiredProtocolAction(ctx) {
		t.Fatal("expected no pending required action before the protocol forces one")
	}

	state.requireTerminalAction(PipelineProtocolActionOT, "handoff required")
	if !PendingRequiredProtocolAction(ctx) {
		t.Fatal("expected pending pipeline required action")
	}
	if got := ExtendRequiredProtocolGrace(ctx, 0); got != RequiredProtocolGraceTurns {
		t.Fatalf("ExtendRequiredProtocolGrace = %d, want %d", got, RequiredProtocolGraceTurns)
	}

	if err := state.setTerminalAction(&PipelineTurnAction{Type: PipelineProtocolActionOT}); err != nil {
		t.Fatalf("setTerminalAction: %v", err)
	}
	if PendingRequiredProtocolAction(ctx) {
		t.Fatal("expected pipeline required action to clear after terminal action")
	}
}

func TestPendingRequiredProtocolAction_GlobalReview(t *testing.T) {
	state := NewGlobalReviewState(&GlobalReviewSnapshot{ReviewID: "review-1"}, nil)
	ctx := WithGlobalReviewState(context.Background(), state)

	state.requireTerminalAction(GlobalReviewActionCommit, "commit required")
	if !PendingRequiredProtocolAction(ctx) {
		t.Fatal("expected pending global-review required action")
	}
	if got := ExtendRequiredProtocolGrace(ctx, 1); got != RequiredProtocolGraceTurns {
		t.Fatalf("ExtendRequiredProtocolGrace = %d, want %d", got, RequiredProtocolGraceTurns)
	}

	if err := state.setTerminalAction(&GlobalReviewTurnAction{Type: GlobalReviewActionCommit}); err != nil {
		t.Fatalf("setTerminalAction: %v", err)
	}
	if PendingRequiredProtocolAction(ctx) {
		t.Fatal("expected global-review required action to clear after terminal action")
	}
}
