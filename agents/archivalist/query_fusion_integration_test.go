package archivalist

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/knowledge/coldstart"
)

// =============================================================================
// AE.7.4 Query Fusion Integration Tests
// =============================================================================

// TestQueryFusionIntegration_CombinedBleveVectorSearch tests that the
// ArchivalistRetriever correctly combines Bleve and Vector search results.
func TestQueryFusionIntegration_CombinedBleveVectorSearch(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	// Set up different results for each search type
	bleveEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("bleve-1", events.EventTypeAgentDecision, "Keyword match content"),
		createQueryFusionTestEvent("bleve-2", events.EventTypeUserPrompt, "Another keyword match"),
	}
	vectorEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("vector-1", events.EventTypeAgentDecision, "Semantic match content"),
		createQueryFusionTestEvent("vector-2", events.EventTypeFailure, "Related semantic content"),
	}

	mockBleve.SetResults(bleveEvents)
	mockVector.SetResults(vectorEvents)

	// Search should combine results from both
	opts := coldstart.DefaultSearchOptions()
	opts.Limit = 10
	results, err := retriever.Search(context.Background(), "test query", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should have results from both sources
	if len(results) != 4 {
		t.Errorf("Expected 4 combined results, got %d", len(results))
	}

	// Verify both bleve and vector events are present
	foundBleve := false
	foundVector := false
	for _, r := range results {
		if r.Event.ID == "bleve-1" || r.Event.ID == "bleve-2" {
			foundBleve = true
		}
		if r.Event.ID == "vector-1" || r.Event.ID == "vector-2" {
			foundVector = true
		}
	}

	if !foundBleve {
		t.Error("Expected Bleve results in combined output")
	}
	if !foundVector {
		t.Error("Expected Vector results in combined output")
	}
}

// TestQueryFusionIntegration_DeduplicationAcrossSources tests that duplicate
// events from both sources are properly deduplicated.
func TestQueryFusionIntegration_DeduplicationAcrossSources(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	// Create shared event that appears in both results
	sharedEvent := createQueryFusionTestEvent("shared-1", events.EventTypeAgentDecision, "Shared content")

	// Both sources return the same event plus unique ones
	bleveEvents := []*events.ActivityEvent{
		sharedEvent,
		createQueryFusionTestEvent("bleve-only", events.EventTypeUserPrompt, "Bleve only"),
	}
	vectorEvents := []*events.ActivityEvent{
		sharedEvent, // Duplicate
		createQueryFusionTestEvent("vector-only", events.EventTypeFailure, "Vector only"),
	}

	mockBleve.SetResults(bleveEvents)
	mockVector.SetResults(vectorEvents)

	opts := coldstart.DefaultSearchOptions()
	opts.Limit = 10
	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should have 3 unique results (not 4)
	if len(results) != 3 {
		t.Errorf("Expected 3 deduplicated results, got %d", len(results))
	}

	// Count occurrences of shared event
	sharedCount := 0
	for _, r := range results {
		if r.Event.ID == "shared-1" {
			sharedCount++
		}
	}

	if sharedCount != 1 {
		t.Errorf("Expected shared event to appear exactly once, got %d", sharedCount)
	}
}

// TestQueryFusionIntegration_ScoreBlendingWithColdRatio tests that scores
// are correctly blended using the cold ratio when no user history exists.
func TestQueryFusionIntegration_ScoreBlendingWithColdRatio(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	// Create events with different importance levels
	events := []*events.ActivityEvent{
		createQueryFusionTestEvent("high", events.EventTypeAgentDecision, "High importance"),
		createQueryFusionTestEvent("medium", events.EventTypeToolResult, "Medium importance"),
		createQueryFusionTestEvent("low", events.EventTypeIndexFileAdded, "Low importance"),
	}

	mockBleve.SetResults(events)

	opts := coldstart.DefaultSearchOptions()
	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// All results should use pure cold scoring (BlendRatio = 0)
	for _, r := range results {
		if r.BlendRatio != 0.0 {
			t.Errorf("Expected BlendRatio=0 (pure cold), got %f for %s",
				r.BlendRatio, r.Event.ID)
		}

		// Score should match ColdScore exactly
		if r.Score != r.ColdScore {
			t.Errorf("Expected Score=ColdScore when BlendRatio=0: Score=%f, ColdScore=%f",
				r.Score, r.ColdScore)
		}
	}
}

// TestQueryFusionIntegration_RelevanceRanking tests that results are
// properly ranked by relevance score.
func TestQueryFusionIntegration_RelevanceRanking(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	// Create events with known importance differences
	// AgentDecision (0.90) > UserPrompt (0.80) > ToolResult (0.60) > IndexFileAdded (0.30)
	testEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("lowest", events.EventTypeIndexFileAdded, "Lowest importance"),
		createQueryFusionTestEvent("highest", events.EventTypeAgentDecision, "Highest importance"),
		createQueryFusionTestEvent("medium", events.EventTypeToolResult, "Medium importance"),
		createQueryFusionTestEvent("high", events.EventTypeUserPrompt, "High importance"),
	}

	mockBleve.SetResults(testEvents)

	opts := coldstart.DefaultSearchOptions()
	opts.Limit = 4
	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Results should be sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i-1].Score < results[i].Score {
			t.Errorf("Results not sorted by score: [%d]=%f < [%d]=%f",
				i-1, results[i-1].Score, i, results[i].Score)
		}
	}
}

// TestQueryFusionIntegration_MinScoreFiltering tests that results below
// the minimum score threshold are filtered out.
func TestQueryFusionIntegration_MinScoreFiltering(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	testEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("high", events.EventTypeAgentDecision, "High score"),
		createQueryFusionTestEvent("low", events.EventTypeIndexFileAdded, "Low score"),
	}

	mockBleve.SetResults(testEvents)

	// Set a minimum score that should filter low-scoring events
	opts := coldstart.DefaultSearchOptions()
	opts.MinScore = 0.35 // Should filter IndexFileAdded (0.30 base importance)

	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// All results should be above MinScore
	for _, r := range results {
		if r.Score < opts.MinScore {
			t.Errorf("Result %s with score %f should have been filtered (min=%f)",
				r.Event.ID, r.Score, opts.MinScore)
		}
	}
}

// TestQueryFusionIntegration_EventTypeFiltering tests that results can be
// filtered by event type.
func TestQueryFusionIntegration_EventTypeFiltering(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	testEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("decision", events.EventTypeAgentDecision, "Decision"),
		createQueryFusionTestEvent("prompt", events.EventTypeUserPrompt, "Prompt"),
		createQueryFusionTestEvent("failure", events.EventTypeFailure, "Failure"),
		createQueryFusionTestEvent("tool", events.EventTypeToolCall, "Tool call"),
	}

	mockBleve.SetResults(testEvents)
	mockVector.SetResults(testEvents)

	// Filter to only AgentDecision and Failure types
	opts := coldstart.DefaultSearchOptions()
	opts.EventTypes = []events.EventType{
		events.EventTypeAgentDecision,
		events.EventTypeFailure,
	}

	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should only have filtered types (deduplicated)
	expectedCount := 2
	if len(results) != expectedCount {
		t.Errorf("Expected %d filtered results, got %d", expectedCount, len(results))
	}

	for _, r := range results {
		if r.Event.EventType != events.EventTypeAgentDecision &&
			r.Event.EventType != events.EventTypeFailure {
			t.Errorf("Unexpected event type in filtered results: %s", r.Event.EventType.String())
		}
	}
}

// TestQueryFusionIntegration_LimitRespected tests that the result limit
// is properly applied.
func TestQueryFusionIntegration_LimitRespected(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	// Create many events
	manyEvents := make([]*events.ActivityEvent, 20)
	for i := 0; i < 20; i++ {
		manyEvents[i] = createQueryFusionTestEvent(
			string(rune('a'+i)),
			events.EventTypeAgentDecision,
			"Content",
		)
	}

	mockBleve.SetResults(manyEvents[:10])
	mockVector.SetResults(manyEvents[10:])

	opts := coldstart.DefaultSearchOptions()
	opts.Limit = 5

	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) > opts.Limit {
		t.Errorf("Expected at most %d results, got %d", opts.Limit, len(results))
	}
}

// TestQueryFusionIntegration_ParallelSearchExecution tests that Bleve and
// Vector searches are executed in parallel.
func TestQueryFusionIntegration_ParallelSearchExecution(t *testing.T) {
	mockBleve := newDelayedQueryFusionMockBleveSearcher(100 * time.Millisecond)
	mockVector := newDelayedQueryFusionMockVectorSearcher(100 * time.Millisecond)

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	testEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("test-1", events.EventTypeAgentDecision, "Content"),
	}
	mockBleve.SetResults(testEvents)
	mockVector.SetResults(testEvents)

	start := time.Now()
	opts := coldstart.DefaultSearchOptions()
	_, err := retriever.Search(context.Background(), "test", nil, opts)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// If parallel, should take ~100ms; if sequential, ~200ms
	if elapsed > 180*time.Millisecond {
		t.Errorf("Searches appear sequential: elapsed=%v (expected ~100ms for parallel)", elapsed)
	}

	t.Logf("Parallel search elapsed: %v", elapsed)
}

// TestQueryFusionIntegration_BleveOnlySearch tests behavior when only
// Bleve searcher is configured.
func TestQueryFusionIntegration_BleveOnlySearch(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, nil)

	testEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("bleve-1", events.EventTypeAgentDecision, "Content"),
		createQueryFusionTestEvent("bleve-2", events.EventTypeUserPrompt, "Content"),
	}
	mockBleve.SetResults(testEvents)

	opts := coldstart.DefaultSearchOptions()
	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results from Bleve-only, got %d", len(results))
	}
}

// TestQueryFusionIntegration_VectorOnlySearch tests behavior when only
// Vector searcher is configured.
func TestQueryFusionIntegration_VectorOnlySearch(t *testing.T) {
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, nil, mockVector)

	testEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("vector-1", events.EventTypeAgentDecision, "Content"),
		createQueryFusionTestEvent("vector-2", events.EventTypeFailure, "Content"),
	}
	mockVector.SetResults(testEvents)

	opts := coldstart.DefaultSearchOptions()
	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results from Vector-only, got %d", len(results))
	}
}

// TestQueryFusionIntegration_ExplanationGenerated tests that scored results
// include explanations of how scores were computed.
func TestQueryFusionIntegration_ExplanationGenerated(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, nil)

	testEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("test-1", events.EventTypeAgentDecision, "Content"),
	}
	mockBleve.SetResults(testEvents)

	opts := coldstart.DefaultSearchOptions()
	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected at least one result")
	}

	// Each result should have an explanation
	for _, r := range results {
		if r.Explanation == "" {
			t.Errorf("Expected non-empty explanation for %s", r.Event.ID)
		}
	}
}

// TestQueryFusionIntegration_EmptyResults tests handling of empty search results.
func TestQueryFusionIntegration_EmptyResults(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	// No results configured
	mockBleve.SetResults([]*events.ActivityEvent{})
	mockVector.SetResults([]*events.ActivityEvent{})

	opts := coldstart.DefaultSearchOptions()
	results, err := retriever.Search(context.Background(), "nonexistent", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results for empty search, got %d", len(results))
	}
}

// TestQueryFusionIntegration_ConcurrentSearchRequests tests that multiple
// concurrent search requests are handled correctly.
func TestQueryFusionIntegration_ConcurrentSearchRequests(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()
	mockVector := newQueryFusionMockVectorSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, mockVector)

	testEvents := []*events.ActivityEvent{
		createQueryFusionTestEvent("test-1", events.EventTypeAgentDecision, "Content"),
	}
	mockBleve.SetResults(testEvents)
	mockVector.SetResults(testEvents)

	var wg sync.WaitGroup
	numGoroutines := 10
	errors := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			opts := coldstart.DefaultSearchOptions()
			_, err := retriever.Search(context.Background(), "concurrent test", nil, opts)
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// All searches should succeed
	for i, err := range errors {
		if err != nil {
			t.Errorf("Concurrent search %d failed: %v", i, err)
		}
	}
}

// TestQueryFusionIntegration_ResultsRetainEventData tests that results
// retain the full event data from the original events.
func TestQueryFusionIntegration_ResultsRetainEventData(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, nil)

	testEvent := events.NewActivityEvent(
		events.EventTypeAgentDecision,
		"session-retain",
		"Test content for retention",
	)
	testEvent.AgentID = "agent-retain"
	testEvent.Summary = "Test summary"
	testEvent.Keywords = []string{"test", "retention"}
	testEvent.Category = "testing"
	testEvent.Data = map[string]any{"key": "value"}

	mockBleve.SetResults([]*events.ActivityEvent{testEvent})

	opts := coldstart.DefaultSearchOptions()
	results, err := retriever.Search(context.Background(), "test", nil, opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	result := results[0].Event

	// Verify all fields retained
	if result.ID != testEvent.ID {
		t.Errorf("ID mismatch: %s vs %s", result.ID, testEvent.ID)
	}
	if result.Content != testEvent.Content {
		t.Errorf("Content mismatch")
	}
	if result.Summary != testEvent.Summary {
		t.Errorf("Summary mismatch")
	}
	if result.AgentID != testEvent.AgentID {
		t.Errorf("AgentID mismatch")
	}
	if result.Category != testEvent.Category {
		t.Errorf("Category mismatch")
	}
	if len(result.Keywords) != len(testEvent.Keywords) {
		t.Errorf("Keywords length mismatch")
	}
	if result.Data["key"] != testEvent.Data["key"] {
		t.Errorf("Data mismatch")
	}
}

// TestQueryFusionIntegration_ScoreSortingStability tests that score sorting
// is stable and consistent across runs.
func TestQueryFusionIntegration_ScoreSortingStability(t *testing.T) {
	mockBleve := newQueryFusionMockBleveSearcher()

	retriever := coldstart.NewArchivalistRetrieverWithInterfaces(nil, mockBleve, nil)

	// Create events with same type (same base score)
	testEvents := make([]*events.ActivityEvent, 10)
	for i := 0; i < 10; i++ {
		testEvents[i] = createQueryFusionTestEvent(
			string(rune('a'+i)),
			events.EventTypeAgentDecision,
			"Same content",
		)
	}

	mockBleve.SetResults(testEvents)

	opts := coldstart.DefaultSearchOptions()
	opts.Limit = 10

	// Run multiple times and check consistency
	var previousIDs []string
	for run := 0; run < 3; run++ {
		results, err := retriever.Search(context.Background(), "test", nil, opts)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		currentIDs := make([]string, len(results))
		for i, r := range results {
			currentIDs[i] = r.Event.ID
		}

		// Verify sorted by score
		for i := 1; i < len(results); i++ {
			if results[i-1].Score < results[i].Score {
				t.Errorf("Results not sorted in run %d", run)
			}
		}

		// After first run, compare with previous
		if previousIDs != nil {
			// Sort both for comparison (order may vary for same scores)
			sort.Strings(previousIDs)
			sort.Strings(currentIDs)
			for i := range currentIDs {
				if currentIDs[i] != previousIDs[i] {
					t.Errorf("Different event sets across runs")
				}
			}
		}
		previousIDs = currentIDs
	}
}

// =============================================================================
// Mock Implementations for Query Fusion Tests
// =============================================================================

// queryFusionMockBleveSearcher implements BleveSearcherInterface for testing.
type queryFusionMockBleveSearcher struct {
	mu      sync.Mutex
	results []*events.ActivityEvent
	err     error
}

func newQueryFusionMockBleveSearcher() *queryFusionMockBleveSearcher {
	return &queryFusionMockBleveSearcher{
		results: make([]*events.ActivityEvent, 0),
	}
}

func (m *queryFusionMockBleveSearcher) Search(ctx context.Context, query string, limit int) ([]*events.ActivityEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}
	if len(m.results) > limit {
		return m.results[:limit], nil
	}
	return m.results, nil
}

func (m *queryFusionMockBleveSearcher) SetResults(results []*events.ActivityEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = results
}

// queryFusionMockVectorSearcher implements VectorSearcherInterface for testing.
type queryFusionMockVectorSearcher struct {
	mu      sync.Mutex
	results []*events.ActivityEvent
	err     error
}

func newQueryFusionMockVectorSearcher() *queryFusionMockVectorSearcher {
	return &queryFusionMockVectorSearcher{
		results: make([]*events.ActivityEvent, 0),
	}
}

func (m *queryFusionMockVectorSearcher) Search(ctx context.Context, queryText string, limit int) ([]*events.ActivityEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}
	if len(m.results) > limit {
		return m.results[:limit], nil
	}
	return m.results, nil
}

func (m *queryFusionMockVectorSearcher) SetResults(results []*events.ActivityEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = results
}

// delayedQueryFusionMockBleveSearcher adds artificial delay to simulate I/O.
type delayedQueryFusionMockBleveSearcher struct {
	*queryFusionMockBleveSearcher
	delay time.Duration
}

func newDelayedQueryFusionMockBleveSearcher(delay time.Duration) *delayedQueryFusionMockBleveSearcher {
	return &delayedQueryFusionMockBleveSearcher{
		queryFusionMockBleveSearcher: newQueryFusionMockBleveSearcher(),
		delay:                        delay,
	}
}

func (m *delayedQueryFusionMockBleveSearcher) Search(ctx context.Context, query string, limit int) ([]*events.ActivityEvent, error) {
	time.Sleep(m.delay)
	return m.queryFusionMockBleveSearcher.Search(ctx, query, limit)
}

// delayedQueryFusionMockVectorSearcher adds artificial delay to simulate I/O.
type delayedQueryFusionMockVectorSearcher struct {
	*queryFusionMockVectorSearcher
	delay time.Duration
}

func newDelayedQueryFusionMockVectorSearcher(delay time.Duration) *delayedQueryFusionMockVectorSearcher {
	return &delayedQueryFusionMockVectorSearcher{
		queryFusionMockVectorSearcher: newQueryFusionMockVectorSearcher(),
		delay:                         delay,
	}
}

func (m *delayedQueryFusionMockVectorSearcher) Search(ctx context.Context, queryText string, limit int) ([]*events.ActivityEvent, error) {
	time.Sleep(m.delay)
	return m.queryFusionMockVectorSearcher.Search(ctx, queryText, limit)
}

// =============================================================================
// Helper Functions
// =============================================================================

func createQueryFusionTestEvent(id string, eventType events.EventType, content string) *events.ActivityEvent {
	event := events.NewActivityEvent(eventType, "test-session-fusion", content)
	event.ID = id // Override the generated UUID with the specified ID
	event.AgentID = "test-agent-fusion"
	return event
}
