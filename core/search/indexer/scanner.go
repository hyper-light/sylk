// Package indexer provides file scanning and indexing functionality for the Sylk
// Document Search System. It supports configurable file discovery with pattern-based
// inclusion and exclusion rules.
package indexer

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gobwas/glob"
)

// =============================================================================
// Configuration
// =============================================================================

// ScanConfig holds configuration for the file scanner.
type ScanConfig struct {
	// RootPath is the directory to scan (required)
	RootPath string

	// IncludePatterns are glob patterns for files to include (e.g., ["*.go", "*.ts"])
	// If empty, all files are included (subject to exclusions)
	IncludePatterns []string

	// ExcludePatterns are glob patterns for files to exclude (e.g., ["*_test.go"])
	ExcludePatterns []string

	// MaxFileSize is the maximum file size in bytes to include (default: 10MB)
	MaxFileSize int64

	// FollowSymlinks determines whether to follow symbolic links (default: false)
	FollowSymlinks bool
}

// DefaultMaxFileSize is the default maximum file size (10MB).
const DefaultMaxFileSize int64 = 10 * 1024 * 1024

// defaultExcludedDirs contains directories that are always excluded from scanning.
var defaultExcludedDirs = map[string]struct{}{
	".git":        {},
	"node_modules": {},
	"vendor":      {},
	"__pycache__": {},
	".next":       {},
	"dist":        {},
	"build":       {},
	".cache":      {},
	"target":      {},
	"bin":         {},
	"obj":         {},
	".idea":       {},
	".vscode":     {},
}

// =============================================================================
// FileInfo
// =============================================================================

// FileInfo represents metadata about a scanned file.
type FileInfo struct {
	// Path is the absolute path to the file
	Path string

	// Name is the base name of the file
	Name string

	// Size is the file size in bytes
	Size int64

	// ModTime is when the file was last modified
	ModTime time.Time

	// IsDir indicates whether this is a directory
	IsDir bool

	// Extension is the file extension (including the dot, e.g., ".go")
	Extension string
}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrRootPathEmpty indicates the root path was not specified.
	ErrRootPathEmpty = errors.New("root path cannot be empty")

	// ErrRootPathNotExist indicates the root path does not exist.
	ErrRootPathNotExist = errors.New("root path does not exist")

	// ErrRootPathNotDir indicates the root path is not a directory.
	ErrRootPathNotDir = errors.New("root path is not a directory")

	// ErrInvalidPattern indicates a glob pattern could not be compiled.
	ErrInvalidPattern = errors.New("invalid glob pattern")
)

// =============================================================================
// Scanner
// =============================================================================

// Scanner walks a directory tree and yields files matching configured patterns.
type Scanner struct {
	config          ScanConfig
	includeMatchers []glob.Glob
	excludeMatchers []glob.Glob
	maxFileSize     int64
}

// NewScanner creates a new Scanner with the given configuration.
// Patterns are not compiled until Scan is called.
func NewScanner(config ScanConfig) *Scanner {
	maxSize := config.MaxFileSize
	if maxSize <= 0 {
		maxSize = DefaultMaxFileSize
	}

	return &Scanner{
		config:      config,
		maxFileSize: maxSize,
	}
}

// =============================================================================
// Scan Method
// =============================================================================

// Scan walks the configured root path and returns a channel of FileInfo.
// The channel is closed when scanning completes or the context is cancelled.
// Returns an error if the root path is invalid or patterns cannot be compiled.
func (s *Scanner) Scan(ctx context.Context) (<-chan *FileInfo, error) {
	if err := s.validateConfig(); err != nil {
		return nil, err
	}

	if err := s.compilePatterns(); err != nil {
		return nil, err
	}

	fileCh := make(chan *FileInfo)
	go s.scanDirectory(ctx, fileCh)

	return fileCh, nil
}

// =============================================================================
// Validation
// =============================================================================

// validateConfig checks that the scanner configuration is valid.
func (s *Scanner) validateConfig() error {
	if s.config.RootPath == "" {
		return ErrRootPathEmpty
	}

	info, err := os.Stat(s.config.RootPath)
	if os.IsNotExist(err) {
		return ErrRootPathNotExist
	}
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return ErrRootPathNotDir
	}

	return nil
}

// =============================================================================
// Pattern Compilation
// =============================================================================

// compilePatterns compiles all include and exclude glob patterns.
func (s *Scanner) compilePatterns() error {
	var err error

	s.includeMatchers, err = compileGlobs(s.config.IncludePatterns)
	if err != nil {
		return err
	}

	s.excludeMatchers, err = compileGlobs(s.config.ExcludePatterns)
	if err != nil {
		return err
	}

	return nil
}

// compileGlobs compiles a slice of glob pattern strings into matchers.
func compileGlobs(patterns []string) ([]glob.Glob, error) {
	matchers := make([]glob.Glob, 0, len(patterns))

	for _, pattern := range patterns {
		matcher, err := glob.Compile(pattern, '/')
		if err != nil {
			return nil, errors.Join(ErrInvalidPattern, err)
		}
		matchers = append(matchers, matcher)
	}

	return matchers, nil
}

// =============================================================================
// Directory Scanning
// =============================================================================

// scanDirectory walks the root path and sends matching files to the channel.
// Always closes the channel when done.
func (s *Scanner) scanDirectory(ctx context.Context, fileCh chan<- *FileInfo) {
	defer close(fileCh)

	_ = filepath.WalkDir(s.config.RootPath, func(path string, d fs.DirEntry, err error) error {
		return s.processEntry(ctx, path, d, err, fileCh)
	})
}

// processEntry handles a single directory entry during the walk.
func (s *Scanner) processEntry(
	ctx context.Context,
	path string,
	d fs.DirEntry,
	walkErr error,
	fileCh chan<- *FileInfo,
) error {
	if cancelled := s.checkCancellation(ctx); cancelled {
		return fs.SkipAll
	}

	if walkErr != nil {
		return s.handleWalkError(walkErr)
	}

	if d.IsDir() {
		return s.handleDirectory(path, d)
	}

	// Handle symlinks to directories when FollowSymlinks is enabled
	if s.isSymlinkToDirectory(d) {
		return s.handleSymlinkedDirectory(ctx, path, fileCh)
	}

	return s.handleFile(ctx, path, d, fileCh)
}

// checkCancellation returns true if the context has been cancelled.
func (s *Scanner) checkCancellation(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// handleWalkError determines how to proceed after a walk error.
func (s *Scanner) handleWalkError(err error) error {
	if os.IsPermission(err) {
		return nil // Skip files we can't access
	}
	return err
}

// =============================================================================
// Directory Handling
// =============================================================================

// handleDirectory determines whether to descend into a directory.
func (s *Scanner) handleDirectory(path string, d fs.DirEntry) error {
	if s.shouldSkipDirectory(d.Name()) {
		return fs.SkipDir
	}

	if d.Type()&os.ModeSymlink != 0 && !s.config.FollowSymlinks {
		return fs.SkipDir
	}

	return nil
}

// shouldSkipDirectory checks if a directory name is in the default exclusion list.
func (s *Scanner) shouldSkipDirectory(name string) bool {
	_, excluded := defaultExcludedDirs[name]
	return excluded
}

// isSymlinkToDirectory checks if an entry is a symlink pointing to a directory.
func (s *Scanner) isSymlinkToDirectory(d fs.DirEntry) bool {
	if !s.config.FollowSymlinks {
		return false
	}
	return d.Type()&os.ModeSymlink != 0
}

// handleSymlinkedDirectory recursively scans a symlinked directory.
func (s *Scanner) handleSymlinkedDirectory(
	ctx context.Context,
	path string,
	fileCh chan<- *FileInfo,
) error {
	// Resolve the symlink target
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil // Skip broken symlinks
	}

	// Verify target is a directory
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return nil // Not a directory or can't stat
	}

	// Check if target directory should be skipped
	if s.shouldSkipDirectory(info.Name()) {
		return nil
	}

	// Walk the symlinked directory
	return filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		return s.processSymlinkEntry(ctx, path, target, p, d, err, fileCh)
	})
}

// processSymlinkEntry processes entries within a symlinked directory.
func (s *Scanner) processSymlinkEntry(
	ctx context.Context,
	symlinkPath, targetPath, currentPath string,
	d fs.DirEntry,
	walkErr error,
	fileCh chan<- *FileInfo,
) error {
	if cancelled := s.checkCancellation(ctx); cancelled {
		return fs.SkipAll
	}

	if walkErr != nil {
		return s.handleWalkError(walkErr)
	}

	if d.IsDir() {
		return s.handleDirectoryInSymlink(d)
	}

	// Compute relative path from symlink location
	relToTarget, err := filepath.Rel(targetPath, currentPath)
	if err != nil {
		return nil
	}

	virtualPath := filepath.Join(symlinkPath, relToTarget)
	return s.handleFileWithPath(ctx, virtualPath, currentPath, d, fileCh)
}

// handleDirectoryInSymlink handles directories found within a symlinked tree.
func (s *Scanner) handleDirectoryInSymlink(d fs.DirEntry) error {
	if s.shouldSkipDirectory(d.Name()) {
		return fs.SkipDir
	}
	return nil
}

// handleFileWithPath processes a file using a virtual path for pattern matching.
func (s *Scanner) handleFileWithPath(
	ctx context.Context,
	virtualPath, actualPath string,
	d fs.DirEntry,
	fileCh chan<- *FileInfo,
) error {
	relPath, err := s.relativePath(virtualPath)
	if err != nil {
		return nil
	}

	if !s.shouldIncludeFile(relPath, d.Name()) {
		return nil
	}

	return s.sendFileInfoFromPath(ctx, actualPath, fileCh)
}

// sendFileInfoFromPath creates and sends FileInfo using os.Stat.
func (s *Scanner) sendFileInfoFromPath(
	ctx context.Context,
	path string,
	fileCh chan<- *FileInfo,
) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	if info.Size() > s.maxFileSize {
		return nil
	}

	fileInfo := buildFileInfo(path, info)

	select {
	case <-ctx.Done():
		return fs.SkipAll
	case fileCh <- fileInfo:
		return nil
	}
}

// =============================================================================
// File Handling
// =============================================================================

// handleFile processes a regular file entry.
func (s *Scanner) handleFile(
	ctx context.Context,
	path string,
	d fs.DirEntry,
	fileCh chan<- *FileInfo,
) error {
	relPath, err := s.relativePath(path)
	if err != nil {
		return nil // Skip files with path issues
	}

	if !s.shouldIncludeFile(relPath, d.Name()) {
		return nil
	}

	return s.sendFileInfo(ctx, path, d, fileCh)
}

// relativePath returns the path relative to the root.
func (s *Scanner) relativePath(path string) (string, error) {
	return filepath.Rel(s.config.RootPath, path)
}

// shouldIncludeFile checks if a file matches inclusion/exclusion criteria.
func (s *Scanner) shouldIncludeFile(relPath, name string) bool {
	if s.matchesExcludePattern(relPath, name) {
		return false
	}

	return s.matchesIncludePattern(relPath, name)
}

// matchesExcludePattern returns true if the file matches any exclude pattern.
func (s *Scanner) matchesExcludePattern(relPath, name string) bool {
	for _, matcher := range s.excludeMatchers {
		if matcher.Match(relPath) || matcher.Match(name) {
			return true
		}
	}
	return false
}

// matchesIncludePattern returns true if the file matches include criteria.
// If no include patterns are specified, all files are included.
func (s *Scanner) matchesIncludePattern(relPath, name string) bool {
	if len(s.includeMatchers) == 0 {
		return true
	}

	for _, matcher := range s.includeMatchers {
		if matcher.Match(relPath) || matcher.Match(name) {
			return true
		}
	}
	return false
}

// =============================================================================
// FileInfo Creation
// =============================================================================

// sendFileInfo creates and sends FileInfo for a file entry.
func (s *Scanner) sendFileInfo(
	ctx context.Context,
	path string,
	d fs.DirEntry,
	fileCh chan<- *FileInfo,
) error {
	info, err := d.Info()
	if err != nil {
		return nil // Skip files we can't stat
	}

	if info.Size() > s.maxFileSize {
		return nil // Skip files exceeding size limit
	}

	fileInfo := buildFileInfo(path, info)

	select {
	case <-ctx.Done():
		return fs.SkipAll
	case fileCh <- fileInfo:
		return nil
	}
}

// buildFileInfo creates a FileInfo from os.FileInfo.
func buildFileInfo(path string, info os.FileInfo) *FileInfo {
	return &FileInfo{
		Path:      path,
		Name:      info.Name(),
		Size:      info.Size(),
		ModTime:   info.ModTime(),
		IsDir:     info.IsDir(),
		Extension: extractExtension(info.Name()),
	}
}

// extractExtension returns the file extension including the dot.
func extractExtension(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return ""
	}
	return strings.ToLower(ext)
}
