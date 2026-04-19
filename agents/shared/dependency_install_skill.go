package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

const dependencyInstallMaxOutputBytes = 10 * 1024 * 1024

type DependencyInstallSkillConfig struct {
	SkillName          string
	Description        string
	Domain             string
	Keywords           []string
	Priority           int
	Usage              string
	Requirement        string
	Satisfies          string
	Avoid              string
	ResearchSkill      string
	AgentType          string
	AgentID            func() string
	SessionID          func() string
	WorkingDir         func() string
	CommandsEnabled    func() bool
	DefaultTimeout     func() time.Duration
	ValidatePlan       func(*DependencyInstallPlan) error
	ExecutionBroker    func() purevfs.ExecutionBroker
	PrepareExecution   func(context.Context, string) (CommandExecContext, error)
	ExecutionWorkspace func(allowWrites bool) purevfs.ExecutionFS
}

type DependencyInstallCommandRequest struct {
	ToolName      string
	Command       string
	WorkingDir    string
	WorkspaceRoot string
	AgentID       string
	AgentType     string
	SessionID     string
	Timeout       time.Duration
}

type DependencyInstallCommandResult struct {
	Stdout          []byte
	Stderr          []byte
	ExitCode        int
	StdoutTruncated bool
	StderrTruncated bool
}

func NewDependencyInstallExecutionSkill(cfg DependencyInstallSkillConfig) *skills.Skill {
	stepProps := map[string]*skills.Property{
		"command": {Type: "string", Description: "Single install command to run inside the strict-disk execution sandbox against the agent's workspace view. No pipes, chaining, or shell control operators."},
		"reason":  {Type: "string", Description: "Why the step is needed."},
	}
	type params struct {
		Summary           string                  `json:"summary"`
		MissingTool       string                  `json:"missing_tool,omitempty"`
		Framework         string                  `json:"framework,omitempty"`
		ValidationCommand string                  `json:"validation_command,omitempty"`
		Notes             []string                `json:"notes,omitempty"`
		Steps             []DependencyInstallStep `json:"steps"`
	}

	skillName := strings.TrimSpace(cfg.SkillName)
	if skillName == "" {
		skillName = "install_dependency_tooling"
	}
	researchSkill := strings.TrimSpace(cfg.ResearchSkill)
	if researchSkill == "" {
		researchSkill = "research_dependency_install"
	}

	builder := skills.NewSkill(skillName).
		Description(cfg.Description).
		Domain(cfg.Domain).
		Priority(cfg.Priority).
		Usage(cfg.Usage).
		Requirement(cfg.Requirement).
		Satisfies(cfg.Satisfies).
		Avoid(cfg.Avoid).
		BestPractice("These commands execute inside the strict-disk sandbox against the agent's active workspace view. Writes are classified by the workspace-mutation policy; installation artifacts live in the VFS overlay and never touch host disk outside it.").
		BestPractice("Each install or validation step uses exact approval semantics through the same approval flow as command execution tools.").
		BestPractice("When you include validation_command, make it a simple non-mutating availability check such as '--version' or an import probe, not a project test run or repo-wide audit.").
		BestPractice(fmt.Sprintf("If you cannot determine the correct install command with significant confidence, call `%s` first instead of guessing.", researchSkill)).
		StringParam("summary", "Short explanation of the install plan.", true).
		StringParam("missing_tool", "Missing dependency, tool, or utility this plan remedies.", false).
		StringParam("framework", "Framework or ecosystem context for the install plan.", false).
		StringParam("validation_command", "Optional non-mutating command to verify the install succeeded.", false).
		ArrayParam("notes", "Important caveats or assumptions.", "string", false).
		ArrayObjectParam("steps", "Concrete single-command install steps to execute after approval.", stepProps, []string{"command"}, true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return ExecuteDependencyInstallPlan(ctx, cfg, &DependencyInstallPlan{
				Summary:           p.Summary,
				MissingTool:       p.MissingTool,
				Framework:         p.Framework,
				ValidationCommand: p.ValidationCommand,
				Notes:             append([]string(nil), p.Notes...),
				Steps:             append([]DependencyInstallStep(nil), p.Steps...),
			})
		})
	if len(cfg.Keywords) > 0 {
		builder = builder.Keywords(cfg.Keywords...)
	}
	return builder.Build()
}

func ExecuteDependencyInstallPlan(ctx context.Context, cfg DependencyInstallSkillConfig, plan *DependencyInstallPlan) (map[string]any, error) {
	if cfg.CommandsEnabled != nil && !cfg.CommandsEnabled() {
		return nil, fmt.Errorf("command execution is disabled")
	}
	if err := ValidateDependencyInstallPlan(plan); err != nil {
		return nil, err
	}
	if cfg.ValidatePlan != nil {
		if err := cfg.ValidatePlan(plan); err != nil {
			return nil, err
		}
	}

	workspaceRoot := dependencyInstallWorkingDir(cfg)
	timeout := normalizeDependencyInstallTimeout(cfg.DefaultTimeout)
	if pp := ProgressPublisherFromContext(ctx); pp != nil {
		pp.PublishChunk(FormatDependencyInstallPlan(plan))
	}

	stepResults := make([]map[string]any, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		runResult, err := runDependencyInstallCommand(ctx, cfg, DependencyInstallCommandRequest{
			ToolName:      cfg.SkillName,
			Command:       step.Command,
			WorkingDir:    workspaceRoot,
			WorkspaceRoot: workspaceRoot,
			AgentID:       dependencyInstallAgentID(cfg),
			AgentType:     strings.TrimSpace(cfg.AgentType),
			SessionID:     dependencyInstallSessionID(ctx, cfg),
			Timeout:       timeout,
		})
		if err != nil {
			return nil, err
		}
		stepResults = append(stepResults, dependencyInstallStepResult(step, runResult))
	}

	result := map[string]any{
		"installed":    true,
		"summary":      plan.Summary,
		"missing_tool": plan.MissingTool,
		"framework":    plan.Framework,
		"step_count":   len(plan.Steps),
		"steps":        stepResults,
	}
	if validationCommand := strings.TrimSpace(plan.ValidationCommand); validationCommand != "" {
		runResult, err := runDependencyInstallCommandWithOptions(ctx, cfg, DependencyInstallCommandRequest{
			ToolName:      cfg.SkillName,
			Command:       validationCommand,
			WorkingDir:    workspaceRoot,
			WorkspaceRoot: workspaceRoot,
			AgentID:       dependencyInstallAgentID(cfg),
			AgentType:     strings.TrimSpace(cfg.AgentType),
			SessionID:     dependencyInstallSessionID(ctx, cfg),
			Timeout:       timeout,
		}, dependencyInstallCommandRunOptions{AllowNonZeroExit: true})
		validation := map[string]any{
			"command": validationCommand,
			"success": false,
		}
		if runResult != nil {
			validation["exit_code"] = runResult.ExitCode
			validation["stdout"] = string(runResult.Stdout)
			validation["stderr"] = string(runResult.Stderr)
			validation["truncated"] = runResult.StdoutTruncated || runResult.StderrTruncated
			validation["success"] = runResult.ExitCode == 0
		}
		if err != nil {
			validation["error"] = err.Error()
		}
		result["validation"] = validation
		result["validation_passed"], _ = validation["success"].(bool)
	}
	return result, nil
}

func runDependencyInstallCommand(ctx context.Context, cfg DependencyInstallSkillConfig, req DependencyInstallCommandRequest) (*DependencyInstallCommandResult, error) {
	return runDependencyInstallCommandWithOptions(ctx, cfg, req, dependencyInstallCommandRunOptions{})
}

type dependencyInstallCommandRunOptions struct {
	AllowNonZeroExit bool
}

func runDependencyInstallCommandWithOptions(
	ctx context.Context,
	cfg DependencyInstallSkillConfig,
	req DependencyInstallCommandRequest,
	opts dependencyInstallCommandRunOptions,
) (*DependencyInstallCommandResult, error) {
	command := strings.TrimSpace(req.Command)
	if DependencyCommandHasUnsafeShellSyntax(command) {
		return nil, fmt.Errorf("shell control operators are not allowed in %s", strings.TrimSpace(req.ToolName))
	}
	authReq := commandapproval.Request{
		Command:        command,
		WorkingDir:     req.WorkingDir,
		WorkspaceRoot:  req.WorkspaceRoot,
		ToolName:       strings.TrimSpace(req.ToolName),
		AgentID:        strings.TrimSpace(req.AgentID),
		AgentType:      strings.TrimSpace(req.AgentType),
		SessionID:      strings.TrimSpace(req.SessionID),
		ApprovalPolicy: commandapproval.ApprovalPolicyExact,
	}
	PopulateCommandApprovalScope(ctx, &authReq)
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), authReq); err != nil {
		return nil, WrapApprovalDenied(strings.TrimSpace(req.ToolName), err)
	}

	runResult, err := executeDependencyInstallCommand(ctx, cfg, req)
	if err != nil {
		return nil, err
	}
	if runResult.ExitCode != 0 {
		if opts.AllowNonZeroExit {
			return runResult, nil
		}
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

// executeDependencyInstallCommand runs an approved install command through the
// strict-disk execution broker. Dependency installs are never routed to host
// disk — package-manager caches, lockfile updates, and downloaded archives all
// land in the FUSE-projected workspace overlay (classified by the
// workspace-mutation policy), so a command like `pip install` cannot leak
// state outside the VFS. No fallback path exists: if the broker or the
// execution-preparation hook is not wired, the install fails loudly.
func executeDependencyInstallCommand(ctx context.Context, cfg DependencyInstallSkillConfig, req DependencyInstallCommandRequest) (*DependencyInstallCommandResult, error) {
	argv := purevfs.ShellCommandArgv(req.Command)
	if len(argv) == 0 {
		return nil, fmt.Errorf("missing install command")
	}
	if cfg.ExecutionBroker == nil || cfg.PrepareExecution == nil || cfg.ExecutionWorkspace == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	broker := cfg.ExecutionBroker()
	if broker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	execCtx, err := cfg.PrepareExecution(ctx, dependencyInstallRequestDir(req))
	if err != nil {
		return nil, fmt.Errorf("prepare execution workspace: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	runResult, err := broker.Run(callCtx, purevfs.BrokerRunRequest{
		Plan:      execCtx.Plan,
		Argv:      argv,
		Workspace: cfg.ExecutionWorkspace(true),
	})
	if err != nil {
		if callCtx.Err() != nil {
			return nil, fmt.Errorf("%s timed out after %v: %w", req.Command, req.Timeout, callCtx.Err())
		}
		return nil, fmt.Errorf("failed to execute %s: %w", req.Command, err)
	}
	return &DependencyInstallCommandResult{
		Stdout:          runResult.Stdout,
		Stderr:          runResult.Stderr,
		ExitCode:        runResult.ExitCode,
		StdoutTruncated: runResult.StdoutTruncated,
		StderrTruncated: runResult.StderrTruncated,
	}, nil
}

func normalizeDependencyInstallTimeout(defaultTimeout func() time.Duration) time.Duration {
	timeout := 5 * time.Minute
	if defaultTimeout == nil {
		return timeout
	}
	if resolved := defaultTimeout(); resolved > timeout {
		return resolved
	}
	return timeout
}

func dependencyInstallAgentID(cfg DependencyInstallSkillConfig) string {
	if cfg.AgentID == nil {
		return ""
	}
	return strings.TrimSpace(cfg.AgentID())
}

func dependencyInstallWorkingDir(cfg DependencyInstallSkillConfig) string {
	if cfg.WorkingDir == nil {
		return "."
	}
	if resolved := strings.TrimSpace(cfg.WorkingDir()); resolved != "" {
		return resolved
	}
	return "."
}

func dependencyInstallRequestDir(req DependencyInstallCommandRequest) string {
	if resolved := strings.TrimSpace(req.WorkingDir); resolved != "" {
		return resolved
	}
	if resolved := strings.TrimSpace(req.WorkspaceRoot); resolved != "" {
		return resolved
	}
	return "."
}

func dependencyInstallSessionID(ctx context.Context, cfg DependencyInstallSkillConfig) string {
	if sessionID := string(versioning.SessionIDFromContext(ctx)); strings.TrimSpace(sessionID) != "" {
		return sessionID
	}
	if cfg.SessionID == nil {
		return ""
	}
	return strings.TrimSpace(cfg.SessionID())
}

func dependencyInstallStepResult(step DependencyInstallStep, runResult *DependencyInstallCommandResult) map[string]any {
	return map[string]any{
		"command":   step.Command,
		"reason":    step.Reason,
		"exit_code": runResult.ExitCode,
		"stdout":    string(runResult.Stdout),
		"stderr":    string(runResult.Stderr),
		"truncated": runResult.StdoutTruncated || runResult.StderrTruncated,
	}
}
