package shared

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// findSkill returns the first skill in skills with the given name.
func findSkill(t *testing.T, ss []*skills.Skill, name string) *skills.Skill {
	t.Helper()
	for _, s := range ss {
		if s != nil && s.Name == name {
			return s
		}
	}
	t.Fatalf("skill %q not found", name)
	return nil
}

func TestConsultPeerSkill_RequiresClaimsBoard(t *testing.T) {
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-1" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Any prior art?"}`)
	if _, err := skill.Handler(context.Background(), input); err == nil {
		t.Fatal("expected missing claims board to fail")
	}
}

func TestConsultPeerSkill_RejectsSelfConsult(t *testing.T) {
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-self-consult" },
		AgentID:    func() string { return "architect-1" },
		AgentType:  func() string { return "architect" },
		PipelineID: func() string { return "pipe-1" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	_, err := skill.Handler(context.Background(), json.RawMessage(`{"target_agent_type":"architect","query":"What should I do?"}`))
	if err == nil {
		t.Fatal("expected self-consult to be rejected")
	}
}

func TestConsultPeerSkill_RequiresContinuationStoreWithoutSynchronousPeerRoute(t *testing.T) {
	sessionID := "sess-consult-requires-store"
	board := registerCrossPipelineTestBoard(t, sessionID)
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Any prior art?"}`)
	if _, err := skill.Handler(context.Background(), input); err == nil {
		t.Fatal("expected missing continuation store to fail")
	}
	if len(board.Projection().Claims) == 0 {
		t.Fatal("expected consultation claim to be posted before wait precondition failure")
	}
}

func TestConsultPeerSkill_StampsNestedClaimAndBranchMetadata(t *testing.T) {
	sessionID := "sess-consult-nesting"
	registry := claims.DefaultSessionBoardRegistry()
	registry.Remove(sessionID)
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-consult-nesting",
		SessionID: sessionID,
		TaskID:    "task-consult-nesting",
	})
	if err := registry.Register(sessionID, board); err != nil {
		t.Fatalf("register board: %v", err)
	}
	t.Cleanup(func() { registry.Remove(sessionID) })

	const parentClaimID = "claim-parent"
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			ID:    parentClaimID,
			Title: "parent planning turn",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction parent: %v", err)
	}

	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "architect-1" },
		AgentType:  func() string { return "architect" },
		PipelineID: func() string { return "" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")
	ctx := claims.WithParentClaimID(context.Background(), parentClaimID)
	ctx = WithContinuationStore(ctx, NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect-1",
		SessionID: sessionID,
		Board:     board,
		ResumeFn: func(context.Context, *TurnSnapshot, map[string]*AwaitedClaimResult) error {
			return nil
		},
	}))
	ctx = WithTurnContext(ctx, &TurnContext{
		Request:       &providers.Request{},
		CorrelationID: "corr-parent",
		AgentID:       "architect-1",
		SessionID:     sessionID,
	})
	ctx = context.WithValue(ctx, activeToolCallContextKey{}, ActiveToolCallContext{
		ToolCallKey: "consult-peer-tool",
		ToolName:    "consult_peer",
		InterAgent:  &InterAgentToolEvent{Kind: InterAgentToolEventKindConsult},
	})

	result, err := skill.Handler(ctx, json.RawMessage(`{"target_agent_type":"librarian","query":"What exists?"}`))
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	outcome, ok := skills.NormalizeToolOutcome(result)
	if !ok || outcome.Status != skills.ToolStatusYielded {
		t.Fatalf("result = %#v, want yielded ToolOutcome", result)
	}
	consultID := firstContinuationClaimRef(ctx)
	if consultID == "" {
		t.Fatal("consult claim was not registered with continuation store")
	}
	consultClaim, ok := board.CloneClaim(consultID)
	if !ok {
		t.Fatalf("consult claim %q not posted", consultID)
	}
	if !claims.HasRelation(consultClaim.Relations, claims.RelationshipCausedBy, parentClaimID) {
		t.Fatalf("consult claim missing caused_by parent relation: %+v", consultClaim.Relations)
	}
	if !claims.HasRelation(consultClaim.Relations, claims.RelationshipSubject, "librarian") {
		t.Fatalf("consult claim missing librarian subject relation: %+v", consultClaim.Relations)
	}
}

func TestConsultPeerSkill_CrossPipelineMetadataStaysOnClaimTicket(t *testing.T) {
	sessionID := "sess-cross-pipeline-consult"
	board := registerCrossPipelineTestBoard(t, sessionID)
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-origin" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")
	ctx := contextWithYieldingContinuation(context.Background(), "agent-1", sessionID, board)

	// Cross-pipeline consult: engineer in pipe-origin asking the
	// tester-pipeline in pipe-42 about shared fixture state. Per the
	// authority matrix (see docs/COMMS_MATRIX.md), engineer may
	// consult tester-pipeline and holds AllowsCrossPipelineConsult.
	input := json.RawMessage(`{"target_agent_type":"tester-pipeline","target_pipeline_id":"pipe-42","query":"How are you handling retries?"}`)
	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	if outcome, ok := skills.NormalizeToolOutcome(result); !ok || outcome.Status != skills.ToolStatusYielded {
		t.Fatalf("result = %#v, want yielded ToolOutcome", result)
	}
	claimID := firstContinuationClaimRef(ctx)
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("consult claim %q not found", claimID)
	}
	if !claimScopeContains(claim.Scope, "consultation", "tester-pipeline/pipe-42") {
		t.Fatalf("consult claim scope = %#v, want cross-pipeline target", claim.Scope)
	}
}

func TestConsultPeerSkill_YieldsWhenPeerMayLaterFailWithArtifact(t *testing.T) {
	// Peer errors are returned as artifacts in the eventual testament.
	// consult_peer itself posts the claim and yields/waits on lifecycle.
	sessionID := "sess-peer-error-yields"
	board := registerCrossPipelineTestBoard(t, sessionID)
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")
	ctx := contextWithYieldingContinuation(context.Background(), "agent-1", sessionID, board)

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Q"}`)
	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	outcome, ok := skills.NormalizeToolOutcome(result)
	if !ok || outcome.Status != skills.ToolStatusYielded {
		t.Fatalf("result = %#v, want yielded ToolOutcome", result)
	}
}

func TestConsultPeerSkill_TicketModeUsesClaimContinuation(t *testing.T) {
	sessionID := "sess-ticket-mode-claim"
	registry := claims.DefaultSessionBoardRegistry()
	registry.Remove(sessionID)
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-ticket-mode-claim",
		SessionID: sessionID,
		TaskID:    "task-ticket-mode-claim",
	})
	if err := registry.Register(sessionID, board); err != nil {
		t.Fatalf("register board: %v", err)
	}
	t.Cleanup(func() { registry.Remove(sessionID) })

	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "agent-1",
		SessionID: sessionID,
		Board:     board,
		ResumeFn: func(context.Context, *TurnSnapshot, map[string]*AwaitedClaimResult) error {
			return nil
		},
	})
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")
	ctx := WithContinuationStore(context.Background(), store)
	ctx = WithTurnContext(ctx, &TurnContext{
		Request:       &providers.Request{},
		CorrelationID: "corr-ticket-mode-claim",
		AgentID:       "agent-1",
		SessionID:     sessionID,
	})

	result, err := skill.Handler(ctx, json.RawMessage(`{"target_agent_type":"librarian","query":"Q","deadline_seconds":30}`))
	if err != nil {
		t.Fatalf("Handler returned error for yield outcome: %v", err)
	}
	outcome, ok := skills.NormalizeToolOutcome(result)
	if !ok || outcome.Status != skills.ToolStatusYielded {
		t.Fatalf("result = %#v, want yielded ToolOutcome", result)
	}
	store.mu.Lock()
	var consultID string
	for id := range store.claimIndex {
		consultID = id
		break
	}
	store.mu.Unlock()
	if consultID == "" {
		t.Fatal("consult_id was not registered with the continuation store")
	}
	store.DeliverClaimResult(context.Background(), &AwaitedClaimResult{
		SessionID:        sessionID,
		ClaimID:          consultID,
		Action:           claims.DeltaActionTestamentPosted,
		Status:           claims.ConsultStatusCompleted,
		ResponderAgentID: "librarian",
		EmittedAt:        time.Now().UTC(),
	})
}

func TestConsultPeerSkill_EmitsConsultEmittedActivity(t *testing.T) {
	// Audit-trail invariant: the consult_emitted activity is written
	// alongside the claim-backed lifecycle so post-hoc causal_trace and
	// ambient recall still work.
	collector := &activity.TestCollector{}
	prev := activity.SetDefaultSource(nil) // sink-only, no source
	prevSink := activity.SetDefaultSink(collector)
	defer activity.SetDefaultSource(prev)
	defer activity.SetDefaultSink(prevSink)

	sessionID := "sess-consult-activity"
	board := registerCrossPipelineTestBoard(t, sessionID)
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-1" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")
	ctx := contextWithYieldingContinuation(context.Background(), "agent-1", sessionID, board)

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"X","scope":"svc/auth/"}`)
	if _, err := skill.Handler(ctx, input); err != nil {
		t.Fatalf("handler err = %v", err)
	}

	acts := collector.Snapshot()
	matching := make([]activity.AgentActivity, 0, len(acts))
	for _, candidate := range acts {
		if candidate.SessionID == activity.SessionID(sessionID) && candidate.Action == activity.ActionConsultEmitted {
			matching = append(matching, candidate)
		}
	}
	if len(matching) != 1 {
		t.Fatalf("expected 1 matching consult activity, got %d from %d total", len(matching), len(acts))
	}
	act := matching[0]
	if act.Action != activity.ActionConsultEmitted {
		t.Fatalf("action = %q", act.Action)
	}
	if act.Actor.AgentID != "agent-1" || act.Actor.AgentType != "engineer" || act.Actor.PipelineID != "pipe-1" {
		t.Fatalf("actor = %#v", act.Actor)
	}
	if act.Subject.TargetAgent != "librarian" {
		t.Fatalf("subject.TargetAgent = %q", act.Subject.TargetAgent)
	}
	if act.Subject.PathPrefix != "svc/auth/" {
		t.Fatalf("subject.PathPrefix = %q", act.Subject.PathPrefix)
	}
}

func registerCrossPipelineTestBoard(t *testing.T, sessionID string) *claims.ClaimsBoard {
	t.Helper()
	registry := claims.DefaultSessionBoardRegistry()
	registry.Remove(sessionID)
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-" + sessionID,
		SessionID: sessionID,
		TaskID:    "task-" + sessionID,
	})
	if err := registry.Register(sessionID, board); err != nil {
		t.Fatalf("register board: %v", err)
	}
	t.Cleanup(func() { registry.Remove(sessionID) })
	return board
}

func contextWithYieldingContinuation(ctx context.Context, agentID, sessionID string, board *claims.ClaimsBoard) context.Context {
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

func firstContinuationClaimRef(ctx context.Context) string {
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

func claimScopeContains(scope []claims.ClaimScopeEntry, kind, key string) bool {
	for _, entry := range scope {
		if entry.Kind == kind && entry.Key == key {
			return true
		}
	}
	return false
}
