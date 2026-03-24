//go:build linux

package purevfs_test

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/purevfs"
)

type memoryExecutionFS struct {
	root  string
	files map[string][]byte
	dirs  map[string]struct{}
}

type memoryFileInfo struct {
	name string
	size int64
	mode fs.FileMode
}

type memoryDirEntry struct {
	info fs.FileInfo
}

func TestDefaultExecutionBrokerLinuxRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	broker := purevfs.DefaultExecutionBroker()
	caps, err := broker.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.ProcessBroker {
		t.Skip("strict broker unavailable on this host")
	}

	workspace := newMemoryExecutionFS("/workspace", map[string]string{
		"/workspace/hello.txt": "hello broker\n",
	})
	planner := purevfs.NewExecutionPlanner(nil, purevfs.StrictBrokerCapabilities())
	plan, err := planner.Plan(purevfs.ExecutionRequest{
		Mode:          purevfs.ExecutionModeStrictNoDisk,
		Intent:        purevfs.ExecutionIntentCommand,
		Language:      "generic",
		WorkspaceRoot: "/workspace",
		WorkingDir:    "/workspace",
		Overlay:       true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	result, err := broker.Run(ctx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      purevfs.ShellCommandArgv("cat /workspace/hello.txt && printf 'created by broker' > /workspace/generated.txt"),
		Workspace: workspace,
	})
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") || strings.Contains(strings.ToLower(err.Error()), "fuse") {
			t.Skipf("strict broker unavailable in test sandbox: %v", err)
		}
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, stderr = %q", result.ExitCode, string(result.Stderr))
	}
	if got := string(result.Stdout); !strings.Contains(got, "hello broker") {
		t.Fatalf("stdout = %q, want mounted workspace content", got)
	}
	content, err := workspace.ReadFile(ctx, "/workspace/generated.txt")
	if err != nil {
		t.Fatalf("ReadFile generated.txt: %v", err)
	}
	if got := string(content); got != "created by broker" {
		t.Fatalf("generated.txt = %q, want %q", got, "created by broker")
	}
}

func TestDefaultExecutionBrokerLinuxRunsHostPathExecutableWithProjectedRuntimePath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	broker := purevfs.DefaultExecutionBroker()
	caps, err := broker.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.ProcessBroker {
		t.Skip("strict broker unavailable on this host")
	}

	hostBin := t.TempDir()
	scriptPath := filepath.Join(hostBin, "demo-tool")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ncat /workspace/hello.txt\nprintf 'created by host tool' > /workspace/generated.txt\n"), 0o755); err != nil {
		t.Fatalf("WriteFile demo-tool: %v", err)
	}
	t.Setenv("PATH", hostBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspace := newMemoryExecutionFS("/workspace", map[string]string{
		"/workspace/hello.txt": "hello broker\n",
	})
	planner := purevfs.NewExecutionPlanner(nil, purevfs.StrictBrokerCapabilities())
	plan, err := planner.Plan(purevfs.ExecutionRequest{
		Mode:          purevfs.ExecutionModeStrictNoDisk,
		Intent:        purevfs.ExecutionIntentRun,
		Language:      "python",
		WorkspaceRoot: "/workspace",
		WorkingDir:    "/workspace",
		Overlay:       true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	result, err := broker.Run(ctx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      []string{"demo-tool"},
		Workspace: workspace,
	})
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") || strings.Contains(strings.ToLower(err.Error()), "fuse") {
			t.Skipf("strict broker unavailable in test sandbox: %v", err)
		}
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, stderr = %q", result.ExitCode, string(result.Stderr))
	}
	if got := string(result.Stdout); !strings.Contains(got, "hello broker") {
		t.Fatalf("stdout = %q, want mounted workspace content", got)
	}
	content, err := workspace.ReadFile(ctx, "/workspace/generated.txt")
	if err != nil {
		t.Fatalf("ReadFile generated.txt: %v", err)
	}
	if got := string(content); got != "created by host tool" {
		t.Fatalf("generated.txt = %q, want %q", got, "created by host tool")
	}
}

func TestDefaultExecutionBrokerLinuxHonorsWorkingDirInsideProjectedWorkspace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	broker := purevfs.DefaultExecutionBroker()
	caps, err := broker.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.ProcessBroker {
		t.Skip("strict broker unavailable on this host")
	}

	workspace := newMemoryExecutionFS("/workspace/project", map[string]string{
		"/workspace/project/subdir/hello.txt": "hello from subdir\n",
	})
	planner := purevfs.NewExecutionPlanner(nil, purevfs.StrictBrokerCapabilities())
	plan, err := planner.Plan(purevfs.ExecutionRequest{
		Mode:          purevfs.ExecutionModeStrictNoDisk,
		Intent:        purevfs.ExecutionIntentCommand,
		Language:      "generic",
		WorkspaceRoot: "/workspace/project",
		WorkingDir:    "subdir",
		Overlay:       true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	result, err := broker.Run(ctx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      purevfs.ShellCommandArgv("pwd && cat hello.txt"),
		Workspace: workspace,
	})
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") || strings.Contains(strings.ToLower(err.Error()), "fuse") {
			t.Skipf("strict broker unavailable in test sandbox: %v", err)
		}
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, stderr = %q", result.ExitCode, string(result.Stderr))
	}
	stdout := string(result.Stdout)
	if !strings.Contains(stdout, "/workspace/project/subdir") {
		t.Fatalf("stdout = %q, want working directory path", stdout)
	}
	if !strings.Contains(stdout, "hello from subdir") {
		t.Fatalf("stdout = %q, want file content from working_dir", stdout)
	}
}

func newMemoryExecutionFS(root string, files map[string]string) *memoryExecutionFS {
	fs := &memoryExecutionFS{
		root:  root,
		files: make(map[string][]byte),
		dirs:  map[string]struct{}{path.Clean(root): {}, "/": {}},
	}
	for name, content := range files {
		_ = fs.WriteFile(context.Background(), name, []byte(content))
	}
	return fs
}

func (m *memoryExecutionFS) ReadFile(_ context.Context, name string) ([]byte, error) {
	data, ok := m.files[path.Clean(name)]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return slices.Clone(data), nil
}

func (m *memoryExecutionFS) MkdirAll(_ context.Context, name string) error {
	for _, dir := range parentDirs(name) {
		m.dirs[dir] = struct{}{}
	}
	return nil
}

func (m *memoryExecutionFS) WriteFile(_ context.Context, name string, content []byte) error {
	cleaned := path.Clean(name)
	for _, dir := range parentDirs(cleaned) {
		m.dirs[dir] = struct{}{}
	}
	m.files[cleaned] = slices.Clone(content)
	return nil
}

func (m *memoryExecutionFS) DeleteFile(_ context.Context, name string) error {
	cleaned := path.Clean(name)
	if _, ok := m.files[cleaned]; ok {
		delete(m.files, cleaned)
		return nil
	}
	if _, ok := m.dirs[cleaned]; ok {
		delete(m.dirs, cleaned)
		return nil
	}
	return fs.ErrNotExist
}

func (m *memoryExecutionFS) Exists(_ context.Context, name string) (bool, error) {
	cleaned := path.Clean(name)
	_, fileOK := m.files[cleaned]
	_, dirOK := m.dirs[cleaned]
	return fileOK || dirOK, nil
}

func (m *memoryExecutionFS) ListDir(_ context.Context, dir string) ([]fs.DirEntry, error) {
	cleaned := path.Clean(dir)
	if _, ok := m.dirs[cleaned]; !ok {
		return nil, fs.ErrNotExist
	}
	entries := make(map[string]memoryDirEntry)
	for name, data := range m.files {
		addDirEntry(entries, cleaned, name, int64(len(data)), 0o644)
	}
	for name := range m.dirs {
		addDirEntry(entries, cleaned, name, 0, fs.ModeDir|0o755)
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	slices.SortFunc(out, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return out, nil
}

func (m *memoryExecutionFS) Stat(_ context.Context, name string) (fs.FileInfo, error) {
	cleaned := path.Clean(name)
	if data, ok := m.files[cleaned]; ok {
		return memoryFileInfo{name: path.Base(cleaned), size: int64(len(data)), mode: 0o644}, nil
	}
	if _, ok := m.dirs[cleaned]; ok {
		return memoryFileInfo{name: path.Base(cleaned), mode: fs.ModeDir | 0o755}, nil
	}
	return nil, fs.ErrNotExist
}

func (m *memoryExecutionFS) WorkingDir() string {
	return m.root
}

func (m *memoryExecutionFS) IsReadOnly() bool {
	return false
}

func addDirEntry(entries map[string]memoryDirEntry, parent, name string, size int64, mode fs.FileMode) {
	if name == parent || !strings.HasPrefix(name, parent+"/") {
		return
	}
	rel := strings.TrimPrefix(name, parent+"/")
	part := rel
	entryMode := mode
	if idx := strings.IndexRune(rel, '/'); idx >= 0 {
		part = rel[:idx]
		entryMode = fs.ModeDir | 0o755
		size = 0
	}
	if part == "" {
		return
	}
	entries[part] = memoryDirEntry{
		info: memoryFileInfo{name: part, size: size, mode: entryMode},
	}
}

func parentDirs(name string) []string {
	cleaned := path.Clean(name)
	var dirs []string
	for current := path.Dir(cleaned); current != "."; current = path.Dir(current) {
		dirs = append(dirs, current)
		if current == "/" {
			break
		}
	}
	slices.Reverse(dirs)
	return dirs
}

func (i memoryFileInfo) Name() string       { return i.name }
func (i memoryFileInfo) Size() int64        { return i.size }
func (i memoryFileInfo) Mode() fs.FileMode  { return i.mode }
func (i memoryFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i memoryFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i memoryFileInfo) Sys() any           { return nil }

func (e memoryDirEntry) Name() string               { return e.info.Name() }
func (e memoryDirEntry) IsDir() bool                { return e.info.IsDir() }
func (e memoryDirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e memoryDirEntry) Info() (fs.FileInfo, error) { return e.info, nil }
