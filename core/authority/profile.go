package authority

import (
	"context"
	"io/fs"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

type FileScope string

const (
	FileScopeNone              FileScope = "none"
	FileScopeDiskRead          FileScope = "disk-read"
	FileScopeGlobalRead        FileScope = "global-read"
	FileScopeGlobalReadWrite   FileScope = "global-read-write"
	FileScopePipelineRead      FileScope = "pipeline-read"
	FileScopePipelineReadWrite FileScope = "pipeline-read-write"
)

type ExecScope string

const (
	ExecScopeNone        ExecScope = "none"
	ExecScopeDisk        ExecScope = "disk"
	ExecScopeGlobalVFS   ExecScope = "global-vfs"
	ExecScopePipelineVFS ExecScope = "pipeline-vfs"
)

type Profile struct {
	AgentType      string
	FileScope      FileScope
	WorkspaceViews []versioning.WorkspaceView
	ExecScope      ExecScope
}

var profiles = map[string]Profile{
	"academic": {
		AgentType: "academic",
		FileScope: FileScopeNone,
		ExecScope: ExecScopeNone,
	},
	"architect": {
		AgentType: "architect",
		FileScope: FileScopeNone,
		ExecScope: ExecScopeNone,
	},
	"archivalist": {
		AgentType: "archivalist",
		FileScope: FileScopeNone,
		ExecScope: ExecScopeNone,
	},
	"designer": {
		AgentType:      "designer",
		FileScope:      FileScopePipelineReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopePipelineVFS,
	},
	"engineer": {
		AgentType:      "engineer",
		FileScope:      FileScopePipelineReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopePipelineVFS,
	},
	"global-editor": {
		AgentType:      "global-editor",
		FileScope:      FileScopeGlobalReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal},
		ExecScope:      ExecScopeGlobalVFS,
	},
	"guardian": {
		AgentType:      "guardian",
		FileScope:      FileScopeDiskRead,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopeNone,
	},
	"guide": {
		AgentType:      "guide",
		FileScope:      FileScopeDiskRead,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopeNone,
	},
	"inspector": {
		AgentType:      "inspector",
		FileScope:      FileScopeGlobalReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal},
		ExecScope:      ExecScopeGlobalVFS,
	},
	"inspector-pipeline": {
		AgentType:      "inspector-pipeline",
		FileScope:      FileScopePipelineReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopePipelineVFS,
	},
	"librarian": {
		AgentType: "librarian",
		FileScope: FileScopeDiskRead,
		ExecScope: ExecScopeDisk,
	},
	"orchestrator": {
		AgentType:      "orchestrator",
		FileScope:      FileScopeDiskRead,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopeNone,
	},
	"tester": {
		AgentType:      "tester",
		FileScope:      FileScopeGlobalReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal},
		ExecScope:      ExecScopeGlobalVFS,
	},
	"tester-pipeline": {
		AgentType:      "tester-pipeline",
		FileScope:      FileScopePipelineReadWrite,
		WorkspaceViews: []versioning.WorkspaceView{versioning.WorkspaceViewDisk, versioning.WorkspaceViewGlobal, versioning.WorkspaceViewPipeline},
		ExecScope:      ExecScopePipelineVFS,
	},
}

func ProfileFor(agentType string) Profile {
	trimmed := strings.TrimSpace(agentType)
	if profile, ok := profiles[trimmed]; ok {
		return cloneProfile(profile)
	}
	return Profile{
		AgentType: trimmed,
		FileScope: FileScopeNone,
		ExecScope: ExecScopeNone,
	}
}

func cloneProfile(profile Profile) Profile {
	cloned := profile
	cloned.WorkspaceViews = append([]versioning.WorkspaceView(nil), profile.WorkspaceViews...)
	return cloned
}

func (p Profile) AllowsFileReads() bool {
	return p.FileScope != FileScopeNone
}

func (p Profile) AllowsFileWrites() bool {
	switch p.FileScope {
	case FileScopeGlobalReadWrite, FileScopePipelineReadWrite:
		return true
	default:
		return false
	}
}

func (p Profile) AllowsWorkspaceTools() bool {
	return len(p.WorkspaceViews) > 0
}

func (p Profile) AllowsWorkspaceView(view versioning.WorkspaceView) bool {
	for _, allowed := range p.WorkspaceViews {
		if allowed == view {
			return true
		}
	}
	return false
}

func RestrictFileAccess(agentType string, delegate versioning.FileAccess) versioning.FileAccess {
	if delegate == nil {
		return nil
	}
	return restrictedFileAccess{
		profile:  ProfileFor(agentType),
		delegate: delegate,
	}
}

func RestrictWorkspaceViews(agentType string, delegate versioning.WorkspaceViewAccess) versioning.WorkspaceViewAccess {
	if delegate == nil {
		return nil
	}
	return restrictedWorkspaceViews{
		profile:  ProfileFor(agentType),
		delegate: delegate,
	}
}

type restrictedFileAccess struct {
	profile  Profile
	delegate versioning.FileAccess
}

func (r restrictedFileAccess) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.ReadFile(ctx, path)
}

func (r restrictedFileAccess) MkdirAll(ctx context.Context, path string) error {
	if !r.allowsWrites() {
		return versioning.ErrPermissionDenied
	}
	return r.delegate.MkdirAll(ctx, path)
}

func (r restrictedFileAccess) WriteFile(ctx context.Context, path string, content []byte) error {
	if !r.allowsWrites() {
		return versioning.ErrPermissionDenied
	}
	return r.delegate.WriteFile(ctx, path, content)
}

func (r restrictedFileAccess) EditFile(ctx context.Context, path string, edits []versioning.FileEdit) error {
	if !r.allowsWrites() {
		return versioning.ErrPermissionDenied
	}
	return r.delegate.EditFile(ctx, path, edits)
}

func (r restrictedFileAccess) DeleteFile(ctx context.Context, path string) error {
	if !r.allowsWrites() {
		return versioning.ErrPermissionDenied
	}
	return r.delegate.DeleteFile(ctx, path)
}

func (r restrictedFileAccess) Exists(ctx context.Context, path string) (bool, error) {
	if !r.profile.AllowsFileReads() {
		return false, versioning.ErrPermissionDenied
	}
	return r.delegate.Exists(ctx, path)
}

func (r restrictedFileAccess) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.ListDir(ctx, dir)
}

func (r restrictedFileAccess) Glob(ctx context.Context, root, pattern string, exclude []string) ([]string, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Glob(ctx, root, pattern, exclude)
}

func (r restrictedFileAccess) Grep(ctx context.Context, root, pattern, include string, contextLines, maxMatches int) ([]versioning.GrepMatch, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Grep(ctx, root, pattern, include, contextLines, maxMatches)
}

func (r restrictedFileAccess) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	if !r.profile.AllowsFileReads() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Stat(ctx, path)
}

func (r restrictedFileAccess) WorkingDir() string {
	return r.delegate.WorkingDir()
}

func (r restrictedFileAccess) IsReadOnly() bool {
	return !r.allowsWrites() || r.delegate.IsReadOnly()
}

func (r restrictedFileAccess) allowsWrites() bool {
	if !r.profile.AllowsFileWrites() {
		return false
	}
	if _, ok := r.delegate.(*versioning.DiskFileAccess); ok {
		switch r.profile.FileScope {
		case FileScopeGlobalReadWrite, FileScopePipelineReadWrite:
			return false
		}
	}
	return true
}

type restrictedWorkspaceViews struct {
	profile  Profile
	delegate versioning.WorkspaceViewAccess
}

func (r restrictedWorkspaceViews) ReadFile(ctx context.Context, view versioning.WorkspaceView, path string, pipelineID string) ([]byte, error) {
	if !r.profile.AllowsWorkspaceView(view) {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.ReadFile(ctx, view, path, pipelineID)
}

func (r restrictedWorkspaceViews) Glob(ctx context.Context, view versioning.WorkspaceView, root, pattern string, exclude []string, pipelineID string) ([]string, error) {
	if !r.profile.AllowsWorkspaceView(view) {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Glob(ctx, view, root, pattern, exclude, pipelineID)
}

func (r restrictedWorkspaceViews) Grep(ctx context.Context, view versioning.WorkspaceView, root, pattern, include string, contextLines, maxMatches int, pipelineID string) ([]versioning.GrepMatch, error) {
	if !r.profile.AllowsWorkspaceView(view) {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.Grep(ctx, view, root, pattern, include, contextLines, maxMatches, pipelineID)
}

func (r restrictedWorkspaceViews) InspectPath(ctx context.Context, path string, pipelineID string) (*versioning.WorkspacePathState, error) {
	if !r.profile.AllowsWorkspaceTools() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.InspectPath(ctx, path, pipelineID)
}

func (r restrictedWorkspaceViews) SummarizePaths(ctx context.Context, paths []string, pipelineID string) (*versioning.WorkspaceSummary, error) {
	if !r.profile.AllowsWorkspaceTools() {
		return nil, versioning.ErrPermissionDenied
	}
	return r.delegate.SummarizePaths(ctx, paths, pipelineID)
}

func (r restrictedWorkspaceViews) DefaultView() versioning.WorkspaceView {
	defaultView := r.delegate.DefaultView()
	if r.profile.AllowsWorkspaceView(defaultView) {
		return defaultView
	}
	if len(r.profile.WorkspaceViews) == 0 {
		return ""
	}
	return r.profile.WorkspaceViews[0]
}
