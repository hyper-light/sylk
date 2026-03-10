package versioning

import (
	"context"
	"errors"
	"io/fs"
)

var ErrNoActiveSessionVFS = errors.New("no active session VFS for writable global access")

type guardedFallbackFileAccess struct {
	fallback FileAccess
}

func newGuardedFallbackFileAccess(fallback FileAccess) FileAccess {
	if fallback == nil {
		fallback = NewDiskFileAccess(".", true)
	}
	return &guardedFallbackFileAccess{fallback: fallback}
}

func (g *guardedFallbackFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return g.fallback.ReadFile(ctx, path)
}

func (g *guardedFallbackFileAccess) WriteFile(context.Context, string, []byte) error {
	return ErrNoActiveSessionVFS
}

func (g *guardedFallbackFileAccess) EditFile(context.Context, string, []FileEdit) error {
	return ErrNoActiveSessionVFS
}

func (g *guardedFallbackFileAccess) DeleteFile(context.Context, string) error {
	return ErrNoActiveSessionVFS
}

func (g *guardedFallbackFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	return g.fallback.Exists(ctx, path)
}

func (g *guardedFallbackFileAccess) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	return g.fallback.ListDir(ctx, dir)
}

func (g *guardedFallbackFileAccess) Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	return g.fallback.Glob(ctx, root, pattern, exclude)
}

func (g *guardedFallbackFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	return g.fallback.Grep(ctx, root, pattern, include, contextLines, maxMatches)
}

func (g *guardedFallbackFileAccess) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	return g.fallback.Stat(ctx, path)
}

func (g *guardedFallbackFileAccess) WorkingDir() string {
	return g.fallback.WorkingDir()
}

func (g *guardedFallbackFileAccess) IsReadOnly() bool {
	return false
}

var _ FileAccess = (*guardedFallbackFileAccess)(nil)
