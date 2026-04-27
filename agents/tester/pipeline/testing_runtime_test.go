package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/ci/zeroleak"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	coretest "github.com/adalundhe/sylk/core/test"
	"github.com/adalundhe/sylk/core/versioning"
)

type stubExecutionBroker struct {
	caps purevfs.ExecutionCapabilities
	run  func(context.Context, purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error)
}

type allowAllCommandGate struct{}

func (allowAllCommandGate) Authorize(_ context.Context, _ commandapproval.Request) (commandapproval.Evaluation, error) {
	return commandapproval.Evaluation{Decision: commandapproval.DecisionAllow}, nil
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

	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
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

func TestPipelineTesterRunGenericSuite_UsesCreatedTestArtifactForSourceFocus(t *testing.T) {
	pt, ctx, _ := newPythonPipelineTesterWithVFS(t)
	var captured purevfs.BrokerRunRequest
	pt.SetExecutionBroker(stubExecutionBroker{
		caps: purevfs.StrictBrokerCapabilities(),
		run: func(_ context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			captured = req
			return &purevfs.BrokerRunResult{ExitCode: 0}, nil
		},
	})

	harness, err := pt.detectHarness(ctx, []string{"app/service.py"}, "Write service tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	_, err = pt.writeTestArtifact(ctx, harness, testershared.PlannedTestCase{
		Name:       "TestAdd",
		TargetFile: "app/service.py",
	}, "app/test_service.py", "def test_add():\n    assert True\n", nil)
	if err != nil {
		t.Fatalf("writeTestArtifact: %v", err)
	}

	result, err := pt.executeSuite(ctx, harness, nil, []string{"app/service.py"}, nil, false, false, 30)
	if err != nil {
		t.Fatalf("executeSuite: %v", err)
	}
	if got := captured.Argv; len(got) != 4 || got[3] != "app/test_service.py" {
		t.Fatalf("argv = %#v, want focused test artifact", got)
	}
	if got, _ := result["focused_file"].(string); got != "app/test_service.py" {
		t.Fatalf("focused_file = %q, want app/test_service.py", got)
	}
}

func TestPipelineTesterRunGenericSuite_NormalizesAbsoluteSourceFocusPath(t *testing.T) {
	pt, ctx, fa := newPythonPipelineTesterWithVFS(t)
	var captured purevfs.BrokerRunRequest
	pt.SetExecutionBroker(stubExecutionBroker{
		caps: purevfs.StrictBrokerCapabilities(),
		run: func(_ context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			captured = req
			return &purevfs.BrokerRunResult{ExitCode: 0}, nil
		},
	})

	harness, err := pt.detectHarness(ctx, []string{"app/service.py"}, "Write service tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	_, err = pt.writeTestArtifact(ctx, harness, testershared.PlannedTestCase{
		Name:       "TestAdd",
		TargetFile: "app/service.py",
	}, "app/test_service.py", "def test_add():\n    assert True\n", nil)
	if err != nil {
		t.Fatalf("writeTestArtifact: %v", err)
	}

	absoluteSource := filepath.Join(fa.WorkingDir(), "app/service.py")
	if _, err := pt.executeSuite(ctx, harness, nil, []string{absoluteSource}, nil, false, false, 30); err != nil {
		t.Fatalf("executeSuite: %v", err)
	}
	if got := captured.Argv; len(got) != 4 || got[3] != "app/test_service.py" {
		t.Fatalf("argv = %#v, want normalized focused test artifact", got)
	}
}

func TestPipelineTesterWriteTestArtifact_RefreshesStaleBasisViaSharedWriteSkill(t *testing.T) {
	pt, ctx, fa := newGoPipelineTesterWithVFS(t)

	harness, err := pt.detectHarness(ctx, []string{"pkg/service/service.go"}, "Write Add tests", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}

	outputFile := "pkg/service/service_test.go"
	_, err = pt.writeTestArtifact(ctx, harness, testershared.PlannedTestCase{
		Name:       "TestSeed",
		TargetFile: "pkg/service/service.go",
	}, outputFile, "func TestSeed(t *testing.T) {\n\tt.Helper()\n}\n", nil)
	if err != nil {
		t.Fatalf("seed writeTestArtifact: %v", err)
	}

	basis, err := versioning.RefreshWorkspaceWriteBasis(
		ctx,
		pt.workspaceViews,
		versioning.WorkspaceWriteScopePipeline,
		outputFile,
		pt.pipelineID,
	)
	if err != nil {
		t.Fatalf("RefreshWorkspaceWriteBasis: %v", err)
	}

	staleContent := "package service\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T) {\n\tt.Helper()\n}\n"
	if err := fa.WriteFile(ctx, outputFile, []byte(staleContent)); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err = pt.writeTestArtifact(ctx, harness, testershared.PlannedTestCase{
		Name:       "TestFresh",
		TargetFile: "pkg/service/service.go",
	}, outputFile, "func TestFresh(t *testing.T) {\n\tt.Helper()\n}\n", &basis)
	if err != nil {
		t.Fatalf("writeTestArtifact with stale basis: %v", err)
	}

	content, err := fa.ReadFile(ctx, outputFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "func TestExisting") {
		t.Fatalf("expected existing test to be preserved, got %q", got)
	}
	if !strings.Contains(got, "func TestFresh") {
		t.Fatalf("expected fresh test to be written, got %q", got)
	}
}

func TestPipelineTesterCreateTestsDeterministically_EmitsWriteToolCalls(t *testing.T) {
	pt, baseCtx, _ := newGoPipelineTesterWithVFS(t)

	var toolNames []string
	ctx := agentshared.WithToolCallEmitter(baseCtx, func(event agentshared.ToolCallEvent) error {
		if event.Phase != agentshared.ToolCallStart {
			return nil
		}
		toolNames = append(toolNames, event.ToolName)
		return nil
	})

	req := &tester.TesterRequest{
		ID:         "req-1",
		Files:      []string{"pkg/service/service.go"},
		TaskPrompt: "Write specification-first tests for Add.",
		WorkerType: "engineer",
	}
	if err := pt.createTestsDeterministically(ctx, req, req.Files); err != nil {
		t.Fatalf("createTestsDeterministically: %v", err)
	}
	for _, want := range []string{"test_harness", "analyze_risk", "plan_tests"} {
		if !containsName(toolNames, want) {
			t.Fatalf("expected %s tool call, got %v", want, toolNames)
		}
	}
	// prepare_write_context folded into workspace_read(op=prepare_write)
	// — the tool name emitted is now workspace_read.
	if !containsName(toolNames, "workspace_read") {
		t.Fatalf("expected workspace_read(op=prepare_write) tool call, got %v", toolNames)
	}
	if !containsName(toolNames, "write_test") {
		t.Fatalf("expected write_test tool call, got %v", toolNames)
	}
}

func TestPipelineTesterVisibleSkills_IncludeHarnessAndReportingTools(t *testing.T) {
	// workspace_read absorbs the write preflight (op=prepare_write)
	// alongside workspace_write; prepare_write_context is no longer a
	// standalone skill in the LLM catalog.
	for _, want := range []string{
		"test_harness",
		// Phase 2.K / GT-4 + GI-5: collapsed into dependency(action=…, category=test).
		"dependency",
		"workspace_read",
		"workspace_write",
		"write_test",
		"run_test_suite",
		"bash",
		// Claims skills: read-only board introspection is visible.
		"query_claims_board",
	} {
		if !containsName(pipelineTesterVisibleSkillNames(), want) {
			t.Fatalf("visible skills missing %q", want)
		}
	}
}

func TestParseTestToolInstallPlan_ExtractsFencedJSON(t *testing.T) {
	raw := "Here is the plan:\n```json\n{\"summary\":\"Install pytest\",\"missing_tool\":\"pytest\",\"steps\":[{\"command\":\"python -m pip install pytest\",\"reason\":\"provide the requested test runner\"}]}\n```"

	plan, err := parseTestToolInstallPlan(raw)
	if err != nil {
		t.Fatalf("parseTestToolInstallPlan: %v", err)
	}
	if plan.MissingTool != "pytest" {
		t.Fatalf("missing tool = %q, want pytest", plan.MissingTool)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Command != "python -m pip install pytest" {
		t.Fatalf("steps = %+v", plan.Steps)
	}
}

// TestInstallTestTooling_ExecutesApprovedSteps verifies the install plan is
// routed through the broker and no host-disk side effects appear. The test
// stubs the broker so no real execution happens, then asserts (1) the broker
// captured every approved step + validation, (2) the zero-leak harness
// confirms nothing wrote to the workspace during the install.
func TestInstallTestTooling_ExecutesApprovedSteps(t *testing.T) {
	pt, baseCtx, fa := newGoPipelineTesterWithVFS(t)
	ctx := commandapproval.WithGate(baseCtx, allowAllCommandGate{})
	zeroleak.Guard(t, fa.WorkingDir())

	var captured []purevfs.BrokerRunRequest
	pt.SetExecutionBroker(stubExecutionBroker{
		caps: purevfs.ExecutionCapabilities{ProcessBroker: true, InMemoryScratch: true},
		run: func(_ context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			captured = append(captured, req)
			return &purevfs.BrokerRunResult{ExitCode: 0}, nil
		},
	})

	result, err := pt.installTestTooling(ctx, &testToolInstallPlan{
		Summary:           "Install pytest for the Python test harness",
		MissingTool:       "pytest",
		ValidationCommand: "pytest --version",
		Steps: []testToolInstallStep{
			{Command: "python -m pip install pytest", Reason: "install pytest for the harness"},
		},
	})
	if err != nil {
		t.Fatalf("installTestTooling: %v", err)
	}

	if got := len(captured); got != 2 {
		t.Fatalf("broker invocations = %d, want 2 (install step + validation)", got)
	}
	if installed, _ := result["installed"].(bool); !installed {
		t.Fatalf("result installed = %v, want true", result["installed"])
	}
}

func TestInstallTestTooling_RejectsUnsafeCommands(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)

	_, err := pt.installTestTooling(ctx, &testToolInstallPlan{
		Summary: "Install pytest",
		Steps: []testToolInstallStep{
			{Command: "python -m pip install pytest && echo hacked"},
		},
	})
	if err == nil {
		t.Fatal("expected unsafe shell syntax error")
	}
	if !strings.Contains(err.Error(), "unsupported shell syntax") {
		t.Fatalf("error = %v, want unsupported shell syntax", err)
	}
}

func TestResearchTestToolInstall_UsesAcademicRouteAndParsesPlan(t *testing.T) {
	pt, ctx, _ := newGoPipelineTesterWithVFS(t)
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	pt.bus = bus
	pt.channels = guide.NewAgentChannels("tester-pipeline", pt.id)

	sub, err := bus.Subscribe(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.TargetAgentID != "academic" {
			return nil
		}
		if !req.ExplicitTarget {
			t.Fatal("expected academic install research route to use explicit target routing")
		}
		if !strings.Contains(req.Input, "pytest") {
			t.Fatalf("academic prompt missing failure context: %s", req.Input)
		}
		resp := &guide.RouteResponse{
			CorrelationID:       req.CorrelationID,
			Success:             true,
			RespondingAgentID:   "academic-1",
			RespondingAgentName: "Academic",
			Data: map[string]any{
				"type":    "recall",
				"content": "{\"summary\":\"Install pytest\",\"missing_tool\":\"pytest\",\"steps\":[{\"command\":\"python -m pip install pytest\",\"reason\":\"provide pytest\"}]}",
			},
		}
		return bus.Publish(pt.channels.Responses, &guide.Message{
			ID:            "resp-1",
			CorrelationID: req.CorrelationID,
			Type:          guide.MessageTypeResponse,
			SourceAgentID: "academic-1",
			Payload:       resp,
		})
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	plan, err := pt.researchTestToolInstall(ctx, "pytest", "pytest: command not found", "pytest", "pytest", []string{"pkg/service/service.go"}, "Add service tests", "engineer")
	if err != nil {
		t.Fatalf("researchTestToolInstall: %v", err)
	}
	if plan.MissingTool != "pytest" {
		t.Fatalf("missing tool = %q, want pytest", plan.MissingTool)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Command != "python -m pip install pytest" {
		t.Fatalf("steps = %+v", plan.Steps)
	}
}

func TestPipelineTesterDetectHarness_SelectsPythonAndDefaultOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "pyproject.toml"), "[project]\nname = \"tester\"\nversion = \"0.1.0\"\n")
	mustWriteTestFile(t, filepath.Join(root, "app/service.py"), "def add(a, b):\n    return a + b\n")

	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
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

	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
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

	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
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
	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
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

func newPythonPipelineTesterWithVFS(t *testing.T) (*PipelineTester, context.Context, versioning.FileAccess) {
	t.Helper()

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

	fa := svfs.NewPipelineFileAccess(pipe)
	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
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

	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
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

	skill := testHarnessSkill(pt)
	input, _ := json.Marshal(map[string]any{
		"action":      "prepare",
		"files":       []string{"app/service.py"},
		"worker_type": "engineer",
	})
	_, err = skill.Handler(versioning.WithSessionID(context.Background(), "sess-1"), input)
	if err == nil {
		t.Fatal("expected write context error")
	}
	if !strings.Contains(err.Error(), "workspace_read(op=prepare_write)") {
		t.Fatalf("error = %v, want workspace_read(op=prepare_write) guidance", err)
	}
}

// TestPipelineTesterDetectHarness_PrefersPlannerSpecOverLLMTargetExt
// locks the agentic fix for the failure mode where the LLM-tester
// invented a Python target_files path inside an established Go
// project. Pre-fix, the .py extension drove the harness pick to
// pytest. Post-fix, the planner's structured task spec wins: when
// affected_files / workspace.write_set carry .go paths, harness
// selection is "go" with language_source="task_spec" and a
// language_note that flags the .py target_files conflict so the LLM
// sees why its supplied paths were overridden.
func TestPipelineTesterDetectHarness_PrefersPlannerSpecOverLLMTargetExt(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "go.mod"), "module example.com/hellocli\n\ngo 1.24.0\n")
	mustWriteTestFile(t, filepath.Join(root, "hello_cli/cli.go"), "package hello_cli\n\nfunc Run() {}\n")

	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pt.SetFileAccess(versioning.NewDiskFileAccess(root, false))

	pt.swapTaskRuntime(&agentshared.PipelineTaskInput{
		TaskID:    "task_2",
		NodeID:    "task_2-tester",
		AgentType: "tester-pipeline",
		Context: map[string]any{
			"affected_files": []any{"hello_cli/cli.go", "hello_cli/main.go"},
			"workspace": map[string]any{
				"write_set":    []any{"hello_cli/cli.go", "hello_cli/main.go"},
				"test_surface": []any{"hello_cli/cli_test.go"},
			},
		},
	})

	// LLM-tester supplies a .py target — the failure-mode signal that
	// triggered the original bug. Selection must ignore the extension
	// because the planner spec resolves to Go.
	state, err := pt.detectHarness(context.Background(), []string{"hello_cli/tests/test_cli.py"}, "Write pytest spec tests for hello_cli", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	if state.FrameworkID != coretest.FrameworkGoTest {
		t.Fatalf("framework = %s, want %s", state.FrameworkID, coretest.FrameworkGoTest)
	}
	if state.LanguageSource != "task_spec" {
		t.Fatalf("language_source = %q, want %q", state.LanguageSource, "task_spec")
	}
	if !strings.Contains(state.LanguageNote, "python") {
		t.Fatalf("language_note %q must surface the python target_files conflict", state.LanguageNote)
	}
}

// TestPipelineTesterDetectHarness_FallsBackToTargetFilesWhenSpecSilent
// keeps the planner-spec preference from over-reaching. When the task
// has no structured paths (no affected_files / workspace / worker
// packets), the LLM-supplied target_files become the language signal —
// otherwise harness detection would refuse work for tasks the
// planner did not paint with paths (e.g., scratch testing flows).
func TestPipelineTesterDetectHarness_FallsBackToTargetFilesWhenSpecSilent(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "pyproject.toml"), "[project]\nname = \"x\"\nversion = \"0.1.0\"\n")
	mustWriteTestFile(t, filepath.Join(root, "app/service.py"), "def f():\n    pass\n")

	pt, err := New(testershared.PipelineTesterConfig{Factory: newTestFactory(t)}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pt.SetFileAccess(versioning.NewDiskFileAccess(root, false))
	// No swapTaskRuntime — currentTask stays nil, so spec language is empty.

	state, err := pt.detectHarness(context.Background(), []string{"app/service.py"}, "Write tests for service.py", "engineer")
	if err != nil {
		t.Fatalf("detectHarness: %v", err)
	}
	if state.FrameworkID != coretest.FrameworkPytest {
		t.Fatalf("framework = %s, want %s", state.FrameworkID, coretest.FrameworkPytest)
	}
	if state.LanguageSource != "target_files" {
		t.Fatalf("language_source = %q, want %q", state.LanguageSource, "target_files")
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
