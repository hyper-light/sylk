// Package vectorgraphdb provides AR.11.3: PressureAwareSearcher - wraps
// QueryEngine with pressure-aware tier limiting.
package vectorgraphdb

import (
	"sync"
	"sync/atomic"
)

// =============================================================================
// Pressure Level
// =============================================================================

// PressureLevel represents system memory pressure.
type PressureLevel int32

const (
	PressureLevelLow      PressureLevel = 0
	PressureLevelMedium   PressureLevel = 1
	PressureLevelHigh     PressureLevel = 2
	PressureLevelCritical PressureLevel = 3
)

// String returns a string representation of the pressure level.
func (pl PressureLevel) String() string {
	switch pl {
	case PressureLevelLow:
		return "low"
	case PressureLevelMedium:
		return "medium"
	case PressureLevelHigh:
		return "high"
	case PressureLevelCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// =============================================================================
// Tier Limits
// =============================================================================

// MaxTier represents the maximum search tier allowed.
type MaxTier int

const (
	MaxTierFull MaxTier = iota // Full search allowed
	MaxTierWarm                // Warm tier max
	MaxTierHot                 // Hot tier only
	MaxTierNone                // No search allowed
)

// TierForPressure returns the appropriate max tier for a pressure level.
func TierForPressure(level PressureLevel) MaxTier {
	switch level {
	case PressureLevelLow:
		return MaxTierFull
	case PressureLevelMedium:
		return MaxTierWarm
	case PressureLevelHigh:
		return MaxTierHot
	case PressureLevelCritical:
		return MaxTierNone
	default:
		return MaxTierFull
	}
}

// =============================================================================
// Pressure-Aware Searcher
// =============================================================================

// PressureAwareSearcher wraps QueryEngine with pressure-based tier limiting.
type PressureAwareSearcher struct {
	engine  *QueryEngine
	maxTier atomic.Int32
	mu      sync.RWMutex

	// Listeners for tier changes
	listeners []TierChangeListener
}

// TierChangeListener is notified when the max tier changes.
type TierChangeListener func(oldTier, newTier MaxTier)

// NewPressureAwareSearcher creates a new pressure-aware searcher.
func NewPressureAwareSearcher(engine *QueryEngine) *PressureAwareSearcher {
	pas := &PressureAwareSearcher{
		engine:    engine,
		listeners: make([]TierChangeListener, 0),
	}
	pas.maxTier.Store(int32(MaxTierFull))
	return pas
}

// OnPressureChange handles pressure level changes by adjusting maxTier.
func (pas *PressureAwareSearcher) OnPressureChange(level PressureLevel) {
	newMaxTier := TierForPressure(level)
	oldMaxTier := MaxTier(pas.maxTier.Swap(int32(newMaxTier)))

	if oldMaxTier != newMaxTier {
		pas.notifyListeners(oldMaxTier, newMaxTier)
	}
}

// AddTierChangeListener adds a listener for tier changes.
func (pas *PressureAwareSearcher) AddTierChangeListener(listener TierChangeListener) {
	pas.mu.Lock()
	defer pas.mu.Unlock()
	pas.listeners = append(pas.listeners, listener)
}

func (pas *PressureAwareSearcher) notifyListeners(oldTier, newTier MaxTier) {
	pas.mu.RLock()
	listeners := make([]TierChangeListener, len(pas.listeners))
	copy(listeners, pas.listeners)
	pas.mu.RUnlock()

	for _, listener := range listeners {
		listener(oldTier, newTier)
	}
}

// MaxTier returns the current maximum tier.
func (pas *PressureAwareSearcher) MaxTier() MaxTier {
	return MaxTier(pas.maxTier.Load())
}

// Engine returns the underlying query engine.
func (pas *PressureAwareSearcher) Engine() *QueryEngine {
	return pas.engine
}

// =============================================================================
// Search Methods
// =============================================================================

// HybridQuery performs a pressure-aware hybrid query.
// Returns nil results if pressure is critical.
func (pas *PressureAwareSearcher) HybridQuery(
	query []float32,
	seedNodes []string,
	opts *HybridQueryOptions,
) ([]HybridResult, error) {
	maxTier := pas.MaxTier()

	if maxTier == MaxTierNone {
		return nil, nil // Gracefully degrade
	}

	// Adjust options based on tier
	adjustedOpts := pas.adjustOptionsForTier(opts, maxTier)

	return pas.engine.HybridQuery(query, seedNodes, adjustedOpts)
}

// adjustOptionsForTier modifies query options based on max tier.
func (pas *PressureAwareSearcher) adjustOptionsForTier(
	opts *HybridQueryOptions,
	maxTier MaxTier,
) *HybridQueryOptions {
	if opts == nil {
		opts = &HybridQueryOptions{}
	}

	// Create a copy to avoid modifying the original
	adjusted := *opts

	switch maxTier {
	case MaxTierHot:
		// Reduce limit significantly for hot-only
		if adjusted.VectorLimit > 10 {
			adjusted.VectorLimit = 10
		}
		if adjusted.GraphDepth > 1 {
			adjusted.GraphDepth = 1
		}
	case MaxTierWarm:
		// Moderate reduction for warm tier
		if adjusted.VectorLimit > 20 {
			adjusted.VectorLimit = 20
		}
		if adjusted.GraphDepth > 2 {
			adjusted.GraphDepth = 2
		}
	}

	return &adjusted
}

// CanSearch returns true if search is allowed at the current pressure level.
func (pas *PressureAwareSearcher) CanSearch() bool {
	return pas.MaxTier() != MaxTierNone
}

// =============================================================================
// Pressure Stats
// =============================================================================

// PressureStats holds statistics about pressure-aware searching.
type PressureStats struct {
	CurrentTier      MaxTier `json:"current_tier"`
	TierChanges      int64   `json:"tier_changes"`
	QueriesAtHot     int64   `json:"queries_at_hot"`
	QueriesAtWarm    int64   `json:"queries_at_warm"`
	QueriesAtFull    int64   `json:"queries_at_full"`
	QueriesRejected  int64   `json:"queries_rejected"`
}

// PressureAwareSearcherWithStats wraps PressureAwareSearcher with statistics.
type PressureAwareSearcherWithStats struct {
	*PressureAwareSearcher

	tierChanges     atomic.Int64
	queriesAtHot    atomic.Int64
	queriesAtWarm   atomic.Int64
	queriesAtFull   atomic.Int64
	queriesRejected atomic.Int64
}

// NewPressureAwareSearcherWithStats creates a stats-tracking searcher.
func NewPressureAwareSearcherWithStats(engine *QueryEngine) *PressureAwareSearcherWithStats {
	pas := &PressureAwareSearcherWithStats{
		PressureAwareSearcher: NewPressureAwareSearcher(engine),
	}

	// Track tier changes
	pas.AddTierChangeListener(func(_, _ MaxTier) {
		pas.tierChanges.Add(1)
	})

	return pas
}

// HybridQuery performs a stats-tracking hybrid query.
func (pas *PressureAwareSearcherWithStats) HybridQuery(
	query []float32,
	seedNodes []string,
	opts *HybridQueryOptions,
) ([]HybridResult, error) {
	maxTier := pas.MaxTier()

	switch maxTier {
	case MaxTierNone:
		pas.queriesRejected.Add(1)
	case MaxTierHot:
		pas.queriesAtHot.Add(1)
	case MaxTierWarm:
		pas.queriesAtWarm.Add(1)
	case MaxTierFull:
		pas.queriesAtFull.Add(1)
	}

	return pas.PressureAwareSearcher.HybridQuery(query, seedNodes, opts)
}

// Stats returns current pressure statistics.
func (pas *PressureAwareSearcherWithStats) Stats() PressureStats {
	return PressureStats{
		CurrentTier:     pas.MaxTier(),
		TierChanges:     pas.tierChanges.Load(),
		QueriesAtHot:    pas.queriesAtHot.Load(),
		QueriesAtWarm:   pas.queriesAtWarm.Load(),
		QueriesAtFull:   pas.queriesAtFull.Load(),
		QueriesRejected: pas.queriesRejected.Load(),
	}
}
