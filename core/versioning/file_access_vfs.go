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

// VFSFileAccess provides per-pipeline file access through PipelineVFS.
// Reads overlay staged content over disk. Writes stage in memory (copy-on-write).
// No physical disk writes occur until the pipeline is committed.
type VFSFileAccess struct {
	vfs        *PipelineVFS
	workingDir string
	readOnly   bool
}

// NewVFSFileAccess creates a FileAccess backed by a per-pipeline VFS.
func NewVFSFileAccess(vfs *PipelineVFS, workingDir string) *VFSFileAccess {
	return &VFSFileAccess{
		vfs:        vfs,
		workingDir: workingDir,
	}
}

// NewReadOnlyVFSFileAccess creates a read-only FileAccess backed by a VFS.
func NewReadOnlyVFSFileAccess(vfs *PipelineVFS, workingDir string) *VFSFileAccess {
	return &VFSFileAccess{
		vfs:        vfs,
		workingDir: workingDir,
		readOnly:   true,
	}
}

func (v *VFSFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return v.vfs.Read(ctx, v.resolve(path))
}

func (v *VFSFileAccess) WriteFile(ctx context.Context, path string, content []byte) error {
	if v.readOnly {
		return ErrPermissionDenied
	}
	return v.vfs.Write(ctx, v.resolve(path), content)
}

func (v *VFSFileAccess) EditFile(ctx context.Context, path string, edits []FileEdit) error {
	if v.readOnly {
		return ErrPermissionDenied
	}
	resolved := v.resolve(path)
	content, err := v.vfs.Read(ctx, resolved)
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
	return v.vfs.Write(ctx, resolved, []byte(modified))
}

func (v *VFSFileAccess) DeleteFile(ctx context.Context, path string) error {
	if v.readOnly {
		return ErrPermissionDenied
	}
	return v.vfs.Delete(ctx, v.resolve(path))
}

func (v *VFSFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	return v.vfs.Exists(ctx, v.resolve(path))
}

func (v *VFSFileAccess) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	resolved := v.resolve(dir)
	names, err := v.vfs.List(ctx, resolved)
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		full := filepath.Join(resolved, name)
		info, statErr := os.Stat(full)
		if statErr != nil {
			entries = append(entries, vfsDirEntry{name: name, isDir: v.vfs.HasVisibleDescendant(full)})
			continue
		}
		entries = append(entries, vfsDirEntry{
			name:  name,
			isDir: info.IsDir(),
			info:  info,
		})
	}
	return entries, nil
}

func (v *VFSFileAccess) Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	resolvedRoot := v.resolve(root)
	return v.vfsGlob(ctx, resolvedRoot, pattern, exclude)
}

func (v *VFSFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	resolvedRoot := v.resolve(root)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	if maxMatches <= 0 {
		maxMatches = 100
	}
	return v.vfsGrep(ctx, resolvedRoot, re, include, contextLines, maxMatches)
}

func (v *VFSFileAccess) Stat(_ context.Context, path string) (fs.FileInfo, error) {
	return os.Stat(v.resolve(path))
}

func (v *VFSFileAccess) WorkingDir() string { return v.workingDir }
func (v *VFSFileAccess) IsReadOnly() bool   { return v.readOnly }

// RegisterVisiblePath widens this pipeline VFS to include an exact new path
// before the first write. This keeps sparse task bounds intact while allowing
// adjacent test and harness files to be created deliberately.
func (v *VFSFileAccess) RegisterVisiblePath(path string) {
	if path == "" {
		return
	}
	v.vfs.RegisterVisiblePath(v.resolve(path))
}

// VisiblePaths returns the current visible pipeline-local paths rooted under
// the provided directory.
func (v *VFSFileAccess) VisiblePaths(root string) []string {
	return v.vfs.VisiblePaths(v.resolve(root))
}

// Modifications returns the staged pipeline-local overlay changes.
func (v *VFSFileAccess) Modifications() []FileModification {
	return v.vfs.GetModifications()
}

func (v *VFSFileAccess) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if v.workingDir == "" {
		return path
	}
	return filepath.Join(v.workingDir, path)
}

// vfsGlob walks the VFS overlay (staged + disk - deleted) to find matching files.
func (v *VFSFileAccess) vfsGlob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	if candidates := v.visibleCandidates(root); len(candidates) > 0 {
		matches := make([]string, 0, len(candidates))
		for _, absPath := range candidates {
			rel, err := filepath.Rel(root, absPath)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			if isExcluded(rel, exclude) {
				continue
			}
			if matchGlob(rel, pattern) {
				matches = append(matches, rel)
			}
		}
		return matches, nil
	}

	diskMatches, err := diskGlob(root, pattern, exclude)
	if err != nil {
		return nil, err
	}

	// Filter out VFS-deleted files and add VFS-staged files.
	var matches []string
	seen := make(map[string]bool, len(diskMatches))
	for _, m := range diskMatches {
		absPath := filepath.Join(root, m)
		exists, existsErr := v.vfs.Exists(ctx, absPath)
		if existsErr != nil {
			continue
		}
		if exists {
			matches = append(matches, m)
			seen[m] = true
		}
	}

	// Check staged files not already found on disk.
	v.vfs.mu.RLock()
	staged := make([]string, 0, len(v.vfs.stagedContent))
	for absPath := range v.vfs.stagedContent {
		rel, relErr := filepath.Rel(root, absPath)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		staged = append(staged, rel)
	}
	v.vfs.mu.RUnlock()

	for _, rel := range staged {
		if seen[rel] {
			continue
		}
		if isExcluded(rel, exclude) {
			continue
		}
		if matchGlob(rel, pattern) {
			matches = append(matches, rel)
		}
	}
	return matches, nil
}

// vfsGrep searches files through VFS (overlay reads).
func (v *VFSFileAccess) vfsGrep(ctx context.Context, root string, re *regexp.Regexp, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	if candidates := v.visibleCandidates(root); len(candidates) > 0 {
		return v.grepCandidates(ctx, root, candidates, re, include, contextLines, maxMatches)
	}

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

		// Read through VFS for overlay support.
		content, readErr := v.vfs.Read(ctx, path)
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

func (v *VFSFileAccess) visibleCandidates(root string) []string {
	if v.vfs == nil {
		return nil
	}
	if len(v.vfs.AllowedPaths()) == 0 {
		return nil
	}
	return v.vfs.VisiblePaths(root)
}

func (v *VFSFileAccess) grepCandidates(
	ctx context.Context,
	root string,
	candidates []string,
	re *regexp.Regexp,
	include string,
	contextLines, maxMatches int,
) ([]GrepMatch, error) {
	matches := make([]GrepMatch, 0)
	for _, path := range candidates {
		name := filepath.Base(path)
		if include != "" {
			matched, _ := filepath.Match(include, name)
			if !matched {
				continue
			}
		}
		if isBinaryExt(name) {
			continue
		}

		content, err := v.vfs.Read(ctx, path)
		if err != nil {
			continue
		}

		relPath, _ := filepath.Rel(root, path)
		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if len(matches) >= maxMatches {
				return matches, nil
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
	}
	return matches, nil
}

// vfsDirEntry implements fs.DirEntry for VFS list results.
type vfsDirEntry struct {
	name  string
	isDir bool
	info  fs.FileInfo
}

func (e vfsDirEntry) Name() string               { return e.name }
func (e vfsDirEntry) IsDir() bool                { return e.isDir }
func (e vfsDirEntry) Type() fs.FileMode          { return 0 }
func (e vfsDirEntry) Info() (fs.FileInfo, error) { return e.info, nil }
