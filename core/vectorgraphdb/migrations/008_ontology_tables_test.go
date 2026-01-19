package migrations

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// createOntologyTestDB creates a temporary SQLite database.
func createOntologyTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "ontology_migration_test_*.db")
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

func TestOntologyTablesMigration_Apply_HappyPath(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)

	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify ontology_classes table exists
	if !hasTable(t, db, "ontology_classes") {
		t.Error("ontology_classes table not created")
	}

	// Verify ontology_constraints table exists
	if !hasTable(t, db, "ontology_constraints") {
		t.Error("ontology_constraints table not created")
	}

	// Verify ontology_classes columns
	classColumns := []string{"id", "name", "parent_id", "description", "created_at"}
	for _, col := range classColumns {
		if !hasOntologyClassColumn(t, db, col) {
			t.Errorf("column %s not found in ontology_classes table", col)
		}
	}

	// Verify ontology_constraints columns
	constraintColumns := []string{"id", "class_id", "constraint_type", "constraint_value"}
	for _, col := range constraintColumns {
		if !hasOntologyConstraintColumn(t, db, col) {
			t.Errorf("column %s not found in ontology_constraints table", col)
		}
	}

	// Verify migration was recorded
	records, err := GetAppliedMigrations(db)
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}
	found := false
	for _, r := range records {
		if r.Version == OntologyTablesMigrationVersion {
			found = true
			if r.Name != OntologyTablesMigrationName {
				t.Errorf("expected name %q, got %q", OntologyTablesMigrationName, r.Name)
			}
		}
	}
	if !found {
		t.Error("migration record not found")
	}
}

func TestOntologyTablesMigration_Apply_Idempotent(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)

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
		if r.Version == OntologyTablesMigrationVersion {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 migration record, got %d", count)
	}
}

func TestOntologyTablesMigration_IndexesCreated(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Verify indexes exist
	indexes := []string{
		"idx_ontology_hierarchy",
		"idx_ontology_constraints_class",
	}

	for _, idx := range indexes {
		if !hasIndex(t, db, idx) {
			t.Errorf("index %s not found", idx)
		}
	}
}

func TestOntologyTablesMigration_HierarchyNavigable(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert parent class
	_, err := db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"class1", "Entity", nil, "Base entity class",
	)
	if err != nil {
		t.Fatalf("insert parent class: %v", err)
	}

	// Insert child class
	_, err = db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"class2", "Person", "class1", "Person entity",
	)
	if err != nil {
		t.Fatalf("insert child class: %v", err)
	}

	// Navigate hierarchy using parent_id
	var parentName string
	err = db.QueryRow(`
		SELECT p.name
		FROM ontology_classes c
		JOIN ontology_classes p ON c.parent_id = p.id
		WHERE c.id = ?`,
		"class2",
	).Scan(&parentName)
	if err != nil {
		t.Fatalf("navigate hierarchy: %v", err)
	}
	if parentName != "Entity" {
		t.Errorf("expected parent name 'Entity', got %q", parentName)
	}

	// Query children of parent
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM ontology_classes WHERE parent_id = ?`,
		"class1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query children: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 child, got %d", count)
	}
}

func TestOntologyTablesMigration_SelfReferencingFK(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert class with valid parent_id reference
	_, err := db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"class1", "Base", nil, "Base class",
	)
	if err != nil {
		t.Fatalf("insert base class: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"class2", "Derived", "class1", "Derived class",
	)
	if err != nil {
		t.Fatalf("insert derived class with valid FK: %v", err)
	}

	// Attempt to insert class with invalid parent_id
	_, err = db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"class3", "Invalid", "nonexistent", "Invalid class",
	)
	if err == nil {
		t.Error("expected FK constraint error, got nil")
	}
}

func TestOntologyTablesMigration_ConstraintsValidate(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test class
	_, err := db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"class1", "Entity", nil, "Base class",
	)
	if err != nil {
		t.Fatalf("insert class: %v", err)
	}

	// Insert valid constraint types
	validConstraints := []struct {
		constraintType  string
		constraintValue string
	}{
		{ConstraintTypeAllowedEdges, `["calls", "uses"]`},
		{ConstraintTypeRequiredProperties, `["name", "id"]`},
		{ConstraintTypeCardinality, `{"min": 1, "max": 10}`},
	}

	for _, c := range validConstraints {
		_, err := db.Exec(`
			INSERT INTO ontology_constraints (class_id, constraint_type, constraint_value)
			VALUES (?, ?, ?)`,
			"class1", c.constraintType, c.constraintValue,
		)
		if err != nil {
			t.Errorf("failed to insert valid constraint_type %q: %v", c.constraintType, err)
		}
	}

	// Invalid constraint type
	_, err = db.Exec(`
		INSERT INTO ontology_constraints (class_id, constraint_type, constraint_value)
		VALUES (?, ?, ?)`,
		"class1", "invalid_type", "{}",
	)
	if err == nil {
		t.Error("expected CHECK constraint error for invalid constraint_type, got nil")
	}
}

func TestOntologyTablesMigration_ConstraintForeignKey(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert test class
	_, err := db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"class1", "Entity", nil, "Base class",
	)
	if err != nil {
		t.Fatalf("insert class: %v", err)
	}

	// Insert constraint with valid class_id
	_, err = db.Exec(`
		INSERT INTO ontology_constraints (class_id, constraint_type, constraint_value)
		VALUES (?, ?, ?)`,
		"class1", ConstraintTypeAllowedEdges, `["calls"]`,
	)
	if err != nil {
		t.Fatalf("insert constraint with valid FK: %v", err)
	}

	// Attempt to insert constraint with invalid class_id
	_, err = db.Exec(`
		INSERT INTO ontology_constraints (class_id, constraint_type, constraint_value)
		VALUES (?, ?, ?)`,
		"nonexistent", ConstraintTypeAllowedEdges, `["uses"]`,
	)
	if err == nil {
		t.Error("expected FK constraint error, got nil")
	}
}

func TestOntologyTablesMigration_CascadeDelete(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Insert parent and child classes
	_, err := db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"parent", "Parent", nil, "Parent class",
	)
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO ontology_classes (id, name, parent_id, description, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))`,
		"child", "Child", "parent", "Child class",
	)
	if err != nil {
		t.Fatalf("insert child: %v", err)
	}

	// Delete parent class
	_, err = db.Exec("DELETE FROM ontology_classes WHERE id = ?", "parent")
	if err != nil {
		t.Fatalf("delete parent: %v", err)
	}

	// Verify child was cascade deleted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM ontology_classes WHERE id = ?", "child").Scan(&count)
	if err != nil {
		t.Fatalf("query child: %v", err)
	}
	if count != 0 {
		t.Error("expected child to be cascade deleted")
	}
}

func TestOntologyTablesMigration_Rollback(t *testing.T) {
	db, cleanup := createOntologyTestDB(t)
	defer cleanup()

	migration := NewOntologyTablesMigration(db)

	// Apply then rollback
	if err := migration.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := migration.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	// Verify tables no longer exist
	if hasTable(t, db, "ontology_classes") {
		t.Error("ontology_classes table still exists after rollback")
	}
	if hasTable(t, db, "ontology_constraints") {
		t.Error("ontology_constraints table still exists after rollback")
	}

	// Verify indexes were removed
	indexes := []string{
		"idx_ontology_hierarchy",
		"idx_ontology_constraints_class",
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
		if r.Version == OntologyTablesMigrationVersion {
			t.Error("migration record still exists after rollback")
		}
	}
}

// hasOntologyClassColumn checks if a column exists in the ontology_classes table.
func hasOntologyClassColumn(t *testing.T, db *sql.DB, columnName string) bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(ontology_classes)")
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

// hasOntologyConstraintColumn checks if a column exists in the ontology_constraints table.
func hasOntologyConstraintColumn(t *testing.T, db *sql.DB, columnName string) bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(ontology_constraints)")
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
