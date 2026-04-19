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
	// rebindCfg is the BeginPipeline config used when resolve() finds the
	// session has no entry for pipelineID. This is defense-in-depth: under
	// the new authority discipline (inspector-owned commit/rollback), the
	// pipeline VFS should never disappear during legitimate work, but if a
	// race or a stray cleanup path destroys it, this lets the next write
	// silently rebind rather than failing with ErrVFSNotFound. Set via
	// WithRebindConfig.
	rebindCfg *BeginPipelineConfig
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

// WithRebindConfig enables auto-rebind on miss. If the resolve() lookup
// finds no pipeline draft for this access's pipelineID, it calls
// BeginPipeline with the supplied config to recreate one. The config is
// copied so the caller can mutate the original. Returns the same access
// pointer for fluent chaining.
func (p *PipelineRoutingFileAccess) WithRebindConfig(cfg BeginPipelineConfig) *PipelineRoutingFileAccess {
	if p == nil {
		return nil
	}
	cfgCopy := cfg
	cfgCopy.PipelineID = p.pipelineID
	if cfgCopy.WorkingDir == "" {
		cfgCopy.WorkingDir = p.workingDir
	}
	p.rebindCfg = &cfgCopy
	return p
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
	resolveOnce := func() (FileAccess, error) {
		if p.readOnly {
			return svfs.ReadOnlyPipelineFileAccess(p.pipelineID)
		}
		return svfs.PipelineFileAccess(p.pipelineID)
	}
	fa, err := resolveOnce()
	if err == nil {
		return fa, nil
	}
	// Defense-in-depth: under the new inspector-owned-commit discipline the
	// pipeline VFS should not disappear mid-work, but if a race or stray
	// cleanup destroyed it AND the caller pre-configured a rebind, recover
	// silently rather than blocking writes. Without this, the historical
	// "edit_pipeline_file failed: VFS not found" path would re-emerge any
	// time the orchestrator's old commit-on-broadcast behavior is briefly
	// re-introduced or a custom pipeline lifecycle differs.
	if err == ErrVFSNotFound && p.rebindCfg != nil {
		if _, beginErr := svfs.BeginPipeline(*p.rebindCfg); beginErr == nil {
			return resolveOnce()
		}
	}
	return nil, err
}

func (p *PipelineRoutingFileAccess) resolveSession() *SessionVFS {
	if p == nil || p.lookup == nil {
		return nil
	}
	return p.lookup()
}

var _ FileAccess = (*PipelineRoutingFileAccess)(nil)
