# Knowledge Graph Database Implementation TODO

## Executive Summary

Implement the lock-free, sharded Knowledge Graph Database as specified in `DB.md`. This replaces the current SQLite-based storage with a high-performance, concurrent storage engine designed for multi-agent real-time knowledge accumulation.

**Key Architecture Decisions:**
- **Node.Content stores full content** - Knowledge Graph is self-contained (no dangling pointers to Bleve)
- **Bleve is a secondary search index** - Used by ALL agents for full-text search, but not source of truth
- **NodeID = Bleve DocID** - 1:1 mapping via `strconv.FormatUint(nodeID, 10)`
- **Lock-free reads** - `atomic.Pointer`, `sync.Map`, CAS-based `atomicEdgeList`
- **Sharded writes** - Edge shards aligned with node shards (`sourceID >> 16`)
- **Project-local storage** - `.sylk/` directory like `.git`

---

## Current State vs Target State

| Component | Current (SQLite) | Target (DB.md) | Gap |
|-----------|------------------|----------------|-----|
| Node ID | `string` | `uint32` (atomic counter) | Major rewrite |
| Node Storage | SQL rows + transactions | Append-only blocks, lock-free | Major rewrite |
| Node.Content | Stored in Bleve only | Stored in Node struct | Add field |
| Edge Storage | SQL with standard queries | `sync.Map` + `atomicEdgeList` | Major rewrite |
| Edge Sharding | None | Dynamic, `sourceID >> 16` | New implementation |
| WAL | Multiple unrelated WALs | Unified node WAL + per-shard edge WAL | New implementation |
| Vector Storage | HNSW in-memory + IVF | Shard-aligned with nodes | Integrate existing |
| File Layout | No persistent structure | `.sylk/knowledge/`, `.sylk/bleve/` | New implementation |
| Bleve Integration | Exists, partial use | Async secondary index, ALL agents | Wire up |

---

## Implementation Phases

### Phase 0: Architecture Decision

> **COMPLETED**: Greenfield approach selected.

- [x] **Decision: Implementation Strategy**
  - [x] Option A: Greenfield rewrite in `core/knowledge/graph/` alongside existing code ← SELECTED
  - [ ] ~~Option B: Incremental migration of existing SQLite stores~~
  - [ ] ~~Option C: Hybrid - keep SQLite for some, add new for others~~

---

### Phase 1: Lock-Free Primitives

**Location**: `core/knowledge/graph/`

#### 1.1 atomicEdgeList ✅

- [x] Create `core/knowledge/graph/atomic_edge_list.go`
- [x] Implement `atomicEdgeList` struct with `atomic.Pointer[[]Edge]`
- [x] Implement `Append(e Edge)` with CAS retry loop
- [x] Implement `AppendBatch(edges []Edge)` - single CAS for batch
- [x] Implement `Read() []Edge` - lock-free snapshot return
- [x] Unit tests for concurrent append contention
- [x] Unit tests for batch append atomicity
- [x] Benchmark: Read @ 0.24ns/op, AppendParallel @ 107μs/op (lock-free COW)

#### 1.2 Node ID Allocator ✅

- [x] Create `core/knowledge/graph/id_allocator.go`
- [x] Implement atomic `uint32` counter
- [x] Implement `NextID() uint32` with `atomic.AddUint32`
- [x] Implement persistence (save/restore counter on shutdown/startup)
- [x] Unit tests for monotonic increment
- [x] Unit tests for concurrent allocation
- [x] Benchmark: NextID @ 3.5ns/op

#### 1.3 Node Struct (New) ✅

- [x] Create `core/knowledge/graph/node.go`
- [x] Define `Node` struct per DB.md spec (uses `vectorgraphdb.Domain`/`NodeType`)
- [x] Implement `ContentHash` computation (maphash)
- [x] Implement binary serialization for persistence
- [x] Unit tests for serialization round-trip
- [x] Benchmark: Marshal @ 234ns/op, Unmarshal @ 274ns/op

#### 1.4 Edge Struct (New) ✅

- [x] Create `core/knowledge/graph/edge.go`
- [x] Define `Edge` struct per DB.md spec (uses `vectorgraphdb.EdgeType`)
- [x] Implement binary serialization for persistence
- [x] Unit tests for serialization round-trip
- [x] Benchmark: Marshal @ 21ns/op, Unmarshal @ 15ns/op

#### 1.5 edgeKey ✅

- [x] Create `core/knowledge/graph/edge_key.go`
- [x] Define `edgeKey` struct (9 bytes)
- [x] Unit tests for key uniqueness

---

### Phase 2: File Layout & WAL

**Location**: `core/knowledge/storage/`, `core/knowledge/wal/`

#### 2.1 Directory Structure

- [ ] Create `core/knowledge/storage/layout.go`
- [ ] Implement `.sylk/` directory creation:
  ```
  .sylk/
  ├── config.yaml
  ├── knowledge/
  │   ├── meta.json
  │   ├── nodes/
  │   │   ├── blocks/
  │   │   ├── index/
  │   │   └── wal/
  │   ├── edges/
  │   │   └── shard_NNNN/
  │   └── vectors/
  │       ├── shards/
  │       ├── graph/
  │       └── partitions/
  ├── bleve/
  │   └── index/
  └── sessions/
      └── {session_id}/
  ```
- [ ] Implement `InitializeStorage(projectRoot string) error`
- [ ] Implement `OpenStorage(projectRoot string) (*Storage, error)`
- [ ] Unit tests for directory creation
- [ ] Unit tests for idempotent initialization

#### 2.2 WAL Record Format

- [ ] Create `core/knowledge/wal/record.go`
- [ ] Define 16-byte WAL header:
  ```go
  type WALHeader struct {
      Length    uint32  // 4 bytes
      Type      uint8   // 1 byte
      Checksum  uint32  // 4 bytes
      Timestamp uint48  // 6 bytes (truncated)
      Reserved  uint8   // 1 byte
  }
  ```
- [ ] Define record types: `NodeInsert`, `EdgeInsert`, `EdgeUpdate`, `Checkpoint`, `ShardSeal`
- [ ] Implement checksum computation (CRC32)
- [ ] Unit tests for header serialization
- [ ] Unit tests for checksum validation

#### 2.3 Node WAL

- [ ] Create `core/knowledge/wal/node_wal.go`
- [ ] Implement `NodeWAL` struct with buffered writer
- [ ] Implement `Append(node Node) error` with fsync
- [ ] Implement `AppendBatch(nodes []Node) error` - single fsync
- [ ] Implement `Replay(fn func(Node) error) error` for recovery
- [ ] Implement `Checkpoint() error` for truncation
- [ ] Unit tests for append durability
- [ ] Unit tests for replay correctness
- [ ] Unit tests for crash recovery simulation

#### 2.4 Edge WAL (Per-Shard)

- [ ] Create `core/knowledge/wal/edge_wal.go`
- [ ] Implement `EdgeWAL` struct with per-shard files
- [ ] Implement `Append(edge Edge, sessionID string, agentID uint16) error`
- [ ] Implement `AppendBatch(edges []Edge, sessionID string, agentID uint16) error`
- [ ] Implement `Replay(fn func(Edge) error) error`
- [ ] Implement shard-specific file naming: `shard_NNNN/wal.bin`
- [ ] Unit tests for per-shard isolation
- [ ] Unit tests for batch fsync efficiency

---

### Phase 3: Node Storage

**Location**: `core/knowledge/graph/`

#### 3.1 NodeBlock

- [ ] Create `core/knowledge/graph/node_block.go`
- [ ] Implement `NodeBlock` struct:
  ```go
  type NodeBlock struct {
      nodes [NodeBlockSize]Node  // 4096 nodes
      count atomic.Uint32
  }
  ```
- [ ] Implement `Insert(node Node) error` with atomic count increment
- [ ] Implement `Get(localIdx uint32) *Node`
- [ ] Unit tests for block capacity
- [ ] Unit tests for concurrent insert

#### 3.2 NodeStore (New)

- [ ] Create `core/knowledge/graph/node_store.go`
- [ ] Implement `NodeStore` struct:
  ```go
  type NodeStore struct {
      blocks    atomic.Pointer[[]NodeBlock]
      nodeCount atomic.Uint32
      wal       *NodeWAL
  }
  ```
- [ ] Implement `AddNode(node Node) (uint32, error)` - allocate ID, WAL, insert
- [ ] Implement `GetNode(id uint32) *Node` - lock-free read
- [ ] Implement copy-on-write block array growth
- [ ] Unit tests for lock-free read consistency
- [ ] Unit tests for concurrent write correctness
- [ ] Benchmark: read throughput under write load

#### 3.3 Block Persistence

- [ ] Create `core/knowledge/storage/node_blocks.go`
- [ ] Implement `SaveBlock(blockIdx uint32, block *NodeBlock) error`
- [ ] Implement `LoadBlock(blockIdx uint32) (*NodeBlock, error)`
- [ ] Implement mmap for sealed blocks (read-only)
- [ ] Implement block sealing with checksum
- [ ] Unit tests for persistence round-trip
- [ ] Unit tests for mmap read performance

#### 3.4 Node Recovery

- [ ] Create `core/knowledge/recovery/node_recovery.go`
- [ ] Implement `RecoverNodes(wal *NodeWAL, store *NodeStore) error`
- [ ] Implement WAL replay into NodeStore
- [ ] Implement node count restoration
- [ ] Unit tests for crash recovery
- [ ] Integration tests for startup recovery flow

---

### Phase 4: Edge Storage (Sharded)

**Location**: `core/knowledge/graph/`

#### 4.1 EdgeShard

- [ ] Create `core/knowledge/graph/edge_shard.go`
- [ ] Implement `EdgeShard` struct:
  ```go
  type EdgeShard struct {
      edges    sync.Map  // map[edgeKey]*Edge
      outgoing sync.Map  // map[uint32]*atomicEdgeList
      incoming sync.Map  // map[uint32]*atomicEdgeList
      wal      *EdgeWAL
      walMu    sync.Mutex
      shardID  uint32
      dirty    atomic.Bool
  }
  ```
- [ ] Implement `UpsertEdge(edge Edge) error` - WAL + sync.Map + atomicEdgeList
- [ ] Implement `GetOutgoing(nodeID uint32) []Edge` - lock-free
- [ ] Implement `GetIncoming(nodeID uint32) []Edge` - lock-free
- [ ] Unit tests for lock-free read
- [ ] Unit tests for concurrent upsert

#### 4.2 Dynamic Shard Growth

- [ ] Create `core/knowledge/graph/edge_shards.go`
- [ ] Implement `EdgeShardManager` struct:
  ```go
  type EdgeShardManager struct {
      shards   atomic.Pointer[[]EdgeShard]
      shardsMu sync.RWMutex
  }
  ```
- [ ] Implement `GetOrCreateShard(idx uint32) *EdgeShard`
- [ ] Implement shard index calculation: `sourceID >> 16`
- [ ] Implement copy-on-write shard array growth
- [ ] Unit tests for on-demand shard creation
- [ ] Unit tests for shard routing correctness

#### 4.3 Edge Persistence

- [ ] Create `core/knowledge/storage/edge_shards.go`
- [ ] Implement `SaveShard(shard *EdgeShard) error`
- [ ] Implement `LoadShard(shardID uint32) (*EdgeShard, error)`
- [ ] Implement shard file format: `shard_NNNN/edges.bin`, `outgoing.idx`, `incoming.idx`
- [ ] Unit tests for persistence round-trip

#### 4.4 Edge Recovery

- [ ] Create `core/knowledge/recovery/edge_recovery.go`
- [ ] Implement `RecoverEdges(shardID uint32, wal *EdgeWAL, shard *EdgeShard) error`
- [ ] Implement per-shard WAL replay
- [ ] Implement outgoing/incoming index rebuild
- [ ] Unit tests for crash recovery
- [ ] Integration tests with node recovery

---

### Phase 5: Vector Storage Alignment

**Location**: `core/knowledge/vector/`

#### 5.1 VectorShard Alignment

- [ ] Create `core/knowledge/vector/shard.go`
- [ ] Implement `VectorShard` aligned with node shard boundaries
- [ ] Implement shard index: `nodeID >> 16` (same as edges)
- [ ] Unit tests for shard alignment consistency

#### 5.2 HNSW Adapter

- [ ] Create `core/knowledge/vector/hnsw_adapter.go`
- [ ] Adapt existing `core/vectorgraphdb/hnsw/` to use `uint32` IDs
- [ ] Implement ID mapping layer if needed
- [ ] Implement `Insert(nodeID uint32, vector []float32) error`
- [ ] Implement `Search(query []float32, k int) []uint32`
- [ ] Integration tests with existing HNSW

#### 5.3 IVF Integration

- [ ] Create `core/knowledge/vector/ivf_integration.go`
- [ ] Connect `core/vectorgraphdb/vamana/ivf/` to shard alignment
- [ ] Ensure IVF WAL coordinates with Knowledge Graph WAL
- [ ] Integration tests for combined recovery

---

### Phase 6: Bleve Integration

**Location**: `core/knowledge/bleve/`

#### 6.1 BleveAsyncIndexer

- [ ] Create `core/knowledge/bleve/async_indexer.go`
- [ ] Implement `BleveAsyncIndexer` struct:
  ```go
  type BleveAsyncIndexer struct {
      index    bleve.Index
      queue    chan IndexRequest
      wg       sync.WaitGroup
      workers  int
  }
  ```
- [ ] Implement `IndexAsync(req IndexRequest)` - non-blocking queue
- [ ] Implement worker pool for background indexing
- [ ] Implement graceful shutdown with queue drain
- [ ] Implement queue overflow handling (log warning, skip)
- [ ] Unit tests for async behavior
- [ ] Unit tests for graceful shutdown

#### 6.2 Document Mapper

- [ ] Create `core/knowledge/bleve/doc_mapper.go`
- [ ] Implement `NodeIDToDocID(nodeID uint32) string` - `strconv.FormatUint`
- [ ] Implement `DocIDToNodeID(docID string) (uint32, error)`
- [ ] Implement `NodeToDocument(node Node) Document`
- [ ] Unit tests for bidirectional mapping

#### 6.3 Agent Integration Points

- [ ] Audit all agents for Bleve usage:
  - [ ] `agents/archivalist/bleve_index.go` - already uses Bleve
  - [ ] `agents/librarian/` - needs Bleve integration
  - [ ] `agents/academic/` - needs Bleve integration
  - [ ] Other agents - identify needs
- [ ] Create unified Bleve query interface for all agents
- [ ] Ensure all agents use same Bleve index (project-scoped)
- [ ] Integration tests for multi-agent Bleve access

---

### Phase 7: KnowledgeGraph API

**Location**: `core/knowledge/graph/`

#### 7.1 KnowledgeGraph Struct

- [ ] Create `core/knowledge/graph/knowledge_graph.go`
- [ ] Implement `KnowledgeGraph` struct:
  ```go
  type KnowledgeGraph struct {
      nodes       *NodeStore
      edges       *EdgeShardManager
      vectors     *VectorIndex
      bleve       *BleveAsyncIndexer
      nodeWAL     *NodeWAL
      stats       atomic.Pointer[GraphStats]
      closed      atomic.Bool
  }
  ```
- [ ] Implement `Open(projectRoot string) (*KnowledgeGraph, error)`
- [ ] Implement `Close() error` with graceful shutdown
- [ ] Unit tests for lifecycle management

#### 7.2 Read Operations

- [ ] Create `core/knowledge/graph/read_ops.go`
- [ ] Implement `GetNode(id uint32) *Node` - lock-free
- [ ] Implement `GetOutgoingEdges(nodeID uint32) []Edge` - lock-free
- [ ] Implement `GetIncomingEdges(nodeID uint32) []Edge` - lock-free
- [ ] Implement `TraverseGraph(startID uint32, maxDepth int, visitor func) error`
- [ ] Implement `VectorSearch(query []float32, k int) []SearchResult`
- [ ] Benchmark: read throughput
- [ ] Benchmark: traversal performance

#### 7.3 Write Operations

- [ ] Create `core/knowledge/graph/write_ops.go`
- [ ] Implement `AddNode(node Node, embedding []float32) (uint32, error)`
- [ ] Implement `UpsertEdge(edge Edge) error`
- [ ] Implement `AddVector(nodeID uint32, embedding []float32) error`
- [ ] Unit tests for write correctness
- [ ] Unit tests for concurrent writes

#### 7.4 Concurrent Write Flow

- [ ] Create `core/knowledge/graph/concurrent_insert.go`
- [ ] Implement 6-parallel operation flow per DB.md:
  ```
  1. Node + Vector (WAL + nodeBlock + vectorShard)
  2. Bleve (async, fire-and-forget)
  3. Structural edges (AST analysis + batch WAL + batch CAS)
  4. Temporal edges (vector search + batch WAL + batch CAS)
  5. Cross-domain edges (domain linking + batch WAL + batch CAS)
  6. Semantic edges (citation analysis + batch WAL + batch CAS)
  ```
- [ ] Implement `sync.WaitGroup` coordination
- [ ] Implement error aggregation
- [ ] Benchmark: insert latency (target: ~1.75ms per DB.md)
- [ ] Benchmark: throughput with concurrent agents

---

### Phase 8: Migration & Integration

**Location**: Various

#### 8.1 SQLite Export

- [ ] Create `core/knowledge/migration/sqlite_export.go`
- [ ] Implement `ExportNodes(sqliteDB, newStore) error`
- [ ] Implement `ExportEdges(sqliteDB, newStore) error`
- [ ] Implement ID mapping (string → uint32)
- [ ] Implement progress reporting
- [ ] Unit tests for data integrity

#### 8.2 Agent Integration

- [ ] Update `agents/archivalist/` to use new KnowledgeGraph
- [ ] Update `agents/librarian/` to use new KnowledgeGraph
- [ ] Update `agents/academic/` to use new KnowledgeGraph
- [ ] Ensure all agents share single KnowledgeGraph instance per project
- [ ] Integration tests for multi-agent scenarios

#### 8.3 Session Integration

- [ ] Create `core/session/knowledge_session.go`
- [ ] Connect session management to Knowledge Graph
- [ ] Implement session-scoped views if needed
- [ ] Implement `SessionID` attribution on writes
- [ ] Integration tests for session isolation

#### 8.4 Deprecation

- [ ] Mark `core/vectorgraphdb/nodes.go` (`NodeStore`) as deprecated
- [ ] Mark `core/vectorgraphdb/edges.go` (`EdgeStore`) as deprecated
- [ ] Create `core/vectorgraphdb/DEPRECATED.md` with migration guide
- [ ] Add deprecation warnings to old code paths
- [ ] Plan removal timeline

---

## Recovery & Durability

### Startup Recovery Flow

- [ ] Implement full recovery sequence per DB.md:
  1. Load metadata (`meta.json`)
  2. Load sealed shards (parallel, mmap)
  3. Load node index shards (parallel)
  4. Replay node WAL
  5. Load edge shards (parallel)
  6. Replay edge WALs (per-shard)
  7. Load vector index
  8. Replay vector WAL
  9. Checkpoint WALs
  10. Ready for operations
- [ ] Integration test: full recovery flow
- [ ] Benchmark: recovery time for various corpus sizes

### Crash Recovery Guarantees

- [ ] Verify: Every write logged to WAL before returning
- [ ] Verify: WAL fsync'd periodically (configurable)
- [ ] Verify: Sealed shards fsync'd at seal time
- [ ] Verify: WAL replay is idempotent
- [ ] Test: Crash during node insert
- [ ] Test: Crash during edge insert
- [ ] Test: Crash during shard seal

---

## Performance Targets

Per DB.md spec:

| Operation | Target (p99) | Notes |
|-----------|--------------|-------|
| GetNode | < 100ns | Lock-free |
| GetOutgoingEdges | < 2μs | Lock-free |
| AddNode (new) | < 10μs | Index + WAL |
| UpsertEdge | < 5μs | WAL + CAS |
| Traverse (depth=3) | < 100μs | Depends on branching |
| VectorSearch (k=20) | < 50ms | IVF + reranking |

| Throughput (8 agents) | Target |
|-----------------------|--------|
| Mixed reads | > 1M ops/sec |
| Mixed writes | > 100K ops/sec |
| Node creation | > 50K ops/sec |
| Edge upserts | > 200K ops/sec |

- [ ] Create benchmark suite for all targets
- [ ] CI integration for performance regression detection

---

## Testing Strategy

### Unit Tests

- [ ] All lock-free primitives: atomicEdgeList, NodeStore, EdgeShard
- [ ] All persistence: WAL, block files, shard files
- [ ] All serialization: Node, Edge, WAL records

### Integration Tests

- [ ] Multi-agent concurrent access
- [ ] Recovery after simulated crash
- [ ] Shard growth under load
- [ ] Bleve async indexing lag

### Stress Tests

- [ ] 8+ concurrent writers
- [ ] 100K+ nodes, 1M+ edges
- [ ] Recovery from large WAL

---

## Dependencies

### External Libraries

- [x] `bleve` - Full-text search (already in use)
- [x] `gonum` - Vector math (already in use)
- [ ] Consider: `xxhash` for ContentHash (faster than SHA256)

### Internal Dependencies

- [x] `core/vectorgraphdb/hnsw/` - HNSW index (adapt, don't replace)
- [x] `core/vectorgraphdb/vamana/ivf/` - IVF index (integrate)
- [x] `core/vectorgraphdb/types.go` - Domain, NodeType, EdgeType (reuse)

---

## Notes

- **No SQLite extensions** - Per CLAUDE.md, FTS5/FTS4/FTS3 are BANNED
- **Bleve for full-text** - This is the approved search technology
- **Content in Node** - Ensures no dangling pointers, KG is self-contained
- **All agents use Bleve** - Single shared index per project
- **Project-local storage** - `.sylk/` like `.git`, not `~/.sylk/`

---

## References

- `DB.md` - Full architecture specification
- `TODO.md` - Agent implementation tasks
- `CLAUDE.md` - Project constraints (banned extensions)

---

*Last updated: 2025-01-24*
