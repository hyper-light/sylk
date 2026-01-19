package context

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// SearchTier Tests
// =============================================================================

func TestSearchTier_String_AllValidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tier     SearchTier
		expected string
	}{
		{
			name:     "TierNone returns none",
			tier:     TierNone,
			expected: "none",
		},
		{
			name:     "TierHotCache returns hot_cache",
			tier:     TierHotCache,
			expected: "hot_cache",
		},
		{
			name:     "TierWarmIndex returns warm_index",
			tier:     TierWarmIndex,
			expected: "warm_index",
		},
		{
			name:     "TierFullSearch returns full_search",
			tier:     TierFullSearch,
			expected: "full_search",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.tier.String())
		})
	}
}

func TestSearchTier_String_InvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tier SearchTier
	}{
		{name: "negative value", tier: SearchTier(-1)},
		{name: "value beyond range", tier: SearchTier(4)},
		{name: "large positive value", tier: SearchTier(100)},
		{name: "large negative value", tier: SearchTier(-100)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "unknown", tt.tier.String())
		})
	}
}

func TestSearchTier_Constants(t *testing.T) {
	t.Parallel()

	// Verify tier ordering (lower = faster)
	assert.Less(t, int(TierNone), int(TierHotCache))
	assert.Less(t, int(TierHotCache), int(TierWarmIndex))
	assert.Less(t, int(TierWarmIndex), int(TierFullSearch))

	// Verify specific values
	assert.Equal(t, SearchTier(0), TierNone)
	assert.Equal(t, SearchTier(1), TierHotCache)
	assert.Equal(t, SearchTier(2), TierWarmIndex)
	assert.Equal(t, SearchTier(3), TierFullSearch)
}

// =============================================================================
// Excerpt Tests
// =============================================================================

func TestExcerpt_ZeroValue(t *testing.T) {
	t.Parallel()

	var excerpt Excerpt

	assert.Empty(t, excerpt.ID)
	assert.Empty(t, excerpt.Content)
	assert.Empty(t, excerpt.Source)
	assert.Equal(t, 0.0, excerpt.Confidence)
	assert.Equal(t, 0, excerpt.TokenCount)
	assert.Equal(t, [2]int{0, 0}, excerpt.LineRange)
	assert.Equal(t, 0.0, excerpt.Relevance)
}

func TestExcerpt_FullPopulation(t *testing.T) {
	t.Parallel()

	excerpt := Excerpt{
		ID:         "excerpt-123",
		Content:    "func main() { fmt.Println(\"Hello\") }",
		Source:     "/path/to/main.go",
		Confidence: 0.95,
		TokenCount: 50,
		LineRange:  [2]int{10, 15},
		Relevance:  0.87,
	}

	assert.Equal(t, "excerpt-123", excerpt.ID)
	assert.Contains(t, excerpt.Content, "func main()")
	assert.Equal(t, "/path/to/main.go", excerpt.Source)
	assert.Equal(t, 0.95, excerpt.Confidence)
	assert.Equal(t, 50, excerpt.TokenCount)
	assert.Equal(t, [2]int{10, 15}, excerpt.LineRange)
	assert.Equal(t, 0.87, excerpt.Relevance)
}

func TestExcerpt_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		excerpt Excerpt
	}{
		{
			name: "zero line range",
			excerpt: Excerpt{
				LineRange: [2]int{0, 0},
			},
		},
		{
			name: "same start and end line",
			excerpt: Excerpt{
				LineRange: [2]int{5, 5},
			},
		},
		{
			name: "boundary confidence values",
			excerpt: Excerpt{
				Confidence: 0.0,
				Relevance:  1.0,
			},
		},
		{
			name: "negative token count (edge case)",
			excerpt: Excerpt{
				TokenCount: -1, // Should be avoided but test resilience
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Just verify no panics occur
			_ = tt.excerpt.ID
			_ = tt.excerpt.LineRange[0]
			_ = tt.excerpt.LineRange[1]
		})
	}
}

// =============================================================================
// Summary Tests
// =============================================================================

func TestSummary_ZeroValue(t *testing.T) {
	t.Parallel()

	var summary Summary

	assert.Empty(t, summary.ID)
	assert.Empty(t, summary.Text)
	assert.Empty(t, summary.Source)
	assert.Equal(t, 0.0, summary.Confidence)
	assert.Equal(t, 0, summary.TokenCount)
}

func TestSummary_FullPopulation(t *testing.T) {
	t.Parallel()

	summary := Summary{
		ID:         "summary-456",
		Text:       "Main entry point for the application",
		Source:     "/path/to/main.go",
		Confidence: 0.82,
		TokenCount: 200,
	}

	assert.Equal(t, "summary-456", summary.ID)
	assert.Equal(t, "Main entry point for the application", summary.Text)
	assert.Equal(t, "/path/to/main.go", summary.Source)
	assert.Equal(t, 0.82, summary.Confidence)
	assert.Equal(t, 200, summary.TokenCount)
}

// =============================================================================
// AugmentedQuery Tests
// =============================================================================

func TestAugmentedQuery_ZeroValue(t *testing.T) {
	t.Parallel()

	var aq AugmentedQuery

	assert.Empty(t, aq.OriginalQuery)
	assert.Nil(t, aq.Excerpts)
	assert.Nil(t, aq.Summaries)
	assert.Equal(t, 0, aq.TokensUsed)
	assert.Equal(t, 0, aq.BudgetMax)
	assert.Equal(t, TierNone, aq.TierSource)
	assert.Equal(t, time.Duration(0), aq.PrefetchDuration)
}

func TestAugmentedQuery_FullPopulation(t *testing.T) {
	t.Parallel()

	excerpts := []Excerpt{
		{ID: "e1", Content: "content1", Confidence: 0.9},
		{ID: "e2", Content: "content2", Confidence: 0.8},
	}
	summaries := []Summary{
		{ID: "s1", Text: "summary1", Confidence: 0.7},
	}

	aq := AugmentedQuery{
		OriginalQuery:    "How do I implement X?",
		Excerpts:         excerpts,
		Summaries:        summaries,
		TokensUsed:       500,
		BudgetMax:        2000,
		TierSource:       TierWarmIndex,
		PrefetchDuration: 50 * time.Millisecond,
	}

	assert.Equal(t, "How do I implement X?", aq.OriginalQuery)
	assert.Len(t, aq.Excerpts, 2)
	assert.Len(t, aq.Summaries, 1)
	assert.Equal(t, 500, aq.TokensUsed)
	assert.Equal(t, 2000, aq.BudgetMax)
	assert.Equal(t, TierWarmIndex, aq.TierSource)
	assert.Equal(t, 50*time.Millisecond, aq.PrefetchDuration)
}

func TestAugmentedQuery_EmptySlices(t *testing.T) {
	t.Parallel()

	aq := AugmentedQuery{
		OriginalQuery: "test query",
		Excerpts:      []Excerpt{},
		Summaries:     []Summary{},
	}

	assert.NotNil(t, aq.Excerpts)
	assert.NotNil(t, aq.Summaries)
	assert.Len(t, aq.Excerpts, 0)
	assert.Len(t, aq.Summaries, 0)
}

// =============================================================================
// RetrievalResult Tests
// =============================================================================

func TestRetrievalResult_ZeroValue(t *testing.T) {
	t.Parallel()

	var rr RetrievalResult

	assert.Nil(t, rr.Entries)
	assert.Equal(t, 0, rr.TotalTokens)
	assert.False(t, rr.Truncated)
	assert.Empty(t, rr.Query)
	assert.Empty(t, rr.Source)
}

func TestRetrievalResult_FullPopulation(t *testing.T) {
	t.Parallel()

	entries := []*ContentEntry{
		{ID: "entry1", Content: "content1"},
		{ID: "entry2", Content: "content2"},
	}

	rr := RetrievalResult{
		Entries:     entries,
		TotalTokens: 1000,
		Truncated:   true,
		Query:       "search query",
		Source:      "search",
	}

	assert.Len(t, rr.Entries, 2)
	assert.Equal(t, 1000, rr.TotalTokens)
	assert.True(t, rr.Truncated)
	assert.Equal(t, "search query", rr.Query)
	assert.Equal(t, "search", rr.Source)
}

func TestRetrievalResult_NilEntries(t *testing.T) {
	t.Parallel()

	rr := RetrievalResult{
		Entries: nil,
		Query:   "test",
	}

	assert.Nil(t, rr.Entries)
	assert.Len(t, rr.Entries, 0) // nil slice has length 0
}

func TestRetrievalResult_EmptyEntries(t *testing.T) {
	t.Parallel()

	rr := RetrievalResult{
		Entries: []*ContentEntry{},
		Query:   "test",
	}

	assert.NotNil(t, rr.Entries)
	assert.Len(t, rr.Entries, 0)
}

func TestRetrievalResult_SourceValues(t *testing.T) {
	t.Parallel()

	sources := []string{"reference", "search", "direct"}

	for _, source := range sources {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			rr := RetrievalResult{Source: source}
			assert.Equal(t, source, rr.Source)
		})
	}
}

// =============================================================================
// RetrievalBudget Tests
// =============================================================================

func TestRetrievalBudget_ZeroValue(t *testing.T) {
	t.Parallel()

	var rb RetrievalBudget

	assert.Equal(t, 0, rb.MaxTokensPerQuery)
	assert.Equal(t, 0, rb.MaxQueriesPerTurn)
	assert.Equal(t, 0, rb.MaxTotalTokens)
	assert.Equal(t, int64(0), rb.UsedTokens.Load())
	assert.Equal(t, int64(0), rb.UsedQueries.Load())
}

func TestRetrievalBudget_CanQuery_AllCombinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		maxTotalTokens  int
		maxQueriesPerTurn int
		usedTokens      int64
		usedQueries     int64
		expectedCanQuery bool
	}{
		{
			name:             "both within budget",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 10,
			usedTokens:       500,
			usedQueries:      5,
			expectedCanQuery: true,
		},
		{
			name:             "tokens at zero, queries at zero",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 10,
			usedTokens:       0,
			usedQueries:      0,
			expectedCanQuery: true,
		},
		{
			name:             "tokens exhausted, queries available",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 10,
			usedTokens:       1000,
			usedQueries:      5,
			expectedCanQuery: false,
		},
		{
			name:             "tokens available, queries exhausted",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 10,
			usedTokens:       500,
			usedQueries:      10,
			expectedCanQuery: false,
		},
		{
			name:             "both exhausted",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 10,
			usedTokens:       1000,
			usedQueries:      10,
			expectedCanQuery: false,
		},
		{
			name:             "tokens over budget, queries available",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 10,
			usedTokens:       1500,
			usedQueries:      5,
			expectedCanQuery: false,
		},
		{
			name:             "tokens available, queries over budget",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 10,
			usedTokens:       500,
			usedQueries:      15,
			expectedCanQuery: false,
		},
		{
			name:             "zero max tokens - always exhausted",
			maxTotalTokens:   0,
			maxQueriesPerTurn: 10,
			usedTokens:       0,
			usedQueries:      0,
			expectedCanQuery: false,
		},
		{
			name:             "zero max queries - always exhausted",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 0,
			usedTokens:       0,
			usedQueries:      0,
			expectedCanQuery: false,
		},
		{
			name:             "both max at zero - always exhausted",
			maxTotalTokens:   0,
			maxQueriesPerTurn: 0,
			usedTokens:       0,
			usedQueries:      0,
			expectedCanQuery: false,
		},
		{
			name:             "one token remaining, one query remaining",
			maxTotalTokens:   1000,
			maxQueriesPerTurn: 10,
			usedTokens:       999,
			usedQueries:      9,
			expectedCanQuery: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rb := RetrievalBudget{
				MaxTotalTokens:    tt.maxTotalTokens,
				MaxQueriesPerTurn: tt.maxQueriesPerTurn,
			}
			rb.UsedTokens.Store(tt.usedTokens)
			rb.UsedQueries.Store(tt.usedQueries)

			assert.Equal(t, tt.expectedCanQuery, rb.CanQuery())
		})
	}
}

func TestRetrievalBudget_RecordUsage_Basic(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTokensPerQuery: 100,
		MaxQueriesPerTurn: 10,
		MaxTotalTokens:    1000,
	}

	assert.Equal(t, int64(0), rb.UsedTokens.Load())
	assert.Equal(t, int64(0), rb.UsedQueries.Load())

	rb.RecordUsage(50)

	assert.Equal(t, int64(50), rb.UsedTokens.Load())
	assert.Equal(t, int64(1), rb.UsedQueries.Load())

	rb.RecordUsage(75)

	assert.Equal(t, int64(125), rb.UsedTokens.Load())
	assert.Equal(t, int64(2), rb.UsedQueries.Load())
}

func TestRetrievalBudget_RecordUsage_ZeroTokens(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    1000,
		MaxQueriesPerTurn: 10,
	}

	rb.RecordUsage(0)

	assert.Equal(t, int64(0), rb.UsedTokens.Load())
	assert.Equal(t, int64(1), rb.UsedQueries.Load())
}

func TestRetrievalBudget_RecordUsage_NegativeTokens(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    1000,
		MaxQueriesPerTurn: 10,
	}

	rb.RecordUsage(100)
	rb.RecordUsage(-50) // Edge case: negative tokens

	assert.Equal(t, int64(50), rb.UsedTokens.Load())
	assert.Equal(t, int64(2), rb.UsedQueries.Load())
}

func TestRetrievalBudget_RecordUsage_MultipleRecords(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    10000,
		MaxQueriesPerTurn: 100,
	}

	expectedTokens := int64(0)
	for i := 0; i < 50; i++ {
		tokens := (i + 1) * 10
		rb.RecordUsage(tokens)
		expectedTokens += int64(tokens)
	}

	assert.Equal(t, expectedTokens, rb.UsedTokens.Load())
	assert.Equal(t, int64(50), rb.UsedQueries.Load())
}

func TestRetrievalBudget_CanQuery_AfterRecordUsage(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    100,
		MaxQueriesPerTurn: 3,
	}

	// Initially can query
	assert.True(t, rb.CanQuery())

	// First query
	rb.RecordUsage(30)
	assert.True(t, rb.CanQuery())

	// Second query
	rb.RecordUsage(30)
	assert.True(t, rb.CanQuery())

	// Third query - hits query limit
	rb.RecordUsage(30)
	assert.False(t, rb.CanQuery()) // 3 queries used, max is 3
}

func TestRetrievalBudget_CanQuery_TokenExhaustion(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    100,
		MaxQueriesPerTurn: 10,
	}

	assert.True(t, rb.CanQuery())

	rb.RecordUsage(50)
	assert.True(t, rb.CanQuery())

	rb.RecordUsage(50)
	assert.False(t, rb.CanQuery()) // 100 tokens used, max is 100
}

// =============================================================================
// RetrievalBudget Race Condition Tests
// =============================================================================

func TestRetrievalBudget_RecordUsage_Concurrent(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    1000000,
		MaxQueriesPerTurn: 10000,
	}

	const numGoroutines = 100
	const usagePerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			rb.RecordUsage(10)
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(numGoroutines*10), rb.UsedTokens.Load())
	assert.Equal(t, int64(numGoroutines), rb.UsedQueries.Load())
}

func TestRetrievalBudget_CanQuery_Concurrent(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    1000,
		MaxQueriesPerTurn: 100,
	}

	const numGoroutines = 50

	var wg sync.WaitGroup
	results := make(chan bool, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			result := rb.CanQuery()
			results <- result
		}()
	}

	wg.Wait()
	close(results)

	// All should return true since no usage was recorded
	for result := range results {
		assert.True(t, result)
	}
}

func TestRetrievalBudget_ConcurrentRecordAndQuery(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    500,
		MaxQueriesPerTurn: 50,
	}

	const numWriters = 25
	const numReaders = 25

	var wg sync.WaitGroup
	wg.Add(numWriters + numReaders)

	// Writers record usage
	for i := 0; i < numWriters; i++ {
		go func() {
			defer wg.Done()
			rb.RecordUsage(10)
		}()
	}

	// Readers check if can query
	canQueryResults := make(chan bool, numReaders)
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			result := rb.CanQuery()
			canQueryResults <- result
		}()
	}

	wg.Wait()
	close(canQueryResults)

	// Final state should be consistent
	assert.Equal(t, int64(numWriters*10), rb.UsedTokens.Load())
	assert.Equal(t, int64(numWriters), rb.UsedQueries.Load())
}

func TestRetrievalBudget_HighContentionRecordUsage(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTotalTokens:    10000000,
		MaxQueriesPerTurn: 100000,
	}

	const numGoroutines = 1000
	const recordsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				rb.RecordUsage(1)
			}
		}()
	}

	wg.Wait()

	expectedTokens := int64(numGoroutines * recordsPerGoroutine)
	expectedQueries := int64(numGoroutines * recordsPerGoroutine)

	assert.Equal(t, expectedTokens, rb.UsedTokens.Load())
	assert.Equal(t, expectedQueries, rb.UsedQueries.Load())
}

// =============================================================================
// ContentType Tests
// =============================================================================

func TestContentType_AllValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ct       ContentType
		expected string
	}{
		{"ContentTypeUserPrompt", ContentTypeUserPrompt, "user_prompt"},
		{"ContentTypeAgentResponse", ContentTypeAgentResponse, "agent_response"},
		{"ContentTypeToolCall", ContentTypeToolCall, "tool_call"},
		{"ContentTypeToolResult", ContentTypeToolResult, "tool_result"},
		{"ContentTypeCodeFile", ContentTypeCodeFile, "code_file"},
		{"ContentTypeWebFetch", ContentTypeWebFetch, "web_fetch"},
		{"ContentTypeResearchPaper", ContentTypeResearchPaper, "research_paper"},
		{"ContentTypeAgentMessage", ContentTypeAgentMessage, "agent_message"},
		{"ContentTypePlanWorkflow", ContentTypePlanWorkflow, "plan_workflow"},
		{"ContentTypeTestResult", ContentTypeTestResult, "test_result"},
		{"ContentTypeInspectorFinding", ContentTypeInspectorFinding, "inspector_finding"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, string(tt.ct))
		})
	}
}

func TestContentType_CustomValue(t *testing.T) {
	t.Parallel()

	customType := ContentType("custom_type")
	assert.Equal(t, "custom_type", string(customType))
}

// =============================================================================
// ContentEntry Tests
// =============================================================================

func TestContentEntry_ZeroValue(t *testing.T) {
	t.Parallel()

	var ce ContentEntry

	assert.Empty(t, ce.ID)
	assert.Empty(t, ce.SessionID)
	assert.Empty(t, ce.AgentID)
	assert.Empty(t, ce.AgentType)
	assert.Empty(t, ce.ContentType)
	assert.Empty(t, ce.Content)
	assert.Equal(t, 0, ce.TokenCount)
	assert.True(t, ce.Timestamp.IsZero())
	assert.Equal(t, 0, ce.TurnNumber)
	assert.Nil(t, ce.Embedding)
	assert.Nil(t, ce.Keywords)
	assert.Nil(t, ce.Entities)
	assert.Empty(t, ce.ParentID)
	assert.Nil(t, ce.ChildIDs)
	assert.Nil(t, ce.RelatedFiles)
	assert.Nil(t, ce.Metadata)
}

func TestContentEntry_FullPopulation(t *testing.T) {
	t.Parallel()

	now := time.Now()
	embedding := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	keywords := []string{"func", "main", "go"}
	entities := []string{"function", "entry_point"}
	childIDs := []string{"child1", "child2"}
	relatedFiles := []string{"/path/to/file1.go", "/path/to/file2.go"}
	metadata := map[string]any{
		"key1": "value1",
		"key2": 42,
	}

	ce := ContentEntry{
		ID:           "sha256-hash-here",
		SessionID:    "session-123",
		AgentID:      "agent-456",
		AgentType:    "code_writer",
		ContentType:  ContentTypeCodeFile,
		Content:      "func main() {}",
		TokenCount:   10,
		Timestamp:    now,
		TurnNumber:   5,
		Embedding:    embedding,
		Keywords:     keywords,
		Entities:     entities,
		ParentID:     "parent-789",
		ChildIDs:     childIDs,
		RelatedFiles: relatedFiles,
		Metadata:     metadata,
	}

	assert.Equal(t, "sha256-hash-here", ce.ID)
	assert.Equal(t, "session-123", ce.SessionID)
	assert.Equal(t, "agent-456", ce.AgentID)
	assert.Equal(t, "code_writer", ce.AgentType)
	assert.Equal(t, ContentTypeCodeFile, ce.ContentType)
	assert.Equal(t, "func main() {}", ce.Content)
	assert.Equal(t, 10, ce.TokenCount)
	assert.Equal(t, now, ce.Timestamp)
	assert.Equal(t, 5, ce.TurnNumber)
	assert.Equal(t, embedding, ce.Embedding)
	assert.Equal(t, keywords, ce.Keywords)
	assert.Equal(t, entities, ce.Entities)
	assert.Equal(t, "parent-789", ce.ParentID)
	assert.Equal(t, childIDs, ce.ChildIDs)
	assert.Equal(t, relatedFiles, ce.RelatedFiles)
	assert.Equal(t, metadata, ce.Metadata)
}

func TestContentEntry_EmptySlices(t *testing.T) {
	t.Parallel()

	ce := ContentEntry{
		ID:           "test",
		Keywords:     []string{},
		Entities:     []string{},
		ChildIDs:     []string{},
		RelatedFiles: []string{},
		Embedding:    []float32{},
	}

	assert.NotNil(t, ce.Keywords)
	assert.NotNil(t, ce.Entities)
	assert.NotNil(t, ce.ChildIDs)
	assert.NotNil(t, ce.RelatedFiles)
	assert.NotNil(t, ce.Embedding)
	assert.Len(t, ce.Keywords, 0)
	assert.Len(t, ce.Entities, 0)
	assert.Len(t, ce.ChildIDs, 0)
	assert.Len(t, ce.RelatedFiles, 0)
	assert.Len(t, ce.Embedding, 0)
}

func TestContentEntry_NilMetadata(t *testing.T) {
	t.Parallel()

	ce := ContentEntry{
		ID:       "test",
		Metadata: nil,
	}

	assert.Nil(t, ce.Metadata)

	// Verify we can check for nil without panic
	if ce.Metadata != nil {
		t.Error("Expected metadata to be nil")
	}
}

func TestContentEntry_EmptyMetadata(t *testing.T) {
	t.Parallel()

	ce := ContentEntry{
		ID:       "test",
		Metadata: map[string]any{},
	}

	assert.NotNil(t, ce.Metadata)
	assert.Len(t, ce.Metadata, 0)
}

func TestContentEntry_MetadataWithVariousTypes(t *testing.T) {
	t.Parallel()

	ce := ContentEntry{
		ID: "test",
		Metadata: map[string]any{
			"string":  "value",
			"int":     42,
			"float":   3.14,
			"bool":    true,
			"nil":     nil,
			"slice":   []string{"a", "b"},
			"nested":  map[string]any{"key": "value"},
		},
	}

	assert.Equal(t, "value", ce.Metadata["string"])
	assert.Equal(t, 42, ce.Metadata["int"])
	assert.Equal(t, 3.14, ce.Metadata["float"])
	assert.Equal(t, true, ce.Metadata["bool"])
	assert.Nil(t, ce.Metadata["nil"])
	assert.Equal(t, []string{"a", "b"}, ce.Metadata["slice"])
	assert.Equal(t, map[string]any{"key": "value"}, ce.Metadata["nested"])
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestRetrievalBudget_RealWorldScenario(t *testing.T) {
	t.Parallel()

	rb := RetrievalBudget{
		MaxTokensPerQuery: 500,
		MaxQueriesPerTurn: 5,
		MaxTotalTokens:    2000,
	}

	// Simulate a series of queries
	queries := []struct {
		tokens   int
		canQuery bool
	}{
		{400, true},  // Query 1: 400 tokens, can query
		{500, true},  // Query 2: 900 total, can query
		{400, true},  // Query 3: 1300 total, can query
		{500, true},  // Query 4: 1800 total, can query
		{100, true},  // Query 5: 1900 total, can query (last allowed query)
		{100, false}, // Query 6: Would be 2000 total, but query limit reached
	}

	for i, q := range queries {
		canQuery := rb.CanQuery()
		assert.Equal(t, q.canQuery, canQuery, "Query %d: expected canQuery=%v, got %v", i+1, q.canQuery, canQuery)

		if canQuery {
			rb.RecordUsage(q.tokens)
		}
	}

	assert.Equal(t, int64(1900), rb.UsedTokens.Load())
	assert.Equal(t, int64(5), rb.UsedQueries.Load())
}

func TestAugmentedQuery_WithAllTiers(t *testing.T) {
	t.Parallel()

	tiers := []SearchTier{TierNone, TierHotCache, TierWarmIndex, TierFullSearch}

	for _, tier := range tiers {
		t.Run(tier.String(), func(t *testing.T) {
			t.Parallel()

			aq := AugmentedQuery{
				OriginalQuery: "test query",
				TierSource:    tier,
			}

			assert.Equal(t, tier, aq.TierSource)
			assert.NotEmpty(t, aq.TierSource.String())
		})
	}
}
