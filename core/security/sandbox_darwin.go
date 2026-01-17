//go:build darwin

package security

import (
	"os"
	"os/exec"
	"path/filepath"
)

type DarwinOSWrapper struct {
	sandboxExecPath string
	available       bool
}

func newPlatformOSWrapper() OSWrapper {
	w := &DarwinOSWrapper{}
	w.checkAvailability()
	return w
}

func (w *DarwinOSWrapper) checkAvailability() {
	path := "/usr/bin/sandbox-exec"
	if _, err := os.Stat(path); err == nil {
		w.sandboxExecPath = path
		w.available = true
	}
}

func (w *DarwinOSWrapper) Available() bool {
	return w.available
}

func (w *DarwinOSWrapper) Platform() string {
	return "darwin-seatbelt"
}

func (w *DarwinOSWrapper) WrapCommand(cmd *exec.Cmd, workDir string, allowedPaths []string) (*exec.Cmd, error) {
	if !w.available {
		return nil, ErrSandboxUnavailable
	}

	profile := w.generateProfile(workDir, allowedPaths)

	profilePath := filepath.Join(os.TempDir(), "sylk-sandbox.sb")
	if err := os.WriteFile(profilePath, []byte(profile), 0600); err != nil {
		return nil, err
	}

	args := []string{"-f", profilePath, cmd.Path}
	args = append(args, cmd.Args[1:]...)

	wrapped := exec.Command(w.sandboxExecPath, args...)
	wrapped.Dir = cmd.Dir
	wrapped.Env = cmd.Env
	wrapped.Stdin = cmd.Stdin
	wrapped.Stdout = cmd.Stdout
	wrapped.Stderr = cmd.Stderr

	return wrapped, nil
}

func (w *DarwinOSWrapper) generateProfile(workDir string, allowedPaths []string) string {
	profile := "(version 1)\n"
	profile += "(deny default)\n"
	profile += "(allow process-fork)\n"
	profile += "(allow process-exec)\n"
	profile += "(allow sysctl-read)\n"
	profile += "(allow mach-lookup)\n"
	profile += "(allow signal)\n"

	profile += "(allow file-read* (subpath \"/usr\"))\n"
	profile += "(allow file-read* (subpath \"/bin\"))\n"
	profile += "(allow file-read* (subpath \"/sbin\"))\n"
	profile += "(allow file-read* (subpath \"/Library\"))\n"
	profile += "(allow file-read* (subpath \"/System\"))\n"
	profile += "(allow file-read* (literal \"/dev/null\"))\n"
	profile += "(allow file-read* (literal \"/dev/urandom\"))\n"
	profile += "(allow file-read* (literal \"/private/etc/resolv.conf\"))\n"
	profile += "(allow file-read* (subpath \"/private/etc/ssl\"))\n"
	profile += "(allow file-read-data (subpath \"/dev\"))\n"
	profile += "(allow file-write-data (subpath \"/dev/null\"))\n"

	if workDir != "" {
		profile += "(allow file-read* (subpath \"" + workDir + "\"))\n"
		profile += "(allow file-write* (subpath \"" + workDir + "\"))\n"
	}

	for _, path := range allowedPaths {
		profile += "(allow file-read* (subpath \"" + path + "\"))\n"
		profile += "(allow file-write* (subpath \"" + path + "\"))\n"
	}

	profile += "(allow network-outbound)\n"
	profile += "(allow network-inbound)\n"

	return profile
}
