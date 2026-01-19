// Package context provides ES.2 RecencyBasedEviction for the Context Virtualization System.
// RecencyBasedEviction implements a simple eviction strategy that removes the oldest
// entries first while preserving recent turns.
package context

import (
	"context"
	"sort"
	"sync"
	"time"
)

// RecencyEvictionName is the name identifier for the recency-based eviction strategy.
const RecencyEvictionName = "recency_based"

// DefaultRecencyPriority is the default priority for recency-based eviction.
const DefaultRecencyPriority = 100

// RecencyBasedEviction implements EvictionStrategy by evicting oldest entries first.
// It preserves the most recent N turns to maintain conversational coherence.
type RecencyBasedEviction struct {
	mu                  sync.RWMutex
	preserveRecentTurns int
	currentTurn         int
	priority            int
}

// NewRecencyBasedEviction creates a new RecencyBasedEviction with the given configuration.
func NewRecencyBasedEviction(preserveRecentTurns int) *RecencyBasedEviction {
	if preserveRecentTurns < 0 {
		preserveRecentTurns = 0
	}
	return &RecencyBasedEviction{
		preserveRecentTurns: preserveRecentTurns,
		currentTurn:         0,
		priority:            DefaultRecencyPriority,
	}
}

// Name returns the strategy identifier.
func (e *RecencyBasedEviction) Name() string {
	return RecencyEvictionName
}

// Priority returns the strategy priority. Lower values indicate higher priority.
func (e *RecencyBasedEviction) Priority() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.priority
}

// SetPriority sets the strategy priority.
func (e *RecencyBasedEviction) SetPriority(priority int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.priority = priority
}

// SetPreserveRecentTurns updates the number of recent turns to preserve.
func (e *RecencyBasedEviction) SetPreserveRecentTurns(turns int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if turns < 0 {
		turns = 0
	}
	e.preserveRecentTurns = turns
}

// GetPreserveRecentTurns returns the number of recent turns to preserve.
func (e *RecencyBasedEviction) GetPreserveRecentTurns() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.preserveRecentTurns
}

// SetCurrentTurn updates the current turn number for eviction calculations.
func (e *RecencyBasedEviction) SetCurrentTurn(turn int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.currentTurn = turn
}

// GetCurrentTurn returns the current turn number.
func (e *RecencyBasedEviction) GetCurrentTurn() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentTurn
}

// SelectForEviction selects entries to evict based on recency.
// Oldest entries (by timestamp) are selected first, skipping entries from recent turns.
func (e *RecencyBasedEviction) SelectForEviction(
	_ context.Context,
	entries []*ContentEntry,
	targetTokens int,
) ([]*ContentEntry, error) {
	if len(entries) == 0 || targetTokens <= 0 {
		return nil, nil
	}

	preserveThreshold := e.calculatePreserveThreshold()
	candidates := e.filterEvictableCandidates(entries, preserveThreshold)

	if len(candidates) == 0 {
		return nil, nil
	}

	sortedCandidates := sortEntriesByTimestamp(candidates)
	return selectUpToTokenLimit(sortedCandidates, targetTokens), nil
}

// calculatePreserveThreshold returns the minimum turn number to preserve.
func (e *RecencyBasedEviction) calculatePreserveThreshold() int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	threshold := e.currentTurn - e.preserveRecentTurns
	if threshold < 0 {
		return 0
	}
	return threshold
}

// filterEvictableCandidates returns entries that can be evicted (not in recent turns).
func (e *RecencyBasedEviction) filterEvictableCandidates(
	entries []*ContentEntry,
	preserveThreshold int,
) []*ContentEntry {
	var candidates []*ContentEntry
	for _, entry := range entries {
		if entry != nil && entry.TurnNumber < preserveThreshold {
			candidates = append(candidates, entry)
		}
	}
	return candidates
}

// sortEntriesByTimestamp returns a copy sorted from oldest to newest.
func sortEntriesByTimestamp(entries []*ContentEntry) []*ContentEntry {
	sorted := make([]*ContentEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})
	return sorted
}

// selectUpToTokenLimit selects entries until the target token count is reached.
func selectUpToTokenLimit(candidates []*ContentEntry, targetTokens int) []*ContentEntry {
	var selected []*ContentEntry
	var tokensSelected int

	for _, entry := range candidates {
		if tokensSelected >= targetTokens {
			break
		}
		selected = append(selected, entry)
		tokensSelected += entry.TokenCount
	}

	return selected
}

// SelectForEvictionByTurn selects entries to evict based on turn number.
// Entries from earlier turns are selected first.
func (e *RecencyBasedEviction) SelectForEvictionByTurn(
	entries []*ContentEntry,
	targetTokens int,
) []*ContentEntry {
	if len(entries) == 0 || targetTokens <= 0 {
		return nil
	}

	preserveThreshold := e.calculatePreserveThreshold()
	candidates := e.filterEvictableCandidates(entries, preserveThreshold)

	if len(candidates) == 0 {
		return nil
	}

	sortedCandidates := sortEntriesByTurnNumber(candidates)
	return selectUpToTokenLimit(sortedCandidates, targetTokens)
}

// sortEntriesByTurnNumber returns a copy sorted from lowest to highest turn.
func sortEntriesByTurnNumber(entries []*ContentEntry) []*ContentEntry {
	sorted := make([]*ContentEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].TurnNumber < sorted[j].TurnNumber
	})
	return sorted
}

// EstimateEvictableTokens returns the total tokens available for eviction.
// This does not actually evict anything, just calculates the potential.
func (e *RecencyBasedEviction) EstimateEvictableTokens(entries []*ContentEntry) int {
	if len(entries) == 0 {
		return 0
	}

	preserveThreshold := e.calculatePreserveThreshold()
	var total int

	for _, entry := range entries {
		if entry != nil && entry.TurnNumber < preserveThreshold {
			total += entry.TokenCount
		}
	}

	return total
}

// RecencyEvictionSummary contains metadata about a recency-based eviction operation.
type RecencyEvictionSummary struct {
	EntriesEvicted int
	TokensFreed    int
	OldestEvicted  time.Time
	NewestEvicted  time.Time
}

// CreateRecencyEvictionSummary creates a summary from selected entries.
func CreateRecencyEvictionSummary(entries []*ContentEntry) *RecencyEvictionSummary {
	if len(entries) == 0 {
		return &RecencyEvictionSummary{}
	}

	summary := &RecencyEvictionSummary{
		EntriesEvicted: len(entries),
		OldestEvicted:  entries[0].Timestamp,
		NewestEvicted:  entries[0].Timestamp,
	}

	for _, entry := range entries {
		summary.TokensFreed += entry.TokenCount
		summary.OldestEvicted = earlierTime(summary.OldestEvicted, entry.Timestamp)
		summary.NewestEvicted = laterTime(summary.NewestEvicted, entry.Timestamp)
	}

	return summary
}

// earlierTime returns the earlier of two times.
func earlierTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// laterTime returns the later of two times.
func laterTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
