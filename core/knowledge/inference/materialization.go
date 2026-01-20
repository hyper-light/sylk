package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// =============================================================================
// MaterializedEdgeRecord (IE.4.1)
// =============================================================================

// MaterializedEdgeRecord represents a derived edge stored in the database.
// It captures the rule that created it, the edge key, when it was derived,
// and the evidence edges that supported the inference.
type MaterializedEdgeRecord struct {
	ID        int64     `json:"id"`
	RuleID    string    `json:"rule_id"`
	EdgeKey   string    `json:"edge_key"` // source:predicate:target
	DerivedAt time.Time `json:"derived_at"`
	Evidence  []string  `json:"evidence"` // JSON array of evidence edge keys
}

// =============================================================================
// MaterializationManager (IE.4.1)
// =============================================================================

// MaterializationManager handles the persistence of derived edges.
// It stores edges created by inference rules and tracks their provenance.
// Uses a write-only mutex to serialize write operations while allowing
// concurrent reads. Data preparation is done outside the lock to minimize
// contention during I/O operations.
type MaterializationManager struct {
	db      *sql.DB
	writeMu sync.Mutex // Protects write operations only
}

// NewMaterializationManager creates a new MaterializationManager with the given database.
func NewMaterializationManager(db *sql.DB) *MaterializationManager {
	return &MaterializationManager{
		db: db,
	}
}

// preparedResult holds pre-computed data for materialization.
// This allows data preparation outside of database transactions.
type preparedResult struct {
	edgeKey      string
	ruleID       string
	evidenceJSON string
	derivedAt    string
}

// Materialize stores derived edges from inference results.
// It inserts records into the materialized_edges table, avoiding duplicates.
// Data preparation is done outside the transaction to minimize lock time.
func (m *MaterializationManager) Materialize(ctx context.Context, results []InferenceResult) error {
	if len(results) == 0 {
		return nil
	}

	// Prepare all data outside of transaction (no lock held)
	prepared, err := m.prepareResults(results)
	if err != nil {
		return err
	}

	// Execute transaction with prepared data
	return m.executeMaterialize(ctx, prepared)
}

// prepareResults builds edge keys and evidence JSON for all results.
// This is CPU-bound work done outside any lock or transaction.
func (m *MaterializationManager) prepareResults(results []InferenceResult) ([]preparedResult, error) {
	prepared := make([]preparedResult, len(results))
	for i, result := range results {
		pr, err := m.prepareOneResult(result)
		if err != nil {
			return nil, err
		}
		prepared[i] = pr
	}
	return prepared, nil
}

// prepareOneResult prepares a single result for materialization.
func (m *MaterializationManager) prepareOneResult(result InferenceResult) (preparedResult, error) {
	edgeKey := m.buildEdgeKey(result.DerivedEdge)
	evidenceKeys := m.buildEvidenceKeys(result.Evidence)
	evidenceJSON, err := json.Marshal(evidenceKeys)
	if err != nil {
		return preparedResult{}, fmt.Errorf("marshal evidence: %w", err)
	}

	return preparedResult{
		edgeKey:      edgeKey,
		ruleID:       result.RuleID,
		evidenceJSON: string(evidenceJSON),
		derivedAt:    result.DerivedAt.UTC().Format(time.RFC3339),
	}, nil
}

// executeMaterialize runs the database transaction with prepared data.
// Lock is acquired only for the write transaction, not during data preparation.
func (m *MaterializationManager) executeMaterialize(ctx context.Context, prepared []preparedResult) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, pr := range prepared {
		if err := m.insertPrepared(ctx, tx, pr); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// insertPrepared inserts a single prepared result, skipping duplicates.
func (m *MaterializationManager) insertPrepared(ctx context.Context, tx *sql.Tx, pr preparedResult) error {
	exists, err := m.existsInTx(ctx, tx, pr.edgeKey)
	if err != nil {
		return fmt.Errorf("check existence: %w", err)
	}
	if exists {
		return nil
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO materialized_edges (rule_id, edge_key, evidence_json, derived_at)
		VALUES (?, ?, ?, ?)
	`, pr.ruleID, pr.edgeKey, pr.evidenceJSON, pr.derivedAt)
	if err != nil {
		return fmt.Errorf("insert materialized edge: %w", err)
	}
	return nil
}


// existsInTx checks if an edge key already exists in the materialized_edges table.
func (m *MaterializationManager) existsInTx(ctx context.Context, tx *sql.Tx, edgeKey string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM materialized_edges WHERE edge_key = ?
	`, edgeKey).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// buildEdgeKey creates a unique key for a derived edge.
// Format: source|predicate|target (matches Edge.Key() format)
func (m *MaterializationManager) buildEdgeKey(edge DerivedEdge) string {
	return fmt.Sprintf("%s|%s|%s", edge.SourceID, edge.EdgeType, edge.TargetID)
}

// buildEvidenceKeys converts evidence edges to their key representations.
func (m *MaterializationManager) buildEvidenceKeys(evidence []EvidenceEdge) []string {
	keys := make([]string, len(evidence))
	for i, e := range evidence {
		keys[i] = fmt.Sprintf("%s|%s|%s", e.SourceID, e.EdgeType, e.TargetID)
	}
	return keys
}

// IsMaterialized checks if an edge was derived by inference.
// No application lock needed - SQLite handles read concurrency.
func (m *MaterializationManager) IsMaterialized(ctx context.Context, edgeKey string) (bool, error) {
	var count int
	err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM materialized_edges WHERE edge_key = ?
	`, edgeKey).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check materialized: %w", err)
	}
	return count > 0, nil
}

// GetMaterializedEdges retrieves all edges derived by a specific rule.
// No application lock needed - SQLite handles read concurrency.
func (m *MaterializationManager) GetMaterializedEdges(ctx context.Context, ruleID string) ([]MaterializedEdgeRecord, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, rule_id, edge_key, evidence_json, derived_at
		FROM materialized_edges
		WHERE rule_id = ?
	`, ruleID)
	if err != nil {
		return nil, fmt.Errorf("query materialized edges: %w", err)
	}
	defer rows.Close()

	return m.scanMaterializedEdges(rows)
}

// scanMaterializedEdges scans rows into MaterializedEdgeRecord slice.
func (m *MaterializationManager) scanMaterializedEdges(rows *sql.Rows) ([]MaterializedEdgeRecord, error) {
	var records []MaterializedEdgeRecord
	for rows.Next() {
		record, err := m.scanSingleRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return records, nil
}

// scanSingleRecord scans a single row into a MaterializedEdgeRecord.
func (m *MaterializationManager) scanSingleRecord(rows *sql.Rows) (MaterializedEdgeRecord, error) {
	var record MaterializedEdgeRecord
	var evidenceJSON string
	var derivedAtStr string

	err := rows.Scan(&record.ID, &record.RuleID, &record.EdgeKey, &evidenceJSON, &derivedAtStr)
	if err != nil {
		return MaterializedEdgeRecord{}, fmt.Errorf("scan row: %w", err)
	}

	if err := json.Unmarshal([]byte(evidenceJSON), &record.Evidence); err != nil {
		return MaterializedEdgeRecord{}, fmt.Errorf("unmarshal evidence: %w", err)
	}

	derivedAt, err := time.Parse(time.RFC3339, derivedAtStr)
	if err != nil {
		return MaterializedEdgeRecord{}, fmt.Errorf("parse derived_at: %w", err)
	}
	record.DerivedAt = derivedAt

	return record, nil
}

// DeleteMaterializedByRule removes all edges derived by a specific rule.
// This is used when a rule is updated or deleted.
// Uses write lock to serialize with other write operations.
func (m *MaterializationManager) DeleteMaterializedByRule(ctx context.Context, ruleID string) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	_, err := m.db.ExecContext(ctx, `
		DELETE FROM materialized_edges WHERE rule_id = ?
	`, ruleID)
	if err != nil {
		return fmt.Errorf("delete materialized edges: %w", err)
	}
	return nil
}

// GetAllMaterializedEdges retrieves all materialized edges.
// No application lock needed - SQLite handles read concurrency.
func (m *MaterializationManager) GetAllMaterializedEdges(ctx context.Context) ([]MaterializedEdgeRecord, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, rule_id, edge_key, evidence_json, derived_at
		FROM materialized_edges
	`)
	if err != nil {
		return nil, fmt.Errorf("query all materialized edges: %w", err)
	}
	defer rows.Close()

	return m.scanMaterializedEdges(rows)
}

// GetMaterializedByEvidence retrieves all edges that depend on a given evidence edge.
// No application lock needed - SQLite handles read concurrency.
func (m *MaterializationManager) GetMaterializedByEvidence(ctx context.Context, evidenceKey string) ([]MaterializedEdgeRecord, error) {
	records, err := m.queryByEvidencePattern(ctx, evidenceKey)
	if err != nil {
		return nil, err
	}

	return m.filterByExactEvidence(records, evidenceKey), nil
}

// queryByEvidencePattern retrieves candidates that may contain the evidence key.
// Uses a simple substring match as a pre-filter. Exact matching is done by
// filterByExactEvidence after unmarshaling the JSON evidence arrays.
func (m *MaterializationManager) queryByEvidencePattern(ctx context.Context, evidenceKey string) ([]MaterializedEdgeRecord, error) {
	// Simple pattern match - filterByExactEvidence does precise matching
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, rule_id, edge_key, evidence_json, derived_at
		FROM materialized_edges
		WHERE evidence_json LIKE ?
	`, "%"+evidenceKey+"%")
	if err != nil {
		return nil, fmt.Errorf("query by evidence: %w", err)
	}
	defer rows.Close()

	return m.scanMaterializedEdges(rows)
}

// filterByExactEvidence filters records to those with exact evidence match.
func (m *MaterializationManager) filterByExactEvidence(records []MaterializedEdgeRecord, evidenceKey string) []MaterializedEdgeRecord {
	var filtered []MaterializedEdgeRecord
	for _, record := range records {
		if m.containsEvidence(record.Evidence, evidenceKey) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

// containsEvidence checks if the evidence array contains a specific key.
func (m *MaterializationManager) containsEvidence(evidence []string, key string) bool {
	for _, e := range evidence {
		if e == key {
			return true
		}
	}
	return false
}

// DeleteMaterializedEdge deletes a specific materialized edge by its key.
// Uses write lock to serialize with other write operations.
func (m *MaterializationManager) DeleteMaterializedEdge(ctx context.Context, edgeKey string) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	_, err := m.db.ExecContext(ctx, `
		DELETE FROM materialized_edges WHERE edge_key = ?
	`, edgeKey)
	if err != nil {
		return fmt.Errorf("delete materialized edge: %w", err)
	}
	return nil
}
