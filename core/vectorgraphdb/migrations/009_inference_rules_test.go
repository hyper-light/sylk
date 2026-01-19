package migrations

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// createInferenceTestDB creates a temporary SQLite database.
func createInferenceTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "inference_migration_test_*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("sqlite3", tmpFile.Name()+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

// createInferenceBaseSchema creates edges table for FK references.
func createInferenceBaseSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			edge_type INTEGER NOT NULL,
			weight REAL DEFAULT 1.0,
			metadata JSON,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE
		)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func TestInferenceRulesMigration_Apply_HappyPath(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	migration := NewInferenceRulesMigration(db)

	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify inference_rules table exists
	if !hasTable(t, db, "inference_rules") {
		t.Error("inference_rules table not created")
	}

	// Verify materialized_edges table exists
	if !hasTable(t, db, "materialized_edges") {
		t.Error("materialized_edges table not created")
	}

	// Verify inference_rules columns
	ruleColumns := []string{
		"id", "name", "head_subject", "head_predicate",
		"head_object", "body_json", "priority", "enabled", "created_at",
	}
	for _, col := range ruleColumns {
		if !hasInferenceRuleColumn(t, db, col) {
			t.Errorf("column %s not found in inference_rules table", col)
		}
	}

	// Verify materialized_edges columns
	edgeColumns := []string{"id", "rule_id", "edge_id", "derived_at"}
	for _, col := range edgeColumns {
		if !hasMaterializedEdgeColumn(t, db, col) {
			t.Errorf("column %s not found in materialized_edges table", col)
		}
	}

	// Verify migration was recorded
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	found := false
	for _, r := range records {
		if r.Version == InferenceRulesMigrationVersion {
			found = true
			if r.Name != InferenceRulesMigrationName {
				t.Errorf("expected name %q, got %q", InferenceRulesMigrationName, r.Name)
			}
		}
	}
	if !found {
		t.Error("migration record not found")
	}
}

func TestInferenceRulesMigration_Apply_Idempotent(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	migration := NewInferenceRulesMigration(db)

	// Apply migration twice
	if err := migration.Apply(); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if err := migration.Apply(); err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}

	// Verify only one migration record exists
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}

	count := 0
	for _, r := range records {
		if r.Version == InferenceRulesMigrationVersion {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 migration record, got %d", count)
	}
}

func TestInferenceRulesMigration_IndexesCreated(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	migration := NewInferenceRulesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify indexes exist
	indexes := []string{
		"idx_inference_enabled",
		"idx_materialized_rule",
		"idx_materialized_edge",
	}

	for _, idx := range indexes {
		if !hasIndex(t, db, idx) {
			t.Errorf("index %s not found", idx)
		}
	}
}

func TestInferenceRulesMigration_RulesStored(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	migration := NewInferenceRulesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test rule (Horn clause: calls(X,Y) :- imports(X,Z), defines(Z,Y))
	bodyJSON := `[{"subject":"?X","predicate":"imports","object":"?Z"},{"subject":"?Z","predicate":"defines","object":"?Y"}]`
	_, err := db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"rule1", "Transitive Call",
		"?X", "calls", "?Y",
		bodyJSON, 10, 1,
	)
	if err != nil {
		t.Fatalf("insert rule: %v", err)
	}

	// Query rule
	var name, headPredicate, body string
	var priority, enabled int
	err = db.QueryRow(`
		SELECT name, head_predicate, body_json, priority, enabled
		FROM inference_rules WHERE id = ?`,
		"rule1",
	).Scan(&name, &headPredicate, &body, &priority, &enabled)
	if err != nil {
		t.Fatalf("query rule: %v", err)
	}

	if name != "Transitive Call" {
		t.Errorf("expected name 'Transitive Call', got %q", name)
	}
	if headPredicate != "calls" {
		t.Errorf("expected head_predicate 'calls', got %q", headPredicate)
	}
	if body != bodyJSON {
		t.Errorf("expected body_json %q, got %q", bodyJSON, body)
	}
	if priority != 10 {
		t.Errorf("expected priority 10, got %d", priority)
	}
	if enabled != 1 {
		t.Errorf("expected enabled 1, got %d", enabled)
	}
}

func TestInferenceRulesMigration_EnabledConstraint(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	migration := NewInferenceRulesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert rule with enabled = 0
	_, err := db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"rule1", "Rule1", "?X", "pred", "?Y", "[]", 0, 0,
	)
	if err != nil {
		t.Fatalf("insert rule with enabled=0: %v", err)
	}

	// Insert rule with enabled = 1
	_, err = db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"rule2", "Rule2", "?X", "pred", "?Y", "[]", 0, 1,
	)
	if err != nil {
		t.Fatalf("insert rule with enabled=1: %v", err)
	}

	// Attempt to insert rule with invalid enabled value
	_, err = db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"rule3", "Rule3", "?X", "pred", "?Y", "[]", 0, 2,
	)
	if err == nil {
		t.Error("expected CHECK constraint error for enabled value 2, got nil")
	}
}

func TestInferenceRulesMigration_MaterializedEdgesTracked(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	// Create base schema with edges table
	if err := createInferenceBaseSchema(db); err != nil {
		t.Fatalf("create base schema: %v", err)
	}

	migration := NewInferenceRulesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test rule
	_, err := db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"rule1", "Rule1", "?X", "pred", "?Y", "[]", 0, 1,
	)
	if err != nil {
		t.Fatalf("insert rule: %v", err)
	}

	// Insert test nodes and edge
	_, err = db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES
		('node1', 0, 0, 'Node1'),
		('node2', 0, 0, 'Node2')`)
	if err != nil {
		t.Fatalf("insert nodes: %v", err)
	}

	result, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type)
		VALUES (?, ?, ?)`,
		"node1", "node2", 1,
	)
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	edgeID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get edge ID: %v", err)
	}

	// Track materialized edge
	_, err = db.Exec(`
		INSERT INTO materialized_edges (rule_id, edge_id, derived_at)
		VALUES (?, ?, datetime('now'))`,
		"rule1", edgeID,
	)
	if err != nil {
		t.Fatalf("insert materialized edge: %v", err)
	}

	// Query materialized edges for rule
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM materialized_edges WHERE rule_id = ?`,
		"rule1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query materialized edges: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 materialized edge, got %d", count)
	}
}

func TestInferenceRulesMigration_ForeignKeyConstraints(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	if err := createInferenceBaseSchema(db); err != nil {
		t.Fatalf("create base schema: %v", err)
	}

	migration := NewInferenceRulesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test rule
	_, err := db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"rule1", "Rule1", "?X", "pred", "?Y", "[]", 0, 1,
	)
	if err != nil {
		t.Fatalf("insert rule: %v", err)
	}

	// Attempt to insert materialized edge with invalid rule_id
	_, err = db.Exec(`
		INSERT INTO materialized_edges (rule_id, edge_id, derived_at)
		VALUES (?, ?, datetime('now'))`,
		"nonexistent", 1,
	)
	if err == nil {
		t.Error("expected FK constraint error for invalid rule_id, got nil")
	}
}

func TestInferenceRulesMigration_CascadeDelete(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	if err := createInferenceBaseSchema(db); err != nil {
		t.Fatalf("create base schema: %v", err)
	}

	migration := NewInferenceRulesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test rule
	_, err := db.Exec(`
		INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"rule1", "Rule1", "?X", "pred", "?Y", "[]", 0, 1,
	)
	if err != nil {
		t.Fatalf("insert rule: %v", err)
	}

	// Insert test nodes and edge
	_, err = db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES
		('node1', 0, 0, 'Node1'),
		('node2', 0, 0, 'Node2')`)
	if err != nil {
		t.Fatalf("insert nodes: %v", err)
	}

	result, err := db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type)
		VALUES (?, ?, ?)`,
		"node1", "node2", 1,
	)
	if err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	edgeID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get edge ID: %v", err)
	}

	// Track materialized edge
	_, err = db.Exec(`
		INSERT INTO materialized_edges (rule_id, edge_id, derived_at)
		VALUES (?, ?, datetime('now'))`,
		"rule1", edgeID,
	)
	if err != nil {
		t.Fatalf("insert materialized edge: %v", err)
	}

	// Delete rule
	_, err = db.Exec("DELETE FROM inference_rules WHERE id = ?", "rule1")
	if err != nil {
		t.Fatalf("delete rule: %v", err)
	}

	// Verify materialized edge was cascade deleted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM materialized_edges WHERE rule_id = ?", "rule1").Scan(&count)
	if err != nil {
		t.Fatalf("query materialized edges: %v", err)
	}
	if count != 0 {
		t.Error("expected materialized edge to be cascade deleted")
	}
}

func TestInferenceRulesMigration_EnabledPriorityIndex(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	migration := NewInferenceRulesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test rules with different priorities and enabled states
	rules := []struct {
		id       string
		priority int
		enabled  int
	}{
		{"rule1", 10, 1},
		{"rule2", 5, 1},
		{"rule3", 15, 0},
	}

	for _, r := range rules {
		_, err := db.Exec(`
			INSERT INTO inference_rules (id, name, head_subject, head_predicate, head_object, body_json, priority, enabled, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			r.id, "Rule", "?X", "pred", "?Y", "[]", r.priority, r.enabled,
		)
		if err != nil {
			t.Fatalf("insert rule %s: %v", r.id, err)
		}
	}

	// Query enabled rules ordered by priority
	rows, err := db.Query(`
		SELECT id, priority FROM inference_rules
		WHERE enabled = 1
		ORDER BY priority DESC`)
	if err != nil {
		t.Fatalf("query enabled rules: %v", err)
	}
	defer rows.Close()

	var results []struct {
		id       string
		priority int
	}
	for rows.Next() {
		var id string
		var priority int
		if err := rows.Scan(&id, &priority); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		results = append(results, struct {
			id       string
			priority int
		}{id, priority})
	}

	if len(results) != 2 {
		t.Errorf("expected 2 enabled rules, got %d", len(results))
	}
	if len(results) >= 2 {
		if results[0].priority < results[1].priority {
			t.Error("rules not ordered by priority descending")
		}
	}
}

func TestInferenceRulesMigration_Rollback(t *testing.T) {
	db, cleanup := createInferenceTestDB(t)
	defer cleanup()

	migration := NewInferenceRulesMigration(db)

	// Apply then rollback
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	// Verify tables no longer exist
	if hasTable(t, db, "inference_rules") {
		t.Error("inference_rules table still exists after rollback")
	}
	if hasTable(t, db, "materialized_edges") {
		t.Error("materialized_edges table still exists after rollback")
	}

	// Verify indexes were removed
	indexes := []string{
		"idx_inference_enabled",
		"idx_materialized_rule",
		"idx_materialized_edge",
	}
	for _, idx := range indexes {
		if hasIndex(t, db, idx) {
			t.Errorf("index %s still exists after rollback", idx)
		}
	}

	// Verify migration record was removed
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	for _, r := range records {
		if r.Version == InferenceRulesMigrationVersion {
			t.Error("migration record still exists after rollback")
		}
	}
}

// hasInferenceRuleColumn checks if a column exists in the inference_rules table.
func hasInferenceRuleColumn(t *testing.T, db *sql.DB, columnName string) bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(inference_rules)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == columnName {
			return true
		}
	}
	return false
}

// hasMaterializedEdgeColumn checks if a column exists in the materialized_edges table.
func hasMaterializedEdgeColumn(t *testing.T, db *sql.DB, columnName string) bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(materialized_edges)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == columnName {
			return true
		}
	}
	return false
}
