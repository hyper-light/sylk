package versioning

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

type BaseFSFileAccess struct {
	base       vfsBaseFS
	workingDir string
}

func NewBaseFSFileAccess(base vfsBaseFS, workingDir string) *BaseFSFileAccess {
	return &BaseFSFileAccess{
		base:       base,
		workingDir: workingDir,
	}
}

func (b *BaseFSFileAccess) ReadFile(_ context.Context, path string) ([]byte, error) {
	return b.base.ReadFile(b.resolve(path))
}

func (b *BaseFSFileAccess) MkdirAll(context.Context, string) error {
	return ErrPermissionDenied
}

func (b *BaseFSFileAccess) WriteFile(context.Context, string, []byte) error {
	return ErrPermissionDenied
}

func (b *BaseFSFileAccess) EditFile(context.Context, string, []FileEdit) error {
	return ErrPermissionDenied
}

func (b *BaseFSFileAccess) DeleteFile(context.Context, string) error {
	return ErrPermissionDenied
}

func (b *BaseFSFileAccess) Exists(_ context.Context, path string) (bool, error) {
	return b.base.Exists(b.resolve(path))
}

func (b *BaseFSFileAccess) ListDir(_ context.Context, dir string) ([]fs.DirEntry, error) {
	resolved := b.resolve(dir)
	names, err := b.base.ListDir(resolved)
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		info, statErr := b.base.Stat(filepath.Join(resolved, name))
		if statErr != nil {
			return nil, statErr
		}
		entries = append(entries, overlayDirEntry{name: name, info: info})
	}
	return entries, nil
}

func (b *BaseFSFileAccess) Glob(_ context.Context, root, pattern string, exclude []string) ([]string, error) {
	searchRoot := b.resolve(root)
	matches := make([]string, 0, 16)
	err := b.walk(searchRoot, func(fullPath string, info fs.FileInfo) error {
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(searchRoot, fullPath)
		if err != nil || isExcluded(rel, exclude) {
			return err
		}
		if matchGlob(rel, pattern) {
			matches = append(matches, rel)
		}
		return nil
	})
	return matches, err
}

func (b *BaseFSFileAccess) Grep(_ context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	if maxMatches <= 0 {
		maxMatches = 100
	}
	searchRoot := b.resolve(root)
	matches := make([]GrepMatch, 0, maxMatches)
	err = b.walk(searchRoot, func(fullPath string, info fs.FileInfo) error {
		if info.IsDir() || (include != "" && !baseFSIncludeMatch(include, fullPath)) || isBinaryExt(info.Name()) {
			return nil
		}
		content, err := b.base.ReadFile(fullPath)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(searchRoot, fullPath)
		if err != nil {
			return err
		}
		baseFSMatches := grepContent(rel, string(content), re, contextLines, maxMatches-len(matches))
		matches = append(matches, baseFSMatches...)
		if len(matches) >= maxMatches {
			return fs.SkipAll
		}
		return nil
	})
	return matches, err
}

func (b *BaseFSFileAccess) Stat(_ context.Context, path string) (fs.FileInfo, error) {
	return b.base.Stat(b.resolve(path))
}

func (b *BaseFSFileAccess) WorkingDir() string { return b.workingDir }
func (b *BaseFSFileAccess) IsReadOnly() bool   { return true }

func (b *BaseFSFileAccess) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if b.workingDir == "" {
		return path
	}
	return filepath.Join(b.workingDir, path)
}

func (b *BaseFSFileAccess) walk(root string, visit func(string, fs.FileInfo) error) error {
	info, err := b.base.Stat(root)
	if err != nil {
		return err
	}
	if err := visit(root, info); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return b.walkChildren(root, visit)
}

func (b *BaseFSFileAccess) walkChildren(root string, visit func(string, fs.FileInfo) error) error {
	names, err := b.base.ListDir(root)
	if err != nil {
		return err
	}
	for _, name := range names {
		child := filepath.Join(root, name)
		if err := b.walk(child, visit); err != nil {
			return err
		}
	}
	return nil
}

func baseFSIncludeMatch(include, fullPath string) bool {
	matched, _ := filepath.Match(include, filepath.Base(fullPath))
	return matched
}

func grepContent(relPath, content string, re *regexp.Regexp, contextLines, remaining int) []GrepMatch {
	lines := strings.Split(content, "\n")
	matches := make([]GrepMatch, 0, remaining)
	for i, line := range lines {
		if len(matches) >= remaining {
			break
		}
		if !re.MatchString(line) {
			continue
		}
		match := GrepMatch{
			File:    relPath,
			Line:    i + 1,
			Content: strings.TrimSpace(line),
		}
		if contextLines > 0 {
			start := max(0, i-contextLines)
			end := min(len(lines), i+contextLines+1)
			match.Context = strings.Join(lines[start:end], "\n")
		}
		matches = append(matches, match)
	}
	return matches
}

var _ FileAccess = (*BaseFSFileAccess)(nil)
