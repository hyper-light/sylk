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

func TestConsultPeerSkill_NilRouteSyncReturnsConsultID(t *testing.T) {
	// Fire-and-forget fallback: when no RouteSync transport is wired,
	// the skill emits the consult_emitted activity and returns the
	// consult_id + in_flight status without blocking on a response.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-1" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Any prior art?"}`)
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("handler result type = %T", result)
	}
	if got["status"] != "in_flight" {
		t.Fatalf("status = %v, want in_flight", got["status"])
	}
	if _, ok := got["consult_id"].(string); !ok {
		t.Fatalf("consult_id missing or wrong type: %#v", got)
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

func TestConsultPeerSkill_ReturnsTicketWithoutSynchronousPeerRoute(t *testing.T) {
	// Without continuation context, consult_peer returns a claim ticket
	// and lets claim.directed/testament.submitted drive the work. There
	// is no RouteSync hook in the config: the test fails at compile time
	// if the old synchronous route authority returns.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Any prior art?"}`)
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("handler result type = %T", result)
	}
	if got["status"] != "in_flight" {
		t.Fatalf("status = %v, want in_flight", got["status"])
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
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("handler result type = %T", result)
	}
	consultID, _ := got["consult_id"].(string)
	if consultID == "" {
		t.Fatalf("consult_id missing: %#v", got)
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
	// Cross-pipeline consults carry target_pipeline_id in the claim
	// ticket/activity. The config has no synchronous route hook, so no
	// side-channel metadata path can run.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-origin" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	// Cross-pipeline consult: engineer in pipe-origin asking the
	// tester-pipeline in pipe-42 about shared fixture state. Per the
	// authority matrix (see docs/COMMS_MATRIX.md), engineer may
	// consult tester-pipeline and holds AllowsCrossPipelineConsult.
	input := json.RawMessage(`{"target_agent_type":"tester-pipeline","target_pipeline_id":"pipe-42","query":"How are you handling retries?"}`)
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("handler result type = %T", result)
	}
	if got["target"] != "tester-pipeline/pipe-42" {
		t.Fatalf("target = %#v, want tester-pipeline/pipe-42", got["target"])
	}
}

func TestConsultPeerSkill_ReturnsTicketWhenPeerMayLaterFailWithArtifact(t *testing.T) {
	// Peer errors are returned as artifacts in the eventual testament.
	// consult_peer itself only posts the claim and returns/yields.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Q"}`)
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	got := result.(map[string]any)
	if got["status"] != "in_flight" {
		t.Fatalf("status = %#v, want in_flight", got["status"])
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
	// regardless of which mode (sync vs. fire-and-forget) the caller
	// is in, so post-hoc causal_trace and ambient recall still work.
	collector := &activity.TestCollector{}
	prev := activity.SetDefaultSource(nil) // sink-only, no source
	prevSink := activity.SetDefaultSink(collector)
	defer activity.SetDefaultSource(prev)
	defer activity.SetDefaultSink(prevSink)

	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-1" },
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"X","scope":"svc/auth/"}`)
	if _, err := skill.Handler(context.Background(), input); err != nil {
		t.Fatalf("handler err = %v", err)
	}

	acts := collector.Snapshot()
	if len(acts) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(acts))
	}
	act := acts[0]
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
