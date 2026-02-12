package git

import (
	"testing"

	gogit "github.com/go-git/go-git/v5"
)

// =============================================================================
// classifyFileStatus Tests
// =============================================================================

func TestClassifyFileStatus_Untracked(t *testing.T) {
	result := classifySingle("newfile.go", &gogit.FileStatus{
		Worktree: gogit.Untracked,
	})
	assertPaths(t, "Untracked", result.Untracked, "newfile.go")
}

func TestClassifyFileStatus_Added(t *testing.T) {
	result := classifySingle("staged.go", &gogit.FileStatus{
		Staging: gogit.Added,
	})
	assertPaths(t, "Added", result.Added, "staged.go")
}

func TestClassifyFileStatus_Modified_Worktree(t *testing.T) {
	result := classifySingle("src/main.go", &gogit.FileStatus{
		Staging:  gogit.Unmodified,
		Worktree: gogit.Modified,
	})
	assertPaths(t, "Modified", result.Modified, "src/main.go")
}

func TestClassifyFileStatus_Modified_Index(t *testing.T) {
	result := classifySingle("src/main.go", &gogit.FileStatus{
		Staging:  gogit.Modified,
		Worktree: gogit.Unmodified,
	})
	assertPaths(t, "Modified", result.Modified, "src/main.go")
}

func TestClassifyFileStatus_Deleted_Worktree(t *testing.T) {
	result := classifySingle("removed.go", &gogit.FileStatus{
		Staging:  gogit.Unmodified,
		Worktree: gogit.Deleted,
	})
	assertPaths(t, "Deleted", result.Deleted, "removed.go")
}

func TestClassifyFileStatus_Deleted_Index(t *testing.T) {
	result := classifySingle("staged_del.go", &gogit.FileStatus{
		Staging: gogit.Deleted,
	})
	assertPaths(t, "Deleted", result.Deleted, "staged_del.go")
}

func TestClassifyFileStatus_Conflict(t *testing.T) {
	result := classifySingle("conflict.go", &gogit.FileStatus{
		Staging:  gogit.UpdatedButUnmerged,
		Worktree: gogit.UpdatedButUnmerged,
	})
	assertPaths(t, "Conflict", result.Conflict, "conflict.go")
}

func TestClassifyFileStatus_Conflict_StagingOnly(t *testing.T) {
	result := classifySingle("conflict.go", &gogit.FileStatus{
		Staging: gogit.UpdatedButUnmerged,
	})
	assertPaths(t, "Conflict", result.Conflict, "conflict.go")
}

// =============================================================================
// classifyRename Tests
// =============================================================================

func TestClassifyRename_Normal(t *testing.T) {
	result := classifySingle("new_name.go", &gogit.FileStatus{
		Staging: gogit.Renamed,
		Extra:   "old_name.go",
	})
	assertPaths(t, "Modified", result.Modified, "new_name.go")
	assertContains(t, "Deleted", result.Deleted, "old_name.go")
}

func TestClassifyRename_WorktreeDeleted(t *testing.T) {
	result := classifySingle("new_name.go", &gogit.FileStatus{
		Staging:  gogit.Renamed,
		Worktree: gogit.Deleted,
		Extra:    "old_name.go",
	})
	assertContains(t, "Deleted", result.Deleted, "new_name.go")
	assertContains(t, "Deleted", result.Deleted, "old_name.go")
}

// =============================================================================
// classifyCopy Tests
// =============================================================================

func TestClassifyCopy_Normal(t *testing.T) {
	result := classifySingle("copy.go", &gogit.FileStatus{
		Staging: gogit.Copied,
	})
	assertPaths(t, "Modified", result.Modified, "copy.go")
	// Original path should NOT be marked deleted for copies.
	assertEmpty(t, "Deleted", result.Deleted)
}

func TestClassifyCopy_WorktreeDeleted(t *testing.T) {
	result := classifySingle("copy.go", &gogit.FileStatus{
		Staging:  gogit.Copied,
		Worktree: gogit.Deleted,
	})
	assertPaths(t, "Deleted", result.Deleted, "copy.go")
}

// =============================================================================
// classifyGoGitStatus Tests (multi-file)
// =============================================================================

func TestClassifyGoGitStatus_Mixed(t *testing.T) {
	s := gogit.Status{
		"modified.go":  {Staging: gogit.Unmodified, Worktree: gogit.Modified},
		"added.go":     {Staging: gogit.Added},
		"deleted.go":   {Staging: gogit.Unmodified, Worktree: gogit.Deleted},
		"untracked.go": {Worktree: gogit.Untracked},
		"conflict.go":  {Staging: gogit.UpdatedButUnmerged, Worktree: gogit.UpdatedButUnmerged},
	}
	result := classifyGoGitStatus(s)

	assertContains(t, "Modified", result.Modified, "modified.go")
	assertContains(t, "Added", result.Added, "added.go")
	assertContains(t, "Deleted", result.Deleted, "deleted.go")
	assertContains(t, "Untracked", result.Untracked, "untracked.go")
	assertContains(t, "Conflict", result.Conflict, "conflict.go")
	if !result.HasIndexStaged {
		t.Error("HasIndexStaged: expected true (added.go has Staging=Added)")
	}
}

func TestClassifyGoGitStatus_Empty(t *testing.T) {
	result := classifyGoGitStatus(gogit.Status{})
	assertEmpty(t, "Modified", result.Modified)
	assertEmpty(t, "Added", result.Added)
	assertEmpty(t, "Deleted", result.Deleted)
	assertEmpty(t, "Untracked", result.Untracked)
	assertEmpty(t, "Conflict", result.Conflict)
	if result.HasIndexStaged {
		t.Error("HasIndexStaged: expected false for empty status")
	}
}

func TestClassifyGoGitStatus_IndexStagedModification(t *testing.T) {
	s := gogit.Status{
		"staged_mod.go": {Staging: gogit.Modified, Worktree: gogit.Unmodified},
	}
	result := classifyGoGitStatus(s)
	if !result.HasIndexStaged {
		t.Error("HasIndexStaged: expected true (staged_mod.go has Staging=Modified)")
	}
}

func TestClassifyGoGitStatus_OnlyWorktreeChanges(t *testing.T) {
	s := gogit.Status{
		"unstaged.go": {Staging: gogit.Unmodified, Worktree: gogit.Modified},
		"new.go":      {Worktree: gogit.Untracked},
	}
	result := classifyGoGitStatus(s)
	if result.HasIndexStaged {
		t.Error("HasIndexStaged: expected false (only worktree changes)")
	}
}

// =============================================================================
// BuildStatusMap Tests
// =============================================================================

func TestBuildStatusMap_PropagatesNewStates(t *testing.T) {
	wts := &WorkingTreeStatus{
		Added:   []string{"src/new.go"},
		Deleted: []string{"src/old.go"},
	}
	m := BuildStatusMap(wts)

	if m["src/new.go"] != GitAdded {
		t.Errorf("src/new.go: got %d, want GitAdded(%d)", m["src/new.go"], GitAdded)
	}
	if m["src/old.go"] != GitDeleted {
		t.Errorf("src/old.go: got %d, want GitDeleted(%d)", m["src/old.go"], GitDeleted)
	}
	// Parent directory should have the highest priority child state.
	if m["src"] != GitDeleted {
		t.Errorf("src: got %d, want GitDeleted(%d)", m["src"], GitDeleted)
	}
}

func TestBuildStatusMap_PriorityOrder(t *testing.T) {
	wts := &WorkingTreeStatus{
		Untracked: []string{"dir/b"},
		Added:     []string{"dir/c"},
		Modified:  []string{"dir/d"},
		Deleted:   []string{"dir/e"},
		Conflict:  []string{"dir/f"},
	}
	m := BuildStatusMap(wts)

	// Directory should have the highest-priority child state (Conflict).
	if m["dir"] != GitConflict {
		t.Errorf("dir: got %d, want GitConflict(%d)", m["dir"], GitConflict)
	}

	// Each file should have its own state.
	cases := []struct {
		path string
		want GitFileState
	}{
		{"dir/b", GitUntracked},
		{"dir/c", GitAdded},
		{"dir/d", GitModified},
		{"dir/e", GitDeleted},
		{"dir/f", GitConflict},
	}
	for _, tc := range cases {
		if got := m[tc.path]; got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.path, got, tc.want)
		}
	}
}

// =============================================================================
// BuildTrackedDirs Tests
// =============================================================================

func TestBuildTrackedDirs_DerivesAncestors(t *testing.T) {
	tracked := map[string]struct{}{
		"src/pkg/main.go":  {},
		"src/pkg/util.go":  {},
		"docs/readme.md":   {},
	}
	dirs := BuildTrackedDirs(tracked)

	expected := []string{"src", "src/pkg", "docs"}
	for _, d := range expected {
		if _, ok := dirs[d]; !ok {
			t.Errorf("expected %q in tracked dirs", d)
		}
	}
}

func TestBuildTrackedDirs_Empty(t *testing.T) {
	dirs := BuildTrackedDirs(nil)
	if len(dirs) != 0 {
		t.Errorf("expected empty, got %v", dirs)
	}
}

// =============================================================================
// Helpers
// =============================================================================

// classifySingle classifies a single file status entry for testing.
func classifySingle(path string, fs *gogit.FileStatus) *WorkingTreeStatus {
	result := &WorkingTreeStatus{
		Modified:  make([]string, 0),
		Added:     make([]string, 0),
		Deleted:   make([]string, 0),
		Untracked: make([]string, 0),
		Conflict:  make([]string, 0),
	}
	classifyFileStatus(path, fs, result)
	return result
}

func assertEmpty(t *testing.T, name string, slice []string) {
	t.Helper()
	if len(slice) != 0 {
		t.Errorf("%s: expected empty, got %v", name, slice)
	}
}

func assertPaths(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d paths %v, want %d paths %v", name, len(got), got, len(want), want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("%s[%d]: got %q, want %q", name, i, got[i], w)
		}
	}
}

func assertContains(t *testing.T, name string, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("%s: %v does not contain %q", name, slice, want)
}
