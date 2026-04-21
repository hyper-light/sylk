package pipeline

import (
	"context"
	"time"

	inspectorshared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
)

func bashSkill(pi *PipelineInspector) *skills.Skill {
	return agentshared.NewBashSkill(pipelineInspectorCommandSkillConfig(pi))
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
		PreAuthorizeCheck: func(command, toolName string) error {
			return inspectorshared.InspectorRejectTestExecution(command, toolName)
		},
	}
}
