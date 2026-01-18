package vectorgraphdb

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// IntegrityValidator provides proactive corruption detection for VectorGraphDB.
// It runs invariant checks to detect and optionally repair data inconsistencies.
type IntegrityValidator struct {
	db     *VectorGraphDB
	logger *slog.Logger
	config IntegrityConfig
}

// NewIntegrityValidator creates a new IntegrityValidator for the given database.
func NewIntegrityValidator(db *VectorGraphDB, cfg IntegrityConfig, logger *slog.Logger) *IntegrityValidator {
	if logger == nil {
		logger = slog.Default()
	}
	return &IntegrityValidator{
		db:     db,
		logger: logger,
		config: cfg,
	}
}

// ValidateAll runs all standard invariant checks and returns any violations found.
func (v *IntegrityValidator) ValidateAll() (*ValidationResult, error) {
	return v.ValidateChecks(StandardChecks())
}

// ValidateChecks runs the specified invariant checks and returns any violations found.
func (v *IntegrityValidator) ValidateChecks(checks []InvariantCheck) (*ValidationResult, error) {
	start := time.Now()
	result := &ValidationResult{
		Violations: make([]Violation, 0),
		Timestamp:  start,
	}

	for _, check := range checks {
		violations, err := v.runCheck(check)
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", check.Name, err)
		}
		result.Violations = append(result.Violations, violations...)
		result.ChecksRun++
	}

	result.Duration = time.Since(start)
	v.logViolations(result.Violations)
	return result, nil
}

// runCheck executes a single invariant check and returns any violations found.
func (v *IntegrityValidator) runCheck(check InvariantCheck) ([]Violation, error) {
	rows, err := v.db.db.Query(check.Query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var violations []Violation
	for rows.Next() {
		var entityID string
		if err := rows.Scan(&entityID); err != nil {
			return nil, err
		}
		violations = append(violations, Violation{
			Check:       check.Name,
			EntityID:    entityID,
			Description: check.Description,
			Severity:    check.Severity,
		})
	}
	return violations, rows.Err()
}

// ValidateSample runs checks on a random sample of nodes based on SampleSize config.
func (v *IntegrityValidator) ValidateSample() (*ValidationResult, error) {
	start := time.Now()
	result := &ValidationResult{
		Violations: make([]Violation, 0),
		Timestamp:  start,
	}

	sampleChecks := v.buildSampleChecks()
	for _, check := range sampleChecks {
		violations, err := v.runCheck(check)
		if err != nil {
			return nil, fmt.Errorf("sample check %s: %w", check.Name, err)
		}
		result.Violations = append(result.Violations, violations...)
		result.ChecksRun++
	}

	result.TotalChecked = v.config.SampleSize
	result.Duration = time.Since(start)
	v.logViolations(result.Violations)
	return result, nil
}

// buildSampleChecks returns checks modified to only operate on a sample of nodes.
func (v *IntegrityValidator) buildSampleChecks() []InvariantCheck {
	return []InvariantCheck{
		{
			Name:        "sample_orphaned_vectors",
			Description: "Vectors without corresponding nodes (sampled)",
			Query: fmt.Sprintf(`SELECT node_id FROM vectors
				WHERE node_id IN (SELECT id FROM nodes ORDER BY RANDOM() LIMIT %d)
				AND node_id NOT IN (SELECT id FROM nodes)`, v.config.SampleSize),
			Severity: SeverityError,
		},
		{
			Name:        "sample_dimension_mismatch",
			Description: "Vectors with incorrect embedding dimensions (sampled)",
			Query: fmt.Sprintf(`SELECT node_id FROM vectors
				WHERE node_id IN (SELECT id FROM nodes ORDER BY RANDOM() LIMIT %d)
				AND dimensions != %d`, v.config.SampleSize, EmbeddingDimension),
			Severity: SeverityCritical,
		},
	}
}

// PeriodicValidation runs validation at configured intervals until context is cancelled.
func (v *IntegrityValidator) PeriodicValidation(ctx context.Context) {
	ticker := time.NewTicker(v.config.PeriodicInterval)
	defer ticker.Stop()

	v.logger.Info("starting periodic validation", "interval", v.config.PeriodicInterval)

	for {
		select {
		case <-ctx.Done():
			v.logger.Info("periodic validation stopped")
			return
		case <-ticker.C:
			v.runPeriodicCheck()
		}
	}
}

// runPeriodicCheck performs a single periodic validation run.
func (v *IntegrityValidator) runPeriodicCheck() {
	result, err := v.ValidateSample()
	if err != nil {
		v.logger.Error("periodic validation failed", "error", err)
		return
	}
	v.logger.Info("periodic validation complete",
		"checks_run", result.ChecksRun,
		"violations", len(result.Violations),
		"duration", result.Duration,
	)
}

// logViolations logs each violation at the appropriate level based on severity.
func (v *IntegrityValidator) logViolations(violations []Violation) {
	for _, violation := range violations {
		v.logViolation(violation)
	}
}

// logViolation logs a single violation at the appropriate level.
func (v *IntegrityValidator) logViolation(violation Violation) {
	attrs := []any{
		"check", violation.Check,
		"entity_id", violation.EntityID,
		"description", violation.Description,
	}

	switch violation.Severity {
	case SeverityWarning:
		v.logger.Warn("integrity violation", attrs...)
	case SeverityError:
		v.logger.Error("integrity violation", attrs...)
	case SeverityCritical:
		v.logger.Error("CRITICAL integrity violation", attrs...)
	}
}

// =============================================================================
// Auto-Repair Methods
// =============================================================================

// RepairViolations attempts to fix violations based on InvariantCheck.Repair SQL.
// Returns the list of violations that could not be repaired.
func (v *IntegrityValidator) RepairViolations(violations []Violation) []Violation {
	if !v.config.AutoRepair {
		return violations
	}

	var unrepaired []Violation
	for i := range violations {
		if !v.tryRepairViolation(&violations[i]) {
			unrepaired = append(unrepaired, violations[i])
		}
	}
	return unrepaired
}

// tryRepairViolation attempts to repair a single violation.
// Returns true if the violation was successfully repaired.
func (v *IntegrityValidator) tryRepairViolation(violation *Violation) bool {
	check, ok := GetCheckByName(violation.Check)
	if !ok || check.Repair == "" {
		return false
	}

	if err := v.executeRepair(check); err != nil {
		v.logger.Error("repair failed", "check", violation.Check, "error", err)
		return false
	}

	violation.Repaired = true
	v.logger.Info("repaired violation", "check", violation.Check, "entity", violation.EntityID)
	return true
}

// executeRepair runs the repair SQL for a check.
func (v *IntegrityValidator) executeRepair(check InvariantCheck) error {
	_, err := v.db.db.Exec(check.Repair)
	return err
}

// ValidateAndRepair runs validation then attempts repairs.
// Returns the result with only unrepaired violations remaining.
func (v *IntegrityValidator) ValidateAndRepair() (*ValidationResult, error) {
	return v.ValidateAndRepairWithChecks(StandardChecks())
}

// ValidateAndRepairWithChecks runs the specified checks then attempts repairs.
// Returns the result with only unrepaired violations remaining.
func (v *IntegrityValidator) ValidateAndRepairWithChecks(checks []InvariantCheck) (*ValidationResult, error) {
	result, err := v.ValidateChecks(checks)
	if err != nil {
		return nil, err
	}

	originalCount := len(result.Violations)
	unrepaired := v.RepairViolations(result.Violations)
	result.Violations = unrepaired
	result.RepairedCount = originalCount - len(unrepaired)

	return result, nil
}

// =============================================================================
// Quarantine Methods
// =============================================================================

// QuarantineEntity marks an entity as quarantined for manual review.
// The quarantine table is created if it doesn't exist.
func (v *IntegrityValidator) QuarantineEntity(entityID, reason string) error {
	if err := v.ensureQuarantineTable(); err != nil {
		return err
	}
	return v.insertQuarantineRecord(entityID, reason)
}

// ensureQuarantineTable creates the quarantine table if it doesn't exist.
func (v *IntegrityValidator) ensureQuarantineTable() error {
	query := `CREATE TABLE IF NOT EXISTS quarantine (
		entity_id TEXT PRIMARY KEY,
		reason TEXT NOT NULL,
		quarantined_at TEXT NOT NULL
	)`
	_, err := v.db.db.Exec(query)
	return err
}

// insertQuarantineRecord inserts or updates a quarantine record.
func (v *IntegrityValidator) insertQuarantineRecord(entityID, reason string) error {
	query := `INSERT INTO quarantine (entity_id, reason, quarantined_at)
		VALUES (?, ?, ?)
		ON CONFLICT(entity_id) DO UPDATE SET reason = excluded.reason, quarantined_at = excluded.quarantined_at`
	_, err := v.db.db.Exec(query, entityID, reason, time.Now().Format(time.RFC3339))
	return err
}

// IsQuarantined checks if an entity is in the quarantine table.
func (v *IntegrityValidator) IsQuarantined(entityID string) (bool, error) {
	if err := v.ensureQuarantineTable(); err != nil {
		return false, err
	}

	var count int
	err := v.db.db.QueryRow("SELECT COUNT(*) FROM quarantine WHERE entity_id = ?", entityID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// RemoveFromQuarantine removes an entity from quarantine.
func (v *IntegrityValidator) RemoveFromQuarantine(entityID string) error {
	if err := v.ensureQuarantineTable(); err != nil {
		return err
	}
	_, err := v.db.db.Exec("DELETE FROM quarantine WHERE entity_id = ?", entityID)
	return err
}

// GetQuarantinedEntities returns all quarantined entity IDs.
func (v *IntegrityValidator) GetQuarantinedEntities() ([]string, error) {
	if err := v.ensureQuarantineTable(); err != nil {
		return nil, err
	}
	return v.queryQuarantinedEntities()
}

// queryQuarantinedEntities executes the query to retrieve quarantined entity IDs.
func (v *IntegrityValidator) queryQuarantinedEntities() ([]string, error) {
	rows, err := v.db.db.Query("SELECT entity_id FROM quarantine")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return v.scanEntityIDs(rows)
}

// scanEntityIDs scans rows and returns a slice of entity IDs.
func (v *IntegrityValidator) scanEntityIDs(rows *sql.Rows) ([]string, error) {
	var entities []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		entities = append(entities, id)
	}
	return entities, rows.Err()
}
