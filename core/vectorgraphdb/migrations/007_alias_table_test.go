package migrations

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// createAliasTestDB creates a temporary SQLite database with entities table.
func createAliasTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "alias_migration_test_*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("sqlite3", tmpFile.Name()+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("open database: %v", err)
	}

	if err := createAliasBaseSchema(db); err != nil {
		db.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("create base schema: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

// createAliasBaseSchema creates the nodes and entities tables.
func createAliasBaseSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE entities (
			id TEXT PRIMARY KEY,
			canonical_name TEXT NOT NULL,
			entity_type INTEGER NOT NULL,
			source_node_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY (source_node_id) REFERENCES nodes(id) ON DELETE CASCADE
		)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func TestAliasTableMigration_Apply_HappyPath(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)

	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify entity_aliases table exists
	if !hasTable(t, db, "entity_aliases") {
		t.Error("entity_aliases table not created")
	}

	// Verify schema columns
	columns := []string{"id", "entity_id", "alias", "alias_type", "confidence", "created_at"}
	for _, col := range columns {
		if !hasAliasColumn(t, db, col) {
			t.Errorf("column %s not found in entity_aliases table", col)
		}
	}

	// Verify migration was recorded
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	found := false
	for _, r := range records {
		if r.Version == AliasTableMigrationVersion {
			found = true
			if r.Name != AliasTableMigrationName {
				t.Errorf("expected name %q, got %q", AliasTableMigrationName, r.Name)
			}
		}
	}
	if !found {
		t.Error("migration record not found")
	}
}

func TestAliasTableMigration_Apply_Idempotent(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)

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
		if r.Version == AliasTableMigrationVersion {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 migration record, got %d", count)
	}
}

func TestAliasTableMigration_IndexesCreated(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify indexes exist
	indexes := []string{
		"idx_aliases_lookup",
		"idx_aliases_entity",
		"idx_aliases_type",
	}

	for _, idx := range indexes {
		if !hasIndex(t, db, idx) {
			t.Errorf("index %s not found", idx)
		}
	}
}

func TestAliasTableMigration_ForeignKeyConstraint(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test data
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
		VALUES ('entity1', 'MyFunction', 0, 'node1', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	// Insert alias with valid FK
	_, err = db.Exec(`
		INSERT INTO entity_aliases (entity_id, alias, alias_type, confidence, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"entity1", "my_func", AliasTypeShorthand, 0.95,
	)
	if err != nil {
		t.Fatalf("insert alias with valid FK: %v", err)
	}

	// Attempt to insert alias with invalid FK
	_, err = db.Exec(`
		INSERT INTO entity_aliases (entity_id, alias, alias_type, confidence, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"nonexistent", "invalid_alias", AliasTypeCanonical, 1.0,
	)
	if err == nil {
		t.Error("expected FK constraint error, got nil")
	}
}

func TestAliasTableMigration_ConfidenceConstraint(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test data
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
		VALUES ('entity1', 'MyFunction', 0, 'node1', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	// Valid confidence values
	validConfidences := []float64{0.0, 0.5, 1.0}
	for i, conf := range validConfidences {
		_, err := db.Exec(`
			INSERT INTO entity_aliases (entity_id, alias, alias_type, confidence, created_at)
			VALUES (?, ?, ?, ?, datetime('now'))`,
			"entity1", "alias_"+string(rune(i+'0')), AliasTypeCanonical, conf,
		)
		if err != nil {
			t.Errorf("failed to insert valid confidence %f: %v", conf, err)
		}
	}

	// Invalid confidence values
	invalidConfidences := []float64{-0.1, 1.1}
	for _, conf := range invalidConfidences {
		_, err := db.Exec(`
			INSERT INTO entity_aliases (entity_id, alias, alias_type, confidence, created_at)
			VALUES (?, ?, ?, ?, datetime('now'))`,
			"entity1", "invalid_alias", AliasTypeCanonical, conf,
		)
		if err == nil {
			t.Errorf("expected CHECK constraint error for confidence %f, got nil", conf)
		}
	}
}

func TestAliasTableMigration_AliasTypeConstraint(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test data
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
		VALUES ('entity1', 'MyFunction', 0, 'node1', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	// Valid alias types
	validTypes := []string{
		AliasTypeCanonical, AliasTypeImport, AliasTypeType,
		AliasTypeShorthand, AliasTypeQualified,
	}

	for i, aliasType := range validTypes {
		_, err := db.Exec(`
			INSERT INTO entity_aliases (entity_id, alias, alias_type, confidence, created_at)
			VALUES (?, ?, ?, ?, datetime('now'))`,
			"entity1", "alias_"+string(rune(i+'0')), aliasType, 1.0,
		)
		if err != nil {
			t.Errorf("failed to insert valid alias_type %s: %v", aliasType, err)
		}
	}

	// Invalid alias type
	_, err = db.Exec(`
		INSERT INTO entity_aliases (entity_id, alias, alias_type, confidence, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"entity1", "invalid_alias", "invalid_type", 1.0,
	)
	if err == nil {
		t.Error("expected CHECK constraint error for invalid alias_type, got nil")
	}
}

func TestAliasTableMigration_AliasResolution(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test data
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
		VALUES ('entity1', 'MyFunction', 0, 'node1', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	// Insert multiple aliases for the same entity
	aliases := []struct {
		alias      string
		aliasType  string
		confidence float64
	}{
		{"MyFunction", AliasTypeCanonical, 1.0},
		{"my_func", AliasTypeShorthand, 0.9},
		{"pkg.MyFunction", AliasTypeQualified, 0.95},
		{"MF", AliasTypeShorthand, 0.7},
	}

	for _, a := range aliases {
		_, err := db.Exec(`
			INSERT INTO entity_aliases (entity_id, alias, alias_type, confidence, created_at)
			VALUES (?, ?, ?, ?, datetime('now'))`,
			"entity1", a.alias, a.aliasType, a.confidence,
		)
		if err != nil {
			t.Fatalf("insert alias %s: %v", a.alias, err)
		}
	}

	// Query by alias
	var entityID string
	err = db.QueryRow(`
		SELECT entity_id FROM entity_aliases WHERE alias = ?`,
		"my_func",
	).Scan(&entityID)
	if err != nil {
		t.Fatalf("query by alias: %v", err)
	}
	if entityID != "entity1" {
		t.Errorf("expected entity_id 'entity1', got %s", entityID)
	}

	// Query all aliases for an entity
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM entity_aliases WHERE entity_id = ?`,
		"entity1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query aliases for entity: %v", err)
	}
	if count != 4 {
		t.Errorf("expected 4 aliases, got %d", count)
	}

	// Query by alias type and confidence
	err = db.QueryRow(`
		SELECT COUNT(*) FROM entity_aliases
		WHERE alias_type = ? AND confidence >= ?`,
		AliasTypeShorthand, 0.8,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query by alias_type and confidence: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 alias, got %d", count)
	}
}

func TestAliasTableMigration_Rollback(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)

	// Apply then rollback
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	// Verify table no longer exists
	if hasTable(t, db, "entity_aliases") {
		t.Error("entity_aliases table still exists after rollback")
	}

	// Verify indexes were removed
	indexes := []string{
		"idx_aliases_lookup",
		"idx_aliases_entity",
		"idx_aliases_type",
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
		if r.Version == AliasTableMigrationVersion {
			t.Error("migration record still exists after rollback")
		}
	}
}

func TestAliasTableMigration_CascadeDelete(t *testing.T) {
	db, cleanup := createAliasTestDB(t)
	defer cleanup()

	migration := NewAliasTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test data
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
		VALUES ('entity1', 'MyFunction', 0, 'node1', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO entity_aliases (entity_id, alias, alias_type, confidence, created_at)
		VALUES ('entity1', 'my_func', 'shorthand', 0.9, datetime('now'))`)
	if err != nil {
		t.Fatalf("insert alias: %v", err)
	}

	// Delete entity and verify cascade
	_, err = db.Exec("DELETE FROM entities WHERE id = 'entity1'")
	if err != nil {
		t.Fatalf("delete entity: %v", err)
	}

	// Verify aliases were cascade deleted
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM entity_aliases WHERE entity_id = 'entity1'`).Scan(&count)
	if err != nil {
		t.Fatalf("query aliases: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 aliases after cascade delete, got %d", count)
	}
}

// hasAliasColumn checks if a column exists in the entity_aliases table.
func hasAliasColumn(t *testing.T, db *sql.DB, columnName string) bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(entity_aliases)")
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
