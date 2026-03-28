package forest

import (
	"database/sql"
	"fmt"
)

func ensureSchema(db *sql.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS forest_events (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
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

func ensureNodeMemoryColumns(db *sql.DB) error {
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
