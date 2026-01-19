package cmt

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// =============================================================================
// Recovery Source
// =============================================================================

// RecoverySource indicates where recovery data came from.
type RecoverySource int

const (
	// RecoverySourceWAL indicates recovery from Write-Ahead Log.
	RecoverySourceWAL RecoverySource = iota

	// RecoverySourceFilesystem indicates recovery from filesystem scan.
	RecoverySourceFilesystem

	// RecoverySourceGit indicates recovery from git repository.
	RecoverySourceGit
)

// =============================================================================
// RecoveryResult
// =============================================================================

// RecoveryResult contains recovery outcome.
type RecoveryResult struct {
	Success        bool
	Source         RecoverySource
	RecoveredAt    time.Time
	FilesRecovered int64
	Errors         []error
}

// =============================================================================
// CMTRecovery
// =============================================================================

// CMTRecovery handles tree reconstruction.
type CMTRecovery struct {
	walPath  string
	rootPath string
	gitPath  string
}

// NewCMTRecovery creates a recovery handler.
func NewCMTRecovery(walPath, rootPath, gitPath string) *CMTRecovery {
	return &CMTRecovery{
		walPath:  walPath,
		rootPath: rootPath,
		gitPath:  gitPath,
	}
}

// Recover attempts recovery in priority order: WAL > Filesystem > Git.
func (r *CMTRecovery) Recover(ctx context.Context) (*Node, *RecoveryResult, error) {
	result := &RecoveryResult{
		RecoveredAt: time.Now(),
		Errors:      make([]error, 0),
	}

	root, source, err := r.tryAllSources(ctx, result)
	if err == nil {
		result.Success = true
		result.Source = source
		result.FilesRecovered = countNodes(root)
		return root, result, nil
	}

	result.Success = false
	return nil, result, nil
}

// tryAllSources tries each recovery source in priority order.
func (r *CMTRecovery) tryAllSources(ctx context.Context, result *RecoveryResult) (*Node, RecoverySource, error) {
	sources := []struct {
		fn     func(context.Context) (*Node, error)
		source RecoverySource
		needsRoot bool
	}{
		{r.RecoverFromWAL, RecoverySourceWAL, true},
		{r.RecoverFromFilesystem, RecoverySourceFilesystem, false},
		{r.RecoverFromGit, RecoverySourceGit, false},
	}

	var lastErr error
	for _, s := range sources {
		root, err := s.fn(ctx)
		if root, source, ok := r.checkRecoverySuccess(root, err, s.source, s.needsRoot, result); ok {
			return root, source, nil
		}
		if err != nil {
			lastErr = err
		}
	}

	return nil, 0, lastErr
}

// checkRecoverySuccess checks if recovery was successful and updates result.
func (r *CMTRecovery) checkRecoverySuccess(root *Node, err error, source RecoverySource, needsRoot bool, result *RecoveryResult) (*Node, RecoverySource, bool) {
	if err != nil {
		result.Errors = append(result.Errors, err)
		return nil, 0, false
	}

	if needsRoot && root == nil {
		return nil, 0, false
	}

	return root, source, true
}

// countNodes counts nodes in the tree.
func countNodes(node *Node) int64 {
	if node == nil {
		return 0
	}
	return node.Size()
}

// RecoverFromWAL rebuilds tree by replaying WAL.
func (r *CMTRecovery) RecoverFromWAL(ctx context.Context) (*Node, error) {
	if err := r.verifyWALExists(); err != nil {
		return nil, err
	}

	wal, err := NewWAL(WALConfig{Path: r.walPath})
	if err != nil {
		return nil, err
	}
	defer wal.Close()

	return r.replayWALEntries(ctx, wal)
}

// verifyWALExists checks if WAL file exists.
func (r *CMTRecovery) verifyWALExists() error {
	if _, err := os.Stat(r.walPath); os.IsNotExist(err) {
		return err
	}
	return nil
}

// replayWALEntries replays all entries from WAL.
func (r *CMTRecovery) replayWALEntries(ctx context.Context, wal *WAL) (*Node, error) {
	entries, err := wal.Recover()
	if err != nil {
		return nil, err
	}

	var root *Node
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return root, err
		}
		root = r.applyWALEntry(root, &entry)
	}

	return root, nil
}

// applyWALEntry applies a single WAL entry to the tree.
func (r *CMTRecovery) applyWALEntry(root *Node, entry *WALEntry) *Node {
	switch entry.Op {
	case WALOpInsert:
		return r.applyInsert(root, entry)
	case WALOpDelete:
		return Delete(root, entry.Key)
	default:
		return root
	}
}

// applyInsert applies an insert entry.
func (r *CMTRecovery) applyInsert(root *Node, entry *WALEntry) *Node {
	if entry.Info == nil {
		return root
	}
	node := NewNode(entry.Key, entry.Info)
	return Insert(root, node)
}

// RecoverFromFilesystem rebuilds by scanning disk.
func (r *CMTRecovery) RecoverFromFilesystem(ctx context.Context) (*Node, error) {
	if err := r.verifyRootPath(); err != nil {
		return nil, err
	}

	return r.scanFilesystem(ctx)
}

// verifyRootPath verifies the root path exists and is a directory.
func (r *CMTRecovery) verifyRootPath() error {
	stat, err := os.Stat(r.rootPath)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return os.ErrNotExist
	}
	return nil
}

// scanFilesystem walks the filesystem and builds the tree.
func (r *CMTRecovery) scanFilesystem(ctx context.Context) (*Node, error) {
	var root *Node

	err := filepath.WalkDir(r.rootPath, func(path string, d os.DirEntry, err error) error {
		return r.processFilesystemEntry(ctx, path, d, err, &root)
	})

	return root, filterWalkError(err)
}

// processFilesystemEntry processes a single filesystem entry during walk.
func (r *CMTRecovery) processFilesystemEntry(ctx context.Context, path string, d os.DirEntry, err error, root **Node) error {
	if err != nil {
		return nil // Skip errors
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	return r.handleEntry(path, d, root)
}

// handleEntry handles a directory entry.
func (r *CMTRecovery) handleEntry(path string, d os.DirEntry, root **Node) error {
	if isHidden(d.Name()) {
		return skipHiddenEntry(d)
	}

	if d.IsDir() {
		return nil
	}

	return r.addFileToTree(path, root)
}

// skipHiddenEntry returns appropriate skip action for hidden entries.
func skipHiddenEntry(d os.DirEntry) error {
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// addFileToTree adds a file to the tree.
func (r *CMTRecovery) addFileToTree(path string, root **Node) error {
	relPath, err := filepath.Rel(r.rootPath, path)
	if err != nil {
		return nil
	}

	info, err := r.createFileInfo(path, relPath)
	if err != nil {
		return nil // Skip files we can't read
	}

	node := NewNode(relPath, info)
	*root = Insert(*root, node)
	return nil
}

// filterWalkError filters out context errors from walk.
func filterWalkError(err error) error {
	if err == nil || err == context.Canceled || err == context.DeadlineExceeded {
		return nil
	}
	return err
}

// createFileInfo creates a FileInfo for a file.
func (r *CMTRecovery) createFileInfo(fullPath, relPath string) (*FileInfo, error) {
	stat, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}

	hash, err := computeFileHash(fullPath)
	if err != nil {
		return nil, err
	}

	return &FileInfo{
		Path:        relPath,
		ContentHash: hash,
		Size:        stat.Size(),
		ModTime:     stat.ModTime(),
		Permissions: uint32(stat.Mode().Perm()),
		Indexed:     false,
	}, nil
}

// RecoverFromGit rebuilds using git ls-files (if git repo).
// Returns error if not a git repository.
func (r *CMTRecovery) RecoverFromGit(ctx context.Context) (*Node, error) {
	gitDir := r.effectiveGitDir()

	if !isGitRepo(gitDir) {
		return nil, os.ErrNotExist
	}

	files, err := r.getGitTrackedFiles(ctx, gitDir)
	if err != nil {
		return nil, err
	}

	return r.buildTreeFromGitFiles(ctx, files)
}

// effectiveGitDir returns the git directory to use.
func (r *CMTRecovery) effectiveGitDir() string {
	if r.gitPath != "" {
		return r.gitPath
	}
	return r.rootPath
}

// buildTreeFromGitFiles builds a tree from git tracked files.
func (r *CMTRecovery) buildTreeFromGitFiles(ctx context.Context, files []string) (*Node, error) {
	var root *Node

	for _, relPath := range files {
		if err := ctx.Err(); err != nil {
			return root, err
		}

		if node := r.tryAddGitFile(relPath); node != nil {
			root = Insert(root, node)
		}
	}

	return root, nil
}

// tryAddGitFile attempts to add a git file to the tree.
func (r *CMTRecovery) tryAddGitFile(relPath string) *Node {
	fullPath := filepath.Join(r.rootPath, relPath)

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return nil
	}

	info, err := r.createFileInfo(fullPath, relPath)
	if err != nil {
		return nil
	}

	return NewNode(relPath, info)
}

// isGitRepo checks if directory is a git repository.
func isGitRepo(dir string) bool {
	gitDir := filepath.Join(dir, ".git")
	stat, err := os.Stat(gitDir)
	if err != nil {
		return false
	}
	return stat.IsDir()
}

// getGitTrackedFiles returns list of tracked files using git ls-files.
func (r *CMTRecovery) getGitTrackedFiles(ctx context.Context, gitDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files")
	cmd.Dir = gitDir

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parseGitLsFilesOutput(output)
}

// parseGitLsFilesOutput parses the output of git ls-files.
func parseGitLsFilesOutput(output []byte) ([]string, error) {
	var files []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}

	return files, scanner.Err()
}
