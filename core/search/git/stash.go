package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage"
)

// =============================================================================
// Stash Operations
// =============================================================================

// StashFiles stashes the specified files using go-git plumbing.
// Creates the standard git stash commit structure (worktree commit with
// parents=[HEAD, indexCommit]) and resets the specified files to HEAD state.
// If no paths are given, the call is a no-op.
func (c *GitClient) StashFiles(paths []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}
	if len(paths) == 0 {
		return nil
	}

	headRef, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}

	headCommit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return err
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return err
	}

	branchName := extractBranchName(headRef)
	shortHash := headRef.Hash().String()[:7]
	now := time.Now()
	sig := defaultSignature(now)

	// Build index tree from full index.
	idx, err := c.repo.Storer.Index()
	if err != nil {
		return err
	}

	indexTreeHash, err := buildTreeFromIndex(c.repo.Storer, idx)
	if err != nil {
		return fmt.Errorf("build index tree: %w", err)
	}

	// Create index commit (parent: HEAD, tree: full index state).
	indexMsg := fmt.Sprintf("index on %s: %s", branchName, shortHash)
	indexCommitHash, err := storeCommitObj(
		c.repo.Storer, indexTreeHash,
		[]plumbing.Hash{headRef.Hash()},
		sig, sig, indexMsg,
	)
	if err != nil {
		return fmt.Errorf("create index commit: %w", err)
	}

	// Build worktree tree: HEAD tree with stashed paths replaced by disk content.
	wtMods := make(map[string]plumbing.Hash, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(filepath.Join(c.repoPath, p))
		if err != nil {
			// File deleted from disk — mark for removal by omitting from tree.
			wtMods[p] = plumbing.ZeroHash
			continue
		}
		hash, err := storeBlob(c.repo.Storer, data)
		if err != nil {
			return fmt.Errorf("store blob %s: %w", p, err)
		}
		wtMods[p] = hash
	}

	wtTreeHash, err := modifyTree(c.repo.Storer, headTree, wtMods)
	if err != nil {
		return fmt.Errorf("build worktree tree: %w", err)
	}

	// Create worktree commit (parents: [HEAD, indexCommit]).
	wtMsg := fmt.Sprintf("WIP on %s: %s", branchName, shortHash)
	wtCommitHash, err := storeCommitObj(
		c.repo.Storer, wtTreeHash,
		[]plumbing.Hash{headRef.Hash(), indexCommitHash},
		sig, sig, wtMsg,
	)
	if err != nil {
		return fmt.Errorf("create worktree commit: %w", err)
	}

	// Update refs/stash and reflog.
	oldStashHash := resolveStashHash(c.repo)
	stashRef := plumbing.NewHashReference(
		plumbing.ReferenceName("refs/stash"), wtCommitHash,
	)
	if err := c.repo.Storer.SetReference(stashRef); err != nil {
		return fmt.Errorf("set refs/stash: %w", err)
	}

	gitDir, err := resolveGitDir(c)
	if err != nil {
		return err
	}
	if err := appendStashReflog(gitDir, oldStashHash, wtCommitHash, sig, wtMsg); err != nil {
		return fmt.Errorf("write stash reflog: %w", err)
	}

	// Reset stashed files in the worktree to HEAD state.
	return c.resetPathsToTree(headTree, paths)
}

// UnstashFiles pops the most recent stash entry, restoring files to the
// working tree and removing the stash.
func (c *GitClient) UnstashFiles() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	stashRef, err := c.repo.Reference(plumbing.ReferenceName("refs/stash"), true)
	if err != nil {
		return fmt.Errorf("no stash found: %w", err)
	}

	stashCommit, err := c.repo.CommitObject(stashRef.Hash())
	if err != nil {
		return err
	}

	// The stash worktree commit's first parent is the HEAD at stash time.
	// Diff the stash tree against that parent to find what files changed.
	if len(stashCommit.ParentHashes) == 0 {
		return fmt.Errorf("malformed stash commit: no parents")
	}

	baseCommit, err := c.repo.CommitObject(stashCommit.ParentHashes[0])
	if err != nil {
		return err
	}

	baseTree, err := baseCommit.Tree()
	if err != nil {
		return err
	}

	stashTree, err := stashCommit.Tree()
	if err != nil {
		return err
	}

	changes, err := object.DiffTree(baseTree, stashTree)
	if err != nil {
		return err
	}

	// Restore each changed file to its stash state on disk.
	for _, ch := range changes {
		if err := c.applyChange(ch, stashTree); err != nil {
			return fmt.Errorf("apply stash change: %w", err)
		}
	}

	// Pop the stash: read reflog, remove last entry, update/remove ref.
	gitDir, err := resolveGitDir(c)
	if err != nil {
		return err
	}

	return popStashRef(c.repo.Storer, gitDir)
}

// HasStash reports whether the stash list is non-empty.
func (c *GitClient) HasStash() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return false
	}

	_, err := c.repo.Reference(plumbing.ReferenceName("refs/stash"), true)
	return err == nil
}

// =============================================================================
// Stash Helpers
// =============================================================================

// applyChange writes a single stash change to the working directory.
func (c *GitClient) applyChange(ch *object.Change, stashTree *object.Tree) error {
	path := changePath(ch)
	fullPath := filepath.Join(c.repoPath, path)

	// Deletion: stash recorded a file removal.
	if ch.To.Name == "" {
		err := os.Remove(fullPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil // Already absent — no-op.
		}
		return err
	}

	// Addition or modification: write blob content to disk.
	f, err := stashTree.File(ch.To.Name)
	if err != nil {
		return err
	}

	content, err := f.Contents()
	if err != nil {
		return err
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	return os.WriteFile(fullPath, []byte(content), 0o644)
}

// resetPathsToTree restores specified paths in the working directory to
// their state in the given tree. Also updates the index.
func (c *GitClient) resetPathsToTree(tree *object.Tree, paths []string) error {
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	for _, p := range paths {
		if err := c.resetSinglePath(wt, tree, p); err != nil {
			return fmt.Errorf("reset %s: %w", p, err)
		}
	}

	return nil
}

// resetSinglePath restores a single file to its state in the given tree.
func (c *GitClient) resetSinglePath(wt *gogit.Worktree, tree *object.Tree, path string) error {
	fullPath := filepath.Join(c.repoPath, path)

	f, err := tree.File(path)
	if err != nil {
		// File didn't exist in HEAD — remove it from disk and index.
		rmErr := os.Remove(fullPath)
		if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return rmErr
		}
		return nil
	}

	content, err := f.Contents()
	if err != nil {
		return err
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return err
	}

	// Update index to match HEAD.
	if _, err := wt.Add(path); err != nil {
		return fmt.Errorf("index add: %w", err)
	}

	return nil
}

// resolveStashHash returns the current refs/stash hash, or ZeroHash if none.
func resolveStashHash(repo *gogit.Repository) plumbing.Hash {
	ref, err := repo.Reference(plumbing.ReferenceName("refs/stash"), true)
	if err != nil {
		return plumbing.ZeroHash
	}
	return ref.Hash()
}

// appendStashReflog appends an entry to the stash reflog file.
func appendStashReflog(gitDir string, oldHash, newHash plumbing.Hash, sig object.Signature, msg string) error {
	logPath := filepath.Join(gitDir, "logs", "refs", "stash")

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}

	entry := oldHash.String() + " " + newHash.String() +
		" " + sig.Name + " <" + sig.Email + "> " +
		strconv.FormatInt(sig.When.Unix(), 10) + " " +
		formatTZOffset(sig.When) + "\t" + msg + "\n"

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(entry)
	return err
}

// formatTZOffset returns the timezone offset in git reflog format (e.g. "+0530", "-0800").
func formatTZOffset(t time.Time) string {
	_, offset := t.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	hours := offset / 3600
	minutes := (offset % 3600) / 60
	return sign + fmt.Sprintf("%02d%02d", hours, minutes)
}

// popStashRef removes the most recent stash entry. If no previous entries
// remain, refs/stash is deleted entirely.
func popStashRef(s storage.Storer, gitDir string) error {
	logPath := filepath.Join(gitDir, "logs", "refs", "stash")

	data, err := os.ReadFile(logPath)
	if err != nil {
		// No reflog — just remove the ref.
		return s.RemoveReference(plumbing.ReferenceName("refs/stash"))
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= 1 {
		// Last entry — remove everything.
		_ = os.Remove(logPath)
		return s.RemoveReference(plumbing.ReferenceName("refs/stash"))
	}

	// Remove last line, update reflog.
	lines = lines[:len(lines)-1]
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}

	// Update refs/stash to the previous entry's new hash.
	lastLine := lines[len(lines)-1]
	fields := strings.Fields(lastLine)
	if len(fields) < 2 {
		return s.RemoveReference(plumbing.ReferenceName("refs/stash"))
	}

	prevHash := plumbing.NewHash(fields[1])
	ref := plumbing.NewHashReference(plumbing.ReferenceName("refs/stash"), prevHash)
	return s.SetReference(ref)
}

// =============================================================================
// Plumbing Helpers (shared with merge3, cherrypick, rebase)
// =============================================================================

// storeBlob writes content as a blob object and returns its hash.
func storeBlob(s storage.Storer, content []byte) (plumbing.Hash, error) {
	obj := s.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)

	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	if _, err := w.Write(content); err != nil {
		w.Close()
		return plumbing.ZeroHash, err
	}

	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}

	return s.SetEncodedObject(obj)
}

// storeTree encodes a sorted list of tree entries as a tree object.
func storeTree(s storage.Storer, entries []object.TreeEntry) (plumbing.Hash, error) {
	sort.Sort(object.TreeEntrySorter(entries))

	tree := &object.Tree{Entries: entries}
	obj := s.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}

	return s.SetEncodedObject(obj)
}

// storeCommitObj creates and stores a commit object.
func storeCommitObj(s storage.Storer, treeHash plumbing.Hash, parents []plumbing.Hash, author, committer object.Signature, msg string) (plumbing.Hash, error) {
	commit := &object.Commit{
		TreeHash:     treeHash,
		ParentHashes: parents,
		Author:       author,
		Committer:    committer,
		Message:      msg,
	}

	obj := s.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}

	return s.SetEncodedObject(obj)
}

// modifyTree creates a new tree based on 'base' with the specified
// modifications applied. Keys in modifications are slash-separated paths.
// A ZeroHash value means the path should be removed from the tree.
// Handles nested directories recursively.
func modifyTree(s storage.Storer, base *object.Tree, modifications map[string]plumbing.Hash) (plumbing.Hash, error) {
	if len(modifications) == 0 {
		return base.Hash, nil
	}

	// Partition modifications into direct entries and subdirectory entries.
	fileMods := make(map[string]plumbing.Hash)
	dirMods := make(map[string]map[string]plumbing.Hash)

	for path, hash := range modifications {
		dir, rest, hasSep := strings.Cut(path, "/")
		if !hasSep {
			fileMods[dir] = hash
		} else {
			if dirMods[dir] == nil {
				dirMods[dir] = make(map[string]plumbing.Hash)
			}
			dirMods[dir][rest] = hash
		}
	}

	entries := make([]object.TreeEntry, 0, len(base.Entries))

	for _, entry := range base.Entries {
		if hash, ok := fileMods[entry.Name]; ok {
			if hash == plumbing.ZeroHash {
				continue // Deletion: omit from new tree.
			}
			entries = append(entries, object.TreeEntry{
				Name: entry.Name,
				Mode: entry.Mode,
				Hash: hash,
			})
			delete(fileMods, entry.Name)
			continue
		}

		if mods, ok := dirMods[entry.Name]; ok {
			subtree, err := object.GetTree(s, entry.Hash)
			if err != nil {
				return plumbing.ZeroHash, fmt.Errorf("get subtree %s: %w", entry.Name, err)
			}

			newHash, err := modifyTree(s, subtree, mods)
			if err != nil {
				return plumbing.ZeroHash, err
			}

			entries = append(entries, object.TreeEntry{
				Name: entry.Name,
				Mode: entry.Mode,
				Hash: newHash,
			})
			continue
		}

		entries = append(entries, entry)
	}

	// Add new files not in the base tree.
	for name, hash := range fileMods {
		if hash == plumbing.ZeroHash {
			continue
		}
		entries = append(entries, object.TreeEntry{
			Name: name,
			Mode: filemode.Regular,
			Hash: hash,
		})
	}

	return storeTree(s, entries)
}

// buildTreeFromIndex constructs a tree object from the full index state.
func buildTreeFromIndex(s storage.Storer, idx *index.Index) (plumbing.Hash, error) {
	return buildTreeFromFlatEntries(s, idx.Entries)
}

// buildTreeFromFlatEntries builds a hierarchical tree from flat index entries.
// Each entry.Name is a slash-separated path.
func buildTreeFromFlatEntries(s storage.Storer, entries []*index.Entry) (plumbing.Hash, error) {
	type dirBucket struct {
		entries []*index.Entry
	}

	var treeEntries []object.TreeEntry
	subdirs := make(map[string]*dirBucket)

	for _, entry := range entries {
		dir, rest, hasSep := strings.Cut(entry.Name, "/")
		if !hasSep {
			treeEntries = append(treeEntries, object.TreeEntry{
				Name: dir,
				Mode: entry.Mode,
				Hash: entry.Hash,
			})
		} else {
			if subdirs[dir] == nil {
				subdirs[dir] = &dirBucket{}
			}
			clone := *entry
			clone.Name = rest
			subdirs[dir].entries = append(subdirs[dir].entries, &clone)
		}
	}

	for dir, bucket := range subdirs {
		subHash, err := buildTreeFromFlatEntries(s, bucket.entries)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		treeEntries = append(treeEntries, object.TreeEntry{
			Name: dir,
			Mode: filemode.Dir,
			Hash: subHash,
		})
	}

	return storeTree(s, treeEntries)
}

// defaultSignature returns a Signature for internal operations.
func defaultSignature(when time.Time) object.Signature {
	return object.Signature{
		Name:  "sylk",
		Email: "sylk@localhost",
		When:  when,
	}
}
