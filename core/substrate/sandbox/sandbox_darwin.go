//go:build darwin

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

// DarwinSandbox is the macOS substrate sandbox.
//
// Backed by Apple's `sandbox-exec` — the same kernel-enforced
// sandbox(7) machinery that App Store apps use, exposed as a CLI that
// accepts an inline TinyScheme-like profile via the `-p` flag. Apple
// has marked the `sandbox-exec` *binary* as "deprecated for new apps"
// since macOS 10.8 but the underlying `sandbox(7)` kernel facility
// remains the canonical macOS process-isolation primitive and ships
// in every supported macOS release through Sequoia.
//
// The substrate uses inline profiles (`-p`) rather than profile files
// so the no-disk-write invariant is preserved — the profile is
// generated at spawn time, fed to `sandbox-exec` over its argv, and
// never touches the host filesystem.
//
// Hermeticity guarantees:
//
//   - Filesystem: kernel-enforced via the sandbox profile's
//     `(deny file-read*) (deny file-write*)` baseline plus explicit
//     allowlist subpaths for substrate-mounted views and the system
//     framework directories dyld needs to load libSystem.
//   - Network: kernel-enforced via `(deny network*)`; the kernel's
//     network-system-call interceptor blocks `socket(2)`/`connect(2)`/
//     `bind(2)` for the sandboxed pid before they reach the network
//     stack. No NEFilterDataProvider extension required.
//   - Process: macOS does not expose Linux-style PID namespaces.
//     `(deny process-info-listpids)` prevents PID enumeration; full
//     PID isolation is a documented limitation accepted in exchange
//     for the substrate not needing to ship a kernel extension.
//
// Mounts: the substrate's FUSE projection (Phase 5/5.5) is what
// actually exposes substrate content as a filesystem on macOS. The
// Mount entries in cfg.Mounts tell this sandbox WHICH paths the
// generated profile should allow reads under. The substrate's FUSE
// layer (cgofuse over macFUSE / FUSE-T) handles the actual mounting;
// the sandbox just constrains the profile so the spawned process can
// only see those paths.
type DarwinSandbox struct {
	platform recipe.Platform
}

// NewDarwin constructs a macOS sandbox.
func NewDarwin() *DarwinSandbox {
	return &DarwinSandbox{
		platform: recipe.Platform{OS: "darwin", Arch: runtime.GOARCH, Libc: "system"},
	}
}

// Platform returns the configured platform tuple.
func (s *DarwinSandbox) Platform() recipe.Platform { return s.platform }

// Spawn executes cfg under sandbox-exec with full hermeticity.
//
// When sandbox-exec is unavailable (vanishingly rare on macOS — it
// ships in /usr/bin in every macOS release), Spawn falls back to
// degraded execution and surfaces a Hermetic=false result with an
// explanatory warning.
func (s *DarwinSandbox) Spawn(ctx context.Context, cfg Config) (*Result, error) {
	if err := preflightDarwin(cfg); err != nil {
		return nil, err
	}
	if !commandAvailableDarwin("sandbox-exec") {
		return s.degradedExec(ctx, cfg, "sandbox-exec not available; spawn is not hermetic")
	}
	return s.runUnderSandboxExec(ctx, cfg)
}

func preflightDarwin(cfg Config) error {
	if len(cfg.Cmd) == 0 {
		return ErrEmptyCmd
	}
	return nil
}

func (s *DarwinSandbox) runUnderSandboxExec(ctx context.Context, cfg Config) (*Result, error) {
	profile := generateDarwinSandboxProfile(cfg)
	cmdCtx, cancel := contextWithTimeout(ctx, cfg.Timeout)
	defer cancel()

	args := buildDarwinSandboxArgs(profile, cfg)
	cmd := exec.CommandContext(cmdCtx, "sandbox-exec", args...)
	cmd.Dir = cfg.Cwd
	cmd.Env = envSlice(composedEnvDarwin(cfg))
	stdout, stderr := setupCaptureStreamsDarwin(cmd, cfg)

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	exitCode, classifyErr := classifyExitErrorDarwin(err, cmdCtx.Err())
	if classifyErr != nil {
		return nil, classifyErr
	}
	return &Result{
		ExitCode: exitCode,
		Stdout:   captureBytesDarwin(stdout),
		Stderr:   captureBytesDarwin(stderr),
		Duration: duration,
		Hermetic: true,
	}, nil
}

func buildDarwinSandboxArgs(profile string, cfg Config) []string {
	args := []string{"-p", profile}
	args = append(args, "--")
	args = append(args, cfg.Cmd...)
	return args
}

// generateDarwinSandboxProfile builds the sandbox profile for one spawn.
// Returns an inline TinyScheme expression suitable for `sandbox-exec -p`.
//
// Profile structure:
//
//   1. (version 1) — required header
//   2. (deny default) — start from a fully-denied baseline
//   3. (allow process-*) — the spawned process needs to fork/exec/signal itself
//   4. (allow mach-lookup ...) — minimum mach IPC for libSystem
//   5. (allow file-read* ...) — system framework dirs + substrate mounts
//   6. (allow file-write* ...) — writable substrate mounts only
//   7. (deny network*) when cfg.Network is None — explicit for clarity
//      even though `deny default` already covers it
func generateDarwinSandboxProfile(cfg Config) string {
	var b strings.Builder
	writeProfileHeader(&b)
	writeProcessRules(&b)
	writeMachLookupRules(&b)
	writeSystemReadRules(&b)
	writeMountRules(&b, cfg.Mounts)
	writeNetworkRules(&b, cfg.Network)
	writeCapabilityRules(&b, cfg.Capabilities)
	return b.String()
}

func writeProfileHeader(b *strings.Builder) {
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
}

func writeProcessRules(b *strings.Builder) {
	// The spawned process needs to fork/exec for any non-trivial workload
	// (e.g. python -> compiler). signal-target-self is needed for the
	// process to deliver signals to its own children.
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow signal (target self))\n")
	b.WriteString("(allow process-info-pidinfo (target self))\n")
}

func writeMachLookupRules(b *strings.Builder) {
	// Minimal mach service set required for dyld and libSystem to
	// initialize. These are the well-known service names every
	// CLI process consumes; deny-by-default would otherwise block
	// process startup before main() runs.
	services := []string{
		"com.apple.system.notification_center",
		"com.apple.system.opendirectoryd.libinfo",
		"com.apple.system.opendirectoryd.membership",
		"com.apple.system.logger",
		"com.apple.dyld.logging",
		"com.apple.distributed_notifications.2",
	}
	for _, name := range services {
		fmt.Fprintf(b, "(allow mach-lookup (global-name %q))\n", name)
	}
}

func writeSystemReadRules(b *strings.Builder) {
	// Apple framework + dyld + system library trees the spawned process
	// must be able to read just to load libSystem and any linked
	// framework. Allowed read-only; the substrate's no-disk-write
	// invariant means the spawned process has no business writing
	// here either.
	systemReads := []string{
		"/System",
		"/usr/lib",
		"/usr/share",
		"/Library/Frameworks",
		"/Library/Apple/System",
		"/private/var/db/dyld",
		"/private/var/db/timezone",
		"/private/etc/localtime",
	}
	for _, path := range systemReads {
		fmt.Fprintf(b, "(allow file-read* (subpath %q))\n", path)
	}
	// Metadata reads (stat, readlink) are needed for argv[0] resolution
	// and dyld's library search; allow globally with no path restriction.
	b.WriteString("(allow file-read-metadata)\n")
}

func writeMountRules(b *strings.Builder, mounts []Mount) {
	for _, m := range mounts {
		fmt.Fprintf(b, "(allow file-read* (subpath %q))\n", m.HostPath)
		if !m.ReadOnly {
			fmt.Fprintf(b, "(allow file-write* (subpath %q))\n", m.HostPath)
		}
	}
}

func writeNetworkRules(b *strings.Builder, policy NetworkPolicy) {
	// (deny default) already blocks network operations; the explicit
	// directive here documents intent and survives any future profile
	// reorganization that might allow other operations broadly.
	if policy == NetworkNone || policy == NetworkLimitedToSubstrate || policy == "" {
		b.WriteString("(deny network*)\n")
		return
	}
	// Future: NetworkLimitedToSubstrate could allow a substrate-managed
	// loopback mirror endpoint here. Today the substrate has no such
	// endpoint, so it falls through to deny-all.
}

func writeCapabilityRules(b *strings.Builder, caps Capabilities) {
	// AllowRawSockets / AllowPtrace are extension points for future use;
	// neither is grantable from inside a sandbox-exec profile alone:
	//   - Raw sockets: covered by the network rules above when network
	//     is permitted.
	//   - ptrace: requires task_for_pid privileges, which can only be
	//     granted via host-side entitlements on the spawned binary.
	// Documented here so the substrate's capability set has visible
	// platform-mapping notes; no profile bytes emitted today.
	_ = caps
}

// degradedExec runs the command without sandbox isolation. Reached when
// sandbox-exec is missing — vanishingly rare on macOS.
func (s *DarwinSandbox) degradedExec(ctx context.Context, cfg Config, warning string) (*Result, error) {
	cmdCtx, cancel := contextWithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, cfg.Cmd[0], cfg.Cmd[1:]...)
	cmd.Dir = cfg.Cwd
	cmd.Env = envSlice(composedEnvDarwin(cfg))
	stdout, stderr := setupCaptureStreamsDarwin(cmd, cfg)

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	exitCode, classifyErr := classifyExitErrorDarwin(err, cmdCtx.Err())
	if classifyErr != nil {
		return nil, classifyErr
	}
	return &Result{
		ExitCode: exitCode,
		Stdout:   captureBytesDarwin(stdout),
		Stderr:   captureBytesDarwin(stderr),
		Duration: duration,
		Hermetic: false,
		Warnings: []string{warning},
	}, nil
}

func composedEnvDarwin(cfg Config) map[string]string {
	return composeEnvironment(cfg.Env, cfg.Determinism)
}

func setupCaptureStreamsDarwin(cmd *exec.Cmd, cfg Config) (io.Writer, io.Writer) {
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

func captureBytesDarwin(w io.Writer) []byte {
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

func classifyExitErrorDarwin(runErr error, ctxErr error) (int, error) {
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

func commandAvailableDarwin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
