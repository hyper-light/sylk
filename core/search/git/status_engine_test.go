package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// Stat-Dirty Comparison Tests
// =============================================================================

func TestStatClean_AllFieldsMatch(t *testing.T) {
	// Entry mtime is well before the index mtime → not racy.
	entryTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	indexMtime := time.Now().Add(-time.Hour)
	entry := &index.Entry{
		ModifiedAt: entryTime,
		Size:       42,
		Mode:       filemode.Regular,
	}
	info := fakeFileInfo{modTime: entryTime, size: 42, mode: 0644}

	got := compareStatToIndex(info, entry, indexMtime)
	if got != statClean {
		t.Errorf("expected statClean, got %d", got)
	}
}

func TestStatDirty_MtimeDiffers(t *testing.T) {
	entryTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	indexMtime := time.Now().Add(-time.Hour)
	entry := &index.Entry{
		ModifiedAt: entryTime,
		Size:       42,
		Mode:       filemode.Regular,
	}
	// Disk mtime is newer than entry mtime.
	info := fakeFileInfo{modTime: entryTime.Add(time.Second), size: 42, mode: 0644}

	got := compareStatToIndex(info, entry, indexMtime)
	if got != statDirty {
		t.Errorf("expected statDirty, got %d", got)
	}
}

func TestStatDirty_SizeDiffers(t *testing.T) {
	entryTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	indexMtime := time.Now().Add(-time.Hour)
	entry := &index.Entry{
		ModifiedAt: entryTime,
		Size:       42,
		Mode:       filemode.Regular,
	}
	info := fakeFileInfo{modTime: entryTime, size: 100, mode: 0644}

	got := compareStatToIndex(info, entry, indexMtime)
	if got != statDirty {
		t.Errorf("expected statDirty, got %d", got)
	}
}

func TestStatDirty_ModeDiffers(t *testing.T) {
	entryTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	indexMtime := time.Now().Add(-time.Hour)
	entry := &index.Entry{
		ModifiedAt: entryTime,
		Size:       42,
		Mode:       filemode.Regular, // 0100644
	}
	info := fakeFileInfo{modTime: entryTime, size: 42, mode: 0755} // Executable

	got := compareStatToIndex(info, entry, indexMtime)
	if got != statDirty {
		t.Errorf("expected statDirty, got %d", got)
	}
}

func TestStatRacy_EntryMtimeMatchesIndex(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	// Entry mtime == index mtime → racy (could have been modified in same second).
	entry := &index.Entry{
		ModifiedAt: now,
		Size:       42,
		Mode:       filemode.Regular,
	}
	info := fakeFileInfo{modTime: now, size: 42, mode: 0644}
	indexMtime := now

	got := compareStatToIndex(info, entry, indexMtime)
	if got != statRacy {
		t.Errorf("expected statRacy, got %d", got)
	}
}

func TestSizeOverflow_Uint32Wrap(t *testing.T) {
	entryTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	indexMtime := time.Now().Add(-time.Hour)
	entry := &index.Entry{
		ModifiedAt: entryTime,
		Size:       0, // Wraps for files > 4GB
		Mode:       filemode.Regular,
	}
	// Disk has a large file but entry.Size wrapped to 0.
	info := fakeFileInfo{modTime: entryTime, size: 5_000_000_000, mode: 0644}

	got := compareStatToIndex(info, entry, indexMtime)
	if got != statDirty {
		t.Errorf("expected statDirty for uint32-wrapped size, got %d", got)
	}
}

// =============================================================================
// Cache Tests
// =============================================================================

func TestIndexCache_ReuseOnSameStat(t *testing.T) {
	dir := initTestRepo(t)
	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	gitDir := filepath.Join(dir, ".git")
	engine := NewStatusEngine(client, gitDir)

	cache1, err := engine.refreshIndexCache()
	if err != nil {
		t.Fatal(err)
	}

	cache2, err := engine.refreshIndexCache()
	if err != nil {
		t.Fatal(err)
	}

	// Same pointer means the cache was reused, not re-parsed.
	if cache1 != cache2 {
		t.Error("expected index cache to be reused when mtime/size unchanged")
	}
}

func TestIndexCache_InvalidateOnChange(t *testing.T) {
	dir := initTestRepo(t)
	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	gitDir := filepath.Join(dir, ".git")
	engine := NewStatusEngine(client, gitDir)

	cache1, err := engine.refreshIndexCache()
	if err != nil {
		t.Fatal(err)
	}

	// Stage a new file to modify the index.
	writeFile(t, dir, "new.txt", "content")
	addFile(t, dir, "new.txt")

	cache2, err := engine.refreshIndexCache()
	if err != nil {
		t.Fatal(err)
	}

	if cache1 == cache2 {
		t.Error("expected index cache to be invalidated after staging a file")
	}
}

func TestHeadCache_ReuseOnSameHash(t *testing.T) {
	dir := initTestRepo(t)
	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	gitDir := filepath.Join(dir, ".git")
	engine := NewStatusEngine(client, gitDir)

	// Need an index cache first for buildHeadEntries capacity hint.
	_, err = engine.refreshIndexCache()
	if err != nil {
		t.Fatal(err)
	}

	cache1, err := engine.refreshHeadCache()
	if err != nil {
		t.Fatal(err)
	}

	cache2, err := engine.refreshHeadCache()
	if err != nil {
		t.Fatal(err)
	}

	if cache1 != cache2 {
		t.Error("expected HEAD cache to be reused when HEAD unchanged")
	}
}

func TestHeadCache_InvalidateOnNewCommit(t *testing.T) {
	dir := initTestRepo(t)
	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	gitDir := filepath.Join(dir, ".git")
	engine := NewStatusEngine(client, gitDir)

	_, err = engine.refreshIndexCache()
	if err != nil {
		t.Fatal(err)
	}

	cache1, err := engine.refreshHeadCache()
	if err != nil {
		t.Fatal(err)
	}

	// Create a new commit.
	writeFile(t, dir, "second.txt", "data")
	addFile(t, dir, "second.txt")
	commitAll(t, dir, "second commit")

	// Re-open the repo to pick up the new commit via storer.
	client2, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine2 := NewStatusEngine(client2, gitDir)
	engine2.idxCache = engine.idxCache // share index cache
	engine2.headCache = cache1         // share old HEAD cache

	_, err = engine2.refreshIndexCache()
	if err != nil {
		t.Fatal(err)
	}

	cache2, err := engine2.refreshHeadCache()
	if err != nil {
		t.Fatal(err)
	}

	if cache1 == cache2 {
		t.Error("expected HEAD cache to be invalidated after new commit")
	}
}

// =============================================================================
// Classification Tests
// =============================================================================

func TestConflictDetection_NonZeroStage(t *testing.T) {
	// go-git decodes Stage from (flags>>12)&0x3: 0=merged, 1=ancestor, 2=ours, 3=theirs.
	idx := &index.Index{
		Entries: []*index.Entry{
			{Name: "conflict.go", Stage: 1}, // ancestor
			{Name: "conflict.go", Stage: 2}, // ours
			{Name: "conflict.go", Stage: 3}, // theirs
			{Name: "clean.go", Stage: 0},    // merged (normal)
		},
	}

	now := time.Now()
	cache := buildIndexMaps(idx, now, 100)

	if _, ok := cache.conflicts["conflict.go"]; !ok {
		t.Error("expected conflict.go in conflicts map")
	}
	if _, ok := cache.entries["conflict.go"]; ok {
		t.Error("conflict entries should not be in stage-0 entries map")
	}
	if _, ok := cache.entries["clean.go"]; !ok {
		t.Error("expected clean.go in entries map")
	}
}

func TestStagedAdded_InIndexNotInHEAD(t *testing.T) {
	idxEntries := map[string]*index.Entry{
		"new.go": {Hash: plumbing.NewHash("aaaa"), Mode: filemode.Regular},
	}
	headEntries := map[string]plumbing.Hash{}
	wts := &WorkingTreeStatus{
		Modified:  make([]string, 0),
		Added:     make([]string, 0),
		Deleted:   make([]string, 0),
		Untracked: make([]string, 0),
		Conflict:  make([]string, 0),
	}

	classifyStagedChanges(idxEntries, headEntries, wts)

	assertContains(t, "Added", wts.Added, "new.go")
	if !wts.HasIndexStaged {
		t.Error("expected HasIndexStaged=true for staged addition")
	}
}

func TestStagedDeleted_InHEADNotInIndex(t *testing.T) {
	idxEntries := map[string]*index.Entry{}
	headEntries := map[string]plumbing.Hash{
		"removed.go": plumbing.NewHash("bbbb"),
	}
	wts := &WorkingTreeStatus{
		Modified:  make([]string, 0),
		Added:     make([]string, 0),
		Deleted:   make([]string, 0),
		Untracked: make([]string, 0),
		Conflict:  make([]string, 0),
	}

	classifyStagedChanges(idxEntries, headEntries, wts)

	assertContains(t, "Deleted", wts.Deleted, "removed.go")
	if !wts.HasIndexStaged {
		t.Error("expected HasIndexStaged=true for staged deletion")
	}
}

func TestIntentToAdd_ClassifiedAsAdded(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "intent.txt", "data")

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	gitDir := filepath.Join(dir, ".git")
	engine := NewStatusEngine(client, gitDir)

	// Manually build an index with IntentToAdd.
	idxEntries := map[string]*index.Entry{
		"intent.txt": {
			Name:        "intent.txt",
			IntentToAdd: true,
			Mode:        filemode.Regular,
		},
	}
	headEntries := map[string]plumbing.Hash{}
	conflicts := map[string]struct{}{}
	ignores := newIgnoreStack(engine.repoPath, engine.gitDir)
	indexMtime := time.Now().Add(-time.Hour)

	wts, _, err := walkWorktree(
		context.Background(), dir,
		idxEntries, headEntries, conflicts,
		ignores, indexMtime,
	)
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, "Added", wts.Added, "intent.txt")
}

func TestIgnoredSkip_GitignoreMatch(t *testing.T) {
	dir := initTestRepo(t)

	// Create .gitignore.
	writeFile(t, dir, ".gitignore", "*.log\n")
	addFile(t, dir, ".gitignore")
	commitAll(t, dir, "add gitignore")

	// Create an ignored file.
	writeFile(t, dir, "debug.log", "log data")

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	gitDir := filepath.Join(dir, ".git")
	engine := NewStatusEngine(client, gitDir)

	update, err := engine.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// debug.log should NOT appear in the status map (ignored).
	if _, ok := update.StatusMap["debug.log"]; ok {
		t.Error("expected debug.log to be ignored, but found in StatusMap")
	}
}

func TestSkipWorktree_Excluded(t *testing.T) {
	idxEntries := map[string]*index.Entry{
		"sparse.go": {
			Name:         "sparse.go",
			SkipWorktree: true,
			Mode:         filemode.Regular,
			Hash:         plumbing.NewHash("cccc"),
		},
	}
	headEntries := map[string]plumbing.Hash{
		"sparse.go": plumbing.NewHash("cccc"),
	}
	wts := &WorkingTreeStatus{
		Modified:  make([]string, 0),
		Added:     make([]string, 0),
		Deleted:   make([]string, 0),
		Untracked: make([]string, 0),
		Conflict:  make([]string, 0),
	}

	// SkipWorktree entries should not appear in staged changes.
	classifyStagedChanges(idxEntries, headEntries, wts)

	if len(wts.Added) != 0 || len(wts.Modified) != 0 || len(wts.Deleted) != 0 {
		t.Error("expected SkipWorktree entry to be excluded from staged changes")
	}
}

// =============================================================================
// Integration Test
// =============================================================================

func TestFullRefresh_MatchesGoGit(t *testing.T) {
	dir := initTestRepo(t)

	// Create various file states.
	writeFile(t, dir, "tracked.go", "package main")
	addFile(t, dir, "tracked.go")
	commitAll(t, dir, "initial")

	// Modified file.
	writeFile(t, dir, "tracked.go", "package main\n// modified")

	// Untracked file.
	writeFile(t, dir, "untracked.txt", "hello")

	// Staged addition.
	writeFile(t, dir, "staged.go", "package staged")
	addFile(t, dir, "staged.go")

	// Build StatusEngine result.
	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(dir, ".git")
	engine := NewStatusEngine(client, gitDir)
	engineUpdate, err := engine.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Build go-git result for comparison.
	goGitWts, err := client.WorktreeStatus()
	if err != nil {
		t.Fatal(err)
	}
	goGitMap := BuildStatusMap(goGitWts)
	goGitTracked := client.TrackedSet()

	// Compare: every non-clean path in go-git should appear in engine output.
	for path, state := range goGitMap {
		engineState, ok := engineUpdate.StatusMap[path]
		if !ok {
			t.Errorf("engine missing path %q (go-git state=%d)", path, state)
			continue
		}
		// States may not match exactly for directories (propagation order)
		// so only check file-level entries.
		if !isDirInTracked(path, goGitTracked) && engineState != state {
			t.Errorf("path %q: engine=%d, go-git=%d", path, engineState, state)
		}
	}

	// Check tracked set coverage.
	for path := range goGitTracked {
		if _, ok := engineUpdate.TrackedSet[path]; !ok {
			t.Errorf("engine TrackedSet missing %q", path)
		}
	}
}

// isDir reports whether a path appears to be a directory in the status map
// (not present in the tracked set as a file).
func isDirInTracked(path string, tracked map[string]struct{}) bool {
	_, isFile := tracked[path]
	return !isFile
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkStatusEngine_Refresh(b *testing.B) {
	dir := initBenchRepo(b)
	client, err := NewGitClient(dir)
	if err != nil {
		b.Fatal(err)
	}
	gitDir := filepath.Join(dir, ".git")
	engine := NewStatusEngine(client, gitDir)
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		if _, err := engine.Refresh(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGoGit_WorktreeStatus(b *testing.B) {
	dir := initBenchRepo(b)
	client, err := NewGitClient(dir)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		wts, err := client.WorktreeStatus()
		if err != nil {
			b.Fatal(err)
		}
		_ = BuildStatusMap(wts)
		_ = client.TrackedSet()
		_ = BuildTrackedDirs(client.TrackedSet())
	}
}

// =============================================================================
// Test Helpers
// =============================================================================

// fakeFileInfo implements fs.FileInfo for unit testing stat comparison.
type fakeFileInfo struct {
	modTime time.Time
	size    int64
	mode    os.FileMode
}

func (f fakeFileInfo) Name() string      { return "test" }
func (f fakeFileInfo) Size() int64       { return f.size }
func (f fakeFileInfo) Mode() os.FileMode { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return f.modTime }
func (f fakeFileInfo) IsDir() bool       { return false }
func (f fakeFileInfo) Sys() any          { return nil }

// initTestRepo creates a temporary git repository with an initial commit.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	// Configure author for commits.
	cfg, _ := repo.Config()
	cfg.User.Name = "Test"
	cfg.User.Email = "test@test.com"
	_ = repo.SetConfig(cfg)

	// Create initial file and commit.
	writeFile(t, dir, "README.md", "# test")
	addFile(t, dir, "README.md")
	commitAll(t, dir, "init")

	return dir
}

// initBenchRepo creates a repo with many tracked files for benchmarking.
func initBenchRepo(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		b.Fatal(err)
	}

	cfg, _ := repo.Config()
	cfg.User.Name = "Bench"
	cfg.User.Email = "bench@test.com"
	_ = repo.SetConfig(cfg)

	wt, err := repo.Worktree()
	if err != nil {
		b.Fatal(err)
	}

	// Create 200 files across subdirectories.
	dirs := []string{"src", "pkg", "internal", "cmd"}
	fileIdx := 0
	for _, d := range dirs {
		dirPath := filepath.Join(dir, d)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			b.Fatal(err)
		}
		for range 50 {
			name := filepath.Join(d, fmt.Sprintf("file_%d.go", fileIdx))
			content := fmt.Sprintf("package %s\n// file %d\n", d, fileIdx)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
				b.Fatal(err)
			}
			if _, err := wt.Add(name); err != nil {
				b.Fatal(err)
			}
			fileIdx++
		}
	}

	_, err = wt.Commit("bench init", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Bench",
			Email: "bench@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		b.Fatal(err)
	}

	// Modify a few files to create dirty state.
	for i := range 5 {
		name := filepath.Join("src", fmt.Sprintf("file_%d.go", i))
		if err := os.WriteFile(filepath.Join(dir, name), []byte("modified\n"), 0644); err != nil {
			b.Fatal(err)
		}
	}

	return dir
}

func writeFile(t testing.TB, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func addFile(t testing.TB, dir, name string) {
	t.Helper()
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t testing.TB, dir, msg string) {
	t.Helper()
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	_, err = wt.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

