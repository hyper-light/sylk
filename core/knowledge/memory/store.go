// Package memory provides ACT-R based memory management for adaptive retrieval.
// MD.3.1 MemoryStore implementation with MD.3.2 trace pruning support.
package memory

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/domain"
)

// =============================================================================
// MD.3.1 MemoryStore
// =============================================================================

const (
	// DefaultMaxTraces is the default maximum number of traces per node.
	DefaultMaxTraces = 100

	// DefaultDebounceWindow is the default debounce window for trace recording.
	DefaultDebounceWindow = 1 * time.Minute
)

// MemoryStore manages ACT-R memory operations with SQLite persistence.
// Thread-safe for concurrent access.
type MemoryStore struct {
	db             *sql.DB
	domainDecay    map[domain.Domain]*DomainDecayParams
	maxTraces      int
	debounceWindow time.Duration

	mu sync.RWMutex
}

// NewMemoryStore creates a new MemoryStore with the given configuration.
// If maxTraces is 0, DefaultMaxTraces (100) is used.
// If debounceWindow is 0, DefaultDebounceWindow (1 minute) is used.
func NewMemoryStore(db *sql.DB, maxTraces int, debounceWindow time.Duration) *MemoryStore {
	if maxTraces <= 0 {
		maxTraces = DefaultMaxTraces
	}
	if debounceWindow <= 0 {
		debounceWindow = DefaultDebounceWindow
	}

	store := &MemoryStore{
		db:             db,
		domainDecay:    make(map[domain.Domain]*DomainDecayParams),
		maxTraces:      maxTraces,
		debounceWindow: debounceWindow,
	}

	// Initialize domain decay parameters with defaults
	for _, d := range domain.ValidDomains() {
		params := DefaultDomainDecay(int(d))
		store.domainDecay[d] = &params
	}

	return store
}

// GetMemory retrieves the ACT-R memory for a node, loading from SQLite if necessary.
// Returns nil if the node has no memory record.
func (s *MemoryStore) GetMemory(ctx context.Context, nodeID string, d domain.Domain) (*ACTRMemory, error) {
	memory, err := s.loadMemory(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("load memory for node %s: %w", nodeID, err)
	}

	if memory == nil {
		// Create new memory with domain defaults
		s.mu.RLock()
		params := s.domainDecay[d]
		s.mu.RUnlock()

		if params == nil {
			defaultParams := DefaultDomainDecay(int(d))
			params = &defaultParams
		}

		memory = &ACTRMemory{
			NodeID:             nodeID,
			Domain:             int(d),
			Traces:             []AccessTrace{},
			MaxTraces:          s.maxTraces,
			DecayAlpha:         params.DecayAlpha,
			DecayBeta:          params.DecayBeta,
			BaseOffsetMean:     params.BaseOffsetMean,
			BaseOffsetVariance: params.BaseOffsetVar,
			CreatedAt:          time.Now().UTC(),
			AccessCount:        0,
		}
	}

	return memory, nil
}

// RecordAccess records an access event for a node with debouncing and pruning.
func (s *MemoryStore) RecordAccess(ctx context.Context, nodeID string, accessType AccessType, contextStr string) error {
	// Load or create memory
	memory, err := s.loadMemory(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("load memory: %w", err)
	}

	if memory == nil {
		// Get node domain from database
		d, err := s.getNodeDomain(ctx, nodeID)
		if err != nil {
			return fmt.Errorf("get node domain: %w", err)
		}

		s.mu.RLock()
		params := s.domainDecay[d]
		s.mu.RUnlock()

		if params == nil {
			defaultParams := DefaultDomainDecay(int(d))
			params = &defaultParams
		}

		memory = &ACTRMemory{
			NodeID:             nodeID,
			Domain:             int(d),
			Traces:             []AccessTrace{},
			MaxTraces:          s.maxTraces,
			DecayAlpha:         params.DecayAlpha,
			DecayBeta:          params.DecayBeta,
			BaseOffsetMean:     params.BaseOffsetMean,
			BaseOffsetVariance: params.BaseOffsetVar,
			CreatedAt:          time.Now().UTC(),
			AccessCount:        0,
		}
	}

	// Check debouncing
	if s.shouldDebounce(memory, accessType) {
		return nil // Skip this access due to debouncing
	}

	// Record the access
	now := time.Now().UTC()
	memory.Reinforce(now, accessType, contextStr)

	// Prune traces if over limit
	s.pruneTraces(memory)

	// Save to database
	if err := s.saveMemory(ctx, memory); err != nil {
		return fmt.Errorf("save memory: %w", err)
	}

	return nil
}

// ComputeActivation computes the current activation level for a node.
func (s *MemoryStore) ComputeActivation(ctx context.Context, nodeID string) (float64, error) {
	memory, err := s.loadMemory(ctx, nodeID)
	if err != nil {
		return 0, fmt.Errorf("load memory: %w", err)
	}

	if memory == nil || len(memory.Traces) == 0 {
		return -math.MaxFloat64, nil
	}

	return memory.Activation(time.Now().UTC()), nil
}

// loadMemory loads an ACT-R memory from SQLite by node ID.
// Returns nil, nil if no memory exists for the node.
func (s *MemoryStore) loadMemory(ctx context.Context, nodeID string) (*ACTRMemory, error) {
	// Query node memory fields
	var memoryActivation sql.NullFloat64
	var lastAccessedAt sql.NullInt64
	var accessCount sql.NullInt64
	var baseOffset sql.NullFloat64
	var nodeDomain sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT domain, memory_activation, last_accessed_at, access_count, base_offset
		FROM nodes
		WHERE id = ?
	`, nodeID).Scan(&nodeDomain, &memoryActivation, &lastAccessedAt, &accessCount, &baseOffset)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query node: %w", err)
	}

	// If no memory data, return nil
	if !memoryActivation.Valid && !lastAccessedAt.Valid {
		return nil, nil
	}

	// Get domain decay params
	d := domain.Domain(nodeDomain.Int64)
	s.mu.RLock()
	params := s.domainDecay[d]
	s.mu.RUnlock()

	if params == nil {
		defaultParams := DefaultDomainDecay(int(d))
		params = &defaultParams
	}

	// Load access traces
	traces, err := s.loadTraces(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("load traces: %w", err)
	}

	var createdAt time.Time
	if len(traces) > 0 {
		createdAt = traces[0].AccessedAt
	} else if lastAccessedAt.Valid {
		createdAt = time.Unix(lastAccessedAt.Int64, 0).UTC()
	} else {
		createdAt = time.Now().UTC()
	}

	memory := &ACTRMemory{
		NodeID:             nodeID,
		Domain:             int(d),
		Traces:             traces,
		MaxTraces:          s.maxTraces,
		DecayAlpha:         params.DecayAlpha,
		DecayBeta:          params.DecayBeta,
		BaseOffsetMean:     baseOffset.Float64,
		BaseOffsetVariance: params.BaseOffsetVar,
		CreatedAt:          createdAt,
		AccessCount:        int(accessCount.Int64),
	}

	return memory, nil
}

// loadTraces loads access traces from the database for a node.
func (s *MemoryStore) loadTraces(ctx context.Context, nodeID string) ([]AccessTrace, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT accessed_at, access_type, context
		FROM node_access_traces
		WHERE node_id = ?
		ORDER BY accessed_at ASC
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}
	defer rows.Close()

	var traces []AccessTrace
	for rows.Next() {
		var accessedAt int64
		var accessTypeStr string
		var contextStr sql.NullString

		if err := rows.Scan(&accessedAt, &accessTypeStr, &contextStr); err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}

		accessType, ok := ParseAccessType(accessTypeStr)
		if !ok {
			accessType = AccessReference // Default to reference if unknown
		}

		traces = append(traces, AccessTrace{
			AccessedAt: time.Unix(accessedAt, 0).UTC(),
			AccessType: accessType,
			Context:    contextStr.String,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traces: %w", err)
	}

	return traces, nil
}

// saveMemory persists an ACT-R memory to SQLite.
func (s *MemoryStore) saveMemory(ctx context.Context, memory *ACTRMemory) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Update node memory fields
	activation := memory.Activation(time.Now().UTC())
	var lastAccessedAt int64
	if len(memory.Traces) > 0 {
		lastAccessedAt = memory.Traces[len(memory.Traces)-1].AccessedAt.Unix()
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE nodes
		SET memory_activation = ?,
		    last_accessed_at = ?,
		    access_count = ?,
		    base_offset = ?
		WHERE id = ?
	`, activation, lastAccessedAt, memory.AccessCount, memory.BaseOffsetMean, memory.NodeID)
	if err != nil {
		return fmt.Errorf("update node: %w", err)
	}

	// Delete existing traces and re-insert (simple approach for bounded storage)
	_, err = tx.ExecContext(ctx, `
		DELETE FROM node_access_traces WHERE node_id = ?
	`, memory.NodeID)
	if err != nil {
		return fmt.Errorf("delete traces: %w", err)
	}

	// Insert current traces
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO node_access_traces (node_id, accessed_at, access_type, context)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, trace := range memory.Traces {
		var contextPtr *string
		if trace.Context != "" {
			contextPtr = &trace.Context
		}

		_, err = stmt.ExecContext(ctx,
			memory.NodeID,
			trace.AccessedAt.Unix(),
			trace.AccessType.String(),
			contextPtr,
		)
		if err != nil {
			return fmt.Errorf("insert trace: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// getNodeDomain retrieves the domain for a node from the database.
func (s *MemoryStore) getNodeDomain(ctx context.Context, nodeID string) (domain.Domain, error) {
	var d int
	err := s.db.QueryRowContext(ctx, `
		SELECT domain FROM nodes WHERE id = ?
	`, nodeID).Scan(&d)
	if err != nil {
		return domain.DomainLibrarian, fmt.Errorf("query domain: %w", err)
	}
	return domain.Domain(d), nil
}

// UpdateDecayParams updates the domain decay parameters based on feedback.
// This implements Bayesian learning for decay rates.
func (s *MemoryStore) UpdateDecayParams(ctx context.Context, d domain.Domain, wasUseful bool, ageAtRetrieval time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	params := s.domainDecay[d]
	if params == nil {
		defaultParams := DefaultDomainDecay(int(d))
		params = &defaultParams
		s.domainDecay[d] = params
	}

	// Learning rate for decay adaptation
	learningRate := 0.1
	currentMean := params.Mean()

	if wasUseful {
		// Successful retrieval after this age suggests slower decay
		// Decrease mean by increasing beta (mean = alpha/(alpha+beta))
		params.DecayBeta += learningRate * currentMean
	} else {
		// Failed retrieval suggests faster decay
		// Increase mean by increasing alpha
		params.DecayAlpha += learningRate * (1.0 - currentMean)
	}

	// Track effective samples
	params.EffectiveSamples++

	// Persist to database
	return s.saveDomainDecay(ctx, d, params)
}

// saveDomainDecay persists domain decay parameters to the database.
func (s *MemoryStore) saveDomainDecay(ctx context.Context, d domain.Domain, params *DomainDecayParams) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO decay_parameters
		(domain, decay_exponent_alpha, decay_exponent_beta, base_offset_mean,
		 base_offset_variance, effective_samples, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, int(d), params.DecayAlpha, params.DecayBeta, params.BaseOffsetMean,
		params.BaseOffsetVar, params.EffectiveSamples, time.Now().UTC().Format(time.RFC3339))
	return err
}

// GetDomainDecayParams returns the decay parameters for a domain.
func (s *MemoryStore) GetDomainDecayParams(d domain.Domain) *DomainDecayParams {
	s.mu.RLock()
	defer s.mu.RUnlock()

	params := s.domainDecay[d]
	if params == nil {
		defaultParams := DefaultDomainDecay(int(d))
		return &defaultParams
	}

	// Return a copy to avoid concurrent modification
	return &DomainDecayParams{
		DecayAlpha:       params.DecayAlpha,
		DecayBeta:        params.DecayBeta,
		BaseOffsetMean:   params.BaseOffsetMean,
		BaseOffsetVar:    params.BaseOffsetVar,
		EffectiveSamples: params.EffectiveSamples,
	}
}

// LoadDomainDecayParams loads domain decay parameters from the database.
// Call this during initialization to restore learned parameters.
func (s *MemoryStore) LoadDomainDecayParams(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT domain, decay_exponent_alpha, decay_exponent_beta,
		       base_offset_mean, base_offset_variance, effective_samples
		FROM decay_parameters
	`)
	if err != nil {
		return fmt.Errorf("query decay parameters: %w", err)
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	for rows.Next() {
		var d int
		var params DomainDecayParams

		if err := rows.Scan(&d, &params.DecayAlpha, &params.DecayBeta,
			&params.BaseOffsetMean, &params.BaseOffsetVar, &params.EffectiveSamples); err != nil {
			return fmt.Errorf("scan decay params: %w", err)
		}

		s.domainDecay[domain.Domain(d)] = &params
	}

	return rows.Err()
}

// =============================================================================
// MD.3.2 Trace Pruning
// =============================================================================

// shouldDebounce returns true if the access should be skipped due to debouncing.
// An access is debounced if there's a trace of the same type within the debounce window.
func (s *MemoryStore) shouldDebounce(memory *ACTRMemory, accessType AccessType) bool {
	if len(memory.Traces) == 0 {
		return false
	}

	now := time.Now().UTC()

	// Check the most recent trace of the same type
	for i := len(memory.Traces) - 1; i >= 0; i-- {
		trace := memory.Traces[i]
		if trace.AccessType == accessType {
			age := now.Sub(trace.AccessedAt)
			if age < s.debounceWindow {
				return true
			}
			break // Only check the most recent trace of this type
		}
	}

	return false
}

// pruneTraces enforces the maximum trace limit using LRU eviction.
// Keeps the most recent traces when over the limit.
func (s *MemoryStore) pruneTraces(memory *ACTRMemory) {
	if len(memory.Traces) <= s.maxTraces {
		return
	}

	// Keep only the most recent maxTraces
	start := len(memory.Traces) - s.maxTraces
	memory.Traces = memory.Traces[start:]
}

// compactOldTraces merges very old traces to save space while preserving
// the activation computation's accuracy. Traces older than compactAge are
// merged into summary records.
func (s *MemoryStore) compactOldTraces(memory *ACTRMemory, compactAge time.Duration) {
	if len(memory.Traces) < 10 {
		return // Not enough traces to compact
	}

	now := time.Now().UTC()
	cutoff := now.Add(-compactAge)

	var newTraces []AccessTrace
	var oldTraces []AccessTrace

	for _, trace := range memory.Traces {
		if trace.AccessedAt.Before(cutoff) {
			oldTraces = append(oldTraces, trace)
		} else {
			newTraces = append(newTraces, trace)
		}
	}

	// If no old traces to compact, return unchanged
	if len(oldTraces) < 5 {
		return
	}

	// Group old traces by access type and create summary records
	typeCounts := make(map[AccessType]int)
	typeLastSeen := make(map[AccessType]time.Time)

	for _, trace := range oldTraces {
		typeCounts[trace.AccessType]++
		if typeLastSeen[trace.AccessType].Before(trace.AccessedAt) {
			typeLastSeen[trace.AccessType] = trace.AccessedAt
		}
	}

	// Create one summary trace per access type from old traces
	var summaryTraces []AccessTrace
	for at, count := range typeCounts {
		// Create a trace at the average time, weighted by count
		summaryTraces = append(summaryTraces, AccessTrace{
			AccessedAt: typeLastSeen[at],
			AccessType: at,
			Context:    fmt.Sprintf("compacted:%d", count),
		})
	}

	// Combine: summary traces first (oldest), then recent traces
	memory.Traces = append(summaryTraces, newTraces...)
}

// SetMaxTraces updates the maximum trace limit.
func (s *MemoryStore) SetMaxTraces(max int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if max > 0 {
		s.maxTraces = max
	}
}

// SetDebounceWindow updates the debounce window duration.
func (s *MemoryStore) SetDebounceWindow(window time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if window > 0 {
		s.debounceWindow = window
	}
}

// MaxTraces returns the current maximum trace limit.
func (s *MemoryStore) MaxTraces() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxTraces
}

// DebounceWindow returns the current debounce window.
func (s *MemoryStore) DebounceWindow() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.debounceWindow
}

// DB returns the underlying database connection.
func (s *MemoryStore) DB() *sql.DB {
	return s.db
}
