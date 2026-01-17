//go:build darwin

package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	var builder strings.Builder
	builder.Grow(512)

	builder.WriteString("(version 1)\n")
	builder.WriteString("(deny default)\n")
	builder.WriteString("(allow process*)\n")
	builder.WriteString("(allow sysctl*)\n")
	builder.WriteString("(allow file-read* (subpath \"/usr\"))\n")
	builder.WriteString("(allow file-read* (subpath \"/bin\"))\n")
	builder.WriteString("(allow file-read* (subpath \"/sbin\"))\n")
	builder.WriteString("(allow file-read* (subpath \"/Library\"))\n")
	builder.WriteString("(allow file-read* (subpath \"/System\"))\n")
	builder.WriteString("(allow file-read* (literal \"/dev/null\"))\n")
	builder.WriteString("(allow file-read* (literal \"/dev/urandom\"))\n")
	builder.WriteString("(allow file-read* (literal \"/private/etc/resolv.conf\"))\n")
	builder.WriteString("(allow file-read* (subpath \"/private/etc/ssl\"))\n")
	builder.WriteString("(allow file-read-data (subpath \"/dev\"))\n")
	builder.WriteString("(allow file-write-data (subpath \"/dev/null\"))\n")

	if workDir != "" {
		builder.WriteString("(allow file-read* (subpath \"")
		builder.WriteString(workDir)
		builder.WriteString("\"))\n")
		builder.WriteString("(allow file-write* (subpath \"")
		builder.WriteString(workDir)
		builder.WriteString("\"))\n")
	}

	for _, path := range allowedPaths {
		builder.WriteString("(allow file-read* (subpath \"")
		builder.WriteString(path)
		builder.WriteString("\"))\n")
		builder.WriteString("(allow file-write* (subpath \"")
		builder.WriteString(path)
		builder.WriteString("\"))\n")
	}

	builder.WriteString("(allow network-outbound)\n")
	builder.WriteString("(allow network-inbound)\n")

	return builder.String()

}
