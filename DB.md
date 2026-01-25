# Sylk Knowledge Graph Database Architecture

## Overview

Sylk is a multi-agent knowledge graph database designed for real-time, conversation-driven knowledge accumulation. Multiple agents concurrently read and write to a shared graph during user sessions, requiring high throughput, low latency, and strong durability guarantees.

**Design Principles:**
- Robustness over simplicity
- Performance over ease of implementation
- Lock-free reads where possible
- Sharded writes to eliminate contention
- Append-only structures for crash safety

---

## System Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              AGENT LAYER                                     │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐           │
│  │ Agent A │  │ Agent B │  │ Agent C │  │ Agent D │  │ Agent E │           │
│  │ (code)  │  │ (search)│  │ (papers)│  │ (chat)  │  │ (tools) │           │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘           │
│       │            │            │            │            │                 │
└───────┼────────────┼────────────┼────────────┼────────────┼─────────────────┘
        │            │            │            │            │
        └────────────┴────────────┴────────────┴────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          KNOWLEDGE GRAPH API                                 │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         READ OPERATIONS                               │   │
│  │  • GetNode(id) ────────────────────────────── Lock-free              │   │
│  │  • FindNode(canonicalKey) ─────────────────── Sharded lock (1/64)    │   │
│  │  • GetOutgoingEdges(nodeID) ───────────────── Sharded lock (1/64)    │   │
│  │  • GetIncomingEdges(nodeID) ───────────────── Sharded lock (1/64)    │   │
│  │  • TraverseGraph(start, depth) ────────────── Multi-shard, parallel  │   │
│  │  • VectorSearch(query, k) ─────────────────── IVF index              │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                        WRITE OPERATIONS                               │   │
│  │  • AddNode(canonicalKey, data) ────────────── Sharded + atomic ID    │   │
│  │  • UpsertEdge(src, dst, type, weight) ─────── Sharded lock (1/64)    │   │
│  │  • UpdateEdgeWeight(src, dst, type, weight) ─ Sharded lock (1/64)    │   │
│  │  • AddVector(nodeID, embedding) ───────────── IVF index              │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           STORAGE ENGINE                                     │
│                                                                              │
│  ┌────────────────────┐  ┌────────────────────┐  ┌────────────────────┐    │
│  │    NODE STORE      │  │    EDGE STORE      │  │   VECTOR STORE     │    │
│  │                    │  │                    │  │                    │    │
│  │  • Append-only     │  │  • Dynamic shards  │  │  • IVF + BBQ       │    │
│  │  • Lock-free reads │  │  • Lock-free R/W   │  │  • Sharded storage │    │
│  │  • Block-allocated │  │  • Per-shard WAL   │  │  • Vamana graph    │    │
│  └────────────────────┘  └────────────────────┘  └────────────────────┘    │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         SHARD ALIGNMENT                               │   │
│  │                                                                       │   │
│  │  NumShards = ⌈TotalNodes / 65536⌉  (derived from 16-bit addressing)  │   │
│  │                                                                       │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐  ┌────────────┐      │   │
│  │  │  Shard 0   │  │  Shard 1   │  │  Shard 2   │  │    ...     │      │   │
│  │  │ 0-65535    │  │65536-131071│  │131072-...  │  │  (grows)   │      │   │
│  │  │            │  │            │  │            │  │            │      │   │
│  │  │ NodeShard  │  │ NodeShard  │  │ NodeShard  │  │ NodeShard  │      │   │
│  │  │ EdgeShard  │  │ EdgeShard  │  │ EdgeShard  │  │ EdgeShard  │      │   │
│  │  │VectorShard │  │VectorShard │  │VectorShard │  │VectorShard │      │   │
│  │  └────────────┘  └────────────┘  └────────────┘  └────────────┘      │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                              WAL LAYER                                │   │
│  │                                                                       │   │
│  │  ┌─────────┐  ┌─────────────────────────────────────────────────┐    │   │
│  │  │ Node    │  │         Edge WAL (one per edge shard)           │    │   │
│  │  │ WAL     │  │  [0] [1] [2] ... [N]  (N = current shard count) │    │   │
│  │  └─────────┘  └─────────────────────────────────────────────────┘    │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Data Model

### Domains

The knowledge graph is partitioned into three domains, each with its own set of valid node types:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                               DOMAINS                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────┐  ┌─────────────────────┐  ┌─────────────────────┐  │
│  │     DomainCode      │  │    DomainHistory    │  │   DomainAcademic    │  │
│  │                     │  │                     │  │                     │  │
│  │  Code entities,     │  │  Conversation and   │  │  External knowledge │  │
│  │  symbols, files,    │  │  session history,   │  │  papers, docs,      │  │
│  │  modules            │  │  agent actions      │  │  web content        │  │
│  └─────────────────────┘  └─────────────────────┘  └─────────────────────┘  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Node Types

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              NODE TYPES                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  DomainCode:                                                                 │
│  ┌─────────┬─────────┬──────────┬─────────┬───────────┬───────────────────┐ │
│  │  File   │ Package │ Function │ Method  │  Struct   │    Interface      │ │
│  └─────────┴─────────┴──────────┴─────────┴───────────┴───────────────────┘ │
│  ┌─────────┬─────────┬──────────┐                                           │
│  │Variable │Constant │  Import  │                                           │
│  └─────────┴─────────┴──────────┘                                           │
│                                                                              │
│  DomainHistory:                                                              │
│  ┌─────────────┬─────────┬──────────┬─────────┬───────────┐                 │
│  │HistoryEntry │ Session │ Workflow │ Outcome │ Decision  │                 │
│  └─────────────┴─────────┴──────────┴─────────┴───────────┘                 │
│                                                                              │
│  DomainAcademic:                                                             │
│  ┌─────────┬───────────────┬──────────────┬─────────┬───────────────┐       │
│  │  Paper  │ Documentation │ BestPractice │   RFC   │ StackOverflow │       │
│  └─────────┴───────────────┴──────────────┴─────────┴───────────────┘       │
│  ┌─────────┬──────────┐                                                     │
│  │BlogPost │ Tutorial │                                                     │
│  └─────────┴──────────┘                                                     │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Edge Types

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              EDGE TYPES                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  STRUCTURAL (Code Topology):                                                 │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ Calls ↔ CalledBy    │ Imports ↔ ImportedBy  │ Implements ↔ ImplementedBy│ │
│  │ Embeds              │ HasField              │ HasMethod                 │ │
│  │ Defines ↔ DefinedIn │ Returns               │ Receives                  │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  TEMPORAL (History):                                                         │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ ProducedBy          │ ResultedIn            │ SimilarTo                 │ │
│  │ FollowedBy          │ Supersedes            │                           │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  CROSS-DOMAIN (Linking):                                                     │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ Modified            │ Created               │ Deleted                   │ │
│  │ BasedOn             │ References            │ ValidatedBy               │ │
│  │ Documents           │ UsesLibrary           │ ImplementsPattern         │ │
│  │ Cites               │ RelatedTo             │                           │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  SEMANTIC (Agent-Derived):                                                   │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ SimilarTo           │ RelatedTo             │ DerivedFrom               │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Core Data Structures

### Node Structure

```go
type Node struct {
    ID          uint32            // Globally unique, assigned atomically
                                  // NOTE: NodeID = Bleve DocID (1:1 mapping)
    Domain      Domain            // Code, History, or Academic
    Type        NodeType          // Specific type within domain
    Name        string            // Human-readable name
    Path        string            // File path or URL (optional)
    Package     string            // Package/module (optional)
    Signature   string            // Function signature (optional)
    CreatedAt   uint64            // Unix nano timestamp
    SessionID   string            // Session that created this node (audit)
    CreatedBy   uint16            // Agent ID that created this node
}

// IMPORTANT: Append-only model
//   • No deduplication at write time
//   • No CanonicalKey lookup before insert
//   • Similar/newer content = new node (not update)
//   • Similarity determined at QUERY time, not write time
//
// Content storage:
//   • Full content stored in Bleve Document DB
//   • DocID = strconv.FormatUint(uint64(node.ID), 10)
//   • Node struct is lightweight (metadata + embedding reference)
```

### Edge Structure

```go
type Edge struct {
    SourceID    uint32            // Source node ID
    TargetID    uint32            // Target node ID
    Type        EdgeType          // Relationship type (uint8)
    Weight      float32           // Computed weight (last-writer-wins)
    SessionID   string            // Session that created/last updated (audit)
    AgentID     uint16            // Agent that created/last updated
    CreatedAt   uint64            // Unix nano timestamp (immutable)
    UpdatedAt   uint64            // Unix nano timestamp (mutable)
}

// Edge key for deduplication:
type edgeKey struct {
    src  uint32
    dst  uint32
    typ  uint8   // EdgeType compressed
}
// Total: 9 bytes, fits in register
```

### Knowledge Graph Structure

```go
const (
    // NodeShardSize derived from 16-bit local addressing within a shard
    // This is the ONLY constant - everything else derives from it
    NodeShardSize = 1 << 16               // 65536 nodes per shard (2^16)
    NodeBlockSize = 4096                  // Nodes per block (page-aligned)
)

type KnowledgeGraph struct {
    // === NODE STORAGE (append-only, lock-free reads) ===
    nodeBlocks    atomic.Pointer[[]NodeBlock]  // Copy-on-write block array
    nodeCount     atomic.Uint32                 // Next node ID
    nodeIndex     *ShardedMap[string, uint32]   // canonical_key → node_id
    
    // === EDGE STORAGE (dynamic, aligned with node shards) ===
    // NumEdgeShards = NumNodeShards = ⌈TotalNodes / NodeShardSize⌉
    // Edge shard N contains edges where sourceID ∈ [N×65536, (N+1)×65536)
    edgeShards    atomic.Pointer[[]EdgeShard]  // Grows with node shards
    edgeShardsMu  sync.RWMutex                  // For adding new shards
    
    // === VECTOR STORAGE ===
    vectorIndex   *IVFIndex                     // Existing IVF implementation
    
    // === DURABILITY ===
    nodeWAL       *WALSegment
    
    // === METADATA ===
    stats         atomic.Pointer[GraphStats]
    
    // === LIFECYCLE ===
    closed        atomic.Bool
}

type NodeBlock struct {
    nodes         [NodeBlockSize]Node
    count         atomic.Uint32                 // Valid nodes in this block
}

// EdgeShard uses lock-free data structures for concurrent access.
// No arbitrary stripe count - contention handled at per-source-node granularity.
type EdgeShard struct {
    // Lock-free primary storage (sync.Map handles internal synchronization)
    edges      sync.Map  // map[edgeKey]*Edge
    
    // Per-source-node edge lists (lock-free reads, CAS writes)
    outgoing   sync.Map  // map[uint32]*atomicEdgeList
    
    // Per-target-node edge lists (for incoming traversal)
    incoming   sync.Map  // map[uint32]*atomicEdgeList
    
    // WAL for durability (single writer, append-only)
    wal        *WALSegment
    walMu      sync.Mutex
    
    // Shard metadata
    shardID    uint32
    dirty      atomic.Bool
}

// atomicEdgeList provides lock-free reads with copy-on-write appends.
// No fixed capacity - grows dynamically with edges.
type atomicEdgeList struct {
    list atomic.Pointer[[]Edge]
}

func (a *atomicEdgeList) Append(e Edge) {
    for {
        old := a.list.Load()
        var newList []Edge
        if old != nil {
            newList = make([]Edge, len(*old)+1)
            copy(newList, *old)
            newList[len(*old)] = e
        } else {
            newList = []Edge{e}
        }
        if a.list.CompareAndSwap(old, &newList) {
            return
        }
        // CAS failed (concurrent write), retry with new snapshot
    }
}

func (a *atomicEdgeList) Read() []Edge {
    ptr := a.list.Load()
    if ptr == nil {
        return nil
    }
    return *ptr  // Lock-free, returns immutable snapshot
}

// Edge shard assignment - derived from node shard boundaries
func (kg *KnowledgeGraph) edgeShardForSource(sourceID uint32) *EdgeShard {
    shardIdx := sourceID >> 16  // sourceID / NodeShardSize
    return kg.getOrCreateEdgeShard(shardIdx)
}

func (kg *KnowledgeGraph) getOrCreateEdgeShard(idx uint32) *EdgeShard {
    shards := kg.edgeShards.Load()
    if shards != nil && idx < uint32(len(*shards)) {
        return &(*shards)[idx]
    }
    
    // Need to grow - acquire write lock
    kg.edgeShardsMu.Lock()
    defer kg.edgeShardsMu.Unlock()
    
    // Double-check after acquiring lock
    shards = kg.edgeShards.Load()
    if shards != nil && idx < uint32(len(*shards)) {
        return &(*shards)[idx]
    }
    
    // Grow shard array
    newLen := idx + 1
    newShards := make([]EdgeShard, newLen)
    if shards != nil {
        copy(newShards, *shards)
    }
    
    // Initialize new shard
    newShards[idx] = EdgeShard{
        shardID: idx,
        wal:     kg.createEdgeWAL(idx),
    }
    
    kg.edgeShards.Store(&newShards)
    return &newShards[idx]
}
```

---

## Sharded Map Implementation

```go
type ShardedMap[K comparable, V any] struct {
    shards [NumShards]mapShard[K, V]
}

type mapShard[K comparable, V any] struct {
    mu   sync.RWMutex
    data map[K]V
}

// Hash function for shard selection
func (sm *ShardedMap[K, V]) shardIndex(key K) uint32 {
    // FNV-1a hash, then mask
    h := fnv1a(key)
    return h & ShardMask
}

func (sm *ShardedMap[K, V]) shard(key K) *mapShard[K, V] {
    return &sm.shards[sm.shardIndex(key)]
}
```

---

## Operation Flows

### Node Creation Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          NODE CREATION FLOW                                  │
└─────────────────────────────────────────────────────────────────────────────┘

  Agent calls: AddNode(canonicalKey, nodeData)
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  1. Compute shard index from key        │
  │     shardIdx = hash(canonicalKey) & 63  │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  2. Acquire READ lock on shard          │
  │     shard.mu.RLock()                    │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  3. Check if key exists                 │──── EXISTS ────▶ Return existing ID
  │     id, ok := shard.data[canonicalKey]  │                  (release lock)
  └─────────────────────────────────────────┘
        │
        │ NOT EXISTS
        ▼
  ┌─────────────────────────────────────────┐
  │  4. Release read lock, acquire write    │
  │     shard.mu.RUnlock()                  │
  │     shard.mu.Lock()                     │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  5. Double-check (another thread may    │──── EXISTS ────▶ Return existing ID
  │     have created while we waited)       │                  (release lock)
  └─────────────────────────────────────────┘
        │
        │ STILL NOT EXISTS
        ▼
  ┌─────────────────────────────────────────┐
  │  6. Allocate node ID atomically         │
  │     newID := nodeCount.Add(1) - 1       │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  7. Ensure block capacity               │
  │     (grow blocks array if needed)       │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  8. Write node to block                 │
  │     blockIdx := newID / NodeBlockSize   │
  │     nodeIdx := newID % NodeBlockSize    │
  │     blocks[blockIdx].nodes[nodeIdx] = n │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  9. Update index                        │
  │     shard.data[canonicalKey] = newID    │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  10. Write to WAL                       │
  │      nodeWAL.Append(node)               │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  11. Release write lock                 │
  │      shard.mu.Unlock()                  │
  └─────────────────────────────────────────┘
        │
        ▼
  Return (newID, created=true)
```

### Edge Upsert Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           EDGE UPSERT FLOW                                   │
└─────────────────────────────────────────────────────────────────────────────┘

  Agent calls: UpsertEdge(src, dst, type, weight, agentID)
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  1. Compute shard from SOURCE node      │
  │     shardIdx = src & 63                 │
  │     (edges sharded by source for        │
  │      locality on outgoing traversals)   │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  2. Construct edge key                  │
  │     key = {src, dst, type}              │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  3. Acquire WRITE lock on shard         │
  │     shard.mu.Lock()                     │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  4. Check if edge exists                │
  │     edge, exists := shard.edges[key]    │
  └─────────────────────────────────────────┘
        │
        ├──── EXISTS ────────────────────────────────────────┐
        │                                                     │
        ▼                                                     ▼
  ┌─────────────────────────────────┐      ┌─────────────────────────────────┐
  │  5a. CREATE new edge            │      │  5b. UPDATE existing edge       │
  │                                 │      │                                 │
  │  edge = &Edge{                  │      │  edge.Weight = weight           │
  │    SourceID:  src,              │      │  edge.AgentID = agentID         │
  │    TargetID:  dst,              │      │  edge.UpdatedAt = now()         │
  │    Type:      type,             │      │                                 │
  │    Weight:    weight,           │      │  (CreatedAt unchanged)          │
  │    AgentID:   agentID,          │      │                                 │
  │    CreatedAt: now(),            │      └─────────────────────────────────┘
  │    UpdatedAt: now(),            │                        │
  │  }                              │                        │
  │                                 │                        │
  │  shard.edges[key] = edge        │                        │
  │  shard.outgoing[src] = append   │                        │
  │  shard.incoming[dst] = append   │                        │
  └─────────────────────────────────┘                        │
        │                                                     │
        └──────────────────────┬──────────────────────────────┘
                               │
                               ▼
  ┌─────────────────────────────────────────┐
  │  6. Write to shard WAL                  │
  │     shard.wal.Append(edge)              │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  7. Mark shard dirty                    │
  │     shard.dirty.Store(true)             │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  8. Release write lock                  │
  │     shard.mu.Unlock()                   │
  └─────────────────────────────────────────┘
        │
        ▼
  Return edge
```

### Node Read Flow (Lock-Free)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       NODE READ FLOW (LOCK-FREE)                             │
└─────────────────────────────────────────────────────────────────────────────┘

  Agent calls: GetNode(id)
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  1. Load blocks pointer atomically      │
  │     blocks := nodeBlocks.Load()         │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  2. Compute block and node index        │
  │     blockIdx := id / NodeBlockSize      │
  │     nodeIdx := id % NodeBlockSize       │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  3. Bounds check                        │──── OUT OF BOUNDS ────▶ Return nil
  │     if blockIdx >= len(blocks)          │
  └─────────────────────────────────────────┘
        │
        │ IN BOUNDS
        ▼
  ┌─────────────────────────────────────────┐
  │  4. Load block count atomically         │
  │     count := blocks[blockIdx].count     │
  │     if nodeIdx >= count                 │──── NOT YET WRITTEN ──▶ Return nil
  └─────────────────────────────────────────┘
        │
        │ VALID
        ▼
  ┌─────────────────────────────────────────┐
  │  5. Return pointer to node              │
  │     return &blocks[blockIdx].nodes[idx] │
  │                                         │
  │     (NO LOCKS, NO COPIES)               │
  └─────────────────────────────────────────┘
```

### Graph Traversal Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         GRAPH TRAVERSAL FLOW                                 │
└─────────────────────────────────────────────────────────────────────────────┘

  Agent calls: TraverseOutgoing(startID, maxDepth, visitor)
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  Initialize:                            │
  │    visited = map[uint32]struct{}        │
  │    queue = [startID]                    │
  │    depth = 0                            │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  WHILE queue not empty AND              │◀─────────────────────────┐
  │        depth < maxDepth                 │                          │
  └─────────────────────────────────────────┘                          │
        │                                                               │
        ▼                                                               │
  ┌─────────────────────────────────────────┐                          │
  │  FOR each nodeID in current level:      │◀──────────────────┐      │
  └─────────────────────────────────────────┘                   │      │
        │                                                        │      │
        ▼                                                        │      │
  ┌─────────────────────────────────────────┐                   │      │
  │  Skip if visited                        │── VISITED ────────┤      │
  │  visited[nodeID] = struct{}{}           │                   │      │
  └─────────────────────────────────────────┘                   │      │
        │                                                        │      │
        ▼                                                        │      │
  ┌─────────────────────────────────────────┐                   │      │
  │  Get node (LOCK-FREE)                   │                   │      │
  │  node := GetNode(nodeID)                │                   │      │
  └─────────────────────────────────────────┘                   │      │
        │                                                        │      │
        ▼                                                        │      │
  ┌─────────────────────────────────────────┐                   │      │
  │  Get edges (SINGLE SHARD LOCK)          │                   │      │
  │  edges := GetOutgoingEdges(nodeID)      │                   │      │
  └─────────────────────────────────────────┘                   │      │
        │                                                        │      │
        ▼                                                        │      │
  ┌─────────────────────────────────────────┐                   │      │
  │  FOR each edge:                         │                   │      │
  │    if !visitor(node, edge):             │── STOP ───────────┼──────┼──▶ Return
  │      return                             │                   │      │
  │    queue.append(edge.TargetID)          │                   │      │
  └─────────────────────────────────────────┘                   │      │
        │                                                        │      │
        └────────────────────────────────────────────────────────┘      │
        │                                                               │
        ▼                                                               │
  ┌─────────────────────────────────────────┐                          │
  │  depth++                                │                          │
  │  Advance queue to next level            │                          │
  └─────────────────────────────────────────┘                          │
        │                                                               │
        └───────────────────────────────────────────────────────────────┘
```

---

## Storage Architecture

### Shard Lifecycle

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          SHARD LIFECYCLE                                     │
└─────────────────────────────────────────────────────────────────────────────┘

                    ┌─────────────────────────────────────┐
                    │            ACTIVE SHARD             │
                    │                                     │
  Writes ──────────▶│  • Accepts appends                  │
                    │  • In-memory + WAL                  │
                    │  • Mutable                          │
                    │                                     │
                    └─────────────────────────────────────┘
                                     │
                                     │ Capacity reached
                                     │ (65536 vectors)
                                     ▼
                    ┌─────────────────────────────────────┐
                    │           SEALING PROCESS           │
                    │                                     │
                    │  1. Block new writes to shard       │
                    │  2. Flush all buffers               │
                    │  3. Write shard metadata            │
                    │  4. Compute checksums               │
                    │  5. fsync all files                 │
                    │  6. Checkpoint WAL                  │
                    │  7. Mark shard as sealed            │
                    │                                     │
                    └─────────────────────────────────────┘
                                     │
                                     │ Seal complete
                                     ▼
                    ┌─────────────────────────────────────┐
                    │           SEALED SHARD              │
                    │                                     │
  Reads ───────────▶│  • Read-only                        │
                    │  • mmap'd for zero-copy access      │
                    │  • Immutable (safe concurrent read) │
                    │  • Checksummed for integrity        │
                    │                                     │
                    └─────────────────────────────────────┘


  STATE DIAGRAM (Node Shards):

  ┌──────────┐      capacity       ┌─────────┐       complete      ┌────────┐
  │  ACTIVE  │ ──────────────────▶ │ SEALING │ ─────────────────▶  │ SEALED │
  └──────────┘                     └─────────┘                     └────────┘
       │                                                                │
       │ (only one active shard at a time)                              │
       │                                                                │
       │◀─────────────────── new shard created ─────────────────────────┘
```

### Node/Edge Shard Lifecycle Alignment

Edge shards are aligned with node shard boundaries but have different lifecycle semantics:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    NODE/EDGE SHARD LIFECYCLE ALIGNMENT                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  DERIVATION:                                                                 │
│    NumEdgeShards = NumNodeShards = ⌈TotalNodes / 65536⌉                     │
│    EdgeShard[N] contains edges where sourceID ∈ [N×65536, (N+1)×65536)      │
│    65536 = 2^16 (derived from 16-bit local node addressing)                 │
│                                                                              │
│  Time ──────────────────────────────────────────────────────────────────▶   │
│                                                                              │
│  NodeShard[0]:  ████████████████████ SEAL ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   │
│  EdgeShard[0]:  ████████████████████ ░░░░ ▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒   │
│                                      │                                       │
│  NodeShard[1]:                       ████████████████████ SEAL ░░░░░░░░░░   │
│  EdgeShard[1]:                       ████████████████████ ░░░░ ▒▒▒▒▒▒▒▒▒▒   │
│                                                           │                  │
│  NodeShard[2]:                                            ████████████████   │
│  EdgeShard[2]:                                            ████████████████   │
│                                                                              │
│  Legend:                                                                     │
│    ████ = Active (nodes: accepting inserts, edges: accepting writes)        │
│    ░░░░ = Node shard sealed (immutable, mmap'd)                             │
│    ▒▒▒▒ = Edge shard writable (nodes sealed, but edges can still be added)  │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  KEY INSIGHT: Edge shards NEVER fully seal.                                  │
│                                                                              │
│  Why? Edges can always be created between existing nodes:                    │
│    - User asks agent to find relationship between old code and new paper    │
│    - Agent creates edge from node in EdgeShard[0] to node in EdgeShard[2]   │
│    - The edge goes to EdgeShard[0] (source node's shard)                    │
│    - EdgeShard[0] must remain writable even though NodeShard[0] is sealed   │
│                                                                              │
│  COMPACTION (not sealing):                                                   │
│    Edge shards can be compacted (rewritten without tombstones) during       │
│    low-activity periods, but this is a maintenance operation, not sealing.  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Edge Shard Growth

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         EDGE SHARD GROWTH                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Edge shards are created on-demand when edges reference new node ranges:    │
│                                                                              │
│  Initial state (0 nodes):                                                    │
│    edgeShards = []                                                          │
│                                                                              │
│  After adding node 0:                                                        │
│    edgeShards = [EdgeShard{0}]     // Shard 0 created                       │
│                                                                              │
│  After adding node 65536:                                                    │
│    edgeShards = [EdgeShard{0}, EdgeShard{1}]  // Shard 1 created            │
│                                                                              │
│  Edge from node 100 to node 70000:                                          │
│    shardIdx = 100 >> 16 = 0                                                 │
│    Edge stored in EdgeShard[0]                                              │
│                                                                              │
│  Edge from node 70000 to node 100:                                          │
│    shardIdx = 70000 >> 16 = 1                                               │
│    Edge stored in EdgeShard[1]                                              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### File Layout

`.sylk/` is **project-local** (like `.git`). Each project has its own Knowledge Graph and session storage.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            FILE LAYOUT                                       │
└─────────────────────────────────────────────────────────────────────────────┘

  /path/to/project/                      # User's project root
  │
  ├── .sylk/                             # PROJECT-LOCAL (like .git)
  │   │
  │   ├── config.yaml                    # Project-level configuration
  │   │
  │   │
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │  KNOWLEDGE GRAPH & BLEVE (shared across all sessions in this project)
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │
  │   ├── knowledge/                     # Knowledge Graph (project-scoped)
  │   │   │
  │   │   ├── meta.json                  # Graph metadata, edge shard count
  │   │   │
  │   │   ├── nodes/
  │   │   │   ├── blocks/
  │   │   │   │   ├── block_0000.bin     # Nodes 0-4095
  │   │   │   │   ├── block_0001.bin     # Nodes 4096-8191
  │   │   │   │   └── ...
  │   │   │   ├── index/
  │   │   │   │   ├── shard_00.idx       # Canonical key → node ID
  │   │   │   │   └── ...
  │   │   │   └── wal/
  │   │   │       └── nodes.wal
  │   │   │
  │   │   ├── edges/                     # Dynamic shards (aligned w/ nodes)
  │   │   │   │
  │   │   │   │  # NumEdgeShards = ⌈TotalNodes / 65536⌉
  │   │   │   │  # EdgeShard[N] = edges where sourceID ∈ [N×65536, (N+1)×65536)
  │   │   │   │
  │   │   │   ├── shard_0000/            # Edges for nodes 0-65535
  │   │   │   │   ├── edges.bin
  │   │   │   │   ├── outgoing.idx
  │   │   │   │   ├── incoming.idx
  │   │   │   │   └── wal.bin
  │   │   │   │
  │   │   │   └── shard_NNNN/            # Created on-demand
  │   │   │       └── ...
  │   │   │
  │   │   └── vectors/
  │   │       ├── shards/
  │   │       │   ├── shard_0000/        # Aligned with node shards
  │   │       │   │   ├── vectors.bin
  │   │       │   │   ├── bbq.bin
  │   │       │   │   ├── norms.bin
  │   │       │   │   └── meta.bin
  │   │       │   └── ...
  │   │       ├── graph/
  │   │       │   ├── adjacency.bin      # Vamana graph
  │   │       │   └── medoid.bin
  │   │       ├── partitions/
  │   │       │   ├── centroids.bin      # IVF centroids
  │   │       │   └── assignments.bin
  │   │       └── wal/
  │   │           └── segment_NNNN.wal
  │   │
  │   ├── bleve/                         # Bleve Document DB (project-scoped)
  │   │   └── index/
  │   │
  │   │
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │  SESSIONS (isolated per-session storage)
  │   │  ═══════════════════════════════════════════════════════════════════
  │   │
  │   └── sessions/
  │       │
  │       ├── active -> ses_abc123       # Symlink to current session
  │       │
  │       └── {session_id}/              # Per-session isolated storage
  │           │
  │           ├── meta.json              # Session metadata
  │           │   # {
  │           │   #   "id": "ses_abc123",
  │           │   #   "created_at": "2025-01-24T12:00:00Z",
  │           │   #   "status": "active"
  │           │   # }
  │           │
  │           ├── state/                 # Session state
  │           │   ├── dag.json           # Architect's task DAG
  │           │   ├── orchestrator.json  # Pipeline scheduling state
  │           │   └── validation.json    # Session-wide Inspector/Tester
  │           │
  │           ├── staging/               # Pipeline staging (copy-on-write)
  │           │   └── {pipeline_id}/
  │           │       ├── files/         # Modified files (isolated)
  │           │       └── manifest.json  # What was changed
  │           │
  │           ├── pipelines/             # Pipeline execution state
  │           │   └── {pipeline_id}/
  │           │       ├── state.json     # TDD phase, loop count
  │           │       ├── inspector.json # Criteria, feedback
  │           │       ├── tester.json    # Tests, results
  │           │       └── artifacts.json # Worker outputs
  │           │
  │           ├── agents/                # Knowledge Agent contexts
  │           │   ├── librarian.ctx      # Context window state
  │           │   ├── academic.ctx
  │           │   ├── archivalist.ctx
  │           │   └── handoff/           # Handoff state if needed
  │           │       └── {agent}.json
  │           │
  │           ├── messages/              # Conversation history
  │           │   └── log.jsonl          # Append-only message log
  │           │
  │           └── wal/                   # Session-local WAL
  │               └── session.wal        # For crash recovery
  │
  ├── src/                               # Project source code
  ├── go.mod
  └── ...

  ═══════════════════════════════════════════════════════════════════════════
  SHARD ALIGNMENT (Knowledge Graph internal)
  ═══════════════════════════════════════════════════════════════════════════

  ┌────────────────────────────────────────────────────────────────────────┐
  │  NodeShard[N]   ←→  EdgeShard[N]   ←→  VectorShard[N]                 │
  │                                                                        │
  │  All three use the same boundary: N × 65536 to (N+1) × 65536 - 1      │
  │  This alignment enables efficient co-location and recovery             │
  └────────────────────────────────────────────────────────────────────────┘
```

### Storage Scope Summary

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         STORAGE SCOPE SUMMARY                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SCOPE           │ LOCATION                    │ PURPOSE                    │
│  ────────────────┼─────────────────────────────┼────────────────────────────│
│  Project-Global  │ .sylk/knowledge/            │ Knowledge Graph            │
│                  │ .sylk/bleve/                │ Full-text search index     │
│                  │                             │                            │
│  Per-Session     │ .sylk/sessions/{id}/state/  │ DAG, orchestrator state    │
│                  │ .sylk/sessions/{id}/staging/│ Pipeline file isolation    │
│                  │ .sylk/sessions/{id}/agents/ │ Agent context windows      │
│                  │ .sylk/sessions/{id}/messages│ Conversation history       │
│                  │                             │                            │
│  Per-Pipeline    │ .../staging/{pipeline_id}/  │ Isolated file changes      │
│                  │ .../pipelines/{pipeline_id}/│ TDD state, artifacts       │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  WHY PROJECT-LOCAL?                                                          │
│                                                                              │
│  • Knowledge Graph contains project-specific code understanding             │
│  • Different projects have different codebases, patterns, conventions       │
│  • Portable: clone repo + .sylk/ = full context                             │
│  • Isolation: projects don't pollute each other's knowledge                 │
│  • Like .git: natural boundary at project root                              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Write Operations

### NodeID = Bleve DocID

The Knowledge Graph and Bleve Document DB share a common identifier:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         NodeID = DocID                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  KNOWLEDGE GRAPH                           BLEVE DOCUMENT DB                 │
│  ┌─────────────────────────┐              ┌─────────────────────────┐       │
│  │  Node                   │              │  Document               │       │
│  │  ├── ID: 12345          │◄────────────►│  ├── DocID: "12345"     │       │
│  │  ├── Domain             │    1:1       │  ├── Content: "..."     │       │
│  │  ├── Type               │   mapping    │  ├── Path               │       │
│  │  ├── Name               │              │  └── Metadata           │       │
│  │  └── (no content)       │              └─────────────────────────┘       │
│  └─────────────────────────┘                                                │
│           │                                                                  │
│           │ embedding                                                        │
│           ▼                                                                  │
│  ┌─────────────────────────┐                                                │
│  │  Vector Store           │                                                │
│  │  (semantic search)      │                                                │
│  └─────────────────────────┘                                                │
│                                                                              │
│  BENEFITS:                                                                   │
│    • No DocID field needed on Node                                          │
│    • Direct lookup: NodeID → strconv.Itoa(NodeID) → Bleve document          │
│    • Enables fully concurrent writes (see below)                            │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Concurrent Write Flow

When indexing content, three independent write paths execute in parallel:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    CONCURRENT WRITE FLOW                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Agent indexes entity (e.g., function "ParseConfig")                        │
│                                                                              │
│                              │                                               │
│                              ▼                                               │
│              ┌───────────────────────────────┐                              │
│              │  nodeID = nodeCount.Add(1)    │  ◄── atomic allocation       │
│              │  embedding = embed(content)   │                              │
│              └───────────────────────────────┘                              │
│                              │                                               │
│          ┌───────────────────┼───────────────────┐                          │
│          │                   │                   │                          │
│          ▼                   ▼                   ▼                          │
│  ┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐            │
│  │ NODE + VECTOR    │ │ BLEVE DOC DB     │ │ EDGES            │            │
│  │ (goroutine 1)    │ │ (goroutine 2)    │ │ (goroutine 3)    │            │
│  │                  │ │                  │ │                  │            │
│  │ ┌──────────────┐ │ │ ┌──────────────┐ │ │ ┌──────────────┐ │            │
│  │ │ 1. Node WAL  │ │ │ │ 1. Bleve     │ │ │ │ 1. Query     │ │            │
│  │ │    .Append() │ │ │ │    .Index()  │ │ │ │    similar   │ │            │
│  │ └──────────────┘ │ │ └──────────────┘ │ │ │    nodes     │ │            │
│  │        │         │ │                  │ │ └──────────────┘ │            │
│  │        ▼         │ │                  │ │        │         │            │
│  │ ┌──────────────┐ │ │                  │ │        ▼         │            │
│  │ │ 2. nodeBlock │ │ │                  │ │ ┌──────────────┐ │            │
│  │ │    .Insert() │ │ │                  │ │ │ 2. Edge WAL  │ │            │
│  │ └──────────────┘ │ │                  │ │ │    .Append() │ │            │
│  │        │         │ │                  │ │ │    (per edge)│ │            │
│  │        ▼         │ │                  │ │ └──────────────┘ │            │
│  │ ┌──────────────┐ │ │                  │ │        │         │            │
│  │ │ 3. vector    │ │ │                  │ │        ▼         │            │
│  │ │    .Insert() │ │ │                  │ │ ┌──────────────┐ │            │
│  │ └──────────────┘ │ │                  │ │ │ 3. edgeShard │ │            │
│  │                  │ │                  │ │ │    .outgoing │ │            │
│  │                  │ │                  │ │ │    .incoming │ │            │
│  │                  │ │                  │ │ └──────────────┘ │            │
│  └────────┬─────────┘ └────────┬─────────┘ └────────┬─────────┘            │
│           │                    │                    │                       │
│           └────────────────────┴────────────────────┘                       │
│                              │                                               │
│                              ▼                                               │
│                    sync.WaitGroup.Wait()                                    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Write Path Details

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    THREE INDEPENDENT WRITE PATHS                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  PATH 1: NODE + VECTOR (Knowledge Graph)                                    │
│  ────────────────────────────────────────                                   │
│    Input:  nodeID, embedding, metadata                                      │
│    Steps:                                                                   │
│      1. nodeWAL.Append(NodeInsertRecord{...})     → durability             │
│      2. nodeBlocks[nodeID/4096].Insert(node)      → node storage           │
│      3. vectorShards[nodeID>>16].Insert(emb)      → vector storage         │
│    Output: Node visible for queries                                        │
│                                                                              │
│  PATH 2: BLEVE DOCUMENT DB                                                  │
│  ─────────────────────────                                                  │
│    Input:  nodeID (as DocID), content, metadata                            │
│    Steps:                                                                   │
│      1. Bleve.Index(Document{                                              │
│           DocID:   strconv.FormatUint(nodeID, 10),                         │
│           Content: content,                                                │
│           Path:    path,                                                   │
│           ...                                                              │
│         })                                                                 │
│    Output: Document searchable via full-text                               │
│                                                                              │
│  PATH 3: EDGES                                                              │
│  ────────────                                                               │
│    Input:  nodeID, embedding (for similarity query)                        │
│    Steps:                                                                   │
│      1. similarNodes = vectorSearch(embedding, k=N)  → find targets        │
│      2. For each targetID in similarNodes:                                 │
│         a. shardIdx = nodeID >> 16                                         │
│         b. edgeShards[shardIdx].wal.Append(EdgeInsert{...})               │
│         c. edgeShards[shardIdx].outgoing.Store(nodeID, edge)              │
│         d. edgeShards[shardIdx].incoming.Store(targetID, edge)            │
│    Output: Edges connecting new node to similar existing nodes             │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  WHY FULLY CONCURRENT?                                                       │
│                                                                              │
│    • All three paths only need nodeID (allocated atomically upfront)        │
│    • No path depends on another path's completion                           │
│    • Edge queries hit EXISTING nodes, not the new one being inserted        │
│    • Each path has its own WAL/storage (no shared locks)                    │
│                                                                              │
│  PERFORMANCE:                                                                │
│                                                                              │
│    Sequential: O(node) + O(bleve) + O(edges)                                │
│    Concurrent: O(max(node, bleve, edges))                                   │
│                                                                              │
│    With N edges and typical latencies:                                      │
│      Sequential: ~5ms + ~10ms + ~(N × 0.5ms) = 15ms + N×0.5ms              │
│      Concurrent: ~max(5ms, 10ms, N×0.5ms) = ~10ms (for small N)            │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Concurrent Edge Creation by Category

Edges come from different analyses that can run in parallel:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         EDGE TYPE CATEGORIES                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  STRUCTURAL (0-12)         TEMPORAL (50-54)                                 │
│  ├── Calls                 ├── ProducedBy                                   │
│  ├── CalledBy              ├── ResultedIn                                   │
│  ├── Imports               ├── SimilarTo                                    │
│  ├── ImportedBy            ├── FollowedBy                                   │
│  ├── Implements            └── Supersedes                                   │
│  ├── ImplementedBy                                                          │
│  ├── Embeds                CROSS-DOMAIN (100-108)                           │
│  ├── HasField              ├── Modified                                     │
│  ├── HasMethod             ├── Created                                      │
│  ├── Defines               ├── Deleted                                      │
│  ├── DefinedIn             ├── BasedOn                                      │
│  ├── Returns               ├── References                                   │
│  └── Receives              ├── ValidatedBy                                  │
│                            ├── Documents                                    │
│  SEMANTIC (150-151)        ├── UsesLibrary                                  │
│  ├── Cites                 └── ImplementsPattern                            │
│  └── RelatedTo                                                              │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SOURCE OF EACH CATEGORY:                                                    │
│                                                                              │
│  │ Category     │ Analysis Source          │ Independent? │                 │
│  ├──────────────┼──────────────────────────┼──────────────┤                 │
│  │ Structural   │ AST parsing              │ Yes          │                 │
│  │ Temporal     │ Vector similarity search │ Yes          │                 │
│  │ Cross-Domain │ Domain linking           │ Yes          │                 │
│  │ Semantic     │ Citation/relation        │ Yes          │                 │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Parallel Edge Analysis

Each edge category is computed by an independent analysis:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│               PARALLEL EDGE ANALYSIS                                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│          ┌───────────────────────────────┐                                  │
│          │  nodeID, content, embedding   │                                  │
│          └───────────────────────────────┘                                  │
│                         │                                                    │
│     ┌───────────────────┼───────────────────┬───────────────────┐           │
│     │                   │                   │                   │           │
│     ▼                   ▼                   ▼                   ▼           │
│  ┌────────────┐   ┌────────────┐   ┌────────────┐   ┌────────────┐         │
│  │ STRUCTURAL │   │ TEMPORAL   │   │CROSS-DOMAIN│   │  SEMANTIC  │         │
│  │ (goro 1)   │   │ (goro 2)   │   │ (goro 3)   │   │ (goro 4)   │         │
│  │            │   │            │   │            │   │            │         │
│  │ Parse AST: │   │ Vector     │   │ Find docs  │   │ Citation   │         │
│  │ • Calls    │   │ search:    │   │ matching   │   │ analysis:  │         │
│  │ • Imports  │   │ • SimilarTo│   │ this code: │   │ • Cites    │         │
│  │ • Implements│  │ • Supersedes│  │ • Documents│   │ • RelatedTo│         │
│  │ • HasField │   │            │   │ • BasedOn  │   │            │         │
│  │ • etc.     │   │            │   │ • etc.     │   │            │         │
│  └─────┬──────┘   └─────┬──────┘   └─────┬──────┘   └─────┬──────┘         │
│        │                │                │                │                 │
│        ▼                ▼                ▼                ▼                 │
│   []Edge           []Edge           []Edge           []Edge                │
│   (batch)          (batch)          (batch)          (batch)               │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Batched Edge Insertion (Single CAS per Category)

Instead of one CAS per edge, batch all edges from each category into a single atomic operation:

```go
// atomicEdgeList with batch support
type atomicEdgeList struct {
    list atomic.Pointer[[]Edge]
}

// Append adds a single edge (multiple CAS attempts if concurrent)
func (a *atomicEdgeList) Append(e Edge) {
    for {
        old := a.list.Load()
        newList := appendOne(old, e)
        if a.list.CompareAndSwap(old, &newList) {
            return
        }
        // CAS failed, retry
    }
}

// AppendBatch adds multiple edges in a SINGLE CAS operation
// This is the preferred method when adding multiple edges
func (a *atomicEdgeList) AppendBatch(edges []Edge) {
    if len(edges) == 0 {
        return
    }
    for {
        old := a.list.Load()
        newList := appendMany(old, edges)
        if a.list.CompareAndSwap(old, &newList) {
            return
        }
        // CAS failed (concurrent write), retry with new snapshot
    }
}

func appendMany(old *[]Edge, edges []Edge) []Edge {
    if old == nil {
        result := make([]Edge, len(edges))
        copy(result, edges)
        return result
    }
    result := make([]Edge, len(*old)+len(edges))
    copy(result, *old)
    copy(result[len(*old):], edges)
    return result
}
```

### Batched WAL Write

Similarly, batch WAL writes per category:

```go
// EdgeWAL with batch support
type EdgeWAL struct {
    mu     sync.Mutex
    writer *bufio.Writer
    file   *os.File
}

// AppendBatch writes multiple edge records in a single fsync
func (w *EdgeWAL) AppendBatch(edges []Edge, sessionID string, agentID uint16) error {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    // Write all records to buffer (no syscalls yet)
    for _, edge := range edges {
        record := EdgeInsertRecord{
            SourceID:  edge.SourceID,
            TargetID:  edge.TargetID,
            Type:      edge.Type,
            Weight:    edge.Weight,
            SessionID: sessionID,
            AgentID:   agentID,
            Timestamp: time.Now().UnixNano(),
        }
        if err := w.writeRecord(record); err != nil {
            return err
        }
    }
    
    // Single flush + fsync for entire batch
    if err := w.writer.Flush(); err != nil {
        return err
    }
    return w.file.Sync()
}
```

### Full Concurrent Edge Write Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│               CONCURRENT EDGE WRITE FLOW (BATCHED)                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Four goroutines analyze edges concurrently, then batch-write:              │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │ GOROUTINE 1: Structural Edges                                        │   │
│  │                                                                       │   │
│  │   1. ANALYZE: Parse AST, find calls/imports/etc.                     │   │
│  │      structuralEdges := analyzeStructure(content)                    │   │
│  │      // Returns: []Edge{{Calls, target1}, {Imports, target2}, ...}   │   │
│  │                                                                       │   │
│  │   2. BATCH WAL: Single fsync for all structural edges                │   │
│  │      edgeShard.wal.AppendBatch(structuralEdges, sessionID, agentID)  │   │
│  │                                                                       │   │
│  │   3. BATCH INSERT: Single CAS for outgoing map                       │   │
│  │      edgeShard.outgoing.LoadOrStore(nodeID, &atomicEdgeList{})       │   │
│  │      list.AppendBatch(structuralEdges)  // ONE CAS                   │   │
│  │                                                                       │   │
│  │   4. UPDATE INCOMING: One CAS per unique target                      │   │
│  │      for targetID := range uniqueTargets(structuralEdges):           │   │
│  │          incomingList.AppendBatch(edgesForTarget)                    │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │ GOROUTINE 2: Temporal Edges (Similarity)                             │   │
│  │                                                                       │   │
│  │   1. ANALYZE: Vector search for similar nodes                        │   │
│  │      similarNodes := vectorSearch(embedding, k=20)                   │   │
│  │      temporalEdges := []Edge{}                                       │   │
│  │      for _, match := range similarNodes:                             │   │
│  │          temporalEdges = append(temporalEdges, Edge{                 │   │
│  │              SourceID: nodeID,                                       │   │
│  │              TargetID: match.ID,                                     │   │
│  │              Type:     EdgeTypeSimilarTo,                            │   │
│  │              Weight:   match.Score,                                  │   │
│  │          })                                                          │   │
│  │                                                                       │   │
│  │   2. BATCH WAL + BATCH INSERT (same as above)                        │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │ GOROUTINE 3: Cross-Domain Edges                                      │   │
│  │                                                                       │   │
│  │   1. ANALYZE: Find related documents in other domains                │   │
│  │      crossDomainEdges := findCrossDomainLinks(nodeID, content)       │   │
│  │      // E.g., code → documentation, code → best practice             │   │
│  │                                                                       │   │
│  │   2. BATCH WAL + BATCH INSERT                                        │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │ GOROUTINE 4: Semantic Edges                                          │   │
│  │                                                                       │   │
│  │   1. ANALYZE: Citation and relation analysis                         │   │
│  │      semanticEdges := analyzeSemanticRelations(content)              │   │
│  │                                                                       │   │
│  │   2. BATCH WAL + BATCH INSERT                                        │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  PERFORMANCE COMPARISON:                                                     │
│                                                                              │
│  Scenario: 50 edges total (15 structural, 20 temporal, 10 cross, 5 semantic)│
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ NAIVE (per-edge CAS + per-edge WAL):                                   │ │
│  │                                                                         │ │
│  │   WAL writes:  50 × (serialize + write + fsync) = 50 × 500μs = 25ms   │ │
│  │   CAS ops:     50 × (load + copy + CAS) = 50 × 100ns = 5μs            │ │
│  │   Total:       ~25ms (dominated by fsync)                              │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ BATCHED (per-category CAS + per-category WAL):                         │ │
│  │                                                                         │ │
│  │   WAL writes:  4 × (serialize batch + write + fsync) = 4 × 600μs = 2.4ms│ │
│  │   CAS ops:     4 × (load + copy batch + CAS) = 4 × 200ns = 800ns      │ │
│  │   Total:       ~2.4ms (10x faster)                                     │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │ BATCHED + PARALLEL (4 goroutines):                                     │ │
│  │                                                                         │ │
│  │   All 4 categories execute concurrently                                │ │
│  │   Total: ~max(600μs, 600μs, 600μs, 600μs) ≈ 600μs (40x faster)        │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Updated Full Write Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│               COMPLETE CONCURRENT WRITE FLOW                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│                              │                                               │
│                              ▼                                               │
│              ┌───────────────────────────────┐                              │
│              │  nodeID = nodeCount.Add(1)    │                              │
│              │  embedding = embed(content)   │                              │
│              └───────────────────────────────┘                              │
│                              │                                               │
│      ┌───────────────────────┼───────────────────────┐                      │
│      │                       │                       │                      │
│      ▼                       ▼                       ▼                      │
│  ┌────────┐            ┌──────────┐          ┌─────────────┐               │
│  │ NODE + │            │  BLEVE   │          │   EDGES     │               │
│  │ VECTOR │            │  INDEX   │          │ (4 parallel)│               │
│  └────┬───┘            └────┬─────┘          └──────┬──────┘               │
│       │                     │                       │                       │
│       │                     │           ┌───────────┼───────────┐           │
│       │                     │           │           │           │           │
│       │                     │           ▼           ▼           ▼           │
│       │                     │       Structural  Temporal   Cross  Semantic │
│       │                     │       (batch)     (batch)    (batch) (batch) │
│       │                     │           │           │           │           │
│       │                     │           └───────────┴───────────┘           │
│       │                     │                       │                       │
│       └─────────────────────┴───────────────────────┘                       │
│                              │                                               │
│                              ▼                                               │
│                    sync.WaitGroup.Wait()                                    │
│                                                                              │
│  TOTAL PARALLELISM: 6 concurrent operations                                 │
│    1. Node + Vector (WAL + insert)                                          │
│    2. Bleve Index                                                           │
│    3. Structural edges (batch WAL + batch CAS)                              │
│    4. Temporal edges (batch WAL + batch CAS)                                │
│    5. Cross-domain edges (batch WAL + batch CAS)                            │
│    6. Semantic edges (batch WAL + batch CAS)                                │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Single Agent Insert Timeline

Timeline showing all operations for a single entity insert.

**Key insight: Bleve indexing is ASYNC (fire-and-forget).** The node is immediately searchable via vectors/edges in the Knowledge Graph. Bleve full-text search catches up asynchronously.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    SINGLE AGENT INSERT TIMELINE                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Time (μs)   0       500      1000     1500     2000                        │
│              │        │        │        │        │                          │
│              ▼        ▼        ▼        ▼        ▼                          │
│                                                                              │
│  SETUP      ┌────────┐                                                       │
│  (sync)     │allocate│                                                       │
│             │nodeID  │                                                       │
│             │embed() │                                                       │
│             └───┬────┘                                                       │
│                 │                                                            │
│                 │ fan-out                                                   │
│                 │                                                            │
│  ════════════════════════════════════════════════════════════════════════   │
│                 │                                                            │
│  NODE+VEC   ────┼──┬─────────────────┐                                       │
│  (goro 1)       │  │ WAL    nodeBlk  │ vecShard                             │
│                 │  │ 200μs   50μs    │  100μs                               │
│                 │  └─────────────────┴──────┐                                │
│                 │                      350μs│✓                               │
│                 │                           │                                │
│  BLEVE      ────┼──►──────────────────────────────────────────► (async)     │
│  (fire+forget)  │   Bleve.Index() runs in background                        │
│                 │   NOT on critical path                                    │
│                 │                                                            │
│  STRUCTURAL ────┼──┬───────────────────────┐                                 │
│  (goro 2)       │  │ AST parse   WAL  CAS  │                                 │
│                 │  │   500μs    200μs 50μs │                                 │
│                 │  └───────────────────────┴─┐                               │
│                 │                       750μs│✓                              │
│                 │                            │                               │
│  TEMPORAL   ────┼──┬─────────────────────────┴──┐                            │
│  (goro 3)       │  │ vecSearch    WAL     CAS   │  ◄── CRITICAL PATH        │
│                 │  │  1000μs     200μs    50μs  │                            │
│                 │  └────────────────────────────┴─┐                          │
│                 │                           1250μs│✓                         │
│                 │                                 │                          │
│  CROSS-DOM  ────┼──┬──────────────────────────┐   │                          │
│  (goro 4)       │  │ query     WAL     CAS    │   │                          │
│                 │  │  800μs   200μs    50μs   │   │                          │
│                 │  └──────────────────────────┴─┐ │                          │
│                 │                         1050μs│✓│                          │
│                 │                               │ │                          │
│  SEMANTIC   ────┼──┬────────────────┐           │ │                          │
│  (goro 5)       │  │ analyze WAL CAS│           │ │                          │
│                 │  │  300μs  200μs  │           │ │                          │
│                 │  └────────────────┴─┐         │ │                          │
│                 │                550μs│✓        │ │                          │
│                 │                     │         │ │                          │
│  ════════════════════════════════════════════════════════════════════════   │
│                 │                     │         │ │                          │
│  WAIT       ────┼─────────────────────┴─────────┴─┴──┐                       │
│                 │                              1250μs│                       │
│                 │                                    │                       │
│  COMPLETE   ────┴────────────────────────────────────┴───────────────────   │
│                                                                              │
│                                                                              │
│  TIMELINE ANALYSIS:                                                          │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                         │ │
│  │  Critical Path: SETUP (500μs) + TEMPORAL (1250μs) = 1750μs             │ │
│  │                                                                         │ │
│  │  Bleve is ASYNC - not on critical path:                                │ │
│  │    • Node+Vector:  350μs  (completes at  850μs)                        │ │
│  │    • Structural:   750μs  (completes at 1250μs)                        │ │
│  │    • Temporal:    1250μs  (completes at 1750μs) ← CRITICAL PATH       │ │
│  │    • Cross-dom:   1050μs  (completes at 1550μs)                        │ │
│  │    • Semantic:     550μs  (completes at 1050μs)                        │ │
│  │    • Bleve:       async   (catches up in background)                   │ │
│  │                                                                         │ │
│  │  Total wall-clock time: ~1.75ms                                        │ │
│  │  Speedup vs sequential: 7450μs / 1750μs = 4.3x                         │ │
│  │                                                                         │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Async Bleve Queue

Bleve indexing is fire-and-forget with a background worker:

```go
// BleveAsyncIndexer handles async document indexing
type BleveAsyncIndexer struct {
    index    bleve.Index
    queue    chan IndexRequest
    wg       sync.WaitGroup
    workers  int
}

type IndexRequest struct {
    DocID   string
    Content string
    Path    string
    Meta    map[string]any
}

func NewBleveAsyncIndexer(index bleve.Index, workers int, queueSize int) *BleveAsyncIndexer {
    b := &BleveAsyncIndexer{
        index:   index,
        queue:   make(chan IndexRequest, queueSize),
        workers: workers,
    }
    
    // Start background workers
    for i := 0; i < workers; i++ {
        b.wg.Add(1)
        go b.worker()
    }
    
    return b
}

// IndexAsync queues a document for async indexing (non-blocking)
func (b *BleveAsyncIndexer) IndexAsync(req IndexRequest) {
    select {
    case b.queue <- req:
        // Queued successfully
    default:
        // Queue full - log warning, document will be indexed on next full reindex
        // This is acceptable because Knowledge Graph is the source of truth
    }
}

func (b *BleveAsyncIndexer) worker() {
    defer b.wg.Done()
    for req := range b.queue {
        doc := Document{
            DocID:   req.DocID,
            Content: req.Content,
            Path:    req.Path,
            Meta:    req.Meta,
        }
        if err := b.index.Index(req.DocID, doc); err != nil {
            // Log error, but don't fail - KG is source of truth
            slog.Warn("bleve index failed", "docID", req.DocID, "err", err)
        }
    }
}

func (b *BleveAsyncIndexer) Close() {
    close(b.queue)
    b.wg.Wait()  // Wait for queue to drain
}
```

### Gantt View (With Async Bleve)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     GANTT VIEW (ASYNC BLEVE)                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Time:     0μs     500μs    1000μs   1500μs   2000μs                        │
│            ├────────┼────────┼────────┼────────┼────────┤                   │
│                                                                              │
│  SETUP:    ████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░                     │
│            │ alloc+embed │                                                   │
│                                                                              │
│  Node+Vec: ░░░░░░░░█████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░                     │
│                    │WAL+ins│                                                 │
│                                                                              │
│  Bleve:    ░░░░░░░░─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─►  (async, bg)       │
│                    │ fire-and-forget, not awaited │                         │
│                                                                              │
│  Struct:   ░░░░░░░░██████████████░░░░░░░░░░░░░░░░░░░░░░                     │
│                    │ AST+WAL+CAS │                                           │
│                                                                              │
│  Temporal: ░░░░░░░░██████████████████████░░░░░░░░░░░░░░  ◄── CRITICAL      │
│                    │  vecSearch+WAL+CAS  │                                   │
│                                                                              │
│  Cross:    ░░░░░░░░█████████████████████░░░░░░░░░░░░░░░                     │
│                    │ query+WAL+CAS │                                         │
│                                                                              │
│  Semantic: ░░░░░░░░██████████░░░░░░░░░░░░░░░░░░░░░░░░░░                     │
│                    │analyze+WAL│                                             │
│                                                                              │
│            ├────────┼────────┼────────┼────────┼────────┤                   │
│                                                                              │
│  RETURN:   ░░░░░░░░░░░░░░░░░░░░░░░░░░░░████                                 │
│                                        │DONE│ @ 1750μs                      │
│                                                                              │
│  Legend:   ░ = waiting    █ = executing    ─ ─ = async (not awaited)        │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Sequential vs Parallel Comparison (Async Bleve)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│              SEQUENTIAL vs PARALLEL COMPARISON                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SEQUENTIAL (naive):                                                         │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                         │ │
│  │  setup ──► node ──► vector ──► bleve ──► struct ──► temp ──► cross ──► │ │
│  │  500μs    200μs     150μs     3000μs    750μs     1250μs    1050μs     │ │
│  │                                                                         │ │
│  │  ──► semantic ──► DONE                                                  │ │
│  │       550μs                                                             │ │
│  │                                                                         │ │
│  │  Total: 7450μs                                                          │ │
│  │                                                                         │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  PARALLEL (concurrent, async Bleve):                                         │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                         │ │
│  │  setup ──┬──► node+vector ─────┐                                        │ │
│  │  500μs   │                     │                                        │ │
│  │          ├──► bleve ──────────►│ (async, fire-and-forget)               │ │
│  │          │                     │                                        │ │
│  │          ├──► structural ──────┤                                        │ │
│  │          ├──► temporal ────────┼──► DONE                                │ │
│  │          ├──► cross-domain ────┤      @ 1750μs                          │ │
│  │          └──► semantic ────────┘                                        │ │
│  │                                                                         │ │
│  │  Total: 500 + max(350, 750, 1250, 1050, 550) = 1750μs                  │ │
│  │                                                                         │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                         │ │
│  │  SPEEDUP: 7450μs / 1750μs = 4.3x                                       │ │
│  │                                                                         │ │
│  │  WHY ASYNC BLEVE IS SAFE:                                               │ │
│  │    • Knowledge Graph is the source of truth                            │ │
│  │    • Node is immediately searchable via:                               │ │
│  │      - Vector similarity (semantic search)                             │ │
│  │      - Graph edges (structural/relational queries)                     │ │
│  │    • Bleve provides supplementary full-text (keyword) search           │ │
│  │    • If Bleve queue drops items, next full reindex recovers them       │ │
│  │                                                                         │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### WAL Record Format

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          WAL RECORD FORMAT                                   │
└─────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────────────────────────────────────────┐
  │                         RECORD HEADER (16 bytes)                          │
  ├────────────┬────────────┬────────────┬────────────┬──────────────────────┤
  │  Length    │   Type     │  Checksum  │ Timestamp  │       Reserved       │
  │  (4 bytes) │  (1 byte)  │  (4 bytes) │  (6 bytes) │       (1 byte)       │
  └────────────┴────────────┴────────────┴────────────┴──────────────────────┘
  
  Record Types:
    0x01 = NodeInsert
    0x02 = EdgeInsert
    0x03 = EdgeUpdate
    0x04 = Checkpoint
    0x05 = ShardSeal

  ┌──────────────────────────────────────────────────────────────────────────┐
  │                      NODE INSERT RECORD                                   │
  ├────────────┬────────────┬────────────┬────────────┬──────────────────────┤
  │  NodeID    │  Domain    │   Type     │  NameLen   │       Name           │
  │  (4 bytes) │  (1 byte)  │  (1 byte)  │  (2 bytes) │     (variable)       │
  ├────────────┴────────────┴────────────┴────────────┴──────────────────────┤
  │  CanonicalKeyLen │ CanonicalKey │  PathLen  │   Path   │  ... more fields │
  └──────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────────────────────────────────────────┐
  │                      EDGE INSERT/UPDATE RECORD                            │
  ├────────────┬────────────┬────────────┬────────────┬──────────────────────┤
  │  SourceID  │  TargetID  │   Type     │   Weight   │      AgentID         │
  │  (4 bytes) │  (4 bytes) │  (1 byte)  │  (4 bytes) │      (2 bytes)       │
  └────────────┴────────────┴────────────┴────────────┴──────────────────────┘
```

---

## Concurrency Model

### Lock Hierarchy

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          LOCK HIERARCHY                                      │
└─────────────────────────────────────────────────────────────────────────────┘

  The new design minimizes explicit locking through lock-free data structures:

  Level 0 (Global - rare):
    └── edgeShardsMu: Only when growing edge shard array (new node shard created)

  Level 1 (Node Index Shards):
    ├── nodeIndex.shards[0].mu
    ├── nodeIndex.shards[1].mu
    ├── ...
    └── nodeIndex.shards[N].mu    (N = number of index shards, configurable)

  Level 2 (WAL - per shard):
    ├── nodeWAL.mu
    └── edgeShards[i].walMu       (one per edge shard)

  LOCK-FREE OPERATIONS (no explicit locks):
    ├── Node reads:     atomic.Pointer load (nodeBlocks)
    ├── Edge reads:     sync.Map Load (edges, outgoing, incoming)
    ├── Edge writes:    sync.Map Store + atomicEdgeList CAS
    └── Edge traversal: Lock-free iteration over atomicEdgeList snapshot

  RULES:
    1. Edge operations are lock-free (sync.Map + CAS)
    2. WAL locks are per-shard and only serialize WAL appends
    3. Node index shards use traditional RWMutex (canonical key dedup)
    4. Global lock only for structural changes (adding edge shards)
```

### Contention Analysis

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        CONTENTION ANALYSIS                                   │
└─────────────────────────────────────────────────────────────────────────────┘

  With lock-free edge storage (sync.Map + atomicEdgeList):

  EDGE WRITE OPERATIONS (Lock-Free):
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  Contention occurs ONLY when two agents write edges from the SAME      │
  │  source node simultaneously. This triggers CAS retry, not blocking.    │
  │                                                                         │
  │  Given:                                                                 │
  │    C = concurrent writers                                               │
  │    N = nodes with edges in shard                                        │
  │                                                                         │
  │  P(CAS retry) = P(two agents write from same source)                   │
  │               = C / N  (assuming uniform distribution)                  │
  │                                                                         │
  │  Example: 8 agents, 10,000 nodes with edges:                           │
  │    P(CAS retry) ≈ 8 / 10,000 = 0.08%                                   │
  │                                                                         │
  │  CAS retry cost: ~100ns (re-read + re-allocate + retry)                │
  │  Expected overhead per write: 0.08% × 100ns ≈ 0.08ns (negligible)      │
  │                                                                         │
  │  SCALING: As N grows, contention DECREASES (more source nodes)         │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  NODE READ OPERATIONS:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  LOCK-FREE: atomic.Pointer load                                        │
  │  Zero contention, zero waiting                                         │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  EDGE READ OPERATIONS (Lock-Free):
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  LOCK-FREE: sync.Map Load + atomicEdgeList.Read()                      │
  │                                                                         │
  │  Read returns immutable snapshot - never blocked by writers            │
  │  Writers use copy-on-write, readers see consistent view                │
  │                                                                         │
  │  Zero contention, zero waiting                                         │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  TRAVERSAL OPERATIONS (Lock-Free):
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  Each hop: atomicEdgeList.Read() returns snapshot                      │
  │  No locks held during traversal                                        │
  │  Traversal sees consistent snapshot at each node                       │
  │                                                                         │
  │  For traversal of depth D with branching factor B:                     │
  │    Operations: B^D atomicEdgeList.Read() calls                         │
  │    Locks held: 0 (all lock-free)                                       │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  WAL CONTENTION:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  WAL is the serialization point (one writer per shard at a time)       │
  │                                                                         │
  │  Given:                                                                 │
  │    S = number of edge shards = ⌈TotalNodes / 65536⌉                    │
  │    C = concurrent writers                                               │
  │                                                                         │
  │  P(WAL contention) = C / S  (assuming uniform shard distribution)      │
  │                                                                         │
  │  Example: 8 agents, 3 edge shards (up to 196K nodes):                  │
  │    P(WAL contention) ≈ 8 / 3 ≈ 2.7 writers per shard                  │
  │    Average WAL wait: ~500ns (disk buffer append)                       │
  │    Expected overhead: 500ns × (2.7 - 1) / 2.7 ≈ 315ns per write        │
  │                                                                         │
  │  SCALING: As data grows, more edge shards, less WAL contention         │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘
```

### Session Concurrency Model

The Knowledge Graph is a **global shared resource** accessed by multiple concurrent sessions. Each session has its own Knowledge Agent instances (Librarian, Academic, Archivalist), but all agents read/write to the same global graph.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      SESSION CONCURRENCY MODEL                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SESSION A                              SESSION B                            │
│  ┌────────────────────────────┐        ┌────────────────────────────┐       │
│  │ ┌──────────┐ ┌──────────┐  │        │ ┌──────────┐ ┌──────────┐  │       │
│  │ │Librarian │ │ Academic │  │        │ │Librarian │ │ Academic │  │       │
│  │ │(instance)│ │(instance)│  │        │ │(instance)│ │(instance)│  │       │
│  │ └────┬─────┘ └────┬─────┘  │        │ └────┬─────┘ └────┬─────┘  │       │
│  │      │            │        │        │      │            │        │       │
│  │ ┌────┴────────────┴─────┐  │        │ ┌────┴────────────┴─────┐  │       │
│  │ │     Archivalist       │  │        │ │     Archivalist       │  │       │
│  │ │     (instance)        │  │        │ │     (instance)        │  │       │
│  │ └───────────┬───────────┘  │        │ └───────────┬───────────┘  │       │
│  │             │              │        │             │              │       │
│  │  ┌──────────┴──────────┐   │        │  ┌──────────┴──────────┐   │       │
│  │  │ Pipelines (N)       │   │        │  │ Pipelines (M)       │   │       │
│  │  │ Inspector→Tester→   │   │        │  │ Inspector→Tester→   │   │       │
│  │  │ Engineer/Designer   │   │        │  │ Engineer/Designer   │   │       │
│  │  └─────────────────────┘   │        │  └─────────────────────┘   │       │
│  └────────────┬───────────────┘        └────────────┬───────────────┘       │
│               │                                      │                       │
│               │    All Knowledge Agents access       │                       │
│               │    the SAME global stores            │                       │
│               │                                      │                       │
│               └──────────────┬───────────────────────┘                       │
│                              │                                               │
│                              ▼                                               │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      GLOBAL (shared)                                 │    │
│  │                                                                      │    │
│  │   ┌─────────────────────────┐     ┌─────────────────────────┐       │    │
│  │   │     Knowledge Graph     │     │   Bleve Document DB     │       │    │
│  │   │                         │     │                         │       │    │
│  │   │  • sync.Map (lock-free) │     │  • Thread-safe indexer  │       │    │
│  │   │  • atomicEdgeList (CAS) │     │  • Concurrent R/W       │       │    │
│  │   │  • Per-shard WAL        │     │                         │       │    │
│  │   └─────────────────────────┘     └─────────────────────────┘       │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Why This Works

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    MULTI-SESSION CONCURRENCY GUARANTEES                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  1. LOCK-FREE DATA STRUCTURES                                                │
│     • sync.Map: Concurrent reads/writes without explicit locking            │
│     • atomicEdgeList: CAS-based appends, lock-free reads                    │
│     • Unlimited concurrent goroutines from any number of sessions           │
│                                                                              │
│  2. KNOWLEDGE IS INTENTIONALLY GLOBAL                                        │
│     • Session A indexes code file X → creates nodes                         │
│     • Session B queries for file X → finds Session A's nodes                │
│     • This is GOOD - sessions benefit from shared knowledge                 │
│                                                                              │
│  3. AUDIT ATTRIBUTION                                                        │
│     • Node.SessionID: Which session created this node                       │
│     • Edge.SessionID: Which session created/last updated this edge          │
│     • Node.CreatedBy / Edge.AgentID: Which agent within the session         │
│     • Full provenance chain: Session → Agent → Timestamp                    │
│                                                                              │
│  4. NO SESSION ISOLATION NEEDED                                              │
│     • Knowledge Graph is a shared resource by design                        │
│     • Sessions don't need private views                                     │
│     • Conflicts resolved by last-writer-wins (Edge.Weight)                  │
│                                                                              │
│  5. BLEVE IS THREAD-SAFE                                                     │
│     • Built-in support for concurrent indexing and searching                │
│     • No additional synchronization needed                                  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Session Attribution Queries

With SessionID tracked on nodes and edges, common audit queries become possible:

```go
// Find all nodes created by a specific session
func (kg *KnowledgeGraph) NodesBySession(sessionID string) []Node

// Find all edges created/updated by a specific session
func (kg *KnowledgeGraph) EdgesBySession(sessionID string) []Edge

// Get session activity summary
func (kg *KnowledgeGraph) SessionActivity(sessionID string) *SessionStats

// SessionStats provides audit information
type SessionStats struct {
    SessionID     string
    NodesCreated  int
    EdgesCreated  int
    EdgesUpdated  int
    FirstActivity time.Time
    LastActivity  time.Time
    AgentBreakdown map[uint16]int  // AgentID → operation count
}
```

---

## Recovery Process

### Startup Recovery Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         STARTUP RECOVERY FLOW                                │
└─────────────────────────────────────────────────────────────────────────────┘

  Application starts
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  1. Load metadata                       │
  │     - Read meta.json                    │
  │     - Verify version compatibility      │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  2. Load sealed shards (PARALLEL)       │
  │                                         │
  │     FOR each sealed shard:              │
  │       - mmap shard files                │
  │       - Verify checksums                │
  │       - Register in shard list          │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  3. Load node index shards (PARALLEL)   │
  │                                         │
  │     FOR each index shard (0..63):       │
  │       - Load from disk                  │
  │       - Build in-memory map             │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  4. Replay node WAL                     │
  │                                         │
  │     FOR each record in node WAL:        │
  │       - Apply to node blocks            │
  │       - Update node index               │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  5. Load edge shards (PARALLEL)         │
  │                                         │
  │     FOR each edge shard (0..63):        │
  │       - Load edges.bin                  │
  │       - Build outgoing/incoming indices │
  │       - Replay shard WAL                │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  6. Load vector index                   │
  │     - Load IVF partitions               │
  │     - Load Vamana graph                 │
  │     - Replay vector WAL                 │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  7. Checkpoint WALs                     │
  │     - Flush recovered state to disk     │
  │     - Truncate WAL files                │
  └─────────────────────────────────────────┘
        │
        ▼
  ┌─────────────────────────────────────────┐
  │  8. Ready for operations                │
  └─────────────────────────────────────────┘
```

### Crash Recovery Guarantees

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      CRASH RECOVERY GUARANTEES                               │
└─────────────────────────────────────────────────────────────────────────────┘

  DURABILITY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  • Every write is logged to WAL BEFORE returning to caller             │
  │  • WAL is fsync'd periodically (configurable interval)                 │
  │  • Sealed shards are fsync'd at seal time                              │
  │                                                                         │
  │  GUARANTEE: No acknowledged write is lost after crash                  │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  CONSISTENCY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  • Node IDs are allocated atomically (monotonic counter)               │
  │  • Edge keys are unique (map semantics)                                │
  │  • WAL replay is idempotent (same result if replayed multiple times)   │
  │                                                                         │
  │  GUARANTEE: Graph is always in consistent state after recovery         │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘

  ATOMICITY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │                                                                         │
  │  • Single node/edge operations are atomic                              │
  │  • Multi-operation transactions NOT supported (by design)              │
  │  • Partial writes detected via WAL checksums                           │
  │                                                                         │
  │  GUARANTEE: Each operation either fully completes or fully rolls back  │
  │                                                                         │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Agent Integration

### Agent ID Assignment

```go
const (
    AgentUnknown    uint16 = 0
    AgentUser       uint16 = 1
    AgentCodeSearch uint16 = 2
    AgentWebSearch  uint16 = 3
    AgentPaperSearch uint16 = 4
    AgentChat       uint16 = 5
    AgentTools      uint16 = 6
    AgentInference  uint16 = 7
    // ... extensible up to 65535
)
```

### Agent Write Pattern

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         AGENT WRITE PATTERN                                  │
└─────────────────────────────────────────────────────────────────────────────┘

  Example: Agent discovers a paper and its relationships

  ┌─────────────────────────────────────────────────────────────────────────┐
  │  Agent: PaperSearch (ID=4)                                              │
  │                                                                          │
  │  1. Fetch paper from URL                                                │
  │     url := "https://arxiv.org/abs/2301.00001"                           │
  │                                                                          │
  │  2. Create/find paper node                                              │
  │     paperID, created := kg.AddNode(                                      │
  │       canonicalKey: "doi:10.1234/paper",                                │
  │       node: Node{                                                        │
  │         Domain: DomainAcademic,                                         │
  │         Type:   NodeTypePaper,                                          │
  │         Name:   "Attention Is All You Need",                            │
  │         Path:   url,                                                    │
  │       },                                                                 │
  │     )                                                                    │
  │                                                                          │
  │  3. Find related code symbol (already in graph)                         │
  │     symbolID, _ := kg.FindNode("repo:path:Transformer:struct")          │
  │                                                                          │
  │  4. Create edge linking paper to code                                   │
  │     kg.UpsertEdge(                                                       │
  │       src:    paperID,                                                  │
  │       dst:    symbolID,                                                 │
  │       type:   EdgeTypeDocuments,                                        │
  │       weight: 0.95,                                                     │
  │       agent:  AgentPaperSearch,  // <-- Agent attribution               │
  │     )                                                                    │
  │                                                                          │
  │  5. Add vector embedding                                                │
  │     kg.vectorIndex.Insert(paperID, embedding)                           │
  │                                                                          │
  └─────────────────────────────────────────────────────────────────────────┘
```

### Concurrent Agent Scenario

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     CONCURRENT AGENT SCENARIO                                │
└─────────────────────────────────────────────────────────────────────────────┘

  Timeline:
  
  T=0ms    Agent A: AddNode("url:example.com/page1", ...) → creates node 1000
  T=0ms    Agent B: AddNode("url:example.com/page2", ...) → creates node 1001
  T=1ms    Agent C: FindNode("url:example.com/page1") → returns 1000
  T=1ms    Agent A: UpsertEdge(1000, 500, References, 0.8, A)
  T=2ms    Agent D: UpsertEdge(1000, 500, References, 0.9, D)  ← SAME EDGE!
  T=2ms    Agent B: TraverseOutgoing(1001, 3, visitor)
  T=3ms    Agent C: UpsertEdge(1000, 1001, SimilarTo, 0.7, C)

  Result at T=3ms:
  
  ┌─────────────────────────────────────────────────────────────────────────┐
  │  Node 1000 (created by Agent A)                                         │
  │    └── Edge to 500 (References)                                         │
  │        Weight: 0.9 (Agent D's update won - later timestamp)             │
  │        AgentID: D                                                        │
  │        UpdatedAt: T=2ms                                                  │
  │    └── Edge to 1001 (SimilarTo)                                         │
  │        Weight: 0.7                                                       │
  │        AgentID: C                                                        │
  │        CreatedAt: T=3ms                                                  │
  │                                                                          │
  │  Node 1001 (created by Agent B)                                         │
  │    └── (no outgoing edges yet)                                          │
  └─────────────────────────────────────────────────────────────────────────┘
```

---

## Performance Targets

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         PERFORMANCE TARGETS                                  │
└─────────────────────────────────────────────────────────────────────────────┘

  LATENCY (p99):
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Operation              │  Target      │  Notes                        │
  ├─────────────────────────┼──────────────┼───────────────────────────────┤
  │  GetNode                │  < 100ns     │  Lock-free, cache-friendly    │
  │  FindNode               │  < 1μs       │  Single shard lock            │
  │  AddNode (new)          │  < 10μs      │  Index + WAL                  │
  │  AddNode (exists)       │  < 1μs       │  Index lookup only            │
  │  UpsertEdge             │  < 5μs       │  Single shard lock + WAL      │
  │  GetOutgoingEdges       │  < 2μs       │  Single shard read lock       │
  │  Traverse (depth=3)     │  < 100μs     │  Depends on branching factor  │
  │  VectorSearch (k=20)    │  < 50ms      │  IVF + reranking              │
  └────────────────────────────────────────────────────────────────────────┘

  THROUGHPUT (8 agents):
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Operation              │  Target         │  Notes                     │
  ├─────────────────────────┼─────────────────┼────────────────────────────┤
  │  Mixed reads            │  > 1M ops/sec   │  Mostly lock-free          │
  │  Mixed writes           │  > 100K ops/sec │  Sharded contention        │
  │  Node creation          │  > 50K ops/sec  │  Index + WAL bound         │
  │  Edge upserts           │  > 200K ops/sec │  WAL bound                 │
  │  Vector inserts         │  > 10K ops/sec  │  IVF partitioning bound    │
  └────────────────────────────────────────────────────────────────────────┘

  MEMORY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Component              │  Size per unit  │  Notes                     │
  ├─────────────────────────┼─────────────────┼────────────────────────────┤
  │  Node (in-memory)       │  ~200 bytes     │  Varies with name/path     │
  │  Edge (in-memory)       │  ~32 bytes      │  Fixed size                │
  │  Node index entry       │  ~40 bytes      │  Key + ID                  │
  │  Vector (768-dim)       │  ~3KB raw       │  96 bytes BBQ compressed   │
  └────────────────────────────────────────────────────────────────────────┘

  RECOVERY:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Corpus Size            │  Recovery Time  │  Notes                     │
  ├─────────────────────────┼─────────────────┼────────────────────────────┤
  │  100K nodes, 1M edges   │  < 1 second     │  Parallel shard loading    │
  │  1M nodes, 10M edges    │  < 10 seconds   │  Parallel shard loading    │
  │  10M nodes, 100M edges  │  < 2 minutes    │  mmap + parallel WAL       │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Future Considerations

### Compaction

When edge shards accumulate too many updates, compaction rewrites the shard:
1. Create new shard file
2. Write only latest state (no tombstones)
3. Atomic swap of file handles
4. Delete old shard file

### Shard Splitting

If a single node has extremely high edge count (hot node), consider:
1. Detecting hot nodes (> 10K edges)
2. Splitting edges into sub-shards by target node
3. Transparent routing at query time

### Distributed Mode

For future multi-machine deployment:
1. Shard assignment to machines
2. Cross-machine edge routing
3. Consensus for node ID allocation
4. Eventually consistent replication

---

## References

- Vamana: DiskANN paper for graph-based ANN
- IVF: Inverted file indexing for vector search
- BBQ: Binary Quantization for compressed vectors
- LSM: Log-structured merge for append-only storage
- CRDT: Conflict-free replicated data types (inspiration for edge weights)
