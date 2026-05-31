package skills

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/versioning"
)

type mockForestService struct {
	resolveIntent  func(ctx context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error)
	retrieve       func(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error)
	retrieveForest func(ctx context.Context, query forest.Query) ([]*forest.ForestPacket, error)
	predict        func(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error)
	createCursor   func(ctx context.Context, input forest.ForestCursorInput) (*forest.ForestCursor, error)
	proposeClaim   func(ctx context.Context, proposal forest.ForestClaimProposal) error
	recordOutcome  func(ctx context.Context, record forest.OutcomeRecord) error
}

func (m *mockForestService) ResolveIntent(ctx context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
	if m.resolveIntent != nil {
		return m.resolveIntent(ctx, input)
	}
	return &forest.IntentResolution{}, nil
}

func (m *mockForestService) Retrieve(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
	if m.retrieve != nil {
		return m.retrieve(ctx, query)
	}
	return nil, nil
}

func (m *mockForestService) RetrieveForest(ctx context.Context, query forest.Query) ([]*forest.ForestPacket, error) {
	if m.retrieveForest != nil {
		return m.retrieveForest(ctx, query)
	}
	var packets []*forest.BranchPacket
	var err error
	switch {
	case m.retrieve != nil:
		packets, err = m.retrieve(ctx, query)
	case m.predict != nil:
		packets, err = m.predict(ctx, query)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return forestPacketsFromBranchPackets(packets), nil
}

func (m *mockForestService) CreateForestCursor(ctx context.Context, input forest.ForestCursorInput) (*forest.ForestCursor, error) {
	if m.createCursor != nil {
		return m.createCursor(ctx, input)
	}
	cursor := &forest.ForestCursor{ID: "cursor-test", SessionID: input.SessionID, TaskID: input.TaskID}
	for _, packet := range input.Packets {
		if packet == nil {
			continue
		}
		cursor.ActiveNodeIDs = append(cursor.ActiveNodeIDs, packet.Node.ID)
	}
	return cursor, nil
}

func (m *mockForestService) ProposeForestClaim(ctx context.Context, proposal forest.ForestClaimProposal) error {
	if m.proposeClaim != nil {
		return m.proposeClaim(ctx, proposal)
	}
	return nil
}

func (m *mockForestService) PredictNextBranches(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
	if m.predict != nil {
		return m.predict(ctx, query)
	}
	return nil, nil
}

func (m *mockForestService) RecordOutcome(ctx context.Context, record forest.OutcomeRecord) error {
	if m.recordOutcome != nil {
		return m.recordOutcome(ctx, record)
	}
	return nil
}

func forestPacketsFromBranchPackets(packets []*forest.BranchPacket) []*forest.ForestPacket {
	out := make([]*forest.ForestPacket, 0, len(packets))
	for _, packet := range packets {
		if packet == nil || packet.Branch == nil {
			continue
		}
		nodeID := packet.Branch.ID
		summary := packet.Branch.Summary
		out = append(out, &forest.ForestPacket{
			Node: forest.ForestNode{
				ID:            nodeID,
				Kind:          testNodeKindForFamily(packet.Branch.Family),
				Title:         packet.Branch.Title,
				Summary:       summary,
				EvidenceGrade: forest.EvidenceGradeObserved,
				Confidence:    packet.Branch.Confidence,
			},
			Evidence: []forest.ForestEvidence{{
				RefType: "node",
				RefID:   nodeID,
				NodeID:  nodeID,
				Grade:   forest.EvidenceGradeObserved,
				Summary: summary,
			}},
			Score: 1,
		})
	}
	return out
}

func testNodeKindForFamily(family forest.TreeFamily) forest.ForestNodeKind {
	switch family {
	case forest.TreeFamilyConstraint:
		return forest.ForestNodeKind("Constraint")
	case forest.TreeFamilyEvidence:
		return forest.ForestNodeKind("Evidence")
	case forest.TreeFamilyOutcome:
		return forest.ForestNodeKind("Outcome")
	case forest.TreeFamilyAntiPattern:
		return forest.ForestNodeKind("AntiPattern")
	default:
		return forest.ForestNodeKind("Intent")
	}
}

func TestForestResolveIntentSkill(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			resolveIntent: func(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
				return &forest.IntentResolution{
					Query:         input.Query,
					PrimaryIntent: "ship safer retries",
					ActiveRoots:   []string{"root-1"},
				}, nil
			},
		},
	}

	skill := NewForestResolveIntentSkill(deps)
	input, _ := json.Marshal(forest.ResolveIntentInput{Query: "make retries safer"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*forest.IntentResolution)
	if output.PrimaryIntent != "ship safer retries" {
		t.Fatalf("unexpected primary intent: %s", output.PrimaryIntent)
	}
}

func TestForestRecallSkill(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			retrieve: func(_ context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
				return []*forest.BranchPacket{
					{Branch: &forest.Branch{ID: "branch-1", Family: forest.TreeFamilyIntent, Summary: query.Query}},
				}, nil
			},
		},
	}

	skill := NewForestRecallSkill(deps)
	input, _ := json.Marshal(ForestRecallInput{Query: "retry policy"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*ForestRecallOutput)
	if len(output.Packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(output.Packets))
	}
}

func TestForestRecallSkillDefaultsScopeFromContext(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			retrieve: func(_ context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
				if query.SessionID != "sess-ctx" {
					t.Fatalf("retrieve session_id = %q, want sess-ctx", query.SessionID)
				}
				if query.TaskID != "task-ctx" {
					t.Fatalf("retrieve task_id = %q, want task-ctx", query.TaskID)
				}
				if query.Horizon != forest.CanopyHorizonTask {
					t.Fatalf("retrieve horizon = %q, want %q", query.Horizon, forest.CanopyHorizonTask)
				}
				return []*forest.BranchPacket{}, nil
			},
		},
	}

	ctx := versioning.WithTaskID(versioning.WithSessionID(context.Background(), "sess-ctx"), "task-ctx")
	skill := NewForestRecallSkill(deps)
	input, _ := json.Marshal(ForestRecallInput{Query: "retry policy"})
	if _, err := skill.Handler(ctx, input); err != nil {
		t.Fatalf("handler error: %v", err)
	}
}

func TestRecallRecentSkillRecoversSummaryAndFocus(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			resolveIntent: func(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
				if input.Query != "python hello cli" {
					t.Fatalf("resolve query = %q, want %q", input.Query, "python hello cli")
				}
				return &forest.IntentResolution{PrimaryIntent: "Create the Python hello CLI package"}, nil
			},
			retrieve: func(_ context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
				if query.SessionID != "sess-ctx" {
					t.Fatalf("retrieve session_id = %q, want sess-ctx", query.SessionID)
				}
				if query.Horizon != forest.CanopyHorizonSession {
					t.Fatalf("retrieve horizon = %q, want %q", query.Horizon, forest.CanopyHorizonSession)
				}
				if !query.IncludeCounterEvidence {
					t.Fatal("expected include_counter_evidence to default true")
				}
				return []*forest.BranchPacket{
					{Branch: &forest.Branch{ID: "decision-1", Family: forest.TreeFamilyIntent, Summary: "Use argparse from the stdlib"}},
					{Branch: &forest.Branch{ID: "outcome-1", Family: forest.TreeFamilyOutcome, Summary: "Preserve python -m hello entrypoint"}},
				}, nil
			},
		},
	}

	ctx := versioning.WithSessionID(context.Background(), "sess-ctx")
	skill := NewRecallRecentSkill(deps)
	input, _ := json.Marshal(RecallRecentInput{Query: "python hello cli"})
	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*RecallRecentOutput)
	if !strings.Contains(output.Summary, "Create the Python hello CLI package") {
		t.Fatalf("summary = %q, want primary intent", output.Summary)
	}
	if len(output.Focus) < 2 {
		t.Fatalf("focus len = %d, want at least 2", len(output.Focus))
	}
	if output.Focus[0] != "Intent: Use argparse from the stdlib" {
		t.Fatalf("focus[0] = %q, want intent summary", output.Focus[0])
	}
}

func TestRecallRecentSkillAllowsEmptyQuery(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			retrieve: func(_ context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
				if query.Query != "" {
					t.Fatalf("retrieve query = %q, want empty", query.Query)
				}
				return []*forest.BranchPacket{
					{Branch: &forest.Branch{ID: "intent-1", Family: forest.TreeFamilyIntent, Summary: "Continue the earlier architectural plan"}},
				}, nil
			},
		},
	}

	skill := NewRecallRecentSkill(deps)
	input, _ := json.Marshal(RecallRecentInput{})
	result, err := skill.Handler(versioning.WithSessionID(context.Background(), "sess-ctx"), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*RecallRecentOutput)
	if !strings.Contains(output.Summary, "Continue the earlier architectural plan") {
		t.Fatalf("summary = %q, want recovered intent", output.Summary)
	}
}

func TestForestRecordOutcomeSkill(t *testing.T) {
	t.Parallel()

	recorded := false
	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			recordOutcome: func(_ context.Context, record forest.OutcomeRecord) error {
				recorded = record.BranchID == "branch-1" && record.Status == forest.OutcomeStatusSucceeded
				return nil
			},
		},
	}

	skill := NewForestRecordOutcomeSkill(deps)
	input, _ := json.Marshal(ForestOutcomeInput{
		BranchID: "branch-1",
		Status:   "succeeded",
		Summary:  "tests passed",
	})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !recorded {
		t.Fatal("expected record outcome to be invoked")
	}
	if !result.(*ForestOutcomeOutput).Recorded {
		t.Fatal("expected recorded=true")
	}
}

func TestPhase9ForestAgencySkillsUsePacketsCursorsAndProposalArtifacts(t *testing.T) {
	packet := &forest.ForestPacket{
		Node: forest.ForestNode{
			ID:            "node-agency",
			Kind:          forest.ForestNodeClaim,
			Summary:       "agency packet",
			EvidenceGrade: forest.EvidenceGradeObserved,
		},
		Evidence: []forest.ForestEvidence{{
			RefType: "artifact",
			RefID:   "artifact-agency",
			NodeID:  "node-agency",
			Grade:   forest.EvidenceGradeObserved,
		}},
		ValidationNeed: 1,
		ProposedClaims: []forest.ForestClaimProposalTemplate{{
			ProposalID:   "proposal-template",
			Summary:      "Add validation for agency packet",
			EvidenceRefs: []string{"artifact:artifact-agency"},
		}},
		Score: 1,
	}
	var proposed forest.ForestClaimProposal
	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			retrieveForest: func(_ context.Context, query forest.Query) ([]*forest.ForestPacket, error) {
				if !query.IncludeCounterEvidence {
					t.Fatal("agency evidence skills must include counter-evidence for review")
				}
				return []*forest.ForestPacket{packet}, nil
			},
			createCursor: func(_ context.Context, input forest.ForestCursorInput) (*forest.ForestCursor, error) {
				if len(input.Packets) != 1 {
					t.Fatalf("cursor input packet count = %d, want 1", len(input.Packets))
				}
				return &forest.ForestCursor{ID: "cursor-agency", ActiveNodeIDs: []string{"node-agency"}}, nil
			},
			proposeClaim: func(_ context.Context, proposal forest.ForestClaimProposal) error {
				proposed = proposal
				return nil
			},
		},
	}

	retrieved, err := NewForestRetrieveEvidenceSkill(deps).Handler(context.Background(), mustJSON(t, ForestRecallInput{Query: "agency", IncludeCounterEvidence: true}))
	if err != nil {
		t.Fatalf("retrieve evidence: %v", err)
	}
	retrieveOutput := retrieved.(*ForestRecallOutput)
	if len(retrieveOutput.Packets) != 1 || retrieveOutput.Cursor == nil || retrieveOutput.Cursor.ID != "cursor-agency" {
		t.Fatalf("retrieve evidence output missing packet/cursor: %+v", retrieveOutput)
	}

	suggested, err := NewForestSuggestValidationsSkill(deps).Handler(context.Background(), mustJSON(t, ForestRecallInput{Query: "agency"}))
	if err != nil {
		t.Fatalf("suggest validations: %v", err)
	}
	suggestions := suggested.(*ForestValidationSuggestionOutput)
	if len(suggestions.ValidationNeeds) == 0 || len(suggestions.ProposedClaims) == 0 {
		t.Fatalf("validation suggestions missing needs/proposals: %+v", suggestions)
	}

	if _, err := NewForestProposeClaimSkill(deps).Handler(context.Background(), mustJSON(t, ForestProposalInput{
		Summary:      "install skill from generated proposal",
		EvidenceRefs: []string{"artifact:artifact-agency"},
	})); err == nil {
		t.Fatal("privileged generated-skill proposal was accepted")
	}
	if _, err := NewForestProposeClaimSkill(deps).Handler(context.Background(), mustJSON(t, ForestProposalInput{
		Summary:      "Validate packet before adoption",
		ClusterID:    "cluster-agency",
		Dimension:    "validation",
		EvidenceRefs: []string{"artifact:artifact-agency"},
	})); err != nil {
		t.Fatalf("propose claim: %v", err)
	}
	if proposed.ID == "" || proposed.Summary != "Validate packet before adoption" || len(proposed.EvidenceRefs) != 1 {
		t.Fatalf("proposal not passed to forest service with evidence: %+v", proposed)
	}
}

func TestForestResolveIntentSkillDefaultsScopeFromContext(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			resolveIntent: func(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
				if input.SessionID != "sess-ctx" {
					t.Fatalf("resolve session_id = %q, want sess-ctx", input.SessionID)
				}
				if input.TaskID != "task-ctx" {
					t.Fatalf("resolve task_id = %q, want task-ctx", input.TaskID)
				}
				return &forest.IntentResolution{PrimaryIntent: "ctx intent"}, nil
			},
		},
	}

	ctx := versioning.WithTaskID(versioning.WithSessionID(context.Background(), "sess-ctx"), "task-ctx")
	skill := NewForestResolveIntentSkill(deps)
	input, _ := json.Marshal(forest.ResolveIntentInput{Query: "make retries safer"})
	if _, err := skill.Handler(ctx, input); err != nil {
		t.Fatalf("handler error: %v", err)
	}
}

func mustJSON(t testing.TB, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}
