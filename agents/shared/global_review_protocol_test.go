package shared

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type globalReviewDirectiveCarrierStub struct {
	Response  string
	Directive *guide.ResponseDirective
}

func (s *globalReviewDirectiveCarrierStub) ResponseText() string {
	if s == nil {
		return ""
	}
	return s.Response
}

func (s *globalReviewDirectiveCarrierStub) ResponseDirective() *guide.ResponseDirective {
	if s == nil {
		return nil
	}
	return s.Directive
}

func TestNewGlobalReviewProtocolSkills_AgentSpecificOwnership(t *testing.T) {
	tests := []struct {
		name      string
		agentType string
		want      []string
	}{
		{
			name:      "inspector owns global handoff challenge and commit path",
			agentType: GlobalReviewAgentInspector,
			want: []string{
				"challenge_global_tester",
				"challenge_architect",
				"challenge_orchestrator",
				"handoff_next",
				"validate_work",
				"process_validation",
				"finalize_global_review",
				"accept_checkpoint",
				"commit_to_disk",
				"discard_checkpoint",
				"query_global_review_state",
			},
		},
		{
			name:      "tester mirrors pipeline handoff and challenge mechanics",
			agentType: GlobalReviewAgentTester,
			want:      []string{"challenge_inspector", "handoff_next", "validate_work", "process_validation", "query_global_review_state"},
		},
		{
			name:      "architect only validates",
			agentType: GlobalReviewAgentArchitect,
			want:      []string{"validate_work", "query_global_review_state"},
		},
		{
			name:      "orchestrator only validates",
			agentType: GlobalReviewAgentOrchestrator,
			want:      []string{"validate_work", "query_global_review_state"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
				AgentType: func() string { return tt.agentType },
			})
			if len(skills) != len(tt.want) {
				t.Fatalf("len(skills) = %d, want %d", len(skills), len(tt.want))
			}
			got := make([]string, 0, len(skills))
			for _, skill := range skills {
				got = append(got, skill.Name)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("skills[%d] = %q, want %q; got=%v", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestGlobalReviewOrchestratorChallenge_CarriesExecutionStateGuard(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.TargetAgentID != GlobalReviewAgentOrchestrator {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	snapshot := &GlobalReviewSnapshot{
		ReviewID:       "review-orchestrator",
		CurrentRequest: "Audit the merged checkpoint against current workflow progress.",
	}
	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(snapshot, GlobalReviewMetadata(map[string]any{
		"global_review_stage":      "checkpoint",
		"workflow_total_tasks":     3,
		"workflow_completed_tasks": 1,
		"workflow_remaining_tasks": 2,
	}, snapshot)))
	ctx = WithStreamContext(ctx, "corr-orchestrator", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentInspector },
		AgentID:   func() string { return "inspector-global-1" },
		ResolveTarget: func(agentType string) string {
			if agentType == GlobalReviewAgentTester {
				return "tester-global-1"
			}
			return agentType
		},
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	})
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_orchestrator", map[string]any{
		"reason":  "Need authoritative workflow progress before deciding whether the checkpoint is on track.",
		"request": "Report DAG/workflow progress, remaining tasks, and any blockers for the current merged checkpoint.",
	}); err != nil {
		t.Fatalf("challenge_orchestrator: %v", err)
	}

	select {
	case req := <-reqCh:
		if !strings.Contains(req.Input, "Global review request for the orchestrator.") {
			t.Fatalf("input = %q, want orchestrator request heading", req.Input)
		}
		if !strings.Contains(req.Input, "Orchestrator scope rule") {
			t.Fatalf("input = %q, want orchestrator scope rule", req.Input)
		}
		if !strings.Contains(req.Input, "validate_work") {
			t.Fatalf("input = %q, want validate_work instruction", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for orchestrator challenge route")
	}
}

func TestGlobalReviewChallengePublishesUserVisibleRoute(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.TargetAgentID != "tester-global-1" {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	snapshot := &GlobalReviewSnapshot{
		ReviewID:       "review-1",
		CurrentRequest: "Audit the merged result.",
	}
	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(snapshot, GlobalReviewMetadata(map[string]any{
		"plan_snapshot":            "full plan",
		"global_review_stage":      "checkpoint",
		"workflow_total_tasks":     3,
		"workflow_completed_tasks": 1,
		"workflow_remaining_tasks": 2,
	}, snapshot)))
	ctx = WithStreamContext(ctx, "corr-root", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentInspector },
		AgentID:   func() string { return "inspector-global-1" },
		ResolveTarget: func(agentType string) string {
			if agentType == GlobalReviewAgentTester {
				return "tester-global-1"
			}
			return agentType
		},
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	})
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_global_tester", map[string]any{
		"reason":  "Need merged-state adversarial validation.",
		"request": "Run the full tester audit against the merged plan.",
	}); err != nil {
		t.Fatalf("challenge_global_tester: %v", err)
	}

	select {
	case req := <-reqCh:
		if req.TargetAgentID != "tester-global-1" {
			t.Fatalf("target_agent_id = %q, want tester-global-1", req.TargetAgentID)
		}
		if req.SourceAgentID != "tui" {
			t.Fatalf("source_agent_id = %q, want tui", req.SourceAgentID)
		}
		if req.Metadata["global_review"] != true {
			t.Fatalf("metadata global_review = %#v, want true", req.Metadata["global_review"])
		}
		if _, ok := req.Metadata["global_review_protocol"]; !ok {
			t.Fatal("expected global_review_protocol metadata")
		}
		if !strings.Contains(req.Input, "Review stage: checkpoint") {
			t.Fatalf("input = %q, want review stage context", req.Input)
		}
		if !strings.Contains(req.Input, "Future planned work that has not been merged yet is pending, not missing.") {
			t.Fatalf("input = %q, want checkpoint pending guidance", req.Input)
		}
		if !strings.Contains(req.Input, "Protocol obligations:") || !strings.Contains(req.Input, "validate_work") {
			t.Fatalf("input = %q, want protocol obligation guidance", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for global review route request")
	}
}

func TestGlobalReviewHandoffPublishesTopLevelRoute(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.TargetAgentID != "tester-global-1" {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	snapshot := &GlobalReviewSnapshot{
		ReviewID:       "review-handoff",
		CurrentRequest: "Audit the merged result.",
	}
	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(snapshot, GlobalReviewMetadata(map[string]any{
		"global_review_stage": "checkpoint",
	}, snapshot)))
	ctx = WithStreamContext(ctx, "corr-handoff", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentInspector },
		AgentID:   func() string { return "inspector-global-1" },
		ResolveTarget: func(agentType string) string {
			if agentType == GlobalReviewAgentTester {
				return "tester-global-1"
			}
			return agentType
		},
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	})
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "handoff_next", map[string]any{
		"target_agents": []string{GlobalReviewAgentTester},
		"reason":        "Tester should perform the next broad merged-state validation pass.",
		"request":       "Audit the merged checkpoint and return the broad testing verdict.",
	}); err != nil {
		t.Fatalf("handoff_next: %v", err)
	}

	select {
	case req := <-reqCh:
		if req.TargetAgentID != "tester-global-1" {
			t.Fatalf("target_agent_id = %q, want tester-global-1", req.TargetAgentID)
		}
		if req.ParentCorrelationID != "corr-handoff" {
			t.Fatalf("parent_correlation_id = %q, want corr-handoff", req.ParentCorrelationID)
		}
		if req.Metadata["chat_nested_branch"] == true {
			t.Fatalf("handoff_next should not stamp nested challenge metadata: %#v", req.Metadata)
		}
		if !strings.Contains(req.Input, "ordinary top-level handoff") {
			t.Fatalf("input = %q, want top-level handoff guidance", req.Input)
		}
		if !strings.Contains(req.Input, "`handoff_next`") {
			t.Fatalf("input = %q, want handoff_next guidance", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for global review handoff route")
	}
}

func TestGlobalReviewHandoffRefusesRepeatedUnchangedMergedState(t *testing.T) {
	views := stubGlobalReviewProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:        versioning.WorkspaceViewGlobal,
			SourceOfTruth:      versioning.WorkspaceViewGlobal,
			Paths:              []string{"src/app.go"},
			GlobalChangedPaths: []string{"src/app.go"},
		},
	}
	baseSnapshot := &GlobalReviewSnapshot{ReviewID: "review-repeat"}
	metadata := GlobalReviewMetadata(map[string]any{
		"global_review_stage": "checkpoint",
		"affected_files":      []any{"src/app.go"},
		"plan_snapshot":       "checkpoint plan v1",
	}, baseSnapshot)
	cfg := GlobalReviewProtocolSkillConfig{
		AgentType:      func() string { return GlobalReviewAgentInspector },
		AgentID:        func() string { return "inspector-global-1" },
		ResolveTarget:  func(agentType string) string { return agentType },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
	}
	currentAction := &GlobalReviewTurnAction{
		Type:        GlobalReviewActionHandoff,
		AgentType:   GlobalReviewAgentInspector,
		TargetAgent: GlobalReviewAgentTester,
		Reason:      "Tester should perform the next broad merged-state validation pass.",
		Request:     "Audit the merged checkpoint and return the broad testing verdict.",
	}
	tempState := NewGlobalReviewState(baseSnapshot, metadata)
	tempCtx := WithGlobalReviewState(context.Background(), tempState)
	stateFingerprint := resolveGlobalReviewSelectionStateFingerprint(tempCtx, cfg, tempState)
	requestFingerprint := globalReviewSelectionRequestFingerprint(currentAction)
	if stateFingerprint == "" {
		t.Fatal("expected merged-state fingerprint")
	}
	if requestFingerprint == "" {
		t.Fatal("expected request fingerprint")
	}

	snapshot := &GlobalReviewSnapshot{
		ReviewID: "review-repeat",
		RecentEvents: []GlobalReviewEvent{{
			Type:               string(GlobalReviewActionHandoff),
			AgentType:          GlobalReviewAgentInspector,
			TargetAgent:        GlobalReviewAgentTester,
			Summary:            currentAction.Request,
			StateFingerprint:   stateFingerprint,
			RequestFingerprint: requestFingerprint,
		}},
	}
	state := NewGlobalReviewState(snapshot, metadata)
	ctx := WithGlobalReviewState(context.Background(), state)
	ctx = WithStreamContext(ctx, "corr-repeat", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(cfg)
	result, err := invokeGlobalReviewSkill(t, ctx, skills, "handoff_next", map[string]any{
		"target_agents": []string{GlobalReviewAgentTester},
		"reason":        currentAction.Reason,
		"request":       currentAction.Request,
	})
	if err != nil {
		t.Fatalf("handoff_next error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil || resultMap["refused"] != true {
		t.Fatalf("handoff_next result = %#v, want refused=true", result)
	}
	if resultMap["refused_by"] != "global-review-protocol" {
		t.Fatalf("refused_by = %#v, want global-review-protocol", resultMap["refused_by"])
	}
	if resultMap["must_wait"] != true {
		t.Fatalf("must_wait = %#v, want true", resultMap["must_wait"])
	}
	if !strings.Contains(resultMap["reason"].(string), "fresh merged-state evidence") {
		t.Fatalf("reason = %#v, want merged-state evidence guidance", resultMap["reason"])
	}
	if action := state.TerminalAction(); action == nil || action.Type != GlobalReviewActionRefusal {
		t.Fatalf("terminal action = %#v, want refusal", action)
	}
}

func TestGlobalReviewHandoffRefusedWhenMergedStateUnchangedEvenWithNewRequest(t *testing.T) {
	// Under the original AND'd semantics the LLM could bypass repeat-refusal
	// simply by rewording its challenge/handoff. The new OR'd semantics
	// treat workspace content and request text as independent gates, so
	// state-unchanged alone must refuse regardless of how the LLM rephrases.
	// This is the guard that kept the pipeline inspector from looping and
	// that global review previously lacked.
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	views := stubGlobalReviewProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:        versioning.WorkspaceViewGlobal,
			SourceOfTruth:      versioning.WorkspaceViewGlobal,
			Paths:              []string{"src/app.go"},
			GlobalChangedPaths: []string{"src/app.go"},
		},
	}
	baseSnapshot := &GlobalReviewSnapshot{ReviewID: "review-reframe"}
	metadata := GlobalReviewMetadata(map[string]any{
		"global_review_stage": "checkpoint",
		"affected_files":      []any{"src/app.go"},
		"plan_snapshot":       "checkpoint plan v1",
	}, baseSnapshot)
	cfg := GlobalReviewProtocolSkillConfig{
		AgentType:      func() string { return GlobalReviewAgentInspector },
		AgentID:        func() string { return "inspector-global-1" },
		ResolveTarget:  func(agentType string) string { return agentType },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	}
	previousAction := &GlobalReviewTurnAction{
		Type:        GlobalReviewActionHandoff,
		AgentType:   GlobalReviewAgentInspector,
		TargetAgent: GlobalReviewAgentTester,
		Reason:      "Tester should perform the next broad merged-state validation pass.",
		Request:     "Audit the merged checkpoint and return the broad testing verdict.",
	}
	tempState := NewGlobalReviewState(baseSnapshot, metadata)
	tempCtx := WithGlobalReviewState(context.Background(), tempState)

	snapshot := &GlobalReviewSnapshot{
		ReviewID: "review-reframe",
		RecentEvents: []GlobalReviewEvent{{
			Type:               string(GlobalReviewActionHandoff),
			AgentType:          GlobalReviewAgentInspector,
			TargetAgent:        GlobalReviewAgentTester,
			Summary:            previousAction.Request,
			StateFingerprint:   resolveGlobalReviewSelectionStateFingerprint(tempCtx, cfg, tempState),
			RequestFingerprint: globalReviewSelectionRequestFingerprint(previousAction),
		}},
	}
	state := NewGlobalReviewState(snapshot, metadata)
	ctx := WithGlobalReviewState(context.Background(), state)
	ctx = WithStreamContext(ctx, "corr-reframe", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(cfg)
	result, err := invokeGlobalReviewSkill(t, ctx, skills, "handoff_next", map[string]any{
		"target_agents": []string{GlobalReviewAgentTester},
		"reason":        "Tester should re-audit the same merged checkpoint, but now focused on CLI entrypoint integration and install docs coherence.",
		"request":       "Audit the merged checkpoint with emphasis on argparse wiring, CLI invocation behavior, and README-facing install flow.",
	})
	if err != nil {
		t.Fatalf("handoff_next error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil {
		t.Fatalf("handoff_next result is nil, want refusal map")
	}
	if resultMap["refused"] != true {
		t.Fatalf("handoff_next result = %#v, want refused=true (workspace unchanged)", resultMap)
	}
	if got, _ := resultMap["refused_by"].(string); got != "global-review-protocol" {
		t.Fatalf("refused_by = %q, want global-review-protocol", got)
	}
	if action := state.TerminalAction(); action == nil || action.Type != GlobalReviewActionRefusal {
		t.Fatalf("terminal action = %#v, want refusal", action)
	}
}

func TestWithGlobalReviewContext_PreservesExistingState(t *testing.T) {
	snapshot := &GlobalReviewSnapshot{
		ReviewID:       "global-review-task_1",
		CurrentRequest: "Audit the merged checkpoint for task_1.",
		ActiveAgents:   []string{GlobalReviewAgentInspector},
	}
	outer := WithGlobalReviewContext(
		context.Background(),
		GlobalReviewMetadata(map[string]any{"task_id": "task_1"}, snapshot),
	)
	outerState := GlobalReviewStateFromContext(outer)
	if outerState == nil {
		t.Fatal("expected outer global review state")
	}

	inner := WithGlobalReviewContext(
		outer,
		GlobalReviewMetadata(map[string]any{"task_id": "task_1", "global_review_stage": "checkpoint"}, snapshot),
	)
	innerState := GlobalReviewStateFromContext(inner)
	if innerState == nil {
		t.Fatal("expected inner global review state")
	}
	if innerState != outerState {
		t.Fatal("expected inner context to reuse the existing global review state")
	}
}

func TestWrapGlobalReviewTurnResult_OuterContextPreservesRecordedAction(t *testing.T) {
	snapshot := &GlobalReviewSnapshot{
		ReviewID:       "global-review-task_1",
		CurrentRequest: "Audit the merged checkpoint for task_1.",
		ActiveAgents:   []string{GlobalReviewAgentInspector},
	}
	outer := WithGlobalReviewContext(
		context.Background(),
		GlobalReviewMetadata(map[string]any{"task_id": "task_1"}, snapshot),
	)
	inner := WithGlobalReviewContext(
		outer,
		GlobalReviewMetadata(map[string]any{"task_id": "task_1", "global_review_stage": "checkpoint"}, snapshot),
	)
	state := GlobalReviewStateFromContext(inner)
	if state == nil {
		t.Fatal("expected inner global review state")
	}
	action := &GlobalReviewTurnAction{
		Type:          GlobalReviewActionChallenge,
		AgentType:     GlobalReviewAgentInspector,
		AgentID:       "inspector-global-1",
		TargetAgent:   GlobalReviewAgentTester,
		TargetAgentID: "tester-global-1",
		Reason:        "Need tester-backed global validation before accepting the checkpoint.",
		Request:       "Audit the merged checkpoint and return a validation result.",
	}
	if err := state.setTerminalAction(action); err != nil {
		t.Fatalf("setTerminalAction: %v", err)
	}

	wrapped := WrapGlobalReviewTurnResult(outer, map[string]any{"response": "ok"})
	turnResp, err := DecodeGlobalReviewTurnResponse(wrapped)
	if err != nil {
		t.Fatalf("DecodeGlobalReviewTurnResponse: %v", err)
	}
	if turnResp == nil || turnResp.Action == nil {
		t.Fatal("expected wrapped global review turn response to include the recorded action")
	}
	if turnResp.Action.Type != GlobalReviewActionChallenge {
		t.Fatalf("action type = %q, want %q", turnResp.Action.Type, GlobalReviewActionChallenge)
	}
	if turnResp.Action.TargetAgent != GlobalReviewAgentTester {
		t.Fatalf("target agent = %q, want %q", turnResp.Action.TargetAgent, GlobalReviewAgentTester)
	}
	if turnResp.Action.AgentID != "inspector-global-1" {
		t.Fatalf("agent id = %q, want inspector-global-1", turnResp.Action.AgentID)
	}
	if turnResp.Action.TargetAgentID != "tester-global-1" {
		t.Fatalf("target agent id = %q, want tester-global-1", turnResp.Action.TargetAgentID)
	}

	wrapped = WrapGlobalReviewTurnResult(outer, &globalReviewDirectiveCarrierStub{
		Response: "ok",
		Directive: &guide.ResponseDirective{
			Phase:   guide.PhasePlanApproval,
			AgentID: "architect",
		},
	})
	turnResp, err = DecodeGlobalReviewTurnResponse(wrapped)
	if err != nil {
		t.Fatalf("DecodeGlobalReviewTurnResponse(second): %v", err)
	}
	if directive := turnResp.ResponseDirective(); directive == nil || directive.AgentID != "architect" {
		t.Fatalf("directive = %#v, want architect directive passthrough", directive)
	}
}

func TestGlobalReviewValidateWork_RoutesBackToExactRequestingAgentID(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.TargetAgentID != "inspector-global-1" {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	snapshot := &GlobalReviewSnapshot{
		ReviewID: "review-validate-1",
		PendingChallenge: &GlobalReviewChallenge{
			ID:                "review-validate-1-challenge-1",
			RequestingAgent:   GlobalReviewAgentInspector,
			RequestingAgentID: "inspector-global-1",
			TargetAgent:       GlobalReviewAgentArchitect,
			TargetAgentID:     "architect",
			Request:           "Clarify the checkpoint.",
		},
	}
	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(snapshot, GlobalReviewMetadata(nil, snapshot)))
	ctx = WithStreamContext(ctx, "corr-validate", "tui")

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentArchitect },
		AgentID:   func() string { return "architect" },
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	})
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "validate_work", map[string]any{
		"challenge_id":     "review-validate-1-challenge-1",
		"requesting_agent": GlobalReviewAgentInspector,
		"status":           string(GlobalReviewValidationPassed),
		"summary":          "Checkpoint is plan-adherent.",
	}); err != nil {
		t.Fatalf("validate_work: %v", err)
	}

	select {
	case req := <-reqCh:
		if req.TargetAgentID != "inspector-global-1" {
			t.Fatalf("target_agent_id = %q, want inspector-global-1", req.TargetAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for validation route request")
	}
}

// TestValidateGlobalReviewCompletion_OrchestratorMustAnswerChallenge locks in
// the gate that prevents the orchestrator from ending its turn while a
// pending inspector challenge targets it. Without this enforcement, the
// orchestrator's LLM responds with text and exits silently, leaving the
// inspector waiting forever for a validate_work that is never dispatched.
// Mirrors how pipeline workers, the global tester, and the global inspector
// already gate completion on their respective protocol obligations.
func TestValidateGlobalReviewCompletion_OrchestratorMustAnswerChallenge(t *testing.T) {
	snapshot := &GlobalReviewSnapshot{
		ReviewID: "review-orch-pending-1",
		PendingChallenge: &GlobalReviewChallenge{
			ID:                "review-orch-pending-1-challenge-1",
			RequestingAgent:   GlobalReviewAgentInspector,
			RequestingAgentID: "inspector-global-1",
			TargetAgent:       GlobalReviewAgentOrchestrator,
			TargetAgentID:     "orchestrator",
			Request:           "Investigate the visibility mismatch in the merged checkpoint.",
		},
	}
	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(snapshot, GlobalReviewMetadata(nil, snapshot)))

	err := ValidateGlobalReviewCompletion(ctx, GlobalReviewAgentOrchestrator)
	if err == nil {
		t.Fatal("ValidateGlobalReviewCompletion = nil, want error forcing validate_work for the pending orchestrator-targeted challenge")
	}
	if !strings.Contains(err.Error(), "validate_work") {
		t.Fatalf("ValidateGlobalReviewCompletion error = %q, want validate_work guidance", err.Error())
	}

	// Sanity check: the same gate is a no-op when no global-review state is
	// hydrated (ordinary orchestrator turns must not be affected).
	if got := ValidateGlobalReviewCompletion(context.Background(), GlobalReviewAgentOrchestrator); got != nil {
		t.Fatalf("ValidateGlobalReviewCompletion without state = %v, want nil (no-op for ordinary turns)", got)
	}
}

func TestGlobalReviewArchitectChallenge_CarriesCheckpointGuard(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.TargetAgentID != GlobalReviewAgentArchitect {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	snapshot := &GlobalReviewSnapshot{
		ReviewID:       "review-architect",
		CurrentRequest: "Review the merged checkpoint against the architect plan.",
	}
	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(snapshot, GlobalReviewMetadata(map[string]any{
		"plan_snapshot":            "full plan",
		"global_review_stage":      "checkpoint",
		"workflow_total_tasks":     3,
		"workflow_completed_tasks": 1,
		"workflow_remaining_tasks": 2,
	}, snapshot)))
	ctx = WithStreamContext(ctx, "corr-architect", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentInspector },
		AgentID:   func() string { return "inspector-global-1" },
		ResolveTarget: func(agentType string) string {
			if agentType == GlobalReviewAgentTester {
				return "tester-global-1"
			}
			return agentType
		},
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	})
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_architect", map[string]any{
		"reason":  "Need plan-level clarification for this checkpoint review.",
		"request": "Explain whether the current checkpoint is still aligned with the plan.",
	}); err != nil {
		t.Fatalf("challenge_architect: %v", err)
	}

	select {
	case req := <-reqCh:
		if !strings.Contains(req.Input, "Checkpoint rule for the architect") {
			t.Fatalf("input = %q, want architect checkpoint guard", req.Input)
		}
		if !strings.Contains(req.Input, "may freely consult the orchestrator") {
			t.Fatalf("input = %q, want architect orchestrator consultation allowance", req.Input)
		}
		if !strings.Contains(req.Input, "do not call a later planned task missing solely because it is absent from the current merged state") {
			t.Fatalf("input = %q, want explicit missing-task guard", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for architect challenge route")
	}
}

func TestFinalizeGlobalReview_CheckpointChallengeUsesProgressiveRequest(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.TargetAgentID != GlobalReviewAgentTester {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(&GlobalReviewSnapshot{ReviewID: "review-3"}, GlobalReviewMetadata(map[string]any{
		"global_review_stage":      "checkpoint",
		"workflow_total_tasks":     4,
		"workflow_completed_tasks": 2,
		"workflow_remaining_tasks": 2,
	}, &GlobalReviewSnapshot{ReviewID: "review-3"})))
	ctx = WithStreamContext(ctx, "corr-review-3", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentInspector },
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	})
	resultAny, err := invokeGlobalReviewSkill(t, ctx, skills, "finalize_global_review", map[string]any{
		"summary": "Current merged checkpoint looks healthy enough for tester challenge.",
	})
	if err != nil {
		t.Fatalf("finalize_global_review: %v", err)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", resultAny)
	}
	if result["target_agent"] != GlobalReviewAgentTester {
		t.Fatalf("target_agent = %#v, want %q", result["target_agent"], GlobalReviewAgentTester)
	}
	action := GlobalReviewStateFromContext(ctx).TerminalAction()
	if action == nil {
		t.Fatal("expected terminal action to be recorded")
	}
	if !strings.Contains(action.Request, "Future planned work that has not been merged yet is pending, not missing") {
		t.Fatalf("request = %q, want checkpoint-aware request", action.Request)
	}
	select {
	case req := <-reqCh:
		if !strings.Contains(req.Input, "Review stage: checkpoint") {
			t.Fatalf("input = %q, want review stage context", req.Input)
		}
		if !strings.Contains(req.Input, "Future planned work that has not been merged yet is pending, not missing.") {
			t.Fatalf("input = %q, want checkpoint pending guidance", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for finalize_global_review challenge route")
	}
}

// TestFinalizeGlobalReview_TesterRouteIsHandoffNotChallenge locks in the
// pipeline-symmetry fix: the inspector → tester audit-closure leg of
// finalize_global_review must (a) carry action.Type=Handoff (with
// CreatesChallenge=true), (b) stamp the finalize-marker on References, and
// (c) route on the wire as "finalize_global_review" rather than
// "challenge_global_tester" so the chat panel and responder prompt can
// distinguish the closure-round handoff from a peer-targeted challenge.
func TestFinalizeGlobalReview_TesterRouteIsHandoffNotChallenge(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.TargetAgentID != GlobalReviewAgentTester {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	ctx := WithGlobalReviewState(context.Background(), NewGlobalReviewState(&GlobalReviewSnapshot{ReviewID: "review-finalize-handoff"}, GlobalReviewMetadata(map[string]any{
		"global_review_stage":      "checkpoint",
		"workflow_total_tasks":     2,
		"workflow_completed_tasks": 1,
		"workflow_remaining_tasks": 1,
	}, &GlobalReviewSnapshot{ReviewID: "review-finalize-handoff"})))
	ctx = WithStreamContext(ctx, "corr-review-finalize-handoff", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentInspector },
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-finalize-handoff" },
		},
	})
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "finalize_global_review", map[string]any{
		"summary":       "Closure-round verification ready for the global tester.",
		"evidence_refs": []string{"docs/review.md"},
	}); err != nil {
		t.Fatalf("finalize_global_review: %v", err)
	}

	action := GlobalReviewStateFromContext(ctx).TerminalAction()
	if action == nil {
		t.Fatal("expected terminal action to be recorded")
	}
	if action.Type != GlobalReviewActionHandoff {
		t.Fatalf("action.Type = %q, want %q (audit-closure leg must be a Handoff with CreatesChallenge=true, mirroring pipeline finalize_pipeline)", action.Type, GlobalReviewActionHandoff)
	}
	if !action.CreatesChallenge {
		t.Fatal("action.CreatesChallenge = false, want true (branch ref + audit lock + validate_work return path are still needed)")
	}
	if !containsNormalizedString(action.References, finalizeGlobalReviewVerificationReference) {
		t.Fatalf("action.References = %v, want to contain %q marker", action.References, finalizeGlobalReviewVerificationReference)
	}
	if got := globalReviewToolNameForAction(action); got != "finalize_global_review" {
		t.Fatalf("globalReviewToolNameForAction = %q, want %q", got, "finalize_global_review")
	}

	snapshot := GlobalReviewStateFromContext(ctx).Snapshot()
	if !finalizeGlobalReviewChallengePending(snapshot) {
		t.Fatal("finalizeGlobalReviewChallengePending = false, want true after recording the closure handoff")
	}

	select {
	case req := <-reqCh:
		if !strings.Contains(req.Input, "audit-closure handoff to the global tester") {
			t.Fatalf("input = %q, want audit-closure handoff prompt language", req.Input)
		}
		if strings.Contains(req.Input, "This is a targeted challenge turn.") {
			t.Fatalf("input = %q, must not include the generic targeted-challenge prompt", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for finalize_global_review handoff route")
	}
}

func TestFinalizeGlobalReview_CheckpointRequiresAcceptAfterAcceptedTesterValidation(t *testing.T) {
	state := NewGlobalReviewState(&GlobalReviewSnapshot{ReviewID: "review-2"}, nil)
	state.addProcessedValidation(GlobalReviewValidationProcessing{
		ChallengeID: "review-2-challenge-1",
		AgentType:   GlobalReviewAgentInspector,
		Decision:    GlobalReviewValidationDecisionAccept,
		Summary:     "Tester-backed global review passed.",
		Validation: &GlobalReviewValidationRecord{
			ChallengeID:     "review-2-challenge-1",
			RequestingAgent: GlobalReviewAgentInspector,
			RespondingAgent: GlobalReviewAgentTester,
			Status:          string(GlobalReviewValidationPassed),
			Summary:         "Merged state passed the tester's adversarial validation.",
		},
	})
	ctx := WithGlobalReviewState(context.Background(), state)

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentInspector },
	})
	resultAny, err := invokeGlobalReviewSkill(t, ctx, skills, "finalize_global_review", map[string]any{
		"summary": "Final merged review is ready to commit.",
	})
	if err != nil {
		t.Fatalf("finalize_global_review: %v", err)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", resultAny)
	}
	if result["must_accept_checkpoint"] != true {
		t.Fatalf("must_accept_checkpoint = %#v, want true", result["must_accept_checkpoint"])
	}
	if err := ValidateGlobalReviewCompletion(ctx, GlobalReviewAgentInspector); err == nil {
		t.Fatal("expected completion guard to require accept_checkpoint")
	}
}

func TestFinalizeGlobalReview_FinalStageRequiresCommitAfterAcceptedTesterValidation(t *testing.T) {
	state := NewGlobalReviewState(&GlobalReviewSnapshot{ReviewID: "review-final"}, GlobalReviewMetadata(map[string]any{
		"global_review_stage": "final",
	}, &GlobalReviewSnapshot{ReviewID: "review-final"}))
	state.addProcessedValidation(GlobalReviewValidationProcessing{
		ChallengeID: "review-final-challenge-1",
		AgentType:   GlobalReviewAgentInspector,
		Decision:    GlobalReviewValidationDecisionAccept,
		Summary:     "Tester-backed final global review passed.",
		Validation: &GlobalReviewValidationRecord{
			ChallengeID:     "review-final-challenge-1",
			RequestingAgent: GlobalReviewAgentInspector,
			RespondingAgent: GlobalReviewAgentTester,
			Status:          string(GlobalReviewValidationPassed),
			Summary:         "Merged state passed the tester's adversarial validation.",
		},
	})
	ctx := WithGlobalReviewState(context.Background(), state)

	skills := NewGlobalReviewProtocolSkills(GlobalReviewProtocolSkillConfig{
		AgentType: func() string { return GlobalReviewAgentInspector },
	})
	resultAny, err := invokeGlobalReviewSkill(t, ctx, skills, "finalize_global_review", map[string]any{
		"summary": "Final merged review is ready to commit.",
	})
	if err != nil {
		t.Fatalf("finalize_global_review: %v", err)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", resultAny)
	}
	if result["must_commit_to_disk"] != true {
		t.Fatalf("must_commit_to_disk = %#v, want true", result["must_commit_to_disk"])
	}
	if err := ValidateGlobalReviewCompletion(ctx, GlobalReviewAgentInspector); err == nil {
		t.Fatal("expected completion guard to require commit_to_disk")
	}
}

func invokeGlobalReviewSkill(t *testing.T, ctx context.Context, skills []*skills.Skill, name string, payload any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	for _, skill := range skills {
		if skill.Name != name {
			continue
		}
		return skill.Handler(ctx, raw)
	}
	t.Fatalf("skill %s not found", name)
	return nil, nil
}

type stubGlobalReviewProtocolWorkspaceViews struct {
	summary *versioning.WorkspaceSummary
}

func (s stubGlobalReviewProtocolWorkspaceViews) ReadFile(context.Context, versioning.WorkspaceView, string, string) ([]byte, error) {
	return nil, nil
}

func (s stubGlobalReviewProtocolWorkspaceViews) Glob(context.Context, versioning.WorkspaceView, string, string, []string, string) ([]string, error) {
	return nil, nil
}

func (s stubGlobalReviewProtocolWorkspaceViews) Grep(context.Context, versioning.WorkspaceView, string, string, string, int, int, string) ([]versioning.GrepMatch, error) {
	return nil, nil
}

func (s stubGlobalReviewProtocolWorkspaceViews) InspectPath(context.Context, string, string) (*versioning.WorkspacePathState, error) {
	return nil, nil
}

func (s stubGlobalReviewProtocolWorkspaceViews) SummarizePaths(context.Context, []string, string) (*versioning.WorkspaceSummary, error) {
	return s.summary, nil
}

func (s stubGlobalReviewProtocolWorkspaceViews) DefaultView() versioning.WorkspaceView {
	return versioning.WorkspaceViewGlobal
}

// TestGlobalReviewStateFingerprint_MovesWithWorkspaceContent confirms the
// content-derived fingerprint actually changes when the live review surface
// changes — the property that the old metadata-derived fingerprint lacked.
func TestGlobalReviewStateFingerprint_MovesWithWorkspaceContent(t *testing.T) {
	baseSnapshot := &GlobalReviewSnapshot{ReviewID: "review-move"}
	metadata := GlobalReviewMetadata(map[string]any{
		"global_review_stage": "checkpoint",
		"affected_files":      []any{"src/app.go"},
	}, baseSnapshot)
	state := NewGlobalReviewState(baseSnapshot, metadata)
	ctx := WithGlobalReviewState(context.Background(), state)

	originalViews := stubGlobalReviewProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:        versioning.WorkspaceViewGlobal,
			SourceOfTruth:      versioning.WorkspaceViewGlobal,
			Paths:              []string{"src/app.go"},
			GlobalChangedPaths: []string{"src/app.go"},
		},
	}
	cfg := GlobalReviewProtocolSkillConfig{
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return originalViews },
	}
	first := resolveGlobalReviewSelectionStateFingerprint(ctx, cfg, state)
	if first == "" {
		t.Fatal("expected non-empty initial fingerprint")
	}

	// Same call again produces the same fingerprint (determinism).
	if same := resolveGlobalReviewSelectionStateFingerprint(ctx, cfg, state); same != first {
		t.Fatalf("stable fingerprint expected: got %q, want %q", same, first)
	}

	// Content change → fingerprint moves. A different summary represents a
	// real file edit on the review surface.
	changedViews := stubGlobalReviewProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:        versioning.WorkspaceViewGlobal,
			SourceOfTruth:      versioning.WorkspaceViewGlobal,
			Paths:              []string{"src/app.go", "src/cli.go"},
			GlobalChangedPaths: []string{"src/app.go", "src/cli.go"},
		},
	}
	cfg.WorkspaceViews = func() versioning.WorkspaceViewAccess { return changedViews }
	second := resolveGlobalReviewSelectionStateFingerprint(ctx, cfg, state)
	if second == "" {
		t.Fatal("expected non-empty fingerprint after workspace change")
	}
	if second == first {
		t.Fatalf("fingerprint did not move with workspace content change: %q", second)
	}

	// No evidence paths → empty fingerprint (degenerate safety rather than
	// a stable-but-meaningless value).
	emptyMeta := GlobalReviewMetadata(map[string]any{
		"global_review_stage": "checkpoint",
	}, baseSnapshot)
	emptyState := NewGlobalReviewState(baseSnapshot, emptyMeta)
	if fp := resolveGlobalReviewSelectionStateFingerprint(
		WithGlobalReviewState(context.Background(), emptyState),
		cfg, emptyState,
	); fp != "" {
		t.Fatalf("expected empty fingerprint when no evidence paths, got %q", fp)
	}
}

// TestGlobalReviewChallengeRefusedOnIdenticalRequestText confirms the
// identical-text refusal fires when the LLM re-challenges with the exact
// prior request body even if the workspace *did* change (so the
// state-fingerprint check wouldn't fire on its own).
func TestGlobalReviewChallengeRefusedOnIdenticalRequestText(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	requestText := "Clarify whether the plan should continue using a root pyproject.toml."
	views := stubGlobalReviewProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:        versioning.WorkspaceViewGlobal,
			SourceOfTruth:      versioning.WorkspaceViewGlobal,
			Paths:              []string{"pyproject.toml"},
			GlobalChangedPaths: []string{"pyproject.toml"},
		},
	}
	baseSnapshot := &GlobalReviewSnapshot{ReviewID: "review-text"}
	metadata := GlobalReviewMetadata(map[string]any{
		"global_review_stage": "checkpoint",
		"affected_files":      []any{"pyproject.toml"},
	}, baseSnapshot)
	cfg := GlobalReviewProtocolSkillConfig{
		AgentType:      func() string { return GlobalReviewAgentInspector },
		AgentID:        func() string { return "inspector-global-1" },
		ResolveTarget:  func(agentType string) string { return agentType },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	}
	// Prior challenge event with an *obsolete* state fingerprint so the
	// state-based refusal cannot fire; only the text match should trigger.
	prior := GlobalReviewEvent{
		Type:               string(GlobalReviewActionChallenge),
		AgentType:          GlobalReviewAgentInspector,
		TargetAgent:        GlobalReviewAgentArchitect,
		Summary:            requestText,
		StateFingerprint:   "stale-fp-from-earlier-round",
		RequestFingerprint: "stale-request-fp",
		CreatesChallenge:   true,
	}
	snapshot := &GlobalReviewSnapshot{ReviewID: "review-text", RecentEvents: []GlobalReviewEvent{prior}}
	state := NewGlobalReviewState(snapshot, metadata)
	ctx := WithGlobalReviewState(context.Background(), state)
	ctx = WithStreamContext(ctx, "corr-text", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(cfg)
	result, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_architect", map[string]any{
		"reason":  "Same concern, fresh file state.",
		"request": requestText, // byte-identical
	})
	if err != nil {
		t.Fatalf("challenge_architect error = %v", err)
	}
	m, _ := result.(map[string]any)
	if m == nil || m["refused"] != true {
		t.Fatalf("result = %#v, want refused=true on identical request text", result)
	}
	reason, _ := m["reason"].(string)
	if !strings.Contains(reason, "exact request text") {
		t.Fatalf("reason = %q, want identical-text guidance", reason)
	}
}

// TestGlobalReviewAuditLock_RefusesPeerChallengeToInspectorDuringFinalize
// confirms the one-way audit-phase lock blocks architect/tester/orchestrator
// from pulling the inspector back into mid-cycle audit work while the
// inspector is in finalize_global_review.
func TestGlobalReviewAuditLock_RefusesPeerChallengeToInspectorDuringFinalize(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	views := stubGlobalReviewProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:        versioning.WorkspaceViewGlobal,
			SourceOfTruth:      versioning.WorkspaceViewGlobal,
			Paths:              []string{"src/app.go"},
			GlobalChangedPaths: []string{"src/app.go"},
		},
	}
	baseSnapshot := &GlobalReviewSnapshot{
		ReviewID: "review-lock",
		AuditLock: &GlobalReviewAuditLock{
			OwnerAgent: GlobalReviewAgentInspector,
			Phase:      GlobalReviewAuditPhaseFinalizing,
			Reason:     "Inspector is finalizing the whole-plan review.",
		},
	}
	metadata := GlobalReviewMetadata(map[string]any{
		"global_review_stage": "final",
		"affected_files":      []any{"src/app.go"},
	}, baseSnapshot)
	cfg := GlobalReviewProtocolSkillConfig{
		AgentType:      func() string { return GlobalReviewAgentTester },
		AgentID:        func() string { return "tester-global-1" },
		ResolveTarget:  func(agentType string) string { return agentType },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	}
	state := NewGlobalReviewState(baseSnapshot, metadata)
	ctx := WithGlobalReviewState(context.Background(), state)
	ctx = WithStreamContext(ctx, "corr-lock", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(cfg)
	result, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_inspector", map[string]any{
		"reason":  "Tester wants to bounce back to the inspector mid-finalize.",
		"request": "Please re-audit the merged state before committing.",
	})
	if err != nil {
		t.Fatalf("challenge_inspector error = %v", err)
	}
	m, _ := result.(map[string]any)
	if m == nil || m["refused"] != true {
		t.Fatalf("result = %#v, want refused=true", result)
	}
	if got, _ := m["refused_by"].(string); got != GlobalReviewAgentInspector {
		t.Fatalf("refused_by = %q, want inspector", got)
	}
	if got, _ := m["audit_phase"].(string); got != GlobalReviewAuditPhaseFinalizing {
		t.Fatalf("audit_phase = %q, want %q", got, GlobalReviewAuditPhaseFinalizing)
	}
}

// TestGlobalReviewAuditLock_AllowsInspectorChallengeDuringFinalize confirms
// the lock is one-way: the inspector can still challenge peers even when its
// own finalize lock is engaged. Without this the final-audit-fail branch
// ("challenge the global tester if bugs found") would be dead.
func TestGlobalReviewAuditLock_AllowsInspectorChallengeDuringFinalize(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	capturedRequests := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok && req != nil {
			select {
			case capturedRequests <- req:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	views := stubGlobalReviewProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:        versioning.WorkspaceViewGlobal,
			SourceOfTruth:      versioning.WorkspaceViewGlobal,
			Paths:              []string{"src/app.go"},
			GlobalChangedPaths: []string{"src/app.go"},
		},
	}
	baseSnapshot := &GlobalReviewSnapshot{
		ReviewID: "review-lock-inspector",
		AuditLock: &GlobalReviewAuditLock{
			OwnerAgent: GlobalReviewAgentInspector,
			Phase:      GlobalReviewAuditPhaseFinalizing,
		},
	}
	metadata := GlobalReviewMetadata(map[string]any{
		"global_review_stage": "final",
		"affected_files":      []any{"src/app.go"},
	}, baseSnapshot)
	cfg := GlobalReviewProtocolSkillConfig{
		AgentType:      func() string { return GlobalReviewAgentInspector },
		AgentID:        func() string { return "inspector-global-1" },
		ResolveTarget:  func(agentType string) string { return agentType },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
		Route: GlobalReviewRouteConfig{
			Bus:       bus,
			SessionID: func() string { return "sess-1" },
		},
	}
	state := NewGlobalReviewState(baseSnapshot, metadata)
	ctx := WithGlobalReviewState(context.Background(), state)
	ctx = WithStreamContext(ctx, "corr-lock-inspector", "orchestrator")

	skills := NewGlobalReviewProtocolSkills(cfg)
	result, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_global_tester", map[string]any{
		"reason":  "Final audit surfaced a regression; tester must re-verify.",
		"request": "Re-run the merged-state test suite and confirm the regression is resolved.",
	})
	if err != nil {
		t.Fatalf("challenge_global_tester error = %v", err)
	}
	m, _ := result.(map[string]any)
	if m == nil || m["selected"] != true {
		t.Fatalf("result = %#v, want selected=true (inspector → tester allowed during lock)", result)
	}
	if m["refused"] == true {
		t.Fatalf("inspector-originated challenge refused under its own lock: %#v", result)
	}
	select {
	case <-capturedRequests:
	case <-time.After(time.Second):
		t.Fatal("expected route request dispatched for inspector → tester")
	}
}

// TestNextGlobalReviewAuditLock_FinalizeSetsLock_InspectorContinuationClearsIt
// pins the AuditLock transition rules as a unit: a finalize challenge sets
// the lock, an inspector-originated non-finalize action clears it, a
// peer-originated action leaves it untouched.
func TestNextGlobalReviewAuditLock_Transitions(t *testing.T) {
	// Finalize challenge sets lock.
	locked := nextGlobalReviewAuditLock(nil, &GlobalReviewTurnAction{
		Type:             GlobalReviewActionChallenge,
		AgentType:        GlobalReviewAgentInspector,
		AuditLockPhase:   GlobalReviewAuditPhaseFinalizing,
		Reason:           "Finalizing the whole-plan review.",
		CreatesChallenge: true,
	})
	if locked == nil || locked.OwnerAgent != GlobalReviewAgentInspector || locked.Phase != GlobalReviewAuditPhaseFinalizing {
		t.Fatalf("finalize should set lock, got %#v", locked)
	}

	// Inspector-originated ordinary action clears the lock.
	cleared := nextGlobalReviewAuditLock(locked, &GlobalReviewTurnAction{
		Type:      GlobalReviewActionChallenge,
		AgentType: GlobalReviewAgentInspector,
	})
	if cleared != nil {
		t.Fatalf("inspector-originated action should clear lock, got %#v", cleared)
	}

	// Peer-originated action preserves the lock (peer cannot unilaterally clear).
	preserved := nextGlobalReviewAuditLock(locked, &GlobalReviewTurnAction{
		Type:      GlobalReviewActionChallenge,
		AgentType: GlobalReviewAgentTester,
	})
	if preserved == nil || preserved.Phase != GlobalReviewAuditPhaseFinalizing {
		t.Fatalf("peer action must preserve lock, got %#v", preserved)
	}
}
