package claims

import (
	"context"
	"testing"
)

func TestTestamentAccumulatorFlushBlockingSubmitsBeforeReturn(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{
		BoardID:   "board-acc-flush-blocking",
		SessionID: "sess-acc-flush-blocking",
		TaskID:    "task-acc-flush-blocking",
	})
	_ = board.Projection()

	acc := NewTestamentAccumulator("architect", "sess-acc-flush-blocking")
	acc.RecordResponseText("Plan ready.")
	if err := acc.FlushBlocking(context.Background(), board); err != nil {
		t.Fatalf("FlushBlocking returned error: %v", err)
	}

	proj := board.Projection()
	if proj.TotalTestaments != 1 {
		t.Fatalf("TotalTestaments = %d, want 1", proj.TotalTestaments)
	}
	if proj.TotalArtifacts != 2 {
		t.Fatalf("TotalArtifacts = %d, want response_text + duration", proj.TotalArtifacts)
	}
	if got := proj.Testaments[0].Summary; got != "Plan ready." {
		t.Fatalf("Summary = %q, want response text", got)
	}
	var response *Artifact
	for _, artifact := range proj.Testaments[0].Artifacts {
		if artifact != nil && artifact.Kind == ArtifactKindResponseText {
			response = artifact
			break
		}
	}
	if response == nil {
		t.Fatal("response_text artifact missing")
	}
	if !PresentationMatches(response.Presentation, string(PresentationAudienceUser), string(PresentationSurfaceChat)) {
		t.Fatalf("response_text missing default user/chat presentation: %+v", response.Presentation)
	}
	if response.Presentation.Placement != PresentationPlacementAfterResponse || response.Presentation.Format != PresentationFormatMarkdown {
		t.Fatalf("response_text default presentation = %+v", response.Presentation)
	}
}

func TestTestamentAccumulatorClaimIDDoesNotCreateClaimRelation(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{
		BoardID:   "board-acc-routing-only",
		SessionID: "sess-acc-routing-only",
		TaskID:    "task-acc-routing-only",
	})
	if err := board.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{{
		ID:         "claim-routing",
		Title:      "routing only",
		ActionType: ActionTypeTask,
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			Type: ValidationTypeInspection, Required: true, Description: "review", QualityBar: "ok", Status: ValidationStatusPending,
		}},
	}}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}

	acc := NewTestamentAccumulator("librarian", "sess-acc-routing-only").WithClaimID("claim-routing")
	acc.RecordResponseText("Observed, but not a directed response.")
	if err := acc.FlushBlocking(context.Background(), board); err != nil {
		t.Fatalf("FlushBlocking: %v", err)
	}

	proj := board.Projection()
	if len(proj.Testaments) != 1 {
		t.Fatalf("testaments = %d, want 1", len(proj.Testaments))
	}
	if HasRelation(proj.Testaments[0].Relations, RelationshipClaim, "claim-routing") {
		t.Fatalf("routing-only accumulator stamped RelationshipClaim: %+v", proj.Testaments[0].Relations)
	}
	claim, ok := board.CloneClaim("claim-routing")
	if !ok {
		t.Fatal("claim missing")
	}
	if claim.Status != ClaimStatusPending {
		t.Fatalf("claim status = %q, want pending", claim.Status)
	}
}

func TestTestamentAccumulatorResponseClaimIDCreatesClaimRelation(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{
		BoardID:   "board-acc-response",
		SessionID: "sess-acc-response",
		TaskID:    "task-acc-response",
	})
	if err := board.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{{
		ID:         "claim-response",
		Title:      "consult librarian",
		ActionType: ActionTypeConsultation,
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			Type: ValidationTypeInspection, Required: true, Description: "response received", QualityBar: "ok", Status: ValidationStatusPending,
		}},
	}}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}

	acc := NewTestamentAccumulator("librarian", "sess-acc-response").
		WithClaimID("claim-response").
		WithResponseClaimID("claim-response")
	acc.RecordResponseText("No Python package metadata exists.")
	if err := acc.FlushBlocking(context.Background(), board); err != nil {
		t.Fatalf("FlushBlocking: %v", err)
	}

	proj := board.Projection()
	if len(proj.Testaments) != 1 {
		t.Fatalf("testaments = %d, want 1", len(proj.Testaments))
	}
	if !HasRelation(proj.Testaments[0].Relations, RelationshipClaim, "claim-response") {
		t.Fatalf("response accumulator missing RelationshipClaim: %+v", proj.Testaments[0].Relations)
	}
	claim, ok := board.CloneClaim("claim-response")
	if !ok {
		t.Fatal("claim missing")
	}
	if claim.Status != ClaimStatusTestified {
		t.Fatalf("claim status = %q, want testified", claim.Status)
	}
}
