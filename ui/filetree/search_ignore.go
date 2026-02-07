package filetree

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// ---------------------------------------------------------------------------
// Gitignore-aware file collection
// ---------------------------------------------------------------------------

// maxWalkDepth bounds the recursive walk to prevent runaway recursion.
// Derived from: 32 levels covers the deepest reasonable project structures.
const maxWalkDepth = 32

// collectSearchableFiles walks rootPath recursively, respecting .gitignore
// rules at every directory level, and returns all non-ignored, non-hidden
// file paths. Entire ignored directory trees are pruned at the directory
// level — a single matcher.Match call skips node_modules/, vendor/, etc.
// without descending into them.
//
// No artificial file count limit; output volume is bounded downstream by
// maxTotalMatches.
func collectSearchableFiles(rootPath string) []string {
	basePatterns := rootIgnorePatterns(rootPath)
	var files []string
	walkCollect(rootPath, nil, basePatterns, &files)
	return files
}

// rootIgnorePatterns reads the project-level ignore rules:
// .gitignore at the project root and .git/info/exclude.
func rootIgnorePatterns(rootPath string) []gitignore.Pattern {
	var patterns []gitignore.Pattern
	patterns = appendIgnoreFile(patterns, filepath.Join(rootPath, ".gitignore"), nil)
	patterns = appendIgnoreFile(patterns, filepath.Join(rootPath, ".git", "info", "exclude"), nil)
	return patterns
}

// walkCollect recursively collects non-ignored, non-hidden file paths from
// absPath. parentPatterns accumulate gitignore rules from ancestor dirs.
// relParts is the slash-split relative path from the project root.
func walkCollect(absPath string, relParts []string, parentPatterns []gitignore.Pattern, files *[]string) {
	if len(relParts) >= maxWalkDepth {
		return
	}
	dirEntries, err := os.ReadDir(absPath)
	if err != nil {
		return
	}

	localPatterns := localIgnorePatterns(absPath, relParts)
	allPatterns := extendPatterns(parentPatterns, localPatterns)
	matcher := gitignore.NewMatcher(allPatterns)

	for _, de := range dirEntries {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		childRel := append(relParts[:len(relParts):len(relParts)], name)
		childAbs := filepath.Join(absPath, name)
		dispatchEntry(de, childAbs, childRel, allPatterns, matcher, files)
	}
}

// dispatchEntry routes a directory entry through the ignore matcher and
// either recurses into a directory or appends a file path.
func dispatchEntry(
	de os.DirEntry,
	childAbs string,
	childRel []string,
	allPatterns []gitignore.Pattern,
	matcher gitignore.Matcher,
	files *[]string,
) {
	if de.IsDir() {
		if matcher.Match(childRel, true) {
			return
		}
		walkCollect(childAbs, childRel, allPatterns, files)
		return
	}
	if matcher.Match(childRel, false) {
		return
	}
	*files = append(*files, childAbs)
}

// localIgnorePatterns reads a .gitignore file from the given directory,
// scoping its patterns to the directory's relative path.
func localIgnorePatterns(dirAbs string, relParts []string) []gitignore.Pattern {
	return readIgnorePatterns(filepath.Join(dirAbs, ".gitignore"), relParts)
}

// readIgnorePatterns reads a gitignore-format file and parses each
// non-comment, non-blank line into a gitignore.Pattern scoped to domain.
func readIgnorePatterns(path string, domain []string) []gitignore.Pattern {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseIgnoreLines(string(data), domain)
}

// parseIgnoreLines splits raw gitignore content into lines and parses each
// meaningful line into a gitignore.Pattern.
func parseIgnoreLines(content string, domain []string) []gitignore.Pattern {
	lines := strings.Split(content, "\n")
	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if isIgnoreComment(line) {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	return patterns
}

// isIgnoreComment returns true for blank lines and comment lines.
func isIgnoreComment(line string) bool {
	return line == "" || strings.HasPrefix(line, "#")
}

// appendIgnoreFile reads an ignore file and appends its patterns to dst.
func appendIgnoreFile(dst []gitignore.Pattern, path string, domain []string) []gitignore.Pattern {
	patterns := readIgnorePatterns(path, domain)
	return append(dst, patterns...)
}

// extendPatterns returns a combined pattern list with child patterns appended
// to a copy of parent patterns. Returns parentPatterns unchanged if there are
// no local patterns to add (avoids allocation).
func extendPatterns(parentPatterns, localPatterns []gitignore.Pattern) []gitignore.Pattern {
	if len(localPatterns) == 0 {
		return parentPatterns
	}
	combined := make([]gitignore.Pattern, len(parentPatterns)+len(localPatterns))
	copy(combined, parentPatterns)
	copy(combined[len(parentPatterns):], localPatterns)
	return combined
}
