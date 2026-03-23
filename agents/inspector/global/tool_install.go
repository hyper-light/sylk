package global

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type globalInspectorDependencyInstallPlan = agentshared.DependencyInstallPlan
type globalInspectorDependencyInstallStep = agentshared.DependencyInstallStep

type globalInspectorCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

type globalInspectorOverlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

func researchDependencyInstallSkill(gi *GlobalInspector) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
	}

	return skills.NewSkill("research_dependency_install").
		Description("Ask Academic to research concrete dependency or tool installation steps for missing audit or validation tooling.").
		Domain("audit").
		Keywords("install", "dependency", "tooling", "academic", "linter", "type checker").
		Priority(83).
		Usage("Use when global validation or audit work is blocked by missing project tooling and you need concrete install steps before proceeding.").
		Requirement("Provide the missing tool/package or the failing output that proves the dependency gap.").
		Satisfies("Produces a concrete install plan that can be shown to the user and then executed through install_dependency_tooling using the existing approval dialogue.").
		Avoid("Do not guess install commands when Academic can infer the repository’s package manager and minimal steps first.").
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
			return gi.researchDependencyInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installDependencyToolingSkill(gi *GlobalInspector) *skills.Skill {
	stepProps := map[string]*skills.Property{
		"command": {Type: "string", Description: "Single install command to run. No pipes, chaining, or shell control operators."},
		"reason":  {Type: "string", Description: "Why the step is needed."},
	}
	type params struct {
		Summary           string                                 `json:"summary"`
		MissingTool       string                                 `json:"missing_tool,omitempty"`
		Framework         string                                 `json:"framework,omitempty"`
		ValidationCommand string                                 `json:"validation_command,omitempty"`
		Notes             []string                               `json:"notes,omitempty"`
		Steps             []globalInspectorDependencyInstallStep `json:"steps"`
	}

	return skills.NewSkill("install_dependency_tooling").
		Description("Execute an approved dependency install plan step-by-step using the existing command-approval dialogue.").
		Domain("audit").
		Keywords("install", "dependency", "tooling", "approval", "package manager").
		Priority(81).
		Usage("Use after research_dependency_install once you have a concrete plan to show the user. Each command will go through the existing allow once / allow always / deny once / deny always approval dialogue.").
		Requirement("Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.").
		Satisfies("Installs missing project tooling or dependencies and captures command output plus optional validation evidence.").
		Avoid("Do not use for speculative dependency changes or arbitrary shell work unrelated to unblocking the requested project tooling.").
		StringParam("summary", "Short explanation of the install plan.", true).
		StringParam("missing_tool", "Missing tool or package this plan remedies.", false).
		StringParam("framework", "Framework or ecosystem context for the install plan.", false).
		StringParam("validation_command", "Optional non-mutating command to verify the install succeeded.", false).
		ArrayParam("notes", "Important caveats or assumptions.", "string", false).
		ArrayObjectParam("steps", "Concrete single-command install steps to execute after approval.", stepProps, []string{"command"}, true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return gi.installDependencyTooling(ctx, &globalInspectorDependencyInstallPlan{
				Summary:           p.Summary,
				MissingTool:       p.MissingTool,
				Framework:         p.Framework,
				ValidationCommand: p.ValidationCommand,
				Notes:             append([]string(nil), p.Notes...),
				Steps:             append([]globalInspectorDependencyInstallStep(nil), p.Steps...),
			})
		}).
		Build()
}

func (gi *GlobalInspector) researchDependencyInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*globalInspectorDependencyInstallPlan, error) {
	if gi.bus == nil || gi.channels == nil {
		return nil, fmt.Errorf("global inspector bus is unavailable for Academic install research")
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             gi.bus,
		ResponseTopic:   gi.channels.Responses,
		SourceAgentID:   gi.id,
		SourceAgentName: "inspector",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  gi.toolRunner.WorkingDir(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  agentshared.ProjectInstallSignals(ctx, gi.fileAccess, gi.toolRunner.WorkingDir()),
	})
}

func (gi *GlobalInspector) installDependencyTooling(ctx context.Context, plan *globalInspectorDependencyInstallPlan) (map[string]any, error) {
	if err := agentshared.ValidateDependencyInstallPlan(plan); err != nil {
		return nil, err
	}
	execCtx, err := gi.commandExecutionContext(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("prepare execution workspace: %w", err)
	}
	if pp := agentshared.ProgressPublisherFromContext(ctx); pp != nil {
		pp.PublishChunk(agentshared.FormatDependencyInstallPlan(plan))
	}

	stepResults := make([]map[string]any, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		runResult, err := gi.runDependencyInstallCommand(ctx, execCtx, step.Command)
		if err != nil {
			return nil, err
		}
		stepResults = append(stepResults, map[string]any{
			"command":   step.Command,
			"reason":    step.Reason,
			"exit_code": runResult.ExitCode,
			"stdout":    string(runResult.Stdout),
			"stderr":    string(runResult.Stderr),
			"truncated": runResult.StdoutTruncated || runResult.StderrTruncated,
		})
	}

	result := map[string]any{
		"installed":    true,
		"summary":      plan.Summary,
		"missing_tool": plan.MissingTool,
		"framework":    plan.Framework,
		"step_count":   len(plan.Steps),
		"steps":        stepResults,
	}
	if strings.TrimSpace(plan.ValidationCommand) != "" {
		validationResult, err := gi.runDependencyInstallCommand(ctx, execCtx, plan.ValidationCommand)
		if err != nil {
			return nil, err
		}
		result["validation"] = map[string]any{
			"command":   plan.ValidationCommand,
			"exit_code": validationResult.ExitCode,
			"stdout":    string(validationResult.Stdout),
			"stderr":    string(validationResult.Stderr),
			"truncated": validationResult.StdoutTruncated || validationResult.StderrTruncated,
		}
	}
	return result, nil
}

func (gi *GlobalInspector) commandExecutionContext(ctx context.Context, requested string) (globalInspectorCommandExecContext, error) {
	overlay, overlayDeletes := globalInspectorOverlayState(gi.fileAccess)
	workspaceRoot := gi.toolRunner.WorkingDir()
	language := purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	planner := purevfs.NewExecutionPlanner(nil, gi.executionCapabilities(ctx))
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
		return globalInspectorCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return globalInspectorCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return globalInspectorCommandExecContext{workDir: runDir, plan: plan}, nil
}

func (gi *GlobalInspector) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	if gi.executionBroker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := gi.executionBroker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (gi *GlobalInspector) executionWorkspace(allowWrites bool) purevfs.ExecutionFS {
	if gi.fileAccess == nil {
		return versioning.NewDiskFileAccess(gi.toolRunner.WorkingDir(), true)
	}
	if allowWrites && globalInspectorWorkspaceWritesAllowed(gi.fileAccess) {
		return gi.fileAccess
	}
	return purevfs.ReadOnlyExecutionFS(gi.fileAccess)
}

func globalInspectorWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
	if fa == nil || fa.IsReadOnly() {
		return false
	}
	switch fa.(type) {
	case *versioning.DiskFileAccess:
		return false
	default:
		return true
	}
}

func globalInspectorOverlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(globalInspectorOverlayAwareFileAccess)
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

func (gi *GlobalInspector) runDependencyInstallCommand(
	ctx context.Context,
	execCtx globalInspectorCommandExecContext,
	command string,
) (*purevfs.BrokerRunResult, error) {
	if agentshared.DependencyCommandHasUnsafeShellSyntax(command) {
		return nil, fmt.Errorf("shell control operators are not allowed in install_dependency_tooling")
	}
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), commandapproval.Request{
		Command:       command,
		WorkingDir:    execCtx.workDir,
		WorkspaceRoot: gi.toolRunner.WorkingDir(),
		ToolName:      "install_dependency_tooling",
		AgentID:       gi.id,
		AgentType:     "inspector",
		SessionID:     versioning.SessionIDFromContext(ctx),
	}); err != nil {
		return nil, err
	}
	if gi.executionBroker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := gi.executionBroker.Run(ctx, purevfs.BrokerRunRequest{
		Plan:      execCtx.plan,
		Argv:      purevfs.ShellCommandArgv(command),
		Workspace: gi.executionWorkspace(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %w", err)
	}
	if runResult.ExitCode != 0 {
		stderr := strings.TrimSpace(string(runResult.Stderr))
		if stderr == "" {
			stderr = strings.TrimSpace(string(runResult.Stdout))
		}
		if stderr == "" {
			stderr = fmt.Sprintf("command exited with code %d", runResult.ExitCode)
		}
		return nil, fmt.Errorf("%s: %s", command, stderr)
	}
	return runResult, nil
}
