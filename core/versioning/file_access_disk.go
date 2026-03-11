package versioning

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DiskFileAccess provides direct filesystem access for agents that operate
// on the physical working directory (Architect, Guide, Librarian, etc.).
type DiskFileAccess struct {
	workingDir string
	readOnly   bool
}

// NewDiskFileAccess creates a FileAccess backed by direct OS calls.
func NewDiskFileAccess(workingDir string, readOnly bool) *DiskFileAccess {
	return &DiskFileAccess{
		workingDir: workingDir,
		readOnly:   readOnly,
	}
}

func (d *DiskFileAccess) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(d.resolve(path))
}

func (d *DiskFileAccess) MkdirAll(_ context.Context, path string) error {
	if d.readOnly {
		return fmt.Errorf("disk file access is read-only")
	}
	return os.MkdirAll(d.resolve(path), 0755)
}

func (d *DiskFileAccess) WriteFile(_ context.Context, path string, content []byte) error {
	if d.readOnly {
		return fmt.Errorf("disk file access is read-only")
	}
	full := d.resolve(path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	return os.WriteFile(full, content, 0644)
}

func (d *DiskFileAccess) EditFile(_ context.Context, path string, edits []FileEdit) error {
	if d.readOnly {
		return fmt.Errorf("disk file access is read-only")
	}
	full := d.resolve(path)
	content, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	modified := string(content)
	for i, edit := range edits {
		if !strings.Contains(modified, edit.OldText) {
			return fmt.Errorf("edit %d: old_text not found in file", i)
		}
		modified = strings.Replace(modified, edit.OldText, edit.NewText, 1)
	}
	return os.WriteFile(full, []byte(modified), 0644)
}

func (d *DiskFileAccess) DeleteFile(_ context.Context, path string) error {
	if d.readOnly {
		return fmt.Errorf("disk file access is read-only")
	}
	return os.Remove(d.resolve(path))
}

func (d *DiskFileAccess) Exists(_ context.Context, path string) (bool, error) {
	_, err := os.Stat(d.resolve(path))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (d *DiskFileAccess) ListDir(_ context.Context, dir string) ([]fs.DirEntry, error) {
	return os.ReadDir(d.resolve(dir))
}

func (d *DiskFileAccess) Glob(_ context.Context, root, pattern string, exclude []string) ([]string, error) {
	searchRoot := d.resolve(root)
	return diskGlob(searchRoot, pattern, exclude)
}

func (d *DiskFileAccess) Grep(_ context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	searchRoot := d.resolve(root)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	if maxMatches <= 0 {
		maxMatches = 100
	}
	return diskGrep(searchRoot, re, include, contextLines, maxMatches)
}

func (d *DiskFileAccess) Stat(_ context.Context, path string) (fs.FileInfo, error) {
	return os.Stat(d.resolve(path))
}

func (d *DiskFileAccess) WorkingDir() string { return d.workingDir }
func (d *DiskFileAccess) IsReadOnly() bool   { return d.readOnly }

func (d *DiskFileAccess) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if d.workingDir == "" {
		return path
	}
	return filepath.Join(d.workingDir, path)
}

// diskGlob walks the filesystem matching files against a glob pattern.
func diskGlob(root, pattern string, exclude []string) ([]string, error) {
	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if isExcluded(relPath, exclude) {
			return nil
		}
		if matchGlob(relPath, pattern) {
			matches = append(matches, relPath)
		}
		return nil
	})
	return matches, err
}

// diskGrep searches files for regex matches.
func diskGrep(root string, re *regexp.Regexp, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	var matches []GrepMatch
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if include != "" {
			matched, _ := filepath.Match(include, d.Name())
			if !matched {
				return nil
			}
		}
		if isBinaryExt(d.Name()) {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		relPath, _ := filepath.Rel(root, path)
		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if len(matches) >= maxMatches {
				return filepath.SkipAll
			}
			if re.MatchString(line) {
				m := GrepMatch{
					File:    relPath,
					Line:    i + 1,
					Content: strings.TrimSpace(line),
				}
				if contextLines > 0 {
					start := max(0, i-contextLines)
					end := min(len(lines), i+contextLines+1)
					m.Context = strings.Join(lines[start:end], "\n")
				}
				matches = append(matches, m)
			}
		}
		return nil
	})
	return matches, err
}

// isExcluded checks if a path matches any exclusion pattern.
func isExcluded(relPath string, exclude []string) bool {
	for _, excl := range exclude {
		if matched, _ := filepath.Match(excl, relPath); matched {
			return true
		}
		if strings.Contains(excl, "**") {
			simple := strings.ReplaceAll(excl, "**", "*")
			if matched, _ := filepath.Match(simple, relPath); matched {
				return true
			}
		}
	}
	return false
}

// matchGlob matches a path against a glob pattern, handling ** patterns.
func matchGlob(relPath, pattern string) bool {
	if matched, err := filepath.Match(pattern, relPath); err == nil && matched {
		return true
	}
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "/")
		if len(parts) > 0 {
			last := parts[len(parts)-1]
			if strings.HasPrefix(last, "*.") {
				ext := strings.TrimPrefix(last, "*")
				return strings.HasSuffix(relPath, ext)
			}
		}
	}
	return false
}

// binaryExtensions is the set of file extensions treated as binary.
var binaryExtensions = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".bin": true, ".o": true, ".a": true, ".png": true,
	".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".pdf": true, ".zip": true, ".tar": true, ".gz": true,
}

// isBinaryExt reports whether a filename has a known binary extension.
func isBinaryExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return binaryExtensions[ext]
}
