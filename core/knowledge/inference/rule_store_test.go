package inference

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Test Helpers
// =============================================================================

// setupTestDB creates an in-memory SQLite database with the inference_rules table.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE inference_rules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			head_subject TEXT NOT NULL,
			head_predicate TEXT NOT NULL,
			head_object TEXT NOT NULL,
			body_json TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			CHECK (enabled IN (0, 1))
		)
	`)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create table: %v", err)
	}

	return db
}

// createTestRule creates a test rule with the given ID and optional modifications.
func createTestRule(id string, priority int, enabled bool) InferenceRule {
	return InferenceRule{
		ID:   id,
		Name: "Test Rule " + id,
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: priority,
		Enabled:  enabled,
	}
}

// =============================================================================
// NewRuleStore Tests
// =============================================================================

func TestNewRuleStore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewRuleStore(db)

	if store == nil {
		t.Fatal("NewRuleStore returned nil")
	}
	if store.db != db {
		t.Error("database not set correctly")
	}
	if store.cache == nil {
		t.Error("cache not initialized")
	}
}

// =============================================================================
// SaveRule Tests
// =============================================================================

func TestRuleStore_SaveRule_Insert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := createTestRule("rule1", 1, true)
	err := store.SaveRule(ctx, rule)
	if err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	loaded, err := store.GetRule(ctx, "rule1")
	if err != nil {
		t.Fatalf("GetRule failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("rule not found after save")
	}

	assertRulesEqual(t, rule, *loaded)
}

func TestRuleStore_SaveRule_Update(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := createTestRule("rule1", 1, true)
	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("initial save failed: %v", err)
	}

	rule.Name = "Updated Name"
	rule.Priority = 5
	rule.Enabled = false
	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("update save failed: %v", err)
	}

	loaded, err := store.GetRule(ctx, "rule1")
	if err != nil {
		t.Fatalf("GetRule failed: %v", err)
	}

	if loaded.Name != "Updated Name" {
		t.Errorf("name not updated: got %s, want Updated Name", loaded.Name)
	}
	if loaded.Priority != 5 {
		t.Errorf("priority not updated: got %d, want 5", loaded.Priority)
	}
	if loaded.Enabled != false {
		t.Error("enabled not updated: expected false")
	}
}

func TestRuleStore_SaveRule_WithComplexBody(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := InferenceRule{
		ID:   "complex",
		Name: "Complex Rule",
		Head: RuleCondition{
			Subject:   "?a",
			Predicate: "indirectly_calls",
			Object:    "?d",
		},
		Body: []RuleCondition{
			{Subject: "?a", Predicate: "calls", Object: "?b"},
			{Subject: "?b", Predicate: "calls", Object: "?c"},
			{Subject: "?c", Predicate: "calls", Object: "?d"},
		},
		Priority: 10,
		Enabled:  true,
	}

	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	loaded, err := store.GetRule(ctx, "complex")
	if err != nil {
		t.Fatalf("GetRule failed: %v", err)
	}

	if len(loaded.Body) != 3 {
		t.Errorf("body length mismatch: got %d, want 3", len(loaded.Body))
	}

	for i, cond := range loaded.Body {
		if cond != rule.Body[i] {
			t.Errorf("body[%d] mismatch: got %+v, want %+v", i, cond, rule.Body[i])
		}
	}
}

// =============================================================================
// LoadRules Tests
// =============================================================================

func TestRuleStore_LoadRules_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rules, err := store.LoadRules(ctx)
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}

	if len(rules) != 0 {
		t.Errorf("expected empty slice, got %d rules", len(rules))
	}
}

func TestRuleStore_LoadRules_Multiple(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rules := []InferenceRule{
		createTestRule("rule1", 1, true),
		createTestRule("rule2", 2, false),
		createTestRule("rule3", 3, true),
	}

	for _, rule := range rules {
		if err := store.SaveRule(ctx, rule); err != nil {
			t.Fatalf("SaveRule failed: %v", err)
		}
	}

	loaded, err := store.LoadRules(ctx)
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}

	if len(loaded) != 3 {
		t.Errorf("expected 3 rules, got %d", len(loaded))
	}
}

func TestRuleStore_LoadRules_PopulatesCache(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := createTestRule("cached", 1, true)
	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	store.cache = make(map[string]*InferenceRule)

	_, err := store.LoadRules(ctx)
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}

	if _, ok := store.cache["cached"]; !ok {
		t.Error("cache not populated after LoadRules")
	}
}

// =============================================================================
// DeleteRule Tests
// =============================================================================

func TestRuleStore_DeleteRule_Exists(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := createTestRule("todelete", 1, true)
	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	if err := store.DeleteRule(ctx, "todelete"); err != nil {
		t.Fatalf("DeleteRule failed: %v", err)
	}

	loaded, err := store.GetRule(ctx, "todelete")
	if err != nil {
		t.Fatalf("GetRule failed: %v", err)
	}
	if loaded != nil {
		t.Error("rule still exists after delete")
	}
}

func TestRuleStore_DeleteRule_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	err := store.DeleteRule(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error when deleting non-existent rule")
	}
}

func TestRuleStore_DeleteRule_RemovesFromCache(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := createTestRule("cached", 1, true)
	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	if _, ok := store.cache["cached"]; !ok {
		t.Fatal("rule not in cache after save")
	}

	if err := store.DeleteRule(ctx, "cached"); err != nil {
		t.Fatalf("DeleteRule failed: %v", err)
	}

	if _, ok := store.cache["cached"]; ok {
		t.Error("rule still in cache after delete")
	}
}

// =============================================================================
// GetRulesByPriority Tests
// =============================================================================

func TestRuleStore_GetRulesByPriority_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rules, err := store.GetRulesByPriority(ctx)
	if err != nil {
		t.Fatalf("GetRulesByPriority failed: %v", err)
	}

	if len(rules) != 0 {
		t.Errorf("expected empty slice, got %d rules", len(rules))
	}
}

func TestRuleStore_GetRulesByPriority_Ordering(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	if err := store.SaveRule(ctx, createTestRule("high", 100, true)); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}
	if err := store.SaveRule(ctx, createTestRule("low", 1, true)); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}
	if err := store.SaveRule(ctx, createTestRule("medium", 50, true)); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	rules, err := store.GetRulesByPriority(ctx)
	if err != nil {
		t.Fatalf("GetRulesByPriority failed: %v", err)
	}

	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}

	expectedOrder := []string{"low", "medium", "high"}
	for i, expected := range expectedOrder {
		if rules[i].ID != expected {
			t.Errorf("position %d: expected %s, got %s", i, expected, rules[i].ID)
		}
	}
}

// =============================================================================
// GetEnabledRules Tests
// =============================================================================

func TestRuleStore_GetEnabledRules_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rules, err := store.GetEnabledRules(ctx)
	if err != nil {
		t.Fatalf("GetEnabledRules failed: %v", err)
	}

	if len(rules) != 0 {
		t.Errorf("expected empty slice, got %d rules", len(rules))
	}
}

func TestRuleStore_GetEnabledRules_FiltersDisabled(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	if err := store.SaveRule(ctx, createTestRule("enabled1", 1, true)); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}
	if err := store.SaveRule(ctx, createTestRule("disabled", 2, false)); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}
	if err := store.SaveRule(ctx, createTestRule("enabled2", 3, true)); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	rules, err := store.GetEnabledRules(ctx)
	if err != nil {
		t.Fatalf("GetEnabledRules failed: %v", err)
	}

	if len(rules) != 2 {
		t.Fatalf("expected 2 enabled rules, got %d", len(rules))
	}

	for _, rule := range rules {
		if !rule.Enabled {
			t.Errorf("got disabled rule: %s", rule.ID)
		}
		if rule.ID == "disabled" {
			t.Error("disabled rule should not be returned")
		}
	}
}

func TestRuleStore_GetEnabledRules_SortedByPriority(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	if err := store.SaveRule(ctx, createTestRule("high", 100, true)); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}
	if err := store.SaveRule(ctx, createTestRule("low", 1, true)); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	rules, err := store.GetEnabledRules(ctx)
	if err != nil {
		t.Fatalf("GetEnabledRules failed: %v", err)
	}

	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	if rules[0].ID != "low" {
		t.Errorf("expected low first, got %s", rules[0].ID)
	}
	if rules[1].ID != "high" {
		t.Errorf("expected high second, got %s", rules[1].ID)
	}
}

// =============================================================================
// GetRule Tests
// =============================================================================

func TestRuleStore_GetRule_Exists(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := createTestRule("exists", 5, true)
	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	loaded, err := store.GetRule(ctx, "exists")
	if err != nil {
		t.Fatalf("GetRule failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("rule not found")
	}

	assertRulesEqual(t, rule, *loaded)
}

func TestRuleStore_GetRule_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	loaded, err := store.GetRule(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetRule returned error: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil for non-existent rule")
	}
}

func TestRuleStore_GetRule_UsesCache(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := createTestRule("cached", 1, true)
	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	_, err := db.Exec("DELETE FROM inference_rules WHERE id = ?", "cached")
	if err != nil {
		t.Fatalf("failed to delete from db: %v", err)
	}

	loaded, err := store.GetRule(ctx, "cached")
	if err != nil {
		t.Fatalf("GetRule failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected rule from cache")
	}
}

func TestRuleStore_GetRule_CacheReturnsCopy(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	store := NewRuleStore(db)
	ctx := context.Background()

	rule := createTestRule("original", 1, true)
	if err := store.SaveRule(ctx, rule); err != nil {
		t.Fatalf("SaveRule failed: %v", err)
	}

	loaded1, _ := store.GetRule(ctx, "original")
	loaded1.Name = "Modified"

	loaded2, _ := store.GetRule(ctx, "original")

	if loaded2.Name == "Modified" {
		t.Error("cache returned reference instead of copy")
	}
}

// =============================================================================
// Helper Functions Tests
// =============================================================================

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("boolToInt(true) should return 1")
	}
	if boolToInt(false) != 0 {
		t.Error("boolToInt(false) should return 0")
	}
}

func TestIntToBool(t *testing.T) {
	if intToBool(1) != true {
		t.Error("intToBool(1) should return true")
	}
	if intToBool(0) != false {
		t.Error("intToBool(0) should return false")
	}
	if intToBool(42) != true {
		t.Error("intToBool(42) should return true")
	}
}

// =============================================================================
// Assertion Helpers
// =============================================================================

func assertRulesEqual(t *testing.T, expected, actual InferenceRule) {
	t.Helper()

	if actual.ID != expected.ID {
		t.Errorf("ID mismatch: got %s, want %s", actual.ID, expected.ID)
	}
	if actual.Name != expected.Name {
		t.Errorf("Name mismatch: got %s, want %s", actual.Name, expected.Name)
	}
	if actual.Head != expected.Head {
		t.Errorf("Head mismatch: got %+v, want %+v", actual.Head, expected.Head)
	}
	if len(actual.Body) != len(expected.Body) {
		t.Errorf("Body length mismatch: got %d, want %d", len(actual.Body), len(expected.Body))
	}
	for i := range expected.Body {
		if i < len(actual.Body) && actual.Body[i] != expected.Body[i] {
			t.Errorf("Body[%d] mismatch: got %+v, want %+v", i, actual.Body[i], expected.Body[i])
		}
	}
	if actual.Priority != expected.Priority {
		t.Errorf("Priority mismatch: got %d, want %d", actual.Priority, expected.Priority)
	}
	if actual.Enabled != expected.Enabled {
		t.Errorf("Enabled mismatch: got %v, want %v", actual.Enabled, expected.Enabled)
	}
}
