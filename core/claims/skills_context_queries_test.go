package claims

import (
	"context"
	"encoding/json"
	"testing"
)

func contextSkillBoard(t *testing.T) *ClaimsBoard {
	t.Helper()
	return NewClaimsBoard(ClaimsBoardConfig{
		SessionID:  "sess",
		PipelineID: "pipe",
		TaskID:     "task",
	})
}

func newContextSkillBoardProvider(board *ClaimsBoard) BoardProvider {
	return func() *ClaimsBoard { return board }
}

func invokeSkill(t *testing.T, s interface {
	Handler() func(context.Context, json.RawMessage) (any, error)
}, params any) any {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Handler()(context.Background(), data)
	if err != nil {
		t.Fatalf("skill handler error: %v", err)
	}
	return result
}

// Since skills.Skill's Handler field isn't exposed the same way,
// invoke via the builder and test handler function directly. Use a
// helper that locates the handler registered on the skill.
//
// Each Skill registered via NewSkill(...).Handler(fn).Build() keeps
// the handler accessible — in the sylk codebase, Skill exposes a
// getter. We avoid coupling to it by invoking the board's state
// directly where possible, and using the integration tests above
// for end-to-end coverage.

func TestTraceClaimAncestry_WalksSupersedesChain(t *testing.T) {
	board := contextSkillBoard(t)
	// Post initial claim.
	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "v1",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
		}},
	)
	originalID := board.Projection().Claims[0].ID

	// Reject with replacement (which carries supersedes).
	_ = board.RejectClaim(context.Background(), originalID,
		StatusChange{AgentID: "inspector", Reason: "v2 needed"},
		&Action{Type: ActionTypeCorrective, AgentID: "inspector"},
		[]Claim{{
			Title: "v2",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
		}},
	)
	// Find v2's ID.
	var v2ID string
	for _, c := range board.Projection().Claims {
		if c.Title == "v2" {
			v2ID = c.ID
			break
		}
	}
	if v2ID == "" {
		t.Fatal("v2 claim not posted")
	}

	nodes := Traverse(board, v2ID, "supersedes|amends|refines", 100)
	chain := extractClaims(nodes)
	// Traverse returns nodes excluding the start node, so we expect
	// the ancestor (v1) as the only result.
	if len(chain) != 1 {
		t.Fatalf("expected 1 ancestor node, got %d", len(chain))
	}
	if chain[0].Title != "v1" {
		t.Errorf("expected ancestor title v1, got %s", chain[0].Title)
	}
}

func TestListActionClaimsReturnsSiblings(t *testing.T) {
	board := contextSkillBoard(t)
	err := board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{
			{Title: "A", Relations: []Relation{{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject}}},
			{Title: "B", Relations: []Relation{{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject}}},
			{Title: "C", Relations: []Relation{{Related: "designer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject}}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Find the action ID via one claim's claim_action relation.
	claims := board.Projection().Claims
	actionID := ClaimActionID(claims[0].Relations)
	if actionID == "" {
		t.Fatal("no claim_action relation on claim")
	}

	ids := board.ObjectIDsWithRelation(RelatedTypeAction, RelationshipClaimAction, actionID)
	if len(ids) != 3 {
		t.Fatalf("expected 3 sibling claims, got %d", len(ids))
	}
	collected := collectClaimsByIDs(board, ids)
	if len(collected) != 3 {
		t.Fatalf("collected: %d", len(collected))
	}
}

func TestFindOverlappingClaims_UsesScopeIndex(t *testing.T) {
	board := contextSkillBoard(t)
	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{
			{
				Title: "Auth fn",
				Scope: []ClaimScopeEntry{{Kind: "file", Key: "services/auth.go"}},
				Relations: []Relation{{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject}},
			},
			{
				Title: "Auth test",
				Scope: []ClaimScopeEntry{{Kind: "file", Key: "services/auth.go"}, {Kind: "file", Key: "services/auth_test.go"}},
				Relations: []Relation{{Related: "tester", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject}},
			},
		},
	)
	matches := board.ClaimIDsWithScope("file", "services/auth.go")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
}

func TestShowPhaseHistory_LogsTransitions(t *testing.T) {
	board := contextSkillBoard(t)
	_ = board.PostAction(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "inspector"},
		[]Claim{{
			Title: "F",
			Relations: []Relation{
				{Related: "eng", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
				{Related: "inspector", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			},
		}},
	)
	claimID := board.Projection().Claims[0].ID
	_ = board.SubmitTestaments(context.Background(),
		Action{Type: ActionTypeTask, AgentID: "eng"},
		[]Testament{{
			Summary: "done", AgentID: "eng",
			Relations: []Relation{
				{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			},
		}},
	)
	if err := board.TransitionToValidation(context.Background()); err != nil {
		t.Fatal(err)
	}
	history := board.PhaseHistory()
	if len(history) != 1 {
		t.Fatalf("expected 1 phase history entry, got %d", len(history))
	}
	if history[0].To != string(BoardPhaseValidation) {
		t.Errorf("to: %q", history[0].To)
	}
}

func TestAllContextQuerySkills_Registers(t *testing.T) {
	board := contextSkillBoard(t)
	bp := newContextSkillBoardProvider(board)
	all := AllContextQuerySkills(bp)
	if len(all) != 9 {
		t.Fatalf("expected 9 skills (8 named queries + traverse), got %d", len(all))
	}
	// Each must be non-nil and have a distinct name.
	names := make(map[string]struct{})
	for _, s := range all {
		if s == nil {
			t.Fatal("nil skill")
		}
		if _, dup := names[s.Name]; dup {
			t.Errorf("duplicate skill name: %s", s.Name)
		}
		names[s.Name] = struct{}{}
	}
}
