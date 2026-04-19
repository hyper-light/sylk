package engineer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type dependencyInstallPlan = agentshared.DependencyInstallPlan
type dependencyInstallStep = agentshared.DependencyInstallStep

func researchDependencyInstallSkill(e *Engineer) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
	}

	return skills.NewSkill("research_dependency_install").
		Description("Ask Academic to research concrete dependency or tool installation steps for the current project block.").
		Domain("code").
		Keywords("install", "dependency", "tooling", "missing package", "academic").
		Priority(87).
		Usage("Use when implementation or validation is blocked by missing project tooling or dependencies and you need concrete install steps before proceeding.").
		Requirement("Provide the missing tool/package or the failing output that proves the dependency gap.").
		Satisfies("Produces a concrete install plan that can be shown to the user and then executed through install_dependency_tooling using the existing approval dialogue.").
		Avoid("Do not guess install commands when Academic can research the repository-specific package manager and minimal steps first.").
		StringParam("missing_tool", "Missing executable or package if already known.", false).
		StringParam("failure", "Error output showing the missing dependency or tool.", false).
		StringParam("framework_id", "Detected framework or ecosystem identifier.", false).
		StringParam("run_command", "Blocked command that would verify or use the dependency.", false).
		ArrayParam("files", "Relevant source or config files for project context.", "string", false).
		StringParam("task_spec", "Task brief or acceptance criteria.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return e.researchDependencyInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installDependencyToolingSkill(e *Engineer) *skills.Skill {
	return agentshared.NewDependencyInstallExecutionSkill(agentshared.DependencyInstallSkillConfig{
		SkillName:     "install_dependency_tooling",
		Description:   "Execute an approved dependency install plan step-by-step using the existing command-approval dialogue.",
		Domain:        "code",
		Keywords:      []string{"install", "dependency", "tooling", "approval", "package manager"},
		Priority:      85,
		Usage:         "Use after research_dependency_install once you have a concrete plan to show the user. Each command goes through the existing approval dialogue and executes against the real disk workspace.",
		Requirement:   "Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.",
		Satisfies:     "Installs missing project tooling or dependencies to disk and captures command output plus optional validation evidence.",
		Avoid:         "Do not use for speculative dependency changes or arbitrary shell work unrelated to unblocking the requested project tooling.",
		ResearchSkill: "research_dependency_install",
		AgentType:     "engineer",
		AgentID:       func() string { return e.id },
		SessionID:     func() string { return e.config.SessionID },
		WorkingDir:    e.effectiveWorkingDirectory,
		CommandsEnabled: func() bool {
			return e.config.EngineerConfig.EnableCommands
		},
		DefaultTimeout: func() time.Duration { return e.config.EngineerConfig.CommandTimeout },
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
		SkillName:       "install_dependency_tooling",
		ResearchSkill:   "research_dependency_install",
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
