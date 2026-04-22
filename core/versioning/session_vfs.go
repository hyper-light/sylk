package versioning

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SessionVFS owns the per-session versioning infrastructure:
// a global VFS overlay, MergePipe (OT-based pipeline→global merges),
// an in-memory semantic WAL, DiskFlusher
// (global→disk with checkpoint), and VFSManager (pipeline VFS lifecycle).
type SessionVFS struct {
	mu sync.Mutex

	sessionID           SessionID
	workingDir          string
	snapshotFS          vfsMutableBaseFS
	baseFS              vfsBaseFS
	baseImage           *workspaceImageHandle
	globalVFS           *PipelineVFS
	reviewVFS           *PipelineVFS
	mergePipe           *MergePipe
	wal                 SemanticWAL
	diskFlusher         *DiskFlusher
	vfsManager          VFSManager
	otEngine            OTEngine
	draftMu             *sync.Mutex
	persistSessionState bool
	allowDiskExport     bool

	// Legacy CVS shim for backward compatibility during transition.
	cvsShim *CVSShim

	// Legacy stores kept temporarily for CVS shim.
	cvs       *DefaultCVS
	blobStore BlobStore
	dagStore  DAGStore
	opLog     OperationLog
	oldWAL    WriteAheadLog

	pipelines   map[string]*sessionPipeline
	reviews     *sessionReviewState
	merges      *mergeLog
	commitQueue *CommitQueue
	closed      bool
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
	SessionID              SessionID
	WorkingDir             string
	StorageRoot            string
	PersistSessionState    bool
	AllowDiskExport        bool
	WorkspaceImageMaxBytes int64
}

// NewSessionVFS creates an isolated set of versioning subsystems for a session.
func NewSessionVFS(cfg SessionVFSConfig) (*SessionVFS, error) {
	otEngine := NewOTEngine()
	vwal, err := openSessionSemanticWAL(cfg)
	if err != nil {
		return nil, err
	}
	draftMu := &sync.Mutex{}
	baseFS, snapshotFS, baseImage, err := sessionBaseFS(cfg)
	if err != nil {
		return nil, err
	}

	// Create global VFS overlay (no version/blob store needed).
	globalVFS := NewGlobalVFS(VFSConfig{
		PipelineID: "global",
		SessionID:  cfg.SessionID,
		WorkingDir: cfg.WorkingDir,
	})
	globalVFS.SetBaseFS(baseFS)

	// Create a separate review overlay for the currently active OT/global-review
	// candidate. This sits above the dependency-visible global checkpoint state.
	reviewVFS := NewGlobalVFS(VFSConfig{
		PipelineID: "review",
		SessionID:  cfg.SessionID,
		WorkingDir: cfg.WorkingDir,
	})
	reviewVFS.SetBaseReader(func(path string) ([]byte, error) {
		return globalVFS.Read(context.Background(), path)
	})
	reviewVFS.SetBaseFS(pipelineOverlayBaseFS{vfs: globalVFS})

	// Create MergePipe wired to global VFS + WAL + OT.
	mp := NewMergePipe(MergePipeConfig{
		GlobalVFS: globalVFS,
		WAL:       vwal,
		OTEngine:  otEngine,
		DraftMu:   draftMu,
	})

	// Create DiskFlusher wired to global VFS + WAL.
	df := NewDiskFlusher(DiskFlusherConfig{
		GlobalVFS:       globalVFS,
		WAL:             vwal,
		SnapshotFS:      snapshotFS,
		WorkingDir:      cfg.WorkingDir,
		DraftMu:         draftMu,
		AllowDiskExport: cfg.AllowDiskExport,
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
		sessionID:           cfg.SessionID,
		workingDir:          cfg.WorkingDir,
		snapshotFS:          snapshotFS,
		baseFS:              baseFS,
		baseImage:           baseImage,
		globalVFS:           globalVFS,
		reviewVFS:           reviewVFS,
		mergePipe:           mp,
		wal:                 vwal,
		diskFlusher:         df,
		vfsManager:          vfsMgr,
		otEngine:            otEngine,
		draftMu:             draftMu,
		persistSessionState: cfg.PersistSessionState,
		allowDiskExport:     cfg.AllowDiskExport,
		cvs:                 cvs,
		blobStore:           blobStore,
		dagStore:            dagStore,
		opLog:               opLog,
		oldWAL:              oldWAL,
		pipelines:           make(map[string]*sessionPipeline),
		reviews:             newSessionReviewState(),
		merges:              newMergeLog(),
		commitQueue:         NewCommitQueue(),
	}

	s.cvsShim = NewCVSShim(s)
	if err := s.replayDraftFromWAL(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func sessionBaseFS(cfg SessionVFSConfig) (vfsBaseFS, vfsMutableBaseFS, *workspaceImageHandle, error) {
	baseImage, err := acquireWorkspaceImage(cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("session vfs: acquire workspace image: %w", err)
	}
	baseFS := newWorkspaceImageFS(baseImage)
	return baseFS, baseFS, baseImage, nil
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
	for _, path := range vfsCfg.AllowedPaths {
		pipelineVFS.RegisterVisiblePath(path)
	}

	// Choose the base version the pipeline is "pinned to." When
	// BaseCopyVersion is explicitly set (remediation dispatch), that
	// wins; otherwise the pipeline pins at the current WAL head.
	baseVersion := s.wal.CurrentVersion()
	if !cfg.BaseCopyVersion.IsZero() {
		baseVersion = cfg.BaseCopyVersion
	}

	// Install the pipeline's base reader / base FS.
	//
	// Default path: read-through to live green. This matches the
	// legacy behavior — the pipeline sees the most-recent committed
	// global state for paths it hasn't written itself.
	//
	// BaseCopyVersion path (parallel-global-VFS §3.6): read from a
	// pre-materialized CopyMaterialization that holds byte-for-byte
	// content at the target Copy's version, falling through to disk
	// baseline ONLY for paths the Copy didn't capture (unchanged
	// since disk). The materialization is owned by this pipeline's
	// dispatch and isolates it from subsequent green advancement.
	if !cfg.BaseCopyVersion.IsZero() {
		mat, matErr := s.copyAtLocked(cfg.BaseCopyVersion)
		if matErr != nil {
			_ = s.vfsManager.ClosePipelineVFS(cfg.PipelineID)
			return nil, fmt.Errorf("session vfs: materialize base copy %s: %w", cfg.BaseCopyVersion.String(), matErr)
		}
		baseFS := s.baseFS
		pipelineVFS.SetBaseReader(func(path string) ([]byte, error) {
			if content, ok := mat.Read(path); ok {
				return content, nil
			}
			if baseFS == nil {
				return nil, ErrFileNotFound
			}
			return baseFS.ReadFile(path)
		})
		pipelineVFS.SetBaseFS(&copyBackedBaseFS{mat: mat, fallback: baseFS})
	} else {
		pipelineVFS.SetBaseReader(func(path string) ([]byte, error) {
			return s.globalVFS.Read(context.Background(), path)
		})
		pipelineVFS.SetBaseFS(pipelineOverlayBaseFS{vfs: s.globalVFS})
	}

	if err := s.mergePipe.RegisterPipelineAt(cfg.PipelineID, baseVersion); err != nil {
		s.vfsManager.ClosePipelineVFS(cfg.PipelineID)
		return nil, fmt.Errorf("session vfs: register pipeline: %w", err)
	}

	s.pipelines[cfg.PipelineID] = &sessionPipeline{
		Config: BeginPipelineConfig{
			PipelineID:      cfg.PipelineID,
			SessionID:       cfg.SessionID,
			BaseVersion:     cfg.BaseVersion,
			AgentID:         cfg.AgentID,
			AgentRole:       cfg.AgentRole,
			WorkingDir:      cfg.WorkingDir,
			Files:           append([]string(nil), cfg.Files...),
			BaseCopyVersion: cfg.BaseCopyVersion,
		},
		VFS:         pipelineVFS,
		BaseVersion: baseVersion,
		State:       sessionPipelineActive,
	}

	return pipelineVFS, nil
}

// MergePipelineResult describes a pipeline's merge into green.
//
// Returned by MergePipelineIntoGreen. Callers use it to identify the
// merge event downstream (global review dispatch, audit replica launch,
// observability) without re-deriving state from the session VFS.
type MergePipelineResult struct {
	// PipelineID identifies the source pipeline whose mods were merged.
	PipelineID string
	// HadDraft reports whether any modifications existed at merge time.
	// False when the pipeline VFS held no writes (rollback semantics
	// applied instead).
	HadDraft bool
	// BaseVersion is the green version the pipeline was built against.
	BaseVersion SemanticVersion
	// MergedVersion is the green version AFTER the merge completed.
	// Equal to BaseVersion when HadDraft is false.
	MergedVersion SemanticVersion
	// PathCount is the number of distinct paths touched by the merge.
	// Zero when HadDraft is false.
	PathCount int
}

// MergePipelineIntoGreen merges a pipeline's accumulated modifications
// directly into the session's global VFS ("green") via MergePipe/OT. The
// pipeline VFS is closed and removed from the session map.
//
// This replaces the pipelineVFS → ReviewCandidate → AcceptActiveReviewCandidate
// intermediate on the pipeline-inspector-accept path. After this call,
// the pipeline's work is immediately visible to:
//   - sibling pipelines that read through the global VFS base reader
//   - subsequent pipeline dispatches (including remediation pipelines)
//     that open their own pipeline VFS against green's current state
//   - the global review audit (which inspects green's accumulated state)
//
// Semantics:
//   - Zero-mods pipelines are rolled back (no WAL entry, no green mutation).
//   - Merge conflicts are resolved by MergePipe via OT transform against
//     any green advancement since this pipeline's BaseVersion.
//   - On merge failure, the pipeline's state is marked failed and the
//     pipeline VFS is closed; the error is returned.
//
// The returned MergePipelineResult carries the identifiers downstream
// machinery needs to launch audits, dispatch remediation, or record
// observability events.
func (s *SessionVFS) MergePipelineIntoGreen(ctx context.Context, pipelineID string) (MergePipelineResult, error) {
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return MergePipelineResult{}, fmt.Errorf("session vfs: merge pipeline: pipeline id is required")
	}

	baseVersion := SemanticVersion{}
	pathCount := 0
	s.mu.Lock()
	if pipe := s.pipelines[pipelineID]; pipe != nil {
		baseVersion = pipe.BaseVersion
	}
	s.mu.Unlock()

	mods, err := s.extractPipelineMods(pipelineID)
	if err != nil {
		return MergePipelineResult{PipelineID: pipelineID}, err
	}
	pathCount = len(mods)
	if pathCount == 0 {
		if rollbackErr := s.RollbackPipeline(pipelineID); rollbackErr != nil {
			return MergePipelineResult{PipelineID: pipelineID, BaseVersion: baseVersion}, rollbackErr
		}
		current := s.CurrentVersion()
		return MergePipelineResult{
			PipelineID:    pipelineID,
			HadDraft:      false,
			BaseVersion:   baseVersion,
			MergedVersion: current,
			PathCount:     0,
		}, nil
	}

	merged, mergeErr := s.mergePipe.Merge(
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
		return MergePipelineResult{
				PipelineID:  pipelineID,
				HadDraft:    true,
				BaseVersion: baseVersion,
				PathCount:   pathCount,
			},
			fmt.Errorf("session vfs: merge pipeline %s into green: %w", pipelineID, mergeErr)
	}

	s.mu.Lock()
	if pipe := s.pipelines[pipelineID]; pipe != nil {
		pipe.State = sessionPipelineCommitted
	}
	_ = s.vfsManager.ClosePipelineVFS(pipelineID)
	delete(s.pipelines, pipelineID)
	s.mu.Unlock()

	// Record the merge descriptor so downstream machinery (audit
	// replica dispatch, remediation base-Copy lookup, cleanup water
	// line) can reference this merge by its monotonic MergedVersion.
	descriptor := MergeDescriptor{
		PipelineID:    pipelineID,
		BaseVersion:   baseVersion,
		MergedVersion: merged,
		Paths:         pathsFromMods(mods),
		PathCount:     pathCount,
		MergedAt:      time.Now().UTC(),
	}
	s.merges.record(descriptor)

	// Enqueue this merge on the commit queue in auditing state. The
	// audit replica dispatch layer (stage 3 wiring) observes new
	// queue entries and launches per-merge audit replicas. The commit
	// resolver (below) progresses entries from accepted → committed
	// in arrival order. See docs/PARALLEL_GLOBAL_VFS.md §3.4.
	if s.commitQueue != nil {
		s.commitQueue.Enqueue(descriptor)
	}

	return MergePipelineResult{
		PipelineID:    pipelineID,
		HadDraft:      true,
		BaseVersion:   baseVersion,
		MergedVersion: merged,
		PathCount:     pathCount,
	}, nil
}

// MergeDescriptors returns an ordered snapshot of all retained merge
// descriptors for this session. Ordering is arrival (earliest first).
//
// Used by:
//   - stage 3 audit replica dispatch to enumerate in-flight merges
//   - remediation lookup to find the Copy corresponding to a failing
//     merged version
//   - observability to expose the current merge pipeline
func (s *SessionVFS) MergeDescriptors() []MergeDescriptor {
	if s == nil || s.merges == nil {
		return nil
	}
	return s.merges.snapshot()
}

// LatestMergeDescriptor returns the most recent merge descriptor. ok is
// false when no merges have occurred in this session.
func (s *SessionVFS) LatestMergeDescriptor() (MergeDescriptor, bool) {
	if s == nil || s.merges == nil {
		return MergeDescriptor{}, false
	}
	return s.merges.latest()
}

// FindMergeDescriptor returns the descriptor whose MergedVersion matches
// the given version. ok is false when no such descriptor exists.
//
// This is the primary lookup path for remediation dispatch: given a
// rejected MergedVersion, produce the MergeDescriptor carrying the
// Copy identity to materialize for the remediation pipeline's VFS.
func (s *SessionVFS) FindMergeDescriptor(ver SemanticVersion) (MergeDescriptor, bool) {
	if s == nil || s.merges == nil {
		return MergeDescriptor{}, false
	}
	return s.merges.findByMergedVersion(ver)
}

// CommitQueue returns the session's commit queue. Callers use this to
// observe entries, mark audit decisions (accepted / rejected), record
// supersession (remediation-supersedes-rejection), and drive the
// commit resolver.
//
// The queue is automatically populated by MergePipelineIntoGreen on
// each successful merge; no external enqueue is required for the
// happy-path pipeline-accept flow.
func (s *SessionVFS) CommitQueue() *CommitQueue {
	if s == nil {
		return nil
	}
	return s.commitQueue
}

// MergesAfter returns merge descriptors for all merges with
// MergedVersion > the given version. Used by mid-audit replicas to
// discover new work that arrived after their audit context was cut.
//
// See docs/PARALLEL_GLOBAL_VFS.md §3.8 for the mid-audit-awareness
// design. An auditing replica typically starts by querying
// MergesAfter(its_base_copy) to decide whether its context remains
// valid or whether it should rebase.
func (s *SessionVFS) MergesAfter(ver SemanticVersion) []MergeDescriptor {
	if s == nil || s.merges == nil {
		return nil
	}
	return s.merges.since(ver)
}

// CommitPipeline extracts modifications from the pipeline VFS, merges them
// into the global VFS via MergePipe (OT transform), and closes the pipeline.
//
// Deprecated: prefer MergePipelineIntoGreen which returns a structured
// result. CommitPipeline is retained for backward compatibility with
// existing tests and legacy callers; new call sites should use the new
// method.
func (s *SessionVFS) CommitPipeline(ctx context.Context, pipelineID string) (SemanticVersion, error) {
	mods, err := s.extractPipelineMods(pipelineID)
	if err != nil {
		return SemanticVersion{}, err
	}
	if len(mods) == 0 {
		if rollbackErr := s.RollbackPipeline(pipelineID); rollbackErr != nil {
			return SemanticVersion{}, rollbackErr
		}
		return s.CurrentVersion(), nil
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
	return persistentFileModifications(pipelineVFS.GetModifications()), nil
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
	if err := s.reviewVFS.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if s.baseImage != nil {
		s.baseImage.Release()
		s.baseImage = nil
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

func (s *SessionVFS) replayDraftFromWAL() error {
	checkpoint, ok := s.wal.LatestCheckpoint()
	base := VersionZero
	if ok {
		base = checkpoint
	}
	entries, err := s.wal.GetDeltasSince(base)
	if err != nil {
		return fmt.Errorf("session vfs: replay WAL: %w", err)
	}
	for _, entry := range entries {
		for _, delta := range entry.Deltas {
			switch delta.Op {
			case WALDeltaOpCreate, WALDeltaOpModify:
				if err := s.globalVFS.Write(context.Background(), delta.Path, delta.NewContent); err != nil {
					return fmt.Errorf("session vfs: replay write %s: %w", delta.Path, err)
				}
			case WALDeltaOpDelete:
				if err := s.globalVFS.Delete(context.Background(), delta.Path); err != nil && !errors.Is(err, ErrFileNotFound) {
					return fmt.Errorf("session vfs: replay delete %s: %w", delta.Path, err)
				}
			case WALDeltaOpMkdir:
				if err := s.globalVFS.MkdirAll(context.Background(), delta.Path); err != nil && !errors.Is(err, ErrFileExists) {
					return fmt.Errorf("session vfs: replay mkdir %s: %w", delta.Path, err)
				}
			}
		}
	}
	return nil
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
