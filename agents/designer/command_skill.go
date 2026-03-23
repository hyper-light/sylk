package designer

import (
	"context"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
)

func runCommandSkill(d *Designer) *skills.Skill {
	return agentshared.NewRunCommandSkill(designerCommandSkillConfig(d))
}

func runShellScriptSkill(d *Designer) *skills.Skill {
	return agentshared.NewRunShellScriptSkill(designerCommandSkillConfig(d))
}

func designerCommandSkillConfig(d *Designer) agentshared.CommandSkillConfig {
	return agentshared.CommandSkillConfig{
		AgentType:      "designer",
		AgentID:        func() string { return d.id },
		SessionID:      func() string { return d.config.SessionID },
		WorkspaceRoot:  d.effectiveWorkingDirectory,
		DefaultTimeout: func() time.Duration { return d.config.DesignerConfig.DefaultTimeout },
		PrepareExecution: func(ctx context.Context, workingDir string) (agentshared.CommandExecContext, error) {
			execCtx, err := d.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return agentshared.CommandExecContext{}, err
			}
			return agentshared.CommandExecContext{
				WorkDir: execCtx.workDir,
				Plan:    execCtx.plan,
			}, nil
		},
		ExecutionBroker:      func() purevfs.ExecutionBroker { return d.executionBroker },
		ExecutionWorkspace:   d.executionWorkspace,
		AllowWorkspaceWrites: true,
	}
}
