package forest

import (
	"database/sql"
	"fmt"
)

type forestSchemaMigration struct {
	id string
	fn func(*sql.DB) error
}

func runForestSchemaMigrations(db *sql.DB) error {
	migrations := []forestSchemaMigration{
		{id: "phase123_pre_extension_audit", fn: ensureNoProhibitedSQLiteObjects},
		{id: "phase123_meta", fn: ensureForestSchemaMeta},
		{id: "phase123_ledger", fn: ensureForestLedgerSchema},
		{id: "phase123_evidence", fn: ensureForestEvidenceSchema},
		{id: "phase456_graph_cluster_ecology", fn: ensureForestPhase456Schema},
		{id: "phase789_immunity_retrieval_fabric", fn: ensureForestPhase789Schema},
		{id: "phase101112_policy_memetic_foundry", fn: ensureForestPhase101112Schema},
		{id: "phase131415_governance_validation_rollout", fn: ensureForestPhase131415Schema},
		{id: "phase123_post_extension_audit", fn: ensureNoProhibitedSQLiteObjects},
	}
	for _, migration := range migrations {
		if err := migration.fn(db); err != nil {
			return fmt.Errorf("forest schema migration %s: %w", migration.id, err)
		}
	}
	return nil
}
