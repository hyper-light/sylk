//go:build linux

package purevfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxExecutionEnvMergesProjectedToolchainPathWithHostPath(t *testing.T) {
	t.Setenv("PATH", "/tmp/host-bin:/usr/bin")

	env := linuxExecutionEnv(ExecutionPlan{
		Env: map[string]string{
			"PATH": executionToolchainRoot,
		},
	}, nil)

	if got := envValue(env, "PATH"); got != executionToolchainRoot+string(os.PathListSeparator)+"/tmp/host-bin:/usr/bin" {
		t.Fatalf("PATH = %q", got)
	}
}

func TestLinuxProjectionBindsIncludesAvailableToolchainMounts(t *testing.T) {
	mountRoot := t.TempDir()
	toolPath := filepath.Join(t.TempDir(), "demo-tool")
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	args := linuxProjectionBinds(ExecutionPlan{
		Mounts: []MountSpec{
			{
				Kind:        MountWorkspace,
				Access:      MountReadOnly,
				VirtualPath: "/workspace",
			},
			{
				Kind:        MountToolchain,
				Access:      MountReadOnly,
				VirtualPath: executionToolchainPath("demo-tool"),
				BackingPath: toolPath,
			},
			{
				Kind:        MountToolchain,
				Access:      MountReadOnly,
				VirtualPath: executionToolchainPath("missing-tool"),
				BackingPath: filepath.Join(t.TempDir(), "missing-tool"),
			},
		},
	}, mountRoot)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, normalizeExecPath(executionToolchainPath("demo-tool"))) {
		t.Fatalf("toolchain bind missing from args: %v", args)
	}
	if strings.Contains(joined, normalizeExecPath(executionToolchainPath("missing-tool"))) {
		t.Fatalf("missing toolchain unexpectedly bound: %v", args)
	}
}

func envValue(entries []string, key string) string {
	prefix := key + "="
	for _, entry := range entries {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
