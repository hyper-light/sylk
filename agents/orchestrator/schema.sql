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

-- Durable architect->orchestrator handoff receipts
CREATE TABLE IF NOT EXISTS plan_handoff_receipts (
    receipt_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    plan_id TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 0,
    correlation_id TEXT,
    requester_agent_id TEXT,
    attempt INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL,
    workflow_id TEXT,
    dag_id TEXT,
    task_count INTEGER DEFAULT 0,
    layer_count INTEGER DEFAULT 0,
    error_text TEXT,
    accepted_at DATETIME,
    dag_built_at DATETIME,
    submitted_at DATETIME,
    running_at DATETIME,
    failed_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_id, plan_id, revision)
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

-- Validation / remediation control plane
CREATE TABLE IF NOT EXISTS validation_epochs (
    epoch_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    status TEXT NOT NULL,
    reason TEXT,
    workflow_ids_json TEXT,
    dag_ids_json TEXT,
    task_ids_json TEXT,
    plan_id TEXT,
    summary TEXT,
    validator_verdict_json TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

CREATE TABLE IF NOT EXISTS execution_holds (
    hold_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    epoch_id TEXT,
    remediation_case_id TEXT,
    status TEXT NOT NULL,
    reason TEXT NOT NULL,
    summary TEXT,
    created_by_agent_id TEXT NOT NULL,
    created_by_agent_type TEXT NOT NULL,
    released_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS remediation_cases (
    case_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    epoch_id TEXT,
    hold_id TEXT NOT NULL,
    plan_id TEXT,
    status TEXT NOT NULL,
    summary TEXT NOT NULL,
    request_json TEXT NOT NULL,
    result_json TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

-- Indexes derived from query patterns
CREATE INDEX IF NOT EXISTS idx_dag_exec_plan ON dag_executions(plan_id);
CREATE INDEX IF NOT EXISTS idx_dag_exec_session ON dag_executions(session_id);
CREATE INDEX IF NOT EXISTS idx_dag_exec_state ON dag_executions(state);
CREATE INDEX IF NOT EXISTS idx_handoff_receipts_plan ON plan_handoff_receipts(session_id, plan_id, revision);
CREATE INDEX IF NOT EXISTS idx_handoff_receipts_status ON plan_handoff_receipts(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_dag_revisions_dag ON dag_revisions(dag_id);
CREATE INDEX IF NOT EXISTS idx_task_updates_dag ON task_updates(dag_id);
CREATE INDEX IF NOT EXISTS idx_task_updates_task ON task_updates(task_id);
CREATE INDEX IF NOT EXISTS idx_task_updates_node ON task_updates(node_id);
CREATE INDEX IF NOT EXISTS idx_task_updates_created ON task_updates(created_at);
CREATE INDEX IF NOT EXISTS idx_pipeline_state_dag ON pipeline_state(dag_id);
CREATE INDEX IF NOT EXISTS idx_plan_versions_session ON plan_versions(session_id);
CREATE INDEX IF NOT EXISTS idx_validation_epochs_session ON validation_epochs(session_id, status, updated_at);
CREATE INDEX IF NOT EXISTS idx_execution_holds_session ON execution_holds(session_id, status, updated_at);
CREATE INDEX IF NOT EXISTS idx_remediation_cases_session ON remediation_cases(session_id, status, updated_at);

-- Task coordination ledger (authoritative per-task coordination state)
CREATE TABLE IF NOT EXISTS coordination_actions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    task_name TEXT,
    kind TEXT NOT NULL,
    actor_agent_id TEXT NOT NULL,
    actor_type TEXT NOT NULL,
    idempotency_key TEXT,
    payload_json TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS coordination_claims (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    task_name TEXT,
    scope_kind TEXT NOT NULL,
    scope_key TEXT NOT NULL,
    purpose TEXT,
    mode TEXT NOT NULL,
    state TEXT NOT NULL,
    owner_agent_id TEXT NOT NULL,
    owner_type TEXT NOT NULL,
    idempotency_key TEXT,
    evidence_json TEXT,
    lease_expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS coordination_artifacts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    task_name TEXT,
    kind TEXT NOT NULL,
    title TEXT,
    summary TEXT NOT NULL,
    scope_kind TEXT,
    scope_key TEXT,
    status TEXT NOT NULL,
    producer_agent_id TEXT NOT NULL,
    producer_type TEXT NOT NULL,
    idempotency_key TEXT,
    payload_json TEXT,
    evidence_json TEXT,
    upstream_artifact_ids_json TEXT,
    supersedes_artifact_id TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS coordination_reviews (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    requester_agent_id TEXT NOT NULL,
    requester_type TEXT NOT NULL,
    reviewer_type TEXT NOT NULL,
    summary TEXT NOT NULL,
    criteria_json TEXT,
    status TEXT NOT NULL,
    idempotency_key TEXT,
    result_json TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_coord_actions_task ON coordination_actions(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_coord_actions_actor ON coordination_actions(actor_agent_id, created_at);
CREATE INDEX IF NOT EXISTS idx_coord_claims_task ON coordination_claims(task_id, state, updated_at);
CREATE INDEX IF NOT EXISTS idx_coord_claims_scope ON coordination_claims(task_id, scope_kind, scope_key, state);
CREATE INDEX IF NOT EXISTS idx_coord_artifacts_task ON coordination_artifacts(task_id, status, updated_at);
CREATE INDEX IF NOT EXISTS idx_coord_artifacts_kind ON coordination_artifacts(task_id, kind, updated_at);
CREATE INDEX IF NOT EXISTS idx_coord_reviews_task ON coordination_reviews(task_id, status, updated_at);
CREATE INDEX IF NOT EXISTS idx_coord_reviews_artifact ON coordination_reviews(artifact_id, status);
