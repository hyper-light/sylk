package forest

import (
	"database/sql"
	"fmt"
)

const (
	forestSchemaVersionPhase456     = 6
	forestSchemaMigrationPhase456   = "memory_forest_phase_4_5_6"
	forestProjectionVersionPhase456 = "ledger_v1_evidence_v1_node_graph_v1_cluster_v1_ecology_v1"
)

func ensureForestPhase456Schema(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{name: "node_graph", fn: ensureForestNodeGraphSchema},
		{name: "clusters", fn: ensureForestClusterSchema},
		{name: "ecology", fn: ensureForestEcologySchema},
	}
	for _, step := range steps {
		if err := step.fn(db); err != nil {
			return fmt.Errorf("ensure phase 4-6 schema %s: %w", step.name, err)
		}
	}
	return nil
}

func ensureForestNodeGraphSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_nodes (
			node_id TEXT PRIMARY KEY,
			node_kind TEXT NOT NULL,
			source_kind TEXT NOT NULL,
			source_partition TEXT NOT NULL,
			source_key TEXT NOT NULL,
			source_seq INTEGER NOT NULL DEFAULT 0,
			subject_type TEXT NOT NULL,
			subject_id TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			evidence_grade TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			salience REAL NOT NULL DEFAULT 0,
			utility REAL NOT NULL DEFAULT 0,
			novelty REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			policy_version TEXT NOT NULL DEFAULT '',
			first_seen_at INTEGER NOT NULL DEFAULT 0,
			last_seen_at INTEGER NOT NULL DEFAULT 0,
			payload_hash TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(source_partition, source_key, node_kind, subject_type, subject_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_nodes_subject
			ON forest_nodes(subject_type, subject_id, source_seq DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_nodes_kind_session
			ON forest_nodes(node_kind, session_id, last_seen_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_nodes_source
			ON forest_nodes(source_kind, source_partition, source_seq)`,
		`CREATE TABLE IF NOT EXISTS forest_node_edges (
			edge_id TEXT PRIMARY KEY,
			source_node_id TEXT NOT NULL,
			target_node_id TEXT NOT NULL,
			edge_kind TEXT NOT NULL,
			source_kind TEXT NOT NULL,
			source_partition TEXT NOT NULL,
			source_key TEXT NOT NULL,
			source_seq INTEGER NOT NULL DEFAULT 0,
			weight REAL NOT NULL DEFAULT 0,
			evidence_grade TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(source_node_id, target_node_id, edge_kind, source_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_node_edges_source
			ON forest_node_edges(source_node_id, edge_kind, weight DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_node_edges_target
			ON forest_node_edges(target_node_id, edge_kind, weight DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_node_edges_kind
			ON forest_node_edges(edge_kind, source_seq DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_node_projection_partitions (
			source_partition TEXT PRIMARY KEY,
			last_seq INTEGER NOT NULL DEFAULT 0,
			last_projected_at INTEGER NOT NULL DEFAULT 0,
			policy_version TEXT NOT NULL DEFAULT '',
			health_status TEXT NOT NULL DEFAULT 'idle',
			last_error TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS forest_projection_failures (
			projection_name TEXT NOT NULL,
			source_key TEXT NOT NULL,
			source_seq INTEGER NOT NULL DEFAULT 0,
			source_partition TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			failure_kind TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			critical INTEGER NOT NULL DEFAULT 0,
			artifact_id TEXT NOT NULL DEFAULT '',
			last_attempt_at INTEGER NOT NULL DEFAULT 0,
			resolved_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (projection_name, source_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_projection_failures_active
			ON forest_projection_failures(projection_name, resolved_at, last_attempt_at DESC)`,
	}
	return execForestSchemaStatements(db, stmts)
}

func ensureForestClusterSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_clusters (
			cluster_id TEXT PRIMARY KEY,
			policy_version TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			candidate_name TEXT NOT NULL DEFAULT '',
			stable_name TEXT NOT NULL DEFAULT '',
			signature TEXT NOT NULL DEFAULT '',
			first_seen_at INTEGER NOT NULL DEFAULT 0,
			last_seen_at INTEGER NOT NULL DEFAULT 0,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_clusters_status
			ON forest_clusters(status, last_seen_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_cluster_membership (
			cluster_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			membership_weight REAL NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			source_seq INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (cluster_id, node_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_cluster_membership_node
			ON forest_cluster_membership(node_id, membership_weight DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_cluster_metrics (
			cluster_id TEXT PRIMARY KEY,
			policy_version TEXT NOT NULL,
			member_count INTEGER NOT NULL DEFAULT 0,
			cohesion REAL NOT NULL DEFAULT 0,
			validation_density REAL NOT NULL DEFAULT 0,
			contradiction_load REAL NOT NULL DEFAULT 0,
			novelty REAL NOT NULL DEFAULT 0,
			utility REAL NOT NULL DEFAULT 0,
			decay_pressure REAL NOT NULL DEFAULT 0,
			computed_at INTEGER NOT NULL DEFAULT 0,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS forest_cluster_lineage (
			lineage_id TEXT PRIMARY KEY,
			operation TEXT NOT NULL,
			source_cluster_id TEXT NOT NULL DEFAULT '',
			target_cluster_id TEXT NOT NULL DEFAULT '',
			source_ledger_id TEXT NOT NULL DEFAULT '',
			policy_version TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_cluster_lineage_cluster
			ON forest_cluster_lineage(source_cluster_id, target_cluster_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_bridge_nodes (
			bridge_id TEXT PRIMARY KEY,
			node_id TEXT NOT NULL,
			source_cluster_id TEXT NOT NULL,
			target_cluster_id TEXT NOT NULL,
			bridge_score REAL NOT NULL DEFAULT 0,
			cross_edge_count INTEGER NOT NULL DEFAULT 0,
			traversal_frequency REAL NOT NULL DEFAULT 0,
			validation_support REAL NOT NULL DEFAULT 0,
			contradiction_risk REAL NOT NULL DEFAULT 0,
			source_edge_ids TEXT NOT NULL DEFAULT '',
			policy_version TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(node_id, source_cluster_id, target_cluster_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_bridge_nodes_node
			ON forest_bridge_nodes(node_id, bridge_score DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_poi_cache (
			poi_id TEXT PRIMARY KEY,
			cluster_id TEXT NOT NULL DEFAULT '',
			node_id TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL,
			priority REAL NOT NULL DEFAULT 0,
			source_metrics TEXT NOT NULL DEFAULT '',
			expires_at INTEGER NOT NULL DEFAULT 0,
			invalidation_sequence INTEGER NOT NULL DEFAULT 0,
			policy_version TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_poi_cache_cluster
			ON forest_poi_cache(cluster_id, priority DESC, expires_at)`,
	}
	return execForestSchemaStatements(db, stmts)
}

func ensureForestEcologySchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_substrate_channels (
			channel TEXT PRIMARY KEY,
			source_description TEXT NOT NULL,
			sink_description TEXT NOT NULL,
			lower_bound REAL NOT NULL,
			upper_bound REAL NOT NULL,
			conservation_rule TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS forest_substrate_field (
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			channel TEXT NOT NULL,
			value REAL NOT NULL,
			adaptive_threshold REAL NOT NULL DEFAULT 0,
			policy_version TEXT NOT NULL,
			source_seq INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			metadata TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (scope_type, scope_id, channel, policy_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_substrate_field_scope
			ON forest_substrate_field(scope_type, scope_id, value DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_resource_accounting (
			accounting_id TEXT PRIMARY KEY,
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT '',
			event_kind TEXT NOT NULL,
			amount REAL NOT NULL,
			balance_after REAL NOT NULL,
			source_key TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			recorded_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(scope_type, scope_id, event_kind, source_key, policy_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_resource_accounting_scope
			ON forest_resource_accounting(scope_type, scope_id, recorded_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_activation_history (
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			channel TEXT NOT NULL,
			bucket INTEGER NOT NULL,
			exposure_count INTEGER NOT NULL DEFAULT 0,
			success_count INTEGER NOT NULL DEFAULT 0,
			weak_exposure_count INTEGER NOT NULL DEFAULT 0,
			threshold REAL NOT NULL DEFAULT 0,
			policy_version TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (scope_type, scope_id, channel, bucket, policy_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_activation_history_scope
			ON forest_activation_history(scope_type, scope_id, bucket DESC)`,
	}
	if err := execForestSchemaStatements(db, stmts); err != nil {
		return err
	}
	return seedForestSubstrateChannels(db)
}

func execForestSchemaStatements(db *sql.DB, stmts []string) error {
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
