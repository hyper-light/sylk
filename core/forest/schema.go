package forest

import (
	"database/sql"
	"errors"
	"fmt"
)

func ensureSchema(db *sql.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS forest_events (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			task_id TEXT,
			agent_id TEXT,
			agent_type TEXT,
			event_type TEXT NOT NULL,
			family TEXT NOT NULL,
			scope TEXT NOT NULL,
			root_id TEXT NOT NULL,
			branch_id TEXT NOT NULL,
			parent_branch_id TEXT,
			intent_id TEXT,
			content_id TEXT,
			source_id TEXT,
			confidence REAL NOT NULL,
			salience REAL NOT NULL,
			timestamp INTEGER NOT NULL,
			title TEXT,
			summary TEXT,
			provenance_refs TEXT,
			supersedes TEXT,
			contradicts TEXT,
			related_branch_ids TEXT,
			payload TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_forest_events_session_time
			ON forest_events(session_id, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_events_branch_time
			ON forest_events(branch_id, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_events_root_time
			ON forest_events(root_id, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_events_family
			ON forest_events(family, scope, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_events_content
			ON forest_events(content_id);

		CREATE TABLE IF NOT EXISTS forest_branches (
			id TEXT PRIMARY KEY,
			root_id TEXT NOT NULL,
			parent_id TEXT,
			family TEXT NOT NULL,
			scope TEXT NOT NULL,
			state TEXT NOT NULL,
			session_id TEXT NOT NULL,
			task_id TEXT,
			agent_id TEXT,
			agent_type TEXT,
			intent_id TEXT,
			title TEXT NOT NULL,
			summary TEXT NOT NULL,
			confidence REAL NOT NULL,
			salience REAL NOT NULL,
			utility REAL NOT NULL,
			success_rate REAL NOT NULL,
			scope_risk REAL NOT NULL,
			conflict_score REAL NOT NULL,
			support_count INTEGER NOT NULL,
			counter_count INTEGER NOT NULL,
			success_count INTEGER NOT NULL,
			failure_count INTEGER NOT NULL,
			access_count INTEGER NOT NULL,
			last_accessed_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_forest_branches_session
			ON forest_branches(session_id, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_branches_root
			ON forest_branches(root_id, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_branches_family
			ON forest_branches(family, scope, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_branches_state
			ON forest_branches(state, updated_at DESC);

		CREATE TABLE IF NOT EXISTS forest_relay_edges (
			source_branch_id TEXT NOT NULL,
			target_branch_id TEXT NOT NULL,
			relation TEXT NOT NULL,
			weight REAL NOT NULL,
			cofire_count INTEGER NOT NULL,
			last_reinforced_at INTEGER NOT NULL,
			metadata TEXT,
			PRIMARY KEY (source_branch_id, target_branch_id, relation)
		);

		CREATE INDEX IF NOT EXISTS idx_forest_relays_source
			ON forest_relay_edges(source_branch_id, weight DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_relays_target
			ON forest_relay_edges(target_branch_id, weight DESC);

		CREATE TABLE IF NOT EXISTS forest_canopies (
			canopy_key TEXT PRIMARY KEY,
			session_id TEXT,
			task_id TEXT,
			intent_id TEXT,
			horizon TEXT NOT NULL,
			root_ids TEXT NOT NULL,
			summary TEXT,
			updated_at INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_forest_canopies_session
			ON forest_canopies(session_id, horizon, updated_at DESC);

		CREATE TABLE IF NOT EXISTS forest_replay_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			branch_id TEXT NOT NULL,
			root_id TEXT NOT NULL,
			priority REAL NOT NULL,
			reason TEXT NOT NULL,
			state TEXT NOT NULL,
			available_at INTEGER NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			payload TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_forest_replay_ready
			ON forest_replay_queue(state, available_at, priority DESC);

		CREATE TABLE IF NOT EXISTS forest_branch_traces (
			branch_id TEXT NOT NULL,
			accessed_at INTEGER NOT NULL,
			access_type TEXT NOT NULL,
			context TEXT,
			PRIMARY KEY (branch_id, accessed_at, access_type)
		);

		CREATE INDEX IF NOT EXISTS idx_forest_branch_traces_lookup
			ON forest_branch_traces(branch_id, accessed_at DESC);

		CREATE TABLE IF NOT EXISTS forest_training_examples (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			retrieval_id TEXT NOT NULL,
			branch_id TEXT NOT NULL,
			root_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			caller_agent_type TEXT,
			branch_agent_type TEXT,
			rank_position INTEGER NOT NULL,
			base_score REAL NOT NULL,
			predicted_utility REAL NOT NULL DEFAULT 0,
			predicted_risk REAL NOT NULL DEFAULT 0,
			utility_label REAL,
			risk_label REAL,
			features BLOB NOT NULL,
			metadata TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(retrieval_id, branch_id)
		);

		CREATE INDEX IF NOT EXISTS idx_forest_training_branch
			ON forest_training_examples(branch_id, session_id, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_training_labels
			ON forest_training_examples(caller_agent_type, updated_at DESC);

		CREATE TABLE IF NOT EXISTS forest_models (
			model_key TEXT NOT NULL,
			version INTEGER NOT NULL,
			objective TEXT NOT NULL,
			agent_type TEXT NOT NULL,
			trained_at INTEGER NOT NULL,
			example_count INTEGER NOT NULL,
			feature_count INTEGER NOT NULL,
			confidence REAL NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			metrics TEXT,
			payload BLOB NOT NULL,
			PRIMARY KEY (model_key, version)
		);

		CREATE INDEX IF NOT EXISTS idx_forest_models_active
			ON forest_models(model_key, active, trained_at DESC);

		CREATE TABLE IF NOT EXISTS forest_substrate_edges (
			session_id TEXT NOT NULL,
			source_branch_id TEXT NOT NULL,
			target_branch_id TEXT NOT NULL,
			conductance REAL NOT NULL,
			flux REAL NOT NULL,
			redundancy REAL NOT NULL,
			inhibition REAL NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT,
			PRIMARY KEY (session_id, source_branch_id, target_branch_id)
		);

		CREATE INDEX IF NOT EXISTS idx_forest_substrate_edges_source
			ON forest_substrate_edges(session_id, source_branch_id, conductance DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_substrate_edges_target
			ON forest_substrate_edges(session_id, target_branch_id, conductance DESC);

		CREATE TABLE IF NOT EXISTS forest_substrate_sessions (
			session_id TEXT PRIMARY KEY,
			graph_version INTEGER NOT NULL DEFAULT 0,
			substrate_version INTEGER NOT NULL DEFAULT 0,
			dirty INTEGER NOT NULL DEFAULT 0,
			dirty_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		);

		CREATE INDEX IF NOT EXISTS idx_forest_substrate_sessions_dirty
			ON forest_substrate_sessions(dirty, dirty_at DESC, updated_at DESC);

		CREATE TABLE IF NOT EXISTS forest_substrate_state (
			context_key TEXT NOT NULL,
			session_id TEXT NOT NULL,
			intent_id TEXT,
			horizon TEXT NOT NULL,
			agent_type TEXT,
			branch_id TEXT NOT NULL,
			nutrient_potential REAL NOT NULL,
			frontier_score REAL NOT NULL,
			inhibition REAL NOT NULL,
			conductance_mass REAL NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (context_key, branch_id)
		);

		CREATE INDEX IF NOT EXISTS idx_forest_substrate_state_lookup
			ON forest_substrate_state(context_key, updated_at DESC);

		CREATE TABLE IF NOT EXISTS forest_substrate_frontiers (
			context_key TEXT NOT NULL,
			agent_type TEXT NOT NULL,
			root_id TEXT NOT NULL,
			branch_id TEXT NOT NULL,
			frontier_score REAL NOT NULL,
			budget REAL NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (context_key, agent_type, branch_id)
		);

		CREATE INDEX IF NOT EXISTS idx_forest_substrate_frontiers_lookup
			ON forest_substrate_frontiers(context_key, frontier_score DESC);

		-- ─────────────────────────────────────────────────────────────────
		-- CQRS sequencing layer (forest event sourcing).
		--
		-- forest_event_seq_log provides monotonic ordering over the
		-- forest_events ledger. The events table stays append-only
		-- (MEM-03 trigger forbids in-place updates), so seq lives in
		-- a sibling table. AUTOINCREMENT supplies atomic seq
		-- allocation under any concurrent writer; UNIQUE(event_id)
		-- makes appends idempotent.
		-- ─────────────────────────────────────────────────────────────────
		CREATE TABLE IF NOT EXISTS forest_event_seq_log (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			appended_at INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_forest_event_seq_log_event
			ON forest_event_seq_log(event_id);

		-- ─────────────────────────────────────────────────────────────────
		-- Projector state — lease coordination + watermark per logical
		-- projector. Multi-process safety relies on the atomic UPDATE
		-- WHERE clause on leader_lease_until.
		-- ─────────────────────────────────────────────────────────────────
		CREATE TABLE IF NOT EXISTS forest_projector_state (
			projector_name TEXT PRIMARY KEY,
			last_applied_seq INTEGER NOT NULL DEFAULT 0,
			last_applied_at INTEGER NOT NULL DEFAULT 0,
			leader_holder TEXT NOT NULL DEFAULT '',
			leader_lease_until INTEGER NOT NULL DEFAULT 0,
			schema_version INTEGER NOT NULL DEFAULT 1,
			health_status TEXT NOT NULL DEFAULT 'idle',
			last_error TEXT NOT NULL DEFAULT '',
			last_error_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create forest schema: %w", err)
	}

	if err := ensureNodeMemoryColumns(db); err != nil {
		return err
	}

	if err := ensureForestSupportTables(db); err != nil {
		return err
	}

	if err := ensureForestTaskColumns(db); err != nil {
		return err
	}

	if err := ensureForestEventsAppendOnly(db); err != nil {
		return err
	}

	if err := ensureForestProjectorColumns(db); err != nil {
		return err
	}

	if err := ensureForestEventSeqBackfilled(db); err != nil {
		return err
	}

	return nil
}

// ensureForestProjectorColumns adds the last_applied_seq watermark
// columns to projection tables. Idempotent — uses PRAGMA table_info
// to introspect existing columns and skip ALTERs that already ran.
func ensureForestProjectorColumns(db *sql.DB) error {
	additions := []struct {
		table  string
		column string
		decl   string
	}{
		{"forest_branches", "last_applied_seq", "INTEGER NOT NULL DEFAULT 0"},
		{"forest_relay_edges", "last_applied_seq", "INTEGER NOT NULL DEFAULT 0"},
		{"forest_canopies", "last_applied_seq", "INTEGER NOT NULL DEFAULT 0"},
		{"forest_substrate_sessions", "last_applied_seq", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, a := range additions {
		if err := addColumnIfMissing(db, a.table, a.column, a.decl); err != nil {
			return fmt.Errorf("add %s.%s: %w", a.table, a.column, err)
		}
	}
	return nil
}

// addColumnIfMissing adds a column to a SQLite table only if it
// doesn't already exist. Idempotent via PRAGMA table_info.
func addColumnIfMissing(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("introspect %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			deflt   sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &deflt, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate columns: %w", err)
	}
	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl)
	if _, err := db.Exec(stmt); err != nil {
		return fmt.Errorf("alter %s: %w", table, err)
	}
	return nil
}

// ensureForestEventSeqBackfilled populates forest_event_seq_log from
// forest_events for events that don't yet have a seq. Idempotent —
// UNIQUE(event_id) constraint silently skips rows already present.
// Backfill ordering is (timestamp, id) which preserves causal-ish
// ordering with deterministic id-based tiebreak.
func ensureForestEventSeqBackfilled(db *sql.DB) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO forest_event_seq_log (event_id, appended_at)
		SELECT e.id, e.timestamp
		FROM   forest_events e
		LEFT JOIN forest_event_seq_log s ON s.event_id = e.id
		WHERE  s.event_id IS NULL
		ORDER BY e.timestamp ASC, e.id ASC
	`)
	if err != nil {
		return fmt.Errorf("backfill forest_event_seq_log: %w", err)
	}
	return nil
}

// ensureForestEventsAppendOnly installs the SQLite trigger that enforces
// MEM-03: the Soil / Evidence layer of the memory forest is append-only.
// Every observation persists an immutable audit record; later opinions
// about that observation are expressed as NEW rows (supersedes /
// contradicts) rather than in-place edits. An in-place UPDATE would
// silently rewrite history and break every downstream learner that
// assumes the WAL is monotonic.
//
// The trigger raises an ABORT on any UPDATE attempt, reverting the
// transaction. DELETEs are also forbidden for the same reason — retention
// and compaction are handled by archival workflows that move rows to
// cold storage, not by in-place mutation.
//
// The trigger definitions are idempotent via `CREATE TRIGGER IF NOT
// EXISTS`; re-running ensureSchema on an existing DB is a no-op.
func ensureForestEventsAppendOnly(db *sql.DB) error {
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS forest_events_no_update
			BEFORE UPDATE ON forest_events
			BEGIN
				SELECT RAISE(ABORT,
					'forest_events is append-only (MEM-03); use supersedes/contradicts columns on a new row');
			END;`,
		`CREATE TRIGGER IF NOT EXISTS forest_events_no_delete
			BEFORE DELETE ON forest_events
			BEGIN
				SELECT RAISE(ABORT,
					'forest_events is append-only (MEM-03); archival must go through cold-storage migration, not DELETE');
			END;`,
	}
	for _, stmt := range triggers {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("install forest_events append-only trigger: %w", err)
		}
	}
	return nil
}

func ensureForestSupportTables(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS node_access_traces (
			node_id TEXT NOT NULL,
			accessed_at INTEGER NOT NULL,
			access_type TEXT NOT NULL,
			context TEXT,
			PRIMARY KEY (node_id, accessed_at, access_type)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_node_access_traces_lookup
			ON node_access_traces(node_id, accessed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS decay_parameters (
			domain INTEGER PRIMARY KEY,
			decay_exponent_alpha REAL NOT NULL,
			decay_exponent_beta REAL NOT NULL,
			base_offset_mean REAL NOT NULL,
			base_offset_variance REAL NOT NULL,
			effective_samples REAL NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create support table: %w", err)
		}
	}
	return nil
}

func ensureForestTaskColumns(db *sql.DB) error {
	required := map[string]map[string]string{
		"forest_events": {
			"task_id": "ALTER TABLE forest_events ADD COLUMN task_id TEXT",
		},
		"forest_branches": {
			"task_id": "ALTER TABLE forest_branches ADD COLUMN task_id TEXT",
		},
		"forest_canopies": {
			"task_id": "ALTER TABLE forest_canopies ADD COLUMN task_id TEXT",
		},
	}
	for table, columnsToAdd := range required {
		exists, err := tableExists(db, table)
		if err != nil {
			return fmt.Errorf("inspect %s table: %w", table, err)
		}
		if !exists {
			continue
		}
		columns, err := tableColumns(db, table)
		if err != nil {
			return fmt.Errorf("inspect %s columns: %w", table, err)
		}
		for column, statement := range columnsToAdd {
			if _, ok := columns[column]; ok {
				continue
			}
			if _, err := db.Exec(statement); err != nil {
				return fmt.Errorf("add %s.%s: %w", table, column, err)
			}
		}
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_forest_events_task_time
			ON forest_events(session_id, task_id, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_branches_task
			ON forest_branches(session_id, task_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_canopies_task
			ON forest_canopies(session_id, task_id, horizon, updated_at DESC)`,
	}
	for _, statement := range indexes {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create forest task index: %w", err)
		}
	}
	return nil
}

func ensureNodeMemoryColumns(db *sql.DB) error {
	exists, err := tableExists(db, "nodes")
	if err != nil {
		return fmt.Errorf("inspect nodes table: %w", err)
	}
	if !exists {
		return errors.New("forest db missing nodes table; initialize the backing DB with vectorgraphdb.Open before forest.New")
	}

	columns, err := tableColumns(db, "nodes")
	if err != nil {
		return fmt.Errorf("inspect node columns: %w", err)
	}

	required := map[string]string{
		"memory_activation": "ALTER TABLE nodes ADD COLUMN memory_activation REAL DEFAULT 0.0",
		"last_accessed_at":  "ALTER TABLE nodes ADD COLUMN last_accessed_at INTEGER",
		"access_count":      "ALTER TABLE nodes ADD COLUMN access_count INTEGER DEFAULT 0",
		"base_offset":       "ALTER TABLE nodes ADD COLUMN base_offset REAL DEFAULT 0.0",
	}
	for column, statement := range required {
		if _, ok := columns[column]; ok {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("add nodes.%s: %w", column, err)
		}
	}
	return nil
}

func tableExists(db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

func tableColumns(db *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	return columns, rows.Err()
}
