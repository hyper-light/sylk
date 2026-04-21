package pipeline

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

// Phase 2.K / GT-4 + GI-5 refactor: research_test_tool_install +
// install_test_tooling collapsed into dependency(action=…, category="test").
func dependencySkill(pt *PipelineTester) *skills.Skill {
	return agentshared.NewDependencyManagementSkill(agentshared.DependencyManagementSkillConfig{
		Category: "test",
		ResearchHandler: func(ctx context.Context, missingTool, failure, frameworkID, runCommand string, files []string, taskSpec string) (*testToolInstallPlan, error) {
			return pt.researchTestToolInstall(ctx, missingTool, failure, frameworkID, runCommand, files, taskSpec, "")
		},
		InstallHandler: pt.installTestTooling,
	})
}

type testerCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

func (pt *PipelineTester) researchTestToolInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec, workerType string,
) (*testToolInstallPlan, error) {
	if pt.bus == nil || pt.channels == nil {
		return nil, fmt.Errorf("tester bus is unavailable for Academic install research")
	}
	harness := pt.currentHarnessState()
	if harness == nil && len(files) > 0 {
		detected, err := pt.detectHarness(ctx, files, taskSpec, workerType)
		if err == nil {
			harness = detected
		}
	}
	if frameworkID == "" && harness != nil {
		frameworkID = string(harness.FrameworkID)
	}
	if runCommand == "" && harness != nil {
		runCommand = harness.RunCommand
	}
	signals := pt.projectInstallSignals(ctx)
	if harness != nil {
		signals = append(signals,
			fmt.Sprintf("framework=%s", harness.FrameworkName),
			fmt.Sprintf("language=%s", harness.Language),
		)
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             pt.bus,
		ResponseTopic:   pt.channels.Responses,
		SourceAgentID:   pt.id,
		SourceAgentName: "tester-pipeline",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  pt.workingDir(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  signals,
	})
}

func (pt *PipelineTester) projectInstallSignals(ctx context.Context) []string {
	return agentshared.ProjectInstallSignals(ctx, pt.fileAccess, pt.workingDir())
}

func parseTestToolInstallPlan(raw string) (*testToolInstallPlan, error) {
	return agentshared.ParseDependencyInstallPlan(raw)
}

func validateTestToolInstallPlan(plan *testToolInstallPlan) error {
	return agentshared.ValidateDependencyInstallPlan(plan)
}

func testerCommandHasUnsafeShellSyntax(command string) bool {
	return agentshared.DependencyCommandHasUnsafeShellSyntax(command)
}

func formatTestToolInstallPlan(plan *testToolInstallPlan) string {
	return agentshared.FormatDependencyInstallPlan(plan)
}

func (pt *PipelineTester) installTestTooling(ctx context.Context, plan *testToolInstallPlan) (map[string]any, error) {
	return agentshared.ExecuteDependencyInstallPlan(ctx, agentshared.DependencyInstallSkillConfig{
		// Phase 2.K: ToolName surfaces in the approval dialogue —
		// keep it specific so operators see what's being installed.
		SkillName:       "dependency",
		ResearchSkill:   "dependency",
		AgentType:       "tester-pipeline",
		AgentID:         func() string { return pt.id },
		SessionID:       func() string { return pt.config.SessionID },
		WorkingDir:      pt.workingDir,
		DefaultTimeout:  func() time.Duration { return pt.config.DefaultTimeout },
		ExecutionBroker: func() purevfs.ExecutionBroker { return pt.executionBroker },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := pt.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{WorkDir: execCtx.workDir, Plan: execCtx.plan}, nil
		},
		ExecutionWorkspace: pt.executionWorkspace,
	}, plan)
}

func (pt *PipelineTester) commandExecutionContext(ctx context.Context, requested string) (testerCommandExecContext, error) {
	overlay, overlayDeletes := overlayState(pt.fileAccess)
	workspaceRoot := pt.workingDir()
	language := ""
	if harness := pt.currentHarnessState(); harness != nil {
		language = harness.Language
	}
	if strings.TrimSpace(language) == "" {
		language = purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	}
	planner := purevfs.NewExecutionPlanner(nil, pt.executionCapabilities(ctx))
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
		return testerCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return testerCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return testerCommandExecContext{
		workDir: runDir,
		plan:    plan,
	}, nil
}
