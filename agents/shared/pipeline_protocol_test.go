package shared

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

func TestPipelineProtocolSkills_RecordTurnActions(t *testing.T) {
	snapshot := &PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
			{AgentType: PipelineAgentEngineer},
		},
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "challenge-1",
			RequestingAgent: PipelineAgentInspector,
			TargetAgents:    []string{PipelineAgentTester},
		},
		PendingValidation: &PipelineValidationRecord{
			ChallengeID:     "challenge-0",
			RequestingAgent: PipelineAgentInspector,
			RespondingAgent: PipelineAgentTester,
			Status:          "passed",
			Summary:         "tests are ready",
		},
	}
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(snapshot))
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		InspectorOT: true,
	})

	runSkill(t, ctx, skills, "process_validation", map[string]any{
		"challenge_id": "challenge-0",
		"decision":     "accept",
		"summary":      "accepted the tester response",
	})
	runSkill(t, ctx, skills, "handoff_next", map[string]any{
		"target_agents": []string{"tester"},
		"reason":        "need follow-up",
		"request":       "Clarify the failing case.",
	})

	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		t.Fatal("pipeline protocol state missing from context")
	}
	if action := state.TerminalAction(); action == nil || action.Type != PipelineProtocolActionHandoff {
		t.Fatalf("terminal action = %#v, want handoff", action)
	}
	if processed := state.ProcessedValidations(); len(processed) != 1 || processed[0].Decision != PipelineValidationDecisionAccept {
		t.Fatalf("processed validations = %#v", processed)
	}
}

func TestValidatePipelineProtocolCompletion_RequiresTurnAction(t *testing.T) {
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(&PipelineProtocolSnapshot{}))
	if err := ValidatePipelineProtocolCompletion(ctx, PipelineAgentEngineer); err == nil {
		t.Fatal("expected missing turn action to fail completion")
	}
}

func runSkill(t *testing.T, ctx context.Context, skills []*skills.Skill, name string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	for _, skill := range skills {
		if skill.Name != name {
			continue
		}
		if _, err := skill.Handler(ctx, raw); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return
	}
	t.Fatalf("skill %s not found", name)
}
