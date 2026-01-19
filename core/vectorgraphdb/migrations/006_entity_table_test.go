package migrations

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// createEntityTestDB creates a temporary SQLite database with nodes table.
func createEntityTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "entity_migration_test_*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("sqlite3", tmpFile.Name()+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("open database: %v", err)
	}

	if err := createEntityBaseSchema(db); err != nil {
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

// createEntityBaseSchema creates the nodes table for entity references.
func createEntityBaseSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		)
	`)
	return err
}

func TestEntityTableMigration_Apply_HappyPath(t *testing.T) {
	db, cleanup := createEntityTestDB(t)
	defer cleanup()

	migration := NewEntityTableMigration(db)

	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify entities table exists
	if !hasTable(t, db, "entities") {
		t.Error("entities table not created")
	}

	// Verify schema columns
	columns := []string{"id", "canonical_name", "entity_type", "source_node_id", "created_at", "updated_at"}
	for _, col := range columns {
		if !hasEntityColumn(t, db, col) {
			t.Errorf("column %s not found in entities table", col)
		}
	}

	// Verify migration was recorded
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	found := false
	for _, r := range records {
		if r.Version == EntityTableMigrationVersion {
			found = true
			if r.Name != EntityTableMigrationName {
				t.Errorf("expected name %q, got %q", EntityTableMigrationName, r.Name)
			}
		}
	}
	if !found {
		t.Error("migration record not found")
	}
}

func TestEntityTableMigration_Apply_Idempotent(t *testing.T) {
	db, cleanup := createEntityTestDB(t)
	defer cleanup()

	migration := NewEntityTableMigration(db)

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
		if r.Version == EntityTableMigrationVersion {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 migration record, got %d", count)
	}
}

func TestEntityTableMigration_IndexesCreated(t *testing.T) {
	db, cleanup := createEntityTestDB(t)
	defer cleanup()

	migration := NewEntityTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify indexes exist
	indexes := []string{
		"idx_entities_canonical",
		"idx_entities_source",
	}

	for _, idx := range indexes {
		if !hasIndex(t, db, idx) {
			t.Errorf("index %s not found", idx)
		}
	}
}

func TestEntityTableMigration_ForeignKeyConstraint(t *testing.T) {
	db, cleanup := createEntityTestDB(t)
	defer cleanup()

	migration := NewEntityTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test node
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// Insert entity with valid FK
	_, err = db.Exec(`
		INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"entity1", "MyFunction", EntityTypeFunction, "node1",
	)
	if err != nil {
		t.Fatalf("insert entity with valid FK: %v", err)
	}

	// Attempt to insert entity with invalid FK
	_, err = db.Exec(`
		INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"entity2", "InvalidFunction", EntityTypeFunction, "nonexistent",
	)
	if err == nil {
		t.Error("expected FK constraint error, got nil")
	}
}

func TestEntityTableMigration_EntityTypeConstraint(t *testing.T) {
	db, cleanup := createEntityTestDB(t)
	defer cleanup()

	migration := NewEntityTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test node
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// Valid entity types (0-9)
	validTypes := []int{
		EntityTypeFunction, EntityTypeType, EntityTypeVariable,
		EntityTypeImport, EntityTypeFile, EntityTypeModule,
		EntityTypePackage, EntityTypeInterface, EntityTypeMethod,
		EntityTypeConstant,
	}

	for i, entityType := range validTypes {
		_, err := db.Exec(`
			INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`,
			"entity_"+string(rune(i+'0')), "Entity", entityType, "node1",
		)
		if err != nil {
			t.Errorf("failed to insert valid entity_type %d: %v", entityType, err)
		}
	}

	// Invalid entity type
	_, err = db.Exec(`
		INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"invalid", "Invalid", 10, "node1",
	)
	if err == nil {
		t.Error("expected CHECK constraint error for entity_type > 9, got nil")
	}
}

func TestEntityTableMigration_CanonicalNameIndex(t *testing.T) {
	db, cleanup := createEntityTestDB(t)
	defer cleanup()

	migration := NewEntityTableMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test node
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES ('node1', 0, 0, 'Node1')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// Insert test entities
	entities := []struct {
		id            string
		canonicalName string
		entityType    int
	}{
		{"e1", "MyFunction", EntityTypeFunction},
		{"e2", "MyClass", EntityTypeType},
		{"e3", "MyVariable", EntityTypeVariable},
	}

	for _, e := range entities {
		_, err := db.Exec(`
			INSERT INTO entities (id, canonical_name, entity_type, source_node_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`,
			e.id, e.canonicalName, e.entityType, "node1",
		)
		if err != nil {
			t.Fatalf("insert entity %s: %v", e.id, err)
		}
	}

	// Query by canonical_name
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM entities WHERE canonical_name = ?`,
		"MyFunction",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query by canonical_name: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 entity, got %d", count)
	}

	// Query by canonical_name and entity_type
	err = db.QueryRow(`
		SELECT COUNT(*) FROM entities WHERE canonical_name = ? AND entity_type = ?`,
		"MyClass", EntityTypeType,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query by canonical_name and entity_type: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 entity, got %d", count)
	}
}

func TestEntityTableMigration_Rollback(t *testing.T) {
	db, cleanup := createEntityTestDB(t)
	defer cleanup()

	migration := NewEntityTableMigration(db)

	// Apply then rollback
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	// Verify table no longer exists
	if hasTable(t, db, "entities") {
		t.Error("entities table still exists after rollback")
	}

	// Verify indexes were removed
	indexes := []string{
		"idx_entities_canonical",
		"idx_entities_source",
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
		if r.Version == EntityTableMigrationVersion {
			t.Error("migration record still exists after rollback")
		}
	}
}

// hasTable checks if a table exists in the database.
func hasTable(t *testing.T, db *sql.DB, tableName string) bool {
	t.Helper()

	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
		tableName,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query table: %v", err)
	}
	return count > 0
}

// hasEntityColumn checks if a column exists in the entities table.
func hasEntityColumn(t *testing.T, db *sql.DB, columnName string) bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(entities)")
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
