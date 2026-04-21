package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Phase 2.K / GT-4 + GI-5 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// research_dependency_install + install_dependency_tooling collapsed
// into dependency(action=…). The test-tooling reject gate is now
// enforced inside the handler via InspectorRejectTestDependency*
// helpers rather than the pre-registration skill description. The
// test-routing guidance itself lives in the prompt layer; see
// TestPipelineInspectorSystemPrompt_* for that coverage.

func TestDependencySkill_RejectsTestToolingResearch(t *testing.T) {
	skill := dependencySkill(&PipelineInspector{})
	if skill == nil {
		t.Fatal("expected skill")
	}
	// action=research with a pytest framework id must be rejected by
	// the inspector-side gate.
	input, err := json.Marshal(map[string]any{
		"action":       "research",
		"missing_tool": "pytest",
		"framework_id": "pytest",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected inspector-side gate to reject pytest research")
	}
	if !strings.Contains(err.Error(), "test") {
		t.Fatalf("unexpected error from gate: %v", err)
	}
}

func TestDependencySkill_RejectsTestToolingInstallPlan(t *testing.T) {
	skill := dependencySkill(&PipelineInspector{})
	if skill == nil {
		t.Fatal("expected skill")
	}
	// action=install with a pytest-style step must be rejected by
	// the inspector-side plan validator.
	input, err := json.Marshal(map[string]any{
		"action":  "install",
		"summary": "install pytest",
		"steps": []map[string]any{
			{"command": "pip install pytest", "reason": "runner"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected inspector-side gate to reject pytest install plan")
	}
	if !strings.Contains(err.Error(), "test") {
		t.Fatalf("unexpected error from gate: %v", err)
	}
}
