package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// Evidence Validation Errors (W4P.35)
// =============================================================================

// ErrInvalidEvidence is returned when evidence data fails validation.
var ErrInvalidEvidence = errors.New("invalid evidence data")

// =============================================================================
// Retry Configuration and Dead-Letter Queue (W4P.20)
// =============================================================================

// RetryConfig holds settings for materialization retry behavior.
type RetryConfig struct {
	MaxRetries      int           // Maximum number of retry attempts (default 3)
	InitialBackoff  time.Duration // Initial backoff duration (default 100ms)
	MaxBackoff      time.Duration // Maximum backoff duration (default 5s)
	BackoffMultiple float64       // Multiplier for exponential backoff (default 2.0)
}

// BatchConfig holds settings for write batching behavior.
type BatchConfig struct {
	BatchSize int // Maximum number of edges per transaction batch (default 100)
}

// DefaultBatchConfig returns the default batch configuration.
func DefaultBatchConfig() BatchConfig {
	return BatchConfig{
		BatchSize: 100,
	}
}

// DefaultRetryConfig returns the default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:      3,
		InitialBackoff:  100 * time.Millisecond,
		MaxBackoff:      5 * time.Second,
		BackoffMultiple: 2.0,
	}
}

// FailedEdge represents an edge that failed materialization after all retries.
type FailedEdge struct {
	EdgeKey   string    `json:"edge_key"`
	RuleID    string    `json:"rule_id"`
	Error     string    `json:"error"`
	Attempts  int       `json:"attempts"`
	FailedAt  time.Time `json:"failed_at"`
	Prepared  preparedResult
}

// FailureCallback is called when materialization fails.
// Can be used for metrics, alerting, or custom logging.
type FailureCallback func(edge FailedEdge)

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
//
// W4P.20: Added retry with exponential backoff for failed materializations.
// Failed edges are logged and queued for later retry or manual intervention.
//
// W4P.39: Added batch configuration for write batching to reduce transaction
// overhead when materializing many edges.
type MaterializationManager struct {
	db              *sql.DB
	writeMu         sync.Mutex // Protects write operations only
	retryConfig     RetryConfig
	batchConfig     BatchConfig
	deadLetterMu    sync.Mutex
	deadLetterQueue []FailedEdge
	onFailure       FailureCallback
}

// NewMaterializationManager creates a new MaterializationManager with the given database.
func NewMaterializationManager(db *sql.DB) *MaterializationManager {
	return &MaterializationManager{
		db:              db,
		retryConfig:     DefaultRetryConfig(),
		batchConfig:     DefaultBatchConfig(),
		deadLetterQueue: make([]FailedEdge, 0),
	}
}

// SetRetryConfig sets the retry configuration for materialization.
func (m *MaterializationManager) SetRetryConfig(config RetryConfig) {
	m.retryConfig = config
}

// SetBatchConfig sets the batch configuration for write batching.
func (m *MaterializationManager) SetBatchConfig(config BatchConfig) {
	m.batchConfig = config
}

// SetFailureCallback sets a callback function for failed materializations.
func (m *MaterializationManager) SetFailureCallback(cb FailureCallback) {
	m.onFailure = cb
}

// GetDeadLetterQueue returns a copy of the dead-letter queue.
// Failed edges remain in the queue until cleared.
func (m *MaterializationManager) GetDeadLetterQueue() []FailedEdge {
	m.deadLetterMu.Lock()
	defer m.deadLetterMu.Unlock()
	result := make([]FailedEdge, len(m.deadLetterQueue))
	copy(result, m.deadLetterQueue)
	return result
}

// ClearDeadLetterQueue removes all edges from the dead-letter queue.
func (m *MaterializationManager) ClearDeadLetterQueue() {
	m.deadLetterMu.Lock()
	defer m.deadLetterMu.Unlock()
	m.deadLetterQueue = make([]FailedEdge, 0)
}

// addToDeadLetter adds a failed edge to the dead-letter queue.
func (m *MaterializationManager) addToDeadLetter(failed FailedEdge) {
	m.deadLetterMu.Lock()
	m.deadLetterQueue = append(m.deadLetterQueue, failed)
	m.deadLetterMu.Unlock()

	// Log the failure for visibility
	log.Printf("[WARN] materialization failed after %d attempts: edge=%s rule=%s error=%s",
		failed.Attempts, failed.EdgeKey, failed.RuleID, failed.Error)

	// Notify callback if set
	if m.onFailure != nil {
		m.onFailure(failed)
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
// W4P.20: Uses retry with exponential backoff for transient failures.
func (m *MaterializationManager) executeMaterialize(ctx context.Context, prepared []preparedResult) error {
	var lastErr error
	backoff := m.retryConfig.InitialBackoff

	for attempt := 0; attempt <= m.retryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := m.waitBackoff(ctx, backoff); err != nil {
				return err // Context cancelled
			}
			backoff = m.nextBackoff(backoff)
		}

		lastErr = m.tryMaterialize(ctx, prepared)
		if lastErr == nil {
			return nil
		}
	}

	// All retries exhausted - queue failed edges for manual intervention
	m.queueFailedEdges(prepared, lastErr)
	return lastErr
}

// tryMaterialize attempts to insert all prepared edges in a transaction.
func (m *MaterializationManager) tryMaterialize(ctx context.Context, prepared []preparedResult) error {
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

// waitBackoff waits for the backoff duration or until context is cancelled.
func (m *MaterializationManager) waitBackoff(ctx context.Context, backoff time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(backoff):
		return nil
	}
}

// nextBackoff calculates the next backoff duration with exponential increase.
func (m *MaterializationManager) nextBackoff(current time.Duration) time.Duration {
	next := time.Duration(float64(current) * m.retryConfig.BackoffMultiple)
	if next > m.retryConfig.MaxBackoff {
		return m.retryConfig.MaxBackoff
	}
	return next
}

// queueFailedEdges adds all prepared edges to the dead-letter queue.
func (m *MaterializationManager) queueFailedEdges(prepared []preparedResult, err error) {
	now := time.Now()
	for _, pr := range prepared {
		failed := FailedEdge{
			EdgeKey:  pr.edgeKey,
			RuleID:   pr.ruleID,
			Error:    err.Error(),
			Attempts: m.retryConfig.MaxRetries + 1,
			FailedAt: now,
			Prepared: pr,
		}
		m.addToDeadLetter(failed)
	}
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
// W4P.35: Added JSON schema validation after unmarshaling evidence data.
func (m *MaterializationManager) scanSingleRecord(rows *sql.Rows) (MaterializedEdgeRecord, error) {
	var record MaterializedEdgeRecord
	var evidenceJSON string
	var derivedAtStr string

	err := rows.Scan(&record.ID, &record.RuleID, &record.EdgeKey, &evidenceJSON, &derivedAtStr)
	if err != nil {
		return MaterializedEdgeRecord{}, fmt.Errorf("scan row: %w", err)
	}

	evidence, err := unmarshalAndValidateEvidence(evidenceJSON)
	if err != nil {
		return MaterializedEdgeRecord{}, err
	}
	record.Evidence = evidence

	derivedAt, err := time.Parse(time.RFC3339, derivedAtStr)
	if err != nil {
		return MaterializedEdgeRecord{}, fmt.Errorf("parse derived_at: %w", err)
	}
	record.DerivedAt = derivedAt

	return record, nil
}

// unmarshalAndValidateEvidence parses and validates evidence JSON data.
// W4P.35: Provides schema validation and integrity checks for evidence arrays.
// Returns ErrInvalidEvidence for malformed or invalid data.
func unmarshalAndValidateEvidence(evidenceJSON string) ([]string, error) {
	if evidenceJSON == "" {
		return nil, fmt.Errorf("%w: empty evidence JSON", ErrInvalidEvidence)
	}

	// Check for JSON null (which is valid JSON but not a valid array)
	trimmed := strings.TrimSpace(evidenceJSON)
	if trimmed == "null" {
		return nil, fmt.Errorf("%w: malformed JSON: expected array, got null", ErrInvalidEvidence)
	}

	var evidence []string
	if err := json.Unmarshal([]byte(evidenceJSON), &evidence); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON: %v", ErrInvalidEvidence, err)
	}

	if err := validateEvidenceArray(evidence); err != nil {
		return nil, err
	}

	return evidence, nil
}

// validateEvidenceArray checks integrity of deserialized evidence keys.
// Each evidence key must be a non-empty string in the format "source|predicate|target".
func validateEvidenceArray(evidence []string) error {
	for i, key := range evidence {
		if key == "" {
			return fmt.Errorf("%w: evidence[%d] is empty", ErrInvalidEvidence, i)
		}
		parts := strings.Split(key, "|")
		if len(parts) != 3 {
			return fmt.Errorf("%w: evidence[%d] has invalid format (expected source|predicate|target): %q", ErrInvalidEvidence, i, key)
		}
		if parts[1] == "" {
			return fmt.Errorf("%w: evidence[%d] has empty predicate: %q", ErrInvalidEvidence, i, key)
		}
	}
	return nil
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

// =============================================================================
// Write Batching (W4P.39)
// =============================================================================

// BatchResult captures the outcome of a single batch commit operation.
type BatchResult struct {
	BatchIndex    int      // Index of this batch (0-based)
	CommittedKeys []string // Edge keys successfully committed
	FailedKeys    []string // Edge keys that failed
	Error         error    // Error if batch failed (nil on success)
}

// MaterializeBatched stores derived edges using configurable batch sizes.
// Commits edges in batches to reduce transaction overhead. Returns results
// for each batch, allowing partial success tracking.
func (m *MaterializationManager) MaterializeBatched(ctx context.Context, results []InferenceResult) []BatchResult {
	if len(results) == 0 {
		return nil
	}

	prepared, err := m.prepareResults(results)
	if err != nil {
		return []BatchResult{{Error: err}}
	}

	return m.executeBatches(ctx, prepared)
}

// executeBatches processes prepared results in batches using configured size.
func (m *MaterializationManager) executeBatches(ctx context.Context, prepared []preparedResult) []BatchResult {
	batchSize := m.batchConfig.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	batches := m.splitIntoBatches(prepared, batchSize)
	results := make([]BatchResult, len(batches))

	for i, batch := range batches {
		results[i] = m.processSingleBatch(ctx, i, batch)
	}

	return results
}

// splitIntoBatches divides prepared results into chunks of specified size.
func (m *MaterializationManager) splitIntoBatches(prepared []preparedResult, batchSize int) [][]preparedResult {
	if len(prepared) == 0 {
		return nil
	}

	numBatches := (len(prepared) + batchSize - 1) / batchSize
	batches := make([][]preparedResult, 0, numBatches)

	for i := 0; i < len(prepared); i += batchSize {
		end := i + batchSize
		if end > len(prepared) {
			end = len(prepared)
		}
		batches = append(batches, prepared[i:end])
	}

	return batches
}

// processSingleBatch commits one batch and returns its result.
func (m *MaterializationManager) processSingleBatch(ctx context.Context, index int, batch []preparedResult) BatchResult {
	result := BatchResult{BatchIndex: index}
	keys := m.extractEdgeKeys(batch)

	err := m.executeMaterialize(ctx, batch)
	if err != nil {
		result.FailedKeys = keys
		result.Error = err
		return result
	}

	result.CommittedKeys = keys
	return result
}

// extractEdgeKeys returns the edge keys from a batch of prepared results.
func (m *MaterializationManager) extractEdgeKeys(batch []preparedResult) []string {
	keys := make([]string, len(batch))
	for i, pr := range batch {
		keys[i] = pr.edgeKey
	}
	return keys
}

// CountSuccessfulBatches returns the number of batches that committed.
func CountSuccessfulBatches(results []BatchResult) int {
	count := 0
	for _, r := range results {
		if r.Error == nil {
			count++
		}
	}
	return count
}

// CollectAllFailedKeys returns all failed edge keys across all batches.
func CollectAllFailedKeys(results []BatchResult) []string {
	var failed []string
	for _, r := range results {
		failed = append(failed, r.FailedKeys...)
	}
	return failed
}

// CollectAllCommittedKeys returns all committed edge keys across all batches.
func CollectAllCommittedKeys(results []BatchResult) []string {
	var committed []string
	for _, r := range results {
		committed = append(committed, r.CommittedKeys...)
	}
	return committed
}
