package orchestrator

// decisionManifestSchema declares the SQLite schema for the cross-pipeline
// Decision Manifest. Lives on the same BunSQLite handle as the rest of the
// orchestrator state so it inherits the WAL + busy-timeout + writeMu policy
// fixed in the bun_sqlite DSN repair.
//
// Indexed access patterns:
//   - resolution scan: (session_id, domain, confidence) → bounded set of
//     candidate decisions to filter by scope match in code.
//   - lookup by id: PRIMARY KEY.
//   - tentative reaper: (confidence, expires_at) for stale-cleanup sweeps.
const decisionManifestSchema = `
CREATE TABLE IF NOT EXISTS decision_manifest (
    id                TEXT PRIMARY KEY,
    session_id        TEXT NOT NULL,
    domain            TEXT NOT NULL,
    scope_json        TEXT NOT NULL,
    value             TEXT NOT NULL,
    author_agent_id   TEXT NOT NULL,
    author_agent_type TEXT NOT NULL,
    declared_at       TIMESTAMP NOT NULL,
    confidence        TEXT NOT NULL,
    evidence_json     TEXT,
    supersedes        TEXT,
    history_json      TEXT,
    expires_at        TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_decision_manifest_resolve
    ON decision_manifest(session_id, domain, confidence);

CREATE INDEX IF NOT EXISTS idx_decision_manifest_session
    ON decision_manifest(session_id);

CREATE INDEX IF NOT EXISTS idx_decision_manifest_reap
    ON decision_manifest(confidence, expires_at);
`
