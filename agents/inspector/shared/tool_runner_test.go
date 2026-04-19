package shared

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/versioning"
)

type allowAllApprovalGate struct{}

func (allowAllApprovalGate) Authorize(_ context.Context, _ commandapproval.Request) (commandapproval.Evaluation, error) {
	return commandapproval.Evaluation{Decision: commandapproval.DecisionAllow}, nil
}

// TestToolRunnerExecRefusesWhenBrokerMissing locks in the no-disk invariant:
// a ToolRunner constructed without a Broker must fail loudly rather than
// silently running commands against host disk. This replaces the former
// execDirect() escape hatch with a compile-time guarantee.
func TestToolRunnerExecRefusesWhenBrokerMissing(t *testing.T) {
	root := t.TempDir()
	runner := NewToolRunner(ToolRunnerConfig{
		WorkingDir: root,
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Broker intentionally omitted.
	})
	ctx := commandapproval.WithGate(context.Background(), allowAllApprovalGate{})
	_, err := runner.Exec(ctx, "echo", "leak")
	if err == nil {
		t.Fatal("expected Exec to refuse without a broker, got nil error")
	}
	if !errors.Is(err, purevfs.ErrStrictExecutionUnavailable) {
		t.Fatalf("want ErrStrictExecutionUnavailable, got %v", err)
	}
}

func TestToolRunnerWorkingDirUsesFileAccessWorkingDir(t *testing.T) {
	root := t.TempDir()
	runner := NewToolRunner(ToolRunnerConfig{
		WorkingDir: ".",
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		FileAccess: func() versioning.FileAccess {
			return versioning.NewDiskFileAccess(root, true)
		},
	})

	if got := runner.WorkingDir(); got != root {
		t.Fatalf("WorkingDir() = %q, want %q", got, root)
	}
}

func TestNormalizeGoPackageExecutionTargetsConvertsWorkspaceAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	runner := NewToolRunner(ToolRunnerConfig{
		WorkingDir: root,
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	targets := normalizeGoPackageExecutionTargets(runner, []string{
		filepath.Join(root, "pkg") + string(filepath.Separator) + "...",
		filepath.Join(root, "cmd", "tool"),
	})

	want := []string{"./pkg/...", "./cmd/tool"}
	if len(targets) != len(want) {
		t.Fatalf("len(targets) = %d, want %d (%v)", len(targets), len(want), targets)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Fatalf("targets[%d] = %q, want %q (all=%v)", i, targets[i], want[i], targets)
		}
	}
}

func TestRunDeadlockDetectorUsesFileAccessForVFSOnlyFiles(t *testing.T) {
	root := t.TempDir()
	fa := &stubAnalysisFileAccess{
		root: root,
		files: map[string]string{
			filepath.Join(root, "locks.go"): `package locks

import "sync"

type S struct {
	mu  sync.Mutex
	mu2 sync.Mutex
}

func (s *S) LockBoth() {
	s.mu.Lock()
	s.mu2.Lock()
	s.mu2.Unlock()
	s.mu.Unlock()
}
`,
		},
	}
	runner := NewToolRunner(ToolRunnerConfig{
		WorkingDir: ".",
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		FileAccess: func() versioning.FileAccess { return fa },
	})

	issues := RunDeadlockDetector(context.Background(), runner, []string{"."})
	if len(issues) == 0 {
		t.Fatalf("expected deadlock issue for VFS-only file, got none")
	}
	if issues[0].File != "locks.go" {
		t.Fatalf("issue file = %q, want %q", issues[0].File, "locks.go")
	}
	if issues[0].RuleID != "deadlock" {
		t.Fatalf("rule_id = %q, want %q", issues[0].RuleID, "deadlock")
	}
}

type stubAnalysisFileAccess struct {
	root  string
	files map[string]string
}

func (s *stubAnalysisFileAccess) ReadFile(_ context.Context, path string) ([]byte, error) {
	content, ok := s.files[s.resolve(path)]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(content), nil
}

func (s *stubAnalysisFileAccess) MkdirAll(context.Context, string) error {
	return fmt.Errorf("unsupported")
}
func (s *stubAnalysisFileAccess) WriteFile(context.Context, string, []byte) error {
	return fmt.Errorf("unsupported")
}
func (s *stubAnalysisFileAccess) EditFile(context.Context, string, []versioning.FileEdit) error {
	return fmt.Errorf("unsupported")
}
func (s *stubAnalysisFileAccess) DeleteFile(context.Context, string) error {
	return fmt.Errorf("unsupported")
}

func (s *stubAnalysisFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	_, err := s.Stat(ctx, path)
	if err == nil {
		return true, nil
	}
	if err == fs.ErrNotExist {
		return false, nil
	}
	return false, err
}

func (s *stubAnalysisFileAccess) ListDir(_ context.Context, dir string) ([]fs.DirEntry, error) {
	resolved := s.resolve(dir)
	dirs := s.directories()
	if _, ok := dirs[resolved]; !ok {
		return nil, fs.ErrNotExist
	}
	children := make(map[string]bool)
	for path := range dirs {
		if path == resolved {
			continue
		}
		parent := filepath.Dir(path)
		if parent == resolved {
			children[filepath.Base(path)] = true
		}
	}
	for path := range s.files {
		if filepath.Dir(path) == resolved {
			children[filepath.Base(path)] = false
		}
	}
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, stubDirEntry{
			info: stubFileInfo{
				name:  name,
				size:  int64(len(s.files[filepath.Join(resolved, name)])),
				mode:  fileMode(children[name]),
				isDir: children[name],
			},
		})
	}
	return entries, nil
}

func (s *stubAnalysisFileAccess) Glob(context.Context, string, string, []string) ([]string, error) {
	return nil, nil
}

func (s *stubAnalysisFileAccess) Grep(context.Context, string, string, string, int, int) ([]versioning.GrepMatch, error) {
	return nil, nil
}

func (s *stubAnalysisFileAccess) Stat(_ context.Context, path string) (fs.FileInfo, error) {
	resolved := s.resolve(path)
	if content, ok := s.files[resolved]; ok {
		return stubFileInfo{
			name: filepath.Base(resolved),
			size: int64(len(content)),
			mode: 0o644,
		}, nil
	}
	if _, ok := s.directories()[resolved]; ok {
		return stubFileInfo{
			name:  filepath.Base(resolved),
			mode:  fs.ModeDir | 0o755,
			isDir: true,
		}, nil
	}
	return nil, fs.ErrNotExist
}

func (s *stubAnalysisFileAccess) WorkingDir() string { return s.root }
func (s *stubAnalysisFileAccess) IsReadOnly() bool   { return true }

func (s *stubAnalysisFileAccess) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	clean := filepath.Clean(path)
	if clean == "." {
		return filepath.Clean(s.root)
	}
	return filepath.Join(s.root, clean)
}

func (s *stubAnalysisFileAccess) directories() map[string]struct{} {
	dirs := map[string]struct{}{filepath.Clean(s.root): {}}
	for path := range s.files {
		for current := filepath.Dir(path); current != "." && current != string(filepath.Separator); current = filepath.Dir(current) {
			dirs[current] = struct{}{}
			if samePath(current, s.root) {
				break
			}
		}
	}
	return dirs
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func fileMode(isDir bool) fs.FileMode {
	if isDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}

type stubDirEntry struct {
	info stubFileInfo
}

func (d stubDirEntry) Name() string               { return d.info.name }
func (d stubDirEntry) IsDir() bool                { return d.info.isDir }
func (d stubDirEntry) Type() fs.FileMode          { return d.info.mode.Type() }
func (d stubDirEntry) Info() (fs.FileInfo, error) { return d.info, nil }

type stubFileInfo struct {
	name  string
	size  int64
	mode  fs.FileMode
	isDir bool
}

func (i stubFileInfo) Name() string       { return i.name }
func (i stubFileInfo) Size() int64        { return i.size }
func (i stubFileInfo) Mode() fs.FileMode  { return i.mode }
func (i stubFileInfo) ModTime() time.Time { return time.Time{} }
func (i stubFileInfo) IsDir() bool        { return i.isDir }
func (i stubFileInfo) Sys() any           { return nil }

var _ versioning.FileAccess = (*stubAnalysisFileAccess)(nil)
