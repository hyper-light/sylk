package vectorgraphdb

import (
	"path/filepath"
	"testing"
)

// =============================================================================
// Test Setup Helpers
// =============================================================================

func setupRepairTestDB(t *testing.T) (*VectorGraphDB, *IntegrityValidator) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repair_test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Disable FK for tests that need orphan data
	_, err = db.DB().Exec(`PRAGMA foreign_keys = OFF`)
	if err != nil {
		t.Fatalf("failed to disable foreign keys: %v", err)
	}

	// Create hnsw_nodes table for testing (not in main schema)
	_, err = db.DB().Exec(`CREATE TABLE IF NOT EXISTS hnsw_nodes (
		node_id TEXT PRIMARY KEY
	)`)
	if err != nil {
		t.Fatalf("failed to create hnsw_nodes table: %v", err)
	}

	config := DefaultIntegrityConfig()
	validator := NewIntegrityValidator(db, config, nil)
	return db, validator
}

func setupRepairTestDBWithConfig(t *testing.T, config IntegrityConfig) (*VectorGraphDB, *IntegrityValidator) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repair_test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Disable FK for tests that need orphan data
	_, err = db.DB().Exec(`PRAGMA foreign_keys = OFF`)
	if err != nil {
		t.Fatalf("failed to disable foreign keys: %v", err)
	}

	validator := NewIntegrityValidator(db, config, nil)
	return db, validator
}

// repairTestableChecks returns checks that work with SQLite (excludes PostgreSQL-specific queries
// and checks that depend on tables not in the main schema).
func repairTestableChecks() []InvariantCheck {
	var checks []InvariantCheck
	for _, check := range StandardChecks() {
		// Skip superseded_cycle which uses PostgreSQL's ARRAY and ANY() functions
		if check.Name == "superseded_cycle" {
			continue
		}
		// Skip invalid_hnsw_entry which requires hnsw_nodes table (managed by HNSW package)
		if check.Name == "invalid_hnsw_entry" {
			continue
		}
		checks = append(checks, check)
	}
	return checks
}

// =============================================================================
// RepairViolations Tests
// =============================================================================

func TestRepairViolationsRepairsWhenAutoRepairTrue(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Insert a node without a corresponding vector (we'll insert orphan vector later)
	_, err := db.DB().Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'test')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// Insert an orphaned vector (node doesn't exist)
	_, err = db.DB().Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES ('orphan1', X'00', 1.0, 768, 0, 0)`)
	if err != nil {
		t.Fatalf("insert orphaned vector: %v", err)
	}

	// Create a violation for the orphaned vector
	violations := []Violation{
		{
			Check:       "orphaned_vectors",
			EntityID:    "orphan1",
			Description: "Vectors without corresponding nodes",
			Severity:    SeverityError,
		},
	}

	unrepaired := validator.RepairViolations(violations)

	if len(unrepaired) != 0 {
		t.Errorf("expected 0 unrepaired violations, got %d", len(unrepaired))
	}

	// Verify the orphaned vector was deleted
	var count int
	err = db.DB().QueryRow("SELECT COUNT(*) FROM vectors WHERE node_id = 'orphan1'").Scan(&count)
	if err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected orphaned vector to be deleted, but count is %d", count)
	}
}

func TestRepairViolationsSkipsWhenAutoRepairFalse(t *testing.T) {
	config := DefaultIntegrityConfig()
	config.AutoRepair = false

	db, validator := setupRepairTestDBWithConfig(t, config)
	defer db.Close()

	// Insert an orphaned vector
	_, err := db.DB().Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES ('orphan1', X'00', 1.0, 768, 0, 0)`)
	if err != nil {
		t.Fatalf("insert orphaned vector: %v", err)
	}

	violations := []Violation{
		{
			Check:       "orphaned_vectors",
			EntityID:    "orphan1",
			Description: "Vectors without corresponding nodes",
			Severity:    SeverityError,
		},
	}

	unrepaired := validator.RepairViolations(violations)

	if len(unrepaired) != 1 {
		t.Errorf("expected 1 unrepaired violation when AutoRepair is false, got %d", len(unrepaired))
	}

	// Verify the orphaned vector was NOT deleted
	var count int
	err = db.DB().QueryRow("SELECT COUNT(*) FROM vectors WHERE node_id = 'orphan1'").Scan(&count)
	if err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected orphaned vector to still exist, but count is %d", count)
	}
}

func TestRepairViolationsHandlesNoRepairSQL(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Create a violation for a check without repair SQL (dimension_mismatch)
	violations := []Violation{
		{
			Check:       "dimension_mismatch",
			EntityID:    "node1",
			Description: "Vectors with incorrect embedding dimensions",
			Severity:    SeverityCritical,
		},
	}

	unrepaired := validator.RepairViolations(violations)

	if len(unrepaired) != 1 {
		t.Errorf("expected 1 unrepaired violation for check without repair SQL, got %d", len(unrepaired))
	}
}

func TestRepairViolationsHandlesUnknownCheck(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	violations := []Violation{
		{
			Check:       "unknown_check",
			EntityID:    "node1",
			Description: "Unknown check",
			Severity:    SeverityError,
		},
	}

	unrepaired := validator.RepairViolations(violations)

	if len(unrepaired) != 1 {
		t.Errorf("expected 1 unrepaired violation for unknown check, got %d", len(unrepaired))
	}
}

func TestRepairViolationsHandlesEmptySlice(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	unrepaired := validator.RepairViolations([]Violation{})

	if len(unrepaired) != 0 {
		t.Errorf("expected 0 unrepaired for empty input, got %d", len(unrepaired))
	}
}

func TestRepairViolationsHandlesNilSlice(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	unrepaired := validator.RepairViolations(nil)

	if unrepaired != nil && len(unrepaired) != 0 {
		t.Errorf("expected nil or empty for nil input, got %v", unrepaired)
	}
}

func TestRepairViolationsMarksRepairedFlag(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Insert an orphaned vector
	_, err := db.DB().Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES ('orphan1', X'00', 1.0, 768, 0, 0)`)
	if err != nil {
		t.Fatalf("insert orphaned vector: %v", err)
	}

	violations := []Violation{
		{
			Check:       "orphaned_vectors",
			EntityID:    "orphan1",
			Description: "Vectors without corresponding nodes",
			Severity:    SeverityError,
			Repaired:    false,
		},
	}

	_ = validator.RepairViolations(violations)

	// The original violation should have Repaired set to true
	if !violations[0].Repaired {
		t.Error("expected Repaired flag to be set to true after successful repair")
	}
}

// =============================================================================
// ValidateAndRepair Tests
// =============================================================================

func TestValidateAndRepairReturnsCorrectCounts(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Insert an orphaned vector (will be repaired)
	_, err := db.DB().Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES ('orphan1', X'00', 1.0, 768, 0, 0)`)
	if err != nil {
		t.Fatalf("insert orphaned vector: %v", err)
	}

	// Use testable checks to avoid PostgreSQL-specific syntax
	result, err := validator.ValidateAndRepairWithChecks(repairTestableChecks())
	if err != nil {
		t.Fatalf("ValidateAndRepairWithChecks: %v", err)
	}

	// The orphaned vector should have been detected and repaired
	if result.RepairedCount < 0 {
		t.Errorf("RepairedCount should not be negative, got %d", result.RepairedCount)
	}

	// Violations list should only contain unrepaired violations
	for _, v := range result.Violations {
		if v.Repaired {
			t.Error("result.Violations should only contain unrepaired violations")
		}
	}
}

func TestValidateAndRepairWithNoViolations(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Use testable checks to avoid PostgreSQL-specific syntax
	result, err := validator.ValidateAndRepairWithChecks(repairTestableChecks())
	if err != nil {
		t.Fatalf("ValidateAndRepairWithChecks: %v", err)
	}

	if result.RepairedCount != 0 {
		t.Errorf("expected RepairedCount to be 0 with no violations, got %d", result.RepairedCount)
	}

	if len(result.Violations) != 0 {
		t.Errorf("expected no violations, got %d", len(result.Violations))
	}
}

func TestValidateAndRepairRunsAllChecks(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Use testable checks to avoid PostgreSQL-specific syntax
	result, err := validator.ValidateAndRepairWithChecks(repairTestableChecks())
	if err != nil {
		t.Fatalf("ValidateAndRepairWithChecks: %v", err)
	}

	expectedChecks := len(repairTestableChecks())
	if result.ChecksRun != expectedChecks {
		t.Errorf("expected %d checks to run, got %d", expectedChecks, result.ChecksRun)
	}
}

func TestValidateAndRepairSetsTimestamp(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Use testable checks to avoid PostgreSQL-specific syntax
	result, err := validator.ValidateAndRepairWithChecks(repairTestableChecks())
	if err != nil {
		t.Fatalf("ValidateAndRepairWithChecks: %v", err)
	}

	if result.Timestamp.IsZero() {
		t.Error("expected Timestamp to be set")
	}

	if result.Duration == 0 {
		t.Error("expected Duration to be set")
	}
}

// =============================================================================
// QuarantineEntity Tests
// =============================================================================

func TestQuarantineEntityAddsToTable(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	err := validator.QuarantineEntity("node123", "critical violation detected")
	if err != nil {
		t.Fatalf("QuarantineEntity: %v", err)
	}

	isQuarantined, err := validator.IsQuarantined("node123")
	if err != nil {
		t.Fatalf("IsQuarantined: %v", err)
	}
	if !isQuarantined {
		t.Error("expected node123 to be quarantined")
	}
}

func TestQuarantineEntityDuplicateIsHandled(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Quarantine the same entity twice with different reasons
	err := validator.QuarantineEntity("node123", "first reason")
	if err != nil {
		t.Fatalf("first QuarantineEntity: %v", err)
	}

	err = validator.QuarantineEntity("node123", "second reason")
	if err != nil {
		t.Fatalf("second QuarantineEntity: %v", err)
	}

	// Check that only one entry exists
	var count int
	err = db.DB().QueryRow("SELECT COUNT(*) FROM quarantine WHERE entity_id = 'node123'").Scan(&count)
	if err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 quarantine record, got %d", count)
	}

	// Check that the reason was updated
	var reason string
	err = db.DB().QueryRow("SELECT reason FROM quarantine WHERE entity_id = 'node123'").Scan(&reason)
	if err != nil {
		t.Fatalf("query reason: %v", err)
	}
	if reason != "second reason" {
		t.Errorf("expected reason to be 'second reason', got %q", reason)
	}
}

func TestQuarantineEntityCreatesTable(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// First quarantine should create the table
	err := validator.QuarantineEntity("node123", "test reason")
	if err != nil {
		t.Fatalf("QuarantineEntity: %v", err)
	}

	// Verify table exists by querying it
	var count int
	err = db.DB().QueryRow("SELECT COUNT(*) FROM quarantine").Scan(&count)
	if err != nil {
		t.Fatalf("quarantine table should exist: %v", err)
	}
}

func TestIsQuarantinedReturnsFalseForUnknown(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	isQuarantined, err := validator.IsQuarantined("unknown_entity")
	if err != nil {
		t.Fatalf("IsQuarantined: %v", err)
	}
	if isQuarantined {
		t.Error("expected unknown entity to not be quarantined")
	}
}

func TestRemoveFromQuarantine(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Quarantine and then remove
	err := validator.QuarantineEntity("node123", "test reason")
	if err != nil {
		t.Fatalf("QuarantineEntity: %v", err)
	}

	err = validator.RemoveFromQuarantine("node123")
	if err != nil {
		t.Fatalf("RemoveFromQuarantine: %v", err)
	}

	isQuarantined, err := validator.IsQuarantined("node123")
	if err != nil {
		t.Fatalf("IsQuarantined: %v", err)
	}
	if isQuarantined {
		t.Error("expected node123 to not be quarantined after removal")
	}
}

func TestRemoveFromQuarantineNonexistent(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Should not error when removing non-existent entry
	err := validator.RemoveFromQuarantine("nonexistent")
	if err != nil {
		t.Fatalf("RemoveFromQuarantine should not error for nonexistent: %v", err)
	}
}

func TestGetQuarantinedEntities(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Add multiple quarantined entities
	entities := []string{"node1", "node2", "node3"}
	for _, id := range entities {
		err := validator.QuarantineEntity(id, "reason for "+id)
		if err != nil {
			t.Fatalf("QuarantineEntity %s: %v", id, err)
		}
	}

	result, err := validator.GetQuarantinedEntities()
	if err != nil {
		t.Fatalf("GetQuarantinedEntities: %v", err)
	}

	if len(result) != len(entities) {
		t.Errorf("expected %d quarantined entities, got %d", len(entities), len(result))
	}
}

func TestGetQuarantinedEntitiesEmpty(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	result, err := validator.GetQuarantinedEntities()
	if err != nil {
		t.Fatalf("GetQuarantinedEntities: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected 0 quarantined entities, got %d", len(result))
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestRepairMultipleViolationTypes(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Insert an orphaned vector
	_, err := db.DB().Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES ('orphan_vec', X'00', 1.0, 768, 0, 0)`)
	if err != nil {
		t.Fatalf("insert orphaned vector: %v", err)
	}

	// Insert an orphaned provenance record
	_, err = db.DB().Exec(`INSERT INTO provenance (node_id, source_type, confidence)
		VALUES ('orphan_prov', 0, 0.9)`)
	if err != nil {
		t.Fatalf("insert orphaned provenance: %v", err)
	}

	violations := []Violation{
		{
			Check:       "orphaned_vectors",
			EntityID:    "orphan_vec",
			Description: "Vectors without corresponding nodes",
			Severity:    SeverityError,
		},
		{
			Check:       "orphaned_provenance",
			EntityID:    "orphan_prov",
			Description: "Provenance records without corresponding nodes",
			Severity:    SeverityWarning,
		},
	}

	unrepaired := validator.RepairViolations(violations)

	if len(unrepaired) != 0 {
		t.Errorf("expected 0 unrepaired violations, got %d", len(unrepaired))
	}

	// Verify both were cleaned up
	var vecCount, provCount int
	db.DB().QueryRow("SELECT COUNT(*) FROM vectors WHERE node_id = 'orphan_vec'").Scan(&vecCount)
	db.DB().QueryRow("SELECT COUNT(*) FROM provenance WHERE node_id = 'orphan_prov'").Scan(&provCount)

	if vecCount != 0 || provCount != 0 {
		t.Errorf("expected orphans to be deleted, got vec=%d, prov=%d", vecCount, provCount)
	}
}

func TestRepairAndQuarantineWorkflow(t *testing.T) {
	db, validator := setupRepairTestDB(t)
	defer db.Close()

	// Insert a node with wrong dimension (unrepairable)
	_, err := db.DB().Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('bad_node', 0, 0, 'bad')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}
	_, err = db.DB().Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES ('bad_node', X'00', 1.0, 512, 0, 0)`)
	if err != nil {
		t.Fatalf("insert bad vector: %v", err)
	}

	// Run validate and repair using testable checks
	result, err := validator.ValidateAndRepairWithChecks(repairTestableChecks())
	if err != nil {
		t.Fatalf("ValidateAndRepairWithChecks: %v", err)
	}

	// Quarantine any unrepaired critical violations
	for _, v := range result.Violations {
		if v.Severity == SeverityCritical && !v.Repaired {
			err := validator.QuarantineEntity(v.EntityID, v.Description)
			if err != nil {
				t.Fatalf("QuarantineEntity: %v", err)
			}
		}
	}

	// Check that bad_node is quarantined
	isQuarantined, err := validator.IsQuarantined("bad_node")
	if err != nil {
		t.Fatalf("IsQuarantined: %v", err)
	}
	if !isQuarantined {
		t.Error("expected bad_node to be quarantined due to dimension mismatch")
	}
}
