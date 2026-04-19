package global

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	inspectorshared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type globalInspectorDependencyInstallPlan = agentshared.DependencyInstallPlan
type globalInspectorDependencyInstallStep = agentshared.DependencyInstallStep

type globalInspectorCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

type globalInspectorOverlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

func researchDependencyInstallSkill(gi *GlobalInspector) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
	}

	return skills.NewSkill("research_dependency_install").
		Description("Ask Academic to research concrete installation steps for missing non-test audit or validation tooling.").
		Domain("audit").
		Keywords("install", "dependency", "tooling", "academic", "linter", "type checker").
		Priority(83).
		Usage("Use when global validation or audit work is blocked by missing non-test project tooling and you need concrete install steps before proceeding.").
		Requirement("Provide the missing non-test tool/package or the failing output that proves the dependency gap.").
		Satisfies("Produces a concrete install plan for non-test audit or validation tooling that can be shown to the user and then executed through install_dependency_tooling using the existing approval dialogue.").
		Avoid("Do not guess install commands when Academic can infer the repository’s package manager and minimal steps first.").
		Avoid("Do not use for pytest, vitest, jest, playwright, cypress, or any other test-execution tool; route that work to Tester so it can use `research_test_tool_install` and `install_test_tooling`.").
		StringParam("missing_tool", "Missing executable or package if already known.", false).
		StringParam("failure", "Error output showing the missing dependency or tool.", false).
		StringParam("framework_id", "Detected framework or ecosystem identifier.", false).
		StringParam("run_command", "Blocked command that would verify or use the dependency.", false).
		ArrayParam("files", "Relevant source or config files for project context.", "string", false).
		StringParam("task_spec", "Task brief or acceptance criteria.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if err := inspectorshared.InspectorRejectTestDependencyResearch(p.MissingTool, p.FrameworkID, p.RunCommand, p.Failure); err != nil {
				return nil, err
			}
			return gi.researchDependencyInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installDependencyToolingSkill(gi *GlobalInspector) *skills.Skill {
	return agentshared.NewDependencyInstallExecutionSkill(agentshared.DependencyInstallSkillConfig{
		SkillName:     "install_dependency_tooling",
		Description:   "Execute an approved dependency install plan step-by-step for non-test tooling using the existing command-approval dialogue.",
		Domain:        "audit",
		Keywords:      []string{"install", "dependency", "tooling", "approval", "package manager"},
		Priority:      81,
		Usage:         "Use after research_dependency_install once you have a concrete plan to show the user for missing non-test tooling. Each command goes through the existing approval dialogue and executes against the real disk workspace.",
		Requirement:   "Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.",
		Satisfies:     "Installs missing non-test project tooling or dependencies to disk and captures command output plus optional validation evidence.",
		Avoid:         "Do not use for speculative dependency changes, arbitrary shell work unrelated to unblocking the requested project tooling, or test runners/harnesses/execution-only test tooling; route those to Tester so it can use `research_test_tool_install` and `install_test_tooling`.",
		ResearchSkill: "research_dependency_install",
		AgentType:     "inspector",
		AgentID:       func() string { return gi.id },
		SessionID:     func() string { return gi.config.SessionID },
		WorkingDir:    gi.toolRunner.WorkingDir,
		DefaultTimeout: func() time.Duration {
			return gi.config.DefaultTimeout
		},
		ValidatePlan: inspectorshared.InspectorRejectTestDependencyInstallPlan,
	})
}

func (gi *GlobalInspector) researchDependencyInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*globalInspectorDependencyInstallPlan, error) {
	if gi.bus == nil || gi.channels == nil {
		return nil, fmt.Errorf("global inspector bus is unavailable for Academic install research")
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             gi.bus,
		ResponseTopic:   gi.channels.Responses,
		SourceAgentID:   gi.id,
		SourceAgentName: "inspector",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  gi.toolRunner.WorkingDir(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  agentshared.ProjectInstallSignals(ctx, gi.fileAccess, gi.toolRunner.WorkingDir()),
	})
}

func (gi *GlobalInspector) installDependencyTooling(ctx context.Context, plan *globalInspectorDependencyInstallPlan) (map[string]any, error) {
	return agentshared.ExecuteDependencyInstallPlan(ctx, agentshared.DependencyInstallSkillConfig{
		SkillName:       "install_dependency_tooling",
		ResearchSkill:   "research_dependency_install",
		AgentType:       "inspector",
		AgentID:         func() string { return gi.id },
		SessionID:       func() string { return gi.config.SessionID },
		WorkingDir:      gi.toolRunner.WorkingDir,
		DefaultTimeout:  func() time.Duration { return gi.config.DefaultTimeout },
		ValidatePlan:    inspectorshared.InspectorRejectTestDependencyInstallPlan,
		ExecutionBroker: func() purevfs.ExecutionBroker { return gi.executionBroker },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := gi.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{WorkDir: execCtx.workDir, Plan: execCtx.plan}, nil
		},
		ExecutionWorkspace: gi.executionWorkspace,
	}, plan)
}

func (gi *GlobalInspector) commandExecutionContext(ctx context.Context, requested string) (globalInspectorCommandExecContext, error) {
	overlay, overlayDeletes := globalInspectorOverlayState(gi.fileAccess)
	workspaceRoot := gi.toolRunner.WorkingDir()
	language := purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	planner := purevfs.NewExecutionPlanner(nil, gi.executionCapabilities(ctx))
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
		return globalInspectorCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return globalInspectorCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return globalInspectorCommandExecContext{workDir: runDir, plan: plan}, nil
}

func (gi *GlobalInspector) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	if gi.executionBroker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := gi.executionBroker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (gi *GlobalInspector) executionWorkspace(allowWrites bool) purevfs.ExecutionFS {
	if gi.fileAccess == nil {
		return versioning.NewDiskFileAccess(gi.toolRunner.WorkingDir(), true)
	}
	if allowWrites && globalInspectorWorkspaceWritesAllowed(gi.fileAccess) {
		return gi.fileAccess
	}
	return purevfs.ReadOnlyExecutionFS(gi.fileAccess)
}

func globalInspectorWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
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

func globalInspectorOverlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(globalInspectorOverlayAwareFileAccess)
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
