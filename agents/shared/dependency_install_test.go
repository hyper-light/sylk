package shared

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/ci/zeroleak"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
)

type stubInstallBroker struct {
	caps purevfs.ExecutionCapabilities
	run  func(context.Context, purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error)
}

func (s stubInstallBroker) Capabilities(context.Context) (purevfs.ExecutionCapabilities, error) {
	return s.caps, nil
}

func (s stubInstallBroker) Run(ctx context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
	if s.run == nil {
		return &purevfs.BrokerRunResult{ExitCode: 0}, nil
	}
	return s.run(ctx, req)
}

func strictNoDiskInstallConfig(broker purevfs.ExecutionBroker, workspaceRoot string) (func() purevfs.ExecutionBroker, func(context.Context, string) (CommandExecContext, error), func(bool) purevfs.ExecutionFS) {
	prepare := func(ctx context.Context, workingDir string) (CommandExecContext, error) {
		return CommandExecContext{
			WorkDir: workspaceRoot,
			Plan:    purevfs.ExecutionPlan{Mode: purevfs.ExecutionModeStrictNoDisk, WorkingDir: workspaceRoot},
		}, nil
	}
	workspace := func(allowWrites bool) purevfs.ExecutionFS { return nil }
	return func() purevfs.ExecutionBroker { return broker }, prepare, workspace
}

type captureInstallGate struct {
	requests []commandapproval.Request
}

type mockDependencyInstallResponseText struct {
	Response string
}

func (m mockDependencyInstallResponseText) ResponseText() string { return m.Response }

func (g *captureInstallGate) Authorize(_ context.Context, req commandapproval.Request) (commandapproval.Evaluation, error) {
	g.requests = append(g.requests, req)
	return commandapproval.Evaluation{Decision: commandapproval.DecisionAllow}, nil
}

func TestParseDependencyInstallPlan_ExtractsFencedJSON(t *testing.T) {
	raw := "Plan:\n```json\n{\"summary\":\"Install pytest\",\"steps\":[{\"command\":\"python -m pip install pytest\"}]}\n```"
	plan, err := ParseDependencyInstallPlan(raw)
	if err != nil {
		t.Fatalf("ParseDependencyInstallPlan() error = %v", err)
	}
	if plan.Summary != "Install pytest" {
		t.Fatalf("summary = %q, want Install pytest", plan.Summary)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Command != "python -m pip install pytest" {
		t.Fatalf("steps = %#v", plan.Steps)
	}
}

func TestValidateDependencyInstallPlan_RejectsUnsafeCommand(t *testing.T) {
	err := ValidateDependencyInstallPlan(&DependencyInstallPlan{
		Summary: "Install tooling",
		Steps: []DependencyInstallStep{
			{Command: "npm install jest && npm install vitest"},
		},
	})
	if err == nil {
		t.Fatal("expected unsafe install command to be rejected")
	}
}

func TestValidateDependencyInstallPlan_RejectsAdHocVirtualenvBootstrap(t *testing.T) {
	err := ValidateDependencyInstallPlan(&DependencyInstallPlan{
		Summary: "Install pytest",
		Steps: []DependencyInstallStep{
			{Command: "python3 -m venv /tmp/test_venv"},
		},
	})
	if err == nil {
		t.Fatal("expected ad-hoc virtualenv bootstrap to be rejected")
	}
}

func TestValidateDependencyInstallPlan_RejectsLocalVenvExecutable(t *testing.T) {
	err := ValidateDependencyInstallPlan(&DependencyInstallPlan{
		Summary: "Install pytest",
		Steps: []DependencyInstallStep{
			{Command: ".venv/bin/pip install pytest"},
		},
	})
	if err == nil {
		t.Fatal("expected local venv executable to be rejected")
	}
}

func TestBuildDependencyInstallResearchPrompt_AvoidsAdHocVirtualenvBootstrap(t *testing.T) {
	prompt := BuildDependencyInstallResearchPrompt(DependencyInstallResearchRequest{
		RepositoryRoot: "/repo",
		MissingTool:    "pytest",
	})
	for _, want := range []string{
		"Do not create ad-hoc virtual environments or activation steps",
		"Do not install tooling into temporary scratch locations such as '/tmp'",
		"prefer 'python -m pip ...' or 'python3 -m pip ...' over bare 'pip' or 'pip3'",
		"Do not run the project test suite",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestExtractDependencyInstallResearchContent_AcceptsPlanObjectPayload(t *testing.T) {
	msg := guide.NewResponseMessage("academic", &guide.RouteResponse{
		CorrelationID:       "corr-install-plan-object",
		Success:             true,
		RespondingAgentID:   "academic-1",
		RespondingAgentName: "Academic",
		Data: map[string]any{
			"summary":      "Install pytest",
			"missing_tool": "pytest",
			"steps": []map[string]any{
				{
					"command": "python -m pip install pytest",
					"reason":  "provide pytest",
				},
			},
		},
	})
	content, err := ExtractDependencyInstallResearchContent(msg)
	if err != nil {
		t.Fatalf("ExtractDependencyInstallResearchContent() error = %v", err)
	}
	plan, err := ParseDependencyInstallPlan(content)
	if err != nil {
		t.Fatalf("ParseDependencyInstallPlan() error = %v", err)
	}
	if plan.MissingTool != "pytest" {
		t.Fatalf("missing tool = %q, want pytest", plan.MissingTool)
	}
}

func TestExtractDependencyInstallResearchContent_AcceptsNestedConversationPayload(t *testing.T) {
	msg := guide.NewResponseMessage("academic", &guide.RouteResponse{
		CorrelationID:       "corr-install-plan-conversation",
		Success:             true,
		RespondingAgentID:   "academic-1",
		RespondingAgentName: "Academic",
		Data: map[string]any{
			"response": map[string]any{
				"content": "1. `python -m pip install pytest`\nValidation: `python -m pytest --version`",
			},
		},
	})
	content, err := ExtractDependencyInstallResearchContent(msg)
	if err != nil {
		t.Fatalf("ExtractDependencyInstallResearchContent() error = %v", err)
	}
	if !strings.Contains(content, "python -m pip install pytest") {
		t.Fatalf("content = %q, want nested install command", content)
	}
}

func TestExtractDependencyInstallResearchContent_AcceptsResponseTextCarrier(t *testing.T) {
	msg := guide.NewResponseMessage("academic", &guide.RouteResponse{
		CorrelationID:       "corr-install-plan-response-text",
		Success:             true,
		RespondingAgentID:   "academic-1",
		RespondingAgentName: "Academic",
		Data: mockDependencyInstallResponseText{
			Response: "1. `python -m pip install pytest`\nValidation: `python -m pytest --version`",
		},
	})
	content, err := ExtractDependencyInstallResearchContent(msg)
	if err != nil {
		t.Fatalf("ExtractDependencyInstallResearchContent() error = %v", err)
	}
	if !strings.Contains(content, "python -m pip install pytest") {
		t.Fatalf("content = %q, want install command from response text", content)
	}
}

func TestExtractDependencyInstallResearchContent_AcceptsCapitalizedResponseField(t *testing.T) {
	msg := guide.NewResponseMessage("academic", &guide.RouteResponse{
		CorrelationID:       "corr-install-plan-capitalized",
		Success:             true,
		RespondingAgentID:   "academic-1",
		RespondingAgentName: "Academic",
		Data: map[string]any{
			"Response": "1. `python -m pip install pytest`\nValidation: `python -m pytest --version`",
		},
	})
	content, err := ExtractDependencyInstallResearchContent(msg)
	if err != nil {
		t.Fatalf("ExtractDependencyInstallResearchContent() error = %v", err)
	}
	if !strings.Contains(content, "python -m pip install pytest") {
		t.Fatalf("content = %q, want install command from Response field", content)
	}
}

func TestExtractDependencyInstallResearchContent_AcceptsContentBlocks(t *testing.T) {
	msg := guide.NewResponseMessage("academic", &guide.RouteResponse{
		CorrelationID:       "corr-install-plan-content-blocks",
		Success:             true,
		RespondingAgentID:   "academic-1",
		RespondingAgentName: "Academic",
		Data: map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "1. `python -m pip install pytest`"},
				map[string]any{"type": "text", "text": "Validation: `python -m pytest --version`"},
			},
		},
	})
	content, err := ExtractDependencyInstallResearchContent(msg)
	if err != nil {
		t.Fatalf("ExtractDependencyInstallResearchContent() error = %v", err)
	}
	if !strings.Contains(content, "python -m pytest --version") {
		t.Fatalf("content = %q, want validation command from content blocks", content)
	}
}

func TestParseDependencyInstallPlan_FallsBackToTextInstructions(t *testing.T) {
	raw := strings.TrimSpace(`
Install pytest for this repository.

1. ` + "`python -m pip install pytest`" + `
Validation: ` + "`python -m pytest --version`" + `
`)
	plan, err := ParseDependencyInstallPlan(raw)
	if err != nil {
		t.Fatalf("ParseDependencyInstallPlan() error = %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Command != "python -m pip install pytest" {
		t.Fatalf("steps = %#v", plan.Steps)
	}
	if plan.ValidationCommand != "python -m pytest --version" {
		t.Fatalf("validation_command = %q, want python -m pytest --version", plan.ValidationCommand)
	}
}

// TestExecuteDependencyInstallPlan_RoutesInstallsThroughBrokerWithoutDiskWrites
// verifies the strict no-disk invariant: an install plan drives the broker
// only, never `exec.CommandContext` on host disk. The test asserts (1) the
// broker is invoked for every approved step and the validation command, (2)
// each broker request carries the approved argv, and (3) nothing the test
// did landed in the working directory as a side effect.
func TestExecuteDependencyInstallPlan_RoutesInstallsThroughBrokerWithoutDiskWrites(t *testing.T) {
	root := t.TempDir()
	zeroleak.Guard(t, root)
	gate := &captureInstallGate{}
	ctx := commandapproval.WithGate(context.Background(), gate)

	var captured []purevfs.BrokerRunRequest
	broker := stubInstallBroker{
		run: func(_ context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			captured = append(captured, req)
			return &purevfs.BrokerRunResult{ExitCode: 0}, nil
		},
	}
	brokerFn, prepare, workspace := strictNoDiskInstallConfig(broker, root)

	result, err := ExecuteDependencyInstallPlan(ctx, DependencyInstallSkillConfig{
		SkillName:          "install_dependency_tooling",
		ResearchSkill:      "research_dependency_install",
		AgentType:          "tester-pipeline",
		AgentID:            func() string { return "tester-1" },
		SessionID:          func() string { return "sess-1" },
		WorkingDir:         func() string { return root },
		ExecutionBroker:    brokerFn,
		PrepareExecution:   prepare,
		ExecutionWorkspace: workspace,
	}, &DependencyInstallPlan{
		Summary:           "Install a marker utility",
		MissingTool:       "marker",
		ValidationCommand: "marker --version",
		Steps: []DependencyInstallStep{
			{Command: "python -m pip install marker", Reason: "install the package"},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteDependencyInstallPlan() error = %v", err)
	}

	if got := len(captured); got != 2 {
		t.Fatalf("broker invocations = %d, want 2 (install step + validation)", got)
	}
	// ShellCommandArgv wraps plain commands as `/bin/sh -c "<command>"`; the
	// user-approved command text lives in Argv[2].
	if got := captured[0].Argv; len(got) != 3 || got[0] != "/bin/sh" || !strings.Contains(got[2], "pip install marker") {
		t.Fatalf("install argv = %#v, want [/bin/sh -c \"... pip install marker\"]", got)
	}
	if got := captured[1].Argv; len(got) != 3 || got[0] != "/bin/sh" || !strings.Contains(got[2], "marker --version") {
		t.Fatalf("validation argv = %#v, want [/bin/sh -c \"marker --version\"]", got)
	}

	if got := len(gate.requests); got != 2 {
		t.Fatalf("approval requests = %d, want 2", got)
	}
	for i, req := range gate.requests {
		if req.ApprovalPolicy != commandapproval.ApprovalPolicyExact {
			t.Fatalf("request %d approval policy = %q, want %q", i, req.ApprovalPolicy, commandapproval.ApprovalPolicyExact)
		}
		if req.WorkingDir != root {
			t.Fatalf("request %d working dir = %q, want %q", i, req.WorkingDir, root)
		}
	}
	if installed, _ := result["installed"].(bool); !installed {
		t.Fatalf("result installed = %v, want true", result["installed"])
	}
	// Zero-leak invariant is enforced by the zeroleak.Guard above, which
	// fires on t.Cleanup and fails the test if any inode appeared under
	// root during the install.
}

// TestExecuteDependencyInstallPlan_DoesNotFailWhenValidationCommandFails
// verifies that a non-zero validation exit is recorded but does not propagate
// as an error — the validation command runs through the broker with
// AllowNonZeroExit semantics preserved under the broker-routed fallback.
func TestExecuteDependencyInstallPlan_DoesNotFailWhenValidationCommandFails(t *testing.T) {
	root := t.TempDir()
	gate := &captureInstallGate{}
	ctx := commandapproval.WithGate(context.Background(), gate)

	broker := stubInstallBroker{
		run: func(_ context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			// Install command succeeds; validation fails with exit 1. Commands
			// are shell-wrapped, so the user command text lives in Argv[2].
			shellCmd := ""
			if len(req.Argv) >= 3 {
				shellCmd = req.Argv[2]
			}
			if strings.Contains(shellCmd, "nonexistent-tool") {
				return &purevfs.BrokerRunResult{ExitCode: 1, Stderr: []byte("not installed")}, nil
			}
			return &purevfs.BrokerRunResult{ExitCode: 0}, nil
		},
	}
	brokerFn, prepare, workspace := strictNoDiskInstallConfig(broker, root)

	result, err := ExecuteDependencyInstallPlan(ctx, DependencyInstallSkillConfig{
		SkillName:          "install_dependency_tooling",
		ResearchSkill:      "research_dependency_install",
		AgentType:          "tester-pipeline",
		AgentID:            func() string { return "tester-1" },
		SessionID:          func() string { return "sess-1" },
		WorkingDir:         func() string { return root },
		ExecutionBroker:    brokerFn,
		PrepareExecution:   prepare,
		ExecutionWorkspace: workspace,
	}, &DependencyInstallPlan{
		Summary:           "Install a marker utility",
		MissingTool:       "marker",
		ValidationCommand: "nonexistent-tool --version",
		Steps: []DependencyInstallStep{
			{Command: "python -m pip install marker", Reason: "prove install succeeded"},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteDependencyInstallPlan() error = %v", err)
	}
	validation, ok := result["validation"].(map[string]any)
	if !ok {
		t.Fatalf("validation = %#v, want map", result["validation"])
	}
	if success, _ := validation["success"].(bool); success {
		t.Fatalf("validation success = true, want false: %#v", validation)
	}
	if passed, _ := result["validation_passed"].(bool); passed {
		t.Fatalf("validation_passed = true, want false")
	}
}

// TestExecuteDependencyInstallPlan_RefusesWhenBrokerUnavailable locks in the
// no-silent-fallback invariant: if the broker wiring is missing, the install
// fails loudly rather than silently executing on host disk.
func TestExecuteDependencyInstallPlan_RefusesWhenBrokerUnavailable(t *testing.T) {
	root := t.TempDir()
	gate := &captureInstallGate{}
	ctx := commandapproval.WithGate(context.Background(), gate)

	_, err := ExecuteDependencyInstallPlan(ctx, DependencyInstallSkillConfig{
		SkillName:     "install_dependency_tooling",
		ResearchSkill: "research_dependency_install",
		AgentType:     "tester-pipeline",
		AgentID:       func() string { return "tester-1" },
		SessionID:     func() string { return "sess-1" },
		WorkingDir:    func() string { return root },
		// Intentionally leave ExecutionBroker/PrepareExecution/ExecutionWorkspace nil.
	}, &DependencyInstallPlan{
		Summary:     "Install a marker utility",
		MissingTool: "marker",
		Steps: []DependencyInstallStep{
			{Command: "python -m pip install marker", Reason: "install the package"},
		},
	})
	if err == nil {
		t.Fatal("expected ErrStrictExecutionUnavailable when broker wiring missing, got nil")
	}
}

// _ forces filepath to stay imported — it is referenced by other tests in
// this package when they construct paths.
var _ = filepath.Join

func TestExecuteDependencyInstallPlan_RespectsValidatePlanHook(t *testing.T) {
	_, err := ExecuteDependencyInstallPlan(context.Background(), DependencyInstallSkillConfig{
		SkillName: "install_dependency_tooling",
		AgentType: "inspector",
		WorkingDir: func() string {
			return t.TempDir()
		},
		ValidatePlan: func(*DependencyInstallPlan) error {
			return fmt.Errorf("blocked by policy")
		},
	}, &DependencyInstallPlan{
		Summary: "Install tooling",
		Steps: []DependencyInstallStep{
			{Command: "python3 -m pip install mypy"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("expected validate-plan policy error, got %v", err)
	}
}
