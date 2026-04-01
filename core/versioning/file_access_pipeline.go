package versioning

import (
	"context"
	"io/fs"
)

// PipelineRoutingFileAccess resolves each operation against the current
// session-scoped pipeline draft for a fixed pipeline/task ID.
type PipelineRoutingFileAccess struct {
	readOnly   bool
	lookup     func() *SessionVFS
	pipelineID string
	workingDir string
}

// NewPipelineRoutingFileAccess creates a file-access router that resolves
// each operation against the current tracked pipeline draft.
func NewPipelineRoutingFileAccess(
	readOnly bool,
	lookup func() *SessionVFS,
	pipelineID string,
	workingDir string,
) *PipelineRoutingFileAccess {
	return &PipelineRoutingFileAccess{
		readOnly:   readOnly,
		lookup:     lookup,
		pipelineID: pipelineID,
		workingDir: workingDir,
	}
}

func (p *PipelineRoutingFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	fa, err := p.resolve()
	if err != nil {
		return nil, err
	}
	return fa.ReadFile(ctx, path)
}

func (p *PipelineRoutingFileAccess) MkdirAll(ctx context.Context, path string) error {
	fa, err := p.resolve()
	if err != nil {
		return err
	}
	return fa.MkdirAll(ctx, path)
}

func (p *PipelineRoutingFileAccess) WriteFile(ctx context.Context, path string, content []byte) error {
	fa, err := p.resolve()
	if err != nil {
		return err
	}
	return fa.WriteFile(ctx, path, content)
}

func (p *PipelineRoutingFileAccess) EditFile(ctx context.Context, path string, edits []FileEdit) error {
	fa, err := p.resolve()
	if err != nil {
		return err
	}
	return fa.EditFile(ctx, path, edits)
}

func (p *PipelineRoutingFileAccess) DeleteFile(ctx context.Context, path string) error {
	fa, err := p.resolve()
	if err != nil {
		return err
	}
	return fa.DeleteFile(ctx, path)
}

func (p *PipelineRoutingFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	fa, err := p.resolve()
	if err != nil {
		return false, err
	}
	return fa.Exists(ctx, path)
}

func (p *PipelineRoutingFileAccess) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	fa, err := p.resolve()
	if err != nil {
		return nil, err
	}
	return fa.ListDir(ctx, dir)
}

func (p *PipelineRoutingFileAccess) Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	fa, err := p.resolve()
	if err != nil {
		return nil, err
	}
	return fa.Glob(ctx, root, pattern, exclude)
}

func (p *PipelineRoutingFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]GrepMatch, error) {
	fa, err := p.resolve()
	if err != nil {
		return nil, err
	}
	return fa.Grep(ctx, root, pattern, include, contextLines, maxMatches)
}

func (p *PipelineRoutingFileAccess) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	fa, err := p.resolve()
	if err != nil {
		return nil, err
	}
	return fa.Stat(ctx, path)
}

func (p *PipelineRoutingFileAccess) WorkingDir() string {
	if p == nil {
		return ""
	}
	if p.workingDir != "" {
		return p.workingDir
	}
	if svfs := p.resolveSession(); svfs != nil {
		return svfs.WorkingDir()
	}
	return ""
}

func (p *PipelineRoutingFileAccess) IsReadOnly() bool {
	return p != nil && p.readOnly
}

func (p *PipelineRoutingFileAccess) resolve() (FileAccess, error) {
	svfs := p.resolveSession()
	if svfs == nil {
		return nil, ErrNoActiveSessionVFS
	}
	if p.readOnly {
		return svfs.ReadOnlyPipelineFileAccess(p.pipelineID)
	}
	return svfs.PipelineFileAccess(p.pipelineID)
}

func (p *PipelineRoutingFileAccess) resolveSession() *SessionVFS {
	if p == nil || p.lookup == nil {
		return nil
	}
	return p.lookup()
}

var _ FileAccess = (*PipelineRoutingFileAccess)(nil)
