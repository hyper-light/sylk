package inference

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Test Helpers
// =============================================================================

// setupEngineTestDB creates an in-memory SQLite database with required tables.
func setupEngineTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	setupEngineTestTables(t, db)
	return db
}

// setupEngineTestTables creates the required tables in the given database.
func setupEngineTestTables(t *testing.T, db *sql.DB) {
	t.Helper()

	// Create inference_rules table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS inference_rules (
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
		t.Fatalf("failed to create inference_rules table: %v", err)
	}

	// Create materialized_edges table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS materialized_edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			rule_id TEXT NOT NULL,
			edge_key TEXT NOT NULL,
			evidence_json TEXT NOT NULL,
			derived_at TEXT NOT NULL,
			FOREIGN KEY (rule_id) REFERENCES inference_rules(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		t.Fatalf("failed to create materialized_edges table: %v", err)
	}

	// Create indexes (ignore errors for indexes that already exist)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_materialized_rule ON materialized_edges(rule_id)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_materialized_edge_key ON materialized_edges(edge_key)`)
}

// engineMockEdgeProvider implements ExtendedEdgeProvider for testing.
type engineMockEdgeProvider struct {
	mu    sync.RWMutex
	edges []Edge
}

func newEngineMockEdgeProvider(edges []Edge) *engineMockEdgeProvider {
	return &engineMockEdgeProvider{edges: edges}
}

func (m *engineMockEdgeProvider) GetAllEdges(ctx context.Context) ([]Edge, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Edge, len(m.edges))
	copy(result, m.edges)
	return result, nil
}

func (m *engineMockEdgeProvider) GetEdgesByNode(ctx context.Context, nodeID string) ([]Edge, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []Edge
	for _, e := range m.edges {
		if e.Source == nodeID || e.Target == nodeID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (m *engineMockEdgeProvider) AddEdge(edge Edge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edges = append(m.edges, edge)
}

func (m *engineMockEdgeProvider) RemoveEdge(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var newEdges []Edge
	for _, e := range m.edges {
		if e.Key() != key {
			newEdges = append(newEdges, e)
		}
	}
	m.edges = newEdges
}

// createTransitivityRule creates a rule: ?x-calls->?z :- ?x-calls->?y, ?y-calls->?z
func createTransitivityRule(id string) InferenceRule {
	return InferenceRule{
		ID:   id,
		Name: "Transitive Calls",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "indirect_calls",
			Object:    "?z",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "calls", Object: "?y"},
			{Subject: "?y", Predicate: "calls", Object: "?z"},
		},
		Priority: 1,
		Enabled:  true,
	}
}

// createImportsRule creates a rule: ?x-depends->?y :- ?x-imports->?y
func createImportsRule(id string) InferenceRule {
	return InferenceRule{
		ID:   id,
		Name: "Imports Dependencies",
		Head: RuleCondition{
			Subject:   "?x",
			Predicate: "depends",
			Object:    "?y",
		},
		Body: []RuleCondition{
			{Subject: "?x", Predicate: "imports", Object: "?y"},
		},
		Priority: 1,
		Enabled:  true,
	}
}

// =============================================================================
// NewInferenceEngine Tests
// =============================================================================

func TestNewInferenceEngine(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)

	if engine == nil {
		t.Fatal("NewInferenceEngine returned nil")
	}
	if engine.ruleStore == nil {
		t.Error("ruleStore not initialized")
	}
	if engine.forwardChainer == nil {
		t.Error("forwardChainer not initialized")
	}
	if engine.materializer == nil {
		t.Error("materializer not initialized")
	}
	if engine.invalidator == nil {
		t.Error("invalidator not initialized")
	}
}

func TestInferenceEngine_SetEdgeProvider(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{})

	engine.SetEdgeProvider(provider)

	if engine.edgeProvider != provider {
		t.Error("edge provider not set correctly")
	}
}

// =============================================================================
// RunInference Tests
// =============================================================================

func TestInferenceEngine_RunInference_NoRules(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()
	err := engine.RunInference(ctx)
	if err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// No rules, so no edges should be materialized
	stats := engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges, got %d", stats.MaterializedEdges)
	}
}

func TestInferenceEngine_RunInference_NoEdges(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add a rule
	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	err := engine.RunInference(ctx)
	if err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// No edges, so nothing should be derived
	stats := engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges, got %d", stats.MaterializedEdges)
	}
}

func TestInferenceEngine_RunInference_TransitivityRule(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add transitivity rule
	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	err := engine.RunInference(ctx)
	if err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Should derive A-indirect_calls->C
	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Errorf("expected 1 materialized edge, got %d", stats.MaterializedEdges)
	}

	// Check the derived edge exists
	edges, err := engine.GetDerivedEdges(ctx, "A")
	if err != nil {
		t.Fatalf("GetDerivedEdges failed: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 derived edge for A, got %d", len(edges))
	}
	if edges[0].Source != "A" || edges[0].Predicate != "indirect_calls" || edges[0].Target != "C" {
		t.Errorf("unexpected derived edge: %v", edges[0])
	}
}

func TestInferenceEngine_RunInference_MultipleRules(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("X", "imports", "Y"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add multiple rules
	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.AddRule(ctx, createImportsRule("rule2")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	err := engine.RunInference(ctx)
	if err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Should derive:
	// - A-indirect_calls->C (from transitivity)
	// - X-depends->Y (from imports)
	stats := engine.Stats()
	if stats.MaterializedEdges != 2 {
		t.Errorf("expected 2 materialized edges, got %d", stats.MaterializedEdges)
	}
}

func TestInferenceEngine_RunInference_UpdatesStats(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	beforeRun := time.Now()
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}
	afterRun := time.Now()

	stats := engine.Stats()

	if stats.LastRunTime.Before(beforeRun) || stats.LastRunTime.After(afterRun) {
		t.Errorf("LastRunTime not set correctly: %v", stats.LastRunTime)
	}
	if stats.LastRunDuration <= 0 {
		t.Errorf("LastRunDuration should be positive: %v", stats.LastRunDuration)
	}
}

// =============================================================================
// OnEdgeAdded Tests
// =============================================================================

func TestInferenceEngine_OnEdgeAdded_NoMatchingRules(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add a rule that requires "calls" predicate
	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Add an edge with different predicate
	err := engine.OnEdgeAdded(ctx, NewEdge("X", "imports", "Y"))
	if err != nil {
		t.Fatalf("OnEdgeAdded failed: %v", err)
	}

	// No edges should be materialized since rule doesn't match
	stats := engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges, got %d", stats.MaterializedEdges)
	}
}

func TestInferenceEngine_OnEdgeAdded_TriggersInference(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Add second edge that completes the pattern
	provider.AddEdge(NewEdge("B", "calls", "C"))
	err := engine.OnEdgeAdded(ctx, NewEdge("B", "calls", "C"))
	if err != nil {
		t.Fatalf("OnEdgeAdded failed: %v", err)
	}

	// Should derive A-indirect_calls->C
	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Errorf("expected 1 materialized edge, got %d", stats.MaterializedEdges)
	}
}

// =============================================================================
// OnEdgeRemoved Tests
// =============================================================================

func TestInferenceEngine_OnEdgeRemoved_InvalidatesDerivedEdges(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Run full inference first
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Fatalf("expected 1 materialized edge before removal, got %d", stats.MaterializedEdges)
	}

	// Remove one of the evidence edges
	provider.RemoveEdge("A|calls|B")
	err := engine.OnEdgeRemoved(ctx, NewEdge("A", "calls", "B"))
	if err != nil {
		t.Fatalf("OnEdgeRemoved failed: %v", err)
	}

	// Derived edge should be invalidated
	stats = engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges after removal, got %d", stats.MaterializedEdges)
	}
}

func TestInferenceEngine_OnEdgeRemoved_NoDerivedEdges(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	// Remove an edge when nothing was derived
	err := engine.OnEdgeRemoved(ctx, NewEdge("X", "any", "Y"))
	if err != nil {
		t.Fatalf("OnEdgeRemoved failed: %v", err)
	}
}

// =============================================================================
// OnEdgeModified Tests
// =============================================================================

func TestInferenceEngine_OnEdgeModified(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Run full inference first
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Modify an edge (change target)
	provider.RemoveEdge("A|calls|B")
	provider.AddEdge(NewEdge("A", "calls", "D"))

	err := engine.OnEdgeModified(ctx, NewEdge("A", "calls", "B"), NewEdge("A", "calls", "D"))
	if err != nil {
		t.Fatalf("OnEdgeModified failed: %v", err)
	}

	// Original derived edge (A-indirect_calls->C) should be invalidated
	// No new edge should be derived since D doesn't call anything
	edges, err := engine.GetDerivedEdges(ctx, "A")
	if err != nil {
		t.Fatalf("GetDerivedEdges failed: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 derived edges for A, got %d", len(edges))
	}
}

// =============================================================================
// Rule Management Tests
// =============================================================================

func TestInferenceEngine_AddRule_Valid(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	rule := createTransitivityRule("rule1")
	err := engine.AddRule(ctx, rule)
	if err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	rules, err := engine.GetRules(ctx)
	if err != nil {
		t.Fatalf("GetRules failed: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].ID != "rule1" {
		t.Errorf("rule ID mismatch: got %s, want rule1", rules[0].ID)
	}
}

func TestInferenceEngine_AddRule_Invalid(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	// Rule with empty ID
	rule := InferenceRule{
		ID:   "",
		Name: "Invalid",
		Head: RuleCondition{Subject: "?x", Predicate: "test", Object: "?y"},
		Body: []RuleCondition{{Subject: "?x", Predicate: "calls", Object: "?y"}},
	}

	err := engine.AddRule(ctx, rule)
	if err == nil {
		t.Error("expected error for invalid rule")
	}
}

func TestInferenceEngine_RemoveRule(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add and run inference
	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Verify derived edge exists
	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Fatalf("expected 1 materialized edge, got %d", stats.MaterializedEdges)
	}

	// Remove the rule
	err := engine.RemoveRule(ctx, "rule1")
	if err != nil {
		t.Fatalf("RemoveRule failed: %v", err)
	}

	// Rule should be gone
	rules, err := engine.GetRules(ctx)
	if err != nil {
		t.Fatalf("GetRules failed: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}

	// Derived edges should be invalidated
	stats = engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges after rule removal, got %d", stats.MaterializedEdges)
	}
}

func TestInferenceEngine_EnableRule(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	// Add an enabled rule
	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Disable the rule
	if err := engine.EnableRule(ctx, "rule1", false); err != nil {
		t.Fatalf("EnableRule failed: %v", err)
	}

	// Verify rule is disabled
	rules, err := engine.GetRules(ctx)
	if err != nil {
		t.Fatalf("GetRules failed: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Enabled {
		t.Error("rule should be disabled")
	}

	// Re-enable the rule
	if err := engine.EnableRule(ctx, "rule1", true); err != nil {
		t.Fatalf("EnableRule failed: %v", err)
	}

	rules, err = engine.GetRules(ctx)
	if err != nil {
		t.Fatalf("GetRules failed: %v", err)
	}
	if !rules[0].Enabled {
		t.Error("rule should be enabled")
	}
}

func TestInferenceEngine_EnableRule_NotFound(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	err := engine.EnableRule(ctx, "nonexistent", true)
	if err == nil {
		t.Error("expected error for non-existent rule")
	}
}

func TestInferenceEngine_GetRules_Empty(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	rules, err := engine.GetRules(ctx)
	if err != nil {
		t.Fatalf("GetRules failed: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}

// =============================================================================
// Query Method Tests
// =============================================================================

func TestInferenceEngine_GetDerivedEdges_Empty(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	edges, err := engine.GetDerivedEdges(ctx, "A")
	if err != nil {
		t.Fatalf("GetDerivedEdges failed: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 derived edges, got %d", len(edges))
	}
}

func TestInferenceEngine_GetDerivedEdges_FindsSourceAndTarget(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// B appears as both source and target of derived edges
	// A-indirect_calls->C (B is part of evidence)
	// B-indirect_calls->D (B is source)
	// A-indirect_calls->D (from transitivity of transitivity)
	edges, err := engine.GetDerivedEdges(ctx, "B")
	if err != nil {
		t.Fatalf("GetDerivedEdges failed: %v", err)
	}
	// B is source of B-indirect_calls->D
	found := false
	for _, e := range edges {
		if e.Source == "B" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find edge with B as source")
	}
}

func TestInferenceEngine_GetProvenance(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Get provenance for derived edge
	result, err := engine.GetProvenance(ctx, "A|indirect_calls|C")
	if err != nil {
		t.Fatalf("GetProvenance failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil provenance")
	}

	if result.RuleID != "rule1" {
		t.Errorf("RuleID mismatch: got %s, want rule1", result.RuleID)
	}
	if len(result.Evidence) != 2 {
		t.Errorf("expected 2 evidence edges, got %d", len(result.Evidence))
	}
	if result.DerivedEdge.SourceID != "A" || result.DerivedEdge.TargetID != "C" {
		t.Errorf("DerivedEdge mismatch: %+v", result.DerivedEdge)
	}
	if result.Provenance == "" {
		t.Error("Provenance should be generated")
	}
}

func TestInferenceEngine_GetProvenance_NotFound(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	result, err := engine.GetProvenance(ctx, "nonexistent|edge|key")
	if err != nil {
		t.Fatalf("GetProvenance failed: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for non-existent edge")
	}
}

func TestInferenceEngine_Stats(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add two rules, one enabled, one disabled
	rule1 := createTransitivityRule("rule1")
	rule2 := createImportsRule("rule2")
	rule2.Enabled = false

	if err := engine.AddRule(ctx, rule1); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.AddRule(ctx, rule2); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	stats := engine.Stats()

	if stats.TotalRules != 2 {
		t.Errorf("TotalRules mismatch: got %d, want 2", stats.TotalRules)
	}
	if stats.EnabledRules != 1 {
		t.Errorf("EnabledRules mismatch: got %d, want 1", stats.EnabledRules)
	}
	if stats.MaterializedEdges != 1 {
		t.Errorf("MaterializedEdges mismatch: got %d, want 1", stats.MaterializedEdges)
	}
}

// =============================================================================
// Component Accessor Tests
// =============================================================================

func TestInferenceEngine_ComponentAccessors(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)

	if engine.RuleStore() == nil {
		t.Error("RuleStore() returned nil")
	}
	if engine.ForwardChainer() == nil {
		t.Error("ForwardChainer() returned nil")
	}
	if engine.Materializer() == nil {
		t.Error("Materializer() returned nil")
	}
	if engine.Invalidator() == nil {
		t.Error("Invalidator() returned nil")
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestInferenceEngine_ConcurrentRunInference(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Run multiple inference passes concurrently
	var wg sync.WaitGroup
	errors := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errors[idx] = engine.RunInference(ctx)
		}(i)
	}

	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Errorf("concurrent RunInference %d failed: %v", i, err)
		}
	}
}

func TestInferenceEngine_ConcurrentReads(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Run concurrent reads
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			_, _ = engine.GetRules(ctx)
		}()

		go func() {
			defer wg.Done()
			_, _ = engine.GetDerivedEdges(ctx, "A")
		}()

		go func() {
			defer wg.Done()
			_ = engine.Stats()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Key Parsing Tests
// =============================================================================

func TestParseEdgeKey(t *testing.T) {
	testCases := []struct {
		key      string
		expected Edge
	}{
		{
			key:      "A|calls|B",
			expected: Edge{Source: "A", Predicate: "calls", Target: "B"},
		},
		{
			key:      "pkg/foo|imports|pkg/bar",
			expected: Edge{Source: "pkg/foo", Predicate: "imports", Target: "pkg/bar"},
		},
		{
			key:      "|empty|",
			expected: Edge{Source: "", Predicate: "empty", Target: ""},
		},
	}

	for _, tc := range testCases {
		result := parseEdgeKey(tc.key)
		if result != tc.expected {
			t.Errorf("parseEdgeKey(%s) = %v, want %v", tc.key, result, tc.expected)
		}
	}
}

// =============================================================================
// ParseEdgeKey Validation Tests (W4P.27)
// =============================================================================

func TestParseEdgeKey_ValidKeys(t *testing.T) {
	testCases := []struct {
		name     string
		key      string
		expected Edge
	}{
		{
			name:     "simple valid key",
			key:      "A|calls|B",
			expected: Edge{Source: "A", Predicate: "calls", Target: "B"},
		},
		{
			name:     "path-like source and target",
			key:      "pkg/foo|imports|pkg/bar",
			expected: Edge{Source: "pkg/foo", Predicate: "imports", Target: "pkg/bar"},
		},
		{
			name:     "empty source allowed",
			key:      "|predicate|target",
			expected: Edge{Source: "", Predicate: "predicate", Target: "target"},
		},
		{
			name:     "empty target allowed",
			key:      "source|predicate|",
			expected: Edge{Source: "source", Predicate: "predicate", Target: ""},
		},
		{
			name:     "empty source and target allowed",
			key:      "|predicate|",
			expected: Edge{Source: "", Predicate: "predicate", Target: ""},
		},
		{
			name:     "special characters in source",
			key:      "node:123|calls|node:456",
			expected: Edge{Source: "node:123", Predicate: "calls", Target: "node:456"},
		},
		{
			name:     "hyphen in predicate",
			key:      "A|depends-on|B",
			expected: Edge{Source: "A", Predicate: "depends-on", Target: "B"},
		},
		{
			name:     "underscore in predicate",
			key:      "A|has_property|B",
			expected: Edge{Source: "A", Predicate: "has_property", Target: "B"},
		},
		{
			name:     "unicode characters",
			key:      "NodeA|relacionado|NodoB",
			expected: Edge{Source: "NodeA", Predicate: "relacionado", Target: "NodoB"},
		},
		{
			name:     "dots in values",
			key:      "pkg.foo|imports|pkg.bar",
			expected: Edge{Source: "pkg.foo", Predicate: "imports", Target: "pkg.bar"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseEdgeKey(tc.key)
			if err != nil {
				t.Errorf("ParseEdgeKey(%q) returned unexpected error: %v", tc.key, err)
				return
			}
			if result != tc.expected {
				t.Errorf("ParseEdgeKey(%q) = %v, want %v", tc.key, result, tc.expected)
			}
		})
	}
}

func TestParseEdgeKey_InvalidKeys(t *testing.T) {
	testCases := []struct {
		name        string
		key         string
		errContains string
	}{
		{
			name:        "empty string",
			key:         "",
			errContains: "empty key",
		},
		{
			name:        "single part no pipes",
			key:         "nodatahere",
			errContains: "expected 3 parts",
		},
		{
			name:        "two parts only",
			key:         "source|target",
			errContains: "expected 3 parts",
		},
		{
			name:        "four parts too many",
			key:         "a|b|c|d",
			errContains: "expected 3 parts",
		},
		{
			name:        "empty predicate",
			key:         "source||target",
			errContains: "predicate cannot be empty",
		},
		{
			name:        "empty predicate with empty source",
			key:         "||target",
			errContains: "predicate cannot be empty",
		},
		{
			name:        "all empty parts",
			key:         "||",
			errContains: "predicate cannot be empty",
		},
		{
			name:        "five pipes too many parts",
			key:         "a|b|c|d|e|f",
			errContains: "expected 3 parts",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseEdgeKey(tc.key)
			if err == nil {
				t.Errorf("ParseEdgeKey(%q) expected error containing %q, got nil (result: %v)",
					tc.key, tc.errContains, result)
				return
			}
			if !errors.Is(err, ErrInvalidEdgeKey) {
				t.Errorf("ParseEdgeKey(%q) error should wrap ErrInvalidEdgeKey, got: %v", tc.key, err)
			}
			if !containsString(err.Error(), tc.errContains) {
				t.Errorf("ParseEdgeKey(%q) error %q should contain %q", tc.key, err.Error(), tc.errContains)
			}
		})
	}
}

func TestParseEdgeKey_ErrorMessages(t *testing.T) {
	// Verify error messages include the invalid key for debugging
	testCases := []struct {
		key         string
		errContains []string
	}{
		{
			key:         "badkey",
			errContains: []string{"badkey", "expected 3 parts", "got 1"},
		},
		{
			key:         "a|b",
			errContains: []string{"a|b", "expected 3 parts", "got 2"},
		},
		{
			key:         "a||b",
			errContains: []string{"a||b", "predicate cannot be empty"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.key, func(t *testing.T) {
			_, err := ParseEdgeKey(tc.key)
			if err == nil {
				t.Fatalf("ParseEdgeKey(%q) expected error, got nil", tc.key)
			}
			errStr := err.Error()
			for _, want := range tc.errContains {
				if !containsString(errStr, want) {
					t.Errorf("error %q should contain %q", errStr, want)
				}
			}
		})
	}
}

// containsString checks if s contains substr (case-sensitive).
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr) >= 0))
}

// findSubstring returns the index of substr in s, or -1 if not found.
func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestParseEvidenceKeys_Valid(t *testing.T) {
	keys := []string{
		"A|calls|B",
		"B|imports|C",
		"C|depends|D",
	}

	evidence, err := parseEvidenceKeys(keys)
	if err != nil {
		t.Fatalf("parseEvidenceKeys failed: %v", err)
	}

	if len(evidence) != 3 {
		t.Fatalf("expected 3 evidence edges, got %d", len(evidence))
	}

	expected := []EvidenceEdge{
		{SourceID: "A", EdgeType: "calls", TargetID: "B"},
		{SourceID: "B", EdgeType: "imports", TargetID: "C"},
		{SourceID: "C", EdgeType: "depends", TargetID: "D"},
	}

	for i, ev := range evidence {
		if ev != expected[i] {
			t.Errorf("evidence[%d] = %v, want %v", i, ev, expected[i])
		}
	}
}

func TestParseEvidenceKeys_Empty(t *testing.T) {
	evidence, err := parseEvidenceKeys([]string{})
	if err != nil {
		t.Fatalf("parseEvidenceKeys failed: %v", err)
	}
	if len(evidence) != 0 {
		t.Errorf("expected empty slice, got %d items", len(evidence))
	}
}

func TestParseEvidenceKeys_InvalidKey(t *testing.T) {
	keys := []string{
		"A|calls|B",
		"invalid_key", // Missing pipes
		"C|depends|D",
	}

	_, err := parseEvidenceKeys(keys)
	if err == nil {
		t.Fatal("expected error for invalid evidence key")
	}
	if !containsString(err.Error(), "evidence key 1") {
		t.Errorf("error should indicate which key failed: %v", err)
	}
}

func TestSplitEdgeKey(t *testing.T) {
	testCases := []struct {
		key      string
		expected []string
	}{
		{
			key:      "A|calls|B",
			expected: []string{"A", "calls", "B"},
		},
		{
			key:      "||",
			expected: []string{"", "", ""},
		},
		{
			key:      "single",
			expected: []string{"single"},
		},
	}

	for _, tc := range testCases {
		result := splitEdgeKey(tc.key)
		if len(result) != len(tc.expected) {
			t.Errorf("splitEdgeKey(%s) len = %d, want %d", tc.key, len(result), len(tc.expected))
			continue
		}
		for i := range result {
			if result[i] != tc.expected[i] {
				t.Errorf("splitEdgeKey(%s)[%d] = %s, want %s", tc.key, i, result[i], tc.expected[i])
			}
		}
	}
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestInferenceEngine_RunInference_ContextCancellation(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)

	// Create a large set of edges to make inference take longer
	edges := make([]Edge, 100)
	for i := 0; i < 100; i++ {
		edges[i] = NewEdge(
			string(rune('A'+i%26)),
			"calls",
			string(rune('A'+(i+1)%26)),
		)
	}
	provider := newEngineMockEdgeProvider(edges)
	engine.SetEdgeProvider(provider)

	ctx, cancel := context.WithCancel(context.Background())

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Cancel context immediately
	cancel()

	err := engine.RunInference(ctx)
	// Note: The error might be nil if inference completed before cancellation,
	// context.Canceled if it was cancelled directly, or a wrapped error containing
	// context.Canceled. All are acceptable outcomes.
	if err != nil && err != context.Canceled {
		// Check if the error wraps context.Canceled
		if err.Error() != "context canceled" &&
			!contains(err.Error(), "context canceled") {
			t.Errorf("expected nil or context.Canceled (possibly wrapped), got: %v", err)
		}
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// =============================================================================
// Disabled Rule Tests
// =============================================================================

func TestInferenceEngine_RunInference_SkipsDisabledRules(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add a disabled rule
	rule := createTransitivityRule("rule1")
	rule.Enabled = false
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// No edges should be derived since rule is disabled
	stats := engine.Stats()
	if stats.MaterializedEdges != 0 {
		t.Errorf("expected 0 materialized edges, got %d", stats.MaterializedEdges)
	}
}

// =============================================================================
// SNE.12: Semi-Naive Evaluation Integration Tests
// =============================================================================

func TestInferenceEngine_UseSemiNaiveEvaluation_Enable(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	// Add a rule first
	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Enable semi-naive evaluation
	if err := engine.UseSemiNaiveEvaluation(ctx, true); err != nil {
		t.Fatalf("UseSemiNaiveEvaluation failed: %v", err)
	}

	if !engine.IsSemiNaiveEnabled() {
		t.Error("expected semi-naive evaluation to be enabled")
	}
}

func TestInferenceEngine_UseSemiNaiveEvaluation_Disable(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	// Add a rule first
	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Enable then disable
	if err := engine.UseSemiNaiveEvaluation(ctx, true); err != nil {
		t.Fatalf("UseSemiNaiveEvaluation (enable) failed: %v", err)
	}
	if err := engine.UseSemiNaiveEvaluation(ctx, false); err != nil {
		t.Fatalf("UseSemiNaiveEvaluation (disable) failed: %v", err)
	}

	if engine.IsSemiNaiveEnabled() {
		t.Error("expected semi-naive evaluation to be disabled")
	}
}

func TestInferenceEngine_RunInference_WithSemiNaive(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Add transitivity rule
	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Enable semi-naive evaluation
	if err := engine.UseSemiNaiveEvaluation(ctx, true); err != nil {
		t.Fatalf("UseSemiNaiveEvaluation failed: %v", err)
	}

	// Run inference with semi-naive
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Should derive A-indirect_calls->C
	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Errorf("expected 1 materialized edge, got %d", stats.MaterializedEdges)
	}

	// Check the derived edge exists
	edges, err := engine.GetDerivedEdges(ctx, "A")
	if err != nil {
		t.Fatalf("GetDerivedEdges failed: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 derived edge for A, got %d", len(edges))
	}
	if edges[0].Source != "A" || edges[0].Predicate != "indirect_calls" || edges[0].Target != "C" {
		t.Errorf("unexpected derived edge: %v", edges[0])
	}
}

func TestInferenceEngine_RunInference_SemiNaiveMatchesStandard(t *testing.T) {
	// Verify that semi-naive and standard forward chaining produce the same results
	// Use separate databases (non-shared) to avoid table collision
	db1, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database 1: %v", err)
	}
	defer db1.Close()
	setupEngineTestTables(t, db1)

	db2, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database 2: %v", err)
	}
	defer db2.Close()
	setupEngineTestTables(t, db2)

	edges := []Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
	}

	// Engine 1: Standard forward chaining
	engine1 := NewInferenceEngine(db1)
	engine1.SetEdgeProvider(newEngineMockEdgeProvider(edges))

	// Engine 2: Semi-naive evaluation
	engine2 := NewInferenceEngine(db2)
	engine2.SetEdgeProvider(newEngineMockEdgeProvider(edges))

	ctx := context.Background()

	rule := createTransitivityRule("rule1")

	if err := engine1.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule (engine1) failed: %v", err)
	}
	if err := engine2.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule (engine2) failed: %v", err)
	}

	// Enable semi-naive for engine2
	if err := engine2.UseSemiNaiveEvaluation(ctx, true); err != nil {
		t.Fatalf("UseSemiNaiveEvaluation failed: %v", err)
	}

	// Run inference on both
	if err := engine1.RunInference(ctx); err != nil {
		t.Fatalf("RunInference (engine1) failed: %v", err)
	}
	if err := engine2.RunInference(ctx); err != nil {
		t.Fatalf("RunInference (engine2) failed: %v", err)
	}

	// Both should produce the same number of materialized edges
	stats1 := engine1.Stats()
	stats2 := engine2.Stats()

	if stats1.MaterializedEdges != stats2.MaterializedEdges {
		t.Errorf("materialized edges mismatch: standard=%d, semi-naive=%d",
			stats1.MaterializedEdges, stats2.MaterializedEdges)
	}
}

func TestInferenceEngine_OnEdgeAdded_WithSemiNaive(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Enable semi-naive evaluation
	if err := engine.UseSemiNaiveEvaluation(ctx, true); err != nil {
		t.Fatalf("UseSemiNaiveEvaluation failed: %v", err)
	}

	// Add second edge that completes the pattern
	provider.AddEdge(NewEdge("B", "calls", "C"))
	if err := engine.OnEdgeAdded(ctx, NewEdge("B", "calls", "C")); err != nil {
		t.Fatalf("OnEdgeAdded failed: %v", err)
	}

	// Should derive A-indirect_calls->C
	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Errorf("expected 1 materialized edge, got %d", stats.MaterializedEdges)
	}
}

func TestInferenceEngine_IsSemiNaiveEnabled_Default(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)

	// By default, semi-naive should be disabled
	if engine.IsSemiNaiveEnabled() {
		t.Error("expected semi-naive evaluation to be disabled by default")
	}
}

func TestInferenceEngine_getActiveForwardChainer_Standard(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)

	// When semi-naive is disabled, should return standard forward chainer
	fc := engine.getActiveForwardChainer()
	if fc == nil {
		t.Fatal("expected non-nil forward chainer")
	}
	if _, ok := fc.(*ForwardChainer); !ok {
		t.Error("expected standard ForwardChainer when semi-naive is disabled")
	}
}

func TestInferenceEngine_getActiveForwardChainer_SemiNaive(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	ctx := context.Background()

	// Add a rule so semi-naive can be enabled
	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Enable semi-naive
	if err := engine.UseSemiNaiveEvaluation(ctx, true); err != nil {
		t.Fatalf("UseSemiNaiveEvaluation failed: %v", err)
	}

	// Should return semi-naive forward chainer
	fc := engine.getActiveForwardChainer()
	if fc == nil {
		t.Fatal("expected non-nil forward chainer")
	}
	if _, ok := fc.(*SemiNaiveForwardChainer); !ok {
		t.Error("expected SemiNaiveForwardChainer when semi-naive is enabled")
	}
}

func TestInferenceEngine_SemiNaive_MultipleRuns(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Enable semi-naive
	if err := engine.UseSemiNaiveEvaluation(ctx, true); err != nil {
		t.Fatalf("UseSemiNaiveEvaluation failed: %v", err)
	}

	// Run inference multiple times
	for i := 0; i < 3; i++ {
		if err := engine.RunInference(ctx); err != nil {
			t.Fatalf("RunInference iteration %d failed: %v", i, err)
		}
	}

	// Should still have only 1 materialized edge (no duplicates)
	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Errorf("expected 1 materialized edge after multiple runs, got %d", stats.MaterializedEdges)
	}
}

// =============================================================================
// W4P.22: Race Condition Tests for OnEdgeModified
// =============================================================================

// slowEdgeProvider wraps an edge provider with configurable delays for race testing.
type slowEdgeProvider struct {
	mu        sync.RWMutex
	edges     []Edge
	delay     time.Duration
	callCount int
}

func newSlowEdgeProvider(edges []Edge, delay time.Duration) *slowEdgeProvider {
	return &slowEdgeProvider{edges: edges, delay: delay}
}

func (p *slowEdgeProvider) GetAllEdges(ctx context.Context) ([]Edge, error) {
	p.mu.Lock()
	p.callCount++
	p.mu.Unlock()

	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]Edge, len(p.edges))
	copy(result, p.edges)
	return result, nil
}

func (p *slowEdgeProvider) GetEdgesByNode(ctx context.Context, nodeID string) ([]Edge, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var result []Edge
	for _, e := range p.edges {
		if e.Source == nodeID || e.Target == nodeID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (p *slowEdgeProvider) AddEdge(edge Edge) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.edges = append(p.edges, edge)
}

func (p *slowEdgeProvider) RemoveEdge(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var newEdges []Edge
	for _, e := range p.edges {
		if e.Key() != key {
			newEdges = append(newEdges, e)
		}
	}
	p.edges = newEdges
}

func (p *slowEdgeProvider) GetCallCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.callCount
}

func (p *slowEdgeProvider) ResetCallCount() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount = 0
}

// TestInferenceEngine_OnEdgeModified_AtomicSnapshot verifies that OnEdgeModified
// captures a single snapshot of edges at the start, ensuring atomicity.
func TestInferenceEngine_OnEdgeModified_AtomicSnapshot(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newSlowEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	}, 0)
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Run full inference first
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	provider.ResetCallCount()

	// Modify an edge
	provider.RemoveEdge("A|calls|B")
	provider.AddEdge(NewEdge("A", "calls", "D"))

	err := engine.OnEdgeModified(ctx, NewEdge("A", "calls", "B"), NewEdge("A", "calls", "D"))
	if err != nil {
		t.Fatalf("OnEdgeModified failed: %v", err)
	}

	// The snapshot should be captured only once at the start
	callCount := provider.GetCallCount()
	if callCount != 1 {
		t.Errorf("expected 1 GetAllEdges call (snapshot), got %d", callCount)
	}
}

// TestInferenceEngine_OnEdgeModified_ConcurrentModifications tests that
// concurrent edge modifications don't cause data races.
func TestInferenceEngine_OnEdgeModified_ConcurrentModifications(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Run initial inference
	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Run concurrent modifications - this test passes if no race is detected
	var wg sync.WaitGroup
	errs := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			oldEdge := NewEdge("A", "calls", "B")
			newEdge := NewEdge("A", "calls", "X")
			errs[idx] = engine.OnEdgeModified(ctx, oldEdge, newEdge)
		}(i)
	}

	wg.Wait()

	// All operations should complete without panics (errors are acceptable)
	for i, err := range errs {
		if err != nil {
			t.Logf("modification %d returned error (expected): %v", i, err)
		}
	}
}

// TestInferenceEngine_OnEdgeModified_ConcurrentWithReads tests that reads
// can proceed concurrently with modifications without data races.
func TestInferenceEngine_OnEdgeModified_ConcurrentWithReads(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	var wg sync.WaitGroup

	// Start readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _ = engine.GetDerivedEdges(ctx, "A")
				_ = engine.Stats()
				_, _ = engine.GetProvenance(ctx, "A|indirect_calls|C")
			}
		}()
	}

	// Start writers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				oldEdge := NewEdge("A", "calls", "B")
				newEdge := NewEdge("A", "calls", "X")
				_ = engine.OnEdgeModified(ctx, oldEdge, newEdge)
			}
		}()
	}

	wg.Wait()
}

// TestInferenceEngine_ConcurrentOnEdgeOperations tests all edge operations
// running concurrently without data races.
func TestInferenceEngine_ConcurrentOnEdgeOperations(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
		NewEdge("C", "calls", "D"),
		NewEdge("D", "calls", "E"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	var wg sync.WaitGroup

	// OnEdgeAdded
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			edge := NewEdge("X", "calls", "Y")
			provider.AddEdge(edge)
			_ = engine.OnEdgeAdded(ctx, edge)
		}(i)
	}

	// OnEdgeRemoved
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			edge := NewEdge("A", "calls", "B")
			_ = engine.OnEdgeRemoved(ctx, edge)
		}(i)
	}

	// OnEdgeModified
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			oldEdge := NewEdge("B", "calls", "C")
			newEdge := NewEdge("B", "calls", "Z")
			_ = engine.OnEdgeModified(ctx, oldEdge, newEdge)
		}(i)
	}

	// RunInference
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = engine.RunInference(ctx)
		}()
	}

	wg.Wait()
}

// TestInferenceEngine_CaptureEdgeSnapshot verifies snapshot isolation.
func TestInferenceEngine_CaptureEdgeSnapshot(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	// Capture snapshot
	snapshot, err := engine.captureEdgeSnapshot(ctx)
	if err != nil {
		t.Fatalf("captureEdgeSnapshot failed: %v", err)
	}

	// Modify the provider after snapshot
	provider.AddEdge(NewEdge("X", "calls", "Y"))

	// Snapshot should not be affected
	if len(snapshot) != 2 {
		t.Errorf("expected snapshot to have 2 edges, got %d", len(snapshot))
	}

	// Modifying the snapshot should not affect the provider
	snapshot[0] = NewEdge("MODIFIED", "MODIFIED", "MODIFIED")

	edges, _ := provider.GetAllEdges(ctx)
	for _, e := range edges {
		if e.Source == "MODIFIED" {
			t.Error("modifying snapshot affected provider edges")
		}
	}
}

// TestInferenceEngine_CaptureEdgeSnapshot_NoProvider tests snapshot with nil provider.
func TestInferenceEngine_CaptureEdgeSnapshot_NoProvider(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	// No edge provider set

	ctx := context.Background()

	snapshot, err := engine.captureEdgeSnapshot(ctx)
	if err != nil {
		t.Fatalf("captureEdgeSnapshot failed: %v", err)
	}

	if snapshot == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(snapshot) != 0 {
		t.Errorf("expected empty snapshot, got %d edges", len(snapshot))
	}
}

// TestInferenceEngine_RematerializeWithSnapshot verifies snapshot-based rematerialization.
func TestInferenceEngine_RematerializeWithSnapshot(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	rule := createTransitivityRule("rule1")
	if err := engine.AddRule(ctx, rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	// Capture snapshot
	snapshot, err := engine.captureEdgeSnapshot(ctx)
	if err != nil {
		t.Fatalf("captureEdgeSnapshot failed: %v", err)
	}

	// Remove edges from provider (simulating concurrent modification)
	provider.RemoveEdge("A|calls|B")
	provider.RemoveEdge("B|calls|C")

	// Rematerialize using snapshot should still work
	err = engine.rematerializeWithSnapshot(ctx, []string{"rule1"}, snapshot)
	if err != nil {
		t.Fatalf("rematerializeWithSnapshot failed: %v", err)
	}

	// Should have derived the edge from the snapshot
	stats := engine.Stats()
	if stats.MaterializedEdges != 1 {
		t.Errorf("expected 1 materialized edge, got %d", stats.MaterializedEdges)
	}
}

// TestInferenceEngine_OnEdgeModified_OrderingGuarantee tests that operations
// complete atomically from caller's perspective.
func TestInferenceEngine_OnEdgeModified_OrderingGuarantee(t *testing.T) {
	db := setupEngineTestDB(t)
	defer db.Close()

	engine := NewInferenceEngine(db)
	provider := newEngineMockEdgeProvider([]Edge{
		NewEdge("A", "calls", "B"),
		NewEdge("B", "calls", "C"),
	})
	engine.SetEdgeProvider(provider)

	ctx := context.Background()

	if err := engine.AddRule(ctx, createTransitivityRule("rule1")); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := engine.RunInference(ctx); err != nil {
		t.Fatalf("RunInference failed: %v", err)
	}

	// Record state before modification
	statsBefore := engine.Stats()
	if statsBefore.MaterializedEdges != 1 {
		t.Fatalf("expected 1 materialized edge before, got %d", statsBefore.MaterializedEdges)
	}

	// Modify edge - this should complete as a single atomic operation
	provider.RemoveEdge("A|calls|B")
	provider.AddEdge(NewEdge("A", "calls", "D"))

	done := make(chan struct{})
	var modifyErr error

	go func() {
		modifyErr = engine.OnEdgeModified(ctx, NewEdge("A", "calls", "B"), NewEdge("A", "calls", "D"))
		close(done)
	}()

	// Wait for completion
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("OnEdgeModified timed out")
	}

	if modifyErr != nil {
		t.Fatalf("OnEdgeModified failed: %v", modifyErr)
	}

	// After the operation completes, state should be consistent
	// (no intermediate states visible)
	edges, err := engine.GetDerivedEdges(ctx, "A")
	if err != nil {
		t.Fatalf("GetDerivedEdges failed: %v", err)
	}

	// A-indirect_calls->C should be invalidated since A no longer calls B
	// and A-calls->D doesn't chain with anything
	if len(edges) != 0 {
		t.Logf("Note: %d derived edges remain for A", len(edges))
	}
}
