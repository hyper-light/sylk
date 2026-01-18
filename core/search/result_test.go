package search

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

func newTestScoredDocument(id string, score float64) ScoredDocument {
	content := "Test content for " + id
	return ScoredDocument{
		Document: Document{
			ID:         id,
			Path:       "/test/path/" + id + ".go",
			Type:       DocTypeSourceCode,
			Language:   "go",
			Content:    content,
			Checksum:   GenerateChecksum([]byte(content)),
			ModifiedAt: time.Now(),
			IndexedAt:  time.Now(),
		},
		Score:         score,
		Highlights:    map[string][]string{"content": {"highlighted"}},
		MatchedFields: []string{"path", "content"},
	}
}

func newTestSearchResult(n int) *SearchResult {
	docs := make([]ScoredDocument, n)
	for i := 0; i < n; i++ {
		docs[i] = newTestScoredDocument(
			string(rune('A'+i)),
			float64(n-i)*0.1, // Scores: 0.5, 0.4, 0.3, 0.2, 0.1 for n=5
		)
	}
	return &SearchResult{
		Documents:  docs,
		TotalHits:  int64(n),
		SearchTime: 100 * time.Millisecond,
		Query:      "test query",
	}
}

// =============================================================================
// FusionMethod Tests
// =============================================================================

func TestFusionMethodValues(t *testing.T) {
	assert.Equal(t, FusionMethod("rrf"), FusionRRF)
	assert.Equal(t, FusionMethod("linear"), FusionLinear)
	assert.Equal(t, FusionMethod("max"), FusionMax)
}

// =============================================================================
// ScoredDocument Tests
// =============================================================================

func TestScoredDocument_EmbeddedDocument(t *testing.T) {
	doc := newTestScoredDocument("test-id", 0.95)

	assert.Equal(t, "test-id", doc.ID)
	assert.Equal(t, "/test/path/test-id.go", doc.Path)
	assert.Equal(t, DocTypeSourceCode, doc.Type)
	assert.Equal(t, 0.95, doc.Score)
	assert.Contains(t, doc.Highlights, "content")
	assert.Contains(t, doc.MatchedFields, "content")
}

// =============================================================================
// SearchResult Tests
// =============================================================================

func TestSearchResult_HasResults(t *testing.T) {
	tests := []struct {
		name     string
		result   *SearchResult
		expected bool
	}{
		{
			name:     "empty result",
			result:   &SearchResult{Documents: nil},
			expected: false,
		},
		{
			name:     "empty slice",
			result:   &SearchResult{Documents: []ScoredDocument{}},
			expected: false,
		},
		{
			name:     "with documents",
			result:   newTestSearchResult(3),
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.result.HasResults())
		})
	}
}

func TestSearchResult_TopN(t *testing.T) {
	tests := []struct {
		name          string
		result        *SearchResult
		n             int
		expectedLen   int
		expectedFirst string
	}{
		{
			name:        "n is zero",
			result:      newTestSearchResult(5),
			n:           0,
			expectedLen: 0,
		},
		{
			name:        "n is negative",
			result:      newTestSearchResult(5),
			n:           -1,
			expectedLen: 0,
		},
		{
			name:          "n less than total",
			result:        newTestSearchResult(5),
			n:             3,
			expectedLen:   3,
			expectedFirst: "A",
		},
		{
			name:          "n equals total",
			result:        newTestSearchResult(5),
			n:             5,
			expectedLen:   5,
			expectedFirst: "A",
		},
		{
			name:          "n greater than total",
			result:        newTestSearchResult(3),
			n:             10,
			expectedLen:   3,
			expectedFirst: "A",
		},
		{
			name:        "empty result",
			result:      &SearchResult{Documents: []ScoredDocument{}},
			n:           5,
			expectedLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			top := tc.result.TopN(tc.n)
			assert.Len(t, top, tc.expectedLen)
			if tc.expectedLen > 0 && tc.expectedFirst != "" {
				assert.Equal(t, tc.expectedFirst, top[0].ID)
			}
		})
	}
}

func TestSearchResult_FilterByScore(t *testing.T) {
	tests := []struct {
		name        string
		result      *SearchResult
		minScore    float64
		expectedLen int
	}{
		{
			name:        "filter all (high threshold)",
			result:      newTestSearchResult(5),
			minScore:    1.0,
			expectedLen: 0,
		},
		{
			name:        "filter none (low threshold)",
			result:      newTestSearchResult(5),
			minScore:    0.0,
			expectedLen: 5,
		},
		{
			name:        "filter some",
			result:      newTestSearchResult(5),
			minScore:    0.35,
			expectedLen: 2, // 0.5 and 0.4 pass
		},
		{
			name:        "exact match threshold",
			result:      newTestSearchResult(5),
			minScore:    0.3,
			expectedLen: 3, // 0.5, 0.4, 0.3 pass (>= minScore)
		},
		{
			name:        "negative threshold",
			result:      newTestSearchResult(5),
			minScore:    -1.0,
			expectedLen: 5,
		},
		{
			name:        "empty result",
			result:      &SearchResult{Documents: []ScoredDocument{}},
			minScore:    0.5,
			expectedLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filtered := tc.result.FilterByScore(tc.minScore)
			assert.Len(t, filtered, tc.expectedLen)
		})
	}
}

func TestSearchResult_FilterByScore_PreservesOrder(t *testing.T) {
	result := newTestSearchResult(5)
	filtered := result.FilterByScore(0.25)

	require.Len(t, filtered, 3)
	assert.True(t, filtered[0].Score >= filtered[1].Score)
	assert.True(t, filtered[1].Score >= filtered[2].Score)
}

// =============================================================================
// SearchRequest Tests
// =============================================================================

func TestSearchRequest_Validate_EmptyQuery(t *testing.T) {
	req := &SearchRequest{Query: ""}
	err := req.Validate()
	assert.ErrorIs(t, err, ErrEmptyQuery)
}

func TestSearchRequest_Validate_ValidQuery(t *testing.T) {
	req := &SearchRequest{Query: "search term", Limit: 10}
	err := req.Validate()
	assert.NoError(t, err)
}

func TestSearchRequest_Validate_LimitBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		limit       int
		expectError error
	}{
		{
			name:        "limit zero (valid, will be normalized)",
			limit:       0,
			expectError: nil,
		},
		{
			name:        "limit negative",
			limit:       -1,
			expectError: ErrLimitNegative,
		},
		{
			name:        "limit at max",
			limit:       MaxLimit,
			expectError: nil,
		},
		{
			name:        "limit exceeds max",
			limit:       MaxLimit + 1,
			expectError: ErrLimitTooHigh,
		},
		{
			name:        "limit very high",
			limit:       10000,
			expectError: ErrLimitTooHigh,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &SearchRequest{Query: "test", Limit: tc.limit}
			err := req.Validate()
			if tc.expectError != nil {
				assert.ErrorIs(t, err, tc.expectError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSearchRequest_Validate_OffsetBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		offset      int
		expectError error
	}{
		{
			name:        "offset zero",
			offset:      0,
			expectError: nil,
		},
		{
			name:        "offset positive",
			offset:      100,
			expectError: nil,
		},
		{
			name:        "offset negative",
			offset:      -1,
			expectError: ErrOffsetNegative,
		},
		{
			name:        "offset very negative",
			offset:      -1000,
			expectError: ErrOffsetNegative,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &SearchRequest{Query: "test", Offset: tc.offset}
			err := req.Validate()
			if tc.expectError != nil {
				assert.ErrorIs(t, err, tc.expectError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSearchRequest_Validate_FuzzyLevelBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		fuzzy       int
		expectError error
	}{
		{
			name:        "fuzzy zero",
			fuzzy:       0,
			expectError: nil,
		},
		{
			name:        "fuzzy one",
			fuzzy:       1,
			expectError: nil,
		},
		{
			name:        "fuzzy two (max)",
			fuzzy:       2,
			expectError: nil,
		},
		{
			name:        "fuzzy three (exceeds max)",
			fuzzy:       3,
			expectError: ErrFuzzyInvalid,
		},
		{
			name:        "fuzzy negative",
			fuzzy:       -1,
			expectError: ErrFuzzyInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &SearchRequest{Query: "test", FuzzyLevel: tc.fuzzy}
			err := req.Validate()
			if tc.expectError != nil {
				assert.ErrorIs(t, err, tc.expectError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSearchRequest_Normalize(t *testing.T) {
	tests := []struct {
		name          string
		initialLimit  int
		expectedLimit int
	}{
		{
			name:          "zero limit normalized to default",
			initialLimit:  0,
			expectedLimit: DefaultLimit,
		},
		{
			name:          "explicit limit preserved",
			initialLimit:  50,
			expectedLimit: 50,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &SearchRequest{Query: "test", Limit: tc.initialLimit}
			req.Normalize()
			assert.Equal(t, tc.expectedLimit, req.Limit)
		})
	}
}

func TestSearchRequest_ValidateAndNormalize(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		req := &SearchRequest{Query: "test", Limit: 0}
		err := req.ValidateAndNormalize()
		assert.NoError(t, err)
		assert.Equal(t, DefaultLimit, req.Limit)
	})

	t.Run("invalid request not normalized", func(t *testing.T) {
		req := &SearchRequest{Query: "", Limit: 0}
		err := req.ValidateAndNormalize()
		assert.ErrorIs(t, err, ErrEmptyQuery)
		assert.Equal(t, 0, req.Limit) // Not normalized due to validation failure
	})
}

func TestSearchRequest_WithDocumentType(t *testing.T) {
	req := &SearchRequest{
		Query: "test",
		Type:  DocTypeSourceCode,
	}
	err := req.Validate()
	assert.NoError(t, err)
	assert.Equal(t, DocTypeSourceCode, req.Type)
}

func TestSearchRequest_WithPathFilter(t *testing.T) {
	req := &SearchRequest{
		Query:      "test",
		PathFilter: "**/*.go",
	}
	err := req.Validate()
	assert.NoError(t, err)
	assert.Equal(t, "**/*.go", req.PathFilter)
}

func TestSearchRequest_FullConfiguration(t *testing.T) {
	req := &SearchRequest{
		Query:             "search term",
		Type:              DocTypeMarkdown,
		PathFilter:        "docs/**/*.md",
		Limit:             50,
		Offset:            100,
		FuzzyLevel:        2,
		IncludeHighlights: true,
	}

	err := req.ValidateAndNormalize()
	assert.NoError(t, err)
	assert.Equal(t, "search term", req.Query)
	assert.Equal(t, DocTypeMarkdown, req.Type)
	assert.Equal(t, "docs/**/*.md", req.PathFilter)
	assert.Equal(t, 50, req.Limit)
	assert.Equal(t, 100, req.Offset)
	assert.Equal(t, 2, req.FuzzyLevel)
	assert.True(t, req.IncludeHighlights)
}

// =============================================================================
// HybridSearchResult Tests
// =============================================================================

func TestHybridSearchResult_HasResults(t *testing.T) {
	tests := []struct {
		name     string
		result   *HybridSearchResult
		expected bool
	}{
		{
			name:     "empty fused results",
			result:   &HybridSearchResult{FusedResults: nil},
			expected: false,
		},
		{
			name:     "empty slice fused results",
			result:   &HybridSearchResult{FusedResults: []ScoredDocument{}},
			expected: false,
		},
		{
			name: "with fused results",
			result: &HybridSearchResult{
				FusedResults: []ScoredDocument{newTestScoredDocument("A", 0.9)},
			},
			expected: true,
		},
		{
			name: "bleve only (no fused)",
			result: &HybridSearchResult{
				BleveResults: []ScoredDocument{newTestScoredDocument("A", 0.9)},
				FusedResults: nil,
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.result.HasResults())
		})
	}
}

func TestHybridSearchResult_TopN(t *testing.T) {
	fused := []ScoredDocument{
		newTestScoredDocument("A", 0.9),
		newTestScoredDocument("B", 0.8),
		newTestScoredDocument("C", 0.7),
	}

	tests := []struct {
		name        string
		n           int
		expectedLen int
	}{
		{
			name:        "n is zero",
			n:           0,
			expectedLen: 0,
		},
		{
			name:        "n is negative",
			n:           -1,
			expectedLen: 0,
		},
		{
			name:        "n less than total",
			n:           2,
			expectedLen: 2,
		},
		{
			name:        "n equals total",
			n:           3,
			expectedLen: 3,
		},
		{
			name:        "n greater than total",
			n:           10,
			expectedLen: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := &HybridSearchResult{FusedResults: fused}
			top := result.TopN(tc.n)
			assert.Len(t, top, tc.expectedLen)
		})
	}
}

func TestHybridSearchResult_FilterByScore(t *testing.T) {
	fused := []ScoredDocument{
		newTestScoredDocument("A", 0.9),
		newTestScoredDocument("B", 0.7),
		newTestScoredDocument("C", 0.5),
		newTestScoredDocument("D", 0.3),
	}

	tests := []struct {
		name        string
		minScore    float64
		expectedLen int
	}{
		{
			name:        "filter all",
			minScore:    1.0,
			expectedLen: 0,
		},
		{
			name:        "filter none",
			minScore:    0.0,
			expectedLen: 4,
		},
		{
			name:        "filter some",
			minScore:    0.6,
			expectedLen: 2, // 0.9 and 0.7
		},
		{
			name:        "exact match",
			minScore:    0.5,
			expectedLen: 3, // 0.9, 0.7, 0.5
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := &HybridSearchResult{FusedResults: fused}
			filtered := result.FilterByScore(tc.minScore)
			assert.Len(t, filtered, tc.expectedLen)
		})
	}
}

func TestHybridSearchResult_TotalSources(t *testing.T) {
	tests := []struct {
		name     string
		bleve    []ScoredDocument
		vector   []ScoredDocument
		expected int
	}{
		{
			name:     "no sources",
			bleve:    nil,
			vector:   nil,
			expected: 0,
		},
		{
			name:     "bleve only",
			bleve:    []ScoredDocument{newTestScoredDocument("A", 0.9)},
			vector:   nil,
			expected: 1,
		},
		{
			name:     "vector only",
			bleve:    nil,
			vector:   []ScoredDocument{newTestScoredDocument("A", 0.9)},
			expected: 1,
		},
		{
			name:     "both sources",
			bleve:    []ScoredDocument{newTestScoredDocument("A", 0.9)},
			vector:   []ScoredDocument{newTestScoredDocument("B", 0.8)},
			expected: 2,
		},
		{
			name:     "empty slices count as no source",
			bleve:    []ScoredDocument{},
			vector:   []ScoredDocument{},
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := &HybridSearchResult{
				BleveResults:  tc.bleve,
				VectorResults: tc.vector,
			}
			assert.Equal(t, tc.expected, result.TotalSources())
		})
	}
}

func TestHybridSearchResult_FusionMethodStored(t *testing.T) {
	tests := []struct {
		method FusionMethod
	}{
		{FusionRRF},
		{FusionLinear},
		{FusionMax},
	}

	for _, tc := range tests {
		t.Run(string(tc.method), func(t *testing.T) {
			result := &HybridSearchResult{
				FusionMethod: tc.method,
			}
			assert.Equal(t, tc.method, result.FusionMethod)
		})
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestSearchResult_VeryHighScores(t *testing.T) {
	docs := []ScoredDocument{
		newTestScoredDocument("A", 1000000.0),
		newTestScoredDocument("B", 999999.0),
	}
	result := &SearchResult{Documents: docs}

	filtered := result.FilterByScore(999999.5)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "A", filtered[0].ID)
}

func TestSearchResult_VerySmallScores(t *testing.T) {
	docs := []ScoredDocument{
		newTestScoredDocument("A", 0.0000001),
		newTestScoredDocument("B", 0.00000001),
	}
	result := &SearchResult{Documents: docs}

	filtered := result.FilterByScore(0.00000005)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "A", filtered[0].ID)
}

func TestSearchRequest_LargePagination(t *testing.T) {
	req := &SearchRequest{
		Query:  "test",
		Limit:  MaxLimit,
		Offset: 1000000,
	}
	err := req.Validate()
	assert.NoError(t, err)
}

func TestSearchResult_EmptyHighlights(t *testing.T) {
	doc := ScoredDocument{
		Document:   Document{ID: "test"},
		Score:      0.5,
		Highlights: nil,
	}
	assert.Nil(t, doc.Highlights)

	doc.Highlights = map[string][]string{}
	assert.Empty(t, doc.Highlights)
}

func TestSearchResult_EmptyMatchedFields(t *testing.T) {
	doc := ScoredDocument{
		Document:      Document{ID: "test"},
		Score:         0.5,
		MatchedFields: nil,
	}
	assert.Nil(t, doc.MatchedFields)

	doc.MatchedFields = []string{}
	assert.Empty(t, doc.MatchedFields)
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestSearchRequestConstants(t *testing.T) {
	assert.Equal(t, 20, DefaultLimit)
	assert.Equal(t, 100, MaxLimit)
	assert.Equal(t, 0, MinFuzzy)
	assert.Equal(t, 2, MaxFuzzy)
}

func TestErrorValues(t *testing.T) {
	assert.NotNil(t, ErrEmptyQuery)
	assert.NotNil(t, ErrLimitTooHigh)
	assert.NotNil(t, ErrLimitNegative)
	assert.NotNil(t, ErrOffsetNegative)
	assert.NotNil(t, ErrFuzzyInvalid)

	// Ensure error messages are distinct
	errors := []error{
		ErrEmptyQuery,
		ErrLimitTooHigh,
		ErrLimitNegative,
		ErrOffsetNegative,
		ErrFuzzyInvalid,
	}

	messages := make(map[string]bool)
	for _, err := range errors {
		msg := err.Error()
		assert.False(t, messages[msg], "duplicate error message: %s", msg)
		messages[msg] = true
	}
}
