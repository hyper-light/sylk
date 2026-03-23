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

type testToolInstallStep = agentshared.DependencyInstallStep
type testToolInstallPlan = agentshared.DependencyInstallPlan

type globalTesterCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

type globalTesterOverlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

func researchTestToolInstallSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
	}

	return skills.NewSkill("research_test_tool_install").
		Description("Ask Academic to research concrete installation steps for missing global test tooling, then synthesize the result into an executable step plan.").
		Domain("testing").
		Keywords("install", "tooling", "missing dependency", "academic", "pytest", "playwright").
		Priority(91).
		Usage("Use when run_test_suite or harness preparation is blocked by missing global test tooling. Pass the failing command/output so Academic can research concrete, project-aware install steps.").
		Requirement("Provide the missing tool or the failing output that proves the current test command cannot run.").
		Satisfies("Produces a concrete install plan that can be explained to the user and then executed through install_test_tooling with standard approval prompts.").
		Avoid("Do not guess package-manager commands when this skill can research them first. Do not use it for ordinary test failures that are not missing-tool problems.").
		StringParam("missing_tool", "Name of the missing executable or package if already known.", false).
		StringParam("failure", "The failing test output or error that indicates missing tooling.", false).
		StringParam("framework_id", "Detected framework identifier such as pytest or playwright.", false).
		StringParam("run_command", "The test command that failed or is expected to fail.", false).
		ArrayParam("files", "Relevant source or test files for project context.", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return gt.researchTestToolInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
		}).
		Build()
}

func installTestToolingSkill(gt *GlobalTester) *skills.Skill {
	stepProps := map[string]*skills.Property{
		"command": {Type: "string", Description: "Single install command to run. No pipes, chaining, or shell control operators."},
		"reason":  {Type: "string", Description: "Why the step is needed."},
	}
	type params struct {
		Summary           string                `json:"summary"`
		MissingTool       string                `json:"missing_tool,omitempty"`
		Framework         string                `json:"framework,omitempty"`
		ValidationCommand string                `json:"validation_command,omitempty"`
		Notes             []string              `json:"notes,omitempty"`
		Steps             []testToolInstallStep `json:"steps"`
	}

	return skills.NewSkill("install_test_tooling").
		Description("Execute an approved global test-tool installation plan step-by-step using the existing command-approval dialogue.").
		Domain("testing").
		Keywords("install", "tooling", "dependency", "pytest", "playwright", "approval").
		Priority(89).
		Usage("Use after research_test_tool_install once you have a concrete plan to show the user. The install commands will go through the existing allow once / allow always / deny once / deny always approval dialogue.").
		Requirement("Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.").
		Satisfies("Installs missing global test tooling, captures command output, and optionally validates that the toolchain is now runnable.").
		Avoid("Do not use for speculative dependency changes or for arbitrary shell work unrelated to restoring the global test toolchain.").
		StringParam("summary", "Short explanation of the install plan.", true).
		StringParam("missing_tool", "Missing tool this plan remedies.", false).
		StringParam("framework", "Framework or ecosystem context for the install plan.", false).
		StringParam("validation_command", "Optional non-mutating command to verify the install succeeded.", false).
		ArrayParam("notes", "Important caveats or assumptions.", "string", false).
		ArrayObjectParam("steps", "Concrete single-command install steps to execute after approval.", stepProps, []string{"command"}, true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return gt.installTestTooling(ctx, &testToolInstallPlan{
				Summary:           p.Summary,
				MissingTool:       p.MissingTool,
				Framework:         p.Framework,
				ValidationCommand: p.ValidationCommand,
				Notes:             append([]string(nil), p.Notes...),
				Steps:             append([]testToolInstallStep(nil), p.Steps...),
			})
		}).
		Build()
}

func (gt *GlobalTester) researchTestToolInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*testToolInstallPlan, error) {
	if gt.bus == nil || gt.channels == nil {
		return nil, fmt.Errorf("global tester bus is unavailable for Academic install research")
	}
	return agentshared.ResearchDependencyInstallPlan(ctx, agentshared.DependencyInstallResearchRequest{
		Bus:             gt.bus,
		ResponseTopic:   gt.channels.Responses,
		SourceAgentID:   gt.id,
		SourceAgentName: "tester",
		SessionID:       versioning.SessionIDFromContext(ctx),
		RepositoryRoot:  gt.workingDir(),
		FrameworkID:     frameworkID,
		RunCommand:      runCommand,
		MissingTool:     missingTool,
		Failure:         failure,
		TaskSpec:        taskSpec,
		Files:           append([]string(nil), files...),
		ProjectSignals:  agentshared.ProjectInstallSignals(ctx, gt.fileAccess, gt.workingDir()),
	})
}

func (gt *GlobalTester) installTestTooling(ctx context.Context, plan *testToolInstallPlan) (map[string]any, error) {
	if err := agentshared.ValidateDependencyInstallPlan(plan); err != nil {
		return nil, err
	}
	execCtx, err := gt.commandExecutionContext(ctx, "")
	if err != nil {
		return nil, err
	}
	if pp := agentshared.ProgressPublisherFromContext(ctx); pp != nil {
		pp.PublishChunk(agentshared.FormatDependencyInstallPlan(plan))
	}

	stepResults := make([]map[string]any, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		runResult, err := gt.runInstallCommand(ctx, execCtx, step.Command)
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
		validationResult, err := gt.runInstallCommand(ctx, execCtx, plan.ValidationCommand)
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

func (gt *GlobalTester) workingDir() string {
	if gt.fileAccess != nil && strings.TrimSpace(gt.fileAccess.WorkingDir()) != "" {
		return gt.fileAccess.WorkingDir()
	}
	return "."
}

func (gt *GlobalTester) commandExecutionContext(ctx context.Context, requested string) (globalTesterCommandExecContext, error) {
	overlay, overlayDeletes := globalTesterOverlayState(gt.fileAccess)
	workspaceRoot := gt.workingDir()
	language := purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	planner := purevfs.NewExecutionPlanner(nil, gt.executionCapabilities(ctx))
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
		return globalTesterCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return globalTesterCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return globalTesterCommandExecContext{workDir: runDir, plan: plan}, nil
}

func (gt *GlobalTester) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	if gt.executionBroker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := gt.executionBroker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (gt *GlobalTester) executionWorkspace(allowWrites bool) purevfs.ExecutionFS {
	if gt.fileAccess == nil {
		return versioning.NewDiskFileAccess(gt.workingDir(), true)
	}
	if allowWrites && globalTesterWorkspaceWritesAllowed(gt.fileAccess) {
		return gt.fileAccess
	}
	return purevfs.ReadOnlyExecutionFS(gt.fileAccess)
}

func globalTesterWorkspaceWritesAllowed(fa versioning.FileAccess) bool {
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

func globalTesterOverlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(globalTesterOverlayAwareFileAccess)
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

func (gt *GlobalTester) runInstallCommand(
	ctx context.Context,
	execCtx globalTesterCommandExecContext,
	command string,
) (*purevfs.BrokerRunResult, error) {
	if agentshared.DependencyCommandHasUnsafeShellSyntax(command) {
		return nil, fmt.Errorf("shell control operators are not allowed in install_test_tooling")
	}
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), commandapproval.Request{
		Command:       command,
		WorkingDir:    execCtx.workDir,
		WorkspaceRoot: gt.workingDir(),
		ToolName:      "install_test_tooling",
		AgentID:       gt.id,
		AgentType:     "tester",
		SessionID:     versioning.SessionIDFromContext(ctx),
	}); err != nil {
		return nil, err
	}
	if gt.executionBroker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := gt.executionBroker.Run(ctx, purevfs.BrokerRunRequest{
		Plan:      execCtx.plan,
		Argv:      purevfs.ShellCommandArgv(command),
		Workspace: gt.executionWorkspace(true),
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
