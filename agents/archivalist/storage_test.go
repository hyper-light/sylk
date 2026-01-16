package archivalist

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Store Unit Tests
// =============================================================================

func TestStore_InsertEntry(t *testing.T) {
	store := newTestStore(t)

	entry := makeEntry(CategoryInsight, "Test insight content", SourceModelClaudeOpus45)
	id, err := store.InsertEntry(entry)

	require.NoError(t, err, "InsertEntry")
	assert.NotEmpty(t, id, "ID should not be empty")
	assert.Equal(t, id, entry.ID, "Entry ID should match returned ID")
	assert.False(t, entry.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.False(t, entry.UpdatedAt.IsZero(), "UpdatedAt should be set")
	assert.Equal(t, store.GetCurrentSession().ID, entry.SessionID, "SessionID should match current session")
	assert.Greater(t, entry.TokensEstimate, 0, "TokensEstimate should be positive")
}

func TestStore_InsertEntry_WithExistingID(t *testing.T) {
	store := newTestStore(t)

	entry := makeEntry(CategoryInsight, "Test content", SourceModelClaudeOpus45)
	entry.ID = "custom-id-123"

	id, err := store.InsertEntry(entry)

	require.NoError(t, err, "InsertEntry")
	assert.Equal(t, "custom-id-123", id, "Should use provided ID")
}

func TestStore_GetEntry(t *testing.T) {
	store := newTestStore(t)

	// Insert entry
	entry := makeEntry(CategoryDecision, "Decision content", SourceModelGPT52Codex)
	id, _ := store.InsertEntry(entry)

	// Retrieve entry
	retrieved, found := store.GetEntry(id)

	assert.True(t, found, "Entry should be found")
	assert.Equal(t, id, retrieved.ID, "Retrieved ID should match")
	assert.Equal(t, "Decision content", retrieved.Content, "Content should match")
	assert.Equal(t, SourceModelGPT52Codex, retrieved.Source, "Source should match")
}

func TestStore_GetEntry_NotFound(t *testing.T) {
	store := newTestStore(t)

	_, found := store.GetEntry("nonexistent-id")

	assert.False(t, found, "Entry should not be found")
}

func TestStore_UpdateEntry(t *testing.T) {
	store := newTestStore(t)

	entry := makeEntry(CategoryIssue, "Original content", SourceModelClaudeOpus45)
	id, _ := store.InsertEntry(entry)
	originalUpdatedAt := entry.UpdatedAt

	time.Sleep(10 * time.Millisecond) // Ensure time difference

	err := store.UpdateEntry(id, func(e *Entry) {
		e.Content = "Updated content"
		e.Title = "New Title"
	})

	require.NoError(t, err, "UpdateEntry")

	updated, _ := store.GetEntry(id)
	assert.Equal(t, "Updated content", updated.Content, "Content should be updated")
	assert.Equal(t, "New Title", updated.Title, "Title should be updated")
	assert.True(t, updated.UpdatedAt.After(originalUpdatedAt), "UpdatedAt should be newer")
}

func TestStore_UpdateEntry_NotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.UpdateEntry("nonexistent", func(e *Entry) {
		e.Content = "New content"
	})

	assert.Error(t, err, "UpdateEntry should fail for nonexistent entry")
}

func TestStore_Query_ByCategory(t *testing.T) {
	store := newTestStore(t)

	// Insert entries with different categories
	store.InsertEntry(makeEntry(CategoryInsight, "Insight 1", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryDecision, "Decision 1", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryInsight, "Insight 2", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryIssue, "Issue 1", SourceModelClaudeOpus45))

	results, err := store.Query(ArchiveQuery{
		Categories: []Category{CategoryInsight},
	})

	require.NoError(t, err, "Query")
	assert.Len(t, results, 2, "Should find 2 insights")
	for _, r := range results {
		assert.Equal(t, CategoryInsight, r.Category, "All results should be insights")
	}
}

func TestStore_Query_BySource(t *testing.T) {
	store := newTestStore(t)

	store.InsertEntry(makeEntry(CategoryGeneral, "Content 1", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryGeneral, "Content 2", SourceModelGPT52Codex))
	store.InsertEntry(makeEntry(CategoryGeneral, "Content 3", SourceModelClaudeOpus45))

	results, err := store.Query(ArchiveQuery{
		Sources: []SourceModel{SourceModelGPT52Codex},
	})

	require.NoError(t, err, "Query")
	assert.Len(t, results, 1, "Should find 1 entry from GPT")
	assert.Equal(t, SourceModelGPT52Codex, results[0].Source, "Source should match")
}

func TestStore_Query_BySessionID(t *testing.T) {
	store := newTestStore(t)

	// Insert entries in current session
	store.InsertEntry(makeEntry(CategoryGeneral, "Content 1", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryGeneral, "Content 2", SourceModelClaudeOpus45))
	currentSessionID := store.GetCurrentSession().ID

	// End session and start new one
	store.EndSession("Session summary", "Testing")

	// Insert entries in new session
	store.InsertEntry(makeEntry(CategoryGeneral, "Content 3", SourceModelClaudeOpus45))

	// Query for old session
	results, err := store.Query(ArchiveQuery{
		SessionIDs: []string{currentSessionID},
	})

	require.NoError(t, err, "Query")
	assert.Len(t, results, 2, "Should find 2 entries from first session")
}

func TestStore_Query_DateRange(t *testing.T) {
	store := newTestStore(t)

	// Insert entries
	store.InsertEntry(makeEntry(CategoryGeneral, "Content 1", SourceModelClaudeOpus45))
	time.Sleep(50 * time.Millisecond)

	midTime := time.Now()
	time.Sleep(50 * time.Millisecond)

	store.InsertEntry(makeEntry(CategoryGeneral, "Content 2", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryGeneral, "Content 3", SourceModelClaudeOpus45))

	// Query for entries after midTime
	results, err := store.Query(ArchiveQuery{
		Since: timePtr(midTime),
	})

	require.NoError(t, err, "Query")
	assert.Len(t, results, 2, "Should find 2 entries after midTime")
}

func TestStore_Query_Combined(t *testing.T) {
	store := newTestStore(t)

	store.InsertEntry(makeEntry(CategoryInsight, "Insight from Claude", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryInsight, "Insight from GPT", SourceModelGPT52Codex))
	store.InsertEntry(makeEntry(CategoryDecision, "Decision from Claude", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryInsight, "Another Claude insight", SourceModelClaudeOpus45))

	results, err := store.Query(ArchiveQuery{
		Categories: []Category{CategoryInsight},
		Sources:    []SourceModel{SourceModelClaudeOpus45},
	})

	require.NoError(t, err, "Query")
	assert.Len(t, results, 2, "Should find 2 Claude insights")
}

func TestStore_Query_Limit(t *testing.T) {
	store := newTestStore(t)

	for i := 0; i < 10; i++ {
		store.InsertEntry(makeEntry(CategoryGeneral, "Content", SourceModelClaudeOpus45))
	}

	results, err := store.Query(ArchiveQuery{
		Limit: 3,
	})

	require.NoError(t, err, "Query")
	assert.Len(t, results, 3, "Should respect limit")
}

func TestStore_QueryByCategory(t *testing.T) {
	store := newTestStore(t)

	for i := 0; i < 5; i++ {
		store.InsertEntry(makeEntry(CategoryTaskState, "Task", SourceModelClaudeOpus45))
	}

	results := store.QueryByCategory(CategoryTaskState, 3)

	assert.Len(t, results, 3, "Should return limited results")
}

func TestStore_QueryByCategory_ReturnsRecentFirst(t *testing.T) {
	store := newTestStore(t)

	store.InsertEntry(makeEntryWithTitle(CategoryInsight, "First", "First", SourceModelClaudeOpus45))
	time.Sleep(10 * time.Millisecond)
	store.InsertEntry(makeEntryWithTitle(CategoryInsight, "Second", "Second", SourceModelClaudeOpus45))
	time.Sleep(10 * time.Millisecond)
	store.InsertEntry(makeEntryWithTitle(CategoryInsight, "Third", "Third", SourceModelClaudeOpus45))

	results := store.QueryByCategory(CategoryInsight, 2)

	assert.Len(t, results, 2, "Should return 2 results")
	assert.Equal(t, "Third", results[0].Title, "Most recent should be first")
	assert.Equal(t, "Second", results[1].Title, "Second most recent should be second")
}

func TestStore_SearchText(t *testing.T) {
	store := newTestStore(t)

	store.InsertEntry(makeEntryWithTitle(CategoryGeneral, "Authentication", "How to implement auth", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntryWithTitle(CategoryGeneral, "Database", "Setting up PostgreSQL", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntryWithTitle(CategoryGeneral, "Auth Tokens", "JWT token handling", SourceModelClaudeOpus45))

	results, err := store.SearchText("auth", false, 10)

	require.NoError(t, err, "SearchText")
	assert.Len(t, results, 2, "Should find 2 entries matching 'auth'")
}

func TestStore_SearchText_CaseInsensitive(t *testing.T) {
	store := newTestStore(t)

	store.InsertEntry(makeEntry(CategoryGeneral, "UPPERCASE content", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryGeneral, "lowercase content", SourceModelClaudeOpus45))

	results, err := store.SearchText("CONTENT", false, 10)

	require.NoError(t, err, "SearchText")
	assert.Len(t, results, 2, "Should find both regardless of case")
}

func TestStore_Stats(t *testing.T) {
	store := newTestStore(t)

	store.InsertEntry(makeEntry(CategoryInsight, "Content", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryInsight, "Content", SourceModelClaudeOpus45))
	store.InsertEntry(makeEntry(CategoryDecision, "Content", SourceModelGPT52Codex))

	stats := store.Stats()

	assert.Equal(t, 3, stats.TotalEntries, "Total entries")
	assert.Equal(t, 2, stats.EntriesByCategory[CategoryInsight], "Insights count")
	assert.Equal(t, 1, stats.EntriesByCategory[CategoryDecision], "Decisions count")
	assert.Equal(t, 2, stats.EntriesBySource[SourceModelClaudeOpus45], "Claude entries")
	assert.Equal(t, 1, stats.EntriesBySource[SourceModelGPT52Codex], "GPT entries")
	assert.Greater(t, stats.HotMemoryTokens, 0, "Token count should be positive")
}

func TestStore_EndSession(t *testing.T) {
	store := newTestStore(t)

	originalSessionID := store.GetCurrentSession().ID

	store.InsertEntry(makeEntry(CategoryGeneral, "Content", SourceModelClaudeOpus45))

	err := store.EndSession("Session completed", "Testing")

	require.NoError(t, err, "EndSession")
	assert.NotEqual(t, originalSessionID, store.GetCurrentSession().ID, "New session should have different ID")
}

func TestStore_ConcurrentAccess(t *testing.T) {
	store := newTestStore(t)

	var wg sync.WaitGroup
	numWriters := 10
	numReaders := 10
	entriesPerWriter := 100

	// Writers
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < entriesPerWriter; j++ {
				store.InsertEntry(makeEntry(CategoryGeneral, "Content", SourceModelClaudeOpus45))
			}
		}(i)
	}

	// Readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				store.Query(ArchiveQuery{Categories: []Category{CategoryGeneral}})
				store.Stats()
			}
		}()
	}

	wg.Wait()

	stats := store.Stats()
	assert.Equal(t, numWriters*entriesPerWriter, stats.TotalEntries, "All entries should be stored")
}

func TestStore_TokenThreshold_TriggersArchival(t *testing.T) {
	store, archive := newTestStoreWithArchive(t)

	// Create a store with very low threshold
	store = NewStore(StoreConfig{
		TokenThreshold: 1000, // Very low
		Archive:        archive,
	})

	// Insert entries until we exceed threshold
	for i := 0; i < 100; i++ {
		store.InsertEntry(makeEntry(CategoryGeneral, "This is some content that takes up tokens", SourceModelClaudeOpus45))
	}

	stats := store.Stats()
	assert.LessOrEqual(t, stats.HotMemoryTokens, 1000, "Hot memory should stay under threshold")
}

// =============================================================================
// Entry Lifecycle Tests
// =============================================================================

func TestEntry_CreateWithAllCategories(t *testing.T) {
	store := newTestStore(t)

	categories := AllCategories()
	for _, cat := range categories {
		entry := makeEntry(cat, "Test content for "+string(cat), SourceModelClaudeOpus45)
		id, err := store.InsertEntry(entry)

		require.NoError(t, err, "InsertEntry for category "+string(cat))
		assert.NotEmpty(t, id, "ID should not be empty for "+string(cat))

		retrieved, found := store.GetEntry(id)
		assert.True(t, found, "Entry should be found for "+string(cat))
		assert.Equal(t, cat, retrieved.Category, "Category should match for "+string(cat))
	}
}

func TestEntry_TokenEstimation(t *testing.T) {
	store := newTestStore(t)

	shortEntry := makeEntry(CategoryGeneral, "Short", SourceModelClaudeOpus45)
	longEntry := makeEntry(CategoryGeneral, "This is a much longer piece of content that should have more tokens estimated", SourceModelClaudeOpus45)

	store.InsertEntry(shortEntry)
	store.InsertEntry(longEntry)

	assert.Greater(t, longEntry.TokensEstimate, shortEntry.TokensEstimate, "Longer content should have more tokens")
}

func TestEntry_MetadataFlexibility(t *testing.T) {
	store := newTestStore(t)

	entry := makeEntry(CategoryGeneral, "Content", SourceModelClaudeOpus45)
	entry.Metadata = map[string]any{
		"string_field": "value",
		"int_field":    42,
		"bool_field":   true,
		"nested_field": map[string]any{"inner": "value"},
		"array_field":  []string{"a", "b", "c"},
	}

	id, err := store.InsertEntry(entry)
	require.NoError(t, err, "InsertEntry with metadata")

	retrieved, found := store.GetEntry(id)
	assert.True(t, found, "Entry should be found")
	assert.Equal(t, "value", retrieved.Metadata["string_field"], "String metadata preserved")
	assert.Equal(t, 42, retrieved.Metadata["int_field"], "Int metadata preserved")
	assert.Equal(t, true, retrieved.Metadata["bool_field"], "Bool metadata preserved")
}

func TestEntry_RelatedIDsIntegrity(t *testing.T) {
	store := newTestStore(t)

	// Create first entry
	entry1 := makeEntry(CategoryDecision, "Decision 1", SourceModelClaudeOpus45)
	id1, _ := store.InsertEntry(entry1)

	// Create second entry related to first
	entry2 := makeEntry(CategoryIssue, "Issue related to decision", SourceModelClaudeOpus45)
	entry2.RelatedIDs = []string{id1}
	id2, _ := store.InsertEntry(entry2)

	// Verify relationship
	retrieved, _ := store.GetEntry(id2)
	assert.Len(t, retrieved.RelatedIDs, 1, "Should have one related ID")
	assert.Equal(t, id1, retrieved.RelatedIDs[0], "Related ID should match")
}
