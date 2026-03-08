CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO schema_version (version) VALUES (1);

CREATE TABLE IF NOT EXISTS plans (
    plan_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    status TEXT NOT NULL,
    epoch INTEGER NOT NULL DEFAULT 0,
    plan_json TEXT NOT NULL,
    updated_at DATETIME NOT NULL,
    synced_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS continuations (
    continuation_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    state TEXT NOT NULL,
    plan_id TEXT,
    session_id TEXT NOT NULL,
    target_agent_id TEXT NOT NULL,
    response_correlation_id TEXT NOT NULL UNIQUE,
    invocation_correlation_id TEXT,
    tool_name TEXT,
    tool_call_id TEXT,
    raw_arguments TEXT,
    request_json TEXT,
    response_json TEXT,
    error_text TEXT,
    created_at DATETIME NOT NULL,
    expires_at DATETIME,
    completed_at DATETIME
);

CREATE TABLE IF NOT EXISTS continuation_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    continuation_id TEXT NOT NULL,
    state TEXT NOT NULL,
    note TEXT,
    payload_json TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_architect_plans_session ON plans(session_id);
CREATE INDEX IF NOT EXISTS idx_architect_plans_status ON plans(status);
CREATE INDEX IF NOT EXISTS idx_architect_continuations_state ON continuations(state);
CREATE INDEX IF NOT EXISTS idx_architect_continuations_plan ON continuations(plan_id);
CREATE INDEX IF NOT EXISTS idx_architect_continuations_session ON continuations(session_id);
CREATE INDEX IF NOT EXISTS idx_architect_continuation_events_cont ON continuation_events(continuation_id);
