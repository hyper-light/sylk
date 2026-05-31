package forest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
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
		-- Legacy CQRS sequencing layer (historical forest event sourcing).
		--
		-- Runtime appends now use forest_ledger. These tables remain for
		-- historical migration fixtures and archive compatibility only.
		-- ─────────────────────────────────────────────────────────────────
		CREATE TABLE IF NOT EXISTS forest_event_seq_log (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			appended_at INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_forest_event_seq_log_event
			ON forest_event_seq_log(event_id);

		-- ─────────────────────────────────────────────────────────────────
		-- Retrieval audit ledger — append-only record of every Retrieve
		-- call. Captures full ranked candidate set (not just top-K) so
		-- counterfactual training can label all candidates from outcome
		-- events, plus operator drill-down + A/B tooling.
		--
		-- Sibling forest_retrieval_event_seq_log mirrors the events log
		-- pattern: AUTOINCREMENT seq with UNIQUE event_id for idempotent
		-- replay safety.
		-- ─────────────────────────────────────────────────────────────────
		CREATE TABLE IF NOT EXISTS forest_retrieval_events (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			task_id TEXT,
			agent_id TEXT,
			agent_type TEXT,
			intent_id TEXT,
			query TEXT NOT NULL,
			horizon TEXT,
			families_blob TEXT,
			requested_limit INTEGER NOT NULL,
			include_counter_evidence INTEGER NOT NULL,
			requested_at INTEGER NOT NULL,
			duration_micros INTEGER NOT NULL,
			candidate_count INTEGER NOT NULL,
			returned_count INTEGER NOT NULL,
			model_key TEXT,
			model_version INTEGER,
			error_message TEXT,
			branch_projection_seq INTEGER NOT NULL DEFAULT 0,
			candidates_blob TEXT NOT NULL,
			metadata_blob TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_session
			ON forest_retrieval_events(session_id, requested_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_agent
			ON forest_retrieval_events(agent_id, requested_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_intent
			ON forest_retrieval_events(intent_id, requested_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_time
			ON forest_retrieval_events(requested_at DESC);

		CREATE TABLE IF NOT EXISTS forest_retrieval_event_seq_log (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			appended_at INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_seq_log_event
			ON forest_retrieval_event_seq_log(event_id);

		-- ─────────────────────────────────────────────────────────────────
		-- Retrieval candidates projection — denormalized one-row-per-
		-- (retrieval, branch) view of forest_retrieval_events.candidates_blob.
		-- Maintained by the retrieval-candidates projector (CQRS) and used
		-- by counterfactual labeling to find retrievals containing a
		-- branch via a single indexed lookup instead of a JSON LIKE scan.
		-- ─────────────────────────────────────────────────────────────────
		CREATE TABLE IF NOT EXISTS forest_retrieval_candidates (
			retrieval_event_id TEXT NOT NULL,
			retrieval_seq INTEGER NOT NULL,
			branch_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			rank_position INTEGER NOT NULL,
			returned INTEGER NOT NULL,
			base_score REAL NOT NULL,
			final_score REAL NOT NULL,
			predicted_utility REAL NOT NULL,
			predicted_risk REAL NOT NULL,
			exploration_mode INTEGER NOT NULL DEFAULT 0,
			retrieval_at INTEGER NOT NULL,
			PRIMARY KEY (retrieval_event_id, branch_id)
		);

		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_candidates_branch
			ON forest_retrieval_candidates(branch_id, retrieval_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_candidates_session_branch
			ON forest_retrieval_candidates(session_id, branch_id, retrieval_at DESC);
		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_candidates_seq
			ON forest_retrieval_candidates(retrieval_seq);

		-- Sibling table for the implicit-negative sweeper to record
		-- which retrieval events have been processed. Lives outside
		-- forest_retrieval_events because that table is append-only
		-- via trigger; sweep state is mutable per-retrieval state.
		CREATE TABLE IF NOT EXISTS forest_retrieval_sweep_state (
			retrieval_event_id TEXT PRIMARY KEY,
			swept_at INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_forest_retrieval_sweep_at
			ON forest_retrieval_sweep_state(swept_at DESC);

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

	if err := ensurePhase123Schema(db); err != nil {
		return err
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

	// Issue #10 — archive tables must exist before the append-only
	// triggers reference them in their conditional WHEN clauses.
	if err := ensureForestArchiveTables(db); err != nil {
		return err
	}

	if err := ensureForestEventsAppendOnly(db); err != nil {
		return err
	}

	if err := ensureForestRetrievalEventsAppendOnly(db); err != nil {
		return err
	}

	if err := ensureForestProjectorColumns(db); err != nil {
		return err
	}

	if err := ensureForestEventSeqBackfilled(db); err != nil {
		return err
	}

	if err := ensureForestConstraintSeverity(db); err != nil {
		return err
	}

	if err := ensureForestFamilyMigration(db); err != nil {
		return err
	}

	if err := recordSchemaVersionHash(db); err != nil {
		return err
	}

	return nil
}

// ensureForestFamilyMigration is the Phase 3 of Issue #11 one-shot
// data migration that rewrites legacy family values to the collapsed
// taxonomy. Idempotent: every UPDATE is conditional on the row still
// using the old value, so re-running on an already-migrated DB is a
// no-op.
//
// Mapping (audit-driven; see types.go family invariants):
//
//	decision     → intent       (a decision is "intent + selected
//	                             branch"; selection denormalized via
//	                             metadata if needed)
//	preference   → constraint   (already populated severity=soft via
//	                             ensureForestConstraintSeverity)
//	capability   → intent       (an agent's affordance is "what it
//	                             can pursue"; lift to intent)
//	opportunity  → intent       (a time-bound capability/intent
//	                             match; encode timing via metadata)
//	conflict     → antipattern  (the durable negative-evidence
//	                             successor; live conflict already
//	                             encoded by RelayRelationContradicts)
//
// Only forest_branches (the projection) is rewritten — forest_events
// is the append-only ledger (MEM-03) and rewriting it would violate
// the append-only trigger. Old events keep their historical family
// values; the projector applies canonicalizeFamily before writing
// each event to the projection (see projectBranchTx) so a from-scratch
// projection rebuild lands canonical families even when replaying
// pre-migration ledger rows.
//
// Operations are wrapped in a single transaction so a partial failure
// rolls back the entire migration.
func ensureForestFamilyMigration(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin family migration tx: %w", err)
	}
	defer tx.Rollback()

	migrations := []struct {
		oldFamily string
		newFamily string
	}{
		{"decision", "intent"},
		{"preference", "constraint"},
		{"capability", "intent"},
		{"opportunity", "intent"},
		{"conflict", "antipattern"},
	}
	for _, m := range migrations {
		if _, err := tx.Exec(
			`UPDATE forest_branches SET family = ? WHERE family = ?`,
			m.newFamily, m.oldFamily,
		); err != nil {
			return fmt.Errorf("migrate forest_branches family %s→%s: %w", m.oldFamily, m.newFamily, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit family migration: %w", err)
	}
	return nil
}

// ensureForestConstraintSeverity populates constraint_severity for
// any branch with family='preference' (legacy TreeFamilyPreference).
// Idempotent: only updates rows where severity is still empty so
// re-running on an already-migrated DB is a no-op.
//
// Phase 1 of Issue #11 keeps the family value as 'preference' so
// existing readers continue to work; Phase 3 rewrites the family
// itself in a separate migration step.
func ensureForestConstraintSeverity(db *sql.DB) error {
	if _, err := db.Exec(`
		UPDATE forest_branches
		SET    constraint_severity = ?
		WHERE  family = ?
		  AND  constraint_severity = ''
	`, string(ConstraintSeveritySoft), string(TreeFamilyPreference)); err != nil {
		return fmt.Errorf("backfill preference severity: %w", err)
	}
	return nil
}

// recordSchemaVersionHash writes a row in forest_schema_versions
// reflecting the current expected schema hash. Idempotent: if the
// most recent row already matches the current hash, no new row is
// written. A drift (DB row hash != current code hash) is the
// detectable signal Health() reports.
func recordSchemaVersionHash(db *sql.DB) error {
	currentHash := expectedSchemaHash()
	row := db.QueryRow(`
		SELECT schema_hash
		FROM   forest_schema_versions
		ORDER BY version DESC
		LIMIT  1
	`)
	var lastHash string
	err := row.Scan(&lastHash)
	if err == nil && lastHash == currentHash {
		return nil
	}
	// Either no row yet (first migration) or the hash changed.
	now := time.Now().UTC().Unix()
	if _, insertErr := db.Exec(`
		INSERT INTO forest_schema_versions (schema_hash, applied_at)
		VALUES (?, ?)
	`, currentHash, now); insertErr != nil {
		return fmt.Errorf("record schema version: %w", insertErr)
	}
	return nil
}

// expectedSchemaHash returns a deterministic hash of the schema
// expected by this build. Computed at runtime over a stable list of
// table + trigger + index identifiers. Operators see drift when the
// DB's recorded hash doesn't match this value — usually meaning the
// process is running against a database migrated by a different
// build.
//
// Hash inputs:
//   - sorted list of table names this package creates
//   - sorted list of trigger names (append-only enforcement)
//   - sorted list of index names this package creates
//
// Bumping the schema (adding a column, table, trigger, or index)
// mechanically changes this hash because the input lists differ.
//
// MAINTENANCE NOTE: every CREATE TABLE / CREATE INDEX / CREATE TRIGGER
// in this package's schema files must appear in one of the lists
// below. A name that exists in the schema but not in the list won't
// affect the hash, weakening drift detection.
func expectedSchemaHash() string {
	tables := []string{
		"forest_events",
		"forest_schema_meta",
		"forest_ledger",
		"forest_ledger_payloads",
		"forest_ledger_refs",
		"forest_ledger_delivery",
		"forest_projection_offsets",
		"forest_artifacts",
		"forest_artifact_edges",
		"forest_validations",
		"forest_validation_patterns",
		"forest_validation_pattern_observations",
		"forest_delta_projection_audit",
		"forest_evidence_errors",
		"forest_nodes",
		"forest_node_edges",
		"forest_node_projection_partitions",
		"forest_projection_failures",
		"forest_clusters",
		"forest_cluster_membership",
		"forest_cluster_metrics",
		"forest_cluster_lineage",
		"forest_bridge_nodes",
		"forest_poi_cache",
		"forest_substrate_channels",
		"forest_substrate_field",
		"forest_resource_accounting",
		"forest_activation_history",
		"forest_branches",
		"forest_relay_edges",
		"forest_canopies",
		"forest_event_seq_log",
		"forest_branch_traces",
		"forest_substrate_edges",
		"forest_substrate_sessions",
		"forest_substrate_state",
		"forest_substrate_frontiers",
		"forest_replay_queue",
		"forest_training_examples",
		"forest_models",
		"forest_projector_state",
		"forest_retrieval_events",
		"forest_retrieval_event_seq_log",
		"forest_retrieval_candidates",
		"forest_retrieval_sweep_state",
		"forest_base_score_models",
		"forest_schema_versions",
		"forest_events_archive",
		"forest_events_archive_summary",
		"forest_retrieval_events_archive",
		"forest_retrieval_events_archive_summary",
		"node_access_traces",
		"decay_parameters",
	}
	triggers := []string{
		"forest_events_no_update",
		"forest_events_no_delete",
		"forest_ledger_no_update",
		"forest_ledger_no_delete",
		"forest_ledger_payloads_no_update",
		"forest_ledger_payloads_no_delete",
		"forest_ledger_refs_no_update",
		"forest_ledger_refs_no_delete",
		"forest_ledger_delivery_no_update",
		"forest_ledger_delivery_no_delete",
		"forest_retrieval_events_no_update",
		"forest_retrieval_events_no_delete",
		"forest_events_archive_no_update",
		"forest_events_archive_no_delete",
		"forest_retrieval_events_archive_no_update",
		"forest_retrieval_events_archive_no_delete",
	}
	indexes := []string{
		"idx_forest_events_session_time",
		"idx_forest_ledger_source",
		"idx_forest_ledger_event",
		"idx_forest_ledger_session",
		"idx_forest_ledger_subject",
		"idx_forest_ledger_refs_lookup",
		"idx_forest_ledger_delivery_route",
		"idx_forest_artifacts_claim",
		"idx_forest_artifacts_testament",
		"idx_forest_artifacts_status",
		"idx_forest_artifact_edges_target",
		"idx_forest_validations_claim",
		"idx_forest_validations_artifact",
		"idx_forest_validations_status",
		"idx_forest_validation_patterns_lookup",
		"idx_forest_validation_pattern_observations_pattern",
		"idx_forest_delta_projection_audit_action",
		"idx_forest_evidence_errors_entity",
		"idx_forest_nodes_subject",
		"idx_forest_nodes_kind_session",
		"idx_forest_nodes_source",
		"idx_forest_node_edges_source",
		"idx_forest_node_edges_target",
		"idx_forest_node_edges_kind",
		"idx_forest_projection_failures_active",
		"idx_forest_clusters_status",
		"idx_forest_cluster_membership_node",
		"idx_forest_cluster_lineage_cluster",
		"idx_forest_bridge_nodes_node",
		"idx_forest_poi_cache_cluster",
		"idx_forest_substrate_field_scope",
		"idx_forest_resource_accounting_scope",
		"idx_forest_activation_history_scope",
		"idx_forest_events_branch_time",
		"idx_forest_events_root_time",
		"idx_forest_events_family",
		"idx_forest_events_content",
		"idx_forest_events_task_time",
		"idx_forest_branches_session",
		"idx_forest_branches_root",
		"idx_forest_branches_family",
		"idx_forest_branches_state",
		"idx_forest_branches_task",
		"idx_forest_relays_source",
		"idx_forest_relays_target",
		"idx_forest_canopies_session",
		"idx_forest_canopies_task",
		"idx_forest_replay_ready",
		"idx_forest_branch_traces_lookup",
		"idx_forest_training_branch",
		"idx_forest_training_labels",
		"idx_forest_training_label_source",
		"idx_forest_training_audit_event",
		"idx_forest_models_active",
		"idx_forest_substrate_edges_source",
		"idx_forest_substrate_edges_target",
		"idx_forest_substrate_sessions_dirty",
		"idx_forest_substrate_state_lookup",
		"idx_forest_substrate_frontiers_lookup",
		"idx_forest_event_seq_log_event",
		"idx_forest_retrieval_session",
		"idx_forest_retrieval_agent",
		"idx_forest_retrieval_intent",
		"idx_forest_retrieval_time",
		"idx_forest_retrieval_seq_log_event",
		"idx_forest_retrieval_candidates_branch",
		"idx_forest_retrieval_candidates_session_branch",
		"idx_forest_retrieval_candidates_seq",
		"idx_forest_retrieval_sweep_at",
		"idx_forest_base_score_models_version",
		"idx_forest_base_score_models_role",
		"idx_forest_events_archive_branch",
		"idx_forest_events_archive_session_time",
		"idx_forest_retrieval_events_archive_session",
		"idx_node_access_traces_lookup",
	}
	sort.Strings(tables)
	sort.Strings(triggers)
	sort.Strings(indexes)
	hasher := sha256.New()
	for _, t := range tables {
		hasher.Write([]byte("table:"))
		hasher.Write([]byte(t))
		hasher.Write([]byte{0})
	}
	for _, t := range triggers {
		hasher.Write([]byte("trigger:"))
		hasher.Write([]byte(t))
		hasher.Write([]byte{0})
	}
	for _, i := range indexes {
		hasher.Write([]byte("index:"))
		hasher.Write([]byte(i))
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// ensureForestProjectorColumns adds the last_applied_seq watermark
// columns to projection tables, plus selection-bias-fix columns on
// forest_retrieval_events and forest_training_examples. Idempotent —
// uses PRAGMA table_info to introspect existing columns and skip
// ALTERs that already ran.
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
		// Issue #3 — selection-bias-fix columns.
		// Note: swept_at lives on the sibling forest_retrieval_sweep_state
		// table, NOT on forest_retrieval_events (which is append-only and
		// would reject the UPDATE).
		//
		// label_source default is empty string ('') meaning "no outcome
		// label yet" — distinct from the four LabelSource enum values
		// which only apply once an outcome label is recorded.
		// retrieval_mode is set at retrieval-example creation time to
		// 'exploration' for ε-greedy retrievals, '' otherwise.
		// audit_event_id links a training example back to the retrieval
		// audit event that produced it; counterfactual labeling JOINs
		// via this column to forest_retrieval_candidates.
		{"forest_retrieval_events", "exploration_mode", "INTEGER NOT NULL DEFAULT 0"},
		{"forest_training_examples", "label_source", "TEXT NOT NULL DEFAULT ''"},
		{"forest_training_examples", "label_weight", "REAL NOT NULL DEFAULT 1.0"},
		{"forest_training_examples", "retrieval_mode", "TEXT NOT NULL DEFAULT ''"},
		{"forest_training_examples", "audit_event_id", "TEXT NOT NULL DEFAULT ''"},
		// Issue #7 — substrate-mode A/B capture. Default empty so legacy
		// rows back-fill as "unknown" and SubstrateModeStatsSince filters
		// them out of comparisons.
		{"forest_retrieval_events", "substrate_mode", "TEXT NOT NULL DEFAULT ''"},
		// Issue #8 — learned base scorer A/B capture. Default 0 means
		// "hardcoded defaults" (cold-start or pre-Issue #8). Non-zero
		// values reference forest_base_score_models.version.
		{"forest_retrieval_events", "base_score_version", "INTEGER NOT NULL DEFAULT 0"},
		{"forest_retrieval_events", "base_score_variant", "TEXT NOT NULL DEFAULT ''"},
		// Issue #11 Phase 1 — Constraint vs Preference distinction is
		// now encoded by this column rather than two TreeFamily values.
		// Empty string = not applicable (non-Constraint family); 'hard'
		// or 'soft' for Constraint family rows.
		{"forest_branches", "constraint_severity", "TEXT NOT NULL DEFAULT ''"},
		// Hyperparameter A/B capture — the snapshot id used to score
		// this retrieval, plus a flag indicating it was the proposed
		// (challenger) snapshot rather than the active one. Default 0
		// / 0 means "snapshot id unknown" (legacy rows or pre-tuner
		// retrievals); the audit-driven adapter uses these to attribute
		// outcome metrics to the correct A/B arm. Mirrored on the
		// archive table so the columns survive the cold-storage move.
		{"forest_retrieval_events", "hyperparam_snapshot_id", "INTEGER NOT NULL DEFAULT 0"},
		{"forest_retrieval_events", "proposed_hyperparams", "INTEGER NOT NULL DEFAULT 0"},
		{"forest_retrieval_events_archive", "hyperparam_snapshot_id", "INTEGER NOT NULL DEFAULT 0"},
		{"forest_retrieval_events_archive", "proposed_hyperparams", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, a := range additions {
		if err := addColumnIfMissing(db, a.table, a.column, a.decl); err != nil {
			return fmt.Errorf("add %s.%s: %w", a.table, a.column, err)
		}
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_forest_training_label_source
			ON forest_training_examples(label_source, updated_at DESC)
	`); err != nil {
		return fmt.Errorf("create idx_forest_training_label_source: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_forest_training_audit_event
			ON forest_training_examples(audit_event_id, branch_id)
			WHERE audit_event_id != ''
	`); err != nil {
		return fmt.Errorf("create idx_forest_training_audit_event: %w", err)
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
// The no-update trigger preserves immutability of any row that
// remains in forest_events. The no-delete trigger is conditional:
// DELETE is only permitted for rows whose id has already been
// inserted into forest_events_archive. Issue #10 archival worker
// uses this contract — INSERT into archive first, THEN DELETE from
// hot. The conditional preserves the append-only audit invariant
// (a row never disappears without a durable archived copy) while
// allowing storage reclamation.
//
// Idempotent via CREATE TRIGGER IF NOT EXISTS + DROP TRIGGER IF
// EXISTS for the rewritten variant. Re-running ensureSchema on an
// upgraded DB drops the legacy unconditional no-delete trigger and
// installs the archive-conditional one.
func ensureForestEventsAppendOnly(db *sql.DB) error {
	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS forest_events_no_update
			BEFORE UPDATE ON forest_events
			BEGIN
				SELECT RAISE(ABORT,
					'forest_events is append-only (MEM-03); use supersedes/contradicts columns on a new row');
			END;`,
		// Replace any pre-existing legacy unconditional no-delete
		// trigger before installing the archive-conditional variant.
		`DROP TRIGGER IF EXISTS forest_events_no_delete`,
		`CREATE TRIGGER IF NOT EXISTS forest_events_no_delete
			BEFORE DELETE ON forest_events
			WHEN NOT EXISTS (
				SELECT 1 FROM forest_events_archive WHERE id = OLD.id
			)
			BEGIN
				SELECT RAISE(ABORT,
					'forest_events DELETE requires prior archival (Issue #10): row must exist in forest_events_archive before deletion');
			END;`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("install forest_events append-only trigger: %w", err)
		}
	}
	return nil
}

// ensureForestRetrievalEventsAppendOnly installs the SQLite triggers
// that enforce append-only semantics on the retrieval audit ledger.
// Mirrors forest_events: no-update is unconditional; no-delete
// requires the row to exist in forest_retrieval_events_archive.
func ensureForestRetrievalEventsAppendOnly(db *sql.DB) error {
	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS forest_retrieval_events_no_update
			BEFORE UPDATE ON forest_retrieval_events
			BEGIN
				SELECT RAISE(ABORT,
					'forest_retrieval_events is append-only; an audit row records what was returned at a point in time and must not be rewritten');
			END;`,
		`DROP TRIGGER IF EXISTS forest_retrieval_events_no_delete`,
		`CREATE TRIGGER IF NOT EXISTS forest_retrieval_events_no_delete
			BEFORE DELETE ON forest_retrieval_events
			WHEN NOT EXISTS (
				SELECT 1 FROM forest_retrieval_events_archive WHERE id = OLD.id
			)
			BEGIN
				SELECT RAISE(ABORT,
					'forest_retrieval_events DELETE requires prior archival (Issue #10): row must exist in forest_retrieval_events_archive before deletion');
			END;`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("install forest_retrieval_events append-only trigger: %w", err)
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
		// Issue #8 — learned base scorer model store. Each row is one
		// trained version of the 13-component linear scorer. Role
		// flags ('champion' / 'challenger' / '') determine which
		// version is currently serving traffic. Append-only-by-
		// convention: training writes new rows; promotion mutates
		// only the role column.
		`CREATE TABLE IF NOT EXISTS forest_base_score_models (
			id            TEXT PRIMARY KEY,
			version       INTEGER NOT NULL,
			weights_blob  TEXT NOT NULL,
			bias          REAL NOT NULL DEFAULT 0,
			l1_norm       REAL NOT NULL DEFAULT 0,
			accuracy      REAL NOT NULL DEFAULT 0,
			training_size INTEGER NOT NULL DEFAULT 0,
			trained_at    INTEGER NOT NULL,
			role          TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_forest_base_score_models_version
			ON forest_base_score_models(version)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_base_score_models_role
			ON forest_base_score_models(role) WHERE role != ''`,
		// Issue #9 — schema-version journal. Each successful migration
		// run records its expected-schema hash so Health() can detect
		// drift (DB at one version, code at another). Append-only by
		// convention; the latest row is the active version.
		`CREATE TABLE IF NOT EXISTS forest_schema_versions (
			version      INTEGER PRIMARY KEY AUTOINCREMENT,
			schema_hash  TEXT NOT NULL,
			applied_at   INTEGER NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create support table: %w", err)
		}
	}
	return nil
}

// ensureForestArchiveTables creates the cold-storage tables for the
// Issue #10 archival worker. Schema mirrors forest_events /
// forest_retrieval_events except payload is stored compressed
// (gzip) in a BLOB column. Append-only by convention; the worker
// never UPDATEs archived rows. Summary tables hold per-branch /
// per-session denormalized counts for fast operator queries
// without scanning the archive.
func ensureForestArchiveTables(db *sql.DB) error {
	statements := []string{
		// Compressed cold storage for forest_events. Same column
		// shape so a JOIN UNION ALL across hot+archive returns a
		// uniform result set (modulo payload_compressed vs payload).
		`CREATE TABLE IF NOT EXISTS forest_events_archive (
			id               TEXT PRIMARY KEY,
			session_id       TEXT NOT NULL,
			task_id          TEXT,
			agent_id         TEXT,
			agent_type       TEXT,
			event_type       TEXT NOT NULL,
			family           TEXT NOT NULL,
			scope            TEXT NOT NULL,
			root_id          TEXT NOT NULL,
			branch_id        TEXT NOT NULL,
			parent_branch_id TEXT,
			intent_id        TEXT,
			content_id       TEXT,
			source_id        TEXT,
			confidence       REAL NOT NULL,
			salience         REAL NOT NULL,
			timestamp        INTEGER NOT NULL,
			title            TEXT,
			summary          TEXT,
			provenance_refs  TEXT,
			supersedes       TEXT,
			contradicts      TEXT,
			related_branch_ids TEXT,
			payload_compressed BLOB,
			archived_at      INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_events_archive_branch
			ON forest_events_archive(branch_id, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_events_archive_session_time
			ON forest_events_archive(session_id, timestamp DESC)`,
		// Archive immutability: once archived, rows never change or
		// disappear. Operators with a forensic need can always read
		// the archive directly.
		`CREATE TRIGGER IF NOT EXISTS forest_events_archive_no_update
			BEFORE UPDATE ON forest_events_archive
			BEGIN
				SELECT RAISE(ABORT, 'forest_events_archive is immutable');
			END;`,
		`CREATE TRIGGER IF NOT EXISTS forest_events_archive_no_delete
			BEFORE DELETE ON forest_events_archive
			BEGIN
				SELECT RAISE(ABORT, 'forest_events_archive is immutable');
			END;`,
		// Per-(branch, event_type) summary. UPSERTed by the archival
		// worker so the operator-facing read surface ("how many
		// decisions were archived for branch X") is one query.
		`CREATE TABLE IF NOT EXISTS forest_events_archive_summary (
			branch_id   TEXT NOT NULL,
			event_type  TEXT NOT NULL,
			count       INTEGER NOT NULL DEFAULT 0,
			first_seen  INTEGER NOT NULL,
			last_seen   INTEGER NOT NULL,
			PRIMARY KEY (branch_id, event_type)
		)`,
		// Same archive shape for retrieval audit events.
		`CREATE TABLE IF NOT EXISTS forest_retrieval_events_archive (
			id                       TEXT PRIMARY KEY,
			session_id               TEXT NOT NULL,
			task_id                  TEXT,
			agent_id                 TEXT,
			agent_type               TEXT,
			intent_id                TEXT,
			query                    TEXT NOT NULL,
			horizon                  TEXT,
			families_blob            TEXT,
			requested_limit          INTEGER NOT NULL,
			include_counter_evidence INTEGER NOT NULL DEFAULT 0,
			requested_at             INTEGER NOT NULL,
			duration_micros          INTEGER NOT NULL DEFAULT 0,
			candidate_count          INTEGER NOT NULL DEFAULT 0,
			returned_count           INTEGER NOT NULL DEFAULT 0,
			model_key                TEXT,
			model_version            INTEGER NOT NULL DEFAULT 0,
			error_message            TEXT,
			branch_projection_seq    INTEGER NOT NULL DEFAULT 0,
			candidates_compressed    BLOB,
			metadata_compressed      BLOB,
			exploration_mode         INTEGER NOT NULL DEFAULT 0,
			substrate_mode           TEXT NOT NULL DEFAULT '',
			base_score_version       INTEGER NOT NULL DEFAULT 0,
			base_score_variant       TEXT NOT NULL DEFAULT '',
			hyperparam_snapshot_id   INTEGER NOT NULL DEFAULT 0,
			proposed_hyperparams     INTEGER NOT NULL DEFAULT 0,
			archived_at              INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_retrieval_events_archive_session
			ON forest_retrieval_events_archive(session_id, requested_at DESC)`,
		`CREATE TRIGGER IF NOT EXISTS forest_retrieval_events_archive_no_update
			BEFORE UPDATE ON forest_retrieval_events_archive
			BEGIN
				SELECT RAISE(ABORT, 'forest_retrieval_events_archive is immutable');
			END;`,
		`CREATE TRIGGER IF NOT EXISTS forest_retrieval_events_archive_no_delete
			BEFORE DELETE ON forest_retrieval_events_archive
			BEGIN
				SELECT RAISE(ABORT, 'forest_retrieval_events_archive is immutable');
			END;`,
		// Per-session summary. Counts retrievals + earliest/latest.
		`CREATE TABLE IF NOT EXISTS forest_retrieval_events_archive_summary (
			session_id  TEXT PRIMARY KEY,
			count       INTEGER NOT NULL DEFAULT 0,
			first_seen  INTEGER NOT NULL,
			last_seen   INTEGER NOT NULL
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("install archive schema: %w", err)
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
