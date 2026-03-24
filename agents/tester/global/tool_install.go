package global

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type testToolInstallStep = agentshared.DependencyInstallStep
type testToolInstallPlan = agentshared.DependencyInstallPlan

type globalTesterCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

type globalTesterOverlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

func researchTestToolInstallSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
	}

	return skills.NewSkill("research_test_tool_install").
		Description("Ask Academic to research concrete installation steps for missing global test tooling, then synthesize the result into an executable step plan.").
		Domain("testing").
		Keywords("install", "tooling", "missing dependency", "academic", "pytest", "playwright").
		Priority(91).
		Usage("Use when run_test_suite or harness preparation is blocked by missing global test tooling. Pass the failing command/output so Academic can research concrete, project-aware install steps.").
		Requirement("Provide the missing tool or the failing output that proves the current test command cannot run.").
		Satisfies("Produces a concrete install plan that can be explained to the user and then executed through install_test_tooling with standard approval prompts.").
		Avoid("Do not guess package-manager commands when this skill can research them first. Do not use it for ordinary test failures that are not missing-tool problems.").
		StringParam("missing_tool", "Name of the missing executable or package if already known.", false).
		StringParam("failure", "The failing test output or error that indicates missing tooling.", false).
		StringParam("framework_id", "Detected framework identifier such as pytest or playwright.", false).
		StringParam("run_command", "The test command that failed or is expected to fail.", false).
		ArrayParam("files", "Relevant source or test files for project context.", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return gt.researchTestToolInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installTestToolingSkill(gt *GlobalTester) *skills.Skill {
	return agentshared.NewDependencyInstallExecutionSkill(agentshared.DependencyInstallSkillConfig{
		SkillName:     "install_test_tooling",
		Description:   "Execute an approved global test-tool installation plan step-by-step using the existing command-approval dialogue.",
		Domain:        "testing",
		Keywords:      []string{"install", "tooling", "dependency", "pytest", "playwright", "approval"},
		Priority:      89,
		Usage:         "Use after research_test_tool_install once you have a concrete plan to show the user. Each command goes through the existing approval dialogue and executes against the real disk workspace.",
		Requirement:   "Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.",
		Satisfies:     "Installs missing global test tooling to disk, captures command output, and optionally validates that the toolchain is now runnable.",
		Avoid:         "Do not use for speculative dependency changes or for arbitrary shell work unrelated to restoring the global test toolchain.",
		ResearchSkill: "research_test_tool_install",
		AgentType:     "tester",
		AgentID:       func() string { return gt.id },
		SessionID:     func() string { return gt.config.SessionID },
		WorkingDir:    gt.workingDir,
		DefaultTimeout: func() time.Duration {
			return gt.config.DefaultTimeout
		},
	})
}

func (gt *GlobalTester) researchTestToolInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*testToolInstallPlan, error) {
	if gt.bus == nil || gt.channels == nil {
		return nil, fmt.Errorf("global tester bus is unavailable for Academic install research")
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             gt.bus,
		ResponseTopic:   gt.channels.Responses,
		SourceAgentID:   gt.id,
		SourceAgentName: "tester",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  gt.workingDir(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  agentshared.ProjectInstallSignals(ctx, gt.fileAccess, gt.workingDir()),
	})
}

func (gt *GlobalTester) installTestTooling(ctx context.Context, plan *testToolInstallPlan) (map[string]any, error) {
	return agentshared.ExecuteDependencyInstallPlan(ctx, agentshared.DependencyInstallSkillConfig{
		SkillName:      "install_test_tooling",
		ResearchSkill:  "research_test_tool_install",
		AgentType:      "tester",
		AgentID:        func() string { return gt.id },
		SessionID:      func() string { return gt.config.SessionID },
		WorkingDir:     gt.workingDir,
		DefaultTimeout: func() time.Duration { return gt.config.DefaultTimeout },
	}, plan)
}

func (gt *GlobalTester) workingDir() string {
	if gt.fileAccess != nil && strings.TrimSpace(gt.fileAccess.WorkingDir()) != "" {
		return gt.fileAccess.WorkingDir()
	}
	return "."
}

func (gt *GlobalTester) commandExecutionContext(ctx context.Context, requested string) (globalTesterCommandExecContext, error) {
	overlay, overlayDeletes := globalTesterOverlayState(gt.fileAccess)
	workspaceRoot := gt.workingDir()
	language := purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	planner := purevfs.NewExecutionPlanner(nil, gt.executionCapabilities(ctx))
	plan, err := planner.Plan(purevfs.ExecutionRequest{
		Mode:           purevfs.ExecutionModeStrictNoDisk,
		Intent:         purevfs.ExecutionIntentCommand,
		Language:       language,
		WorkspaceRoot:  workspaceRoot,
		WorkingDir:     requested,
		Overlay:        overlay,
		OverlayDeletes: overlayDeletes,
	})
	if err != nil {
		return globalTesterCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return globalTesterCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return globalTesterCommandExecContext{workDir: runDir, plan: plan}, nil
}

func (gt *GlobalTester) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	if gt.executionBroker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := gt.executionBroker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (gt *GlobalTester) executionWorkspace(allowWrites bool) purevfs.ExecutionFS {
	if gt.fileAccess == nil {
		return versioning.NewDiskFileAccess(gt.workingDir(), true)
	}
	if allowWrites && globalTesterWorkspaceWritesAllowed(gt.fileAccess) {
		return gt.fileAccess
	}
	return purevfs.ReadOnlyExecutionFS(gt.fileAccess)
}

func globalTesterWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
	if fa == nil || fa.IsReadOnly() {
		return false
	}
	switch fa.(type) {
	case *versioning.DiskFileAccess:
		return false
	default:
		return true
	}
}

func globalTesterOverlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(globalTesterOverlayAwareFileAccess)
	if !ok {
		return false, false
	}
	mods := overlay.Modifications()
	if len(mods) == 0 {
		return false, false
	}
	hasDeletes := false
	for _, mod := range mods {
		if mod.Operation == versioning.FileOpDelete {
			hasDeletes = true
			break
		}
	}
	return true, hasDeletes
}
