package global

import (
	"context"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
)

func runCommandSkill(gt *GlobalTester) *skills.Skill {
	return agentshared.NewRunCommandSkill(globalTesterCommandSkillConfig(gt))
}

func runShellScriptSkill(gt *GlobalTester) *skills.Skill {
	return agentshared.NewRunShellScriptSkill(globalTesterCommandSkillConfig(gt))
}

func globalTesterCommandSkillConfig(gt *GlobalTester) agentshared.CommandSkillConfig {
	return agentshared.CommandSkillConfig{
		AgentType:      "tester",
		AgentID:        func() string { return gt.id },
		SessionID:      func() string { return gt.config.SessionID },
		WorkspaceRoot:  gt.workingDir,
		DefaultTimeout: func() time.Duration { return gt.config.DefaultTimeout },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := gt.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{
				WorkDir: execCtx.workDir,
				Plan:    execCtx.plan,
			}, nil
		},
		ExecutionBroker:      func() purevfs.ExecutionBroker { return gt.executionBroker },
		ExecutionWorkspace:   gt.executionWorkspace,
		AllowWorkspaceWrites: true,
	}
}
