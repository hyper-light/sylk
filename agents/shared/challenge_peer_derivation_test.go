package shared

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	coreskills "github.com/adalundhe/sylk/core/skills"
)

func TestChallengePeerSkill_PostsClaimAndYieldsFromResolvedActivity(t *testing.T) {
	source := &fakeActivitySource{
		activities: map[activity.ActivityID]activity.AgentActivity{
			"target-activity-1": {
				ID: "target-activity-1",
				Actor: activity.Actor{
					// Designer is a legitimate challenge target for the
					// engineer per authority.PermittedChallengeTargets.
					// See docs/COMMS_MATRIX.md.
					AgentID:    "designer-1",
					AgentType:  "designer",
					PipelineID: "pipeline-a",
				},
			},
		},
	}
	prev := activity.SetDefaultSource(source)
	t.Cleanup(func() { activity.SetDefaultSource(prev) })
	sessionID := "sess-challenge-claim-yield"
	board := registerChallengePeerTestBoard(t, sessionID)

	skills := CrossPipelineSkills(CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "engineer-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipeline-b" },
	})

	var challenge *struct {
		Name    string
		Handler func(ctx context.Context, input json.RawMessage) (any, error)
	}
	for _, s := range skills {
		if s.Name == "challenge_peer" {
			challenge = &struct {
				Name    string
				Handler func(ctx context.Context, input json.RawMessage) (any, error)
			}{Name: s.Name, Handler: s.Handler}
			break
		}
	}
	if challenge == nil {
		t.Fatal("challenge_peer skill not found")
	}

	input := json.RawMessage(`{
		"target_activity_id": "target-activity-1",
		"evidence": "Designer's style decision conflicts with a published convention in docs/STYLE.md",
		"alternative": "Adopt convention from docs/STYLE.md instead",
		"deadline_seconds": 300
	}`)
	ctx := contextWithChallengeContinuation(context.Background(), "engineer-1", sessionID, board)
	raw, err := challenge.Handler(ctx, input)
	if err != nil {
		t.Fatalf("challenge_peer handler returned error: %v", err)
	}
	outcome, ok := coreskills.NormalizeToolOutcome(raw)
	if !ok || outcome.Status != coreskills.ToolStatusYielded {
		t.Fatalf("handler result = %#v, want yielded ToolOutcome", raw)
	}
	claimID := firstChallengeContinuationClaimRef(ctx)
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("challenge claim %q missing", claimID)
	}
	if claim.ActionType != claims.ActionTypeChallenge {
		t.Fatalf("claim action type = %q, want challenge", claim.ActionType)
	}
	if !claims.HasRelation(claim.Relations, claims.RelationshipSubject, "designer") {
		t.Fatalf("challenge claim relations = %#v, want designer subject", claim.Relations)
	}
	if !claimScopeContainsChallenge(claim.Scope, "challenge", "designer") {
		t.Fatalf("challenge claim scope = %#v, want designer challenge scope", claim.Scope)
	}
}

func TestChallengePeerSkill_RejectsUnresolvedTarget(t *testing.T) {
	prev := activity.SetDefaultSource(nil)
	t.Cleanup(func() { activity.SetDefaultSource(prev) })

	skills := CrossPipelineSkills(CrossPipelineSkillConfig{
		SessionID: func() string { return "sess-1" },
		AgentID:   func() string { return "engineer-1" },
		AgentType: func() string { return "engineer" },
	})
	var handler func(ctx context.Context, input json.RawMessage) (any, error)
	for _, s := range skills {
		if s.Name == "challenge_peer" {
			handler = s.Handler
			break
		}
	}
	if handler == nil {
		t.Fatal("challenge_peer not found")
	}
	_, err := handler(context.Background(), json.RawMessage(`{
		"target_activity_id": "some-id",
		"evidence": "x"
	}`))
	if err == nil {
		t.Fatal("expected unresolved challenge target to fail")
	}
}

// fakeActivitySource implements activity.Source for the challenge_peer
// derivation test. FilterActivities and LatestActivityID are unused
// here; minimal stubs keep the interface satisfied.
type fakeActivitySource struct {
	activities map[activity.ActivityID]activity.AgentActivity
}

func (f *fakeActivitySource) FilterActivities(context.Context, activity.QueryFilter) ([]activity.AgentActivity, error) {
	return nil, nil
}

func (f *fakeActivitySource) GetActivity(_ context.Context, id activity.ActivityID) (*activity.AgentActivity, error) {
	if a, ok := f.activities[id]; ok {
		return &a, nil
	}
	return nil, nil
}

func (f *fakeActivitySource) LatestActivityID(context.Context, activity.QueryFilter) (activity.ActivityID, error) {
	return "", nil
}

func registerChallengePeerTestBoard(t *testing.T, sessionID string) *claims.ClaimsBoard {
	t.Helper()
	registry := claims.DefaultSessionBoardRegistry()
	registry.Remove(sessionID)
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "board-" + sessionID, SessionID: sessionID, TaskID: "task-" + sessionID})
	if err := registry.Register(sessionID, board); err != nil {
		t.Fatalf("register board: %v", err)
	}
	t.Cleanup(func() { registry.Remove(sessionID) })
	return board
}

func contextWithChallengeContinuation(ctx context.Context, agentID, sessionID string, board *claims.ClaimsBoard) context.Context {
	ctx = WithContinuationStore(ctx, NewContinuationStore(ContinuationStoreConfig{
		AgentID:   agentID,
		SessionID: sessionID,
		Board:     board,
		ResumeFn: func(context.Context, *TurnSnapshot, map[string]*AwaitedClaimResult) error {
			return nil
		},
	}))
	return WithTurnContext(ctx, &TurnContext{
		Request:       &providers.Request{},
		CorrelationID: "corr-" + sessionID,
		AgentID:       agentID,
		SessionID:     sessionID,
	})
}

func firstChallengeContinuationClaimRef(ctx context.Context) string {
	store := ContinuationStoreFromContext(ctx)
	if store == nil {
		return ""
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for id := range store.claimIndex {
		return id
	}
	return ""
}

func claimScopeContainsChallenge(scope []claims.ClaimScopeEntry, kind, key string) bool {
	for _, entry := range scope {
		if entry.Kind == kind && entry.Key == key {
			return true
		}
	}
	return false
}

// Sanity on build: make sure the package-local helper we're depending
// on still normalizes "librarian" as we expect.
func TestChallengePeerDerivation_NormalizesKnownAgentType(t *testing.T) {
	t.Parallel()
	got := normalizeAgentTypeList([]string{"librarian"})
	if len(got) != 1 || !strings.EqualFold(got[0], "librarian") {
		t.Fatalf("normalizeAgentTypeList([librarian]) = %v, want [librarian]", got)
	}
}
