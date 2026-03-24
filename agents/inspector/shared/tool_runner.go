package shared

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/versioning"
)

// ToolRunner executes external analysis tools with bounded output and timeouts.
type ToolRunner struct {
	workingDir    string
	timeout       time.Duration
	logger        *slog.Logger
	agentID       string
	agentType     string
	sessionID     func() string
	fileAccess    func() versioning.FileAccess
	broker        func() purevfs.ExecutionBroker
	requireBroker bool
	available     sync.Map // caches binary availability checks
}

type ToolRunnerConfig struct {
	WorkingDir    string
	Timeout       time.Duration
	Logger        *slog.Logger
	AgentID       string
	AgentType     string
	SessionID     func() string
	FileAccess    func() versioning.FileAccess
	Broker        func() purevfs.ExecutionBroker
	RequireBroker bool
}

type overlayAwareFileAccess interface {
	versioning.FileAccess
	Modifications() []versioning.FileModification
}

// NewToolRunner creates a ToolRunner rooted at the given working directory.
func NewToolRunner(cfg ToolRunnerConfig) *ToolRunner {
	return &ToolRunner{
		workingDir:    cfg.WorkingDir,
		timeout:       cfg.Timeout,
		logger:        cfg.Logger,
		agentID:       cfg.AgentID,
		agentType:     cfg.AgentType,
		sessionID:     cfg.SessionID,
		fileAccess:    cfg.FileAccess,
		broker:        cfg.Broker,
		requireBroker: cfg.RequireBroker,
	}
}

// maxOutputBytes bounds stdout/stderr reads to prevent unbounded memory growth.
const maxOutputBytes = 10 * 1024 * 1024 // 10 MiB

// ExecResult holds the output of an external tool invocation.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Elapsed  time.Duration
}

// Exec runs a binary with args, capturing stdout and stderr.
// It uses context for cancellation and applies the runner's timeout.
func (r *ToolRunner) Exec(ctx context.Context, binary string, args ...string) (*ExecResult, error) {
	command := strings.Join(append([]string{binary}, args...), " ")
	return r.exec(ctx, command, append([]string{binary}, args...))
}

func (r *ToolRunner) ExecShell(ctx context.Context, command string) (*ExecResult, error) {
	return r.exec(ctx, command, purevfs.ShellCommandArgv(command))
}

func (r *ToolRunner) exec(ctx context.Context, command string, argv []string) (*ExecResult, error) {
	workspaceRoot := r.currentWorkingDir()
	authReq := commandapproval.Request{
		Command:       command,
		WorkingDir:    workspaceRoot,
		WorkspaceRoot: workspaceRoot,
		ToolName:      runnerToolName(argv),
		AgentID:       r.agentID,
		AgentType:     r.agentType,
		SessionID:     r.currentSessionID(ctx),
	}
	agentshared.PopulateCommandApprovalScope(ctx, &authReq)
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), authReq); err != nil {
		return nil, err
	}
	if r.requireBroker {
		return r.execBrokered(ctx, argv)
	}
	return r.execDirect(ctx, argv)
}

func (r *ToolRunner) execDirect(ctx context.Context, argv []string) (*ExecResult, error) {
	execCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	if len(argv) == 0 {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	cmd := exec.CommandContext(execCtx, argv[0], argv[1:]...)
	cmd.Dir = r.currentWorkingDir()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe for %s: %w", argv[0], err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe for %s: %w", argv[0], err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", argv[0], err)
	}

	stdout, _ := io.ReadAll(io.LimitReader(stdoutPipe, maxOutputBytes))
	stderr, _ := io.ReadAll(io.LimitReader(stderrPipe, maxOutputBytes))

	waitErr := cmd.Wait()
	elapsed := time.Since(start)

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if execCtx.Err() != nil {
			return nil, fmt.Errorf("%s timed out after %v: %w", argv[0], r.timeout, execCtx.Err())
		}
		// Non-zero exit is normal for linters finding issues — not an error
	}

	r.logger.Debug("tool executed",
		"binary", argv[0],
		"argv", argv,
		"exit_code", exitCode,
		"elapsed", elapsed,
		"stdout_bytes", len(stdout),
		"stderr_bytes", len(stderr),
	)

	return &ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		Elapsed:  elapsed,
	}, nil
}

func (r *ToolRunner) execBrokered(ctx context.Context, argv []string) (*ExecResult, error) {
	fa := r.currentFileAccess()
	if fa == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	broker := r.currentBroker()
	if broker == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	plan, err := r.executionPlan(ctx, fa)
	if err != nil {
		return nil, err
	}
	if plan.RequiresMaterialize || !plan.RequiresBroker {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	start := time.Now()
	result, err := broker.Run(runCtx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      argv,
		Workspace: purevfs.ReadOnlyExecutionFS(fa),
	})
	if err != nil {
		return nil, err
	}
	return &ExecResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Elapsed:  time.Since(start),
	}, nil
}

func (r *ToolRunner) executionPlan(ctx context.Context, fa versioning.FileAccess) (purevfs.ExecutionPlan, error) {
	workspaceRoot := r.currentWorkingDir()
	planner := purevfs.NewExecutionPlanner(nil, r.executionCapabilities(ctx))
	overlay, overlayDeletes := overlayState(fa)
	return planner.Plan(purevfs.ExecutionRequest{
		Mode:           purevfs.ExecutionModeStrictNoDisk,
		Intent:         purevfs.ExecutionIntentCommand,
		Language:       inferExecutionLanguage(workspaceRoot, fa),
		WorkspaceRoot:  workspaceRoot,
		WorkingDir:     workspaceRoot,
		Overlay:        overlay,
		OverlayDeletes: overlayDeletes,
	})
}

func (r *ToolRunner) executionCapabilities(ctx context.Context) purevfs.ExecutionCapabilities {
	broker := r.currentBroker()
	if broker == nil {
		return purevfs.ExecutionCapabilities{}
	}
	caps, err := broker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionCapabilities{}
	}
	return caps
}

func (r *ToolRunner) currentFileAccess() versioning.FileAccess {
	if r.fileAccess == nil {
		return nil
	}
	return r.fileAccess()
}

func (r *ToolRunner) currentBroker() purevfs.ExecutionBroker {
	if r.broker == nil {
		return nil
	}
	return r.broker()
}

func (r *ToolRunner) currentWorkingDir() string {
	if fa := r.currentFileAccess(); fa != nil && strings.TrimSpace(fa.WorkingDir()) != "" {
		return fa.WorkingDir()
	}
	return r.workingDir
}

func (r *ToolRunner) currentSessionID(ctx context.Context) string {
	if sessionID := string(versioning.SessionIDFromContext(ctx)); strings.TrimSpace(sessionID) != "" {
		return sessionID
	}
	if r.sessionID == nil {
		return ""
	}
	return strings.TrimSpace(r.sessionID())
}

// Available checks whether a binary is on PATH.
// Results are cached per binary name.
func (r *ToolRunner) Available(binary string) bool {
	if cached, ok := r.available.Load(binary); ok {
		return cached.(bool)
	}
	_, err := exec.LookPath(binary)
	found := err == nil
	r.available.Store(binary, found)
	return found
}

// WorkingDir returns the runner's effective working directory.
// When FileAccess is present this tracks the injected VFS-backed workspace root.
func (r *ToolRunner) WorkingDir() string {
	return r.currentWorkingDir()
}

func (r *ToolRunner) LogicalTempDir() string {
	if r.requireBroker {
		return purevfs.DefaultLogicalExecRoots(r.currentWorkingDir()).Temp
	}
	return osTempDir()
}

func overlayState(fa versioning.FileAccess) (bool, bool) {
	overlay, ok := fa.(overlayAwareFileAccess)
	if !ok {
		return false, false
	}
	mods := overlay.Modifications()
	if len(mods) == 0 {
		return false, false
	}
	for _, mod := range mods {
		if mod.Operation == versioning.FileOpDelete {
			return true, true
		}
	}
	return true, false
}

func inferExecutionLanguage(workspaceRoot string, fa versioning.FileAccess) string {
	if summary := purevfs.DefaultCatalog().DetectProject(workspaceRoot); summary.PrimaryLanguage != "" {
		return summary.PrimaryLanguage
	}
	overlay, ok := fa.(overlayAwareFileAccess)
	if !ok {
		return ""
	}
	files := make([]string, 0, len(overlay.Modifications()))
	for _, mod := range overlay.Modifications() {
		files = append(files, mod.OriginalPath)
	}
	return purevfs.InferPrimaryLanguage(files, "inspector")
}

func runnerToolName(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[0]
}

func osTempDir() string {
	return os.TempDir()
}
