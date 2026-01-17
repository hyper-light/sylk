-- =============================================================================
-- DOMAIN AND TYPE ENUMS (stored as integers)
-- =============================================================================
-- Domain: 0 = code, 1 = history, 2 = academic
-- NodeType: See node type constants below

-- =============================================================================
-- NODES TABLE (unified for all domains)
-- =============================================================================
CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    domain INTEGER NOT NULL,
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
    timestamp DATETIME,
    category TEXT,

    -- Academic domain fields
    url TEXT,
    source TEXT,
    authors JSON,
    published_at DATETIME,

    -- Common fields
    content TEXT,
    content_hash TEXT,
    metadata JSON,

    -- Verification and trust
    verified BOOLEAN DEFAULT FALSE,
    verification_type INTEGER,
    confidence REAL DEFAULT 1.0,
    trust_level INTEGER DEFAULT 50,

    -- Temporal tracking
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    superseded_by TEXT,

    CHECK (domain IN (0, 1, 2)),
    FOREIGN KEY (superseded_by) REFERENCES nodes(id) ON DELETE SET NULL
);

-- =============================================================================
-- EDGES TABLE
-- =============================================================================
CREATE TABLE edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    edge_type INTEGER NOT NULL,
    weight REAL DEFAULT 1.0,
    metadata JSON,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE,
    UNIQUE (source_id, target_id, edge_type)
);

-- =============================================================================
-- VECTORS TABLE (embeddings as BLOBs)
-- =============================================================================
CREATE TABLE vectors (
    node_id TEXT PRIMARY KEY,
    embedding BLOB NOT NULL,
    magnitude REAL NOT NULL,
    dimensions INTEGER NOT NULL DEFAULT 768,
    domain INTEGER NOT NULL,
    node_type INTEGER NOT NULL,

    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

-- =============================================================================
-- PROVENANCE TABLE (tracks source of information)
-- =============================================================================
CREATE TABLE provenance (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id TEXT NOT NULL,
    source_type INTEGER NOT NULL,
    source_node_id TEXT,
    source_path TEXT,
    source_url TEXT,
    confidence REAL NOT NULL,
    verified_at DATETIME,

    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (source_node_id) REFERENCES nodes(id) ON DELETE SET NULL
);

-- =============================================================================
-- CONFLICTS TABLE (detected contradictions)
-- =============================================================================
CREATE TABLE conflicts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conflict_type INTEGER NOT NULL,
    subject TEXT NOT NULL,
    node_id_a TEXT NOT NULL,
    node_id_b TEXT NOT NULL,
    description TEXT NOT NULL,
    resolution TEXT,
    resolved BOOLEAN DEFAULT FALSE,
    detected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME,

    FOREIGN KEY (node_id_a) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (node_id_b) REFERENCES nodes(id) ON DELETE CASCADE
);

-- =============================================================================
-- HNSW INDEX PERSISTENCE
-- =============================================================================
CREATE TABLE hnsw_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE hnsw_edges (
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    level INTEGER NOT NULL,
    PRIMARY KEY (source_id, target_id, level)
);

-- =============================================================================
-- ACADEMIC-SPECIFIC TABLES
-- =============================================================================
CREATE TABLE academic_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    api_endpoint TEXT,
    rate_limit_per_min INTEGER,
    requires_auth BOOLEAN DEFAULT FALSE,
    last_crawled_at DATETIME
);

CREATE TABLE academic_chunks (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    UNIQUE (node_id, chunk_index)
);

CREATE TABLE library_docs (
    library_path TEXT PRIMARY KEY,
    doc_node_id TEXT,
    FOREIGN KEY (doc_node_id) REFERENCES nodes(id) ON DELETE SET NULL
);

-- =============================================================================
-- INDEXES
-- =============================================================================
CREATE INDEX idx_nodes_domain_type ON nodes(domain, node_type);
CREATE INDEX idx_nodes_path ON nodes(path) WHERE path IS NOT NULL;
CREATE INDEX idx_nodes_name ON nodes(name);
CREATE INDEX idx_nodes_session ON nodes(session_id) WHERE session_id IS NOT NULL;
CREATE INDEX idx_nodes_hash ON nodes(content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX idx_nodes_superseded ON nodes(superseded_by) WHERE superseded_by IS NOT NULL;

CREATE INDEX idx_edges_source ON edges(source_id, edge_type);
CREATE INDEX idx_edges_target ON edges(target_id, edge_type);
CREATE INDEX idx_edges_type ON edges(edge_type);

CREATE INDEX idx_vectors_domain ON vectors(domain);
CREATE INDEX idx_vectors_domain_type ON vectors(domain, node_type);

CREATE INDEX idx_provenance_node ON provenance(node_id);
CREATE INDEX idx_conflicts_unresolved ON conflicts(resolved) WHERE resolved = FALSE;

CREATE INDEX idx_hnsw_edges_level ON hnsw_edges(level, source_id);
CREATE INDEX idx_chunks_node ON academic_chunks(node_id);
