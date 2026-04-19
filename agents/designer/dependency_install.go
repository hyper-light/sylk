package designer

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

type designerDependencyInstallPlan = agentshared.DependencyInstallPlan
type designerDependencyInstallStep = agentshared.DependencyInstallStep

type designerCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

type designerOverlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

func researchDependencyInstallSkill(d *Designer) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
	}

	return skills.NewSkill("research_dependency_install").
		Description("Ask Academic to research concrete dependency or tool installation steps for the current design-system or frontend block.").
		Domain("ui").
		Keywords("install", "dependency", "tooling", "package manager", "academic").
		Priority(84).
		Usage("Use when design validation or implementation is blocked by missing tooling, dependencies, or workspace package setup.").
		Requirement("Provide the missing tool/package or the failing output that proves the dependency gap.").
		Satisfies("Produces a concrete install plan that can be shown to the user and then executed through install_dependency_tooling using the existing approval dialogue.").
		Avoid("Do not guess package-manager commands when Academic can infer the repository’s package manager and minimal steps first.").
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
			return d.researchDependencyInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installDependencyToolingSkill(d *Designer) *skills.Skill {
	return agentshared.NewDependencyInstallExecutionSkill(agentshared.DependencyInstallSkillConfig{
		SkillName:     "install_dependency_tooling",
		Description:   "Execute an approved dependency install plan step-by-step using the existing command-approval dialogue.",
		Domain:        "ui",
		Keywords:      []string{"install", "dependency", "tooling", "approval", "package manager"},
		Priority:      82,
		Usage:         "Use after research_dependency_install once you have a concrete plan to show the user. Each command goes through the existing approval dialogue and executes against the real disk workspace.",
		Requirement:   "Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.",
		Satisfies:     "Installs missing project tooling or dependencies to disk and captures command output plus optional validation evidence.",
		Avoid:         "Do not use for speculative dependency changes or arbitrary shell work unrelated to unblocking the requested project tooling.",
		ResearchSkill: "research_dependency_install",
		AgentType:     "designer",
		AgentID:       func() string { return d.id },
		SessionID:     func() string { return d.config.SessionID },
		WorkingDir:    d.effectiveWorkingDirectory,
		DefaultTimeout: func() time.Duration {
			return d.config.DesignerConfig.DefaultTimeout
		},
	})
}

func (d *Designer) researchDependencyInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*designerDependencyInstallPlan, error) {
	if d.bus == nil || d.channels == nil {
		return nil, fmt.Errorf("designer bus is unavailable for Academic install research")
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             d.bus,
		ResponseTopic:   d.channels.Responses,
		SourceAgentID:   d.id,
		SourceAgentName: "designer",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  d.effectiveWorkingDirectory(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  agentshared.ProjectInstallSignals(ctx, d.fileAccess, d.effectiveWorkingDirectory()),
	})
}

func (d *Designer) installDependencyTooling(ctx context.Context, plan *designerDependencyInstallPlan) (map[string]any, error) {
	return agentshared.ExecuteDependencyInstallPlan(ctx, agentshared.DependencyInstallSkillConfig{
		SkillName:       "install_dependency_tooling",
		ResearchSkill:   "research_dependency_install",
		AgentType:       "designer",
		AgentID:         func() string { return d.id },
		SessionID:       func() string { return d.config.SessionID },
		WorkingDir:      d.effectiveWorkingDirectory,
		DefaultTimeout:  func() time.Duration { return d.config.DesignerConfig.DefaultTimeout },
		ExecutionBroker: func() purevfs.ExecutionBroker { return d.executionBroker },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := d.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{WorkDir: execCtx.workDir, Plan: execCtx.plan}, nil
		},
		ExecutionWorkspace: d.executionWorkspace,
	}, plan)
}

func (d *Designer) effectiveWorkingDirectory() string {
	if d.fileAccess != nil && strings.TrimSpace(d.fileAccess.WorkingDir()) != "" {
		return d.fileAccess.WorkingDir()
	}
	if trimmed := strings.TrimSpace(d.config.DesignerConfig.WorkingDirectory); trimmed != "" {
		return trimmed
	}
	return "."
}

func (d *Designer) commandExecutionContext(ctx context.Context, requested string) (designerCommandExecContext, error) {
	overlay, overlayDeletes := designerOverlayState(d.fileAccess)
	workspaceRoot := d.effectiveWorkingDirectory()
	language := purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	planner := purevfs.NewExecutionPlanner(nil, d.executionCapabilities(ctx))
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
		return designerCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return designerCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return designerCommandExecContext{workDir: runDir, plan: plan}, nil
}

func (d *Designer) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	if d.executionBroker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := d.executionBroker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (d *Designer) executionWorkspace(allowWrites bool) purevfs.ExecutionFS {
	if d.fileAccess == nil {
		return versioning.NewDiskFileAccess(d.effectiveWorkingDirectory(), true)
	}
	if allowWrites {
		if designerWorkspaceWritesAllowed(d.fileAccess) {
			return versioning.TagExecutionFS(d.fileAccess, versioning.WorkspaceMutationOriginCommandExecution)
		}
		return versioning.TagExecutionFS(purevfs.ReadOnlyExecutionFS(d.fileAccess), versioning.WorkspaceMutationOriginCommandExecution)
	}
	return purevfs.ReadOnlyExecutionFS(d.fileAccess)
}

func designerWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
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

func designerOverlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(designerOverlayAwareFileAccess)
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
