# Sylk Knowledge Graph Implementation Plan

## Overview

This document tracks the implementation work required to bring the Knowledge Graph from its current state to full compliance with DB.md specification. Work is organized **back-to-front**: storage layer first, then ingestion, then query, then agent integration.

**Current State Summary:**
- `.sylk/` directory structure: NOT IMPLEMENTED
- Node/Edge binary storage: NOT IMPLEMENTED (using SQLite)
- Node/Edge schema: NON-COMPLIANT (string IDs, missing fields)
- IVF vector index: EXISTS but NOT WIRED to query path
- Embedder: EXISTS but NOT CALLED in production
- Boot indexing: Bleve only, NO vectors
- Agent query path: MOCKS ONLY, no production wiring
- Session versioning: NOT IMPLEMENTED

---

## Phase 1: Filesystem Foundation

### TODO 1.1: Create .sylk Directory Manager

**Description**: Implement the core directory structure manager that creates and validates the .sylk layout.

**Acceptance Criteria**:
- [ ] `SylkDir.Init(projectPath)` creates:
  ```
  .sylk/
  ├── config.yaml
  ├── knowledge/
  │   ├── meta.json
  │   ├── nodes/blocks/
  │   ├── nodes/index/
  │   ├── edges/
  │   └── vectors/
  │       ├── shards/
  │       ├── graph/
  │       └── partitions/
  ├── bleve/index/
  └── sessions/
  ```
- [ ] `SylkDir.Validate()` returns errors for missing/corrupt structure
- [ ] `SylkDir.Lock()` creates `.sylk/lock` file preventing concurrent access
- [ ] Unit tests verify structure creation and validation

---

### TODO 1.2: Implement Global Meta Store

**Description**: Create `knowledge/meta.json` manager for global state.

**Acceptance Criteria**:
- [ ] `GlobalMeta` struct matches DB.md:
  ```go
  type GlobalMeta struct {
      SchemaVersion      int                 `json:"schema_version"`
      NextNodeID         uint32              `json:"next_node_id"`
      NextSessionID      uint32              `json:"next_session_id"`
      CommittedSessions  []CommittedSession  `json:"committed_sessions"`
  }
  ```
- [ ] `GlobalMeta.AllocateNodeID()` atomically increments and persists `next_node_id`
- [ ] `GlobalMeta.AllocateSessionID()` atomically increments `next_session_id`
- [ ] `GlobalMeta.RegisterCommit(sessionID, finalVersion)` appends to committed_sessions
- [ ] File locking prevents concurrent writes
- [ ] Atomic write (write to temp, rename) prevents corruption

---

### TODO 1.3: Implement Node Block Storage

**Description**: Replace SQLite node storage with block-based binary files.

**Acceptance Criteria**:
- [ ] Nodes stored in `knowledge/nodes/blocks/block_NNNN.bin`
- [ ] Block size = 4096 nodes (configurable)
- [ ] `NodeBlockStore.Write(node)` appends to correct block based on `node.ID / blockSize`
- [ ] `NodeBlockStore.Read(nodeID)` loads from correct block with mmap
- [ ] Binary format matches DB.md Node structure (fixed header + variable content)
- [ ] `knowledge/nodes/index/shard_NN.idx` provides nodeID → block+offset lookup
- [ ] Benchmark: Read 10K random nodes < 100ms

---

### TODO 1.4: Implement Canonical Key Index

**Description**: Create index from CanonicalKey → current NodeID.

**Acceptance Criteria**:
- [ ] `knowledge/nodes/index/canonical_keys.idx` stores key→ID mappings
- [ ] `CanonicalKeyIndex.Lookup(key)` returns nodeID or ErrNotFound
- [ ] `CanonicalKeyIndex.Set(key, nodeID)` updates mapping
- [ ] `CanonicalKeyIndex.Delete(key)` removes mapping
- [ ] Uses sorted string table (SST) or similar for O(log n) lookup
- [ ] Handles supersession: old key points to new nodeID after update

---

### TODO 1.5: Implement Edge Shard Storage

**Description**: Replace SQLite edge storage with sharded binary files.

**Acceptance Criteria**:
- [ ] Edges stored in `knowledge/edges/shard_NNNN/`
- [ ] Shard determined by `sourceID / 65536`
- [ ] Each shard contains:
  - `edges.bin` - packed edge records (35 bytes each)
  - `outgoing.idx` - sourceID → edge offsets
  - `incoming.idx` - targetID → edge offsets
- [ ] `EdgeShardStore.Write(edge)` appends to correct shard
- [ ] `EdgeShardStore.GetOutgoing(nodeID)` returns all outgoing edges
- [ ] `EdgeShardStore.GetIncoming(nodeID)` returns all incoming edges
- [ ] Benchmark: Load all edges for a node < 1ms

---

### TODO 1.6: Integrate IVF Persistence with .sylk Layout

**Description**: Move IVF storage into `knowledge/vectors/` structure.

**Acceptance Criteria**:
- [ ] IVF saves to `knowledge/vectors/` not arbitrary path:
  - `vectors/shards/shard_NNNN/vectors.bin`
  - `vectors/shards/shard_NNNN/bbq.bin`
  - `vectors/shards/shard_NNNN/norms.bin`
  - `vectors/graph/adjacency.bin`
  - `vectors/graph/medoid.bin`
  - `vectors/partitions/centroids.bin`
  - `vectors/partitions/assignments.bin`
- [ ] `IVF.Save()` and `IVF.Load()` use SylkDir paths
- [ ] Existing IVF tests pass with new paths
- [ ] Vector shard determined by `vectorID / 65536`

---

### TODO 1.7: Integrate Global Bleve with .sylk Layout

**Description**: Configure global (committed) Bleve index at `.sylk/bleve/index/`.

**Acceptance Criteria**:
- [ ] Global Bleve index created at `.sylk/bleve/index/`
- [ ] `GlobalBleveStore.Open(sylkDir)` uses correct path
- [ ] Global index holds ONLY committed session data (merged on commit)
- [ ] Existing Bleve tests pass
- [ ] Index survives process restart

**NOTE**: This is the GLOBAL committed index. Per-session documents are stored in version folders and merged here on commit. See TODO 3.7.

---

### TODO 1.8: Implement Session Directory Structure

**Description**: Create per-session storage with versioned data directories.

**Acceptance Criteria**:
- [ ] `SessionStore.Create(sessionID)` creates:
  ```
  sessions/ses_XXX/
  ├── meta.json              # Session metadata
  ├── base/
  │   └── snapshot.json      # Global state at session start
  ├── versions/
  │   ├── manifest.json      # Version DAG
  │   └── v000001/           # Initial version (session_start)
  │       ├── meta.json
  │       ├── nodes/
  │       ├── edges/
  │       ├── vectors/
  │       ├── docs/          # Per-version document storage
  │       │   └── batch.jsonl
  │       └── deletions.json
  ├── delta/
  │   └── tracker.json
  ├── state/
  ├── agents/
  └── messages/
  ```
- [ ] `sessions/active` symlink points to current session
- [ ] `SessionStore.SetActive(sessionID)` updates symlink atomically
- [ ] Benchmark: Create session with 5 versions, verify structure

---

## Phase 2: Data Structure Compliance

### TODO 2.1: Add CanonicalKey to Node

**Description**: Implement canonical key for entity deduplication.

**Acceptance Criteria**:
- [ ] `GraphNode.CanonicalKey string` field added
- [ ] Format: `"repo:path/to/file.go:FunctionName:func"`, `"doi:10.1234/paper"`, etc.
- [ ] `NodeStore.Insert()` checks canonical key index first
- [ ] If key exists in visible data → return existing ID (no duplicate)
- [ ] Binary format includes CanonicalKey with length prefix

---

### TODO 2.2: Add Supersedes Field to Node

**Description**: Complete supersession chain support.

**Acceptance Criteria**:
- [ ] `GraphNode.Supersedes uint32` field (currently only SupersededBy exists)
- [ ] When node B supersedes node A:
  - `A.SupersededBy = B.ID`
  - `B.Supersedes = A.ID`
- [ ] `NodeStore.GetSupersessionChain(nodeID)` walks chain
- [ ] Binary format includes both fields

---

### TODO 2.3: Switch Node IDs to uint32

**Description**: Replace string UUIDs with global atomic counter.

**Acceptance Criteria**:
- [ ] `GraphNode.ID` type changes from `string` to `uint32`
- [ ] IDs allocated from `GlobalMeta.NextNodeID`
- [ ] All references updated (edges, vectors, etc.)
- [ ] String ID kept as `GraphNode.ExternalID` for API compatibility if needed
- [ ] Binary format uses 4 bytes for ID

---

### TODO 2.4: Add CreatedBy (AgentID) to Node

**Description**: Track which agent created each node.

**Acceptance Criteria**:
- [ ] `GraphNode.CreatedBy uint16` field added
- [ ] All node creation paths populate this field
- [ ] Binary format includes 2-byte AgentID

---

### TODO 2.5: Convert SessionID to uint32

**Description**: Align session ID type with spec.

**Acceptance Criteria**:
- [ ] `GraphNode.SessionID uint32` (was string)
- [ ] `GraphEdge.SessionID uint32` (new field)
- [ ] Session IDs allocated from `GlobalMeta.NextSessionID`
- [ ] String ID (`ses_001`) kept as `Session.StringID` for display

---

### TODO 2.6: Add Provenance Fields to Edge

**Description**: Track who created/modified edges.

**Acceptance Criteria**:
- [ ] `GraphEdge` struct updated:
  ```go
  type GraphEdge struct {
      SourceID  uint32
      TargetID  uint32
      Type      EdgeType    // uint8
      Weight    float32
      SessionID uint32      // NEW
      AgentID   uint16      // NEW
      CreatedAt uint64      // Unix nano
      UpdatedAt uint64      // NEW: Unix nano
  }
  ```
- [ ] All edge write paths populate provenance fields

---

### TODO 2.7: Remove Autoincrement ID from Edges

**Description**: Edge identity is (src, dst, type), not a separate ID.

**Acceptance Criteria**:
- [ ] Edge primary key is `(source_id, target_id, edge_type)`
- [ ] No separate autoincrement ID
- [ ] All code referencing `edge.ID` updated to use compound key
- [ ] Queries use compound key for lookups

---

### TODO 2.8: Convert Edge IDs to uint32

**Description**: Align edge source/target with uint32 node IDs.

**Acceptance Criteria**:
- [ ] `GraphEdge.SourceID uint32` (was string)
- [ ] `GraphEdge.TargetID uint32` (was string)
- [ ] Foreign keys reference nodes as uint32

---

### TODO 2.9: Implement Compact EdgeType

**Description**: EdgeType as uint8 for space efficiency.

**Acceptance Criteria**:
- [ ] `EdgeType` underlying type is `uint8`
- [ ] Max 256 edge types (current ~20, plenty of room)
- [ ] Binary format uses 1 byte

---

### TODO 2.10: Implement Binary Edge Record Format

**Description**: Define fixed-size binary format for edges.

**Acceptance Criteria**:
- [ ] Edge binary record:
  ```
  SourceID:  4 bytes (uint32)
  TargetID:  4 bytes (uint32)
  Type:      1 byte  (uint8)
  Weight:    4 bytes (float32)
  SessionID: 4 bytes (uint32)
  AgentID:   2 bytes (uint16)
  CreatedAt: 8 bytes (uint64)
  UpdatedAt: 8 bytes (uint64)
  ─────────────────────────────
  Total:     35 bytes per edge
  ```
- [ ] Reader/writer functions for binary format
- [ ] Unit tests verify round-trip serialization

---

## Phase 3: Session Storage

### TODO 3.1: Implement Session Directory Structure

**Description**: Create per-session storage directories.

**Acceptance Criteria**:
- [ ] `SessionStore.Create(sessionID)` creates:
  ```
  sessions/ses_XXX/
  ├── meta.json
  ├── base/snapshot.json
  ├── versions/manifest.json
  ├── delta/tracker.json
  ├── state/
  ├── agents/
  └── messages/
  ```
- [ ] `sessions/active` symlink points to current session
- [ ] `SessionStore.SetActive(sessionID)` updates symlink atomically
- [ ] `SessionStore.List()` returns all session IDs with status

---

### TODO 3.2: Implement Session Meta

**Description**: Create session metadata management.

**Acceptance Criteria**:
- [ ] `SessionMeta` struct matches DB.md:
  ```go
  type SessionMeta struct {
      ID          uint32        `json:"id"`
      StringID    string        `json:"string_id"`
      CreatedAt   time.Time     `json:"created_at"`
      Status      SessionStatus `json:"status"`
      CommittedAt *time.Time    `json:"committed_at,omitempty"`
  }
  ```
- [ ] Status transitions: `active` → `committed`
- [ ] `SessionMeta.Save()` persists to `meta.json`
- [ ] `SessionMeta.Load(sessionID)` reads from disk

---

### TODO 3.3: Implement Base Snapshot

**Description**: Capture global state at session start.

**Acceptance Criteria**:
- [ ] `BaseSnapshot` struct:
  ```go
  type BaseSnapshot struct {
      CommittedSessions []uint32  `json:"committed_sessions"`
      SnapshotAt        time.Time `json:"snapshot_at"`
      NextNodeID        uint32    `json:"next_node_id"`
  }
  ```
- [ ] `Session.Start()` creates `base/snapshot.json` from current GlobalMeta
- [ ] Snapshot is immutable after creation
- [ ] Used for visibility rules during queries

---

### TODO 3.4: Implement Version Manifest

**Description**: Create per-session version DAG.

**Acceptance Criteria**:
- [ ] `VersionManifest` struct:
  ```go
  type VersionManifest struct {
      SessionID   uint32    `json:"session_id"`
      Head        uint32    `json:"head"`
      NextVersion uint32    `json:"next_version"`
      Versions    []Version `json:"versions"`
  }
  type Version struct {
      ID        uint32       `json:"id"`
      ParentID  uint32       `json:"parent_id"`
      Name      string       `json:"name,omitempty"`
      CreatedAt time.Time    `json:"created_at"`
      Trigger   string       `json:"trigger"` // "explicit", "auto_delta", "implicit"
      Stats     VersionStats `json:"stats"`
  }
  ```
- [ ] `Manifest.CreateVersion(name, trigger)` adds version, updates Head
- [ ] `Manifest.GetAncestorChain(versionID)` returns [head, parent, ..., 1]
- [ ] `Manifest.Save()` persists atomically

---

### TODO 3.5: Implement Version Storage

**Description**: Create per-version data directories.

**Acceptance Criteria**:
- [ ] Each version creates `versions/v000001/`:
  ```
  v000001/
  ├── meta.json
  ├── nodes/data.bin, index.bin
  ├── edges/
  ├── vectors/
  ├── docs/
  └── deletions.json
  ```
- [ ] `VersionStore.WriteNode(versionID, node)` appends to version's node store
- [ ] `VersionStore.WriteEdge(versionID, edge)` appends to version's edge store
- [ ] `deletions.json` tracks IDs deleted in this version
- [ ] Data is append-only within version

---

### TODO 3.6: Implement Delta Tracker

**Description**: Track changes for auto-checkpoint triggers.

**Acceptance Criteria**:
- [ ] `DeltaTracker` struct with atomic counters:
  ```go
  type DeltaTracker struct {
      NodesCreated   atomic.Uint32
      EdgesCreated   atomic.Uint32
      EdgesModified  atomic.Uint32
      VectorsCreated atomic.Uint32
      DocsBytes      atomic.Uint64
      LastCheckpoint time.Time
  }
  ```
- [ ] `DeltaTracker.ShouldCheckpoint()` returns true when:
  - NodesCreated >= 50, OR
  - EdgesCreated + EdgesModified >= 200, OR
  - DocsBytes >= 512KB, OR
  - time.Since(LastCheckpoint) >= 10 minutes
- [ ] `DeltaTracker.Reset()` zeros counters, updates LastCheckpoint
- [ ] Persisted to `delta/tracker.json` for crash recovery

---

### TODO 3.7: Implement Per-Version Document Storage

**Description**: Store documents as JSONL in version folders for session isolation.

**Acceptance Criteria**:
- [ ] `VersionDocStore.Write(versionID, doc)` appends to `versions/vNNN/docs/batch.jsonl`
- [ ] JSONL format for efficient append-only writes:
  ```jsonl
  {"id":"doc1","path":"/test.go","content":"...","indexed_at":"..."}
  {"id":"doc2","path":"/test2.go","content":"...","indexed_at":"..."}
  ```
- [ ] `VersionDocStore.Read(versionID)` streams documents from version
- [ ] `VersionDocStore.ReadAncestorChain(versions []uint32)` yields docs from all ancestors
- [ ] Per-session document count tracked in DeltaTracker.DocsBytes
- [ ] Benchmark: Write 1000 docs to version < 500ms

---

### TODO 3.8: Implement Session-Aware Document Search

**Description**: Document search respects session visibility rules.

**Acceptance Criteria**:
- [ ] `SessionDocSearcher` searches:
  1. Current session's version docs (ancestor chain of HEAD)
  2. Global Bleve for committed sessions in BaseSnapshot
- [ ] Query result merges both sources with deduplication by doc ID
- [ ] Visibility: docs from other active sessions NOT visible
- [ ] Visibility: docs committed AFTER session start NOT visible
- [ ] Integration test: two sessions, verify isolation

---

## Phase 4: Ingestion Pipeline

### TODO 4.1: Create Ingestion Coordinator

**Description**: Unified entry point for all document ingestion (boot and runtime).

**Acceptance Criteria**:
- [ ] `IngestionCoordinator` struct:
  ```go
  type IngestionCoordinator struct {
      sylkDir     *SylkDir
      nodeStore   *NodeBlockStore
      edgeStore   *EdgeShardStore
      vectorStore *IVFIndex
      bleveStore  *BleveStore
      embedder    Embedder
      chunker     *Chunker
  }
  ```
- [ ] `Ingest(ctx, source, content, metadata)` performs full pipeline:
  1. Chunk content
  2. Generate embeddings (batch)
  3. Create GraphNodes with CanonicalKey
  4. Store nodes in NodeBlockStore
  5. Store embeddings in IVF
  6. Index text in Bleve
  7. Update DeltaTracker
- [ ] Returns `IngestionResult` with node IDs, chunk count, timing

---

### TODO 4.2: Implement Boot Index Pipeline

**Description**: Initial codebase indexing at startup.

**Acceptance Criteria**:
- [ ] `BootIndexer` struct with config:
  ```go
  type BootIndexConfig struct {
      RootPath        string
      IncludePatterns []string
      ExcludePatterns []string
      BatchSize       int
      Concurrency     int
  }
  ```
- [ ] `BootIndexer.Run(ctx)` performs:
  1. Scan files matching patterns
  2. For each file:
     - Parse with tree-sitter (if code)
     - Extract functions, structs, etc. as separate nodes
     - Generate CanonicalKey: `"repo:path:Name:type"`
  3. Batch embed (configurable batch size)
  4. Batch insert to all stores
  5. Progress callback for CLI display
- [ ] Resume from checkpoint if interrupted
- [ ] Integration test: index 1000 files, verify searchable

---

### TODO 4.3: Implement Tree-Sitter Code Extraction

**Description**: Extract semantic units from source code.

**Acceptance Criteria**:
- [ ] `CodeExtractor.Extract(path, content, lang)` returns:
  ```go
  type CodeUnit struct {
      Kind        string  // "function", "struct", "interface", "method"
      Name        string
      Signature   string
      Body        string
      StartLine   int
      EndLine     int
      Parent      string  // containing struct for methods
  }
  ```
- [ ] Supported languages: Go, Python, TypeScript, Rust
- [ ] Each CodeUnit becomes a GraphNode with:
  - CanonicalKey: `"repo:{path}:{Name}:{Kind}"`
  - Content: signature + body
  - NodeType: mapped from Kind
- [ ] File-level node links to all contained units via edges

---

### TODO 4.4: Implement Agent Ingest Skill

**Description**: Allow agents to add documents at runtime.

**Acceptance Criteria**:
- [ ] `IngestDocumentSkill` for agents:
  ```go
  type IngestDocumentInput struct {
      Content     string
      Source      string            // URL, file path, etc.
      Domain      Domain
      NodeType    NodeType
      Metadata    map[string]any
  }
  ```
- [ ] Skill calls `IngestionCoordinator.Ingest()`
- [ ] Returns node ID for future reference
- [ ] Respects session isolation (writes to session version)
- [ ] Integration test: agent ingests doc → agent queries → finds doc

---

### TODO 4.5: Wire Embedder to Production

**Description**: Ensure embedder is created and used.

**Acceptance Criteria**:
- [ ] Application bootstrap creates embedder:
  ```go
  embedder, err := embedder.NewEmbedder(ctx, embedder.Config{
      Provider: "voyage",  // or "local"
      Model:    "voyage-code-2",
  })
  ```
- [ ] Embedder passed to IngestionCoordinator
- [ ] Embedder passed to query pipeline (for query embedding)
- [ ] Config loaded from `.sylk/config.yaml`
- [ ] Graceful fallback if API unavailable

---

### TODO 4.6: Implement IVF Build from Ingestion

**Description**: Build/update IVF index from ingested vectors.

**Acceptance Criteria**:
- [ ] Two modes:
  1. **Initial Build**: After boot index completes
     - Collect all vectors from storage
     - Run `ivf.Build(vectors, config)`
     - Save to `.sylk/knowledge/vectors/`
  2. **Incremental Update**: During runtime
     - `ivf.Insert(id, vector)` for new nodes
     - Batch insertions via `ivf.StitchBatch()`
- [ ] IVF config derived from vector count (no magic numbers)
- [ ] Rebuild triggered when partition imbalance detected

---

## Phase 5: Query Pipeline

### TODO 5.1: Implement IVF Adapter

**Description**: Bridge IVF to VectorIndexSearcher interface.

**Acceptance Criteria**:
- [ ] `IVFAdapter` implements `VectorIndexSearcher`:
  ```go
  func (a *IVFAdapter) Search(query []float32, k int, filter *VectorIndexSearchFilter) []VectorIndexSearchResult {
      // 1. Call ivf.SearchVamana(query, k*2)  // overfetch for filtering
      // 2. Map uint32 IDs to node metadata
      // 3. Apply Domain/NodeType filters
      // 4. Return top k results
  }
  func (a *IVFAdapter) GetVector(id string) ([]float32, error)
  ```
- [ ] ID mapping persisted in `.sylk/knowledge/vectors/id_map.bin`
- [ ] Filter support: Domain, NodeType, MinSimilarity
- [ ] Benchmark: 10K search < 50ms with filters

---

### TODO 5.2: Wire VectorSearcher to Production

**Description**: Create VectorSearcher with real IVFAdapter.

**Acceptance Criteria**:
- [ ] Application bootstrap:
  ```go
  ivfIndex, _ := ivf.LoadIndex(sylkDir.VectorsPath())
  ivfAdapter := NewIVFAdapter(ivfIndex, idMap)
  vectorSearcher := vectorgraphdb.NewVectorSearcher(db, ivfAdapter)
  ```
- [ ] VectorSearcher available for coordinator
- [ ] Existing VectorSearcher tests pass with real adapter

---

### TODO 5.3: Wire VectorDBAdapter to Production

**Description**: Connect VectorSearcher to coordinator layer.

**Acceptance Criteria**:
- [ ] Application bootstrap:
  ```go
  vectorDBAdapter, _ := coordinator.NewVectorDBAdapter(vectorSearcher, embedder)
  ```
- [ ] VectorDBAdapter implements `coordinator.VectorSearcher`
- [ ] Integration test: query string → embed → search → return nodes

---

### TODO 5.4: Wire TieredSearcher to Production

**Description**: Connect all search tiers.

**Acceptance Criteria**:
- [ ] Application bootstrap:
  ```go
  tieredSearcher := context.NewTieredSearcher(context.TieredSearcherConfig{
      HotCache: hotCache,
      Bleve:    bleveSearcher,
      Vector:   vectorDBAdapter,
      Embedder: queryEmbedder,
  })
  ```
- [ ] All three tiers functional:
  - Hot (in-memory cache)
  - Warm (Bleve full-text)
  - Full (Bleve + Vector with RRF fusion)
- [ ] Integration test: query at each tier budget

---

### TODO 5.5: Wire Agent Skills to TieredSearcher

**Description**: Connect agent query skills to real search.

**Acceptance Criteria**:
- [ ] `LibrarianDependencies.CodeSearcher` uses TieredSearcher
- [ ] `ArchivalistDependencies.VectorSearcher` uses TieredSearcher
- [ ] `AcademicDependencies.ResearchSearcher` uses TieredSearcher
- [ ] All agent search skills return real results (not mocks)
- [ ] Integration test: agent skill query → real results

---

## Phase 6: Query Visibility & Context

### TODO 6.1: Implement QueryContext

**Description**: Create session-aware query context.

**Acceptance Criteria**:
- [ ] `QueryContext` struct:
  ```go
  type QueryContext struct {
      SessionID        uint32
      HeadVersion      uint32
      AncestorVersions []uint32
      BaseSnapshot     *BaseSnapshot
  }
  ```
- [ ] `Session.BuildQueryContext()` constructs from current state
- [ ] Used by all read operations

---

### TODO 6.2: Implement Visibility Rules

**Description**: Filter data based on session visibility.

**Acceptance Criteria**:
- [ ] `NodeStore.GetNode(ctx *QueryContext, id)` applies visibility:
  1. Check deletions in session versions
  2. Search session versions (newest first)
  3. Search global committed state (only sessions in BaseSnapshot)
  4. Check supersession (skip if SupersededBy is visible)
- [ ] `EdgeStore.GetEdge(ctx, src, dst, type)` similar logic
- [ ] `VectorStore.Search(ctx, query, k)` filters by visibility
- [ ] Unit tests verify isolation between sessions

---

## Phase 7: Session Operations

### TODO 7.1: Implement Checkpoint

**Description**: Create explicit version checkpoints.

**Acceptance Criteria**:
- [ ] `Session.Checkpoint(name string)` creates new version:
  1. Flush pending writes to current version directory
  2. Create new version in manifest with trigger="explicit"
  3. Update Head pointer
  4. Reset DeltaTracker
- [ ] `Session.AutoCheckpoint()` same but trigger="auto_delta"
- [ ] Called automatically when `DeltaTracker.ShouldCheckpoint()` returns true

---

### TODO 7.2: Implement Checkout

**Description**: Switch HEAD to different version.

**Acceptance Criteria**:
- [ ] `Session.Checkout(versionID)` changes HEAD:
  1. Verify versionID exists in manifest
  2. Update manifest.Head
  3. Rebuild QueryContext with new ancestor chain
- [ ] Queries now see state at that version
- [ ] New writes create branch from checked-out version

---

### TODO 7.3: Implement CommitToGlobal

**Description**: Merge session data to global knowledge graph including documents.

**Acceptance Criteria**:
- [ ] `Session.Commit()` performs merge:
  1. Collect all entities from ancestor chain (nodes, edges, vectors, **docs**)
  2. For each node:
     - If canonical key exists in global → create supersession
     - Else → append to global
  3. Update canonical key index
  4. Resolve edge endpoints (map session IDs to global)
  5. Append vectors to global IVF
  6. **Index documents in global Bleve** (merge docs/batch.jsonl from all versions)
  7. Apply deletions (mark deleted in global, remove from Bleve)
  8. Register in GlobalMeta.CommittedSessions
  9. Update session status to "committed"
- [ ] Document merge:
  ```go
  docs := s.collectDocsFromVersions(ancestorChain)
  for _, doc := range docs {
      kg.GlobalBleve.Index(ctx, doc)
  }
  ```
- [ ] Atomic: either all succeeds or nothing changes
- [ ] Integration test: create session → add docs → commit → query from new session → finds docs in global Bleve

---

## Phase 8: Application Bootstrap & CLI

### TODO 8.1: Create Unified Bootstrap

**Description**: Single entry point that wires everything.

**Acceptance Criteria**:
- [ ] `sylk.Open(projectPath)` returns:
  ```go
  type Sylk struct {
      // Storage
      SylkDir        *SylkDir
      GlobalMeta     *GlobalMeta
      NodeStore      *NodeBlockStore
      EdgeStore      *EdgeShardStore
      IVFIndex       *ivf.Index
      BleveStore     *BleveStore

      // Ingestion
      Embedder       Embedder
      Coordinator    *IngestionCoordinator
      BootIndexer    *BootIndexer

      // Query
      VectorSearcher *VectorSearcher
      TieredSearcher *TieredSearcher

      // Session
      SessionStore   *SessionStore
      ActiveSession  *Session
  }
  ```
- [ ] Load existing index if present
- [ ] Create new .sylk if not exists
- [ ] All components properly wired
- [ ] Graceful shutdown flushes all data

---

### TODO 8.2: Implement CLI Commands

**Description**: User-facing commands for indexing.

**Acceptance Criteria**:
- [ ] `sylk init` - Create .sylk directory
- [ ] `sylk index` - Run boot indexer
- [ ] `sylk index --watch` - Watch for changes
- [ ] `sylk search <query>` - Search from CLI
- [ ] `sylk status` - Show index stats
- [ ] `sylk session list` - List sessions
- [ ] `sylk session commit` - Commit active session
- [ ] Progress bars and timing output

---

### TODO 8.3: End-to-End Integration Test

**Description**: Full flow verification.

**Acceptance Criteria**:
- [ ] Test scenario:
  1. `sylk.Open()` new project
  2. Boot index 100 Go files
  3. Verify nodes in NodeStore
  4. Verify embeddings in IVF
  5. Verify documents in Bleve
  6. Query via TieredSearcher → find indexed code
  7. Agent ingests new document
  8. Agent queries → finds new document
  9. Session checkpoint
  10. Session commit
  11. New session → sees committed data
- [ ] All assertions pass
- [ ] Simulated crash recovery works
- [ ] No data loss scenarios

---

## Summary

| Phase | TODOs | Focus |
|-------|-------|-------|
| **1. Filesystem** | 1.1-1.8 | .sylk directory, binary storage layout, session dirs |
| **2. Data Structures** | 2.1-2.10 | Node/Edge compliance, binary formats |
| **3. Session Storage** | 3.1-3.8 | Per-session versioning, delta tracking, **per-version docs** |
| **4. Ingestion** | 4.1-4.6 | Boot index, embeddings, IVF build |
| **5. Query Pipeline** | 5.1-5.5 | IVF adapter, wiring, agent skills |
| **6. Query Visibility** | 6.1-6.2 | Session-aware queries, visibility rules |
| **7. Session Ops** | 7.1-7.3 | Checkpoint, Checkout, CommitToGlobal **(incl. doc merge)** |
| **8. Bootstrap & CLI** | 8.1-8.3 | Unified init, CLI commands, E2E test |

**Total: 43 TODOs**

---

## Critical Path

The minimum path to "agents can query real data":

```
1.1 (SylkDir)
  → 1.6 (IVF paths)
    → 4.5 (Wire embedder)
      → 4.2 (Boot indexer)
        → 4.6 (IVF build)
          → 5.1 (IVF adapter)
            → 5.2 (VectorSearcher)
              → 5.3 (VectorDBAdapter)
                → 5.4 (TieredSearcher)
                  → 5.5 (Agent skills)
                    → 8.1 (Bootstrap)
```

**Estimated critical path: 12 TODOs**

---

## Dependencies

```
Phase 1 (Filesystem) ──┬──→ Phase 2 (Data Structures)
                       │
                       └──→ Phase 3 (Session Storage)

Phase 2 ──→ Phase 4 (Ingestion) ──→ Phase 5 (Query)

Phase 3 ──→ Phase 6 (Visibility) ──→ Phase 7 (Session Ops)

Phase 5 + Phase 7 ──→ Phase 8 (Bootstrap)
```

---

*Last updated: 2025-01-26*
