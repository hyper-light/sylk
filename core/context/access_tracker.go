// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.4.3: AccessTracker - bounded-memory access pattern tracking.
package context

import (
	"sort"
	"sync"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultAccessWindowSize is the default number of turns to track access counts.
const DefaultAccessWindowSize = 100

// DefaultMaxAccessEntries is the default maximum entries in the lastAccess map.
const DefaultMaxAccessEntries = 10000

// DefaultMaxAccessLogSize is the default size of the access log ring buffer.
const DefaultMaxAccessLogSize = 1000

// AccessSource constants for tracking how content was accessed.
const (
	AccessSourceInResponse   = "in_response"
	AccessSourceToolRetrieve = "tool_retrieved"
	AccessSourcePrefetched   = "prefetched"
)

// =============================================================================
// Access Event
// =============================================================================

// AccessEvent records a single content access.
type AccessEvent struct {
	ContentID string    `json:"content_id"`
	Turn      int       `json:"turn"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
}

// =============================================================================
// Access Tracker
// =============================================================================

// AccessTracker provides bounded-memory access pattern tracking.
// Implements the resources.EvictableCache interface.
type AccessTracker struct {
	mu sync.RWMutex

	// accessCounts tracks count per content ID, keyed by turn then content ID
	// Limited to last windowSize turns
	accessCounts map[int]map[string]int

	// lastAccess tracks when each content was last accessed
	// LRU eviction when exceeding maxEntries
	lastAccess map[string]time.Time
	lruOrder   []string

	// accessLog is a ring buffer of recent access events
	accessLog     []AccessEvent
	logHead       int
	logCount      int

	// Configuration
	windowSize  int
	maxEntries  int
	maxLogSize  int
	currentTurn int
}

// AccessTrackerConfig holds configuration for the access tracker.
type AccessTrackerConfig struct {
	WindowSize int
	MaxEntries int
	MaxLogSize int
}

// NewAccessTracker creates a new access tracker with the given config.
func NewAccessTracker(config AccessTrackerConfig) *AccessTracker {
	if config.WindowSize <= 0 {
		config.WindowSize = DefaultAccessWindowSize
	}
	if config.MaxEntries <= 0 {
		config.MaxEntries = DefaultMaxAccessEntries
	}
	if config.MaxLogSize <= 0 {
		config.MaxLogSize = DefaultMaxAccessLogSize
	}

	return &AccessTracker{
		accessCounts: make(map[int]map[string]int),
		lastAccess:   make(map[string]time.Time),
		lruOrder:     make([]string, 0, config.MaxEntries),
		accessLog:    make([]AccessEvent, config.MaxLogSize),
		windowSize:   config.WindowSize,
		maxEntries:   config.MaxEntries,
		maxLogSize:   config.MaxLogSize,
	}
}

// NewDefaultAccessTracker creates a tracker with default configuration.
func NewDefaultAccessTracker() *AccessTracker {
	return NewAccessTracker(AccessTrackerConfig{})
}

// =============================================================================
// Recording Methods
// =============================================================================

// RecordAccess records an access to content.
func (t *AccessTracker) RecordAccess(contentID string, turn int, source string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.recordAccessCount(contentID, turn)
	t.updateLastAccess(contentID)
	t.recordAccessEvent(contentID, turn, source)
	t.pruneOldTurns(turn)
}

func (t *AccessTracker) recordAccessCount(contentID string, turn int) {
	if t.accessCounts[turn] == nil {
		t.accessCounts[turn] = make(map[string]int)
	}
	t.accessCounts[turn][contentID]++
}

func (t *AccessTracker) updateLastAccess(contentID string) {
	now := time.Now()

	// Check if already in LRU
	if _, exists := t.lastAccess[contentID]; exists {
		t.lastAccess[contentID] = now
		t.moveToFront(contentID)
		return
	}

	// Add new entry
	t.lastAccess[contentID] = now
	t.lruOrder = append(t.lruOrder, contentID)

	// Evict LRU if at capacity
	t.evictLRUIfNeeded()
}

func (t *AccessTracker) moveToFront(contentID string) {
	for i, id := range t.lruOrder {
		if id == contentID {
			// Remove from current position
			t.lruOrder = append(t.lruOrder[:i], t.lruOrder[i+1:]...)
			// Add to end (most recent)
			t.lruOrder = append(t.lruOrder, contentID)
			return
		}
	}
}

func (t *AccessTracker) evictLRUIfNeeded() {
	for len(t.lruOrder) > t.maxEntries {
		oldest := t.lruOrder[0]
		t.lruOrder = t.lruOrder[1:]
		delete(t.lastAccess, oldest)
	}
}

func (t *AccessTracker) recordAccessEvent(contentID string, turn int, source string) {
	event := AccessEvent{
		ContentID: contentID,
		Turn:      turn,
		Source:    source,
		Timestamp: time.Now(),
	}

	idx := (t.logHead + t.logCount) % t.maxLogSize
	t.accessLog[idx] = event

	if t.logCount < t.maxLogSize {
		t.logCount++
	} else {
		t.logHead = (t.logHead + 1) % t.maxLogSize
	}
}

func (t *AccessTracker) pruneOldTurns(currentTurn int) {
	t.currentTurn = currentTurn
	minTurn := currentTurn - t.windowSize

	for turn := range t.accessCounts {
		if turn < minTurn {
			delete(t.accessCounts, turn)
		}
	}
}

// =============================================================================
// Query Methods
// =============================================================================

// GetAccessCount returns the total access count for a content ID within the window.
func (t *AccessTracker) GetAccessCount(contentID string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	count := 0
	for _, counts := range t.accessCounts {
		count += counts[contentID]
	}
	return count
}

// GetMostAccessed returns the top N most accessed content IDs.
func (t *AccessTracker) GetMostAccessed(n int) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Aggregate counts across all turns
	totals := make(map[string]int)
	for _, counts := range t.accessCounts {
		for id, count := range counts {
			totals[id] += count
		}
	}

	// Sort by count
	type idCount struct {
		id    string
		count int
	}
	sorted := make([]idCount, 0, len(totals))
	for id, count := range totals {
		sorted = append(sorted, idCount{id, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	// Return top N
	result := make([]string, 0, n)
	for i := 0; i < n && i < len(sorted); i++ {
		result = append(result, sorted[i].id)
	}
	return result
}

// GetLastAccessed returns the last access time for a content ID.
func (t *AccessTracker) GetLastAccessed(contentID string) (time.Time, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	ts, ok := t.lastAccess[contentID]
	return ts, ok
}

// GetRecentEvents returns the most recent N events from the log.
func (t *AccessTracker) GetRecentEvents(n int) []AccessEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if n > t.logCount {
		n = t.logCount
	}

	result := make([]AccessEvent, n)
	for i := 0; i < n; i++ {
		// Read from newest to oldest
		idx := (t.logHead + t.logCount - 1 - i) % t.maxLogSize
		if idx < 0 {
			idx += t.maxLogSize
		}
		result[i] = t.accessLog[idx]
	}
	return result
}

// GetEventsBySource returns events from the log filtered by source.
func (t *AccessTracker) GetEventsBySource(source string, n int) []AccessEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]AccessEvent, 0, n)
	for i := 0; i < t.logCount && len(result) < n; i++ {
		idx := (t.logHead + t.logCount - 1 - i) % t.maxLogSize
		if idx < 0 {
			idx += t.maxLogSize
		}
		if t.accessLog[idx].Source == source {
			result = append(result, t.accessLog[idx])
		}
	}
	return result
}

// =============================================================================
// EvictableCache Interface
// =============================================================================

// Name returns the name of this cache for identification.
func (t *AccessTracker) Name() string {
	return "access-tracker"
}

// Size returns the approximate memory size in bytes.
func (t *AccessTracker) Size() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.estimateSize()
}

func (t *AccessTracker) estimateSize() int64 {
	var size int64

	// accessCounts: map overhead + entries
	for _, counts := range t.accessCounts {
		size += 8 // map header
		for id := range counts {
			size += int64(len(id)) + 8 // string + int
		}
	}

	// lastAccess: map overhead + entries
	size += 8 // map header
	for id := range t.lastAccess {
		size += int64(len(id)) + 16 // string + time.Time
	}

	// lruOrder: slice overhead + strings
	size += 24 // slice header
	for _, id := range t.lruOrder {
		size += int64(len(id))
	}

	// accessLog: slice of events
	size += int64(t.maxLogSize * 100) // estimate per event

	return size
}

// EvictPercent removes the given percent of entries.
func (t *AccessTracker) EvictPercent(percent float64) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	sizeBefore := t.estimateSize()

	t.evictAccessCounts(percent)
	t.evictLastAccess(percent)
	t.evictAccessLog(percent)

	sizeAfter := t.estimateSize()
	return sizeBefore - sizeAfter
}

func (t *AccessTracker) evictAccessCounts(percent float64) {
	// Remove oldest turns
	turns := make([]int, 0, len(t.accessCounts))
	for turn := range t.accessCounts {
		turns = append(turns, turn)
	}
	sort.Ints(turns)

	toRemove := int(float64(len(turns)) * percent)
	for i := 0; i < toRemove && i < len(turns); i++ {
		delete(t.accessCounts, turns[i])
	}
}

func (t *AccessTracker) evictLastAccess(percent float64) {
	toRemove := int(float64(len(t.lruOrder)) * percent)
	for i := 0; i < toRemove && len(t.lruOrder) > 0; i++ {
		oldest := t.lruOrder[0]
		t.lruOrder = t.lruOrder[1:]
		delete(t.lastAccess, oldest)
	}
}

func (t *AccessTracker) evictAccessLog(percent float64) {
	toRemove := int(float64(t.logCount) * percent)
	if toRemove > t.logCount {
		toRemove = t.logCount
	}
	t.logHead = (t.logHead + toRemove) % t.maxLogSize
	t.logCount -= toRemove
}

// =============================================================================
// Statistics Methods
// =============================================================================

// Stats returns current tracker statistics.
type AccessTrackerStats struct {
	TotalContentTracked int
	TotalAccessEvents   int
	TurnsTracked        int
	CurrentTurn         int
	WindowSize          int
	MaxEntries          int
	MaxLogSize          int
	MemorySize          int64
}

// GetStats returns current tracker statistics.
func (t *AccessTracker) GetStats() AccessTrackerStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return AccessTrackerStats{
		TotalContentTracked: len(t.lastAccess),
		TotalAccessEvents:   t.logCount,
		TurnsTracked:        len(t.accessCounts),
		CurrentTurn:         t.currentTurn,
		WindowSize:          t.windowSize,
		MaxEntries:          t.maxEntries,
		MaxLogSize:          t.maxLogSize,
		MemorySize:          t.estimateSize(),
	}
}

// =============================================================================
// Reset Methods
// =============================================================================

// Reset clears all tracked data.
func (t *AccessTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.accessCounts = make(map[int]map[string]int)
	t.lastAccess = make(map[string]time.Time)
	t.lruOrder = make([]string, 0, t.maxEntries)
	t.accessLog = make([]AccessEvent, t.maxLogSize)
	t.logHead = 0
	t.logCount = 0
	t.currentTurn = 0
}

// =============================================================================
// SetTurn Method
// =============================================================================

// SetTurn updates the current turn and prunes old data.
func (t *AccessTracker) SetTurn(turn int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.pruneOldTurns(turn)
}

// =============================================================================
// Persistence Support
// =============================================================================

// AccessTrackerSnapshot represents a serializable snapshot of the tracker state.
type AccessTrackerSnapshot struct {
	AccessCounts map[int]map[string]int `json:"access_counts"`
	LastAccess   map[string]time.Time   `json:"last_access"`
	LRUOrder     []string               `json:"lru_order"`
	Events       []AccessEvent          `json:"events"`
	CurrentTurn  int                    `json:"current_turn"`
}

// Snapshot returns a serializable snapshot of current state.
func (t *AccessTracker) Snapshot() AccessTrackerSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Copy access counts
	counts := make(map[int]map[string]int)
	for turn, turnCounts := range t.accessCounts {
		counts[turn] = make(map[string]int)
		for id, count := range turnCounts {
			counts[turn][id] = count
		}
	}

	// Copy last access
	lastAccess := make(map[string]time.Time)
	for id, ts := range t.lastAccess {
		lastAccess[id] = ts
	}

	// Copy LRU order
	lruOrder := make([]string, len(t.lruOrder))
	copy(lruOrder, t.lruOrder)

	// Copy events
	events := make([]AccessEvent, t.logCount)
	for i := 0; i < t.logCount; i++ {
		idx := (t.logHead + i) % t.maxLogSize
		events[i] = t.accessLog[idx]
	}

	return AccessTrackerSnapshot{
		AccessCounts: counts,
		LastAccess:   lastAccess,
		LRUOrder:     lruOrder,
		Events:       events,
		CurrentTurn:  t.currentTurn,
	}
}

// RestoreFromSnapshot restores state from a snapshot.
func (t *AccessTracker) RestoreFromSnapshot(snap AccessTrackerSnapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.accessCounts = snap.AccessCounts
	if t.accessCounts == nil {
		t.accessCounts = make(map[int]map[string]int)
	}

	t.lastAccess = snap.LastAccess
	if t.lastAccess == nil {
		t.lastAccess = make(map[string]time.Time)
	}

	t.lruOrder = snap.LRUOrder
	if t.lruOrder == nil {
		t.lruOrder = make([]string, 0, t.maxEntries)
	}

	t.currentTurn = snap.CurrentTurn

	// Restore events
	t.accessLog = make([]AccessEvent, t.maxLogSize)
	t.logHead = 0
	t.logCount = len(snap.Events)
	if t.logCount > t.maxLogSize {
		t.logCount = t.maxLogSize
	}
	for i := 0; i < t.logCount; i++ {
		t.accessLog[i] = snap.Events[i]
	}
}
