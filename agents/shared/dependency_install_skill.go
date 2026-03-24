package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

const dependencyInstallMaxOutputBytes = 10 * 1024 * 1024

type DependencyInstallSkillConfig struct {
	SkillName       string
	Description     string
	Domain          string
	Keywords        []string
	Priority        int
	Usage           string
	Requirement     string
	Satisfies       string
	Avoid           string
	ResearchSkill   string
	AgentType       string
	AgentID         func() string
	SessionID       func() string
	WorkingDir      func() string
	CommandsEnabled func() bool
	DefaultTimeout  func() time.Duration
	ExecuteCommand  func(context.Context, DependencyInstallCommandRequest) (*DependencyInstallCommandResult, error)
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
		"command": {Type: "string", Description: "Single install command to run against the real disk workspace. No pipes, chaining, or shell control operators."},
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
		BestPractice("These commands execute against the real disk workspace, not the VFS overlay, so successful installs persist to disk for later agent turns.").
		BestPractice("Each install or validation step uses exact approval semantics through the same approval flow as command execution tools.").
		BestPractice(fmt.Sprintf("If you cannot determine the correct install command with significant confidence, call `%s` first instead of guessing.", researchSkill)).
		StringParam("summary", "Short explanation of the install plan.", true).
		StringParam("missing_tool", "Missing dependency, tool, or utility this plan remedies.", false).
		StringParam("framework", "Framework or ecosystem context for the install plan.", false).
		StringParam("validation_command", "Optional non-mutating command to verify the disk-backed install succeeded.", false).
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
		runResult, err := runDependencyInstallCommand(ctx, cfg, DependencyInstallCommandRequest{
			ToolName:      cfg.SkillName,
			Command:       validationCommand,
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
		result["validation"] = map[string]any{
			"command":   validationCommand,
			"exit_code": runResult.ExitCode,
			"stdout":    string(runResult.Stdout),
			"stderr":    string(runResult.Stderr),
			"truncated": runResult.StdoutTruncated || runResult.StderrTruncated,
		}
	}
	return result, nil
}

func runDependencyInstallCommand(ctx context.Context, cfg DependencyInstallSkillConfig, req DependencyInstallCommandRequest) (*DependencyInstallCommandResult, error) {
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
		return nil, err
	}

	runner := cfg.ExecuteCommand
	if runner == nil {
		runner = executeDependencyInstallCommandOnDisk
	}
	runResult, err := runner(ctx, req)
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

func executeDependencyInstallCommandOnDisk(ctx context.Context, req DependencyInstallCommandRequest) (*DependencyInstallCommandResult, error) {
	argv := purevfs.ShellCommandArgv(req.Command)
	if len(argv) == 0 {
		return nil, fmt.Errorf("missing install command")
	}
	callCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	cmd := exec.CommandContext(callCtx, argv[0], argv[1:]...)
	cmd.Dir = dependencyInstallRequestDir(req)
	cmd.Env = os.Environ()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe for %s: %w", req.Command, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe for %s: %w", req.Command, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", req.Command, err)
	}

	var (
		stdout []byte
		stderr []byte
		wg     sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		stdout, _ = io.ReadAll(io.LimitReader(stdoutPipe, dependencyInstallMaxOutputBytes))
	}()
	go func() {
		defer wg.Done()
		stderr, _ = io.ReadAll(io.LimitReader(stderrPipe, dependencyInstallMaxOutputBytes))
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if callCtx.Err() != nil {
			return nil, fmt.Errorf("%s timed out after %v: %w", req.Command, req.Timeout, callCtx.Err())
		} else {
			return nil, waitErr
		}
	}

	return &DependencyInstallCommandResult{
		Stdout:          stdout,
		Stderr:          stderr,
		ExitCode:        exitCode,
		StdoutTruncated: len(stdout) >= dependencyInstallMaxOutputBytes,
		StderrTruncated: len(stderr) >= dependencyInstallMaxOutputBytes,
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
