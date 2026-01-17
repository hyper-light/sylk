-- VectorGraphDB Schema
-- Version: 1
-- 
-- This schema supports a unified knowledge graph with vector similarity search
-- across three domains: Code, History, and Academic.

-- Enable WAL mode for concurrent read/write
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA synchronous = NORMAL;

-- Schema version tracking for migrations
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now')),
    description TEXT
);

-- Insert initial schema version
INSERT OR IGNORE INTO schema_version (version, description)
VALUES (1, 'Initial schema with nodes, edges, vectors, provenance, conflicts, and HNSW tables');

-- =============================================================================
-- Core Tables
-- =============================================================================

-- Nodes table: stores all graph nodes across all domains
CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    domain TEXT NOT NULL CHECK (domain IN ('code', 'history', 'academic')),
    node_type TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    metadata TEXT DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    accessed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Edges table: stores directed edges between nodes
CREATE TABLE IF NOT EXISTS edges (
    id TEXT PRIMARY KEY,
    from_node_id TEXT NOT NULL,
    to_node_id TEXT NOT NULL,
    edge_type TEXT NOT NULL,
    weight REAL DEFAULT 1.0 CHECK (weight >= 0.0 AND weight <= 1.0),
    metadata TEXT DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (from_node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (to_node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

-- Vectors table: stores embeddings for semantic search
CREATE TABLE IF NOT EXISTS vectors (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL UNIQUE,
    embedding BLOB NOT NULL,
    magnitude REAL NOT NULL CHECK (magnitude > 0),
    model_version TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

-- Provenance table: tracks source and verification of nodes
CREATE TABLE IF NOT EXISTS provenance (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL,
    source_type TEXT NOT NULL CHECK (source_type IN ('git', 'user', 'llm', 'web', 'api')),
    source_id TEXT NOT NULL,
    confidence REAL NOT NULL DEFAULT 1.0 CHECK (confidence >= 0.0 AND confidence <= 1.0),
    verified_at TEXT,
    verifier TEXT,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

-- Conflicts table: tracks detected contradictions between nodes
CREATE TABLE IF NOT EXISTS conflicts (
    id TEXT PRIMARY KEY,
    node_a_id TEXT NOT NULL,
    node_b_id TEXT NOT NULL,
    conflict_type TEXT NOT NULL,
    detected_at TEXT NOT NULL DEFAULT (datetime('now')),
    resolution TEXT,
    resolved_at TEXT,
    FOREIGN KEY (node_a_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (node_b_id) REFERENCES nodes(id) ON DELETE CASCADE
);

-- =============================================================================
-- HNSW Index Tables
-- =============================================================================

-- HNSW graph structure: stores neighbor connections per layer
CREATE TABLE IF NOT EXISTS hnsw_graph (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL,
    layer INTEGER NOT NULL CHECK (layer >= 0),
    neighbors TEXT NOT NULL DEFAULT '[]',
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    UNIQUE (node_id, layer)
);

-- HNSW metadata: stores index configuration and state
CREATE TABLE IF NOT EXISTS hnsw_metadata (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    entry_point TEXT,
    max_level INTEGER NOT NULL DEFAULT 0,
    m INTEGER NOT NULL DEFAULT 16,
    ef_construct INTEGER NOT NULL DEFAULT 200,
    ef_search INTEGER NOT NULL DEFAULT 50,
    level_mult REAL NOT NULL DEFAULT 0.36067977499789996,
    total_nodes INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Insert default HNSW metadata
INSERT OR IGNORE INTO hnsw_metadata (id, m, ef_construct, ef_search)
VALUES (1, 16, 200, 50);

-- =============================================================================
-- Indexes for Efficient Querying
-- =============================================================================

-- Nodes indexes
CREATE INDEX IF NOT EXISTS idx_nodes_domain ON nodes(domain);
CREATE INDEX IF NOT EXISTS idx_nodes_node_type ON nodes(node_type);
CREATE INDEX IF NOT EXISTS idx_nodes_domain_type ON nodes(domain, node_type);
CREATE INDEX IF NOT EXISTS idx_nodes_content_hash ON nodes(content_hash);
CREATE INDEX IF NOT EXISTS idx_nodes_accessed_at ON nodes(accessed_at);
CREATE INDEX IF NOT EXISTS idx_nodes_updated_at ON nodes(updated_at);

-- Edges indexes
CREATE INDEX IF NOT EXISTS idx_edges_from_node ON edges(from_node_id);
CREATE INDEX IF NOT EXISTS idx_edges_to_node ON edges(to_node_id);
CREATE INDEX IF NOT EXISTS idx_edges_type ON edges(edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_from_type ON edges(from_node_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_to_type ON edges(to_node_id, edge_type);

-- Vectors indexes
CREATE INDEX IF NOT EXISTS idx_vectors_node_id ON vectors(node_id);
CREATE INDEX IF NOT EXISTS idx_vectors_model ON vectors(model_version);

-- Provenance indexes
CREATE INDEX IF NOT EXISTS idx_provenance_node_id ON provenance(node_id);
CREATE INDEX IF NOT EXISTS idx_provenance_source ON provenance(source_type, source_id);
CREATE INDEX IF NOT EXISTS idx_provenance_confidence ON provenance(confidence);

-- Conflicts indexes
CREATE INDEX IF NOT EXISTS idx_conflicts_node_a ON conflicts(node_a_id);
CREATE INDEX IF NOT EXISTS idx_conflicts_node_b ON conflicts(node_b_id);
CREATE INDEX IF NOT EXISTS idx_conflicts_type ON conflicts(conflict_type);
CREATE INDEX IF NOT EXISTS idx_conflicts_unresolved ON conflicts(resolved_at) WHERE resolved_at IS NULL;

-- HNSW indexes
CREATE INDEX IF NOT EXISTS idx_hnsw_node_layer ON hnsw_graph(node_id, layer);
CREATE INDEX IF NOT EXISTS idx_hnsw_layer ON hnsw_graph(layer);

-- =============================================================================
-- Triggers for Automatic Timestamp Updates
-- =============================================================================

-- Update updated_at on node modification
CREATE TRIGGER IF NOT EXISTS trg_nodes_updated_at
AFTER UPDATE ON nodes
FOR EACH ROW
BEGIN
    UPDATE nodes SET updated_at = datetime('now') WHERE id = NEW.id;
END;

-- Update HNSW metadata timestamp
CREATE TRIGGER IF NOT EXISTS trg_hnsw_metadata_updated
AFTER UPDATE ON hnsw_metadata
FOR EACH ROW
BEGIN
    UPDATE hnsw_metadata SET updated_at = datetime('now') WHERE id = 1;
END;
