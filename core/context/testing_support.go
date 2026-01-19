// Package context provides testing support types for cross-package testing.
// These types are exported to allow hooks and other subpackages to create test fixtures.
package context

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/coordinator"
)

// =============================================================================
// Mock Implementations for Testing
// =============================================================================

// MockBleveSearcher implements TieredBleveSearcher for testing.
type MockBleveSearcher struct {
	mu        sync.Mutex
	results   *search.SearchResult
	err       error
	isOpen    bool
	callCount int
	delay     time.Duration
}

// NewMockBleveSearcher creates a new mock bleve searcher for testing.
func NewMockBleveSearcher() *MockBleveSearcher {
	return &MockBleveSearcher{isOpen: true}
}

// Search implements TieredBleveSearcher.
func (m *MockBleveSearcher) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	m.mu.Lock()
	m.callCount++
	delay := m.delay
	err := m.err
	results := m.results
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return results, err
}

// IsOpen implements TieredBleveSearcher.
func (m *MockBleveSearcher) IsOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isOpen
}

// SetResults sets the results to return from Search.
func (m *MockBleveSearcher) SetResults(results *search.SearchResult) {
	m.mu.Lock()
	m.results = results
	m.mu.Unlock()
}

// SetError sets the error to return from Search.
func (m *MockBleveSearcher) SetError(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
}

// SetOpen sets whether the searcher reports as open.
func (m *MockBleveSearcher) SetOpen(open bool) {
	m.mu.Lock()
	m.isOpen = open
	m.mu.Unlock()
}

// SetDelay sets a delay for Search calls.
func (m *MockBleveSearcher) SetDelay(d time.Duration) {
	m.mu.Lock()
	m.delay = d
	m.mu.Unlock()
}

// CallCount returns the number of Search calls.
func (m *MockBleveSearcher) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// MockVectorSearcher implements TieredVectorSearcher for testing.
type MockVectorSearcher struct {
	mu        sync.Mutex
	results   []coordinator.ScoredVectorResult
	err       error
	callCount int
	delay     time.Duration
}

// NewMockVectorSearcher creates a new mock vector searcher for testing.
func NewMockVectorSearcher() *MockVectorSearcher {
	return &MockVectorSearcher{}
}

// Search implements TieredVectorSearcher.
func (m *MockVectorSearcher) Search(ctx context.Context, query []float32, limit int) ([]coordinator.ScoredVectorResult, error) {
	m.mu.Lock()
	m.callCount++
	delay := m.delay
	err := m.err
	results := m.results
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return results, err
}

// SetResults sets the results to return from Search.
func (m *MockVectorSearcher) SetResults(results []coordinator.ScoredVectorResult) {
	m.mu.Lock()
	m.results = results
	m.mu.Unlock()
}

// SetError sets the error to return from Search.
func (m *MockVectorSearcher) SetError(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
}

// SetDelay sets a delay for Search calls.
func (m *MockVectorSearcher) SetDelay(d time.Duration) {
	m.mu.Lock()
	m.delay = d
	m.mu.Unlock()
}

// CallCount returns the number of Search calls.
func (m *MockVectorSearcher) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// MockEmbedder implements TieredEmbeddingGenerator for testing.
type MockEmbedder struct {
	mu        sync.Mutex
	embedding []float32
	err       error
	callCount int
}

// NewMockEmbedder creates a new mock embedder for testing.
func NewMockEmbedder() *MockEmbedder {
	return &MockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}
}

// Generate implements TieredEmbeddingGenerator.
func (m *MockEmbedder) Generate(ctx context.Context, text string) ([]float32, error) {
	m.mu.Lock()
	m.callCount++
	err := m.err
	embedding := m.embedding
	m.mu.Unlock()

	return embedding, err
}

// SetError sets the error to return from Generate.
func (m *MockEmbedder) SetError(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
}

// CallCount returns the number of Generate calls.
func (m *MockEmbedder) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}
