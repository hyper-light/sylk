package orchestrator

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

// TestBuildPipelineClaimTitle_FallbackChain verifies the title falls
// through name → slug → DAG/node id placeholder so the inspector
// always sees a non-empty title even on minimal dispatch contexts.
func TestBuildPipelineClaimTitle_FallbackChain(t *testing.T) {
	cases := []struct {
		name    string
		input   *taskDispatchContext
		want    string
	}{
		{
			name:  "name wins",
			input: &taskDispatchContext{name: "Build CLI", pipelineTaskSlug: "cli", dagID: "d1", nodeID: "n1"},
			want:  "Build CLI",
		},
		{
			name:  "slug wins when name empty",
			input: &taskDispatchContext{pipelineTaskSlug: "create-pyproject", dagID: "d1", nodeID: "n1"},
			want:  "create-pyproject",
		},
		{
			name:  "dag/node placeholder when both empty",
			input: &taskDispatchContext{dagID: "d1", nodeID: "n1"},
			want:  "Pipeline task: d1/n1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildPipelineClaimTitle(tc.input); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildPipelineClaimDescription_PromptIsPrimary verifies the
// engineer-facing prompt content is the first thing the description
// surfaces, before metadata.
func TestBuildPipelineClaimDescription_PromptIsPrimary(t *testing.T) {
	dispatch := &taskDispatchContext{
		prompt:        "Create pyproject.toml and src/hello_cli/__init__.py",
		dagID:         "dag-1",
		nodeID:        "task_1",
		pipelineTaskID: "task_1",
		pipelineStage: "implement",
	}
	got := buildPipelineClaimDescription(dispatch)
	if !strings.HasPrefix(got, "Create pyproject.toml") {
		t.Errorf("description should lead with the prompt; got: %q", got[:min(80, len(got))])
	}
	for _, marker := range []string{"DAG: dag-1", "Node: task_1", "Task: task_1", "Stage: implement"} {
		if !strings.Contains(got, marker) {
			t.Errorf("description should contain %q; got: %s", marker, got)
		}
	}
}

// TestBuildPipelineClaimValidations_AcceptanceCriteriaMapToValidations
// verifies that each acceptance criterion in the dispatch context
// becomes one Validation record. The inspector evaluates each.
func TestBuildPipelineClaimValidations_AcceptanceCriteriaMapToValidations(t *testing.T) {
	dispatch := &taskDispatchContext{
		nodeCtx: map[string]any{
			"acceptance_criteria": []any{
				map[string]any{
					"given":    "the project root",
					"when":     "pyproject.toml is created",
					"then":     "setuptools build-system is declared",
					"category": "test",
				},
				map[string]any{
					"given":    "the package directory exists",
					"when":     "__init__.py is created",
					"then":     "the package exposes __version__",
					"category": "design",
				},
			},
		},
	}
	got := buildPipelineClaimValidations(dispatch)
	if len(got) != 2 {
		t.Fatalf("expected 2 validations, got %d", len(got))
	}
	if got[0].Type != claims.ValidationTypeTest {
		t.Errorf("first validation type: got %v want %v", got[0].Type, claims.ValidationTypeTest)
	}
	if got[1].Type != claims.ValidationTypeDesign {
		t.Errorf("second validation type: got %v want %v", got[1].Type, claims.ValidationTypeDesign)
	}
	if !strings.Contains(got[0].QualityBar, "Given the project root") {
		t.Errorf("quality bar should carry given/when/then chain; got: %s", got[0].QualityBar)
	}
	for _, v := range got {
		if !v.Required {
			t.Errorf("acceptance-criteria validations must be required; %v", v)
		}
		if v.Status != claims.ValidationStatusPending {
			t.Errorf("validations start pending; got %v", v.Status)
		}
	}
}

// TestBuildPipelineClaimValidations_NoCriteriaProvidesReceiptFallback
// guarantees the inspector always has at least one validation to
// evaluate. Without a fallback, a task with no explicit criteria
// would auto-accept on testament submission, which is not the spec.
func TestBuildPipelineClaimValidations_NoCriteriaProvidesReceiptFallback(t *testing.T) {
	dispatch := &taskDispatchContext{nodeCtx: map[string]any{}}
	got := buildPipelineClaimValidations(dispatch)
	if len(got) != 1 {
		t.Fatalf("expected 1 receipt-fallback validation, got %d", len(got))
	}
	if got[0].Type != claims.ValidationTypeReceipt {
		t.Errorf("fallback should be receipt; got %v", got[0].Type)
	}
	if !got[0].Required {
		t.Error("fallback validation must be required")
	}
}

// TestBuildPipelineClaimScope_DerivesFromAffectedFilesAndWorkspace
// verifies scope entries are pulled from both affected_files and
// the workspace.{read_set, write_set, test_surface, prefetch_paths}
// keys, deduplicated.
func TestBuildPipelineClaimScope_DerivesFromAffectedFilesAndWorkspace(t *testing.T) {
	dispatch := &taskDispatchContext{
		nodeCtx: map[string]any{
			"affected_files": []any{
				"pyproject.toml",
				map[string]any{"path": "src/hello_cli/__init__.py"},
			},
			"workspace": map[string]any{
				"read_set":       []any{"docs/CLAUDE.md"},
				"write_set":      []any{"pyproject.toml"}, // dup
				"test_surface":   []any{"tests/test_cli.py"},
				"prefetch_paths": []any{"src/hello_cli/cli.py"},
			},
		},
	}
	got := buildPipelineClaimScope(dispatch)
	wantKeys := map[string]bool{
		"pyproject.toml":             true,
		"src/hello_cli/__init__.py":  true,
		"docs/CLAUDE.md":             true,
		"tests/test_cli.py":          true,
		"src/hello_cli/cli.py":       true,
	}
	if len(got) != len(wantKeys) {
		t.Errorf("expected %d unique scope entries, got %d: %v", len(wantKeys), len(got), got)
	}
	for _, entry := range got {
		if entry.Kind != "file" {
			t.Errorf("scope kind should be 'file'; got %q", entry.Kind)
		}
		if !wantKeys[entry.Key] {
			t.Errorf("unexpected scope key %q", entry.Key)
		}
	}
}

// TestIsPipelineWorkerType_RecognizesPipelineAgents covers the four
// pipeline worker types so the orchestrator wires the inspector as
// evaluator on every dispatched task claim.
func TestIsPipelineWorkerType_RecognizesPipelineAgents(t *testing.T) {
	pipeline := []string{"engineer", "designer", "tester-pipeline", "inspector-pipeline"}
	for _, agentType := range pipeline {
		if !isPipelineWorkerType(agentType) {
			t.Errorf("%q should be recognized as pipeline worker", agentType)
		}
	}
	non := []string{"architect", "guide", "orchestrator", "guardian", "librarian", "academic", ""}
	for _, agentType := range non {
		if isPipelineWorkerType(agentType) {
			t.Errorf("%q should NOT be recognized as pipeline worker", agentType)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
