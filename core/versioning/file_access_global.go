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

// GlobalVFSFileAccess provides per-session CVS-backed file access for global
// agents (Global Inspector, Global Tester). Reads from the session's CVS
// committed state, falling back to disk for unversioned files. Writes go
// through CVS staging.
type GlobalVFSFileAccess struct {
	cvs        CVS
	workingDir string
	readOnly   bool
}

// NewGlobalVFSFileAccess creates a FileAccess backed by a session's CVS.
func NewGlobalVFSFileAccess(cvs CVS, workingDir string, readOnly bool) *GlobalVFSFileAccess {
	return &GlobalVFSFileAccess{
		cvs:        cvs,
		workingDir: workingDir,
		readOnly:   readOnly,
	}
}

func (g *GlobalVFSFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	resolved := g.resolve(path)
	content, err := g.cvs.Read(ctx, resolved)
	if err == nil {
		return content, nil
	}
	// Fall back to disk for files not yet versioned.
	return os.ReadFile(resolved)
}

func (g *GlobalVFSFileAccess) WriteFile(ctx context.Context, path string, content []byte) error {
	if g.readOnly {
		return fmt.Errorf("global file access is read-only")
	}
	resolved := g.resolve(path)
	_, err := g.cvs.Write(ctx, resolved, content, WriteMetadata{
		AgentRole: "global",
	})
	return err
}

func (g *GlobalVFSFileAccess) EditFile(ctx context.Context, path string, edits []FileEdit) error {
	if g.readOnly {
		return fmt.Errorf("global file access is read-only")
	}
	resolved := g.resolve(path)
	content, err := g.ReadFile(ctx, resolved)
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
	_, err = g.cvs.Write(ctx, resolved, []byte(modified), WriteMetadata{
		AgentRole: "global",
	})
	return err
}

func (g *GlobalVFSFileAccess) DeleteFile(ctx context.Context, path string) error {
	if g.readOnly {
		return fmt.Errorf("global file access is read-only")
	}
	resolved := g.resolve(path)
	_, err := g.cvs.Delete(ctx, resolved, WriteMetadata{
		AgentRole: "global",
	})
	return err
}

func (g *GlobalVFSFileAccess) Exists(_ context.Context, path string) (bool, error) {
	resolved := g.resolve(path)
	// Check CVS first via Read.
	head, err := g.cvs.GetHead(resolved)
	if err == nil && head != nil {
		return true, nil
	}
	// Fall back to disk.
	_, statErr := os.Stat(resolved)
	if statErr == nil {
		return true, nil
	}
	if os.IsNotExist(statErr) {
		return false, nil
	}
	return false, statErr
}

func (g *GlobalVFSFileAccess) ListDir(_ context.Context, dir string) ([]fs.DirEntry, error) {
	return os.ReadDir(g.resolve(dir))
}

func (g *GlobalVFSFileAccess) Glob(_ context.Context, root, pattern string, exclude []string) ([]string, error) {
	return diskGlob(g.resolve(root), pattern, exclude)
}

func (g *GlobalVFSFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	resolvedRoot := g.resolve(root)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	if maxMatches <= 0 {
		maxMatches = 100
	}
	return g.globalGrep(ctx, resolvedRoot, re, include, contextLines, maxMatches)
}

func (g *GlobalVFSFileAccess) Stat(_ context.Context, path string) (fs.FileInfo, error) {
	return os.Stat(g.resolve(path))
}

func (g *GlobalVFSFileAccess) WorkingDir() string { return g.workingDir }
func (g *GlobalVFSFileAccess) IsReadOnly() bool   { return g.readOnly }

func (g *GlobalVFSFileAccess) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if g.workingDir == "" {
		return path
	}
	return filepath.Join(g.workingDir, path)
}

// globalGrep searches files, reading through CVS for versioned content
// and falling back to disk for unversioned files.
func (g *GlobalVFSFileAccess) globalGrep(ctx context.Context, root string, re *regexp.Regexp, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	var matches []GrepMatch
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
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

		// Try CVS first, fall back to disk.
		content, readErr := g.cvs.Read(ctx, path)
		if readErr != nil {
			content, readErr = os.ReadFile(path)
			if readErr != nil {
				return nil
			}
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
