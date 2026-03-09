package pod

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/versioning"
)

// VFSVolumeConfig configures a VFS-backed volume for pipeline pods.
type VFSVolumeConfig struct {
	Name       string
	PipelineID string
	SessionID  versioning.SessionID
	WorkingDir string
	SessionVFS *versioning.SessionVFS
	Files      []string
}

// VFSVolume wraps a PipelineVFS with pod-lifecycle-aware mount/unmount.
// Mount creates a per-pipeline VFS via the CVS; Unmount closes it via
// the VFSManager.
type VFSVolume struct {
	name       string
	pipelineID string
	sessionID  versioning.SessionID
	workingDir string
	sessionVFS *versioning.SessionVFS
	files      []string

	mu          sync.Mutex
	pipelineFA  versioning.FileAccess
	workspace   versioning.WorkspaceViewAccess
	pipelineVFS *versioning.PipelineVFS
	mounted     bool
}

// NewVFSVolume creates a VFS-backed volume for a pipeline pod.
func NewVFSVolume(cfg VFSVolumeConfig) *VFSVolume {
	return &VFSVolume{
		name:       cfg.Name,
		pipelineID: cfg.PipelineID,
		sessionID:  cfg.SessionID,
		workingDir: cfg.WorkingDir,
		sessionVFS: cfg.SessionVFS,
		files:      append([]string(nil), cfg.Files...),
	}
}

func (v *VFSVolume) VolumeName() string { return v.name }

func (v *VFSVolume) Mount(_ context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.mounted {
		return nil
	}

	pipelineVFS, err := v.sessionVFS.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: v.pipelineID,
		SessionID:  v.sessionID,
		WorkingDir: v.workingDir,
		Files:      append([]string(nil), v.files...),
	})
	if err != nil {
		return err
	}
	v.pipelineVFS = pipelineVFS
	v.pipelineFA = v.sessionVFS.NewPipelineFileAccess(pipelineVFS)
	v.workspace = versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:       versioning.WorkspaceViewPipeline,
		DefaultPipelineID: v.pipelineID,
		DefaultSessionID:  string(v.sessionID),
		WorkingDir:        v.workingDir,
		Session:           v.sessionVFS,
		DiskFallback:      versioning.NewDiskFileAccess(v.workingDir, true),
	})
	v.mounted = true
	return nil
}

func (v *VFSVolume) Unmount(_ context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.mounted {
		return nil
	}

	v.pipelineFA = nil
	v.workspace = nil
	v.pipelineVFS = nil
	v.mounted = false
	return nil
}

func (v *VFSVolume) FileAccess() versioning.FileAccess {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.pipelineFA
}

func (v *VFSVolume) WorkspaceViews() versioning.WorkspaceViewAccess {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.workspace
}

func (v *VFSVolume) Commit(ctx context.Context) (versioning.SemanticVersion, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sessionVFS.CommitPipeline(ctx, v.pipelineID)
}

func (v *VFSVolume) Rollback() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sessionVFS.RollbackPipeline(v.pipelineID)
}

// ---------- DiskVolume ----------

// DiskVolume provides direct disk access as a ManagedVolume. Used by
// singleton pods (architect, guide) that operate on the working directory.
type DiskVolume struct {
	name       string
	workingDir string
	fa         versioning.FileAccess
	workspace  versioning.WorkspaceViewAccess
}

// NewDiskVolume creates a disk-passthrough volume.
func NewDiskVolume(name, workingDir string) *DiskVolume {
	return &DiskVolume{
		name:       name,
		workingDir: workingDir,
		fa:         versioning.NewDiskFileAccess(workingDir, false),
		workspace: versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:  versioning.WorkspaceViewDisk,
			WorkingDir:   workingDir,
			DiskFallback: versioning.NewDiskFileAccess(workingDir, true),
		}),
	}
}

func (v *DiskVolume) VolumeName() string                { return v.name }
func (v *DiskVolume) Mount(_ context.Context) error     { return nil }
func (v *DiskVolume) Unmount(_ context.Context) error   { return nil }
func (v *DiskVolume) FileAccess() versioning.FileAccess { return v.fa }
func (v *DiskVolume) WorkspaceViews() versioning.WorkspaceViewAccess {
	return v.workspace
}

// ---------- GlobalVFSVolume ----------

// GlobalVFSVolume wraps a per-session in-memory global-overlay FileAccess for
// global agents. The FileAccess is set externally and does not change with
// mount/unmount.
type GlobalVFSVolume struct {
	name  string
	mu    sync.Mutex
	fa    versioning.FileAccess
	views versioning.WorkspaceViewAccess
}

// NewGlobalVFSVolume creates a global VFS volume with the given FileAccess.
func NewGlobalVFSVolume(name string, fa versioning.FileAccess, views versioning.WorkspaceViewAccess) *GlobalVFSVolume {
	return &GlobalVFSVolume{name: name, fa: fa, views: views}
}

func (v *GlobalVFSVolume) VolumeName() string              { return v.name }
func (v *GlobalVFSVolume) Mount(_ context.Context) error   { return nil }
func (v *GlobalVFSVolume) Unmount(_ context.Context) error { return nil }

func (v *GlobalVFSVolume) FileAccess() versioning.FileAccess {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fa
}

func (v *GlobalVFSVolume) WorkspaceViews() versioning.WorkspaceViewAccess {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.views
}

// SetFileAccess updates the underlying FileAccess (e.g., when session changes).
func (v *GlobalVFSVolume) SetFileAccess(fa versioning.FileAccess) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.fa = fa
}

// SetWorkspaceViews updates the explicit workspace-view accessor for this volume.
func (v *GlobalVFSVolume) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.views = views
}
