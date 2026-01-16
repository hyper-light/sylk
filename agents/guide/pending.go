package guide

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Pending Request Store
// =============================================================================

// PendingStore manages pending requests awaiting responses.
// It provides correlation-based lookup for routing responses back to sources.
type PendingStore struct {
	mu sync.RWMutex

	// Pending requests by correlation ID
	pending map[string]*PendingRequest

	// Index by source agent for cleanup
	bySource map[string]map[string]struct{}

	// Index by target agent for monitoring
	byTarget map[string]map[string]struct{}

	// Configuration
	defaultTimeout time.Duration
	maxPerAgent    int
}

// PendingStoreConfig configures the pending store
type PendingStoreConfig struct {
	DefaultTimeout time.Duration // Default: 5 minutes
	MaxPerAgent    int           // Default: 1000
}

// DefaultPendingStoreConfig returns sensible defaults
func DefaultPendingStoreConfig() PendingStoreConfig {
	return PendingStoreConfig{
		DefaultTimeout: 5 * time.Minute,
		MaxPerAgent:    1000,
	}
}

// NewPendingStore creates a new pending request store
func NewPendingStore(cfg PendingStoreConfig) *PendingStore {
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 5 * time.Minute
	}
	if cfg.MaxPerAgent == 0 {
		cfg.MaxPerAgent = 1000
	}

	return &PendingStore{
		pending:        make(map[string]*PendingRequest),
		bySource:       make(map[string]map[string]struct{}),
		byTarget:       make(map[string]map[string]struct{}),
		defaultTimeout: cfg.DefaultTimeout,
		maxPerAgent:    cfg.MaxPerAgent,
	}
}

// =============================================================================
// Store Operations
// =============================================================================

// Add stores a new pending request and returns the correlation ID
func (ps *PendingStore) Add(req *RouteRequest, classification *RouteResult, targetAgentID string) string {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Generate correlation ID if not provided
	corrID := req.CorrelationID
	if corrID == "" {
		corrID = uuid.New().String()
	}

	now := time.Now()
	pending := &PendingRequest{
		CorrelationID:   corrID,
		SourceAgentID:   req.SourceAgentID,
		SourceAgentName: req.SourceAgentName,
		TargetAgentID:   targetAgentID,
		Request:         req,
		Classification:  classification,
		CreatedAt:       now,
		ExpiresAt:       now.Add(ps.defaultTimeout),
	}

	// Store by correlation ID
	ps.pending[corrID] = pending

	// Index by source
	if ps.bySource[req.SourceAgentID] == nil {
		ps.bySource[req.SourceAgentID] = make(map[string]struct{})
	}
	ps.bySource[req.SourceAgentID][corrID] = struct{}{}

	// Index by target
	if ps.byTarget[targetAgentID] == nil {
		ps.byTarget[targetAgentID] = make(map[string]struct{})
	}
	ps.byTarget[targetAgentID][corrID] = struct{}{}

	return corrID
}

// Get retrieves a pending request by correlation ID
func (ps *PendingStore) Get(correlationID string) *PendingRequest {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	return ps.pending[correlationID]
}

// Remove removes a pending request and returns it
func (ps *PendingStore) Remove(correlationID string) *PendingRequest {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	pending, ok := ps.pending[correlationID]
	if !ok {
		return nil
	}

	// Remove from main store
	delete(ps.pending, correlationID)

	// Remove from source index
	if sourceIdx := ps.bySource[pending.SourceAgentID]; sourceIdx != nil {
		delete(sourceIdx, correlationID)
		if len(sourceIdx) == 0 {
			delete(ps.bySource, pending.SourceAgentID)
		}
	}

	// Remove from target index
	if targetIdx := ps.byTarget[pending.TargetAgentID]; targetIdx != nil {
		delete(targetIdx, correlationID)
		if len(targetIdx) == 0 {
			delete(ps.byTarget, pending.TargetAgentID)
		}
	}

	return pending
}

// GetBySource returns all pending requests from a source agent
func (ps *PendingStore) GetBySource(sourceAgentID string) []*PendingRequest {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	corrIDs := ps.bySource[sourceAgentID]
	if corrIDs == nil {
		return nil
	}

	result := make([]*PendingRequest, 0, len(corrIDs))
	for corrID := range corrIDs {
		if pending := ps.pending[corrID]; pending != nil {
			result = append(result, pending)
		}
	}
	return result
}

// GetByTarget returns all pending requests to a target agent
func (ps *PendingStore) GetByTarget(targetAgentID string) []*PendingRequest {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	corrIDs := ps.byTarget[targetAgentID]
	if corrIDs == nil {
		return nil
	}

	result := make([]*PendingRequest, 0, len(corrIDs))
	for corrID := range corrIDs {
		if pending := ps.pending[corrID]; pending != nil {
			result = append(result, pending)
		}
	}
	return result
}

// =============================================================================
// Expiration
// =============================================================================

// CleanupExpired removes expired pending requests and returns them
func (ps *PendingStore) CleanupExpired() []*PendingRequest {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	now := time.Now()
	var expired []*PendingRequest

	for corrID, pending := range ps.pending {
		if now.After(pending.ExpiresAt) {
			expired = append(expired, pending)

			// Remove from main store
			delete(ps.pending, corrID)

			// Remove from source index
			if sourceIdx := ps.bySource[pending.SourceAgentID]; sourceIdx != nil {
				delete(sourceIdx, corrID)
				if len(sourceIdx) == 0 {
					delete(ps.bySource, pending.SourceAgentID)
				}
			}

			// Remove from target index
			if targetIdx := ps.byTarget[pending.TargetAgentID]; targetIdx != nil {
				delete(targetIdx, corrID)
				if len(targetIdx) == 0 {
					delete(ps.byTarget, pending.TargetAgentID)
				}
			}
		}
	}

	return expired
}

// =============================================================================
// Statistics
// =============================================================================

// PendingStats contains statistics about pending requests
type PendingStats struct {
	TotalPending   int            `json:"total_pending"`
	BySource       map[string]int `json:"by_source"`
	ByTarget       map[string]int `json:"by_target"`
	OldestPending  time.Time      `json:"oldest_pending,omitempty"`
	NextExpiration time.Time      `json:"next_expiration,omitempty"`
}

// Stats returns statistics about pending requests
func (ps *PendingStore) Stats() PendingStats {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	stats := PendingStats{
		TotalPending: len(ps.pending),
		BySource:     make(map[string]int),
		ByTarget:     make(map[string]int),
	}

	for sourceID, corrIDs := range ps.bySource {
		stats.BySource[sourceID] = len(corrIDs)
	}

	for targetID, corrIDs := range ps.byTarget {
		stats.ByTarget[targetID] = len(corrIDs)
	}

	// Find oldest and next expiration
	var oldest, nextExp time.Time
	for _, pending := range ps.pending {
		if oldest.IsZero() || pending.CreatedAt.Before(oldest) {
			oldest = pending.CreatedAt
		}
		if nextExp.IsZero() || pending.ExpiresAt.Before(nextExp) {
			nextExp = pending.ExpiresAt
		}
	}
	stats.OldestPending = oldest
	stats.NextExpiration = nextExp

	return stats
}

// Count returns the number of pending requests
func (ps *PendingStore) Count() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.pending)
}

// CountBySource returns pending request count for a source agent
func (ps *PendingStore) CountBySource(sourceAgentID string) int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.bySource[sourceAgentID])
}

// CountByTarget returns pending request count for a target agent
func (ps *PendingStore) CountByTarget(targetAgentID string) int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.byTarget[targetAgentID])
}
