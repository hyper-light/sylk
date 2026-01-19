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
10. [Domain Partitioning](#domain-partitioning)
11. [Multi-Session Coordination](#multi-session-coordination)
12. [Integration with Wave 4 Groups](#integration-with-wave-4-groups)
13. [Implementation Phases](#implementation-phases)
14. [Acceptance Criteria](#acceptance-criteria)
15. [Edge Cases and Mitigations](#edge-cases-and-mitigations)

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
6. **Inference is materialized**: Pre-compute transitive closures, update incrementally
7. **All parameters are learned**: Traversal depth, edge weights, scoring - Bayesian posteriors

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
-- INDEXES FOR KNOWLEDGE GRAPH QUERIES
-- ============================================================================

-- Temporal queries
CREATE INDEX IF NOT EXISTS idx_edges_valid_time ON edges(valid_from, valid_to);
CREATE INDEX IF NOT EXISTS idx_edges_transaction_time ON edges(transaction_time);
CREATE INDEX IF NOT EXISTS idx_nodes_valid_time ON nodes(valid_from, valid_to);

-- Type hierarchy queries
CREATE INDEX IF NOT EXISTS idx_ontology_parent ON ontology_types(parent_type_id);

-- Inference
CREATE INDEX IF NOT EXISTS idx_inferred_edges_source ON inferred_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_inferred_edges_target ON inferred_edges(target_id);
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

The Inference Engine pre-computes derived edges (transitive closures, implied relations) for fast query-time access.

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          INFERENCE ENGINE                                │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌──────────────────┐         ┌──────────────────┐                      │
│  │  Inference Rules │         │ Materialized View│                      │
│  │  (inference_rules│────────►│ (inferred_edges) │                      │
│  │   table)         │         │                  │                      │
│  └──────────────────┘         └────────┬─────────┘                      │
│                                        │                                │
│                               ┌────────▼─────────┐                      │
│                               │Incremental Update│                      │
│                               │ (on edge change) │                      │
│                               └──────────────────┘                      │
│                                                                          │
│  Triggers:                                                               │
│  - New edge inserted → check rules → add inferred edges                 │
│  - Edge deleted → invalidate dependent inferred edges                   │
│  - Periodic full recomputation (nightly)                                │
└─────────────────────────────────────────────────────────────────────────┘
```

### Implementation

```go
// core/vectorgraphdb/inference/engine.go

package inference

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"

    vgdb "sylk/core/vectorgraphdb"
)

// InferenceEngine computes and maintains derived edges
type InferenceEngine struct {
    db    *vgdb.VectorGraphDB
    rules []*InferenceRule
}

// InferenceRule represents a Horn clause rule
type InferenceRule struct {
    RuleID      int64
    RuleName    string
    Antecedent  []RuleCondition  // Conditions that must be true
    Consequent  RuleConsequent   // Edge to create if conditions met
    Confidence  float64
    IsEnabled   bool
    Description string
}

// RuleCondition is one edge pattern to match
type RuleCondition struct {
    EdgeType vgdb.EdgeType
    Source   string  // Variable name (e.g., "A", "B")
    Target   string  // Variable name
}

// RuleConsequent is the edge to create
type RuleConsequent struct {
    EdgeType vgdb.EdgeType
    Source   string  // Variable name
    Target   string  // Variable name
}

// InferredEdge is a derived edge with provenance
type InferredEdge struct {
    ID         int64
    SourceID   string
    TargetID   string
    EdgeType   vgdb.EdgeType
    RuleID     int64
    Confidence float64
    Evidence   []string  // IDs of edges that produced this
    ComputedAt time.Time
    Valid      bool
}

func NewInferenceEngine(db *vgdb.VectorGraphDB) (*InferenceEngine, error) {
    engine := &InferenceEngine{db: db}

    // Load rules from database
    if err := engine.loadRules(); err != nil {
        return nil, err
    }

    return engine, nil
}

func (e *InferenceEngine) loadRules() error {
    rows, err := e.db.Query(`
        SELECT rule_id, rule_name, antecedent, consequent, confidence, is_enabled, description
        FROM inference_rules
        WHERE is_enabled = 1
    `)
    if err != nil {
        return err
    }
    defer rows.Close()

    e.rules = nil
    for rows.Next() {
        var rule InferenceRule
        var antecedentJSON, consequentJSON string

        if err := rows.Scan(&rule.RuleID, &rule.RuleName, &antecedentJSON,
            &consequentJSON, &rule.Confidence, &rule.IsEnabled, &rule.Description); err != nil {
            return err
        }

        if err := json.Unmarshal([]byte(antecedentJSON), &rule.Antecedent); err != nil {
            return fmt.Errorf("parse antecedent for %s: %w", rule.RuleName, err)
        }
        if err := json.Unmarshal([]byte(consequentJSON), &rule.Consequent); err != nil {
            return fmt.Errorf("parse consequent for %s: %w", rule.RuleName, err)
        }

        e.rules = append(e.rules, &rule)
    }

    return nil
}

// OnEdgeInserted is called when a new edge is added to trigger incremental inference
func (e *InferenceEngine) OnEdgeInserted(ctx context.Context, edge *vgdb.GraphEdge) error {
    // For each rule, check if this edge completes any patterns
    for _, rule := range e.rules {
        if err := e.checkRuleForEdge(ctx, rule, edge); err != nil {
            // Log but don't fail - inference is best-effort
            continue
        }
    }

    return nil
}

// checkRuleForEdge checks if inserting this edge triggers a rule
func (e *InferenceEngine) checkRuleForEdge(ctx context.Context, rule *InferenceRule, newEdge *vgdb.GraphEdge) error {
    // Find which condition(s) this edge might satisfy
    for i, condition := range rule.Antecedent {
        if condition.EdgeType != newEdge.EdgeType {
            continue
        }

        // This edge matches condition i - find bindings for other conditions
        bindings := make(map[string]string)
        bindings[condition.Source] = newEdge.SourceID
        bindings[condition.Target] = newEdge.TargetID

        // Try to satisfy remaining conditions
        otherConditions := make([]RuleCondition, 0, len(rule.Antecedent)-1)
        for j, c := range rule.Antecedent {
            if j != i {
                otherConditions = append(otherConditions, c)
            }
        }

        // Find all valid binding completions
        completions := e.findBindingCompletions(ctx, bindings, otherConditions)

        // For each valid completion, create inferred edge
        for _, completion := range completions {
            sourceID := completion[rule.Consequent.Source]
            targetID := completion[rule.Consequent.Target]

            // Check if inferred edge already exists
            exists, err := e.inferredEdgeExists(ctx, sourceID, targetID, rule.Consequent.EdgeType)
            if err != nil || exists {
                continue
            }

            // Create inferred edge
            evidence := e.collectEvidence(newEdge, otherConditions, completion)
            if err := e.insertInferredEdge(ctx, sourceID, targetID, rule, evidence); err != nil {
                continue
            }
        }
    }

    return nil
}

// findBindingCompletions finds all ways to satisfy remaining conditions given initial bindings
func (e *InferenceEngine) findBindingCompletions(ctx context.Context, bindings map[string]string,
                                                   conditions []RuleCondition) []map[string]string {
    if len(conditions) == 0 {
        // All conditions satisfied - return current bindings
        return []map[string]string{copyBindings(bindings)}
    }

    condition := conditions[0]
    remaining := conditions[1:]

    var completions []map[string]string

    // Determine what we know about source and target
    sourceKnown := bindings[condition.Source] != ""
    targetKnown := bindings[condition.Target] != ""

    if sourceKnown && targetKnown {
        // Both bound - check if edge exists
        exists, _ := e.db.EdgeExists(ctx, bindings[condition.Source],
            bindings[condition.Target], condition.EdgeType)
        if exists {
            completions = append(completions, e.findBindingCompletions(ctx, bindings, remaining)...)
        }
    } else if sourceKnown {
        // Source bound - find all targets
        targets, _ := e.db.GetEdgeTargets(ctx, bindings[condition.Source], condition.EdgeType)
        for _, targetID := range targets {
            newBindings := copyBindings(bindings)
            newBindings[condition.Target] = targetID
            completions = append(completions, e.findBindingCompletions(ctx, newBindings, remaining)...)
        }
    } else if targetKnown {
        // Target bound - find all sources
        sources, _ := e.db.GetEdgeSources(ctx, bindings[condition.Target], condition.EdgeType)
        for _, sourceID := range sources {
            newBindings := copyBindings(bindings)
            newBindings[condition.Source] = sourceID
            completions = append(completions, e.findBindingCompletions(ctx, newBindings, remaining)...)
        }
    } else {
        // Neither bound - find all edges of this type (expensive, limit)
        edges, _ := e.db.GetEdgesByType(ctx, condition.EdgeType, 1000)
        for _, edge := range edges {
            newBindings := copyBindings(bindings)
            newBindings[condition.Source] = edge.SourceID
            newBindings[condition.Target] = edge.TargetID
            completions = append(completions, e.findBindingCompletions(ctx, newBindings, remaining)...)
        }
    }

    return completions
}

func (e *InferenceEngine) insertInferredEdge(ctx context.Context, sourceID, targetID string,
                                               rule *InferenceRule, evidence []string) error {
    evidenceJSON, _ := json.Marshal(evidence)

    _, err := e.db.Exec(`
        INSERT OR IGNORE INTO inferred_edges
        (source_id, target_id, edge_type, rule_id, confidence, evidence, computed_at, valid)
        VALUES (?, ?, ?, ?, ?, ?, datetime('now'), 1)
    `, sourceID, targetID, rule.Consequent.EdgeType, rule.RuleID, rule.Confidence, string(evidenceJSON))

    return err
}

// OnEdgeDeleted is called when an edge is removed to invalidate dependent inferences
func (e *InferenceEngine) OnEdgeDeleted(ctx context.Context, edge *vgdb.GraphEdge) error {
    edgeKey := fmt.Sprintf("%d", edge.ID)

    // Find inferred edges that depend on this edge
    _, err := e.db.Exec(`
        UPDATE inferred_edges
        SET valid = 0
        WHERE evidence LIKE ?
    `, "%"+edgeKey+"%")

    return err
}

// RecomputeAll performs full recomputation of all inferred edges
func (e *InferenceEngine) RecomputeAll(ctx context.Context) error {
    // Invalidate all existing inferred edges
    if _, err := e.db.Exec(`UPDATE inferred_edges SET valid = 0`); err != nil {
        return err
    }

    // For each rule, compute all valid inferences
    for _, rule := range e.rules {
        if err := e.computeRuleFull(ctx, rule); err != nil {
            // Log but continue with other rules
            continue
        }
    }

    // Clean up invalid edges older than 24 hours
    _, _ = e.db.Exec(`
        DELETE FROM inferred_edges
        WHERE valid = 0 AND computed_at < datetime('now', '-24 hours')
    `)

    return nil
}

// GetInferredEdges returns inferred edges for a node
func (e *InferenceEngine) GetInferredEdges(ctx context.Context, nodeID string,
                                            direction vgdb.TraversalDirection) ([]*InferredEdge, error) {
    var query string
    switch direction {
    case vgdb.DirectionOutgoing:
        query = `SELECT * FROM inferred_edges WHERE source_id = ? AND valid = 1`
    case vgdb.DirectionIncoming:
        query = `SELECT * FROM inferred_edges WHERE target_id = ? AND valid = 1`
    case vgdb.DirectionBoth:
        query = `SELECT * FROM inferred_edges WHERE (source_id = ? OR target_id = ?) AND valid = 1`
    }

    rows, err := e.db.Query(query, nodeID, nodeID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var edges []*InferredEdge
    for rows.Next() {
        var edge InferredEdge
        var evidenceJSON string

        if err := rows.Scan(&edge.ID, &edge.SourceID, &edge.TargetID, &edge.EdgeType,
            &edge.RuleID, &edge.Confidence, &evidenceJSON, &edge.ComputedAt, &edge.Valid); err != nil {
            continue
        }

        json.Unmarshal([]byte(evidenceJSON), &edge.Evidence)
        edges = append(edges, &edge)
    }

    return edges, nil
}

func copyBindings(b map[string]string) map[string]string {
    c := make(map[string]string, len(b))
    for k, v := range b {
        c[k] = v
    }
    return c
}
```

**ACCEPTANCE CRITERIA - Inference Engine:**
- [ ] Transitive calls computed correctly (A→B→C implies A→→C)
- [ ] Transitive imports computed correctly
- [ ] Implements-method inference works (S implements I, I has M → S must have M)
- [ ] Incremental update adds new inferences in <10ms
- [ ] Edge deletion invalidates dependent inferences
- [ ] Full recomputation completes in <5min for 100K edges
- [ ] Inferred edges queryable alongside direct edges
- [ ] Confidence correctly propagated through inference chain

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

- **ARCHITECTURE.md**: Overall system architecture
- **MEMORY.md**: Learned parameter system
- **SCORING.md**: Quality scoring
- **CONTEXT.md**: Adaptive retrieval
- **HANDOFF.md**: GP-based handoff
- **CHUNKING.md**: Chunk parameter learning
- **TODO.md**: Wave 4 implementation plan
