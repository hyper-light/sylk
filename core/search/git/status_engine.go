package git

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// Platform Normalization
// =============================================================================

// toSlash converts OS-native path separators to forward slashes.
// Git index entries always use forward slashes regardless of platform.
// On Unix this is a no-op; on Windows it converts backslashes.
func toSlash(path string) string {
	return filepath.ToSlash(path)
}

// platformHasExecBit reports whether the current OS tracks executable
// permission bits. False on Windows where all files report mode 0666/0444.
var platformHasExecBit = runtime.GOOS != "windows"

// =============================================================================
// Types
// =============================================================================

// StatusEngine computes working-tree status using stat-dirty optimization.
// Caches the parsed index and HEAD tree across refreshes, invalidating
// only when the underlying files change.
type StatusEngine struct {
	client   *GitClient
	gitDir   string
	repoPath string
	idxCache *indexCache
	headCache *headTreeCache
}

// indexCache caches the parsed git index, keyed by .git/index mtime and size.
// Only stage-0 entries are stored in the entries map; higher stages are
// recorded in the conflicts set.
type indexCache struct {
	entries   map[string]*index.Entry // stage-0 only
	conflicts map[string]struct{}     // paths with Stage > Merged
	modTime   time.Time               // .git/index mtime at parse
	fileSize  int64                   // .git/index size at parse
}

// headTreeCache caches the flat blob hash map derived from the HEAD commit tree.
// Invalidated when HEAD changes.
type headTreeCache struct {
	entries  map[string]plumbing.Hash // path -> blob hash in HEAD
	headHash plumbing.Hash            // HEAD commit hash at build time
}

// statResult classifies the outcome of comparing a file's stat data against
// its index entry.
type statResult int

const (
	// statClean means all stat fields match the index — skip content hash.
	statClean statResult = iota
	// statDirty means at least one stat field differs — hash to confirm.
	statDirty
	// statRacy means the entry's mtime is >= the index file's mtime,
	// so the stat data is unreliable and a content hash is required.
	statRacy
)

// ignoreStack tracks active gitignore patterns during a filesystem walk.
// Processes .gitignore files as it descends directories, enabling entire
// ignored subtrees to be skipped.
type ignoreStack struct {
	layers []ignoreLayer
	global []gitignore.Pattern // ~/.gitconfig core.excludesfile
	repo   []gitignore.Pattern // .git/info/exclude
}

// ignoreLayer holds the gitignore patterns loaded from a single directory's
// .gitignore file, along with the directory path for stack unwinding.
type ignoreLayer struct {
	dir      string
	patterns []gitignore.Pattern
}

// =============================================================================
// Constructor
// =============================================================================

// NewStatusEngine creates a StatusEngine for the given client and resolved
// .git directory path.
func NewStatusEngine(client *GitClient, gitDir string) *StatusEngine {
	return &StatusEngine{
		client:   client,
		gitDir:   gitDir,
		repoPath: client.repoPath,
	}
}

// =============================================================================
// Stat-Dirty Comparison
// =============================================================================

// compareStatToIndex compares filesystem stat data against the index entry.
// Returns statClean if all fields match, statRacy if the entry might be
// racily clean, or statDirty if any field differs.
func compareStatToIndex(info fs.FileInfo, entry *index.Entry, indexMtime time.Time) statResult {
	if isRacyEntry(entry, indexMtime) {
		return statRacy
	}
	if !timesMatch(info, entry) {
		return statDirty
	}
	if !sizeMatches(info, entry) {
		return statDirty
	}
	if !modeMatches(info, entry) {
		return statDirty
	}
	return statClean
}

// isRacyEntry detects the "racy git" condition: the entry's ModifiedAt is
// at or after the index file's own mtime. In this case, the file could have
// been modified in the same second the index was written, making stat data
// unreliable.
func isRacyEntry(entry *index.Entry, indexMtime time.Time) bool {
	return !entry.ModifiedAt.Before(indexMtime)
}

// timesMatch compares the filesystem mtime against the index entry mtime.
// Uses nanosecond precision when available.
func timesMatch(info fs.FileInfo, entry *index.Entry) bool {
	return info.ModTime().Equal(entry.ModifiedAt)
}

// sizeMatches compares the filesystem size against the index entry size.
// The index stores size as uint32, so files > 4GB wrap to 0.
// When entry.Size is 0 and the file is non-empty, we conservatively
// report a mismatch (statDirty) to force a content hash.
func sizeMatches(info fs.FileInfo, entry *index.Entry) bool {
	diskSize := info.Size()
	entrySize := int64(entry.Size)
	if entry.Size == 0 && diskSize > 0 {
		return false
	}
	return diskSize == entrySize
}

// modeMatches compares the filesystem mode against the index entry mode.
// Only distinguishes Regular, Executable, and Symlink — other modes
// (Dir, Submodule) are handled by the caller before reaching stat comparison.
//
// On platforms without executable permission bits (Windows), the
// Regular/Executable distinction is skipped since the OS always reports
// 0666/0444 regardless of the index mode. This mirrors native git's
// core.fileMode=false behavior.
func modeMatches(info fs.FileInfo, entry *index.Entry) bool {
	diskMode, err := filemode.NewFromOSFileMode(info.Mode())
	if err != nil {
		return false
	}
	if !platformHasExecBit {
		return normalizeFileMode(diskMode) == normalizeFileMode(entry.Mode)
	}
	return diskMode == entry.Mode
}

// normalizeFileMode collapses Regular and Executable into Regular.
// Used on platforms without executable bits to avoid false stat-dirty results.
func normalizeFileMode(m filemode.FileMode) filemode.FileMode {
	if m == filemode.Executable {
		return filemode.Regular
	}
	return m
}

// =============================================================================
// Content Hash
// =============================================================================

// hashFileContent computes the git blob SHA-1: "blob <size>\0<content>".
// Only called for stat-dirty or racy files. relPath uses forward slashes.
func hashFileContent(repoPath, relPath string, size int64) (plumbing.Hash, error) {
	f, err := os.Open(filepath.Join(repoPath, filepath.FromSlash(relPath)))
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer f.Close()

	h := sha1.New()
	header := fmt.Sprintf("blob %d\x00", size)
	h.Write([]byte(header))

	if _, err := io.Copy(h, f); err != nil {
		return plumbing.ZeroHash, err
	}

	var hash plumbing.Hash
	copy(hash[:], h.Sum(nil))
	return hash, nil
}

// hashSymlinkTarget computes the git blob SHA-1 for a symlink's target path.
// relPath uses forward slashes.
func hashSymlinkTarget(repoPath, relPath string) (plumbing.Hash, error) {
	target, err := os.Readlink(filepath.Join(repoPath, filepath.FromSlash(relPath)))
	if err != nil {
		return plumbing.ZeroHash, err
	}

	h := sha1.New()
	header := fmt.Sprintf("blob %d\x00", len(target))
	h.Write([]byte(header))
	h.Write([]byte(target))

	var hash plumbing.Hash
	copy(hash[:], h.Sum(nil))
	return hash, nil
}

// =============================================================================
// Index Cache
// =============================================================================

// statIndexFile returns the mtime and size of the .git/index file.
func statIndexFile(gitDir string) (time.Time, int64, error) {
	info, err := os.Stat(filepath.Join(gitDir, "index"))
	if err != nil {
		return time.Time{}, 0, err
	}
	return info.ModTime(), info.Size(), nil
}

// refreshIndexCache reloads the index from disk if it has changed,
// or returns the existing cache if still valid.
func (e *StatusEngine) refreshIndexCache() (*indexCache, error) {
	mtime, size, err := statIndexFile(e.gitDir)
	if err != nil {
		return nil, fmt.Errorf("stat index: %w", err)
	}

	if e.idxCache != nil && e.idxCache.modTime.Equal(mtime) && e.idxCache.fileSize == size {
		return e.idxCache, nil
	}

	idx, err := e.loadIndex()
	if err != nil {
		return nil, err
	}

	cache := buildIndexMaps(idx, mtime, size)
	e.idxCache = cache
	return cache, nil
}

// loadIndex parses the git index from the repository's storer.
func (e *StatusEngine) loadIndex() (*index.Index, error) {
	e.client.mu.RLock()
	defer e.client.mu.RUnlock()

	if !e.client.isRepo || e.client.repo == nil {
		return nil, ErrNotGitRepo
	}
	return e.client.repo.Storer.Index()
}

// buildIndexMaps separates index entries into stage-0 (normal) and conflicts
// (stage > 0). Returns a populated indexCache.
//
// Note: go-git decodes Stage from (flags>>12) & 0x3. Normal (merged) entries
// have Stage=0 (the zero value). Conflict entries have Stage 1, 2, or 3
// (ancestor, ours, theirs respectively).
func buildIndexMaps(idx *index.Index, mtime time.Time, size int64) *indexCache {
	entries := make(map[string]*index.Entry, len(idx.Entries))
	conflicts := make(map[string]struct{})

	for _, entry := range idx.Entries {
		if entry.Stage != 0 {
			conflicts[entry.Name] = struct{}{}
			continue
		}
		entries[entry.Name] = entry
	}

	return &indexCache{
		entries:   entries,
		conflicts: conflicts,
		modTime:   mtime,
		fileSize:  size,
	}
}

// =============================================================================
// HEAD Tree Cache
// =============================================================================

// resolveHEADHash returns the current HEAD commit hash.
func (e *StatusEngine) resolveHEADHash() (plumbing.Hash, error) {
	e.client.mu.RLock()
	defer e.client.mu.RUnlock()

	if !e.client.isRepo || e.client.repo == nil {
		return plumbing.ZeroHash, ErrNotGitRepo
	}

	ref, err := e.client.repo.Head()
	if err != nil {
		// Empty repo (no commits) — return zero hash, no error.
		if err == plumbing.ErrReferenceNotFound {
			return plumbing.ZeroHash, nil
		}
		return plumbing.ZeroHash, err
	}
	return ref.Hash(), nil
}

// refreshHeadCache rebuilds the HEAD tree map if HEAD has changed,
// or returns the existing cache if still valid.
func (e *StatusEngine) refreshHeadCache() (*headTreeCache, error) {
	headHash, err := e.resolveHEADHash()
	if err != nil {
		return nil, err
	}

	if e.headCache != nil && e.headCache.headHash == headHash {
		return e.headCache, nil
	}

	entries, err := e.buildHeadEntries(headHash)
	if err != nil {
		return nil, err
	}

	cache := &headTreeCache{
		entries:  entries,
		headHash: headHash,
	}
	e.headCache = cache
	return cache, nil
}

// buildHeadEntries walks the HEAD commit's tree and returns a flat map of
// path -> blob hash. Returns an empty map for an empty repository.
func (e *StatusEngine) buildHeadEntries(headHash plumbing.Hash) (map[string]plumbing.Hash, error) {
	if headHash.IsZero() {
		return make(map[string]plumbing.Hash), nil
	}

	e.client.mu.RLock()
	defer e.client.mu.RUnlock()

	commit, err := e.client.repo.CommitObject(headHash)
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD commit: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD tree: %w", err)
	}

	entries := make(map[string]plumbing.Hash, len(e.idxCache.entries))
	err = tree.Files().ForEach(func(f *object.File) error {
		entries[f.Name] = f.Hash
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk HEAD tree: %w", err)
	}

	return entries, nil
}

// =============================================================================
// Gitignore Stack
// =============================================================================

// newIgnoreStack creates an ignoreStack pre-loaded with global excludes
// (~/.config/git/ignore) and the repo-level .git/info/exclude patterns.
func newIgnoreStack(repoPath, gitDir string) *ignoreStack {
	s := &ignoreStack{
		layers: make([]ignoreLayer, 0, 8),
	}
	s.loadGlobalPatterns()
	s.loadRepoExclude(gitDir)
	s.loadDirPatterns(repoPath, ".")
	return s
}

// loadGlobalPatterns loads patterns from the global git excludes file.
func (s *ignoreStack) loadGlobalPatterns() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// Standard location: ~/.config/git/ignore
	path := filepath.Join(home, ".config", "git", "ignore")
	patterns := readPatternsFromFile(path, nil)
	s.global = patterns
}

// loadRepoExclude loads patterns from .git/info/exclude.
func (s *ignoreStack) loadRepoExclude(gitDir string) {
	path := filepath.Join(gitDir, "info", "exclude")
	patterns := readPatternsFromFile(path, nil)
	s.repo = patterns
}

// readPatternsFromFile reads gitignore patterns from a single file.
// domain is the path segments of the directory containing the file
// (nil for global/repo patterns).
func readPatternsFromFile(path string, domain []string) []gitignore.Pattern {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var patterns []gitignore.Pattern
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	return patterns
}

// loadDirPatterns loads .gitignore from the given directory and pushes
// it onto the stack. relDir uses forward slashes (git convention).
func (s *ignoreStack) loadDirPatterns(repoPath, relDir string) {
	// Convert back to OS path for filesystem access.
	absDir := filepath.Join(repoPath, filepath.FromSlash(relDir))
	path := filepath.Join(absDir, ".gitignore")

	// Gitignore domains use forward-slash segments.
	var domain []string
	if relDir != "." {
		domain = strings.Split(relDir, "/")
	}

	patterns := readPatternsFromFile(path, domain)
	s.layers = append(s.layers, ignoreLayer{
		dir:      relDir,
		patterns: patterns,
	})
}

// popTo unwinds the stack, removing all layers deeper than the given directory.
func (s *ignoreStack) popTo(relDir string) {
	keep := 0
	for i, layer := range s.layers {
		if layer.dir == relDir || isParentOf(layer.dir, relDir) {
			keep = i + 1
		}
	}
	s.layers = s.layers[:keep]
}

// isParentOf reports whether parent is a path prefix of child.
// Both paths use forward slashes (git convention).
func isParentOf(parent, child string) bool {
	if parent == "." {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}

// isIgnored checks whether a path is ignored by any active pattern layer.
// Patterns are checked in priority order: global < repo < .gitignore layers
// (deepest last = highest priority). relPath uses forward slashes.
func (s *ignoreStack) isIgnored(relPath string, isDir bool) bool {
	parts := strings.Split(relPath, "/")

	// Check from highest to lowest priority (deepest .gitignore first).
	for i := len(s.layers) - 1; i >= 0; i-- {
		for _, p := range s.layers[i].patterns {
			if r := p.Match(parts, isDir); r == gitignore.Exclude {
				return true
			} else if r == gitignore.Include {
				return false
			}
		}
	}

	// Repo-level .git/info/exclude
	for _, p := range s.repo {
		if r := p.Match(parts, isDir); r == gitignore.Exclude {
			return true
		} else if r == gitignore.Include {
			return false
		}
	}

	// Global excludes
	for _, p := range s.global {
		if r := p.Match(parts, isDir); r == gitignore.Exclude {
			return true
		} else if r == gitignore.Include {
			return false
		}
	}

	return false
}

// =============================================================================
// Filesystem Walker
// =============================================================================

// walkWorktree walks the working tree comparing each file against the index
// using stat-dirty optimization. Returns a classified WorkingTreeStatus and
// the set of paths found on disk (for deleted-file detection).
func walkWorktree(
	ctx context.Context,
	repoPath string,
	idxEntries map[string]*index.Entry,
	headEntries map[string]plumbing.Hash,
	conflicts map[string]struct{},
	ignores *ignoreStack,
	indexMtime time.Time,
) (*WorkingTreeStatus, map[string]struct{}, error) {
	wts := &WorkingTreeStatus{
		Modified:  make([]string, 0),
		Added:     make([]string, 0),
		Deleted:   make([]string, 0),
		Untracked: make([]string, 0),
		Conflict:  make([]string, 0),
	}
	seen := make(map[string]struct{}, len(idxEntries))

	err := filepath.WalkDir(repoPath, func(absPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip inaccessible entries
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		osRel, err := filepath.Rel(repoPath, absPath)
		if err != nil || osRel == "." {
			return nil
		}

		// Normalize to forward slashes to match git index paths.
		relPath := toSlash(osRel)

		// Always skip .git directory.
		if d.IsDir() && relPath == ".git" {
			return filepath.SkipDir
		}

		if d.IsDir() {
			return walkDirectory(repoPath, relPath, ignores, idxEntries)
		}

		return walkFile(repoPath, relPath, d, wts, seen, idxEntries, headEntries, conflicts, ignores, indexMtime)
	})

	return wts, seen, err
}

// walkDirectory handles directory entries during the walk. Loads gitignore
// patterns and skips ignored directories entirely.
func walkDirectory(repoPath, relDir string, ignores *ignoreStack, idxEntries map[string]*index.Entry) error {
	if ignores.isIgnored(relDir, true) {
		// Check if any index entry is under this directory.
		// If so, we must not skip it (override pattern).
		if !hasIndexEntryUnder(idxEntries, relDir) {
			return filepath.SkipDir
		}
	}
	ignores.loadDirPatterns(repoPath, relDir)
	return nil
}

// hasIndexEntryUnder reports whether any index entry has the given directory
// as a prefix. Used to prevent skipping directories that contain tracked files
// despite matching a gitignore pattern.
func hasIndexEntryUnder(entries map[string]*index.Entry, dir string) bool {
	prefix := dir + "/"
	for name := range entries {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// walkFile handles a single file entry during the walk.
func walkFile(
	repoPath, relPath string,
	d fs.DirEntry,
	wts *WorkingTreeStatus,
	seen map[string]struct{},
	idxEntries map[string]*index.Entry,
	headEntries map[string]plumbing.Hash,
	conflicts map[string]struct{},
	ignores *ignoreStack,
	indexMtime time.Time,
) error {
	// Conflict entries (stage > 0).
	if _, inConflict := conflicts[relPath]; inConflict {
		seen[relPath] = struct{}{}
		wts.Conflict = append(wts.Conflict, relPath)
		return nil
	}

	entry, inIndex := idxEntries[relPath]

	if !inIndex {
		return walkUntrackedFile(relPath, ignores, wts)
	}

	seen[relPath] = struct{}{}
	return walkTrackedFile(repoPath, relPath, d, entry, headEntries, wts, indexMtime)
}

// walkUntrackedFile classifies a file not present in the index.
func walkUntrackedFile(relPath string, ignores *ignoreStack, wts *WorkingTreeStatus) error {
	if !ignores.isIgnored(relPath, false) {
		wts.Untracked = append(wts.Untracked, relPath)
	}
	return nil
}

// walkTrackedFile classifies a tracked file by comparing stat data and
// optionally hashing its content.
func walkTrackedFile(
	repoPath, relPath string,
	d fs.DirEntry,
	entry *index.Entry,
	headEntries map[string]plumbing.Hash,
	wts *WorkingTreeStatus,
	indexMtime time.Time,
) error {
	// SkipWorktree entries are not checked against disk.
	if entry.SkipWorktree {
		return nil
	}

	// IntentToAdd entries are classified as Added.
	if entry.IntentToAdd {
		wts.Added = append(wts.Added, relPath)
		wts.HasIndexStaged = true
		return nil
	}

	// Submodules are skipped.
	if entry.Mode == filemode.Submodule {
		return nil
	}

	info, err := d.Info()
	if err != nil {
		return nil // skip files we can't stat
	}

	// Symlinks need special handling.
	if entry.Mode == filemode.Symlink {
		return classifySymlink(repoPath, relPath, entry, headEntries, wts)
	}

	result := compareStatToIndex(info, entry, indexMtime)
	if result == statClean {
		return nil
	}

	// Stat-dirty or racy: hash content to confirm actual change.
	return classifyByHash(repoPath, relPath, info.Size(), entry, headEntries, wts)
}

// classifySymlink compares a symlink's target against the index hash.
func classifySymlink(
	repoPath, relPath string,
	entry *index.Entry,
	headEntries map[string]plumbing.Hash,
	wts *WorkingTreeStatus,
) error {
	diskHash, err := hashSymlinkTarget(repoPath, relPath)
	if err != nil {
		return nil // skip unreadable symlinks
	}

	if diskHash != entry.Hash {
		classifyWorktreeChange(relPath, entry, headEntries, wts)
	}
	return nil
}

// classifyByHash hashes a file and compares it to the index entry hash.
// If different, classifies the change as worktree-modified.
func classifyByHash(
	repoPath, relPath string,
	diskSize int64,
	entry *index.Entry,
	headEntries map[string]plumbing.Hash,
	wts *WorkingTreeStatus,
) error {
	diskHash, err := hashFileContent(repoPath, relPath, diskSize)
	if err != nil {
		return nil // skip unhashable files
	}

	if diskHash != entry.Hash {
		classifyWorktreeChange(relPath, entry, headEntries, wts)
	}
	return nil
}

// classifyWorktreeChange adds a file to the appropriate WorkingTreeStatus
// bucket based on whether it exists in HEAD.
func classifyWorktreeChange(
	relPath string,
	entry *index.Entry,
	headEntries map[string]plumbing.Hash,
	wts *WorkingTreeStatus,
) {
	wts.Modified = append(wts.Modified, relPath)
}

// =============================================================================
// Staged Changes (Index vs HEAD)
// =============================================================================

// classifyStagedChanges compares index entries against the HEAD tree to detect
// staged additions, modifications, and deletions. This is pure hash comparison
// with no filesystem I/O.
func classifyStagedChanges(
	idxEntries map[string]*index.Entry,
	headEntries map[string]plumbing.Hash,
	wts *WorkingTreeStatus,
) {
	// Index entries not in HEAD or with different hash → staged change.
	for path, entry := range idxEntries {
		if entry.SkipWorktree || entry.IntentToAdd {
			continue
		}
		headHash, inHead := headEntries[path]
		if !inHead {
			wts.Added = append(wts.Added, path)
			wts.HasIndexStaged = true
			continue
		}
		if entry.Hash != headHash {
			wts.Modified = append(wts.Modified, path)
			wts.HasIndexStaged = true
		}
	}

	// HEAD entries not in index → staged deletion.
	for path := range headEntries {
		if _, inIndex := idxEntries[path]; !inIndex {
			wts.Deleted = append(wts.Deleted, path)
			wts.HasIndexStaged = true
		}
	}
}

// =============================================================================
// Refresh (Main Entry Point)
// =============================================================================

// Refresh computes a full StatusUpdate using stat-dirty comparison.
// Reuses cached index/HEAD when source files are unchanged.
func (e *StatusEngine) Refresh(ctx context.Context) (StatusUpdate, error) {
	if ctx.Err() != nil {
		return StatusUpdate{}, ctx.Err()
	}

	// Step 1-2: Refresh index cache.
	idxCache, err := e.refreshIndexCache()
	if err != nil {
		return StatusUpdate{}, fmt.Errorf("index cache: %w", err)
	}

	// Step 3-4: Refresh HEAD tree cache.
	headCache, err := e.refreshHeadCache()
	if err != nil {
		return StatusUpdate{}, fmt.Errorf("head cache: %w", err)
	}

	// Step 5: Build ignore stack.
	ignores := newIgnoreStack(e.repoPath, e.gitDir)

	// Step 6: Walk working tree with stat-dirty comparison.
	wts, seen, err := walkWorktree(
		ctx, e.repoPath,
		idxCache.entries, headCache.entries, idxCache.conflicts,
		ignores, idxCache.modTime,
	)
	if err != nil {
		return StatusUpdate{}, fmt.Errorf("walk worktree: %w", err)
	}

	// Step 7: Detect deleted files (in index but not on disk).
	for path := range idxCache.entries {
		if _, onDisk := seen[path]; onDisk {
			continue
		}
		entry := idxCache.entries[path]
		if entry.SkipWorktree || entry.IntentToAdd {
			continue
		}
		wts.Deleted = append(wts.Deleted, path)
	}

	// Step 8: Classify staged changes (index vs HEAD).
	classifyStagedChanges(idxCache.entries, headCache.entries, wts)

	// Step 9: Build output maps.
	statusMap := BuildStatusMap(wts)
	trackedSet := buildTrackedSetFromIndex(idxCache.entries)
	trackedDirs := BuildTrackedDirs(trackedSet)

	return StatusUpdate{
		StatusMap:   statusMap,
		TrackedSet:  trackedSet,
		TrackedDirs: trackedDirs,
	}, nil
}

// buildTrackedSetFromIndex constructs the tracked set directly from the
// cached index entries, avoiding a second index parse.
func buildTrackedSetFromIndex(entries map[string]*index.Entry) map[string]struct{} {
	set := make(map[string]struct{}, len(entries))
	for path := range entries {
		set[path] = struct{}{}
	}
	return set
}

