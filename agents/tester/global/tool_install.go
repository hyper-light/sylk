package global

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

// Phase 2.K / GT-4 + GI-5 refactor: research_test_tool_install +
// install_test_tooling collapsed into dependency(action=…, category="test").
func dependencySkill(gt *GlobalTester) *skills.Skill {
	return agentshared.NewDependencyManagementSkill(agentshared.DependencyManagementSkillConfig{
		Category:        "test",
		ResearchHandler: gt.researchTestToolInstall,
		InstallHandler:  gt.installTestTooling,
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
		SkillName:       "install_test_tooling",
		ResearchSkill:   "research_test_tool_install",
		AgentType:       "tester",
		AgentID:         func() string { return gt.id },
		SessionID:       func() string { return gt.config.SessionID },
		WorkingDir:      gt.workingDir,
		DefaultTimeout:  func() time.Duration { return gt.config.DefaultTimeout },
		ExecutionBroker: func() purevfs.ExecutionBroker { return gt.executionBroker },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := gt.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{WorkDir: execCtx.workDir, Plan: execCtx.plan}, nil
		},
		ExecutionWorkspace: gt.executionWorkspace,
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
	if allowWrites {
		if globalTesterWorkspaceWritesAllowed(gt.fileAccess) {
			return versioning.TagExecutionFS(gt.fileAccess, versioning.WorkspaceMutationOriginCommandExecution)
		}
		return versioning.TagExecutionFS(purevfs.ReadOnlyExecutionFS(gt.fileAccess), versioning.WorkspaceMutationOriginCommandExecution)
	}
	return purevfs.ReadOnlyExecutionFS(gt.fileAccess)
}

func globalTesterWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
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
