// Package vectorgraphdb provides AR.11.1: BleveIntegratedDB - wraps VectorGraphDB
// with Bleve full-text search for hybrid vector + text search capabilities.
package vectorgraphdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	coreerrors "github.com/adalundhe/sylk/core/errors"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrBleveNotAvailable indicates the Bleve index is not available.
	ErrBleveNotAvailable = errors.New("bleve index not available")

	// ErrSearchFailed indicates a search operation failed.
	ErrSearchFailed = errors.New("search operation failed")
)

// =============================================================================
// Bleve Index Interface
// =============================================================================

// BleveIndex abstracts Bleve index operations for testing and flexibility.
type BleveIndex interface {
	// Search performs a full-text search and returns document IDs with scores.
	Search(ctx context.Context, query string, limit int) ([]BleveSearchResult, error)

	// Close closes the index.
	Close() error
}

// BleveSearchResult represents a single Bleve search result.
type BleveSearchResult struct {
	ID    string
	Score float64
}

// =============================================================================
// RRF Fusion
// =============================================================================

// RRFFusionConstant is the k constant for Reciprocal Rank Fusion.
// A value of 60 is commonly used in information retrieval.
const RRFFusionConstant = 60

// FuseResultsRRF merges vector and text search results using Reciprocal Rank Fusion.
func FuseResultsRRF(
	vectorResults []*ExtendedHybridResult,
	bleveResults []BleveSearchResult,
) []*ExtendedHybridResult {
	// Build score maps
	vectorScores := make(map[string]float64)
	for rank, r := range vectorResults {
		rrfScore := 1.0 / float64(RRFFusionConstant+rank+1)
		vectorScores[r.Node.ID] = rrfScore
	}

	bleveScores := make(map[string]float64)
	bleveOriginalScores := make(map[string]float64)
	for rank, r := range bleveResults {
		rrfScore := 1.0 / float64(RRFFusionConstant+rank+1)
		bleveScores[r.ID] = rrfScore
		bleveOriginalScores[r.ID] = r.Score
	}

	// Combine scores
	combinedScores := make(map[string]float64)
	for id, score := range vectorScores {
		combinedScores[id] = score
	}
	for id, score := range bleveScores {
		combinedScores[id] += score
	}

	// Update extended results with fusion info
	resultMap := make(map[string]*ExtendedHybridResult)
	for _, r := range vectorResults {
		r.FusionRank = 0 // Will be set later
		r.SourceCount = 1
		if _, hasBleveScore := bleveScores[r.Node.ID]; hasBleveScore {
			r.BleveScore = bleveOriginalScores[r.Node.ID]
			r.SourceCount = 2
		}
		resultMap[r.Node.ID] = r
	}

	// Sort by combined score
	type scoredResult struct {
		id    string
		score float64
	}
	var sorted []scoredResult
	for id, score := range combinedScores {
		sorted = append(sorted, scoredResult{id, score})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})

	// Build final result list
	var results []*ExtendedHybridResult
	for rank, sr := range sorted {
		if r, exists := resultMap[sr.id]; exists {
			r.FusionRank = rank + 1
			results = append(results, r)
		}
	}

	return results
}

// =============================================================================
// Bleve Integrated DB
// =============================================================================

// BleveIntegratedDB wraps VectorGraphDB with Bleve full-text search.
type BleveIntegratedDB struct {
	vectorDB       *VectorGraphDB
	queryEngine    *QueryEngine
	bleveIndex     BleveIndex
	circuitBreaker *coreerrors.CircuitBreaker

	mu     sync.RWMutex
	closed bool
}

// BleveIntegratedDBConfig holds configuration for BleveIntegratedDB.
type BleveIntegratedDBConfig struct {
	VectorDB           *VectorGraphDB
	QueryEngine        *QueryEngine
	BleveIndex         BleveIndex
	CircuitBreakerID   string
	CircuitBreakerConf coreerrors.CircuitBreakerConfig
}

// NewBleveIntegratedDB creates a new Bleve-integrated database.
func NewBleveIntegratedDB(config BleveIntegratedDBConfig) *BleveIntegratedDB {
	cbID := config.CircuitBreakerID
	if cbID == "" {
		cbID = "bleve_search"
	}

	cb := coreerrors.NewCircuitBreaker(cbID, config.CircuitBreakerConf)

	return &BleveIntegratedDB{
		vectorDB:       config.VectorDB,
		queryEngine:    config.QueryEngine,
		bleveIndex:     config.BleveIndex,
		circuitBreaker: cb,
	}
}

// =============================================================================
// Hybrid Search
// =============================================================================

// HybridSearchOptions configures hybrid search behavior.
type HybridSearchOptions struct {
	// Vector search options
	VectorWeight float64
	VectorLimit  int

	// Text search options
	TextWeight float64
	TextLimit  int

	// Graph options
	GraphWeight float64
	GraphDepth  int

	// Filtering
	Domains   []Domain
	NodeTypes []NodeType

	// Timeout for the search operation
	Timeout time.Duration
}

// DefaultHybridSearchOptions returns sensible defaults.
func DefaultHybridSearchOptions() *HybridSearchOptions {
	return &HybridSearchOptions{
		VectorWeight: 0.5,
		VectorLimit:  20,
		TextWeight:   0.3,
		TextLimit:    20,
		GraphWeight:  0.2,
		GraphDepth:   2,
		Timeout:      5 * time.Second,
	}
}

// HybridSearch performs a combined vector + text search with RRF fusion.
// The RLock is held throughout the entire operation to prevent use-after-close.
func (db *BleveIntegratedDB) HybridSearch(
	ctx context.Context,
	query string,
	embedding []float32,
	opts *HybridSearchOptions,
) ([]*ExtendedHybridResult, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return nil, ErrViewClosed
	}

	return db.hybridSearchLocked(ctx, query, embedding, opts)
}

// hybridSearchLocked performs the hybrid search assuming the RLock is held.
func (db *BleveIntegratedDB) hybridSearchLocked(
	ctx context.Context,
	query string,
	embedding []float32,
	opts *HybridSearchOptions,
) ([]*ExtendedHybridResult, error) {
	if opts == nil {
		opts = DefaultHybridSearchOptions()
	}

	// Apply timeout
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Perform vector search (lock already held)
	vectorResults, err := db.vectorSearchLocked(embedding, opts)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// Perform text search with circuit breaker (lock already held)
	bleveResults := db.textSearchLocked(ctx, query, opts)

	// Fuse results using RRF
	return FuseResultsRRF(vectorResults, bleveResults), nil
}

// vectorSearchLocked performs vector search assuming the RLock is held.
func (db *BleveIntegratedDB) vectorSearchLocked(
	embedding []float32,
	opts *HybridSearchOptions,
) ([]*ExtendedHybridResult, error) {
	if db.queryEngine == nil {
		return nil, nil
	}

	hybridOpts := &HybridQueryOptions{
		VectorWeight: opts.VectorWeight,
		GraphWeight:  opts.GraphWeight,
		VectorLimit:  opts.VectorLimit,
		GraphDepth:   opts.GraphDepth,
		Domains:      opts.Domains,
		NodeTypes:    opts.NodeTypes,
	}

	results, err := db.queryEngine.HybridQuery(embedding, nil, hybridOpts)
	if err != nil {
		return nil, err
	}

	return ExtendResults(results), nil
}

// textSearchLocked performs text search assuming the RLock is held.
func (db *BleveIntegratedDB) textSearchLocked(
	ctx context.Context,
	query string,
	opts *HybridSearchOptions,
) []BleveSearchResult {
	if db.bleveIndex == nil || query == "" {
		return nil
	}

	// Check circuit breaker
	if !db.circuitBreaker.Allow() {
		return nil
	}

	results, err := db.bleveIndex.Search(ctx, query, opts.TextLimit)
	if err != nil {
		db.circuitBreaker.RecordResult(false)
		return nil
	}

	db.circuitBreaker.RecordResult(true)
	return results
}

// =============================================================================
// Component Access
// =============================================================================

// VectorDB returns the underlying VectorGraphDB.
func (db *BleveIntegratedDB) VectorDB() *VectorGraphDB {
	return db.vectorDB
}

// QueryEngine returns the underlying QueryEngine.
func (db *BleveIntegratedDB) QueryEngine() *QueryEngine {
	return db.queryEngine
}

// BleveIndex returns the underlying Bleve index.
func (db *BleveIntegratedDB) BleveIndex() BleveIndex {
	return db.bleveIndex
}

// CircuitBreaker returns the circuit breaker for Bleve operations.
func (db *BleveIntegratedDB) CircuitBreaker() *coreerrors.CircuitBreaker {
	return db.circuitBreaker
}

// =============================================================================
// Lifecycle
// =============================================================================

// Close closes the integrated database.
func (db *BleveIntegratedDB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil
	}
	db.closed = true

	var errs []error

	if db.bleveIndex != nil {
		if err := db.bleveIndex.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close bleve: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// IsClosed returns true if the database is closed.
func (db *BleveIntegratedDB) IsClosed() bool {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.closed
}
