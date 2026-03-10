package versioning

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
)

// GlobalDraftFileAccess exposes the session-global draft workspace for
// writable global agents. Reads come from the merged draft VFS, while writes
// are funneled back through SessionVFS so every mutation is serialized through
// the same merge/WAL path as pipeline commits.
type GlobalDraftFileAccess struct {
	session  *SessionVFS
	delegate *VFSFileAccess
}

func NewGlobalDraftFileAccess(session *SessionVFS) *GlobalDraftFileAccess {
	return &GlobalDraftFileAccess{
		session:  session,
		delegate: NewVFSFileAccess(session.globalVFS, session.workingDir),
	}
}

func (g *GlobalDraftFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return g.delegate.ReadFile(ctx, path)
}

func (g *GlobalDraftFileAccess) WriteFile(ctx context.Context, path string, content []byte) error {
	return g.session.ApplyGlobalWrite(ctx, path, content)
}

func (g *GlobalDraftFileAccess) EditFile(ctx context.Context, path string, edits []FileEdit) error {
	content, err := g.ReadFile(ctx, path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	modified := string(content)
	for i, edit := range edits {
		if !bytes.Contains([]byte(modified), []byte(edit.OldText)) {
			return fmt.Errorf("edit %d: old_text not found in file", i)
		}
		modified = string(bytes.Replace([]byte(modified), []byte(edit.OldText), []byte(edit.NewText), 1))
	}
	return g.session.ApplyGlobalWrite(ctx, path, []byte(modified))
}

func (g *GlobalDraftFileAccess) DeleteFile(ctx context.Context, path string) error {
	return g.session.ApplyGlobalDelete(ctx, path)
}

func (g *GlobalDraftFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	return g.delegate.Exists(ctx, path)
}

func (g *GlobalDraftFileAccess) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	return g.delegate.ListDir(ctx, dir)
}

func (g *GlobalDraftFileAccess) Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	return g.delegate.Glob(ctx, root, pattern, exclude)
}

func (g *GlobalDraftFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	return g.delegate.Grep(ctx, root, pattern, include, contextLines, maxMatches)
}

func (g *GlobalDraftFileAccess) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	return g.delegate.Stat(ctx, path)
}

func (g *GlobalDraftFileAccess) WorkingDir() string { return g.delegate.WorkingDir() }
func (g *GlobalDraftFileAccess) IsReadOnly() bool   { return false }

var _ FileAccess = (*GlobalDraftFileAccess)(nil)
