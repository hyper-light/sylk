package shared

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

// TestTesterFinalize_MissingTarget_ErrorIsEnrichedWithState locks the
// exact failure recovery path that previously cascaded a tester into a
// two-turn error sequence before the circuit-breaker halted the
// pipeline. The original bare fmt.Errorf("targets[0].target is
// required") bypassed enrichPipelineProtocolError, so the LLM got no
// state context and no recovery hint. After one confused retry it
// called action=validate, which errored differently, and the
// inspector killed the DAG layer.
//
// With the spec validators returning ProtocolErrors, the dispatcher's
// enrichment wrapper attaches:
//   - current snapshot state (pending_challenge: none)
//   - currently-legal actions (challenge, finalize, handoff)
//   - attempted action tagged as NOT in the legal list when applicable
//   - a pointer at query_pipeline_state for re-inspection
//
// If any layer of this chain breaks, the LLM goes back to flying
// blind on spec errors.
func TestTesterFinalize_MissingTarget_ErrorIsEnrichedWithState(t *testing.T) {
	unified := buildTesterPipelineProtocol(t)
	ctx := testerContextWithNoPendingChallenge()

	// Call action=finalize with a targets entry missing `target` —
	// the exact shape that silently confused the LLM previously.
	input := json.RawMessage(`{
		"action": "finalize",
		"targets": [{"summary": "Verification complete"}]
	}`)
	_, err := unified.Handler(ctx, input)
	if err == nil {
		t.Fatal("expected error for missing target")
	}

	// Must be a typed ProtocolError (so enrichment fired).
	pe, ok := AsProtocolError(err)
	if !ok {
		t.Fatalf("expected ProtocolError for enrichment, got %T: %v", err, err)
	}
	if pe.RuleID != "pipeline.finalize.target_required" {
		t.Fatalf("rule = %q, want pipeline.finalize.target_required", pe.RuleID)
	}

	// Recovery must include the state + legal-actions enrichment.
	recovery := pe.RecoveryAction
	mustContain := []string{
		"Current pipeline state:",
		"pending_challenge: none",
		"Currently legal actions:",
		"challenge",
		"finalize",
		"handoff",
		"query_pipeline_state",
	}
	for _, want := range mustContain {
		if !strings.Contains(recovery, want) {
			t.Errorf("recovery missing %q\n\nrecovery:\n%s", want, recovery)
		}
	}
}

// TestTesterFinalize_EmptyTargets_CaughtByPreValidator verifies the
// empty-targets case short-circuits at the façade pre-validator layer
// BEFORE reaching our spec validator. The pre-validator already emits
// a schema-echoing error with the expected shape, so enrichment isn't
// needed here — this test pins that behavior so a future change
// doesn't accidentally route empty targets through the runtime path
// and strip the shape hint.
func TestTesterFinalize_EmptyTargets_CaughtByPreValidator(t *testing.T) {
	unified := buildTesterPipelineProtocol(t)
	ctx := testerContextWithNoPendingChallenge()

	_, err := unified.Handler(ctx, json.RawMessage(`{"action":"finalize","targets":[]}`))
	if err == nil {
		t.Fatal("expected error for empty targets")
	}
	msg := err.Error()
	// Pre-validator error carries the tool name, action, and expected
	// shape of the targets field so the LLM can self-correct on the
	// next turn without a state lookup.
	for _, want := range []string{
		`tool "pipeline_protocol"`,
		`action=finalize`,
		"targets",
		"Expected shape for this action",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("pre-validator error missing %q\n\nerror: %s", want, msg)
		}
	}
}

// TestTesterFinalize_ProtocolErrorsRetainChainIntegrity verifies the
// errors.As contract the dispatcher relies on. If this breaks, the
// façade's pre-validation wrapping or downstream observer code that
// type-asserts on ProtocolError silently stops matching.
func TestTesterFinalize_ProtocolErrorsRetainChainIntegrity(t *testing.T) {
	err := requireExplicitFinalizeTargets([]PipelineTesterFinalizeTargetSpec{{Summary: "x"}})
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As(ProtocolError) failed on %T: %v", err, err)
	}
	if pe.Scope != "pipeline" {
		t.Fatalf("scope = %q, want pipeline", pe.Scope)
	}
}

// buildTesterPipelineProtocol constructs a tester-pipeline
// pipeline_protocol façade with the minimum config needed for the
// finalize action to dispatch.
func buildTesterPipelineProtocol(t *testing.T) *skills.Skill {
	t.Helper()
	cfg := PipelineProtocolSkillConfig{
		AgentType: func() string { return PipelineAgentTester },
		AgentID:   func() string { return "tester-1" },
		TesterFinalize: func(context.Context, string, []PipelineTesterFinalizeTargetSpec) ([]PipelineHandoffArtifactRef, error) {
			return nil, nil
		},
		TesterCurrentSuiteID: func() string { return "suite-1" },
	}
	for _, s := range PipelineProtocolSkills(cfg) {
		if s.Name == "pipeline_protocol" {
			return s
		}
	}
	t.Fatal("pipeline_protocol skill not found")
	return nil
}

func testerContextWithNoPendingChallenge() context.Context {
	snapshot := &PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentTester},
			{AgentType: PipelineAgentEngineer},
			{AgentType: PipelineAgentInspector},
		},
	}
	return WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(snapshot))
}
