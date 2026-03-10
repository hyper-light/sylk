package versioning

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
)

// SessionVFS owns the per-session versioning infrastructure:
// a global VFS overlay, MergePipe (OT-based pipeline→global merges),
// an in-memory semantic WAL, DiskFlusher
// (global→disk with checkpoint), and VFSManager (pipeline VFS lifecycle).
type SessionVFS struct {
	mu sync.Mutex

	sessionID   SessionID
	workingDir  string
	globalVFS   *PipelineVFS
	mergePipe   *MergePipe
	wal         SemanticWAL
	diskFlusher *DiskFlusher
	vfsManager  VFSManager
	otEngine    OTEngine
	draftMu     *sync.Mutex

	// Legacy CVS shim for backward compatibility during transition.
	cvsShim *CVSShim

	// Legacy stores kept temporarily for CVS shim.
	cvs       *DefaultCVS
	blobStore BlobStore
	dagStore  DAGStore
	opLog     OperationLog
	oldWAL    WriteAheadLog

	pipelines map[string]*sessionPipeline
	closed    bool
}

// SessionVFSStats captures live in-memory session VFS state without routing
// through the legacy CVS compatibility layer.
type SessionVFSStats struct {
	TrackedFiles      int64
	TotalVersions     int64
	TotalOperations   int64
	ActivePipelines   int64
	ActiveVariants    int64
	ActiveLocks       int64
	ActiveSubscribers int64
	CurrentVersion    SemanticVersion
	WALEntries        int64
}

type sessionPipelineState string

const (
	sessionPipelineActive    sessionPipelineState = "active"
	sessionPipelineFailed    sessionPipelineState = "failed"
	sessionPipelineCommitted sessionPipelineState = "committed"
)

type sessionPipeline struct {
	Config      BeginPipelineConfig
	VFS         *PipelineVFS
	BaseVersion SemanticVersion
	State       sessionPipelineState
	LastError   error
}

// SessionVFSConfig configures the per-session VFS infrastructure.
type SessionVFSConfig struct {
	SessionID   SessionID
	WorkingDir  string
	StorageRoot string
}

// NewSessionVFS creates an isolated set of versioning subsystems for a session.
func NewSessionVFS(cfg SessionVFSConfig) (*SessionVFS, error) {
	otEngine := NewOTEngine()
	vwal, err := openSessionSemanticWAL(cfg)
	if err != nil {
		return nil, err
	}
	draftMu := &sync.Mutex{}

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
		DraftMu:   draftMu,
	})

	// Create DiskFlusher wired to global VFS + WAL.
	df := NewDiskFlusher(DiskFlusherConfig{
		GlobalVFS:  globalVFS,
		WAL:        vwal,
		WorkingDir: cfg.WorkingDir,
		DraftMu:    draftMu,
	})

	// Legacy stores for CVS shim backward compatibility.
	blobStore := NewMemoryBlobStore()
	dagStore := NewMemoryDAGStore()
	opLog := NewMemoryOperationLog()
	oldWAL := NewMemoryWAL()

	// Create VFSManager backed by the new SessionVFS stores so history and
	// versioned reads can operate against real in-memory version/blob state.
	versionStore := &dagVersionStoreAdapter{dag: dagStore}
	vfsMgr := NewMemoryVFSManager(VFSManagerConfig{
		VersionStore: versionStore,
		BlobStore:    blobStore,
	})

	// Start merge goroutine.
	mp.Start()

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
		draftMu:     draftMu,
		cvs:         cvs,
		blobStore:   blobStore,
		dagStore:    dagStore,
		opLog:       opLog,
		oldWAL:      oldWAL,
		pipelines:   make(map[string]*sessionPipeline),
	}

	s.cvsShim = NewCVSShim(s)
	return s, nil
}

// BeginPipeline creates a new pipeline VFS and registers it with the MergePipe.
func (s *SessionVFS) BeginPipeline(cfg BeginPipelineConfig) (*PipelineVFS, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrVFSClosed
	}

	if existing := s.pipelines[cfg.PipelineID]; existing != nil && existing.VFS != nil {
		switch existing.State {
		case sessionPipelineActive, sessionPipelineFailed:
			return existing.VFS, nil
		}
	}

	vfsCfg := VFSConfig{
		PipelineID:   cfg.PipelineID,
		SessionID:    cfg.SessionID,
		WorkingDir:   cfg.WorkingDir,
		AgentID:      cfg.AgentID,
		AgentRole:    cfg.AgentRole,
		AllowedPaths: normalizeAllowedPaths(cfg.WorkingDir, cfg.Files),
	}

	pipelineVFS, err := s.vfsManager.CreatePipelineVFS(vfsCfg)
	if err != nil {
		return nil, fmt.Errorf("session vfs: begin pipeline: %w", err)
	}
	pipelineVFS.SetBaseReader(func(path string) ([]byte, error) {
		return s.globalVFS.Read(context.Background(), path)
	})
	for _, path := range vfsCfg.AllowedPaths {
		pipelineVFS.RegisterVisiblePath(path)
		content, readErr := s.globalVFS.Read(context.Background(), path)
		if readErr == nil {
			pipelineVFS.SeedFile(path, content)
		}
	}

	baseVersion := s.wal.CurrentVersion()
	if err := s.mergePipe.RegisterPipelineAt(cfg.PipelineID, baseVersion); err != nil {
		s.vfsManager.ClosePipelineVFS(cfg.PipelineID)
		return nil, fmt.Errorf("session vfs: register pipeline: %w", err)
	}

	s.pipelines[cfg.PipelineID] = &sessionPipeline{
		Config: BeginPipelineConfig{
			PipelineID:  cfg.PipelineID,
			SessionID:   cfg.SessionID,
			BaseVersion: cfg.BaseVersion,
			AgentID:     cfg.AgentID,
			AgentRole:   cfg.AgentRole,
			WorkingDir:  cfg.WorkingDir,
			Files:       append([]string(nil), cfg.Files...),
		},
		VFS:         pipelineVFS,
		BaseVersion: baseVersion,
		State:       sessionPipelineActive,
	}

	return pipelineVFS, nil
}

// CommitPipeline extracts modifications from the pipeline VFS, merges them
// into the global VFS via MergePipe (OT transform), and closes the pipeline.
func (s *SessionVFS) CommitPipeline(ctx context.Context, pipelineID string) (SemanticVersion, error) {
	mods, err := s.extractPipelineMods(pipelineID)
	if err != nil {
		return SemanticVersion{}, err
	}

	// Merge outside of s.mu — the merge goroutine is independent.
	ver, mergeErr := s.mergePipe.Merge(
		contextWithoutCancel(ctx),
		pipelineID,
		mods,
	)

	if mergeErr != nil {
		s.mu.Lock()
		if pipe := s.pipelines[pipelineID]; pipe != nil {
			pipe.State = sessionPipelineFailed
			pipe.LastError = mergeErr
		}
		_ = s.vfsManager.ClosePipelineVFS(pipelineID)
		delete(s.pipelines, pipelineID)
		s.mu.Unlock()
		return SemanticVersion{}, fmt.Errorf("session vfs: merge pipeline: %w", mergeErr)
	}

	s.mu.Lock()
	if pipe := s.pipelines[pipelineID]; pipe != nil {
		pipe.State = sessionPipelineCommitted
	}
	_ = s.vfsManager.ClosePipelineVFS(pipelineID)
	delete(s.pipelines, pipelineID)
	s.mu.Unlock()
	return ver, nil
}

// HasPipeline reports whether a task/pipeline draft is currently tracked.
func (s *SessionVFS) HasPipeline(pipelineID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	pipe := s.pipelines[pipelineID]
	return pipe != nil && pipe.VFS != nil
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
	err := s.vfsManager.ClosePipelineVFS(pipelineID)
	delete(s.pipelines, pipelineID)
	return err
}

// RollbackPipelineIfTracked rolls back and cleans up a tracked pipeline draft
// when present, returning whether a draft existed.
func (s *SessionVFS) RollbackPipelineIfTracked(pipelineID string) (bool, error) {
	if !s.HasPipeline(pipelineID) {
		return false, nil
	}
	return true, s.RollbackPipeline(pipelineID)
}

// GlobalVFS returns the session-scoped global VFS overlay.
func (s *SessionVFS) GlobalVFS() *PipelineVFS { return s.globalVFS }

// MergePipe returns the session's merge pipe.
func (s *SessionVFS) MergePipe() *MergePipe { return s.mergePipe }

// DiskFlusher returns the session's disk flusher.
func (s *SessionVFS) DiskFlusher() *DiskFlusher { return s.diskFlusher }

// WAL returns the session's live semantic WAL.
func (s *SessionVFS) WAL() SemanticWAL { return s.wal }

// CurrentVersion returns the current semantic version from the WAL.
func (s *SessionVFS) CurrentVersion() SemanticVersion { return s.wal.CurrentVersion() }

// Stats returns live in-memory session VFS metrics for observability and
// health reporting.
func (s *SessionVFS) Stats() SessionVFSStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	managerStats := s.vfsManager.Stats()
	trackedFiles := int64(s.globalVFS.KnownFileCount())
	for _, pipe := range s.pipelines {
		if pipe == nil || pipe.VFS == nil {
			continue
		}
		trackedFiles += int64(pipe.VFS.KnownFileCount())
	}

	return SessionVFSStats{
		TrackedFiles:      trackedFiles,
		TotalVersions:     int64(s.wal.IndexLen()),
		TotalOperations:   int64(s.wal.IndexLen()),
		ActivePipelines:   int64(len(s.pipelines)),
		ActiveVariants:    int64(managerStats.VariantGroups),
		ActiveLocks:       0,
		ActiveSubscribers: 0,
		CurrentVersion:    s.wal.CurrentVersion(),
		WALEntries:        int64(s.wal.IndexLen()),
	}
}

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
	return NewGlobalDraftFileAccess(s)
}

// NewPipelineFileAccess creates a VFSFileAccess backed by a per-pipeline VFS.
func (s *SessionVFS) NewPipelineFileAccess(vfs *PipelineVFS) FileAccess {
	return NewVFSFileAccess(vfs, s.workingDir)
}

// NewReadOnlyPipelineFileAccess creates a read-only FileAccess backed by a
// per-pipeline VFS.
func (s *SessionVFS) NewReadOnlyPipelineFileAccess(vfs *PipelineVFS) FileAccess {
	return NewReadOnlyVFSFileAccess(vfs, s.workingDir)
}

// WorkingDir returns the session working directory bound to this VFS.
func (s *SessionVFS) WorkingDir() string { return s.workingDir }

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
func (s *SessionVFS) PipelineFileAccess(pipelineID string) (FileAccess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrVFSClosed
	}
	pipe := s.pipelines[pipelineID]
	if pipe == nil || pipe.VFS == nil {
		return nil, ErrVFSNotFound
	}
	return s.NewPipelineFileAccess(pipe.VFS), nil
}

// ReadOnlyPipelineFileAccess returns a read-only FileAccess for a pipeline VFS.
func (s *SessionVFS) ReadOnlyPipelineFileAccess(pipelineID string) (FileAccess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrVFSClosed
	}
	pipe := s.pipelines[pipelineID]
	if pipe == nil || pipe.VFS == nil {
		return nil, ErrVFSNotFound
	}
	return s.NewReadOnlyPipelineFileAccess(pipe.VFS), nil
}

func normalizeAllowedPaths(workingDir string, files []string) []string {
	if len(files) == 0 {
		return nil
	}
	allowed := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if file == "" {
			continue
		}
		path := file
		if !filepath.IsAbs(path) {
			path = filepath.Join(workingDir, path)
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		allowed = append(allowed, path)
	}
	return allowed
}

func contextWithoutCancel(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
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
