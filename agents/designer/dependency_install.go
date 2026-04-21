package designer

import (
	"context"
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

// Phase 2.K / GT-4 + GI-5 refactor: research_dependency_install +
// install_dependency_tooling collapsed into dependency(action=…).
func dependencySkill(d *Designer) *skills.Skill {
	return agentshared.NewDependencyManagementSkill(agentshared.DependencyManagementSkillConfig{
		Category: "general",
		ResearchHandler: func(ctx context.Context, missingTool, failure, frameworkID, runCommand string, files []string, taskSpec string) (*designerDependencyInstallPlan, error) {
			return d.researchDependencyInstall(ctx, missingTool, failure, frameworkID, runCommand, files, taskSpec)
		},
		InstallHandler: d.installDependencyTooling,
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
		SkillName:       "dependency",
		ResearchSkill:   "dependency",
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
	switch versioning.Underlying(fa).(type) {
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
