package purevfs

import (
	"errors"
	"os"
	"path"
	"path/filepath"
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

func TestExecutionPlannerPythonIncludesPip3Toolchain(t *testing.T) {
	planner := NewExecutionPlanner(nil, StrictBrokerCapabilities())

	plan, err := planner.Plan(ExecutionRequest{
		Mode:          ExecutionModeStrictNoDisk,
		Intent:        ExecutionIntentCommand,
		Language:      "python",
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := path.Join(executionToolchainRoot, "pip3")
	for _, mount := range plan.Mounts {
		if mount.Kind == MountToolchain && mount.VirtualPath == want {
			if got := plan.Cell.ExecutableAliases["python"]; len(got) == 1 && got[0] == "python3" {
				return
			}
			t.Fatalf("python executable aliases = %#v, want [\"python3\"]", plan.Cell.ExecutableAliases["python"])
		}
	}
	t.Fatalf("toolchain mount %q missing from %#v", want, plan.Mounts)
}

func TestResolveHostExecutionExecutable_UsesExecutableAliasFallback(t *testing.T) {
	tmpDir := t.TempDir()
	aliasPath := filepath.Join(tmpDir, "python3")
	if err := os.WriteFile(aliasPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	plan := ExecutionPlan{
		Cell: ExecCellPlan{
			ExecutableAliases: map[string][]string{
				"python": {"python3"},
			},
		},
	}
	resolved, err := resolveHostExecutionExecutable(plan, map[string]string{"PATH": tmpDir}, "", "python")
	if err != nil {
		t.Fatalf("resolveHostExecutionExecutable: %v", err)
	}
	if resolved != aliasPath {
		t.Fatalf("resolved = %q, want %q", resolved, aliasPath)
	}
}

func TestResolveHostExecutionExecutable_ReturnsTypedMissingExecutable(t *testing.T) {
	plan := ExecutionPlan{
		Cell: ExecCellPlan{
			ExecutableAliases: map[string][]string{
				"python": {"python3"},
			},
		},
	}
	_, err := resolveHostExecutionExecutable(plan, map[string]string{"PATH": t.TempDir()}, "", "python")
	if err == nil {
		t.Fatal("expected missing executable error")
	}
	var execErr *ExecutableNotFoundError
	if !errors.As(err, &execErr) {
		t.Fatalf("error = %T, want *ExecutableNotFoundError", err)
	}
	if execErr.Executable != "python" {
		t.Fatalf("executable = %q, want python", execErr.Executable)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected errors.Is(err, os.ErrNotExist) to succeed, got %v", err)
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
