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

func TestNewGlobalReviewProtocolSkills_AgentSpecificOwnership(t *testing.T) {
	tests := []struct {
		name      string
		agentType string
		want      []string
	}{
		{
			name:      "inspector owns challenge and commit path",
			agentType: GlobalReviewAgentInspector,
			want: []string{
				"challenge_global_tester",
				"challenge_architect",
				"process_global_validation",
				"finalize_global_review",
				"commit_to_disk",
			},
		},
		{
			name:      "tester only validates",
			agentType: GlobalReviewAgentTester,
			want:      []string{"validate_global_review"},
		},
		{
			name:      "architect only validates",
			agentType: GlobalReviewAgentArchitect,
			want:      []string{"validate_global_review"},
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

func TestGlobalReviewChallengePublishesUserVisibleRoute(t *testing.T) {
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
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for global review route request")
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

func TestFinalizeGlobalReview_RequiresCommitAfterAcceptedTesterValidation(t *testing.T) {
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
