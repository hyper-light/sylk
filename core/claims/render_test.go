package claims

import (
	"context"
	"strings"
	"testing"
)

func TestRenderBoardPreambleEmpty(t *testing.T) {
	result := RenderBoardPreamble(nil, "engineer")
	if result != "" {
		t.Errorf("nil projection should return empty, got %q", result)
	}

	result = RenderBoardPreamble(&ClaimsBoardProjection{}, "engineer")
	if result == "" {
		t.Error("empty projection should still render phase header")
	}
}

func TestRenderBoardPreambleShowsMyClaims(t *testing.T) {
	proj := &ClaimsBoardProjection{
		Phase:       BoardPhaseImplementation,
		Iteration:   0,
		TotalClaims: 2,
		Claims: []Claim{
			{
				ID: "c1", Title: "Implement HS256", Status: ClaimStatusInProgress,
				Relations: []Relation{
					{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				},
				Scope: []ClaimScopeEntry{{Kind: "file", Key: "services/auth/jwk.go"}},
				Validations: []*Validation{
					{ID: "v1", Status: ValidationStatusPending, Required: true},
				},
			},
			{
				ID: "c2", Title: "Design login page", Status: ClaimStatusPending,
				Relations: []Relation{
					{Related: "designer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				},
			},
		},
		TotalValidations: 1,
	}

	result := RenderBoardPreamble(proj, "engineer")

	if !strings.Contains(result, "My Claims") {
		t.Error("should contain My Claims section")
	}
	if !strings.Contains(result, "Implement HS256") {
		t.Error("should contain engineer's claim title")
	}
	if !strings.Contains(result, "Peer Progress") {
		t.Error("should contain Peer Progress section")
	}
	if !strings.Contains(result, "designer") {
		t.Error("should show designer in peer progress")
	}
	if !strings.Contains(result, "submit_testaments") {
		t.Error("should contain implementation phase guidance")
	}
}

func TestPrependBoardPreambleNilBoard(t *testing.T) {
	msg := "original message"
	result := PrependBoardPreamble(msg, nil, "engineer")
	if result != msg {
		t.Errorf("nil board should return original message, got %q", result)
	}
}

func TestPrependBoardPreamblePrepends(t *testing.T) {
	b := NewClaimsBoard(ClaimsBoardConfig{BoardID: "b1", TaskID: "t1"})
	b.PostAction(nil, Action{AgentID: "a", Type: ActionTypeTask}, []Claim{
		{Title: "test claim", Relations: []Relation{
			{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		}},
	})

	result := PrependBoardPreamble("do the work", b, "engineer")
	if !strings.HasPrefix(result, "## Claims Board") {
		t.Error("should prepend claims board header")
	}
	if !strings.HasSuffix(result, "do the work") {
		t.Error("should end with original message")
	}
}

func TestRenderBoardPreambleShowsCarryForwardContinuity(t *testing.T) {
	board := carryForwardTestBoard()
	submitCarrySource(t, board, "librarian", "CLI context", []*Artifact{
		{AgentID: "librarian", Kind: "workspace_read", Reference: "Existing Click CLI evidence."},
	})
	result, err := CarryForward(context.Background(), board, CarryForwardOptions{
		AgentID: "architect",
		Topic:   "python cli plan",
	})
	if err != nil {
		t.Fatalf("carry forward failed: %v", err)
	}

	preamble := RenderBoardPreamble(board.Projection(), "architect")
	for _, want := range []string{
		"My Carry-Forward Continuity",
		`recall_forward(topic=...)`,
		`topic "python cli plan"`,
		result.TestamentID,
		"carried testaments/artifacts",
	} {
		if !strings.Contains(preamble, want) {
			t.Fatalf("preamble missing %q:\n%s", want, preamble)
		}
	}
}

func TestHasAgentTypeRelation(t *testing.T) {
	relations := []Relation{
		{Related: "engineer-pipeline-abc", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
	}

	if !hasAgentTypeRelation(relations, "engineer", RelationshipSubject) {
		t.Error("should match engineer prefix")
	}
	if hasAgentTypeRelation(relations, "chief-engineer", RelationshipSubject) {
		t.Error("should not match chief-engineer (not a prefix)")
	}
	if hasAgentTypeRelation(relations, "eng", RelationshipSubject) {
		t.Error("should not match partial prefix 'eng'")
	}
}
