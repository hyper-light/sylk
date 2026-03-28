// Package sylkdir provides session-aware storage with commit-to-global merge.
package sylkdir

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/bleve"
	"golang.org/x/sync/errgroup"
)

// CommitConfig holds the dependencies required to commit a session to global.
type CommitConfig struct {
	Session          *Session
	SylkDir          *SylkDir // Required for version directory creation
	GlobalMeta       *GlobalMeta
	CanonicalIndex   *CanonicalKeyIndex
	GlobalBleveStore *GlobalVersionBleveStore // nil means skip Bleve indexing
	CommitWAL        *CommitWAL               // nil means skip commit WAL bracketing

	// Vectors are collected but not written to IVF here.
	// IVF integration is Phase 5.
	// The caller is responsible for indexing vectors after commit.

	// ScopeRoots is the set of root directories this session fully indexed.
	// Any global file key whose path is under a ScopeRoot but NOT in the
	// session's file nodes is considered a deleted file — all its entities
	// are tombstoned. If empty, only entity-level orphan detection runs.
	ScopeRoots []string

	// DeletedPaths lists absolute file paths to tombstone explicitly.
	// Used for delta commits where the session only contains changed files.
	// When set, scope-based detectDeletedFiles is skipped.
	DeletedPaths []string

	// DeferBleveIndexing defers document indexing to a BackgroundIndexer.
	// When true, commit only runs the fast directory-copy snapshot and returns
	// a BackgroundIndexer in CommitResult. The caller is responsible for
	// either waiting on it or letting it run asynchronously.
	// When false (default), inline indexing runs as before.
	DeferBleveIndexing bool

	// Scope optionally tracks deferred background indexing so callers can tie
	// it to a larger lifecycle, such as TUI shutdown.
	Scope *concurrency.GoroutineScope

	// Logger is the structured event logger for commit phases. Nil-safe.
	Logger *agentlog.BootEventLogger
}

// CommitResult describes what the commit produced.
type CommitResult struct {
	SessionID        uint32
	CommitID         string
	PreCommitVersion SemanticVersion // Session version after minor bump
	GlobalVersion    SemanticVersion // Global version after major bump

	NodesMerged    int
	EdgesMerged    int
	VectorsStaged  int // Collected, not yet indexed in IVF
	DocsStaged     int // Collected from session ancestor chain
	ChunksStaged   int // Chunk refs collected from session ancestor chain
	DocsIndexed    int // Actually indexed into Bleve (0 if BleveStore nil)
	DocsSuperseded int // Deleted from Bleve due to node supersession

	DeadNodeCount  uint32  // Total dead nodes in new version (including inherited)
	TombstoneRatio float64 // DeadNodeCount / TotalNodes

	OrphanedKeys int // Keys removed by scope-aware orphan detection
	DeletedFiles int // Files detected as deleted via ScopeRoots

	// Superseded is the set of canonical keys that already existed in global.
	// For each entry: key → (oldGlobalNodeID, newSessionNodeID).
	Superseded map[string][2]uint32

	// StagedVectors holds all vectors from the session for the caller to
	// push into the IVF index after commit returns.
	StagedVectors []*VersionVector

	// StagedDocs holds all documents from the session for the caller to
	// push into the Bleve index after commit returns.
	StagedDocs []*VersionDocument

	// BackgroundIndexer is non-nil when DeferBleveIndexing is true.
	// It indexes docs asynchronously; the caller can Wait() on it or
	// let it run. PromoteDocs enables on-demand query-time indexing.
	BackgroundIndexer *BackgroundIndexer

	Duration time.Duration
}

// ConvertToSearchDocument converts a session VersionDocument into a
// search.Document suitable for Bleve indexing.
func ConvertToSearchDocument(vdoc *VersionDocument) *search.Document {
	content := []byte(vdoc.Content)
	return &search.Document{
		ID:         vdoc.ID,
		Path:       vdoc.Path,
		Type:       search.DocumentType(vdoc.Type),
		Language:   vdoc.Language,
		Content:    vdoc.Content,
		Checksum:   search.GenerateChecksum(content),
		IndexedAt:  time.Unix(0, vdoc.IndexedAt),
		ModifiedAt: time.Unix(0, vdoc.IndexedAt),
	}
}

// CommitToGlobal merges a session's data into the global knowledge graph.
//
// The commit flow:
//  0. Calculate new global version + WAL begin bracket.
//  1. Pre-commit minor checkpoint in session (captures final state).
//  2. Collect all entities from session ancestor chain.
//  3. Create version directory + snapshot parent data.
//  4. Append session nodes/edges/vectors/docs to new version (with supersession).
//  5. Snapshot Bleve into new version and index new docs.
//  6. Update manifest + register commit + WAL end bracket.
//  7. Mark session as committed.
//
// When CommitWAL is non-nil, the commit is bracketed by OpCommitBegin/OpCommitEnd
// entries. On crash recovery, FindIncompleteCommits identifies partial writes.
func CommitToGlobal(ctx context.Context, cfg CommitConfig) (*CommitResult, error) {
	start := time.Now()
	sess := cfg.Session

	if sess.Meta.Status == SessionCommitted {
		return nil, fmt.Errorf("sylkdir: session %d already committed", sess.Meta.ID)
	}

	// ── Step 0: Calculate version + WAL bracket begin ───────────────────
	oldHead := cfg.GlobalMeta.GetHead()
	newVersion, err := cfg.GlobalMeta.AllocateGlobalVersion()
	if err != nil {
		return nil, fmt.Errorf("sylkdir: allocate global version: %w", err)
	}

	if err := logCommitBegin(cfg.CommitWAL, sess.Meta.ID, newVersion); err != nil {
		return nil, err
	}

	// ── Step 1: Pre-commit minor checkpoint ────────────────────────────
	t := time.Now()
	preCommitVer, err := sess.Checkpoint("pre-commit", CheckpointMinor)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: pre-commit checkpoint: %w", err)
	}
	logCommitEvent(cfg.Logger, agentlog.EventCommitStarted, "info", &agentlog.CommitPhasePayload{
		Phase: "checkpoint",
		DurNs: time.Since(t).Nanoseconds(),
	})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── Step 2: Collect all entities from ancestor chain (parallel) ────
	// When Pending* fields are non-nil (PendingMode), entities are already
	// in memory from writeEntities — no disk read needed.
	var (
		nodes     []*Node
		edges     []*Edge
		docs      []*VersionDocument
		vectors   []*VersionVector
		chunkRefs []*ChunkRef
	)
	collectG, _ := errgroup.WithContext(ctx)
	collectG.Go(func() error {
		if sess.PendingNodes != nil {
			nodes = sess.PendingNodes
			return nil
		}
		var readErr error
		nodes, readErr = NewVersionNodeStore(sess).ReadAllFromAncestorChain()
		return readErr
	})
	collectG.Go(func() error {
		if sess.PendingEdges != nil {
			edges = sess.PendingEdges
			return nil
		}
		var readErr error
		edges, readErr = NewVersionEdgeStore(sess).ReadAllFromAncestorChain()
		return readErr
	})
	collectG.Go(func() error {
		if sess.PendingDocs != nil {
			docs = sess.PendingDocs
			return nil
		}
		var readErr error
		docs, readErr = NewVersionDocStore(sess).ReadFromAncestorChain()
		return readErr
	})
	collectG.Go(func() error {
		if sess.PendingVectors != nil {
			vectors = sess.PendingVectors
			return nil
		}
		var readErr error
		vectors, readErr = NewVersionVectorStore(sess).ReadAllFromAncestorChain()
		return readErr
	})
	collectG.Go(func() error {
		if sess.PendingChunkRefs != nil {
			chunkRefs = sess.PendingChunkRefs
			return nil
		}
		chunkRefs, _ = collectSessionChunkRefsFromDisk(sess)
		return nil
	})
	if err := collectG.Wait(); err != nil {
		return nil, fmt.Errorf("sylkdir: collect session entities: %w", err)
	}
	// Release Pending references; entities held by local vars.
	sess.PendingNodes = nil
	sess.PendingEdges = nil
	sess.PendingDocs = nil
	sess.PendingVectors = nil
	sess.PendingChunkRefs = nil
	logCommitEvent(cfg.Logger, agentlog.EventCommitMergeNodes, "info", &agentlog.CommitPhasePayload{
		Phase:       "collect",
		DurNs:       time.Since(t).Nanoseconds(),
		NodesMerged: len(nodes),
		EdgesMerged: len(edges),
		DocsIndexed: len(docs),
	})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── Step 3: Create version directory + snapshot or compact parent data ─
	if err := cfg.SylkDir.CreateGlobalVersion(newVersion); err != nil {
		return nil, fmt.Errorf("sylkdir: create global version %s: %w", newVersion.String(), err)
	}

	// Decide whether to compact (physically remove dead data) or snapshot (copy as-is).
	parentInfo := cfg.GlobalMeta.GetVersionInfo(oldHead)
	var parentDeadCount, parentTotalNodes uint32
	if parentInfo != nil {
		parentTotalNodes = parentInfo.Stats.TotalNodes
		parentDeadCount = parentInfo.Stats.TombstoneCount
	}

	if ShouldCompact(parentDeadCount, parentTotalNodes) {
		if err := cfg.SylkDir.CompactGlobalData(oldHead, newVersion); err != nil {
			return nil, fmt.Errorf("sylkdir: compact global data: %w", err)
		}
	} else {
		if err := cfg.SylkDir.SnapshotGlobalData(oldHead, newVersion); err != nil {
			return nil, fmt.Errorf("sylkdir: snapshot global data: %w", err)
		}
	}

	// Create version stores for the new global version
	globalNodeStore, err := NewGlobalVersionNodeStore(cfg.SylkDir, newVersion)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: open global node store: %w", err)
	}
	defer globalNodeStore.Close()

	globalEdgeStore, err := NewGlobalVersionEdgeStore(cfg.SylkDir, newVersion)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: open global edge store: %w", err)
	}
	defer globalEdgeStore.Close()

	globalDocStore, err := NewGlobalVersionDocStore(cfg.SylkDir, newVersion)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: open global doc store: %w", err)
	}
	defer globalDocStore.Close()

	globalVectorStore, err := NewGlobalVersionVectorStore(cfg.SylkDir, newVersion)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: open global vector store: %w", err)
	}
	defer globalVectorStore.Close()

	logCommitEvent(cfg.Logger, agentlog.EventCommitMergeEdges, "info", &agentlog.CommitPhasePayload{
		Phase: "snapshot_compact",
		DurNs: time.Since(t).Nanoseconds(),
	})

	// ── Steps 5+6: Store writes and Bleve indexing run in parallel ─────
	// Store writes (nodes, edges, docs, vectors) and Bleve indexing write
	// to independent files and can safely overlap.
	chunksStaged := len(chunkRefs)

	// Close session Bleve before copying — ensures a clean snapshot for promotion.
	sessionBlevePath := closeAndGetSessionBlevePath(sess)

	var (
		storeResult    storeWriteResult
		tb             *TombstoneBitmap
		orphanedKeys   int
		deletedFiles   int
		docsIndexed    int
		docsSuperseded int
		bgIndexer      *BackgroundIndexer
	)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	commitG, gctx := errgroup.WithContext(ctx)

	// Path A: Mutation pass → batch store writes → tombstone/orphan.
	commitG.Go(func() error {
		var writeErr error
		storeResult, writeErr = commitWriteEntities(gctx, cfg, sess, newVersion, oldHead,
			nodes, edges, docs, vectors, chunkRefs, chunksStaged,
			globalNodeStore, globalEdgeStore, globalDocStore, globalVectorStore)
		if writeErr != nil {
			return writeErr
		}
		tb, orphanedKeys, deletedFiles, writeErr = commitTombstoneOrphan(cfg, newVersion, storeResult.updates, nodes, parentTotalNodes)
		return writeErr
	})

	// Path B: Bleve snapshot/promote.
	// When DeferBleveIndexing is true, only runs the fast directory-copy
	// snapshot — actual indexing is deferred to a BackgroundIndexer.
	// When false, runs the full inline snapshot + index flow.
	commitG.Go(func() error {
		if cfg.DeferBleveIndexing {
			return snapshotBleveOnly(cfg.GlobalBleveStore, oldHead, newVersion, sessionBlevePath)
		}
		var bleveErr error
		docsIndexed, bleveErr = snapshotAndIndexBleve(gctx, cfg.GlobalBleveStore, oldHead, newVersion, docs, sessionBlevePath)
		return bleveErr
	})

	if err := commitG.Wait(); err != nil {
		return nil, fmt.Errorf("sylkdir: commit parallel: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── Step 6: Publish global commit atomically ───────────────────────
	deadCount := tb.Count()

	// TotalNodes and TotalVectors are derived from the offset index count,
	// which reflects the true global total (inherited parent + session data).
	// TotalEdges and TotalDocs are session-only counts; the inherited
	// per-version files are not re-scanned (acceptable for non-critical stats).
	gv := GlobalVersion{
		ID:         newVersion,
		ParentID:   oldHead,
		SessionID:  sess.Meta.ID,
		SessionVer: preCommitVer,
		CreatedAt:  time.Now().UTC(),
		Stats: GlobalVersionStats{
			TotalNodes:     globalNodeStore.CountForVersion(newVersion),
			TotalEdges:     uint32(len(edges)),
			TotalVectors:   globalVectorStore.CountForVersion(newVersion),
			TotalDocs:      uint32(len(docs)),
			TombstoneCount: deadCount,
		},
	}
	commitID, err := cfg.GlobalMeta.PublishCommit(gv, sess.Meta.ID, preCommitVer)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: publish global commit: %w", err)
	}

	// WAL bracket end — commit is now durable.
	if err := logCommitEnd(cfg.CommitWAL, sess.Meta.ID, newVersion); err != nil {
		return nil, err
	}
	if err := cfg.CanonicalIndex.Save(); err != nil {
		return nil, fmt.Errorf("sylkdir: save canonical index: %w", err)
	}

	// After publication: finalize derived Bleve state.
	if cfg.DeferBleveIndexing {
		bgIndexer = NewBackgroundIndexerWithScope(
			cfg.Scope,
			cfg.GlobalBleveStore,
			docs,
			storeResult.supersededDocIDs,
			func() error { return recordBleveIndexed(cfg.GlobalBleveStore, cfg.GlobalMeta, newVersion) },
		)
		logCommitEvent(cfg.Logger, agentlog.EventCommitBleve, "info", &agentlog.CommitPhasePayload{
			Phase:       "bleve_deferred",
			DocsIndexed: len(docs),
		})
	} else {
		docsSuperseded = deleteSupersededBleveDocs(cfg.GlobalBleveStore, storeResult.supersededDocIDs)
		if err := recordBleveIndexed(cfg.GlobalBleveStore, cfg.GlobalMeta, newVersion); err != nil {
			return nil, err
		}
		logCommitEvent(cfg.Logger, agentlog.EventCommitBleve, "info", &agentlog.CommitPhasePayload{
			Phase:       "bleve_inline",
			DurNs:       time.Since(t).Nanoseconds(),
			DocsIndexed: docsIndexed,
		})
	}

	// ── Step 7: Mark session as committed ──────────────────────────────
	now := time.Now()
	sess.Meta.Status = SessionCommitted
	sess.Meta.CommittedAt = &now

	// Close per-version Bleve — data is now in global.
	if sess.BleveStore != nil {
		if err := sess.BleveStore.CloseAll(); err != nil {
			return nil, fmt.Errorf("sylkdir: close session bleve: %w", err)
		}
		sess.BleveStore = nil
	}

	if err := sess.Save(); err != nil {
		return nil, fmt.Errorf("sylkdir: save session meta: %w", err)
	}

	logCommitEvent(cfg.Logger, agentlog.EventCommitCompleted, "info", &agentlog.CommitPhasePayload{
		Phase:        "completed",
		DurNs:        time.Since(start).Nanoseconds(),
		NodesMerged:  len(nodes),
		EdgesMerged:  len(edges),
		OrphanedKeys: orphanedKeys,
		TombstoneRat: tb.DeadRatio(uint32(len(nodes))),
		Version:      newVersion.String(),
	})

	return &CommitResult{
		SessionID:         sess.Meta.ID,
		CommitID:          commitID,
		PreCommitVersion:  preCommitVer,
		GlobalVersion:     newVersion,
		NodesMerged:       len(nodes),
		EdgesMerged:       len(edges),
		VectorsStaged:     len(vectors),
		DocsStaged:        len(docs),
		ChunksStaged:      chunksStaged,
		DocsIndexed:       docsIndexed,
		DocsSuperseded:    docsSuperseded,
		BackgroundIndexer: bgIndexer,
		DeadNodeCount:     deadCount,
		TombstoneRatio:    tb.DeadRatio(uint32(len(nodes))),
		OrphanedKeys:      orphanedKeys,
		DeletedFiles:      deletedFiles,
		Superseded:        storeResult.superseded,
		StagedVectors:     vectors,
		StagedDocs:        docs,
		Duration:          time.Since(start),
	}, nil
}

// detectOrphans finds canonical keys in the global index that are scoped to
// files the session re-indexed but are not present in the session's output.
// These are entities that were removed (function deleted, etc.) or files that
// were deleted from disk. Returns counts for reporting.
//
// When parentTotalNodes is 0, no previous global data exists and orphan
// detection is skipped (no prior entities can be orphaned).
func detectOrphans(cfg CommitConfig, sessionNodes []*Node, tb *TombstoneBitmap, parentTotalNodes uint32) (orphanedKeys, deletedFiles int) {
	if parentTotalNodes == 0 {
		return 0, 0
	}

	sessionKeys, indexedPaths := buildSessionGroundTruth(sessionNodes)
	orphanedKeys = detectEntityOrphans(cfg, sessionKeys, indexedPaths, tb)

	// Explicit deletions (delta commits) take priority over scope-based detection.
	explicitDeleted, explicitOrphans := tombstoneExplicitDeletions(cfg, tb)
	deletedFiles += explicitDeleted
	orphanedKeys += explicitOrphans

	if len(cfg.DeletedPaths) == 0 {
		scopeDeleted, scopeOrphans := detectDeletedFiles(cfg, indexedPaths, tb)
		deletedFiles += scopeDeleted
		orphanedKeys += scopeOrphans
	}
	return orphanedKeys, deletedFiles
}

// buildSessionGroundTruth extracts canonical keys and indexed file paths from session nodes.
func buildSessionGroundTruth(nodes []*Node) (sessionKeys map[string]bool, indexedPaths map[string]bool) {
	sessionKeys = make(map[string]bool, len(nodes))
	indexedPaths = make(map[string]bool)
	for _, n := range nodes {
		if n.CanonicalKey != "" {
			sessionKeys[n.CanonicalKey] = true
		}
		if NodeType(n.NodeType) == NodeTypeFile && n.Path != "" {
			indexedPaths[n.Path] = true
		}
	}
	return sessionKeys, indexedPaths
}

// detectEntityOrphans finds per-file entity orphans using the path index.
// For each file the session touched, keys in the canonical index that the
// session didn't produce are tombstoned (removed entities).
func detectEntityOrphans(cfg CommitConfig, sessionKeys, indexedPaths map[string]bool, tb *TombstoneBitmap) int {
	orphaned := 0
	for path := range indexedPaths {
		pathKeys := cfg.CanonicalIndex.LookupByPath(path)
		orphaned += tombstoneOrphans(cfg.CanonicalIndex, tb, sessionKeys, pathKeys)
	}
	return orphaned
}

// detectDeletedFiles finds files that were deleted from disk via ScopeRoots.
// Uses AllFilePaths for O(files) enumeration instead of O(keys) prefix scan.
func detectDeletedFiles(cfg CommitConfig, indexedPaths map[string]bool, tb *TombstoneBitmap) (deletedFiles, orphanedKeys int) {
	if len(cfg.ScopeRoots) == 0 {
		return 0, 0
	}

	allFiles := cfg.CanonicalIndex.AllFilePaths()
	for filePath, nodeID := range allFiles {
		if indexedPaths[filePath] || !isUnderAnyRoot(filePath, cfg.ScopeRoots) {
			continue
		}
		tb.MarkDead(nodeID)
		cfg.CanonicalIndex.Delete("file:" + filePath)
		orphanedKeys++
		deletedFiles++

		// Tombstone all symbols under the deleted file.
		pathKeys := cfg.CanonicalIndex.LookupByPath(filePath)
		orphanedKeys += tombstoneOrphans(cfg.CanonicalIndex, tb, nil, pathKeys)
	}
	return deletedFiles, orphanedKeys
}

// tombstoneExplicitDeletions tombstones files listed in DeletedPaths and all their child entities.
// Used for delta commits where the session only contains changed files.
func tombstoneExplicitDeletions(cfg CommitConfig, tb *TombstoneBitmap) (deletedFiles, orphanedKeys int) {
	for _, filePath := range cfg.DeletedPaths {
		nodeID, err := cfg.CanonicalIndex.Lookup("file:" + filePath)
		if err != nil {
			continue
		}
		tb.MarkDead(nodeID)
		cfg.CanonicalIndex.Delete("file:" + filePath)
		orphanedKeys++
		deletedFiles++

		pathKeys := cfg.CanonicalIndex.LookupByPath(filePath)
		orphanedKeys += tombstoneOrphans(cfg.CanonicalIndex, tb, nil, pathKeys)
	}
	return deletedFiles, orphanedKeys
}

// tombstoneOrphans marks nodes as dead and removes their canonical keys.
// A key is orphaned if it exists in globalKeys but not in sessionKeys.
// If sessionKeys is nil, all globalKeys are treated as orphaned.
func tombstoneOrphans(idx *CanonicalKeyIndex, tb *TombstoneBitmap, sessionKeys map[string]bool, globalKeys map[string]uint32) int {
	orphaned := 0
	for key, nodeID := range globalKeys {
		if sessionKeys != nil && sessionKeys[key] {
			continue
		}
		tb.MarkDead(nodeID)
		idx.Delete(key)
		orphaned++
	}
	return orphaned
}

// isUnderAnyRoot returns true if filePath starts with any of the root prefixes.
func isUnderAnyRoot(filePath string, roots []string) bool {
	for _, root := range roots {
		if strings.HasPrefix(filePath, root) {
			return true
		}
	}
	return false
}

// collectSessionChunkRefsFromDisk reads all chunk refs from the session's ancestor chain.
func collectSessionChunkRefsFromDisk(sess *Session) ([]*ChunkRef, int) {
	if sess.ChunkDataFile == nil {
		return nil, 0
	}
	store := NewChunkRefStore(sess)
	refs, err := store.ReadAllFromAncestorChain()
	if err != nil {
		return nil, 0
	}
	return refs, len(refs)
}

// NewGlobalChunkRefStore creates a ChunkRefStore for a global version.
// It opens the global chunk data file and creates the store.
func NewGlobalChunkRefStore(sd *SylkDir, version SemanticVersion) (*ChunkRefStore, error) {
	dataPath := sd.GlobalChunkDataPath()
	chunkDataFile, err := OpenSharedDataFile(dataPath)
	if err != nil {
		return nil, fmt.Errorf("open global chunk data: %w", err)
	}
	globalSess := &Session{
		path:          sd.GlobalPath(),
		Manifest:      &VersionManifest{Head: version},
		ChunkDataFile: chunkDataFile,
	}
	return NewChunkRefStore(globalSess), nil
}

// ── Commit step helpers ────────────────────────────────────────────────────

// supersessionUpdate tracks a single supersession for deferred old-node update.
type supersessionUpdate struct {
	oldID uint32
	newID uint32
}

// prepareNodeMutations runs the canonical-index check and DocRef remap pass
// over all session nodes. This mutates nodes in place (Supersedes, DocRef)
// without performing any I/O, so subsequent batch writes see correct values.
func prepareNodeMutations(cfg CommitConfig, nodes []*Node, globalDocIDMap *DocIDMap) (superseded map[string][2]uint32, updates []supersessionUpdate) {
	superseded = make(map[string][2]uint32)
	sessDocIDMap := cfg.Session.DocIDMap

	for _, node := range nodes {
		if node.CanonicalKey != "" {
			updates = detectSupersession(cfg, node, superseded, updates)
		}
		remapDocRef(node, sessDocIDMap, globalDocIDMap)
	}
	return superseded, updates
}

// detectSupersession checks if a node's canonical key already exists in the index.
// If so, records the supersession. Mutates node.Supersedes in place.
func detectSupersession(cfg CommitConfig, node *Node, superseded map[string][2]uint32, updates []supersessionUpdate) []supersessionUpdate {
	existingID, wasSet := cfg.CanonicalIndex.SetIfNotExists(node.CanonicalKey, node.ID)
	if wasSet {
		return updates
	}
	cfg.CanonicalIndex.Set(node.CanonicalKey, node.ID)
	superseded[node.CanonicalKey] = [2]uint32{existingID, node.ID}
	node.Supersedes = existingID
	return append(updates, supersessionUpdate{oldID: existingID, newID: node.ID})
}

// remapDocRef translates a node's DocRef from session to global address space.
func remapDocRef(node *Node, sessDocIDMap, globalDocIDMap *DocIDMap) {
	if node.DocRef == 0 || sessDocIDMap == nil || globalDocIDMap == nil {
		return
	}
	if docStringID := sessDocIDMap.Reverse(node.DocRef); docStringID != "" {
		node.DocRef = globalDocIDMap.GetOrAssign(docStringID)
	}
}

// writeGlobalStores batch-writes all entity types to global stores.
// Nodes, edges, docs, and vectors use independent data files and can
// be written in parallel via errgroup.
func writeGlobalStores(
	ctx context.Context,
	sd *SylkDir, version SemanticVersion,
	nodeStore *GlobalVersionNodeStore,
	edgeStore *GlobalVersionEdgeStore,
	docStore *GlobalVersionDocStore,
	vectorStore *GlobalVersionVectorStore,
	nodes []*Node, edges []*Edge,
	docs []*VersionDocument, vectors []*VersionVector,
	chunkRefs []*ChunkRef, chunksStaged int,
) error {
	g, _ := errgroup.WithContext(ctx)

	g.Go(func() error {
		return nodeStore.WriteBatch(nodes)
	})
	g.Go(func() error {
		return edgeStore.WriteBatch(edges)
	})
	g.Go(func() error {
		return docStore.WriteBatch(docs)
	})
	g.Go(func() error {
		return vectorStore.WriteBatch(vectors)
	})
	g.Go(func() error {
		return writeGlobalChunkRefs(sd, version, chunkRefs, chunksStaged)
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("sylkdir: write global stores: %w", err)
	}
	return nil
}

// writeGlobalChunkRefs writes chunk refs to the global store if any exist.
func writeGlobalChunkRefs(sd *SylkDir, version SemanticVersion, refs []*ChunkRef, count int) error {
	if count == 0 {
		return nil
	}
	store, err := NewGlobalChunkRefStore(sd, version)
	if err != nil {
		return fmt.Errorf("open global chunk ref store: %w", err)
	}
	if err := store.WriteBatch(refs); err != nil {
		return fmt.Errorf("write global chunk refs: %w", err)
	}
	return store.SaveIndexes()
}

// applySupersessions updates old nodes' SupersededBy field and collects
// superseded document IDs for Bleve cleanup.
func applySupersessions(sd *SylkDir, oldHead SemanticVersion, globalNodeStore *GlobalVersionNodeStore, globalDocIDMap *DocIDMap, updates []supersessionUpdate) []string {
	if len(updates) == 0 {
		return nil
	}
	parentNodeStore, err := NewGlobalVersionNodeStore(sd, oldHead)
	if err != nil {
		return nil
	}
	defer parentNodeStore.Close()

	var supersededDocIDs []string
	for _, upd := range updates {
		docID := applySingleSupersession(parentNodeStore, globalNodeStore, globalDocIDMap, oldHead, upd)
		if docID != "" {
			supersededDocIDs = append(supersededDocIDs, docID)
		}
	}
	return supersededDocIDs
}

// applySingleSupersession updates one old node and returns its doc ID (if any).
func applySingleSupersession(parent, global *GlobalVersionNodeStore, docIDMap *DocIDMap, oldHead SemanticVersion, upd supersessionUpdate) string {
	oldNode, err := parent.ReadFromVersion(oldHead, upd.oldID)
	if err != nil {
		return ""
	}
	docID := resolveSupersededDocID(oldNode, docIDMap, upd.oldID)
	oldNode.SupersededBy = upd.newID
	_ = global.Write(oldNode) // Non-fatal: supplementary metadata
	return docID
}

// resolveSupersededDocID extracts the document string ID from a superseded node.
func resolveSupersededDocID(node *Node, docIDMap *DocIDMap, fallbackID uint32) string {
	if node.DocRef != 0 && docIDMap != nil {
		if docID := docIDMap.Reverse(node.DocRef); docID != "" {
			return docID
		}
	}
	return fmt.Sprintf("file_%d", fallbackID)
}

// saveGlobalIndexes saves offset indexes for all global stores to disk in parallel.
func saveGlobalIndexes(ctx context.Context, nodeStore *GlobalVersionNodeStore, edgeStore *GlobalVersionEdgeStore, docStore *GlobalVersionDocStore, vectorStore *GlobalVersionVectorStore) error {
	g, _ := errgroup.WithContext(ctx)
	g.Go(func() error { return nodeStore.SaveIndexes() })
	g.Go(func() error { return vectorStore.SaveIndexes() })
	g.Go(func() error { return docStore.SaveIndexes() })
	return g.Wait()
}

// ── Parallel commit helpers ────────────────────────────────────────────────

// storeWriteResult holds outputs from the store write path needed by later steps.
type storeWriteResult struct {
	supersededDocIDs []string
	superseded       map[string][2]uint32
	updates          []supersessionUpdate
}

// commitWriteEntities performs mutation pass, batch writes, supersession, and index saves.
func commitWriteEntities(
	ctx context.Context,
	cfg CommitConfig, sess *Session, newVersion, oldHead SemanticVersion,
	nodes []*Node, edges []*Edge, docs []*VersionDocument, vectors []*VersionVector,
	chunkRefs []*ChunkRef, chunksStaged int,
	ns *GlobalVersionNodeStore, es *GlobalVersionEdgeStore, ds *GlobalVersionDocStore, vs *GlobalVersionVectorStore,
) (storeWriteResult, error) {
	globalDocIDMap := ds.DocIDMap()
	superseded, updates := prepareNodeMutations(cfg, nodes, globalDocIDMap)
	if err := writeGlobalStores(ctx, cfg.SylkDir, newVersion, ns, es, ds, vs, nodes, edges, docs, vectors, chunkRefs, chunksStaged); err != nil {
		return storeWriteResult{}, err
	}
	supersededDocIDs := applySupersessions(cfg.SylkDir, oldHead, ns, globalDocIDMap, updates)
	if err := saveGlobalIndexes(ctx, ns, es, ds, vs); err != nil {
		return storeWriteResult{}, err
	}
	return storeWriteResult{
		supersededDocIDs: supersededDocIDs,
		superseded:       superseded,
		updates:          updates,
	}, nil
}

// commitTombstoneOrphan loads tombstone bitmap, marks dead nodes, detects orphans, and saves.
func commitTombstoneOrphan(
	cfg CommitConfig, newVersion SemanticVersion,
	updates []supersessionUpdate, nodes []*Node, parentTotalNodes uint32,
) (*TombstoneBitmap, int, int, error) {
	tb, err := loadAndMarkTombstones(cfg, newVersion, updates)
	if err != nil {
		return nil, 0, 0, err
	}
	orphanedKeys, deletedFiles := detectOrphans(cfg, nodes, tb, parentTotalNodes)
	return tb, orphanedKeys, deletedFiles, saveTombstone(tb)
}

// loadAndMarkTombstones loads the tombstone bitmap and marks superseded nodes as dead.
func loadAndMarkTombstones(cfg CommitConfig, newVersion SemanticVersion, updates []supersessionUpdate) (*TombstoneBitmap, error) {
	newVersionPath := cfg.SylkDir.GlobalVersionPath(newVersion)
	tb, err := LoadOrCreateTombstoneBitmap(newVersionPath, cfg.GlobalMeta.GetCurrentNodeID())
	if err != nil {
		return nil, fmt.Errorf("sylkdir: load tombstone bitmap: %w", err)
	}
	for _, upd := range updates {
		tb.MarkDead(upd.oldID)
	}
	return tb, nil
}

// saveTombstone persists the tombstone bitmap to disk.
func saveTombstone(tb *TombstoneBitmap) error {
	if err := tb.Save(); err != nil {
		return fmt.Errorf("sylkdir: save tombstone bitmap: %w", err)
	}
	return nil
}

// closeAndGetSessionBlevePath closes the session Bleve and returns its path.
// Closing before copy ensures a clean snapshot for promotion to global.
func closeAndGetSessionBlevePath(sess *Session) string {
	if sess.BleveStore == nil {
		return ""
	}
	_ = sess.BleveStore.CloseHead()
	return sess.BleveStore.HeadBlevePath()
}

// snapshotBleveOnly runs only the fast directory-copy snapshot step.
// Used when DeferBleveIndexing is true — actual indexing is handled by BackgroundIndexer.
func snapshotBleveOnly(store *GlobalVersionBleveStore, oldHead, newVersion SemanticVersion, sessionBlevePath string) error {
	if store == nil {
		return nil
	}
	_, err := store.SnapshotOrPromote(oldHead, newVersion, sessionBlevePath)
	if err != nil {
		return fmt.Errorf("sylkdir: snapshot global bleve: %w", err)
	}
	return nil
}

// snapshotAndIndexBleve prepares the global Bleve for the new version.
// On cold start (no parent global Bleve), promotes the session Bleve — all docs
// are already indexed, so no re-indexing is needed.
// On incremental boot, copies the parent global Bleve and indexes new session docs.
func snapshotAndIndexBleve(ctx context.Context, store *GlobalVersionBleveStore, oldHead, newVersion SemanticVersion, docs []*VersionDocument, sessionBlevePath string) (int, error) {
	if store == nil {
		return 0, nil
	}
	return promoteOrIndex(ctx, store, oldHead, newVersion, docs, sessionBlevePath)
}

// promoteOrIndex promotes session Bleve on cold start, or indexes new docs on incremental.
func promoteOrIndex(ctx context.Context, store *GlobalVersionBleveStore, oldHead, newVersion SemanticVersion, docs []*VersionDocument, sessionBlevePath string) (int, error) {
	promoted, err := store.SnapshotOrPromote(oldHead, newVersion, sessionBlevePath)
	if err != nil {
		return 0, fmt.Errorf("sylkdir: snapshot/promote global bleve: %w", err)
	}
	if promoted {
		return len(docs), nil
	}
	return indexNewDocs(ctx, store, docs)
}

// indexNewDocs converts session docs to search docs and indexes them into the global Bleve.
func indexNewDocs(ctx context.Context, store *GlobalVersionBleveStore, docs []*VersionDocument) (int, error) {
	if len(docs) == 0 {
		return 0, nil
	}
	searchDocs := convertToSearchDocs(docs)
	if err := store.IndexBatch(ctx, searchDocs); err != nil {
		return 0, fmt.Errorf("sylkdir: index documents in global bleve: %w", err)
	}
	return len(searchDocs), nil
}

// convertToSearchDocs converts session documents to Bleve-compatible search documents.
func convertToSearchDocs(docs []*VersionDocument) []*search.Document {
	searchDocs := make([]*search.Document, len(docs))
	for i, vdoc := range docs {
		searchDocs[i] = ConvertToSearchDocument(vdoc)
	}
	return searchDocs
}

// deleteSupersededBleveDocs removes superseded document IDs from the Bleve index.
func deleteSupersededBleveDocs(store *GlobalVersionBleveStore, docIDs []string) int {
	if store == nil || store.Store() == nil || len(docIDs) == 0 {
		return 0
	}
	mgr := store.Store().Manager()
	if mgr == nil {
		return 0
	}
	return countBleveDeletions(mgr, docIDs)
}

// countBleveDeletions deletes each doc ID from the IndexManager, returning the count of successful deletions.
func countBleveDeletions(mgr *bleve.IndexManager, docIDs []string) int {
	var count int
	for _, id := range docIDs {
		if mgr.Delete(context.Background(), id) == nil {
			count++
		}
	}
	return count
}

// recordBleveIndexed records that Bleve indexing completed for the given version.
func recordBleveIndexed(store *GlobalVersionBleveStore, gm *GlobalMeta, ver SemanticVersion) error {
	if store == nil {
		return nil
	}
	return gm.SetLastBleveIndexed(ver)
}

// logCommitEvent emits a structured commit event. No-op if logger is nil.
func logCommitEvent(logger *agentlog.BootEventLogger, eventType agentlog.EventType, level string, data any) {
	if logger == nil {
		return
	}
	logger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     level,
		Agent:     "boot",
		Event:     eventType.String(),
		EventCode: eventType,
		Data:      data,
	})
}

// logCommitBegin writes an OpCommitBegin entry if the WAL is non-nil.
func logCommitBegin(cwal *CommitWAL, sessionID uint32, ver SemanticVersion) error {
	if cwal == nil {
		return nil
	}
	d := &WALCommitData{
		SessionID: sessionID,
		Major:     ver.Major,
		Minor:     ver.Minor,
		Patch:     ver.Patch,
	}
	if _, err := cwal.LogCommitBegin(d); err != nil {
		return fmt.Errorf("sylkdir: commit wal begin: %w", err)
	}
	return nil
}

// logCommitEnd writes an OpCommitEnd entry and fsyncs the WAL if non-nil.
func logCommitEnd(cwal *CommitWAL, sessionID uint32, ver SemanticVersion) error {
	if cwal == nil {
		return nil
	}
	d := &WALCommitData{
		SessionID: sessionID,
		Major:     ver.Major,
		Minor:     ver.Minor,
		Patch:     ver.Patch,
	}
	if _, err := cwal.LogCommitEnd(d); err != nil {
		return fmt.Errorf("sylkdir: commit wal end: %w", err)
	}
	return cwal.Sync()
}
