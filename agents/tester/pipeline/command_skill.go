package pipeline

import (
	"context"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
)

func bashSkill(pt *PipelineTester) *skills.Skill {
	return agentshared.NewBashSkill(pipelineTesterCommandSkillConfig(pt))
}

func pipelineTesterCommandSkillConfig(pt *PipelineTester) agentshared.CommandSkillConfig {
	return agentshared.CommandSkillConfig{
		AgentType:      "tester-pipeline",
		AgentID:        func() string { return pt.id },
		SessionID:      func() string { return pt.config.SessionID },
		WorkspaceRoot:  pt.workingDir,
		DefaultTimeout: func() time.Duration { return pt.config.DefaultTimeout },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := pt.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{
				WorkDir: execCtx.workDir,
				Plan:    execCtx.plan,
			}, nil
		},
		ExecutionBroker:      func() purevfs.ExecutionBroker { return pt.executionBroker },
		ExecutionWorkspace:   pt.executionWorkspace,
		AllowWorkspaceWrites: true,
	}
}
