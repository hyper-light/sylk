package shared

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/commandapproval"
)

type captureInstallGate struct {
	requests []commandapproval.Request
}

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
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestExecuteDependencyInstallPlan_WritesToDiskWithExactApproval(t *testing.T) {
	root := t.TempDir()
	gate := &captureInstallGate{}
	ctx := commandapproval.WithGate(context.Background(), gate)

	result, err := ExecuteDependencyInstallPlan(ctx, DependencyInstallSkillConfig{
		SkillName:     "install_dependency_tooling",
		ResearchSkill: "research_dependency_install",
		AgentType:     "tester-pipeline",
		AgentID:       func() string { return "tester-1" },
		SessionID:     func() string { return "sess-1" },
		WorkingDir:    func() string { return root },
	}, &DependencyInstallPlan{
		Summary:           "Install a marker utility",
		MissingTool:       "marker",
		ValidationCommand: "test -f .install-marker",
		Steps: []DependencyInstallStep{
			{Command: "touch .install-marker", Reason: "prove installs write to disk rather than VFS"},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteDependencyInstallPlan() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".install-marker")); err != nil {
		t.Fatalf("disk marker missing: %v", err)
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
}
