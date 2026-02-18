package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/epiclabs-io/diff3"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

// =============================================================================
// Errors
// =============================================================================

// ErrMergeConflict indicates the merge produced conflicts.
var ErrMergeConflict = errors.New("merge conflict")

// =============================================================================
// Types
// =============================================================================

// TreeMergeResult holds the outcome of a 3-way tree merge.
type TreeMergeResult struct {
	// TreeHash is the hash of the merged tree stored in the object database.
	TreeHash plumbing.Hash

	// Conflicts lists files that could not be cleanly merged.
	Conflicts []MergeConflict
}

// HasConflicts reports whether the merge produced any conflicts.
func (r *TreeMergeResult) HasConflicts() bool {
	return len(r.Conflicts) > 0
}

// MergeConflict describes a single file conflict from a 3-way merge.
type MergeConflict struct {
	// Path is the file path relative to the repository root.
	Path string

	// Type classifies the conflict.
	Type ConflictType

	// BaseHash is the blob hash in the common ancestor (ZeroHash if absent).
	BaseHash plumbing.Hash

	// OursHash is the blob hash on our side (ZeroHash if absent).
	OursHash plumbing.Hash

	// TheirsHash is the blob hash on their side (ZeroHash if absent).
	TheirsHash plumbing.Hash

	// MergedContent contains the merge result with conflict markers
	// (only for content conflicts, empty for delete/modify).
	MergedContent string
}

// ConflictType classifies a merge conflict.
type ConflictType int

const (
	// ContentConflict means both sides modified the file differently
	// and the 3-way text merge could not resolve automatically.
	ContentConflict ConflictType = iota

	// DeleteModifyConflict means one side deleted the file while
	// the other modified it.
	DeleteModifyConflict

	// BothAddedConflict means both sides added a file at the same path
	// with different content.
	BothAddedConflict

	// BinaryConflict means a binary file was modified on both sides.
	BinaryConflict
)

// =============================================================================
// 3-Way Tree Merge
// =============================================================================

// treeMerge3 performs a 3-way merge between base, ours, and theirs trees.
//
// Algorithm:
//  1. Compute DiffTree(base, ours) → oursChanges
//  2. Compute DiffTree(base, theirs) → theirsChanges
//  3. Index both change sets by path
//  4. For each unique path:
//     - Only one side changed → take that side's version
//     - Both changed to same hash → take either (identical)
//     - Both deleted → omit from result
//     - One deleted, one modified → delete/modify conflict
//     - Both modified differently → 3-way text merge via diff3
//     - Both added same hash → take it
//     - Both added different hash → 3-way merge with empty base
//  5. Construct and store the merged tree
//
// The result tree is always stored in the object database. Conflicts are
// reported but the tree still contains the conflict-marker versions so
// callers can write them to the worktree.
func treeMerge3(s storage.Storer, base, ours, theirs *object.Tree) (*TreeMergeResult, error) {
	ctx := context.Background()

	oursChanges, err := diffTreeSafe(ctx, base, ours)
	if err != nil {
		return nil, err
	}

	theirsChanges, err := diffTreeSafe(ctx, base, theirs)
	if err != nil {
		return nil, err
	}

	// Index changes by path.
	oursMap, err := indexChangesByPath(oursChanges)
	if err != nil {
		return nil, err
	}

	theirsMap, err := indexChangesByPath(theirsChanges)
	if err != nil {
		return nil, err
	}

	// Collect all unique changed paths.
	allPaths := mergeKeys(oursMap, theirsMap)

	// Start with base tree entries, then apply merge decisions.
	builder := newTreeBuilder(s, base)

	var conflicts []MergeConflict

	for _, path := range allPaths {
		oc, oursChanged := oursMap[path]
		tc, theirsChanged := theirsMap[path]

		switch {
		case oursChanged && !theirsChanged:
			builder.apply(oc)

		case !oursChanged && theirsChanged:
			builder.apply(tc)

		case oursChanged && theirsChanged:
			c, err := mergeDoubleChange(s, base, oc, tc, path, builder)
			if err != nil {
				return nil, err
			}
			if c != nil {
				conflicts = append(conflicts, *c)
			}
		}
	}

	treeHash, err := builder.build()
	if err != nil {
		return nil, err
	}

	return &TreeMergeResult{
		TreeHash:  treeHash,
		Conflicts: conflicts,
	}, nil
}

// =============================================================================
// Change Resolution
// =============================================================================

// mergeDoubleChange handles the case where both ours and theirs modified
// the same path. Returns nil conflict if cleanly resolved. On clean merge,
// applies the result to the builder. On conflict, stores conflict-marked
// content in the builder and returns the conflict descriptor.
func mergeDoubleChange(s storage.Storer, base *object.Tree, oc, tc *changeAction, path string, builder *treeBuilder) (*MergeConflict, error) {
	// Both deleted — clean, no builder action needed.
	if oc.action == merkletrie.Delete && tc.action == merkletrie.Delete {
		return nil, nil
	}

	oursHash := actionToHash(oc)
	theirsHash := actionToHash(tc)

	// One deleted, the other modified/inserted — delete/modify conflict.
	if oc.action == merkletrie.Delete || tc.action == merkletrie.Delete {
		return deleteModifyConflict(base, path, oursHash, theirsHash), nil
	}

	// Both changed to identical result — take ours, clean.
	if oursHash == theirsHash {
		builder.apply(oc)
		return nil, nil
	}

	// Different changes — attempt 3-way text merge.
	return textMerge3(s, base, oc, path, oursHash, theirsHash, builder)
}

// deleteModifyConflict constructs a MergeConflict for delete/modify scenarios.
func deleteModifyConflict(base *object.Tree, path string, oursHash, theirsHash plumbing.Hash) *MergeConflict {
	return &MergeConflict{
		Path:       path,
		Type:       DeleteModifyConflict,
		BaseHash:   blobHashFromEntry(base, path),
		OursHash:   oursHash,
		TheirsHash: theirsHash,
	}
}

// textMerge3 performs a 3-way text merge for a single file where both sides
// modified differently. On clean merge, stores the result and applies to
// builder. On conflict, stores conflict-marked content and returns the conflict.
func textMerge3(s storage.Storer, base *object.Tree, oc *changeAction, path string, oursHash, theirsHash plumbing.Hash, builder *treeBuilder) (*MergeConflict, error) {
	baseContent, err := treeFileContent(base, path)
	if err != nil {
		return nil, err
	}

	oursContent, err := readBlobContent(s, oursHash)
	if err != nil {
		return nil, fmt.Errorf("read ours blob %s: %w", path, err)
	}

	theirsContent, err := readBlobContent(s, theirsHash)
	if err != nil {
		return nil, fmt.Errorf("read theirs blob %s: %w", path, err)
	}

	if isBinary(baseContent) || isBinary(oursContent) || isBinary(theirsContent) {
		return &MergeConflict{
			Path:       path,
			Type:       BinaryConflict,
			BaseHash:   blobHashFromEntry(base, path),
			OursHash:   oursHash,
			TheirsHash: theirsHash,
		}, nil
	}

	merged, hasConflicts, err := diff3Merge(oursContent, baseContent, theirsContent)
	if err != nil {
		return nil, err
	}

	baseHash := blobHashFromEntry(base, path)

	if hasConflicts {
		mergedStr := string(merged)
		hash, err := storeBlob(s, merged)
		if err != nil {
			return nil, err
		}
		builder.set(path, hash, filemode.Regular)
		return &MergeConflict{
			Path:          path,
			Type:          ContentConflict,
			BaseHash:      baseHash,
			OursHash:      oursHash,
			TheirsHash:    theirsHash,
			MergedContent: mergedStr,
		}, nil
	}

	// Clean merge — store blob and apply to builder.
	mergedHash, err := storeBlob(s, merged)
	if err != nil {
		return nil, err
	}
	oc.mergedHash = &mergedHash
	builder.apply(oc)
	return nil, nil
}

// diff3Merge performs a 3-way text merge and returns the merged bytes and
// whether conflicts were detected.
func diff3Merge(ours, base, theirs string) ([]byte, bool, error) {
	result, err := diff3.Merge(
		strings.NewReader(ours),
		strings.NewReader(base),
		strings.NewReader(theirs),
		true, "ours", "theirs",
	)
	if err != nil {
		return nil, false, err
	}

	merged, err := io.ReadAll(result.Result)
	if err != nil {
		return nil, false, err
	}

	return merged, result.Conflicts, nil
}

// readBlobContent reads blob content by hash. Returns an error if the blob
// cannot be read (unlike readBlobByHash in diff.go which silently returns "").
func readBlobContent(s storer.EncodedObjectStorer, hash plumbing.Hash) (string, error) {
	blob, err := object.GetBlob(s, hash)
	if err != nil {
		return "", fmt.Errorf("get blob: %w", err)
	}
	reader, err := blob.Reader()
	if err != nil {
		return "", fmt.Errorf("blob reader: %w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("read blob: %w", err)
	}
	return string(data), nil
}

// =============================================================================
// Change Indexing
// =============================================================================

// changeAction wraps a Change with its pre-computed action and the
// optional merged blob hash (set during conflict resolution).
type changeAction struct {
	change     *object.Change
	action     merkletrie.Action
	mergedHash *plumbing.Hash // non-nil after clean 3-way merge
}

// indexChangesByPath creates a map from file path to changeAction.
func indexChangesByPath(changes object.Changes) (map[string]*changeAction, error) {
	m := make(map[string]*changeAction, len(changes))
	for _, ch := range changes {
		path := changePath(ch)
		action, err := ch.Action()
		if err != nil {
			return nil, fmt.Errorf("action for %s: %w", path, err)
		}
		m[path] = &changeAction{change: ch, action: action}
	}
	return m, nil
}

// mergeKeys returns the sorted union of keys from two maps.
func mergeKeys(a, b map[string]*changeAction) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}

	slices.Sort(keys)
	return keys
}

// =============================================================================
// Tree Builder
// =============================================================================

// treeBuilder accumulates merge decisions and produces a final tree.
// It starts from the base tree and applies changes from ours/theirs.
type treeBuilder struct {
	s    storage.Storer
	base *object.Tree

	// modifications maps path → (hash, mode). ZeroHash means delete.
	modifications map[string]treeMod
}

type treeMod struct {
	hash plumbing.Hash
	mode filemode.FileMode
}

func newTreeBuilder(s storage.Storer, base *object.Tree) *treeBuilder {
	return &treeBuilder{
		s:             s,
		base:          base,
		modifications: make(map[string]treeMod),
	}
}

// apply records a change action in the builder.
func (b *treeBuilder) apply(ca *changeAction) {
	path := changePath(ca.change)

	// If a clean 3-way merge produced a result, use that.
	if ca.mergedHash != nil {
		mode := entryMode(ca.change)
		b.modifications[path] = treeMod{hash: *ca.mergedHash, mode: mode}
		return
	}

	switch ca.action {
	case merkletrie.Delete:
		b.modifications[path] = treeMod{hash: plumbing.ZeroHash}
	case merkletrie.Insert, merkletrie.Modify:
		b.modifications[path] = treeMod{
			hash: ca.change.To.TreeEntry.Hash,
			mode: ca.change.To.TreeEntry.Mode,
		}
	}
}

// set records a direct hash/mode for a path.
func (b *treeBuilder) set(path string, hash plumbing.Hash, mode filemode.FileMode) {
	b.modifications[path] = treeMod{hash: hash, mode: mode}
}

// build constructs the final tree from base + modifications.
func (b *treeBuilder) build() (plumbing.Hash, error) {
	if len(b.modifications) == 0 {
		if b.base != nil {
			return b.base.Hash, nil
		}
		return storeTree(b.s, nil)
	}

	// Convert treeMod map to the format modifyTree expects,
	// plus handle mode changes.
	hashMods := make(map[string]plumbing.Hash, len(b.modifications))
	for path, mod := range b.modifications {
		hashMods[path] = mod.hash
	}

	if b.base == nil {
		// No base tree — build from scratch using only insertions.
		return buildTreeFromMods(b.s, hashMods)
	}

	return modifyTree(b.s, b.base, hashMods)
}

// buildTreeFromMods creates a tree from a flat map of path→hash entries.
// Used when the base tree is nil (e.g., initial commit).
func buildTreeFromMods(s storage.Storer, mods map[string]plumbing.Hash) (plumbing.Hash, error) {
	entries := make([]object.TreeEntry, 0, len(mods))
	for path, hash := range mods {
		if hash == plumbing.ZeroHash {
			continue
		}
		entries = append(entries, object.TreeEntry{
			Name: path,
			Mode: filemode.Regular,
			Hash: hash,
		})
	}
	return storeTree(s, entries)
}

// =============================================================================
// Helpers
// =============================================================================

// diffTreeSafe wraps DiffTreeWithOptions with nil-safe tree handling.
// A nil tree is treated as an empty tree.
func diffTreeSafe(ctx context.Context, a, b *object.Tree) (object.Changes, error) {
	return object.DiffTreeWithOptions(ctx, a, b, nil)
}

// actionToHash returns the blob hash for the "result" side of a change.
// For deletions, returns ZeroHash.
func actionToHash(ca *changeAction) plumbing.Hash {
	switch ca.action {
	case merkletrie.Delete:
		return plumbing.ZeroHash
	default:
		return ca.change.To.TreeEntry.Hash
	}
}

// entryMode returns the filemode for the destination side of a change.
func entryMode(ch *object.Change) filemode.FileMode {
	if ch.To.TreeEntry.Mode != 0 {
		return ch.To.TreeEntry.Mode
	}
	return ch.From.TreeEntry.Mode
}

// blobHashFromEntry returns the blob hash of a file in a tree.
// Returns ZeroHash if the file is not found or tree is nil.
func blobHashFromEntry(tree *object.Tree, path string) plumbing.Hash {
	if tree == nil {
		return plumbing.ZeroHash
	}
	entry, err := entryByPath(tree, path)
	if err != nil {
		return plumbing.ZeroHash
	}
	return entry.Hash
}

// entryByPath finds a tree entry by its slash-separated path,
// using the tree's FindEntry method which handles subdirectories.
func entryByPath(tree *object.Tree, path string) (*object.TreeEntry, error) {
	return tree.FindEntry(path)
}

// treeFileContent reads a file's content from a tree. Returns ""
// if the file doesn't exist or the tree is nil.
func treeFileContent(tree *object.Tree, path string) (string, error) {
	if tree == nil {
		return "", nil
	}
	f, err := tree.File(path)
	if err != nil {
		// File doesn't exist in this tree — treat as empty for merge base.
		return "", nil
	}
	return f.Contents()
}

// writeConflictsToWorktree writes conflict-marked files to the working
// directory so the user can manually resolve them.
func (c *GitClient) writeConflictsToWorktree(conflicts []MergeConflict) error {
	for _, cf := range conflicts {
		if cf.MergedContent == "" {
			continue // Delete/modify or binary — no merged content to write.
		}
		fullPath := filepath.Join(c.repoPath, cf.Path)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(cf.MergedContent), 0o644); err != nil {
			return err
		}
	}
	return nil
}
