package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	coretest "github.com/adalundhe/sylk/core/test"
	"github.com/adalundhe/sylk/core/versioning"
)

type stubExecutionBroker struct {
	caps purevfs.ExecutionCapabilities
	run  func(context.Context, purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error)
}

func (s stubExecutionBroker) Capabilities(context.Context) (purevfs.ExecutionCapabilities, error) {
	return s.caps, nil
}

func (s stubExecutionBroker) Run(ctx context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
	if s.run == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	return s.run(ctx, req)
}

func TestPipelineTesterDetectHarness_SelectsGoAndDefaultOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "go.mod"), "module example.com/tester\n\ngo 1.24.0\n")
	mustWriteTestFile(t, filepath.Join(root, "pkg/service/service.go"), "package service\n\nfunc Add(a, b int) int { return a + b }\n")

	pt, err := New(testershared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pt.SetFileAccess(versioning.NewDiskFileAccess(root, false))

	state, err := pt.detectHarness(context.Background(), []string{"pkg/service/service.go"}, "Add service tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	if state.FrameworkID != coretest.FrameworkGoTest {
		t.Fatalf("framework = %s, want %s", state.FrameworkID, coretest.FrameworkGoTest)
	}
	if got := state.RecommendedOutputs["pkg/service/service.go"]; got != "pkg/service/service_test.go" {
		t.Fatalf("recommended output = %q, want %q", got, "pkg/service/service_test.go")
	}
}

func TestPipelineTesterWriteTestArtifact_CreatesSiblingTestInSparseVFS(t *testing.T) {
	pt, ctx, fa := newGoPipelineTesterWithVFS(t)

	harness, err := pt.detectHarness(ctx, []string{"pkg/service/service.go"}, "Write Add tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}

	_, err = pt.writeTestArtifact(ctx, harness, testershared.PlannedTestCase{
		Name:       "TestAdd",
		TargetFile: "pkg/service/service.go",
	}, "pkg/service/service_test.go", "func TestAdd(t *testing.T) {\n\tt.Helper()\n}\n", nil)
	if err != nil {
		t.Fatalf("writeTestArtifact: %v", err)
	}

	content, err := fa.ReadFile(ctx, "pkg/service/service_test.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(content); got == "" || got[:15] != "package service" {
		t.Fatalf("unexpected written content: %q", got)
	}
}

func TestPipelineTesterRunGoTestSuite_RequiresStrictBroker(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)
	pt.SetExecutionBroker(stubExecutionBroker{})

	harness, err := pt.detectHarness(ctx, []string{"pkg/service/service.go"}, "Write Add tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}

	_, err = pt.writeTestArtifact(ctx, harness, testershared.PlannedTestCase{
		Name:       "TestAdd",
		TargetFile: "pkg/service/service.go",
	}, "pkg/service/service_test.go", "func TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"got %d, want 5\", got)\n\t}\n}\n", nil)
	if err != nil {
		t.Fatalf("writeTestArtifact: %v", err)
	}

	_, err = pt.executeSuite(ctx, harness, nil, []string{"pkg/service/service.go"}, nil, false, true, 30)
	if !errors.Is(err, purevfs.ErrStrictExecutionUnavailable) {
		t.Fatalf("executeSuite error = %v, want %v", err, purevfs.ErrStrictExecutionUnavailable)
	}
}

func TestPipelineTesterRunGoTestSuite_UsesStrictBroker(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)
	pt.SetExecutionBroker(stubExecutionBroker{
		caps: purevfs.StrictBrokerCapabilities(),
		run: func(context.Context, purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			output := `{"Action":"pass","Package":"example.com/tester/pkg/service","Test":"TestAdd","Elapsed":0.01}` + "\n"
			return &purevfs.BrokerRunResult{
				ExitCode: 0,
				Stdout:   []byte(output),
			}, nil
		},
	})

	harness, err := pt.detectHarness(ctx, []string{"pkg/service/service.go"}, "Write Add tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	_, err = pt.writeTestArtifact(ctx, harness, testershared.PlannedTestCase{
		Name:       "TestAdd",
		TargetFile: "pkg/service/service.go",
	}, "pkg/service/service_test.go", "func TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"got %d, want 5\", got)\n\t}\n}\n", nil)
	if err != nil {
		t.Fatalf("writeTestArtifact: %v", err)
	}

	result, err := pt.executeSuite(ctx, harness, nil, []string{"pkg/service/service.go"}, nil, false, true, 30)
	if err != nil {
		t.Fatalf("executeSuite: %v", err)
	}
	if got := result["passed"]; got != 1 {
		t.Fatalf("passed = %v, want 1", got)
	}
	if got := result["execution_strategy"]; got != purevfs.StrategyProcessBroker {
		t.Fatalf("strategy = %v, want %v", got, purevfs.StrategyProcessBroker)
	}
}

func TestPipelineTesterVisibleSkills_IncludeHarnessAndReportingTools(t *testing.T) {
	for _, want := range []string{
		"check_inspector_gate",
		"detect_test_harness",
		"prepare_test_harness",
		"prepare_pipeline_write_context",
		"write_pipeline_file",
		"list_pipeline_changes",
		"write_test",
		"run_test_suite",
		"report_to_engineer",
		"report_to_designer",
	} {
		if !containsName(pipelineTesterVisibleSkillNames(), want) {
			t.Fatalf("visible skills missing %q", want)
		}
	}
}

func TestPipelineTesterDetectHarness_SelectsPythonAndDefaultOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "pyproject.toml"), "[project]\nname = \"tester\"\nversion = \"0.1.0\"\n")
	mustWriteTestFile(t, filepath.Join(root, "app/service.py"), "def add(a, b):\n    return a + b\n")

	pt, err := New(testershared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pt.SetFileAccess(versioning.NewDiskFileAccess(root, false))

	state, err := pt.detectHarness(context.Background(), []string{"app/service.py"}, "Add service tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	if state.FrameworkID != coretest.FrameworkPytest {
		t.Fatalf("framework = %s, want %s", state.FrameworkID, coretest.FrameworkPytest)
	}
	if got := state.RecommendedOutputs["app/service.py"]; got != "app/test_service.py" {
		t.Fatalf("recommended output = %q, want %q", got, "app/test_service.py")
	}
}

func TestPipelineTesterDetectHarness_SelectsGradleForKotlin(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "build.gradle.kts"), "plugins { kotlin(\"jvm\") version \"2.1.0\" }\n")
	mustWriteTestFile(t, filepath.Join(root, "src/main/kotlin/demo/Service.kt"), "package demo\nclass Service\n")

	pt, err := New(testershared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pt.SetFileAccess(versioning.NewDiskFileAccess(root, false))

	state, err := pt.detectHarness(context.Background(), []string{"src/main/kotlin/demo/Service.kt"}, "Add service tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	if state.FrameworkID != coretest.FrameworkGradleTest {
		t.Fatalf("framework = %s, want %s", state.FrameworkID, coretest.FrameworkGradleTest)
	}
	if got := state.RecommendedOutputs["src/main/kotlin/demo/Service.kt"]; got != "src/test/kotlin/demo/ServiceTest.kt" {
		t.Fatalf("recommended output = %q, want %q", got, "src/test/kotlin/demo/ServiceTest.kt")
	}
}

func TestPipelineTesterDetectHarness_SelectsSBTForScala(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "build.sbt"), "scalaVersion := \"3.6.2\"\n")
	mustWriteTestFile(t, filepath.Join(root, "src/main/scala/demo/Service.scala"), "package demo\nclass Service\n")

	pt, err := New(testershared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pt.SetFileAccess(versioning.NewDiskFileAccess(root, false))

	state, err := pt.detectHarness(context.Background(), []string{"src/main/scala/demo/Service.scala"}, "Add service tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	if state.FrameworkID != coretest.FrameworkSBTTest {
		t.Fatalf("framework = %s, want %s", state.FrameworkID, coretest.FrameworkSBTTest)
	}
	if got := state.RecommendedOutputs["src/main/scala/demo/Service.scala"]; got != "src/test/scala/demo/ServiceTest.scala" {
		t.Fatalf("recommended output = %q, want %q", got, "src/test/scala/demo/ServiceTest.scala")
	}
}

func newGoPipelineTesterWithVFS(t *testing.T) (*PipelineTester, context.Context, versioning.FileAccess) {
	t.Helper()

	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "go.mod"), "module example.com/tester\n\ngo 1.24.0\n")
	mustWriteTestFile(t, filepath.Join(root, "pkg/service/service.go"), "package service\n\nfunc Add(a, b int) int { return a + b }\n")

	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	pipe, err := svfs.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: root,
		Files:      []string{"pkg/service/service.go"},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}

	fa := svfs.NewPipelineFileAccess(pipe)
	pt, err := New(testershared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pt.SetFileAccess(fa)
	pt.SetWorkspaceViews(versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:       versioning.WorkspaceViewPipeline,
		DefaultPipelineID: "task-1",
		Session:           svfs,
		WorkingDir:        root,
		DiskFallback:      versioning.NewDiskFileAccess(root, true),
	}))
	pt.pipelineID = "task-1"

	return pt, versioning.WithSessionID(context.Background(), "sess-1"), fa
}

func TestPipelineTesterWriteTestSkillRequiresBasis(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)

	harness, err := pt.detectHarness(ctx, []string{"pkg/service/service.go"}, "Write Add tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	pt.setHarnessState(harness)

	skill := writeTestSkill(pt)
	input, _ := json.Marshal(map[string]any{
		"test_case": map[string]any{
			"name":        "TestAdd",
			"target_file": "pkg/service/service.go",
		},
		"target_file": "pkg/service/service.go",
		"output_file": "pkg/service/service_test.go",
		"content":     "func TestAdd(t *testing.T) {}\n",
	})
	_, err = skill.Handler(ctx, input)
	if err == nil {
		t.Fatal("expected basis validation error")
	}
	if !strings.Contains(err.Error(), "basis") {
		t.Fatalf("error = %v, want basis error", err)
	}
}

func TestPipelineTesterPrepareHarnessRequiresWriteContexts(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "pyproject.toml"), "[project]\nname = \"tester\"\nversion = \"0.1.0\"\n")
	mustWriteTestFile(t, filepath.Join(root, "app/service.py"), "def add(a, b):\n    return a + b\n")

	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	pipe, err := svfs.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: root,
		Files:      []string{"app/service.py"},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}

	pt, err := New(testershared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pt.SetFileAccess(svfs.NewPipelineFileAccess(pipe))
	pt.SetWorkspaceViews(versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:       versioning.WorkspaceViewPipeline,
		DefaultPipelineID: "task-1",
		Session:           svfs,
		WorkingDir:        root,
		DiskFallback:      versioning.NewDiskFileAccess(root, true),
	}))
	pt.pipelineID = "task-1"

	skill := prepareTestHarnessSkill(pt)
	input, _ := json.Marshal(map[string]any{
		"files":       []string{"app/service.py"},
		"worker_type": "engineer",
	})
	_, err = skill.Handler(versioning.WithSessionID(context.Background(), "sess-1"), input)
	if err == nil {
		t.Fatal("expected write context error")
	}
	if !strings.Contains(err.Error(), "prepare_pipeline_write_context") {
		t.Fatalf("error = %v, want prepare_pipeline_write_context guidance", err)
	}
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func containsName(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
