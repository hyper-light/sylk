package ingestion

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gobwas/glob"
)

// =============================================================================
// File Discovery
// =============================================================================

// DiscoverFiles finds all parseable files in the given root directory.
// Uses parallel directory traversal for performance.
// Respects gitignore patterns and skips hidden directories.
func DiscoverFiles(ctx context.Context, root string, ignorePatterns []string) ([]FileInfo, error) {
	matcher, err := compileIgnorePatterns(ignorePatterns)
	if err != nil {
		return nil, err
	}

	d := &discoverer{
		root:    root,
		matcher: matcher,
		files:   NewAccumulator[FileInfo](EstimateCapacity(100000).Files), // Pre-allocate for 1M LOC
	}

	return d.discover(ctx)
}

// discoverer holds state for file discovery.
type discoverer struct {
	root    string
	matcher *ignoreMatcher
	files   *Accumulator[FileInfo]
}

// discover performs the parallel file discovery.
func (d *discoverer) discover(ctx context.Context) ([]FileInfo, error) {
	entries, err := d.readTopLevel()
	if err != nil {
		return nil, err
	}

	if err := d.scanParallel(ctx, entries); err != nil {
		return nil, err
	}

	return d.files.Items(), nil
}

// readTopLevel reads the top-level directory entries.
func (d *discoverer) readTopLevel() ([]fs.DirEntry, error) {
	return os.ReadDir(d.root)
}

// scanParallel processes top-level directories in parallel.
func (d *discoverer) scanParallel(ctx context.Context, entries []fs.DirEntry) error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(entries))

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			break
		}

		if shouldSkipEntry(entry) {
			continue
		}

		wg.Add(1)
		go d.scanEntry(ctx, entry, &wg, errChan)
	}

	wg.Wait()
	close(errChan)

	return collectFirstError(errChan)
}

// scanEntry processes a single top-level entry (file or directory).
func (d *discoverer) scanEntry(ctx context.Context, entry fs.DirEntry, wg *sync.WaitGroup, errChan chan<- error) {
	defer wg.Done()

	path := filepath.Join(d.root, entry.Name())

	if entry.IsDir() {
		d.scanDirectory(ctx, path, errChan)
		return
	}

	d.processFile(path)
}

// scanDirectory recursively scans a directory.
func (d *discoverer) scanDirectory(ctx context.Context, dirPath string, errChan chan<- error) {
	err := filepath.WalkDir(dirPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		return d.visitPath(path, entry)
	})

	if err != nil && err != context.Canceled {
		select {
		case errChan <- err:
		default:
		}
	}
}

// visitPath processes a single path during directory walk.
func (d *discoverer) visitPath(path string, entry fs.DirEntry) error {
	relPath := d.relativePath(path)

	if entry.IsDir() {
		return d.handleDirectory(relPath, entry.Name())
	}

	return d.handleFile(path, relPath)
}

// handleDirectory decides whether to skip a directory.
func (d *discoverer) handleDirectory(relPath, name string) error {
	if shouldSkipDirName(name) {
		return fs.SkipDir
	}

	if d.matcher.matches(relPath + "/") {
		return fs.SkipDir
	}

	return nil
}

// handleFile processes a single file.
func (d *discoverer) handleFile(path, relPath string) error {
	if d.matcher.matches(relPath) {
		return nil
	}

	d.processFile(path)
	return nil
}

// processFile adds a file to the accumulator if it's parseable.
func (d *discoverer) processFile(path string) {
	ext := extractExtension(path)
	if !IsSupportedExtension(ext) {
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		return
	}

	if info.Size() > MaxFileSizeBytes {
		return
	}

	d.files.Append(FileInfo{
		Path:    path,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	})
}

// relativePath returns the path relative to the root.
func (d *discoverer) relativePath(path string) string {
	rel, err := filepath.Rel(d.root, path)
	if err != nil {
		return path
	}
	return rel
}

// =============================================================================
// Ignore Pattern Matching
// =============================================================================

// ignoreMatcher holds compiled gitignore patterns.
type ignoreMatcher struct {
	patterns []glob.Glob
}

// compileIgnorePatterns compiles gitignore patterns into matchers.
func compileIgnorePatterns(patterns []string) (*ignoreMatcher, error) {
	patterns = appendDefaultPatterns(patterns)

	compiled := make([]glob.Glob, 0, len(patterns))
	for _, pattern := range patterns {
		g, err := glob.Compile(pattern)
		if err != nil {
			continue // Skip invalid patterns
		}
		compiled = append(compiled, g)
	}

	return &ignoreMatcher{patterns: compiled}, nil
}

// appendDefaultPatterns adds common ignore patterns.
func appendDefaultPatterns(patterns []string) []string {
	defaults := []string{
		".git/**",
		".git/",
		"node_modules/**",
		"node_modules/",
		"vendor/**",
		"vendor/",
		"__pycache__/**",
		"__pycache__/",
		".venv/**",
		".venv/",
		"venv/**",
		"venv/",
		"dist/**",
		"dist/",
		"build/**",
		"build/",
		"*.min.js",
		"*.min.css",
		"*.bundle.js",
		"*.map",
		"*.lock",
		"go.sum",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
	}

	return append(defaults, patterns...)
}

// matches returns true if the path matches any ignore pattern.
func (m *ignoreMatcher) matches(path string) bool {
	path = filepath.ToSlash(path) // Normalize path separators
	for _, g := range m.patterns {
		if g.Match(path) {
			return true
		}
	}
	return false
}

// =============================================================================
// Helper Functions
// =============================================================================

// shouldSkipEntry returns true if the entry should be skipped entirely.
func shouldSkipEntry(entry fs.DirEntry) bool {
	name := entry.Name()
	return shouldSkipDirName(name) || strings.HasPrefix(name, ".")
}

// shouldSkipDirName returns true if the directory name should be skipped.
func shouldSkipDirName(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".bzr":
		return true
	case "node_modules", "vendor", "__pycache__", ".venv", "venv":
		return true
	case "dist", "build", "target", "out":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

// extractExtension extracts the file extension including the dot.
func extractExtension(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		switch path[i] {
		case '.':
			return path[i:]
		case '/', '\\':
			return ""
		}
	}
	return ""
}

// collectFirstError returns the first error from the channel, or nil.
func collectFirstError(errChan <-chan error) error {
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	return nil
}
