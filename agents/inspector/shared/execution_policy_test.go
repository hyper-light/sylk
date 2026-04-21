package shared

import (
	"errors"
	"strings"
	"testing"

	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func TestInspectorRejectTestExecution_RejectsPytest(t *testing.T) {
	err := InspectorRejectTestExecution("python3 -m pytest tests/test_cli.py", "bash")
	if err == nil {
		t.Fatal("expected pytest execution to be rejected for inspector")
	}
	if !strings.Contains(err.Error(), "route test execution to Tester") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !errors.Is(err, agentshared.ErrRouteTestExecutionToTester) {
		t.Fatalf("expected ErrRouteTestExecutionToTester, got %v", err)
	}
}

func TestInspectorRejectTestExecution_AllowsLinterCommand(t *testing.T) {
	if err := InspectorRejectTestExecution("golangci-lint run ./...", "bash"); err != nil {
		t.Fatalf("expected linter execution to be allowed, got %v", err)
	}
}

func TestInspectorRejectTestDependencyResearch_RejectsTestTooling(t *testing.T) {
	err := InspectorRejectTestDependencyResearch("pytest", "", "", "pytest: command not found")
	if err == nil {
		t.Fatal("expected test-tooling research to be rejected for inspector")
	}
	if !errors.Is(err, agentshared.ErrRouteTestToolingToTester) {
		t.Fatalf("expected ErrRouteTestToolingToTester, got %v", err)
	}
}

func TestInspectorRejectTestDependencyInstallPlan_RejectsTestTooling(t *testing.T) {
	err := InspectorRejectTestDependencyInstallPlan(&agentshared.DependencyInstallPlan{
		Summary:           "Install pytest",
		MissingTool:       "pytest",
		ValidationCommand: "python3 -m pytest --version",
		Steps: []agentshared.DependencyInstallStep{
			{Command: "python3 -m pip install pytest"},
		},
	})
	if err == nil {
		t.Fatal("expected test-tooling install plan to be rejected for inspector")
	}
	if !errors.Is(err, agentshared.ErrRouteTestToolingToTester) {
		t.Fatalf("expected ErrRouteTestToolingToTester, got %v", err)
	}
}

func TestInspectorRejectTestDependencyInstallPlan_AllowsAuditTooling(t *testing.T) {
	err := InspectorRejectTestDependencyInstallPlan(&agentshared.DependencyInstallPlan{
		Summary:           "Install mypy",
		MissingTool:       "mypy",
		ValidationCommand: "python3 -m mypy --version",
		Steps: []agentshared.DependencyInstallStep{
			{Command: "python3 -m pip install mypy"},
		},
	})
	if err != nil {
		t.Fatalf("expected audit tooling plan to be allowed, got %v", err)
	}
}
