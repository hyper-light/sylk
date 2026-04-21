package pipeline

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

// Phase 2.K / GT-4 + GI-5 refactor: research_dependency_install +
// install_dependency_tooling collapsed into dependency(action=…).
// Inspector keeps the test-tooling reject gate (tester owns test tooling).
func dependencySkill(pi *PipelineInspector) *skills.Skill {
	return agentshared.NewDependencyManagementSkill(agentshared.DependencyManagementSkillConfig{
		Category: "general",
		ResearchHandler: func(ctx context.Context, missingTool, failure, frameworkID, runCommand string, files []string, taskSpec string) (*pipelineInspectorDependencyInstallPlan, error) {
			if err := inspectorshared.InspectorRejectTestDependencyResearch(missingTool, frameworkID, runCommand, failure); err != nil {
				return nil, err
			}
			return pi.researchDependencyInstall(ctx, missingTool, failure, frameworkID, runCommand, files, taskSpec)
		},
		InstallHandler: func(ctx context.Context, plan *pipelineInspectorDependencyInstallPlan) (map[string]any, error) {
			if err := inspectorshared.InspectorRejectTestDependencyInstallPlan(plan); err != nil {
				return nil, err
			}
			return pi.installDependencyTooling(ctx, plan)
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
		SkillName:       "dependency",
		ResearchSkill:   "dependency",
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
	switch versioning.Underlying(fa).(type) {
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
