package versioning

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

type WorkspaceMutationOrigin string

const (
	WorkspaceMutationOriginUnknown          WorkspaceMutationOrigin = ""
	WorkspaceMutationOriginCommandExecution WorkspaceMutationOrigin = "command_execution"
)

type FilePersistence string

const (
	FilePersistenceDurable   FilePersistence = "durable"
	FilePersistenceEphemeral FilePersistence = "ephemeral"
)

type workspaceMutationOriginKey struct{}

var transientExecutionRootNames = map[string]struct{}{
	".git":          {},
	".hg":           {},
	".hypothesis":   {},
	".mypy_cache":   {},
	".nox":          {},
	".pytest_cache": {},
	".ruff_cache":   {},
	".svn":          {},
	".sylk":         {},
	".tox":          {},
	"__pycache__":   {},
}

func WithWorkspaceMutationOrigin(ctx context.Context, origin WorkspaceMutationOrigin) context.Context {
	if ctx == nil || origin == "" {
		return ctx
	}
	return context.WithValue(ctx, workspaceMutationOriginKey{}, origin)
}

func WorkspaceMutationOriginFromContext(ctx context.Context) WorkspaceMutationOrigin {
	if ctx == nil {
		return WorkspaceMutationOriginUnknown
	}
	origin, _ := ctx.Value(workspaceMutationOriginKey{}).(WorkspaceMutationOrigin)
	return origin
}

func classifyWorkspaceMutation(ctx context.Context, workingDir, resolvedPath string, isDir bool) FilePersistence {
	if WorkspaceMutationOriginFromContext(ctx) != WorkspaceMutationOriginCommandExecution {
		return FilePersistenceDurable
	}
	if isTransientExecutionArtifactPath(workingDir, resolvedPath, isDir) {
		return FilePersistenceEphemeral
	}
	return FilePersistenceDurable
}

func persistentFileModifications(mods []FileModification) []FileModification {
	if len(mods) == 0 {
		return nil
	}
	filtered := make([]FileModification, 0, len(mods))
	for _, mod := range mods {
		if mod.Persistence == FilePersistenceEphemeral {
			continue
		}
		filtered = append(filtered, mod)
	}
	return filtered
}

func ephemeralFileModifications(mods []FileModification) []FileModification {
	if len(mods) == 0 {
		return nil
	}
	filtered := make([]FileModification, 0, len(mods))
	for _, mod := range mods {
		if mod.Persistence != FilePersistenceEphemeral {
			continue
		}
		filtered = append(filtered, mod)
	}
	return filtered
}

func isTransientExecutionArtifactPath(workingDir, resolvedPath string, isDir bool) bool {
	rel, ok := workspaceRelativeSlashPath(workingDir, resolvedPath)
	if !ok {
		return false
	}
	if rel == "" {
		return false
	}
	if matchesTransientExecutionSegments(rel) || matchesTransientExecutionFile(rel) {
		return true
	}
	return workspacePathIgnoredByGit(workingDir, resolvedPath, isDir)
}

func workspaceRelativeSlashPath(workingDir, resolvedPath string) (string, bool) {
	root := filepath.Clean(strings.TrimSpace(workingDir))
	target := filepath.Clean(strings.TrimSpace(resolvedPath))
	if root == "" || target == "" {
		return "", false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func matchesTransientExecutionSegments(rel string) bool {
	parts := strings.Split(strings.Trim(strings.TrimSpace(rel), "/"), "/")
	if len(parts) == 0 {
		return false
	}
	for i, part := range parts {
		if _, ok := transientExecutionRootNames[part]; ok {
			return true
		}
		if i > 0 && parts[i-1] == "node_modules" && part == ".cache" {
			return true
		}
	}
	return false
}

func matchesTransientExecutionFile(rel string) bool {
	name := strings.ToLower(filepath.Base(rel))
	switch {
	case strings.HasSuffix(name, ".pyc"):
		return true
	case strings.HasSuffix(name, ".pyo"):
		return true
	case strings.HasSuffix(name, ".pyd"):
		return true
	case strings.HasSuffix(name, ".py,cover"):
		return true
	case name == ".coverage":
		return true
	case strings.HasPrefix(name, ".coverage."):
		return true
	case name == ".eslintcache":
		return true
	default:
		return false
	}
}

func workspacePathIgnoredByGit(workingDir, resolvedPath string, isDir bool) bool {
	rel, ok := workspaceRelativeSlashPath(workingDir, resolvedPath)
	if !ok {
		return false
	}
	root, err := filepath.Abs(filepath.Clean(workingDir))
	if err != nil {
		return false
	}
	parts := splitWorkspacePathParts(rel)
	if len(parts) == 0 {
		return false
	}
	patterns := workspaceImageRootPatterns(root)
	dir := root
	dirParts := make([]string, 0, len(parts))
	for i := 0; i < len(parts)-1; i++ {
		dir = filepath.Join(dir, parts[i])
		dirParts = append(dirParts, parts[i])
		patterns = workspaceImageExtendPatterns(patterns, workspaceImageLocalPatterns(dir, dirParts))
	}
	return gitignore.NewMatcher(patterns).Match(parts, isDir)
}

func splitWorkspacePathParts(rel string) []string {
	trimmed := strings.Trim(strings.TrimSpace(filepath.ToSlash(rel)), "/")
	if trimmed == "" || trimmed == "." {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		out = append(out, part)
	}
	return out
}

type executionFSLike interface {
	ReadFile(ctx context.Context, path string) ([]byte, error)
	MkdirAll(ctx context.Context, path string) error
	WriteFile(ctx context.Context, path string, content []byte) error
	DeleteFile(ctx context.Context, path string) error
	Exists(ctx context.Context, path string) (bool, error)
	ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error)
	Stat(ctx context.Context, path string) (fs.FileInfo, error)
	WorkingDir() string
	IsReadOnly() bool
}

type executionMutationTaggedFS struct {
	base   executionFSLike
	origin WorkspaceMutationOrigin
}

func TagExecutionFS(base executionFSLike, origin WorkspaceMutationOrigin) executionFSLike {
	if base == nil || origin == "" {
		return base
	}
	return executionMutationTaggedFS{base: base, origin: origin}
}

func (e executionMutationTaggedFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return e.base.ReadFile(ctx, path)
}

func (e executionMutationTaggedFS) MkdirAll(ctx context.Context, path string) error {
	return e.base.MkdirAll(WithWorkspaceMutationOrigin(ctx, e.origin), path)
}

func (e executionMutationTaggedFS) WriteFile(ctx context.Context, path string, content []byte) error {
	return e.base.WriteFile(WithWorkspaceMutationOrigin(ctx, e.origin), path, content)
}

func (e executionMutationTaggedFS) DeleteFile(ctx context.Context, path string) error {
	return e.base.DeleteFile(WithWorkspaceMutationOrigin(ctx, e.origin), path)
}

func (e executionMutationTaggedFS) Exists(ctx context.Context, path string) (bool, error) {
	return e.base.Exists(ctx, path)
}

func (e executionMutationTaggedFS) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	return e.base.ListDir(ctx, dir)
}

func (e executionMutationTaggedFS) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	return e.base.Stat(ctx, path)
}

func (e executionMutationTaggedFS) WorkingDir() string {
	return e.base.WorkingDir()
}

func (e executionMutationTaggedFS) IsReadOnly() bool {
	return e.base.IsReadOnly()
}
