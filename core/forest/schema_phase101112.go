package forest

import (
	"database/sql"
	"fmt"
)

const (
	forestSchemaVersionPhase101112     = 12
	forestSchemaMigrationPhase101112   = "memory_forest_phase_10_11_12"
	forestProjectionVersionPhase101112 = forestProjectionVersionPhase789 + "_policy_v1_memetic_v1_skill_foundry_v1"
)

func ensureForestPhase101112Schema(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{name: "policy_engine", fn: ensureForestPolicySchema},
		{name: "memetic_layer", fn: ensureForestMemeSchema},
		{name: "skill_foundry", fn: ensureForestSkillFoundrySchema},
	}
	for _, step := range steps {
		if err := step.fn(db); err != nil {
			return fmt.Errorf("ensure phase 10-12 schema %s: %w", step.name, err)
		}
	}
	return nil
}

func ensureForestPolicySchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_policies (
			policy_id TEXT PRIMARY KEY,
			policy_version TEXT NOT NULL,
			status TEXT NOT NULL,
			source TEXT NOT NULL,
			genome TEXT NOT NULL,
			hyperparameters TEXT NOT NULL,
			validation_evidence_refs TEXT NOT NULL DEFAULT '',
			rollback_policy TEXT NOT NULL,
			fitness TEXT NOT NULL DEFAULT '',
			active_since INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_policies_status
			ON forest_policies(status, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_forest_policies_single_active
			ON forest_policies(status)
			WHERE status = 'active'`,
		`CREATE TABLE IF NOT EXISTS forest_policy_candidates (
			candidate_id TEXT PRIMARY KEY,
			parent_policy_id TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			genome TEXT NOT NULL,
			hyperparameters TEXT NOT NULL,
			status TEXT NOT NULL,
			source TEXT NOT NULL,
			evidence_refs TEXT NOT NULL DEFAULT '',
			validation_evidence_refs TEXT NOT NULL DEFAULT '',
			rejection_reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_policy_candidates_status
			ON forest_policy_candidates(status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_policy_candidates_parent
			ON forest_policy_candidates(parent_policy_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_policy_trials (
			trial_id TEXT PRIMARY KEY,
			candidate_id TEXT NOT NULL,
			champion_policy_id TEXT NOT NULL,
			cohort_key TEXT NOT NULL,
			assigned_arm TEXT NOT NULL,
			seed INTEGER NOT NULL,
			started_at INTEGER NOT NULL,
			ended_at INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_policy_trials_candidate
			ON forest_policy_trials(candidate_id, status, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_policy_trials_cohort
			ON forest_policy_trials(cohort_key, started_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_policy_outcomes (
			outcome_id TEXT PRIMARY KEY,
			candidate_id TEXT NOT NULL,
			policy_id TEXT NOT NULL,
			trial_id TEXT NOT NULL DEFAULT '',
			arm TEXT NOT NULL,
			correctness REAL NOT NULL,
			safety REAL NOT NULL,
			cost REAL NOT NULL,
			ecology REAL NOT NULL,
			validation_pass_rate REAL NOT NULL,
			latency_micros INTEGER NOT NULL DEFAULT 0,
			contradiction_delta REAL NOT NULL DEFAULT 0,
			sample_count INTEGER NOT NULL,
			evidence_refs TEXT NOT NULL DEFAULT '',
			observed_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_policy_outcomes_candidate
			ON forest_policy_outcomes(candidate_id, observed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_policy_outcomes_policy
			ON forest_policy_outcomes(policy_id, observed_at DESC)`,
	}
	return execForestSchemaStatements(db, stmts)
}

func ensureForestMemeSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_memes (
			meme_id TEXT PRIMARY KEY,
			meme_kind TEXT NOT NULL,
			signature TEXT NOT NULL,
			status TEXT NOT NULL,
			polarity TEXT NOT NULL,
			summary TEXT NOT NULL,
			schema_version TEXT NOT NULL,
			source_node_ids TEXT NOT NULL DEFAULT '',
			source_artifact_refs TEXT NOT NULL DEFAULT '',
			source_validation_refs TEXT NOT NULL DEFAULT '',
			parent_meme_ids TEXT NOT NULL DEFAULT '',
			fitness REAL NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(meme_kind, signature, polarity)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_memes_status
			ON forest_memes(status, meme_kind, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_memes_signature
			ON forest_memes(meme_kind, signature)`,
		`CREATE TABLE IF NOT EXISTS forest_meme_sources (
			meme_id TEXT NOT NULL,
			source_ref TEXT NOT NULL,
			source_kind TEXT NOT NULL,
			weight REAL NOT NULL,
			evidence_grade TEXT NOT NULL,
			recorded_at INTEGER NOT NULL,
			PRIMARY KEY (meme_id, source_ref)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_meme_sources_ref
			ON forest_meme_sources(source_ref, recorded_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_meme_lineage (
			lineage_id TEXT PRIMARY KEY,
			child_meme_id TEXT NOT NULL,
			parent_meme_ids TEXT NOT NULL,
			operation TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_meme_lineage_child
			ON forest_meme_lineage(child_meme_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_meme_fitness (
			meme_id TEXT NOT NULL,
			fitness_version TEXT NOT NULL,
			fitness REAL NOT NULL,
			success_count INTEGER NOT NULL,
			failure_count INTEGER NOT NULL,
			suppression_count INTEGER NOT NULL DEFAULT 0,
			evidence_refs TEXT NOT NULL DEFAULT '',
			recorded_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (meme_id, fitness_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_meme_fitness_score
			ON forest_meme_fitness(fitness DESC, recorded_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_meme_candidates (
			candidate_id TEXT PRIMARY KEY,
			meme_kind TEXT NOT NULL,
			status TEXT NOT NULL,
			parent_meme_ids TEXT NOT NULL,
			signature TEXT NOT NULL,
			summary TEXT NOT NULL,
			generation INTEGER NOT NULL,
			rejection_reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(meme_kind, signature, parent_meme_ids)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_meme_candidates_status
			ON forest_meme_candidates(status, updated_at DESC)`,
	}
	return execForestSchemaStatements(db, stmts)
}

func ensureForestSkillFoundrySchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS forest_skill_candidates (
			candidate_id TEXT PRIMARY KEY,
			candidate_key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			role_scope TEXT NOT NULL,
			trigger TEXT NOT NULL,
			status TEXT NOT NULL,
			artifact_type TEXT NOT NULL,
			source_meme_ids TEXT NOT NULL DEFAULT '',
			source_node_ids TEXT NOT NULL DEFAULT '',
			source_claim_ids TEXT NOT NULL DEFAULT '',
			source_validation_refs TEXT NOT NULL DEFAULT '',
			rejected_variant_ids TEXT NOT NULL DEFAULT '',
			permission_diff TEXT NOT NULL DEFAULT '',
			explicit_permission_claim_id TEXT NOT NULL DEFAULT '',
			promotion_rationale TEXT NOT NULL DEFAULT '',
			guardian_review_required INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_skill_candidates_status
			ON forest_skill_candidates(status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_skill_candidates_role
			ON forest_skill_candidates(role_scope, status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS forest_skill_candidate_files (
			candidate_id TEXT NOT NULL,
			path TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			content TEXT NOT NULL,
			file_kind TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (candidate_id, path)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_skill_candidate_files_hash
			ON forest_skill_candidate_files(content_hash)`,
		`CREATE TABLE IF NOT EXISTS forest_skill_candidate_validations (
			validation_id TEXT PRIMARY KEY,
			candidate_id TEXT NOT NULL,
			validator TEXT NOT NULL,
			status TEXT NOT NULL,
			summary TEXT NOT NULL,
			evidence_refs TEXT NOT NULL DEFAULT '',
			recorded_at INTEGER NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			UNIQUE(candidate_id, validator)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forest_skill_candidate_validations_candidate
			ON forest_skill_candidate_validations(candidate_id, status, recorded_at DESC)`,
	}
	return execForestSchemaStatements(db, stmts)
}
