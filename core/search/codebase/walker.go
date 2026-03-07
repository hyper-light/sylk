// Package codebase provides gitignore-aware, parallel codebase search with
// tree-sitter structural context. It composes the project's existing
// gitignore walker, gobwas/glob matching, and tree-sitter symbol extraction
// into a unified search API for the librarian agent.
package codebase

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/gobwas/glob"
)

// maxWalkDepth bounds recursive traversal.
const maxWalkDepth = 32

// Walker discovers files in a project directory, respecting .gitignore rules
// and optional include/exclude glob filters.
type Walker struct {
	root    string
	include []glob.Glob
	exclude []glob.Glob
}

// NewWalker creates a Walker rooted at the given directory.
func NewWalker(root string) *Walker {
	return &Walker{root: root}
}

// SetInclude compiles and sets include glob patterns.
// Only files matching at least one include pattern are returned.
func (w *Walker) SetInclude(patterns []string) error {
	compiled, err := compileGlobs(patterns)
	if err != nil {
		return err
	}
	w.include = compiled
	return nil
}

// SetExclude compiles and sets exclude glob patterns.
func (w *Walker) SetExclude(patterns []string) error {
	compiled, err := compileGlobs(patterns)
	if err != nil {
		return err
	}
	w.exclude = compiled
	return nil
}

// Collect returns all matching file paths (relative to root).
// Respects .gitignore at every directory level, skips hidden dirs and
// well-known non-source dirs (.git, node_modules, vendor, etc.).
func (w *Walker) Collect() []string {
	basePatterns := rootIgnorePatterns(w.root)
	var files []string
	w.walkCollect(w.root, nil, basePatterns, &files)
	return files
}

func (w *Walker) walkCollect(absPath string, relParts []string, parentPatterns []gitignore.Pattern, files *[]string) {
	if len(relParts) >= maxWalkDepth {
		return
	}
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return
	}

	localPats := localIgnorePatterns(absPath, relParts)
	allPats := extendPatterns(parentPatterns, localPats)
	matcher := gitignore.NewMatcher(allPats)

	for _, de := range entries {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		childRel := append(relParts[:len(relParts):len(relParts)], name)
		childAbs := filepath.Join(absPath, name)

		if de.IsDir() {
			if matcher.Match(childRel, true) || isExcludedDir(name) {
				continue
			}
			w.walkCollect(childAbs, childRel, allPats, files)
			continue
		}

		if matcher.Match(childRel, false) {
			continue
		}

		relPath := strings.Join(childRel, "/")
		if !w.matchesInclude(relPath) {
			continue
		}
		if w.matchesExclude(relPath) {
			continue
		}
		*files = append(*files, relPath)
	}
}

func (w *Walker) matchesInclude(rel string) bool {
	if len(w.include) == 0 {
		return true
	}
	for _, g := range w.include {
		if g.Match(rel) {
			return true
		}
	}
	return false
}

func (w *Walker) matchesExclude(rel string) bool {
	for _, g := range w.exclude {
		if g.Match(rel) {
			return true
		}
	}
	return false
}

// excludedDirs are always pruned regardless of .gitignore.
var excludedDirs = map[string]struct{}{
	"node_modules": {}, "vendor": {}, "__pycache__": {},
	".next": {}, "dist": {}, "build": {}, ".cache": {},
	"target": {}, "bin": {}, "obj": {},
}

func isExcludedDir(name string) bool {
	_, ok := excludedDirs[name]
	return ok
}

// ---------------------------------------------------------------------------
// gitignore helpers (extracted from ui/filetree/search_ignore.go patterns)
// ---------------------------------------------------------------------------

func rootIgnorePatterns(rootPath string) []gitignore.Pattern {
	var patterns []gitignore.Pattern
	patterns = appendIgnoreFile(patterns, filepath.Join(rootPath, ".gitignore"), nil)
	patterns = appendIgnoreFile(patterns, filepath.Join(rootPath, ".git", "info", "exclude"), nil)
	return patterns
}

func localIgnorePatterns(dirAbs string, relParts []string) []gitignore.Pattern {
	return readIgnorePatterns(filepath.Join(dirAbs, ".gitignore"), relParts)
}

func readIgnorePatterns(path string, domain []string) []gitignore.Pattern {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	return patterns
}

func appendIgnoreFile(dst []gitignore.Pattern, path string, domain []string) []gitignore.Pattern {
	return append(dst, readIgnorePatterns(path, domain)...)
}

func extendPatterns(parent, local []gitignore.Pattern) []gitignore.Pattern {
	if len(local) == 0 {
		return parent
	}
	combined := make([]gitignore.Pattern, len(parent)+len(local))
	copy(combined, parent)
	copy(combined[len(parent):], local)
	return combined
}

// ---------------------------------------------------------------------------
// glob compilation
// ---------------------------------------------------------------------------

var globCache sync.Map // string → glob.Glob

func compileGlobs(patterns []string) ([]glob.Glob, error) {
	result := make([]glob.Glob, 0, len(patterns))
	for _, p := range patterns {
		if cached, ok := globCache.Load(p); ok {
			result = append(result, cached.(glob.Glob))
			continue
		}
		g, err := glob.Compile(p, '/')
		if err != nil {
			return nil, err
		}
		globCache.Store(p, g)
		result = append(result, g)
	}
	return result, nil
}
