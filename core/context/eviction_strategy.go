// Package context provides content storage and context management for multi-agent systems.
// ES.1: EvictionStrategy interface extensions and supporting types for adaptive context eviction.
package context

import (
	"context"
	"sort"
	"sync"
	"time"
)

// EvictionStrategy defines the interface for context eviction strategies.
// Different strategies can be used based on agent type and context characteristics.
type EvictionStrategy interface {
	// SelectForEviction selects entries to evict to free the target number of tokens.
	// It should respect preserved content types and return entries that can be safely evicted.
	SelectForEviction(ctx context.Context, entries []*ContentEntry, targetTokens int) ([]*ContentEntry, error)
}

// EvictionResult contains the outcome of an eviction operation.
type EvictionResult struct {
	EntriesEvicted int `json:"entries_evicted"`
	TokensFreed    int `json:"tokens_freed"`
}

// EvictionStrategyWithPriority extends EvictionStrategy with identification and priority.
// This interface allows strategy selection and ordering when multiple strategies are available.
type EvictionStrategyWithPriority interface {
	EvictionStrategy

	// Name returns the strategy identifier.
	Name() string

	// Priority returns the strategy priority. Lower values indicate higher priority.
	Priority() int
}

// EvictionResultExtended tracks the outcome of an eviction operation with full details.
type EvictionResultExtended struct {
	StrategyName    string        `json:"strategy_name"`
	EntriesEvicted  int           `json:"entries_evicted"`
	TokensFreed     int           `json:"tokens_freed"`
	TokensRequested int           `json:"tokens_requested"`
	Duration        time.Duration `json:"duration"`
	Timestamp       time.Time     `json:"timestamp"`
	EvictedIDs      []string      `json:"evicted_ids,omitempty"`
	PreservedTypes  []ContentType `json:"preserved_types,omitempty"`
}

// Success returns true if the eviction freed at least the requested tokens.
func (r *EvictionResultExtended) Success() bool {
	return r.TokensFreed >= r.TokensRequested
}

// Efficiency returns the ratio of tokens freed to tokens requested.
func (r *EvictionResultExtended) Efficiency() float64 {
	if r.TokensRequested == 0 {
		return 0
	}
	return float64(r.TokensFreed) / float64(r.TokensRequested)
}

// ToBasicResult converts to the basic EvictionResult type.
func (r *EvictionResultExtended) ToBasicResult() *EvictionResult {
	return &EvictionResult{
		EntriesEvicted: r.EntriesEvicted,
		TokensFreed:    r.TokensFreed,
	}
}

// EvictionMetrics tracks eviction performance over time.
type EvictionMetrics struct {
	mu sync.RWMutex

	TotalEvictions      int64         `json:"total_evictions"`
	TotalTokensFreed    int64         `json:"total_tokens_freed"`
	TotalEntriesEvicted int64         `json:"total_entries_evicted"`
	SuccessfulEvictions int64         `json:"successful_evictions"`
	FailedEvictions     int64         `json:"failed_evictions"`
	TotalDuration       time.Duration `json:"total_duration"`
	LastEviction        time.Time     `json:"last_eviction"`

	StrategyStats map[string]*StrategyMetrics `json:"strategy_stats"`
}

// StrategyMetrics tracks performance for a specific eviction strategy.
type StrategyMetrics struct {
	Invocations   int64         `json:"invocations"`
	TokensFreed   int64         `json:"tokens_freed"`
	EntriesEvict  int64         `json:"entries_evicted"`
	TotalDuration time.Duration `json:"total_duration"`
	Successes     int64         `json:"successes"`
	Failures      int64         `json:"failures"`
}

// NewEvictionMetrics creates a new metrics tracker.
func NewEvictionMetrics() *EvictionMetrics {
	return &EvictionMetrics{
		StrategyStats: make(map[string]*StrategyMetrics),
	}
}

// Record adds an eviction result to the metrics.
func (m *EvictionMetrics) Record(result *EvictionResultExtended) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updateGlobalStats(result)
	m.updateStrategyStats(result)
}

func (m *EvictionMetrics) updateGlobalStats(result *EvictionResultExtended) {
	m.TotalEvictions++
	m.TotalTokensFreed += int64(result.TokensFreed)
	m.TotalEntriesEvicted += int64(result.EntriesEvicted)
	m.TotalDuration += result.Duration
	m.LastEviction = result.Timestamp

	if result.Success() {
		m.SuccessfulEvictions++
	} else {
		m.FailedEvictions++
	}
}

func (m *EvictionMetrics) updateStrategyStats(result *EvictionResultExtended) {
	stats := m.getOrCreateStrategyStats(result.StrategyName)
	stats.Invocations++
	stats.TokensFreed += int64(result.TokensFreed)
	stats.EntriesEvict += int64(result.EntriesEvicted)
	stats.TotalDuration += result.Duration

	if result.Success() {
		stats.Successes++
	} else {
		stats.Failures++
	}
}

func (m *EvictionMetrics) getOrCreateStrategyStats(name string) *StrategyMetrics {
	if m.StrategyStats[name] == nil {
		m.StrategyStats[name] = &StrategyMetrics{}
	}
	return m.StrategyStats[name]
}

// SuccessRate returns the overall success rate as a fraction.
func (m *EvictionMetrics) SuccessRate() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.TotalEvictions == 0 {
		return 0
	}
	return float64(m.SuccessfulEvictions) / float64(m.TotalEvictions)
}

// AverageDuration returns the mean duration per eviction.
func (m *EvictionMetrics) AverageDuration() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.TotalEvictions == 0 {
		return 0
	}
	return m.TotalDuration / time.Duration(m.TotalEvictions)
}

// GetStrategyStats returns metrics for a specific strategy.
func (m *EvictionMetrics) GetStrategyStats(name string) *StrategyMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if stats, ok := m.StrategyStats[name]; ok {
		copy := *stats
		return &copy
	}
	return nil
}

// SortEntriesByAge sorts entries oldest first based on timestamp.
func SortEntriesByAge(entries []*ContentEntry) []*ContentEntry {
	if len(entries) == 0 {
		return entries
	}

	sorted := make([]*ContentEntry, len(entries))
	copy(sorted, entries)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	return sorted
}

// SortEntriesByTokenCount sorts entries by token count, largest first.
func SortEntriesByTokenCount(entries []*ContentEntry) []*ContentEntry {
	if len(entries) == 0 {
		return entries
	}

	sorted := make([]*ContentEntry, len(entries))
	copy(sorted, entries)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].TokenCount > sorted[j].TokenCount
	})

	return sorted
}

// FilterByContentTypes returns entries matching any of the specified types.
func FilterByContentTypes(entries []*ContentEntry, types []ContentType) []*ContentEntry {
	if len(entries) == 0 || len(types) == 0 {
		return nil
	}

	typeSet := makeContentTypeSet(types)
	return filterEntriesByTypeSet(entries, typeSet)
}

func makeContentTypeSet(types []ContentType) map[ContentType]struct{} {
	set := make(map[ContentType]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	return set
}

func filterEntriesByTypeSet(entries []*ContentEntry, set map[ContentType]struct{}) []*ContentEntry {
	var result []*ContentEntry
	for _, e := range entries {
		if _, ok := set[e.ContentType]; ok {
			result = append(result, e)
		}
	}
	return result
}

// ExcludeByContentTypes returns entries not matching any of the specified types.
func ExcludeByContentTypes(entries []*ContentEntry, types []ContentType) []*ContentEntry {
	if len(entries) == 0 {
		return nil
	}
	if len(types) == 0 {
		return copyEntriesSlice(entries)
	}

	typeSet := makeContentTypeSet(types)
	return excludeEntriesByTypeSet(entries, typeSet)
}

func excludeEntriesByTypeSet(entries []*ContentEntry, set map[ContentType]struct{}) []*ContentEntry {
	var result []*ContentEntry
	for _, e := range entries {
		if _, ok := set[e.ContentType]; !ok {
			result = append(result, e)
		}
	}
	return result
}

func copyEntriesSlice(entries []*ContentEntry) []*ContentEntry {
	result := make([]*ContentEntry, len(entries))
	copy(result, entries)
	return result
}

// FilterByTurnRange returns entries within the specified turn range [minTurn, maxTurn].
func FilterByTurnRange(entries []*ContentEntry, minTurn, maxTurn int) []*ContentEntry {
	if len(entries) == 0 {
		return nil
	}

	var result []*ContentEntry
	for _, e := range entries {
		if e.TurnNumber >= minTurn && e.TurnNumber <= maxTurn {
			result = append(result, e)
		}
	}
	return result
}

// ExcludeRecentTurns returns entries older than the specified turn threshold.
func ExcludeRecentTurns(entries []*ContentEntry, currentTurn, preserveCount int) []*ContentEntry {
	if len(entries) == 0 {
		return nil
	}

	threshold := currentTurn - preserveCount
	var result []*ContentEntry
	for _, e := range entries {
		if e.TurnNumber < threshold {
			result = append(result, e)
		}
	}
	return result
}

// CalculateTotalTokens returns the sum of token counts for all entries.
func CalculateTotalTokens(entries []*ContentEntry) int {
	total := 0
	for _, e := range entries {
		total += e.TokenCount
	}
	return total
}

// SelectUntilTokenLimit returns entries until the cumulative tokens reach the limit.
func SelectUntilTokenLimit(entries []*ContentEntry, limit int) []*ContentEntry {
	if len(entries) == 0 || limit <= 0 {
		return nil
	}

	var result []*ContentEntry
	total := 0

	for _, e := range entries {
		if total >= limit {
			break
		}
		result = append(result, e)
		total += e.TokenCount
	}

	return result
}

// ExtractEntryIDs returns the IDs of all provided entries.
func ExtractEntryIDs(entries []*ContentEntry) []string {
	if len(entries) == 0 {
		return nil
	}

	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}
