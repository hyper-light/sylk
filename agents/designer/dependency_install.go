package designer

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

type designerDependencyInstallPlan = agentshared.DependencyInstallPlan
type designerDependencyInstallStep = agentshared.DependencyInstallStep

type designerCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

type designerOverlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

func researchDependencyInstallSkill(d *Designer) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
	}

	return skills.NewSkill("research_dependency_install").
		Description("Ask Academic to research concrete dependency or tool installation steps for the current design-system or frontend block.").
		Domain("ui").
		Keywords("install", "dependency", "tooling", "package manager", "academic").
		Priority(84).
		Usage("Use when design validation or implementation is blocked by missing tooling, dependencies, or workspace package setup.").
		Requirement("Provide the missing tool/package or the failing output that proves the dependency gap.").
		Satisfies("Produces a concrete install plan that can be shown to the user and then executed through install_dependency_tooling using the existing approval dialogue.").
		Avoid("Do not guess package-manager commands when Academic can infer the repository’s package manager and minimal steps first.").
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
			return d.researchDependencyInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installDependencyToolingSkill(d *Designer) *skills.Skill {
	stepProps := map[string]*skills.Property{
		"command": {Type: "string", Description: "Single install command to run. No pipes, chaining, or shell control operators."},
		"reason":  {Type: "string", Description: "Why the step is needed."},
	}
	type params struct {
		Summary           string                          `json:"summary"`
		MissingTool       string                          `json:"missing_tool,omitempty"`
		Framework         string                          `json:"framework,omitempty"`
		ValidationCommand string                          `json:"validation_command,omitempty"`
		Notes             []string                        `json:"notes,omitempty"`
		Steps             []designerDependencyInstallStep `json:"steps"`
	}

	return skills.NewSkill("install_dependency_tooling").
		Description("Execute an approved dependency install plan step-by-step using the existing command-approval dialogue.").
		Domain("ui").
		Keywords("install", "dependency", "tooling", "approval", "package manager").
		Priority(82).
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
			return d.installDependencyTooling(ctx, &designerDependencyInstallPlan{
				Summary:           p.Summary,
				MissingTool:       p.MissingTool,
				Framework:         p.Framework,
				ValidationCommand: p.ValidationCommand,
				Notes:             append([]string(nil), p.Notes...),
				Steps:             append([]designerDependencyInstallStep(nil), p.Steps...),
			})
		}).
		Build()
}

func (d *Designer) researchDependencyInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*designerDependencyInstallPlan, error) {
	if d.bus == nil || d.channels == nil {
		return nil, fmt.Errorf("designer bus is unavailable for Academic install research")
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             d.bus,
		ResponseTopic:   d.channels.Responses,
		SourceAgentID:   d.id,
		SourceAgentName: "designer",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  d.effectiveWorkingDirectory(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  agentshared.ProjectInstallSignals(ctx, d.fileAccess, d.effectiveWorkingDirectory()),
	})
}

func (d *Designer) installDependencyTooling(ctx context.Context, plan *designerDependencyInstallPlan) (map[string]any, error) {
	if err := agentshared.ValidateDependencyInstallPlan(plan); err != nil {
		return nil, err
	}
	execCtx, err := d.commandExecutionContext(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("prepare execution workspace: %w", err)
	}

	if pp := agentshared.ProgressPublisherFromContext(ctx); pp != nil {
		pp.PublishChunk(agentshared.FormatDependencyInstallPlan(plan))
	}

	stepResults := make([]map[string]any, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		runResult, err := d.runDependencyInstallCommand(ctx, execCtx, step.Command)
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
		validationResult, err := d.runDependencyInstallCommand(ctx, execCtx, plan.ValidationCommand)
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

func (d *Designer) effectiveWorkingDirectory() string {
	if d.fileAccess != nil && strings.TrimSpace(d.fileAccess.WorkingDir()) != "" {
		return d.fileAccess.WorkingDir()
	}
	if trimmed := strings.TrimSpace(d.config.DesignerConfig.WorkingDirectory); trimmed != "" {
		return trimmed
	}
	return "."
}

func (d *Designer) commandExecutionContext(ctx context.Context, requested string) (designerCommandExecContext, error) {
	overlay, overlayDeletes := designerOverlayState(d.fileAccess)
	workspaceRoot := d.effectiveWorkingDirectory()
	language := purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	planner := purevfs.NewExecutionPlanner(nil, d.executionCapabilities(ctx))
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
		return designerCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return designerCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return designerCommandExecContext{workDir: runDir, plan: plan}, nil
}

func (d *Designer) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	if d.executionBroker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := d.executionBroker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (d *Designer) executionWorkspace(allowWrites bool) purevfs.ExecutionFS {
	if d.fileAccess == nil {
		return versioning.NewDiskFileAccess(d.effectiveWorkingDirectory(), true)
	}
	if allowWrites && designerWorkspaceWritesAllowed(d.fileAccess) {
		return d.fileAccess
	}
	return purevfs.ReadOnlyExecutionFS(d.fileAccess)
}

func designerWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
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

func designerOverlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(designerOverlayAwareFileAccess)
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

func (d *Designer) runDependencyInstallCommand(
	ctx context.Context,
	execCtx designerCommandExecContext,
	command string,
) (*purevfs.BrokerRunResult, error) {
	if agentshared.DependencyCommandHasUnsafeShellSyntax(command) {
		return nil, fmt.Errorf("shell control operators are not allowed in install_dependency_tooling")
	}
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), commandapproval.Request{
		Command:       command,
		WorkingDir:    execCtx.workDir,
		WorkspaceRoot: d.effectiveWorkingDirectory(),
		ToolName:      "install_dependency_tooling",
		AgentID:       d.id,
		AgentType:     "designer",
		SessionID:     versioning.SessionIDFromContext(ctx),
	}); err != nil {
		return nil, err
	}
	if d.executionBroker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := d.executionBroker.Run(ctx, purevfs.BrokerRunRequest{
		Plan:      execCtx.plan,
		Argv:      purevfs.ShellCommandArgv(command),
		Workspace: d.executionWorkspace(true),
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
