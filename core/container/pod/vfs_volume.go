package pod

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/versioning"
)

// VFSVolumeConfig configures a VFS-backed volume for pipeline pods.
type VFSVolumeConfig struct {
	Name        string
	PipelineID  string
	SessionID   versioning.SessionID
	WorkingDir  string
	SessionVFS  *versioning.SessionVFS
	Files       []string
	EventLogger *agentlog.SessionEventLogger
	AgentID     string
}

// VFSVolume wraps a PipelineVFS with pod-lifecycle-aware mount/unmount.
// Mount creates a per-pipeline VFS via the CVS; Unmount closes it via
// the VFSManager.
type VFSVolume struct {
	name        string
	pipelineID  string
	sessionID   versioning.SessionID
	workingDir  string
	sessionVFS  *versioning.SessionVFS
	files       []string
	eventLogger *agentlog.SessionEventLogger
	agentID     string

	mu          sync.Mutex
	pipelineFA  versioning.FileAccess
	workspace   versioning.WorkspaceViewAccess
	pipelineVFS *versioning.PipelineVFS
	generation  uint64
	mounted     bool
}

// NewVFSVolume creates a VFS-backed volume for a pipeline pod.
func NewVFSVolume(cfg VFSVolumeConfig) *VFSVolume {
	return &VFSVolume{
		name:        cfg.Name,
		pipelineID:  cfg.PipelineID,
		sessionID:   cfg.SessionID,
		workingDir:  cfg.WorkingDir,
		sessionVFS:  cfg.SessionVFS,
		files:       append([]string(nil), cfg.Files...),
		eventLogger: cfg.EventLogger,
		agentID:     cfg.AgentID,
	}
}

func (v *VFSVolume) VolumeName() string { return v.name }

func (v *VFSVolume) Mount(_ context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.mounted {
		if err := v.ensureBindingLocked("mount_hot_reuse"); err != nil {
			return err
		}
		v.logTrace("vfs_volume_mount_skipped", "debug", agentlog.EventRegistryEvent, map[string]any{
			"pipeline_id": v.pipelineID,
			"reason":      "already_mounted",
			"generation":  v.generation,
		})
		return nil
	}

	v.logTrace("vfs_volume_mount_begin", "debug", agentlog.EventRegistryEvent, map[string]any{
		"pipeline_id":   v.pipelineID,
		"working_dir":   v.workingDir,
		"files_count":   len(v.files),
		"files_preview": previewStrings(v.files, 8),
		"session_id":    string(v.sessionID),
		"volume_name":   v.name,
	})
	if err := v.rebindLocked("mount"); err != nil {
		v.logTrace("vfs_volume_mount_failed", "error", agentlog.EventError, map[string]any{
			"pipeline_id": v.pipelineID,
			"working_dir": v.workingDir,
			"files_count": len(v.files),
			"error":       err.Error(),
		})
		return err
	}
	v.mounted = true
	v.logTrace("vfs_volume_mount_done", "debug", agentlog.EventRegistryEvent, map[string]any{
		"pipeline_id":   v.pipelineID,
		"working_dir":   v.workingDir,
		"files_count":   len(v.files),
		"files_preview": previewStrings(v.files, 8),
		"session_id":    string(v.sessionID),
		"volume_name":   v.name,
		"generation":    v.generation,
	})
	return nil
}

func (v *VFSVolume) EnsureReady(_ context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.mounted {
		return nil
	}
	return v.ensureBindingLocked("ensure_ready")
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
	v.generation = 0
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
	ver, err := v.sessionVFS.CommitPipeline(ctx, v.pipelineID)
	if err == nil {
		v.pipelineVFS = nil
	}
	return ver, err
}

func (v *VFSVolume) Rollback() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	err := v.sessionVFS.RollbackPipeline(v.pipelineID)
	if err == nil {
		v.pipelineVFS = nil
	}
	return err
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

func (v *DiskVolume) VolumeName() string            { return v.name }
func (v *DiskVolume) Mount(_ context.Context) error { return nil }
func (v *DiskVolume) EnsureReady(_ context.Context) error {
	return nil
}
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

func (v *GlobalVFSVolume) VolumeName() string            { return v.name }
func (v *GlobalVFSVolume) Mount(_ context.Context) error { return nil }
func (v *GlobalVFSVolume) EnsureReady(_ context.Context) error {
	return nil
}
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

func (v *VFSVolume) ensureBindingLocked(reason string) error {
	if v.sessionVFS == nil {
		return versioning.ErrNoActiveSessionVFS
	}
	if v.sessionVFS.HasPipeline(v.pipelineID) {
		return nil
	}
	return v.rebindLocked(reason)
}

func (v *VFSVolume) rebindLocked(reason string) error {
	pipelineVFS, err := v.sessionVFS.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: v.pipelineID,
		SessionID:  v.sessionID,
		WorkingDir: v.workingDir,
		Files:      append([]string(nil), v.files...),
	})
	if err != nil {
		v.logTrace("vfs_volume_rebind_failed", "error", agentlog.EventError, map[string]any{
			"pipeline_id": v.pipelineID,
			"reason":      reason,
			"error":       err.Error(),
		})
		return err
	}
	v.pipelineVFS = pipelineVFS
	v.generation++
	if v.pipelineFA == nil {
		v.pipelineFA = versioning.NewPipelineRoutingFileAccess(false, func() *versioning.SessionVFS {
			return v.sessionVFS
		}, v.pipelineID, v.workingDir)
	}
	if v.workspace == nil {
		v.workspace = versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:       versioning.WorkspaceViewPipeline,
			DefaultPipelineID: v.pipelineID,
			DefaultSessionID:  string(v.sessionID),
			WorkingDir:        v.workingDir,
			Session:           v.sessionVFS,
			DiskFallback:      versioning.NewDiskFileAccess(v.workingDir, true),
		})
	}
	v.logTrace("vfs_volume_rebind_done", "debug", agentlog.EventRegistryEvent, map[string]any{
		"pipeline_id": v.pipelineID,
		"reason":      reason,
		"generation":  v.generation,
	})
	return nil
}

func (v *VFSVolume) logTrace(event, level string, eventCode agentlog.EventType, data map[string]any) {
	if v == nil || v.eventLogger == nil {
		return
	}
	agentID := v.agentID
	if agentID == "" {
		agentID = v.pipelineID
	}
	v.eventLogger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     agentID,
		SessionID: string(v.sessionID),
		Event:     event,
		EventCode: eventCode,
		Data:      data,
	})
}

func previewStrings(values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	if limit <= 0 || len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}
