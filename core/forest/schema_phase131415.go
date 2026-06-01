package forest

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	forestSchemaVersionPhase131415     = 15
	forestSchemaMigrationPhase131415   = "memory_forest_phase_13_14_15"
	forestProjectionVersionPhase131415 = forestProjectionVersionPhase101112 + "_governance_v1_validation_gates_v1_rollout_v1"

	forestMigrationSliceStatusComplete = "complete"
)

type forestMigrationSlice struct {
	ID      string
	Ordinal int
	Summary string
}

func ensureForestPhase131415Schema(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{name: "governance", fn: ensureForestGovernanceSchema},
		{name: "rollout", fn: ensureForestRolloutSchema},
	}
	for _, step := range steps {
		if err := step.fn(db); err != nil {
			return fmt.Errorf("ensure phase 13-15 schema %s: %w", step.name, err)
		}
	}
	return validateForestRolloutState(context.Background(), db)
}

func ensureForestGovernanceSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_governance_proposals (
			proposal_id TEXT PRIMARY KEY,
			proposal_kind TEXT NOT NULL,
			subject_type TEXT NOT NULL,
			subject_id TEXT NOT NULL,
			status TEXT NOT NULL,
			claim_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			evidence_refs TEXT NOT NULL DEFAULT '',
			rollback_path TEXT NOT NULL,
			guardian_review_required INTEGER NOT NULL DEFAULT 0,
			permission_diff TEXT NOT NULL DEFAULT '',
			approval_claim_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(proposal_kind, subject_type, subject_id, evidence_refs)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_governance_proposals_status
			ON forest_governance_proposals(status, proposal_kind, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_governance_proposals_claim
			ON forest_governance_proposals(claim_id)`,
		`CREATE TABLE IF NOT EXISTS forest_governance_approvals (
			approval_id TEXT PRIMARY KEY,
			claim_id TEXT NOT NULL UNIQUE,
			actor TEXT NOT NULL,
			scope TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			permission_diff TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_governance_approvals_scope
			ON forest_governance_approvals(scope, expires_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_governance_quarantine (
			quarantine_id TEXT PRIMARY KEY,
			subject_type TEXT NOT NULL,
			subject_id TEXT NOT NULL,
			reason TEXT NOT NULL,
			evidence_refs TEXT NOT NULL DEFAULT '',
			proposal_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(subject_type, subject_id, reason)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_governance_quarantine_subject
			ON forest_governance_quarantine(subject_type, subject_id, created_at DESC)`,
	}
	return execForestSchemaStatements(db, stmts)
}

func ensureForestRolloutSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_migration_slices (
			slice_id TEXT PRIMARY KEY,
			slice_order INTEGER NOT NULL UNIQUE,
			status TEXT NOT NULL,
			schema_version INTEGER NOT NULL,
			projection_version TEXT NOT NULL,
			applied_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_migration_slices_status
			ON forest_migration_slices(status, slice_order)`,
		`CREATE TABLE IF NOT EXISTS forest_legacy_archives (
			archive_id TEXT PRIMARY KEY,
			legacy_object TEXT NOT NULL,
			archive_kind TEXT NOT NULL,
			schema_version INTEGER NOT NULL,
			archived_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(legacy_object, archive_kind)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_legacy_archives_object
			ON forest_legacy_archives(legacy_object, archive_kind)`,
	}
	if err := execForestSchemaStatements(db, stmts); err != nil {
		return err
	}
	return seedForestMigrationSlices(db)
}

func seedForestMigrationSlices(db *sql.DB) error {
	var existing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_migration_slices`).Scan(&existing); err != nil {
		return fmt.Errorf("count forest migration slices: %w", err)
	}
	if existing != 0 {
		return nil
	}
	now := time.Now().UTC().Unix()
	for _, slice := range forestMigrationSlices() {
		if _, err := db.Exec(`
			INSERT INTO forest_migration_slices
				(slice_id, slice_order, status, schema_version, projection_version, applied_at, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, slice.ID, slice.Ordinal, forestMigrationSliceStatusComplete, forestSchemaVersionPhase131415,
			forestProjectionVersionPhase131415, now, marshalJSON(slice)); err != nil {
			return fmt.Errorf("seed forest migration slice %s: %w", slice.ID, err)
		}
	}
	for _, archive := range forestLegacyArchives() {
		if _, err := db.Exec(`
			INSERT INTO forest_legacy_archives
				(archive_id, legacy_object, archive_kind, schema_version, archived_at, metadata)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(legacy_object, archive_kind) DO NOTHING
		`, stableID("forest_legacy_archive", archive.object, archive.kind), archive.object, archive.kind,
			forestSchemaVersionPhase131415, now, marshalJSON(archive)); err != nil {
			return fmt.Errorf("seed forest legacy archive %s: %w", archive.object, err)
		}
	}
	return nil
}

func validateForestRolloutState(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT slice_id, slice_order, status, schema_version, projection_version
		FROM forest_migration_slices
		ORDER BY slice_order ASC
	`)
	if err != nil {
		return fmt.Errorf("load forest migration slices: %w", err)
	}
	defer rows.Close()
	expected := forestMigrationSlices()
	for idx := range expected {
		if !rows.Next() {
			return fmt.Errorf("unsupported partial forest migration: missing slice %s", expected[idx].ID)
		}
		if err := scanAndValidateForestMigrationSlice(rows, expected[idx]); err != nil {
			return err
		}
	}
	if rows.Next() {
		return fmt.Errorf("unsupported forest migration state: extra slice row")
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate forest migration slices: %w", err)
	}
	return nil
}

func scanAndValidateForestMigrationSlice(row interface{ Scan(dest ...any) error }, expected forestMigrationSlice) error {
	var id, status, projection string
	var ordinal, schemaVersion int
	if err := row.Scan(&id, &ordinal, &status, &schemaVersion, &projection); err != nil {
		return fmt.Errorf("scan forest migration slice: %w", err)
	}
	if id != expected.ID || ordinal != expected.Ordinal {
		return fmt.Errorf("unsupported forest migration slice order: got %s/%d want %s/%d", id, ordinal, expected.ID, expected.Ordinal)
	}
	if status != forestMigrationSliceStatusComplete {
		return fmt.Errorf("unsupported incomplete forest migration slice %s status %s", id, status)
	}
	if schemaVersion != forestSchemaVersionPhase131415 || projection != forestProjectionVersionPhase131415 {
		return fmt.Errorf("unsupported forest migration slice %s version", id)
	}
	return nil
}

func forestMigrationSlices() []forestMigrationSlice {
	summaries := []string{
		"runtime_scope_and_schema_gates",
		"forest_ledger",
		"canonical_delta_ingestion",
		"artifact_and_validation_evidence_records",
		"node_graph_and_projector",
		"branch_projector_runtime_deletion",
		"cluster_topology_and_bridge_nodes",
		"multi_channel_substrate",
		"antigenic_field_and_quarantine",
		"forest_packet_retrieval",
		"cursor_propagation_and_role_projections",
		"agency_aware_forest_skills",
		"policy_engine",
		"memetic_layer",
		"skill_foundry",
		"governance_hard_gates",
		"legacy_schema_archival",
	}
	slices := make([]forestMigrationSlice, 0, len(summaries))
	for idx, summary := range summaries {
		ordinal := idx + 1
		slices = append(slices, forestMigrationSlice{
			ID:      fmt.Sprintf("memory_forest_slice_%02d_%s", ordinal, summary),
			Ordinal: ordinal,
			Summary: summary,
		})
	}
	return slices
}

type forestLegacyArchive struct {
	object string
	kind   string
}

func forestLegacyArchives() []forestLegacyArchive {
	return []forestLegacyArchive{
		{object: "forest_branches", kind: "historical_projection"},
		{object: "forest_relay_edges", kind: "historical_projection"},
		{object: "BranchPacket", kind: "archive_adapter"},
		{object: "Branch", kind: "archive_adapter"},
		{object: "TreeFamily", kind: "archive_adapter"},
		{object: "claims_activity_harvester", kind: "deleted_runtime_path"},
	}
}
