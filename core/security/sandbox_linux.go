//go:build linux

package security

import (
	"os/exec"
)

type LinuxOSWrapper struct {
	bwrapPath string
	available bool
}

func newPlatformOSWrapper() OSWrapper {
	w := &LinuxOSWrapper{}
	w.checkAvailability()
	return w
}

func (w *LinuxOSWrapper) checkAvailability() {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		w.available = false
		return
	}
	w.bwrapPath = path
	w.available = true
}

func (w *LinuxOSWrapper) Available() bool {
	return w.available
}

func (w *LinuxOSWrapper) Platform() string {
	return "linux-bubblewrap"
}

func (w *LinuxOSWrapper) WrapCommand(cmd *exec.Cmd, workDir string, allowedPaths []string) (*exec.Cmd, error) {
	if !w.available {
		return nil, ErrSandboxUnavailable
	}

	args := w.buildBwrapArgs(cmd, workDir, allowedPaths)

	wrapped := exec.Command(w.bwrapPath, args...)
	wrapped.Dir = cmd.Dir
	wrapped.Env = cmd.Env
	wrapped.Stdin = cmd.Stdin
	wrapped.Stdout = cmd.Stdout
	wrapped.Stderr = cmd.Stderr

	return wrapped, nil
}

func (w *LinuxOSWrapper) buildBwrapArgs(cmd *exec.Cmd, workDir string, allowedPaths []string) []string {
	args := []string{
		"--unshare-all",
		"--share-net",
		"--die-with-parent",
	}

	args = append(args, "--ro-bind", "/usr", "/usr")
	args = append(args, "--ro-bind", "/lib", "/lib")
	args = append(args, "--ro-bind", "/lib64", "/lib64")
	args = append(args, "--ro-bind", "/bin", "/bin")
	args = append(args, "--ro-bind", "/etc/resolv.conf", "/etc/resolv.conf")
	args = append(args, "--ro-bind", "/etc/ssl", "/etc/ssl")
	args = append(args, "--proc", "/proc")
	args = append(args, "--dev", "/dev")
	args = append(args, "--tmpfs", "/tmp")

	if workDir != "" {
		args = append(args, "--bind", workDir, workDir)
		args = append(args, "--chdir", workDir)
	}

	for _, path := range allowedPaths {
		args = append(args, "--bind", path, path)
	}

	args = append(args, "--")
	args = append(args, cmd.Path)
	args = append(args, cmd.Args[1:]...)

	return args
}
