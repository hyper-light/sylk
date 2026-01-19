package context

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Helper to create test entries with specified timestamps and turn numbers.
func createRecencyTestEntry(id string, tokens int, turn int, timestamp time.Time) *ContentEntry {
	return &ContentEntry{
		ID:         id,
		TokenCount: tokens,
		TurnNumber: turn,
		Timestamp:  timestamp,
	}
}

// Helper to create entries at sequential times starting from a base time.
func createSequentialRecencyEntries(count int, tokensPerEntry int, baseTime time.Time) []*ContentEntry {
	entries := make([]*ContentEntry, count)
	for i := 0; i < count; i++ {
		entries[i] = createRecencyTestEntry(
			string(rune('a'+i)),
			tokensPerEntry,
			i+1,
			baseTime.Add(time.Duration(i)*time.Minute),
		)
	}
	return entries
}

func TestNewRecencyBasedEviction(t *testing.T) {
	tests := []struct {
		name                string
		preserveRecentTurns int
		expected            int
	}{
		{"positive value", 5, 5},
		{"zero value", 0, 0},
		{"negative value normalizes to zero", -3, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewRecencyBasedEviction(tt.preserveRecentTurns)
			if e.GetPreserveRecentTurns() != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, e.GetPreserveRecentTurns())
			}
		})
	}
}

func TestRecencyBasedEviction_Name(t *testing.T) {
	e := NewRecencyBasedEviction(5)
	if e.Name() != RecencyEvictionName {
		t.Errorf("expected %s, got %s", RecencyEvictionName, e.Name())
	}
}

func TestRecencyBasedEviction_Priority(t *testing.T) {
	e := NewRecencyBasedEviction(5)
	if e.Priority() != DefaultRecencyPriority {
		t.Errorf("expected %d, got %d", DefaultRecencyPriority, e.Priority())
	}

	e.SetPriority(50)
	if e.Priority() != 50 {
		t.Errorf("expected 50, got %d", e.Priority())
	}
}

func TestRecencyBasedEviction_SetPreserveRecentTurns(t *testing.T) {
	e := NewRecencyBasedEviction(5)

	e.SetPreserveRecentTurns(10)
	if e.GetPreserveRecentTurns() != 10 {
		t.Errorf("expected 10, got %d", e.GetPreserveRecentTurns())
	}

	e.SetPreserveRecentTurns(-5)
	if e.GetPreserveRecentTurns() != 0 {
		t.Errorf("expected 0 for negative value, got %d", e.GetPreserveRecentTurns())
	}
}

func TestRecencyBasedEviction_SetCurrentTurn(t *testing.T) {
	e := NewRecencyBasedEviction(5)

	e.SetCurrentTurn(10)
	if e.GetCurrentTurn() != 10 {
		t.Errorf("expected 10, got %d", e.GetCurrentTurn())
	}
}

func TestRecencyBasedEviction_SelectForEviction_BasicByTimestamp(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(2) // Preserve last 2 turns
	e.SetCurrentTurn(5)

	// Create 5 entries, turns 1-5, 100 tokens each
	entries := createSequentialRecencyEntries(5, 100, baseTime)

	// Current turn is 5, preserve turns 4 and 5 (threshold = 3)
	// So turns 1, 2 are evictable
	selected, err := e.SelectForEviction(context.Background(), entries, 150)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(selected))
	}

	// Should be sorted by timestamp (oldest first)
	if selected[0].ID != "a" {
		t.Errorf("expected first entry ID 'a', got '%s'", selected[0].ID)
	}
	if selected[1].ID != "b" {
		t.Errorf("expected second entry ID 'b', got '%s'", selected[1].ID)
	}
}

func TestRecencyBasedEviction_SelectForEviction_ByTurnNumber(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(2)
	e.SetCurrentTurn(5)

	// Create entries with non-sequential timestamps but sequential turns
	entries := []*ContentEntry{
		createRecencyTestEntry("c", 100, 1, baseTime.Add(2*time.Minute)), // turn 1, newest timestamp
		createRecencyTestEntry("a", 100, 2, baseTime),                    // turn 2, oldest timestamp
		createRecencyTestEntry("b", 100, 3, baseTime.Add(1*time.Minute)), // turn 3
		createRecencyTestEntry("d", 100, 4, baseTime.Add(3*time.Minute)), // turn 4
		createRecencyTestEntry("e", 100, 5, baseTime.Add(4*time.Minute)), // turn 5
	}

	// Use SelectForEvictionByTurn to sort by turn number instead
	selected := e.SelectForEvictionByTurn(entries, 150)

	if len(selected) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(selected))
	}

	// Should be sorted by turn number (lowest first)
	if selected[0].TurnNumber != 1 {
		t.Errorf("expected first entry turn 1, got %d", selected[0].TurnNumber)
	}
	if selected[1].TurnNumber != 2 {
		t.Errorf("expected second entry turn 2, got %d", selected[1].TurnNumber)
	}
}

func TestRecencyBasedEviction_SelectForEviction_PreservesRecentTurns(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(3) // Preserve last 3 turns
	e.SetCurrentTurn(5)

	entries := createSequentialRecencyEntries(5, 100, baseTime)

	// Current turn is 5, preserve turns 3, 4, 5 (threshold = 2)
	// So only turn 1 is evictable
	selected, err := e.SelectForEviction(context.Background(), entries, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(selected))
	}

	if selected[0].TurnNumber != 1 {
		t.Errorf("expected turn 1, got %d", selected[0].TurnNumber)
	}
}

func TestRecencyBasedEviction_SelectForEviction_EmptyEntries(t *testing.T) {
	e := NewRecencyBasedEviction(2)
	e.SetCurrentTurn(5)
	ctx := context.Background()

	selected, err := e.SelectForEviction(ctx, nil, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Errorf("expected nil for nil entries, got %v", selected)
	}

	selected, err = e.SelectForEviction(ctx, []*ContentEntry{}, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Errorf("expected nil for empty entries, got %v", selected)
	}
}

func TestRecencyBasedEviction_SelectForEviction_ZeroTargetTokens(t *testing.T) {
	e := NewRecencyBasedEviction(2)
	e.SetCurrentTurn(5)
	entries := createSequentialRecencyEntries(5, 100, time.Now())
	ctx := context.Background()

	selected, err := e.SelectForEviction(ctx, entries, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Errorf("expected nil for zero target tokens, got %v", selected)
	}

	selected, err = e.SelectForEviction(ctx, entries, -100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Errorf("expected nil for negative target tokens, got %v", selected)
	}
}

func TestRecencyBasedEviction_SelectForEviction_AllEntriesTooRecent(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(10) // Preserve last 10 turns
	e.SetCurrentTurn(5)

	// Only 5 entries, all within preserve window
	entries := createSequentialRecencyEntries(5, 100, baseTime)

	selected, err := e.SelectForEviction(context.Background(), entries, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Errorf("expected nil when all entries too recent, got %v", selected)
	}
}

func TestRecencyBasedEviction_SelectForEviction_ExactTokenMatch(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(1) // Preserve only last turn
	e.SetCurrentTurn(5)

	entries := createSequentialRecencyEntries(5, 100, baseTime)

	// Request exactly 300 tokens, should get 3 entries
	selected, err := e.SelectForEviction(context.Background(), entries, 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 3 {
		t.Fatalf("expected 3 entries for exact 300 tokens, got %d", len(selected))
	}

	totalTokens := 0
	for _, entry := range selected {
		totalTokens += entry.TokenCount
	}
	if totalTokens != 300 {
		t.Errorf("expected exactly 300 tokens, got %d", totalTokens)
	}
}

func TestRecencyBasedEviction_SelectForEviction_TokenLimitStopsSelection(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(0) // Preserve nothing
	e.SetCurrentTurn(10)

	entries := createSequentialRecencyEntries(10, 100, baseTime)

	// Request 250 tokens, should get 3 entries (first exceeds at 300)
	selected, err := e.SelectForEviction(context.Background(), entries, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(selected))
	}
}

func TestRecencyBasedEviction_SelectForEviction_NilEntriesInSlice(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(0)
	e.SetCurrentTurn(5)

	entries := []*ContentEntry{
		createRecencyTestEntry("a", 100, 1, baseTime),
		nil, // nil entry should be skipped
		createRecencyTestEntry("c", 100, 2, baseTime.Add(time.Minute)),
	}

	selected, err := e.SelectForEviction(context.Background(), entries, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 2 {
		t.Fatalf("expected 2 entries (skipping nil), got %d", len(selected))
	}
}

func TestRecencyBasedEviction_SelectForEvictionByTurn_EmptyAndEdgeCases(t *testing.T) {
	e := NewRecencyBasedEviction(2)
	e.SetCurrentTurn(5)

	// Test nil entries
	selected := e.SelectForEvictionByTurn(nil, 100)
	if selected != nil {
		t.Errorf("expected nil for nil entries")
	}

	// Test empty entries
	selected = e.SelectForEvictionByTurn([]*ContentEntry{}, 100)
	if selected != nil {
		t.Errorf("expected nil for empty entries")
	}

	// Test zero tokens
	entries := createSequentialRecencyEntries(5, 100, time.Now())
	selected = e.SelectForEvictionByTurn(entries, 0)
	if selected != nil {
		t.Errorf("expected nil for zero target tokens")
	}
}

func TestRecencyBasedEviction_EstimateEvictableTokens(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(2) // Preserve last 2 turns
	e.SetCurrentTurn(5)

	entries := createSequentialRecencyEntries(5, 100, baseTime)

	// Current turn is 5, preserve turns 4 and 5 (threshold = 3)
	// Evictable: turns 1, 2 = 200 tokens
	estimate := e.EstimateEvictableTokens(entries)

	if estimate != 200 {
		t.Errorf("expected 200 tokens, got %d", estimate)
	}
}

func TestRecencyBasedEviction_EstimateEvictableTokens_EmptyEntries(t *testing.T) {
	e := NewRecencyBasedEviction(2)
	e.SetCurrentTurn(5)

	if e.EstimateEvictableTokens(nil) != 0 {
		t.Error("expected 0 for nil entries")
	}

	if e.EstimateEvictableTokens([]*ContentEntry{}) != 0 {
		t.Error("expected 0 for empty entries")
	}
}

func TestRecencyBasedEviction_EstimateEvictableTokens_AllPreserved(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(10)
	e.SetCurrentTurn(5)

	entries := createSequentialRecencyEntries(5, 100, baseTime)

	estimate := e.EstimateEvictableTokens(entries)
	if estimate != 0 {
		t.Errorf("expected 0 when all preserved, got %d", estimate)
	}
}

func TestCreateRecencyEvictionSummary(t *testing.T) {
	baseTime := time.Now()
	entries := []*ContentEntry{
		createRecencyTestEntry("a", 100, 1, baseTime),
		createRecencyTestEntry("b", 150, 2, baseTime.Add(time.Minute)),
		createRecencyTestEntry("c", 200, 3, baseTime.Add(2*time.Minute)),
	}

	result := CreateRecencyEvictionSummary(entries)

	if result.EntriesEvicted != 3 {
		t.Errorf("expected 3 entries evicted, got %d", result.EntriesEvicted)
	}
	if result.TokensFreed != 450 {
		t.Errorf("expected 450 tokens freed, got %d", result.TokensFreed)
	}
	if !result.OldestEvicted.Equal(baseTime) {
		t.Errorf("expected oldest = baseTime, got %v", result.OldestEvicted)
	}
	if !result.NewestEvicted.Equal(baseTime.Add(2 * time.Minute)) {
		t.Errorf("expected newest = baseTime+2min, got %v", result.NewestEvicted)
	}
}

func TestCreateRecencyEvictionSummary_EmptyEntries(t *testing.T) {
	result := CreateRecencyEvictionSummary(nil)
	if result.EntriesEvicted != 0 {
		t.Errorf("expected 0 entries evicted for nil, got %d", result.EntriesEvicted)
	}

	result = CreateRecencyEvictionSummary([]*ContentEntry{})
	if result.EntriesEvicted != 0 {
		t.Errorf("expected 0 entries evicted for empty, got %d", result.EntriesEvicted)
	}
}

func TestCreateRecencyEvictionSummary_SingleEntry(t *testing.T) {
	baseTime := time.Now()
	entries := []*ContentEntry{createRecencyTestEntry("a", 100, 1, baseTime)}

	result := CreateRecencyEvictionSummary(entries)

	if result.EntriesEvicted != 1 {
		t.Errorf("expected 1 entry, got %d", result.EntriesEvicted)
	}
	if !result.OldestEvicted.Equal(result.NewestEvicted) {
		t.Error("oldest and newest should be equal for single entry")
	}
}

func TestRecencyBasedEviction_ConcurrencySafety(t *testing.T) {
	e := NewRecencyBasedEviction(5)
	baseTime := time.Now()
	entries := createSequentialRecencyEntries(20, 100, baseTime)

	var wg sync.WaitGroup
	numGoroutines := 100

	// Test concurrent reads and writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(4)

		// Reader goroutine
		go func() {
			defer wg.Done()
			_ = e.GetPreserveRecentTurns()
		}()

		// Writer goroutine - preserve turns
		go func(n int) {
			defer wg.Done()
			e.SetPreserveRecentTurns(n % 10)
		}(i)

		// Writer goroutine - current turn
		go func(n int) {
			defer wg.Done()
			e.SetCurrentTurn(n + 10)
		}(i)

		// SelectForEviction goroutine
		go func() {
			defer wg.Done()
			e.SetCurrentTurn(20)
			_, _ = e.SelectForEviction(context.Background(), entries, 500)
		}()
	}

	wg.Wait()
	// Test passes if no race conditions or panics occur
}

func TestRecencyBasedEviction_SelectForEviction_ZeroPreserve(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(0) // Preserve nothing
	e.SetCurrentTurn(5)

	entries := createSequentialRecencyEntries(5, 100, baseTime)

	// All 5 entries should be evictable (threshold = currentTurn - 0 = 5)
	// But entries have turns 1-5, and threshold is 5
	// So entries with turn < 5 are evictable = turns 1,2,3,4
	selected, err := e.SelectForEviction(context.Background(), entries, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 4 {
		t.Fatalf("expected 4 entries with zero preserve, got %d", len(selected))
	}
}

func TestRecencyBasedEviction_SelectForEviction_LowCurrentTurn(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(5) // Preserve 5 turns
	e.SetCurrentTurn(3)

	entries := createSequentialRecencyEntries(3, 100, baseTime)

	// Current turn is 3, preserve 5 turns
	// threshold = 3 - 5 = -2, normalized to 0
	// So entries with turn < 0 = none evictable
	selected, err := e.SelectForEviction(context.Background(), entries, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if selected != nil {
		t.Errorf("expected nil when current turn < preserve turns, got %d entries", len(selected))
	}
}

func TestRecencyBasedEviction_SelectForEviction_VaryingTokenCounts(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(1)
	e.SetCurrentTurn(5)

	entries := []*ContentEntry{
		createRecencyTestEntry("a", 50, 1, baseTime),
		createRecencyTestEntry("b", 150, 2, baseTime.Add(time.Minute)),
		createRecencyTestEntry("c", 75, 3, baseTime.Add(2*time.Minute)),
		createRecencyTestEntry("d", 200, 4, baseTime.Add(3*time.Minute)),
		createRecencyTestEntry("e", 100, 5, baseTime.Add(4*time.Minute)),
	}

	// Current turn 5, preserve 1, threshold = 4
	// Evictable: turns 1, 2, 3 (50 + 150 + 75 = 275 tokens)
	selected, err := e.SelectForEviction(context.Background(), entries, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(selected))
	}

	totalTokens := 0
	for _, entry := range selected {
		totalTokens += entry.TokenCount
	}
	// First 2 entries = 50 + 150 = 200
	if totalTokens != 200 {
		t.Errorf("expected 200 tokens, got %d", totalTokens)
	}
}

func TestEarlierTime(t *testing.T) {
	t1 := time.Now()
	t2 := t1.Add(time.Hour)

	if !earlierTime(t1, t2).Equal(t1) {
		t.Error("earlierTime should return earlier time")
	}
	if !earlierTime(t2, t1).Equal(t1) {
		t.Error("earlierTime should return earlier time (reversed)")
	}
	if !earlierTime(t1, t1).Equal(t1) {
		t.Error("earlierTime should return same time when equal")
	}
}

func TestLaterTime(t *testing.T) {
	t1 := time.Now()
	t2 := t1.Add(time.Hour)

	if !laterTime(t1, t2).Equal(t2) {
		t.Error("laterTime should return later time")
	}
	if !laterTime(t2, t1).Equal(t2) {
		t.Error("laterTime should return later time (reversed)")
	}
	if !laterTime(t1, t1).Equal(t1) {
		t.Error("laterTime should return same time when equal")
	}
}

func TestRecencyBasedEviction_ImplementsInterface(t *testing.T) {
	// Compile-time check that RecencyBasedEviction implements EvictionStrategy
	var _ EvictionStrategy = (*RecencyBasedEviction)(nil)
}

func TestRecencyBasedEviction_SelectForEviction_SameTimestampDifferentTurns(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(1)
	e.SetCurrentTurn(5)

	// Entries with same timestamp but different turns
	entries := []*ContentEntry{
		createRecencyTestEntry("a", 100, 1, baseTime),
		createRecencyTestEntry("b", 100, 2, baseTime),
		createRecencyTestEntry("c", 100, 3, baseTime),
		createRecencyTestEntry("d", 100, 4, baseTime),
		createRecencyTestEntry("e", 100, 5, baseTime),
	}

	// threshold = 4, evictable: turns 1, 2, 3
	selected, err := e.SelectForEviction(context.Background(), entries, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(selected))
	}
}

func TestRecencyBasedEviction_SelectForEvictionByTurn_OutOfOrderTimestamps(t *testing.T) {
	baseTime := time.Now()
	e := NewRecencyBasedEviction(1)
	e.SetCurrentTurn(5)

	// Entries with timestamps in different order than turns
	entries := []*ContentEntry{
		createRecencyTestEntry("a", 100, 3, baseTime),                    // turn 3, earliest
		createRecencyTestEntry("b", 100, 1, baseTime.Add(2*time.Minute)), // turn 1, latest
		createRecencyTestEntry("c", 100, 2, baseTime.Add(time.Minute)),   // turn 2, middle
		createRecencyTestEntry("d", 100, 4, baseTime.Add(3*time.Minute)), // turn 4
		createRecencyTestEntry("e", 100, 5, baseTime.Add(4*time.Minute)), // turn 5
	}

	// By turn number: should get turns 1, 2, 3 in that order
	selected := e.SelectForEvictionByTurn(entries, 250)

	if len(selected) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(selected))
	}

	// Verify order is by turn number
	if selected[0].TurnNumber != 1 {
		t.Errorf("expected first entry turn 1, got %d", selected[0].TurnNumber)
	}
	if selected[1].TurnNumber != 2 {
		t.Errorf("expected second entry turn 2, got %d", selected[1].TurnNumber)
	}
	if selected[2].TurnNumber != 3 {
		t.Errorf("expected third entry turn 3, got %d", selected[2].TurnNumber)
	}
}

func TestRecencyBasedEviction_EstimateEvictableTokens_WithNilEntries(t *testing.T) {
	e := NewRecencyBasedEviction(1)
	e.SetCurrentTurn(5)

	entries := []*ContentEntry{
		createRecencyTestEntry("a", 100, 1, time.Now()),
		nil,
		createRecencyTestEntry("c", 100, 2, time.Now()),
		nil,
		createRecencyTestEntry("e", 100, 3, time.Now()),
	}

	// threshold = 4, turns 1, 2, 3 are evictable = 300 tokens (skipping nils)
	estimate := e.EstimateEvictableTokens(entries)

	if estimate != 300 {
		t.Errorf("expected 300 tokens, got %d", estimate)
	}
}
