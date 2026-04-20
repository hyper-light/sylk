//go:build linux

// FUSE-library-agnostic Linux process broker support.
//
// The split between this file and the FUSE-specific files
// (process_broker_linux_bazil.go, process_broker_linux_hanwen.go)
// follows the substrate's two-track FUSE strategy on Linux: bazil/fuse
// is the legacy implementation kept available behind a build tag during
// migration; hanwen/go-fuse/v2 is the production-target implementation.
//
// Only one FUSE backend is compiled at a time. The selector is the
// `substrate_fuse_v2` build tag:
//
//   - default (no tag): bazil/fuse
//   - `-tags substrate_fuse_v2`: hanwen/go-fuse/v2
//
// This file exports everything that is library-independent: capability
// probing, sandbox argument construction, environment composition,
// mountpoint creation, the bind-mount builder, and the bwrap exec
// wrapper. Both backend files import these names and supply only the
// pieces that talk to the FUSE library directly.

package purevfs

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

var linuxSystemReadOnlyRoots = []string{
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib64",
	"/etc",
}

func isWindowsPlatform() bool {
	return false
}

func platformExecutionCapabilities() ExecutionCapabilities {
	if strictExecutionProbe() != nil {
		return ExecutionCapabilities{}
	}
	return StrictBrokerCapabilities()
}

func strictExecutionProbe() error {
	switch {
	case !commandAvailable("bwrap"):
		return fmt.Errorf("%w: bwrap not installed", ErrStrictExecutionUnavailable)
	case !commandAvailable("fusermount3"):
		return fmt.Errorf("%w: fusermount3 not installed", ErrStrictExecutionUnavailable)
	case !linuxSharedMemoryRootAvailable():
		return fmt.Errorf("%w: /dev/shm is not tmpfs", ErrStrictExecutionUnavailable)
	default:
		return nil
	}
}

func runMountedExecution(ctx context.Context, req BrokerRunRequest, mountRoot string, guard *brokerMemoryGuard) (*BrokerRunResult, error) {
	cmd, err := newLinuxSandboxCommand(ctx, req, mountRoot)
	if err != nil {
		return nil, err
	}
	stdout := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	stderr := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	exitCode, exitErr := captureExitCode(err)
	if exitErr != nil {
		return nil, exitErr
	}
	return &BrokerRunResult{
		ExitCode:        exitCode,
		Stdout:          stdout.Bytes(),
		Stderr:          stderr.Bytes(),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
	}, nil
}

func newLinuxMountpoint() (string, error) {
	if err := strictExecutionProbe(); err != nil {
		return "", fmt.Errorf("purevfs: create linux mountpoint: %w", err)
	}
	mountpoint, err := os.MkdirTemp("/dev/shm", "sylk-execfs-*")
	if err != nil {
		return "", fmt.Errorf("purevfs: create linux mountpoint: %w", err)
	}
	return mountpoint, nil
}

func linuxSharedMemoryRootAvailable() bool {
	var stat unix.Statfs_t
	if err := unix.Statfs("/dev/shm", &stat); err != nil {
		return false
	}
	return stat.Type == unix.TMPFS_MAGIC
}

func inodeForPath(name string) uint64 {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(normalizeExecPath(name)))
	return sum.Sum64()
}

func childExecPath(parent, name string) string {
	if normalizeExecPath(parent) == "/" {
		return normalizeExecPath("/" + name)
	}
	return normalizeExecPath(path.Join(parent, name))
}

func newLinuxSandboxCommand(ctx context.Context, req BrokerRunRequest, mountRoot string) (*exec.Cmd, error) {
	args := linuxSandboxArgs(req, mountRoot)
	cmd := exec.CommandContext(ctx, "bwrap", args...)
	cmd.Dir = mountRoot
	cmd.Env = linuxExecutionEnv(req.Plan, req.Env)
	return cmd, nil
}

func linuxSandboxArgs(req BrokerRunRequest, mountRoot string) []string {
	args := []string{
		"--unshare-all",
		"--share-net",
		"--die-with-parent",
		"--proc", "/proc",
		"--dev", "/dev",
	}
	args = append(args, linuxProjectionBinds(req.Plan, mountRoot)...)
	args = append(args, linuxReadOnlyHostBinds(req.Argv)...)
	if dir := strings.TrimSpace(req.Plan.WorkingDir); dir != "" {
		args = append(args, "--chdir", dir)
	}
	args = append(args, "--")
	return append(args, req.Argv...)
}

func linuxProjectionBinds(plan ExecutionPlan, mountRoot string) []string {
	builder := newLinuxBindBuilder()
	for _, mount := range plan.Mounts {
		if !linuxProjectedMountAvailable(mount) {
			continue
		}
		src := mountedExecPath(mountRoot, mount.VirtualPath)
		dst := normalizeExecPath(mount.VirtualPath)
		builder.add(dst, src, linuxMountReadOnly(mount))
	}
	return builder.args()
}

func linuxReadOnlyHostBinds(argv []string) []string {
	builder := newLinuxBindBuilder()
	for _, root := range linuxSystemReadOnlyRoots {
		builder.add(root, root, true)
	}
	for _, dir := range linuxExtraReadOnlyDirs(argv) {
		builder.add(dir, dir, true)
	}
	return builder.args()
}

func linuxExtraReadOnlyDirs(argv []string) []string {
	seen := make(map[string]struct{})
	var dirs []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			return
		}
		if linuxCoveredBySystemRoot(dir) {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		add(dir)
	}
	if len(argv) > 0 && filepath.IsAbs(argv[0]) {
		add(filepath.Dir(argv[0]))
	}
	slices.Sort(dirs)
	return dirs
}

func linuxCoveredBySystemRoot(name string) bool {
	for _, root := range linuxSystemReadOnlyRoots {
		if name == root || strings.HasPrefix(name, root+"/") {
			return true
		}
	}
	return false
}

func linuxMountReadOnly(mount MountSpec) bool {
	return mount.Access == MountReadOnly
}

func linuxExecutionEnv(plan ExecutionPlan, extra map[string]string) []string {
	env := baseExecutionEnv()
	mergeLinuxExecutionEnv(env, plan.Env)
	mergeLinuxExecutionEnv(env, extra)
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func mergeLinuxExecutionEnv(dst map[string]string, values map[string]string) {
	for key, value := range values {
		if isPathListKey(key) {
			dst[key] = mergeExecutionPathList(dst[key], value)
			continue
		}
		dst[key] = value
	}
}

func linuxProjectedMountAvailable(mount MountSpec) bool {
	if mount.Kind != MountToolchain {
		return true
	}
	path := strings.TrimSpace(mount.BackingPath)
	if path == "" {
		return false
	}
	if filepath.IsAbs(path) {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(path)
	return err == nil
}

type linuxBindBuilder struct {
	dirs  map[string]struct{}
	binds map[string]linuxBind
}

type linuxBind struct {
	src      string
	readOnly bool
}

func newLinuxBindBuilder() *linuxBindBuilder {
	return &linuxBindBuilder{
		dirs:  make(map[string]struct{}),
		binds: make(map[string]linuxBind),
	}
}

func (b *linuxBindBuilder) add(dst, src string, readOnly bool) {
	dst = normalizeExecPath(dst)
	if dst == "/" {
		return
	}
	for _, dir := range linuxParentDirs(dst) {
		b.dirs[dir] = struct{}{}
	}
	b.binds[dst] = linuxBind{src: src, readOnly: readOnly}
}

func (b *linuxBindBuilder) args() []string {
	var out []string
	dirs := make([]string, 0, len(b.dirs))
	for dir := range b.dirs {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		out = append(out, "--dir", dir)
	}
	dsts := make([]string, 0, len(b.binds))
	for dst := range b.binds {
		dsts = append(dsts, dst)
	}
	slices.Sort(dsts)
	for _, dst := range dsts {
		bind := b.binds[dst]
		flag := "--bind"
		if bind.readOnly {
			flag = "--ro-bind"
		}
		out = append(out, flag, bind.src, dst)
	}
	return out
}

func linuxParentDirs(name string) []string {
	current := normalizeExecPath(path.Dir(name))
	if current == "/" {
		return nil
	}
	var dirs []string
	for current != "/" {
		dirs = append(dirs, current)
		current = normalizeExecPath(path.Dir(current))
	}
	slices.Reverse(dirs)
	return dirs
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
