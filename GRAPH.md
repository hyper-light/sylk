# Knowledge Graph Architecture

## Document Purpose

This document specifies the architecture for transforming Sylk's existing VectorGraphDB + Bleve infrastructure into a maximally robust, performant knowledge graph optimized for responsive multi-session terminal experiences.

**Design Philosophy**: No complexity constraints. Build the most correct, robust system possible.

---

## Table of Contents

1. [Current Architecture](#current-architecture)
2. [Target Architecture](#target-architecture)
3. [Storage Layer Integration](#storage-layer-integration)
4. [Entity Extraction Pipeline](#entity-extraction-pipeline)
5. [Relation Extraction Pipeline](#relation-extraction-pipeline)
6. [Hybrid Query Coordinator](#hybrid-query-coordinator)
7. [Graph Query Language](#graph-query-language)
8. [Inference Engine](#inference-engine)
9. [Temporal Graph Support](#temporal-graph-support)
10. [Memory Decay Model (ACT-R)](#memory-decay-model-act-r)
11. [Cold-Start Retrieval (Cold RAG)](#cold-start-retrieval-cold-rag)
12. [Domain Partitioning](#domain-partitioning)
13. [Multi-Session Coordination](#multi-session-coordination)
14. [Integration with Wave 4 Groups](#integration-with-wave-4-groups)
15. [Implementation Phases](#implementation-phases)
16. [Acceptance Criteria](#acceptance-criteria)
17. [Edge Cases and Mitigations](#edge-cases-and-mitigations)

---

## Current Architecture

### VectorGraphDB (Unified Storage)

VectorGraphDB uses SQLite as its backing store with an in-memory HNSW index for vector similarity search.

```
┌─────────────────────────────────────────────────────────────┐
│                      VectorGraphDB                           │
├─────────────────────────────────────────────────────────────┤
│  HNSW Index (in-memory)                                      │
│  - Loaded from SQLite on startup                             │
│  - Persisted via hnsw_meta, hnsw_edges tables               │
│  - Cosine similarity search                                  │
│  - Config: M=16, EfConstruct=200, EfSearch=100              │
├─────────────────────────────────────────────────────────────┤
│  SQLite Database (WAL mode)                                  │
│  ├── nodes (27 columns, multi-domain entities)              │
│  ├── edges (29 edge types, weighted relationships)          │
│  ├── vectors (embeddings as BLOB, 768-dim default)          │
│  ├── hnsw_meta, hnsw_edges (index persistence)              │
│  ├── provenance (source tracking)                           │
│  ├── conflicts (contradiction detection)                    │
│  ├── academic_sources, academic_chunks                      │
│  └── library_docs                                           │
└─────────────────────────────────────────────────────────────┘
```

### Existing Schema (core/vectorgraphdb/schema.sql)

```sql
-- Nodes table: Multi-domain entity storage
CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    domain INTEGER NOT NULL,           -- 0=Code, 1=History, 2=Academic (extending to 10)
    node_type INTEGER NOT NULL,
    name TEXT NOT NULL,

    -- Code domain fields
    path TEXT,
    package TEXT,
    line_start INTEGER,
    line_end INTEGER,
    signature TEXT,

    -- History domain fields
    session_id TEXT,
    timestamp TEXT,
    category TEXT,

    -- Academic domain fields
    url TEXT,
    source TEXT,
    authors TEXT,
    published_at TEXT,

    -- Common fields
    content TEXT,
    content_hash TEXT,
    metadata TEXT,                     -- JSON

    -- Verification
    verified INTEGER DEFAULT 0,
    verification_type TEXT,
    confidence REAL DEFAULT 0.0,
    trust_level REAL DEFAULT 0.5,

    -- Temporal
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    expires_at TEXT,
    superseded_by TEXT,

    -- Optimistic concurrency
    version INTEGER DEFAULT 1
);

-- Edges table: 29 typed relationships
CREATE TABLE IF NOT EXISTS edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL REFERENCES nodes(id),
    target_id TEXT NOT NULL REFERENCES nodes(id),
    edge_type INTEGER NOT NULL,        -- See EdgeType enum
    weight REAL DEFAULT 1.0,
    metadata TEXT,                     -- JSON
    created_at TEXT NOT NULL,
    UNIQUE(source_id, target_id, edge_type)
);

-- Vectors table: Embedding storage
CREATE TABLE IF NOT EXISTS vectors (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id),
    embedding BLOB NOT NULL,
    magnitude REAL NOT NULL,
    dimensions INTEGER NOT NULL,
    domain INTEGER NOT NULL,
    node_type INTEGER NOT NULL
);
```

### Existing Edge Types (core/vectorgraphdb/types.go)

```go
type EdgeType int

const (
    // Structural (13 types)
    EdgeCalls EdgeType = iota
    EdgeCalledBy
    EdgeImports
    EdgeImportedBy
    EdgeImplements
    EdgeImplementedBy
    EdgeEmbeds
    EdgeHasField
    EdgeHasMethod
    EdgeDefines
    EdgeDefinedIn
    EdgeReturns
    EdgeReceives

    // Temporal (5 types)
    EdgeProducedBy
    EdgeResultedIn
    EdgeSimilarTo
    EdgeFollowedBy
    EdgeSupersedes

    // Cross-Domain (11 types)
    EdgeModified
    EdgeCreated
    EdgeDeleted
    EdgeBasedOn
    EdgeReferences
    EdgeValidatedBy
    EdgeDocuments
    EdgeUsesLibrary
    EdgeImplementsPattern
    EdgeCites
    EdgeRelatedTo
)
```

### Existing Traversal (core/vectorgraphdb/traversal.go)

```go
type GraphTraverser struct {
    db *VectorGraphDB
}

type TraversalDirection int

const (
    DirectionOutgoing TraversalDirection = iota
    DirectionIncoming
    DirectionBoth
)

type TraversalOptions struct {
    Direction  TraversalDirection
    EdgeTypes  []EdgeType           // Filter by edge types
    MaxDepth   int                  // Max hops
    MaxResults int
}

func (t *GraphTraverser) GetNeighbors(nodeID string, opts TraversalOptions) ([]*GraphNode, error)
func (t *GraphTraverser) FindPath(startID, endID string, opts TraversalOptions) ([]*GraphEdge, error)
```

### Bleve (Planned - Group 4L)

Bleve provides full-text search with code-aware tokenization (CamelCase, snake_case splitting).

```
┌─────────────────────────────────────────────────────────────┐
│                         Bleve                                │
├─────────────────────────────────────────────────────────────┤
│  Indexes:                                                    │
│  ├── Code documents (functions, structs, files)             │
│  ├── LLM communications                                      │
│  ├── Web fetch results                                       │
│  ├── Git commits                                             │
│  └── Academic sources                                        │
│                                                              │
│  Features:                                                   │
│  ├── Code-aware tokenizer                                    │
│  ├── CamelCase/snake_case splitting                         │
│  ├── Faceted search                                          │
│  └── Highlighting                                            │
└─────────────────────────────────────────────────────────────┘
```

---

## Target Architecture

### Unified Knowledge Graph

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          KNOWLEDGE GRAPH API                              │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐            │
│  │ Query Lang │ │ Inference  │ │ Extraction │ │ Learning   │            │
│  │ (GQL)      │ │ Engine     │ │ Pipeline   │ │ (Bayesian) │            │
│  └─────┬──────┘ └─────┬──────┘ └─────┬──────┘ └─────┬──────┘            │
│        └──────────────┴──────────────┴──────────────┘                    │
│                               │                                           │
│              ┌────────────────▼────────────────┐                         │
│              │    Hybrid Query Coordinator      │                         │
│              │  (Bleve × HNSW × Graph × Time)   │                         │
│              └────────────────┬────────────────┘                         │
├───────────────────────────────┼───────────────────────────────────────────┤
│          VectorGraphDB        │               Bleve                       │
│  ┌──────────────────────────┐ │ ┌──────────────────────────────────────┐ │
│  │ SQLite                   │ │ │ Synchronized Indexes:                │ │
│  │ ├── nodes ◄──────────────┼─┼─┤ ├── node_content (full-text)        │ │
│  │ │   + valid_from/to      │ │ │ ├── edge_metadata (searchable)      │ │
│  │ │   + transaction_time   │ │ │ ├── entity_aliases                  │ │
│  │ ├── edges ◄──────────────┼─┼─┤ ├── relation_evidence               │ │
│  │ │   + valid_from/to      │ │ │ ├── subgraph_summaries              │ │
│  │ │   + transaction_time   │ │ │ └── provenance_text                 │ │
│  │ ├── vectors              │ │ └──────────────────────────────────────┘ │
│  │ ├── provenance           │ │                                          │
│  │ ├── conflicts            │ │ All Bleve docs contain:                  │
│  │ ├── inferred_edges       │ │ - node_id / edge_id (FK)                │
│  │ ├── ontology_types       │ │ - domain, node_type                      │
│  │ └── inference_rules      │ │ - timestamps                             │
│  └──────────────────────────┘ │                                          │
│  ┌──────────────────────────┐ │                                          │
│  │ HNSW Index (in-memory)   │ │                                          │
│  │ ├── Per-domain partitions│ │                                          │
│  │ └── Structure-aware emb  │ │                                          │
│  └──────────────────────────┘ │                                          │
└───────────────────────────────┴──────────────────────────────────────────┘
```

### Design Principles

1. **VectorGraphDB is the source of truth** for structure (nodes, edges, vectors)
2. **Bleve is a synchronized index** for fast text access and relation evidence
3. **All mutations go through VectorGraphDB** and propagate to Bleve
4. **Temporal by default**: Every edge has valid-time and transaction-time
5. **Domain partitioning**: Separate HNSW indexes per domain for performance
6. **Zero recursion**: All graph algorithms use explicit stacks/queues, never call-stack recursion
7. **O(1) reachability**: Pre-computed interval labels enable constant-time transitive queries
8. **Iterative fixed-point**: Inference uses worklist algorithms with bounded memory
9. **Inference is materialized**: Pre-compute transitive closures, update incrementally
10. **All parameters are learned**: Traversal depth, edge weights, scoring - Bayesian posteriors

### Why No Recursion?

Recursive algorithms are dangerous for graph processing:

| Problem | Impact | Solution |
|---------|--------|----------|
| Stack overflow | Crashes on deep graphs (>10K nodes) | Explicit stack data structure |
| Unpredictable memory | Call frames accumulate unboundedly | Bounded worklist size |
| No checkpointing | Cannot pause/resume computation | Persist worklist to disk |
| No parallelization | Call stack is thread-local | Shard worklist across workers |
| Hard to debug | Stack traces are opaque | Explicit state is inspectable |

**All algorithms in this architecture use iterative implementations with explicit data structures.**

---

## Storage Layer Integration

### Schema Extensions

Add to `core/vectorgraphdb/schema.sql`:

```sql
-- ============================================================================
-- TEMPORAL EXTENSIONS
-- ============================================================================

-- Add temporal columns to edges (bi-temporal model)
ALTER TABLE edges ADD COLUMN valid_from TEXT;           -- When relationship became true
ALTER TABLE edges ADD COLUMN valid_to TEXT;             -- When relationship ceased (NULL = current)
ALTER TABLE edges ADD COLUMN transaction_time TEXT      -- When we learned about it
    DEFAULT (datetime('now'));

-- Add temporal columns to nodes
ALTER TABLE nodes ADD COLUMN valid_from TEXT;
ALTER TABLE nodes ADD COLUMN valid_to TEXT;
ALTER TABLE nodes ADD COLUMN transaction_time TEXT
    DEFAULT (datetime('now'));

-- ============================================================================
-- ONTOLOGY EXTENSIONS
-- ============================================================================

-- Type hierarchy for nodes
CREATE TABLE IF NOT EXISTS ontology_types (
    type_id INTEGER PRIMARY KEY,
    type_name TEXT NOT NULL UNIQUE,
    parent_type_id INTEGER REFERENCES ontology_types(type_id),
    domain INTEGER NOT NULL,
    description TEXT,
    constraints TEXT,                  -- JSON: cardinality, required fields, etc.
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Initial type hierarchy
INSERT INTO ontology_types (type_id, type_name, parent_type_id, domain, description) VALUES
    -- Code domain hierarchy
    (1, 'Entity', NULL, 0, 'Root type for all entities'),
    (2, 'CodeEntity', 1, 0, 'Any code-related entity'),
    (3, 'Function', 2, 0, 'Function or method'),
    (4, 'Method', 3, 0, 'Method with receiver'),
    (5, 'Struct', 2, 0, 'Struct or class'),
    (6, 'Interface', 2, 0, 'Interface definition'),
    (7, 'Variable', 2, 0, 'Variable or constant'),
    (8, 'Constant', 7, 0, 'Immutable variable'),
    (9, 'Package', 2, 0, 'Package or module'),
    (10, 'File', 2, 0, 'Source file'),
    (11, 'Import', 2, 0, 'Import statement'),
    -- History domain hierarchy
    (20, 'HistoryEntity', 1, 1, 'Any history-related entity'),
    (21, 'Session', 20, 1, 'User session'),
    (22, 'Decision', 20, 1, 'Decision made during session'),
    (23, 'Outcome', 20, 1, 'Result of decision'),
    (24, 'Failure', 23, 1, 'Failed outcome'),
    (25, 'Success', 23, 1, 'Successful outcome'),
    (26, 'Handoff', 20, 1, 'Agent handoff event'),
    -- Academic domain hierarchy
    (40, 'AcademicEntity', 1, 2, 'Any academic-related entity'),
    (41, 'Paper', 40, 2, 'Research paper'),
    (42, 'Documentation', 40, 2, 'Technical documentation'),
    (43, 'RFC', 42, 2, 'RFC specification'),
    (44, 'BestPractice', 40, 2, 'Best practice pattern'),
    (45, 'Tutorial', 42, 2, 'Tutorial or guide');

-- Edge type ontology with constraints
CREATE TABLE IF NOT EXISTS edge_type_ontology (
    edge_type INTEGER PRIMARY KEY,
    edge_name TEXT NOT NULL UNIQUE,
    inverse_type INTEGER REFERENCES edge_type_ontology(edge_type),
    source_type_constraint TEXT,       -- JSON array of allowed source types
    target_type_constraint TEXT,       -- JSON array of allowed target types
    is_symmetric INTEGER DEFAULT 0,
    is_transitive INTEGER DEFAULT 0,
    is_reflexive INTEGER DEFAULT 0,
    cardinality TEXT,                  -- "one-to-one", "one-to-many", "many-to-many"
    description TEXT
);

-- Initial edge type ontology
INSERT INTO edge_type_ontology (edge_type, edge_name, inverse_type, source_type_constraint,
                                 target_type_constraint, is_transitive, cardinality) VALUES
    (0, 'Calls', 1, '["Function","Method"]', '["Function","Method"]', 0, 'many-to-many'),
    (1, 'CalledBy', 0, '["Function","Method"]', '["Function","Method"]', 0, 'many-to-many'),
    (2, 'Imports', 3, '["File","Package"]', '["Package"]', 0, 'many-to-many'),
    (3, 'ImportedBy', 2, '["Package"]', '["File","Package"]', 0, 'many-to-many'),
    (4, 'Implements', 5, '["Struct"]', '["Interface"]', 0, 'many-to-many'),
    (5, 'ImplementedBy', 4, '["Interface"]', '["Struct"]', 0, 'many-to-many'),
    (6, 'Embeds', NULL, '["Struct"]', '["Struct"]', 0, 'many-to-many'),
    (7, 'HasField', NULL, '["Struct"]', '["Variable"]', 0, 'one-to-many'),
    (8, 'HasMethod', NULL, '["Struct","Interface"]', '["Method"]', 0, 'one-to-many'),
    (9, 'Defines', 10, '["File","Package"]', '["CodeEntity"]', 0, 'one-to-many'),
    (10, 'DefinedIn', 9, '["CodeEntity"]', '["File","Package"]', 0, 'many-to-one'),
    (11, 'Returns', NULL, '["Function","Method"]', '["Entity"]', 0, 'many-to-many'),
    (12, 'Receives', NULL, '["Method"]', '["Struct"]', 0, 'many-to-one'),
    -- SimilarTo is symmetric
    (15, 'SimilarTo', 15, NULL, NULL, 0, 'many-to-many');

UPDATE edge_type_ontology SET is_symmetric = 1 WHERE edge_name = 'SimilarTo';

-- ============================================================================
-- INFERENCE EXTENSIONS
-- ============================================================================

-- Inference rules (Horn clauses)
CREATE TABLE IF NOT EXISTS inference_rules (
    rule_id INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_name TEXT NOT NULL UNIQUE,
    antecedent TEXT NOT NULL,          -- JSON: [{"edge_type": 0, "var": "A->B"}, ...]
    consequent TEXT NOT NULL,          -- JSON: {"edge_type": X, "source": "A", "target": "C"}
    confidence REAL DEFAULT 1.0,       -- Confidence of derived edge
    is_enabled INTEGER DEFAULT 1,
    description TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Initial inference rules
INSERT INTO inference_rules (rule_name, antecedent, consequent, confidence, description) VALUES
    ('transitive_calls',
     '[{"edge_type": 0, "source": "A", "target": "B"}, {"edge_type": 0, "source": "B", "target": "C"}]',
     '{"edge_type": 100, "source": "A", "target": "C"}',  -- 100 = TransitivelyCalls
     0.9,
     'If A calls B and B calls C, then A transitively calls C'),

    ('transitive_imports',
     '[{"edge_type": 2, "source": "A", "target": "B"}, {"edge_type": 2, "source": "B", "target": "C"}]',
     '{"edge_type": 101, "source": "A", "target": "C"}',  -- 101 = TransitivelyImports
     0.95,
     'If A imports B and B imports C, then A transitively depends on C'),

    ('implements_method',
     '[{"edge_type": 4, "source": "S", "target": "I"}, {"edge_type": 8, "source": "I", "target": "M"}]',
     '{"edge_type": 102, "source": "S", "target": "M"}',  -- 102 = MustImplement
     1.0,
     'If S implements I and I has method M, then S must implement M');

-- Materialized inferred edges (pre-computed for performance)
CREATE TABLE IF NOT EXISTS inferred_edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL REFERENCES nodes(id),
    target_id TEXT NOT NULL REFERENCES nodes(id),
    edge_type INTEGER NOT NULL,
    rule_id INTEGER REFERENCES inference_rules(rule_id),
    confidence REAL NOT NULL,
    evidence TEXT,                     -- JSON: list of edge IDs that produced this
    computed_at TEXT NOT NULL DEFAULT (datetime('now')),
    valid INTEGER DEFAULT 1,           -- Set to 0 when invalidated
    UNIQUE(source_id, target_id, edge_type)
);

-- ============================================================================
-- BLEVE SYNC TRACKING
-- ============================================================================

-- Track what's been indexed in Bleve (for sync verification)
CREATE TABLE IF NOT EXISTS bleve_sync_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,         -- 'node', 'edge', 'alias', 'evidence'
    entity_id TEXT NOT NULL,
    bleve_doc_id TEXT NOT NULL,
    operation TEXT NOT NULL,           -- 'index', 'update', 'delete'
    synced_at TEXT NOT NULL DEFAULT (datetime('now')),
    checksum TEXT                      -- For drift detection
);

CREATE INDEX IF NOT EXISTS idx_bleve_sync_entity ON bleve_sync_log(entity_type, entity_id);

-- ============================================================================
-- REACHABILITY INDEX (O(1) Transitive Queries)
-- ============================================================================

-- Strongly Connected Components (nodes that mutually reach each other)
CREATE TABLE IF NOT EXISTS sccs (
    scc_id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_count INTEGER NOT NULL,
    representative_node TEXT NOT NULL,  -- Canonical node for this SCC
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- SCC membership (which SCC each node belongs to)
CREATE TABLE IF NOT EXISTS scc_members (
    node_id TEXT PRIMARY KEY,
    scc_id INTEGER NOT NULL REFERENCES sccs(scc_id),
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_scc_members_scc ON scc_members(scc_id);

-- Interval labels for O(1) reachability on condensed DAG
-- A reaches B iff: pre_order[A] <= pre_order[B] AND post_order[A] >= post_order[B]
CREATE TABLE IF NOT EXISTS reachability_index (
    node_id TEXT PRIMARY KEY,
    scc_id INTEGER NOT NULL,
    pre_order INTEGER NOT NULL,         -- DFS pre-order number
    post_order INTEGER NOT NULL,        -- DFS post-order number
    topo_order INTEGER NOT NULL,        -- Topological sort position
    edge_type INTEGER NOT NULL,         -- Which edge type this index is for
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_reach_scc ON reachability_index(scc_id);
CREATE INDEX IF NOT EXISTS idx_reach_pre ON reachability_index(pre_order);
CREATE INDEX IF NOT EXISTS idx_reach_post ON reachability_index(post_order);
CREATE INDEX IF NOT EXISTS idx_reach_type ON reachability_index(edge_type);

-- ============================================================================
-- NORMALIZED PROVENANCE (O(1) Invalidation)
-- ============================================================================

-- Instead of JSON evidence with LIKE scans, use normalized table
-- Enables O(1) lookup of which inferred edges depend on a base edge
CREATE TABLE IF NOT EXISTS inference_provenance (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    inferred_edge_id INTEGER NOT NULL,
    evidence_edge_id INTEGER NOT NULL,
    evidence_position INTEGER NOT NULL,  -- Position in rule antecedent (0, 1, 2...)
    FOREIGN KEY (inferred_edge_id) REFERENCES inferred_edges(id) ON DELETE CASCADE,
    FOREIGN KEY (evidence_edge_id) REFERENCES edges(id) ON DELETE CASCADE,
    UNIQUE(inferred_edge_id, evidence_edge_id)
);

-- Critical index: when edge X is deleted, find all inferences that depend on it
CREATE INDEX IF NOT EXISTS idx_provenance_evidence ON inference_provenance(evidence_edge_id);
CREATE INDEX IF NOT EXISTS idx_provenance_inferred ON inference_provenance(inferred_edge_id);

-- ============================================================================
-- INDEXES FOR KNOWLEDGE GRAPH QUERIES
-- ============================================================================

-- Temporal queries
CREATE INDEX IF NOT EXISTS idx_edges_valid_time ON edges(valid_from, valid_to);
CREATE INDEX IF NOT EXISTS idx_edges_transaction_time ON edges(transaction_time);
CREATE INDEX IF NOT EXISTS idx_nodes_valid_time ON nodes(valid_from, valid_to);

-- Type hierarchy queries
CREATE INDEX IF NOT EXISTS idx_ontology_parent ON ontology_types(parent_type_id);

-- Edge traversal (CRITICAL for O(fan-out) instead of O(n) lookups)
CREATE INDEX IF NOT EXISTS idx_edges_source_type ON edges(source_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_target_type ON edges(target_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_type_source ON edges(edge_type, source_id);
CREATE INDEX IF NOT EXISTS idx_edges_type_target ON edges(edge_type, target_id);
CREATE INDEX IF NOT EXISTS idx_edges_composite ON edges(source_id, target_id, edge_type);

-- Inference
CREATE INDEX IF NOT EXISTS idx_inferred_edges_source ON inferred_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_inferred_edges_target ON inferred_edges(target_id);
CREATE INDEX IF NOT EXISTS idx_inferred_edges_source_type ON inferred_edges(source_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_inferred_edges_target_type ON inferred_edges(target_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_inferred_edges_valid ON inferred_edges(valid) WHERE valid = 1;
```

### Migration Strategy

```go
// core/vectorgraphdb/migrations/003_knowledge_graph.go

package migrations

import (
    "database/sql"
)

func MigrateToKnowledgeGraph(db *sql.DB) error {
    migrations := []string{
        // Step 1: Add temporal columns (non-breaking, nullable)
        `ALTER TABLE edges ADD COLUMN valid_from TEXT`,
        `ALTER TABLE edges ADD COLUMN valid_to TEXT`,
        `ALTER TABLE edges ADD COLUMN transaction_time TEXT DEFAULT (datetime('now'))`,
        `ALTER TABLE nodes ADD COLUMN valid_from TEXT`,
        `ALTER TABLE nodes ADD COLUMN valid_to TEXT`,
        `ALTER TABLE nodes ADD COLUMN transaction_time TEXT DEFAULT (datetime('now'))`,

        // Step 2: Backfill temporal columns from created_at
        `UPDATE edges SET valid_from = created_at, transaction_time = created_at WHERE valid_from IS NULL`,
        `UPDATE nodes SET valid_from = created_at, transaction_time = created_at WHERE valid_from IS NULL`,

        // Step 3: Create ontology tables
        // ... (see schema above)

        // Step 4: Create inference tables
        // ... (see schema above)

        // Step 5: Create indexes
        // ... (see schema above)
    }

    for _, migration := range migrations {
        if _, err := db.Exec(migration); err != nil {
            // Check if error is "column already exists" and skip
            if !isColumnExistsError(err) {
                return fmt.Errorf("migration failed: %w", err)
            }
        }
    }

    return nil
}
```

**ACCEPTANCE CRITERIA - Schema Extensions:**
- [ ] All new columns added without data loss
- [ ] Existing queries continue to work (backward compatible)
- [ ] Temporal columns backfilled from created_at
- [ ] Ontology tables populated with initial type hierarchy
- [ ] All indexes created and query planner uses them
- [ ] Migration is idempotent (can run multiple times safely)

---

## Entity Extraction Pipeline

### Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                       ENTITY EXTRACTION PIPELINE                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐              │
│  │ AST Extractor│    │ Doc Extractor│    │History Extrc │              │
│  │ (Tree-Sitter)│    │ (LLM-based)  │    │(Session logs)│              │
│  └──────┬───────┘    └──────┬───────┘    └──────┬───────┘              │
│         │                   │                   │                        │
│         └───────────────────┼───────────────────┘                        │
│                             ▼                                            │
│                   ┌─────────────────┐                                   │
│                   │  Entity Linker  │                                   │
│                   │ (Disambiguation)│                                   │
│                   └────────┬────────┘                                   │
│                            ▼                                            │
│                   ┌─────────────────┐                                   │
│                   │ VectorGraphDB   │                                   │
│                   │ + Bleve Sync    │                                   │
│                   └─────────────────┘                                   │
└─────────────────────────────────────────────────────────────────────────┘
```

### Extractor Interface

```go
// core/vectorgraphdb/extraction/extractor.go

package extraction

import (
    "context"

    vgdb "sylk/core/vectorgraphdb"
)

// ExtractedEntity represents a candidate entity before linking
type ExtractedEntity struct {
    // Temporary ID (will be replaced after linking)
    TempID          string

    // Entity properties
    Name            string
    Content         string
    EntityType      string                  // "Function", "Struct", etc.
    Domain          vgdb.Domain

    // Source information
    SourceFile      string
    SourceLine      int
    SourceColumn    int
    ExtractionMethod string                 // "ast", "ner", "pattern", "llm"

    // Confidence and metadata
    Confidence      float64
    Metadata        map[string]interface{}

    // For linking
    Aliases         []string                // Alternative names found
    References      []string                // Other entities this references
}

// ExtractedRelation represents a candidate relation before validation
type ExtractedRelation struct {
    SourceTempID    string                  // References ExtractedEntity.TempID
    TargetTempID    string
    RelationType    vgdb.EdgeType
    Confidence      float64
    Evidence        string                  // Text that suggests this relation
    SourceLocation  string                  // Where we found this evidence
}

// ExtractionResult contains all entities and relations from one extraction run
type ExtractionResult struct {
    Entities        []*ExtractedEntity
    Relations       []*ExtractedRelation
    SourceID        string                  // File path, session ID, etc.
    ExtractedAt     time.Time
}

// EntityExtractor is the interface all extractors implement
type EntityExtractor interface {
    // Extract entities and relations from source
    Extract(ctx context.Context, source interface{}) (*ExtractionResult, error)

    // SupportsSource returns true if this extractor can handle the source type
    SupportsSource(source interface{}) bool

    // Name returns the extractor name for logging
    Name() string
}
```

### AST Extractor (Tree-Sitter Integration)

```go
// core/vectorgraphdb/extraction/ast_extractor.go

package extraction

import (
    "context"
    "fmt"

    "sylk/core/treesitter"
    vgdb "sylk/core/vectorgraphdb"
)

// ASTExtractor extracts entities from Tree-Sitter AST
type ASTExtractor struct {
    tsManager   *treesitter.TreeSitterManager
    symbolTable *SymbolTable
}

// ASTSource represents a source file to extract from
type ASTSource struct {
    FilePath    string
    Content     []byte
    Language    string                      // "go", "typescript", "python", etc.
    CommitHash  string                      // For versioning
}

func NewASTExtractor(tsManager *treesitter.TreeSitterManager) *ASTExtractor {
    return &ASTExtractor{
        tsManager:   tsManager,
        symbolTable: NewSymbolTable(),
    }
}

func (e *ASTExtractor) Extract(ctx context.Context, source interface{}) (*ExtractionResult, error) {
    astSource, ok := source.(*ASTSource)
    if !ok {
        return nil, fmt.Errorf("ASTExtractor requires *ASTSource, got %T", source)
    }

    // Parse with Tree-Sitter
    tree, err := e.tsManager.Parse(ctx, astSource.Content, astSource.Language)
    if err != nil {
        return nil, fmt.Errorf("parse failed: %w", err)
    }
    defer tree.Close()

    result := &ExtractionResult{
        SourceID:    astSource.FilePath,
        ExtractedAt: time.Now(),
    }

    // Extract based on language
    switch astSource.Language {
    case "go":
        e.extractGo(ctx, tree, astSource, result)
    case "typescript", "javascript":
        e.extractTypeScript(ctx, tree, astSource, result)
    case "python":
        e.extractPython(ctx, tree, astSource, result)
    default:
        e.extractGeneric(ctx, tree, astSource, result)
    }

    return result, nil
}

func (e *ASTExtractor) extractGo(ctx context.Context, tree *treesitter.Tree,
                                  source *ASTSource, result *ExtractionResult) {

    // Query for function declarations
    funcQuery := `
        (function_declaration
            name: (identifier) @func_name
            parameters: (parameter_list) @params
            result: (_)? @return_type
            body: (block) @body
        ) @func
    `

    matches := e.tsManager.Query(tree, funcQuery)
    for _, match := range matches {
        funcNode := match.Captures["func"]
        nameNode := match.Captures["func_name"]

        entity := &ExtractedEntity{
            TempID:           fmt.Sprintf("go:func:%s:%d", source.FilePath, funcNode.StartPoint().Row),
            Name:             nameNode.Content(source.Content),
            Content:          funcNode.Content(source.Content),
            EntityType:       "Function",
            Domain:           vgdb.DomainCode,
            SourceFile:       source.FilePath,
            SourceLine:       int(funcNode.StartPoint().Row) + 1,
            SourceColumn:     int(funcNode.StartPoint().Column),
            ExtractionMethod: "ast",
            Confidence:       1.0, // AST extraction is deterministic
            Metadata: map[string]interface{}{
                "language":   "go",
                "signature":  e.buildGoSignature(match, source.Content),
                "commit":     source.CommitHash,
            },
        }

        result.Entities = append(result.Entities, entity)

        // Register in symbol table for reference resolution
        e.symbolTable.Register(entity.Name, entity.TempID, source.FilePath)
    }

    // Query for method declarations (functions with receivers)
    methodQuery := `
        (method_declaration
            receiver: (parameter_list
                (parameter_declaration
                    type: (_) @receiver_type
                )
            )
            name: (field_identifier) @method_name
            parameters: (parameter_list) @params
            result: (_)? @return_type
            body: (block) @body
        ) @method
    `

    methodMatches := e.tsManager.Query(tree, methodQuery)
    for _, match := range methodMatches {
        methodNode := match.Captures["method"]
        nameNode := match.Captures["method_name"]
        receiverNode := match.Captures["receiver_type"]

        entity := &ExtractedEntity{
            TempID:           fmt.Sprintf("go:method:%s:%d", source.FilePath, methodNode.StartPoint().Row),
            Name:             nameNode.Content(source.Content),
            Content:          methodNode.Content(source.Content),
            EntityType:       "Method",
            Domain:           vgdb.DomainCode,
            SourceFile:       source.FilePath,
            SourceLine:       int(methodNode.StartPoint().Row) + 1,
            ExtractionMethod: "ast",
            Confidence:       1.0,
            Metadata: map[string]interface{}{
                "language":     "go",
                "receiver":     receiverNode.Content(source.Content),
                "signature":    e.buildGoMethodSignature(match, source.Content),
            },
            References: []string{receiverNode.Content(source.Content)}, // Reference to receiver type
        }

        result.Entities = append(result.Entities, entity)
        e.symbolTable.Register(entity.Name, entity.TempID, source.FilePath)
    }

    // Query for struct declarations
    structQuery := `
        (type_declaration
            (type_spec
                name: (type_identifier) @struct_name
                type: (struct_type
                    (field_declaration_list) @fields
                )
            )
        ) @struct
    `

    structMatches := e.tsManager.Query(tree, structQuery)
    for _, match := range structMatches {
        structNode := match.Captures["struct"]
        nameNode := match.Captures["struct_name"]

        entity := &ExtractedEntity{
            TempID:           fmt.Sprintf("go:struct:%s:%d", source.FilePath, structNode.StartPoint().Row),
            Name:             nameNode.Content(source.Content),
            Content:          structNode.Content(source.Content),
            EntityType:       "Struct",
            Domain:           vgdb.DomainCode,
            SourceFile:       source.FilePath,
            SourceLine:       int(structNode.StartPoint().Row) + 1,
            ExtractionMethod: "ast",
            Confidence:       1.0,
            Metadata: map[string]interface{}{
                "language": "go",
                "fields":   e.extractStructFields(match, source.Content),
            },
        }

        result.Entities = append(result.Entities, entity)
        e.symbolTable.Register(entity.Name, entity.TempID, source.FilePath)
    }

    // Extract call relations
    e.extractGoCallRelations(ctx, tree, source, result)

    // Extract import relations
    e.extractGoImports(ctx, tree, source, result)

    // Extract implements relations (interface satisfaction)
    e.extractGoImplements(ctx, tree, source, result)
}

func (e *ASTExtractor) extractGoCallRelations(ctx context.Context, tree *treesitter.Tree,
                                               source *ASTSource, result *ExtractionResult) {
    // Find all call expressions within functions
    callQuery := `
        (call_expression
            function: [
                (identifier) @callee
                (selector_expression
                    field: (field_identifier) @callee
                )
            ]
        ) @call
    `

    // We need to know which function contains each call
    // First, find all function bodies and their ranges
    funcRanges := e.buildFunctionRanges(tree, source)

    callMatches := e.tsManager.Query(tree, callQuery)
    for _, match := range callMatches {
        callNode := match.Captures["call"]
        calleeNode := match.Captures["callee"]

        // Find which function contains this call
        callerTempID := e.findContainingFunction(callNode.StartPoint().Row, funcRanges)
        if callerTempID == "" {
            continue // Call at module level, skip
        }

        // Resolve callee (may be in same file, imported, or unresolved)
        calleeName := calleeNode.Content(source.Content)
        calleeTempID := e.symbolTable.Resolve(calleeName, source.FilePath)

        if calleeTempID == "" {
            // Create placeholder for external/unresolved
            calleeTempID = fmt.Sprintf("unresolved:%s", calleeName)
        }

        relation := &ExtractedRelation{
            SourceTempID:   callerTempID,
            TargetTempID:   calleeTempID,
            RelationType:   vgdb.EdgeCalls,
            Confidence:     1.0,
            Evidence:       callNode.Content(source.Content),
            SourceLocation: fmt.Sprintf("%s:%d", source.FilePath, callNode.StartPoint().Row+1),
        }

        result.Relations = append(result.Relations, relation)
    }
}

func (e *ASTExtractor) SupportsSource(source interface{}) bool {
    _, ok := source.(*ASTSource)
    return ok
}

func (e *ASTExtractor) Name() string {
    return "ast_extractor"
}
```

### Symbol Table for Cross-File Resolution

```go
// core/vectorgraphdb/extraction/symbol_table.go

package extraction

import (
    "sync"
)

// SymbolTable tracks symbols across files for cross-reference resolution
type SymbolTable struct {
    mu sync.RWMutex

    // symbolName -> []SymbolEntry (can have multiple definitions)
    symbols map[string][]*SymbolEntry

    // filePath -> []symbolName (for incremental updates)
    fileSymbols map[string][]string

    // Qualified names: "package.Name" -> SymbolEntry
    qualified map[string]*SymbolEntry
}

type SymbolEntry struct {
    Name      string
    TempID    string
    FilePath  string
    Package   string
    Exported  bool
    Type      string // "Function", "Struct", etc.
}

func NewSymbolTable() *SymbolTable {
    return &SymbolTable{
        symbols:     make(map[string][]*SymbolEntry),
        fileSymbols: make(map[string][]string),
        qualified:   make(map[string]*SymbolEntry),
    }
}

// Register adds a symbol to the table
func (st *SymbolTable) Register(name, tempID, filePath string) {
    st.mu.Lock()
    defer st.mu.Unlock()

    entry := &SymbolEntry{
        Name:     name,
        TempID:   tempID,
        FilePath: filePath,
        Exported: isExported(name),
    }

    st.symbols[name] = append(st.symbols[name], entry)
    st.fileSymbols[filePath] = append(st.fileSymbols[filePath], name)
}

// Resolve finds the TempID for a symbol name, preferring same-file definitions
func (st *SymbolTable) Resolve(name, currentFile string) string {
    st.mu.RLock()
    defer st.mu.RUnlock()

    entries, ok := st.symbols[name]
    if !ok || len(entries) == 0 {
        return ""
    }

    // Prefer same-file definition
    for _, e := range entries {
        if e.FilePath == currentFile {
            return e.TempID
        }
    }

    // Fall back to first exported definition
    for _, e := range entries {
        if e.Exported {
            return e.TempID
        }
    }

    // Fall back to first definition
    return entries[0].TempID
}

// InvalidateFile removes all symbols from a file (for incremental re-extraction)
func (st *SymbolTable) InvalidateFile(filePath string) {
    st.mu.Lock()
    defer st.mu.Unlock()

    names, ok := st.fileSymbols[filePath]
    if !ok {
        return
    }

    for _, name := range names {
        entries := st.symbols[name]
        filtered := entries[:0]
        for _, e := range entries {
            if e.FilePath != filePath {
                filtered = append(filtered, e)
            }
        }
        if len(filtered) == 0 {
            delete(st.symbols, name)
        } else {
            st.symbols[name] = filtered
        }
    }

    delete(st.fileSymbols, filePath)
}

func isExported(name string) bool {
    if len(name) == 0 {
        return false
    }
    r := []rune(name)[0]
    return r >= 'A' && r <= 'Z'
}
```

**ACCEPTANCE CRITERIA - Entity Extraction:**
- [ ] AST extractor correctly identifies functions, methods, structs, interfaces in Go
- [ ] AST extractor correctly identifies functions, classes, interfaces in TypeScript
- [ ] Symbol table resolves cross-file references with >95% accuracy
- [ ] Extraction is incremental: only changed files are re-extracted
- [ ] Call relations correctly link caller → callee with evidence
- [ ] Confidence scores reflect extraction method reliability (AST=1.0)
- [ ] All extracted entities have source location for navigation
- [ ] Extraction completes in <100ms per 1000 LOC

### Entity Linker

The Entity Linker disambiguates extracted entities and links them to existing nodes in VectorGraphDB.

```go
// core/vectorgraphdb/extraction/entity_linker.go

package extraction

import (
    "context"
    "fmt"

    vgdb "sylk/core/vectorgraphdb"
)

// LinkResult contains the result of entity linking
type LinkResult struct {
    // Mapping from TempID to resolved NodeID
    LinkedEntities map[string]string

    // New entities that need to be created (no match found)
    NewEntities []*ExtractedEntity

    // Entities merged into existing (match found)
    MergedEntities map[string]*MergeInfo

    // Ambiguous entities (multiple possible matches)
    Ambiguous []*AmbiguousEntity
}

type MergeInfo struct {
    TempID        string
    ExistingID    string
    Confidence    float64
    MergeStrategy string  // "update", "version", "skip"
}

type AmbiguousEntity struct {
    Entity     *ExtractedEntity
    Candidates []*LinkCandidate
}

type LinkCandidate struct {
    NodeID     string
    Similarity float64
    MatchType  string  // "exact_name", "fuzzy_name", "embedding", "alias"
}

// EntityLinker resolves extracted entities to existing graph nodes
type EntityLinker struct {
    db          *vgdb.VectorGraphDB
    bleve       *BleveClient
    embedder    Embedder
    config      *LinkerConfig
}

type LinkerConfig struct {
    ExactMatchThreshold  float64  // Confidence for exact name match (default: 0.95)
    FuzzyMatchThreshold  float64  // Min similarity for fuzzy match (default: 0.85)
    EmbeddingThreshold   float64  // Min similarity for embedding match (default: 0.80)
    AmbiguityThreshold   float64  // If top 2 candidates within this diff, mark ambiguous (default: 0.05)
    MaxCandidates        int      // Max candidates to consider (default: 10)
}

func NewEntityLinker(db *vgdb.VectorGraphDB, bleve *BleveClient, embedder Embedder) *EntityLinker {
    return &EntityLinker{
        db:       db,
        bleve:    bleve,
        embedder: embedder,
        config: &LinkerConfig{
            ExactMatchThreshold: 0.95,
            FuzzyMatchThreshold: 0.85,
            EmbeddingThreshold:  0.80,
            AmbiguityThreshold:  0.05,
            MaxCandidates:       10,
        },
    }
}

// Link resolves all entities in an extraction result
func (l *EntityLinker) Link(ctx context.Context, result *ExtractionResult) (*LinkResult, error) {
    linkResult := &LinkResult{
        LinkedEntities: make(map[string]string),
        MergedEntities: make(map[string]*MergeInfo),
    }

    for _, entity := range result.Entities {
        candidates, err := l.findCandidates(ctx, entity)
        if err != nil {
            return nil, fmt.Errorf("find candidates for %s: %w", entity.TempID, err)
        }

        if len(candidates) == 0 {
            // No match - this is a new entity
            linkResult.NewEntities = append(linkResult.NewEntities, entity)
            continue
        }

        // Check for ambiguity
        if len(candidates) > 1 &&
            candidates[0].Similarity-candidates[1].Similarity < l.config.AmbiguityThreshold {
            linkResult.Ambiguous = append(linkResult.Ambiguous, &AmbiguousEntity{
                Entity:     entity,
                Candidates: candidates,
            })
            continue
        }

        // Use best candidate
        best := candidates[0]
        if best.Similarity >= l.config.ExactMatchThreshold {
            // High confidence match - merge
            linkResult.LinkedEntities[entity.TempID] = best.NodeID
            linkResult.MergedEntities[entity.TempID] = &MergeInfo{
                TempID:        entity.TempID,
                ExistingID:    best.NodeID,
                Confidence:    best.Similarity,
                MergeStrategy: l.determineMergeStrategy(entity, best),
            }
        } else if best.Similarity >= l.config.FuzzyMatchThreshold {
            // Medium confidence - merge but flag for review
            linkResult.LinkedEntities[entity.TempID] = best.NodeID
            linkResult.MergedEntities[entity.TempID] = &MergeInfo{
                TempID:        entity.TempID,
                ExistingID:    best.NodeID,
                Confidence:    best.Similarity,
                MergeStrategy: "review",
            }
        } else {
            // Low confidence - treat as new
            linkResult.NewEntities = append(linkResult.NewEntities, entity)
        }
    }

    return linkResult, nil
}

func (l *EntityLinker) findCandidates(ctx context.Context, entity *ExtractedEntity) ([]*LinkCandidate, error) {
    var candidates []*LinkCandidate

    // Stage 1: Exact name match in same domain
    exactMatches, err := l.bleve.SearchExact(ctx, entity.Name, entity.Domain, l.config.MaxCandidates)
    if err != nil {
        return nil, err
    }
    for _, match := range exactMatches {
        candidates = append(candidates, &LinkCandidate{
            NodeID:     match.NodeID,
            Similarity: 0.98, // High confidence for exact match
            MatchType:  "exact_name",
        })
    }

    // If we have a high-confidence exact match, return early
    if len(candidates) > 0 && candidates[0].Similarity >= l.config.ExactMatchThreshold {
        return candidates, nil
    }

    // Stage 2: Alias match
    aliasMatches, err := l.bleve.SearchAliases(ctx, entity.Name, entity.Domain, l.config.MaxCandidates)
    if err != nil {
        return nil, err
    }
    for _, match := range aliasMatches {
        candidates = append(candidates, &LinkCandidate{
            NodeID:     match.NodeID,
            Similarity: match.Score * 0.95, // Slightly lower than exact
            MatchType:  "alias",
        })
    }

    // Stage 3: Fuzzy name match
    fuzzyMatches, err := l.bleve.SearchFuzzy(ctx, entity.Name, entity.Domain, l.config.MaxCandidates)
    if err != nil {
        return nil, err
    }
    for _, match := range fuzzyMatches {
        candidates = append(candidates, &LinkCandidate{
            NodeID:     match.NodeID,
            Similarity: match.Score * 0.90,
            MatchType:  "fuzzy_name",
        })
    }

    // Stage 4: Embedding similarity (if we have content)
    if entity.Content != "" {
        embedding, err := l.embedder.Embed(ctx, entity.Content)
        if err == nil {
            embeddingMatches, err := l.db.VectorSearch(ctx, embedding, vgdb.VectorSearchOptions{
                Domains:    []vgdb.Domain{entity.Domain},
                MaxResults: l.config.MaxCandidates,
            })
            if err == nil {
                for _, match := range embeddingMatches {
                    candidates = append(candidates, &LinkCandidate{
                        NodeID:     match.Node.ID,
                        Similarity: match.Similarity,
                        MatchType:  "embedding",
                    })
                }
            }
        }
    }

    // Deduplicate and sort by similarity
    candidates = l.deduplicateAndSort(candidates)

    return candidates, nil
}

func (l *EntityLinker) determineMergeStrategy(entity *ExtractedEntity, candidate *LinkCandidate) string {
    // If source file and line match, this is an update to same entity
    if candidate.MatchType == "exact_name" {
        return "update"
    }

    // If entity has different source location, create version
    return "version"
}

func (l *EntityLinker) deduplicateAndSort(candidates []*LinkCandidate) []*LinkCandidate {
    seen := make(map[string]bool)
    deduped := make([]*LinkCandidate, 0, len(candidates))

    for _, c := range candidates {
        if !seen[c.NodeID] {
            seen[c.NodeID] = true
            deduped = append(deduped, c)
        }
    }

    // Sort by similarity descending
    sort.Slice(deduped, func(i, j int) bool {
        return deduped[i].Similarity > deduped[j].Similarity
    })

    return deduped
}
```

**ACCEPTANCE CRITERIA - Entity Linker:**
- [ ] Exact name matches resolve with >98% confidence
- [ ] Alias matches resolve with >90% confidence
- [ ] Embedding matches resolve with >80% confidence
- [ ] Ambiguous entities flagged when top 2 candidates within 5% similarity
- [ ] Deduplication prevents same node appearing multiple times
- [ ] Merge strategy correctly identifies "update" vs "version" vs "review"
- [ ] Linking completes in <50ms for batch of 100 entities

---

## Relation Extraction Pipeline

### Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                      RELATION EXTRACTION PIPELINE                        │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐              │
│  │Static Extract│    │Semantic Ext  │    │Evidence Ext  │              │
│  │ (AST-based)  │    │(Embedding)   │    │(Doc patterns)│              │
│  └──────┬───────┘    └──────┬───────┘    └──────┬───────┘              │
│         │                   │                   │                        │
│         └───────────────────┼───────────────────┘                        │
│                             ▼                                            │
│                   ┌─────────────────┐                                   │
│                   │Relation Validator│                                  │
│                   │(Ontology check)  │                                  │
│                   └────────┬────────┘                                   │
│                            ▼                                            │
│                   ┌─────────────────┐                                   │
│                   │ VectorGraphDB   │                                   │
│                   │(edges + inferred)│                                  │
│                   └─────────────────┘                                   │
└─────────────────────────────────────────────────────────────────────────┘
```

### Relation Validator

```go
// core/vectorgraphdb/extraction/relation_validator.go

package extraction

import (
    "context"
    "fmt"

    vgdb "sylk/core/vectorgraphdb"
)

// RelationValidator validates extracted relations against ontology constraints
type RelationValidator struct {
    db       *vgdb.VectorGraphDB
    ontology *OntologyCache
}

type ValidationResult struct {
    Valid       []*ValidatedRelation
    Invalid     []*InvalidRelation
    NeedsReview []*ReviewRelation
}

type ValidatedRelation struct {
    Relation   *ExtractedRelation
    SourceNode *vgdb.GraphNode
    TargetNode *vgdb.GraphNode
    Confidence float64
}

type InvalidRelation struct {
    Relation *ExtractedRelation
    Reason   string
    Details  map[string]interface{}
}

type ReviewRelation struct {
    Relation   *ExtractedRelation
    Reason     string
    Confidence float64
}

// OntologyCache caches edge type constraints for fast validation
type OntologyCache struct {
    edgeConstraints map[vgdb.EdgeType]*EdgeConstraint
    typeHierarchy   map[string][]string // type -> all ancestor types
}

type EdgeConstraint struct {
    EdgeType         vgdb.EdgeType
    AllowedSources   []string  // Node types that can be source
    AllowedTargets   []string  // Node types that can be target
    IsSymmetric      bool
    IsTransitive     bool
    Cardinality      string    // "one-to-one", "one-to-many", "many-to-many"
    InverseType      *vgdb.EdgeType
}

func NewRelationValidator(db *vgdb.VectorGraphDB) (*RelationValidator, error) {
    ontology, err := loadOntologyCache(db)
    if err != nil {
        return nil, fmt.Errorf("load ontology: %w", err)
    }

    return &RelationValidator{
        db:       db,
        ontology: ontology,
    }, nil
}

// Validate checks all relations against ontology constraints
func (v *RelationValidator) Validate(ctx context.Context, relations []*ExtractedRelation,
                                       linkResult *LinkResult) (*ValidationResult, error) {
    result := &ValidationResult{}

    for _, rel := range relations {
        // Resolve source and target node IDs
        sourceID := v.resolveNodeID(rel.SourceTempID, linkResult)
        targetID := v.resolveNodeID(rel.TargetTempID, linkResult)

        if sourceID == "" {
            result.Invalid = append(result.Invalid, &InvalidRelation{
                Relation: rel,
                Reason:   "source_not_found",
                Details:  map[string]interface{}{"temp_id": rel.SourceTempID},
            })
            continue
        }

        if targetID == "" {
            result.Invalid = append(result.Invalid, &InvalidRelation{
                Relation: rel,
                Reason:   "target_not_found",
                Details:  map[string]interface{}{"temp_id": rel.TargetTempID},
            })
            continue
        }

        // Load source and target nodes
        sourceNode, err := v.db.GetNode(ctx, sourceID)
        if err != nil {
            result.Invalid = append(result.Invalid, &InvalidRelation{
                Relation: rel,
                Reason:   "source_load_failed",
                Details:  map[string]interface{}{"error": err.Error()},
            })
            continue
        }

        targetNode, err := v.db.GetNode(ctx, targetID)
        if err != nil {
            result.Invalid = append(result.Invalid, &InvalidRelation{
                Relation: rel,
                Reason:   "target_load_failed",
                Details:  map[string]interface{}{"error": err.Error()},
            })
            continue
        }

        // Validate against ontology constraints
        constraint, ok := v.ontology.edgeConstraints[rel.RelationType]
        if !ok {
            // Unknown edge type - needs review
            result.NeedsReview = append(result.NeedsReview, &ReviewRelation{
                Relation:   rel,
                Reason:     "unknown_edge_type",
                Confidence: rel.Confidence,
            })
            continue
        }

        // Check source type constraint
        if !v.isTypeAllowed(sourceNode.NodeType, constraint.AllowedSources) {
            result.Invalid = append(result.Invalid, &InvalidRelation{
                Relation: rel,
                Reason:   "source_type_violation",
                Details: map[string]interface{}{
                    "actual":   sourceNode.NodeType.String(),
                    "allowed":  constraint.AllowedSources,
                },
            })
            continue
        }

        // Check target type constraint
        if !v.isTypeAllowed(targetNode.NodeType, constraint.AllowedTargets) {
            result.Invalid = append(result.Invalid, &InvalidRelation{
                Relation: rel,
                Reason:   "target_type_violation",
                Details: map[string]interface{}{
                    "actual":   targetNode.NodeType.String(),
                    "allowed":  constraint.AllowedTargets,
                },
            })
            continue
        }

        // Check for duplicate edge
        exists, err := v.db.EdgeExists(ctx, sourceID, targetID, rel.RelationType)
        if err != nil {
            return nil, fmt.Errorf("check edge exists: %w", err)
        }
        if exists {
            result.Invalid = append(result.Invalid, &InvalidRelation{
                Relation: rel,
                Reason:   "duplicate_edge",
                Details:  map[string]interface{}{},
            })
            continue
        }

        // Check cardinality constraints
        if err := v.checkCardinality(ctx, sourceID, targetID, rel.RelationType, constraint); err != nil {
            result.Invalid = append(result.Invalid, &InvalidRelation{
                Relation: rel,
                Reason:   "cardinality_violation",
                Details:  map[string]interface{}{"error": err.Error()},
            })
            continue
        }

        // Validation passed
        result.Valid = append(result.Valid, &ValidatedRelation{
            Relation:   rel,
            SourceNode: sourceNode,
            TargetNode: targetNode,
            Confidence: rel.Confidence,
        })
    }

    return result, nil
}

func (v *RelationValidator) isTypeAllowed(nodeType vgdb.NodeType, allowed []string) bool {
    if len(allowed) == 0 {
        return true // No constraint
    }

    nodeTypeName := nodeType.String()

    // Check direct match
    for _, t := range allowed {
        if t == nodeTypeName {
            return true
        }
    }

    // Check hierarchy (e.g., "Method" allowed if "Function" is allowed)
    ancestors := v.ontology.typeHierarchy[nodeTypeName]
    for _, ancestor := range ancestors {
        for _, t := range allowed {
            if t == ancestor {
                return true
            }
        }
    }

    return false
}

func (v *RelationValidator) checkCardinality(ctx context.Context, sourceID, targetID string,
                                              edgeType vgdb.EdgeType, constraint *EdgeConstraint) error {
    switch constraint.Cardinality {
    case "one-to-one":
        // Source can only have one outgoing edge of this type
        count, err := v.db.CountEdges(ctx, sourceID, edgeType, vgdb.DirectionOutgoing)
        if err != nil {
            return err
        }
        if count > 0 {
            return fmt.Errorf("source already has %s edge", edgeType)
        }

        // Target can only have one incoming edge of this type
        count, err = v.db.CountEdges(ctx, targetID, edgeType, vgdb.DirectionIncoming)
        if err != nil {
            return err
        }
        if count > 0 {
            return fmt.Errorf("target already has incoming %s edge", edgeType)
        }

    case "one-to-many":
        // Target can only have one incoming edge of this type (many sources → one target)
        count, err := v.db.CountEdges(ctx, targetID, edgeType, vgdb.DirectionIncoming)
        if err != nil {
            return err
        }
        if count > 0 {
            return fmt.Errorf("target already has incoming %s edge (one-to-many)", edgeType)
        }

    case "many-to-one":
        // Source can only have one outgoing edge of this type (one source → many targets)
        count, err := v.db.CountEdges(ctx, sourceID, edgeType, vgdb.DirectionOutgoing)
        if err != nil {
            return err
        }
        if count > 0 {
            return fmt.Errorf("source already has %s edge (many-to-one)", edgeType)
        }

    case "many-to-many":
        // No cardinality constraint
    }

    return nil
}

func (v *RelationValidator) resolveNodeID(tempID string, linkResult *LinkResult) string {
    // Check if linked to existing node
    if nodeID, ok := linkResult.LinkedEntities[tempID]; ok {
        return nodeID
    }

    // Check if it's a new entity that will be created
    for _, entity := range linkResult.NewEntities {
        if entity.TempID == tempID {
            // Return tempID - caller will create the node
            return tempID
        }
    }

    return ""
}
```

### Document-Based Relation Extractor

```go
// core/vectorgraphdb/extraction/doc_relation_extractor.go

package extraction

import (
    "context"
    "regexp"

    vgdb "sylk/core/vectorgraphdb"
)

// DocRelationExtractor extracts relations from documentation and comments
type DocRelationExtractor struct {
    patterns   []*RelationPattern
    linker     *EntityLinker
    llmClient  LLMClient
    config     *DocExtractorConfig
}

type RelationPattern struct {
    Name         string
    Pattern      *regexp.Regexp
    RelationType vgdb.EdgeType
    SourceGroup  int           // Regex capture group for source
    TargetGroup  int           // Regex capture group for target
    Confidence   float64
}

type DocExtractorConfig struct {
    UseLLMFallback     bool
    LLMConfidenceMin   float64
    MaxPatternsPerDoc  int
}

// Default patterns for common relation phrases
var defaultPatterns = []*RelationPattern{
    {
        Name:         "depends_on",
        Pattern:      regexp.MustCompile(`["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]\s+depends\s+on\s+["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]`),
        RelationType: vgdb.EdgeImports,
        SourceGroup:  1,
        TargetGroup:  2,
        Confidence:   0.85,
    },
    {
        Name:         "implements",
        Pattern:      regexp.MustCompile(`["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]\s+implements\s+["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]`),
        RelationType: vgdb.EdgeImplements,
        SourceGroup:  1,
        TargetGroup:  2,
        Confidence:   0.90,
    },
    {
        Name:         "calls",
        Pattern:      regexp.MustCompile(`["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]\s+calls\s+["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]`),
        RelationType: vgdb.EdgeCalls,
        SourceGroup:  1,
        TargetGroup:  2,
        Confidence:   0.80,
    },
    {
        Name:         "deprecated_use",
        Pattern:      regexp.MustCompile(`["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]\s+is\s+deprecated.*use\s+["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]`),
        RelationType: vgdb.EdgeSupersedes,
        SourceGroup:  2,  // New one supersedes old
        TargetGroup:  1,
        Confidence:   0.95,
    },
    {
        Name:         "based_on",
        Pattern:      regexp.MustCompile(`["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]\s+(?:is\s+)?based\s+on\s+["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]`),
        RelationType: vgdb.EdgeBasedOn,
        SourceGroup:  1,
        TargetGroup:  2,
        Confidence:   0.85,
    },
}

func NewDocRelationExtractor(linker *EntityLinker, llmClient LLMClient) *DocRelationExtractor {
    return &DocRelationExtractor{
        patterns:  defaultPatterns,
        linker:    linker,
        llmClient: llmClient,
        config: &DocExtractorConfig{
            UseLLMFallback:    true,
            LLMConfidenceMin:  0.70,
            MaxPatternsPerDoc: 100,
        },
    }
}

// Extract extracts relations from a document
func (e *DocRelationExtractor) Extract(ctx context.Context, doc *DocumentSource) ([]*ExtractedRelation, error) {
    var relations []*ExtractedRelation

    // Stage 1: Pattern-based extraction
    for _, pattern := range e.patterns {
        matches := pattern.Pattern.FindAllStringSubmatch(doc.Content, e.config.MaxPatternsPerDoc)
        for _, match := range matches {
            if len(match) <= pattern.TargetGroup {
                continue
            }

            sourceName := match[pattern.SourceGroup]
            targetName := match[pattern.TargetGroup]

            rel := &ExtractedRelation{
                SourceTempID:   fmt.Sprintf("mention:%s", sourceName),
                TargetTempID:   fmt.Sprintf("mention:%s", targetName),
                RelationType:   pattern.RelationType,
                Confidence:     pattern.Confidence,
                Evidence:       match[0],
                SourceLocation: fmt.Sprintf("%s (pattern: %s)", doc.Path, pattern.Name),
            }

            relations = append(relations, rel)
        }
    }

    // Stage 2: LLM-based extraction for complex relations (if enabled)
    if e.config.UseLLMFallback && len(relations) < 5 {
        llmRelations, err := e.extractWithLLM(ctx, doc)
        if err == nil {
            relations = append(relations, llmRelations...)
        }
        // Don't fail on LLM error - pattern extraction is primary
    }

    return relations, nil
}

func (e *DocRelationExtractor) extractWithLLM(ctx context.Context, doc *DocumentSource) ([]*ExtractedRelation, error) {
    prompt := fmt.Sprintf(`Analyze the following documentation and extract any relationships between code entities.

For each relationship found, output a JSON object with:
- source: the name of the source entity
- target: the name of the target entity
- relation: one of [calls, imports, implements, extends, depends_on, based_on, supersedes]
- confidence: your confidence (0.0-1.0)
- evidence: the sentence that indicates this relationship

Documentation:
%s

Output only valid JSON array. If no relationships found, output [].`, doc.Content)

    response, err := e.llmClient.Complete(ctx, prompt, LLMOptions{
        MaxTokens:   1000,
        Temperature: 0.0, // Deterministic
    })
    if err != nil {
        return nil, err
    }

    // Parse LLM response
    var llmResults []struct {
        Source     string  `json:"source"`
        Target     string  `json:"target"`
        Relation   string  `json:"relation"`
        Confidence float64 `json:"confidence"`
        Evidence   string  `json:"evidence"`
    }

    if err := json.Unmarshal([]byte(response), &llmResults); err != nil {
        return nil, fmt.Errorf("parse LLM response: %w", err)
    }

    var relations []*ExtractedRelation
    for _, r := range llmResults {
        if r.Confidence < e.config.LLMConfidenceMin {
            continue
        }

        edgeType, ok := parseRelationType(r.Relation)
        if !ok {
            continue
        }

        relations = append(relations, &ExtractedRelation{
            SourceTempID:   fmt.Sprintf("mention:%s", r.Source),
            TargetTempID:   fmt.Sprintf("mention:%s", r.Target),
            RelationType:   edgeType,
            Confidence:     r.Confidence * 0.9, // Discount LLM confidence slightly
            Evidence:       r.Evidence,
            SourceLocation: fmt.Sprintf("%s (llm extraction)", doc.Path),
        })
    }

    return relations, nil
}

type DocumentSource struct {
    Path    string
    Content string
    Type    string // "comment", "readme", "docstring", "commit_message"
}
```

**ACCEPTANCE CRITERIA - Relation Extraction:**
- [ ] Pattern-based extraction finds >80% of explicit relation mentions
- [ ] LLM fallback improves coverage by >15% on complex docs
- [ ] Validator correctly rejects type constraint violations
- [ ] Validator correctly rejects cardinality violations
- [ ] Duplicate edges are detected and rejected
- [ ] Invalid relations include clear error reasons
- [ ] Extraction + validation completes in <200ms per document

---

## Hybrid Query Coordinator

### Architecture

The Hybrid Query Coordinator combines results from Bleve (text), HNSW (vector), and Graph traversal.

```
┌─────────────────────────────────────────────────────────────────────────┐
│                       HYBRID QUERY COORDINATOR                           │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Query: "Find functions related to authentication that call database"   │
│                                                                          │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐         │
│  │   Bleve Search  │  │   HNSW Search   │  │ Graph Traversal │         │
│  │                 │  │                 │  │                 │         │
│  │ "authentication"│  │ embed(query)    │  │ start: authFunc │         │
│  │ "database"      │  │ → similar nodes │  │ edges: Calls    │         │
│  │ type: Function  │  │ domain: Code    │  │ depth: 1-3      │         │
│  └────────┬────────┘  └────────┬────────┘  └────────┬────────┘         │
│           │                    │                    │                   │
│           └────────────────────┼────────────────────┘                   │
│                                ▼                                        │
│                    ┌───────────────────────┐                           │
│                    │  Candidate Aggregator │                           │
│                    │  (Union + Dedupe)     │                           │
│                    └───────────┬───────────┘                           │
│                                ▼                                        │
│                    ┌───────────────────────┐                           │
│                    │   Hybrid Scorer       │                           │
│                    │  (Learned weights)    │                           │
│                    └───────────┬───────────┘                           │
│                                ▼                                        │
│                    ┌───────────────────────┐                           │
│                    │   Results + Explain   │                           │
│                    └───────────────────────┘                           │
└─────────────────────────────────────────────────────────────────────────┘
```

### Implementation

```go
// core/vectorgraphdb/query/hybrid_coordinator.go

package query

import (
    "context"
    "sort"
    "sync"

    vgdb "sylk/core/vectorgraphdb"
)

// HybridQuery represents a multi-modal query
type HybridQuery struct {
    // Text search (Bleve)
    TextQuery    string
    TextBoost    float64           // Weight for text results (learned)

    // Vector search (HNSW)
    Embedding    []float32         // Pre-computed or will be computed
    VectorBoost  float64           // Weight for vector results (learned)

    // Graph traversal
    StartNodeIDs []string          // Starting points for traversal
    TraversalSpec *TraversalSpec
    GraphBoost   float64           // Weight for graph proximity (learned)

    // Filters
    Domains      []vgdb.Domain
    NodeTypes    []vgdb.NodeType
    TimeRange    *TimeRange        // For temporal queries

    // Options
    MaxResults   int
    MinScore     float64
    IncludeExplanation bool
}

type TraversalSpec struct {
    EdgeTypes    []vgdb.EdgeType
    Direction    vgdb.TraversalDirection
    MinDepth     int
    MaxDepth     int
    IncludeStart bool
}

type TimeRange struct {
    ValidFrom    *time.Time
    ValidTo      *time.Time
    AsOf         *time.Time  // Point-in-time query
}

// HybridResult contains a scored result with explanation
type HybridResult struct {
    Node         *vgdb.GraphNode
    Score        float64
    Explanation  *ResultExplanation
}

type ResultExplanation struct {
    TextScore      float64
    TextMatches    []string
    VectorScore    float64
    VectorSimilar  []string   // Similar nodes that contributed
    GraphScore     float64
    GraphPath      []string   // Path from start node
    FinalFormula   string
}

// HybridCoordinator orchestrates multi-modal queries
type HybridCoordinator struct {
    db          *vgdb.VectorGraphDB
    bleve       *BleveSearcher
    hnsw        *vgdb.HNSWIndex
    traverser   *vgdb.GraphTraverser
    embedder    Embedder
    scorer      *HybridScorer
}

func NewHybridCoordinator(db *vgdb.VectorGraphDB, bleve *BleveSearcher,
                           embedder Embedder) *HybridCoordinator {
    return &HybridCoordinator{
        db:        db,
        bleve:     bleve,
        hnsw:      db.HNSW(),
        traverser: vgdb.NewGraphTraverser(db),
        embedder:  embedder,
        scorer:    NewHybridScorer(),
    }
}

// Search executes a hybrid query
func (c *HybridCoordinator) Search(ctx context.Context, q *HybridQuery) ([]*HybridResult, error) {
    // Set defaults
    if q.MaxResults == 0 {
        q.MaxResults = 20
    }
    if q.TextBoost == 0 {
        q.TextBoost = 1.0
    }
    if q.VectorBoost == 0 {
        q.VectorBoost = 1.0
    }
    if q.GraphBoost == 0 {
        q.GraphBoost = 1.0
    }

    // Collect candidates from all sources in parallel
    var wg sync.WaitGroup
    var mu sync.Mutex

    candidates := make(map[string]*candidateEntry)

    // Text search
    if q.TextQuery != "" {
        wg.Add(1)
        go func() {
            defer wg.Done()
            results, err := c.bleveSearch(ctx, q)
            if err != nil {
                return // Log but don't fail
            }
            mu.Lock()
            for _, r := range results {
                c.addCandidate(candidates, r.NodeID, "text", r.Score*q.TextBoost, r.Matches)
            }
            mu.Unlock()
        }()
    }

    // Vector search
    if len(q.Embedding) > 0 || q.TextQuery != "" {
        wg.Add(1)
        go func() {
            defer wg.Done()
            results, err := c.vectorSearch(ctx, q)
            if err != nil {
                return
            }
            mu.Lock()
            for _, r := range results {
                c.addCandidate(candidates, r.NodeID, "vector", r.Similarity*q.VectorBoost, nil)
            }
            mu.Unlock()
        }()
    }

    // Graph traversal
    if len(q.StartNodeIDs) > 0 && q.TraversalSpec != nil {
        wg.Add(1)
        go func() {
            defer wg.Done()
            results, err := c.graphTraversal(ctx, q)
            if err != nil {
                return
            }
            mu.Lock()
            for _, r := range results {
                // Score decays with distance
                score := 1.0 / float64(r.Depth+1) * q.GraphBoost
                c.addCandidate(candidates, r.NodeID, "graph", score, r.Path)
            }
            mu.Unlock()
        }()
    }

    wg.Wait()

    // Score and rank candidates
    results := c.rankCandidates(ctx, candidates, q)

    return results, nil
}

type candidateEntry struct {
    nodeID      string
    textScore   float64
    textMatches []string
    vectorScore float64
    graphScore  float64
    graphPath   []string
}

func (c *HybridCoordinator) addCandidate(candidates map[string]*candidateEntry,
                                          nodeID, source string, score float64, extra interface{}) {
    entry, ok := candidates[nodeID]
    if !ok {
        entry = &candidateEntry{nodeID: nodeID}
        candidates[nodeID] = entry
    }

    switch source {
    case "text":
        entry.textScore = max(entry.textScore, score)
        if matches, ok := extra.([]string); ok {
            entry.textMatches = matches
        }
    case "vector":
        entry.vectorScore = max(entry.vectorScore, score)
    case "graph":
        entry.graphScore = max(entry.graphScore, score)
        if path, ok := extra.([]string); ok {
            entry.graphPath = path
        }
    }
}

func (c *HybridCoordinator) rankCandidates(ctx context.Context,
                                            candidates map[string]*candidateEntry,
                                            q *HybridQuery) []*HybridResult {
    var results []*HybridResult

    for nodeID, entry := range candidates {
        // Compute final score using RRF (Reciprocal Rank Fusion) or learned weights
        finalScore := c.scorer.Score(entry, q)

        if finalScore < q.MinScore {
            continue
        }

        // Load full node
        node, err := c.db.GetNode(ctx, nodeID)
        if err != nil {
            continue
        }

        result := &HybridResult{
            Node:  node,
            Score: finalScore,
        }

        if q.IncludeExplanation {
            result.Explanation = &ResultExplanation{
                TextScore:     entry.textScore,
                TextMatches:   entry.textMatches,
                VectorScore:   entry.vectorScore,
                GraphScore:    entry.graphScore,
                GraphPath:     entry.graphPath,
                FinalFormula:  c.scorer.ExplainFormula(entry, q),
            }
        }

        results = append(results, result)
    }

    // Sort by score descending
    sort.Slice(results, func(i, j int) bool {
        return results[i].Score > results[j].Score
    })

    // Limit results
    if len(results) > q.MaxResults {
        results = results[:q.MaxResults]
    }

    return results
}

func (c *HybridCoordinator) bleveSearch(ctx context.Context, q *HybridQuery) ([]*BleveResult, error) {
    bleveQuery := &BleveQuery{
        Text:      q.TextQuery,
        Domains:   q.Domains,
        NodeTypes: q.NodeTypes,
        Limit:     q.MaxResults * 3, // Over-fetch for fusion
    }

    if q.TimeRange != nil && q.TimeRange.AsOf != nil {
        bleveQuery.AsOf = q.TimeRange.AsOf
    }

    return c.bleve.Search(ctx, bleveQuery)
}

func (c *HybridCoordinator) vectorSearch(ctx context.Context, q *HybridQuery) ([]*VectorResult, error) {
    embedding := q.Embedding

    // Compute embedding if not provided
    if len(embedding) == 0 && q.TextQuery != "" {
        var err error
        embedding, err = c.embedder.Embed(ctx, q.TextQuery)
        if err != nil {
            return nil, err
        }
    }

    return c.hnsw.Search(ctx, embedding, vgdb.HNSWSearchOptions{
        K:         q.MaxResults * 3,
        Domains:   q.Domains,
        NodeTypes: q.NodeTypes,
    })
}

func (c *HybridCoordinator) graphTraversal(ctx context.Context, q *HybridQuery) ([]*GraphResult, error) {
    var allResults []*GraphResult

    for _, startID := range q.StartNodeIDs {
        results, err := c.traverser.BFS(ctx, startID, &vgdb.BFSOptions{
            EdgeTypes:  q.TraversalSpec.EdgeTypes,
            Direction:  q.TraversalSpec.Direction,
            MaxDepth:   q.TraversalSpec.MaxDepth,
            MaxResults: q.MaxResults * 2,
        })
        if err != nil {
            continue
        }

        for _, r := range results {
            allResults = append(allResults, &GraphResult{
                NodeID: r.Node.ID,
                Depth:  r.Depth,
                Path:   r.Path,
            })
        }
    }

    return allResults, nil
}
```

### Hybrid Scorer with Learned Weights

```go
// core/vectorgraphdb/query/hybrid_scorer.go

package query

import (
    "fmt"
    "math"
)

// HybridScorer computes final scores using learned weights
type HybridScorer struct {
    // Learned weights (Bayesian posteriors from 4M Adaptive Retrieval)
    textWeight   *LearnedWeight
    vectorWeight *LearnedWeight
    graphWeight  *LearnedWeight

    // RRF parameter (k=60 is standard)
    rrfK float64
}

type LearnedWeight struct {
    Alpha float64 // Beta distribution alpha
    Beta  float64 // Beta distribution beta
}

func NewHybridScorer() *HybridScorer {
    return &HybridScorer{
        // Default priors (will be updated from learning system)
        textWeight:   &LearnedWeight{Alpha: 2.0, Beta: 2.0},
        vectorWeight: &LearnedWeight{Alpha: 3.0, Beta: 2.0},
        graphWeight:  &LearnedWeight{Alpha: 2.0, Beta: 3.0},
        rrfK:         60.0,
    }
}

// Score computes final score for a candidate
func (s *HybridScorer) Score(entry *candidateEntry, q *HybridQuery) float64 {
    // Use RRF (Reciprocal Rank Fusion) as base
    // RRF combines rankings rather than raw scores for better calibration

    rrfScore := 0.0

    if entry.textScore > 0 {
        // Convert score to rank (approximation)
        textRank := 1.0 / entry.textScore
        rrfScore += s.textWeight.Mean() * (1.0 / (s.rrfK + textRank))
    }

    if entry.vectorScore > 0 {
        vectorRank := 1.0 / entry.vectorScore
        rrfScore += s.vectorWeight.Mean() * (1.0 / (s.rrfK + vectorRank))
    }

    if entry.graphScore > 0 {
        graphRank := 1.0 / entry.graphScore
        rrfScore += s.graphWeight.Mean() * (1.0 / (s.rrfK + graphRank))
    }

    // Boost if appears in multiple sources (intersection bonus)
    sourceCount := 0
    if entry.textScore > 0 {
        sourceCount++
    }
    if entry.vectorScore > 0 {
        sourceCount++
    }
    if entry.graphScore > 0 {
        sourceCount++
    }

    if sourceCount >= 2 {
        rrfScore *= 1.0 + 0.2*float64(sourceCount-1) // 20% bonus per additional source
    }

    return rrfScore
}

func (s *HybridScorer) ExplainFormula(entry *candidateEntry, q *HybridQuery) string {
    return fmt.Sprintf(
        "text(%.2f)*%.2f + vector(%.2f)*%.2f + graph(%.2f)*%.2f",
        entry.textScore, s.textWeight.Mean(),
        entry.vectorScore, s.vectorWeight.Mean(),
        entry.graphScore, s.graphWeight.Mean(),
    )
}

func (w *LearnedWeight) Mean() float64 {
    return w.Alpha / (w.Alpha + w.Beta)
}

// Sample returns a Thompson-sampled weight for exploration
func (w *LearnedWeight) Sample() float64 {
    // Beta distribution sampling (using approximation for simplicity)
    return betaSample(w.Alpha, w.Beta)
}

// Update updates the weight based on observed feedback
func (w *LearnedWeight) Update(wasHelpful bool, weight float64) {
    if wasHelpful {
        w.Alpha += weight
    } else {
        w.Beta += weight
    }
}
```

**ACCEPTANCE CRITERIA - Hybrid Query Coordinator:**
- [ ] Parallel execution of Bleve, HNSW, and Graph searches
- [ ] Correct deduplication of candidates from multiple sources
- [ ] RRF scoring produces well-calibrated final scores
- [ ] Multi-source intersection bonus improves precision
- [ ] Explanation includes contribution from each source
- [ ] Temporal filters correctly applied to all sources
- [ ] Domain/type filters correctly applied to all sources
- [ ] Query latency <100ms for 90th percentile
- [ ] Memory usage bounded regardless of result count

---

## Bleve Integration

### Bleve Index Types

Bleve serves as a synchronized text index over VectorGraphDB, enabling fast full-text search and relation evidence discovery.

```go
// core/search/bleve/kg_indexes.go

package bleve

import (
    "github.com/blevesearch/bleve/v2"
    "github.com/blevesearch/bleve/v2/mapping"
)

// KnowledgeGraphIndexes holds all Bleve indexes for the knowledge graph
type KnowledgeGraphIndexes struct {
    // Primary indexes
    NodeContent      bleve.Index  // Full-text search on node content
    EdgeMetadata     bleve.Index  // Searchable edge metadata

    // Secondary indexes
    EntityAliases    bleve.Index  // Alternative names for entities
    RelationEvidence bleve.Index  // Text evidence for relations
    SubgraphSummary  bleve.Index  // Summaries of important subgraphs
}

// NodeContentDocument indexes node content for full-text search
type NodeContentDocument struct {
    NodeID      string   `json:"node_id"`       // FK to VectorGraphDB nodes
    Domain      string   `json:"domain"`        // For domain filtering
    NodeType    string   `json:"node_type"`     // Function, Struct, etc.
    Name        string   `json:"name"`          // Exact + analyzed
    Content     string   `json:"content"`       // Full-text searchable
    Path        string   `json:"path"`          // File path (for code)
    Package     string   `json:"package"`       // Package/module
    Signature   string   `json:"signature"`     // For functions
    Tags        []string `json:"tags"`          // Extracted keywords
    ValidFrom   string   `json:"valid_from"`    // For temporal queries
    ValidTo     string   `json:"valid_to"`      // NULL = current
    UpdatedAt   string   `json:"updated_at"`
}

// EdgeMetadataDocument indexes edge metadata for searchable relations
type EdgeMetadataDocument struct {
    EdgeID       string  `json:"edge_id"`
    SourceID     string  `json:"source_id"`
    TargetID     string  `json:"target_id"`
    SourceName   string  `json:"source_name"`   // Denormalized for search
    TargetName   string  `json:"target_name"`   // Denormalized for search
    EdgeType     string  `json:"edge_type"`
    Metadata     string  `json:"metadata"`      // JSON flattened to text
    Reason       string  `json:"reason"`        // Why does this edge exist?
    CommitMsg    string  `json:"commit_msg"`    // If from git history
    Evidence     string  `json:"evidence"`      // Text evidence
    Confidence   float64 `json:"confidence"`
    CreatedAt    string  `json:"created_at"`
}

// EntityAliasDocument indexes alternative names for entities
type EntityAliasDocument struct {
    AliasID     string  `json:"alias_id"`
    NodeID      string  `json:"node_id"`       // Canonical entity
    Alias       string  `json:"alias"`         // Alternative name
    AliasType   string  `json:"alias_type"`    // import_alias, comment_ref, doc_mention
    Domain      string  `json:"domain"`
    SourceFile  string  `json:"source_file"`   // Where alias was found
    Confidence  float64 `json:"confidence"`
}

// RelationEvidenceDocument indexes text evidence suggesting relations
type RelationEvidenceDocument struct {
    EvidenceID    string  `json:"evidence_id"`
    SourceText    string  `json:"source_text"`    // Entity name in text
    TargetText    string  `json:"target_text"`    // Entity name in text
    SourceNodeID  string  `json:"source_node_id"` // Resolved node (may be empty)
    TargetNodeID  string  `json:"target_node_id"` // Resolved node (may be empty)
    RelationType  string  `json:"relation_type"`  // Suggested relation
    Evidence      string  `json:"evidence"`       // Full sentence/context
    SourceDoc     string  `json:"source_doc"`     // Where found
    Confidence    float64 `json:"confidence"`
    IsResolved    bool    `json:"is_resolved"`    // Both nodes linked?
}

// SubgraphSummaryDocument indexes textual summaries of subgraphs
type SubgraphSummaryDocument struct {
    SubgraphID   string   `json:"subgraph_id"`
    RootNodeID   string   `json:"root_node_id"`
    Summary      string   `json:"summary"`       // Generated text description
    Keywords     []string `json:"keywords"`      // Key concepts
    NodeIDs      []string `json:"node_ids"`      // All nodes in subgraph
    EdgeTypes    []string `json:"edge_types"`    // Edge types present
    Domain       string   `json:"domain"`
    Depth        int      `json:"depth"`
    NodeCount    int      `json:"node_count"`
    EdgeCount    int      `json:"edge_count"`
}

// BuildNodeContentMapping creates the Bleve mapping for node content
func BuildNodeContentMapping() mapping.IndexMapping {
    nodeMapping := bleve.NewDocumentMapping()

    // Keyword field (exact match)
    keywordFieldMapping := bleve.NewTextFieldMapping()
    keywordFieldMapping.Analyzer = "keyword"

    // Text field (analyzed)
    textFieldMapping := bleve.NewTextFieldMapping()
    textFieldMapping.Analyzer = "standard"

    // Code-aware text field
    codeFieldMapping := bleve.NewTextFieldMapping()
    codeFieldMapping.Analyzer = "code_analyzer" // Custom analyzer with CamelCase/snake_case

    // Map fields
    nodeMapping.AddFieldMappingsAt("node_id", keywordFieldMapping)
    nodeMapping.AddFieldMappingsAt("domain", keywordFieldMapping)
    nodeMapping.AddFieldMappingsAt("node_type", keywordFieldMapping)
    nodeMapping.AddFieldMappingsAt("name", codeFieldMapping)
    nodeMapping.AddFieldMappingsAt("content", codeFieldMapping)
    nodeMapping.AddFieldMappingsAt("path", keywordFieldMapping)
    nodeMapping.AddFieldMappingsAt("package", keywordFieldMapping)
    nodeMapping.AddFieldMappingsAt("signature", codeFieldMapping)
    nodeMapping.AddFieldMappingsAt("tags", keywordFieldMapping)

    indexMapping := bleve.NewIndexMapping()
    indexMapping.AddDocumentMapping("node", nodeMapping)

    return indexMapping
}
```

### Bleve Synchronization

```go
// core/search/bleve/sync.go

package bleve

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "sync"
    "time"

    vgdb "sylk/core/vectorgraphdb"
)

// BleveSynchronizer keeps Bleve indexes in sync with VectorGraphDB
type BleveSynchronizer struct {
    db      *vgdb.VectorGraphDB
    indexes *KnowledgeGraphIndexes

    // Sync state
    mu          sync.RWMutex
    lastSyncAt  time.Time
    pendingOps  chan syncOp

    // Configuration
    config *SyncConfig
}

type SyncConfig struct {
    BatchSize       int           // Number of operations to batch (default: 100)
    FlushInterval   time.Duration // Max time before flush (default: 100ms)
    RetryAttempts   int           // Retries on failure (default: 3)
    VerifyChecksums bool          // Verify data integrity (default: true)
}

type syncOp struct {
    OpType   string // "index", "update", "delete"
    DocType  string // "node", "edge", "alias", "evidence"
    DocID    string
    Document interface{}
}

func NewBleveSynchronizer(db *vgdb.VectorGraphDB, indexes *KnowledgeGraphIndexes) *BleveSynchronizer {
    sync := &BleveSynchronizer{
        db:      db,
        indexes: indexes,
        config: &SyncConfig{
            BatchSize:       100,
            FlushInterval:   100 * time.Millisecond,
            RetryAttempts:   3,
            VerifyChecksums: true,
        },
        pendingOps: make(chan syncOp, 10000),
    }

    // Start background sync worker
    go sync.syncWorker()

    return sync
}

// IndexNode indexes a node in Bleve (called after VectorGraphDB insert)
func (s *BleveSynchronizer) IndexNode(ctx context.Context, node *vgdb.GraphNode) error {
    doc := &NodeContentDocument{
        NodeID:    node.ID,
        Domain:    node.Domain.String(),
        NodeType:  node.NodeType.String(),
        Name:      node.Name,
        Content:   node.Content,
        Path:      node.Path,
        Package:   node.Package,
        Signature: node.Signature,
        ValidFrom: node.ValidFrom,
        ValidTo:   node.ValidTo,
        UpdatedAt: node.UpdatedAt,
    }

    // Extract tags from content
    doc.Tags = extractTags(node.Content, node.NodeType)

    s.pendingOps <- syncOp{
        OpType:   "index",
        DocType:  "node",
        DocID:    node.ID,
        Document: doc,
    }

    return nil
}

// IndexEdge indexes an edge in Bleve
func (s *BleveSynchronizer) IndexEdge(ctx context.Context, edge *vgdb.GraphEdge,
                                        sourceName, targetName string) error {
    doc := &EdgeMetadataDocument{
        EdgeID:     fmt.Sprintf("%d", edge.ID),
        SourceID:   edge.SourceID,
        TargetID:   edge.TargetID,
        SourceName: sourceName,
        TargetName: targetName,
        EdgeType:   edge.EdgeType.String(),
        Metadata:   flattenMetadata(edge.Metadata),
        CreatedAt:  edge.CreatedAt.Format(time.RFC3339),
    }

    // Extract reason/evidence from metadata if present
    if reason, ok := edge.Metadata["reason"].(string); ok {
        doc.Reason = reason
    }
    if evidence, ok := edge.Metadata["evidence"].(string); ok {
        doc.Evidence = evidence
    }
    if confidence, ok := edge.Metadata["confidence"].(float64); ok {
        doc.Confidence = confidence
    }

    s.pendingOps <- syncOp{
        OpType:   "index",
        DocType:  "edge",
        DocID:    doc.EdgeID,
        Document: doc,
    }

    return nil
}

// IndexAlias indexes an entity alias
func (s *BleveSynchronizer) IndexAlias(ctx context.Context, nodeID, alias, aliasType,
                                         domain, sourceFile string, confidence float64) error {
    doc := &EntityAliasDocument{
        AliasID:    fmt.Sprintf("%s:%s", nodeID, alias),
        NodeID:     nodeID,
        Alias:      alias,
        AliasType:  aliasType,
        Domain:     domain,
        SourceFile: sourceFile,
        Confidence: confidence,
    }

    s.pendingOps <- syncOp{
        OpType:   "index",
        DocType:  "alias",
        DocID:    doc.AliasID,
        Document: doc,
    }

    return nil
}

// IndexRelationEvidence indexes text evidence for a potential relation
func (s *BleveSynchronizer) IndexRelationEvidence(ctx context.Context, evidence *RelationEvidenceDocument) error {
    s.pendingOps <- syncOp{
        OpType:   "index",
        DocType:  "evidence",
        DocID:    evidence.EvidenceID,
        Document: evidence,
    }

    return nil
}

// DeleteNode removes a node from Bleve indexes
func (s *BleveSynchronizer) DeleteNode(ctx context.Context, nodeID string) error {
    s.pendingOps <- syncOp{
        OpType:  "delete",
        DocType: "node",
        DocID:   nodeID,
    }

    return nil
}

// syncWorker processes pending sync operations in batches
func (s *BleveSynchronizer) syncWorker() {
    ticker := time.NewTicker(s.config.FlushInterval)
    defer ticker.Stop()

    var batch []syncOp

    flush := func() {
        if len(batch) == 0 {
            return
        }

        // Group by document type for batch operations
        nodeOps := make([]syncOp, 0)
        edgeOps := make([]syncOp, 0)
        aliasOps := make([]syncOp, 0)
        evidenceOps := make([]syncOp, 0)

        for _, op := range batch {
            switch op.DocType {
            case "node":
                nodeOps = append(nodeOps, op)
            case "edge":
                edgeOps = append(edgeOps, op)
            case "alias":
                aliasOps = append(aliasOps, op)
            case "evidence":
                evidenceOps = append(evidenceOps, op)
            }
        }

        // Execute batches
        s.executeBatch(s.indexes.NodeContent, nodeOps)
        s.executeBatch(s.indexes.EdgeMetadata, edgeOps)
        s.executeBatch(s.indexes.EntityAliases, aliasOps)
        s.executeBatch(s.indexes.RelationEvidence, evidenceOps)

        batch = batch[:0]
        s.mu.Lock()
        s.lastSyncAt = time.Now()
        s.mu.Unlock()
    }

    for {
        select {
        case op := <-s.pendingOps:
            batch = append(batch, op)
            if len(batch) >= s.config.BatchSize {
                flush()
            }
        case <-ticker.C:
            flush()
        }
    }
}

func (s *BleveSynchronizer) executeBatch(index bleve.Index, ops []syncOp) {
    if len(ops) == 0 {
        return
    }

    batch := index.NewBatch()

    for _, op := range ops {
        switch op.OpType {
        case "index", "update":
            batch.Index(op.DocID, op.Document)
        case "delete":
            batch.Delete(op.DocID)
        }
    }

    for attempt := 0; attempt < s.config.RetryAttempts; attempt++ {
        if err := index.Batch(batch); err == nil {
            break
        }
        time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
    }
}

// VerifyConsistency checks that Bleve and VectorGraphDB are in sync
func (s *BleveSynchronizer) VerifyConsistency(ctx context.Context) (*ConsistencyReport, error) {
    report := &ConsistencyReport{
        CheckedAt: time.Now(),
    }

    // Count nodes in VectorGraphDB
    dbCount, err := s.db.CountNodes(ctx)
    if err != nil {
        return nil, err
    }
    report.DBNodeCount = dbCount

    // Count documents in Bleve
    bleveCount, err := s.indexes.NodeContent.DocCount()
    if err != nil {
        return nil, err
    }
    report.BleveNodeCount = int(bleveCount)

    // Check for discrepancy
    report.NodeCountMatch = report.DBNodeCount == report.BleveNodeCount

    // Sample verification (check random nodes)
    if s.config.VerifyChecksums {
        sampleNodes, err := s.db.SampleNodes(ctx, 100)
        if err == nil {
            for _, node := range sampleNodes {
                bleveDoc, err := s.getNodeFromBleve(node.ID)
                if err != nil {
                    report.MissingInBleve = append(report.MissingInBleve, node.ID)
                    continue
                }

                // Verify checksum
                dbChecksum := computeNodeChecksum(node)
                bleveChecksum := computeNodeDocChecksum(bleveDoc)
                if dbChecksum != bleveChecksum {
                    report.ChecksumMismatches = append(report.ChecksumMismatches, node.ID)
                }
            }
        }
    }

    report.IsConsistent = report.NodeCountMatch &&
        len(report.MissingInBleve) == 0 &&
        len(report.ChecksumMismatches) == 0

    return report, nil
}

type ConsistencyReport struct {
    CheckedAt          time.Time
    DBNodeCount        int
    BleveNodeCount     int
    NodeCountMatch     bool
    MissingInBleve     []string
    MissingInDB        []string
    ChecksumMismatches []string
    IsConsistent       bool
}

func computeNodeChecksum(node *vgdb.GraphNode) string {
    data := fmt.Sprintf("%s:%s:%s:%s:%s", node.ID, node.Name, node.Content, node.UpdatedAt, node.ValidTo)
    hash := sha256.Sum256([]byte(data))
    return hex.EncodeToString(hash[:8])
}
```

**ACCEPTANCE CRITERIA - Bleve Integration:**
- [ ] Node content indexed within 100ms of VectorGraphDB insert
- [ ] Edge metadata searchable by reason, evidence, commit message
- [ ] Entity aliases enable fuzzy entity resolution
- [ ] Relation evidence enables relation discovery from text
- [ ] Consistency verification detects drift between stores
- [ ] Batch operations handle 1000+ documents efficiently
- [ ] Delete operations propagate correctly
- [ ] Domain filtering works across all index types

---

## Inference Engine

### Architecture

The Inference Engine uses a **four-layer architecture** with **zero recursion** for production-grade reliability:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    PRODUCTION-GRADE INFERENCE ENGINE                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  LAYER 1: SCC CONDENSATION (Handles Cycles)                         │    │
│  │  ┌─────────┐    ┌─────────┐    ┌─────────┐                          │    │
│  │  │ Tarjan  │───►│Condense │───►│   DAG   │  Iterative, O(V+E)       │    │
│  │  │(iter.)  │    │  SCCs   │    │         │  No recursion            │    │
│  │  └─────────┘    └─────────┘    └─────────┘                          │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                              │                                               │
│  ┌───────────────────────────▼─────────────────────────────────────────┐    │
│  │  LAYER 2: REACHABILITY INDEX (O(1) Transitive Queries)              │    │
│  │                                                                      │    │
│  │  Interval Labeling: Node gets [pre_order, post_order]               │    │
│  │  ┌─────────────────────────────────────────────────────────────┐    │    │
│  │  │ A reaches B?  →  pre[A] <= pre[B] AND post[A] >= post[B]    │    │    │
│  │  │ O(1) query, O(n) space, O(V+E) build time                   │    │    │
│  │  └─────────────────────────────────────────────────────────────┘    │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                              │                                               │
│  ┌───────────────────────────▼─────────────────────────────────────────┐    │
│  │  LAYER 3: ITERATIVE MATERIALIZATION (No Recursion)                  │    │
│  │                                                                      │    │
│  │  ┌──────────┐    ┌──────────┐    ┌──────────┐                       │    │
│  │  │ Worklist │───►│  Apply   │───►│  Delta   │──┐                    │    │
│  │  │ (edges)  │    │  Rules   │    │  Table   │  │                    │    │
│  │  └──────────┘    └──────────┘    └──────────┘  │                    │    │
│  │       ▲                                        │                    │    │
│  │       └────────────────────────────────────────┘                    │    │
│  │                    (iterate until empty)                            │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                              │                                               │
│  ┌───────────────────────────▼─────────────────────────────────────────┐    │
│  │  LAYER 4: NORMALIZED PROVENANCE (O(1) Invalidation)                 │    │
│  │                                                                      │    │
│  │  inference_provenance table with indexed evidence_edge_id           │    │
│  │  No LIKE scans, no JSON parsing - direct index lookup               │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Complexity Comparison

| Operation | Old (Recursive) | New (Iterative) |
|-----------|-----------------|-----------------|
| SCC Detection | O(V+E) recursive | O(V+E) iterative |
| Transitive Closure Query | O(V+E) BFS | **O(1)** interval lookup |
| Full Materialization | O(R × K² × N) ≈ O(n³) | O(R × E × avg_fanout) |
| Incremental Insert | O(R × N) scan | O(R × fanout) indexed |
| Invalidation | O(N) LIKE scan | **O(1)** index lookup |
| Reachability Check | O(V+E) BFS | **O(1)** interval |

### Layer 1: Iterative SCC Detection (Tarjan without Recursion)

```go
// core/knowledge/graph/scc.go

package graph

// IterativeTarjan finds all SCCs without recursion
// Uses explicit stack instead of call stack - safe for arbitrarily deep graphs
type IterativeTarjan struct {
    graph       *AdjacencyList
    index       int
    nodeIndex   map[string]int
    nodeLowlink map[string]int
    onStack     map[string]bool
    stack       []string
    sccs        [][]string

    // Explicit DFS state (replaces call stack)
    dfsStack []dfsFrame
}

type dfsFrame struct {
    nodeID      string
    neighborIdx int      // Which neighbor we're processing
    neighbors   []string // Cached neighbors
    phase       int      // 0=enter, 1=process neighbors, 2=exit
}

// NewIterativeTarjan creates a new SCC finder
func NewIterativeTarjan(graph *AdjacencyList) *IterativeTarjan {
    return &IterativeTarjan{
        graph:       graph,
        nodeIndex:   make(map[string]int),
        nodeLowlink: make(map[string]int),
        onStack:     make(map[string]bool),
    }
}

// FindSCCs returns all strongly connected components
// Time: O(V+E), Space: O(V), NO RECURSION
func (t *IterativeTarjan) FindSCCs() [][]string {
    // Initialize all nodes
    for nodeID := range t.graph.Nodes {
        if _, visited := t.nodeIndex[nodeID]; !visited {
            t.iterativeDFS(nodeID)
        }
    }
    return t.sccs
}

func (t *IterativeTarjan) iterativeDFS(startID string) {
    t.dfsStack = append(t.dfsStack, dfsFrame{
        nodeID:    startID,
        phase:     0,
        neighbors: t.graph.GetOutgoing(startID),
    })

    for len(t.dfsStack) > 0 {
        frame := &t.dfsStack[len(t.dfsStack)-1]

        switch frame.phase {
        case 0: // Enter node (equivalent to function entry in recursive version)
            t.nodeIndex[frame.nodeID] = t.index
            t.nodeLowlink[frame.nodeID] = t.index
            t.index++
            t.stack = append(t.stack, frame.nodeID)
            t.onStack[frame.nodeID] = true
            frame.phase = 1

        case 1: // Process neighbors (equivalent to for loop in recursive version)
            if frame.neighborIdx < len(frame.neighbors) {
                neighbor := frame.neighbors[frame.neighborIdx]
                frame.neighborIdx++

                if _, visited := t.nodeIndex[neighbor]; !visited {
                    // Push neighbor onto DFS stack (replaces recursive call)
                    t.dfsStack = append(t.dfsStack, dfsFrame{
                        nodeID:    neighbor,
                        phase:     0,
                        neighbors: t.graph.GetOutgoing(neighbor),
                    })
                } else if t.onStack[neighbor] {
                    // Back edge - update lowlink
                    if t.nodeIndex[neighbor] < t.nodeLowlink[frame.nodeID] {
                        t.nodeLowlink[frame.nodeID] = t.nodeIndex[neighbor]
                    }
                }
            } else {
                frame.phase = 2
            }

        case 2: // Exit node (equivalent to function return in recursive version)
            // Update parent's lowlink from this node
            if len(t.dfsStack) > 1 {
                parent := &t.dfsStack[len(t.dfsStack)-2]
                if t.nodeLowlink[frame.nodeID] < t.nodeLowlink[parent.nodeID] {
                    t.nodeLowlink[parent.nodeID] = t.nodeLowlink[frame.nodeID]
                }
            }

            // If this is SCC root, pop the SCC
            if t.nodeLowlink[frame.nodeID] == t.nodeIndex[frame.nodeID] {
                var scc []string
                for {
                    top := t.stack[len(t.stack)-1]
                    t.stack = t.stack[:len(t.stack)-1]
                    t.onStack[top] = false
                    scc = append(scc, top)
                    if top == frame.nodeID {
                        break
                    }
                }
                t.sccs = append(t.sccs, scc)
            }

            // Pop this frame
            t.dfsStack = t.dfsStack[:len(t.dfsStack)-1]
        }
    }
}

// CondensedGraph represents SCCs as single nodes, forming a DAG
type CondensedGraph struct {
    SCCs       [][]string      // SCC index -> member nodes
    NodeToSCC  map[string]int  // node -> SCC index
    DAGEdges   map[int][]int   // SCC index -> adjacent SCC indexes
    TopoOrder  []int           // Topological order of SCCs
}

// CondenseGraph creates a DAG from SCCs
func CondenseGraph(graph *AdjacencyList, sccs [][]string) *CondensedGraph {
    cg := &CondensedGraph{
        SCCs:      sccs,
        NodeToSCC: make(map[string]int),
        DAGEdges:  make(map[int][]int),
    }

    // Map nodes to SCCs
    for i, scc := range sccs {
        for _, node := range scc {
            cg.NodeToSCC[node] = i
        }
    }

    // Build DAG edges between SCCs
    edgeSet := make(map[[2]int]bool)
    for node := range graph.Nodes {
        fromSCC := cg.NodeToSCC[node]
        for _, neighbor := range graph.GetOutgoing(node) {
            toSCC := cg.NodeToSCC[neighbor]
            if fromSCC != toSCC {
                key := [2]int{fromSCC, toSCC}
                if !edgeSet[key] {
                    edgeSet[key] = true
                    cg.DAGEdges[fromSCC] = append(cg.DAGEdges[fromSCC], toSCC)
                }
            }
        }
    }

    // Topological sort (Kahn's algorithm - iterative, no recursion)
    cg.TopoOrder = cg.kahnSort()

    return cg
}

// kahnSort performs topological sort using Kahn's algorithm (iterative)
func (cg *CondensedGraph) kahnSort() []int {
    inDegree := make([]int, len(cg.SCCs))
    for _, neighbors := range cg.DAGEdges {
        for _, n := range neighbors {
            inDegree[n]++
        }
    }

    // Queue of nodes with no incoming edges
    queue := make([]int, 0, len(cg.SCCs))
    for i, deg := range inDegree {
        if deg == 0 {
            queue = append(queue, i)
        }
    }

    var order []int
    for len(queue) > 0 {
        node := queue[0]
        queue = queue[1:]
        order = append(order, node)

        for _, neighbor := range cg.DAGEdges[node] {
            inDegree[neighbor]--
            if inDegree[neighbor] == 0 {
                queue = append(queue, neighbor)
            }
        }
    }

    return order
}
```

### Layer 2: Interval Labeling for O(1) Reachability

```go
// core/knowledge/graph/reachability.go

package graph

import (
    "context"
    "database/sql"
)

// IntervalIndex provides O(1) reachability queries on a DAG
// Uses pre/post ordering from iterative DFS
type IntervalIndex struct {
    PreOrder   map[string]int  // Node -> pre-order number
    PostOrder  map[string]int  // Node -> post-order number
    NodeToSCC  map[string]int  // Node -> SCC index
    SCCMembers map[int][]string // SCC index -> member nodes
    EdgeType   int             // Which edge type this index is for
}

// BuildIntervalIndex creates the index using iterative DFS (no recursion)
func BuildIntervalIndex(cg *CondensedGraph, edgeType int) *IntervalIndex {
    idx := &IntervalIndex{
        PreOrder:   make(map[string]int),
        PostOrder:  make(map[string]int),
        NodeToSCC:  cg.NodeToSCC,
        SCCMembers: make(map[int][]string),
        EdgeType:   edgeType,
    }

    for i, scc := range cg.SCCs {
        idx.SCCMembers[i] = scc
    }

    // Iterative DFS for pre/post numbering
    counter := 0
    visited := make(map[int]bool)

    type frame struct {
        sccIdx   int
        phase    int // 0=pre, 1=post
        childIdx int
    }

    // Process in reverse topo order
    for i := len(cg.TopoOrder) - 1; i >= 0; i-- {
        startSCC := cg.TopoOrder[i]
        if visited[startSCC] {
            continue
        }

        stack := []frame{{sccIdx: startSCC, phase: 0}}

        for len(stack) > 0 {
            f := &stack[len(stack)-1]

            if f.phase == 0 {
                // Pre-order: assign to all nodes in this SCC
                visited[f.sccIdx] = true
                for _, node := range cg.SCCs[f.sccIdx] {
                    idx.PreOrder[node] = counter
                }
                counter++
                f.phase = 1
                f.childIdx = 0
            } else {
                children := cg.DAGEdges[f.sccIdx]
                if f.childIdx < len(children) {
                    child := children[f.childIdx]
                    f.childIdx++
                    if !visited[child] {
                        stack = append(stack, frame{sccIdx: child, phase: 0})
                    }
                } else {
                    // Post-order: assign to all nodes in this SCC
                    for _, node := range cg.SCCs[f.sccIdx] {
                        idx.PostOrder[node] = counter
                    }
                    counter++
                    stack = stack[:len(stack)-1]
                }
            }
        }
    }

    return idx
}

// CanReach returns true if 'from' can reach 'to' in O(1)
func (idx *IntervalIndex) CanReach(from, to string) bool {
    fromSCC, fromOK := idx.NodeToSCC[from]
    toSCC, toOK := idx.NodeToSCC[to]

    if !fromOK || !toOK {
        return false
    }

    // Same SCC: all nodes reach each other (by definition of SCC)
    if fromSCC == toSCC {
        return true
    }

    // Different SCCs: use interval containment
    // A reaches B iff pre(A) <= pre(B) AND post(A) >= post(B)
    fromPre := idx.PreOrder[from]
    fromPost := idx.PostOrder[from]
    toPre := idx.PreOrder[to]
    toPost := idx.PostOrder[to]

    return fromPre <= toPre && fromPost >= toPost
}

// GetAllReachable returns all nodes reachable from 'start' using index
func (idx *IntervalIndex) GetAllReachable(start string) []string {
    startPre, ok1 := idx.PreOrder[start]
    startPost, ok2 := idx.PostOrder[start]
    startSCC, ok3 := idx.NodeToSCC[start]

    if !ok1 || !ok2 || !ok3 {
        return nil
    }

    var reachable []string

    for node, pre := range idx.PreOrder {
        post := idx.PostOrder[node]
        nodeSCC := idx.NodeToSCC[node]

        // Same SCC or interval contained
        if nodeSCC == startSCC || (startPre <= pre && startPost >= post) {
            reachable = append(reachable, node)
        }
    }

    return reachable
}

// PersistToSQLite saves the index to the reachability_index table
func (idx *IntervalIndex) PersistToSQLite(ctx context.Context, db *sql.DB) error {
    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // Clear existing index for this edge type
    _, err = tx.ExecContext(ctx,
        `DELETE FROM reachability_index WHERE edge_type = ?`, idx.EdgeType)
    if err != nil {
        return err
    }

    // Insert new index entries
    stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO reachability_index
        (node_id, scc_id, pre_order, post_order, topo_order, edge_type, updated_at)
        VALUES (?, ?, ?, ?, 0, ?, datetime('now'))
    `)
    if err != nil {
        return err
    }
    defer stmt.Close()

    for node, pre := range idx.PreOrder {
        post := idx.PostOrder[node]
        sccID := idx.NodeToSCC[node]
        _, err = stmt.ExecContext(ctx, node, sccID, pre, post, idx.EdgeType)
        if err != nil {
            return err
        }
    }

    return tx.Commit()
}

// LoadFromSQLite loads the index from the reachability_index table
func LoadIntervalIndex(ctx context.Context, db *sql.DB, edgeType int) (*IntervalIndex, error) {
    idx := &IntervalIndex{
        PreOrder:   make(map[string]int),
        PostOrder:  make(map[string]int),
        NodeToSCC:  make(map[string]int),
        SCCMembers: make(map[int][]string),
        EdgeType:   edgeType,
    }

    rows, err := db.QueryContext(ctx, `
        SELECT node_id, scc_id, pre_order, post_order
        FROM reachability_index
        WHERE edge_type = ?
    `, edgeType)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    for rows.Next() {
        var nodeID string
        var sccID, pre, post int
        if err := rows.Scan(&nodeID, &sccID, &pre, &post); err != nil {
            return nil, err
        }
        idx.PreOrder[nodeID] = pre
        idx.PostOrder[nodeID] = post
        idx.NodeToSCC[nodeID] = sccID
        idx.SCCMembers[sccID] = append(idx.SCCMembers[sccID], nodeID)
    }

    return idx, nil
}
```

### Layer 3: Iterative Fixed-Point Materialization

```go
// core/knowledge/inference/iterative_engine.go

package inference

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "time"

    vgdb "sylk/core/vectorgraphdb"
)

// EdgeKey uniquely identifies an edge
type EdgeKey struct {
    SourceID string
    TargetID string
    EdgeType int
}

// Edge represents an edge in the graph
type Edge struct {
    ID       int64
    SourceID string
    TargetID string
    EdgeType int
    Weight   float64
}

func (e Edge) Key() EdgeKey {
    return EdgeKey{e.SourceID, e.TargetID, e.EdgeType}
}

// IterativeInferenceEngine computes derived edges WITHOUT recursion
type IterativeInferenceEngine struct {
    db    *sql.DB
    rules []*InferenceRule

    // In-memory indexes for O(1) lookups during inference
    bySourceType map[int]map[string][]Edge // edgeType -> sourceID -> edges
    byTargetType map[int]map[string][]Edge // edgeType -> targetID -> edges
    edgeExists   map[EdgeKey]bool          // Fast existence check
}

// InferenceRule represents a Horn clause rule
type InferenceRule struct {
    RuleID      int64
    RuleName    string
    Antecedent  []RuleCondition
    Consequent  RuleConsequent
    Confidence  float64
    IsEnabled   bool
    Description string
}

// RuleCondition is one edge pattern to match
type RuleCondition struct {
    EdgeType int
    Source   string // Variable name (e.g., "A", "B")
    Target   string
}

// RuleConsequent is the edge to create
type RuleConsequent struct {
    EdgeType int
    Source   string
    Target   string
}

// NewIterativeInferenceEngine creates a new engine
func NewIterativeInferenceEngine(db *sql.DB) (*IterativeInferenceEngine, error) {
    engine := &IterativeInferenceEngine{
        db:           db,
        bySourceType: make(map[int]map[string][]Edge),
        byTargetType: make(map[int]map[string][]Edge),
        edgeExists:   make(map[EdgeKey]bool),
    }

    if err := engine.loadRules(); err != nil {
        return nil, err
    }

    return engine, nil
}

func (e *IterativeInferenceEngine) loadRules() error {
    rows, err := e.db.Query(`
        SELECT rule_id, rule_name, antecedent, consequent, confidence, is_enabled, description
        FROM inference_rules WHERE is_enabled = 1
    `)
    if err != nil {
        return err
    }
    defer rows.Close()

    for rows.Next() {
        var rule InferenceRule
        var antecedentJSON, consequentJSON string

        if err := rows.Scan(&rule.RuleID, &rule.RuleName, &antecedentJSON,
            &consequentJSON, &rule.Confidence, &rule.IsEnabled, &rule.Description); err != nil {
            return err
        }

        json.Unmarshal([]byte(antecedentJSON), &rule.Antecedent)
        json.Unmarshal([]byte(consequentJSON), &rule.Consequent)
        e.rules = append(e.rules, &rule)
    }

    return nil
}

// buildIndexes creates in-memory indexes for fast lookup
func (e *IterativeInferenceEngine) buildIndexes(edges []Edge) {
    e.bySourceType = make(map[int]map[string][]Edge)
    e.byTargetType = make(map[int]map[string][]Edge)
    e.edgeExists = make(map[EdgeKey]bool)

    for _, edge := range edges {
        // By source+type
        if e.bySourceType[edge.EdgeType] == nil {
            e.bySourceType[edge.EdgeType] = make(map[string][]Edge)
        }
        e.bySourceType[edge.EdgeType][edge.SourceID] = append(
            e.bySourceType[edge.EdgeType][edge.SourceID], edge)

        // By target+type
        if e.byTargetType[edge.EdgeType] == nil {
            e.byTargetType[edge.EdgeType] = make(map[string][]Edge)
        }
        e.byTargetType[edge.EdgeType][edge.TargetID] = append(
            e.byTargetType[edge.EdgeType][edge.TargetID], edge)

        // Existence
        e.edgeExists[edge.Key()] = true
    }
}

func (e *IterativeInferenceEngine) addToIndexes(edge Edge) {
    if e.bySourceType[edge.EdgeType] == nil {
        e.bySourceType[edge.EdgeType] = make(map[string][]Edge)
    }
    e.bySourceType[edge.EdgeType][edge.SourceID] = append(
        e.bySourceType[edge.EdgeType][edge.SourceID], edge)

    if e.byTargetType[edge.EdgeType] == nil {
        e.byTargetType[edge.EdgeType] = make(map[string][]Edge)
    }
    e.byTargetType[edge.EdgeType][edge.TargetID] = append(
        e.byTargetType[edge.EdgeType][edge.TargetID], edge)

    e.edgeExists[edge.Key()] = true
}

// Materialize computes all derived edges using iterative fixed-point
// NO RECURSION - uses explicit worklist
func (e *IterativeInferenceEngine) Materialize(ctx context.Context) error {
    // Phase 1: Load all base edges and build indexes
    baseEdges, err := e.loadAllEdges(ctx)
    if err != nil {
        return err
    }
    e.buildIndexes(baseEdges)

    // Phase 2: Initialize worklist with all base edges
    worklist := make([]Edge, len(baseEdges))
    copy(worklist, baseEdges)

    // Track derived edges to avoid duplicates
    derived := make(map[EdgeKey]bool)
    for _, edge := range baseEdges {
        derived[edge.Key()] = true
    }

    // Phase 3: Iterate until fixpoint (worklist empty)
    iteration := 0
    maxIterations := 1000 // Safety limit

    for len(worklist) > 0 && iteration < maxIterations {
        iteration++

        // Process batch from worklist
        batchSize := min(10000, len(worklist))
        batch := worklist[:batchSize]
        worklist = worklist[batchSize:]

        var newEdges []Edge
        for _, edge := range batch {
            for _, rule := range e.rules {
                inferred := e.applyRule(rule, edge, derived)
                for _, inf := range inferred {
                    if !derived[inf.Key()] {
                        derived[inf.Key()] = true
                        newEdges = append(newEdges, inf)
                        e.addToIndexes(inf)
                    }
                }
            }
        }

        // Add new edges to worklist for next iteration
        worklist = append(worklist, newEdges...)

        // Persist batch
        if len(newEdges) > 0 {
            if err := e.persistBatch(ctx, newEdges); err != nil {
                return err
            }
        }
    }

    return nil
}

// applyRule applies a single rule to an edge
// Returns any new inferences WITHOUT recursion
func (e *IterativeInferenceEngine) applyRule(rule *InferenceRule, trigger Edge, derived map[EdgeKey]bool) []Edge {
    var results []Edge

    for i, cond := range rule.Antecedent {
        if cond.EdgeType != trigger.EdgeType {
            continue
        }

        // Initialize bindings from trigger
        bindings := map[string]string{
            cond.Source: trigger.SourceID,
            cond.Target: trigger.TargetID,
        }

        // Get remaining conditions
        remaining := make([]RuleCondition, 0, len(rule.Antecedent)-1)
        for j, c := range rule.Antecedent {
            if j != i {
                remaining = append(remaining, c)
            }
        }

        // Find binding completions ITERATIVELY (no recursion!)
        completions := e.findBindingsIterative(bindings, remaining)

        for _, completion := range completions {
            sourceID := completion[rule.Consequent.Source]
            targetID := completion[rule.Consequent.Target]

            newEdge := Edge{
                SourceID: sourceID,
                TargetID: targetID,
                EdgeType: rule.Consequent.EdgeType,
            }

            if !derived[newEdge.Key()] {
                results = append(results, newEdge)
            }
        }
    }

    return results
}

// findBindingsIterative finds all binding completions WITHOUT recursion
// Uses explicit queue instead of call stack
func (e *IterativeInferenceEngine) findBindingsIterative(
    initial map[string]string,
    conditions []RuleCondition,
) []map[string]string {

    if len(conditions) == 0 {
        return []map[string]string{copyMap(initial)}
    }

    // Work item for the queue
    type workItem struct {
        bindings  map[string]string
        condIndex int
    }

    queue := []workItem{{bindings: copyMap(initial), condIndex: 0}}
    var results []map[string]string

    for len(queue) > 0 {
        item := queue[0]
        queue = queue[1:]

        if item.condIndex >= len(conditions) {
            // All conditions satisfied
            results = append(results, item.bindings)
            continue
        }

        cond := conditions[item.condIndex]
        sourceVal := item.bindings[cond.Source]
        targetVal := item.bindings[cond.Target]

        if sourceVal != "" && targetVal != "" {
            // Both bound - check existence via index (O(1))
            key := EdgeKey{sourceVal, targetVal, cond.EdgeType}
            if e.edgeExists[key] {
                queue = append(queue, workItem{
                    bindings:  item.bindings,
                    condIndex: item.condIndex + 1,
                })
            }
        } else if sourceVal != "" {
            // Source bound - lookup targets via index (O(fan-out))
            if edges, ok := e.bySourceType[cond.EdgeType][sourceVal]; ok {
                for _, edge := range edges {
                    newBindings := copyMap(item.bindings)
                    newBindings[cond.Target] = edge.TargetID
                    queue = append(queue, workItem{
                        bindings:  newBindings,
                        condIndex: item.condIndex + 1,
                    })
                }
            }
        } else if targetVal != "" {
            // Target bound - lookup sources via index (O(fan-in))
            if edges, ok := e.byTargetType[cond.EdgeType][targetVal]; ok {
                for _, edge := range edges {
                    newBindings := copyMap(item.bindings)
                    newBindings[cond.Source] = edge.SourceID
                    queue = append(queue, workItem{
                        bindings:  newBindings,
                        condIndex: item.condIndex + 1,
                    })
                }
            }
        }
        // If neither bound, skip (would require full scan - indicates bad rule design)
    }

    return results
}

// OnEdgeInserted handles incremental update when new edge added
func (e *IterativeInferenceEngine) OnEdgeInserted(ctx context.Context, newEdge Edge) error {
    e.addToIndexes(newEdge)

    // Mini-fixpoint: just this edge as trigger
    derived := make(map[EdgeKey]bool)
    worklist := []Edge{newEdge}

    var allNew []Edge

    for len(worklist) > 0 {
        edge := worklist[0]
        worklist = worklist[1:]

        for _, rule := range e.rules {
            inferred := e.applyRule(rule, edge, derived)
            for _, inf := range inferred {
                if !derived[inf.Key()] {
                    derived[inf.Key()] = true
                    worklist = append(worklist, inf)
                    allNew = append(allNew, inf)
                    e.addToIndexes(inf)
                }
            }
        }
    }

    return e.persistBatch(ctx, allNew)
}

// OnEdgeDeleted handles edge deletion with O(1) invalidation
// Uses normalized provenance table, NOT LIKE scans
func (e *IterativeInferenceEngine) OnEdgeDeleted(ctx context.Context, edgeID int64) error {
    // O(1) lookup via index on inference_provenance(evidence_edge_id)
    _, err := e.db.ExecContext(ctx, `
        UPDATE inferred_edges
        SET valid = 0
        WHERE id IN (
            SELECT inferred_edge_id
            FROM inference_provenance
            WHERE evidence_edge_id = ?
        )
    `, edgeID)

    return err
}

func (e *IterativeInferenceEngine) loadAllEdges(ctx context.Context) ([]Edge, error) {
    rows, err := e.db.QueryContext(ctx, `
        SELECT id, source_id, target_id, edge_type, weight FROM edges
    `)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var edges []Edge
    for rows.Next() {
        var edge Edge
        if err := rows.Scan(&edge.ID, &edge.SourceID, &edge.TargetID,
            &edge.EdgeType, &edge.Weight); err != nil {
            return nil, err
        }
        edges = append(edges, edge)
    }
    return edges, nil
}

func (e *IterativeInferenceEngine) persistBatch(ctx context.Context, edges []Edge) error {
    if len(edges) == 0 {
        return nil
    }

    tx, err := e.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    stmt, err := tx.PrepareContext(ctx, `
        INSERT OR IGNORE INTO inferred_edges
        (source_id, target_id, edge_type, rule_id, confidence, depth, computed_at)
        VALUES (?, ?, ?, 0, 1.0, 1, datetime('now'))
    `)
    if err != nil {
        return err
    }
    defer stmt.Close()

    for _, edge := range edges {
        _, err = stmt.ExecContext(ctx, edge.SourceID, edge.TargetID, edge.EdgeType)
        if err != nil {
            return err
        }
    }

    return tx.Commit()
}

func copyMap(m map[string]string) map[string]string {
    c := make(map[string]string, len(m))
    for k, v := range m {
        c[k] = v
    }
    return c
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
```

### Layer 4: Hybrid Query Coordinator

```go
// core/knowledge/query/hybrid_coordinator.go

package query

import (
    "context"
    "database/sql"

    "github.com/blevesearch/bleve/v2"

    "sylk/core/knowledge/graph"
    "sylk/core/vectorgraphdb/hnsw"
)

// HybridQueryCoordinator unifies SQLite graph + Bleve text + HNSW vectors
type HybridQueryCoordinator struct {
    db           *sql.DB
    bleveIndex   bleve.Index
    hnswIndex    *hnsw.Index
    reachability map[int]*graph.IntervalIndex // edgeType -> index
}

// HybridQuery represents a multi-modal query
type HybridQuery struct {
    TextQuery     string           // Bleve full-text search
    SemanticQuery *SemanticFilter  // HNSW vector similarity
    GraphPattern  *GraphPattern    // Graph structure pattern
    ReachableFrom string           // Transitive closure filter
    EdgeType      int              // For reachability queries
}

type SemanticFilter struct {
    Embedding []float32
    K         int
}

type GraphPattern struct {
    Conditions []PatternCondition
}

type PatternCondition struct {
    EdgeType int
    Source   string // Variable or concrete ID
    Target   string
}

type QueryResult struct {
    NodeIDs []string
}

// Query executes a hybrid query across all indexes
func (hq *HybridQueryCoordinator) Query(ctx context.Context, q *HybridQuery) (*QueryResult, error) {
    var candidates []string

    // Step 1: Text filter via Bleve (if specified)
    if q.TextQuery != "" {
        searchReq := bleve.NewSearchRequest(bleve.NewQueryStringQuery(q.TextQuery))
        searchReq.Size = 1000
        searchReq.Fields = []string{"node_id"}

        result, err := hq.bleveIndex.Search(searchReq)
        if err != nil {
            return nil, err
        }

        for _, hit := range result.Hits {
            candidates = append(candidates, hit.ID)
        }
    }

    // Step 2: Semantic filter via HNSW (if specified)
    if q.SemanticQuery != nil {
        neighbors := hq.hnswIndex.SearchKNN(q.SemanticQuery.Embedding, q.SemanticQuery.K)

        if len(candidates) > 0 {
            // Intersect with text results
            candidateSet := make(map[string]bool)
            for _, c := range candidates {
                candidateSet[c] = true
            }
            var filtered []string
            for _, n := range neighbors {
                if candidateSet[n.ID] {
                    filtered = append(filtered, n.ID)
                }
            }
            candidates = filtered
        } else {
            for _, n := range neighbors {
                candidates = append(candidates, n.ID)
            }
        }
    }

    // Step 3: Reachability filter (if specified) - O(1) per candidate!
    if q.ReachableFrom != "" {
        idx := hq.reachability[q.EdgeType]
        if idx != nil {
            var reachable []string
            for _, c := range candidates {
                if idx.CanReach(q.ReachableFrom, c) {
                    reachable = append(reachable, c)
                }
            }
            candidates = reachable
        }
    }

    return &QueryResult{NodeIDs: candidates}, nil
}

// RebuildReachabilityIndex rebuilds the interval index for an edge type
func (hq *HybridQueryCoordinator) RebuildReachabilityIndex(
    ctx context.Context,
    edgeType int,
) error {
    // Load graph for this edge type
    adjList, err := hq.loadAdjacencyList(ctx, edgeType)
    if err != nil {
        return err
    }

    // Find SCCs (iterative, no recursion)
    tarjan := graph.NewIterativeTarjan(adjList)
    sccs := tarjan.FindSCCs()

    // Condense to DAG
    condensed := graph.CondenseGraph(adjList, sccs)

    // Build interval index
    idx := graph.BuildIntervalIndex(condensed, edgeType)

    // Persist to SQLite
    if err := idx.PersistToSQLite(ctx, hq.db); err != nil {
        return err
    }

    // Cache in memory
    hq.reachability[edgeType] = idx

    return nil
}

func (hq *HybridQueryCoordinator) loadAdjacencyList(
    ctx context.Context,
    edgeType int,
) (*graph.AdjacencyList, error) {
    adj := graph.NewAdjacencyList()

    rows, err := hq.db.QueryContext(ctx, `
        SELECT source_id, target_id FROM edges WHERE edge_type = ?
    `, edgeType)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    for rows.Next() {
        var source, target string
        if err := rows.Scan(&source, &target); err != nil {
            return nil, err
        }
        adj.AddEdge(source, target)
    }

    return adj, nil
}
```

**ACCEPTANCE CRITERIA - Inference Engine:**
- [ ] All algorithms use explicit stacks/queues (zero recursion)
- [ ] SCC detection handles graphs with 100K+ nodes without stack overflow
- [ ] Transitive reachability queries complete in O(1) via interval index
- [ ] Transitive calls computed correctly (A→B→C implies A→→C)
- [ ] Transitive imports computed correctly
- [ ] Implements-method inference works (S implements I, I has M → S must have M)
- [ ] Incremental update adds new inferences in <10ms
- [ ] Edge deletion invalidates dependent inferences in O(1) via provenance index
- [ ] Full recomputation completes in <5min for 100K edges
- [ ] Inferred edges queryable alongside direct edges
- [ ] Confidence correctly propagated through inference chain
- [ ] Hybrid queries combine Bleve text + HNSW vectors + graph structure

### Versioned TC Reads (Snapshot Isolation)

The reachability index is read-heavy but occasionally rebuilt. To prevent race conditions where readers see partially updated state, we use **versioned snapshots** with atomic pointer swap.

**Problem:**
- Writer rebuilds index (takes seconds)
- Readers query during rebuild
- Without isolation: readers see inconsistent pre/post order values

**Solution: Copy-on-Write with Atomic Swap**

```go
// core/knowledge/relations/versioned_index.go

package relations

import (
    "context"
    "sync/atomic"
    "time"
)

// VersionedIntervalIndex provides snapshot isolation for reachability queries
type VersionedIntervalIndex struct {
    current atomic.Pointer[IndexSnapshot]
}

// IndexSnapshot is an immutable point-in-time view
type IndexSnapshot struct {
    version   uint64
    createdAt time.Time
    edgeType  int

    // Immutable maps - never modified after creation
    intervals map[string]*IntervalLabel  // nodeID -> interval
    sccMap    map[string]int             // nodeID -> sccID
}

// IntervalLabel contains the pre/post order for O(1) reachability
type IntervalLabel struct {
    Pre       int
    Post      int
    TopoOrder int
    SCCID     int
}

func NewVersionedIntervalIndex() *VersionedIntervalIndex {
    v := &VersionedIntervalIndex{}
    // Initialize with empty snapshot
    v.current.Store(&IndexSnapshot{
        version:   0,
        createdAt: time.Now(),
        intervals: make(map[string]*IntervalLabel),
        sccMap:    make(map[string]int),
    })
    return v
}

// Read gets a consistent snapshot - lock-free, O(1)
func (v *VersionedIntervalIndex) Read() *IndexSnapshot {
    return v.current.Load()
}

// CanReach checks reachability using current snapshot - O(1)
func (v *VersionedIntervalIndex) CanReach(from, to string) bool {
    snap := v.Read()
    return snap.CanReach(from, to)
}

// CanReach on snapshot - pure function, no shared state
func (s *IndexSnapshot) CanReach(from, to string) bool {
    fromLabel, ok1 := s.intervals[from]
    toLabel, ok2 := s.intervals[to]
    if !ok1 || !ok2 {
        return false
    }

    // Same SCC: all nodes mutually reachable
    if fromLabel.SCCID == toLabel.SCCID {
        return true
    }

    // Interval containment: from is ancestor of to
    return fromLabel.Pre <= toLabel.Pre && fromLabel.Post >= toLabel.Post
}

// GetVersion returns current snapshot version
func (s *IndexSnapshot) GetVersion() uint64 {
    return s.version
}

// Rebuild creates new snapshot and atomically swaps - single writer assumed
func (v *VersionedIntervalIndex) Rebuild(
    ctx context.Context,
    edgeType int,
    adjList *AdjacencyList,
) error {
    old := v.current.Load()

    // Build entirely new snapshot (expensive, but doesn't block readers)
    tarjan := NewIterativeTarjan(adjList)
    sccs := tarjan.FindSCCs()
    condensed := CondenseGraph(adjList, sccs)

    // Compute interval labels
    intervals := make(map[string]*IntervalLabel)
    sccMap := make(map[string]int)

    counter := 0
    var computeIntervals func(sccID int)
    // Use iterative DFS for interval computation
    type frame struct {
        sccID    int
        childIdx int
        children []int
        phase    int
    }

    stack := []frame{{sccID: condensed.RootSCC, childIdx: 0, children: condensed.Children[condensed.RootSCC], phase: 0}}
    preOrder := make(map[int]int)
    postOrder := make(map[int]int)
    topoOrder := make(map[int]int)
    topoCounter := 0

    for len(stack) > 0 {
        f := &stack[len(stack)-1]

        switch f.phase {
        case 0: // Enter
            counter++
            preOrder[f.sccID] = counter
            f.phase = 1

        case 1: // Process children
            if f.childIdx < len(f.children) {
                child := f.children[f.childIdx]
                f.childIdx++
                if _, visited := preOrder[child]; !visited {
                    stack = append(stack, frame{
                        sccID:    child,
                        childIdx: 0,
                        children: condensed.Children[child],
                        phase:    0,
                    })
                }
            } else {
                f.phase = 2
            }

        case 2: // Exit
            counter++
            postOrder[f.sccID] = counter
            topoOrder[f.sccID] = topoCounter
            topoCounter++
            stack = stack[:len(stack)-1]
        }
    }

    // Assign labels to all nodes
    for sccID, members := range sccs {
        for _, nodeID := range members {
            intervals[nodeID] = &IntervalLabel{
                Pre:       preOrder[sccID],
                Post:      postOrder[sccID],
                TopoOrder: topoOrder[sccID],
                SCCID:     sccID,
            }
            sccMap[nodeID] = sccID
        }
    }

    // Create new immutable snapshot
    newSnap := &IndexSnapshot{
        version:   old.version + 1,
        createdAt: time.Now(),
        edgeType:  edgeType,
        intervals: intervals,
        sccMap:    sccMap,
    }

    // Atomic swap - readers immediately see new version
    v.current.Store(newSnap)

    return nil
}
```

**Guarantees:**
- **No torn reads**: Readers always see complete, consistent snapshot
- **No blocking**: Writers never block readers, readers never block writers
- **Progress**: Writer always completes (no deadlock possible)
- **Memory safety**: Old snapshot GC'd after all readers release it

**ACCEPTANCE CRITERIA - Versioned TC Reads:**
- [ ] Concurrent reads during rebuild return consistent results
- [ ] Version number increments monotonically
- [ ] Old snapshots garbage collected after readers release
- [ ] No mutex contention under read-heavy workloads
- [ ] Rebuild completes even with continuous read traffic

### Stratified Semi-Naive Evaluation

Naive forward chaining recomputes all derivations every iteration. **Semi-naive evaluation** only computes new facts using delta tables, achieving orders of magnitude speedup.

**Problem with Naive Evaluation:**
```
Iteration 1: Derive all facts from base
Iteration 2: Derive all facts from (base + iter1)  // Recomputes iter1 facts!
Iteration 3: Derive all facts from (base + iter1 + iter2)  // Recomputes everything!
```

**Semi-Naive Insight:**
New facts in iteration N can only come from combining:
- New facts from iteration N-1 (delta) with all existing facts
- NOT from combining old facts with old facts (already computed)

```
Δ(N) = derive(Δ(N-1) × All) ∪ derive(All × Δ(N-1)) - Already_Known
```

**Implementation:**

```go
// core/knowledge/relations/semi_naive.go

package relations

import (
    "context"
)

// DeltaTable tracks new facts discovered in current iteration
type DeltaTable struct {
    edges     map[EdgeKey]struct{}
    iteration int
}

// EdgeKey uniquely identifies an edge
type EdgeKey struct {
    Source   string
    Target   string
    EdgeType int
}

func NewDeltaTable(iteration int) *DeltaTable {
    return &DeltaTable{
        edges:     make(map[EdgeKey]struct{}),
        iteration: iteration,
    }
}

func (d *DeltaTable) Add(source, target string, edgeType int) bool {
    key := EdgeKey{Source: source, Target: target, EdgeType: edgeType}
    if _, exists := d.edges[key]; exists {
        return false
    }
    d.edges[key] = struct{}{}
    return true
}

func (d *DeltaTable) IsEmpty() bool {
    return len(d.edges) == 0
}

func (d *DeltaTable) Edges() []EdgeKey {
    result := make([]EdgeKey, 0, len(d.edges))
    for k := range d.edges {
        result = append(result, k)
    }
    return result
}

// StratifiedSemiNaive performs inference with delta tables
type StratifiedSemiNaive struct {
    rules       []*InferenceRule
    strata      [][]int          // strata[i] = rule indices in stratum i
    allFacts    map[EdgeKey]struct{}
    db          Database
}

// Stratum groups rules by dependency level
// Rules in stratum N only depend on rules in strata 0..N-1
func (s *StratifiedSemiNaive) computeStrata() {
    // Build rule dependency graph
    deps := make(map[int][]int) // rule -> rules it depends on

    for i, rule := range s.rules {
        for j, other := range s.rules {
            if i != j && s.ruleProduces(other, rule) {
                deps[i] = append(deps[i], j)
            }
        }
    }

    // Topological sort (Kahn's algorithm - iterative)
    inDegree := make(map[int]int)
    for i := range s.rules {
        inDegree[i] = len(deps[i])
    }

    s.strata = nil
    remaining := len(s.rules)

    for remaining > 0 {
        // Find all rules with no unprocessed dependencies
        var stratum []int
        for i := 0; i < len(s.rules); i++ {
            if inDegree[i] == 0 {
                stratum = append(stratum, i)
                inDegree[i] = -1 // Mark as processed
            }
        }

        if len(stratum) == 0 {
            // Cycle detected - group remaining into single stratum
            for i := 0; i < len(s.rules); i++ {
                if inDegree[i] > 0 {
                    stratum = append(stratum, i)
                    inDegree[i] = -1
                }
            }
        }

        s.strata = append(s.strata, stratum)
        remaining -= len(stratum)

        // Decrease in-degree for rules depending on this stratum
        for _, ruleIdx := range stratum {
            for i, ruleDeps := range deps {
                for _, dep := range ruleDeps {
                    if dep == ruleIdx && inDegree[i] > 0 {
                        inDegree[i]--
                    }
                }
            }
        }
    }
}

// ruleProduces checks if rule A's output can be rule B's input
func (s *StratifiedSemiNaive) ruleProduces(a, b *InferenceRule) bool {
    for _, cond := range b.Conditions {
        if cond.EdgeType == a.OutputType {
            return true
        }
    }
    return false
}

// Evaluate runs semi-naive evaluation with stratification
func (s *StratifiedSemiNaive) Evaluate(ctx context.Context) error {
    s.computeStrata()

    // Process each stratum in order
    for stratumIdx, stratum := range s.strata {
        if err := s.evaluateStratum(ctx, stratumIdx, stratum); err != nil {
            return err
        }
    }

    return nil
}

func (s *StratifiedSemiNaive) evaluateStratum(
    ctx context.Context,
    stratumIdx int,
    ruleIndices []int,
) error {
    // Initialize delta with base facts relevant to this stratum
    delta := NewDeltaTable(0)

    // Seed delta with existing edges that match rule inputs
    for _, ruleIdx := range ruleIndices {
        rule := s.rules[ruleIdx]
        for _, cond := range rule.Conditions {
            edges, _ := s.db.GetEdgesByType(ctx, cond.EdgeType)
            for _, e := range edges {
                delta.Add(e.Source, e.Target, cond.EdgeType)
            }
        }
    }

    iteration := 0
    maxIterations := 1000 // Prevent infinite loops

    for !delta.IsEmpty() && iteration < maxIterations {
        iteration++
        nextDelta := NewDeltaTable(iteration)

        // For each rule in this stratum
        for _, ruleIdx := range ruleIndices {
            rule := s.rules[ruleIdx]

            // Semi-naive: only consider derivations involving delta facts
            newEdges := s.applyRuleSemiNaive(ctx, rule, delta)

            for _, edge := range newEdges {
                // Only add if truly new (not in allFacts)
                if _, exists := s.allFacts[edge]; !exists {
                    s.allFacts[edge] = struct{}{}
                    nextDelta.Add(edge.Source, edge.Target, edge.EdgeType)

                    // Persist to database
                    s.db.InsertInferredEdge(ctx, edge.Source, edge.Target, edge.EdgeType)
                }
            }
        }

        delta = nextDelta
    }

    return nil
}

// applyRuleSemiNaive applies rule using ONLY facts involving delta
func (s *StratifiedSemiNaive) applyRuleSemiNaive(
    ctx context.Context,
    rule *InferenceRule,
    delta *DeltaTable,
) []EdgeKey {
    var results []EdgeKey

    // For transitive closure rule (A→B, B→C ⊢ A→C):
    // Semi-naive computes: (Δ × All) ∪ (All × Δ)
    // Instead of: All × All

    if len(rule.Conditions) == 2 && rule.IsTransitive() {
        // Special case for transitive rules
        deltaEdges := delta.Edges()

        // Δ × All: New first edge, any second edge
        for _, d := range deltaEdges {
            if d.EdgeType == rule.Conditions[0].EdgeType {
                // Find all edges where d.Target == e.Source
                seconds, _ := s.db.GetEdgesFrom(ctx, d.Target, rule.Conditions[1].EdgeType)
                for _, second := range seconds {
                    results = append(results, EdgeKey{
                        Source:   d.Source,
                        Target:   second.Target,
                        EdgeType: rule.OutputType,
                    })
                }
            }
        }

        // All × Δ: Any first edge, new second edge
        for _, d := range deltaEdges {
            if d.EdgeType == rule.Conditions[1].EdgeType {
                // Find all edges where e.Target == d.Source
                firsts, _ := s.db.GetEdgesTo(ctx, d.Source, rule.Conditions[0].EdgeType)
                for _, first := range firsts {
                    results = append(results, EdgeKey{
                        Source:   first.Source,
                        Target:   d.Target,
                        EdgeType: rule.OutputType,
                    })
                }
            }
        }
    } else {
        // General case: use iterative binding with delta constraint
        results = s.applyGeneralRuleSemiNaive(ctx, rule, delta)
    }

    return results
}

func (s *StratifiedSemiNaive) applyGeneralRuleSemiNaive(
    ctx context.Context,
    rule *InferenceRule,
    delta *DeltaTable,
) []EdgeKey {
    var results []EdgeKey
    deltaSet := make(map[EdgeKey]struct{})
    for _, e := range delta.Edges() {
        deltaSet[e] = struct{}{}
    }

    // For each condition position, try using a delta fact there
    for condIdx := range rule.Conditions {
        // Bind condition[condIdx] to delta facts
        for _, deltaEdge := range delta.Edges() {
            if deltaEdge.EdgeType != rule.Conditions[condIdx].EdgeType {
                continue
            }

            // Initialize bindings from this delta edge
            bindings := make(map[string]string)
            cond := rule.Conditions[condIdx]
            if cond.SourceVar != "" {
                bindings[cond.SourceVar] = deltaEdge.Source
            }
            if cond.TargetVar != "" {
                bindings[cond.TargetVar] = deltaEdge.Target
            }

            // Resolve remaining conditions (can use all facts)
            resolver := NewIterativeBindingResolver(s.db)
            allBindings := resolver.ResolveRemaining(ctx, bindings, rule.Conditions, condIdx)

            // Generate output edges
            for _, b := range allBindings {
                source := b[rule.OutputSourceVar]
                target := b[rule.OutputTargetVar]
                if source != "" && target != "" {
                    results = append(results, EdgeKey{
                        Source:   source,
                        Target:   target,
                        EdgeType: rule.OutputType,
                    })
                }
            }
        }
    }

    return results
}
```

**Complexity Comparison:**

| Approach | Per Iteration | Total (k iterations) |
|----------|---------------|----------------------|
| Naive | O(E²) | O(k × E²) |
| Semi-Naive | O(Δ × E) | O(E × log E) |

Where Δ = new facts per iteration, E = total edges. Semi-naive is asymptotically faster because Δ shrinks each iteration.

**ACCEPTANCE CRITERIA - Semi-Naive Evaluation:**
- [ ] Delta tables correctly track new facts per iteration
- [ ] Stratification correctly orders rules by dependency
- [ ] No redundant derivations (facts derived at most once)
- [ ] Transitive closure computed correctly
- [ ] Performance: 10x faster than naive for graphs with >10K edges
- [ ] Handles cyclic dependencies within strata

### Product Quantization for HNSW

Storing full float32[768] vectors requires 3,072 bytes per vector. **Product Quantization (PQ)** compresses to ~96 bytes (32x reduction) with minimal recall loss.

**How PQ Works:**
1. Split 768-dim vector into 96 subvectors of 8 dimensions each
2. Train 256 centroids per subspace (k-means)
3. Encode each subvector as 1-byte centroid ID
4. Result: 96 bytes vs 3,072 bytes

**Distance Computation:**
- Precompute distance table: dist[subspace][centroid] for query
- Sum distances: Σ dist_table[i][code[i]]
- Asymmetric: query is full precision, database is quantized

```go
// core/vectorgraphdb/quantization/product_quantizer.go

package quantization

import (
    "encoding/binary"
    "math"
    "math/rand"
)

const (
    NumSubspaces   = 96   // 768 / 8 = 96 subvectors
    SubspaceDim    = 8    // Dimensions per subspace
    NumCentroids   = 256  // Centroids per subspace (fits in 1 byte)
    FullVectorDim  = 768
    CompressedSize = 96   // 96 bytes vs 3072 bytes = 32x compression
)

// ProductQuantizer compresses vectors using product quantization
type ProductQuantizer struct {
    // Centroids: [NumSubspaces][NumCentroids][SubspaceDim]
    centroids [NumSubspaces][NumCentroids][SubspaceDim]float32
    trained   bool
}

// PQCode is a compressed vector representation
type PQCode [NumSubspaces]uint8

// NewProductQuantizer creates untrained quantizer
func NewProductQuantizer() *ProductQuantizer {
    return &ProductQuantizer{}
}

// Train learns centroids from training vectors using k-means
func (pq *ProductQuantizer) Train(vectors [][]float32, iterations int) {
    if len(vectors) == 0 {
        return
    }

    // Train each subspace independently
    for sub := 0; sub < NumSubspaces; sub++ {
        // Extract subvectors for this subspace
        subvectors := make([][]float32, len(vectors))
        for i, v := range vectors {
            subvectors[i] = v[sub*SubspaceDim : (sub+1)*SubspaceDim]
        }

        // K-means clustering (iterative, no recursion)
        pq.trainSubspace(sub, subvectors, iterations)
    }

    pq.trained = true
}

func (pq *ProductQuantizer) trainSubspace(sub int, vectors [][]float32, iterations int) {
    n := len(vectors)
    if n < NumCentroids {
        // Not enough vectors - use vectors directly as centroids
        for i := 0; i < n; i++ {
            copy(pq.centroids[sub][i][:], vectors[i])
        }
        return
    }

    // Initialize centroids randomly (k-means++)
    assignments := make([]int, n)

    // Random initial centroid
    idx := rand.Intn(n)
    copy(pq.centroids[sub][0][:], vectors[idx])

    // K-means++ initialization
    for c := 1; c < NumCentroids; c++ {
        // Compute distances to nearest centroid
        dists := make([]float64, n)
        total := 0.0
        for i, v := range vectors {
            minDist := math.MaxFloat64
            for j := 0; j < c; j++ {
                d := pq.subspaceDistance(v, pq.centroids[sub][j][:])
                if d < minDist {
                    minDist = d
                }
            }
            dists[i] = minDist
            total += minDist
        }

        // Weighted random selection
        r := rand.Float64() * total
        cumsum := 0.0
        selected := 0
        for i, d := range dists {
            cumsum += d
            if cumsum >= r {
                selected = i
                break
            }
        }
        copy(pq.centroids[sub][c][:], vectors[selected])
    }

    // K-means iterations
    for iter := 0; iter < iterations; iter++ {
        // Assignment step
        for i, v := range vectors {
            minDist := math.MaxFloat64
            minIdx := 0
            for c := 0; c < NumCentroids; c++ {
                d := pq.subspaceDistance(v, pq.centroids[sub][c][:])
                if d < minDist {
                    minDist = d
                    minIdx = c
                }
            }
            assignments[i] = minIdx
        }

        // Update step
        counts := make([]int, NumCentroids)
        sums := make([][SubspaceDim]float64, NumCentroids)

        for i, v := range vectors {
            c := assignments[i]
            counts[c]++
            for d := 0; d < SubspaceDim; d++ {
                sums[c][d] += float64(v[d])
            }
        }

        for c := 0; c < NumCentroids; c++ {
            if counts[c] > 0 {
                for d := 0; d < SubspaceDim; d++ {
                    pq.centroids[sub][c][d] = float32(sums[c][d] / float64(counts[c]))
                }
            }
        }
    }
}

func (pq *ProductQuantizer) subspaceDistance(a []float32, b []float32) float64 {
    sum := 0.0
    for i := 0; i < SubspaceDim; i++ {
        d := float64(a[i] - b[i])
        sum += d * d
    }
    return sum
}

// Encode compresses a full vector to PQ code
func (pq *ProductQuantizer) Encode(vector []float32) PQCode {
    var code PQCode

    for sub := 0; sub < NumSubspaces; sub++ {
        subvec := vector[sub*SubspaceDim : (sub+1)*SubspaceDim]

        // Find nearest centroid
        minDist := math.MaxFloat64
        minIdx := 0
        for c := 0; c < NumCentroids; c++ {
            d := pq.subspaceDistance(subvec, pq.centroids[sub][c][:])
            if d < minDist {
                minDist = d
                minIdx = c
            }
        }
        code[sub] = uint8(minIdx)
    }

    return code
}

// DistanceTable precomputes distances from query to all centroids
type DistanceTable [NumSubspaces][NumCentroids]float32

// ComputeDistanceTable builds lookup table for fast distance computation
func (pq *ProductQuantizer) ComputeDistanceTable(query []float32) DistanceTable {
    var table DistanceTable

    for sub := 0; sub < NumSubspaces; sub++ {
        subquery := query[sub*SubspaceDim : (sub+1)*SubspaceDim]
        for c := 0; c < NumCentroids; c++ {
            d := float32(0)
            for i := 0; i < SubspaceDim; i++ {
                diff := subquery[i] - pq.centroids[sub][c][i]
                d += diff * diff
            }
            table[sub][c] = d
        }
    }

    return table
}

// AsymmetricDistance computes distance using precomputed table - O(96) not O(768)
func (pq *ProductQuantizer) AsymmetricDistance(table *DistanceTable, code PQCode) float32 {
    var dist float32
    for sub := 0; sub < NumSubspaces; sub++ {
        dist += table[sub][code[sub]]
    }
    return dist
}

// Serialize writes PQ code to bytes
func (code PQCode) Serialize() []byte {
    return code[:]
}

// DeserializePQCode reads PQ code from bytes
func DeserializePQCode(data []byte) PQCode {
    var code PQCode
    copy(code[:], data)
    return code
}

// SerializeCentroids writes trained centroids for persistence
func (pq *ProductQuantizer) SerializeCentroids() []byte {
    // 96 subspaces × 256 centroids × 8 dims × 4 bytes = 786,432 bytes
    data := make([]byte, NumSubspaces*NumCentroids*SubspaceDim*4)
    offset := 0

    for sub := 0; sub < NumSubspaces; sub++ {
        for c := 0; c < NumCentroids; c++ {
            for d := 0; d < SubspaceDim; d++ {
                binary.LittleEndian.PutUint32(data[offset:], math.Float32bits(pq.centroids[sub][c][d]))
                offset += 4
            }
        }
    }

    return data
}

// DeserializeCentroids loads trained centroids from bytes
func (pq *ProductQuantizer) DeserializeCentroids(data []byte) {
    offset := 0

    for sub := 0; sub < NumSubspaces; sub++ {
        for c := 0; c < NumCentroids; c++ {
            for d := 0; d < SubspaceDim; d++ {
                pq.centroids[sub][c][d] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
                offset += 4
            }
        }
    }

    pq.trained = true
}
```

**HNSW Integration:**

```go
// core/vectorgraphdb/hnsw/quantized_hnsw.go

package hnsw

import (
    "sylk/core/vectorgraphdb/quantization"
)

// QuantizedHNSW stores PQ codes instead of full vectors
type QuantizedHNSW struct {
    // Graph structure (unchanged)
    layers     [][]*Node
    entryPoint *Node

    // Quantization
    pq          *quantization.ProductQuantizer
    codes       map[string]quantization.PQCode  // nodeID -> 96 bytes
    fullVectors map[string][]float32            // Only for top layer (optional)

    // Config
    storeFullInTop bool // Store full vectors in top layer for precision
}

// Search uses asymmetric distance for candidate selection
func (h *QuantizedHNSW) Search(query []float32, k int) []SearchResult {
    // Precompute distance table once - O(96 × 256)
    distTable := h.pq.ComputeDistanceTable(query)

    // Search using PQ distances - O(96) per comparison instead of O(768)
    candidates := h.searchWithTable(&distTable, k*10) // Over-fetch

    // Optional: Re-rank top candidates with full vectors
    if h.storeFullInTop && len(candidates) > 0 {
        candidates = h.rerankWithFullVectors(query, candidates, k)
    }

    return candidates[:min(k, len(candidates))]
}

func (h *QuantizedHNSW) searchWithTable(
    table *quantization.DistanceTable,
    ef int,
) []SearchResult {
    // Standard HNSW search but using PQ distances
    var results []SearchResult

    // ... HNSW graph traversal using:
    // dist := h.pq.AsymmetricDistance(table, h.codes[nodeID])

    return results
}
```

**Memory Comparison:**

| Storage | Per Vector | 1M Vectors | 10M Vectors |
|---------|------------|------------|-------------|
| Full float32 | 3,072 B | 2.9 GB | 29 GB |
| PQ (96 bytes) | 96 B | 91 MB | 910 MB |
| Savings | 32x | 32x | 32x |

**ACCEPTANCE CRITERIA - Product Quantization:**
- [ ] Compression ratio: ≥30x (96 bytes vs 3072 bytes)
- [ ] Recall@10: ≥95% compared to exact search
- [ ] Training completes in <60s for 100K vectors
- [ ] Search latency: <2x overhead vs full vectors
- [ ] Centroids persist and reload correctly
- [ ] Memory usage matches theoretical compression ratio

### MVCC Version Chains

Replace RWMutex locking with **Multi-Version Concurrency Control (MVCC)** for lock-free reads and better write concurrency.

**MVCC Principles:**
- Writers create new versions, never modify existing data
- Readers see consistent snapshot at their start timestamp
- No read locks needed - reads never block writes
- Old versions garbage collected when no longer needed

```go
// core/vectorgraphdb/mvcc/version_chain.go

package mvcc

import (
    "sync"
    "sync/atomic"
    "time"
    "unsafe"
)

// Version represents a single version in the chain
type Version struct {
    txnID     uint64      // Transaction that created this version
    timestamp time.Time   // Commit timestamp
    data      interface{} // Actual data (immutable after creation)
    next      *Version    // Pointer to older version (nil = oldest)
    deleted   bool        // Tombstone marker
}

// VersionChain manages versions for a single record
type VersionChain struct {
    head unsafe.Pointer // *Version - newest version
}

// NewVersionChain creates chain with initial version
func NewVersionChain(txnID uint64, data interface{}) *VersionChain {
    v := &Version{
        txnID:     txnID,
        timestamp: time.Now(),
        data:      data,
        next:      nil,
        deleted:   false,
    }
    vc := &VersionChain{}
    atomic.StorePointer(&vc.head, unsafe.Pointer(v))
    return vc
}

// Read returns version visible to given transaction
func (vc *VersionChain) Read(readTxnID uint64, readTime time.Time) (interface{}, bool) {
    // Traverse chain from newest to oldest
    v := (*Version)(atomic.LoadPointer(&vc.head))

    for v != nil {
        // Version is visible if:
        // 1. Created by committed transaction before our read time
        // 2. OR created by our own transaction
        if v.timestamp.Before(readTime) || v.timestamp.Equal(readTime) || v.txnID == readTxnID {
            if v.deleted {
                return nil, false // Record was deleted
            }
            return v.data, true
        }
        v = v.next
    }

    return nil, false // No visible version
}

// Write adds new version to chain
func (vc *VersionChain) Write(txnID uint64, data interface{}) bool {
    newVersion := &Version{
        txnID:     txnID,
        timestamp: time.Now(),
        data:      data,
        deleted:   false,
    }

    // CAS loop to atomically prepend
    for {
        oldHead := atomic.LoadPointer(&vc.head)
        newVersion.next = (*Version)(oldHead)

        if atomic.CompareAndSwapPointer(&vc.head, oldHead, unsafe.Pointer(newVersion)) {
            return true
        }
        // CAS failed, retry
    }
}

// Delete adds tombstone version
func (vc *VersionChain) Delete(txnID uint64) bool {
    tombstone := &Version{
        txnID:     txnID,
        timestamp: time.Now(),
        data:      nil,
        deleted:   true,
    }

    for {
        oldHead := atomic.LoadPointer(&vc.head)
        tombstone.next = (*Version)(oldHead)

        if atomic.CompareAndSwapPointer(&vc.head, oldHead, unsafe.Pointer(tombstone)) {
            return true
        }
    }
}

// MVCCStore provides MVCC access to a collection
type MVCCStore struct {
    chains    sync.Map  // key -> *VersionChain
    txnIDGen  atomic.Uint64
    gcTicker  *time.Ticker
    minActive atomic.Uint64 // Oldest active transaction
}

// Transaction represents an MVCC transaction
type Transaction struct {
    id        uint64
    startTime time.Time
    store     *MVCCStore
    writes    map[string]interface{} // Buffered writes
    deletes   map[string]struct{}    // Buffered deletes
}

func NewMVCCStore() *MVCCStore {
    s := &MVCCStore{
        gcTicker: time.NewTicker(time.Minute),
    }
    go s.gcLoop()
    return s
}

// Begin starts new transaction
func (s *MVCCStore) Begin() *Transaction {
    return &Transaction{
        id:        s.txnIDGen.Add(1),
        startTime: time.Now(),
        store:     s,
        writes:    make(map[string]interface{}),
        deletes:   make(map[string]struct{}),
    }
}

// Get reads value visible to this transaction
func (t *Transaction) Get(key string) (interface{}, bool) {
    // Check local writes first
    if v, ok := t.writes[key]; ok {
        return v, true
    }
    if _, ok := t.deletes[key]; ok {
        return nil, false
    }

    // Read from chain
    if chainI, ok := t.store.chains.Load(key); ok {
        chain := chainI.(*VersionChain)
        return chain.Read(t.id, t.startTime)
    }
    return nil, false
}

// Put buffers write (applied on commit)
func (t *Transaction) Put(key string, value interface{}) {
    delete(t.deletes, key)
    t.writes[key] = value
}

// Delete buffers deletion
func (t *Transaction) Delete(key string) {
    delete(t.writes, key)
    t.deletes[key] = struct{}{}
}

// Commit applies all buffered writes
func (t *Transaction) Commit() error {
    // Apply writes
    for key, value := range t.writes {
        chainI, loaded := t.store.chains.LoadOrStore(key, NewVersionChain(t.id, value))
        if loaded {
            chain := chainI.(*VersionChain)
            chain.Write(t.id, value)
        }
    }

    // Apply deletes
    for key := range t.deletes {
        if chainI, ok := t.store.chains.Load(key); ok {
            chain := chainI.(*VersionChain)
            chain.Delete(t.id)
        }
    }

    // Clear buffers
    t.writes = nil
    t.deletes = nil

    return nil
}

// Rollback discards all buffered changes
func (t *Transaction) Rollback() {
    t.writes = nil
    t.deletes = nil
}

// gcLoop periodically removes old versions
func (s *MVCCStore) gcLoop() {
    for range s.gcTicker.C {
        minActive := s.minActive.Load()

        s.chains.Range(func(key, value interface{}) bool {
            chain := value.(*VersionChain)
            s.gcChain(chain, minActive)
            return true
        })
    }
}

func (s *MVCCStore) gcChain(chain *VersionChain, minActiveTxn uint64) {
    // Find first version older than all active transactions
    // Keep it (as the "base" version) but remove everything after it
    head := (*Version)(atomic.LoadPointer(&chain.head))

    var prev *Version
    v := head

    for v != nil && v.next != nil {
        if v.txnID < minActiveTxn && v.next.txnID < minActiveTxn {
            // v.next and everything after can be GC'd
            v.next = nil
            break
        }
        prev = v
        v = v.next
    }
}
```

**Concurrency Comparison:**

| Approach | Reads | Writes | Read-Write | Write-Write |
|----------|-------|--------|------------|-------------|
| RWMutex | Shared | Exclusive | Blocked | Blocked |
| MVCC | Lock-free | Lock-free | Never blocked | CAS retry |

**ACCEPTANCE CRITERIA - MVCC Version Chains:**
- [ ] Readers never blocked by writers
- [ ] Writers never blocked by readers
- [ ] Snapshot isolation: reads see consistent point-in-time view
- [ ] Garbage collection removes old versions
- [ ] No memory leaks under sustained read/write load
- [ ] Correct behavior under concurrent modifications

### TC Checkpointing (WAL-Based)

Long-running transitive closure computation can crash, losing progress. **WAL-based checkpointing** enables resume from last checkpoint.

```go
// core/knowledge/relations/checkpointer.go

package relations

import (
    "bufio"
    "context"
    "encoding/binary"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "sync"
    "time"
)

// CheckpointEntry represents a single WAL entry
type CheckpointEntry struct {
    Type      EntryType       `json:"type"`
    Timestamp time.Time       `json:"ts"`
    Data      json.RawMessage `json:"data"`
}

type EntryType int

const (
    EntryTypeNewFact EntryType = iota
    EntryTypeDeltaComplete
    EntryTypeStratumComplete
    EntryTypeComputationComplete
    EntryTypeWorklist
)

// TCCheckpointer provides durable checkpointing for TC computation
type TCCheckpointer struct {
    walPath     string
    walFile     *os.File
    walWriter   *bufio.Writer
    mu          sync.Mutex

    // Recovery state
    lastStratum int
    lastDelta   int
    facts       map[EdgeKey]struct{}
    worklist    []EdgeKey
}

func NewTCCheckpointer(dir string) (*TCCheckpointer, error) {
    walPath := filepath.Join(dir, "tc_computation.wal")

    cp := &TCCheckpointer{
        walPath: walPath,
        facts:   make(map[EdgeKey]struct{}),
    }

    return cp, nil
}

// StartComputation begins or resumes TC computation
func (cp *TCCheckpointer) StartComputation(ctx context.Context) (*ComputationState, error) {
    cp.mu.Lock()
    defer cp.mu.Unlock()

    // Check for existing WAL (recovery case)
    if _, err := os.Stat(cp.walPath); err == nil {
        state, err := cp.recover()
        if err != nil {
            return nil, fmt.Errorf("recovery failed: %w", err)
        }

        // Append to existing WAL
        f, err := os.OpenFile(cp.walPath, os.O_APPEND|os.O_WRONLY, 0644)
        if err != nil {
            return nil, err
        }
        cp.walFile = f
        cp.walWriter = bufio.NewWriter(f)

        return state, nil
    }

    // Fresh start
    f, err := os.Create(cp.walPath)
    if err != nil {
        return nil, err
    }
    cp.walFile = f
    cp.walWriter = bufio.NewWriter(f)

    return &ComputationState{
        Stratum:  0,
        Delta:    0,
        Facts:    make(map[EdgeKey]struct{}),
        Worklist: nil,
    }, nil
}

// ComputationState represents resumable computation state
type ComputationState struct {
    Stratum  int
    Delta    int
    Facts    map[EdgeKey]struct{}
    Worklist []EdgeKey
}

// recover reads WAL and reconstructs state
func (cp *TCCheckpointer) recover() (*ComputationState, error) {
    f, err := os.Open(cp.walPath)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    state := &ComputationState{
        Facts: make(map[EdgeKey]struct{}),
    }

    reader := bufio.NewReader(f)

    for {
        // Read entry length
        var length uint32
        if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
            if err == io.EOF {
                break
            }
            return nil, err
        }

        // Read entry data
        data := make([]byte, length)
        if _, err := io.ReadFull(reader, data); err != nil {
            return nil, err
        }

        var entry CheckpointEntry
        if err := json.Unmarshal(data, &entry); err != nil {
            return nil, err
        }

        // Replay entry
        switch entry.Type {
        case EntryTypeNewFact:
            var edge EdgeKey
            json.Unmarshal(entry.Data, &edge)
            state.Facts[edge] = struct{}{}

        case EntryTypeDeltaComplete:
            var delta int
            json.Unmarshal(entry.Data, &delta)
            state.Delta = delta + 1

        case EntryTypeStratumComplete:
            var stratum int
            json.Unmarshal(entry.Data, &stratum)
            state.Stratum = stratum + 1
            state.Delta = 0

        case EntryTypeWorklist:
            json.Unmarshal(entry.Data, &state.Worklist)

        case EntryTypeComputationComplete:
            // Computation finished, no recovery needed
            return nil, nil
        }
    }

    return state, nil
}

// LogNewFact records a newly derived fact
func (cp *TCCheckpointer) LogNewFact(edge EdgeKey) error {
    return cp.writeEntry(EntryTypeNewFact, edge)
}

// LogDeltaComplete records completion of a delta iteration
func (cp *TCCheckpointer) LogDeltaComplete(delta int) error {
    return cp.writeEntry(EntryTypeDeltaComplete, delta)
}

// LogStratumComplete records completion of a stratum
func (cp *TCCheckpointer) LogStratumComplete(stratum int) error {
    return cp.writeEntry(EntryTypeStratumComplete, stratum)
}

// LogWorklist saves current worklist for recovery
func (cp *TCCheckpointer) LogWorklist(worklist []EdgeKey) error {
    return cp.writeEntry(EntryTypeWorklist, worklist)
}

// LogComputationComplete marks computation as done
func (cp *TCCheckpointer) LogComputationComplete() error {
    if err := cp.writeEntry(EntryTypeComputationComplete, nil); err != nil {
        return err
    }

    // Remove WAL file (computation complete)
    cp.mu.Lock()
    defer cp.mu.Unlock()

    cp.walWriter.Flush()
    cp.walFile.Close()

    return os.Remove(cp.walPath)
}

func (cp *TCCheckpointer) writeEntry(typ EntryType, data interface{}) error {
    cp.mu.Lock()
    defer cp.mu.Unlock()

    dataBytes, err := json.Marshal(data)
    if err != nil {
        return err
    }

    entry := CheckpointEntry{
        Type:      typ,
        Timestamp: time.Now(),
        Data:      dataBytes,
    }

    entryBytes, err := json.Marshal(entry)
    if err != nil {
        return err
    }

    // Write length-prefixed entry
    if err := binary.Write(cp.walWriter, binary.LittleEndian, uint32(len(entryBytes))); err != nil {
        return err
    }
    if _, err := cp.walWriter.Write(entryBytes); err != nil {
        return err
    }

    // Flush periodically (not every write for performance)
    return nil
}

// Flush forces WAL to disk
func (cp *TCCheckpointer) Flush() error {
    cp.mu.Lock()
    defer cp.mu.Unlock()

    if err := cp.walWriter.Flush(); err != nil {
        return err
    }
    return cp.walFile.Sync()
}

// Close cleanly shuts down checkpointer
func (cp *TCCheckpointer) Close() error {
    cp.mu.Lock()
    defer cp.mu.Unlock()

    if cp.walFile != nil {
        cp.walWriter.Flush()
        return cp.walFile.Close()
    }
    return nil
}
```

**Integration with Semi-Naive:**

```go
// Modified StratifiedSemiNaive with checkpointing

func (s *StratifiedSemiNaive) EvaluateWithCheckpointing(
    ctx context.Context,
    cp *TCCheckpointer,
) error {
    // Start or resume computation
    state, err := cp.StartComputation(ctx)
    if err != nil {
        return err
    }

    // If state is nil, computation was already complete
    if state == nil {
        return nil
    }

    // Restore facts from checkpoint
    for edge := range state.Facts {
        s.allFacts[edge] = struct{}{}
    }

    s.computeStrata()

    // Resume from checkpoint stratum/delta
    for stratumIdx := state.Stratum; stratumIdx < len(s.strata); stratumIdx++ {
        stratum := s.strata[stratumIdx]

        startDelta := 0
        if stratumIdx == state.Stratum {
            startDelta = state.Delta
        }

        if err := s.evaluateStratumWithCheckpointing(
            ctx, cp, stratumIdx, stratum, startDelta,
        ); err != nil {
            return err
        }

        cp.LogStratumComplete(stratumIdx)
        cp.Flush()
    }

    cp.LogComputationComplete()
    return nil
}

func (s *StratifiedSemiNaive) evaluateStratumWithCheckpointing(
    ctx context.Context,
    cp *TCCheckpointer,
    stratumIdx int,
    ruleIndices []int,
    startDelta int,
) error {
    delta := NewDeltaTable(startDelta)

    // Seed delta (or restore from checkpoint)
    // ...

    iteration := startDelta
    maxIterations := 1000

    for !delta.IsEmpty() && iteration < maxIterations {
        iteration++
        nextDelta := NewDeltaTable(iteration)

        for _, ruleIdx := range ruleIndices {
            rule := s.rules[ruleIdx]
            newEdges := s.applyRuleSemiNaive(ctx, rule, delta)

            for _, edge := range newEdges {
                if _, exists := s.allFacts[edge]; !exists {
                    s.allFacts[edge] = struct{}{}
                    nextDelta.Add(edge.Source, edge.Target, edge.EdgeType)

                    // Log to WAL
                    cp.LogNewFact(edge)

                    s.db.InsertInferredEdge(ctx, edge.Source, edge.Target, edge.EdgeType)
                }
            }
        }

        // Checkpoint every delta iteration
        cp.LogDeltaComplete(iteration)

        // Periodic flush (every 10 iterations)
        if iteration%10 == 0 {
            cp.Flush()
        }

        delta = nextDelta
    }

    return nil
}
```

**ACCEPTANCE CRITERIA - TC Checkpointing:**
- [ ] Crash during computation resumes from last checkpoint
- [ ] WAL entries are length-prefixed and parseable
- [ ] No duplicate facts after recovery
- [ ] Stratum progress preserved across restarts
- [ ] WAL file removed after successful completion
- [ ] Recovery completes in <10s for 1M facts logged

---

## Temporal Graph Support

### Bi-Temporal Model

Every edge has two time dimensions:
- **Valid time**: When the relationship was true in the real world
- **Transaction time**: When we learned about it (for auditability)

```go
// core/vectorgraphdb/temporal/temporal.go

package temporal

import (
    "context"
    "time"

    vgdb "sylk/core/vectorgraphdb"
)

// TemporalQuery represents a time-aware graph query
type TemporalQuery struct {
    // Point-in-time query: "What was the graph state at time T?"
    AsOf *time.Time

    // Range query: "What was true during [start, end]?"
    ValidFrom *time.Time
    ValidTo   *time.Time

    // Transaction time filter: "What did we know at time T?"
    KnownAsOf *time.Time
}

// TemporalGraphDB wraps VectorGraphDB with temporal query support
type TemporalGraphDB struct {
    db *vgdb.VectorGraphDB
}

func NewTemporalGraphDB(db *vgdb.VectorGraphDB) *TemporalGraphDB {
    return &TemporalGraphDB{db: db}
}

// GetNodeAsOf returns the node state at a specific point in time
func (t *TemporalGraphDB) GetNodeAsOf(ctx context.Context, nodeID string, asOf time.Time) (*vgdb.GraphNode, error) {
    row := t.db.QueryRow(`
        SELECT id, domain, node_type, name, content, path, package,
               signature, metadata, verified, confidence, created_at,
               updated_at, valid_from, valid_to, version
        FROM nodes
        WHERE id = ?
          AND (valid_from IS NULL OR valid_from <= ?)
          AND (valid_to IS NULL OR valid_to > ?)
          AND transaction_time <= ?
        ORDER BY transaction_time DESC
        LIMIT 1
    `, nodeID, asOf.Format(time.RFC3339), asOf.Format(time.RFC3339), asOf.Format(time.RFC3339))

    node := &vgdb.GraphNode{}
    // Scan row into node...

    return node, nil
}

// GetEdgesAsOf returns edges that were valid at a specific point in time
func (t *TemporalGraphDB) GetEdgesAsOf(ctx context.Context, nodeID string,
                                         direction vgdb.TraversalDirection, asOf time.Time) ([]*vgdb.GraphEdge, error) {
    var query string
    asOfStr := asOf.Format(time.RFC3339)

    switch direction {
    case vgdb.DirectionOutgoing:
        query = `
            SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
            FROM edges
            WHERE source_id = ?
              AND (valid_from IS NULL OR valid_from <= ?)
              AND (valid_to IS NULL OR valid_to > ?)
              AND transaction_time <= ?
        `
    case vgdb.DirectionIncoming:
        query = `
            SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
            FROM edges
            WHERE target_id = ?
              AND (valid_from IS NULL OR valid_from <= ?)
              AND (valid_to IS NULL OR valid_to > ?)
              AND transaction_time <= ?
        `
    case vgdb.DirectionBoth:
        query = `
            SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
            FROM edges
            WHERE (source_id = ? OR target_id = ?)
              AND (valid_from IS NULL OR valid_from <= ?)
              AND (valid_to IS NULL OR valid_to > ?)
              AND transaction_time <= ?
        `
    }

    rows, err := t.db.Query(query, nodeID, asOfStr, asOfStr, asOfStr)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var edges []*vgdb.GraphEdge
    // Scan rows into edges...

    return edges, nil
}

// GetEdgeHistory returns all versions of an edge over time
func (t *TemporalGraphDB) GetEdgeHistory(ctx context.Context, sourceID, targetID string,
                                           edgeType vgdb.EdgeType) ([]*EdgeVersion, error) {
    rows, err := t.db.Query(`
        SELECT id, source_id, target_id, edge_type, weight, metadata,
               valid_from, valid_to, transaction_time, created_at
        FROM edges
        WHERE source_id = ? AND target_id = ? AND edge_type = ?
        ORDER BY transaction_time ASC
    `, sourceID, targetID, edgeType)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var versions []*EdgeVersion
    // Scan rows...

    return versions, nil
}

// EdgeVersion represents one version of an edge in time
type EdgeVersion struct {
    Edge            *vgdb.GraphEdge
    ValidFrom       *time.Time
    ValidTo         *time.Time
    TransactionTime time.Time
}

// TemporalTraverse performs graph traversal respecting temporal constraints
func (t *TemporalGraphDB) TemporalTraverse(ctx context.Context, startID string,
                                            opts *TemporalTraversalOptions) ([]*TemporalTraversalResult, error) {
    // BFS with temporal filtering
    visited := make(map[string]bool)
    queue := []traversalItem{{nodeID: startID, depth: 0, path: []string{startID}}}
    var results []*TemporalTraversalResult

    for len(queue) > 0 && len(results) < opts.MaxResults {
        item := queue[0]
        queue = queue[1:]

        if visited[item.nodeID] {
            continue
        }
        visited[item.nodeID] = true

        if item.depth > 0 { // Don't include start node
            results = append(results, &TemporalTraversalResult{
                NodeID: item.nodeID,
                Depth:  item.depth,
                Path:   item.path,
            })
        }

        if item.depth >= opts.MaxDepth {
            continue
        }

        // Get temporally-valid edges
        edges, err := t.GetEdgesAsOf(ctx, item.nodeID, opts.Direction, opts.AsOf)
        if err != nil {
            continue
        }

        for _, edge := range edges {
            if len(opts.EdgeTypes) > 0 && !containsEdgeType(opts.EdgeTypes, edge.EdgeType) {
                continue
            }

            neighborID := edge.TargetID
            if opts.Direction == vgdb.DirectionIncoming {
                neighborID = edge.SourceID
            }

            if !visited[neighborID] {
                newPath := append([]string{}, item.path...)
                newPath = append(newPath, neighborID)
                queue = append(queue, traversalItem{
                    nodeID: neighborID,
                    depth:  item.depth + 1,
                    path:   newPath,
                })
            }
        }
    }

    return results, nil
}

type TemporalTraversalOptions struct {
    Direction  vgdb.TraversalDirection
    EdgeTypes  []vgdb.EdgeType
    MaxDepth   int
    MaxResults int
    AsOf       time.Time
}

type TemporalTraversalResult struct {
    NodeID string
    Depth  int
    Path   []string
}

type traversalItem struct {
    nodeID string
    depth  int
    path   []string
}
```

**ACCEPTANCE CRITERIA - Temporal Graph Support:**
- [ ] AsOf queries return correct historical state
- [ ] Valid-time correctly filters edges by when relationship was true
- [ ] Transaction-time correctly filters by when we learned about it
- [ ] Edge history returns all versions ordered by transaction time
- [ ] Temporal traversal respects time constraints at each hop
- [ ] Current state queries (valid_to IS NULL) are fast via index
- [ ] Temporal indexes used by query planner

---

## Memory Decay Model (ACT-R)

### Why Not Exponential Decay

Exponential decay (`S × e^(-λt)`) is commonly used but **does not match human memory**:

| Time | Exponential (λ=0.1) | Power Law (d=0.5) | Human Data |
|------|---------------------|-------------------|------------|
| 1 day | 90% | 100% | ~95% |
| 7 days | 50% | 38% | ~40% |
| 30 days | 5% | 18% | ~20% |
| 365 days | ~0% | 5% | ~8% |
| 10 years | 0% | 1.7% | ~2% |

**Key finding**: Human memory follows a **power law** decay curve, not exponential. Old memories never truly vanish—you can still recall childhood events decades later. Exponential decay approaches zero too quickly.

### ACT-R Cognitive Architecture

The ACT-R (Adaptive Control of Thought—Rational) model by Anderson (1990s-present) is the most validated cognitive model of human memory. It has been tested in hundreds of studies.

**Base-Level Learning Equation:**

```
Bᵢ = ln(Σⱼ₌₁ⁿ tⱼ⁻ᵈ) + βᵢ

Where:
- Bᵢ = base-level activation of memory i
- tⱼ = time since the jth access (in hours)
- d  = decay parameter (typically ~0.5)
- n  = number of times accessed
- βᵢ = base offset (learnable)
```

**Why this model is superior:**

1. **Power law emerges naturally** - summing exponentially-decaying traces produces power-law behavior
2. **Handles both recency AND frequency** - more accesses = higher activation
3. **Spacing effect built-in** - spaced retrievals strengthen memory more than massed practice
4. **Validated extensively** - used in cognitive modeling for 30+ years

### Schema Extension

```sql
-- Add memory tracking columns to nodes
ALTER TABLE nodes ADD COLUMN memory_activation REAL DEFAULT 0.0;
ALTER TABLE nodes ADD COLUMN last_accessed_at INTEGER;  -- Unix timestamp (hours)
ALTER TABLE nodes ADD COLUMN access_count INTEGER DEFAULT 0;
ALTER TABLE nodes ADD COLUMN base_offset REAL DEFAULT 0.0;

-- Access trace table for ACT-R calculation
CREATE TABLE IF NOT EXISTS node_access_traces (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    accessed_at INTEGER NOT NULL,  -- Unix timestamp (hours)
    access_type TEXT,              -- 'retrieval', 'reinforcement', 'creation'
    context TEXT,                  -- What prompted the access
    CONSTRAINT fk_node FOREIGN KEY (node_id) REFERENCES nodes(id)
);

-- Index for efficient trace summation
CREATE INDEX idx_access_traces_node ON node_access_traces(node_id, accessed_at DESC);

-- Same for edges
ALTER TABLE edges ADD COLUMN memory_activation REAL DEFAULT 0.0;
ALTER TABLE edges ADD COLUMN last_accessed_at INTEGER;
ALTER TABLE edges ADD COLUMN access_count INTEGER DEFAULT 0;

CREATE TABLE IF NOT EXISTS edge_access_traces (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    edge_id INTEGER NOT NULL REFERENCES edges(id) ON DELETE CASCADE,
    accessed_at INTEGER NOT NULL,
    access_type TEXT,
    context TEXT
);

CREATE INDEX idx_edge_traces ON edge_access_traces(edge_id, accessed_at DESC);

-- Domain-specific decay parameters (learned)
CREATE TABLE IF NOT EXISTS decay_parameters (
    domain INTEGER PRIMARY KEY,
    decay_exponent_alpha REAL DEFAULT 5.0,   -- Beta distribution alpha
    decay_exponent_beta REAL DEFAULT 5.0,    -- Beta distribution beta (prior d=0.5)
    base_offset_mean REAL DEFAULT 0.0,
    base_offset_variance REAL DEFAULT 1.0,
    effective_samples REAL DEFAULT 0.0,
    updated_at TEXT NOT NULL
);
```

### Core Types

```go
// core/knowledge/memory/actr_types.go

package memory

import (
    "math"
    "time"
)

// DecayModel specifies which decay curve to use
type DecayModel int

const (
    DecayACTR      DecayModel = iota  // ACT-R base-level learning (RECOMMENDED)
    DecayPowerLaw                      // Simple power law: t^(-d)
    DecayExponential                   // Legacy: e^(-λt) (NOT RECOMMENDED)
)

// AccessTrace represents a single memory access event
type AccessTrace struct {
    AccessedAt time.Time
    AccessType AccessType
    Context    string
}

type AccessType string

const (
    AccessRetrieval     AccessType = "retrieval"      // Retrieved during query
    AccessReinforcement AccessType = "reinforcement"  // Explicitly reinforced
    AccessCreation      AccessType = "creation"       // Initial creation
    AccessReference     AccessType = "reference"      // Referenced in context
)

// ACTRMemory implements the ACT-R base-level learning equation
type ACTRMemory struct {
    // Identity
    NodeID string
    Domain Domain

    // Access history (bounded circular buffer)
    Traces    []AccessTrace
    MaxTraces int  // Default: 100 traces per node

    // Learned decay parameter (Beta distribution)
    // Prior: Beta(5,5) centered at d=0.5
    DecayAlpha float64
    DecayBeta  float64

    // Learned base offset (Normal distribution)
    BaseOffsetMean     float64
    BaseOffsetVariance float64

    // Metadata
    CreatedAt   time.Time
    AccessCount int
}

// Activation computes base-level activation using ACT-R equation
// B = ln(Σ tⱼ^(-d)) + β
func (m *ACTRMemory) Activation(now time.Time) float64 {
    if len(m.Traces) == 0 {
        return m.BaseOffsetMean  // Cold start
    }

    // Get decay exponent from posterior mean
    d := m.DecayMean()

    // Sum of decaying traces (power law)
    var traceSum float64
    for _, trace := range m.Traces {
        // Time since access in hours (minimum 1 to avoid infinity)
        hours := now.Sub(trace.AccessedAt).Hours()
        t := math.Max(1.0, hours)

        // Power law decay: t^(-d)
        traceSum += math.Pow(t, -d)
    }

    // Avoid log(0) - if traceSum is very small, return minimum activation
    if traceSum < 1e-10 {
        return m.BaseOffsetMean - 10.0  // Very low but not -∞
    }

    // ACT-R base-level equation
    activation := math.Log(traceSum) + m.BaseOffsetMean

    return activation
}

// DecayMean returns the posterior mean of the decay parameter
// E[d] = α / (α + β) for Beta distribution
func (m *ACTRMemory) DecayMean() float64 {
    return m.DecayAlpha / (m.DecayAlpha + m.DecayBeta)
}

// DecayVariance returns the posterior variance of the decay parameter
func (m *ACTRMemory) DecayVariance() float64 {
    sum := m.DecayAlpha + m.DecayBeta
    return (m.DecayAlpha * m.DecayBeta) / (sum * sum * (sum + 1))
}

// DecaySample returns a Thompson sample of the decay parameter
func (m *ACTRMemory) DecaySample() float64 {
    return betaSample(m.DecayAlpha, m.DecayBeta)
}

// RetrievalProbability computes probability of successful retrieval
// Using softmax/logistic transformation of activation
func (m *ACTRMemory) RetrievalProbability(now time.Time, threshold float64) float64 {
    activation := m.Activation(now)

    // Temperature parameter (lower = sharper threshold)
    τ := 0.5

    // Logistic transformation
    return 1.0 / (1.0 + math.Exp(-(activation-threshold)/τ))
}

// Reinforce adds a new access trace
// This is where the spacing effect emerges naturally
func (m *ACTRMemory) Reinforce(now time.Time, accessType AccessType, context string) {
    trace := AccessTrace{
        AccessedAt: now,
        AccessType: accessType,
        Context:    context,
    }

    m.Traces = append(m.Traces, trace)
    m.AccessCount++

    // Bounded history - keep most recent traces
    if len(m.Traces) > m.MaxTraces {
        m.Traces = m.Traces[len(m.Traces)-m.MaxTraces:]
    }
}

// UpdateDecay updates the decay parameter based on observed utility
// If retrieved content was useful, decay may have been too aggressive
func (m *ACTRMemory) UpdateDecay(ageAtRetrieval time.Duration, wasUseful bool) {
    // Asymmetric learning: only update when retrieval was useful
    // This prevents "punishing" decay for naturally forgotten irrelevant content
    if !wasUseful {
        return
    }

    // Observed "effective" decay based on age
    // If useful content was old, our decay was too aggressive
    ageHours := ageAtRetrieval.Hours()

    if ageHours > 168 {  // > 1 week
        // Old content was useful → decrease decay (increase retention)
        // Equivalent to observing a "success" for lower d
        m.DecayAlpha += 0.1  // Small update toward lower d
    } else if ageHours < 24 {  // < 1 day
        // Recent content was useful → current decay is appropriate
        // No update needed - recency expected to be useful
    }

    // Apply decay to effective samples (prevents over-confidence)
    decayFactor := 0.99
    m.DecayAlpha *= decayFactor
    m.DecayBeta *= decayFactor

    // Minimum effective samples (cold start protection)
    minAlpha, minBeta := 1.0, 1.0
    m.DecayAlpha = math.Max(m.DecayAlpha, minAlpha)
    m.DecayBeta = math.Max(m.DecayBeta, minBeta)
}
```

### Memory Store

```go
// core/knowledge/memory/store.go

package memory

import (
    "context"
    "database/sql"
    "time"
)

// MemoryStore manages ACT-R memory states for all nodes/edges
type MemoryStore struct {
    db            *sql.DB
    domainDecay   map[Domain]*DomainDecayParams  // Per-domain learned decay
    globalDecay   *DomainDecayParams             // Fallback global decay
    maxTraces     int
}

// DomainDecayParams holds learned decay for a domain
type DomainDecayParams struct {
    DecayAlpha       float64
    DecayBeta        float64
    BaseOffsetMean   float64
    BaseOffsetVar    float64
    EffectiveSamples float64
}

// DefaultDomainDecay returns weakly informative priors per domain
// Based on cognitive science: d ≈ 0.5 for general memory
func DefaultDomainDecay(domain Domain) *DomainDecayParams {
    // All start with Beta(5,5) prior → E[d] = 0.5
    base := &DomainDecayParams{
        DecayAlpha:       5.0,
        DecayBeta:        5.0,
        BaseOffsetMean:   0.0,
        BaseOffsetVar:    1.0,
        EffectiveSamples: 0.0,
    }

    // Domain-specific adjustments to prior
    switch domain {
    case DomainLibrarian:
        // Code patterns: standard decay
        // No adjustment
    case DomainAcademic:
        // Research: slower decay (longer retention expected)
        base.DecayAlpha = 3.0
        base.DecayBeta = 7.0  // E[d] ≈ 0.3
    case DomainArchivalist:
        // History: moderate decay
        base.DecayAlpha = 4.0
        base.DecayBeta = 6.0  // E[d] ≈ 0.4
    case DomainEngineer:
        // Implementation context: faster decay (very context-dependent)
        base.DecayAlpha = 6.0
        base.DecayBeta = 4.0  // E[d] ≈ 0.6
    }

    return base
}

// GetMemory retrieves or creates memory state for a node
func (s *MemoryStore) GetMemory(ctx context.Context, nodeID string, domain Domain) (*ACTRMemory, error) {
    // Try to load existing memory
    memory, err := s.loadMemory(ctx, nodeID)
    if err == nil {
        return memory, nil
    }

    // Create new memory with domain-specific priors
    domainParams := s.domainDecay[domain]
    if domainParams == nil {
        domainParams = DefaultDomainDecay(domain)
    }

    memory = &ACTRMemory{
        NodeID:             nodeID,
        Domain:             domain,
        Traces:             make([]AccessTrace, 0, s.maxTraces),
        MaxTraces:          s.maxTraces,
        DecayAlpha:         domainParams.DecayAlpha,
        DecayBeta:          domainParams.DecayBeta,
        BaseOffsetMean:     domainParams.BaseOffsetMean,
        BaseOffsetVariance: domainParams.BaseOffsetVar,
        CreatedAt:          time.Now(),
        AccessCount:        0,
    }

    // Record creation trace
    memory.Reinforce(time.Now(), AccessCreation, "initial")

    return memory, nil
}

// RecordAccess records an access and updates activation
func (s *MemoryStore) RecordAccess(ctx context.Context, nodeID string, accessType AccessType, accessContext string) error {
    // Load memory
    memory, err := s.GetMemory(ctx, nodeID, DomainLibrarian)  // Domain looked up internally
    if err != nil {
        return err
    }

    // Add trace
    memory.Reinforce(time.Now(), accessType, accessContext)

    // Persist
    return s.saveMemory(ctx, memory)
}

// ComputeActivation returns current activation for a node
func (s *MemoryStore) ComputeActivation(ctx context.Context, nodeID string) (float64, error) {
    memory, err := s.GetMemory(ctx, nodeID, DomainLibrarian)
    if err != nil {
        return 0, err
    }

    return memory.Activation(time.Now()), nil
}

// loadMemory loads memory state from database
func (s *MemoryStore) loadMemory(ctx context.Context, nodeID string) (*ACTRMemory, error) {
    // Load base memory
    var memory ACTRMemory
    err := s.db.QueryRowContext(ctx, `
        SELECT n.id, n.domain, n.memory_activation, n.last_accessed_at,
               n.access_count, n.base_offset,
               COALESCE(dp.decay_exponent_alpha, 5.0),
               COALESCE(dp.decay_exponent_beta, 5.0)
        FROM nodes n
        LEFT JOIN decay_parameters dp ON n.domain = dp.domain
        WHERE n.id = ?
    `, nodeID).Scan(
        &memory.NodeID, &memory.Domain, &memory.BaseOffsetMean,
        &memory.CreatedAt, &memory.AccessCount, &memory.BaseOffsetMean,
        &memory.DecayAlpha, &memory.DecayBeta,
    )
    if err != nil {
        return nil, err
    }

    // Load traces (most recent MaxTraces)
    rows, err := s.db.QueryContext(ctx, `
        SELECT accessed_at, access_type, context
        FROM node_access_traces
        WHERE node_id = ?
        ORDER BY accessed_at DESC
        LIMIT ?
    `, nodeID, s.maxTraces)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    memory.Traces = make([]AccessTrace, 0, s.maxTraces)
    for rows.Next() {
        var trace AccessTrace
        var accessedAtUnix int64
        if err := rows.Scan(&accessedAtUnix, &trace.AccessType, &trace.Context); err != nil {
            continue
        }
        trace.AccessedAt = time.Unix(accessedAtUnix*3600, 0)  // Hours to seconds
        memory.Traces = append(memory.Traces, trace)
    }

    memory.MaxTraces = s.maxTraces
    return &memory, nil
}

// saveMemory persists memory state to database
func (s *MemoryStore) saveMemory(ctx context.Context, memory *ACTRMemory) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // Update node memory fields
    now := time.Now()
    activation := memory.Activation(now)
    _, err = tx.ExecContext(ctx, `
        UPDATE nodes
        SET memory_activation = ?,
            last_accessed_at = ?,
            access_count = ?,
            base_offset = ?
        WHERE id = ?
    `, activation, now.Unix()/3600, memory.AccessCount, memory.BaseOffsetMean, memory.NodeID)
    if err != nil {
        return err
    }

    // Insert latest trace (if any new)
    if len(memory.Traces) > 0 {
        latestTrace := memory.Traces[len(memory.Traces)-1]
        _, err = tx.ExecContext(ctx, `
            INSERT INTO node_access_traces (node_id, accessed_at, access_type, context)
            VALUES (?, ?, ?, ?)
        `, memory.NodeID, latestTrace.AccessedAt.Unix()/3600, latestTrace.AccessType, latestTrace.Context)
        if err != nil {
            return err
        }
    }

    return tx.Commit()
}
```

### Memory-Weighted Scoring

```go
// core/knowledge/memory/scorer.go

package memory

import (
    "context"
    "math"
    "sort"
    "time"

    vgdb "sylk/core/vectorgraphdb"
)

// MemoryWeightedScorer applies ACT-R memory decay to query results
type MemoryWeightedScorer struct {
    store *MemoryStore

    // Learned weights for combining scores (reuse from 4Q pattern)
    ActivationWeight *LearnedWeight  // How much activation affects final score
    RecencyBonus     *LearnedWeight  // Bonus for very recent accesses
    FrequencyBonus   *LearnedWeight  // Bonus for frequently accessed

    // Threshold for retrieval (learned)
    RetrievalThreshold *LearnedWeight  // Activation threshold for inclusion

    // Configuration
    MinActivation float64  // Floor for activation (nothing truly forgotten)
}

// NewMemoryWeightedScorer creates a scorer with default priors
func NewMemoryWeightedScorer(store *MemoryStore) *MemoryWeightedScorer {
    return &MemoryWeightedScorer{
        store: store,
        // Priors: all weights start at 0.5 with moderate confidence
        ActivationWeight:   NewLearnedWeight(5.0, 5.0),   // E[w] = 0.5
        RecencyBonus:       NewLearnedWeight(3.0, 7.0),   // E[w] = 0.3 (recency less important in KG)
        FrequencyBonus:     NewLearnedWeight(4.0, 6.0),   // E[w] = 0.4
        RetrievalThreshold: NewLearnedWeight(2.0, 8.0),   // E[t] = 0.2 (low threshold)
        MinActivation:      -10.0,  // Practical floor
    }
}

// ApplyMemoryWeighting adjusts result scores by memory activation
func (s *MemoryWeightedScorer) ApplyMemoryWeighting(
    ctx context.Context,
    results []vgdb.HybridResult,
    explore bool,
) ([]vgdb.HybridResult, error) {
    now := time.Now()

    // Sample weights (Thompson Sampling if exploring)
    var activationW, recencyW, frequencyW float64
    if explore {
        activationW = s.ActivationWeight.Sample()
        recencyW = s.RecencyBonus.Sample()
        frequencyW = s.FrequencyBonus.Sample()
    } else {
        activationW = s.ActivationWeight.Mean()
        recencyW = s.RecencyBonus.Mean()
        frequencyW = s.FrequencyBonus.Mean()
    }

    for i := range results {
        memory, err := s.store.GetMemory(ctx, results[i].ID, DomainLibrarian)
        if err != nil {
            // No memory state - use base score
            continue
        }

        // Compute ACT-R activation
        activation := memory.Activation(now)
        activation = math.Max(activation, s.MinActivation)  // Apply floor

        // Normalize activation to [0, 1] range for combination
        // Typical activation range is roughly [-5, 5]
        normalizedActivation := (activation + 5.0) / 10.0
        normalizedActivation = math.Max(0, math.Min(1, normalizedActivation))

        // Compute recency bonus (boost for very recent access)
        var recencyFactor float64 = 0.0
        if len(memory.Traces) > 0 {
            lastAccess := memory.Traces[len(memory.Traces)-1].AccessedAt
            hoursSince := now.Sub(lastAccess).Hours()
            if hoursSince < 24 {
                recencyFactor = 1.0 - (hoursSince / 24.0)  // Linear decay over 24h
            }
        }

        // Compute frequency bonus (log-scaled access count)
        frequencyFactor := math.Log(1+float64(memory.AccessCount)) / math.Log(101)  // Normalized to [0,1]

        // Combine factors with learned weights
        memoryFactor := activationW*normalizedActivation +
                        recencyW*recencyFactor +
                        frequencyW*frequencyFactor

        // Apply to original score (multiplicative)
        // memoryFactor is in [0, 1], so we scale to [0.5, 1.5] range
        scoreMultiplier := 0.5 + memoryFactor
        results[i].Score *= scoreMultiplier

        // Store memory metrics for transparency
        results[i].MemoryActivation = activation
        results[i].MemoryFactor = memoryFactor
    }

    // Re-sort by adjusted scores
    sort.Slice(results, func(i, j int) bool {
        return results[i].Score > results[j].Score
    })

    return results, nil
}

// RecordRetrievalOutcome updates learning based on whether retrieved content was useful
func (s *MemoryWeightedScorer) RecordRetrievalOutcome(
    ctx context.Context,
    nodeID string,
    wasUseful bool,
    ageAtRetrieval time.Duration,
) error {
    memory, err := s.store.GetMemory(ctx, nodeID, DomainLibrarian)
    if err != nil {
        return err
    }

    // Update decay parameter based on outcome
    memory.UpdateDecay(ageAtRetrieval, wasUseful)

    // If useful, reinforce the memory
    if wasUseful {
        memory.Reinforce(time.Now(), AccessReinforcement, "useful_retrieval")
    }

    return s.store.saveMemory(ctx, memory)
}

// FilterByRetrievalProbability removes results below retrieval threshold
func (s *MemoryWeightedScorer) FilterByRetrievalProbability(
    ctx context.Context,
    results []vgdb.HybridResult,
    explore bool,
) ([]vgdb.HybridResult, error) {
    now := time.Now()

    var threshold float64
    if explore {
        threshold = s.RetrievalThreshold.Sample()
    } else {
        threshold = s.RetrievalThreshold.Mean()
    }

    filtered := make([]vgdb.HybridResult, 0, len(results))
    for _, result := range results {
        memory, err := s.store.GetMemory(ctx, result.ID, DomainLibrarian)
        if err != nil {
            // No memory - include by default
            filtered = append(filtered, result)
            continue
        }

        prob := memory.RetrievalProbability(now, threshold)
        if prob > 0.5 || explore {  // Include if >50% chance, or if exploring
            filtered = append(filtered, result)
        }
    }

    return filtered, nil
}
```

### Integration with Hybrid Query

```go
// core/knowledge/query/memory_integration.go

package query

import (
    "context"

    "sylk/core/knowledge/memory"
    vgdb "sylk/core/vectorgraphdb"
)

// HybridQueryWithMemory extends HybridQuery with memory-aware retrieval
type HybridQueryWithMemory struct {
    *HybridQuery
    memoryScorer *memory.MemoryWeightedScorer

    // Options
    ApplyMemoryWeighting bool  // Whether to apply ACT-R decay
    FilterByRetrieval    bool  // Whether to filter by retrieval probability
    Explore              bool  // Thompson Sampling for exploration
}

// Execute runs the hybrid query with memory-weighted scoring
func (q *HybridQueryWithMemory) Execute(ctx context.Context) ([]vgdb.HybridResult, error) {
    // Run base hybrid query
    results, err := q.HybridQuery.Execute(ctx)
    if err != nil {
        return nil, err
    }

    // Apply memory filtering (before scoring for efficiency)
    if q.FilterByRetrieval {
        results, err = q.memoryScorer.FilterByRetrievalProbability(ctx, results, q.Explore)
        if err != nil {
            return nil, err
        }
    }

    // Apply memory-weighted scoring
    if q.ApplyMemoryWeighting {
        results, err = q.memoryScorer.ApplyMemoryWeighting(ctx, results, q.Explore)
        if err != nil {
            return nil, err
        }
    }

    return results, nil
}
```

### Spacing Effect Implementation

The ACT-R model naturally captures the spacing effect: memories are strengthened MORE by spaced retrieval than by massed practice.

```go
// core/knowledge/memory/spacing.go

package memory

import (
    "math"
    "time"
)

// SpacingAnalyzer computes optimal review intervals based on memory state
type SpacingAnalyzer struct {
    targetRetention float64  // Desired retrieval probability (default: 0.9)
}

// OptimalReviewTime computes when a memory should be reviewed
// Based on Pimsleur's graduated interval recall + ACT-R
func (s *SpacingAnalyzer) OptimalReviewTime(memory *ACTRMemory) time.Duration {
    if len(memory.Traces) == 0 {
        return 0  // Review immediately
    }

    // Compute current stability based on spacing history
    stability := s.computeStability(memory)

    // Time until retrieval probability drops below target
    // Solve: P(t) = target for t
    // P(t) = 1 / (1 + exp(-(B(t) - τ) / 0.5))
    // This requires numerical solution, but we can approximate

    // Heuristic: review when activation drops by ~1.0 from current
    d := memory.DecayMean()
    currentActivation := memory.Activation(time.Now())
    targetActivation := currentActivation - 1.0

    // Solve: ln(Σtⱼ^(-d)) = targetActivation - β
    // Approximate: time until single-trace model reaches target
    // t^(-d) = exp(target - β) → t = exp((β - target) / d)

    logTarget := targetActivation - memory.BaseOffsetMean
    if logTarget > 0 {
        return 0  // Already below target
    }

    hoursUntilReview := math.Exp(-logTarget / d) * stability
    return time.Duration(hoursUntilReview) * time.Hour
}

// computeStability estimates memory stability from spacing history
// Longer gaps between successful reviews = more stability
func (s *SpacingAnalyzer) computeStability(memory *ACTRMemory) float64 {
    if len(memory.Traces) < 2 {
        return 1.0  // Baseline stability
    }

    // Compute average inter-access interval
    var totalInterval float64
    for i := 1; i < len(memory.Traces); i++ {
        interval := memory.Traces[i].AccessedAt.Sub(memory.Traces[i-1].AccessedAt).Hours()
        totalInterval += interval
    }
    avgInterval := totalInterval / float64(len(memory.Traces)-1)

    // Stability grows with longer successful intervals
    // Based on spacing effect research
    stability := 1.0 + math.Log(1+avgInterval/24)  // Log-scaled days

    return stability
}
```

### Access Tracking Hook

```go
// core/knowledge/memory/hooks.go

package memory

import (
    "context"
    "time"

    "sylk/core/context/hooks"
)

// MemoryReinforcementHook reinforces memories when content is used
type MemoryReinforcementHook struct {
    store *MemoryStore
}

func (h *MemoryReinforcementHook) Name() string {
    return "memory_reinforcement"
}

func (h *MemoryReinforcementHook) Priority() int {
    return hooks.HookPriorityLate  // Run after retrieval
}

func (h *MemoryReinforcementHook) Agents() []string {
    return nil  // All agents
}

// OnPostPrompt reinforces memories for content referenced in response
func (h *MemoryReinforcementHook) OnPostPrompt(ctx context.Context, data *hooks.PromptHookData) error {
    // Extract referenced node IDs from response
    nodeIDs := h.extractReferencedNodes(data.Response)

    for _, nodeID := range nodeIDs {
        if err := h.store.RecordAccess(ctx, nodeID, AccessReference, "response_reference"); err != nil {
            // Log but don't fail
            continue
        }
    }

    return nil
}

// OnToolResult reinforces memories for content retrieved by tools
func (h *MemoryReinforcementHook) OnToolResult(ctx context.Context, data *hooks.ToolCallHookData) error {
    // If tool was a search/retrieval tool, reinforce retrieved content
    if !isRetrievalTool(data.ToolName) {
        return nil
    }

    nodeIDs := h.extractRetrievedNodes(data.Result)
    for _, nodeID := range nodeIDs {
        if err := h.store.RecordAccess(ctx, nodeID, AccessRetrieval, data.ToolName); err != nil {
            continue
        }
    }

    return nil
}

func (h *MemoryReinforcementHook) extractReferencedNodes(response string) []string {
    // Parse response for node references (e.g., file paths, function names)
    // Implementation depends on response format
    return nil
}

func (h *MemoryReinforcementHook) extractRetrievedNodes(result interface{}) []string {
    // Extract node IDs from tool result
    return nil
}

func isRetrievalTool(name string) bool {
    retrievalTools := map[string]bool{
        "search_codebase":     true,
        "retrieve_context":    true,
        "search_history":      true,
        "get_symbol_context":  true,
    }
    return retrievalTools[name]
}
```

**ACCEPTANCE CRITERIA - Memory Decay Model:**
- [ ] ACT-R activation computed correctly: `B = ln(Σtⱼ^(-d)) + β`
- [ ] Power law decay (d ≈ 0.5) used instead of exponential
- [ ] Access traces stored and bounded (max 100 per node)
- [ ] Memory activation affects hybrid query result ranking
- [ ] Reinforcement on access strengthens memory (spacing effect)
- [ ] Decay parameter learned per domain via Bayesian updates
- [ ] Retrieval probability computed correctly with softmax
- [ ] No memory truly reaches zero activation (floor applied)
- [ ] Domain-specific priors load correctly
- [ ] Memory state persists across restarts via SQLite
- [ ] Access tracking hooks fire on retrieval/reference
- [ ] Memory-weighted scoring integrates with HybridQueryCoordinator

---

## Cold-Start Retrieval (Cold RAG)

### The Cold-Start Problem

When Sylk starts on a new codebase for the first time, knowledge agents face the **cold-start problem**: no usage history exists to inform retrieval ranking.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                           COLD-START SCENARIOS                                       │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  AGENT         │ COLD-START SITUATION          │ AVAILABLE SIGNALS                 │
│  ──────────────┼───────────────────────────────┼─────────────────────────────────  │
│  Librarian     │ New codebase, never indexed   │ Codebase structure (from indexing)│
│                │                               │ Entity types, KG topology         │
│  ──────────────┼───────────────────────────────┼─────────────────────────────────  │
│  Academic      │ First research query          │ Query embedding only              │
│                │                               │ No codebase knowledge relevant    │
│  ──────────────┼───────────────────────────────┼─────────────────────────────────  │
│  Archivalist   │ No prior actions recorded     │ Nothing - records what we DO      │
│                │                               │ Cannot bootstrap from structure   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Cold RAG Approach

Based on research in cold-start recommendation systems (arXiv:2505.20773), we employ a **multi-signal integration** strategy that uses knowledge graph structure as a primary signal when usage data is unavailable.

**Core Insight**: The **structure** of the knowledge graph itself encodes importance, even before any user interaction.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         COLD RAG MULTI-SIGNAL FRAMEWORK                              │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐                  │
│  │  STRUCTURAL     │    │  CONTENT-BASED  │    │ DISTRIBUTIONAL  │                  │
│  │  SIGNALS        │    │  SIGNALS        │    │ SIGNALS         │                  │
│  ├─────────────────┤    ├─────────────────┤    ├─────────────────┤                  │
│  │ • InDegree      │    │ • EntityType    │    │ • TypeFrequency │                  │
│  │ • OutDegree     │    │ • Domain        │    │ • Rarity score  │                  │
│  │ • PageRank      │    │ • NameSalience  │    │ • IDF-style     │                  │
│  │ • ClusterCoeff  │    │ • DocQuality    │    │   weighting     │                  │
│  │ • Betweenness   │    │ • CodeComplexity│    │                 │                  │
│  └────────┬────────┘    └────────┬────────┘    └────────┬────────┘                  │
│           │                      │                      │                           │
│           └──────────────────────┼──────────────────────┘                           │
│                                  ▼                                                  │
│                    ┌─────────────────────────┐                                      │
│                    │   COLD-START PRIOR      │                                      │
│                    │   ColdPrior(node) =     │                                      │
│                    │   w₁·Structural +       │                                      │
│                    │   w₂·ContentBased +     │                                      │
│                    │   w₃·Distributional     │                                      │
│                    └─────────────────────────┘                                      │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Librarian Cold-Start: Structure-Based Priors

The Librarian benefits most from cold-start signals because CV.2 StartupIndexer already scans the codebase on startup. During indexing, we compute structural metrics.

#### Signals Computed During Indexing

```go
// core/knowledge/coldstart/node_signals.go

// NodeColdStartSignals computed during CV.2 StartupIndexer
type NodeColdStartSignals struct {
    NodeID       string
    EntityType   EntityType
    Domain       Domain

    // Structural signals (from KG topology)
    InDegree     int       // Nodes pointing TO this node (callers, importers)
    OutDegree    int       // Nodes this points to (callees, imports)
    PageRank     float64   // Eigenvector centrality (0-1 normalized)
    ClusterCoeff float64   // Local clustering coefficient
    Betweenness  float64   // Betweenness centrality (bridge nodes)

    // Content-based signals
    NameSalience float64   // Descriptive name score (0-1)
    DocCoverage  float64   // Has docstring/comments (0-1)
    Complexity   float64   // Cyclomatic complexity normalized

    // Distributional signals
    TypeFrequency float64  // How common is this EntityType in codebase
    TypeRarity    float64  // IDF-style: log(N / count(type))
}

// CodebaseProfile aggregates codebase-wide statistics
type CodebaseProfile struct {
    TotalNodes      int
    EntityCounts    map[EntityType]int
    DomainCounts    map[Domain]int

    // Graph statistics
    AvgInDegree     float64
    AvgOutDegree    float64
    MaxPageRank     float64

    // For normalization
    PageRankSum     float64
    BetweennessMax  float64
}
```

#### Cold Prior Computation

```go
// core/knowledge/coldstart/cold_prior.go

// ColdPriorWeights configurable weights for cold-start signals
type ColdPriorWeights struct {
    // Structural weight: 0.4 default
    StructuralWeight float64
    PageRankWeight   float64  // Within structural
    DegreeWeight     float64
    ClusterWeight    float64

    // Content-based weight: 0.35 default
    ContentWeight    float64
    EntityTypeWeight float64
    NameWeight       float64
    DocWeight        float64

    // Distributional weight: 0.25 default
    DistributionalWeight float64
    FrequencyWeight      float64
    RarityWeight         float64
}

// DefaultColdPriorWeights returns empirically-tuned defaults
func DefaultColdPriorWeights() *ColdPriorWeights {
    return &ColdPriorWeights{
        StructuralWeight: 0.40,
        PageRankWeight:   0.50,
        DegreeWeight:     0.30,
        ClusterWeight:    0.20,

        ContentWeight:    0.35,
        EntityTypeWeight: 0.40,
        NameWeight:       0.35,
        DocWeight:        0.25,

        DistributionalWeight: 0.25,
        FrequencyWeight:      0.60,
        RarityWeight:         0.40,
    }
}

// ColdPriorCalculator computes cold-start priors for nodes
type ColdPriorCalculator struct {
    profile *CodebaseProfile
    weights *ColdPriorWeights

    // Entity type base priors (hardcoded baseline)
    entityBasePriors map[EntityType]float64
}

// NewColdPriorCalculator creates calculator with codebase profile
func NewColdPriorCalculator(profile *CodebaseProfile, weights *ColdPriorWeights) *ColdPriorCalculator {
    if weights == nil {
        weights = DefaultColdPriorWeights()
    }

    return &ColdPriorCalculator{
        profile: profile,
        weights: weights,
        entityBasePriors: defaultEntityBasePriors(),
    }
}

// defaultEntityBasePriors returns hardcoded baseline priors
// Used when no other signal is available
func defaultEntityBasePriors() map[EntityType]float64 {
    return map[EntityType]float64{
        EntityFunction:    0.75,  // Functions usually relevant
        EntityMethod:      0.75,
        EntityClass:       0.70,
        EntityStruct:      0.70,
        EntityInterface:   0.65,
        EntityType:        0.60,
        EntityVariable:    0.45,
        EntityConstant:    0.50,
        EntityPackage:     0.55,
        EntityModule:      0.55,
        EntityFile:        0.40,
        EntityComment:     0.25,
        EntityImport:      0.20,
    }
}

// ComputeColdPrior calculates the cold-start prior for a node
func (c *ColdPriorCalculator) ComputeColdPrior(signals *NodeColdStartSignals) float64 {
    structural := c.computeStructuralScore(signals)
    contentBased := c.computeContentScore(signals)
    distributional := c.computeDistributionalScore(signals)

    return c.weights.StructuralWeight*structural +
           c.weights.ContentWeight*contentBased +
           c.weights.DistributionalWeight*distributional
}

func (c *ColdPriorCalculator) computeStructuralScore(s *NodeColdStartSignals) float64 {
    // Normalize PageRank
    pageRank := s.PageRank / math.Max(c.profile.MaxPageRank, 1e-10)

    // Normalize degree (log scale to handle high-degree nodes)
    avgDegree := (c.profile.AvgInDegree + c.profile.AvgOutDegree) / 2
    degreeScore := math.Log1p(float64(s.InDegree+s.OutDegree)) /
                   math.Log1p(avgDegree*4) // 4x avg as ceiling
    degreeScore = math.Min(degreeScore, 1.0)

    // Cluster coefficient already 0-1
    cluster := s.ClusterCoeff

    return c.weights.PageRankWeight*pageRank +
           c.weights.DegreeWeight*degreeScore +
           c.weights.ClusterWeight*cluster
}

func (c *ColdPriorCalculator) computeContentScore(s *NodeColdStartSignals) float64 {
    // Entity type base prior
    entityScore := c.entityBasePriors[s.EntityType]

    // Name salience already 0-1
    nameScore := s.NameSalience

    // Doc coverage already 0-1
    docScore := s.DocCoverage

    return c.weights.EntityTypeWeight*entityScore +
           c.weights.NameWeight*nameScore +
           c.weights.DocWeight*docScore
}

func (c *ColdPriorCalculator) computeDistributionalScore(s *NodeColdStartSignals) float64 {
    // Type frequency: common types may be more relevant in aggregate
    freqScore := s.TypeFrequency

    // Rarity: rare types may be more significant when found
    // Balance: not too rare (noise) but not too common (generic)
    // Optimal around 5-15% frequency
    rarityScore := 1.0 - math.Abs(s.TypeFrequency - 0.10) / 0.10
    rarityScore = math.Max(0, math.Min(1, rarityScore))

    return c.weights.FrequencyWeight*freqScore +
           c.weights.RarityWeight*rarityScore
}
```

### Blending Cold Prior with ACT-R Activation

As usage data accumulates, the system transitions from cold prior to ACT-R activation.

```go
// core/knowledge/coldstart/blender.go

// PriorBlender transitions from cold-start to warm retrieval
type PriorBlender struct {
    coldCalculator *ColdPriorCalculator
    memoryStore    *MemoryStore

    // Transition parameters
    minTracesForWarm int     // Traces needed to start blending (default: 3)
    fullWarmTraces   int     // Traces for full ACT-R reliance (default: 20)
}

// EffectivePrior computes the blended prior for retrieval scoring
func (b *PriorBlender) EffectivePrior(
    ctx context.Context,
    nodeID string,
    signals *NodeColdStartSignals,
    now time.Time,
) (float64, error) {

    // Get ACT-R memory state
    memory, err := b.memoryStore.GetMemory(ctx, nodeID, signals.Domain)
    if err != nil {
        // No memory state - pure cold start
        return b.coldCalculator.ComputeColdPrior(signals), nil
    }

    // Compute ACT-R activation
    activation := memory.Activation(now)

    // Compute confidence based on trace count
    traceCount := len(memory.Traces)
    confidence := b.computeConfidence(traceCount)

    // Compute cold prior
    coldPrior := b.coldCalculator.ComputeColdPrior(signals)

    // Blend: confidence-weighted combination
    // High confidence → ACT-R dominates
    // Low confidence → Cold prior dominates
    return confidence*activation + (1-confidence)*coldPrior, nil
}

// computeConfidence returns 0-1 based on trace count
func (b *PriorBlender) computeConfidence(traceCount int) float64 {
    if traceCount < b.minTracesForWarm {
        return 0.0 // Pure cold start
    }
    if traceCount >= b.fullWarmTraces {
        return 1.0 // Full ACT-R
    }

    // Linear interpolation
    progress := float64(traceCount - b.minTracesForWarm) /
                float64(b.fullWarmTraces - b.minTracesForWarm)
    return progress
}

// ExplainBlend returns human-readable explanation of the blend
func (b *PriorBlender) ExplainBlend(
    nodeID string,
    signals *NodeColdStartSignals,
    memory *ACTRMemory,
    now time.Time,
) string {
    traceCount := 0
    if memory != nil {
        traceCount = len(memory.Traces)
    }
    confidence := b.computeConfidence(traceCount)

    switch {
    case confidence == 0:
        return fmt.Sprintf(
            "COLD: Using structural prior (PageRank=%.2f, InDegree=%d, Type=%s)",
            signals.PageRank, signals.InDegree, signals.EntityType,
        )
    case confidence == 1:
        return fmt.Sprintf(
            "WARM: Full ACT-R (traces=%d, activation=%.2f)",
            traceCount, memory.Activation(now),
        )
    default:
        return fmt.Sprintf(
            "TRANSITIONING: %.0f%% ACT-R + %.0f%% cold (traces=%d)",
            confidence*100, (1-confidence)*100, traceCount,
        )
    }
}
```

### Integration with StartupIndexer (CV.2)

The CV.2 StartupIndexer is enhanced to compute cold-start signals during indexing.

```go
// core/context/startup_indexer.go (enhanced)

// StartupIndexer parallel codebase indexer with cold-start signal computation
type StartupIndexer struct {
    contentStore     *UniversalContentStore
    goroutineBudget  *concurrency.GoroutineBudget
    fileBudget       *resources.FileHandleBudget

    // NEW: Cold-start computation
    coldStartBuilder *ColdStartBuilder

    // Progress tracking
    onProgress       func(indexed, total int)
}

// ColdStartBuilder accumulates signals during indexing
type ColdStartBuilder struct {
    mu sync.Mutex

    // Accumulated during indexing
    nodeSignals  map[string]*NodeColdStartSignals
    entityCounts map[EntityType]int
    domainCounts map[Domain]int
    totalNodes   int

    // Edge accumulation for structural metrics
    inDegrees    map[string]int
    outDegrees   map[string]int
}

// Index indexes the codebase and computes cold-start signals
func (s *StartupIndexer) Index(ctx context.Context, root string) (*CodebaseProfile, error) {
    // Phase 1: Scan and index files (existing)
    files, err := s.scanFiles(ctx, root)
    if err != nil {
        return nil, err
    }

    // Phase 2: Index content and accumulate cold-start signals
    for i, file := range files {
        if err := s.indexFileWithSignals(ctx, file); err != nil {
            // Log but continue
            continue
        }
        if s.onProgress != nil {
            s.onProgress(i+1, len(files))
        }
    }

    // Phase 3: Compute graph metrics (PageRank, etc.)
    if err := s.computeGraphMetrics(ctx); err != nil {
        return nil, err
    }

    // Phase 4: Build and persist CodebaseProfile
    profile := s.coldStartBuilder.BuildProfile()
    if err := s.persistProfile(ctx, profile); err != nil {
        return nil, err
    }

    return profile, nil
}

// computeGraphMetrics runs PageRank and other centrality algorithms
func (s *StartupIndexer) computeGraphMetrics(ctx context.Context) error {
    // Load edges from VectorGraphDB
    edges, err := s.contentStore.vectorDB.GetAllEdges(ctx)
    if err != nil {
        return err
    }

    // Build adjacency for PageRank
    graph := buildAdjacencyGraph(edges)

    // Compute PageRank (power iteration)
    pageRanks := computePageRank(graph, 0.85, 100) // damping=0.85, maxIter=100

    // Compute clustering coefficients
    clusterCoeffs := computeClusteringCoefficients(graph)

    // Update node signals
    s.coldStartBuilder.mu.Lock()
    defer s.coldStartBuilder.mu.Unlock()

    for nodeID, rank := range pageRanks {
        if signals, ok := s.coldStartBuilder.nodeSignals[nodeID]; ok {
            signals.PageRank = rank
            signals.ClusterCoeff = clusterCoeffs[nodeID]
        }
    }

    return nil
}
```

### Schema Extension for Cold-Start Signals

```sql
-- Migration: 011_cold_start_signals.go

-- Store computed cold-start signals per node
CREATE TABLE IF NOT EXISTS node_cold_signals (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,

    -- Structural signals
    in_degree INTEGER DEFAULT 0,
    out_degree INTEGER DEFAULT 0,
    page_rank REAL DEFAULT 0.0,
    cluster_coeff REAL DEFAULT 0.0,
    betweenness REAL DEFAULT 0.0,

    -- Content signals
    name_salience REAL DEFAULT 0.5,
    doc_coverage REAL DEFAULT 0.0,
    complexity REAL DEFAULT 0.0,

    -- Distributional signals
    type_frequency REAL DEFAULT 0.0,
    type_rarity REAL DEFAULT 0.0,

    -- Computed cold prior (cached)
    cold_prior REAL DEFAULT 0.5,

    -- Metadata
    computed_at TEXT DEFAULT (datetime('now')),
    profile_version INTEGER DEFAULT 1
);

CREATE INDEX idx_cold_signals_prior ON node_cold_signals(cold_prior DESC);

-- Store codebase-wide profile
CREATE TABLE IF NOT EXISTS codebase_profile (
    id INTEGER PRIMARY KEY CHECK (id = 1),  -- Singleton

    total_nodes INTEGER DEFAULT 0,
    entity_counts_json TEXT DEFAULT '{}',
    domain_counts_json TEXT DEFAULT '{}',

    avg_in_degree REAL DEFAULT 0.0,
    avg_out_degree REAL DEFAULT 0.0,
    max_page_rank REAL DEFAULT 0.0,

    computed_at TEXT DEFAULT (datetime('now')),
    version INTEGER DEFAULT 1
);
```

### Agent-Specific Cold-Start Strategies

Different agents have fundamentally different cold-start situations:

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    AGENT-SPECIFIC COLD-START STRATEGIES                              │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ LIBRARIAN (Codebase Expert)                                                  │   │
│  │                                                                               │   │
│  │ Cold-Start Source: CV.2 StartupIndexer                                       │   │
│  │                                                                               │   │
│  │ Available Signals:                                                           │   │
│  │   ✓ KG structure (PageRank, degree, clustering)                             │   │
│  │   ✓ Entity types (function, class, interface)                               │   │
│  │   ✓ Frequency distribution (common vs rare types)                           │   │
│  │   ✓ Content features (names, documentation)                                 │   │
│  │                                                                               │   │
│  │ Strategy: Full Cold RAG with structural + content + distributional signals  │   │
│  │                                                                               │   │
│  │ Warm-up: Fast (usage data accumulates with every codebase query)            │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ ACADEMIC (Research Expert) - TWO-LAYER MODEL                                │   │
│  │                                                                               │   │
│  │ LAYER 1: Cross-Agent Awareness (Project Fact Registry)                       │   │
│  │   ✓ Reads CODEBASE facts from Librarian (what tech stack we use)            │   │
│  │   ✓ Reads HISTORY facts from Archivalist (what we've tried)                 │   │
│  │   ✓ Reads TASK facts from Architect (current goals)                         │   │
│  │   → No cold start: facts are known or not known                             │   │
│  │   → Informs JUDGMENT, not retrieval ranking                                 │   │
│  │                                                                               │   │
│  │ LAYER 2: Own Domain Retrieval (Research Index)                               │   │
│  │   Cold-Start (0 retrieval traces):                                          │   │
│  │     Score = 0.45×SourceAuthority + 0.20×PublicationRecency + 0.35×Semantic  │   │
│  │     • SourceAuthority: arXiv=0.85, docs=0.80, SO=0.65, blog=0.40           │   │
│  │     • PublicationRecency: gentle exp decay (halfLife=5yr, not aggressive)   │   │
│  │     • SemanticSimilarity: embedding match to query                          │   │
│  │                                                                               │   │
│  │   Warm (has retrieval traces):                                              │   │
│  │     Score = ACT-R activation (power law on RETRIEVAL history)               │   │
│  │     "When did we find this source useful?" (not when published)             │   │
│  │                                                                               │   │
│  │ Warm-up: Medium (research queries accumulate over session)                  │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ ARCHIVALIST (History Expert)                                                 │   │
│  │                                                                               │   │
│  │ Cold-Start Source: None (records actions, cannot bootstrap)                  │   │
│  │                                                                               │   │
│  │ Available Signals:                                                           │   │
│  │   ✗ No codebase structure (records what we DO, not what EXISTS)             │   │
│  │   ✗ No prior actions on new project                                         │   │
│  │   ✓ Action type priors (commits > reads, failures memorable)                │   │
│  │   ✓ Recency within session                                                  │   │
│  │                                                                               │   │
│  │ Strategy: Hardcoded action-type priors + session recency                    │   │
│  │                                                                               │   │
│  │ Warm-up: Medium (accumulates as session progresses)                         │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Project Fact Registry (Cross-Agent Awareness)

The Project Fact Registry provides **factual context** that all agents can read, enabling cross-agent awareness without domain corruption. Facts are not weighted - they're either known or not known.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                           PROJECT FACT REGISTRY                                      │
│                    (Shared Context, Domain-Protected)                                │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  OWNERSHIP MODEL:                                                                   │
│  ────────────────                                                                   │
│  • Each fact has an OWNER AGENT who can create/update it                           │
│  • All agents can READ all facts (cross-agent awareness)                           │
│  • No agent can WRITE facts outside their domain                                   │
│  • Facts are IMMUTABLE once written (new versions supersede old)                   │
│                                                                                     │
│  FACT TYPES BY OWNER:                                                               │
│  ────────────────────                                                               │
│  LIBRARIAN owns CODEBASE facts:                                                     │
│    • technology.cache = "Redis"                                                    │
│    • primary_language = "Go"                                                       │
│    • framework.web = "gin"                                                         │
│    • maturity = "DISCIPLINED"                                                      │
│                                                                                     │
│  ACADEMIC owns RESEARCH facts:                                                      │
│    • memory_model = "ACT-R_power_law"                                              │
│    • rejected_approach = "exponential_decay"                                       │
│    • reference.cold_start = "arXiv:2505.20773"                                    │
│                                                                                     │
│  ARCHIVALIST owns HISTORY facts:                                                    │
│    • decision.memory_model = "chose_ACT-R_over_exponential"                        │
│    • implementation.status = "GRAPH.md_updated"                                    │
│    • failure.attempted = "hardcoded_weights_rejected"                              │
│                                                                                     │
│  ARCHITECT owns TASK facts:                                                         │
│    • current_goal = "implement_cold_start_retrieval"                               │
│    • active_files = ["GRAPH.md", "TODO.md"]                                        │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Project Fact Schema

```go
// core/knowledge/facts/project_fact.go

// ProjectFact represents a factual assertion about the project
// Owned by one agent, readable by all
type ProjectFact struct {
    ID            string       `json:"id"`
    FactType      FactType     `json:"fact_type"`       // CODEBASE | RESEARCH | HISTORY | TASK
    OwnerAgent    AgentType    `json:"owner_agent"`     // Who can update this

    // The assertion
    Category      string       `json:"category"`        // e.g., "technology", "decision"
    Subject       string       `json:"subject"`         // e.g., "cache", "memory_model"
    Assertion     string       `json:"assertion"`       // e.g., "Redis", "ACT-R"

    // Metadata
    Confidence    float64      `json:"confidence"`      // Owner's confidence (0-1)
    Provenance    string       `json:"provenance"`      // "indexing", "research", "user_stated"
    EstablishedAt time.Time    `json:"established_at"`
    UpdatedAt     time.Time    `json:"updated_at"`

    // Temporal tracking
    SupersededBy  *string      `json:"superseded_by,omitempty"`
    ValidUntil    *time.Time   `json:"valid_until,omitempty"`
}

type FactType string

const (
    FactTypeCodebase FactType = "CODEBASE"  // Librarian owns
    FactTypeResearch FactType = "RESEARCH"  // Academic owns
    FactTypeHistory  FactType = "HISTORY"   // Archivalist owns
    FactTypeTask     FactType = "TASK"      // Architect owns
)

// FactRegistry manages project facts with domain protection
type FactRegistry struct {
    db        *sql.DB
    cache     map[string]*ProjectFact
    mu        sync.RWMutex
}

// WriteFact creates/updates a fact (enforces ownership)
func (r *FactRegistry) WriteFact(fact *ProjectFact, writer AgentType) error {
    // STRICT: Only owner can write to their fact type
    if !canAgentWriteFactType(writer, fact.FactType) {
        return fmt.Errorf("agent %s cannot write %s facts", writer, fact.FactType)
    }

    fact.OwnerAgent = writer
    fact.UpdatedAt = time.Now()

    return r.store(fact)
}

// ReadFacts returns all facts of a type (any agent can read)
func (r *FactRegistry) ReadFacts(factType FactType) ([]*ProjectFact, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()

    // All agents can read all facts - this enables cross-agent awareness
    return r.queryByType(factType)
}

// GetFact returns a specific fact by category/subject
func (r *FactRegistry) GetFact(factType FactType, category, subject string) *ProjectFact {
    r.mu.RLock()
    defer r.mu.RUnlock()

    key := fmt.Sprintf("%s:%s:%s", factType, category, subject)
    return r.cache[key]
}

// GetFactHistory returns all versions of a fact over time
func (r *FactRegistry) GetFactHistory(factType FactType, category, subject string) []*ProjectFact {
    // Returns temporal evolution of this fact
    // Enables agents to understand how understanding has evolved
    return r.queryHistory(factType, category, subject)
}

func canAgentWriteFactType(agent AgentType, factType FactType) bool {
    switch factType {
    case FactTypeCodebase:
        return agent == AgentLibrarian
    case FactTypeResearch:
        return agent == AgentAcademic
    case FactTypeHistory:
        return agent == AgentArchivalist
    case FactTypeTask:
        return agent == AgentArchitect || agent == AgentOrchestrator
    default:
        return false
    }
}
```

#### Fact Registry SQL Schema

```sql
-- Migration: 012_project_facts.go

CREATE TABLE IF NOT EXISTS project_facts (
    id TEXT PRIMARY KEY,
    fact_type TEXT NOT NULL CHECK (fact_type IN ('CODEBASE', 'RESEARCH', 'HISTORY', 'TASK')),
    owner_agent TEXT NOT NULL,

    category TEXT NOT NULL,
    subject TEXT NOT NULL,
    assertion TEXT NOT NULL,

    confidence REAL DEFAULT 1.0,
    provenance TEXT NOT NULL,
    established_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,

    superseded_by TEXT REFERENCES project_facts(id),
    valid_until TEXT,

    UNIQUE(fact_type, category, subject, updated_at)
);

CREATE INDEX idx_facts_type ON project_facts(fact_type);
CREATE INDEX idx_facts_lookup ON project_facts(fact_type, category, subject);
CREATE INDEX idx_facts_current ON project_facts(fact_type, category, subject)
    WHERE superseded_by IS NULL;
```

### Academic Cold-Start: Two-Layer Model

The Academic agent uses a two-layer architecture that separates cross-agent awareness from domain-specific retrieval.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                      ACADEMIC TWO-LAYER ARCHITECTURE                                 │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 1: CROSS-AGENT AWARENESS                                               │   │
│  │ (Project Fact Registry - No Cold Start)                                      │   │
│  │                                                                               │   │
│  │ Academic READS (never writes):                                               │   │
│  │   • CODEBASE facts: "This is a Go project using Redis"                      │   │
│  │   • HISTORY facts: "We tried X and rejected it"                             │   │
│  │   • TASK facts: "Currently implementing knowledge graph"                     │   │
│  │                                                                               │   │
│  │ Purpose: INFORM JUDGMENT about relevance of research                        │   │
│  │ Cold Start: NONE - facts are either known or not known                      │   │
│  │                                                                               │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                    │                                                │
│                                    ▼                                                │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 2: OWN DOMAIN RETRIEVAL                                                │   │
│  │ (Research Index - Has Cold Start, Uses ACT-R)                                │   │
│  │                                                                               │   │
│  │ Academic OWNS this index (papers, docs, external knowledge)                  │   │
│  │                                                                               │   │
│  │ COLD (0 retrieval traces):                                                   │   │
│  │   Score = w₁·SourceAuthority + w₂·PublicationRecency + w₃·SemanticSim       │   │
│  │                                                                               │   │
│  │ WARM (has retrieval traces):                                                 │   │
│  │   Score = ACT-R activation based on RETRIEVAL history                        │   │
│  │   B = ln(Σtⱼ^(-d)) + β  where tⱼ = time since retrieval j                   │   │
│  │                                                                               │   │
│  │ KEY DISTINCTION:                                                             │   │
│  │   • Publication recency: When was it WRITTEN (gentle exp decay)             │   │
│  │   • Retrieval recency: When did WE FIND IT USEFUL (ACT-R power law)         │   │
│  │                                                                               │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Academic Source Prior (Cold Start)

```go
// core/knowledge/coldstart/academic_prior.go

// AcademicSourcePrior handles cold-start for research sources
type AcademicSourcePrior struct {
    // Source authority - stable facts about source types (not learned)
    SourceAuthority map[SourceType]float64

    // Publication recency - gentle exponential (data staleness, not memory)
    // Foundational papers remain valuable, so half-life is long
    PublicationHalfLife time.Duration

    // Component weights
    AuthorityWeight float64
    RecencyWeight   float64
    SemanticWeight  float64
}

// SourceType represents the authority level of a research source
type SourceType string

const (
    SourceArxivPeerReviewed    SourceType = "arxiv_peer_reviewed"
    SourceOfficialDocs         SourceType = "official_docs"
    SourceArxivPreprint        SourceType = "arxiv_preprint"
    SourceStackOverflowAccepted SourceType = "stackoverflow_accepted"
    SourceStackOverflow        SourceType = "stackoverflow"
    SourceBlogEstablished      SourceType = "blog_established"
    SourceBlogGeneral          SourceType = "blog_general"
    SourceForumPost            SourceType = "forum_post"
)

// DefaultAcademicSourcePrior returns empirically-tuned defaults
func DefaultAcademicSourcePrior() *AcademicSourcePrior {
    return &AcademicSourcePrior{
        SourceAuthority: map[SourceType]float64{
            SourceArxivPeerReviewed:     0.85,  // Peer-reviewed research
            SourceOfficialDocs:          0.80,  // Official documentation
            SourceArxivPreprint:         0.70,  // Preprints (not yet reviewed)
            SourceStackOverflowAccepted: 0.65,  // Community-validated answers
            SourceStackOverflow:         0.50,  // General SO answers
            SourceBlogEstablished:       0.50,  // Known quality blogs
            SourceBlogGeneral:           0.40,  // General blog posts
            SourceForumPost:             0.35,  // Forum discussions
        },
        // 5-year half-life: foundational papers (ACT-R 1990) remain valuable
        PublicationHalfLife: 5 * 365 * 24 * time.Hour,
        AuthorityWeight:     0.45,
        RecencyWeight:       0.20,
        SemanticWeight:      0.35,
    }
}

// ComputeColdScore calculates score for source with no retrieval history
func (p *AcademicSourcePrior) ComputeColdScore(
    source *ResearchSource,
    queryEmbedding []float32,
) float64 {
    // 1. Source authority (stable, no cold start needed)
    authority := p.SourceAuthority[source.SourceType]
    if authority == 0 {
        authority = 0.30 // Unknown source type default
    }

    // 2. Publication recency (gentle exponential - appropriate for data staleness)
    // Unlike memory, old research CAN become stale (outdated techniques)
    // But half-life is long because foundational work remains valuable
    age := time.Since(source.PublishedAt)
    recency := math.Exp(-age.Hours() / p.PublicationHalfLife.Hours())

    // 3. Semantic similarity (always available)
    semantic := cosineSimilarity(source.Embedding, queryEmbedding)

    return p.AuthorityWeight*authority +
           p.RecencyWeight*recency +
           p.SemanticWeight*semantic
}
```

#### Academic Retriever (Cold + Warm Blending)

```go
// core/knowledge/coldstart/academic_retriever.go

// AcademicRetriever combines cold-start priors with ACT-R memory
type AcademicRetriever struct {
    coldPrior    *AcademicSourcePrior
    memoryStore  *MemoryStore           // ACT-R traces for research sources
    factRegistry *FactRegistry          // Cross-agent awareness

    // Transition thresholds
    minTracesForWarm int  // 3 - start blending after 3 retrievals
    fullWarmTraces   int  // 15 - full ACT-R after 15 retrievals
}

// NewAcademicRetriever creates retriever with both layers
func NewAcademicRetriever(
    memoryStore *MemoryStore,
    factRegistry *FactRegistry,
) *AcademicRetriever {
    return &AcademicRetriever{
        coldPrior:        DefaultAcademicSourcePrior(),
        memoryStore:      memoryStore,
        factRegistry:     factRegistry,
        minTracesForWarm: 3,
        fullWarmTraces:   15,
    }
}

// Search retrieves and ranks research sources
func (r *AcademicRetriever) Search(
    ctx context.Context,
    query string,
    queryEmbedding []float32,
    now time.Time,
) ([]*RankedResearchSource, error) {

    // Layer 1: Read project facts for context (no cold start)
    projectContext := r.gatherProjectContext()

    // Layer 2: Retrieve from own domain (research index)
    sources, err := r.searchResearchIndex(ctx, queryEmbedding)
    if err != nil {
        return nil, err
    }

    // Score each source using cold/warm blending
    ranked := make([]*RankedResearchSource, 0, len(sources))
    for _, source := range sources {
        score := r.scoreSource(ctx, source, queryEmbedding, now)
        explanation := r.explainRelevance(source, projectContext)

        ranked = append(ranked, &RankedResearchSource{
            Source:      source,
            Score:       score,
            Explanation: explanation,
        })
    }

    // Sort by score descending
    sort.Slice(ranked, func(i, j int) bool {
        return ranked[i].Score > ranked[j].Score
    })

    return ranked, nil
}

// scoreSource blends cold prior with ACT-R based on retrieval history
func (r *AcademicRetriever) scoreSource(
    ctx context.Context,
    source *ResearchSource,
    queryEmbedding []float32,
    now time.Time,
) float64 {
    // Get ACT-R memory for this source
    memory, _ := r.memoryStore.GetMemory(ctx, source.ID, DomainResearch)

    traceCount := 0
    if memory != nil {
        traceCount = len(memory.Traces)
    }

    // Pure cold start
    if traceCount < r.minTracesForWarm {
        return r.coldPrior.ComputeColdScore(source, queryEmbedding)
    }

    // Compute ACT-R activation
    activation := memory.Activation(now)
    warmScore := 1.0 / (1.0 + math.Exp(-activation)) // Softmax to 0-1

    // Full warm (ACT-R dominates)
    if traceCount >= r.fullWarmTraces {
        return warmScore
    }

    // Transitioning: linear blend
    confidence := float64(traceCount-r.minTracesForWarm) /
                  float64(r.fullWarmTraces-r.minTracesForWarm)

    coldScore := r.coldPrior.ComputeColdScore(source, queryEmbedding)

    return confidence*warmScore + (1-confidence)*coldScore
}

// gatherProjectContext reads facts from other agents (Layer 1)
func (r *AcademicRetriever) gatherProjectContext() *ProjectContext {
    return &ProjectContext{
        // Read CODEBASE facts (from Librarian)
        Languages:    r.getFactValue(FactTypeCodebase, "primary_language"),
        Frameworks:   r.getFactValues(FactTypeCodebase, "framework"),
        Technologies: r.getFactValues(FactTypeCodebase, "technology"),

        // Read HISTORY facts (from Archivalist)
        Decisions:    r.getFactValues(FactTypeHistory, "decision"),
        Failures:     r.getFactValues(FactTypeHistory, "failure"),

        // Read TASK facts (from Architect)
        CurrentGoal:  r.getFactValue(FactTypeTask, "current_goal"),
    }
}

// explainRelevance uses project context to explain why source is relevant
func (r *AcademicRetriever) explainRelevance(
    source *ResearchSource,
    ctx *ProjectContext,
) string {
    var reasons []string

    // Check technology match
    for _, tech := range ctx.Technologies {
        if source.MentionsTechnology(tech) {
            reasons = append(reasons,
                fmt.Sprintf("Directly applicable: covers %s which this project uses", tech))
        }
    }

    // Check if addresses a known decision
    for _, decision := range ctx.Decisions {
        if source.RelatesTo(decision) {
            reasons = append(reasons,
                fmt.Sprintf("Context: relates to prior decision '%s'", decision))
        }
    }

    // Check current goal alignment
    if ctx.CurrentGoal != "" && source.RelatesTo(ctx.CurrentGoal) {
        reasons = append(reasons,
            fmt.Sprintf("Goal-aligned: relates to current goal '%s'", ctx.CurrentGoal))
    }

    if len(reasons) == 0 {
        return "General relevance based on query similarity"
    }

    return strings.Join(reasons, "; ")
}

// RecordRetrieval records that a source was retrieved (for ACT-R)
func (r *AcademicRetriever) RecordRetrieval(
    ctx context.Context,
    sourceID string,
    wasUseful bool,
) error {
    return r.memoryStore.RecordAccess(ctx, sourceID, DomainResearch, wasUseful)
}
```

### Activity Event Stream (System-Wide Event Capture)

The Activity Event Stream captures **all significant events** across the system, enabling the Archivalist to maintain a complete record of what happened, when, and why.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         ACTIVITY EVENT STREAM                                        │
│                    (System-Wide Event Capture for Archivalist)                       │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  EVENT FLOW:                                                                        │
│  ───────────                                                                        │
│                                                                                     │
│    ┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐                │
│    │   User   │     │  Agent   │     │   Tool   │     │  Index   │                │
│    │  Prompt  │     │  Action  │     │   Call   │     │  Event   │                │
│    └────┬─────┘     └────┬─────┘     └────┬─────┘     └────┬─────┘                │
│         │                │                │                │                        │
│         └────────────────┴────────────────┴────────────────┘                        │
│                                    │                                                │
│                                    ▼                                                │
│                          ┌─────────────────┐                                        │
│                          │ ActivityEventBus │                                       │
│                          │   (Fan-out)      │                                       │
│                          └────────┬────────┘                                        │
│                                   │                                                 │
│                    ┌──────────────┼──────────────┐                                  │
│                    ▼              ▼              ▼                                  │
│             ┌───────────┐  ┌───────────┐  ┌───────────┐                            │
│             │ Archivalist│  │  Metrics  │  │  Audit   │                            │
│             │ Subscriber │  │ Collector │  │  Logger  │                            │
│             └───────────┘  └───────────┘  └───────────┘                            │
│                                                                                     │
│  PUBLISHERS (Who emits events):                                                     │
│  ──────────────────────────────                                                     │
│    • Guide: user_prompt, routing_decision, clarification_request                   │
│    • All Agents: action_start, action_complete, decision_made, error_encountered   │
│    • Tool Executor: tool_call, tool_result, tool_timeout                           │
│    • Librarian: index_start, index_complete, file_indexed, file_removed            │
│    • VirtualContextManager: eviction_triggered, context_restored                   │
│    • HandoffManager: handoff_triggered, handoff_complete (existing)                │
│    • LLM Provider: llm_request, llm_response, token_usage                          │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### ActivityEvent Type

```go
// core/events/activity_types.go

// EventType categorizes system events
type EventType string

const (
    // User interaction events
    EventTypeUserPrompt          EventType = "user_prompt"
    EventTypeUserClarification   EventType = "user_clarification"

    // Agent events
    EventTypeAgentAction         EventType = "agent_action"
    EventTypeAgentDecision       EventType = "agent_decision"
    EventTypeAgentError          EventType = "agent_error"
    EventTypeAgentHandoff        EventType = "agent_handoff"

    // Tool events
    EventTypeToolCall            EventType = "tool_call"
    EventTypeToolResult          EventType = "tool_result"
    EventTypeToolTimeout         EventType = "tool_timeout"

    // LLM events
    EventTypeLLMRequest          EventType = "llm_request"
    EventTypeLLMResponse         EventType = "llm_response"

    // Index events
    EventTypeIndexStart          EventType = "index_start"
    EventTypeIndexComplete       EventType = "index_complete"
    EventTypeIndexFileAdded      EventType = "index_file_added"
    EventTypeIndexFileRemoved    EventType = "index_file_removed"

    // Context events
    EventTypeContextEviction     EventType = "context_eviction"
    EventTypeContextRestore      EventType = "context_restore"

    // Outcome events
    EventTypeSuccess             EventType = "success"
    EventTypeFailure             EventType = "failure"
)

// ActivityEvent represents any significant system event
type ActivityEvent struct {
    ID          string            `json:"id"`
    EventType   EventType         `json:"event_type"`
    Timestamp   time.Time         `json:"timestamp"`
    SessionID   string            `json:"session_id"`
    AgentID     string            `json:"agent_id,omitempty"`

    // Content
    Content     string            `json:"content"`              // Main text (prompt, response, description)
    Summary     string            `json:"summary,omitempty"`    // Short summary for embedding

    // Metadata for structured queries
    Category    string            `json:"category,omitempty"`   // decision, pattern, failure, etc.
    FilePaths   []string          `json:"file_paths,omitempty"` // Related files
    Keywords    []string          `json:"keywords,omitempty"`   // Extracted keywords
    RelatedIDs  []string          `json:"related_ids,omitempty"`// Links to other events

    // Outcome tracking
    Outcome     EventOutcome      `json:"outcome"`              // success, failure, pending
    Importance  float64           `json:"importance"`           // 0-1, for ranking

    // Structured data (type-specific)
    Data        map[string]any    `json:"data,omitempty"`
}

type EventOutcome string

const (
    OutcomeSuccess EventOutcome = "success"
    OutcomeFailure EventOutcome = "failure"
    OutcomePending EventOutcome = "pending"
)

// NewActivityEvent creates a new event with defaults
func NewActivityEvent(eventType EventType, sessionID, content string) *ActivityEvent {
    return &ActivityEvent{
        ID:        generateEventID(),
        EventType: eventType,
        Timestamp: time.Now(),
        SessionID: sessionID,
        Content:   content,
        Outcome:   OutcomePending,
        Importance: defaultImportance(eventType),
    }
}

// defaultImportance returns action-type priors for cold-start ranking
func defaultImportance(eventType EventType) float64 {
    switch eventType {
    case EventTypeAgentDecision:
        return 0.90  // Decisions always relevant
    case EventTypeFailure, EventTypeAgentError:
        return 0.85  // Failures are memorable
    case EventTypeSuccess:
        return 0.75  // Validates approaches
    case EventTypeIndexComplete:
        return 0.70  // Important temporal marker
    case EventTypeAgentHandoff:
        return 0.65  // Context continuity
    case EventTypeToolCall, EventTypeToolResult:
        return 0.50  // Useful but noisy
    case EventTypeLLMResponse:
        return 0.45  // High volume
    case EventTypeUserPrompt:
        return 0.40  // Needs synthesis
    default:
        return 0.30  // Default low
    }
}
```

#### ActivityEventBus

```go
// core/events/activity_bus.go

// ActivityEventBus provides pub/sub for system-wide events
type ActivityEventBus struct {
    subscribers map[string][]EventSubscriber
    mu          sync.RWMutex
    buffer      chan *ActivityEvent
    done        chan struct{}

    // Debouncing
    debouncer   *EventDebouncer
}

// EventSubscriber receives events of interest
type EventSubscriber interface {
    ID() string
    EventTypes() []EventType  // Which events to receive (empty = all)
    OnEvent(event *ActivityEvent) error
}

// NewActivityEventBus creates a new event bus
func NewActivityEventBus(bufferSize int) *ActivityEventBus {
    bus := &ActivityEventBus{
        subscribers: make(map[string][]EventSubscriber),
        buffer:      make(chan *ActivityEvent, bufferSize),
        done:        make(chan struct{}),
        debouncer:   NewEventDebouncer(5 * time.Second),
    }
    go bus.dispatch()
    return bus
}

// Publish sends an event to all interested subscribers
func (b *ActivityEventBus) Publish(event *ActivityEvent) {
    // Debounce similar events
    if b.debouncer.ShouldSkip(event) {
        return
    }

    select {
    case b.buffer <- event:
    default:
        // Buffer full, drop event (log warning)
    }
}

// Subscribe registers a subscriber for events
func (b *ActivityEventBus) Subscribe(sub EventSubscriber) {
    b.mu.Lock()
    defer b.mu.Unlock()

    types := sub.EventTypes()
    if len(types) == 0 {
        // Subscribe to all
        b.subscribers["*"] = append(b.subscribers["*"], sub)
    } else {
        for _, t := range types {
            key := string(t)
            b.subscribers[key] = append(b.subscribers[key], sub)
        }
    }
}

func (b *ActivityEventBus) dispatch() {
    for {
        select {
        case event := <-b.buffer:
            b.deliverEvent(event)
        case <-b.done:
            return
        }
    }
}

func (b *ActivityEventBus) deliverEvent(event *ActivityEvent) {
    b.mu.RLock()
    defer b.mu.RUnlock()

    // Deliver to type-specific subscribers
    key := string(event.EventType)
    for _, sub := range b.subscribers[key] {
        go sub.OnEvent(event)
    }

    // Deliver to wildcard subscribers
    for _, sub := range b.subscribers["*"] {
        go sub.OnEvent(event)
    }
}

// EventDebouncer prevents duplicate events within a time window
type EventDebouncer struct {
    window    time.Duration
    seen      map[string]time.Time
    mu        sync.Mutex
}

func NewEventDebouncer(window time.Duration) *EventDebouncer {
    return &EventDebouncer{
        window: window,
        seen:   make(map[string]time.Time),
    }
}

func (d *EventDebouncer) ShouldSkip(event *ActivityEvent) bool {
    d.mu.Lock()
    defer d.mu.Unlock()

    // Create signature for deduplication
    sig := fmt.Sprintf("%s:%s:%s", event.EventType, event.AgentID, event.SessionID)

    if lastSeen, ok := d.seen[sig]; ok {
        if time.Since(lastSeen) < d.window {
            return true  // Skip duplicate
        }
    }

    d.seen[sig] = time.Now()
    return false
}
```

### Index Event Stream

The Index Event Stream specifically captures indexing operations, enabling the Archivalist to know **when** knowledge was added to the system and correlate queries with available context.

```go
// core/events/index_events.go

// IndexEvent captures indexing operations
type IndexEvent struct {
    ID            string         `json:"id"`
    EventType     IndexEventType `json:"event_type"`
    Timestamp     time.Time      `json:"timestamp"`
    SessionID     string         `json:"session_id"`

    // Index metadata
    IndexType     IndexType      `json:"index_type"`      // full, incremental, file_change
    IndexVersion  int64          `json:"index_version"`
    PrevVersion   int64          `json:"prev_version,omitempty"`

    // Scope
    RootPath      string         `json:"root_path"`
    Duration      time.Duration  `json:"duration"`

    // Statistics
    FilesIndexed  int            `json:"files_indexed"`
    FilesRemoved  int            `json:"files_removed"`
    FilesUpdated  int            `json:"files_updated"`
    EntitiesFound int            `json:"entities_found"`
    EdgesCreated  int            `json:"edges_created"`

    // Error tracking
    Errors        []IndexError   `json:"errors,omitempty"`
}

type IndexEventType string

const (
    IndexEventTypeStart      IndexEventType = "start"
    IndexEventTypeComplete   IndexEventType = "complete"
    IndexEventTypeFileAdd    IndexEventType = "file_add"
    IndexEventTypeFileRemove IndexEventType = "file_remove"
    IndexEventTypeError      IndexEventType = "error"
)

type IndexType string

const (
    IndexTypeFull        IndexType = "full"
    IndexTypeIncremental IndexType = "incremental"
    IndexTypeFileChange  IndexType = "file_change"
)

type IndexError struct {
    FilePath string `json:"file_path"`
    Error    string `json:"error"`
}

// IndexEventPublisher publishes index events to the ActivityEventBus
type IndexEventPublisher struct {
    bus       *ActivityEventBus
    sessionID string
}

func NewIndexEventPublisher(bus *ActivityEventBus, sessionID string) *IndexEventPublisher {
    return &IndexEventPublisher{bus: bus, sessionID: sessionID}
}

func (p *IndexEventPublisher) PublishIndexStart(indexType IndexType, rootPath string) {
    event := &IndexEvent{
        ID:        generateEventID(),
        EventType: IndexEventTypeStart,
        Timestamp: time.Now(),
        SessionID: p.sessionID,
        IndexType: indexType,
        RootPath:  rootPath,
    }

    p.bus.Publish(&ActivityEvent{
        ID:        event.ID,
        EventType: EventTypeIndexStart,
        Timestamp: event.Timestamp,
        SessionID: event.SessionID,
        Content:   fmt.Sprintf("Starting %s index of %s", indexType, rootPath),
        Importance: 0.60,
        Data:      map[string]any{"index_event": event},
    })
}

func (p *IndexEventPublisher) PublishIndexComplete(event *IndexEvent) {
    p.bus.Publish(&ActivityEvent{
        ID:        event.ID,
        EventType: EventTypeIndexComplete,
        Timestamp: event.Timestamp,
        SessionID: event.SessionID,
        Content: fmt.Sprintf("Completed %s index: %d files, %d entities, %d edges",
            event.IndexType, event.FilesIndexed, event.EntitiesFound, event.EdgesCreated),
        Summary:    fmt.Sprintf("Index complete: %d files", event.FilesIndexed),
        Importance: 0.70,
        Data:       map[string]any{"index_event": event},
    })
}
```

### Archivalist Dual-Write Ingestion

The Archivalist ingests events into **both** Document DB (Bleve) and Vector DB (HNSW) with different representations optimized for different query types.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         ARCHIVALIST DUAL-WRITE ARCHITECTURE                          │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌──────────────────┐                                                               │
│  │  ActivityEvent   │                                                               │
│  │ (from EventBus)  │                                                               │
│  └────────┬─────────┘                                                               │
│           │                                                                         │
│           ▼                                                                         │
│  ┌──────────────────┐                                                               │
│  │ Event Classifier │ ──► Determines: write_strategy, importance, category         │
│  └────────┬─────────┘                                                               │
│           │                                                                         │
│           ▼                                                                         │
│  ┌──────────────────────────────────────────────────────────────────────────────┐  │
│  │                        DUAL-WRITE STRATEGY                                    │  │
│  ├──────────────────────────────────────────────────────────────────────────────┤  │
│  │                                                                               │  │
│  │  Event Type          Document DB (Bleve)    Vector DB (HNSW)    Rationale    │  │
│  │  ─────────────────────────────────────────────────────────────────────────── │  │
│  │  user_prompt         ✓ metadata            ✓ full text         Semantic      │  │
│  │  llm_response        ✓ metadata            ✓ full text         Semantic      │  │
│  │  agent_decision      ✓ full + keywords     ✓ full text         Both needed   │  │
│  │  failure             ✓ full + keywords     ✓ full text         Both needed   │  │
│  │  success             ✓ metadata            ✓ description       Light weight  │  │
│  │  tool_call           ✓ structured only     ✗ skip              Too noisy     │  │
│  │  tool_result         ✓ structured only     ✗ skip              Too noisy     │  │
│  │  index_complete      ✓ structured only     ✗ skip              Structured    │  │
│  │  agent_handoff       ✓ full + keywords     ✓ summary           Important     │  │
│  │  context_eviction    ✓ metadata            ✗ skip              Structured    │  │
│  │                                                                               │  │
│  └──────────────────────────────────────────────────────────────────────────────┘  │
│           │                                                                         │
│     ┌─────┴─────┐                                                                   │
│     ▼           ▼                                                                   │
│  ┌──────────┐ ┌──────────┐                                                         │
│  │  Bleve   │ │  HNSW    │                                                         │
│  │ Indexer  │ │ Embedder │                                                         │
│  └──────────┘ └──────────┘                                                         │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Dual-Write Implementation

```go
// agents/archivalist/dual_writer.go

// DualWriteStrategy determines how an event is stored
type DualWriteStrategy struct {
    WriteBleve     bool
    WriteVector    bool
    BleveFields    []string  // Which fields to index in Bleve
    VectorContent  string    // What to embed (full, summary, keywords)
    Aggregate      bool      // Aggregate with similar recent events
}

// EventClassifier determines dual-write strategy per event type
type EventClassifier struct {
    strategies map[EventType]*DualWriteStrategy
}

func NewEventClassifier() *EventClassifier {
    return &EventClassifier{
        strategies: map[EventType]*DualWriteStrategy{
            // High-value events: both stores, full content
            EventTypeAgentDecision: {
                WriteBleve:    true,
                WriteVector:   true,
                BleveFields:   []string{"content", "keywords", "category", "agent_id"},
                VectorContent: "full",
            },
            EventTypeFailure: {
                WriteBleve:    true,
                WriteVector:   true,
                BleveFields:   []string{"content", "keywords", "category", "file_paths"},
                VectorContent: "full",
            },
            EventTypeAgentError: {
                WriteBleve:    true,
                WriteVector:   true,
                BleveFields:   []string{"content", "keywords", "category"},
                VectorContent: "full",
            },

            // User interaction: both stores for semantic search
            EventTypeUserPrompt: {
                WriteBleve:    true,
                WriteVector:   true,
                BleveFields:   []string{"content", "session_id"},
                VectorContent: "full",
            },
            EventTypeLLMResponse: {
                WriteBleve:    true,
                WriteVector:   true,
                BleveFields:   []string{"summary", "agent_id"},
                VectorContent: "summary",  // Responses can be long
            },

            // Handoffs: important for continuity
            EventTypeAgentHandoff: {
                WriteBleve:    true,
                WriteVector:   true,
                BleveFields:   []string{"content", "agent_id", "keywords"},
                VectorContent: "summary",
            },

            // Success: lighter weight
            EventTypeSuccess: {
                WriteBleve:    true,
                WriteVector:   true,
                BleveFields:   []string{"summary", "category"},
                VectorContent: "summary",
            },

            // Tool events: structured only (high volume, low semantic value)
            EventTypeToolCall: {
                WriteBleve:    true,
                WriteVector:   false,
                BleveFields:   []string{"agent_id", "data"},
                Aggregate:     true,  // Aggregate multiple tool calls
            },
            EventTypeToolResult: {
                WriteBleve:    true,
                WriteVector:   false,
                BleveFields:   []string{"agent_id", "outcome"},
                Aggregate:     true,
            },

            // Index events: structured metadata only
            EventTypeIndexStart: {
                WriteBleve:    true,
                WriteVector:   false,
                BleveFields:   []string{"data"},
            },
            EventTypeIndexComplete: {
                WriteBleve:    true,
                WriteVector:   false,
                BleveFields:   []string{"content", "data"},
            },

            // Context events: metadata only
            EventTypeContextEviction: {
                WriteBleve:    true,
                WriteVector:   false,
                BleveFields:   []string{"data"},
            },
        },
    }
}

// DualWriter writes events to both Bleve and VectorDB
type DualWriter struct {
    bleveIndex   *BleveEventIndex
    vectorStore  *VectorEventStore
    classifier   *EventClassifier
    aggregator   *EventAggregator
}

func NewDualWriter(
    bleveIndex *BleveEventIndex,
    vectorStore *VectorEventStore,
) *DualWriter {
    return &DualWriter{
        bleveIndex:  bleveIndex,
        vectorStore: vectorStore,
        classifier:  NewEventClassifier(),
        aggregator:  NewEventAggregator(5 * time.Second),
    }
}

// Write stores an event according to its dual-write strategy
func (w *DualWriter) Write(ctx context.Context, event *ActivityEvent) error {
    strategy := w.classifier.GetStrategy(event.EventType)

    // Handle aggregation for high-volume events
    if strategy.Aggregate {
        aggregated := w.aggregator.Add(event)
        if aggregated == nil {
            return nil  // Buffered for aggregation
        }
        event = aggregated
    }

    var wg sync.WaitGroup
    var bleveErr, vectorErr error

    // Write to Bleve (Document DB)
    if strategy.WriteBleve {
        wg.Add(1)
        go func() {
            defer wg.Done()
            bleveErr = w.bleveIndex.IndexEvent(ctx, event, strategy.BleveFields)
        }()
    }

    // Write to VectorDB
    if strategy.WriteVector {
        wg.Add(1)
        go func() {
            defer wg.Done()
            vectorErr = w.vectorStore.EmbedEvent(ctx, event, strategy.VectorContent)
        }()
    }

    wg.Wait()

    // Return first error (could enhance to return both)
    if bleveErr != nil {
        return bleveErr
    }
    return vectorErr
}
```

### Archivalist Cold-Start: Two-Layer Model

Like the Academic, the Archivalist uses a two-layer model separating cross-agent awareness from own-domain retrieval.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    ARCHIVALIST COLD-START: TWO-LAYER MODEL                           │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 1: CROSS-AGENT AWARENESS                                               │   │
│  │ (Project Fact Registry - No Cold Start)                                      │   │
│  │                                                                               │   │
│  │ Archivalist READS (from other agents):                                       │   │
│  │   • CODEBASE facts: "This is a Go project using Redis, gin"                 │   │
│  │   • RESEARCH facts: "We chose ACT-R over exponential decay"                 │   │
│  │   • TASK facts: "Currently implementing knowledge graph"                     │   │
│  │                                                                               │   │
│  │ Archivalist WRITES (its domain - HISTORY facts):                             │   │
│  │   • decision.memory_model = "chose_ACT-R_over_exponential"                  │   │
│  │   • failure.attempted = "hardcoded_weights_rejected"                        │   │
│  │   • implementation.status = "cold_start_complete"                           │   │
│  │                                                                               │   │
│  │ Purpose: CONTEXTUAL GROUNDING for answering queries                          │   │
│  │ Cold Start: NONE - facts are either known or not known                       │   │
│  │                                                                               │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                    │                                                │
│                                    ▼                                                │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ LAYER 2: OWN DOMAIN RETRIEVAL                                                │   │
│  │ (Activity History - Has Cold Start)                                          │   │
│  │                                                                               │   │
│  │ ═══════════════════════════════════════════════════════════════════════════ │   │
│  │ SCENARIO A: Cold (New Project, First Session)                                │   │
│  │ ═══════════════════════════════════════════════════════════════════════════ │   │
│  │                                                                               │   │
│  │   Score = w₁×ActionTypePrior + w₂×SessionRecency + w₃×SemanticSimilarity    │   │
│  │                                                                               │   │
│  │   ActionTypePrior (Inherent event importance):                               │   │
│  │     • decision=0.90, failure=0.85, success=0.75, file_change=0.70           │   │
│  │     • handoff=0.65, index=0.60, tool_call=0.50, response=0.45               │   │
│  │     • prompt=0.40, file_read=0.20                                           │   │
│  │                                                                               │   │
│  │   SessionRecency (Within-session position):                                  │   │
│  │     Recency = 1 / (1 + turns_since_event/10)                                │   │
│  │     • Recent events rank higher for "what did we just do?"                  │   │
│  │                                                                               │   │
│  │   Component Weights (Cold): Action=0.40, Recency=0.25, Semantic=0.35        │   │
│  │                                                                               │   │
│  │ ═══════════════════════════════════════════════════════════════════════════ │   │
│  │ SCENARIO B: Warm (Session Has Accumulated Events)                            │   │
│  │ ═══════════════════════════════════════════════════════════════════════════ │   │
│  │                                                                               │   │
│  │   Score = ACT-R_activation(event) × SemanticSimilarity                      │   │
│  │                                                                               │   │
│  │   ACT-R: B = ln(Σtⱼ^(-d)) + β                                               │   │
│  │     • d = 0.4 (slower decay - history stays useful longer)                  │   │
│  │     • β = learned base offset per event type                                │   │
│  │     • "Events we've found useful before rank higher"                        │   │
│  │                                                                               │   │
│  │   Transition: minTracesForWarm=3, fullWarmTraces=15                         │   │
│  │                                                                               │   │
│  │ ═══════════════════════════════════════════════════════════════════════════ │   │
│  │ SCENARIO C: Cross-Session Bootstrap (Returning Project)                      │   │
│  │ ═══════════════════════════════════════════════════════════════════════════ │   │
│  │                                                                               │   │
│  │   If prior sessions exist:                                                   │   │
│  │     1. Load session summaries (compressed historical knowledge)              │   │
│  │     2. Inherit ACT-R traces with cross-session decay                        │   │
│  │        Cross-session decay: activation × e^(-days_away/7)                   │   │
│  │     3. Index events tell us "when was this project last indexed?"           │   │
│  │                                                                               │   │
│  │   Query: "What did we do last time?"                                        │   │
│  │     → Retrieves prior session summaries + high-activation events            │   │
│  │                                                                               │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

#### Archivalist Cold-Start Implementation

```go
// core/knowledge/coldstart/archivalist_prior.go

// ActionTypePrior provides cold-start priors based on event type importance
type ActionTypePrior struct {
    // Event type importance (stable, not learned)
    TypeImportance map[EventType]float64

    // Component weights for cold scoring
    ActionWeight   float64  // 0.40
    RecencyWeight  float64  // 0.25
    SemanticWeight float64  // 0.35
}

// DefaultActionTypePrior returns empirically-tuned defaults
func DefaultActionTypePrior() *ActionTypePrior {
    return &ActionTypePrior{
        TypeImportance: map[EventType]float64{
            EventTypeAgentDecision:  0.90,  // Decisions always relevant
            EventTypeFailure:        0.85,  // Failures are memorable
            EventTypeAgentError:     0.85,  // Errors need tracking
            EventTypeSuccess:        0.75,  // Validates approaches
            EventTypeIndexComplete:  0.70,  // Important temporal marker
            EventTypeAgentHandoff:   0.65,  // Context continuity
            EventTypeIndexStart:     0.60,  // Temporal context
            EventTypeToolCall:       0.50,  // Useful but noisy
            EventTypeLLMResponse:    0.45,  // High volume
            EventTypeUserPrompt:     0.40,  // Needs synthesis
            EventTypeToolResult:     0.35,  // Usually tool_call is enough
            EventTypeContextEviction: 0.30, // Rare query target
        },
        ActionWeight:   0.40,
        RecencyWeight:  0.25,
        SemanticWeight: 0.35,
    }
}

// ComputeColdScore calculates score for event with no retrieval history
func (p *ActionTypePrior) ComputeColdScore(
    event *ActivityEvent,
    queryEmbedding []float32,
    eventEmbedding []float32,
    currentTurn int,
    eventTurn int,
) float64 {
    // 1. Action type importance (stable prior)
    actionScore := p.TypeImportance[event.EventType]
    if actionScore == 0 {
        actionScore = 0.30  // Default for unknown types
    }

    // 2. Session recency (within-session position)
    turnsSince := float64(currentTurn - eventTurn)
    recencyScore := 1.0 / (1.0 + turnsSince/10.0)

    // 3. Semantic similarity (always available)
    semanticScore := cosineSimilarity(queryEmbedding, eventEmbedding)

    return p.ActionWeight*actionScore +
           p.RecencyWeight*recencyScore +
           p.SemanticWeight*semanticScore
}
```

#### Archivalist Retriever (Two-Layer)

```go
// core/knowledge/coldstart/archivalist_retriever.go

// ArchivalistRetriever combines cold-start priors with ACT-R memory
type ArchivalistRetriever struct {
    coldPrior    *ActionTypePrior
    memoryStore  *MemoryStore
    factRegistry *FactRegistry
    dualWriter   *DualWriter
    bleveIndex   *BleveEventIndex
    vectorStore  *VectorEventStore

    // Transition thresholds
    minTracesForWarm int  // 3 - start blending after 3 retrievals
    fullWarmTraces   int  // 15 - full ACT-R after 15 retrievals

    // Cross-session settings
    crossSessionDecayDays float64  // 7 - half-life for cross-session decay
}

// NewArchivalistRetriever creates retriever with both layers
func NewArchivalistRetriever(
    memoryStore *MemoryStore,
    factRegistry *FactRegistry,
    dualWriter *DualWriter,
    bleveIndex *BleveEventIndex,
    vectorStore *VectorEventStore,
) *ArchivalistRetriever {
    return &ArchivalistRetriever{
        coldPrior:             DefaultActionTypePrior(),
        memoryStore:           memoryStore,
        factRegistry:          factRegistry,
        dualWriter:            dualWriter,
        bleveIndex:            bleveIndex,
        vectorStore:           vectorStore,
        minTracesForWarm:      3,
        fullWarmTraces:        15,
        crossSessionDecayDays: 7.0,
    }
}

// Search retrieves and ranks historical events
func (r *ArchivalistRetriever) Search(
    ctx context.Context,
    query string,
    queryEmbedding []float32,
    opts *ArchivalistSearchOptions,
) ([]*RankedActivityEvent, error) {

    // Layer 1: Gather project context (no cold start)
    projectContext := r.gatherProjectContext()

    // Layer 2: Retrieve from both stores
    // Parallel search: Bleve (structured) + VectorDB (semantic)
    var wg sync.WaitGroup
    var bleveResults []*ActivityEvent
    var vectorResults []*ActivityEvent

    wg.Add(2)
    go func() {
        defer wg.Done()
        bleveResults, _ = r.bleveIndex.Search(ctx, query, opts)
    }()
    go func() {
        defer wg.Done()
        vectorResults, _ = r.vectorStore.Search(ctx, queryEmbedding, opts)
    }()
    wg.Wait()

    // Merge and deduplicate
    events := r.mergeResults(bleveResults, vectorResults)

    // Score each event using cold/warm blending
    ranked := make([]*RankedActivityEvent, 0, len(events))
    for _, event := range events {
        score := r.scoreEvent(ctx, event, queryEmbedding, opts.CurrentTurn, opts.Now)
        explanation := r.explainRelevance(event, projectContext, query)

        ranked = append(ranked, &RankedActivityEvent{
            Event:       event,
            Score:       score,
            Explanation: explanation,
        })
    }

    // Sort by score descending
    sort.Slice(ranked, func(i, j int) bool {
        return ranked[i].Score > ranked[j].Score
    })

    return ranked, nil
}

// scoreEvent blends cold prior with ACT-R based on retrieval history
func (r *ArchivalistRetriever) scoreEvent(
    ctx context.Context,
    event *ActivityEvent,
    queryEmbedding []float32,
    currentTurn int,
    now time.Time,
) float64 {
    // Get ACT-R memory for this event
    memory, _ := r.memoryStore.GetMemory(ctx, event.ID, DomainHistory)

    traceCount := 0
    if memory != nil {
        traceCount = len(memory.Traces)
    }

    // Get event embedding for semantic scoring
    eventEmbedding := r.vectorStore.GetEmbedding(event.ID)
    eventTurn := r.getTurnForEvent(event)

    // Pure cold start
    if traceCount < r.minTracesForWarm {
        return r.coldPrior.ComputeColdScore(event, queryEmbedding, eventEmbedding, currentTurn, eventTurn)
    }

    // Compute ACT-R activation
    activation := memory.Activation(now)
    warmScore := 1.0 / (1.0 + math.Exp(-activation))  // Softmax to 0-1

    // Add semantic similarity
    semantic := cosineSimilarity(queryEmbedding, eventEmbedding)
    warmScore = 0.7*warmScore + 0.3*semantic

    // Full warm (ACT-R dominates)
    if traceCount >= r.fullWarmTraces {
        return warmScore
    }

    // Transitioning: linear blend
    confidence := float64(traceCount-r.minTracesForWarm) /
                  float64(r.fullWarmTraces-r.minTracesForWarm)

    coldScore := r.coldPrior.ComputeColdScore(event, queryEmbedding, eventEmbedding, currentTurn, eventTurn)

    return confidence*warmScore + (1-confidence)*coldScore
}

// LoadCrossSessionState loads prior session state with decay
func (r *ArchivalistRetriever) LoadCrossSessionState(
    ctx context.Context,
    projectID string,
    daysSinceLastSession float64,
) error {
    // Load prior session summaries
    summaries, err := r.loadSessionSummaries(ctx, projectID)
    if err != nil {
        return err
    }

    // Apply cross-session decay to ACT-R traces
    decayFactor := math.Exp(-daysSinceLastSession / r.crossSessionDecayDays)

    for _, summary := range summaries {
        // Inherit important events with decayed activation
        for _, eventID := range summary.HighValueEventIDs {
            memory, _ := r.memoryStore.GetMemory(ctx, eventID, DomainHistory)
            if memory != nil {
                // Apply decay to all trace times (makes them appear older)
                memory.ApplyCrossSessionDecay(decayFactor)
                r.memoryStore.SaveMemory(ctx, memory)
            }
        }
    }

    return nil
}

// gatherProjectContext reads facts from other agents (Layer 1)
func (r *ArchivalistRetriever) gatherProjectContext() *ProjectContext {
    return &ProjectContext{
        // Read CODEBASE facts (from Librarian)
        Languages:    r.factRegistry.GetFactValue(FactTypeCodebase, "primary_language"),
        Frameworks:   r.factRegistry.GetFactValues(FactTypeCodebase, "framework"),
        Technologies: r.factRegistry.GetFactValues(FactTypeCodebase, "technology"),

        // Read RESEARCH facts (from Academic)
        ApproachesChosen:   r.factRegistry.GetFactValues(FactTypeResearch, "approach"),
        ApproachesRejected: r.factRegistry.GetFactValues(FactTypeResearch, "rejected_approach"),

        // Read TASK facts (from Architect)
        CurrentGoal: r.factRegistry.GetFactValue(FactTypeTask, "current_goal"),
    }
}

// explainRelevance uses project context to explain why event is relevant
func (r *ArchivalistRetriever) explainRelevance(
    event *ActivityEvent,
    ctx *ProjectContext,
    query string,
) string {
    var reasons []string

    // Check event type relevance
    switch event.EventType {
    case EventTypeAgentDecision:
        reasons = append(reasons, "This was a decision point")
    case EventTypeFailure, EventTypeAgentError:
        reasons = append(reasons, "This was a failure that may inform current work")
    case EventTypeSuccess:
        reasons = append(reasons, "This approach succeeded previously")
    }

    // Check technology match
    for _, tech := range ctx.Technologies {
        if containsIgnoreCase(event.Content, tech) {
            reasons = append(reasons,
                fmt.Sprintf("Relates to %s which this project uses", tech))
        }
    }

    // Check if addresses a known decision
    for _, approach := range ctx.ApproachesChosen {
        if containsIgnoreCase(event.Content, approach) {
            reasons = append(reasons,
                fmt.Sprintf("Relates to chosen approach '%s'", approach))
        }
    }

    if len(reasons) == 0 {
        return "General relevance based on query similarity"
    }

    return strings.Join(reasons, "; ")
}

// RecordRetrieval records that an event was retrieved (for ACT-R learning)
func (r *ArchivalistRetriever) RecordRetrieval(
    ctx context.Context,
    eventID string,
    wasUseful bool,
) error {
    return r.memoryStore.RecordAccess(ctx, eventID, DomainHistory, wasUseful)
}
```

#### Schema Extension for Archivalist Events

```sql
-- Migration: 013_archivalist_events.go

-- Activity events table (Document DB backing store)
CREATE TABLE IF NOT EXISTS activity_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    session_id TEXT NOT NULL,
    agent_id TEXT,

    content TEXT NOT NULL,
    summary TEXT,
    category TEXT,

    file_paths_json TEXT DEFAULT '[]',
    keywords_json TEXT DEFAULT '[]',
    related_ids_json TEXT DEFAULT '[]',

    outcome TEXT DEFAULT 'pending',
    importance REAL DEFAULT 0.5,

    data_json TEXT DEFAULT '{}',

    -- For efficient queries
    turn_number INTEGER,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX idx_events_session ON activity_events(session_id, timestamp DESC);
CREATE INDEX idx_events_type ON activity_events(event_type, timestamp DESC);
CREATE INDEX idx_events_agent ON activity_events(agent_id, timestamp DESC);
CREATE INDEX idx_events_category ON activity_events(category, timestamp DESC);
CREATE INDEX idx_events_outcome ON activity_events(outcome);

-- Index events table
CREATE TABLE IF NOT EXISTS index_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    session_id TEXT NOT NULL,

    index_type TEXT NOT NULL,
    index_version INTEGER NOT NULL,
    prev_version INTEGER,

    root_path TEXT NOT NULL,
    duration_ms INTEGER,

    files_indexed INTEGER DEFAULT 0,
    files_removed INTEGER DEFAULT 0,
    files_updated INTEGER DEFAULT 0,
    entities_found INTEGER DEFAULT 0,
    edges_created INTEGER DEFAULT 0,

    errors_json TEXT DEFAULT '[]',

    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX idx_index_events_session ON index_events(session_id, timestamp DESC);
CREATE INDEX idx_index_events_version ON index_events(index_version DESC);

-- Session summaries for cross-session bootstrap
CREATE TABLE IF NOT EXISTS session_summaries (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE,
    project_id TEXT NOT NULL,

    started_at TEXT NOT NULL,
    ended_at TEXT,
    duration_seconds INTEGER,

    summary TEXT NOT NULL,
    key_decisions_json TEXT DEFAULT '[]',
    key_failures_json TEXT DEFAULT '[]',
    high_value_event_ids_json TEXT DEFAULT '[]',

    event_count INTEGER DEFAULT 0,
    decision_count INTEGER DEFAULT 0,
    failure_count INTEGER DEFAULT 0,

    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX idx_session_summaries_project ON session_summaries(project_id, ended_at DESC);
```

### Acceptance Criteria - Cold-Start Retrieval

**ACCEPTANCE CRITERIA - Cold-Start Retrieval (Cold RAG):**
- [ ] NodeColdStartSignals computed during CV.2 StartupIndexer
- [ ] PageRank computed for all nodes after indexing
- [ ] Clustering coefficient computed for graph topology
- [ ] CodebaseProfile persisted to SQLite
- [ ] ColdPriorCalculator produces valid 0-1 scores
- [ ] PriorBlender transitions smoothly from cold → warm
- [ ] Librarian uses structural priors on new codebase
- [ ] Academic uses embedding + source authority (no codebase signals)
- [ ] Archivalist uses action-type priors (no codebase signals)
- [ ] Cold prior explains its reasoning (ExplainBlend)
- [ ] Structural metrics update on incremental re-index

**ACCEPTANCE CRITERIA - Activity Event Stream (Group 4Y):**
- [ ] ActivityEventBus created and running on session start
- [ ] All event types (user_prompt, llm_response, tool_call, etc.) captured
- [ ] Event debouncing prevents duplicate events within 5s window
- [ ] Guide publishes user_prompt events
- [ ] Tool Executor publishes tool_call and tool_result events
- [ ] All agents publish action_start, action_complete, decision_made events
- [ ] Archivalist subscribes and receives all events

**ACCEPTANCE CRITERIA - Index Event Stream (Group 4Y):**
- [ ] IndexEventPublisher created with ActivityEventBus reference
- [ ] StartupIndexer publishes index_start on IndexProject()
- [ ] StartupIndexer publishes index_complete with statistics
- [ ] IncrementalIndex publishes index events
- [ ] Index events include file counts, entity counts, edge counts
- [ ] Archivalist can query "when was this project last indexed?"

**ACCEPTANCE CRITERIA - Archivalist Dual-Write (Group 4Y):**
- [ ] DualWriter receives events from ActivityEventBus subscription
- [ ] EventClassifier determines write strategy per event type
- [ ] High-value events (decisions, failures) written to BOTH stores
- [ ] Tool events written to Bleve only (structured)
- [ ] Event aggregation reduces volume for high-frequency events
- [ ] Parallel writes to Bleve and VectorDB
- [ ] Write failures logged and retried (WAL pattern)

**ACCEPTANCE CRITERIA - Archivalist Cold-Start (Group 4Y):**
- [ ] ActionTypePrior provides stable importance scores per event type
- [ ] Cold scoring uses action + recency + semantic (0.40/0.25/0.35)
- [ ] Warm scoring transitions to ACT-R after 3 retrievals
- [ ] Full ACT-R scoring after 15 retrievals
- [ ] Cross-session bootstrap loads prior session summaries
- [ ] Cross-session decay applied (7-day half-life)
- [ ] Two-layer model: Project Facts (Layer 1) + Activity History (Layer 2)
- [ ] Query fusion: Bleve + VectorDB results merged and ranked

---

## Integration with Wave 4 Groups

### Mapping to Existing Groups

The Knowledge Graph capabilities integrate with existing Wave 4 groups as follows:

| Capability | Primary Group | Integration |
|------------|--------------|-------------|
| Entity Extraction | **4E** (Tree-Sitter) | AST → entities via TreeSitterManager |
| Relation Extraction | **4E** + **4L** | AST → direct edges, Docs → evidence |
| Hybrid Search | **4L** + **4M** | Bleve + HNSW + Graph coordinated |
| Domain Filtering | **4P** | DomainClassifier → query filters |
| Temporal Queries | **4C** | Bi-temporal model with CVS |
| Inference | **4J** | Materialized views, integrity |
| Learning | **4M**, **4Q**, **4R** | Weights, handoff, chunks |

### New Parallel Groups (Post-4R)

To avoid modifying completed groups, new KG capabilities are added as **4S-4W**:

```
WAVE 4 KNOWLEDGE GRAPH EXTENSION
════════════════════════════════

┌─────────────────────────────────────────────────────────────────────────┐
│ PARALLEL GROUP 4S: Entity Extraction Pipeline (EE.1-EE.12)              │
│ ** Depends on: 4E (Tree-Sitter), 4L (Bleve) **                          │
│                                                                          │
│ PHASE 1 (All parallel - types):                                         │
│ • EE.1.1 ExtractedEntity type (core/vectorgraphdb/extraction/types.go)  │
│ • EE.1.2 ExtractedRelation type                                         │
│ • EE.1.3 ExtractionResult type                                          │
│ • EE.1.4 EntityExtractor interface                                      │
│                                                                          │
│ PHASE 2 (After EE.1.x - extractors):                                    │
│ • EE.2.1 ASTExtractor (Tree-Sitter integration)                         │
│ • EE.2.2 DocExtractor (comment/doc parsing)                             │
│ • EE.2.3 HistoryExtractor (session log parsing)                         │
│                                                                          │
│ PHASE 3 (After EE.2.x - symbol resolution):                             │
│ • EE.3.1 SymbolTable (cross-file resolution)                            │
│ • EE.3.2 EntityLinker (disambiguation)                                  │
│                                                                          │
│ PHASE 4 (After EE.3.x - integration):                                   │
│ • EE.4.1 ExtractionOrchestrator (coordinates extractors)                │
│ • EE.4.2 IncrementalExtractor (file-change triggered)                   │
│                                                                          │
│ PHASE 5 (Tests):                                                        │
│ • EE.5.1 Entity extraction integration tests                            │
│                                                                          │
│ FILES:                                                                   │
│   core/vectorgraphdb/extraction/types.go                                │
│   core/vectorgraphdb/extraction/ast_extractor.go                        │
│   core/vectorgraphdb/extraction/doc_extractor.go                        │
│   core/vectorgraphdb/extraction/history_extractor.go                    │
│   core/vectorgraphdb/extraction/symbol_table.go                         │
│   core/vectorgraphdb/extraction/entity_linker.go                        │
│   core/vectorgraphdb/extraction/orchestrator.go                         │
│   core/vectorgraphdb/extraction/incremental.go                          │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│ PARALLEL GROUP 4T: Relation Extraction & Validation (RE.1-RE.10)        │
│ ** Depends on: 4S (Entity Extraction), 4L (Bleve) **                    │
│                                                                          │
│ PHASE 1 (Types):                                                        │
│ • RE.1.1 RelationPattern type                                           │
│ • RE.1.2 ValidationResult type                                          │
│ • RE.1.3 OntologyCache type                                             │
│                                                                          │
│ PHASE 2 (Extractors):                                                   │
│ • RE.2.1 StaticRelationExtractor (from AST)                             │
│ • RE.2.2 DocRelationExtractor (from text patterns)                      │
│ • RE.2.3 SemanticRelationExtractor (from embeddings)                    │
│                                                                          │
│ PHASE 3 (Validation):                                                   │
│ • RE.3.1 RelationValidator (ontology constraints)                       │
│ • RE.3.2 CardinalityChecker                                             │
│                                                                          │
│ PHASE 4 (Integration):                                                  │
│ • RE.4.1 RelationOrchestrator                                           │
│                                                                          │
│ PHASE 5 (Tests):                                                        │
│ • RE.5.1 Relation extraction tests                                      │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│ PARALLEL GROUP 4U: Hybrid Query Coordinator (HQ.1-HQ.8)                 │
│ ** Depends on: 4L (Bleve), 4M (Adaptive Retrieval) **                   │
│                                                                          │
│ PHASE 1 (Types):                                                        │
│ • HQ.1.1 HybridQuery type                                               │
│ • HQ.1.2 HybridResult type                                              │
│ • HQ.1.3 TraversalSpec type                                             │
│                                                                          │
│ PHASE 2 (Coordinator):                                                  │
│ • HQ.2.1 HybridCoordinator (parallel search)                            │
│ • HQ.2.2 CandidateAggregator (union + dedupe)                           │
│                                                                          │
│ PHASE 3 (Scoring):                                                      │
│ • HQ.3.1 HybridScorer (RRF + learned weights)                           │
│ • HQ.3.2 ExplanationBuilder                                             │
│                                                                          │
│ PHASE 4 (Integration):                                                  │
│ • HQ.4.1 QueryEngine integration                                        │
│                                                                          │
│ PHASE 5 (Tests):                                                        │
│ • HQ.5.1 Hybrid query tests                                             │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│ PARALLEL GROUP 4V: Inference Engine (IE.1-IE.10)                        │
│ ** Depends on: 4J (Shared State), 4S/4T (Extraction) **                 │
│                                                                          │
│ PHASE 1 (Schema):                                                       │
│ • IE.1.1 inference_rules table                                          │
│ • IE.1.2 inferred_edges table                                           │
│ • IE.1.3 ontology_types table                                           │
│                                                                          │
│ PHASE 2 (Types):                                                        │
│ • IE.2.1 InferenceRule type                                             │
│ • IE.2.2 InferredEdge type                                              │
│                                                                          │
│ PHASE 3 (Engine):                                                       │
│ • IE.3.1 InferenceEngine                                                │
│ • IE.3.2 RuleEvaluator                                                  │
│ • IE.3.3 BindingFinder                                                  │
│                                                                          │
│ PHASE 4 (Incremental):                                                  │
│ • IE.4.1 OnEdgeInserted handler                                         │
│ • IE.4.2 OnEdgeDeleted handler                                          │
│ • IE.4.3 RecomputeAll (nightly)                                         │
│                                                                          │
│ PHASE 5 (Tests):                                                        │
│ • IE.5.1 Inference engine tests                                         │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│ PARALLEL GROUP 4W: Temporal Graph & Bleve Sync (TG.1-TG.12)             │
│ ** Depends on: 4C (CVS), 4L (Bleve) **                                  │
│                                                                          │
│ PHASE 1 (Schema migration):                                             │
│ • TG.1.1 Add valid_from, valid_to to edges                              │
│ • TG.1.2 Add transaction_time to nodes/edges                            │
│ • TG.1.3 Add temporal indexes                                           │
│                                                                          │
│ PHASE 2 (Temporal queries):                                             │
│ • TG.2.1 TemporalGraphDB wrapper                                        │
│ • TG.2.2 AsOf queries                                                   │
│ • TG.2.3 Edge history                                                   │
│ • TG.2.4 Temporal traversal                                             │
│                                                                          │
│ PHASE 3 (Bleve sync):                                                   │
│ • TG.3.1 BleveSynchronizer                                              │
│ • TG.3.2 NodeContent index                                              │
│ • TG.3.3 EdgeMetadata index                                             │
│ • TG.3.4 EntityAlias index                                              │
│ • TG.3.5 RelationEvidence index                                         │
│                                                                          │
│ PHASE 4 (Consistency):                                                  │
│ • TG.4.1 ConsistencyVerifier                                            │
│ • TG.4.2 DriftDetector                                                  │
│ • TG.4.3 RepairOrchestrator                                             │
│                                                                          │
│ PHASE 5 (Tests):                                                        │
│ • TG.5.1 Temporal query tests                                           │
│ • TG.5.2 Bleve sync tests                                               │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│ PARALLEL GROUP 4X: Memory Decay System - ACT-R (MD.1-MD.14)             │
│ ** FROM GRAPH.md Section 10 - Memory Decay Model **                     │
│ ** REPLACES: Exponential decay with ACT-R power law **                  │
│ ** COGNITIVE SCIENCE: Based on validated human memory research **       │
│                                                                          │
│ DESIGN PRINCIPLES:                                                       │
│   1. Power law decay (d ≈ 0.5), NOT exponential                         │
│   2. ACT-R base-level activation: B = ln(Σtⱼ^(-d)) + β                  │
│   3. Access traces stored for each node/edge (bounded)                  │
│   4. Spacing effect: spaced retrieval strengthens memory                │
│   5. Decay parameter d learned per domain via Bayesian updates          │
│   6. Memory activation affects retrieval ranking                        │
│                                                                          │
│ PHASE 1 (Schema migration):                                             │
│ • MD.1.1 Add memory columns to nodes table                              │
│   - memory_activation REAL, last_accessed_at INTEGER                    │
│   - access_count INTEGER, base_offset REAL                              │
│ • MD.1.2 Add memory columns to edges table                              │
│   - Same columns as nodes                                               │
│ • MD.1.3 Create node_access_traces table                                │
│   - node_id, accessed_at, access_type, context                          │
│   - Index on (node_id, accessed_at DESC)                                │
│ • MD.1.4 Create edge_access_traces table                                │
│   - Same structure as node_access_traces                                │
│ • MD.1.5 Create decay_parameters table                                  │
│   - domain, decay_exponent_alpha/beta, base_offset_mean/variance        │
│   ACCEPTANCE: All migrations run, indexes created                       │
│   FILES: core/vectorgraphdb/migrations/010_memory_decay.go              │
│                                                                          │
│ PHASE 2 (Core types):                                                   │
│ • MD.2.1 AccessTrace type (core/knowledge/memory/actr_types.go)         │
│   - AccessedAt, AccessType, Context fields                              │
│   - AccessType enum: retrieval, reinforcement, creation, reference      │
│ • MD.2.2 ACTRMemory type (core/knowledge/memory/actr_types.go)          │
│   - NodeID, Domain, Traces []AccessTrace, MaxTraces                     │
│   - DecayAlpha/Beta (Beta distribution), BaseOffsetMean/Variance        │
│   - Activation(now) float64 implementing ACT-R equation                 │
│   - DecayMean(), DecaySample() for Thompson Sampling                    │
│   - RetrievalProbability(now, threshold) float64                        │
│   - Reinforce(now, accessType, context)                                 │
│   - UpdateDecay(ageAtRetrieval, wasUseful)                              │
│ • MD.2.3 DomainDecayParams type                                         │
│   - DecayAlpha/Beta, BaseOffsetMean/Var, EffectiveSamples               │
│   - DefaultDomainDecay(domain) with per-domain priors                   │
│   ACCEPTANCE: ACT-R equation correct, power law decay works             │
│   FILES: core/knowledge/memory/actr_types.go, actr_types_test.go        │
│                                                                          │
│ PHASE 3 (Storage):                                                      │
│ • MD.3.1 MemoryStore (core/knowledge/memory/store.go)                   │
│   - GetMemory(ctx, nodeID, domain) (*ACTRMemory, error)                 │
│   - RecordAccess(ctx, nodeID, accessType, context) error                │
│   - ComputeActivation(ctx, nodeID) (float64, error)                     │
│   - loadMemory(), saveMemory() with SQLite persistence                  │
│ • MD.3.2 Trace pruning (core/knowledge/memory/store.go)                 │
│   - Bounded trace storage (max 100 per node)                            │
│   - LRU eviction of oldest traces                                       │
│   ACCEPTANCE: Memory persists across restarts, traces bounded           │
│   FILES: core/knowledge/memory/store.go, store_test.go                  │
│                                                                          │
│ PHASE 4 (Scoring integration):                                          │
│ • MD.4.1 MemoryWeightedScorer (core/knowledge/memory/scorer.go)         │
│   - ActivationWeight, RecencyBonus, FrequencyBonus (learned)            │
│   - RetrievalThreshold (learned)                                        │
│   - ApplyMemoryWeighting(ctx, results, explore) []HybridResult          │
│   - FilterByRetrievalProbability(ctx, results, explore)                 │
│   - RecordRetrievalOutcome(ctx, nodeID, wasUseful, age)                 │
│ • MD.4.2 HybridQueryWithMemory (core/knowledge/query/memory_integration.go)│
│   - Wraps HybridQuery with memory-weighted scoring                      │
│   - ApplyMemoryWeighting, FilterByRetrieval, Explore options            │
│   ACCEPTANCE: Memory weighting affects result ranking                   │
│   FILES: core/knowledge/memory/scorer.go, scorer_test.go                │
│          core/knowledge/query/memory_integration.go                     │
│                                                                          │
│ PHASE 5 (Spacing effect):                                               │
│ • MD.5.1 SpacingAnalyzer (core/knowledge/memory/spacing.go)             │
│   - OptimalReviewTime(memory) time.Duration                             │
│   - computeStability(memory) float64                                    │
│   - Pimsleur graduated interval recall integration                      │
│   ACCEPTANCE: Spaced retrieval strengthens memory more than massed      │
│   FILES: core/knowledge/memory/spacing.go, spacing_test.go              │
│                                                                          │
│ PHASE 6 (Hooks):                                                        │
│ • MD.6.1 MemoryReinforcementHook (core/knowledge/memory/hooks.go)       │
│   - OnPostPrompt: reinforce memories referenced in response             │
│   - OnToolResult: reinforce memories retrieved by tools                 │
│   - extractReferencedNodes(), extractRetrievedNodes()                   │
│   - Priority: HookPriorityLate (run after retrieval)                    │
│   ACCEPTANCE: Hooks fire on retrieval, reinforcement recorded           │
│   FILES: core/knowledge/memory/hooks.go, hooks_test.go                  │
│                                                                          │
│ PHASE 7 (Integration tests):                                            │
│ • MD.7.1 ACT-R equation verification tests                              │
│   - Power law decay matches theoretical curve                           │
│   - Multiple traces sum correctly                                       │
│ • MD.7.2 Spacing effect tests                                           │
│   - Spaced access strengthens more than massed                          │
│ • MD.7.3 Memory-weighted retrieval tests                                │
│   - Old unused content ranks lower                                      │
│   - Recently accessed content ranks higher                              │
│ • MD.7.4 Domain-specific decay tests                                    │
│   - Academic domain decays slower than Engineer domain                  │
│   FILES: core/knowledge/memory/integration_test.go                      │
│                                                                          │
│ FILES (SUMMARY):                                                        │
│   core/vectorgraphdb/migrations/010_memory_decay.go - MD.1.x            │
│   core/knowledge/memory/actr_types.go - MD.2.x                          │
│   core/knowledge/memory/store.go - MD.3.x                               │
│   core/knowledge/memory/scorer.go - MD.4.1                              │
│   core/knowledge/query/memory_integration.go - MD.4.2                   │
│   core/knowledge/memory/spacing.go - MD.5.1                             │
│   core/knowledge/memory/hooks.go - MD.6.1                               │
│                                                                          │
│ INTERNAL DEPENDENCIES:                                                  │
│   Phase 1 (5 parallel) → Phase 2 (3 parallel) → Phase 3 (2 parallel) →  │
│   Phase 4 (2 parallel) → Phase 5 → Phase 6 → Phase 7 (4 parallel tests) │
│                                                                          │
│ EXTERNAL DEPENDENCIES:                                                  │
│   - Group 4U: Hybrid Query Coordinator (HybridQuery integration)        │
│   - Group 4Q: LearnedWeight pattern (reuse for memory weights)          │
│   - Existing VectorGraphDB SQLite infrastructure                        │
│   - Existing hook registry (core/context/hooks)                         │
│                                                                          │
│ MEMORY COST:                                                            │
│   - Per-node: ~4 KB (100 traces × 40 bytes + overhead)                  │
│   - Per-edge: ~4 KB (same)                                              │
│   - Global decay params: ~500 bytes (10 domains × 50 bytes)             │
│                                                                          │
│ CPU COST:                                                               │
│   - Activation computation: ~0.1 ms (100 trace summation)               │
│   - Memory weighting: ~1 ms per query (batch activation lookup)         │
│   - Trace insertion: ~0.05 ms (append + potential prune)                │
│                                                                          │
│ WHY ACT-R OVER EXPONENTIAL:                                             │
│   - Exponential: R = e^(-λt) → reaches 0 too quickly                    │
│   - Power Law: R = t^(-d) → matches human data, long tail               │
│   - ACT-R: B = ln(Σtⱼ^(-d)) + β → handles recency AND frequency         │
│   - Validated in 30+ years of cognitive science research                │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Edge Cases and Mitigations

### 1. Circular References in Inference

**Problem**: Rule `A→B, B→A` could create infinite loop.

**Mitigation**:
```go
// In InferenceEngine
func (e *InferenceEngine) checkRuleForEdge(ctx context.Context, rule *InferenceRule, newEdge *vgdb.GraphEdge) error {
    // Track visited nodes to detect cycles
    visited := make(map[string]bool)

    // Use depth limit for transitive rules
    maxDepth := 10

    // ... rest of implementation with cycle detection
}
```

**Acceptance**: No infinite loops, depth limited to 10 hops.

### 2. Entity Resolution Ambiguity

**Problem**: Multiple entities with same name (e.g., `Context` in different packages).

**Mitigation**:
```go
// In EntityLinker
func (l *EntityLinker) findCandidates(ctx context.Context, entity *ExtractedEntity) ([]*LinkCandidate, error) {
    // Stage 1: Prefer same-file matches
    // Stage 2: Prefer same-package matches
    // Stage 3: Use qualified names (package.Name)
    // Stage 4: Flag ambiguous if confidence gap < 5%
}
```

**Acceptance**: Same-file matches preferred, ambiguous cases flagged for review.

### 3. Bleve/VectorGraphDB Sync Drift

**Problem**: Crash between VectorGraphDB commit and Bleve index could cause inconsistency.

**Mitigation**:
```go
// Use write-ahead approach
func (db *VectorGraphDB) InsertNodeWithSync(ctx context.Context, node *GraphNode) error {
    // 1. Begin transaction
    tx, _ := db.BeginTx(ctx)

    // 2. Insert into SQLite
    if err := db.nodeStore.InsertNode(tx, node); err != nil {
        tx.Rollback()
        return err
    }

    // 3. Log to sync queue (persisted)
    if err := db.syncQueue.Log(tx, "index", "node", node.ID); err != nil {
        tx.Rollback()
        return err
    }

    // 4. Commit transaction
    if err := tx.Commit(); err != nil {
        return err
    }

    // 5. Async Bleve indexing (sync queue ensures delivery)
    db.bleveSyncer.IndexNodeAsync(node)

    return nil
}
```

**Acceptance**: Sync queue ensures at-least-once delivery to Bleve.

### 4. Large Transitive Closure

**Problem**: `TransitivelyCalls` for a frequently-called function could have millions of edges.

**Mitigation**:
```go
// In InferenceEngine
const maxInferredEdgesPerSource = 1000

func (e *InferenceEngine) insertInferredEdge(ctx context.Context, sourceID, targetID string, rule *InferenceRule) error {
    // Check current count for this source
    count, _ := e.countInferredEdges(ctx, sourceID, rule.Consequent.EdgeType)
    if count >= maxInferredEdgesPerSource {
        // Log but don't create - caller can query on-demand
        return nil
    }

    // ... insert edge
}
```

**Acceptance**: Max 1000 inferred edges per source, on-demand traversal for more.

### 5. Temporal Query Performance

**Problem**: Historical queries could be slow without proper indexing.

**Mitigation**:
```sql
-- Covering indexes for temporal queries
CREATE INDEX idx_edges_temporal ON edges(source_id, valid_from, valid_to, transaction_time);
CREATE INDEX idx_nodes_temporal ON nodes(id, valid_from, valid_to, transaction_time);

-- Partial index for current state (most common query)
CREATE INDEX idx_edges_current ON edges(source_id, target_id, edge_type)
    WHERE valid_to IS NULL;
```

**Acceptance**: Current-state queries use partial index, historical queries use temporal index.

### 6. Embedding Dimension Mismatch

**Problem**: Different embedding models produce different dimensions.

**Mitigation**:
```go
// In VectorGraphDB
type VectorMetadata struct {
    ModelID    string
    Dimensions int
    CreatedAt  time.Time
}

func (db *VectorGraphDB) InsertVector(ctx context.Context, nodeID string, embedding []float32, meta *VectorMetadata) error {
    // Verify dimensions match configured model
    if len(embedding) != db.config.EmbeddingDimensions {
        return fmt.Errorf("embedding dimension mismatch: got %d, expected %d",
            len(embedding), db.config.EmbeddingDimensions)
    }
    // ... insert
}
```

**Acceptance**: Dimension mismatch rejected at insert time.

### 7. Concurrent Extraction Race

**Problem**: Two extractors processing same file simultaneously.

**Mitigation**:
```go
// In ExtractionOrchestrator
type ExtractionOrchestrator struct {
    inProgress sync.Map // filePath -> *sync.Mutex
}

func (o *ExtractionOrchestrator) Extract(ctx context.Context, filePath string) (*ExtractionResult, error) {
    // Get or create per-file lock
    lockI, _ := o.inProgress.LoadOrStore(filePath, &sync.Mutex{})
    lock := lockI.(*sync.Mutex)

    lock.Lock()
    defer lock.Unlock()

    // ... extraction logic
}
```

**Acceptance**: Per-file locking prevents concurrent extraction of same file.

### 8. Memory Trace Overflow

**Problem**: High-frequency nodes (e.g., `main()`, common utilities) could generate excessive access traces.

**Mitigation**:
```go
// In ACTRMemory.Reinforce()
func (m *ACTRMemory) Reinforce(now time.Time, accessType AccessType, context string) {
    // Debounce: don't record traces within 1 minute of each other
    if len(m.Traces) > 0 {
        lastTrace := m.Traces[len(m.Traces)-1]
        if now.Sub(lastTrace.AccessedAt) < time.Minute {
            return  // Skip - too recent
        }
    }

    m.Traces = append(m.Traces, AccessTrace{...})
    m.AccessCount++

    // Bounded history - keep most recent
    if len(m.Traces) > m.MaxTraces {
        m.Traces = m.Traces[len(m.Traces)-m.MaxTraces:]
    }
}
```

**Acceptance**: Max 100 traces per node, debounced at 1-minute intervals.

### 9. Cold Start Memory Activation

**Problem**: New nodes have no traces, causing undefined or very low activation.

**Mitigation**:
```go
// In ACTRMemory.Activation()
func (m *ACTRMemory) Activation(now time.Time) float64 {
    if len(m.Traces) == 0 {
        // Cold start: return base offset (prior mean)
        // This ensures new content has reasonable activation
        return m.BaseOffsetMean
    }

    // ... normal ACT-R calculation
}

// On creation, add initial trace
func NewACTRMemory(nodeID string, domain Domain) *ACTRMemory {
    m := &ACTRMemory{...}
    m.Reinforce(time.Now(), AccessCreation, "initial")
    return m
}
```

**Acceptance**: New nodes have reasonable activation, not -∞ or undefined.

### 10. Decay Parameter Drift

**Problem**: Decay parameter d could drift to extreme values (0 or 1) over time.

**Mitigation**:
```go
// In ACTRMemory.UpdateDecay()
func (m *ACTRMemory) UpdateDecay(ageAtRetrieval time.Duration, wasUseful bool) {
    // ... update logic

    // Regularization: apply decay to effective samples
    decayFactor := 0.99
    m.DecayAlpha *= decayFactor
    m.DecayBeta *= decayFactor

    // Hard bounds: d must stay in [0.1, 0.9] range
    minAlpha, minBeta := 1.0, 1.0
    m.DecayAlpha = math.Max(m.DecayAlpha, minAlpha)
    m.DecayBeta = math.Max(m.DecayBeta, minBeta)

    // Prevent extreme posterior means
    mean := m.DecayAlpha / (m.DecayAlpha + m.DecayBeta)
    if mean < 0.1 || mean > 0.9 {
        // Reset to prior
        m.DecayAlpha = 5.0
        m.DecayBeta = 5.0
    }
}
```

**Acceptance**: Decay parameter stays in valid range, no extreme drift.

---

## Implementation Phases

### Phase 1: Foundation (Weeks 1-2)
- Schema migration (temporal columns, ontology tables)
- Basic Bleve integration (node content index)
- Entity extraction types and interfaces

### Phase 2: Extraction (Weeks 3-4)
- AST extractor (Go, TypeScript)
- Symbol table and entity linker
- Relation validator

### Phase 3: Query (Weeks 5-6)
- Hybrid query coordinator
- Bleve synchronization (all indexes)
- Temporal query support

### Phase 4: Inference (Weeks 7-8)
- Inference engine
- Rule evaluation and binding
- Incremental updates

### Phase 5: Integration (Weeks 9-10)
- Integration with existing agents
- Performance optimization
- Comprehensive testing

---

## References

### Internal Documentation
- **ARCHITECTURE.md**: Overall system architecture
- **MEMORY.md**: Learned parameter system
- **SCORING.md**: Quality scoring
- **CONTEXT.md**: Adaptive retrieval
- **HANDOFF.md**: GP-based handoff
- **CHUNKING.md**: Chunk parameter learning
- **TODO.md**: Wave 4 implementation plan

### Cognitive Science (Memory Decay Model)
- **Anderson, J. R. (1990)**. *The Adaptive Character of Thought*. Lawrence Erlbaum Associates.
- **Anderson, J. R., & Schooler, L. J. (1991)**. Reflections of the environment in memory. *Psychological Science*, 2(6), 396-408.
- **Wixted, J. T., & Ebbesen, E. B. (1991)**. On the form of forgetting. *Psychological Science*, 2(6), 409-415.
- **Wixted, J. T. (2004)**. The psychology and neuroscience of forgetting. *Annual Review of Psychology*, 55, 235-269.
- **ACT-R Cognitive Architecture**: http://act-r.psy.cmu.edu/

---

## Product Quantization Upgrade: Residual PQ + Re-ranking + Data-Adaptive Optimization

### Overview

The Product Quantization system has been upgraded from a basic hardcoded implementation to a **maximally robust, correct, and performant** solution with no hardcoded parameters. All configuration values are derived from data characteristics using statistical analysis.

### Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        Adaptive Quantization System                          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                      AdaptiveQuantizer                                 │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐  │  │
│  │  │  Strategy Selection (derived from dataset size)                  │  │  │
│  │  │  • Tiny (<100): StrategyExact (no compression)                   │  │  │
│  │  │  • Small (100-1000): StrategyCoarsePQRerank (stored vectors)     │  │  │
│  │  │  • Medium (1K-100K): StrategyStandardPQ or StrategyResidualPQ    │  │  │
│  │  │  • Large (>100K): StrategyResidualPQ (two-stage encoding)        │  │  │
│  │  └─────────────────────────────────────────────────────────────────┘  │  │
│  │                                                                        │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐  │  │
│  │  │  Residual Product Quantization                                   │  │  │
│  │  │  Stage 1: Primary PQ → captures main structure                   │  │  │
│  │  │  Stage 2: Residual PQ → captures error (original - reconstructed)│  │  │
│  │  │  Result: 37-40% reduction in reconstruction error                │  │  │
│  │  └─────────────────────────────────────────────────────────────────┘  │  │
│  │                                                                        │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐  │  │
│  │  │  Re-ranking System                                               │  │  │
│  │  │  • Retrieve 10x candidates with approximate PQ distance          │  │  │
│  │  │  • Re-rank with exact L2 distance                                │  │  │
│  │  │  • Achieves 95-100% recall vs 40-60% without re-ranking          │  │  │
│  │  └─────────────────────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     CentroidOptimizer                                  │  │
│  │  Data-adaptive centroid count derivation (NO HARDCODING)              │  │
│  │                                                                        │  │
│  │  Statistical Analysis Methods:                                        │  │
│  │  ├── Variance-based allocation (K ∝ √variance per subspace)          │  │
│  │  ├── Rate-distortion bounds (information theory)                      │  │
│  │  ├── Intrinsic dimensionality (PCA eigenvalue analysis)              │  │
│  │  ├── Clustering strength (Hopkins statistic-like)                     │  │
│  │  ├── Data complexity scoring                                          │  │
│  │  └── Cross-validation (optional empirical K selection)                │  │
│  │                                                                        │  │
│  │  Constraints Applied:                                                  │  │
│  │  • min(K) = power of 2 ≥ subspaceDim                                  │  │
│  │  • max(K) = 256 (uint8 encoding limit)                                │  │
│  │  • Data limit: K ≤ n / max(10, subspaceDim)                          │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                     BLAS-Optimized K-Means                             │  │
│  │  High-performance training using gonum/blas                           │  │
│  │                                                                        │  │
│  │  Optimizations:                                                       │  │
│  │  • blas64.Gemm: Batch dot products (N×K matrix multiply)             │  │
│  │  • blas64.Ddot: Vectorized norm computation                          │  │
│  │  • blas64.Axpy + Scal: Centroid updates                              │  │
│  │  • Parallel restarts (up to NumCPU)                                   │  │
│  │  • Sequential path with memory reuse (GOMAXPROCS=1)                   │  │
│  │                                                                        │  │
│  │  Encoding Optimizations:                                              │  │
│  │  • blas32.Gemv: Single-vector encoding                                │  │
│  │  • blas32.Gemm: Batch encoding with chunked parallelism              │  │
│  │  • Cached flat centroid layout + precomputed norms                    │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Performance Results

| Strategy | Recall@10 | Notes |
|----------|-----------|-------|
| Standard PQ (baseline) | ~37-40% | Fundamentally limited without re-ranking |
| Residual PQ | ~60-65% | Two-stage encoding captures more detail |
| Residual PQ + Rerank(5x) | ~98-99% | 5x candidates, exact re-ranking |
| **Residual PQ + Rerank(10x)** | **100%** | 10x candidates, exact re-ranking |
| **Coarse PQ + Rerank** | **100%** | Stores original vectors, always perfect |

**Recall vs. Candidate Multiplier:**

| Multiplier | Recall@10 |
|------------|-----------|
| 1x | 61% |
| 2x | 82% |
| 5x | 99% |
| 10x | 100% |
| 20x | 100% |

**Quantization Error Reduction:**
- Standard PQ RMSE: 0.122
- Residual PQ RMSE: 0.076
- **Error reduction: 37.59%**

### Implementation Files

```
core/vectorgraphdb/quantization/
├── adaptive_quantizer.go      # Main adaptive system with strategy selection
├── centroid_optimizer.go      # Data-adaptive K derivation (no hardcoding)
├── product_quantizer.go       # Core PQ with BLAS optimization
├── kmeans_optimal.go          # BLAS-optimized k-means clustering
├── opq.go                     # Optimized PQ with learned rotation
├── opq_test.go                # OPQ tests
├── adaptive_quantizer_test.go # Comprehensive recall tests
└── quantized_hnsw.go          # HNSW integration with PQ
```

### AdaptiveQuantizer API

```go
// core/vectorgraphdb/quantization/adaptive_quantizer.go

package quantization

// QuantizationStrategy defines the compression approach
type QuantizationStrategy int

const (
    StrategyExact          QuantizationStrategy = iota // No compression
    StrategyCoarsePQRerank                             // PQ + stored vectors
    StrategyStandardPQ                                 // Standard PQ only
    StrategyResidualPQ                                 // Two-stage residual PQ
)

// AdaptiveQuantizerConfig - all defaults work, all parameters derived from data
type AdaptiveQuantizerConfig struct {
    ForceStrategy       QuantizationStrategy // -1 = auto (default)
    TargetRecall        float64              // 0.9 = 90% target (default)
    MaxRerankCandidates int                  // 0 = auto (10x k)
    EnableResidual      bool                 // true = use residual PQ (default)
}

// AdaptiveQuantizer automatically selects optimal quantization
type AdaptiveQuantizer struct {
    strategy      QuantizationStrategy
    primaryPQ     *ProductQuantizer
    residualPQ    *ProductQuantizer      // For StrategyResidualPQ
    storedVectors [][]float32            // For StrategyCoarsePQRerank
    codes         []PQCode
    residualCodes []PQCode
    vectorDim     int
    trained       bool
    config        AdaptiveQuantizerConfig
}

// NewAdaptiveQuantizer creates with default config (all parameters derived)
func NewAdaptiveQuantizer(vectorDim int, config AdaptiveQuantizerConfig) (*AdaptiveQuantizer, error)

// Train analyzes data and selects optimal strategy automatically
func (aq *AdaptiveQuantizer) Train(ctx context.Context, vectors [][]float32) error

// Search returns k nearest neighbors with automatic re-ranking
func (aq *AdaptiveQuantizer) Search(query []float32, k int) ([]SearchResult, error)

// SearchWithRerank forces re-ranking with specified candidate count
func (aq *AdaptiveQuantizer) SearchWithRerank(query []float32, k, numCandidates int) ([]SearchResult, error)

// Strategy returns the automatically selected strategy
func (aq *AdaptiveQuantizer) Strategy() QuantizationStrategy

// Stats returns compression and configuration statistics
func (aq *AdaptiveQuantizer) Stats() AdaptiveQuantizerStats
```

### CentroidOptimizer API

```go
// core/vectorgraphdb/quantization/centroid_optimizer.go

package quantization

// CentroidOptimizerConfig - all parameters derived if not specified
type CentroidOptimizerConfig struct {
    TargetRecall               float64 // 0.95 default
    TargetDistortion           float64 // 0 = auto-derive
    MinCentroidsPerSubspace    int     // 0 = auto-derive
    MaxCentroidsPerSubspace    int     // 256 default (uint8 max)
    EnableCrossValidation      bool    // false default (faster)
    CrossValidationSampleRatio float64 // 0.1 default
    NumSubspaces               int     // Set from data
}

// CentroidOptimizer derives optimal K from statistical analysis
type CentroidOptimizer struct {
    config             CentroidOptimizerConfig
    globalVariance     float64   // Total data variance
    subspaceVariances  []float64 // Per-subspace variance
    eigenvalues        []float64 // PCA eigenvalues
    intrinsicDim       float64   // Estimated intrinsic dimensionality
    dataComplexity     float64   // Complexity score [0,1]
    clusteringStrength float64   // How clustered [0,1]
}

// OptimizeCentroidCount analyzes data and returns optimal K
// Combines 6 derivation methods and returns maximum (most conservative)
func (co *CentroidOptimizer) OptimizeCentroidCount(vectors [][]float32, numSubspaces int) (int, error)

// Methods used internally:
// - computeStatisticalMinimum: K from k-means stability requirements
// - computeVarianceBasedK: K ∝ √variance (rate-distortion approximation)
// - computeRateDistortionK: K from information theory bounds
// - computeIntrinsicDimensionalityK: K scaled by effective data complexity
// - computeRecallBasedK: K needed for target recall
// - computeComplexityBasedK: K scaled by data structure complexity

// GetStatistics returns computed data statistics
func (co *CentroidOptimizer) GetStatistics() CentroidOptimizerStats
```

### Residual PQ Algorithm

```go
// Residual PQ Training:
func (aq *AdaptiveQuantizer) trainResidualPQ(ctx context.Context, vectors [][]float32) error {
    // Stage 1: Train primary (coarse) quantizer
    primaryConfig := aq.derivePQConfigFromData(vectors, false)
    aq.primaryPQ.Train(ctx, vectors)
    
    // Encode with primary
    aq.codes = aq.primaryPQ.EncodeBatch(vectors)
    
    // Compute residuals: residual = original - reconstructed
    residuals := make([][]float32, len(vectors))
    for i, v := range vectors {
        reconstructed := aq.reconstructVector(aq.codes[i])
        for j := range v {
            residuals[i][j] = v[j] - reconstructed[j]
        }
    }
    
    // Stage 2: Train residual quantizer on error
    residualConfig := aq.derivePQConfigFromData(residuals, true)
    aq.residualPQ.Train(ctx, residuals)
    aq.residualCodes = aq.residualPQ.EncodeBatch(residuals)
}

// Residual PQ Search with Re-ranking:
func (aq *AdaptiveQuantizer) searchResidualPQ(query []float32, k int) ([]SearchResult, error) {
    // Stage 1: Get candidates using primary PQ distance (fast)
    primaryTable := aq.primaryPQ.ComputeDistanceTable(query)
    numCandidates := k * 10
    
    candidates := make([]SearchResult, n)
    for i := 0; i < n; i++ {
        candidates[i] = SearchResult{
            Index:    i,
            Distance: aq.primaryPQ.AsymmetricDistance(primaryTable, aq.codes[i]),
        }
    }
    
    // Sort and take top candidates
    sort.Slice(candidates, ...) // By approximate distance
    candidates = candidates[:numCandidates]
    
    // Stage 2: Re-rank with full reconstruction (accurate)
    for i := range candidates {
        idx := candidates[i].Index
        reconstructed := aq.reconstructVectorWithResidual(
            aq.codes[idx], 
            aq.residualCodes[idx],
        )
        candidates[i].Distance = squaredL2Distance(query, reconstructed)
    }
    
    // Final sort by exact distance
    sort.Slice(candidates, ...) // By exact distance
    return candidates[:k]
}
```

### BLAS-Optimized K-Means

```go
// core/vectorgraphdb/quantization/kmeans_optimal.go

// Key optimizations:

// 1. Batch dot products using GEMM (N×K matrix multiply)
// Instead of O(N×K×D) loop-based, use O(1) BLAS call
func computeAllDotProducts(vectors, centroids []float64) []float64 {
    // dots = vectors × centroids^T
    blas64.Gemm(blas.NoTrans, blas.Trans, 1.0,
        blas64.General{Rows: n, Cols: dim, Data: vectors},
        blas64.General{Rows: k, Cols: dim, Data: centroids},
        0.0,
        blas64.General{Rows: n, Cols: k, Data: dots})
    return dots
}

// 2. Precomputed norms for distance calculation
// ||x - c||² = ||x||² + ||c||² - 2(x·c)
// Precompute ||c||² for all centroids once per iteration

// 3. Parallel restarts with goroutines
func kmeansParallelRestarts(vectors [][]float32, k int, config KMeansConfig) [][]float32 {
    numRestarts := config.NumRestarts
    results := make(chan restartResult, numRestarts)
    
    for r := 0; r < numRestarts; r++ {
        go func(seed int64) {
            centroids, objective := runSingleKMeans(vectors, k, config, seed)
            results <- restartResult{centroids, objective}
        }(baseSeed + int64(r))
    }
    
    // Return centroids with lowest objective
}

// 4. Sequential path with memory reuse (GOMAXPROCS=1)
func kmeansSequentialRestarts(vectors [][]float32, k int, config KMeansConfig) [][]float32 {
    state := newKMeansState(vectors, k) // Allocate once
    
    for restart := 0; restart < config.NumRestarts; restart++ {
        state.resetForRestart() // Reuse memory
        objective := state.runSingleKMeans(config, rng)
        if objective < bestObjective {
            copy(bestCentroids, state.centroids)
        }
    }
}
```

### BLAS-Optimized Encoding

```go
// core/vectorgraphdb/quantization/product_quantizer.go

// Cached centroid layout for BLAS operations
type ProductQuantizer struct {
    centroids      [][][]float32  // [numSubspaces][K][subspaceDim]
    centroidsFlat  [][]float32    // [numSubspaces][K × subspaceDim] - contiguous
    centroidNorms  [][]float32    // [numSubspaces][K] - precomputed ||c||²
}

// buildEncodingCache prepares flat layout for BLAS
func (pq *ProductQuantizer) buildEncodingCache() {
    for m := 0; m < pq.numSubspaces; m++ {
        // Flatten centroids: [K × subspaceDim] contiguous
        pq.centroidsFlat[m] = make([]float32, K * subspaceDim)
        for c := 0; c < K; c++ {
            copy(pq.centroidsFlat[m][c*subspaceDim:], pq.centroids[m][c])
        }
        
        // Precompute norms: ||c||² for each centroid
        pq.centroidNorms[m] = make([]float32, K)
        for c := 0; c < K; c++ {
            pq.centroidNorms[m][c] = dotProduct(pq.centroids[m][c], pq.centroids[m][c])
        }
    }
}

// Encode uses BLAS for all K dot products at once
func (pq *ProductQuantizer) Encode(vector []float32) (PQCode, error) {
    code := make(PQCode, pq.numSubspaces)
    
    for m := 0; m < pq.numSubspaces; m++ {
        subvector := vector[m*subspaceDim : (m+1)*subspaceDim]
        
        // All K dot products with single GEMV call
        dots := make([]float32, K)
        blas32.Gemv(blas.NoTrans, 1.0,
            blas32.General{Rows: K, Cols: subspaceDim, Data: pq.centroidsFlat[m]},
            blas32.Vector{N: subspaceDim, Data: subvector},
            0.0,
            blas32.Vector{N: K, Data: dots})
        
        // dist² = ||x||² + ||c||² - 2(x·c)
        xNorm := dotProduct(subvector, subvector)
        minDist, minIdx := float32(math.MaxFloat32), 0
        for c := 0; c < K; c++ {
            dist := xNorm + pq.centroidNorms[m][c] - 2*dots[c]
            if dist < minDist {
                minDist, minIdx = dist, c
            }
        }
        code[m] = uint8(minIdx)
    }
    return code, nil
}

// EncodeBatch uses GEMM for N×K dot products
func (pq *ProductQuantizer) EncodeBatch(vectors [][]float32) ([]PQCode, error) {
    // Chunk processing with parallelism
    // For each chunk: GEMM for all (chunk_size × K) dot products
}
```

### OPQ (Optimized Product Quantization)

```go
// core/vectorgraphdb/quantization/opq.go

// OptimizedProductQuantizer learns rotation matrix to minimize quantization error
type OptimizedProductQuantizer struct {
    pq            *ProductQuantizer
    rotation      []float64 // [dim × dim] rotation matrix R
    rotationT     []float64 // R^T for inverse
    vectorDim     int
    trained       bool
    opqIterations int
}

// Train alternates between:
// 1. Fix R, train PQ on rotated vectors
// 2. Fix PQ, solve Procrustes for optimal R
func (opq *OptimizedProductQuantizer) Train(ctx context.Context, vectors [][]float32) error {
    // Initialize R as identity
    R := identityMatrix(dim)
    
    for iter := 0; iter < opq.opqIterations; iter++ {
        // Step 1: Rotate vectors
        XR := X @ R^T  // Using BLAS GEMM
        
        // Step 2: Train PQ on rotated vectors
        pq.Train(XR)
        
        // Step 3: Reconstruct from PQ codes
        reconstructed := decode(pq.Encode(XR))
        
        // Step 4: Procrustes: minimize ||X @ R^T - reconstructed||²
        // Solution: R = V @ U^T where reconstructed^T @ X = U @ S @ V^T (SVD)
        M := reconstructed^T @ X
        U, S, VT := SVD(M)
        R = V @ U^T
    }
}

// Encode: rotate then PQ encode
func (opq *OptimizedProductQuantizer) Encode(vector []float32) (PQCode, error) {
    rotated := R @ vector  // Apply learned rotation
    return opq.pq.Encode(rotated)
}
```

### Configuration Derivation (No Hardcoding)

All parameters are derived from data characteristics:

```go
// DeriveTrainConfig computes k-means training parameters
func DeriveTrainConfig(centroidsPerSubspace, subspaceDim int) TrainConfig {
    // MaxIterations: sqrt(K) * 5, capped at 50
    maxIter := int(math.Sqrt(float64(centroidsPerSubspace))) * 5
    
    // ConvergenceThreshold: scales with dimension
    threshold := 1e-6 * float64(subspaceDim)
    
    // NumRestarts: more for harder problems
    restarts := max(3, int(math.Log2(float64(centroidsPerSubspace))))
    
    // MinSamplesRatio: statistical requirement for stable means
    minSamples := max(10, subspaceDim)
    
    return TrainConfig{
        MaxIterations:        maxIter,
        ConvergenceThreshold: threshold,
        NumRestarts:          restarts,
        MinSamplesRatio:      float32(minSamples),
    }
}

// DeriveOPQConfig computes OPQ parameters from data
func DeriveOPQConfig(vectorDim, numVectors, targetBytesPerVector int) OPQConfig {
    // NumSubspaces: target 8-16 dim per subspace
    numSubspaces := deriveNumSubspaces(vectorDim)
    
    // Centroids: max that data can support
    subspaceDim := vectorDim / numSubspaces
    minSamples := max(10, subspaceDim)
    maxCentroids := numVectors / minSamples
    centroidsPerSubspace := nearestPowerOf2(min(maxCentroids, 256))
    
    // OPQ iterations: more for high dimensions
    opqIterations := 10
    if vectorDim >= 256 { opqIterations = 15 }
    if vectorDim >= 512 { opqIterations = 20 }
    
    return OPQConfig{...}
}
```

### Acceptance Criteria (Updated)

**ACCEPTANCE CRITERIA - Adaptive Product Quantization:**
- [x] Compression ratio: ≥30x (96 bytes vs 3072 bytes for 768-dim)
- [x] Recall@10 with Rerank(10x): ≥95% (achieved 100%)
- [x] Recall@10 without Rerank: ≥30% baseline (achieved 37-40%)
- [x] Residual PQ error reduction: ≥30% vs standard PQ (achieved 37.59%)
- [x] No hardcoded parameters - all derived from data
- [x] Automatic strategy selection based on dataset size
- [x] BLAS-optimized training (25x speedup)
- [x] Cross-validation option for empirical K selection
- [x] Centroids persist and reload correctly
- [x] Memory usage matches theoretical compression ratio

**Test Results Summary:**
```
=== TestAdaptiveQuantizer_RecallComparison ===
Standard PQ Recall@10: 37.30%
Residual PQ Recall@10: 60.90%
Residual PQ + Rerank(10x) Recall@10: 100.00%
Coarse PQ + Rerank Recall@10: 100.00%
PASS

=== TestAdaptiveQuantizer_RecallVsCandidates ===
Residual PQ + Rerank(1x) Recall@10: 61.00%
Residual PQ + Rerank(2x) Recall@10: 82.40%
Residual PQ + Rerank(5x) Recall@10: 98.60%
Residual PQ + Rerank(10x) Recall@10: 100.00%
PASS

=== TestResidualPQ_QuantizationError ===
Standard PQ RMSE: 0.122456
Residual PQ RMSE: 0.076424
Error reduction: 37.59%
PASS
```

