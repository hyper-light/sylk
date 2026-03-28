package boot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

// PipelineConfig controls the boot pipeline behavior.
type PipelineConfig struct {
	ProjectRoot  string
	Workers      int // Unused after rewrite; kept for API compat.
	BatchSize    int // Unused after rewrite; kept for API compat.
	EmbedWorkers int // Unused after rewrite; kept for API compat.
	ForceTier    *embedder.ModelTier
	OnProgress   func(phase string, current, total int64)
	Logger       *agentlog.BootEventLogger
	Scope        *concurrency.GoroutineScope
}

func (c *PipelineConfig) withDefaults() *PipelineConfig {
	return c
}

// PipelineResult contains the outcome of a boot operation.
type PipelineResult struct {
	ProjectRoot      string
	IsGitRepo        bool
	IsIncremental    bool
	FilesProcessed   int64
	NodesCreated     int64
	EdgesCreated     int64
	DocsCreated      int64
	VectorsCreated   int64
	ChunkRefsCreated int64
	Duration         time.Duration
	EmbedderSource   string
	CommitVersion    string
	CommitID         string
	Phases           PhaseTiming

	// BackgroundIndexer is non-nil when Bleve indexing was deferred.
	// The caller can Wait() on it, use Ready() to check completion, or
	// call PromoteDocs() for on-demand query-time indexing.
	BackgroundIndexer *sylkdir.BackgroundIndexer

	// BackgroundHasher is non-nil when file hashing was deferred.
	// Hashes are only needed for next boot's change detection.
	// The caller can Wait() to ensure hashes are persisted.
	BackgroundHasher *BackgroundHasher
}

// PhaseTiming records wall-clock duration of each boot phase.
type PhaseTiming struct {
	Setup    time.Duration
	Detect   time.Duration
	Allocate time.Duration
	Ingest   time.Duration
	Commit   time.Duration
	Finalize time.Duration
}

// Pipeline orchestrates the boot process: setup → detect → allocate → ingest → commit → finalize.
type Pipeline struct {
	config   *PipelineConfig
	embedder embedder.Embedder
	source   string
	logger   *agentlog.BootEventLogger

	errOnce sync.Once
	err     error
	cancel  context.CancelFunc
}

// NewPipeline creates a boot pipeline with the specified configuration.
// Initializes the embedder; all other resources are created during Run.
func NewPipeline(ctx context.Context, config PipelineConfig) (*Pipeline, error) {
	cfg := config.withDefaults()

	result, err := embedder.NewEmbedder(ctx, embedder.FactoryConfig{
		ForceTier: cfg.ForceTier,
	})
	if err != nil {
		return nil, fmt.Errorf("create embedder: %w", err)
	}

	return &Pipeline{
		config:   cfg,
		embedder: result.Embedder,
		source:   result.Source,
		logger:   cfg.Logger,
	}, nil
}

// Run executes the six-phase boot pipeline:
//
//	Phase 1: Setup     — SylkDir + Lock + GlobalMeta + CanonicalIndex + CommitWAL
//	Phase 2: Detect    — Change detection; skip if nothing changed
//	Phase 3: Allocate  — AllocateSessionID + AllocateNodeIDs + Create Session
//	Phase 4: Ingest    — SessionIngestion.IngestDirectoryWithConfig
//	Phase 5: Commit    — CommitToGlobal with ScopeRoots
//	Phase 6: Finalize  — Update + Save Meta
func (p *Pipeline) Run(ctx context.Context) (*PipelineResult, error) {
	ctx, p.cancel = context.WithCancel(ctx)
	defer p.cancel()

	start := time.Now()
	result := &PipelineResult{
		ProjectRoot:    p.config.ProjectRoot,
		EmbedderSource: p.source,
	}

	projectRoot, err := filepath.Abs(p.config.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	isGitRepo := false
	if gitRoot, err := FindGitRoot(projectRoot); err == nil {
		projectRoot = gitRoot
		isGitRepo = true
	}
	result.ProjectRoot = projectRoot
	result.IsGitRepo = isGitRepo
	result.IsIncremental = MetaExists(projectRoot)

	p.logBootEvent(agentlog.EventBootStarted, "info", &agentlog.BootPhasePayload{
		Phase:       "started",
		ProjectRoot: projectRoot,
		IsIncr:      result.IsIncremental,
	})

	// Phase 1: Setup
	setupStart := time.Now()
	sd, gm, canonIdx, commitWAL, err := p.setupSylkDir(projectRoot)
	if err != nil {
		p.logBootEvent(agentlog.EventBootError, "error", &agentlog.BootPhasePayload{Phase: "setup", Err: err.Error()})
		return nil, fmt.Errorf("setup: %w", err)
	}
	defer sd.Unlock()
	defer commitWAL.Close()
	result.Phases.Setup = time.Since(setupStart)
	p.logBootPhase(agentlog.EventBootSetup, "setup", result.Phases.Setup, nil)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 2: Change detection
	detectStart := time.Now()
	meta, changes, skip := p.detectChanges(ctx, projectRoot, isGitRepo)
	result.Phases.Detect = time.Since(detectStart)
	p.logBootPhase(agentlog.EventBootDetect, "detect", result.Phases.Detect, nil)

	if skip {
		result.BackgroundHasher = completedHasher()
		result.Duration = time.Since(start)
		p.logBootEvent(agentlog.EventBootSkipped, "info", &agentlog.BootPhasePayload{
			Phase: "skipped",
			DurNs: result.Duration.Nanoseconds(),
		})
		return result, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.reportProgress("setup", 1, 6)

	// Phase 3: Allocate session + node IDs
	allocStart := time.Now()
	sess, err := p.createSession(ctx, sd, gm, projectRoot, changes)
	if err != nil {
		p.logBootEvent(agentlog.EventBootError, "error", &agentlog.BootPhasePayload{Phase: "allocate", Err: err.Error()})
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer sess.Close()
	result.Phases.Allocate = time.Since(allocStart)
	p.logBootPhase(agentlog.EventBootAllocate, "allocate", result.Phases.Allocate, nil)

	// Bind logger to session — drains staging into session-scoped log.
	if p.logger != nil {
		_ = p.logger.BindSession(sd.SessionPath(sess.Meta.StringID), sess.Meta.StringID)
	}

	p.reportProgress("allocate", 2, 6)

	// Close session BleveStore — Bleve indexing is deferred to a
	// BackgroundIndexer created during commit. G3 in writeEntities
	// no-ops when BleveStore is nil, eliminating the ~25s session
	// Bleve indexing from the critical boot path.
	_ = sess.CloseBleve()
	sess.BleveStore = nil

	// Activate PendingMode: entities bypass session disk stores and go
	// directly to CommitToGlobal via in-memory Pending* fields.
	// Non-nil (even empty) PendingNodes is the sentinel.
	sess.PendingNodes = make([]*sylkdir.Node, 0)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 4: Ingest via SessionIngestion
	ingestStart := time.Now()
	ingestResult, err := p.ingest(ctx, sess, sd, projectRoot, changes)
	if err != nil {
		p.logBootEvent(agentlog.EventBootError, "error", &agentlog.BootPhasePayload{Phase: "ingest", Err: err.Error()})
		return nil, fmt.Errorf("ingest: %w", err)
	}
	result.Phases.Ingest = time.Since(ingestStart)
	p.logBootPhase(agentlog.EventBootIngest, "ingest", result.Phases.Ingest, nil)

	p.reportProgress("ingest", 4, 6)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 5: Commit to global
	commitStart := time.Now()
	commitResult, err := p.commitToGlobal(ctx, sess, sd, gm, canonIdx, commitWAL, projectRoot, changes)
	if err != nil {
		p.logBootEvent(agentlog.EventBootError, "error", &agentlog.BootPhasePayload{Phase: "commit", Err: err.Error()})
		return nil, fmt.Errorf("commit: %w", err)
	}
	result.Phases.Commit = time.Since(commitStart)
	result.BackgroundIndexer = commitResult.BackgroundIndexer
	p.logBootPhase(agentlog.EventBootCommit, "commit", result.Phases.Commit, nil)

	p.reportProgress("commit", 5, 6)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 6: Finalize meta
	finalizeStart := time.Now()
	p.finalizeMeta(projectRoot, isGitRepo, meta, ingestResult, commitResult, result)
	if err := meta.Save(projectRoot); err != nil {
		p.logBootEvent(agentlog.EventBootError, "error", &agentlog.BootPhasePayload{Phase: "finalize", Err: err.Error()})
		return nil, fmt.Errorf("save meta: %w", err)
	}
	result.Phases.Finalize = time.Since(finalizeStart)
	p.logBootPhase(agentlog.EventBootFinalize, "finalize", result.Phases.Finalize, nil)

	// File hashing — runs synchronously under the caller's context.
	// Previously an untracked goroutine; now bounded by ctx so
	// the scope worker exits promptly on shutdown.
	p.populateFileHashes(ctx, meta, projectRoot)
	if err := ctx.Err(); err == nil {
		if saveErr := meta.Save(projectRoot); saveErr != nil {
			p.logBootEvent(agentlog.EventBootError, "warn", &agentlog.BootPhasePayload{
				Phase: "background_hash",
				Err:   saveErr.Error(),
			})
		}
	}

	result.BackgroundHasher = completedHasher()
	result.Duration = time.Since(start)

	p.logBootEvent(agentlog.EventBootCompleted, "info", &agentlog.BootResultPayload{
		Files:    result.FilesProcessed,
		Nodes:    result.NodesCreated,
		Edges:    result.EdgesCreated,
		Docs:     result.DocsCreated,
		Vectors:  result.VectorsCreated,
		DurNs:    result.Duration.Nanoseconds(),
		Embedder: result.EmbedderSource,
		Version:  result.CommitVersion,
	})

	p.reportProgress("done", 6, 6)
	return result, nil
}

// setupSylkDir initializes the .sylk directory, acquires the process lock,
// and opens GlobalMeta, CanonicalIndex, and CommitWAL.
func (p *Pipeline) setupSylkDir(root string) (*sylkdir.SylkDir, *sylkdir.GlobalMeta, *sylkdir.CanonicalKeyIndex, *sylkdir.CommitWAL, error) {
	sd := sylkdir.New(root)
	if err := sd.Init(); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("init sylkdir: %w", err)
	}

	if err := sd.Lock(); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("lock sylkdir: %w", err)
	}

	gm := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("load global meta: %w", err)
	}

	canonIdx := sylkdir.NewCanonicalKeyIndexFromSylkDir(sd)
	if err := canonIdx.Init(); err != nil {
		sd.Unlock()
		return nil, nil, nil, nil, fmt.Errorf("init canonical index: %w", err)
	}

	commitWAL, err := sylkdir.OpenCommitWAL(sylkdir.CommitWALConfig{
		Dir:         sd.CommitWALPath(),
		SyncOnWrite: true,
	})
	if err != nil {
		sd.Unlock()
		return nil, nil, nil, nil, fmt.Errorf("open commit wal: %w", err)
	}

	if _, err := sylkdir.RunRecovery(sylkdir.RecoveryConfig{
		SylkDir:        sd,
		GlobalMeta:     gm,
		CanonicalIndex: canonIdx,
		CommitWAL:      commitWAL,
	}); err != nil {
		_ = commitWAL.Close()
		sd.Unlock()
		return nil, nil, nil, nil, fmt.Errorf("run recovery: %w", err)
	}

	return sd, gm, canonIdx, commitWAL, nil
}

// detectChanges decides whether the pipeline can skip ingestion entirely.
// Returns the Meta, the list of file changes (nil on cold start), and whether to skip.
// When changes is nil, the caller should run full ingestion.
// When changes is non-nil but empty, the caller should skip.
// When changes is non-nil and non-empty, the caller should run delta ingestion.
func (p *Pipeline) detectChanges(ctx context.Context, root string, isGitRepo bool) (*Meta, []FileChange, bool) {
	if !MetaExists(root) {
		return NewMeta(isGitRepo, p.source), nil, false // nil = cold start
	}

	meta, err := LoadMeta(root)
	if err != nil {
		return NewMeta(isGitRepo, p.source), nil, false
	}

	detector := NewChangeDetector(root, meta, isGitRepo)
	fileList, err := ingestion.DiscoverFiles(ctx, root, nil, nil)
	if err != nil {
		return meta, nil, false // Can't detect changes; run full ingestion
	}

	filePaths := make([]string, len(fileList))
	for i, f := range fileList {
		rel, _ := filepath.Rel(root, f.Path)
		filePaths[i] = rel
	}

	changes, _ := detector.DetectChanges(ctx, filePaths)
	return meta, changes, len(changes) == 0
}

// nodesPerFileEstimate is the average number of nodes per file:
// 1 file node + ~10 symbols + ~20 chunks = ~30.
const nodesPerFileEstimate = 30

// createSession allocates node IDs and a session ID from GlobalMeta,
// then creates a new session with a BaseSnapshot.
// When changes is non-nil (delta), allocates IDs only for changed files.
func (p *Pipeline) createSession(ctx context.Context, sd *sylkdir.SylkDir, gm *sylkdir.GlobalMeta, root string, changes []FileChange) (*sylkdir.Session, error) {
	fileCount := p.estimateFileCount(ctx, root, changes)
	estimated := uint32(fileCount) * nodesPerFileEstimate
	if estimated == 0 {
		estimated = nodesPerFileEstimate // Minimum allocation
	}

	startID, err := gm.AllocateNodeIDs(estimated)
	if err != nil {
		return nil, fmt.Errorf("allocate node IDs: %w", err)
	}

	sessionID, err := gm.AllocateSessionID()
	if err != nil {
		return nil, fmt.Errorf("allocate session ID: %w", err)
	}

	sessStore := sylkdir.NewSessionStore(sd)
	sess, err := sessStore.Create(sessionID, &sylkdir.BaseSnapshot{
		GlobalVersion:     gm.GetHead(),
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        startID,
	})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return sess, nil
}

// estimateFileCount returns the number of files to allocate node IDs for.
// On cold start (changes == nil), discovers all files.
// On delta (changes != nil), counts Added + Modified entries.
func (p *Pipeline) estimateFileCount(ctx context.Context, root string, changes []FileChange) int {
	if changes == nil {
		fileList, err := ingestion.DiscoverFiles(ctx, root, nil, nil)
		if err != nil {
			return 1
		}
		return len(fileList)
	}
	count := 0
	for _, c := range changes {
		if c.Type != ChangeDeleted {
			count++
		}
	}
	return max(1, count)
}

// ingest runs the SessionIngestion pipeline: discovery → parse → chunk → embed → write.
// When changes is non-nil (delta), only changed files are read/parsed/aggregated.
func (p *Pipeline) ingest(ctx context.Context, sess *sylkdir.Session, sd *sylkdir.SylkDir, root string, changes []FileChange) (*sylkdir.SessionIngestionResult, error) {
	si := sylkdir.NewSessionIngestion(sess)
	si.SetEmbedder(p.embedder)
	si.SetLogger(p.logger)

	cache := NewParseCache(filepath.Join(sd.CachePath(), "parse"))

	si.SetChunkCache(cache)

	config := &ingestion.Config{
		RootPath:   root,
		Workers:    ingestion.WorkerCount(),
		ParseCache: cache,
	}
	if changes != nil {
		config.IncludePaths = changesToAbsPaths(root, changes)
	}
	return si.IngestDirectoryWithConfig(ctx, config)
}

// changesToAbsPaths converts FileChange entries to a set of absolute paths
// for delta ingestion. Deleted files are excluded (nothing to ingest).
func changesToAbsPaths(root string, changes []FileChange) map[string]bool {
	paths := make(map[string]bool, len(changes))
	for _, c := range changes {
		if c.Type == ChangeDeleted {
			continue
		}
		paths[filepath.Join(root, c.Path)] = true
	}
	return paths
}

// commitToGlobal merges the session into the global version stores with orphan detection.
// When changes is nil (cold start), uses ScopeRoots for full scope-based orphan detection.
// When changes is non-nil (delta), uses DeletedPaths for explicit file tombstoning.
func (p *Pipeline) commitToGlobal(
	ctx context.Context,
	sess *sylkdir.Session,
	sd *sylkdir.SylkDir,
	gm *sylkdir.GlobalMeta,
	canonIdx *sylkdir.CanonicalKeyIndex,
	commitWAL *sylkdir.CommitWAL,
	root string,
	changes []FileChange,
) (*sylkdir.CommitResult, error) {
	bleveStore := sylkdir.NewGlobalVersionBleveStore(sd, gm.GetHead())

	cfg := sylkdir.CommitConfig{
		Session:            sess,
		SylkDir:            sd,
		GlobalMeta:         gm,
		CanonicalIndex:     canonIdx,
		GlobalBleveStore:   bleveStore,
		CommitWAL:          commitWAL,
		DeferBleveIndexing: true,
		Scope:              p.config.Scope,
		Logger:             p.logger,
	}
	if changes == nil {
		cfg.ScopeRoots = []string{root}
	} else {
		cfg.DeletedPaths = extractDeletedPaths(root, changes)
	}
	return sylkdir.CommitToGlobal(ctx, cfg)
}

// extractDeletedPaths returns absolute paths of deleted files from the change list.
func extractDeletedPaths(root string, changes []FileChange) []string {
	var deleted []string
	for _, c := range changes {
		if c.Type == ChangeDeleted {
			deleted = append(deleted, filepath.Join(root, c.Path))
		}
	}
	return deleted
}

// finalizeMeta updates Meta and PipelineResult from the ingestion and commit results.
func (p *Pipeline) finalizeMeta(
	root string,
	isGitRepo bool,
	meta *Meta,
	ir *sylkdir.SessionIngestionResult,
	cr *sylkdir.CommitResult,
	result *PipelineResult,
) {
	if isGitRepo {
		detector := NewChangeDetector(root, meta, isGitRepo)
		if commit, err := detector.GetCurrentCommit(context.Background()); err == nil {
			meta.SetCommit(commit)
		}
	}

	meta.UpdateStats(int64(ir.NodesCreated), int64(ir.EdgesCreated), int64(ir.VectorsCreated))

	result.FilesProcessed = int64(ir.FilesProcessed)
	result.NodesCreated = int64(ir.NodesCreated)
	result.EdgesCreated = int64(ir.EdgesCreated)
	result.DocsCreated = int64(ir.DocsCreated)
	result.VectorsCreated = int64(ir.VectorsCreated)
	result.ChunkRefsCreated = int64(ir.ChunkRefsCreated)
	result.CommitVersion = cr.GlobalVersion.String()
	result.CommitID = cr.CommitID
}

// populateFileHashes discovers all project files and records their content hashes
// into Meta for change detection on subsequent boots. File hashing is parallel
// across runtime.NumCPU() workers.
func (p *Pipeline) populateFileHashes(ctx context.Context, meta *Meta, root string) {
	meta.ClearFileHashes()
	fileList, err := ingestion.DiscoverFiles(ctx, root, nil, nil)
	if err != nil {
		return
	}

	// Hash files in parallel, collect results into a local map, then merge.
	type hashEntry struct {
		rel  string
		hash string
	}

	workers := runtime.NumCPU()
	if workers > len(fileList) {
		workers = max(1, len(fileList))
	}
	perWorker := (len(fileList) + workers - 1) / workers
	results := make([][]hashEntry, workers)

	var wg sync.WaitGroup
	for w := range workers {
		lo := w * perWorker
		hi := lo + perWorker
		if hi > len(fileList) {
			hi = len(fileList)
		}
		if lo >= hi {
			continue
		}

		wg.Add(1)
		go func(idx int, batch []ingestion.FileInfo) {
			defer wg.Done()
			local := make([]hashEntry, 0, len(batch))
			for _, f := range batch {
				if ctx.Err() != nil {
					break
				}
				rel, _ := filepath.Rel(root, f.Path)
				if h := hashFileContents(f.Path); h != "" {
					local = append(local, hashEntry{rel: rel, hash: h})
				}
			}
			results[idx] = local
		}(w, fileList[lo:hi])
	}
	wg.Wait()

	for _, batch := range results {
		for _, e := range batch {
			meta.SetFileHash(e.rel, e.hash)
		}
	}
}

// recordFileHash computes the SHA-256 hash of absPath and stores it under relPath in meta.
func recordFileHash(meta *Meta, relPath, absPath string) {
	if hash := hashFileContents(absPath); hash != "" {
		meta.SetFileHash(relPath, hash)
	}
}

// hashFileContents returns "sha256:<hex>" for the file at path, or "" on error.
func hashFileContents(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// BackgroundHasher computes file hashes asynchronously after boot completes.
// Hashes are only needed for the next boot's change detection, so they
// run off the critical path. The caller can Wait() to block until done.
// BackgroundHasher signals when file hashing is complete. After the
// refactor to inline hashing under the caller's context, new instances
// are returned already-done; the type is kept for API compatibility.
type BackgroundHasher struct {
	done chan struct{}
}

// Wait blocks until background hashing completes.
func (bh *BackgroundHasher) Wait() {
	if bh != nil {
		<-bh.done
	}
}

// Done returns a channel that closes when background hashing completes.
func (bh *BackgroundHasher) Done() <-chan struct{} {
	return bh.done
}

// completedHasher returns a BackgroundHasher whose channel is already closed.
func completedHasher() *BackgroundHasher {
	ch := make(chan struct{})
	close(ch)
	return &BackgroundHasher{done: ch}
}

func (p *Pipeline) setError(err error) {
	p.errOnce.Do(func() {
		p.err = err
		if p.cancel != nil {
			p.cancel()
		}
	})
}

func (p *Pipeline) reportProgress(phase string, current, total int64) {
	if p.config.OnProgress != nil {
		p.config.OnProgress(phase, current, total)
	}
}

// logBootEvent emits a structured JSONL entry through the BootEventLogger.
func (p *Pipeline) logBootEvent(eventType agentlog.EventType, level string, data any) {
	if p.logger == nil {
		return
	}
	p.logger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     "boot",
		Event:     eventType.String(),
		EventCode: eventType,
		Data:      data,
	})
}

// logBootPhase emits a phase-timing event.
func (p *Pipeline) logBootPhase(eventType agentlog.EventType, phase string, dur time.Duration, extra func(*agentlog.BootPhasePayload)) {
	payload := &agentlog.BootPhasePayload{
		Phase: phase,
		DurNs: dur.Nanoseconds(),
	}
	if extra != nil {
		extra(payload)
	}
	p.logBootEvent(eventType, "info", payload)
}

// Boot runs the full boot pipeline for the given project root.
func Boot(ctx context.Context, projectRoot string) (*PipelineResult, error) {
	return BootWithConfig(ctx, PipelineConfig{ProjectRoot: projectRoot})
}

// BootWithConfig runs the boot pipeline with custom configuration.
func BootWithConfig(ctx context.Context, config PipelineConfig) (*PipelineResult, error) {
	if config.ProjectRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		config.ProjectRoot = cwd
	}

	pipeline, err := NewPipeline(ctx, config)
	if err != nil {
		return nil, err
	}

	return pipeline.Run(ctx)
}

// ErrAlreadyBooted is returned by BootIfNeeded when the project is already initialized.
var ErrAlreadyBooted = errors.New("sylk already initialized")

// BootIfNeeded runs boot only if the project has not been initialized yet.
func BootIfNeeded(ctx context.Context, projectRoot string) (*PipelineResult, error) {
	root := projectRoot
	if gitRoot, err := FindGitRoot(projectRoot); err == nil {
		root = gitRoot
	}
	if MetaExists(root) {
		return nil, ErrAlreadyBooted
	}
	return Boot(ctx, projectRoot)
}
