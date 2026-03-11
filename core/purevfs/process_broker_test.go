package purevfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostProjectedExecutionArgvResolvesToolchainBinary(t *testing.T) {
	mountRoot := t.TempDir()
	binaryPath := mountedExecPath(mountRoot, executionToolchainRoot+"/go")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	plan := ExecutionPlan{
		Mounts: []MountSpec{
			{Kind: MountToolchain, VirtualPath: executionToolchainRoot + "/go"},
		},
		Env: map[string]string{"PATH": executionToolchainRoot},
	}
	argv, err := hostProjectedExecutionArgv(plan, nil, mountRoot, []string{"go", "test", "./..."})
	if err != nil {
		t.Fatalf("hostProjectedExecutionArgv: %v", err)
	}
	if argv[0] != binaryPath {
		t.Fatalf("argv[0] = %q, want %q", argv[0], binaryPath)
	}
}

func TestHostProjectedExecutionArgvTranslatesShellCommandPaths(t *testing.T) {
	mountRoot := t.TempDir()
	plan := ExecutionPlan{
		Mounts: []MountSpec{
			{Kind: MountWorkspace, VirtualPath: "/workspace"},
			{Kind: MountTemp, VirtualPath: "/tmp"},
		},
	}
	argv, err := hostProjectedExecutionArgv(plan, nil, mountRoot, []string{
		"/bin/sh",
		"-c",
		"cat /workspace/hello.txt && printf x > /tmp/out.txt",
	})
	if err != nil {
		t.Fatalf("hostProjectedExecutionArgv: %v", err)
	}
	workspacePath := mountedExecPath(mountRoot, "/workspace")
	tempPath := mountedExecPath(mountRoot, "/tmp")
	if !strings.Contains(argv[2], workspacePath) {
		t.Fatalf("shell command %q missing translated workspace path %q", argv[2], workspacePath)
	}
	if !strings.Contains(argv[2], tempPath) {
		t.Fatalf("shell command %q missing translated temp path %q", argv[2], tempPath)
	}
}
