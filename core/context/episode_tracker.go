// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.4.1: EpisodeTracker - in-memory state machine tracking episode lifecycle.
package context

import (
	"sync"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultMaxTrackedIDs is the maximum IDs tracked per episode to prevent unbounded growth.
const DefaultMaxTrackedIDs = 1000

// DefaultMaxSearches is the maximum searches tracked per episode.
const DefaultMaxSearches = 100

// ToolCall represents a tool invocation during an episode.
type ToolCall struct {
	Name      string        `json:"name"`
	Arguments string        `json:"arguments"`
	Duration  time.Duration `json:"duration"`
	Success   bool          `json:"success"`
}

// =============================================================================
// Episode Tracker
// =============================================================================

// EpisodeTracker manages in-flight episode state machines.
// Thread-safe for concurrent signal recording during LLM tool execution.
type EpisodeTracker struct {
	mu sync.Mutex

	current    *inFlightEpisode
	maxIDs     int
	maxSearchs int
}

// inFlightEpisode holds state for the current episode being tracked.
type inFlightEpisode struct {
	agentID    string
	agentType  string
	turnNumber int
	startTime  time.Time

	prefetchedIDs map[string]struct{}
	usedIDs       map[string]struct{}
	searchedAfter []string
	toolCalls     []ToolCall
}

// NewEpisodeTracker creates a new episode tracker.
func NewEpisodeTracker() *EpisodeTracker {
	return &EpisodeTracker{
		maxIDs:     DefaultMaxTrackedIDs,
		maxSearchs: DefaultMaxSearches,
	}
}

// NewEpisodeTrackerWithLimits creates a tracker with custom limits.
func NewEpisodeTrackerWithLimits(maxIDs, maxSearches int) *EpisodeTracker {
	if maxIDs <= 0 {
		maxIDs = DefaultMaxTrackedIDs
	}
	if maxSearches <= 0 {
		maxSearches = DefaultMaxSearches
	}
	return &EpisodeTracker{
		maxIDs:     maxIDs,
		maxSearchs: maxSearches,
	}
}

// =============================================================================
// Episode Lifecycle
// =============================================================================

// StartEpisode initializes a new episode for tracking.
// Returns a pointer to the observation being built.
func (t *EpisodeTracker) StartEpisode(agentID, agentType string, turnNumber int) *EpisodeObservation {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.current = &inFlightEpisode{
		agentID:       agentID,
		agentType:     agentType,
		turnNumber:    turnNumber,
		startTime:     time.Now(),
		prefetchedIDs: make(map[string]struct{}),
		usedIDs:       make(map[string]struct{}),
		searchedAfter: make([]string, 0, 8),
		toolCalls:     make([]ToolCall, 0, 8),
	}

	return &EpisodeObservation{
		Timestamp: t.current.startTime,
	}
}

// FinalizeEpisode completes the current episode and returns the observation.
// The response and tool calls are analyzed for completion and hedging signals.
func (t *EpisodeTracker) FinalizeEpisode(response string, toolCalls []ToolCall) EpisodeObservation {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return EpisodeObservation{}
	}

	obs := t.buildObservation(response, toolCalls)
	t.current = nil
	return obs
}

func (t *EpisodeTracker) buildObservation(response string, toolCalls []ToolCall) EpisodeObservation {
	obs := EpisodeObservation{
		Timestamp:       t.current.startTime,
		SessionDuration: time.Since(t.current.startTime),
		PrefetchedIDs:   t.sliceFromMap(t.current.prefetchedIDs),
		UsedIDs:         t.sliceFromMap(t.current.usedIDs),
		SearchedAfter:   t.current.searchedAfter,
		ToolCallCount:   len(t.current.toolCalls) + len(toolCalls),
		TaskCompleted:   detectTaskCompletion(response),
		HedgingDetected: detectHedging(response),
	}

	return obs
}

func (t *EpisodeTracker) sliceFromMap(m map[string]struct{}) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	return result
}

// HasActiveEpisode returns true if an episode is in progress.
func (t *EpisodeTracker) HasActiveEpisode() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.current != nil
}

// =============================================================================
// Signal Recording
// =============================================================================

// RecordPrefetchedIDs records content IDs that were prefetched for this episode.
func (t *EpisodeTracker) RecordPrefetchedIDs(ids []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return
	}

	for _, id := range ids {
		if len(t.current.prefetchedIDs) >= t.maxIDs {
			return
		}
		t.current.prefetchedIDs[id] = struct{}{}
	}
}

// RecordUsedID records a content ID that was referenced in the LLM response.
func (t *EpisodeTracker) RecordUsedID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return
	}

	if len(t.current.usedIDs) >= t.maxIDs {
		return
	}
	t.current.usedIDs[id] = struct{}{}
}

// RecordSearchAfterPrefetch records a search performed after prefetch completed.
// This indicates the prefetch was insufficient.
func (t *EpisodeTracker) RecordSearchAfterPrefetch(query string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return
	}

	if len(t.current.searchedAfter) >= t.maxSearchs {
		return
	}
	t.current.searchedAfter = append(t.current.searchedAfter, query)
}

// RecordToolCall records a tool invocation during the episode.
func (t *EpisodeTracker) RecordToolCall(toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return
	}

	t.current.toolCalls = append(t.current.toolCalls, ToolCall{
		Name: toolName,
	})
}

// RecordToolCallWithDetails records a tool invocation with full details.
func (t *EpisodeTracker) RecordToolCallWithDetails(call ToolCall) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return
	}

	t.current.toolCalls = append(t.current.toolCalls, call)
}

// =============================================================================
// State Queries
// =============================================================================

// GetPrefetchedCount returns the number of prefetched IDs in current episode.
func (t *EpisodeTracker) GetPrefetchedCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return 0
	}
	return len(t.current.prefetchedIDs)
}

// GetUsedCount returns the number of used IDs in current episode.
func (t *EpisodeTracker) GetUsedCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return 0
	}
	return len(t.current.usedIDs)
}

// GetSearchCount returns the number of post-prefetch searches in current episode.
func (t *EpisodeTracker) GetSearchCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return 0
	}
	return len(t.current.searchedAfter)
}

// GetToolCallCount returns the number of tool calls in current episode.
func (t *EpisodeTracker) GetToolCallCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return 0
	}
	return len(t.current.toolCalls)
}

// =============================================================================
// Completion Detection
// =============================================================================

// detectTaskCompletion analyzes response for completion indicators.
func detectTaskCompletion(response string) bool {
	if len(response) == 0 {
		return false
	}
	return containsCompletionIndicator(response)
}

func containsCompletionIndicator(response string) bool {
	indicators := []string{
		"done",
		"complete",
		"finished",
		"successfully",
		"created",
		"implemented",
		"fixed",
		"resolved",
	}

	lower := toLower(response)
	for _, ind := range indicators {
		if textContainsWord(lower, ind) {
			return true
		}
	}
	return false
}

// detectHedging analyzes response for hedging language.
func detectHedging(response string) bool {
	if len(response) == 0 {
		return false
	}
	return containsHedgingIndicator(response)
}

func containsHedgingIndicator(response string) bool {
	indicators := []string{
		"i'm not sure",
		"i don't know",
		"might be",
		"could be",
		"perhaps",
		"possibly",
		"unclear",
		"uncertain",
		"need more context",
		"can't determine",
	}

	lower := toLower(response)
	for _, ind := range indicators {
		if textContainsPhrase(lower, ind) {
			return true
		}
	}
	return false
}

// =============================================================================
// String Utilities
// =============================================================================

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func textContainsWord(text, word string) bool {
	return textContains(text, word)
}

func textContainsPhrase(text, phrase string) bool {
	return textContains(text, phrase)
}

func textContains(text, sub string) bool {
	if len(sub) > len(text) {
		return false
	}

	for i := 0; i <= len(text)-len(sub); i++ {
		if text[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
