package purevfs

import (
	"errors"
	"testing"
)

func TestExecutionPlannerStrictRequiresBroker(t *testing.T) {
	planner := NewExecutionPlanner(nil, HostCompatibilityCapabilities())

	_, err := planner.Plan(ExecutionRequest{
		Mode:          ExecutionModeStrictNoDisk,
		Intent:        ExecutionIntentTest,
		Language:      "go",
		FrameworkID:   "go-test",
		WorkspaceRoot: "/workspace",
	})
	if !errors.Is(err, ErrStrictExecutionUnavailable) {
		t.Fatalf("Plan error = %v, want %v", err, ErrStrictExecutionUnavailable)
	}
}

func TestExecutionPlannerCompatibilityUsesGoOverlayFastPath(t *testing.T) {
	planner := NewExecutionPlanner(nil, HostCompatibilityCapabilities())

	plan, err := planner.Plan(ExecutionRequest{
		Mode:           ExecutionModeCompatibility,
		Intent:         ExecutionIntentTest,
		Language:       "go",
		FrameworkID:    "go-test",
		WorkspaceRoot:  "/workspace",
		WorkingDir:     "/workspace",
		Overlay:        true,
		OverlayDeletes: false,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Strategy != StrategyGoOverlayManifest {
		t.Fatalf("strategy = %s, want %s", plan.Strategy, StrategyGoOverlayManifest)
	}
	if plan.RequiresMaterialize {
		t.Fatal("expected go overlay fast path to avoid materialization")
	}
}

func TestExecutionPlannerCompatibilityFallsBackToMaterialization(t *testing.T) {
	planner := NewExecutionPlanner(nil, HostCompatibilityCapabilities())

	plan, err := planner.Plan(ExecutionRequest{
		Mode:           ExecutionModeCompatibility,
		Intent:         ExecutionIntentTest,
		Language:       "python",
		FrameworkID:    "pytest",
		WorkspaceRoot:  "/workspace",
		WorkingDir:     "/workspace",
		Overlay:        true,
		OverlayDeletes: true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Strategy != StrategyWorkspaceMaterialize {
		t.Fatalf("strategy = %s, want %s", plan.Strategy, StrategyWorkspaceMaterialize)
	}
	if !plan.RequiresMaterialize {
		t.Fatal("expected materialization fallback")
	}
}

func TestExecutionPlannerGenericCommandPlan(t *testing.T) {
	planner := NewExecutionPlanner(nil, HostCompatibilityCapabilities())
	root := t.TempDir()

	plan, err := planner.Plan(ExecutionRequest{
		Mode:          ExecutionModeCompatibility,
		Intent:        ExecutionIntentCommand,
		Language:      "generic",
		WorkspaceRoot: root,
		WorkingDir:    "scripts",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Language != "generic" {
		t.Fatalf("language = %q, want generic", plan.Language)
	}
	if plan.Strategy != StrategyDirectPassthrough {
		t.Fatalf("strategy = %s, want %s", plan.Strategy, StrategyDirectPassthrough)
	}
	if got := plan.Env["TMPDIR"]; got != "/tmp" {
		t.Fatalf("TMPDIR = %q, want /tmp", got)
	}
}
