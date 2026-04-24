package versioning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
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
	merges      *mergeLog
	commitQueue *CommitQueue

	// controlWAL durably records session-level decision events
	// (commit-queue transitions, Copy retention, replica lifecycle,
	// session open/close epochs). Separate from the semantic WAL
	// which records file content. nil when session is in strict-RAM
	// mode (no StorageRoot / PersistSessionState disabled).
	controlWAL          *ControlWAL
	copyRetention       *CopyRetention
	replicaLifecycleLog *ReplicaLifecycleLog
	commitResolver      *CommitResolver
	resolverCtx         context.Context
	resolverCancel      context.CancelFunc

	// mergeCallbacks are invoked synchronously from
	// MergePipelineIntoGreen immediately after the descriptor is
	// enqueued on the commit queue. This is the direct dispatch
	// path per docs/PARALLEL_GLOBAL_VFS.md §3.3 — the flow is
	// pipeline-inspector handoff_to_ot → MergePipelineIntoGreen →
	// callback → replica spawn. No bus broadcast, no orchestrator
	// involvement. Session-lifecycle wiring registers the audit
	// coordinator's callback at bootstrap.
	mergeCallbackMu sync.Mutex
	mergeCallbacks  []MergeCallback

	// replicaVFSByVersion tracks per-merge ReplicaVFS instances so
	// they can be looked up by MergedVersion (for parent-chain
	// wiring, observability, and release by the commit resolver).
	replicaVFSByVersion map[SemanticVersion]*ReplicaVFS

	// clock accumulates session-open wall time for SLA tracking (§8).
	// Started in NewSessionVFS after the open epoch is recorded,
	// stopped in Close just before the close epoch fires.
	clock *SessionClock

	// steeringJournalPath is the directory where per-agent steering
	// WAL journals live. Populated when StorageRoot is set; empty in
	// strict-RAM mode. Exposed via SteeringJournalDir so agent
	// bootstrapping code can open journals with consistent paths.
	steeringJournalPath string

	// dispatchGate enforces queue-depth backpressure on BeginPipeline
	// when the config sets DispatchMaxInFlight > 0 (§5.1). Nil when
	// backpressure is disabled.
	dispatchGate *DispatchGate

	// backgroundScope runs the session's per-loop goroutines
	// (MergePipe merge loop, CommitResolver poll loop, DefaultCVS
	// callback fan-out dispatcher). When the caller does not supply
	// one via SessionVFSConfig.BackgroundScope, NewSessionVFS creates
	// an owned scope that Close shuts down. Tracked-goroutine
	// invariant per CLAUDE.md.
	backgroundScope     *concurrency.GoroutineScope
	ownsBackgroundScope bool

	// forensicArchiver, when non-nil, fires on every ReplicaVFS
	// release on water-line advance so operators can persist
	// sealed diffs / abandon markers outside the session's in-RAM
	// state (§5.2).
	forensicArchiver ForensicArchiver

	closed bool
}

// MergeCallback is invoked for every merge completed by
// MergePipelineIntoGreen. Callbacks run synchronously from within
// MergePipelineIntoGreen — a caller that needs asynchronous
// dispatch should spawn its own goroutine. Multiple callbacks fire
// in registration order.
type MergeCallback func(desc MergeDescriptor)

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

	// DispatchMaxInFlight caps the number of non-terminal commit-
	// queue entries. When non-zero, BeginPipeline blocks at an
	// internal DispatchGate until capacity is available (§5.1
	// backpressure). Zero disables backpressure entirely.
	DispatchMaxInFlight int
	// DispatchScope is the GoroutineScope the dispatch gate's
	// subscriber loop runs in. Required when DispatchMaxInFlight > 0
	// — an unscoped gate would spawn an untracked goroutine, so
	// NewSessionVFS returns an error in that case.
	DispatchScope *concurrency.GoroutineScope

	// BackgroundScope runs the session's per-loop goroutines
	// (MergePipe, CommitResolver, DefaultCVS dispatcher). When nil,
	// NewSessionVFS creates an owned scope named
	// "session-vfs-background:<session_id>" and shuts it down on
	// Close. All session-owned goroutines run under this scope so
	// they are tracked per CLAUDE.md.
	BackgroundScope *concurrency.GoroutineScope

	// ForensicArchiver, when non-nil, receives every ReplicaVFS the
	// retention-driven cleanup releases on water-line advance
	// (§5.2). The archiver inspects the sealed diff (if the VFS
	// was accepted) or the abandon marker (if rejected) and
	// persists whatever forensic detail the operator has opted
	// into — typically sealed writes + deletes to a TTL-bounded
	// directory. Fires synchronously BEFORE the in-RAM release so
	// the archiver can safely hold the handle. Errors are logged
	// at Error and do not block release.
	ForensicArchiver ForensicArchiver
}

// ForensicArchiver records the sealed diff / abandon outcome of a
// released ReplicaVFS. Called by the retention GC pass on water-
// line advance for every version whose refcount dropped to zero.
// Implementations are responsible for their own durability + TTL.
type ForensicArchiver interface {
	ArchiveReleased(info ForensicReleaseInfo) error
}

// ForensicReleaseInfo is the payload an archiver receives for a
// released ReplicaVFS. The SealedDiff is nil when the VFS was
// abandoned (rejected audit) — only accepted audits have a diff to
// archive. Paths is populated from the sealed diff when present so
// the archiver does not need to re-walk the VFS.
type ForensicReleaseInfo struct {
	SessionID      SessionID
	MergedVersion  SemanticVersion
	ReleasedAt     time.Time
	Sealed         bool
	Abandoned      bool
	SealedDiff     *ReplicaVFSDiff
	ChainReadPaths []string
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

	// Background scope for session-owned goroutines (MergePipe,
	// CommitResolver, DefaultCVS dispatcher). Use the caller-supplied
	// scope when provided so the session's goroutines are tracked
	// alongside the rest of the caller's tracked work; otherwise
	// create an owned one that Close shuts down.
	backgroundScope := cfg.BackgroundScope
	ownsBackgroundScope := false
	if backgroundScope == nil {
		backgroundScope = concurrency.NewGoroutineScope(context.Background(), "session-vfs-background:"+string(cfg.SessionID), nil)
		ownsBackgroundScope = true
	}
	backgroundCtx := concurrency.WithScope(context.Background(), backgroundScope)

	// Start merge goroutine under the tracked background scope.
	if err := mp.Start(backgroundCtx); err != nil {
		if ownsBackgroundScope {
			_ = backgroundScope.Shutdown(100*time.Millisecond, 2*time.Second)
		}
		return nil, fmt.Errorf("session vfs: start merge pipe: %w", err)
	}

	cvs, cvsErr := NewCVS(CVSConfig{
		VFSManager:       vfsMgr,
		BlobStore:        blobStore,
		OpLog:            opLog,
		DAGStore:         dagStore,
		WAL:              oldWAL,
		OTEngine:         otEngine,
		DispatcherScope:  backgroundScope,
		DispatcherBuffer: 64,
	})
	if cvsErr != nil {
		if ownsBackgroundScope {
			_ = backgroundScope.Shutdown(100*time.Millisecond, 2*time.Second)
		}
		return nil, fmt.Errorf("session vfs: init cvs: %w", cvsErr)
	}

	// Control WAL carries session-level decision events (commit-queue
	// transitions, Copy retention, replica lifecycle, session
	// epochs). Opened alongside the disk-backed semantic WAL when a
	// StorageRoot is set; both WALs share the same durability
	// boundary so a session is either fully durable or fully
	// ephemeral.
	var ctrlWAL *ControlWAL
	if strings.TrimSpace(cfg.StorageRoot) != "" {
		var wErr error
		ctrlWAL, wErr = OpenControlWAL(ControlWALConfig{Dir: cfg.StorageRoot})
		if wErr != nil {
			return nil, fmt.Errorf("session vfs: open control wal: %w", wErr)
		}
	}

	commitQueue := NewCommitQueue(ctrlWAL)
	// Forward-declare so the retention callback can call back into s.
	// The callback fires from AdvanceWaterLine AFTER the retention
	// mutex is released, so it's safe to re-enter session methods.
	var sessionRef *SessionVFS
	retention := NewCopyRetention(CopyRetentionConfig{
		WAL: ctrlWAL,
		OnRelease: func(released []SemanticVersion) {
			if sessionRef == nil {
				return
			}
			sessionRef.releaseAuditReplicaVFSBatch(released)
		},
	})
	replicaLifecycle := NewReplicaLifecycleLog(ctrlWAL)

	s := &SessionVFS{
		sessionID:           cfg.SessionID,
		workingDir:          cfg.WorkingDir,
		snapshotFS:          snapshotFS,
		baseFS:              baseFS,
		baseImage:           baseImage,
		globalVFS:           globalVFS,
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
		merges:              newMergeLog(),
		commitQueue:         commitQueue,
		controlWAL:          ctrlWAL,
		copyRetention:       retention,
		replicaLifecycleLog: replicaLifecycle,
		clock:               NewSessionClock(),
		steeringJournalPath: steeringJournalDir(cfg.StorageRoot),
		backgroundScope:     backgroundScope,
		ownsBackgroundScope: ownsBackgroundScope,
		forensicArchiver:    cfg.ForensicArchiver,
	}

	sessionRef = s
	s.cvsShim = NewCVSShim(s)
	if err := s.replayDraftFromWAL(); err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.replayControlWAL(); err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.startCommitResolver(); err != nil {
		_ = s.Close()
		return nil, err
	}
	s.clock.Start(time.Now().UTC())
	if err := s.recordSessionEpoch(ControlKindSessionOpenEpoch, "session-open"); err != nil {
		_ = s.Close()
		return nil, err
	}
	if cfg.DispatchMaxInFlight > 0 {
		if cfg.DispatchScope == nil {
			_ = s.Close()
			return nil, fmt.Errorf("session vfs: DispatchMaxInFlight set without DispatchScope (untracked goroutines are forbidden)")
		}
		s.dispatchGate = NewDispatchGate(DispatchGateConfig{
			Session:     s,
			MaxInFlight: cfg.DispatchMaxInFlight,
			Scope:       cfg.DispatchScope,
		})
		if err := s.dispatchGate.Start(context.Background()); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("session vfs: start dispatch gate: %w", err)
		}
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

// AcquireDispatchSlot blocks until the commit queue has room for
// another non-terminal merge under the session's configured
// DispatchMaxInFlight cap, or ctx cancels. Returns nil when the
// caller may proceed to BeginPipeline. Callers that do not wire
// backpressure (DispatchMaxInFlight = 0) get pass-through behaviour.
//
// Why a separate method (vs gating BeginPipeline internally): the
// backpressure wait is potentially long and must not hold the
// session mutex. Callers acquire the slot first, then call
// BeginPipeline with the session mutex in the critical section.
//
// See docs/PARALLEL_GLOBAL_VFS.md §5.1.
func (s *SessionVFS) AcquireDispatchSlot(ctx context.Context) error {
	if s == nil || s.dispatchGate == nil {
		return ctx.Err()
	}
	return s.dispatchGate.Acquire(ctx)
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
		if err := s.materializePipelineFromCopyLocked(pipelineVFS, cfg.BaseCopyVersion); err != nil {
			_ = s.vfsManager.ClosePipelineVFS(cfg.PipelineID)
			return nil, err
		}
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

	// Pin the Copy so it cannot be released while this pipeline is
	// running against it (§5 release criteria #3: "no pending
	// remediation dispatch targets this Copy"). Only for pipelines
	// explicitly dispatched against a historical Copy — the default
	// current-green path needs no retention (green is never below
	// the water line by definition). Holder ID namespaced with
	// "pipeline-dispatch:" so it can't collide with the coordinator's
	// per-replica retentions. The release fires from every pipeline-
	// close site (merge success, merge fail, rollback, session close).
	if !cfg.BaseCopyVersion.IsZero() && s.copyRetention != nil {
		if err := s.copyRetention.Retain(cfg.BaseCopyVersion, pipelineDispatchHolderID(cfg.PipelineID)); err != nil {
			s.mergePipe.UnregisterPipeline(cfg.PipelineID)
			s.vfsManager.ClosePipelineVFS(cfg.PipelineID)
			return nil, fmt.Errorf("session vfs: retain base copy %s: %w", cfg.BaseCopyVersion.String(), err)
		}
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
// After this call,
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
//
// cert is the PipelineInspectorCertificate the pipeline inspector
// attached at handoff_to_ot time (declared scope, open concerns,
// summary, tester verdict). It is stored on the resulting
// MergeDescriptor so per-merge audit replicas can reason about the
// pipeline's local attestation alongside the cross-pipeline coherence
// picture. A zero-value certificate is legal — audit replicas treat an
// empty declared scope as a missing attestation and proceed on the
// diff alone. See docs/PARALLEL_GLOBAL_VFS.md §6.5.
func (s *SessionVFS) MergePipelineIntoGreen(ctx context.Context, pipelineID string, cert PipelineInspectorCertificate) (MergePipelineResult, error) {
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return MergePipelineResult{}, fmt.Errorf("session vfs: merge pipeline: pipeline id is required")
	}

	baseVersion := SemanticVersion{}
	baseCopyVersion := SemanticVersion{}
	pathCount := 0
	s.mu.Lock()
	if pipe := s.pipelines[pipelineID]; pipe != nil {
		baseVersion = pipe.BaseVersion
		baseCopyVersion = pipe.Config.BaseCopyVersion
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
		s.releasePipelineCopyHold(pipelineID)
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
	s.releasePipelineCopyHold(pipelineID)

	// Record the merge descriptor so downstream machinery (audit
	// replica dispatch, remediation base-Copy lookup, cleanup water
	// line) can reference this merge by its monotonic MergedVersion.
	descriptor := MergeDescriptor{
		PipelineID:          pipelineID,
		BaseVersion:         baseVersion,
		MergedVersion:       merged,
		Paths:               pathsFromMods(mods),
		PathCount:           pathCount,
		MergedAt:            time.Now().UTC(),
		PipelineCertificate: cert,
	}
	// Remediation dispatch: if the pipeline was pinned to a
	// BaseCopyVersion that the commit queue tracks as Rejected, this
	// merge supersedes it (docs/PARALLEL_GLOBAL_VFS.md §3.8). The
	// coordinator's finalizeDecision reads SupersedesVersion when the
	// audit accepts and calls CommitQueue.MarkSuperseded to transition
	// the rejected slot. We look up the queue entry here rather than
	// relying solely on the caller-supplied flag so the relationship
	// stays authoritative: "superseded" follows from the pinned Copy
	// actually being rejected, not from a metadata bit that could
	// drift.
	if !baseCopyVersion.IsZero() && s.commitQueue != nil {
		if entry := s.commitQueue.Lookup(baseCopyVersion); entry != nil && entry.State == CommitStateRejected {
			descriptor.SupersedesVersion = baseCopyVersion
		}
	}
	s.merges.record(descriptor)

	// Enqueue this merge on the commit queue in auditing state. The
	// audit coordinator is notified via RegisterMergeCallback (fired
	// below); the commit resolver progresses entries from accepted →
	// committed in arrival order. See docs/PARALLEL_GLOBAL_VFS.md
	// §3.4. Enqueue is write-ahead to ControlWAL; a persistence
	// failure aborts the merge so the caller can surface it to the
	// pipeline inspector rather than silently losing the queue
	// entry.
	if s.commitQueue != nil {
		if _, err := s.commitQueue.Enqueue(descriptor); err != nil {
			return MergePipelineResult{}, fmt.Errorf("merge pipeline into green: enqueue commit queue: %w", err)
		}
	}

	// Direct-protocol dispatch: fire merge callbacks synchronously.
	// The audit coordinator's callback spawns per-merge inspector +
	// tester replicas. No bus broadcast, no orchestrator hop. The
	// pipeline-inspector → OT → merge → replica-spawn chain is all
	// direct method calls and durable WAL records.
	s.fireMergeCallbacks(descriptor)

	return MergePipelineResult{
		PipelineID:    pipelineID,
		HadDraft:      true,
		BaseVersion:   baseVersion,
		MergedVersion: merged,
		PathCount:     pathCount,
	}, nil
}

// RecordMergeDescriptorForTest appends a descriptor directly to the
// session's merge log, bypassing the MergePipelineIntoGreen path.
// Intended ONLY for unit tests that need to exercise coordinator /
// commit-queue behavior with synthetic descriptors (supersession,
// re-audit paths). Production code must go through
// MergePipelineIntoGreen so OT, WAL, and callbacks all fire.
func (s *SessionVFS) RecordMergeDescriptorForTest(desc MergeDescriptor) {
	if s == nil || s.merges == nil {
		return
	}
	s.merges.record(desc)
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
		s.releasePipelineCopyHold(pipelineID)
		return SemanticVersion{}, fmt.Errorf("session vfs: merge pipeline: %w", mergeErr)
	}

	s.mu.Lock()
	if pipe := s.pipelines[pipelineID]; pipe != nil {
		pipe.State = sessionPipelineCommitted
	}
	_ = s.vfsManager.ClosePipelineVFS(pipelineID)
	delete(s.pipelines, pipelineID)
	s.mu.Unlock()
	s.releasePipelineCopyHold(pipelineID)
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
	// Release the BaseCopyVersion retention (if any) so the water
	// line can advance past the Copy once remaining holders drop.
	defer s.releasePipelineCopyHold(pipelineID)
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

	// Stop the session clock — SLA timers must not advance while the
	// session is closed (§8). The close-epoch WAL record pairs with
	// this so restarted sessions can reconcile the boundary.
	if s.clock != nil {
		s.clock.Stop(time.Now().UTC())
	}

	// Write the session-close epoch BEFORE tearing down subsystems so
	// the WAL records a durable close marker. Failure to record is
	// logged-and-continue: a crash between now and the close-marker
	// write looks identical to a crash at any other point, and Open's
	// replay handles both.
	if s.controlWAL != nil {
		_ = s.recordSessionEpochLocked(ControlKindSessionCloseEpoch, "session-close")
	}
	if s.dispatchGate != nil {
		s.dispatchGate.Stop()
		s.dispatchGate = nil
	}

	if s.commitResolver != nil && s.resolverCancel != nil {
		s.resolverCancel()
		s.commitResolver.Stop()
	}

	s.mergePipe.Stop()

	// Release all per-merge ReplicaVFS handles so their in-RAM
	// overlays + chain refs drop (docs/PARALLEL_GLOBAL_VFS.md §4.4).
	// The ControlWAL's overlay-write records persist the authoritative
	// history; dropping the in-RAM handle here is pure resource
	// cleanup — the next Open replays content if needed.
	for ver := range s.replicaVFSByVersion {
		delete(s.replicaVFSByVersion, ver)
	}

	var firstErr error
	if s.controlWAL != nil {
		if err := s.controlWAL.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := s.wal.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.vfsManager.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.globalVFS.Close(); err != nil && firstErr == nil {
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

	// Tear down the background scope if we own it. Caller-supplied
	// scopes are their responsibility to shut down. The shutdown is
	// bounded so a misbehaving goroutine doesn't block Close forever;
	// a leak here would surface as a lingering scope in the parent's
	// accounting rather than silent goroutine rot.
	if s.ownsBackgroundScope && s.backgroundScope != nil {
		if err := s.backgroundScope.Shutdown(500*time.Millisecond, 5*time.Second); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// replayControlWAL rebuilds commit queue and retention state from the
// session's ControlWAL. Called once during construction, after the
// semantic WAL draft replay so the merge log is populated before
// queue entries are rehydrated. A failure mid-replay aborts session
// open — the caller Closes and surfaces the error.
//
// Replay is strictly ordered: entries are visited in seq order, and
// each entry is routed to the subsystem it belongs to
// (CommitQueue.ApplyControlEntry for queue kinds;
// CopyRetention.ApplyControlEntry for retention kinds; session-epoch
// and replica-lifecycle kinds are recorded but not yet consumed —
// they land in later stages).
func (s *SessionVFS) replayControlWAL() error {
	if s.controlWAL == nil {
		return nil
	}
	lookup := func(ver SemanticVersion) (MergeDescriptor, bool) {
		desc, ok := s.merges.findByMergedVersion(ver)
		if ok {
			return desc, true
		}
		return MergeDescriptor{}, false
	}
	// Accumulate (open, close) epoch pairs so the session clock can
	// be seeded before Start fires — SLA timers calibrated against
	// prior session-open intervals survive a clean close / open
	// cycle (docs/PARALLEL_GLOBAL_VFS.md §8).
	var (
		priorOpenAt    time.Time
		priorOpenValid bool
		accumulatedOpen time.Duration
	)
	err := s.controlWAL.Replay(func(e *ControlEntry) error {
		switch e.Kind {
		case ControlKindCommitQueueEnqueue,
			ControlKindCommitQueueMarkAccepted,
			ControlKindCommitQueueMarkRejected,
			ControlKindCommitQueueMarkSuperseded,
			ControlKindCommitQueueMarkCommitted,
			ControlKindCommitQueueAbandon:
			return s.commitQueue.ApplyControlEntry(e, lookup)
		case ControlKindRetentionRetain,
			ControlKindRetentionRelease,
			ControlKindRetentionAdvanceWaterLine:
			s.copyRetention.ApplyControlEntry(e)
			return nil
		case ControlKindReplicaSpawned,
			ControlKindReplicaDecided,
			ControlKindReplicaCrashed:
			s.replicaLifecycleLog.ApplyControlEntry(e)
			return nil
		case ControlKindReplicaOverlayWrite,
			ControlKindReplicaOverlayDelete,
			ControlKindReplicaOverlaySealed,
			ControlKindReplicaOverlayAbandoned:
			// Overlay entries rehydrate the per-merge ReplicaVFS so
			// a session that crashed mid-audit recovers its overlay
			// state on next open. §3.6 durability: "in-flight
			// ReplicaVFS writes are durable via the ControlWAL; a
			// crash mid-write replays on next open."
			return s.applyOverlayControlEntryLocked(e)
		case ControlKindSessionOpenEpoch:
			priorOpenAt = e.Timestamp
			priorOpenValid = true
			return nil
		case ControlKindSessionCloseEpoch:
			if priorOpenValid {
				if delta := e.Timestamp.Sub(priorOpenAt); delta > 0 {
					accumulatedOpen += delta
				}
				priorOpenValid = false
			}
			return nil
		default:
			return nil
		}
	})
	if err != nil {
		return err
	}
	if accumulatedOpen > 0 && s.clock != nil {
		// Seed BEFORE Start fires (NewSessionVFS invokes
		// replayControlWAL before it invokes clock.Start). If the
		// invariant flips, surface the error rather than panicking —
		// the session still opens on a fresh clock, we just lose
		// the accumulated history for SLA calculations.
		if err := s.clock.SeedAccumulated(accumulatedOpen); err != nil {
			slog.Default().Error("session vfs: seed clock failed — SLA timers will restart from zero",
				"session_id", string(s.sessionID),
				"accumulated_seconds", accumulatedOpen.Seconds(),
				"error", err.Error(),
			)
		}
	}
	return nil
}

// applyOverlayControlEntryLocked rehydrates a single overlay WAL
// entry into the session's replicaVFSByVersion map. Lazily
// constructs the target ReplicaVFS the first time an entry is seen
// for its MergedVersion. Called only from replayControlWAL (which
// holds no lock because NewSessionVFS is single-threaded at this
// stage — no other goroutine can see s yet). Chain-parent wiring
// matches BeginAuditReplicaVFS's behavior: walk the merge log
// backwards for the most recent non-abandoned replica.
func (s *SessionVFS) applyOverlayControlEntryLocked(e *ControlEntry) error {
	if e == nil {
		return nil
	}
	ver := e.Payload.ReplicaMergeVersion
	if s.replicaVFSByVersion == nil {
		s.replicaVFSByVersion = make(map[SemanticVersion]*ReplicaVFS)
	}
	vfs, ok := s.replicaVFSByVersion[ver]
	if !ok {
		// Chain-parent lookup identical to BeginAuditReplicaVFS:
		// walk merges backward, pick the most recent live replica.
		var parent *ReplicaVFS
		if s.merges != nil {
			descs := s.merges.snapshot()
			for i := len(descs) - 1; i >= 0; i-- {
				d := descs[i]
				if d.MergedVersion.Compare(ver) >= 0 {
					continue
				}
				if r, tracked := s.replicaVFSByVersion[d.MergedVersion]; tracked && r != nil && !r.IsAbandoned() {
					parent = r
					break
				}
			}
		}
		var err error
		vfs, err = NewReplicaVFS(ReplicaVFSConfig{
			MergedVersion: ver,
			ReplicaID:     e.Payload.ReplicaID,
			Parent:        parent,
			DiskBase:      s.baseFS,
			// WAL nil during replay: re-applying the WAL to the
			// WAL would double-record every entry on next close.
			// ApplyControlEntry does not write to WAL regardless.
			WAL: nil,
		})
		if err != nil {
			return fmt.Errorf("session vfs: rehydrate replica vfs %s: %w", ver.String(), err)
		}
		s.replicaVFSByVersion[ver] = vfs
	}
	vfs.ApplyControlEntry(e)
	return nil
}

// startCommitResolver wires the resolver to the session's commit
// queue, merge log, and disk flusher. The resolver runs under the
// session's background GoroutineScope (CLAUDE.md tracked-goroutine
// invariant) with a session-scoped context that cancels on Close.
// Idempotent: calling again while a resolver is running is a no-op.
func (s *SessionVFS) startCommitResolver() error {
	if s.commitResolver != nil {
		return nil
	}
	if s.backgroundScope == nil {
		return fmt.Errorf("session vfs: startCommitResolver: backgroundScope not initialized")
	}
	ctx, cancel := context.WithCancel(concurrency.WithScope(context.Background(), s.backgroundScope))
	s.resolverCtx = ctx
	s.resolverCancel = cancel
	s.commitResolver = NewCommitResolver(CommitResolverConfig{
		Session: s,
	})
	if err := s.commitResolver.Start(ctx); err != nil {
		cancel()
		s.resolverCtx = nil
		s.resolverCancel = nil
		s.commitResolver = nil
		return fmt.Errorf("session vfs: start commit resolver: %w", err)
	}
	return nil
}

// recordSessionEpochLocked writes a session-open or session-close
// marker. Caller holds s.mu.
func (s *SessionVFS) recordSessionEpochLocked(kind ControlEntryKind, marker string) error {
	if s.controlWAL == nil {
		return nil
	}
	_, err := s.controlWAL.Append(kind, ControlEntryPayload{EpochMarker: marker})
	return err
}

// recordSessionEpoch is the public (lock-free, caller-managed)
// variant used by NewSessionVFS to write the open marker without
// holding s.mu (no other goroutine can see s yet).
func (s *SessionVFS) recordSessionEpoch(kind ControlEntryKind, marker string) error {
	return s.recordSessionEpochLocked(kind, marker)
}

// Clock returns the session's open-wall-time accumulator. SLA
// trackers use this as the timebase for deadlines that must not
// advance while the session is closed (§8). Returns nil if the
// clock was never initialized (should never happen in production —
// NewSessionVFS always installs one).
func (s *SessionVFS) Clock() *SessionClock {
	if s == nil {
		return nil
	}
	return s.clock
}

// SteeringJournalDir returns the session-scoped directory where
// per-agent steering WAL journals should be opened. Empty when the
// session is in strict-RAM mode (no StorageRoot). Agent
// bootstrapping code (coordinator + audit-replica spawners) uses
// this as the root for steering-ledger durability — crash recovery
// of an in-flight audit replica depends on the steering journal
// being opened under this path so the restart sees the same
// correlation IDs. See docs/PARALLEL_GLOBAL_VFS.md §8 (robustness).
func (s *SessionVFS) SteeringJournalDir() string {
	if s == nil {
		return ""
	}
	return s.steeringJournalPath
}

// steeringJournalDir derives the session-scoped steering-journal
// directory from the session's storage root. Returns empty when the
// session runs in strict-RAM mode.
func steeringJournalDir(storageRoot string) string {
	storageRoot = strings.TrimSpace(storageRoot)
	if storageRoot == "" {
		return ""
	}
	return filepath.Join(storageRoot, "steering-journals")
}

// pipelineDispatchHolderID returns the retention-holder identifier
// used by BeginPipeline when pinning a Copy for a BaseCopyVersion-
// dispatched pipeline. Namespaced so it cannot collide with the
// coordinator's per-replica retention holders or any other Copy
// holder in the session. Released by releasePipelineCopyHold at
// every pipeline-close site (successful merge, failed merge,
// rollback, session close). See docs/PARALLEL_GLOBAL_VFS.md §5
// release criteria #3.
func pipelineDispatchHolderID(pipelineID string) string {
	return "pipeline-dispatch:" + strings.TrimSpace(pipelineID)
}

// releasePipelineCopyHold drops the BaseCopyVersion retention this
// pipeline acquired in BeginPipeline. Safe to call even when the
// pipeline was not dispatched against a Copy (pure current-green
// dispatch) — Release is a no-op for unknown holders. Always called
// from the pipeline-close paths so retention refcounts don't leak.
func (s *SessionVFS) releasePipelineCopyHold(pipelineID string) {
	if s == nil || s.copyRetention == nil {
		return
	}
	if err := s.copyRetention.Release(pipelineDispatchHolderID(pipelineID)); err != nil {
		slog.Default().Error("session vfs: release pipeline copy hold failed",
			"session_id", string(s.sessionID),
			"pipeline_id", pipelineID,
			"error", err.Error(),
		)
	}
}

// CommitQueueDepth returns the number of non-terminal entries in the
// commit queue. Used by the dispatch-gate backpressure policy and by
// observability surfaces.
func (s *SessionVFS) CommitQueueDepth() int {
	if s == nil || s.commitQueue == nil {
		return 0
	}
	return s.commitQueue.DepthNonTerminal()
}

// CopyRetentionSnapshot exposes the current retention topology for
// observability and for the commit-resolver's water-line-advance
// step.
func (s *SessionVFS) CopyRetentionSnapshot() CopyRetentionSnapshot {
	if s == nil || s.copyRetention == nil {
		return CopyRetentionSnapshot{}
	}
	return s.copyRetention.Snapshot()
}

// ReplicaLifecycleLog returns the replica lifecycle log. Orchestrator
// dispatch uses this to record Spawned/Decided/Crashed transitions;
// resume logic reads InFlight() to re-dispatch interrupted replicas.
// Returns nil when the session is in strict-RAM mode.
func (s *SessionVFS) ReplicaLifecycleLog() *ReplicaLifecycleLog {
	if s == nil {
		return nil
	}
	return s.replicaLifecycleLog
}

// CopyRetention exposes the session's Copy retention tracker. Used
// by replica spawn and pipeline dispatch to retain the audit-base
// Copy until the replica emits a decision / the pipeline merges.
func (s *SessionVFS) CopyRetention() *CopyRetention {
	if s == nil {
		return nil
	}
	return s.copyRetention
}

// BeginAuditReplicaVFS constructs a new ReplicaVFS for the given
// merge. The VFS:
//   - Carries a deterministic replicaID derived from the session
//     ID + merged version (docs/PARALLEL_GLOBAL_VFS.md §3.3).
//   - Chains through the previous merge's ReplicaVFS as its parent,
//     if one exists and is still tracked. Otherwise parent is nil
//     and reads fall directly to the session's disk-base.
//   - Is WAL-backed for overlay writes/deletes/seal/abandon.
//   - Is tracked by the session for lookup and replay.
//
// Called by MergeAuditCoordinator on merge callback fire (direct
// method call; no bus). Both the inspector and tester audit replicas
// for this merge share the returned VFS.
func (s *SessionVFS) BeginAuditReplicaVFS(mergedVersion SemanticVersion, replicaID string) (*ReplicaVFS, error) {
	if s == nil {
		return nil, fmt.Errorf("session vfs: BeginAuditReplicaVFS: nil receiver")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrVFSClosed
	}
	if s.replicaVFSByVersion == nil {
		s.replicaVFSByVersion = make(map[SemanticVersion]*ReplicaVFS)
	}
	if existing, ok := s.replicaVFSByVersion[mergedVersion]; ok {
		return existing, nil
	}

	// Parent lookup: walk the merge log backwards from mergedVersion
	// to find the most recent tracked ReplicaVFS. Skip abandoned
	// layers — they're already chain-bypassed.
	var parent *ReplicaVFS
	if s.merges != nil {
		descs := s.merges.snapshot()
		for i := len(descs) - 1; i >= 0; i-- {
			d := descs[i]
			if d.MergedVersion.Compare(mergedVersion) >= 0 {
				continue
			}
			if r, ok := s.replicaVFSByVersion[d.MergedVersion]; ok && r != nil && !r.IsAbandoned() {
				parent = r
				break
			}
		}
	}

	vfs, err := NewReplicaVFS(ReplicaVFSConfig{
		MergedVersion: mergedVersion,
		ReplicaID:     replicaID,
		Parent:        parent,
		DiskBase:      s.baseFS,
		WAL:           s.controlWAL,
	})
	if err != nil {
		return nil, err
	}
	s.replicaVFSByVersion[mergedVersion] = vfs
	return vfs, nil
}

// LookupAuditReplicaVFS returns the ReplicaVFS for a merged version,
// or nil if none is tracked. Used by the coordinator to obtain the
// handle it passes to spawners + for observability.
func (s *SessionVFS) LookupAuditReplicaVFS(mergedVersion SemanticVersion) *ReplicaVFS {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replicaVFSByVersion[mergedVersion]
}

// ReleaseAuditReplicaVFS drops the session's reference to a
// ReplicaVFS once its parent chain is no longer needed downstream.
// Called by the commit resolver after the corresponding merge's
// disk flush + water-line advance. Safe to call multiple times.
func (s *SessionVFS) ReleaseAuditReplicaVFS(mergedVersion SemanticVersion) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.replicaVFSByVersion, mergedVersion)
}

// releaseAuditReplicaVFSBatch drops in-RAM handles for every
// released MergedVersion. Called by the CopyRetention OnRelease
// callback whenever the water line advances past one or more Copy
// versions (docs/PARALLEL_GLOBAL_VFS.md §5). Also abandons any
// rejected-but-not-superseded ReplicaVFS so downstream chain walks
// bypass it cleanly.
func (s *SessionVFS) releaseAuditReplicaVFSBatch(released []SemanticVersion) {
	if s == nil || len(released) == 0 {
		return
	}
	type pair struct {
		ver SemanticVersion
		vfs *ReplicaVFS
	}
	s.mu.Lock()
	toRelease := make([]pair, 0, len(released))
	for _, ver := range released {
		if vfs, ok := s.replicaVFSByVersion[ver]; ok && vfs != nil {
			toRelease = append(toRelease, pair{ver: ver, vfs: vfs})
			delete(s.replicaVFSByVersion, ver)
		}
	}
	archiver := s.forensicArchiver
	sessionID := s.sessionID
	s.mu.Unlock()

	// Forensic archive (§5.2). Fires BEFORE Abandon so the archiver
	// can read the sealed diff if present. Archive failures are
	// logged but do not block release — a blocked release would
	// stall the water line and corrupt the retention invariant.
	if archiver != nil {
		now := time.Now().UTC()
		for _, p := range toRelease {
			info := ForensicReleaseInfo{
				SessionID:      sessionID,
				MergedVersion:  p.ver,
				ReleasedAt:     now,
				Sealed:         p.vfs.IsSealed(),
				Abandoned:      p.vfs.IsAbandoned(),
				ChainReadPaths: p.vfs.ChainReads(),
			}
			if p.vfs.IsSealed() {
				info.SealedDiff = p.vfs.sealedDiff
			}
			if err := archiver.ArchiveReleased(info); err != nil {
				slog.Default().Error("session vfs: forensic archive failed",
					"session_id", string(sessionID),
					"merged_version", p.ver.String(),
					"sealed", info.Sealed,
					"abandoned", info.Abandoned,
					"error", err.Error(),
				)
			}
		}
	}
	// Abandon outside the lock is safe by construction:
	//   - Map mutations (the delete above) are serialized under s.mu,
	//     so concurrent BeginAuditReplicaVFS / LookupAuditReplicaVFS
	//     never observes a partial release.
	//   - ReplicaVFS.IsSealed / IsAbandoned are atomic.Bool reads;
	//     child replicas that still hold the released instance as a
	//     parent pointer observe its abandoned state atomically and
	//     fall through to the parent-of-parent via readThroughChain's
	//     bypass (docs/PARALLEL_GLOBAL_VFS.md §3.8).
	//   - The Abandon operation itself is idempotent and guarded by
	//     the VFS's internal mutex, so concurrent readers don't see
	//     torn writes.
	// Holding s.mu across Abandon would just serialize the release
	// with other session operations unnecessarily.
	for _, p := range toRelease {
		if p.vfs.IsSealed() || p.vfs.IsAbandoned() {
			continue
		}
		if err := p.vfs.Abandon(); err != nil {
			// The only failure mode of Abandon is "already sealed"
			// (which our IsSealed check above filters), so a non-nil
			// err here is unexpected. Log at Error so operators see
			// inconsistency in state transitions.
			s.logAbandonFailure(p.vfs, err)
		}
	}
}

// logAbandonFailure centralises the error-path logging for release
// abandons so they're diagnosable. Uses the package slog.Default
// until SessionVFS grows a dedicated logger field (session operators
// currently rely on the structured log pattern enforced here).
func (s *SessionVFS) logAbandonFailure(vfs *ReplicaVFS, err error) {
	slog.Default().Error("session vfs: release abandon failed",
		"session_id", string(s.sessionID),
		"error", err.Error(),
		"sealed", vfs.IsSealed(),
	)
}

// SubmitAuditAddendum takes a sealed ReplicaVFS for mergedVersion
// and turns its diff into an audit-addendum commit-queue entry.
// Per docs/PARALLEL_GLOBAL_VFS.md §3.7:
//
//   - The sealed diff represents both inspector + tester writes
//     (linter/formatter output, new tests, harness adjustments) —
//     all part of the committable merged work.
//   - We construct a synthetic MergeDescriptor with AuditAddendum=
//     true, BaseVersion = the original merge's MergedVersion,
//     MergedVersion = a fresh minor bump beyond current green.
//   - The descriptor enqueues on the commit queue. Since its
//     AuditAddendum flag is set, the coordinator skips replica
//     spawn and marks it accepted immediately. The commit resolver
//     flushes it to disk in arrival order.
//
// If the ReplicaVFS has no writes and no deletes, this is a no-op
// (audit touched nothing — nothing to merge).
func (s *SessionVFS) SubmitAuditAddendum(mergedVersion SemanticVersion) error {
	if s == nil {
		return fmt.Errorf("session vfs: SubmitAuditAddendum: nil receiver")
	}
	s.mu.Lock()
	vfs := s.replicaVFSByVersion[mergedVersion]
	if vfs == nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if !vfs.IsSealed() {
		return fmt.Errorf("session vfs: SubmitAuditAddendum: replica vfs for %s not sealed", mergedVersion.String())
	}
	diff := vfs.sealedDiff
	if diff == nil || (len(diff.Writes) == 0 && len(diff.Deletes) == 0) {
		// No audit-authored content to submit.
		return nil
	}

	// Convert the sealed ReplicaVFSDiff into FileModification form so
	// the addendum can be routed through MergePipe.Merge — the same
	// OT path every pipeline merge flows through. Per
	// docs/PARALLEL_GLOBAL_VFS.md §3.7: addendum writes are submitted
	// as a pipeline-equivalent changeset that MergePipe OT-transforms
	// against current green. This keeps "every merge goes through
	// MergePipe" invariant (§3.4) regardless of the merge's origin.
	now := time.Now().UTC()
	mods := make([]FileModification, 0, len(diff.Writes)+len(diff.Deletes))
	for path, content := range diff.Writes {
		mods = append(mods, FileModification{
			OriginalPath: path,
			Operation:    FileOpModify,
			Persistence:  FilePersistenceDurable,
			Timestamp:    now,
			NewContent:   append([]byte(nil), content...),
		})
	}
	for _, path := range diff.Deletes {
		mods = append(mods, FileModification{
			OriginalPath: path,
			Operation:    FileOpDelete,
			Persistence:  FilePersistenceDurable,
			Timestamp:    now,
		})
	}

	// Pipeline ID convention: "audit-addendum:<merged_version>" so
	// observability can distinguish audit-authored merges from
	// pipeline-authored ones by prefix.
	addendumPipelineID := "audit-addendum:" + mergedVersion.String()

	// Register the addendum as a pipeline on MergePipe so Merge
	// accepts it. Base version is the addendum's parent (the merge it
	// accompanies); MergePipe OT-transforms against any green
	// advancement that happened since. Unregister before returning so
	// the pipeline registration doesn't leak.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrVFSClosed
	}
	mergePipe := s.mergePipe
	baseForAddendum := mergedVersion
	s.mu.Unlock()

	if err := mergePipe.RegisterPipelineAt(addendumPipelineID, baseForAddendum); err != nil && !errors.Is(err, ErrMergePipelineExists) {
		return fmt.Errorf("session vfs: register audit addendum pipeline: %w", err)
	}
	addendumVersion, mergeErr := mergePipe.Merge(contextWithoutCancel(context.Background()), addendumPipelineID, mods)
	mergePipe.UnregisterPipeline(addendumPipelineID)
	if mergeErr != nil {
		return fmt.Errorf("session vfs: merge audit addendum: %w", mergeErr)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrVFSClosed
	}

	// Gather touched paths deterministically for the descriptor.
	paths := make([]string, 0, len(mods))
	for _, m := range mods {
		paths = append(paths, m.OriginalPath)
	}

	addendumDesc := MergeDescriptor{
		PipelineID:    addendumPipelineID,
		BaseVersion:   baseForAddendum,
		MergedVersion: addendumVersion,
		Paths:         paths,
		PathCount:     len(paths),
		MergedAt:      time.Now().UTC(),
		AuditAddendum: true,
	}
	s.merges.record(addendumDesc)

	if s.commitQueue != nil {
		entry, err := s.commitQueue.Enqueue(addendumDesc)
		if err != nil {
			return fmt.Errorf("session vfs: enqueue audit addendum: %w", err)
		}
		// Audit-addendum merges are pre-audited (their writes came
		// from replicas whose verdict is already recorded). The
		// queue skips straight from Auditing to Accepted. Observers
		// (TUI, coordinator metrics) see the addendum land via the
		// merge-callback fire below; the coordinator's onMerge checks
		// the AuditAddendum flag and does not spawn replicas.
		if entry != nil && entry.State == CommitStateAuditing {
			if err := s.commitQueue.MarkAccepted(addendumVersion, addendumPipelineID); err != nil {
				return fmt.Errorf("session vfs: mark audit addendum accepted: %w", err)
			}
		}
	}

	// Fire callbacks outside the lock so callbacks that re-enter the
	// session don't deadlock. The deferred re-lock restores the
	// deferred-unlock invariant.
	s.mu.Unlock()
	s.fireMergeCallbacks(addendumDesc)
	s.mu.Lock()

	return nil
}

// RegisterMergeCallback attaches a callback invoked synchronously
// from MergePipelineIntoGreen for every merge that completes. The
// audit coordinator registers here at session open; no orchestrator
// involvement. Callbacks run in registration order. Safe for
// concurrent registration.
func (s *SessionVFS) RegisterMergeCallback(cb MergeCallback) {
	if s == nil || cb == nil {
		return
	}
	s.mergeCallbackMu.Lock()
	defer s.mergeCallbackMu.Unlock()
	s.mergeCallbacks = append(s.mergeCallbacks, cb)
}

// fireMergeCallbacks invokes every registered callback with the
// given descriptor. Called synchronously from inside
// MergePipelineIntoGreen after the commit-queue Enqueue succeeds.
// Panics in a callback are contained — the session's core state
// should never be corrupted by a registered observer — but they are
// NOT silently swallowed: each panic is logged with the callback
// index and the descriptor so operators can diagnose the
// misbehaving observer.
func (s *SessionVFS) fireMergeCallbacks(desc MergeDescriptor) {
	s.mergeCallbackMu.Lock()
	cbs := make([]MergeCallback, len(s.mergeCallbacks))
	copy(cbs, s.mergeCallbacks)
	s.mergeCallbackMu.Unlock()
	for i, cb := range cbs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Default().Error("session vfs: merge callback panicked — isolated",
						"session_id", string(s.sessionID),
						"merged_version", desc.MergedVersion.String(),
						"pipeline_id", desc.PipelineID,
						"callback_index", i,
						"panic", fmt.Sprintf("%v", r),
					)
				}
			}()
			cb(desc)
		}()
	}
}

// knownCopyVersions returns every MergedVersion the session currently
// knows about — committed or in-flight. Used by the retention GC
// pass to determine which versions are releasable below the water
// line. Includes both resolved and still-queued descriptors so an
// in-flight Copy cannot be silently GC'd.
func (s *SessionVFS) knownCopyVersions() []SemanticVersion {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[SemanticVersion]struct{})
	if s.merges != nil {
		for _, d := range s.merges.snapshot() {
			seen[d.MergedVersion] = struct{}{}
		}
	}
	if s.commitQueue != nil {
		for _, e := range s.commitQueue.Snapshot() {
			seen[e.Descriptor.MergedVersion] = struct{}{}
		}
	}
	versions := make([]SemanticVersion, 0, len(seen))
	for v := range seen {
		versions = append(versions, v)
	}
	return versions
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
