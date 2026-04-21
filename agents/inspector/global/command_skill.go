package global

import (
	"context"
	"time"

	inspectorshared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
)

func bashSkill(gi *GlobalInspector) *skills.Skill {
	return agentshared.NewBashSkill(globalInspectorCommandSkillConfig(gi))
}

func globalInspectorCommandSkillConfig(gi *GlobalInspector) agentshared.CommandSkillConfig {
	return agentshared.CommandSkillConfig{
		AgentType:      "inspector",
		AgentID:        func() string { return gi.id },
		SessionID:      func() string { return gi.config.SessionID },
		WorkspaceRoot:  gi.toolRunner.WorkingDir,
		DefaultTimeout: func() time.Duration { return gi.config.DefaultTimeout },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := gi.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{
				WorkDir: execCtx.workDir,
				Plan:    execCtx.plan,
			}, nil
		},
		ExecutionBroker:      func() purevfs.ExecutionBroker { return gi.executionBroker },
		ExecutionWorkspace:   gi.executionWorkspace,
		AllowWorkspaceWrites: false,
		PreAuthorizeCheck: func(command, toolName string) error {
			return inspectorshared.InspectorRejectTestExecution(command, toolName)
		},
	}
}
