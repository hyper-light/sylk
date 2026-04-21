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

type CommandExecution struct {
	Command           string        `json:"command"`
	ExitCode          int           `json:"exit_code"`
	Stdout            string        `json:"stdout,omitempty"`
	Stderr            string        `json:"stderr,omitempty"`
	Duration          time.Duration `json:"duration"`
	StartTime         time.Time     `json:"start_time"`
	WorkingDir        string        `json:"working_dir"`
	ExecutionMode     string        `json:"execution_mode,omitempty"`
	ExecutionStrategy string        `json:"execution_strategy,omitempty"`
	Materialized      bool          `json:"materialized,omitempty"`
	Truncated         bool          `json:"truncated,omitempty"`
}

type CommandExecContext struct {
	WorkDir string
	Plan    purevfs.ExecutionPlan
}

type CommandSkillConfig struct {
	AgentType            string
	AgentID              func() string
	SessionID            func() string
	CommandsEnabled      func() bool
	WorkspaceRoot        func() string
	DefaultTimeout       func() time.Duration
	PrepareExecution     func(context.Context, string) (CommandExecContext, error)
	ExecutionBroker      func() purevfs.ExecutionBroker
	ExecutionWorkspace   func(bool) purevfs.ExecutionFS
	AllowWorkspaceWrites bool
	PreAuthorizeCheck    func(command, toolName string) error
}

type runCommandParams struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir,omitempty"`
	TimeoutMs  int    `json:"timeout_ms,omitempty"`
}

type runShellScriptParams struct {
	Script     string `json:"script"`
	WorkingDir string `json:"working_dir,omitempty"`
	TimeoutMs  int    `json:"timeout_ms,omitempty"`
}

type bashParams struct {
	Script     string `json:"script"`
	WorkingDir string `json:"working_dir,omitempty"`
	TimeoutMs  int    `json:"timeout_ms,omitempty"`
}

// NewBashSkill returns the unified `bash` execution skill that supersedes
// run_command + run_shell_script. One skill, one parameter (`script`),
// dynamic approval policy:
//
//   - scripts that parse as a single plain command (no shell operators,
//     no redirection, no pipes, no multi-line) use the default approval
//     policy — pre-approved patterns can fast-path, matching the old
//     run_command ergonomics.
//   - anything with shell syntax (&&, ||, ;, |, >, <, `, $(), newlines)
//     uses the exact-command approval policy — pre-approval does not
//     apply, matching the old run_shell_script semantics.
//
// The agent sees one tool; Guardian gating is unchanged.
func NewBashSkill(cfg CommandSkillConfig) *skills.Skill {
	return skills.NewSkill("bash").
		Description("Execute a shell command or script against the agent's current workspace view. Pass a single plain command for fast-path approval; pass a compound script (&&, ||, pipes, redirection, multi-line) when the task needs shell features. One tool for both — the approval policy adapts to the script shape automatically.").
		Domain("code").
		Keywords("run", "execute", "command", "shell", "script", "bash", "pipe", "redirect", "verify").
		Priority(80).
		Usage("Use for any shell execution: verifying behavior, running tooling, gathering execution evidence, setting up test fixtures. For simple commands (ls, go test, pytest), just pass the command. When chaining or piping is genuinely needed, write the full script inline — don't fight the tool.").
		Satisfies("Produces concrete execution evidence for validation, diagnosis, and reporting.").
		Avoid("Do not use bash as a shortcut around the workspace write tools — use those for file edits. Do not pack unrelated steps into one script; keep each invocation purpose-built.").
		BestPractice("Use working_dir instead of prefixing the script with cd.").
		BestPractice("When strict execution is available, scripts run against the same disk/global/pipeline workspace view the agent is operating on, including VFS-backed files that are not committed to disk yet.").
		BestPractice("working_dir may point at a directory from that active workspace view, including directories that currently exist only in VFS-backed task state.").
		BestPractice("Simple single-command invocations (no shell operators) can fast-path through pre-approval; compound scripts always require exact-command approval.").
		StringParam("script", "Shell command or script to execute. Single plain command for fast-path approval; compound script (operators, pipes, redirection, multi-line) for full shell features.", true).
		StringParam("working_dir", "Working directory for script execution", false).
		IntParam("timeout_ms", "Script timeout in milliseconds", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params bashParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			script := strings.TrimSpace(params.Script)
			// Script shape determines approval policy. Plain commands
			// keep the old run_command fast-path; anything with shell
			// syntax falls back to exact approval like run_shell_script.
			policy := commandapproval.ApprovalPolicyDefault
			allowShellSyntax := false
			if _, hasOperator := DetectShellControlOperator(script); hasOperator {
				policy = commandapproval.ApprovalPolicyExact
				allowShellSyntax = true
			}
			return executeCommandLike(ctx, cfg, "bash", script, params.WorkingDir, params.TimeoutMs, policy, allowShellSyntax)
		}).
		Build()
}

func executeCommandLike(
	ctx context.Context,
	cfg CommandSkillConfig,
	toolName, command, workingDir string,
	timeoutMs int,
	policy commandapproval.ApprovalPolicy,
	allowShellSyntax bool,
) (*CommandExecution, error) {
	if command == "" {
		return nil, fmt.Errorf("%s requires a non-empty command", toolName)
	}
	if cfg.CommandsEnabled != nil && !cfg.CommandsEnabled() {
		return nil, fmt.Errorf("command execution is disabled")
	}
	if !allowShellSyntax {
		if issue, ok := DetectShellControlOperator(command); ok {
			return nil, SingleCommandOnlyError(toolName, issue, command)
		}
	}
	if cfg.PreAuthorizeCheck != nil {
		if err := cfg.PreAuthorizeCheck(command, toolName); err != nil {
			return nil, err
		}
	}

	workspaceRoot := "."
	if cfg.WorkspaceRoot != nil {
		if resolved := strings.TrimSpace(cfg.WorkspaceRoot()); resolved != "" {
			workspaceRoot = resolved
		}
	}
	sessionID := versioning.SessionIDFromContext(ctx)
	if sessionID == "" && cfg.SessionID != nil {
		sessionID = strings.TrimSpace(cfg.SessionID())
	}
	agentID := ""
	if cfg.AgentID != nil {
		agentID = strings.TrimSpace(cfg.AgentID())
	}
	authReq := commandapproval.Request{
		Command:        command,
		WorkingDir:     workingDir,
		WorkspaceRoot:  workspaceRoot,
		ToolName:       toolName,
		AgentID:        agentID,
		AgentType:      strings.TrimSpace(cfg.AgentType),
		SessionID:      sessionID,
		ApprovalPolicy: policy,
	}
	PopulateCommandApprovalScope(ctx, &authReq)
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), authReq); err != nil {
		return nil, WrapApprovalDenied(toolName, err)
	}
	if cfg.PrepareExecution == nil {
		return nil, fmt.Errorf("command execution is unavailable")
	}
	execCtx, err := cfg.PrepareExecution(ctx, workingDir)
	if err != nil {
		return nil, fmt.Errorf("prepare execution workspace: %w", err)
	}
	broker := purevfs.ExecutionBroker(nil)
	if cfg.ExecutionBroker != nil {
		broker = cfg.ExecutionBroker()
	}
	if broker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	timeout := 30 * time.Second
	if cfg.DefaultTimeout != nil {
		if resolved := cfg.DefaultTimeout(); resolved > 0 {
			timeout = resolved
		}
	}
	if timeoutMs > 0 {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()
	workspace := purevfs.ExecutionFS(nil)
	if cfg.ExecutionWorkspace != nil {
		workspace = cfg.ExecutionWorkspace(cfg.AllowWorkspaceWrites)
	}
	runResult, err := broker.Run(runCtx, purevfs.BrokerRunRequest{
		Plan:      execCtx.Plan,
		Argv:      purevfs.ShellCommandArgv(command),
		Workspace: workspace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %w", err)
	}

	return &CommandExecution{
		Command:           command,
		ExitCode:          runResult.ExitCode,
		Stdout:            string(runResult.Stdout),
		Stderr:            string(runResult.Stderr),
		Duration:          time.Since(startTime),
		StartTime:         startTime,
		WorkingDir:        execCtx.WorkDir,
		ExecutionMode:     string(execCtx.Plan.Mode),
		ExecutionStrategy: string(execCtx.Plan.Strategy),
		Materialized:      execCtx.Plan.RequiresMaterialize,
		Truncated:         runResult.StdoutTruncated || runResult.StderrTruncated,
	}, nil
}

func DetectShellControlOperator(command string) (string, bool) {
	var (
		inSingle bool
		inDouble bool
		escaped  bool
	)
	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case escaped:
			escaped = false
			continue
		case inSingle:
			if ch == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inDouble = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '\\':
			escaped = true
		case '\n', '\r':
			return "multi-line shell", true
		case 0:
			return "NUL byte", true
		case ';':
			return ";", true
		case '|':
			if i+1 < len(command) && command[i+1] == '|' {
				return "||", true
			}
			return "|", true
		case '&':
			if i+1 < len(command) && command[i+1] == '&' {
				return "&&", true
			}
		case '`':
			return "`", true
		case '$':
			if i+1 < len(command) {
				switch command[i+1] {
				case '(':
					return "$(", true
				case '{':
					return "${", true
				}
			}
		case '>':
			return ">", true
		case '<':
			return "<", true
		}
	}
	return "", false
}

func SingleCommandOnlyError(toolName, issue, command string) error {
	message := toolName + " only accepts one plain command"
	if trimmed := strings.TrimSpace(issue); trimmed != "" {
		message += " (detected " + trimmed + ")"
	}
	recovery := []string{
		"use working_dir instead of cd",
		"split chained steps into separate run_command calls",
		"use run_shell_script for &&, ||, ;, pipes, redirection, shell variables, or multi-line shell",
	}
	if !strings.HasPrefix(strings.TrimSpace(command), "cd ") {
		recovery = recovery[1:]
	}
	return fmt.Errorf("%s; %s", message, strings.Join(recovery, "; "))
}
