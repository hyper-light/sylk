package vectorgraphdb

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupValidatorTestDB(t *testing.T) (*VectorGraphDB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "validator_test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Create hnsw_nodes table for testing (not in main schema)
	_, err = db.DB().Exec(`CREATE TABLE IF NOT EXISTS hnsw_nodes (
		node_id TEXT PRIMARY KEY
	)`)
	if err != nil {
		t.Fatalf("failed to create hnsw_nodes table: %v", err)
	}

	return db, dbPath
}

// setupValidatorTestDBNoFK creates a test DB with foreign keys disabled for orphan testing.
func setupValidatorTestDBNoFK(t *testing.T) (*VectorGraphDB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "validator_test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Disable foreign keys for this connection to allow inserting orphan data
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

	return db, dbPath
}

// testableChecks returns checks that work with SQLite (excludes PostgreSQL-specific queries).
func testableChecks() []InvariantCheck {
	var checks []InvariantCheck
	for _, check := range StandardChecks() {
		// Skip superseded_cycle which uses PostgreSQL's ANY() function
		if check.Name == "superseded_cycle" {
			continue
		}
		checks = append(checks, check)
	}
	return checks
}

func TestNewIntegrityValidator(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := DefaultIntegrityConfig()

	t.Run("with logger", func(t *testing.T) {
		logger := slog.Default()
		v := NewIntegrityValidator(db, cfg, logger)
		if v == nil {
			t.Fatal("expected non-nil validator")
		}
		if v.db != db {
			t.Error("db not set correctly")
		}
		if v.logger != logger {
			t.Error("logger not set correctly")
		}
	})

	t.Run("with nil logger", func(t *testing.T) {
		v := NewIntegrityValidator(db, cfg, nil)
		if v == nil {
			t.Fatal("expected non-nil validator")
		}
		if v.logger == nil {
			t.Error("expected default logger to be set")
		}
	})
}

func TestValidateAllReturnsEmptyForCleanDB(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, slog.Default())

	// Use testable checks to avoid PostgreSQL-specific syntax
	result, err := v.ValidateChecks(testableChecks())
	if err != nil {
		t.Fatalf("ValidateChecks: %v", err)
	}

	if len(result.Violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(result.Violations))
	}

	if result.ChecksRun == 0 {
		t.Error("expected at least one check to run")
	}

	if result.Duration == 0 {
		t.Error("expected non-zero duration")
	}

	if result.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestValidateAllReturnsViolations(t *testing.T) {
	db, path := setupValidatorTestDBNoFK(t)
	defer cleanupDB(db, path)

	// Insert an orphaned vector (no corresponding node) - FK disabled
	_, err := db.DB().Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES ('orphan1', X'00000000', 1.0, 768, 0, 0)`)
	if err != nil {
		t.Fatalf("failed to insert orphaned vector: %v", err)
	}

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, slog.Default())

	// Use testable checks to avoid PostgreSQL-specific syntax
	result, err := v.ValidateChecks(testableChecks())
	if err != nil {
		t.Fatalf("ValidateChecks: %v", err)
	}

	if len(result.Violations) == 0 {
		t.Fatal("expected at least one violation")
	}

	foundOrphan := false
	for _, violation := range result.Violations {
		if violation.Check == "orphaned_vectors" && violation.EntityID == "orphan1" {
			foundOrphan = true
			if violation.Severity != SeverityError {
				t.Errorf("expected severity Error, got %v", violation.Severity)
			}
		}
	}

	if !foundOrphan {
		t.Error("expected to find orphaned_vectors violation for orphan1")
	}
}

func TestValidateSampleRespectsSampleSize(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := IntegrityConfig{
		SampleSize:       10,
		PeriodicInterval: time.Minute,
	}
	v := NewIntegrityValidator(db, cfg, slog.Default())

	result, err := v.ValidateSample()
	if err != nil {
		t.Fatalf("ValidateSample: %v", err)
	}

	if result.TotalChecked != cfg.SampleSize {
		t.Errorf("expected TotalChecked=%d, got %d", cfg.SampleSize, result.TotalChecked)
	}

	if result.ChecksRun == 0 {
		t.Error("expected at least one sample check to run")
	}
}

func TestPeriodicValidationRunsAtInterval(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := IntegrityConfig{
		SampleSize:       5,
		PeriodicInterval: 50 * time.Millisecond,
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	v := NewIntegrityValidator(db, cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		v.PeriodicValidation(ctx)
		close(done)
	}()

	<-done

	output := buf.String()
	if !strings.Contains(output, "periodic validation complete") {
		t.Error("expected periodic validation to log completion")
	}
}

func TestPeriodicValidationStopsOnCancel(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := IntegrityConfig{
		SampleSize:       5,
		PeriodicInterval: time.Hour, // long interval
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	v := NewIntegrityValidator(db, cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		v.PeriodicValidation(ctx)
		close(done)
	}()

	// Cancel immediately
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Success - periodic validation stopped
	case <-time.After(time.Second):
		t.Fatal("periodic validation did not stop on context cancel")
	}

	output := buf.String()
	if !strings.Contains(output, "periodic validation stopped") {
		t.Error("expected periodic validation to log stopping")
	}
}

func TestSeverityLevelsLoggedCorrectly(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	tests := []struct {
		name     string
		severity Severity
		expected string
	}{
		{"warning", SeverityWarning, "level=WARN"},
		{"error", SeverityError, "level=ERROR"},
		{"critical", SeverityCritical, "level=ERROR"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))

			cfg := DefaultIntegrityConfig()
			v := NewIntegrityValidator(db, cfg, logger)

			violation := Violation{
				Check:       "test_check",
				EntityID:    "entity1",
				Description: "Test violation",
				Severity:    tc.severity,
			}

			v.logViolation(violation)

			output := buf.String()
			if !strings.Contains(output, tc.expected) {
				t.Errorf("expected log level %q in output: %s", tc.expected, output)
			}
		})
	}
}

func TestRunCheckWithOrphanedEdgesSource(t *testing.T) {
	db, path := setupValidatorTestDBNoFK(t)
	defer cleanupDB(db, path)

	// Insert an edge with non-existent source (FK disabled)
	_, err := db.DB().Exec(`INSERT INTO edges (source_id, target_id, edge_type)
		VALUES ('nonexistent', 'target1', 0)`)
	if err != nil {
		t.Fatalf("failed to insert edge: %v", err)
	}

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, slog.Default())

	check, _ := GetCheckByName("orphaned_edges_source")
	violations, err := v.runCheck(check)
	if err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}

	if violations[0].EntityID != "1" { // edge id is 1
		t.Errorf("expected entity ID '1', got %q", violations[0].EntityID)
	}
}

func TestRunCheckWithOrphanedEdgesTarget(t *testing.T) {
	db, path := setupValidatorTestDBNoFK(t)
	defer cleanupDB(db, path)

	// Insert an edge with non-existent target (FK disabled)
	_, err := db.DB().Exec(`INSERT INTO edges (source_id, target_id, edge_type)
		VALUES ('source1', 'nonexistent', 0)`)
	if err != nil {
		t.Fatalf("failed to insert edge: %v", err)
	}

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, slog.Default())

	check, _ := GetCheckByName("orphaned_edges_target")
	violations, err := v.runCheck(check)
	if err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
}

func TestRunCheckWithInvalidHNSWEntry(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	// Insert an HNSW entry pointing to non-existent node
	_, err := db.DB().Exec(`INSERT INTO hnsw_nodes (node_id) VALUES ('nonexistent_node')`)
	if err != nil {
		t.Fatalf("failed to insert hnsw_nodes entry: %v", err)
	}

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, slog.Default())

	check, _ := GetCheckByName("invalid_hnsw_entry")
	violations, err := v.runCheck(check)
	if err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}

	if violations[0].Severity != SeverityCritical {
		t.Errorf("expected severity Critical, got %v", violations[0].Severity)
	}
}

func TestRunCheckWithDimensionMismatch(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	// Insert a node
	_, err := db.DB().Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'test')`)
	if err != nil {
		t.Fatalf("failed to insert node: %v", err)
	}

	// Insert vector with wrong dimensions
	_, err = db.DB().Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES ('node1', X'00000000', 1.0, 512, 0, 0)`)
	if err != nil {
		t.Fatalf("failed to insert vector: %v", err)
	}

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, slog.Default())

	check, _ := GetCheckByName("dimension_mismatch")
	violations, err := v.runCheck(check)
	if err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}

	if violations[0].Severity != SeverityCritical {
		t.Errorf("expected severity Critical, got %v", violations[0].Severity)
	}
}

func TestRunCheckWithOrphanedProvenance(t *testing.T) {
	db, path := setupValidatorTestDBNoFK(t)
	defer cleanupDB(db, path)

	// Insert orphaned provenance record (FK disabled)
	_, err := db.DB().Exec(`INSERT INTO provenance (node_id, source_type, confidence)
		VALUES ('nonexistent_node', 0, 0.9)`)
	if err != nil {
		t.Fatalf("failed to insert provenance: %v", err)
	}

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, slog.Default())

	check, _ := GetCheckByName("orphaned_provenance")
	violations, err := v.runCheck(check)
	if err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}

	if violations[0].Severity != SeverityWarning {
		t.Errorf("expected severity Warning, got %v", violations[0].Severity)
	}
}

func TestValidateAllDuration(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, slog.Default())

	start := time.Now()
	// Use testable checks to avoid PostgreSQL-specific syntax
	result, err := v.ValidateChecks(testableChecks())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ValidateChecks: %v", err)
	}

	// Duration should be reasonable
	if result.Duration < 0 {
		t.Error("duration should not be negative")
	}

	if result.Duration > elapsed {
		t.Error("result duration should not exceed elapsed time")
	}
}

func TestValidateSampleEmptyDB(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := IntegrityConfig{
		SampleSize:       100,
		PeriodicInterval: time.Minute,
	}
	v := NewIntegrityValidator(db, cfg, slog.Default())

	result, err := v.ValidateSample()
	if err != nil {
		t.Fatalf("ValidateSample: %v", err)
	}

	if len(result.Violations) != 0 {
		t.Errorf("expected 0 violations on empty DB, got %d", len(result.Violations))
	}
}

func TestBuildSampleChecks(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := IntegrityConfig{
		SampleSize:       50,
		PeriodicInterval: time.Minute,
	}
	v := NewIntegrityValidator(db, cfg, slog.Default())

	checks := v.buildSampleChecks()

	if len(checks) < 2 {
		t.Fatalf("expected at least 2 sample checks, got %d", len(checks))
	}

	// Verify sample size is in the queries
	for _, check := range checks {
		if !strings.Contains(check.Query, "50") {
			t.Errorf("expected sample size 50 in query for %s", check.Name)
		}
	}
}

func TestLogViolationsMultiple(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	cfg := DefaultIntegrityConfig()
	v := NewIntegrityValidator(db, cfg, logger)

	violations := []Violation{
		{Check: "check1", EntityID: "e1", Description: "desc1", Severity: SeverityWarning},
		{Check: "check2", EntityID: "e2", Description: "desc2", Severity: SeverityError},
		{Check: "check3", EntityID: "e3", Description: "desc3", Severity: SeverityCritical},
	}

	v.logViolations(violations)

	output := buf.String()
	if !strings.Contains(output, "check1") || !strings.Contains(output, "e1") {
		t.Error("expected first violation to be logged")
	}
	if !strings.Contains(output, "check2") || !strings.Contains(output, "e2") {
		t.Error("expected second violation to be logged")
	}
	if !strings.Contains(output, "check3") || !strings.Contains(output, "e3") {
		t.Error("expected third violation to be logged")
	}
}

func TestPeriodicValidationStartsLogging(t *testing.T) {
	db, path := setupValidatorTestDB(t)
	defer cleanupDB(db, path)

	cfg := IntegrityConfig{
		SampleSize:       5,
		PeriodicInterval: time.Hour,
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	v := NewIntegrityValidator(db, cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		v.PeriodicValidation(ctx)
		close(done)
	}()

	// Give it a moment to start
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done

	output := buf.String()
	if !strings.Contains(output, "starting periodic validation") {
		t.Error("expected starting log message")
	}
}
