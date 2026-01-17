//go:build windows

package security

import (
	"os/exec"
)

type WindowsOSWrapper struct {
	available bool
}

func newPlatformOSWrapper() OSWrapper {
	return &WindowsOSWrapper{
		available: false,
	}
}

func (w *WindowsOSWrapper) Available() bool {
	return w.available
}

func (w *WindowsOSWrapper) Platform() string {
	return "windows-job-objects"
}

func (w *WindowsOSWrapper) WrapCommand(cmd *exec.Cmd, workDir string, allowedPaths []string) (*exec.Cmd, error) {
	return nil, ErrSandboxUnavailable
}
