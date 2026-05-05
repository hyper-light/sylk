package shared

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

// Layer 3: envelope metadata propagation tests.

func TestCycleOptsFromMetadata_Empty(t *testing.T) {
	got := CycleOptsFromMetadata(nil)
	if got != (ForwardedRequestCycleOpts{}) {
		t.Fatalf("nil metadata should yield zero opts, got %+v", got)
	}
	got = CycleOptsFromMetadata(map[string]string{})
	if got != (ForwardedRequestCycleOpts{}) {
		t.Fatalf("empty metadata should yield zero opts, got %+v", got)
	}
}

func TestCycleOptsFromMetadata_AllFields(t *testing.T) {
	meta := map[string]string{
		MetadataKeyParentClaimID:      "parent-1",
		MetadataKeyCycleID:            "cycle-1",
		MetadataKeyHandoffFromClaimID: "predecessor-1",
	}
	got := CycleOptsFromMetadata(meta)
	want := ForwardedRequestCycleOpts{
		ParentClaimID:      "parent-1",
		CycleID:            "cycle-1",
		HandoffFromClaimID: "predecessor-1",
	}
	if got != want {
		t.Fatalf("CycleOptsFromMetadata mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestCycleOptsToMetadata_AllFields(t *testing.T) {
	opts := ForwardedRequestCycleOpts{
		ParentClaimID:      "parent-1",
		CycleID:            "cycle-1",
		HandoffFromClaimID: "predecessor-1",
	}
	meta := CycleOptsToMetadata(nil, opts)
	if meta[MetadataKeyParentClaimID] != "parent-1" {
		t.Fatalf("parent_claim_id = %q, want parent-1", meta[MetadataKeyParentClaimID])
	}
	if meta[MetadataKeyCycleID] != "cycle-1" {
		t.Fatalf("cycle_id = %q, want cycle-1", meta[MetadataKeyCycleID])
	}
	if meta[MetadataKeyHandoffFromClaimID] != "predecessor-1" {
		t.Fatalf("handoff_from_claim_id = %q, want predecessor-1", meta[MetadataKeyHandoffFromClaimID])
	}
}

func TestCycleOptsToMetadata_PreservesExistingKeys(t *testing.T) {
	meta := map[string]string{"unrelated": "preserved"}
	out := CycleOptsToMetadata(meta, ForwardedRequestCycleOpts{ParentClaimID: "p"})
	if out["unrelated"] != "preserved" {
		t.Fatalf("unrelated key was clobbered: got %q", out["unrelated"])
	}
	if out[MetadataKeyParentClaimID] != "p" {
		t.Fatalf("parent_claim_id = %q, want p", out[MetadataKeyParentClaimID])
	}
}

func TestCycleOptsToMetadata_EmptyOptsNoMutation(t *testing.T) {
	meta := map[string]string{"existing": "v"}
	out := CycleOptsToMetadata(meta, ForwardedRequestCycleOpts{})
	if len(out) != 1 || out["existing"] != "v" {
		t.Fatalf("empty opts should leave meta unchanged, got %+v", out)
	}
}

// Layer 2: receive-side verification tests.

func TestVerifyHandoffPredecessorDrained_NilBoard(t *testing.T) {
	if err := verifyHandoffPredecessorDrained(nil, "any"); err != nil {
		t.Fatalf("nil board should be a no-op, got %v", err)
	}
}

func TestVerifyHandoffPredecessorDrained_UnknownPredecessor(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:    "test",
		PipelineID: "p",
		TaskID:     "t",
		SessionID:  "s",
	})
	// No predecessor on board → permissive (out-of-order delivery).
	if err := verifyHandoffPredecessorDrained(board, "ghost-claim"); err != nil {
		t.Fatalf("unknown predecessor should be permissive, got %v", err)
	}
}

func TestVerifyHandoffPredecessorDrained_PredecessorOpen_Rejects(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:    "test",
		PipelineID: "p",
		TaskID:     "t",
		SessionID:  "s",
	})
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "test",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "must pass", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction failed: %v", err)
	}
	proj := board.Projection()
	if len(proj.Claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(proj.Claims))
	}
	predID := proj.Claims[0].ID

	err := verifyHandoffPredecessorDrained(board, predID)
	if err == nil {
		t.Fatal("expected rejection — architect has open issued claim including the predecessor itself")
	}
	var nee *claims.HandoffNotEligibleError
	if !errors.As(err, &nee) {
		t.Fatalf("expected *HandoffNotEligibleError, got %T", err)
	}
}

// Layer 3 + intake: BeginForwardedRequestAccumulatorWithOpts honors
// envelope-supplied parent claim and rejects on handoff verification
// failure.

func TestBeginForwardedRequestAccumulatorWithOpts_ParentFromOpts(t *testing.T) {
	ctx, flush, err := BeginForwardedRequestAccumulatorWithOpts(
		context.Background(),
		"engineer", "ses-1", "corr-1", nil,
		ForwardedRequestCycleOpts{ParentClaimID: "envelope-parent"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer flush()
	if got := claims.ParentClaimIDFromContext(ctx); got != "envelope-parent" {
		t.Fatalf("ParentClaimIDFromContext = %q, want envelope-parent (envelope must override route lookup)", got)
	}
}

func TestBeginForwardedRequestAccumulatorWithOpts_HandoffRejection(t *testing.T) {
	// Set up a board where the predecessor's owner has open work, so
	// HandoffEligible will reject.
	registry := claims.DefaultSessionBoardRegistry()
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:    "test-board-handoff",
		PipelineID: "p",
		TaskID:     "t",
		SessionID:  "ses-handoff",
	})
	if err := registry.Register("ses-handoff", board); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	t.Cleanup(func() { registry.Remove("ses-handoff") })

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "open work",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("seed PostAction failed: %v", err)
	}
	predID := board.Projection().Claims[0].ID

	_, _, err := BeginForwardedRequestAccumulatorWithOpts(
		context.Background(),
		"successor", "ses-handoff", "corr-handoff", nil,
		ForwardedRequestCycleOpts{HandoffFromClaimID: predID},
	)
	if err == nil {
		t.Fatal("expected handoff rejection (predecessor still has open work)")
	}
	if !strings.Contains(err.Error(), "handoff_rejected_by_receiver") {
		t.Fatalf("error should be tagged handoff_rejected_by_receiver: %v", err)
	}
}
