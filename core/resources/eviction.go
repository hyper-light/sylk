package resources

import (
	"math"
	"sort"
	"sync"
	"time"
)

// EvictableEntry represents an entry that can be evicted.
type EvictableEntry interface {
	ID() string
	MemorySize() int64
	TokenCost() int64
	LastAccess() time.Time
}

// EvictionCandidate wraps an entry with its computed score.
type EvictionCandidate struct {
	Entry EvictableEntry
	Score float64
}

// EvictionConfig configures eviction behavior.
type EvictionConfig struct {
	RecencyDecayHours float64
	MinRecencyFactor  float64
}

// DefaultEvictionConfig returns default eviction settings.
func DefaultEvictionConfig() EvictionConfig {
	return EvictionConfig{
		RecencyDecayHours: 24.0,
		MinRecencyFactor:  0.1,
	}
}

// TokenWeightedEvictor implements token-weighted eviction logic.
type TokenWeightedEvictor struct {
	mu     sync.RWMutex
	config EvictionConfig
}

// NewTokenWeightedEvictor creates a new evictor.
func NewTokenWeightedEvictor(config EvictionConfig) *TokenWeightedEvictor {
	return &TokenWeightedEvictor{config: config}
}

// ComputeScore calculates eviction score for an entry.
// EvictionScore = TokenCost / MemorySize * RecencyFactor
// Lower score = evicted first; high token cost entries evicted LAST.
func (e *TokenWeightedEvictor) ComputeScore(entry EvictableEntry) float64 {
	memSize := entry.MemorySize()
	if memSize <= 0 {
		return 0
	}

	tokenCost := entry.TokenCost()
	recencyFactor := e.computeRecencyFactor(entry.LastAccess())

	return (float64(tokenCost) / float64(memSize)) * recencyFactor
}

// computeRecencyFactor decays over hours since last access.
func (e *TokenWeightedEvictor) computeRecencyFactor(lastAccess time.Time) float64 {
	hoursSince := time.Since(lastAccess).Hours()
	if hoursSince <= 0 {
		return 1.0
	}

	decay := math.Exp(-hoursSince / e.config.RecencyDecayHours)
	return math.Max(decay, e.config.MinRecencyFactor)
}

// SelectForEviction returns entries sorted by score (lowest first).
func (e *TokenWeightedEvictor) SelectForEviction(entries []EvictableEntry) []EvictionCandidate {
	if len(entries) == 0 {
		return nil
	}

	candidates := e.scoreCandidates(entries)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score < candidates[j].Score
	})

	return candidates
}

// scoreCandidates computes scores for all entries.
func (e *TokenWeightedEvictor) scoreCandidates(entries []EvictableEntry) []EvictionCandidate {
	candidates := make([]EvictionCandidate, len(entries))
	for i, entry := range entries {
		candidates[i] = EvictionCandidate{
			Entry: entry,
			Score: e.ComputeScore(entry),
		}
	}
	return candidates
}

// SelectToFreeBytes returns entries to evict to free targetBytes.
func (e *TokenWeightedEvictor) SelectToFreeBytes(entries []EvictableEntry, targetBytes int64) []EvictableEntry {
	if targetBytes <= 0 {
		return nil
	}

	candidates := e.SelectForEviction(entries)
	return e.collectUntilTarget(candidates, targetBytes)
}

// collectUntilTarget gathers entries until target is met.
func (e *TokenWeightedEvictor) collectUntilTarget(candidates []EvictionCandidate, targetBytes int64) []EvictableEntry {
	var result []EvictableEntry
	var freed int64

	for _, c := range candidates {
		if freed >= targetBytes {
			break
		}
		result = append(result, c.Entry)
		freed += c.Entry.MemorySize()
	}

	return result
}

// EvictionResult contains eviction operation results.
type EvictionResult struct {
	Evicted    []EvictableEntry
	FreedBytes int64
	Remaining  int64
}

// EvictFromComponent performs eviction on a component's entries.
func (e *TokenWeightedEvictor) EvictFromComponent(
	entries []EvictableEntry,
	current int64,
	budget int64,
	aggressive bool,
) EvictionResult {
	targetPercent := e.targetPercent(aggressive)
	targetBytes := int64(float64(budget) * targetPercent)
	toFree := current - targetBytes

	if toFree <= 0 {
		return EvictionResult{Remaining: current}
	}

	evicted := e.SelectToFreeBytes(entries, toFree)
	return e.buildResult(evicted, current)
}

// targetPercent returns target usage after eviction.
func (e *TokenWeightedEvictor) targetPercent(aggressive bool) float64 {
	if aggressive {
		return 0.50
	}
	return 0.60
}

// buildResult constructs eviction result.
func (e *TokenWeightedEvictor) buildResult(evicted []EvictableEntry, current int64) EvictionResult {
	var freed int64
	for _, entry := range evicted {
		freed += entry.MemorySize()
	}

	return EvictionResult{
		Evicted:    evicted,
		FreedBytes: freed,
		Remaining:  current - freed,
	}
}

// LRUEvictor implements simple LRU eviction.
type LRUEvictor struct{}

// NewLRUEvictor creates a new LRU evictor.
func NewLRUEvictor() *LRUEvictor {
	return &LRUEvictor{}
}

// SelectForEviction returns entries sorted by last access (oldest first).
func (l *LRUEvictor) SelectForEviction(entries []EvictableEntry) []EvictableEntry {
	if len(entries) == 0 {
		return nil
	}

	sorted := make([]EvictableEntry, len(entries))
	copy(sorted, entries)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LastAccess().Before(sorted[j].LastAccess())
	})

	return sorted
}

// SelectToFreeBytes returns oldest entries to free targetBytes.
func (l *LRUEvictor) SelectToFreeBytes(entries []EvictableEntry, targetBytes int64) []EvictableEntry {
	if targetBytes <= 0 {
		return nil
	}

	sorted := l.SelectForEviction(entries)
	return l.collectUntilTarget(sorted, targetBytes)
}

// collectUntilTarget gathers entries until target is met.
func (l *LRUEvictor) collectUntilTarget(entries []EvictableEntry, targetBytes int64) []EvictableEntry {
	var result []EvictableEntry
	var freed int64

	for _, entry := range entries {
		if freed >= targetBytes {
			break
		}
		result = append(result, entry)
		freed += entry.MemorySize()
	}

	return result
}

// EvictionPolicy defines the eviction strategy.
type EvictionPolicy int

const (
	EvictionPolicyLRU EvictionPolicy = iota
	EvictionPolicyTokenWeighted
)

// EvictionManager coordinates eviction across components.
type EvictionManager struct {
	mu                sync.RWMutex
	tokenEvictor      *TokenWeightedEvictor
	lruEvictor        *LRUEvictor
	componentPolicies map[ComponentName]EvictionPolicy
}

// NewEvictionManager creates a new eviction manager.
func NewEvictionManager(config EvictionConfig) *EvictionManager {
	return &EvictionManager{
		tokenEvictor:      NewTokenWeightedEvictor(config),
		lruEvictor:        NewLRUEvictor(),
		componentPolicies: make(map[ComponentName]EvictionPolicy),
	}
}

// SetPolicy sets the eviction policy for a component.
func (m *EvictionManager) SetPolicy(component ComponentName, policy EvictionPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.componentPolicies[component] = policy
}

// GetPolicy returns the eviction policy for a component.
func (m *EvictionManager) GetPolicy(component ComponentName) EvictionPolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.componentPolicies[component]
}

// Evict performs eviction using the component's configured policy.
func (m *EvictionManager) Evict(
	component ComponentName,
	entries []EvictableEntry,
	current int64,
	budget int64,
	aggressive bool,
) EvictionResult {
	policy := m.GetPolicy(component)
	return m.evictWithPolicy(policy, entries, current, budget, aggressive)
}

// evictWithPolicy dispatches to the appropriate evictor.
func (m *EvictionManager) evictWithPolicy(
	policy EvictionPolicy,
	entries []EvictableEntry,
	current int64,
	budget int64,
	aggressive bool,
) EvictionResult {
	if policy == EvictionPolicyTokenWeighted {
		return m.tokenEvictor.EvictFromComponent(entries, current, budget, aggressive)
	}
	return m.lruEvict(entries, current, budget, aggressive)
}

// lruEvict performs LRU eviction.
func (m *EvictionManager) lruEvict(
	entries []EvictableEntry,
	current int64,
	budget int64,
	aggressive bool,
) EvictionResult {
	targetPercent := m.targetPercent(aggressive)
	targetBytes := int64(float64(budget) * targetPercent)
	toFree := current - targetBytes

	if toFree <= 0 {
		return EvictionResult{Remaining: current}
	}

	evicted := m.lruEvictor.SelectToFreeBytes(entries, toFree)
	return m.buildResult(evicted, current)
}

// targetPercent returns target usage after eviction.
func (m *EvictionManager) targetPercent(aggressive bool) float64 {
	if aggressive {
		return 0.50
	}
	return 0.60
}

// buildResult constructs eviction result.
func (m *EvictionManager) buildResult(evicted []EvictableEntry, current int64) EvictionResult {
	var freed int64
	for _, entry := range evicted {
		freed += entry.MemorySize()
	}

	return EvictionResult{
		Evicted:    evicted,
		FreedBytes: freed,
		Remaining:  current - freed,
	}
}
