package claims

import (
	"context"
	"testing"
)

// pullworkTestBoard constructs a board with a NoopDeltaBus and no
// scope — all emissions run synchronously inline.
func pullworkTestBoard(t *testing.T) *ClaimsBoard {
	t.Helper()
	return NewClaimsBoard(ClaimsBoardConfig{
		SessionID:  "sess",
		PipelineID: "pipe",
		TaskID:     "task-1",
	})
}

func TestPullWork_ReturnsNilWithoutBoard(t *testing.T) {
	env := PullWork(PullWorkConfig{AgentID: "eng"})
	if env != nil {
		t.Errorf("expected nil envelope without board, got %+v", env)
	}
}

func TestPullWork_PopulatesDirectedClaims(t *testing.T) {
	board := pullworkTestBoard(t)
	err := board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "Implement feature",
			Relations: []Relation{
				{Related: "eng-a1", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	env := PullWork(PullWorkConfig{
		AgentID:   "eng-a1",
		SessionID: "sess",
		Board:     board,
	})
	if env == nil {
		t.Fatal("expected envelope")
	}
	if len(env.DirectedClaims) != 1 {
		t.Fatalf("expected 1 directed claim, got %d", len(env.DirectedClaims))
	}
	if env.DirectedClaims[0].Title != "Implement feature" {
		t.Errorf("unexpected claim title: %q", env.DirectedClaims[0].Title)
	}
}

func TestPullWork_PopulatesEvaluationQueueForIssuer(t *testing.T) {
	board := pullworkTestBoard(t)
	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "Feature",
			Relations: []Relation{
				{Related: "eng-a1", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
			Validations: []*Validation{{Description: "works", Required: true}},
		}},
	)
	// Find the claim we posted.
	proj := board.Projection()
	if len(proj.Claims) != 1 {
		t.Fatal("expected one claim")
	}
	claimID := proj.Claims[0].ID

	// Submit a testament so the claim becomes testified.
	err := board.SubmitTestaments(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "eng-a1"},
		[]Testament{{
			Summary: "done",
			AgentID: "eng-a1",
			Relations: []Relation{
				{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	env := PullWork(PullWorkConfig{
		AgentID:   "inspector",
		SessionID: "sess",
		Board:     board,
	})
	if len(env.EvaluationQueue) != 1 {
		t.Fatalf("expected 1 evaluation entry, got %d", len(env.EvaluationQueue))
	}
	if env.EvaluationQueue[0].Status != ClaimStatusTestified {
		t.Errorf("expected testified status, got %s", env.EvaluationQueue[0].Status)
	}
}

func TestPullWork_AssemblesWithoutInbox(t *testing.T) {
	// PullWork assembles from board state. The inbox is orthogonal —
	// agents use inbox.Next() for event-driven intake.
	board := pullworkTestBoard(t)
	env := PullWork(PullWorkConfig{
		AgentID:   "eng",
		SessionID: "sess",
		Board:     board,
	})
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.AgentID != "eng" {
		t.Errorf("expected agent_id eng, got %s", env.AgentID)
	}
}

func TestPullWork_ClassifiesInboundVsOutbound(t *testing.T) {
	board := pullworkTestBoard(t)
	// Agent "eng" issues a consultation directed at "designer".
	err := board.PostAction(context.Background(),
		Action{Type: ActionTypeConsultation, AgentID: "eng"},
		[]Claim{{
			Title: "Design guidance",
			Relations: []Relation{
				{Related: "designer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// From eng's perspective, this is outbound_pending.
	envEng := PullWork(PullWorkConfig{AgentID: "eng", SessionID: "sess", Board: board})
	if len(envEng.OutboundPending) != 1 {
		t.Fatalf("expected 1 outbound, got %d", len(envEng.OutboundPending))
	}
	if len(envEng.InboundAsks) != 0 {
		t.Errorf("expected 0 inbound for issuer, got %d", len(envEng.InboundAsks))
	}
	// From designer's perspective, this is inbound_ask AND directed claim.
	envDesigner := PullWork(PullWorkConfig{AgentID: "designer", SessionID: "sess", Board: board})
	if len(envDesigner.InboundAsks) != 1 {
		t.Fatalf("expected 1 inbound ask, got %d", len(envDesigner.InboundAsks))
	}
}
