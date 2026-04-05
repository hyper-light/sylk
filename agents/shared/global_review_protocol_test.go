package shared

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
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
				"challenge_agent",
				"handoff_next",
				"validate_work",
				"process_validation",
				"finalize_global_review",
				"accept_checkpoint",
				"commit_to_disk",
			},
		},
		{
			name:      "tester mirrors pipeline handoff and challenge mechanics",
			agentType: GlobalReviewAgentTester,
			want:      []string{"challenge_agent", "handoff_next", "validate_work", "process_validation"},
		},
		{
			name:      "architect only validates",
			agentType: GlobalReviewAgentArchitect,
			want:      []string{"validate_work"},
		},
		{
			name:      "orchestrator only validates",
			agentType: GlobalReviewAgentOrchestrator,
			want:      []string{"validate_work"},
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
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_agent", map[string]any{
		"target_agents": []string{GlobalReviewAgentOrchestrator},
		"reason":        "Need authoritative workflow progress before deciding whether the checkpoint is on track.",
		"request":       "Report DAG/workflow progress, remaining tasks, and any blockers for the current merged checkpoint.",
	}); err != nil {
		t.Fatalf("challenge_agent orchestrator: %v", err)
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
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_agent", map[string]any{
		"target_agents": []string{GlobalReviewAgentTester},
		"reason":        "Need merged-state adversarial validation.",
		"request":       "Run the full tester audit against the merged plan.",
	}); err != nil {
		t.Fatalf("challenge_agent tester: %v", err)
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
	if _, err := invokeGlobalReviewSkill(t, ctx, skills, "challenge_agent", map[string]any{
		"target_agents": []string{GlobalReviewAgentArchitect},
		"reason":        "Need plan-level clarification for this checkpoint review.",
		"request":       "Explain whether the current checkpoint is still aligned with the plan.",
	}); err != nil {
		t.Fatalf("challenge_agent architect: %v", err)
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
