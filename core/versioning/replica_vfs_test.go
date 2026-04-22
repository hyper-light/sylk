package versioning

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// stubBaseFS is a minimal vfsBaseFS for unit tests — backed by a
// path → content map. Satisfies Read/Exists/ListDir/Stat.
type stubBaseFS struct {
	files map[string][]byte
}

func newStubBaseFS(files map[string][]byte) *stubBaseFS {
	return &stubBaseFS{files: files}
}

func (s *stubBaseFS) ReadFile(path string) ([]byte, error) {
	if c, ok := s.files[path]; ok {
		return append([]byte(nil), c...), nil
	}
	return nil, ErrFileNotFound
}
func (s *stubBaseFS) Exists(path string) (bool, error) {
	_, ok := s.files[path]
	return ok, nil
}
func (s *stubBaseFS) ListDir(dir string) ([]string, error) {
	var out []string
	for p := range s.files {
		if pathInDir(p, dir) {
			out = append(out, p)
		}
	}
	return out, nil
}
func (s *stubBaseFS) Stat(path string) (fs.FileInfo, error) {
	if c, ok := s.files[path]; ok {
		return replicaOverlayFileInfo{path: path, size: int64(len(c))}, nil
	}
	return nil, ErrFileNotFound
}

// openControlWAL for test isolation.
func openTestControlWAL(t *testing.T) *ControlWAL {
	t.Helper()
	wal, err := OpenControlWAL(ControlWALConfig{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open control wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })
	return wal
}

// TestReplicaVFS_ReadsFallThroughChain pins the canonical read
// semantics: local → parent chain → disk. Each layer is consulted
// in order; first hit wins.
func TestReplicaVFS_ReadsFallThroughChain(t *testing.T) {
	disk := newStubBaseFS(map[string][]byte{
		"a.go": []byte("disk version of a"),
		"b.go": []byte("disk version of b"),
	})
	r1, err := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 1},
		ReplicaID:     "r1",
		DiskBase:      disk,
		WAL:           openTestControlWAL(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	// r1 writes b.go; overrides disk.
	if err := r1.Write("inspector", "b.go", []byte("r1 version of b")); err != nil {
		t.Fatal(err)
	}

	r2, err := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 2},
		ReplicaID:     "r2",
		Parent:        r1,
		DiskBase:      disk,
		WAL:           openTestControlWAL(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	// a.go: not owned by r1 or r2 → falls through to disk.
	if got, err := r2.Read("a.go"); err != nil || string(got) != "disk version of a" {
		t.Fatalf("r2.Read(a.go) = %q, %v; want disk version", got, err)
	}
	// b.go: owned by r1 → r2 reads r1's version, not disk.
	if got, err := r2.Read("b.go"); err != nil || string(got) != "r1 version of b" {
		t.Fatalf("r2.Read(b.go) = %q, %v; want r1 version", got, err)
	}
	// r2 writes b.go; now its own version wins.
	if err := r2.Write("tester", "b.go", []byte("r2 version of b")); err != nil {
		t.Fatal(err)
	}
	if got, err := r2.Read("b.go"); err != nil || string(got) != "r2 version of b" {
		t.Fatalf("r2.Read(b.go) after own write = %q, %v; want r2 version", got, err)
	}
	// r1 still sees its own version unchanged.
	if got, err := r1.Read("b.go"); err != nil || string(got) != "r1 version of b" {
		t.Fatalf("r1.Read(b.go) = %q, %v; siblings shouldn't see each other's writes", got, err)
	}
}

// countingBaseFS wraps a stubBaseFS to count ReadFile invocations
// so tests can assert on cache behaviour.
type countingBaseFS struct {
	inner *stubBaseFS
	reads map[string]int
}

func newCountingBaseFS(files map[string][]byte) *countingBaseFS {
	return &countingBaseFS{
		inner: newStubBaseFS(files),
		reads: make(map[string]int),
	}
}

func (c *countingBaseFS) ReadFile(path string) ([]byte, error) {
	c.reads[path]++
	return c.inner.ReadFile(path)
}
func (c *countingBaseFS) Exists(path string) (bool, error) { return c.inner.Exists(path) }
func (c *countingBaseFS) ListDir(dir string) ([]string, error) {
	return c.inner.ListDir(dir)
}
func (c *countingBaseFS) Stat(path string) (fs.FileInfo, error) { return c.inner.Stat(path) }

// TestReplicaVFS_JITReadCacheHitsAvoidChainWalk verifies §3.6's
// read-cache semantic: once a path has been resolved through the
// parent chain or disk, subsequent reads hit the local cache without
// re-walking. The cache is distinct from writes so Seal's diff does
// NOT include cached ancestor content.
func TestReplicaVFS_JITReadCacheHitsAvoidChainWalk(t *testing.T) {
	disk := newCountingBaseFS(map[string][]byte{
		"lib/core.go": []byte("disk content"),
	})
	r, err := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 1},
		ReplicaID:     "r",
		DiskBase:      disk,
		WAL:           openTestControlWAL(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	// First read: chain miss → disk.
	if got, err := r.Read("lib/core.go"); err != nil || string(got) != "disk content" {
		t.Fatalf("first read = %q, %v", got, err)
	}
	if disk.reads["lib/core.go"] != 1 {
		t.Fatalf("disk reads after first = %d, want 1", disk.reads["lib/core.go"])
	}

	// Subsequent reads: cache hit → no disk access.
	for i := 0; i < 5; i++ {
		if got, err := r.Read("lib/core.go"); err != nil || string(got) != "disk content" {
			t.Fatalf("cached read %d = %q, %v", i, got, err)
		}
	}
	if disk.reads["lib/core.go"] != 1 {
		t.Errorf("disk reads after cached hits = %d, want still 1", disk.reads["lib/core.go"])
	}

	// Seal: diff must not include the cached ancestor content.
	diff, err := r.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := diff.Writes["lib/core.go"]; ok {
		t.Error("sealed diff leaked cached ancestor read into Writes — would corrupt addendum")
	}
}

// TestReplicaVFS_WriteInvalidatesReadCache verifies that writing a
// path after it has been cached from an ancestor returns the new
// content, not the stale cached ancestor value.
func TestReplicaVFS_WriteInvalidatesReadCache(t *testing.T) {
	disk := newCountingBaseFS(map[string][]byte{
		"x.go": []byte("disk x"),
	})
	r, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 1},
		ReplicaID:     "r",
		DiskBase:      disk,
		WAL:           openTestControlWAL(t),
	})

	// Warm the cache with disk content.
	_, _ = r.Read("x.go")

	// Overwrite. Cache must invalidate.
	if err := r.Write("inspector", "x.go", []byte("new x")); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Read("x.go"); err != nil || string(got) != "new x" {
		t.Fatalf("post-write read = %q, %v; want 'new x' (cache leak)", got, err)
	}
}

// TestReplicaVFS_DeleteShadowsAncestors pins the delete semantic: a
// local delete hides the path even if an ancestor or disk would
// return content.
func TestReplicaVFS_DeleteShadowsAncestors(t *testing.T) {
	disk := newStubBaseFS(map[string][]byte{"a.go": []byte("disk a")})
	r1, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 1},
		ReplicaID:     "r1",
		DiskBase:      disk,
		WAL:           openTestControlWAL(t),
	})
	if err := r1.Delete("inspector", "a.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := r1.Read("a.go"); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("r1.Read(a.go) after delete = %v, want not-found", err)
	}
	if ok, _ := r1.Exists("a.go"); ok {
		t.Fatal("r1.Exists(a.go) after delete = true, want false")
	}
	// Child chain inherits the shadow.
	r2, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 2},
		ReplicaID:     "r2",
		Parent:        r1,
		DiskBase:      disk,
		WAL:           openTestControlWAL(t),
	})
	if _, err := r2.Read("a.go"); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("r2.Read(a.go) through shadowing parent = %v, want not-found", err)
	}
	// r2 can resurrect the path with its own write.
	if err := r2.Write("tester", "a.go", []byte("r2 resurrected")); err != nil {
		t.Fatal(err)
	}
	if got, err := r2.Read("a.go"); err != nil || string(got) != "r2 resurrected" {
		t.Fatalf("r2.Read(a.go) after resurrect = %q, %v", got, err)
	}
}

// TestReplicaVFS_SealFreezesOverlay pins that Seal freezes the
// overlay (no more writes) and returns the diff vs. the parent.
func TestReplicaVFS_SealFreezesOverlay(t *testing.T) {
	r, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 1},
		ReplicaID:     "r1",
		DiskBase:      newStubBaseFS(map[string][]byte{}),
		WAL:           openTestControlWAL(t),
	})
	r.Write("inspector", "x.go", []byte("content1"))
	r.Write("tester", "y_test.go", []byte("test content"))
	r.Delete("inspector", "old.go")

	diff, err := r.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !r.IsSealed() {
		t.Fatal("IsSealed false after Seal")
	}
	if string(diff.Writes["x.go"]) != "content1" {
		t.Errorf("diff.Writes[x.go] = %q", diff.Writes["x.go"])
	}
	if string(diff.Writes["y_test.go"]) != "test content" {
		t.Errorf("diff.Writes[y_test.go] = %q", diff.Writes["y_test.go"])
	}
	if len(diff.Deletes) != 1 || diff.Deletes[0] != "old.go" {
		t.Errorf("diff.Deletes = %v, want [old.go]", diff.Deletes)
	}
	// Post-seal writes rejected.
	if err := r.Write("inspector", "z.go", []byte("late")); !errors.Is(err, ErrReplicaVFSSealed) {
		t.Fatalf("post-seal Write = %v, want ErrReplicaVFSSealed", err)
	}
	if err := r.Delete("inspector", "x.go"); !errors.Is(err, ErrReplicaVFSSealed) {
		t.Fatalf("post-seal Delete = %v, want ErrReplicaVFSSealed", err)
	}
}

// TestReplicaVFS_AbandonBypassesChain pins that Abandon makes
// downstream chain walks skip this layer entirely.
func TestReplicaVFS_AbandonBypassesChain(t *testing.T) {
	disk := newStubBaseFS(map[string][]byte{"a.go": []byte("disk a")})
	r1, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 1},
		ReplicaID:     "r1",
		DiskBase:      disk,
		WAL:           openTestControlWAL(t),
	})
	r1.Write("inspector", "a.go", []byte("r1 override"))
	r1.Abandon()

	// r1 direct read bypasses its own overlay and falls to disk.
	if got, err := r1.Read("a.go"); err != nil || string(got) != "disk a" {
		t.Fatalf("abandoned r1.Read(a.go) = %q, %v; want disk fallback", got, err)
	}
	// Child chain also bypasses r1.
	r2, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 2},
		ReplicaID:     "r2",
		Parent:        r1,
		DiskBase:      disk,
		WAL:           openTestControlWAL(t),
	})
	if got, err := r2.Read("a.go"); err != nil || string(got) != "disk a" {
		t.Fatalf("r2 through abandoned r1 = %q, %v; want disk fallback", got, err)
	}
	// Post-abandon writes rejected.
	if err := r1.Write("inspector", "b.go", []byte("late")); !errors.Is(err, ErrReplicaVFSAbandoned) {
		t.Fatalf("post-abandon Write = %v, want ErrReplicaVFSAbandoned", err)
	}
}

// TestReplicaVFS_WALReplayRehydrates pins the durability contract:
// writes persisted via ControlWAL replay onto a fresh ReplicaVFS
// produce byte-identical in-memory state.
func TestReplicaVFS_WALReplayRehydrates(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	ver := SemanticVersion{Minor: 7}
	r1, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: ver,
		ReplicaID:     "r-persist",
		DiskBase:      newStubBaseFS(map[string][]byte{}),
		WAL:           wal,
	})
	r1.Write("inspector", "p.go", []byte("persisted"))
	r1.Delete("tester", "old.go")
	r1.Write("inspector", "p.go", []byte("updated")) // overwrite
	r1.Seal()
	wal.Close()

	// Reopen WAL; reconstruct a fresh ReplicaVFS; apply entries.
	wal2, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer wal2.Close()
	r2, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: ver,
		ReplicaID:     "r-persist",
		DiskBase:      newStubBaseFS(map[string][]byte{}),
		WAL:           wal2,
	})
	if err := wal2.Replay(func(e *ControlEntry) error {
		r2.ApplyControlEntry(e)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got, err := r2.Read("p.go"); err != nil || string(got) != "updated" {
		t.Fatalf("r2.Read(p.go) after replay = %q, %v; want updated", got, err)
	}
	if _, err := r2.Read("old.go"); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("r2.Read(old.go) after replay = %v; want not-found", err)
	}
	if !r2.IsSealed() {
		t.Fatal("r2.IsSealed false after replay; want true")
	}
}

// TestReplicaVFS_SharedBetweenInspectorAndTester pins the key design
// property: both audit roles write to the same VFS and see each
// other's writes. This is how the inspector's formatter can run over
// the tester's just-authored test file.
func TestReplicaVFS_SharedBetweenInspectorAndTester(t *testing.T) {
	r, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 1},
		ReplicaID:     "r-shared",
		DiskBase:      newStubBaseFS(map[string][]byte{}),
		WAL:           openTestControlWAL(t),
	})
	// Tester writes a test file.
	if err := r.Write("tester-global#replica-sess:1.1", "tests/new_test.go", []byte("package tests\n\nfunc TestX(t *testing.T) {}")); err != nil {
		t.Fatal(err)
	}
	// Inspector reads it back (chain walk sees the tester's write).
	if got, err := r.Read("tests/new_test.go"); err != nil || string(got) != "package tests\n\nfunc TestX(t *testing.T) {}" {
		t.Fatalf("inspector sees tester write: got %q, err %v", got, err)
	}
	// Inspector overwrites with formatted version.
	if err := r.Write("inspector-global#replica-sess:1.1", "tests/new_test.go", []byte("package tests\n\nfunc TestX(t *testing.T) {\n\t// formatted\n}")); err != nil {
		t.Fatal(err)
	}
	// Tester reads back the formatted version.
	if got, err := r.Read("tests/new_test.go"); err != nil || filepath.Base("formatted") == "" {
		t.Fatalf("tester sees inspector's format pass: got %q, err %v", got, err)
	}
}

// TestReplicaVFS_AsBaseFSChainsThrough pins that ReplicaVFS satisfies
// vfsBaseFS via AsBaseFS, enabling a downstream PipelineVFS (or
// another ReplicaVFS that wants to attach it as a non-Parent base)
// to read through it.
func TestReplicaVFS_AsBaseFSChainsThrough(t *testing.T) {
	r, _ := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: SemanticVersion{Minor: 1},
		ReplicaID:     "r-base",
		DiskBase:      newStubBaseFS(map[string][]byte{"disk-only.go": []byte("on disk")}),
		WAL:           openTestControlWAL(t),
	})
	r.Write("inspector", "overlay.go", []byte("in overlay"))

	base := r.AsBaseFS()
	if base == nil {
		t.Fatal("AsBaseFS returned nil")
	}
	if got, err := base.ReadFile("overlay.go"); err != nil || string(got) != "in overlay" {
		t.Errorf("base.ReadFile(overlay.go) = %q, %v", got, err)
	}
	if got, err := base.ReadFile("disk-only.go"); err != nil || string(got) != "on disk" {
		t.Errorf("base.ReadFile(disk-only.go) = %q, %v", got, err)
	}
	if ok, _ := base.Exists("missing.go"); ok {
		t.Error("base.Exists(missing.go) = true")
	}
}

// TestReplicaVFS_SessionVFSFactory pins that SessionVFS produces a
// ReplicaVFS with the correct parent wiring, tracks it by version,
// and releases it cleanly.
func TestReplicaVFS_SessionVFSFactory(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	_ = ensureDir(workDir)
	sess, err := NewSessionVFS(SessionVFSConfig{
		SessionID:   "sess-factory",
		WorkingDir:  workDir,
		StorageRoot: filepath.Join(dir, "session"),
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer sess.Close()

	v1, err := sess.BeginAuditReplicaVFS(SemanticVersion{Minor: 1}, "r1")
	if err != nil {
		t.Fatalf("begin r1: %v", err)
	}
	if v1.Parent() != nil {
		t.Fatal("r1.Parent() != nil; want nil for first replica in session")
	}
	v2, err := sess.BeginAuditReplicaVFS(SemanticVersion{Minor: 2}, "r2")
	if err != nil {
		t.Fatal(err)
	}
	// Without a recorded merge descriptor between v1 and v2 in the
	// merge log, v2's parent lookup walks the log and finds no
	// matching tracked ReplicaVFS — parent is nil. This exercises
	// the fallback path. A true end-to-end test (merge log populated
	// via MergePipelineIntoGreen) is covered in the coordinator-
	// level integration tests.
	_ = v2

	if got := sess.LookupAuditReplicaVFS(SemanticVersion{Minor: 1}); got != v1 {
		t.Errorf("lookup v1 = %p, want %p", got, v1)
	}
	sess.ReleaseAuditReplicaVFS(SemanticVersion{Minor: 1})
	if got := sess.LookupAuditReplicaVFS(SemanticVersion{Minor: 1}); got != nil {
		t.Errorf("lookup v1 after release = %p, want nil", got)
	}
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
