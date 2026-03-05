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

	mu         sync.Mutex
	pipelineFA versioning.FileAccess
	mounted    bool
}

// NewVFSVolume creates a VFS-backed volume for a pipeline pod.
func NewVFSVolume(cfg VFSVolumeConfig) *VFSVolume {
	return &VFSVolume{
		name:       cfg.Name,
		pipelineID: cfg.PipelineID,
		sessionID:  cfg.SessionID,
		workingDir: cfg.WorkingDir,
		sessionVFS: cfg.SessionVFS,
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
	})
	if err != nil {
		return err
	}
	v.pipelineFA = v.sessionVFS.NewPipelineFileAccess(pipelineVFS)
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
	v.mounted = false

	// Commit pipeline changes to the global VFS via MergePipe.
	// Previously this only closed the VFS (changes were lost).
	_, err := v.sessionVFS.CommitPipeline(v.pipelineID)
	return err
}

func (v *VFSVolume) FileAccess() versioning.FileAccess {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.pipelineFA
}

// ---------- DiskVolume ----------

// DiskVolume provides direct disk access as a ManagedVolume. Used by
// singleton pods (architect, guide) that operate on the working directory.
type DiskVolume struct {
	name       string
	workingDir string
	fa         versioning.FileAccess
}

// NewDiskVolume creates a disk-passthrough volume.
func NewDiskVolume(name, workingDir string) *DiskVolume {
	return &DiskVolume{
		name:       name,
		workingDir: workingDir,
		fa:         versioning.NewDiskFileAccess(workingDir, false),
	}
}

func (v *DiskVolume) VolumeName() string                { return v.name }
func (v *DiskVolume) Mount(_ context.Context) error     { return nil }
func (v *DiskVolume) Unmount(_ context.Context) error   { return nil }
func (v *DiskVolume) FileAccess() versioning.FileAccess { return v.fa }

// ---------- GlobalVFSVolume ----------

// GlobalVFSVolume wraps a per-session CVS-backed FileAccess for global agents.
// The FileAccess is set externally and doesn't change with mount/unmount.
type GlobalVFSVolume struct {
	name string
	mu   sync.Mutex
	fa   versioning.FileAccess
}

// NewGlobalVFSVolume creates a global VFS volume with the given FileAccess.
func NewGlobalVFSVolume(name string, fa versioning.FileAccess) *GlobalVFSVolume {
	return &GlobalVFSVolume{name: name, fa: fa}
}

func (v *GlobalVFSVolume) VolumeName() string                { return v.name }
func (v *GlobalVFSVolume) Mount(_ context.Context) error     { return nil }
func (v *GlobalVFSVolume) Unmount(_ context.Context) error   { return nil }

func (v *GlobalVFSVolume) FileAccess() versioning.FileAccess {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fa
}

// SetFileAccess updates the underlying FileAccess (e.g., when session changes).
func (v *GlobalVFSVolume) SetFileAccess(fa versioning.FileAccess) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.fa = fa
}
