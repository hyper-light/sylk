//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// LinuxSandbox is the production substrate sandbox on Linux. It uses
// bubblewrap as the namespace orchestrator (the same tool the existing
// purevfs Linux process broker uses), which gives us mount/net/pid/
// user/uts/ipc namespace isolation without us reimplementing the
// unshare(2) dance directly.
//
// Bubblewrap is a Flatpak project; it is well-understood, kernel-
// supported, and present in every modern Linux distribution (or
// installable via the substrate's bootstrap toolchain). It accepts the
// substrate's mount plan as a series of --bind / --ro-bind / --tmpfs
// arguments and applies seccomp filtering when paired with --seccomp.
type LinuxSandbox struct {
	platform recipe.Platform
}

// NewLinux constructs a Linux sandbox.
func NewLinux() *LinuxSandbox {
	return &LinuxSandbox{
		platform: recipe.Platform{OS: "linux", Arch: runtime.GOARCH, Libc: "glibc"},
	}
}

// Platform returns the configured platform tuple.
func (s *LinuxSandbox) Platform() recipe.Platform { return s.platform }

// Spawn executes cfg under bubblewrap with full namespace isolation.
//
// Pre-flight checks: bubblewrap must be installed; cfg.Cmd must be
// non-empty.
//
// Network: NetworkNone yields --unshare-net (empty network namespace,
// no interfaces). NetworkLimitedToSubstrate is treated as NetworkNone
// for now (the substrate has no internal endpoints yet).
//
// Determinism: composes cfg.Env with the StandardDeterminismProfile
// defaults (SOURCE_DATE_EPOCH=0, LC_ALL=C, etc.) and passes the
// result via bubblewrap's --setenv arguments rather than inheriting
// the host environment.
func (s *LinuxSandbox) Spawn(ctx context.Context, cfg Config) (*Result, error) {
	if err := preflight(cfg); err != nil {
		return nil, err
	}
	if !commandAvailable("bwrap") {
		return s.degradedExec(ctx, cfg, "bubblewrap not installed; spawn is not hermetic")
	}
	return s.runUnderBubblewrap(ctx, cfg)
}

func preflight(cfg Config) error {
	if len(cfg.Cmd) == 0 {
		return ErrEmptyCmd
	}
	return nil
}

func (s *LinuxSandbox) runUnderBubblewrap(ctx context.Context, cfg Config) (*Result, error) {
	args := s.buildBubblewrapArgs(cfg)
	cmdCtx, cancel := contextWithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bwrap", args...)
	cmd.Env = envSlice(composedEnv(cfg))
	stdout, stderr := setupCaptureStreams(cmd, cfg)

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	exitCode, classifyErr := classifyExitError(err, cmdCtx.Err())
	if classifyErr != nil {
		return nil, classifyErr
	}
	return &Result{
		ExitCode: exitCode,
		Stdout:   captureBytes(stdout),
		Stderr:   captureBytes(stderr),
		Duration: duration,
		Hermetic: true,
	}, nil
}

func (s *LinuxSandbox) buildBubblewrapArgs(cfg Config) []string {
	args := []string{
		"--unshare-all",
		"--die-with-parent",
		"--proc", "/proc",
		"--dev", "/dev",
	}
	args = appendNetworkArgs(args, cfg.Network)
	args = appendMountArgs(args, cfg.Mounts)
	args = appendCwdArgs(args, cfg.Cwd)
	args = appendEnvArgs(args, composedEnv(cfg))
	args = append(args, "--")
	args = append(args, cfg.Cmd...)
	return args
}

func appendNetworkArgs(args []string, policy NetworkPolicy) []string {
	// Default --unshare-all already takes net namespace; if the policy
	// were to allow network, we'd add --share-net to override.
	if policy == "" || policy == NetworkNone || policy == NetworkLimitedToSubstrate {
		return args
	}
	return append(args, "--share-net")
}

func appendMountArgs(args []string, mounts []Mount) []string {
	for _, m := range mounts {
		flag := "--bind"
		if m.ReadOnly {
			flag = "--ro-bind"
		}
		args = append(args, flag, m.HostPath, m.ViewPath)
	}
	return args
}

func appendCwdArgs(args []string, cwd string) []string {
	if strings.TrimSpace(cwd) == "" {
		return args
	}
	return append(args, "--chdir", cwd)
}

func appendEnvArgs(args []string, env map[string]string) []string {
	for k, v := range env {
		args = append(args, "--setenv", k, v)
	}
	return args
}

func composedEnv(cfg Config) map[string]string {
	// composeEnvironment already substitutes defaults for empty fields,
	// so passing the raw profile (which may be the zero value) is safe.
	return composeEnvironment(cfg.Env, cfg.Determinism)
}

func setupCaptureStreams(cmd *exec.Cmd, cfg Config) (io.Writer, io.Writer) {
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = &bytes.Buffer{}
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = &bytes.Buffer{}
	}
	cmd.Stdin = cfg.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return stdout, stderr
}

func captureBytes(w io.Writer) []byte {
	if buf, ok := w.(*bytes.Buffer); ok {
		return buf.Bytes()
	}
	return nil
}

func contextWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func classifyExitError(runErr error, ctxErr error) (int, error) {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return 0, ErrTimeout
	}
	if runErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, fmt.Errorf("%w: %w", ErrSpawnFailed, runErr)
}

// degradedExec runs the command without sandbox isolation. Used as a
// last-resort fallback when bubblewrap is missing — the result is
// surfaced to the caller with Hermetic=false and a warning so the
// degraded mode is observable.
func (s *LinuxSandbox) degradedExec(ctx context.Context, cfg Config, warning string) (*Result, error) {
	cmdCtx, cancel := contextWithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, cfg.Cmd[0], cfg.Cmd[1:]...)
	cmd.Dir = cfg.Cwd
	cmd.Env = envSlice(composedEnv(cfg))
	stdout, stderr := setupCaptureStreams(cmd, cfg)
	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	exitCode, classifyErr := classifyExitError(err, cmdCtx.Err())
	if classifyErr != nil {
		return nil, classifyErr
	}
	return &Result{
		ExitCode: exitCode,
		Stdout:   captureBytes(stdout),
		Stderr:   captureBytes(stderr),
		Duration: duration,
		Hermetic: false,
		Warnings: []string{warning},
	}, nil
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
