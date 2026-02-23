package git

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// Core Commit Tests
// =============================================================================

func TestCommitEngine_BasicMultipleFiles(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"a.txt":     "content a",
		"b.txt":     "content b",
		"sub/c.txt": "content c",
	}
	writeTestFiles(t, dir, files)

	paths := []string{"a.txt", "b.txt", "sub/c.txt"}
	if err := client.CommitFiles(paths, "add three files"); err != nil {
		t.Fatal(err)
	}

	// Verify commit message and tree contents.
	repo, _ := gogit.PlainOpen(dir)
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())

	if commit.Message != "add three files" {
		t.Errorf("message = %q, want %q", commit.Message, "add three files")
	}

	tree, _ := commit.Tree()
	for _, name := range paths {
		f, fErr := tree.File(name)
		if fErr != nil {
			t.Errorf("file %s not in tree: %v", name, fErr)
			continue
		}
		content, _ := f.Contents()
		if content != files[name] {
			t.Errorf("file %s = %q, want %q", name, content, files[name])
		}
	}

	// Verify parent is the initial commit.
	if len(commit.ParentHashes) != 1 {
		t.Fatalf("parents = %d, want 1", len(commit.ParentHashes))
	}
}

func TestCommitEngine_WithDeletions(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Create and commit a file.
	writeTestFiles(t, dir, map[string]string{"delete_me.txt": "ephemeral"})
	if err := client.CommitFiles([]string{"delete_me.txt"}, "add file"); err != nil {
		t.Fatal(err)
	}

	// Delete from disk, commit the deletion.
	os.Remove(filepath.Join(dir, "delete_me.txt"))
	if err := client.CommitFiles([]string{"delete_me.txt"}, "remove file"); err != nil {
		t.Fatal(err)
	}

	// Verify file is absent from the tree.
	repo, _ := gogit.PlainOpen(dir)
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())
	tree, _ := commit.Tree()

	if _, fErr := tree.File("delete_me.txt"); fErr == nil {
		t.Error("deleted file still in tree")
	}

	// Verify original test.txt survives.
	if _, fErr := tree.File("test.txt"); fErr != nil {
		t.Error("test.txt should survive deletion commit")
	}
}

func TestCommitEngine_MixedAddModifyDelete(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Setup: commit two additional files.
	writeTestFiles(t, dir, map[string]string{
		"modify.txt": "original",
		"remove.txt": "doomed",
	})
	if err := client.CommitFiles([]string{"modify.txt", "remove.txt"}, "setup"); err != nil {
		t.Fatal(err)
	}

	// Mixed operation: add new, modify existing, delete one.
	writeTestFiles(t, dir, map[string]string{
		"new.txt":    "brand new",
		"modify.txt": "modified content",
	})
	os.Remove(filepath.Join(dir, "remove.txt"))

	paths := []string{"new.txt", "modify.txt", "remove.txt"}
	if err := client.CommitFiles(paths, "mixed commit"); err != nil {
		t.Fatal(err)
	}

	repo, _ := gogit.PlainOpen(dir)
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())
	tree, _ := commit.Tree()

	// New file present.
	assertTreeFile(t, tree, "new.txt", "brand new")
	// Modified file updated.
	assertTreeFile(t, tree, "modify.txt", "modified content")
	// Deleted file absent.
	if _, fErr := tree.File("remove.txt"); fErr == nil {
		t.Error("remove.txt should not be in tree")
	}
	// Original test.txt untouched.
	assertTreeFile(t, tree, "test.txt", "hello world")
}

func TestCommitEngine_EmptyRepoFirstCommit(t *testing.T) {
	dir, cleanup := testRepo(t) // No initial commit.
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	writeTestFiles(t, dir, map[string]string{"first.txt": "genesis"})

	if err := client.CommitFiles([]string{"first.txt"}, "first commit"); err != nil {
		t.Fatal(err)
	}

	repo, _ := gogit.PlainOpen(dir)
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())

	// First commit has zero parents.
	if len(commit.ParentHashes) != 0 {
		t.Errorf("first commit parents = %d, want 0", len(commit.ParentHashes))
	}

	tree, _ := commit.Tree()
	assertTreeFile(t, tree, "first.txt", "genesis")
}

func TestCommitEngine_ParallelCorrectness(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Create 100 files to exercise parallel workers.
	const fileCount = 100
	files := make(map[string]string, fileCount)
	paths := make([]string, 0, fileCount)
	for i := range fileCount {
		name := filepath.Join("batch", filepath.FromSlash(
			strings.Join([]string{"d" + itoa(i/10), "f" + itoa(i) + ".txt"}, "/"),
		))
		name = filepath.ToSlash(name)
		files[name] = "content-" + itoa(i)
		paths = append(paths, name)
	}
	writeTestFiles(t, dir, files)

	if err := client.CommitFiles(paths, "batch commit"); err != nil {
		t.Fatal(err)
	}

	repo, _ := gogit.PlainOpen(dir)
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())
	tree, _ := commit.Tree()

	for _, name := range paths {
		f, fErr := tree.File(name)
		if fErr != nil {
			t.Errorf("file %s not in tree: %v", name, fErr)
			continue
		}
		content, _ := f.Contents()
		if content != files[name] {
			t.Errorf("file %s = %q, want %q", name, content, files[name])
		}
	}
}

func TestCommitEngine_IndexIntegrity(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"alpha.txt":   "a",
		"beta.txt":    "b",
		"gamma.txt":   "g",
		"sub/deep.go": "package deep",
	}
	writeTestFiles(t, dir, files)

	paths := []string{"alpha.txt", "beta.txt", "gamma.txt", "sub/deep.go"}
	if err := client.CommitFiles(paths, "index test"); err != nil {
		t.Fatal(err)
	}

	// Read index back and verify invariants.
	repo, _ := gogit.PlainOpen(dir)
	idx, idxErr := repo.Storer.Index()
	if idxErr != nil {
		t.Fatal(idxErr)
	}

	// Verify sorted order.
	sorted := slices.IsSortedFunc(idx.Entries, func(a, b *index.Entry) int {
		return strings.Compare(a.Name, b.Name)
	})
	if !sorted {
		names := make([]string, len(idx.Entries))
		for i, e := range idx.Entries {
			names[i] = e.Name
		}
		t.Errorf("index not sorted: %v", names)
	}

	// Verify all committed files are present with correct hashes.
	entryMap := make(map[string]*index.Entry, len(idx.Entries))
	for _, e := range idx.Entries {
		entryMap[e.Name] = e
	}

	for name, content := range files {
		entry, ok := entryMap[name]
		if !ok {
			t.Errorf("file %s not in index", name)
			continue
		}
		expectedHash := computeBlobHash([]byte(content))
		if entry.Hash != expectedHash {
			t.Errorf("file %s hash = %s, want %s", name, entry.Hash, expectedHash)
		}
		if entry.Stage != 0 {
			t.Errorf("file %s stage = %d, want 0", name, entry.Stage)
		}
	}
}

func TestCommitEngine_Symlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows")
	}

	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a symlink.
	target := "test.txt"
	linkPath := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}

	if err := client.CommitFiles([]string{"link.txt"}, "add symlink"); err != nil {
		t.Fatal(err)
	}

	repo, _ := gogit.PlainOpen(dir)
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())
	tree, _ := commit.Tree()

	// Verify the tree entry has Symlink mode.
	entry, entryErr := findTreeEntry(tree, "link.txt")
	if entryErr != nil {
		t.Fatal(entryErr)
	}
	if entry.Mode != filemode.Symlink {
		t.Errorf("mode = %s, want Symlink", entry.Mode)
	}

	// Verify the blob content is the symlink target.
	blob, blobErr := repo.BlobObject(entry.Hash)
	if blobErr != nil {
		t.Fatal(blobErr)
	}
	reader, _ := blob.Reader()
	data, _ := readAll(reader)
	reader.Close()
	if string(data) != target {
		t.Errorf("symlink blob = %q, want %q", string(data), target)
	}
}

func TestCommitEngine_ExecutableFile(t *testing.T) {
	if !platformHasExecBit {
		t.Skip("platform does not track executable bits")
	}

	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho hello"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := client.CommitFiles([]string{"run.sh"}, "add script"); err != nil {
		t.Fatal(err)
	}

	repo, _ := gogit.PlainOpen(dir)
	idx, _ := repo.Storer.Index()
	for _, e := range idx.Entries {
		if e.Name == "run.sh" {
			if e.Mode != filemode.Executable {
				t.Errorf("mode = %s, want Executable", e.Mode)
			}
			return
		}
	}
	t.Error("run.sh not found in index")
}

// =============================================================================
// Signature Resolution Tests
// =============================================================================

func TestResolveSignature_FromConfig(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Set user config.
	cfg, _ := repo.Config()
	cfg.User.Name = "Alice"
	cfg.User.Email = "alice@example.com"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	author, committer := resolveCommitSignature(repo, now)

	if author.Name != "Alice" || author.Email != "alice@example.com" {
		t.Errorf("author = %q <%s>, want Alice <alice@example.com>", author.Name, author.Email)
	}
	if committer.Name != "Alice" || committer.Email != "alice@example.com" {
		t.Errorf("committer = %q <%s>, want Alice <alice@example.com>", committer.Name, committer.Email)
	}
	if !author.When.Equal(now) {
		t.Errorf("author.When = %v, want %v", author.When, now)
	}
}

func TestResolveSignature_Fallback(t *testing.T) {
	// resolveCommitSignature reads GlobalScope config, which includes
	// ~/.gitconfig. We cannot control that in a unit test. Instead,
	// verify the contract: a valid, non-empty signature is always returned,
	// whether from global config or the internal default.
	dir, cleanup := testRepo(t)
	defer cleanup()

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	author, committer := resolveCommitSignature(repo, now)

	if author.Name == "" {
		t.Error("author.Name is empty")
	}
	if author.Email == "" {
		t.Error("author.Email is empty")
	}
	if !author.When.Equal(now) {
		t.Errorf("author.When = %v, want %v", author.When, now)
	}
	if committer.Name == "" {
		t.Error("committer.Name is empty")
	}
	if committer.Email == "" {
		t.Error("committer.Email is empty")
	}
	if !committer.When.Equal(now) {
		t.Errorf("committer.When = %v, want %v", committer.When, now)
	}
}

func TestResolveSignature_Sanitization(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg, _ := repo.Config()
	cfg.User.Name = "Bob <evil>\n"
	cfg.User.Email = "bob@<evil>\n.com"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	author, _ := resolveCommitSignature(repo, now)

	if strings.ContainsAny(author.Name, "<>\n") {
		t.Errorf("author.Name not sanitized: %q", author.Name)
	}
	if strings.ContainsAny(author.Email, "<>\n") {
		t.Errorf("author.Email not sanitized: %q", author.Email)
	}
}

// =============================================================================
// Hash-Skip Optimization Test
// =============================================================================

func TestCommitEngine_HashSkipUnchangedFiles(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Commit test.txt again (unchanged content). Should succeed without
	// re-storing the blob.
	if err := client.CommitFiles([]string{"test.txt"}, "re-commit unchanged"); err != nil {
		t.Fatal(err)
	}

	// Verify the tree still has the same content.
	repo, _ := gogit.PlainOpen(dir)
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())
	tree, _ := commit.Tree()
	assertTreeFile(t, tree, "test.txt", "hello world")
}

// =============================================================================
// Deduplication and Edge Cases
// =============================================================================

func TestCommitEngine_DuplicatePaths(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	writeTestFiles(t, dir, map[string]string{"dup.txt": "data"})

	// Pass same path multiple times.
	paths := []string{"dup.txt", "dup.txt", "dup.txt"}
	if err := client.CommitFiles(paths, "dedup test"); err != nil {
		t.Fatal(err)
	}

	repo, _ := gogit.PlainOpen(dir)
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())
	tree, _ := commit.Tree()
	assertTreeFile(t, tree, "dup.txt", "data")
}

func TestCommitEngine_EmptyPaths(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = client.CommitFiles(nil, "empty")
	if err == nil {
		t.Error("expected error for empty paths")
	}
}

func TestCommitEngine_NotGitRepo(t *testing.T) {
	dir, err := os.MkdirTemp("", "sylk-commit-not-repo-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = client.CommitFiles([]string{"foo.txt"}, "should fail")
	if err != ErrNotGitRepo {
		t.Errorf("error = %v, want ErrNotGitRepo", err)
	}
}

// =============================================================================
// Helper: computeBlobHash
// =============================================================================

func TestComputeBlobHash(t *testing.T) {
	content := []byte("hello world")
	hash := computeBlobHash(content)

	// Known SHA-1 of git blob "hello world":
	// printf 'blob 11\0hello world' | sha1sum = 95d09f2b10159347eece71399a7e2e907ea3df4f
	if hash.String() != "95d09f2b10159347eece71399a7e2e907ea3df4f" {
		t.Errorf("hash = %s, want 95d09f2b10159347eece71399a7e2e907ea3df4f", hash)
	}
}

func TestClampIndexFileSize(t *testing.T) {
	tests := []struct {
		size int64
		want uint32
	}{
		{0, 0},
		{100, 100},
		{1 << 32, 0},       // > uint32 max → 0
		{(1 << 32) + 1, 0}, // also > uint32 max
		{(1 << 32) - 1, (1 << 32) - 1},
	}
	for _, tt := range tests {
		got := clampIndexFileSize(tt.size)
		if got != tt.want {
			t.Errorf("clampIndexFileSize(%d) = %d, want %d", tt.size, got, tt.want)
		}
	}
}

func TestDeduplicatePaths(t *testing.T) {
	input := []string{"b.txt", "a.txt", "b.txt", "c.txt", "a.txt"}
	got := deduplicatePaths(input)
	want := []string{"b.txt", "a.txt", "c.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("deduplicatePaths = %v, want %v", got, want)
	}
}

func TestMergeSortedEntries(t *testing.T) {
	mkEntry := func(name string) *index.Entry {
		return &index.Entry{Name: name}
	}

	sorted := []*index.Entry{mkEntry("a"), mkEntry("c"), mkEntry("e")}
	additional := []*index.Entry{mkEntry("b"), mkEntry("d")}

	merged := mergeSortedEntries(sorted, additional)

	names := make([]string, len(merged))
	for i, e := range merged {
		names[i] = e.Name
	}
	want := []string{"a", "b", "c", "d", "e"}
	if !slices.Equal(names, want) {
		t.Errorf("merged = %v, want %v", names, want)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkCommitEngine measures commit throughput at varying file counts.
// Each sub-benchmark creates a fresh repo, writes N files, then times
// a single CommitFiles call.
func BenchmarkCommitEngine(b *testing.B) {
	for _, fileCount := range []int{10, 100, 500} {
		b.Run(itoa(fileCount)+"_files", func(b *testing.B) {
			b.StopTimer()
			for range b.N {
				dir, err := os.MkdirTemp("", "sylk-bench-*")
				if err != nil {
					b.Fatal(err)
				}
				repo, err := gogit.PlainInit(dir, false)
				if err != nil {
					b.Fatal(err)
				}
				// Seed with one commit so HEAD exists.
				os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init"), 0o644)
				wt, _ := repo.Worktree()
				wt.Add("init.txt")
				wt.Commit("init", &gogit.CommitOptions{
					Author: &object.Signature{Name: "B", Email: "b@b", When: time.Now()},
				})

				paths := make([]string, 0, fileCount)
				for j := range fileCount {
					name := "d" + itoa(j/25) + "/f" + itoa(j) + ".txt"
					p := filepath.Join(dir, filepath.FromSlash(name))
					os.MkdirAll(filepath.Dir(p), 0o755)
					os.WriteFile(p, []byte("content-"+itoa(j)+strings.Repeat("x", 200)), 0o644)
					paths = append(paths, name)
				}

				client, cErr := NewGitClient(dir)
				if cErr != nil {
					b.Fatal(cErr)
				}

				b.StartTimer()
				if err := client.CommitFiles(paths, "bench"); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()

				os.RemoveAll(dir)
			}
		})
	}
}

// =============================================================================
// Test Helpers
// =============================================================================

// writeTestFiles creates files relative to dir, including parent directories.
func writeTestFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// assertTreeFile verifies a file exists in the tree with the expected content.
func assertTreeFile(t *testing.T, tree *object.Tree, name, want string) {
	t.Helper()
	f, err := tree.File(name)
	if err != nil {
		t.Errorf("file %s not in tree: %v", name, err)
		return
	}
	content, err := f.Contents()
	if err != nil {
		t.Errorf("file %s read error: %v", name, err)
		return
	}
	if content != want {
		t.Errorf("file %s = %q, want %q", name, content, want)
	}
}

// findTreeEntry searches for a named entry in the tree (top-level only).
func findTreeEntry(tree *object.Tree, name string) (*object.TreeEntry, error) {
	for _, e := range tree.Entries {
		if e.Name == name {
			return &e, nil
		}
	}
	return nil, os.ErrNotExist
}

// readAll reads all bytes from r.
func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}

// itoa converts int to string without importing strconv in tests.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
