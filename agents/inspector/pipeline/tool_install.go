package pipeline

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
		Description("Ask Academic to research concrete dependency or tool installation steps for missing validation tooling.").
		Domain("analysis").
		Keywords("install", "dependency", "tooling", "academic", "linter", "type checker").
		Priority(83).
		Usage("Use when validation or analysis is blocked by missing project tooling and you need concrete install steps before proceeding.").
		Requirement("Provide the missing tool/package or the failing output that proves the dependency gap.").
		Satisfies("Produces a concrete install plan that can be shown to the user and then executed through install_dependency_tooling using the existing approval dialogue.").
		Avoid("Do not guess install commands when Academic can infer the repository’s package manager and minimal steps first.").
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
			return pi.researchDependencyInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installDependencyToolingSkill(pi *PipelineInspector) *skills.Skill {
	return agentshared.NewDependencyInstallExecutionSkill(agentshared.DependencyInstallSkillConfig{
		SkillName:     "install_dependency_tooling",
		Description:   "Execute an approved dependency install plan step-by-step using the existing command-approval dialogue.",
		Domain:        "analysis",
		Keywords:      []string{"install", "dependency", "tooling", "approval", "package manager"},
		Priority:      81,
		Usage:         "Use after research_dependency_install once you have a concrete plan to show the user. Each command goes through the existing approval dialogue and executes against the real disk workspace.",
		Requirement:   "Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.",
		Satisfies:     "Installs missing project tooling or dependencies to disk and captures command output plus optional validation evidence.",
		Avoid:         "Do not use for speculative dependency changes or arbitrary shell work unrelated to unblocking the requested project tooling.",
		ResearchSkill: "research_dependency_install",
		AgentType:     "inspector-pipeline",
		AgentID:       func() string { return pi.id },
		SessionID:     func() string { return pi.config.SessionID },
		WorkingDir:    pi.toolRunner.WorkingDir,
		DefaultTimeout: func() time.Duration {
			return pi.config.DefaultTimeout
		},
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
		SkillName:      "install_dependency_tooling",
		ResearchSkill:  "research_dependency_install",
		AgentType:      "inspector-pipeline",
		AgentID:        func() string { return pi.id },
		SessionID:      func() string { return pi.config.SessionID },
		WorkingDir:     pi.toolRunner.WorkingDir,
		DefaultTimeout: func() time.Duration { return pi.config.DefaultTimeout },
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
