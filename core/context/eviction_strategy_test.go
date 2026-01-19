// Package context provides content storage and context management for multi-agent systems.
package context

import (
	"context"
	"testing"
	"time"
)

// MockEvictionStrategyWithPriority implements EvictionStrategyWithPriority for testing.
type MockEvictionStrategyWithPriority struct {
	name       string
	priority   int
	selectFunc func(ctx context.Context, entries []*ContentEntry, targetTokens int) ([]*ContentEntry, error)
}

func (m *MockEvictionStrategyWithPriority) SelectForEviction(
	ctx context.Context,
	entries []*ContentEntry,
	targetTokens int,
) ([]*ContentEntry, error) {
	if m.selectFunc != nil {
		return m.selectFunc(ctx, entries, targetTokens)
	}
	return nil, nil
}

func (m *MockEvictionStrategyWithPriority) Name() string {
	return m.name
}

func (m *MockEvictionStrategyWithPriority) Priority() int {
	return m.priority
}

// Verify interface compliance at compile time.
var _ EvictionStrategyWithPriority = (*MockEvictionStrategyWithPriority)(nil)
var _ EvictionStrategy = (*MockEvictionStrategyWithPriority)(nil)

func TestEvictionStrategyWithPriorityInterfaceCompliance(t *testing.T) {
	strategy := &MockEvictionStrategyWithPriority{
		name:     "test-strategy",
		priority: 5,
	}

	if strategy.Name() != "test-strategy" {
		t.Errorf("Name() = %q, want %q", strategy.Name(), "test-strategy")
	}

	if strategy.Priority() != 5 {
		t.Errorf("Priority() = %d, want %d", strategy.Priority(), 5)
	}

	entries := createTestEntriesForEviction(3)
	result, err := strategy.SelectForEviction(context.Background(), entries, 100)
	if err != nil {
		t.Errorf("SelectForEviction() unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("SelectForEviction() should return nil with nil selectFunc")
	}
}

func TestEvictionStrategySelectForEviction(t *testing.T) {
	strategy := &MockEvictionStrategyWithPriority{
		name:     "token-based",
		priority: 1,
		selectFunc: func(_ context.Context, entries []*ContentEntry, targetTokens int) ([]*ContentEntry, error) {
			var selected []*ContentEntry
			total := 0
			for _, e := range entries {
				if total >= targetTokens {
					break
				}
				selected = append(selected, e)
				total += e.TokenCount
			}
			return selected, nil
		},
	}

	entries := createTestEntriesForEviction(5)
	result, err := strategy.SelectForEviction(context.Background(), entries, 150)
	if err != nil {
		t.Errorf("SelectForEviction() unexpected error: %v", err)
	}

	if len(result) == 0 {
		t.Error("SelectForEviction() returned empty result")
	}

	totalTokens := CalculateTotalTokens(result)
	if totalTokens < 150 {
		t.Errorf("Selected tokens = %d, want >= 150", totalTokens)
	}
}

func TestEvictionResultExtendedSuccess(t *testing.T) {
	tests := []struct {
		name            string
		tokensFreed     int
		tokensRequested int
		wantSuccess     bool
	}{
		{"exact match", 100, 100, true},
		{"exceeded", 150, 100, true},
		{"insufficient", 50, 100, false},
		{"zero requested", 100, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &EvictionResultExtended{
				TokensFreed:     tt.tokensFreed,
				TokensRequested: tt.tokensRequested,
			}
			if got := result.Success(); got != tt.wantSuccess {
				t.Errorf("Success() = %v, want %v", got, tt.wantSuccess)
			}
		})
	}
}

func TestEvictionResultExtendedEfficiency(t *testing.T) {
	tests := []struct {
		name            string
		tokensFreed     int
		tokensRequested int
		wantEfficiency  float64
	}{
		{"exact match", 100, 100, 1.0},
		{"double", 200, 100, 2.0},
		{"half", 50, 100, 0.5},
		{"zero requested", 100, 0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &EvictionResultExtended{
				TokensFreed:     tt.tokensFreed,
				TokensRequested: tt.tokensRequested,
			}
			if got := result.Efficiency(); got != tt.wantEfficiency {
				t.Errorf("Efficiency() = %v, want %v", got, tt.wantEfficiency)
			}
		})
	}
}

func TestEvictionResultExtendedToBasicResult(t *testing.T) {
	extended := &EvictionResultExtended{
		StrategyName:    "test",
		EntriesEvicted:  5,
		TokensFreed:     500,
		TokensRequested: 400,
		Duration:        10 * time.Millisecond,
	}

	basic := extended.ToBasicResult()

	if basic.EntriesEvicted != 5 {
		t.Errorf("EntriesEvicted = %d, want 5", basic.EntriesEvicted)
	}
	if basic.TokensFreed != 500 {
		t.Errorf("TokensFreed = %d, want 500", basic.TokensFreed)
	}
}

func TestEvictionMetricsRecord(t *testing.T) {
	metrics := NewEvictionMetrics()

	result := &EvictionResultExtended{
		StrategyName:    "test-strategy",
		EntriesEvicted:  5,
		TokensFreed:     500,
		TokensRequested: 400,
		Duration:        10 * time.Millisecond,
		Timestamp:       time.Now(),
	}

	metrics.Record(result)

	if metrics.TotalEvictions != 1 {
		t.Errorf("TotalEvictions = %d, want 1", metrics.TotalEvictions)
	}
	if metrics.TotalTokensFreed != 500 {
		t.Errorf("TotalTokensFreed = %d, want 500", metrics.TotalTokensFreed)
	}
	if metrics.TotalEntriesEvicted != 5 {
		t.Errorf("TotalEntriesEvicted = %d, want 5", metrics.TotalEntriesEvicted)
	}
	if metrics.SuccessfulEvictions != 1 {
		t.Errorf("SuccessfulEvictions = %d, want 1", metrics.SuccessfulEvictions)
	}
}

func TestEvictionMetricsRecordFailure(t *testing.T) {
	metrics := NewEvictionMetrics()

	result := &EvictionResultExtended{
		StrategyName:    "failing-strategy",
		EntriesEvicted:  2,
		TokensFreed:     100,
		TokensRequested: 500,
		Duration:        5 * time.Millisecond,
		Timestamp:       time.Now(),
	}

	metrics.Record(result)

	if metrics.FailedEvictions != 1 {
		t.Errorf("FailedEvictions = %d, want 1", metrics.FailedEvictions)
	}
	if metrics.SuccessfulEvictions != 0 {
		t.Errorf("SuccessfulEvictions = %d, want 0", metrics.SuccessfulEvictions)
	}
}

func TestEvictionMetricsSuccessRate(t *testing.T) {
	metrics := NewEvictionMetrics()

	// No evictions
	if rate := metrics.SuccessRate(); rate != 0 {
		t.Errorf("SuccessRate() with no evictions = %v, want 0", rate)
	}

	// Add successes and failures
	metrics.Record(&EvictionResultExtended{
		StrategyName:    "test",
		TokensFreed:     100,
		TokensRequested: 50,
		Timestamp:       time.Now(),
	})
	metrics.Record(&EvictionResultExtended{
		StrategyName:    "test",
		TokensFreed:     100,
		TokensRequested: 50,
		Timestamp:       time.Now(),
	})
	metrics.Record(&EvictionResultExtended{
		StrategyName:    "test",
		TokensFreed:     10,
		TokensRequested: 50,
		Timestamp:       time.Now(),
	})

	expectedRate := 2.0 / 3.0
	if rate := metrics.SuccessRate(); rate < expectedRate-0.01 || rate > expectedRate+0.01 {
		t.Errorf("SuccessRate() = %v, want ~%v", rate, expectedRate)
	}
}

func TestEvictionMetricsAverageDuration(t *testing.T) {
	metrics := NewEvictionMetrics()

	// No evictions
	if avg := metrics.AverageDuration(); avg != 0 {
		t.Errorf("AverageDuration() with no evictions = %v, want 0", avg)
	}

	metrics.Record(&EvictionResultExtended{
		StrategyName:    "test",
		Duration:        10 * time.Millisecond,
		TokensFreed:     100,
		TokensRequested: 50,
		Timestamp:       time.Now(),
	})
	metrics.Record(&EvictionResultExtended{
		StrategyName:    "test",
		Duration:        20 * time.Millisecond,
		TokensFreed:     100,
		TokensRequested: 50,
		Timestamp:       time.Now(),
	})

	expectedAvg := 15 * time.Millisecond
	if avg := metrics.AverageDuration(); avg != expectedAvg {
		t.Errorf("AverageDuration() = %v, want %v", avg, expectedAvg)
	}
}

func TestEvictionMetricsGetStrategyStats(t *testing.T) {
	metrics := NewEvictionMetrics()

	// Non-existent strategy
	if stats := metrics.GetStrategyStats("nonexistent"); stats != nil {
		t.Error("GetStrategyStats() for nonexistent should return nil")
	}

	metrics.Record(&EvictionResultExtended{
		StrategyName:    "strategy-a",
		EntriesEvicted:  3,
		TokensFreed:     300,
		TokensRequested: 200,
		Duration:        5 * time.Millisecond,
		Timestamp:       time.Now(),
	})

	stats := metrics.GetStrategyStats("strategy-a")
	if stats == nil {
		t.Fatal("GetStrategyStats() returned nil for existing strategy")
	}
	if stats.Invocations != 1 {
		t.Errorf("Invocations = %d, want 1", stats.Invocations)
	}
	if stats.TokensFreed != 300 {
		t.Errorf("TokensFreed = %d, want 300", stats.TokensFreed)
	}
}

func TestSortEntriesByAge(t *testing.T) {
	now := time.Now()
	entries := []*ContentEntry{
		{ID: "c", Timestamp: now.Add(-1 * time.Hour)},
		{ID: "a", Timestamp: now.Add(-3 * time.Hour)},
		{ID: "b", Timestamp: now.Add(-2 * time.Hour)},
	}

	sorted := SortEntriesByAge(entries)

	if len(sorted) != 3 {
		t.Fatalf("len(sorted) = %d, want 3", len(sorted))
	}
	if sorted[0].ID != "a" {
		t.Errorf("sorted[0].ID = %q, want %q", sorted[0].ID, "a")
	}
	if sorted[1].ID != "b" {
		t.Errorf("sorted[1].ID = %q, want %q", sorted[1].ID, "b")
	}
	if sorted[2].ID != "c" {
		t.Errorf("sorted[2].ID = %q, want %q", sorted[2].ID, "c")
	}
}

func TestSortEntriesByAgeEmpty(t *testing.T) {
	result := SortEntriesByAge(nil)
	if result != nil {
		t.Errorf("SortEntriesByAge(nil) = %v, want nil", result)
	}

	result = SortEntriesByAge([]*ContentEntry{})
	if len(result) != 0 {
		t.Errorf("SortEntriesByAge([]) length = %d, want 0", len(result))
	}
}

func TestSortEntriesByTokenCount(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "small", TokenCount: 10},
		{ID: "large", TokenCount: 100},
		{ID: "medium", TokenCount: 50},
	}

	sorted := SortEntriesByTokenCount(entries)

	if len(sorted) != 3 {
		t.Fatalf("len(sorted) = %d, want 3", len(sorted))
	}
	if sorted[0].ID != "large" {
		t.Errorf("sorted[0].ID = %q, want %q", sorted[0].ID, "large")
	}
	if sorted[1].ID != "medium" {
		t.Errorf("sorted[1].ID = %q, want %q", sorted[1].ID, "medium")
	}
	if sorted[2].ID != "small" {
		t.Errorf("sorted[2].ID = %q, want %q", sorted[2].ID, "small")
	}
}

func TestSortEntriesByTokenCountEmpty(t *testing.T) {
	result := SortEntriesByTokenCount(nil)
	if result != nil {
		t.Errorf("SortEntriesByTokenCount(nil) = %v, want nil", result)
	}
}

func TestFilterByContentTypes(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", ContentType: ContentTypeUserPrompt},
		{ID: "2", ContentType: ContentTypeToolCall},
		{ID: "3", ContentType: ContentTypeUserPrompt},
		{ID: "4", ContentType: ContentTypeCodeFile},
	}

	filtered := FilterByContentTypes(entries, []ContentType{ContentTypeUserPrompt})
	if len(filtered) != 2 {
		t.Errorf("len(filtered) = %d, want 2", len(filtered))
	}

	filtered = FilterByContentTypes(entries, []ContentType{ContentTypeUserPrompt, ContentTypeToolCall})
	if len(filtered) != 3 {
		t.Errorf("len(filtered) = %d, want 3", len(filtered))
	}
}

func TestFilterByContentTypesEdgeCases(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", ContentType: ContentTypeUserPrompt},
	}

	// Empty entries
	if result := FilterByContentTypes(nil, []ContentType{ContentTypeUserPrompt}); result != nil {
		t.Error("FilterByContentTypes(nil, types) should return nil")
	}

	// Empty types
	if result := FilterByContentTypes(entries, nil); result != nil {
		t.Error("FilterByContentTypes(entries, nil) should return nil")
	}

	// Empty both
	if result := FilterByContentTypes(nil, nil); result != nil {
		t.Error("FilterByContentTypes(nil, nil) should return nil")
	}
}

func TestExcludeByContentTypes(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", ContentType: ContentTypeUserPrompt},
		{ID: "2", ContentType: ContentTypeToolCall},
		{ID: "3", ContentType: ContentTypeUserPrompt},
		{ID: "4", ContentType: ContentTypeCodeFile},
	}

	excluded := ExcludeByContentTypes(entries, []ContentType{ContentTypeUserPrompt})
	if len(excluded) != 2 {
		t.Errorf("len(excluded) = %d, want 2", len(excluded))
	}
	for _, e := range excluded {
		if e.ContentType == ContentTypeUserPrompt {
			t.Error("Excluded result contains ContentTypeUserPrompt")
		}
	}
}

func TestExcludeByContentTypesEdgeCases(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", ContentType: ContentTypeUserPrompt},
	}

	// Empty entries
	if result := ExcludeByContentTypes(nil, []ContentType{ContentTypeUserPrompt}); result != nil {
		t.Error("ExcludeByContentTypes(nil, types) should return nil")
	}

	// Empty types - should return copy
	result := ExcludeByContentTypes(entries, nil)
	if len(result) != 1 {
		t.Errorf("ExcludeByContentTypes(entries, nil) length = %d, want 1", len(result))
	}

	// Empty types slice
	result = ExcludeByContentTypes(entries, []ContentType{})
	if len(result) != 1 {
		t.Errorf("ExcludeByContentTypes(entries, []) length = %d, want 1", len(result))
	}
}

func TestFilterByTurnRange(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", TurnNumber: 1},
		{ID: "2", TurnNumber: 5},
		{ID: "3", TurnNumber: 10},
		{ID: "4", TurnNumber: 15},
	}

	filtered := FilterByTurnRange(entries, 5, 10)
	if len(filtered) != 2 {
		t.Errorf("len(filtered) = %d, want 2", len(filtered))
	}

	// Check IDs
	ids := ExtractEntryIDs(filtered)
	if ids[0] != "2" || ids[1] != "3" {
		t.Errorf("IDs = %v, want [2, 3]", ids)
	}
}

func TestFilterByTurnRangeEdgeCases(t *testing.T) {
	if result := FilterByTurnRange(nil, 0, 10); result != nil {
		t.Error("FilterByTurnRange(nil, ...) should return nil")
	}

	entries := []*ContentEntry{
		{ID: "1", TurnNumber: 5},
	}

	// No matches
	if result := FilterByTurnRange(entries, 10, 20); len(result) != 0 {
		t.Errorf("FilterByTurnRange with no matches = %d, want 0", len(result))
	}
}

func TestExcludeRecentTurns(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", TurnNumber: 1},
		{ID: "2", TurnNumber: 5},
		{ID: "3", TurnNumber: 8},
		{ID: "4", TurnNumber: 10},
	}

	result := ExcludeRecentTurns(entries, 10, 3)
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2", len(result))
	}

	// Should have turns 1 and 5 (< 10-3=7)
	ids := ExtractEntryIDs(result)
	if ids[0] != "1" || ids[1] != "2" {
		t.Errorf("IDs = %v, want [1, 2]", ids)
	}
}

func TestExcludeRecentTurnsEdgeCases(t *testing.T) {
	if result := ExcludeRecentTurns(nil, 10, 3); result != nil {
		t.Error("ExcludeRecentTurns(nil, ...) should return nil")
	}
}

func TestCalculateTotalTokens(t *testing.T) {
	entries := []*ContentEntry{
		{TokenCount: 100},
		{TokenCount: 200},
		{TokenCount: 50},
	}

	total := CalculateTotalTokens(entries)
	if total != 350 {
		t.Errorf("CalculateTotalTokens() = %d, want 350", total)
	}
}

func TestCalculateTotalTokensEmpty(t *testing.T) {
	if total := CalculateTotalTokens(nil); total != 0 {
		t.Errorf("CalculateTotalTokens(nil) = %d, want 0", total)
	}
	if total := CalculateTotalTokens([]*ContentEntry{}); total != 0 {
		t.Errorf("CalculateTotalTokens([]) = %d, want 0", total)
	}
}

func TestSelectUntilTokenLimit(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", TokenCount: 50},
		{ID: "2", TokenCount: 50},
		{ID: "3", TokenCount: 50},
		{ID: "4", TokenCount: 50},
	}

	result := SelectUntilTokenLimit(entries, 120)
	if len(result) != 3 {
		t.Errorf("len(result) = %d, want 3", len(result))
	}

	total := CalculateTotalTokens(result)
	if total != 150 {
		t.Errorf("total tokens = %d, want 150", total)
	}
}

func TestSelectUntilTokenLimitEdgeCases(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", TokenCount: 100},
	}

	// Nil entries
	if result := SelectUntilTokenLimit(nil, 100); result != nil {
		t.Error("SelectUntilTokenLimit(nil, ...) should return nil")
	}

	// Zero limit
	if result := SelectUntilTokenLimit(entries, 0); result != nil {
		t.Error("SelectUntilTokenLimit(entries, 0) should return nil")
	}

	// Negative limit
	if result := SelectUntilTokenLimit(entries, -10); result != nil {
		t.Error("SelectUntilTokenLimit(entries, -10) should return nil")
	}
}

func TestExtractEntryIDs(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "entry-1"},
		{ID: "entry-2"},
		{ID: "entry-3"},
	}

	ids := ExtractEntryIDs(entries)
	if len(ids) != 3 {
		t.Fatalf("len(ids) = %d, want 3", len(ids))
	}
	if ids[0] != "entry-1" || ids[1] != "entry-2" || ids[2] != "entry-3" {
		t.Errorf("ids = %v, want [entry-1, entry-2, entry-3]", ids)
	}
}

func TestExtractEntryIDsEmpty(t *testing.T) {
	if result := ExtractEntryIDs(nil); result != nil {
		t.Error("ExtractEntryIDs(nil) should return nil")
	}
	if result := ExtractEntryIDs([]*ContentEntry{}); result != nil {
		t.Error("ExtractEntryIDs([]) should return nil")
	}
}

func TestEvictionMetricsConcurrency(t *testing.T) {
	metrics := NewEvictionMetrics()
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			metrics.Record(&EvictionResultExtended{
				StrategyName:    "concurrent-test",
				TokensFreed:     100,
				TokensRequested: 50,
				Duration:        time.Millisecond,
				Timestamp:       time.Now(),
			})
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = metrics.SuccessRate()
			_ = metrics.AverageDuration()
			_ = metrics.GetStrategyStats("concurrent-test")
		}
		done <- true
	}()

	<-done
	<-done

	if metrics.TotalEvictions != 100 {
		t.Errorf("TotalEvictions = %d, want 100", metrics.TotalEvictions)
	}
}

func TestEvictionStrategyPriorityOrdering(t *testing.T) {
	strategies := []EvictionStrategyWithPriority{
		&MockEvictionStrategyWithPriority{name: "low-priority", priority: 10},
		&MockEvictionStrategyWithPriority{name: "high-priority", priority: 1},
		&MockEvictionStrategyWithPriority{name: "medium-priority", priority: 5},
	}

	// Sort by priority (lower is higher priority)
	for i := 0; i < len(strategies)-1; i++ {
		for j := i + 1; j < len(strategies); j++ {
			if strategies[i].Priority() > strategies[j].Priority() {
				strategies[i], strategies[j] = strategies[j], strategies[i]
			}
		}
	}

	if strategies[0].Name() != "high-priority" {
		t.Errorf("strategies[0] = %q, want high-priority", strategies[0].Name())
	}
	if strategies[1].Name() != "medium-priority" {
		t.Errorf("strategies[1] = %q, want medium-priority", strategies[1].Name())
	}
	if strategies[2].Name() != "low-priority" {
		t.Errorf("strategies[2] = %q, want low-priority", strategies[2].Name())
	}
}

func TestStrategyMetricsSuccessFailureCounts(t *testing.T) {
	metrics := NewEvictionMetrics()

	// Record multiple results for the same strategy
	for i := 0; i < 5; i++ {
		metrics.Record(&EvictionResultExtended{
			StrategyName:    "multi-test",
			TokensFreed:     100,
			TokensRequested: 50, // Success
			Timestamp:       time.Now(),
		})
	}
	for i := 0; i < 3; i++ {
		metrics.Record(&EvictionResultExtended{
			StrategyName:    "multi-test",
			TokensFreed:     20,
			TokensRequested: 100, // Failure
			Timestamp:       time.Now(),
		})
	}

	stats := metrics.GetStrategyStats("multi-test")
	if stats == nil {
		t.Fatal("GetStrategyStats returned nil")
	}
	if stats.Successes != 5 {
		t.Errorf("Successes = %d, want 5", stats.Successes)
	}
	if stats.Failures != 3 {
		t.Errorf("Failures = %d, want 3", stats.Failures)
	}
	if stats.Invocations != 8 {
		t.Errorf("Invocations = %d, want 8", stats.Invocations)
	}
}

// Helper function to create test entries.
func createTestEntriesForEviction(count int) []*ContentEntry {
	entries := make([]*ContentEntry, count)
	now := time.Now()

	for i := 0; i < count; i++ {
		entries[i] = &ContentEntry{
			ID:          "entry-" + string(rune('a'+i)),
			TokenCount:  (i + 1) * 50,
			TurnNumber:  i + 1,
			Timestamp:   now.Add(time.Duration(-count+i) * time.Hour),
			ContentType: ContentTypeUserPrompt,
		}
	}

	return entries
}
