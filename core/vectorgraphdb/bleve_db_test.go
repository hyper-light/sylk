package vectorgraphdb

import (
	"context"
	"errors"
	"testing"
	"time"

	coreerrors "github.com/adalundhe/sylk/core/errors"
)

// =============================================================================
// Mock Bleve Index
// =============================================================================

type mockBleveIndex struct {
	results []BleveSearchResult
	err     error
	closed  bool
}

func (m *mockBleveIndex) Search(_ context.Context, _ string, _ int) ([]BleveSearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

func (m *mockBleveIndex) Close() error {
	m.closed = true
	return nil
}

// =============================================================================
// FuseResultsRRF Tests
// =============================================================================

func TestFuseResultsRRF_Empty(t *testing.T) {
	t.Parallel()

	results := FuseResultsRRF(nil, nil)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestFuseResultsRRF_VectorOnly(t *testing.T) {
	t.Parallel()

	vectorResults := []*ExtendedHybridResult{
		NewExtendedHybridResult(HybridResult{Node: &GraphNode{ID: "a"}}),
		NewExtendedHybridResult(HybridResult{Node: &GraphNode{ID: "b"}}),
	}

	results := FuseResultsRRF(vectorResults, nil)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Should be ranked
	if results[0].FusionRank != 1 {
		t.Errorf("first result FusionRank = %d, want 1", results[0].FusionRank)
	}
	if results[0].SourceCount != 1 {
		t.Errorf("SourceCount = %d, want 1 (vector only)", results[0].SourceCount)
	}
}

func TestFuseResultsRRF_BothSources(t *testing.T) {
	t.Parallel()

	vectorResults := []*ExtendedHybridResult{
		NewExtendedHybridResult(HybridResult{Node: &GraphNode{ID: "a"}}),
		NewExtendedHybridResult(HybridResult{Node: &GraphNode{ID: "b"}}),
	}

	bleveResults := []BleveSearchResult{
		{ID: "b", Score: 0.9}, // b appears in both
		{ID: "c", Score: 0.8},
	}

	results := FuseResultsRRF(vectorResults, bleveResults)

	// Find result b
	var resultB *ExtendedHybridResult
	for _, r := range results {
		if r.Node.ID == "b" {
			resultB = r
			break
		}
	}

	if resultB == nil {
		t.Fatal("result b not found")
	}

	if resultB.SourceCount != 2 {
		t.Errorf("b SourceCount = %d, want 2", resultB.SourceCount)
	}
	if resultB.BleveScore != 0.9 {
		t.Errorf("b BleveScore = %v, want 0.9", resultB.BleveScore)
	}
}

func TestFuseResultsRRF_RankingOrder(t *testing.T) {
	t.Parallel()

	// b appears high in both lists, should rank first
	vectorResults := []*ExtendedHybridResult{
		NewExtendedHybridResult(HybridResult{Node: &GraphNode{ID: "a"}}),
		NewExtendedHybridResult(HybridResult{Node: &GraphNode{ID: "b"}}),
	}

	bleveResults := []BleveSearchResult{
		{ID: "b", Score: 0.9}, // b is first in bleve
		{ID: "a", Score: 0.8}, // a is second in bleve
	}

	results := FuseResultsRRF(vectorResults, bleveResults)

	// Both should have SourceCount 2
	for _, r := range results {
		if r.SourceCount != 2 {
			t.Errorf("%s SourceCount = %d, want 2", r.Node.ID, r.SourceCount)
		}
	}
}

// =============================================================================
// BleveIntegratedDB Tests
// =============================================================================

func TestNewBleveIntegratedDB(t *testing.T) {
	t.Parallel()

	config := BleveIntegratedDBConfig{
		VectorDB:           &VectorGraphDB{},
		QueryEngine:        &QueryEngine{},
		BleveIndex:         &mockBleveIndex{},
		CircuitBreakerConf: coreerrors.DefaultCircuitBreakerConfig(),
	}

	db := NewBleveIntegratedDB(config)

	if db.VectorDB() == nil {
		t.Error("VectorDB should not be nil")
	}
	if db.QueryEngine() == nil {
		t.Error("QueryEngine should not be nil")
	}
	if db.BleveIndex() == nil {
		t.Error("BleveIndex should not be nil")
	}
	if db.CircuitBreaker() == nil {
		t.Error("CircuitBreaker should not be nil")
	}
}

func TestNewBleveIntegratedDB_DefaultCircuitBreakerID(t *testing.T) {
	t.Parallel()

	config := BleveIntegratedDBConfig{}
	db := NewBleveIntegratedDB(config)

	if db.CircuitBreaker() == nil {
		t.Error("should create default circuit breaker")
	}
}

func TestBleveIntegratedDB_HybridSearch_NilOpts(t *testing.T) {
	t.Parallel()

	mock := &mockBleveIndex{results: []BleveSearchResult{}}
	config := BleveIntegratedDBConfig{
		BleveIndex:         mock,
		CircuitBreakerConf: coreerrors.DefaultCircuitBreakerConfig(),
	}
	db := NewBleveIntegratedDB(config)

	// Should not panic with nil options
	_, err := db.HybridSearch(context.Background(), "test", nil, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBleveIntegratedDB_HybridSearch_Closed(t *testing.T) {
	t.Parallel()

	config := BleveIntegratedDBConfig{
		CircuitBreakerConf: coreerrors.DefaultCircuitBreakerConfig(),
	}
	db := NewBleveIntegratedDB(config)
	db.Close()

	_, err := db.HybridSearch(context.Background(), "test", nil, nil)
	if err != ErrViewClosed {
		t.Errorf("expected ErrViewClosed, got %v", err)
	}
}

func TestBleveIntegratedDB_HybridSearch_TextSearchError(t *testing.T) {
	t.Parallel()

	mock := &mockBleveIndex{err: errors.New("bleve error")}
	config := BleveIntegratedDBConfig{
		BleveIndex:         mock,
		CircuitBreakerConf: coreerrors.DefaultCircuitBreakerConfig(),
	}
	db := NewBleveIntegratedDB(config)

	// Should gracefully handle bleve errors
	results, err := db.HybridSearch(context.Background(), "test", nil, nil)
	if err != nil {
		t.Errorf("should handle bleve error gracefully, got: %v", err)
	}

	// Results should be empty (no vector results and bleve failed)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestBleveIntegratedDB_HybridSearch_EmptyQuery(t *testing.T) {
	t.Parallel()

	mock := &mockBleveIndex{
		results: []BleveSearchResult{{ID: "a", Score: 0.9}},
	}
	config := BleveIntegratedDBConfig{
		BleveIndex:         mock,
		CircuitBreakerConf: coreerrors.DefaultCircuitBreakerConfig(),
	}
	db := NewBleveIntegratedDB(config)

	// Empty query should skip text search
	results, err := db.HybridSearch(context.Background(), "", nil, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// No results since no vector search and text search skipped
	if len(results) != 0 {
		t.Errorf("expected empty results for empty query, got %d", len(results))
	}
}

func TestBleveIntegratedDB_Close(t *testing.T) {
	t.Parallel()

	mock := &mockBleveIndex{}
	config := BleveIntegratedDBConfig{
		BleveIndex:         mock,
		CircuitBreakerConf: coreerrors.DefaultCircuitBreakerConfig(),
	}
	db := NewBleveIntegratedDB(config)

	if db.IsClosed() {
		t.Error("should not be closed initially")
	}

	err := db.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	if !db.IsClosed() {
		t.Error("should be closed after Close()")
	}

	if !mock.closed {
		t.Error("bleve index should be closed")
	}

	// Double close should be safe
	err = db.Close()
	if err != nil {
		t.Errorf("double Close() error: %v", err)
	}
}

func TestBleveIntegratedDB_Close_NilIndex(t *testing.T) {
	t.Parallel()

	config := BleveIntegratedDBConfig{
		CircuitBreakerConf: coreerrors.DefaultCircuitBreakerConfig(),
	}
	db := NewBleveIntegratedDB(config)

	// Should not panic with nil bleve index
	err := db.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

// =============================================================================
// HybridSearchOptions Tests
// =============================================================================

func TestDefaultHybridSearchOptions(t *testing.T) {
	t.Parallel()

	opts := DefaultHybridSearchOptions()

	if opts.VectorWeight <= 0 {
		t.Error("VectorWeight should be positive")
	}
	if opts.TextWeight <= 0 {
		t.Error("TextWeight should be positive")
	}
	if opts.VectorLimit <= 0 {
		t.Error("VectorLimit should be positive")
	}
	if opts.TextLimit <= 0 {
		t.Error("TextLimit should be positive")
	}
	if opts.Timeout <= 0 {
		t.Error("Timeout should be positive")
	}
}

func TestBleveIntegratedDB_HybridSearch_Timeout(t *testing.T) {
	t.Parallel()

	mock := &mockBleveIndex{
		results: []BleveSearchResult{},
	}
	config := BleveIntegratedDBConfig{
		BleveIndex:         mock,
		CircuitBreakerConf: coreerrors.DefaultCircuitBreakerConfig(),
	}
	db := NewBleveIntegratedDB(config)

	opts := &HybridSearchOptions{
		Timeout: 100 * time.Millisecond,
	}

	// Should complete within timeout
	_, err := db.HybridSearch(context.Background(), "test", nil, opts)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// RRF Constants Tests
// =============================================================================

func TestRRFFusionConstant(t *testing.T) {
	t.Parallel()

	// Standard value is 60
	if RRFFusionConstant != 60 {
		t.Errorf("RRFFusionConstant = %d, want 60", RRFFusionConstant)
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestBleveDBErrors(t *testing.T) {
	t.Parallel()

	if ErrBleveNotAvailable == nil {
		t.Error("ErrBleveNotAvailable should be defined")
	}
	if ErrSearchFailed == nil {
		t.Error("ErrSearchFailed should be defined")
	}
}
