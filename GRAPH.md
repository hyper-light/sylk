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

---
