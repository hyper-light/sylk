package shared

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// ToolRunner executes external analysis tools with bounded output and timeouts.
type ToolRunner struct {
	workingDir string
	timeout    time.Duration
	logger     *slog.Logger
	available  sync.Map // caches binary availability checks
}

// NewToolRunner creates a ToolRunner rooted at the given working directory.
func NewToolRunner(workingDir string, timeout time.Duration, logger *slog.Logger) *ToolRunner {
	return &ToolRunner{
		workingDir: workingDir,
		timeout:    timeout,
		logger:     logger,
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
	execCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, binary, args...)
	cmd.Dir = r.workingDir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe for %s: %w", binary, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe for %s: %w", binary, err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", binary, err)
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
			return nil, fmt.Errorf("%s timed out after %v: %w", binary, r.timeout, execCtx.Err())
		}
		// Non-zero exit is normal for linters finding issues — not an error
	}

	r.logger.Debug("tool executed",
		"binary", binary,
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

// WorkingDir returns the runner's configured working directory.
func (r *ToolRunner) WorkingDir() string {
	return r.workingDir
}
