package purevfs

import (
	"errors"
	"os"
	"path"
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

	_, err := planner.Plan(ExecutionRequest{
		Mode:           ExecutionModeCompatibility,
		Intent:         ExecutionIntentTest,
		Language:       "go",
		FrameworkID:    "go-test",
		WorkspaceRoot:  "/workspace",
		WorkingDir:     "/workspace",
		Overlay:        true,
		OverlayDeletes: false,
	})
	if !errors.Is(err, ErrStrictExecutionUnavailable) {
		t.Fatalf("Plan error = %v, want %v", err, ErrStrictExecutionUnavailable)
	}
}

func TestExecutionPlannerCompatibilityFallsBackToMaterialization(t *testing.T) {
	planner := NewExecutionPlanner(nil, HostCompatibilityCapabilities())

	_, err := planner.Plan(ExecutionRequest{
		Mode:           ExecutionModeCompatibility,
		Intent:         ExecutionIntentTest,
		Language:       "python",
		FrameworkID:    "pytest",
		WorkspaceRoot:  "/workspace",
		WorkingDir:     "/workspace",
		Overlay:        true,
		OverlayDeletes: true,
	})
	if !errors.Is(err, ErrStrictExecutionUnavailable) {
		t.Fatalf("Plan error = %v, want %v", err, ErrStrictExecutionUnavailable)
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

func TestExecutionPlannerProjectsToolchainsIntoBrokerPath(t *testing.T) {
	planner := NewExecutionPlanner(nil, StrictBrokerCapabilities())

	plan, err := planner.Plan(ExecutionRequest{
		Mode:          ExecutionModeStrictNoDisk,
		Intent:        ExecutionIntentBuild,
		Language:      "go",
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got := plan.Env["PATH"]; got != executionToolchainRoot {
		t.Fatalf("PATH = %q, want %q", got, executionToolchainRoot)
	}
	found := false
	want := path.Join(executionToolchainRoot, "go")
	for _, mount := range plan.Mounts {
		if mount.Kind != MountToolchain {
			continue
		}
		if mount.VirtualPath == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("toolchain mount %q missing from %#v", want, plan.Mounts)
	}
}

func TestTranslateExecutionEnvValueRewritesPathLists(t *testing.T) {
	plan := ExecutionPlan{
		Mounts: []MountSpec{
			{
				Kind:        MountToolchain,
				VirtualPath: executionToolchainPath("go"),
			},
		},
	}
	value := executionToolchainRoot + string(os.PathListSeparator) + "/usr/bin"
	got := translateExecutionEnvValue(plan, "/sandbox", value)
	want := mountedExecPath("/sandbox", executionToolchainRoot) + string(os.PathListSeparator) + "/usr/bin"
	if got != want {
		t.Fatalf("translateExecutionEnvValue(...) = %q, want %q", got, want)
	}
}
