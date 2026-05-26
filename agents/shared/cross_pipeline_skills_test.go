package shared

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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
		RouteSync:  nil,
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

func TestConsultPeerSkill_RouteSyncSuccessReturnsResponse(t *testing.T) {
	// When RouteSync returns a successful RouteResponse, the handler
	// surfaces the payload as the consult response and emits completed
	// status. The target's stream events are expected to stitch as
	// children upstream via the branch metadata the handler stamps on
	// the outgoing RouteRequest — we verify the request shape here,
	// since the stitching is observed in UI integration tests.
	var capturedReq *guide.RouteRequest
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
		RouteSync: func(_ context.Context, req *guide.RouteRequest) (*guide.Message, error) {
			capturedReq = req
			return guide.NewResponseMessage("resp-1", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       true,
				Data:          map[string]any{"user_message": "Found two related modules."},
			}), nil
		},
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
	if got["status"] != "completed" {
		t.Fatalf("status = %v, want completed", got["status"])
	}
	resp, ok := got["response"].(map[string]any)
	if !ok {
		t.Fatalf("response missing or wrong type: %#v", got)
	}
	if resp["user_message"] != "Found two related modules." {
		t.Fatalf("response.user_message = %v", resp["user_message"])
	}
	if capturedReq == nil {
		t.Fatal("route sync never called")
	}
	if capturedReq.TargetAgentID != "librarian" {
		t.Fatalf("target agent = %q", capturedReq.TargetAgentID)
	}
	if capturedReq.Input != "Any prior art?" {
		t.Fatalf("input = %q", capturedReq.Input)
	}
	if !capturedReq.ExplicitTarget {
		t.Fatal("expected ExplicitTarget=true")
	}
}

func TestConsultPeerSkill_RouteSyncStampsNestedClaimAndBranchMetadata(t *testing.T) {
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

	var capturedReq *guide.RouteRequest
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "architect-1" },
		AgentType:  func() string { return "architect" },
		PipelineID: func() string { return "" },
		RouteSync: func(_ context.Context, req *guide.RouteRequest) (*guide.Message, error) {
			capturedReq = req
			return guide.NewResponseMessage("resp-1", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       true,
				Data:          map[string]any{"answer": "ok"},
			}), nil
		},
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
	if capturedReq == nil {
		t.Fatal("route sync never called")
	}
	if capturedReq.Metadata[MetadataKeyParentClaimID] != consultID {
		t.Fatalf("parent_claim_id = %#v, want consult claim %q", capturedReq.Metadata[MetadataKeyParentClaimID], consultID)
	}
	if capturedReq.Metadata["chat_nested_branch"] != true {
		t.Fatalf("chat_nested_branch = %#v, want true", capturedReq.Metadata["chat_nested_branch"])
	}
	if capturedReq.Metadata["chat_parent_correlation_id"] != "corr-parent" {
		t.Fatalf("chat_parent_correlation_id = %#v, want corr-parent", capturedReq.Metadata["chat_parent_correlation_id"])
	}
	if capturedReq.Metadata["chat_parent_tool_call_key"] != "consult-peer-tool" {
		t.Fatalf("chat_parent_tool_call_key = %#v, want consult-peer-tool", capturedReq.Metadata["chat_parent_tool_call_key"])
	}
	if capturedReq.Metadata["chat_inter_agent_kind"] != InterAgentToolEventKindConsult {
		t.Fatalf("chat_inter_agent_kind = %#v, want consult", capturedReq.Metadata["chat_inter_agent_kind"])
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

func TestConsultPeerSkill_RouteSyncCrossPipelineMetadata(t *testing.T) {
	// Cross-pipeline consults carry target_pipeline_id; the handler
	// stamps it into request metadata so the routing layer can resolve
	// a specific pipeline's peer rather than the nearest same-pipeline
	// or knowledge peer.
	var capturedReq *guide.RouteRequest
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-origin" },
		RouteSync: func(_ context.Context, req *guide.RouteRequest) (*guide.Message, error) {
			capturedReq = req
			return guide.NewResponseMessage("resp-1", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       true,
				Data:          map[string]any{"note": "ok"},
			}), nil
		},
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	// Cross-pipeline consult: engineer in pipe-origin asking the
	// tester-pipeline in pipe-42 about shared fixture state. Per the
	// authority matrix (see docs/COMMS_MATRIX.md), engineer may
	// consult tester-pipeline and holds AllowsCrossPipelineConsult.
	input := json.RawMessage(`{"target_agent_type":"tester-pipeline","target_pipeline_id":"pipe-42","query":"How are you handling retries?"}`)
	if _, err := skill.Handler(context.Background(), input); err != nil {
		t.Fatalf("handler err = %v", err)
	}
	if capturedReq == nil {
		t.Fatal("route sync never called")
	}
	if got, _ := capturedReq.Metadata["target_pipeline_id"].(string); got != "pipe-42" {
		t.Fatalf("metadata.target_pipeline_id = %v", capturedReq.Metadata["target_pipeline_id"])
	}
}

func TestConsultPeerSkill_RouteSyncFailureReturnsError(t *testing.T) {
	// A failed RouteResponse.Success=false is propagated as a handler
	// error so the tool-loop's Phase 1 event marks the consult_peer row
	// as Failed. This avoids the silent-success case where a peer
	// rejected the consult but the UI still showed a green checkmark.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
		RouteSync: func(_ context.Context, req *guide.RouteRequest) (*guide.Message, error) {
			return guide.NewResponseMessage("resp-1", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       false,
				Error:         "peer refused consultation: out of scope",
			}), nil
		},
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Q"}`)
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected handler error on failed route response")
	}
	if !strings.Contains(err.Error(), "out of scope") {
		t.Fatalf("error does not mention peer reason: %v", err)
	}
}

func TestConsultPeerSkill_RouteSyncTransportErrorReturnsError(t *testing.T) {
	// Transport-level failures (bus unavailable, timeout, cancellation)
	// propagate as handler errors so the tool row renders as Failed.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
		RouteSync: func(_ context.Context, _ *guide.RouteRequest) (*guide.Message, error) {
			return nil, errors.New("bus unavailable")
		},
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Q"}`)
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected handler error on transport failure")
	}
	if !strings.Contains(err.Error(), "bus unavailable") {
		t.Fatalf("error does not mention transport failure: %v", err)
	}
}

func TestConsultPeerSkill_TicketModeUsesClaimInsteadOfRouteSync(t *testing.T) {
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

	routeCalled := false
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "agent-1",
		SessionID: sessionID,
		Board:     board,
		ResumeFn: func(context.Context, *TurnSnapshot, map[string]*claims.ConsultResolvedDelta) error {
			return nil
		},
	})
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
		RouteSync: func(context.Context, *guide.RouteRequest) (*guide.Message, error) {
			routeCalled = true
			return nil, errors.New("legacy route should not run in claims ticket mode")
		},
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
	if routeCalled {
		t.Fatal("RouteSync was called; claims ticket mode must let the posted claim drive peer work")
	}

	store.mu.Lock()
	var consultID string
	for id := range store.consultIndex {
		consultID = id
		break
	}
	store.mu.Unlock()
	if consultID == "" {
		t.Fatal("consult_id was not registered with the continuation store")
	}
	store.DeliverResolution(context.Background(), &claims.ConsultResolvedDelta{
		SessionID:         sessionID,
		ConsultID:         consultID,
		OriginatorAgentID: "agent-1",
		ResponderAgentID:  "librarian",
		Status:            claims.ConsultStatusCompleted,
		EmittedAt:         time.Now().UTC(),
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
		RouteSync:  nil, // fire-and-forget exercises just the activity write
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
