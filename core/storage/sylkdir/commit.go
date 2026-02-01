// Package sylkdir provides session-aware storage with commit-to-global merge.
package sylkdir

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// CommitConfig holds the dependencies required to commit a session to global.
type CommitConfig struct {
	Session  *Session
	SylkDir  *SylkDir // Required for version directory creation
	GlobalMeta *GlobalMeta
	CanonicalIndex *CanonicalKeyIndex
	GlobalBleveStore *GlobalVersionBleveStore // nil means skip Bleve indexing
	CommitWAL *CommitWAL // nil means skip commit WAL bracketing

	// Vectors are collected but not written to IVF here.
	// IVF integration is Phase 5.
	// The caller is responsible for indexing vectors after commit.

	// ScopeRoots is the set of root directories this session fully indexed.
	// Any global file key whose path is under a ScopeRoot but NOT in the
	// session's file nodes is considered a deleted file — all its entities
	// are tombstoned. If empty, only entity-level orphan detection runs.
	ScopeRoots []string
}

// CommitResult describes what the commit produced.
type CommitResult struct {
	SessionID        uint32
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
func CommitToGlobal(cfg CommitConfig) (*CommitResult, error) {
	start := time.Now()
	sess := cfg.Session

	if sess.Meta.Status == SessionCommitted {
		return nil, fmt.Errorf("sylkdir: session %d already committed", sess.Meta.ID)
	}

	// ── Step 0: Calculate version + WAL bracket begin ───────────────────
	oldHead := cfg.GlobalMeta.GetHead()
	newVersion := oldHead.BumpMajor()

	if err := logCommitBegin(cfg.CommitWAL, sess.Meta.ID, newVersion); err != nil {
		return nil, err
	}

	// ── Step 1: Pre-commit minor checkpoint ────────────────────────────
	preCommitVer, err := sess.Checkpoint("pre-commit", CheckpointMinor)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: pre-commit checkpoint: %w", err)
	}

	// ── Step 2: Collect all entities from ancestor chain ───────────────
	sessNodeStore := NewVersionNodeStore(sess)
	sessEdgeStore := NewVersionEdgeStore(sess)
	sessDocStore := NewVersionDocStore(sess)
	sessVectorStore := NewVersionVectorStore(sess)

	nodes, err := sessNodeStore.ReadAllFromAncestorChain()
	if err != nil {
		return nil, fmt.Errorf("sylkdir: read session nodes: %w", err)
	}

	edges, err := sessEdgeStore.ReadAllFromAncestorChain()
	if err != nil {
		return nil, fmt.Errorf("sylkdir: read session edges: %w", err)
	}

	docs, err := sessDocStore.ReadFromAncestorChain()
	if err != nil {
		return nil, fmt.Errorf("sylkdir: read session docs: %w", err)
	}

	vectors, err := sessVectorStore.ReadAllFromAncestorChain()
	if err != nil {
		return nil, fmt.Errorf("sylkdir: read session vectors: %w", err)
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

	// ── Step 5: Append session data to new version (with supersession) ─
	superseded := make(map[string][2]uint32)

	// Track superseded nodes for updating old nodes
	type supersessionUpdate struct {
		oldID  uint32
		newID  uint32
	}
	var updates []supersessionUpdate

	// Remap DocRef from session DocIDMap → global DocIDMap so the uint32
	// values align with the global doc OffsetIndex keys.
	sessDocIDMap := cfg.Session.DocIDMap
	globalDocIDMap := globalDocStore.DocIDMap()

	for _, node := range nodes {
		if node.CanonicalKey != "" {
			existingID, wasSet := cfg.CanonicalIndex.SetIfNotExists(node.CanonicalKey, node.ID)
			if !wasSet {
				// Key already existed — this is a supersession.
				cfg.CanonicalIndex.Set(node.CanonicalKey, node.ID)
				superseded[node.CanonicalKey] = [2]uint32{existingID, node.ID}
				node.Supersedes = existingID
				updates = append(updates, supersessionUpdate{oldID: existingID, newID: node.ID})
			}
		}

		// Translate DocRef from session to global address space.
		if node.DocRef != 0 && sessDocIDMap != nil && globalDocIDMap != nil {
			docStringID := sessDocIDMap.Reverse(node.DocRef)
			if docStringID != "" {
				node.DocRef = globalDocIDMap.GetOrAssign(docStringID)
			}
		}

		if err := globalNodeStore.Write(node); err != nil {
			return nil, fmt.Errorf("sylkdir: write global node %d: %w", node.ID, err)
		}
	}

	// Update old nodes' SupersededBy field and collect superseded doc IDs for Bleve cleanup.
	var supersededDocIDs []string
	parentNodeStore, err := NewGlobalVersionNodeStore(cfg.SylkDir, oldHead)
	if err == nil {
		defer parentNodeStore.Close()
		for _, upd := range updates {
			oldNode, readErr := parentNodeStore.ReadFromVersion(oldHead, upd.oldID)
			if readErr != nil {
				continue // Non-fatal: supersession metadata is supplementary
			}
			// Collect doc string ID before overwriting the node.
			if oldNode.DocRef != 0 && globalDocIDMap != nil {
				if docID := globalDocIDMap.Reverse(oldNode.DocRef); docID != "" {
					supersededDocIDs = append(supersededDocIDs, docID)
				}
			} else {
				// Backward compat: derive doc ID from node ID for pre-DocRef data.
				supersededDocIDs = append(supersededDocIDs, fmt.Sprintf("file_%d", upd.oldID))
			}
			oldNode.SupersededBy = upd.newID
			if writeErr := globalNodeStore.Write(oldNode); writeErr != nil {
				continue
			}
		}
	}

	// Write edges
	for _, edge := range edges {
		if err := globalEdgeStore.Write(edge); err != nil {
			return nil, fmt.Errorf("sylkdir: write global edge (%d→%d): %w",
				edge.SourceID, edge.TargetID, err)
		}
	}

	// Write docs
	if err := globalDocStore.WriteBatch(docs); err != nil {
		return nil, fmt.Errorf("sylkdir: write global docs: %w", err)
	}

	// Write vectors
	if err := globalVectorStore.WriteBatch(vectors); err != nil {
		return nil, fmt.Errorf("sylkdir: write global vectors: %w", err)
	}

	// Write chunk refs from session ancestor chain (if any exist).
	chunkRefs, chunksStaged := collectSessionChunkRefs(sess)
	if chunksStaged > 0 {
		globalChunkRefStore, chunkErr := NewGlobalChunkRefStore(cfg.SylkDir, newVersion)
		if chunkErr != nil {
			return nil, fmt.Errorf("sylkdir: open global chunk ref store: %w", chunkErr)
		}
		if chunkErr := globalChunkRefStore.WriteBatch(chunkRefs); chunkErr != nil {
			return nil, fmt.Errorf("sylkdir: write global chunk refs: %w", chunkErr)
		}
		if chunkErr := globalChunkRefStore.SaveIndexes(); chunkErr != nil {
			return nil, fmt.Errorf("sylkdir: save global chunk indexes: %w", chunkErr)
		}
	}

	// Save offset indexes for nodes and vectors to disk.
	if err := globalNodeStore.SaveIndexes(); err != nil {
		return nil, fmt.Errorf("sylkdir: save global node indexes: %w", err)
	}
	if err := globalVectorStore.SaveIndexes(); err != nil {
		return nil, fmt.Errorf("sylkdir: save global vector indexes: %w", err)
	}
	if err := globalDocStore.SaveIndexes(); err != nil {
		return nil, fmt.Errorf("sylkdir: save global doc indexes: %w", err)
	}

	// ── Step 5b: Mark superseded nodes in tombstone bitmap ─────────────
	newVersionPath := cfg.SylkDir.GlobalVersionPath(newVersion)
	tb, err := LoadOrCreateTombstoneBitmap(newVersionPath, cfg.GlobalMeta.GetCurrentNodeID())
	if err != nil {
		return nil, fmt.Errorf("sylkdir: load tombstone bitmap: %w", err)
	}
	for _, upd := range updates {
		tb.MarkDead(upd.oldID)
	}

	// ── Step 5c: Scope-aware orphan detection ────────────────────────
	orphanedKeys, deletedFiles := detectOrphans(cfg, nodes, tb)

	// Save tombstone bitmap (includes supersession + orphan marks)
	if err := tb.Save(); err != nil {
		return nil, fmt.Errorf("sylkdir: save tombstone bitmap: %w", err)
	}

	// Save canonical index (after supersession updates + orphan deletions)
	if err := cfg.CanonicalIndex.Save(); err != nil {
		return nil, fmt.Errorf("sylkdir: save canonical index: %w", err)
	}

	// ── Step 6: Snapshot Bleve into new version + index new docs ───────
	var docsIndexed, docsSuperseded int
	if cfg.GlobalBleveStore != nil {
		// Snapshot parent Bleve to new version
		if err := cfg.GlobalBleveStore.SnapshotBleve(oldHead, newVersion); err != nil {
			return nil, fmt.Errorf("sylkdir: snapshot global bleve: %w", err)
		}

		// Delete superseded documents (IDs collected during node update loop)
		if cfg.GlobalBleveStore.Store() != nil && cfg.GlobalBleveStore.Store().Manager() != nil {
			for _, docID := range supersededDocIDs {
				if err := cfg.GlobalBleveStore.Store().Manager().Delete(context.Background(), docID); err == nil {
					docsSuperseded++
				}
			}
		}

		// Index new documents
		if len(docs) > 0 {
			searchDocs := make([]*search.Document, len(docs))
			for i, vdoc := range docs {
				searchDocs[i] = ConvertToSearchDocument(vdoc)
			}
			if err := cfg.GlobalBleveStore.IndexBatch(context.Background(), searchDocs); err != nil {
				return nil, fmt.Errorf("sylkdir: index documents in global bleve: %w", err)
			}
			docsIndexed = len(searchDocs)
		}

		// Record that Bleve indexing completed for this version.
		if err := cfg.GlobalMeta.SetLastBleveIndexed(newVersion); err != nil {
			return nil, fmt.Errorf("sylkdir: set last bleve indexed: %w", err)
		}
	}

	// ── Step 6: Update manifest + register commit ──────────────────────
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
	if err := cfg.GlobalMeta.AddVersion(gv); err != nil {
		return nil, fmt.Errorf("sylkdir: add global version: %w", err)
	}
	if err := cfg.GlobalMeta.SetHead(newVersion); err != nil {
		return nil, fmt.Errorf("sylkdir: set global head: %w", err)
	}

	// Legacy: also call RegisterCommit for backwards compat with committed_sessions
	if err := cfg.GlobalMeta.RegisterCommit(sess.Meta.ID, preCommitVer); err != nil {
		return nil, fmt.Errorf("sylkdir: register commit: %w", err)
	}

	// WAL bracket end — commit is now durable.
	if err := logCommitEnd(cfg.CommitWAL, sess.Meta.ID, newVersion); err != nil {
		return nil, err
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

	return &CommitResult{
		SessionID:        sess.Meta.ID,
		PreCommitVersion: preCommitVer,
		GlobalVersion:    newVersion,
		NodesMerged:      len(nodes),
		EdgesMerged:      len(edges),
		VectorsStaged:    len(vectors),
		DocsStaged:       len(docs),
		ChunksStaged:     chunksStaged,
		DocsIndexed:      docsIndexed,
		DocsSuperseded:   docsSuperseded,
		DeadNodeCount:    deadCount,
		TombstoneRatio:   tb.DeadRatio(uint32(len(nodes))),
		OrphanedKeys:     orphanedKeys,
		DeletedFiles:     deletedFiles,
		Superseded:       superseded,
		StagedVectors:    vectors,
		StagedDocs:       docs,
		Duration:         time.Since(start),
	}, nil
}

// detectOrphans finds canonical keys in the global index that are scoped to
// files the session re-indexed but are not present in the session's output.
// These are entities that were removed (function deleted, etc.) or files that
// were deleted from disk. Returns counts for reporting.
func detectOrphans(cfg CommitConfig, sessionNodes []*Node, tb *TombstoneBitmap) (orphanedKeys, deletedFiles int) {
	// Build session ground truth
	sessionKeys := make(map[string]bool, len(sessionNodes))
	indexedPaths := make(map[string]bool)
	for _, n := range sessionNodes {
		if n.CanonicalKey != "" {
			sessionKeys[n.CanonicalKey] = true
		}
		if NodeType(n.NodeType) == NodeTypeFile && n.Path != "" {
			indexedPaths[n.Path] = true
		}
	}

	// Per-file scope diff: for each file the session touched, find global
	// keys that the session didn't produce (removed entities).
	for path := range indexedPaths {
		fileKeys := cfg.CanonicalIndex.LookupPrefix("file:" + path)
		symbolKeys := cfg.CanonicalIndex.LookupPrefix("symbol:" + path + ":")
		orphanedKeys += tombstoneOrphans(cfg.CanonicalIndex, tb, sessionKeys, fileKeys)
		orphanedKeys += tombstoneOrphans(cfg.CanonicalIndex, tb, sessionKeys, symbolKeys)
	}

	// File deletion detection via ScopeRoots
	if len(cfg.ScopeRoots) > 0 {
		allFileKeys := cfg.CanonicalIndex.LookupPrefix("file:")
		for key, nodeID := range allFileKeys {
			filePath := strings.TrimPrefix(key, "file:")
			if indexedPaths[filePath] || !isUnderAnyRoot(filePath, cfg.ScopeRoots) {
				continue
			}
			// File was deleted — tombstone file node + all its symbols
			tb.MarkDead(nodeID)
			cfg.CanonicalIndex.Delete(key)
			orphanedKeys++
			deletedFiles++

			symbolKeys := cfg.CanonicalIndex.LookupPrefix("symbol:" + filePath + ":")
			orphanedKeys += tombstoneOrphans(cfg.CanonicalIndex, tb, nil, symbolKeys)
		}
	}

	return orphanedKeys, deletedFiles
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

// collectSessionChunkRefs reads all chunk refs from the session's ancestor chain.
func collectSessionChunkRefs(sess *Session) ([]*ChunkRef, int) {
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
