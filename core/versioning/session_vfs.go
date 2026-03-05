package versioning

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SessionVFS owns the per-session versioning infrastructure:
// a global VFS overlay, MergePipe (OT-based pipeline→global merges),
// VersionedWAL (disk-backed WAL with semantic versioning), DiskFlusher
// (global→disk with checkpoint), and VFSManager (pipeline VFS lifecycle).
type SessionVFS struct {
	mu sync.Mutex

	sessionID   SessionID
	workingDir  string
	globalVFS   *PipelineVFS
	mergePipe   *MergePipe
	wal         *VersionedWAL
	diskFlusher *DiskFlusher
	vfsManager  VFSManager
	otEngine    OTEngine

	// Legacy CVS shim for backward compatibility during transition.
	cvsShim *CVSShim

	// Legacy stores kept temporarily for CVS shim.
	cvs       *DefaultCVS
	blobStore BlobStore
	dagStore  DAGStore
	opLog     OperationLog
	oldWAL    WriteAheadLog

	closed bool
}

// SessionVFSConfig configures the per-session VFS infrastructure.
type SessionVFSConfig struct {
	SessionID  SessionID
	WorkingDir string
}

// NewSessionVFS creates an isolated set of versioning subsystems for a session.
func NewSessionVFS(cfg SessionVFSConfig) *SessionVFS {
	walDir := filepath.Join(os.TempDir(), "sylk", "sessions", string(cfg.SessionID), "wal")
	stagingDir := filepath.Join(os.TempDir(), "sylk", "sessions", string(cfg.SessionID))

	otEngine := NewOTEngine()

	// Open disk-backed WAL.
	vwal, err := OpenVersionedWAL(VersionedWALConfig{Dir: walDir})
	if err != nil {
		// Fallback: use temp dir if configured dir fails.
		tmpDir, _ := os.MkdirTemp("", "sylk-wal-*")
		vwal, _ = OpenVersionedWAL(VersionedWALConfig{Dir: tmpDir})
	}

	// Create global VFS overlay (no version/blob store needed).
	globalVFS := NewGlobalVFS(VFSConfig{
		PipelineID: "global",
		SessionID:  cfg.SessionID,
		WorkingDir: cfg.WorkingDir,
	})

	// Create MergePipe wired to global VFS + WAL + OT.
	mp := NewMergePipe(MergePipeConfig{
		GlobalVFS: globalVFS,
		WAL:       vwal,
		OTEngine:  otEngine,
	})

	// Create DiskFlusher wired to global VFS + WAL.
	df := NewDiskFlusher(DiskFlusherConfig{
		GlobalVFS:  globalVFS,
		WAL:        vwal,
		WorkingDir: cfg.WorkingDir,
	})

	// Create simplified VFSManager (no VersionStore/BlobStore needed).
	vfsMgr := NewMemoryVFSManager(VFSManagerConfig{
		BaseDir: stagingDir,
	})

	// Start merge goroutine.
	mp.Start()

	// Legacy stores for CVS shim backward compatibility.
	blobStore := NewMemoryBlobStore()
	dagStore := NewMemoryDAGStore()
	opLog := NewMemoryOperationLog()
	oldWAL := NewMemoryWAL()

	cvs := NewCVS(CVSConfig{
		VFSManager: vfsMgr,
		BlobStore:  blobStore,
		OpLog:      opLog,
		DAGStore:   dagStore,
		WAL:        oldWAL,
		OTEngine:   otEngine,
	})

	s := &SessionVFS{
		sessionID:   cfg.SessionID,
		workingDir:  cfg.WorkingDir,
		globalVFS:   globalVFS,
		mergePipe:   mp,
		wal:         vwal,
		diskFlusher: df,
		vfsManager:  vfsMgr,
		otEngine:    otEngine,
		cvs:         cvs,
		blobStore:   blobStore,
		dagStore:    dagStore,
		opLog:       opLog,
		oldWAL:      oldWAL,
	}

	s.cvsShim = NewCVSShim(s)
	return s
}

// BeginPipeline creates a new pipeline VFS and registers it with the MergePipe.
func (s *SessionVFS) BeginPipeline(cfg BeginPipelineConfig) (*PipelineVFS, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrVFSClosed
	}

	vfsCfg := VFSConfig{
		PipelineID: cfg.PipelineID,
		SessionID:  cfg.SessionID,
		WorkingDir: cfg.WorkingDir,
		AgentID:    cfg.AgentID,
		AgentRole:  cfg.AgentRole,
	}

	pipelineVFS, err := s.vfsManager.CreatePipelineVFS(vfsCfg)
	if err != nil {
		return nil, fmt.Errorf("session vfs: begin pipeline: %w", err)
	}

	if err := s.mergePipe.RegisterPipeline(cfg.PipelineID); err != nil {
		s.vfsManager.ClosePipelineVFS(cfg.PipelineID)
		return nil, fmt.Errorf("session vfs: register pipeline: %w", err)
	}

	return pipelineVFS, nil
}

// CommitPipeline extracts modifications from the pipeline VFS, merges them
// into the global VFS via MergePipe (OT transform), and closes the pipeline.
func (s *SessionVFS) CommitPipeline(pipelineID string) (SemanticVersion, error) {
	mods, err := s.extractPipelineMods(pipelineID)
	if err != nil {
		return SemanticVersion{}, err
	}

	// Merge outside of s.mu — the merge goroutine is independent.
	ver, mergeErr := s.mergePipe.Merge(
		contextWithoutCancel(),
		pipelineID,
		mods,
	)

	// Close the pipeline VFS regardless of merge outcome.
	s.mu.Lock()
	s.vfsManager.ClosePipelineVFS(pipelineID)
	s.mu.Unlock()

	if mergeErr != nil {
		return SemanticVersion{}, fmt.Errorf("session vfs: merge pipeline: %w", mergeErr)
	}
	return ver, nil
}

func (s *SessionVFS) extractPipelineMods(pipelineID string) ([]FileModification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrVFSClosed
	}

	pipelineVFS, err := s.vfsManager.GetPipelineVFS(pipelineID)
	if err != nil {
		return nil, fmt.Errorf("session vfs: get pipeline: %w", err)
	}
	return pipelineVFS.GetModifications(), nil
}

// RollbackPipeline unregisters a pipeline from MergePipe (no merge) and closes it.
func (s *SessionVFS) RollbackPipeline(pipelineID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrVFSClosed
	}

	s.mergePipe.UnregisterPipeline(pipelineID)
	return s.vfsManager.ClosePipelineVFS(pipelineID)
}

// GlobalVFS returns the session-scoped global VFS overlay.
func (s *SessionVFS) GlobalVFS() *PipelineVFS { return s.globalVFS }

// MergePipe returns the session's merge pipe.
func (s *SessionVFS) MergePipe() *MergePipe { return s.mergePipe }

// DiskFlusher returns the session's disk flusher.
func (s *SessionVFS) DiskFlusher() *DiskFlusher { return s.diskFlusher }

// WAL returns the session's versioned WAL.
func (s *SessionVFS) WAL() *VersionedWAL { return s.wal }

// CurrentVersion returns the current semantic version from the WAL.
func (s *SessionVFS) CurrentVersion() SemanticVersion { return s.wal.CurrentVersion() }

// CVS returns the CVS shim for backward compatibility.
func (s *SessionVFS) CVS() CVS { return s.cvsShim }

// VFSManager returns the session's VFS manager.
func (s *SessionVFS) VFSManager() VFSManager { return s.vfsManager }

// SessionID returns the session identifier.
func (s *SessionVFS) SessionID() SessionID { return s.sessionID }

// NewDiskFileAccess creates a DiskFileAccess for agents that bypass VFS.
func (s *SessionVFS) NewDiskFileAccess(readOnly bool) FileAccess {
	return NewDiskFileAccess(s.workingDir, readOnly)
}

// NewGlobalFileAccess creates a FileAccess backed by the global VFS overlay.
// When readOnly is true, returns a read-only VFS file access.
func (s *SessionVFS) NewGlobalFileAccess(readOnly bool) FileAccess {
	if readOnly {
		return NewReadOnlyVFSFileAccess(s.globalVFS, s.workingDir)
	}
	return NewVFSFileAccess(s.globalVFS, s.workingDir)
}

// NewPipelineFileAccess creates a VFSFileAccess backed by a per-pipeline VFS.
func (s *SessionVFS) NewPipelineFileAccess(vfs *PipelineVFS) FileAccess {
	return NewVFSFileAccess(vfs, s.workingDir)
}

// Close shuts down all session subsystems.
func (s *SessionVFS) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	s.mergePipe.Stop()

	var firstErr error
	if err := s.wal.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.vfsManager.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.globalVFS.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	// Close legacy CVS.
	if s.cvs != nil {
		if err := s.cvs.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// contextWithoutCancel returns a context that is never cancelled.
// Used for merge operations that should complete even if the
// requesting context is cancelled.
func contextWithoutCancel() context.Context {
	return context.Background()
}

// dagVersionStoreAdapter adapts a DAGStore to the VersionStore interface
// required by VFSManager and PipelineVFS. Kept for backward compat.
type dagVersionStoreAdapter struct {
	dag DAGStore
}

func (a *dagVersionStoreAdapter) GetHead(filePath string) (*FileVersion, error) {
	return a.dag.GetHead(filePath)
}

func (a *dagVersionStoreAdapter) GetVersion(id VersionID) (*FileVersion, error) {
	return a.dag.Get(id)
}

func (a *dagVersionStoreAdapter) GetHistory(filePath string, limit int) ([]FileVersion, error) {
	versions, err := a.dag.GetHistory(filePath, limit)
	if err != nil {
		return nil, err
	}
	result := make([]FileVersion, len(versions))
	for i, v := range versions {
		result[i] = *v
	}
	return result, nil
}

func (a *dagVersionStoreAdapter) AddVersion(version FileVersion) error {
	return a.dag.Add(version)
}
