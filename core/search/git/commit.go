package git

import (
	"compress/zlib"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage"
	"golang.org/x/sync/errgroup"
)

// =============================================================================
// Batch Commit Engine
// =============================================================================
//
// commitEngine replaces per-file go-git Worktree.Add() + Worktree.Commit()
// with plumbing-level batch operations:
//   - Single index parse + single index write (not N of each)
//   - Fully parallel blob storage via direct loose-object writes
//   - O(1) entry lookup via map (not O(entries) linear scan)
//   - Zero full-tree status scans (not N merkletrie diffs)
//   - Hash-skip: skip blob store when content hash matches existing entry
//   - Skip-existing: tree objects already in the store are not rewritten
//
// For N files in an index of size M, this is O(N + M) vs go-git's O(N² × M).

// commitWorkersMax bounds the blob-staging worker pool.
// Derived: beyond 16 workers the kernel VFS lock and inode allocation
// become the bottleneck on ext4/APFS, not CPU or disk bandwidth.
const commitWorkersMax = 16

// maxBlobReadSize caps individual file reads during commit staging.
// Git's index stores file size as uint32 — beyond 4 GiB the size wraps to 0.
// 256 MiB is a practical ceiling for in-memory blob hashing.
const maxBlobReadSize = 256 << 20

// invalidSignatureRe matches characters that must be stripped from
// commit author/committer Name and Email fields.
// Matches go-git's invalidCharactersRe exactly.
var invalidSignatureRe = regexp.MustCompile(`[<>\n]`)

// commitEngine performs a batch add+commit using plumbing operations.
// The caller must hold GitClient.mu.Lock() and verify isRepo.
type commitEngine struct {
	repoPath   string
	objectsDir string // .git/objects/ — for direct parallel blob writes
	storer     storage.Storer
	repo       *gogit.Repository
}

// stagedFile holds the result of staging a single file.
type stagedFile struct {
	relPath string
	hash    plumbing.Hash
	mode    filemode.FileMode
	size    uint32
	modTime time.Time
	info    fs.FileInfo // retained for fillEntrySystemInfo
	deleted bool
}

func newCommitEngine(c *GitClient) (*commitEngine, error) {
	gitDir, err := resolveGitDir(c)
	if err != nil {
		return nil, fmt.Errorf("resolve git dir: %w", err)
	}
	return &commitEngine{
		repoPath:   c.repoPath,
		objectsDir: filepath.Join(gitDir, "objects"),
		storer:     c.repo.Storer,
		repo:       c.repo,
	}, nil
}

// execute stages the given paths and creates a commit.
//
// Flow:
//  1. Resolve signature from git config (Author → Committer → User → default)
//  2. Resolve HEAD for parent hash (no HEAD = first commit, zero parents)
//  3. Parse index once, build O(1) entry lookup
//  4. Parallel blob staging: read + hash + write loose objects directly
//  5. Batch-update index in memory (update, insert, delete, conflict resolve)
//  6. Write index once
//  7. Build tree from full index (skip-existing tree objects)
//  8. Create commit object + update HEAD
func (e *commitEngine) execute(paths []string, message string) error {
	if len(paths) == 0 {
		return errors.New("commit: no paths specified")
	}

	// Normalize and deduplicate paths.
	paths = deduplicatePaths(paths)

	now := time.Now()
	author, committer := resolveCommitSignature(e.repo, now)

	// Resolve HEAD for parent hash. No HEAD → first commit (zero parents).
	var parents []plumbing.Hash
	headRef, headErr := e.repo.Head()
	if headErr == nil {
		parents = []plumbing.Hash{headRef.Hash()}
	} else if !errors.Is(headErr, plumbing.ErrReferenceNotFound) {
		return fmt.Errorf("resolve HEAD: %w", headErr)
	}

	// Parse index once — used for hash-skip and as update base.
	idx, err := e.storer.Index()
	if err != nil {
		idx = &index.Index{Version: 2}
	}

	// Build O(1) lookup: name → slice index (stage-0 entries only).
	entryLookup := make(map[string]int, len(idx.Entries))
	for i, entry := range idx.Entries {
		if entry.Stage == 0 {
			entryLookup[entry.Name] = i
		}
	}

	// Stage files in parallel with hash-skip optimization.
	staged, err := e.batchStageFiles(paths, idx, entryLookup)
	if err != nil {
		return err
	}

	// Batch-update index entries in memory, write once.
	applyStaged(idx, staged, entryLookup)
	if err := e.storer.SetIndex(idx); err != nil {
		return fmt.Errorf("write index: %w", err)
	}

	// Build tree from full index state, skipping tree objects already in store.
	treeHash, err := buildTreeSkipExisting(e.storer, idx)
	if err != nil {
		return fmt.Errorf("build tree: %w", err)
	}

	// Create commit object.
	commitHash, err := storeCommitObj(e.storer, treeHash, parents, author, committer, message)
	if err != nil {
		return fmt.Errorf("store commit: %w", err)
	}

	return e.updateHEAD(commitHash)
}

// =============================================================================
// Parallel Blob Staging — Direct Loose Object Writes
// =============================================================================

// batchStageFiles processes all paths through a bounded worker pool.
// Each worker: Lstat → ReadFile → computeBlobHash → skip or writeLooseBlob.
// Blob writes bypass go-git's non-thread-safe DotGit entirely, writing
// zlib-compressed loose objects directly to .git/objects/. This is fully
// parallel-safe because each object has a unique hash-derived path.
// Returns on first error, cancelling remaining workers immediately.
func (e *commitEngine) batchStageFiles(
	paths []string,
	idx *index.Index,
	entryLookup map[string]int,
) (map[string]stagedFile, error) {
	workers := min(runtime.GOMAXPROCS(0), len(paths), commitWorkersMax)

	results := make([]stagedFile, len(paths))

	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(workers)

	for i, p := range paths {
		if ctx.Err() != nil {
			break // Context cancelled — stop launching new work.
		}

		// Resolve existing entry for hash-skip (read-only, no race).
		var existing *index.Entry
		if j, ok := entryLookup[p]; ok {
			existing = idx.Entries[j]
		}

		g.Go(func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			sf, err := e.stageFile(p, existing)
			if err != nil {
				return fmt.Errorf("stage %s: %w", p, err)
			}
			results[i] = sf
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	staged := make(map[string]stagedFile, len(results))
	for _, sf := range results {
		if sf.relPath != "" {
			staged[sf.relPath] = sf
		}
	}
	return staged, nil
}

// stageFile processes a single path: stat, read, hash, conditionally store.
func (e *commitEngine) stageFile(relPath string, existing *index.Entry) (stagedFile, error) {
	absPath := filepath.Join(e.repoPath, filepath.FromSlash(relPath))

	info, err := os.Lstat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return stagedFile{relPath: relPath, deleted: true}, nil
		}
		return stagedFile{}, err
	}

	var content []byte
	mode := detectCommitFileMode(info)

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, readErr := os.Readlink(absPath)
		if readErr != nil {
			return stagedFile{}, readErr
		}
		content = []byte(target)
		mode = filemode.Symlink

	case info.Mode().IsRegular():
		if info.Size() > maxBlobReadSize {
			return stagedFile{}, fmt.Errorf("%s: file too large (%d bytes, limit %d)",
				relPath, info.Size(), maxBlobReadSize)
		}
		content, err = os.ReadFile(absPath)
		if err != nil {
			return stagedFile{}, err
		}

	default:
		return stagedFile{}, fmt.Errorf("%s: unsupported file type %s", relPath, info.Mode().Type())
	}

	// Compute git blob hash: SHA1("blob <size>\0<content>").
	hash := computeBlobHash(content)

	// Hash-skip: if existing index entry has the same hash, the object
	// already exists in the store. Skip the write I/O entirely.
	if existing == nil || existing.Hash != hash {
		if err := writeLooseBlob(e.objectsDir, hash, content); err != nil {
			return stagedFile{}, err
		}
	}

	return stagedFile{
		relPath: relPath,
		hash:    hash,
		mode:    mode,
		size:    clampIndexFileSize(info.Size()),
		modTime: info.ModTime(),
		info:    info,
	}, nil
}

// writeLooseBlob writes a blob object directly to .git/objects/<xx>/<yy...>.
// Fully parallel-safe: each hash maps to a unique path. Uses temp file +
// atomic rename. If the object already exists on disk, it is a no-op.
func writeLooseBlob(objectsDir string, hash plumbing.Hash, content []byte) error {
	hex := hash.String()
	dir := filepath.Join(objectsDir, hex[:2])
	finalPath := filepath.Join(dir, hex[2:])

	// Skip if loose object already exists on disk.
	if _, err := os.Stat(finalPath); err == nil {
		return nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp_obj_")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // No-op after successful rename.

	zw := zlib.NewWriter(tmp)
	header := fmt.Sprintf("blob %d\x00", len(content))

	_, err = zw.Write([]byte(header))
	if err == nil {
		_, err = zw.Write(content)
	}
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	err = os.Rename(tmpName, finalPath)
	if err != nil {
		// Race: another worker wrote the same object. Same content → OK.
		if _, statErr := os.Stat(finalPath); statErr == nil {
			return nil
		}
	}
	return err
}

// =============================================================================
// Index Update
// =============================================================================

// applyStaged batch-updates the index with staging results.
//
// Three phases (each O(N) or better):
//  1. Update existing stage-0 entries in-place, collect new entries.
//  2. Single pass to filter deleted entries and conflict entries (stage > 0)
//     for any path being staged.
//  3. Merge new entries into sorted position using O(N + K log K) merge.
func applyStaged(
	idx *index.Index,
	staged map[string]stagedFile,
	entryLookup map[string]int,
) {
	// Phase 1: update existing entries, collect new entries.
	var newEntries []*index.Entry
	for relPath, sf := range staged {
		if sf.deleted {
			continue
		}
		if j, ok := entryLookup[relPath]; ok {
			updateIndexEntry(idx.Entries[j], sf)
		} else {
			newEntries = append(newEntries, newIndexEntry(sf))
		}
	}

	// Phase 2: single-pass filter — remove deleted + conflict entries for staged paths.
	n := 0
	for _, entry := range idx.Entries {
		sf, isStaged := staged[entry.Name]
		if isStaged && (sf.deleted || entry.Stage != 0) {
			continue
		}
		idx.Entries[n] = entry
		n++
	}
	idx.Entries = idx.Entries[:n]

	// Phase 3: merge new entries into sorted position.
	// Existing entries are already sorted. Sort only new entries (K log K),
	// then merge (N + K). Total: O(N + K log K) instead of O(N log N).
	if len(newEntries) > 0 {
		slices.SortFunc(newEntries, compareIndexEntries)
		idx.Entries = mergeSortedEntries(idx.Entries, newEntries)
	}
}

// updateIndexEntry updates an existing index entry in-place.
func updateIndexEntry(entry *index.Entry, sf stagedFile) {
	entry.Hash = sf.hash
	entry.Mode = sf.mode
	entry.Size = sf.size
	entry.ModifiedAt = sf.modTime
	entry.Stage = 0 // Resolve any conflict state.
	if sf.info != nil {
		fillEntrySystemInfo(entry, sf.info)
	}
}

// newIndexEntry creates a fresh index entry from a staged file result.
func newIndexEntry(sf stagedFile) *index.Entry {
	entry := &index.Entry{
		Name:       sf.relPath,
		Hash:       sf.hash,
		Mode:       sf.mode,
		Size:       sf.size,
		ModifiedAt: sf.modTime,
		Stage:      0,
	}
	if sf.info != nil {
		fillEntrySystemInfo(entry, sf.info)
	}
	return entry
}

// compareIndexEntries orders entries by (Name, Stage) matching git's index format.
func compareIndexEntries(a, b *index.Entry) int {
	if c := strings.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	return int(a.Stage) - int(b.Stage)
}

// mergeSortedEntries merges two sorted entry slices into a single sorted slice.
// Both inputs must be sorted by compareIndexEntries. O(N + K).
func mergeSortedEntries(sorted, additional []*index.Entry) []*index.Entry {
	result := make([]*index.Entry, 0, len(sorted)+len(additional))
	i, j := 0, 0
	for i < len(sorted) && j < len(additional) {
		if compareIndexEntries(sorted[i], additional[j]) <= 0 {
			result = append(result, sorted[i])
			i++
		} else {
			result = append(result, additional[j])
			j++
		}
	}
	result = append(result, sorted[i:]...)
	result = append(result, additional[j:]...)
	return result
}

// =============================================================================
// Tree Building — Skip-Existing Optimization
// =============================================================================

// buildTreeSkipExisting builds a tree from the index, but checks
// HasEncodedObject before writing each tree node. For unchanged
// subdirectories the tree object already exists in the store —
// skipping the write eliminates thousands of redundant zlib+file-create
// operations on large repos.
func buildTreeSkipExisting(s storage.Storer, idx *index.Index) (plumbing.Hash, error) {
	return buildFlatEntriesSkipExisting(s, idx.Entries)
}

func buildFlatEntriesSkipExisting(s storage.Storer, entries []*index.Entry) (plumbing.Hash, error) {
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
		subHash, err := buildFlatEntriesSkipExisting(s, bucket.entries)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		treeEntries = append(treeEntries, object.TreeEntry{
			Name: dir,
			Mode: filemode.Dir,
			Hash: subHash,
		})
	}

	return storeTreeSkipExisting(s, treeEntries)
}

// storeTreeSkipExisting encodes a tree object and stores it only if
// it does not already exist in the object store.
func storeTreeSkipExisting(s storage.Storer, entries []object.TreeEntry) (plumbing.Hash, error) {
	sort.Sort(object.TreeEntrySorter(entries))

	tree := &object.Tree{Entries: entries}
	obj := s.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}

	// Check if this exact tree already exists — skip the disk write.
	hash := obj.Hash()
	if s.HasEncodedObject(hash) == nil {
		return hash, nil
	}

	return s.SetEncodedObject(obj)
}

// =============================================================================
// HEAD Update
// =============================================================================

// updateHEAD advances HEAD (or its symbolic target) to the new commit hash.
// Handles both symbolic refs (normal branch) and detached HEAD.
// Matches go-git's Worktree.updateHEAD exactly.
func (e *commitEngine) updateHEAD(commitHash plumbing.Hash) error {
	head, err := e.storer.Reference(plumbing.HEAD)
	if err != nil {
		return fmt.Errorf("read HEAD: %w", err)
	}

	name := plumbing.HEAD
	if head.Type() != plumbing.HashReference {
		name = head.Target() // Follow symbolic ref to branch.
	}

	ref := plumbing.NewHashReference(name, commitHash)
	return e.storer.SetReference(ref)
}

// =============================================================================
// Signature Resolution
// =============================================================================

// resolveCommitSignature reads author and committer identity from git config.
// Resolution chain (matching go-git's loadConfigAuthorAndCommitter):
//
//	Author:    config [author] → config [user] → defaultSignature
//	Committer: config [committer] → resolved Author → defaultSignature
//
// Both Name and Email are sanitized: <, >, \n characters are stripped.
func resolveCommitSignature(repo *gogit.Repository, now time.Time) (author, committer object.Signature) {
	fallback := defaultSignature(now)

	cfg, err := repo.ConfigScoped(config.GlobalScope)
	if err != nil {
		return fallback, fallback
	}

	// Author: [author] → [user] → default.
	author = object.Signature{When: now}
	author.Name = firstNonEmpty(cfg.Author.Name, cfg.User.Name, fallback.Name)
	author.Email = firstNonEmpty(cfg.Author.Email, cfg.User.Email, fallback.Email)

	// Committer: [committer] → author → default.
	committer = object.Signature{When: now}
	committer.Name = firstNonEmpty(cfg.Committer.Name, author.Name, fallback.Name)
	committer.Email = firstNonEmpty(cfg.Committer.Email, author.Email, fallback.Email)

	author = sanitizeSignature(author)
	committer = sanitizeSignature(committer)

	return author, committer
}

// sanitizeSignature strips invalid characters from Name and Email.
func sanitizeSignature(sig object.Signature) object.Signature {
	return object.Signature{
		Name:  invalidSignatureRe.ReplaceAllString(sig.Name, ""),
		Email: invalidSignatureRe.ReplaceAllString(sig.Email, ""),
		When:  sig.When,
	}
}

// firstNonEmpty returns the first non-empty string from the arguments.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// =============================================================================
// Helpers
// =============================================================================

// computeBlobHash computes the git blob SHA-1: "blob <size>\0<content>".
// Used to check whether content matches an existing index entry's hash
// before incurring the cost of writing a loose object.
func computeBlobHash(content []byte) plumbing.Hash {
	h := sha1.New()
	header := fmt.Sprintf("blob %d\x00", len(content))
	h.Write([]byte(header))
	h.Write(content)
	var hash plumbing.Hash
	copy(hash[:], h.Sum(nil))
	return hash
}

// detectCommitFileMode determines the git filemode from os.FileInfo.
// Respects platformHasExecBit: on Windows, always returns Regular.
func detectCommitFileMode(info fs.FileInfo) filemode.FileMode {
	if info.Mode()&os.ModeSymlink != 0 {
		return filemode.Symlink
	}
	if platformHasExecBit && info.Mode()&0o111 != 0 {
		return filemode.Executable
	}
	return filemode.Regular
}

// clampIndexFileSize converts int64 file size to the uint32 stored in the
// git index. Matches native git's fill_stat_cache_info: if the truncated
// value does not round-trip, store 0 (forcing a content hash on next status).
func clampIndexFileSize(size int64) uint32 {
	s := uint32(size)
	if int64(s) != size {
		return 0
	}
	return s
}

// deduplicatePaths returns paths with duplicates removed, preserving order.
// Also normalizes path separators to forward slashes.
func deduplicatePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		p = toSlash(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		result = append(result, p)
	}
	return result
}
