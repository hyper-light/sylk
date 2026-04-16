package global

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/versioning"
)

type allowAllGlobalCommandGate struct{}

func (allowAllGlobalCommandGate) Authorize(_ context.Context, _ commandapproval.Request) (commandapproval.Evaluation, error) {
	return commandapproval.Evaluation{Decision: commandapproval.DecisionAllow}, nil
}

type stubGlobalExecutionBroker struct {
	caps purevfs.ExecutionCapabilities
	run  func(context.Context, purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error)
}

func (s stubGlobalExecutionBroker) Capabilities(context.Context) (purevfs.ExecutionCapabilities, error) {
	return s.caps, nil
}

func (s stubGlobalExecutionBroker) Run(ctx context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
	if s.run == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	return s.run(ctx, req)
}

func TestGlobalTesterCoreSkillsProduceConcreteOutputs(t *testing.T) {
	gt, _, baseCtx := newGoGlobalTesterHarness(t)
	ctx := commandapproval.WithGate(baseCtx, allowAllGlobalCommandGate{})

	analyzeRiskResult := invokeGlobalSkill(t, gt, ctx, "analyze_risk", map[string]any{
		"files":     []string{"pkg/service/service.go"},
		"task_spec": "Acceptance Criteria:\n- Add returns the sum for positive and negative integers.\n- Boundary inputs are handled correctly.",
	})
	if riskCount := intFromMap(t, analyzeRiskResult, "risk_count"); riskCount == 0 {
		t.Fatalf("risk_count = %d, want > 0", riskCount)
	}

	riskAreas := decodeRiskAreas(t, analyzeRiskResult["risk_areas"])
	planTestsResult := invokeGlobalSkill(t, gt, ctx, "plan_tests", map[string]any{
		"files":      []string{"pkg/service/service.go"},
		"task_spec":  "Acceptance Criteria:\n- Add returns the sum for positive and negative integers.\n- Boundary inputs are handled correctly.",
		"risk_areas": riskAreas,
	})
	if caseCount := intFromMap(t, planTestsResult, "case_count"); caseCount == 0 {
		t.Fatalf("plan_tests case_count = %d, want > 0", caseCount)
	}

	analyzeBatchResult := invokeGlobalSkill(t, gt, ctx, "analyze_batch", map[string]any{
		"pipeline_ids":  []string{"task-1", "task-2"},
		"changed_files": []string{"pkg/service/service.go"},
		"task_specs": map[string]any{
			"task-1": "Validate merged Add behavior.",
			"task-2": "Validate compatibility with callers.",
		},
	})
	if got := intFromMap(t, analyzeBatchResult, "changed_file_count"); got != 1 {
		t.Fatalf("changed_file_count = %d, want 1", got)
	}

	integrationRiskResult := invokeGlobalSkill(t, gt, ctx, "analyze_integration_risks", map[string]any{
		"changed_files": []string{"pkg/service/service.go"},
		"pipeline_ids":  []string{"task-1", "task-2"},
		"focus_areas":   []string{"merged compatibility"},
	})
	if riskCount := intFromMap(t, integrationRiskResult, "risk_count"); riskCount == 0 {
		t.Fatalf("integration risk_count = %d, want > 0", riskCount)
	}
	integrationRiskAreas := decodeRiskAreas(t, integrationRiskResult["risk_areas"])

	integrationPlanResult := invokeGlobalSkill(t, gt, ctx, "plan_integration_tests", map[string]any{
		"risk_areas": integrationRiskAreas,
		"batch_context": map[string]any{
			"pipeline_ids":  []string{"task-1", "task-2"},
			"changed_files": []string{"pkg/service/service.go"},
		},
	})
	if caseCount := intFromMap(t, integrationPlanResult, "case_count"); caseCount == 0 {
		t.Fatalf("plan_integration_tests case_count = %d, want > 0", caseCount)
	}

	buildHarnessResult := invokeGlobalSkill(t, gt, ctx, "build_harness", map[string]any{
		"files":      []string{"pkg/service/service.go"},
		"task_spec":  "Validate merged Add behavior.",
		"risk_areas": integrationRiskAreas,
	})
	if built, _ := buildHarnessResult["built"].(bool); !built {
		t.Fatalf("build_harness built = %#v, want true", buildHarnessResult["built"])
	}
	executionHarness, ok := buildHarnessResult["execution_harness"].(map[string]any)
	if !ok {
		t.Fatalf("execution_harness type = %T, want map[string]any", buildHarnessResult["execution_harness"])
	}
	if got, _ := executionHarness["framework_id"].(string); got != "go-test" {
		t.Fatalf("framework_id = %q, want go-test", got)
	}

	e2ePlanResult := invokeGlobalSkill(t, gt, ctx, "plan_e2e_tests", map[string]any{
		"risk_areas": integrationRiskAreas,
		"harness_needs": []map[string]any{
			{"type": "fixture", "description": "Provide merged inputs", "target": "pkg/service/service.go"},
		},
	})
	if caseCount := intFromMap(t, e2ePlanResult, "case_count"); caseCount == 0 {
		t.Fatalf("plan_e2e_tests case_count = %d, want > 0", caseCount)
	}

	basis := prepareGlobalWriteBasis(t, gt, ctx, "pkg/service/service_test.go")
	invokeGlobalSkill(t, gt, ctx, "write_test", map[string]any{
		"test_case": map[string]any{
			"name":        "TestAdd",
			"target_file": "pkg/service/service.go",
		},
		"target_file": "pkg/service/service.go",
		"output_file": "pkg/service/service_test.go",
		"content":     "package service\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"got %d, want 5\", got)\n\t}\n}\n",
		"basis":       basis,
	})

	gt.SetExecutionBroker(stubGlobalExecutionBroker{
		caps: purevfs.StrictBrokerCapabilities(),
		run: func(context.Context, purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			output := `{"Action":"pass","Package":"example.com/tester/pkg/service","Test":"TestAdd","Elapsed":0.01}` + "\n"
			return &purevfs.BrokerRunResult{
				ExitCode: 0,
				Stdout:   []byte(output),
			}, nil
		},
	})

	runSuiteResult := invokeGlobalSkill(t, gt, ctx, "run_test_suite", map[string]any{
		"files":   []string{"pkg/service/service.go"},
		"verbose": true,
		"timeout": 30,
	})
	if got := intFromMap(t, runSuiteResult, "passed"); got != 1 {
		t.Fatalf("run_test_suite passed = %d, want 1", got)
	}
	if command, _ := runSuiteResult["command"].(string); command == "" {
		t.Fatalf("expected concrete run_test_suite command, got %#v", runSuiteResult["command"])
	}
}

func TestGlobalTesterRunTestSuite_UsesCreatedPythonTestArtifactForSourceFocus(t *testing.T) {
	gt, fa, baseCtx := newGlobalTesterWriteHarness(t)
	ctx := commandapproval.WithGate(baseCtx, allowAllGlobalCommandGate{})
	if err := fa.WriteFile(ctx, "pyproject.toml", []byte("[project]\nname = \"tester\"\nversion = \"0.1.0\"\n")); err != nil {
		t.Fatalf("WriteFile pyproject.toml: %v", err)
	}
	if err := fa.WriteFile(ctx, "app/service.py", []byte("def add(a, b):\n    return a + b\n")); err != nil {
		t.Fatalf("WriteFile service.py: %v", err)
	}

	harness, err := gt.detectHarness(ctx, []string{"app/service.py"}, "", "")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	if got := string(harness.FrameworkID); got != "pytest" {
		t.Fatalf("framework_id = %q, want pytest", got)
	}

	basis := prepareGlobalWriteBasis(t, gt, ctx, "app/test_service.py")
	invokeGlobalSkill(t, gt, ctx, "write_test", map[string]any{
		"test_case": map[string]any{
			"name":        "TestAdd",
			"target_file": "app/service.py",
		},
		"target_file": "app/service.py",
		"output_file": "app/test_service.py",
		"content":     "def test_add():\n    assert True\n",
		"basis":       basis,
	})

	var captured purevfs.BrokerRunRequest
	gt.SetExecutionBroker(stubGlobalExecutionBroker{
		caps: purevfs.StrictBrokerCapabilities(),
		run: func(_ context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			captured = req
			return &purevfs.BrokerRunResult{ExitCode: 0}, nil
		},
	})

	runSuiteResult := invokeGlobalSkill(t, gt, ctx, "run_test_suite", map[string]any{
		"files": []string{filepath.Join(fa.WorkingDir(), "app/service.py")},
	})
	if got := captured.Argv; len(got) != 4 || got[3] != "app/test_service.py" {
		t.Fatalf("argv = %#v, want focused test artifact", got)
	}
	if got, _ := runSuiteResult["focused_file"].(string); got != "app/test_service.py" {
		t.Fatalf("focused_file = %q, want app/test_service.py", got)
	}
}

func invokeGlobalSkill(t *testing.T, gt *GlobalTester, ctx context.Context, name string, payload map[string]any) map[string]any {
	t.Helper()
	input, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s input: %v", name, err)
	}
	result := gt.skills.Invoke(ctx, name, input)
	if !result.Success {
		t.Fatalf("%s failed: %s", name, result.Error)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("%s result type = %T, want map[string]any", name, result.Data)
	}
	return data
}

func decodeRiskAreas(t *testing.T, value any) []map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal risk areas: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal risk areas: %v", err)
	}
	return decoded
}

func intFromMap(t *testing.T, values map[string]any, key string) int {
	t.Helper()
	switch typed := values[key].(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		t.Fatalf("%s type = %T, want numeric", key, values[key])
		return 0
	}
}

func newGoGlobalTesterHarness(t *testing.T) (*GlobalTester, versioning.FileAccess, context.Context) {
	t.Helper()

	gt, fa, ctx := newGlobalTesterWriteHarness(t)
	if err := fa.WriteFile(ctx, "go.mod", []byte("module example.com/tester\n\ngo 1.24.0\n")); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
	if err := fa.WriteFile(ctx, "pkg/service/service.go", []byte("package service\n\nfunc Add(a, b int) int { return a + b }\n")); err != nil {
		t.Fatalf("WriteFile service.go: %v", err)
	}
	return gt, fa, ctx
}
