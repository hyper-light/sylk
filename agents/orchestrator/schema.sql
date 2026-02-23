-- Orchestrator SQLite schema (WAL-mode, embedded via //go:embed)
-- Schema version 1

-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO schema_version (version) VALUES (1);

-- DAG executions (one row per DAG, stores full snapshot)
CREATE TABLE IF NOT EXISTS dag_executions (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    name TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending',
    policy_json TEXT NOT NULL,
    dag_json TEXT NOT NULL,
    current_layer INTEGER DEFAULT 0,
    total_layers INTEGER DEFAULT 0,
    nodes_total INTEGER DEFAULT 0,
    nodes_succeeded INTEGER DEFAULT 0,
    nodes_failed INTEGER DEFAULT 0,
    nodes_skipped INTEGER DEFAULT 0,
    error TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME
);

-- DAG revisions (architect mid-flight modifications)
CREATE TABLE IF NOT EXISTS dag_revisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    dag_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    diff_json TEXT NOT NULL,
    reason TEXT NOT NULL,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (dag_id) REFERENCES dag_executions(id) ON DELETE CASCADE,
    UNIQUE(dag_id, revision)
);

-- Task updates (cold storage for BufferRegistry evictions)
CREATE TABLE IF NOT EXISTS task_updates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    dag_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    agent_type TEXT NOT NULL,
    status TEXT NOT NULL,
    progress REAL DEFAULT 0.0,
    message TEXT,
    output_json TEXT,
    error TEXT,
    attempt INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (dag_id) REFERENCES dag_executions(id) ON DELETE CASCADE
);

-- Pipeline agent state snapshots
CREATE TABLE IF NOT EXISTS pipeline_state (
    agent_id TEXT NOT NULL,
    dag_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    state_json TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, dag_id, node_id),
    FOREIGN KEY (dag_id) REFERENCES dag_executions(id) ON DELETE CASCADE
);

-- Plan version tracking
CREATE TABLE IF NOT EXISTS plan_versions (
    plan_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    session_id TEXT NOT NULL,
    status TEXT NOT NULL,
    plan_json TEXT NOT NULL,
    task_count INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plan_id, version)
);

-- Indexes derived from query patterns
CREATE INDEX IF NOT EXISTS idx_dag_exec_plan ON dag_executions(plan_id);
CREATE INDEX IF NOT EXISTS idx_dag_exec_session ON dag_executions(session_id);
CREATE INDEX IF NOT EXISTS idx_dag_exec_state ON dag_executions(state);
CREATE INDEX IF NOT EXISTS idx_dag_revisions_dag ON dag_revisions(dag_id);
CREATE INDEX IF NOT EXISTS idx_task_updates_dag ON task_updates(dag_id);
CREATE INDEX IF NOT EXISTS idx_task_updates_task ON task_updates(task_id);
CREATE INDEX IF NOT EXISTS idx_task_updates_node ON task_updates(node_id);
CREATE INDEX IF NOT EXISTS idx_task_updates_created ON task_updates(created_at);
CREATE INDEX IF NOT EXISTS idx_pipeline_state_dag ON pipeline_state(dag_id);
CREATE INDEX IF NOT EXISTS idx_plan_versions_session ON plan_versions(session_id);
