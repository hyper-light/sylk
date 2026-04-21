package global

import (
	"context"
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

// Phase 2.K / GT-4 + GI-5 refactor: collapsed into dependency(action=…).
func dependencySkill(gi *GlobalInspector) *skills.Skill {
	return agentshared.NewDependencyManagementSkill(agentshared.DependencyManagementSkillConfig{
		Category: "general",
		ResearchHandler: func(ctx context.Context, missingTool, failure, frameworkID, runCommand string, files []string, taskSpec string) (*globalInspectorDependencyInstallPlan, error) {
			if err := inspectorshared.InspectorRejectTestDependencyResearch(missingTool, frameworkID, runCommand, failure); err != nil {
				return nil, err
			}
			return gi.researchDependencyInstall(ctx, missingTool, failure, frameworkID, runCommand, files, taskSpec)
		},
		InstallHandler: func(ctx context.Context, plan *globalInspectorDependencyInstallPlan) (map[string]any, error) {
			if err := inspectorshared.InspectorRejectTestDependencyInstallPlan(plan); err != nil {
				return nil, err
			}
			return gi.installDependencyTooling(ctx, plan)
		},
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
		SkillName:       "dependency",
		ResearchSkill:   "dependency",
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
	switch versioning.Underlying(fa).(type) {
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
