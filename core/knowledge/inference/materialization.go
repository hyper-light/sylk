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
type MaterializationManager struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewMaterializationManager creates a new MaterializationManager with the given database.
func NewMaterializationManager(db *sql.DB) *MaterializationManager {
	return &MaterializationManager{
		db: db,
	}
}

// Materialize stores derived edges from inference results.
// It inserts records into the materialized_edges table, avoiding duplicates.
func (m *MaterializationManager) Materialize(ctx context.Context, results []InferenceResult) error {
	if len(results) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, result := range results {
		if err := m.materializeResult(ctx, tx, result); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// materializeResult stores a single inference result.
func (m *MaterializationManager) materializeResult(ctx context.Context, tx *sql.Tx, result InferenceResult) error {
	edgeKey := m.buildEdgeKey(result.DerivedEdge)

	// Check if already materialized to avoid duplicates
	exists, err := m.existsInTx(ctx, tx, edgeKey)
	if err != nil {
		return fmt.Errorf("check existence: %w", err)
	}
	if exists {
		return nil // Skip duplicate
	}

	// Build evidence keys
	evidenceKeys := m.buildEvidenceKeys(result.Evidence)
	evidenceJSON, err := json.Marshal(evidenceKeys)
	if err != nil {
		return fmt.Errorf("marshal evidence: %w", err)
	}

	derivedAt := result.DerivedAt.UTC().Format(time.RFC3339)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO materialized_edges (rule_id, edge_key, evidence_json, derived_at)
		VALUES (?, ?, ?, ?)
	`, result.RuleID, edgeKey, string(evidenceJSON), derivedAt)
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
func (m *MaterializationManager) IsMaterialized(ctx context.Context, edgeKey string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

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
func (m *MaterializationManager) GetMaterializedEdges(ctx context.Context, ruleID string) ([]MaterializedEdgeRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

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
func (m *MaterializationManager) DeleteMaterializedByRule(ctx context.Context, ruleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, err := m.db.ExecContext(ctx, `
		DELETE FROM materialized_edges WHERE rule_id = ?
	`, ruleID)
	if err != nil {
		return fmt.Errorf("delete materialized edges: %w", err)
	}
	return nil
}

// GetAllMaterializedEdges retrieves all materialized edges.
func (m *MaterializationManager) GetAllMaterializedEdges(ctx context.Context) ([]MaterializedEdgeRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

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
func (m *MaterializationManager) GetMaterializedByEvidence(ctx context.Context, evidenceKey string) ([]MaterializedEdgeRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Query all records and filter by evidence (JSON contains)
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, rule_id, edge_key, evidence_json, derived_at
		FROM materialized_edges
		WHERE evidence_json LIKE ?
	`, "%"+evidenceKey+"%")
	if err != nil {
		return nil, fmt.Errorf("query by evidence: %w", err)
	}
	defer rows.Close()

	records, err := m.scanMaterializedEdges(rows)
	if err != nil {
		return nil, err
	}

	// Filter to ensure exact match in evidence array
	var filtered []MaterializedEdgeRecord
	for _, record := range records {
		if m.containsEvidence(record.Evidence, evidenceKey) {
			filtered = append(filtered, record)
		}
	}

	return filtered, nil
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
func (m *MaterializationManager) DeleteMaterializedEdge(ctx context.Context, edgeKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, err := m.db.ExecContext(ctx, `
		DELETE FROM materialized_edges WHERE edge_key = ?
	`, edgeKey)
	if err != nil {
		return fmt.Errorf("delete materialized edge: %w", err)
	}
	return nil
}
