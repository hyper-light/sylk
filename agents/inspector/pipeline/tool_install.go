package pipeline

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

type pipelineInspectorDependencyInstallPlan = agentshared.DependencyInstallPlan
type pipelineInspectorDependencyInstallStep = agentshared.DependencyInstallStep

type pipelineInspectorCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

type pipelineInspectorOverlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

func researchDependencyInstallSkill(pi *PipelineInspector) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
	}

	return skills.NewSkill("research_dependency_install").
		Description("Ask Academic to research concrete installation steps for missing non-test validation or audit tooling.").
		Domain("analysis").
		Keywords("install", "dependency", "tooling", "academic", "linter", "type checker").
		Priority(83).
		Usage("Use when validation or analysis is blocked by missing non-test project tooling and you need concrete install steps before proceeding.").
		Requirement("Provide the missing non-test tool/package or the failing output that proves the dependency gap.").
		Satisfies("Produces a concrete install plan for non-test validation or audit tooling that can be shown to the user and then executed through install_dependency_tooling using the existing approval dialogue.").
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
			return pi.researchDependencyInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installDependencyToolingSkill(pi *PipelineInspector) *skills.Skill {
	return agentshared.NewDependencyInstallExecutionSkill(agentshared.DependencyInstallSkillConfig{
		SkillName:     "install_dependency_tooling",
		Description:   "Execute an approved dependency install plan step-by-step for non-test tooling using the existing command-approval dialogue.",
		Domain:        "analysis",
		Keywords:      []string{"install", "dependency", "tooling", "approval", "package manager"},
		Priority:      81,
		Usage:         "Use after research_dependency_install once you have a concrete plan to show the user for missing non-test tooling. Each command goes through the existing approval dialogue and executes against the real disk workspace.",
		Requirement:   "Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.",
		Satisfies:     "Installs missing non-test project tooling or dependencies to disk and captures command output plus optional validation evidence.",
		Avoid:         "Do not use for speculative dependency changes, arbitrary shell work unrelated to unblocking the requested project tooling, or test runners/harnesses/execution-only test tooling; route those to Tester so it can use `research_test_tool_install` and `install_test_tooling`.",
		ResearchSkill: "research_dependency_install",
		AgentType:     "inspector-pipeline",
		AgentID:       func() string { return pi.id },
		SessionID:     func() string { return pi.config.SessionID },
		WorkingDir:    pi.toolRunner.WorkingDir,
		DefaultTimeout: func() time.Duration {
			return pi.config.DefaultTimeout
		},
		ValidatePlan: inspectorshared.InspectorRejectTestDependencyInstallPlan,
	})
}

func (pi *PipelineInspector) researchDependencyInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*pipelineInspectorDependencyInstallPlan, error) {
	if pi.bus == nil || pi.channels == nil {
		return nil, fmt.Errorf("pipeline inspector bus is unavailable for Academic install research")
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             pi.bus,
		ResponseTopic:   pi.channels.Responses,
		SourceAgentID:   pi.id,
		SourceAgentName: "inspector-pipeline",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  pi.toolRunner.WorkingDir(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  agentshared.ProjectInstallSignals(ctx, pi.fileAccess, pi.toolRunner.WorkingDir()),
	})
}

func (pi *PipelineInspector) installDependencyTooling(ctx context.Context, plan *pipelineInspectorDependencyInstallPlan) (map[string]any, error) {
	return agentshared.ExecuteDependencyInstallPlan(ctx, agentshared.DependencyInstallSkillConfig{
		SkillName:       "install_dependency_tooling",
		ResearchSkill:   "research_dependency_install",
		AgentType:       "inspector-pipeline",
		AgentID:         func() string { return pi.id },
		SessionID:       func() string { return pi.config.SessionID },
		WorkingDir:      pi.toolRunner.WorkingDir,
		DefaultTimeout:  func() time.Duration { return pi.config.DefaultTimeout },
		ValidatePlan:    inspectorshared.InspectorRejectTestDependencyInstallPlan,
		ExecutionBroker: func() purevfs.ExecutionBroker { return pi.executionBroker },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := pi.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{WorkDir: execCtx.workDir, Plan: execCtx.plan}, nil
		},
		ExecutionWorkspace: pi.executionWorkspace,
	}, plan)
}

func (pi *PipelineInspector) commandExecutionContext(ctx context.Context, requested string) (pipelineInspectorCommandExecContext, error) {
	overlay, overlayDeletes := pipelineInspectorOverlayState(pi.fileAccess)
	workspaceRoot := pi.toolRunner.WorkingDir()
	language := purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	planner := purevfs.NewExecutionPlanner(nil, pi.executionCapabilities(ctx))
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
		return pipelineInspectorCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return pipelineInspectorCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return pipelineInspectorCommandExecContext{workDir: runDir, plan: plan}, nil
}

func (pi *PipelineInspector) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	if pi.executionBroker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := pi.executionBroker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (pi *PipelineInspector) executionWorkspace(allowWrites bool) purevfs.ExecutionFS {
	if pi.fileAccess == nil {
		return versioning.NewDiskFileAccess(pi.toolRunner.WorkingDir(), true)
	}
	if allowWrites && pipelineInspectorWorkspaceWritesAllowed(pi.fileAccess) {
		return pi.fileAccess
	}
	return purevfs.ReadOnlyExecutionFS(pi.fileAccess)
}

func pipelineInspectorWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
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

func pipelineInspectorOverlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(pipelineInspectorOverlayAwareFileAccess)
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
