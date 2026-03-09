package versioning

import (
	"context"
	"io/fs"
)

// SessionRoutingFileAccess resolves each operation against the active
// session's in-memory global overlay when a session ID is present in ctx.
// It falls back to the provided FileAccess when no session-scoped VFS exists.
type SessionRoutingFileAccess struct {
	readOnly bool
	lookup   func(sessionID string) *SessionVFS
	fallback FileAccess
}

// NewSessionRoutingFileAccess creates a file-access router that prefers the
// current session's global overlay and otherwise falls back to the provided
// FileAccess implementation.
func NewSessionRoutingFileAccess(
	readOnly bool,
	lookup func(sessionID string) *SessionVFS,
	fallback FileAccess,
) *SessionRoutingFileAccess {
	return &SessionRoutingFileAccess{
		readOnly: readOnly,
		lookup:   lookup,
		fallback: fallback,
	}
}

func (s *SessionRoutingFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return s.resolve(ctx).ReadFile(ctx, path)
}

func (s *SessionRoutingFileAccess) WriteFile(ctx context.Context, path string, content []byte) error {
	return s.resolve(ctx).WriteFile(ctx, path, content)
}

func (s *SessionRoutingFileAccess) EditFile(ctx context.Context, path string, edits []FileEdit) error {
	return s.resolve(ctx).EditFile(ctx, path, edits)
}

func (s *SessionRoutingFileAccess) DeleteFile(ctx context.Context, path string) error {
	return s.resolve(ctx).DeleteFile(ctx, path)
}

func (s *SessionRoutingFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	return s.resolve(ctx).Exists(ctx, path)
}

func (s *SessionRoutingFileAccess) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	return s.resolve(ctx).ListDir(ctx, dir)
}

func (s *SessionRoutingFileAccess) Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	return s.resolve(ctx).Glob(ctx, root, pattern, exclude)
}

func (s *SessionRoutingFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	return s.resolve(ctx).Grep(ctx, root, pattern, include, contextLines, maxMatches)
}

func (s *SessionRoutingFileAccess) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	return s.resolve(ctx).Stat(ctx, path)
}

func (s *SessionRoutingFileAccess) WorkingDir() string {
	return s.resolve(nil).WorkingDir()
}

func (s *SessionRoutingFileAccess) IsReadOnly() bool {
	return s.readOnly
}

func (s *SessionRoutingFileAccess) resolve(ctx context.Context) FileAccess {
	if s.lookup != nil {
		if sessionID := SessionIDFromContext(ctx); sessionID != "" {
			if svfs := s.lookup(sessionID); svfs != nil {
				return svfs.NewGlobalFileAccess(s.readOnly)
			}
		}
	}
	if s.fallback != nil {
		return s.fallback
	}
	return NewDiskFileAccess(".", s.readOnly)
}

var _ FileAccess = (*SessionRoutingFileAccess)(nil)
