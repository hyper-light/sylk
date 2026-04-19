package shared

import (
	"context"
	"strings"
	"testing"

	coreSkillPkg "github.com/adalundhe/sylk/core/skills"
)

type coreSkill = coreSkillPkg.Skill

func newTesterFinalizeTestState(t *testing.T) (*PipelineProtocolState, context.Context) {
	t.Helper()
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{})
	ctx := WithPipelineProtocolState(context.Background(), state)
	contract := &TaskExecutionContract{RuntimeAgentType: PipelineAgentTester}
	ctx = WithTaskExecutionContract(ctx, contract)
	return state, ctx
}

func newTesterFinalizeCfg(suiteID string, refs []PipelineHandoffArtifactRef) PipelineProtocolSkillConfig {
	return PipelineProtocolSkillConfig{
		AgentType:            func() string { return PipelineAgentTester },
		AgentID:              func() string { return "tester-1" },
		TesterCurrentSuiteID: func() string { return suiteID },
		TesterFinalize: func(ctx context.Context, suite string, specs []PipelineTesterFinalizeTargetSpec) ([]PipelineHandoffArtifactRef, error) {
			out := make([]PipelineHandoffArtifactRef, len(specs))
			for i, spec := range specs {
				ref := refs[i]
				ref.Target = spec.Target
				ref.SuiteID = suite
				out[i] = ref
			}
			return out, nil
		},
	}
}

func TestTesterFinalize_RegistrationGatedByCallback(t *testing.T) {
	without := PipelineProtocolSkills(PipelineProtocolSkillConfig{})
	if containsSkillNamed(without, "finalize_pipeline") {
		t.Fatal("finalize_pipeline must not be registered when TesterFinalize is nil")
	}
	cfg := newTesterFinalizeCfg("suite-1", []PipelineHandoffArtifactRef{{ArtifactID: "a-1"}})
	with := PipelineProtocolSkills(cfg)
	if !containsSkillNamed(with, "finalize_pipeline") {
		t.Fatal("finalize_pipeline must be registered when TesterFinalize is set")
	}
}

func TestTesterFinalize_RejectsNonTesterCallers(t *testing.T) {
	cfg := newTesterFinalizeCfg("suite-1", []PipelineHandoffArtifactRef{{ArtifactID: "a-1"}})
	cfg.AgentType = func() string { return PipelineAgentEngineer }
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{})
	ctx := WithPipelineProtocolState(context.Background(), state)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentEngineer})
	skill := pipelineTesterFinalizePipelineSkill(cfg)

	_, err := skill.Handler(ctx, []byte(`{"targets":[{"target":"engineer","summary":"x"}]}`))
	if err == nil || !strings.Contains(err.Error(), "tester") {
		t.Fatalf("expected tester-only rejection, got %v", err)
	}
}

func TestTesterFinalize_RequiresFreshSuite(t *testing.T) {
	cfg := newTesterFinalizeCfg("", []PipelineHandoffArtifactRef{{ArtifactID: "a-1"}})
	_, ctx := newTesterFinalizeTestState(t)
	skill := pipelineTesterFinalizePipelineSkill(cfg)

	_, err := skill.Handler(ctx, []byte(`{"targets":[{"target":"engineer","summary":"x"}]}`))
	if err == nil || !strings.Contains(err.Error(), "run_test_suite") {
		t.Fatalf("expected fresh-suite refusal, got %v", err)
	}
}

func TestTesterFinalize_QueuesArtifactsForCohort(t *testing.T) {
	cfg := newTesterFinalizeCfg("suite-1", []PipelineHandoffArtifactRef{
		{ArtifactID: "a-eng"}, {ArtifactID: "a-des"},
	})
	state, ctx := newTesterFinalizeTestState(t)
	skill := pipelineTesterFinalizePipelineSkill(cfg)

	_, err := skill.Handler(ctx, []byte(`{"targets":[
		{"target":"engineer","summary":"engineer slice"},
		{"target":"designer","summary":"designer slice"}
	]}`))
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	queue := state.QueuedArtifacts()
	if len(queue) != 2 {
		t.Fatalf("queue len = %d, want 2", len(queue))
	}
	if ref, ok := queue[PipelineAgentEngineer]; !ok || ref.ArtifactID != "a-eng" {
		t.Fatalf("engineer queued ref = %#v", queue[PipelineAgentEngineer])
	}
	if ref, ok := queue[PipelineAgentDesigner]; !ok || ref.ArtifactID != "a-des" {
		t.Fatalf("designer queued ref = %#v", queue[PipelineAgentDesigner])
	}
}

func TestTesterFinalize_RefusesRepeatForSameSuite(t *testing.T) {
	cfg := newTesterFinalizeCfg("suite-1", []PipelineHandoffArtifactRef{{ArtifactID: "a-eng"}})
	_, ctx := newTesterFinalizeTestState(t)
	skill := pipelineTesterFinalizePipelineSkill(cfg)

	if _, err := skill.Handler(ctx, []byte(`{"targets":[{"target":"engineer","summary":"first"}]}`)); err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	_, err := skill.Handler(ctx, []byte(`{"targets":[{"target":"engineer","summary":"second"}]}`))
	if err == nil || !strings.Contains(err.Error(), "fresh evidence") {
		t.Fatalf("expected no-repeat refusal for same suite, got %v", err)
	}
}

func TestTesterFinalize_AllowsRefinalizeForNewSuite(t *testing.T) {
	suite := "suite-1"
	cfg := newTesterFinalizeCfg(suite, []PipelineHandoffArtifactRef{{ArtifactID: "a-eng-1"}})
	cfg.TesterCurrentSuiteID = func() string { return suite }
	state, ctx := newTesterFinalizeTestState(t)
	skill := pipelineTesterFinalizePipelineSkill(cfg)

	if _, err := skill.Handler(ctx, []byte(`{"targets":[{"target":"engineer","summary":"first"}]}`)); err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	suite = "suite-2"
	cfg.TesterFinalize = func(ctx context.Context, s string, specs []PipelineTesterFinalizeTargetSpec) ([]PipelineHandoffArtifactRef, error) {
		return []PipelineHandoffArtifactRef{{ArtifactID: "a-eng-2", Target: specs[0].Target, SuiteID: s}}, nil
	}
	skill = pipelineTesterFinalizePipelineSkill(cfg)
	if _, err := skill.Handler(ctx, []byte(`{"targets":[{"target":"engineer","summary":"second"}]}`)); err != nil {
		t.Fatalf("refinalize after fresh suite: %v", err)
	}
	queue := state.QueuedArtifacts()
	if ref := queue[PipelineAgentEngineer]; ref.ArtifactID != "a-eng-2" || ref.SuiteID != "suite-2" {
		t.Fatalf("engineer queued ref after refinalize = %#v", ref)
	}
}

func TestTesterFinalize_ChallengeResponseRequiresChallengerOnly(t *testing.T) {
	cfg := newTesterFinalizeCfg("suite-1", []PipelineHandoffArtifactRef{{ArtifactID: "a-ins"}})
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:                "ch-1",
			RequestingAgent:   PipelineAgentInspector,
			RequestingAgentID: "inspector-1",
		},
	})
	ctx := WithPipelineProtocolState(context.Background(), state)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentTester})
	skill := pipelineTesterFinalizePipelineSkill(cfg)

	_, err := skill.Handler(ctx, []byte(`{"targets":[
		{"target":"engineer","summary":"x"},
		{"target":"designer","summary":"y"}
	]}`))
	if err == nil || !strings.Contains(err.Error(), "pending challenge") {
		t.Fatalf("expected challenge-mode constraint refusal, got %v", err)
	}
	if _, err := skill.Handler(ctx, []byte(`{"targets":[{"target":"inspector-pipeline","summary":"answer"}]}`)); err != nil {
		t.Fatalf("finalize for challenger: %v", err)
	}
}

func TestQueueValidator_RefusesHandoffMismatch(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{})
	state.queuedArtifacts = map[string]PipelineHandoffArtifactRef{
		PipelineAgentEngineer: {ArtifactID: "a-eng", Target: PipelineAgentEngineer, SuiteID: "suite-1"},
	}
	action := &PipelineTurnAction{
		Type:         PipelineProtocolActionHandoff,
		TargetAgents: []string{PipelineAgentDesigner},
	}
	if err := state.validateTerminalAction(action); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected target-mismatch refusal, got %v", err)
	}
}

func TestQueueValidator_RefusesChallengeWhileQueued(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{})
	state.queuedArtifacts = map[string]PipelineHandoffArtifactRef{
		PipelineAgentEngineer: {ArtifactID: "a-eng", Target: PipelineAgentEngineer},
	}
	action := &PipelineTurnAction{
		Type:             PipelineProtocolActionHandoff,
		CreatesChallenge: true,
		TargetAgents:     []string{PipelineAgentEngineer},
	}
	if err := state.validateTerminalAction(action); err == nil || !strings.Contains(err.Error(), "challenge_agent") {
		t.Fatalf("expected challenge-vs-queue refusal, got %v", err)
	}
}

func TestQueueValidator_AllowsHandoffWhenTargetSetMatches(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{})
	state.queuedArtifacts = map[string]PipelineHandoffArtifactRef{
		PipelineAgentEngineer: {ArtifactID: "a-eng", Target: PipelineAgentEngineer},
		PipelineAgentDesigner: {ArtifactID: "a-des", Target: PipelineAgentDesigner},
	}
	action := &PipelineTurnAction{
		Type:         PipelineProtocolActionHandoff,
		TargetAgents: []string{PipelineAgentDesigner, PipelineAgentEngineer},
	}
	if err := state.validateTerminalAction(action); err != nil {
		t.Fatalf("expected matching cohort to validate, got %v", err)
	}
}

func containsSkillNamed(skills []*coreSkill, name string) bool {
	for _, s := range skills {
		if s != nil && s.Name == name {
			return true
		}
	}
	return false
}
