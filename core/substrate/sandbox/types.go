// Package sandbox is the substrate's hermetic execution environment.
//
// A Sandbox spawns a child process in a tightly-controlled environment:
// no network, no host filesystem visibility outside the explicit mount
// set, no host process visibility, no privilege escalation,
// deterministic environment variables. Three platform backends
// implement the same Sandbox interface using each platform's native
// primitives:
//
//   - Linux:   namespaces (mount/net/pid/user/uts/ipc) + seccomp-bpf +
//              cgroups v2; mature, kernel-enforced, the production target
//   - macOS:   sandbox-exec profile + Network Extension content filter +
//              Endpoint Security framework + restricted entitlements;
//              kernel-enforced, parity with Linux for the substrate's
//              hermeticity requirements
//   - Windows: AppContainer + Job Object + Windows Filtering Platform
//              filter; kernel-enforced, microsoft-supported model
//
// Phase 2.5 ships the interface, the Linux backend in production-ready
// form, and macOS/Windows backends as documented stubs that exec the
// command without hermeticity (returning a structured warning so
// callers can surface the degraded mode). The stub mode is suitable
// for development on macOS/Windows hosts; production substrate
// deployments target Linux.
package sandbox

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Sandbox is the substrate's hermetic execution environment.
type Sandbox interface {
	// Spawn executes cfg.Cmd inside the sandbox and waits for it to
	// complete. Returns the exit code, captured stdout/stderr, and
	// duration.
	//
	// Hermeticity guarantees vary by platform:
	//   - Linux: full kernel-enforced isolation (net/mnt/pid/user/uts/ipc
	//     namespaces; seccomp; cgroups)
	//   - macOS: full sandbox-exec + Network Extension filtering
	//   - Windows: AppContainer + WFP
	//
	// On a platform whose backend is unimplemented, Spawn returns
	// ErrUnimplemented along with a non-nil result whose Hermetic field
	// is false — degraded execution mode for development hosts.
	Spawn(ctx context.Context, cfg Config) (*Result, error)

	// Platform returns the host platform tuple this Sandbox is
	// configured for. Used by the substrate's execution backend to
	// reject mismatched spawns early.
	Platform() recipe.Platform
}

// Config configures one Sandbox spawn.
type Config struct {
	// Cmd is the argv to execute. Cmd[0] is resolved against the
	// PATH supplied in Env (and the host PATH if Env doesn't override).
	Cmd []string

	// Cwd is the working directory inside the sandbox view of the
	// filesystem.
	Cwd string

	// Env is the environment supplied to the spawned process. The
	// determinism layer adds the standard clamps (LC_ALL=C, etc.) on
	// top.
	Env map[string]string

	// Mounts lists the FUSE projections the sandbox should expose
	// inside the spawned process's view. Each Mount is a (host_path,
	// view_path, ro_flag) tuple. The Linux backend translates these
	// into bind mounts; macOS/Windows translate to per-platform
	// equivalents.
	Mounts []Mount

	// Network policy. None is the default for substrate spawns; only
	// recipe-author-declared exceptions get LimitedToSubstrate.
	Network NetworkPolicy

	// Capabilities optionally extends the default capability set.
	// Empty defaults to ReadOnlyMounts + ExecBinaries.
	Capabilities Capabilities

	// Determinism configures the env clamping the substrate applies on
	// top of cfg.Env. Empty uses StandardDeterminismProfile.
	Determinism DeterminismProfile

	// Timeout caps wall-clock time for the spawned process. Zero
	// disables.
	Timeout time.Duration

	// MemoryCap is the maximum RSS the spawned process may consume,
	// in bytes. Zero disables. Linux: applied via cgroups v2. Other
	// platforms apply equivalent limits where available.
	MemoryCap uint64

	// CPUCap caps CPU shares (Linux: cgroup cpu.max; equivalent on
	// other platforms). Zero disables.
	CPUCap float64

	// Stdin, Stdout, Stderr: optional I/O streams. Stdout/Stderr
	// default to in-memory capture buffers; passing non-nil streams
	// suppresses capture for that stream.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Mount describes one filesystem projection visible inside the sandbox.
type Mount struct {
	HostPath string // path on the host (or substrate view path) to bind
	ViewPath string // path the spawned process sees the bind at
	ReadOnly bool
}

// NetworkPolicy enumerates the network access modes a Spawn can request.
type NetworkPolicy string

const (
	// NetworkNone forbids all network access. Kernel-enforced on every
	// supported platform.
	NetworkNone NetworkPolicy = "none"

	// NetworkLimitedToSubstrate allows only egress to substrate-internal
	// addresses (currently empty in practice — reserved for future use
	// like a substrate-managed package mirror). Treated as NetworkNone
	// until the substrate has internal endpoints to whitelist.
	NetworkLimitedToSubstrate NetworkPolicy = "limited-to-substrate"
)

// Capabilities configures additional permissions the sandbox grants.
type Capabilities struct {
	// AllowRawSockets permits raw sockets (typically used by ping,
	// traceroute, network diagnostic tools). Off by default.
	AllowRawSockets bool

	// AllowPtrace permits the spawned process to ptrace its own
	// children. Off by default; debuggers running under the substrate
	// need this.
	AllowPtrace bool
}

// DeterminismProfile configures the env clamping the sandbox applies
// to make output bytes reproducible across hosts.
type DeterminismProfile struct {
	// SourceDateEpoch is the value of the SOURCE_DATE_EPOCH env var.
	// Default 0.
	SourceDateEpoch int64

	// Locale clamps LC_ALL. Default "C".
	Locale string

	// Hostname is what gethostname() returns inside the sandbox.
	// Default "substrate".
	Hostname string

	// User is what whoami / $USER report. Default "builder".
	User string

	// Home is what $HOME reports. Default "/sylk/home".
	Home string

	// EnvScrub names env vars to remove entirely from the sandbox env.
	EnvScrub []string

	// EnvAdd are additional env clamps composed after the defaults.
	EnvAdd map[string]string
}

// StandardDeterminismProfile returns the substrate's default
// determinism settings.
func StandardDeterminismProfile() DeterminismProfile {
	return DeterminismProfile{
		SourceDateEpoch: 0,
		Locale:          "C",
		Hostname:        "substrate",
		User:            "builder",
		Home:            "/sylk/home",
	}
}

// Result is what the sandbox returns after a spawn completes.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration

	// Hermetic reports whether the sandbox actually enforced isolation.
	// True on Linux (and on macOS/Windows once their backends ship).
	// False when the platform falls back to degraded execution.
	Hermetic bool

	// Warnings carry messages from the sandbox itself (e.g. "this
	// platform's backend is a stub; spawned process is not hermetic").
	Warnings []string
}

// Errors.
var (
	// ErrEmptyCmd: cfg.Cmd was empty.
	ErrEmptyCmd = errors.New("sandbox: empty Cmd")

	// ErrUnimplemented: the host platform's backend is not yet
	// implemented in production form. The Sandbox falls back to
	// non-hermetic exec and surfaces this error.
	ErrUnimplemented = errors.New("sandbox: backend not yet implemented for this platform")

	// ErrSpawnFailed: the underlying process spawn or wait failed.
	ErrSpawnFailed = errors.New("sandbox: spawn failed")

	// ErrTimeout: cfg.Timeout elapsed before the process completed.
	ErrTimeout = errors.New("sandbox: spawn timed out")
)
