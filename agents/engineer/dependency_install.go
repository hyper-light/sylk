package engineer

import (
	"context"
	"fmt"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type dependencyInstallPlan = agentshared.DependencyInstallPlan
type dependencyInstallStep = agentshared.DependencyInstallStep

// Phase 2.K / GT-4 + GI-5 refactor: research_dependency_install +
// install_dependency_tooling collapsed into dependency(action=…).
func dependencySkill(e *Engineer) *skills.Skill {
	return agentshared.NewDependencyManagementSkill(agentshared.DependencyManagementSkillConfig{
		Category: "general",
		ResearchHandler: func(ctx context.Context, missingTool, failure, frameworkID, runCommand string, files []string, taskSpec string) (*dependencyInstallPlan, error) {
			return e.researchDependencyInstall(ctx, missingTool, failure, frameworkID, runCommand, files, taskSpec)
		},
		InstallHandler: e.installDependencyTooling,
	})
}

func (e *Engineer) researchDependencyInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*dependencyInstallPlan, error) {
	if e.bus == nil || e.channels == nil {
		return nil, fmt.Errorf("engineer bus is unavailable for Academic install research")
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             e.bus,
		ResponseTopic:   e.channels.Responses,
		SourceAgentID:   e.id,
		SourceAgentName: "engineer",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  e.effectiveWorkingDirectory(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  agentshared.ProjectInstallSignals(ctx, e.fileAccess, e.effectiveWorkingDirectory()),
	})
}

func (e *Engineer) installDependencyTooling(ctx context.Context, plan *dependencyInstallPlan) (map[string]any, error) {
	return agentshared.ExecuteDependencyInstallPlan(ctx, agentshared.DependencyInstallSkillConfig{
		SkillName:       "dependency",
		ResearchSkill:   "dependency",
		AgentType:       "engineer",
		AgentID:         func() string { return e.id },
		SessionID:       func() string { return e.config.SessionID },
		WorkingDir:      e.effectiveWorkingDirectory,
		CommandsEnabled: func() bool { return e.config.EngineerConfig.EnableCommands },
		DefaultTimeout:  func() time.Duration { return e.config.EngineerConfig.CommandTimeout },
		ExecutionBroker: func() purevfs.ExecutionBroker { return e.executionBroker },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := e.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{WorkDir: execCtx.workDir, Plan: execCtx.plan}, nil
		},
		ExecutionWorkspace: e.executionWorkspace,
	}, plan)
}
