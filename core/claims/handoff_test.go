package claims

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// HandoffEligible contract tests — UI_DESIGN.md §4.3 + §4.4 + §7 P2.1.

func handoffBoard() *ClaimsBoard {
	return NewClaimsBoard(ClaimsBoardConfig{
		BoardID:    "handoff-board",
		PipelineID: "pipe-1",
		TaskID:     "task-1",
		SessionID:  "ses-1",
	})
}

func issuerClaim(issuer, subject string) Claim {
	return Claim{
		Title: "test claim",
		Relations: []Relation{
			{Related: issuer, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: subject, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{Description: "v", QualityBar: "must pass", Type: ValidationTypeInspection, Required: true}},
	}
}

func TestHandoffEligible_NoOpenWork_AllowsHandoff(t *testing.T) {
	b := handoffBoard()
	if err := HandoffEligible(b, "architect"); err != nil {
		t.Fatalf("HandoffEligible(empty board) = %v, want nil", err)
	}
}

func TestHandoffEligible_RejectsOnOpenIssuedClaim(t *testing.T) {
	b := handoffBoard()
	if err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{issuerClaim("architect", "engineer")}); err != nil {
		t.Fatalf("PostAction failed: %v", err)
	}
	err := HandoffEligible(b, "architect")
	if err == nil {
		t.Fatal("expected rejection for open issued claim")
	}
	var nee *HandoffNotEligibleError
	if !errors.As(err, &nee) {
		t.Fatalf("error is not *HandoffNotEligibleError: %T", err)
	}
	if len(nee.OpenChildClaims) != 1 {
		t.Fatalf("OpenChildClaims = %v, want 1 entry", nee.OpenChildClaims)
	}
	if !strings.Contains(nee.Reason, "open child claim") {
		t.Fatalf("Reason missing description: %q", nee.Reason)
	}
}

func TestHandoffEligible_RejectsOnOpenSubjectFromPeer(t *testing.T) {
	b := handoffBoard()
	if err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{issuerClaim("architect", "engineer")}); err != nil {
		t.Fatalf("PostAction failed: %v", err)
	}
	err := HandoffEligible(b, "engineer")
	if err == nil {
		t.Fatal("expected rejection for engineer being subject of an open peer claim")
	}
	var nee *HandoffNotEligibleError
	if !errors.As(err, &nee) {
		t.Fatalf("error is not *HandoffNotEligibleError: %T", err)
	}
	if len(nee.OpenAsSubjectFromIssuers) != 1 {
		t.Fatalf("OpenAsSubjectFromIssuers = %v, want 1 entry", nee.OpenAsSubjectFromIssuers)
	}
}

func TestHandoffEligible_AllowsOpenSubjectFromSelf(t *testing.T) {
	b := handoffBoard()
	// Self-issued claim where the agent is also subject — does not
	// make the agent a "child of a peer" so #2 must NOT fire. (#1
	// still fires because the agent has open work it issued, which
	// is the conservative-correct policy: don't handoff with open
	// work, including self-issued.)
	if err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{issuerClaim("architect", "architect")}); err != nil {
		t.Fatalf("PostAction failed: %v", err)
	}
	err := HandoffEligible(b, "architect")
	if err == nil {
		t.Fatal("expected rejection (self-claim still counts as open child work)")
	}
	var nee *HandoffNotEligibleError
	if !errors.As(err, &nee) {
		t.Fatalf("error is not *HandoffNotEligibleError: %T", err)
	}
	if len(nee.OpenAsSubjectFromIssuers) != 0 {
		t.Fatalf("OpenAsSubjectFromIssuers should be empty for self-issued claim, got %v", nee.OpenAsSubjectFromIssuers)
	}
	if len(nee.OpenChildClaims) != 1 {
		t.Fatalf("OpenChildClaims should contain the self-issued claim, got %v", nee.OpenChildClaims)
	}
}

func TestHandoffEligible_AllowsAfterClaimAccepted(t *testing.T) {
	b := handoffBoard()
	c := issuerClaim("architect", "engineer")
	if err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{c}); err != nil {
		t.Fatalf("PostAction failed: %v", err)
	}
	// Architect should now be blocked.
	if err := HandoffEligible(b, "architect"); err == nil {
		t.Fatal("expected initial rejection")
	}
	// Close the claim by setting status manually via projection mutation
	// is not feasible; instead, reject the claim through the public path.
	// RejectClaim moves to terminal status without requiring testaments.
	proj := b.Projection()
	if len(proj.Claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(proj.Claims))
	}
	claimID := proj.Claims[0].ID
	if err := b.RejectClaim(context.Background(), claimID, StatusChange{From: string(proj.Claims[0].Status), To: string(ClaimStatusRejected), Reason: "test", AgentID: "architect"}, nil, nil); err != nil {
		t.Fatalf("RejectClaim failed: %v", err)
	}
	if err := HandoffEligible(b, "architect"); err != nil {
		t.Fatalf("HandoffEligible after reject = %v, want nil", err)
	}
}

func TestHandoffEligible_NilBoard_ReturnsError(t *testing.T) {
	if err := HandoffEligible(nil, "agent"); err == nil {
		t.Fatal("expected error for nil board")
	}
}

func TestHandoffEligible_EmptyAgentID_ReturnsError(t *testing.T) {
	if err := HandoffEligible(handoffBoard(), ""); err == nil {
		t.Fatal("expected error for empty agent ID")
	}
	if err := HandoffEligible(handoffBoard(), "   "); err == nil {
		t.Fatal("expected error for whitespace-only agent ID")
	}
}

func TestHandoffEligible_ConcurrentChecksConsistent(t *testing.T) {
	// Race-test: many concurrent checks over a stable board snapshot
	// must all see the same answer (each call's projection is its own
	// snapshot). Run under -race.
	b := handoffBoard()
	if err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{issuerClaim("architect", "engineer")}); err != nil {
		t.Fatalf("PostAction failed: %v", err)
	}
	const N = 32
	results := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			results <- HandoffEligible(b, "architect")
		}()
	}
	for i := 0; i < N; i++ {
		err := <-results
		if err == nil {
			t.Fatalf("concurrent check returned nil, expected rejection")
		}
	}
}

// Board-level guard tests — UI_DESIGN.md §7 P2.3.

func TestBoard_PostAction_RejectsHandoffWhenIneligible(t *testing.T) {
	b := handoffBoard()
	// Architect has open issued work — handoff must be rejected by the
	// board even if a (hypothetical malicious) caller bypasses the
	// skill-side check.
	if err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{issuerClaim("architect", "engineer")}); err != nil {
		t.Fatalf("seed PostAction failed: %v", err)
	}
	handoffClaim := Claim{
		Title: "handoff to engineer",
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{Description: "v", QualityBar: "must pass", Type: ValidationTypeInspection, Required: true}},
	}
	err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeHandoff}, []Claim{handoffClaim})
	if err == nil {
		t.Fatal("expected board to reject handoff while architect has open child work")
	}
	var nee *HandoffNotEligibleError
	if !errors.As(err, &nee) {
		t.Fatalf("error not *HandoffNotEligibleError: %T", err)
	}
	// Verify the handoff claim was NOT persisted.
	proj := b.Projection()
	if proj.TotalClaims != 1 {
		t.Fatalf("expected only the seed claim to persist, got %d total claims", proj.TotalClaims)
	}
}

func TestBoard_PostAction_AllowsHandoffWhenEligible(t *testing.T) {
	b := handoffBoard()
	// No prior open work — handoff should be accepted.
	handoffClaim := Claim{
		Title: "fresh handoff",
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{Description: "v", QualityBar: "must pass", Type: ValidationTypeInspection, Required: true}},
	}
	if err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeHandoff}, []Claim{handoffClaim}); err != nil {
		t.Fatalf("PostAction handoff failed: %v", err)
	}
	if proj := b.Projection(); proj.TotalClaims != 1 {
		t.Fatalf("expected 1 claim after successful handoff, got %d", proj.TotalClaims)
	}
}

// Ensure the typed error survives errors.As / errors.Is contracts.
func TestHandoffNotEligibleError_AsRoundTrip(t *testing.T) {
	b := handoffBoard()
	if err := b.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{issuerClaim("architect", "engineer")}); err != nil {
		t.Fatalf("PostAction failed: %v", err)
	}
	err := HandoffEligible(b, "architect")
	var nee *HandoffNotEligibleError
	if !errors.As(err, &nee) {
		t.Fatalf("errors.As failed for %T", err)
	}
	if nee.AgentID != "architect" {
		t.Fatalf("AgentID = %q, want architect", nee.AgentID)
	}
}
