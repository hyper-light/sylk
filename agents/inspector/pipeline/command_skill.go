package pipeline

import (
	"context"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
)

func runCommandSkill(pi *PipelineInspector) *skills.Skill {
	return agentshared.NewRunCommandSkill(pipelineInspectorCommandSkillConfig(pi))
}

func runShellScriptSkill(pi *PipelineInspector) *skills.Skill {
	return agentshared.NewRunShellScriptSkill(pipelineInspectorCommandSkillConfig(pi))
}

func pipelineInspectorCommandSkillConfig(pi *PipelineInspector) agentshared.CommandSkillConfig {
	return agentshared.CommandSkillConfig{
		AgentType:      "inspector-pipeline",
		AgentID:        func() string { return pi.id },
		SessionID:      func() string { return pi.config.SessionID },
		WorkspaceRoot:  pi.toolRunner.WorkingDir,
		DefaultTimeout: func() time.Duration { return pi.config.DefaultTimeout },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := pi.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{
				WorkDir: execCtx.workDir,
				Plan:    execCtx.plan,
			}, nil
		},
		ExecutionBroker:      func() purevfs.ExecutionBroker { return pi.executionBroker },
		ExecutionWorkspace:   pi.executionWorkspace,
		AllowWorkspaceWrites: false,
	}
}
