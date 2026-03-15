package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type testToolInstallStep struct {
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type testToolInstallPlan struct {
	Summary           string                `json:"summary"`
	MissingTool       string                `json:"missing_tool,omitempty"`
	Framework         string                `json:"framework,omitempty"`
	ValidationCommand string                `json:"validation_command,omitempty"`
	Notes             []string              `json:"notes,omitempty"`
	Steps             []testToolInstallStep `json:"steps"`
}

func researchTestToolInstallSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		MissingTool string   `json:"missing_tool,omitempty"`
		Failure     string   `json:"failure,omitempty"`
		FrameworkID string   `json:"framework_id,omitempty"`
		RunCommand  string   `json:"run_command,omitempty"`
		Files       []string `json:"files,omitempty"`
		TaskSpec    string   `json:"task_spec,omitempty"`
		WorkerType  string   `json:"worker_type,omitempty"`
	}

	return skills.NewSkill("research_test_tool_install").
		Description("Ask Academic to research concrete installation steps for missing test tooling, then synthesize the result into an executable step plan.").
		Domain("testing").
		Keywords("install", "tooling", "missing dependency", "academic", "pytest", "vitest").
		Priority(91).
		Usage("Use when run_test_suite or harness preparation is blocked by missing test tooling. Pass the failing command/output so Academic can research concrete, project-aware install steps.").
		Requirement("Provide the missing tool or the failing output that proves the current test command cannot run.").
		Satisfies("Produces a concrete install plan that can be explained to the user and then executed through install_test_tooling with standard approval prompts.").
		Avoid("Do not guess package-manager commands when this skill can research them first. Do not use it for ordinary test failures that are not missing-tool problems.").
		StringParam("missing_tool", "Name of the missing executable or package if already known.", false).
		StringParam("failure", "The failing test output or error that indicates missing tooling.", false).
		StringParam("framework_id", "Detected framework identifier such as pytest or vitest.", false).
		StringParam("run_command", "The test command that failed or is expected to fail.", false).
		ArrayParam("files", "Relevant source or test files for project context.", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria.", false).
		StringParam("worker_type", "Primary worker type such as engineer or designer.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return pt.researchTestToolInstall(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec, p.WorkerType)
		}).
		Build()
}

func installTestToolingSkill(pt *PipelineTester) *skills.Skill {
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
		Description("Execute an approved test-tool installation plan step-by-step using the existing command-approval dialogue.").
		Domain("testing").
		Keywords("install", "tooling", "dependency", "pytest", "vitest", "approval").
		Priority(89).
		Usage("Use after research_test_tool_install once you have a concrete plan to show the user. The install commands will go through the existing allow once / allow always / deny once / deny always approval dialogue.").
		Requirement("Provide a concrete summary and a list of single install commands. Each step must be one command without chaining or shell control operators.").
		Satisfies("Installs missing test tooling, captures command output, and optionally validates that the toolchain is now runnable.").
		Avoid("Do not use for speculative dependency changes or for arbitrary shell work unrelated to restoring the test toolchain.").
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
			plan := &testToolInstallPlan{
				Summary:           p.Summary,
				MissingTool:       p.MissingTool,
				Framework:         p.Framework,
				ValidationCommand: p.ValidationCommand,
				Notes:             append([]string(nil), p.Notes...),
				Steps:             append([]testToolInstallStep(nil), p.Steps...),
			}
			return pt.installTestTooling(ctx, plan)
		}).
		Build()
}

type testerCommandExecContext struct {
	workDir string
	plan    purevfs.ExecutionPlan
}

func (pt *PipelineTester) researchTestToolInstall(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec, workerType string,
) (*testToolInstallPlan, error) {
	if pt.bus == nil || pt.channels == nil {
		return nil, fmt.Errorf("tester bus is unavailable for Academic install research")
	}
	harness := pt.currentHarnessState()
	if harness == nil && len(files) > 0 {
		detected, err := pt.detectHarness(ctx, files, taskSpec, workerType)
		if err == nil {
			harness = detected
		}
	}
	if frameworkID == "" && harness != nil {
		frameworkID = string(harness.FrameworkID)
	}
	if runCommand == "" && harness != nil {
		runCommand = harness.RunCommand
	}
	prompt := pt.buildInstallResearchPrompt(ctx, missingTool, failure, frameworkID, runCommand, files, taskSpec, harness)
	msg, err := agentshared.RequestGuideRouteSync(ctx, agentshared.GuideRouteSyncRequest{
		Bus:           pt.bus,
		ResponseTopic: pt.channels.Responses,
		Request: &guide.RouteRequest{
			Input:           prompt,
			TargetAgentID:   "academic",
			SourceAgentID:   pt.id,
			SourceAgentName: "tester-pipeline",
			SessionID:       versioning.SessionIDFromContext(ctx),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("research install steps via Academic: %w", err)
	}
	content, err := extractAcademicContent(msg)
	if err != nil {
		return nil, err
	}
	plan, err := parseTestToolInstallPlan(content)
	if err != nil {
		return nil, fmt.Errorf("parse Academic install plan: %w", err)
	}
	if strings.TrimSpace(plan.MissingTool) == "" {
		plan.MissingTool = strings.TrimSpace(missingTool)
	}
	if strings.TrimSpace(plan.Framework) == "" {
		plan.Framework = strings.TrimSpace(frameworkID)
	}
	return plan, nil
}

func (pt *PipelineTester) buildInstallResearchPrompt(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
	harness *testHarnessState,
) string {
	root := pt.workingDir()
	signals := pt.projectInstallSignals(ctx)
	if harness != nil {
		signals = append(signals,
			fmt.Sprintf("framework=%s", harness.FrameworkName),
			fmt.Sprintf("language=%s", harness.Language),
		)
	}
	signalBlock := "(none)"
	if len(signals) > 0 {
		signalBlock = strings.Join(signals, "\n- ")
		signalBlock = "- " + signalBlock
	}
	targetFiles := "(none)"
	if len(files) > 0 {
		targetFiles = strings.Join(files, "\n- ")
		targetFiles = "- " + targetFiles
	}
	return strings.TrimSpace(fmt.Sprintf(
		`You are helping the Pipeline Tester restore missing test tooling in a repository.

Return JSON only. No prose outside the JSON.

Schema:
{
  "summary": "short explanation",
  "missing_tool": "tool name",
  "framework": "framework id or name",
  "validation_command": "optional single command",
  "notes": ["optional caveat"],
  "steps": [
    {
      "command": "single install command",
      "reason": "why this step is needed"
    }
  ]
}

Hard requirements:
- Each step command must be exactly one command.
- Do not use pipes, &&, ||, ;, redirection, subshells, or multi-line shell.
- Prefer the package manager already implied by the repository files.
- Prefer workspace-local or project-scoped installation when possible.
- Keep the plan minimal.

Repository root: %s
Detected framework: %s
Expected run command: %s
Missing tool hint: %s
Failure output:
%s

Relevant task files:
%s

Relevant project signals:
%s

Task specification:
%s
`,
		root,
		strings.TrimSpace(frameworkID),
		strings.TrimSpace(runCommand),
		strings.TrimSpace(missingTool),
		strings.TrimSpace(failure),
		targetFiles,
		signalBlock,
		strings.TrimSpace(taskSpec),
	))
}

func (pt *PipelineTester) projectInstallSignals(ctx context.Context) []string {
	candidates := []string{
		"package.json",
		"pnpm-lock.yaml",
		"yarn.lock",
		"package-lock.json",
		"bun.lockb",
		"bun.lock",
		"pyproject.toml",
		"requirements.txt",
		"requirements-dev.txt",
		"setup.cfg",
		"setup.py",
		"Pipfile",
		"Pipfile.lock",
		"poetry.lock",
		"go.mod",
		"Cargo.toml",
		"Gemfile",
		"composer.json",
		"phpunit.xml",
		"pom.xml",
		"build.gradle",
		"build.gradle.kts",
		"gradlew",
		"gradlew.bat",
	}
	signals := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		exists, err := pt.fileExists(ctx, candidate)
		if err != nil || !exists {
			continue
		}
		signals = append(signals, candidate)
	}
	return signals
}

func extractAcademicContent(msg *guide.Message) (string, error) {
	if msg == nil {
		return "", fmt.Errorf("academic response is missing")
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		if errText, ok := msg.GetError(); ok {
			return "", fmt.Errorf("%s", strings.TrimSpace(errText))
		}
		return "", fmt.Errorf("academic response payload is unsupported")
	}
	if !resp.Success {
		return "", fmt.Errorf("%s", strings.TrimSpace(resp.Error))
	}
	if content := extractAcademicResponseContent(resp.Data); strings.TrimSpace(content) != "" {
		return content, nil
	}
	return "", fmt.Errorf("academic response did not include install-plan content")
}

func extractAcademicResponseContent(data any) string {
	switch typed := data.(type) {
	case map[string]any:
		content, _ := typed["content"].(string)
		return strings.TrimSpace(content)
	case string:
		return strings.TrimSpace(typed)
	default:
		raw, err := json.Marshal(data)
		if err != nil {
			return ""
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return ""
		}
		content, _ := payload["content"].(string)
		return strings.TrimSpace(content)
	}
}

func parseTestToolInstallPlan(raw string) (*testToolInstallPlan, error) {
	candidates := []string{
		strings.TrimSpace(raw),
		extractFencedJSON(raw),
		extractJSONObject(raw),
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		var plan testToolInstallPlan
		if err := json.Unmarshal([]byte(candidate), &plan); err != nil {
			continue
		}
		if err := validateTestToolInstallPlan(&plan); err != nil {
			return nil, err
		}
		return &plan, nil
	}
	return nil, fmt.Errorf("academic response did not contain valid install-plan JSON")
}

func extractFencedJSON(raw string) string {
	start := strings.Index(raw, "```json")
	if start == -1 {
		start = strings.Index(raw, "```")
		if start == -1 {
			return ""
		}
		start += len("```")
	} else {
		start += len("```json")
	}
	end := strings.Index(raw[start:], "```")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(raw[start : start+end])
}

func extractJSONObject(raw string) string {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end < start {
		return ""
	}
	return strings.TrimSpace(raw[start : end+1])
}

func validateTestToolInstallPlan(plan *testToolInstallPlan) error {
	if plan == nil {
		return fmt.Errorf("install plan is required")
	}
	if len(plan.Steps) == 0 {
		return fmt.Errorf("install plan must contain at least one step")
	}
	for idx, step := range plan.Steps {
		command := strings.TrimSpace(step.Command)
		if command == "" {
			return fmt.Errorf("install step %d is missing a command", idx+1)
		}
		if testerCommandHasUnsafeShellSyntax(command) {
			return fmt.Errorf("install step %d command uses unsupported shell syntax", idx+1)
		}
	}
	if strings.TrimSpace(plan.ValidationCommand) != "" && testerCommandHasUnsafeShellSyntax(plan.ValidationCommand) {
		return fmt.Errorf("validation_command uses unsupported shell syntax")
	}
	if strings.TrimSpace(plan.Summary) == "" {
		plan.Summary = "Install missing test tooling"
	}
	return nil
}

func testerCommandHasUnsafeShellSyntax(command string) bool {
	if strings.ContainsRune(command, '\n') || strings.ContainsRune(command, '\r') || strings.ContainsRune(command, 0) {
		return true
	}
	for _, fragment := range []string{"&&", "||", ";", "|", "`", "$(", "${", ">", "<"} {
		if strings.Contains(command, fragment) {
			return true
		}
	}
	return false
}

func (pt *PipelineTester) installTestTooling(ctx context.Context, plan *testToolInstallPlan) (map[string]any, error) {
	if err := validateTestToolInstallPlan(plan); err != nil {
		return nil, err
	}
	execCtx, err := pt.commandExecutionContext(ctx, "")
	if err != nil {
		return nil, err
	}

	if pp := agentshared.ProgressPublisherFromContext(ctx); pp != nil {
		pp.PublishChunk(formatTestToolInstallPlan(plan))
	}

	stepResults := make([]map[string]any, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		runResult, err := pt.runInstallCommand(ctx, execCtx, step.Command)
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
		validationResult, err := pt.runInstallCommand(ctx, execCtx, plan.ValidationCommand)
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

func formatTestToolInstallPlan(plan *testToolInstallPlan) string {
	lines := []string{strings.TrimSpace(plan.Summary)}
	for idx, step := range plan.Steps {
		lines = append(lines, fmt.Sprintf("%d. %s", idx+1, strings.TrimSpace(step.Command)))
	}
	return strings.Join(lines, "\n")
}

func (pt *PipelineTester) runInstallCommand(
	ctx context.Context,
	execCtx testerCommandExecContext,
	command string,
) (*purevfs.BrokerRunResult, error) {
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), commandapproval.Request{
		Command:       command,
		WorkingDir:    execCtx.workDir,
		WorkspaceRoot: pt.workingDir(),
		ToolName:      "install_test_tooling",
		AgentID:       pt.id,
		AgentType:     "tester-pipeline",
		SessionID:     versioning.SessionIDFromContext(ctx),
	}); err != nil {
		return nil, err
	}
	if pt.executionBroker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := pt.executionBroker.Run(ctx, purevfs.BrokerRunRequest{
		Plan:      execCtx.plan,
		Argv:      purevfs.ShellCommandArgv(command),
		Workspace: pt.executionWorkspace(true),
	})
	if err != nil {
		return nil, err
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

func (pt *PipelineTester) commandExecutionContext(ctx context.Context, requested string) (testerCommandExecContext, error) {
	overlay, overlayDeletes := overlayState(pt.fileAccess)
	workspaceRoot := pt.workingDir()
	language := ""
	if harness := pt.currentHarnessState(); harness != nil {
		language = harness.Language
	}
	if strings.TrimSpace(language) == "" {
		language = purevfs.DefaultCatalog().DetectProject(workspaceRoot).PrimaryLanguage
	}
	planner := purevfs.NewExecutionPlanner(nil, pt.executionCapabilities(ctx))
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
		return testerCommandExecContext{}, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return testerCommandExecContext{}, purevfs.ErrStrictExecutionUnavailable
	}
	runDir := strings.TrimSpace(plan.WorkingDir)
	if runDir == "" {
		runDir = "."
		plan.WorkingDir = runDir
	}
	return testerCommandExecContext{
		workDir: runDir,
		plan:    plan,
	}, nil
}
