//go:build darwin && cgo

package purevfs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var darwinSystemReadOnlyRoots = []string{
	"/usr",
	"/bin",
	"/sbin",
	"/Library",
	"/System",
	"/private/etc",
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
	case !commandAvailable("sandbox-exec"):
		return fmt.Errorf("%w: sandbox-exec not installed", ErrStrictExecutionUnavailable)
	case !darwinProjectionAvailable():
		return fmt.Errorf("%w: macFUSE is not installed", ErrStrictExecutionUnavailable)
	case !darwinVolumesRootAvailable():
		return fmt.Errorf("%w: /Volumes unavailable for projected mounts", ErrStrictExecutionUnavailable)
	default:
		return nil
	}
}

func darwinProjectionAvailable() bool {
	if _, err := os.Stat("/Library/Filesystems/macfuse.fs"); err == nil {
		return true
	}
	_, err := os.Stat("/Library/Filesystems/osxfuse.fs")
	return err == nil
}

func mountExecutionRoot(_ context.Context, _ BrokerRunRequest, root *projectedRoot) (mountedExecutionRoot, error) {
	mountpoint, opts, err := newDarwinMountpoint()
	if err != nil {
		return nil, err
	}
	return mountCGOFuseExecutionRoot(mountpoint, opts, root)
}

func runMountedExecution(ctx context.Context, req BrokerRunRequest, mountRoot string, guard *brokerMemoryGuard) (*BrokerRunResult, error) {
	argv, err := hostProjectedExecutionArgv(req.Plan, req.Env, mountRoot, req.Argv)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "sandbox-exec", darwinSandboxArgs(req, mountRoot, argv)...)
	cmd.Dir = translatedWorkingDir(req.Plan, mountRoot)
	cmd.Env = translatedExecutionEnv(req.Plan, req.Env, mountRoot)
	stdout := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	stderr := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
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

func darwinSandboxArgs(req BrokerRunRequest, mountRoot string, argv []string) []string {
	args := []string{"-p", darwinSandboxProfile(req, mountRoot)}
	return append(args, argv...)
}

func darwinSandboxProfile(req BrokerRunRequest, mountRoot string) string {
	var builder strings.Builder
	builder.WriteString("(version 1)\n")
	builder.WriteString("(deny default)\n")
	builder.WriteString("(allow process*)\n")
	builder.WriteString("(allow sysctl*)\n")
	builder.WriteString("(allow file-read-data (literal \"/dev/null\"))\n")
	builder.WriteString("(allow file-read-data (literal \"/dev/urandom\"))\n")
	builder.WriteString("(allow file-write-data (literal \"/dev/null\"))\n")
	for _, root := range darwinSystemReadOnlyRoots {
		writeDarwinRule(&builder, "file-read*", root)
	}
	for _, dir := range darwinExtraReadOnlyDirs(req.Argv) {
		writeDarwinRule(&builder, "file-read*", dir)
	}
	writeDarwinRule(&builder, "file-read*", mountRoot)
	writeDarwinRule(&builder, "file-write*", mountRoot)
	builder.WriteString("(allow network-outbound)\n")
	builder.WriteString("(allow network-inbound)\n")
	return builder.String()
}

func writeDarwinRule(builder *strings.Builder, action, dir string) {
	builder.WriteString("(allow ")
	builder.WriteString(action)
	builder.WriteString(" (subpath \"")
	builder.WriteString(dir)
	builder.WriteString("\"))\n")
}

func darwinExtraReadOnlyDirs(argv []string) []string {
	seen := make(map[string]struct{})
	var dirs []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
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
	return dirs
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func newDarwinMountpoint() (string, []string, error) {
	if !darwinVolumesRootAvailable() {
		return "", nil, ErrStrictExecutionUnavailable
	}
	name := fmt.Sprintf("sylk-execfs-%d", time.Now().UTC().UnixNano())
	return filepath.Join("/Volumes", name), []string{
		"-o", "volname=" + name,
	}, nil
}

func darwinVolumesRootAvailable() bool {
	info, err := os.Stat("/Volumes")
	if err != nil {
		return false
	}
	return info.IsDir()
}
