package coordinator

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

func newTestScoredDocument(id string, score float64) search.ScoredDocument {
	content := "Test content for " + id
	return search.ScoredDocument{
		Document: search.Document{
			ID:         id,
			Path:       "/test/path/" + id + ".go",
			Type:       search.DocTypeSourceCode,
			Language:   "go",
			Content:    content,
			Checksum:   search.GenerateChecksum([]byte(content)),
			ModifiedAt: time.Now(),
			IndexedAt:  time.Now(),
		},
		Score:         score,
		Highlights:    map[string][]string{"content": {"highlighted"}},
		MatchedFields: []string{"path", "content"},
	}
}

// createRankedList creates a list of ScoredDocuments with specified IDs.
// Scores are assigned in descending order starting from 1.0.
func createRankedList(ids ...string) []search.ScoredDocument {
	docs := make([]search.ScoredDocument, len(ids))
	for i, id := range ids {
		docs[i] = newTestScoredDocument(id, 1.0-float64(i)*0.1)
	}
	return docs
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewRRFMerger(t *testing.T) {
	merger := NewRRFMerger()
	assert.NotNil(t, merger)
	assert.Equal(t, DefaultRRFK, merger.K())
}

func TestNewRRFMergerWithK(t *testing.T) {
	tests := []struct {
		name     string
		inputK   int
		expectedK int
	}{
		{
			name:      "positive k value",
			inputK:    100,
			expectedK: 100,
		},
		{
			name:      "k equals 1",
			inputK:    1,
			expectedK: 1,
		},
		{
			name:      "zero k defaults to DefaultRRFK",
			inputK:    0,
			expectedK: DefaultRRFK,
		},
		{
			name:      "negative k defaults to DefaultRRFK",
			inputK:    -10,
			expectedK: DefaultRRFK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			merger := NewRRFMergerWithK(tc.inputK)
			assert.Equal(t, tc.expectedK, merger.K())
		})
	}
}

// =============================================================================
// K Value Tests
// =============================================================================

func TestRRFMerger_K(t *testing.T) {
	merger := NewRRFMergerWithK(42)
	assert.Equal(t, 42, merger.K())
}

func TestRRFMerger_SetK(t *testing.T) {
	merger := NewRRFMerger()

	merger.SetK(100)
	assert.Equal(t, 100, merger.K())

	merger.SetK(0)
	assert.Equal(t, DefaultRRFK, merger.K())

	merger.SetK(-5)
	assert.Equal(t, DefaultRRFK, merger.K())
}

// =============================================================================
// Basic Merge Tests
// =============================================================================

func TestRRFMerger_Merge_TwoLists(t *testing.T) {
	merger := NewRRFMerger()

	// List 1: A(rank 1), B(rank 2), C(rank 3)
	list1 := createRankedList("A", "B", "C")
	// List 2: D(rank 1), B(rank 2), A(rank 3)
	list2 := createRankedList("D", "B", "A")

	results := merger.Merge(list1, list2)

	// B appears in both lists at different ranks
	// A appears in both lists
	// C appears only in list 1
	// D appears only in list 2
	require.Len(t, results, 4)

	// Check that B (appearing in both lists) has highest score
	// or A has high score since it appears in both
	ids := make([]string, len(results))
	for i, doc := range results {
		ids[i] = doc.ID
	}

	// B: rank 2 in list 1, rank 2 in list 2 = 1/(60+2) + 1/(60+2) = 2/62
	// A: rank 1 in list 1, rank 3 in list 2 = 1/(60+1) + 1/(60+3) = 1/61 + 1/63
	// Both have similar scores but B should be slightly higher
	assert.Contains(t, ids, "A")
	assert.Contains(t, ids, "B")
	assert.Contains(t, ids, "C")
	assert.Contains(t, ids, "D")
}

func TestRRFMerger_Merge_ScoreCalculation(t *testing.T) {
	merger := NewRRFMergerWithK(60)

	// Doc A: rank 1 in list 1, rank 3 in list 2
	// Doc B: rank 2 in list 1 only
	list1 := createRankedList("A", "B")
	list2 := createRankedList("C", "D", "A")

	results := merger.Merge(list1, list2)

	// Find documents by ID
	docByID := make(map[string]search.ScoredDocument)
	for _, doc := range results {
		docByID[doc.ID] = doc
	}

	// A: rank 1 in list 1 (0-indexed: 0), rank 3 in list 2 (0-indexed: 2)
	// RRF score = 1/(60+1) + 1/(60+3) = 1/61 + 1/63
	expectedScoreA := 1.0/61.0 + 1.0/63.0
	assert.InDelta(t, expectedScoreA, docByID["A"].Score, 1e-10)

	// B: rank 2 in list 1 only (0-indexed: 1)
	// RRF score = 1/(60+2) = 1/62
	expectedScoreB := 1.0 / 62.0
	assert.InDelta(t, expectedScoreB, docByID["B"].Score, 1e-10)
}

func TestRRFMerger_Merge_ExampleFromSpec(t *testing.T) {
	merger := NewRRFMergerWithK(60)

	// From the spec:
	// Doc A: rank 1 in list 1, rank 3 in list 2 -> score = 1/(60+1) + 1/(60+3) = 0.0164 + 0.0159 = 0.0323
	// Doc B: rank 2 in list 1 only -> score = 1/(60+2) = 0.0161
	list1 := createRankedList("A", "B")
	list2 := createRankedList("C", "D", "A")

	results := merger.Merge(list1, list2)

	docByID := make(map[string]search.ScoredDocument)
	for _, doc := range results {
		docByID[doc.ID] = doc
	}

	// A should have higher score than B
	assert.Greater(t, docByID["A"].Score, docByID["B"].Score)

	// Verify approximate scores from spec
	assert.InDelta(t, 0.0323, docByID["A"].Score, 0.0001)
	assert.InDelta(t, 0.0161, docByID["B"].Score, 0.0001)
}

// =============================================================================
// Deduplication Tests
// =============================================================================

func TestRRFMerger_Merge_DeduplicatesByID(t *testing.T) {
	merger := NewRRFMerger()

	// Same document in both lists
	list1 := createRankedList("A", "B", "C")
	list2 := createRankedList("A", "B", "C")

	results := merger.Merge(list1, list2)

	// Should have exactly 3 unique documents
	require.Len(t, results, 3)

	ids := make(map[string]bool)
	for _, doc := range results {
		assert.False(t, ids[doc.ID], "duplicate ID found: %s", doc.ID)
		ids[doc.ID] = true
	}
}

func TestRRFMerger_Merge_PreservesFirstDocumentMetadata(t *testing.T) {
	merger := NewRRFMerger()

	// Create two lists with same ID but different metadata
	doc1 := newTestScoredDocument("A", 1.0)
	doc1.Path = "/path/from/list1.go"

	doc2 := newTestScoredDocument("A", 0.9)
	doc2.Path = "/path/from/list2.go"

	list1 := []search.ScoredDocument{doc1}
	list2 := []search.ScoredDocument{doc2}

	results := merger.Merge(list1, list2)

	require.Len(t, results, 1)
	// Should preserve metadata from first occurrence
	assert.Equal(t, "/path/from/list1.go", results[0].Path)
}

// =============================================================================
// Empty Input Tests
// =============================================================================

func TestRRFMerger_Merge_BothEmpty(t *testing.T) {
	merger := NewRRFMerger()

	results := merger.Merge(nil, nil)
	assert.Nil(t, results)

	results = merger.Merge([]search.ScoredDocument{}, []search.ScoredDocument{})
	assert.Nil(t, results)
}

func TestRRFMerger_Merge_FirstEmpty(t *testing.T) {
	merger := NewRRFMerger()

	list2 := createRankedList("A", "B", "C")

	results := merger.Merge(nil, list2)
	require.Len(t, results, 3)

	results = merger.Merge([]search.ScoredDocument{}, list2)
	require.Len(t, results, 3)
}

func TestRRFMerger_Merge_SecondEmpty(t *testing.T) {
	merger := NewRRFMerger()

	list1 := createRankedList("A", "B", "C")

	results := merger.Merge(list1, nil)
	require.Len(t, results, 3)

	results = merger.Merge(list1, []search.ScoredDocument{})
	require.Len(t, results, 3)
}

// =============================================================================
// Different K Values Tests
// =============================================================================

func TestRRFMerger_Merge_DifferentKValues(t *testing.T) {
	list1 := createRankedList("A", "B")
	list2 := createRankedList("A", "C")

	tests := []struct {
		name        string
		k           int
		description string
	}{
		{
			name:        "k=1 (high sensitivity to rank)",
			k:           1,
			description: "Small k values give higher scores to top-ranked documents",
		},
		{
			name:        "k=60 (default)",
			k:           60,
			description: "Standard k value provides balanced fusion",
		},
		{
			name:        "k=100 (low sensitivity to rank)",
			k:           100,
			description: "Large k values reduce the impact of rank differences",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			merger := NewRRFMergerWithK(tc.k)
			results := merger.Merge(list1, list2)

			require.Len(t, results, 3)

			// A should always be first (appears in both lists at rank 1)
			assert.Equal(t, "A", results[0].ID)

			// Verify score calculation is correct for this k
			expectedScoreA := 1.0/float64(tc.k+1) + 1.0/float64(tc.k+1)
			assert.InDelta(t, expectedScoreA, results[0].Score, 1e-10)
		})
	}
}

func TestRRFMerger_MergeWithK_OverridesDefault(t *testing.T) {
	merger := NewRRFMergerWithK(60) // Default k=60

	list1 := createRankedList("A")
	list2 := createRankedList("A")

	// Use MergeWithK to override
	results := merger.MergeWithK(list1, list2, 100)

	require.Len(t, results, 1)

	// Score should be calculated with k=100, not k=60
	expectedScore := 2.0 / float64(100+1)
	assert.InDelta(t, expectedScore, results[0].Score, 1e-10)

	// Original k should be unchanged
	assert.Equal(t, 60, merger.K())
}

func TestRRFMerger_MergeWithK_InvalidK(t *testing.T) {
	merger := NewRRFMerger()

	list1 := createRankedList("A")
	list2 := createRankedList("A")

	// Invalid k values should default to DefaultRRFK
	results := merger.MergeWithK(list1, list2, 0)
	expectedScore := 2.0 / float64(DefaultRRFK+1)
	assert.InDelta(t, expectedScore, results[0].Score, 1e-10)

	results = merger.MergeWithK(list1, list2, -10)
	assert.InDelta(t, expectedScore, results[0].Score, 1e-10)
}

// =============================================================================
// Sorting Tests
// =============================================================================

func TestRRFMerger_Merge_SortedByDescendingScore(t *testing.T) {
	merger := NewRRFMerger()

	// Create lists where final ordering should be clear
	// A appears at rank 1 in both lists
	// B appears at rank 2 in list 1, rank 1 in list 2 (wait, need different setup)
	list1 := createRankedList("A", "B", "C", "D", "E")
	list2 := createRankedList("A", "F", "G", "H", "I")

	results := merger.Merge(list1, list2)

	// A should be first (appears in both at rank 1)
	assert.Equal(t, "A", results[0].ID)

	// Verify descending order
	for i := 1; i < len(results); i++ {
		assert.GreaterOrEqual(t, results[i-1].Score, results[i].Score,
			"results should be sorted by descending score at positions %d and %d", i-1, i)
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestRRFMerger_ThreadSafety(t *testing.T) {
	merger := NewRRFMerger()

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent reads of K
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = merger.K()
		}()
	}

	// Concurrent SetK calls
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			merger.SetK(val)
		}(i + 1)
	}

	// Concurrent Merge calls
	list1 := createRankedList("A", "B", "C")
	list2 := createRankedList("D", "E", "F")

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := merger.Merge(list1, list2)
			assert.Len(t, results, 6)
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestRRFMerger_Merge_SingleDocument(t *testing.T) {
	merger := NewRRFMerger()

	list1 := createRankedList("A")
	list2 := []search.ScoredDocument{}

	results := merger.Merge(list1, list2)

	require.Len(t, results, 1)
	assert.Equal(t, "A", results[0].ID)

	// Score = 1/(60+1) = 1/61
	expectedScore := 1.0 / 61.0
	assert.InDelta(t, expectedScore, results[0].Score, 1e-10)
}

func TestRRFMerger_Merge_LargeLists(t *testing.T) {
	merger := NewRRFMerger()

	// Create large lists
	ids1 := make([]string, 1000)
	ids2 := make([]string, 1000)

	for i := 0; i < 1000; i++ {
		ids1[i] = string(rune('A' + (i % 26))) + "_" + string(rune('0'+(i/26)%10))
		ids2[i] = string(rune('Z' - (i % 26))) + "_" + string(rune('0'+(i/26)%10))
	}

	list1 := createRankedList(ids1...)
	list2 := createRankedList(ids2...)

	results := merger.Merge(list1, list2)

	// Should handle large lists without panic
	assert.NotNil(t, results)
	assert.True(t, len(results) > 0)
}

func TestRRFMerger_Merge_ScoresArePositive(t *testing.T) {
	merger := NewRRFMerger()

	list1 := createRankedList("A", "B", "C")
	list2 := createRankedList("D", "E", "F")

	results := merger.Merge(list1, list2)

	for _, doc := range results {
		assert.Greater(t, doc.Score, 0.0, "RRF scores should always be positive")
	}
}

func TestRRFMerger_Merge_ScoresDontOverflow(t *testing.T) {
	merger := NewRRFMergerWithK(1)

	// Create lists where document appears at rank 1 in both
	list1 := createRankedList("A")
	list2 := createRankedList("A")

	results := merger.Merge(list1, list2)

	require.Len(t, results, 1)
	assert.False(t, math.IsInf(results[0].Score, 1), "score should not overflow")
	assert.False(t, math.IsNaN(results[0].Score), "score should not be NaN")
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestDefaultRRFK(t *testing.T) {
	assert.Equal(t, 60, DefaultRRFK)
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestRRFMerger_RealWorldScenario(t *testing.T) {
	merger := NewRRFMerger()

	// Simulate Bleve (text search) results
	bleveResults := []search.ScoredDocument{
		newTestScoredDocument("auth_handler", 0.95),
		newTestScoredDocument("user_service", 0.85),
		newTestScoredDocument("login_controller", 0.75),
		newTestScoredDocument("session_manager", 0.65),
	}

	// Simulate Vector (semantic search) results
	vectorResults := []search.ScoredDocument{
		newTestScoredDocument("user_service", 0.92),
		newTestScoredDocument("auth_middleware", 0.88),
		newTestScoredDocument("auth_handler", 0.82),
		newTestScoredDocument("permission_checker", 0.78),
	}

	results := merger.Merge(bleveResults, vectorResults)

	// user_service and auth_handler appear in both lists
	// They should have higher RRF scores
	require.GreaterOrEqual(t, len(results), 2)

	// Find auth_handler and user_service
	var authHandlerScore, userServiceScore float64
	for _, doc := range results {
		if doc.ID == "auth_handler" {
			authHandlerScore = doc.Score
		}
		if doc.ID == "user_service" {
			userServiceScore = doc.Score
		}
	}

	// Both should have scores > single-list documents
	singleListScore := 1.0 / float64(DefaultRRFK+1) // Best possible single-list score
	assert.Greater(t, authHandlerScore, singleListScore)
	assert.Greater(t, userServiceScore, singleListScore)
}
