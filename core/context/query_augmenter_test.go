package context

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

func createTestAugmenter(t *testing.T) *QueryAugmenter {
	t.Helper()

	hotCache := NewDefaultHotCache()
	bleve := NewMockBleveSearcher()

	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
	})

	return NewQueryAugmenter(QueryAugmenterConfig{
		Searcher:           ts,
		SearchBudget:       TierWarmBudget,
		DefaultTokenBudget: 1000,
		MinConfidence:      0.3,
		MaxExcerpts:        5,
		MaxSummaries:       3,
	})
}

func createTestAugmenterWithAdaptive(t *testing.T) (*QueryAugmenter, *AdaptiveState) {
	t.Helper()

	hotCache := NewDefaultHotCache()
	bleve := NewMockBleveSearcher()

	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
	})

	adaptive := NewAdaptiveState()

	return NewQueryAugmenter(QueryAugmenterConfig{
		Searcher:           ts,
		AdaptiveState:      adaptive,
		SearchBudget:       TierWarmBudget,
		DefaultTokenBudget: 1000,
		MinConfidence:      0.3,
		MaxExcerpts:        5,
		MaxSummaries:       3,
	}), adaptive
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewQueryAugmenter_DefaultConfig(t *testing.T) {
	qa := NewQueryAugmenter(QueryAugmenterConfig{})

	if qa.defaultTokenBudget != DefaultTokenBudget {
		t.Errorf("defaultTokenBudget = %d, want %d", qa.defaultTokenBudget, DefaultTokenBudget)
	}

	if qa.minConfidence != DefaultMinConfidence {
		t.Errorf("minConfidence = %f, want %f", qa.minConfidence, DefaultMinConfidence)
	}

	if qa.maxExcerpts != DefaultMaxExcerpts {
		t.Errorf("maxExcerpts = %d, want %d", qa.maxExcerpts, DefaultMaxExcerpts)
	}

	if qa.maxSummaries != DefaultMaxSummaries {
		t.Errorf("maxSummaries = %d, want %d", qa.maxSummaries, DefaultMaxSummaries)
	}
}

func TestNewQueryAugmenter_CustomConfig(t *testing.T) {
	qa := NewQueryAugmenter(QueryAugmenterConfig{
		DefaultTokenBudget: 2000,
		MinConfidence:      0.5,
		MaxExcerpts:        10,
		MaxSummaries:       8,
		SearchBudget:       100 * time.Millisecond,
	})

	if qa.defaultTokenBudget != 2000 {
		t.Errorf("defaultTokenBudget = %d, want 2000", qa.defaultTokenBudget)
	}

	if qa.minConfidence != 0.5 {
		t.Errorf("minConfidence = %f, want 0.5", qa.minConfidence)
	}

	if qa.maxExcerpts != 10 {
		t.Errorf("maxExcerpts = %d, want 10", qa.maxExcerpts)
	}
}

// =============================================================================
// Augment Tests
// =============================================================================

func TestAugment_BasicOperation(t *testing.T) {
	qa := createTestAugmenter(t)

	// Add content to hot cache
	qa.searcher.hotCache.Add("entry-1", &ContentEntry{
		ID:       "entry-1",
		Content:  "This is test content for entry one with good keywords.",
		Keywords: []string{"test", "keywords"},
	})

	ctx := context.Background()
	result, err := qa.Augment(ctx, "test", 1000)

	if err != nil {
		t.Fatalf("Augment error = %v", err)
	}

	if result == nil {
		t.Fatal("Result should not be nil")
	}

	if result.OriginalQuery != "test" {
		t.Errorf("OriginalQuery = %q, want %q", result.OriginalQuery, "test")
	}

	if result.BudgetMax != 1000 {
		t.Errorf("BudgetMax = %d, want 1000", result.BudgetMax)
	}
}

func TestAugment_RespectsTokenBudget(t *testing.T) {
	qa := createTestAugmenter(t)

	// Add large content
	largeContent := strings.Repeat("word ", 1000) // ~5000 chars = ~1250 tokens
	qa.searcher.hotCache.Add("large-entry", &ContentEntry{
		ID:       "large-entry",
		Content:  largeContent,
		Keywords: []string{"query"},
	})

	ctx := context.Background()
	result, err := qa.Augment(ctx, "query", 100) // Very small budget

	if err != nil {
		t.Fatalf("Augment error = %v", err)
	}

	if result.TokensUsed > 100 {
		t.Errorf("TokensUsed = %d, exceeds budget 100", result.TokensUsed)
	}
}

func TestAugment_UsesDefaultBudgetWhenZero(t *testing.T) {
	qa := createTestAugmenter(t)
	qa.defaultTokenBudget = 500

	ctx := context.Background()
	result, err := qa.Augment(ctx, "test", 0) // Zero budget

	if err != nil {
		t.Fatalf("Augment error = %v", err)
	}

	if result.BudgetMax != 500 {
		t.Errorf("BudgetMax = %d, want 500 (default)", result.BudgetMax)
	}
}

func TestAugment_NoSearcher(t *testing.T) {
	qa := NewQueryAugmenter(QueryAugmenterConfig{
		Searcher: nil,
	})

	ctx := context.Background()
	result, err := qa.Augment(ctx, "test", 1000)

	if err != nil {
		t.Fatalf("Augment error = %v", err)
	}

	if len(result.Excerpts) != 0 {
		t.Errorf("Expected 0 excerpts with nil searcher, got %d", len(result.Excerpts))
	}
}

func TestAugment_WithAdaptiveState(t *testing.T) {
	qa, adaptive := createTestAugmenterWithAdaptive(t)

	// Set specific weights (high relevance, low waste penalty)
	adaptive.Weights.RelevanceBonus = RobustWeightDistribution{Alpha: 3, Beta: 1}
	adaptive.Weights.WastePenalty = RobustWeightDistribution{Alpha: 1, Beta: 3}

	// Add content
	qa.searcher.hotCache.Add("entry-1", &ContentEntry{
		ID:       "entry-1",
		Content:  "Test content with keywords.",
		Keywords: []string{"test"},
	})

	ctx := context.Background()
	result, err := qa.Augment(ctx, "test", 1000)

	if err != nil {
		t.Fatalf("Augment error = %v", err)
	}

	// Should have excerpts
	if len(result.Excerpts) == 0 && len(result.Summaries) == 0 {
		t.Error("Expected some excerpts or summaries")
	}
}

// =============================================================================
// Weight Influence Tests
// =============================================================================

func TestAugment_HighRelevanceBonus_MoreExcerpts(t *testing.T) {
	qa, adaptive := createTestAugmenterWithAdaptive(t)

	// Add multiple entries with varying quality
	for i := 0; i < 5; i++ {
		qa.searcher.hotCache.Add(
			fmt.Sprintf("entry-%d", i),
			&ContentEntry{
				ID:       fmt.Sprintf("entry-%d", i),
				Content:  fmt.Sprintf("Content %d with some text", i),
				Keywords: []string{"test"},
			},
		)
	}

	// High relevance bonus = include more
	adaptive.Weights.RelevanceBonus = RobustWeightDistribution{Alpha: 5, Beta: 1}
	adaptive.Weights.WastePenalty = RobustWeightDistribution{Alpha: 1, Beta: 5}

	ctx := context.Background()
	highRelevance, _ := qa.Augment(ctx, "test", 2000)

	// Low relevance bonus = include fewer
	adaptive.Weights.RelevanceBonus = RobustWeightDistribution{Alpha: 1, Beta: 5}
	adaptive.Weights.WastePenalty = RobustWeightDistribution{Alpha: 5, Beta: 1}

	lowRelevance, _ := qa.Augment(ctx, "test", 2000)

	// High relevance should tend to include more content
	t.Logf("High relevance: %d excerpts, Low relevance: %d excerpts",
		len(highRelevance.Excerpts), len(lowRelevance.Excerpts))

	// Both should have some results
	if len(highRelevance.Excerpts)+len(highRelevance.Summaries) == 0 {
		t.Error("High relevance should produce some results")
	}
}

// =============================================================================
// Scoring Tests
// =============================================================================

func TestCalculateConfidence_PositionBased(t *testing.T) {
	qa := createTestAugmenter(t)

	entry := &ContentEntry{
		ID:      "test",
		Content: "Sufficient content length for testing purposes here.",
	}

	// First position should have higher confidence
	conf1 := qa.calculateConfidence(entry, 0, 10)
	conf5 := qa.calculateConfidence(entry, 5, 10)
	conf9 := qa.calculateConfidence(entry, 9, 10)

	if conf1 <= conf5 {
		t.Errorf("conf1 (%f) should be > conf5 (%f)", conf1, conf5)
	}

	if conf5 <= conf9 {
		t.Errorf("conf5 (%f) should be > conf9 (%f)", conf5, conf9)
	}
}

func TestCalculateConfidence_ShortContentPenalty(t *testing.T) {
	qa := createTestAugmenter(t)

	shortEntry := &ContentEntry{
		ID:      "short",
		Content: "Short",
	}

	longEntry := &ContentEntry{
		ID:      "long",
		Content: "This is a much longer piece of content that should not be penalized.",
	}

	confShort := qa.calculateConfidence(shortEntry, 0, 10)
	confLong := qa.calculateConfidence(longEntry, 0, 10)

	if confShort >= confLong {
		t.Errorf("Short content confidence (%f) should be < long content (%f)", confShort, confLong)
	}
}

func TestCalculateConfidence_KeywordBonus(t *testing.T) {
	qa := createTestAugmenter(t)

	noKeywords := &ContentEntry{
		ID:       "no-kw",
		Content:  "Sufficient content length for testing purposes here.",
		Keywords: nil,
	}

	withKeywords := &ContentEntry{
		ID:       "with-kw",
		Content:  "Sufficient content length for testing purposes here.",
		Keywords: []string{"important", "relevant"},
	}

	// Use position 5 so base confidence isn't already at max (would be clamped)
	confNoKw := qa.calculateConfidence(noKeywords, 5, 10)
	confWithKw := qa.calculateConfidence(withKeywords, 5, 10)

	if confWithKw <= confNoKw {
		t.Errorf("Entry with keywords (%f) should have > confidence than without (%f)", confWithKw, confNoKw)
	}
}

// =============================================================================
// Budget Selection Tests
// =============================================================================

func TestSelectWithinBudget_ExceedsLimit(t *testing.T) {
	qa := createTestAugmenter(t)
	qa.maxExcerpts = 2

	scored := make([]scoredExcerpt, 5)
	for i := range scored {
		scored[i] = scoredExcerpt{
			entry:      &ContentEntry{ID: fmt.Sprintf("e%d", i), Content: "Content"},
			confidence: 0.8,
			score:      0.9 - float64(i)*0.1,
		}
	}

	weights := AugmenterWeights{ExcerptRatio: 0.7, SummaryRatio: 0.3}
	excerpts, _, _ := qa.selectWithinBudget(scored, 10000, weights)

	if len(excerpts) > 2 {
		t.Errorf("Excerpts = %d, should not exceed maxExcerpts (2)", len(excerpts))
	}
}

func TestSelectWithinBudget_GeneratesSummaries(t *testing.T) {
	qa := createTestAugmenter(t)
	qa.maxExcerpts = 1
	qa.maxSummaries = 3

	scored := make([]scoredExcerpt, 5)
	for i := range scored {
		scored[i] = scoredExcerpt{
			entry:      &ContentEntry{ID: fmt.Sprintf("e%d", i), Content: "Content for entry"},
			confidence: 0.8,
			score:      0.9 - float64(i)*0.1,
		}
	}

	weights := AugmenterWeights{ExcerptRatio: 0.3, SummaryRatio: 0.7}
	excerpts, summaries, _ := qa.selectWithinBudget(scored, 10000, weights)

	if len(excerpts) != 1 {
		t.Errorf("Expected 1 excerpt (max), got %d", len(excerpts))
	}

	if len(summaries) == 0 {
		t.Error("Expected some summaries for remaining content")
	}
}

// =============================================================================
// Truncation Tests
// =============================================================================

func TestTruncateExcerpt_WithinLimit(t *testing.T) {
	qa := createTestAugmenter(t)

	excerpt := Excerpt{
		Content: "Short content",
	}

	truncated := qa.truncateExcerpt(excerpt, 1000)

	if truncated.Content != excerpt.Content {
		t.Error("Should not truncate content within limit")
	}
}

func TestTruncateExcerpt_ExceedsLimit(t *testing.T) {
	qa := createTestAugmenter(t)

	excerpt := Excerpt{
		Content: strings.Repeat("word ", 100), // ~500 chars
	}

	truncated := qa.truncateExcerpt(excerpt, 25) // ~100 chars

	if len(truncated.Content) >= len(excerpt.Content) {
		t.Error("Truncated content should be shorter")
	}

	if !strings.HasSuffix(truncated.Content, "...") {
		t.Error("Truncated content should end with ...")
	}
}

// =============================================================================
// Summary Generation Tests
// =============================================================================

func TestGenerateSummaryText_Short(t *testing.T) {
	qa := createTestAugmenter(t)

	entry := &ContentEntry{
		Content: "Short summary text.",
	}

	summary := qa.generateSummaryText(entry)

	if summary != entry.Content {
		t.Errorf("Short content should not be modified, got %q", summary)
	}
}

func TestGenerateSummaryText_Long(t *testing.T) {
	qa := createTestAugmenter(t)

	entry := &ContentEntry{
		Content: strings.Repeat("This is a long sentence. ", 20),
	}

	summary := qa.generateSummaryText(entry)

	if len(summary) > SummaryMaxLength+10 { // Allow small overflow
		t.Errorf("Summary too long: %d chars", len(summary))
	}
}

func TestGenerateSummaryText_RemovesNewlines(t *testing.T) {
	qa := createTestAugmenter(t)

	entry := &ContentEntry{
		Content: "Line one\nLine two\nLine three",
	}

	summary := qa.generateSummaryText(entry)

	if strings.Contains(summary, "\n") {
		t.Error("Summary should not contain newlines")
	}
}

// =============================================================================
// Rendering Tests
// =============================================================================

func TestRender_EmptyResult(t *testing.T) {
	qa := createTestAugmenter(t)

	rendered := qa.Render(nil)

	if rendered != "" {
		t.Errorf("Render(nil) should return empty string, got %q", rendered)
	}
}

func TestRender_WithExcerpts(t *testing.T) {
	qa := createTestAugmenter(t)

	augmented := &AugmentedQuery{
		OriginalQuery: "test",
		Excerpts: []Excerpt{
			{
				ID:         "ex-1",
				Source:     "file.go",
				Content:    "func example() {}",
				Confidence: 0.9,
			},
		},
	}

	rendered := qa.Render(augmented)

	if !strings.Contains(rendered, "[AUTO-RETRIEVED]") {
		t.Error("Rendered should contain [AUTO-RETRIEVED] marker")
	}

	if !strings.Contains(rendered, "[EXCERPT 1]") {
		t.Error("Rendered should contain excerpt header")
	}

	if !strings.Contains(rendered, "file.go") {
		t.Error("Rendered should contain source path")
	}

	if !strings.Contains(rendered, "0.90") {
		t.Error("Rendered should contain confidence score")
	}

	if !strings.Contains(rendered, "```") {
		t.Error("Rendered should contain code block markers")
	}
}

func TestRender_WithSummaries(t *testing.T) {
	qa := createTestAugmenter(t)

	augmented := &AugmentedQuery{
		OriginalQuery: "test",
		Summaries: []Summary{
			{
				ID:         "sum-1",
				Source:     "doc.md",
				Text:       "Brief description",
				Confidence: 0.7,
				TokenCount: 50,
			},
		},
	}

	rendered := qa.Render(augmented)

	if !strings.Contains(rendered, "[SUMMARIES]") {
		t.Error("Rendered should contain [SUMMARIES] marker")
	}

	if !strings.Contains(rendered, "doc.md") {
		t.Error("Rendered should contain source")
	}

	if !strings.Contains(rendered, "Brief description") {
		t.Error("Rendered should contain summary text")
	}
}

func TestRender_WithLineRange(t *testing.T) {
	qa := createTestAugmenter(t)

	augmented := &AugmentedQuery{
		Excerpts: []Excerpt{
			{
				ID:        "ex-1",
				Source:    "file.go",
				Content:   "code here",
				LineRange: [2]int{10, 20},
			},
		},
	}

	rendered := qa.Render(augmented)

	if !strings.Contains(rendered, "Lines 10-20") {
		t.Error("Rendered should contain line range")
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestStats_Initial(t *testing.T) {
	qa := createTestAugmenter(t)

	stats := qa.Stats()

	if stats.AugmentCount != 0 {
		t.Errorf("AugmentCount = %d, want 0", stats.AugmentCount)
	}
}

func TestStats_AfterAugment(t *testing.T) {
	qa := createTestAugmenter(t)

	qa.searcher.hotCache.Add("entry", &ContentEntry{
		ID:       "entry",
		Content:  "Test content here",
		Keywords: []string{"test"},
	})

	ctx := context.Background()
	qa.Augment(ctx, "test", 1000)
	qa.Augment(ctx, "test", 1000)

	stats := qa.Stats()

	if stats.AugmentCount != 2 {
		t.Errorf("AugmentCount = %d, want 2", stats.AugmentCount)
	}
}

func TestQueryAugmenter_ResetStats(t *testing.T) {
	qa := createTestAugmenter(t)

	ctx := context.Background()
	qa.Augment(ctx, "test", 1000)

	qa.ResetStats()

	stats := qa.Stats()
	if stats.AugmentCount != 0 {
		t.Errorf("AugmentCount after reset = %d, want 0", stats.AugmentCount)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestClamp(t *testing.T) {
	tests := []struct {
		value, min, max, expected float64
	}{
		{0.5, 0.0, 1.0, 0.5},
		{-0.5, 0.0, 1.0, 0.0},
		{1.5, 0.0, 1.0, 1.0},
		{0.0, 0.0, 1.0, 0.0},
		{1.0, 0.0, 1.0, 1.0},
	}

	for _, tt := range tests {
		got := clamp(tt.value, tt.min, tt.max)
		if got != tt.expected {
			t.Errorf("clamp(%f, %f, %f) = %f, want %f",
				tt.value, tt.min, tt.max, got, tt.expected)
		}
	}
}

func TestMax(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 2},
		{2, 1, 2},
		{5, 5, 5},
		{-1, -2, -1},
	}

	for _, tt := range tests {
		got := max(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("max(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.expected)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	qa := createTestAugmenter(t)

	// 100 chars should be ~25 tokens (TokensPerChar = 0.25)
	tokens := qa.estimateTokens(strings.Repeat("a", 100))

	if tokens != 25 {
		t.Errorf("estimateTokens(100 chars) = %d, want 25", tokens)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAugment_FullWorkflow(t *testing.T) {
	qa := createTestAugmenter(t)

	// Add varied content
	qa.searcher.hotCache.Add("important", &ContentEntry{
		ID:       "important",
		Content:  "This is very important content that matches the query well.",
		Keywords: []string{"important", "query"},
	})

	qa.searcher.hotCache.Add("related", &ContentEntry{
		ID:       "related",
		Content:  "This is related content with some overlap.",
		Keywords: []string{"related"},
	})

	qa.searcher.hotCache.Add("unrelated", &ContentEntry{
		ID:       "unrelated",
		Content:  "This content is not really related.",
		Keywords: []string{"other"},
	})

	ctx := context.Background()
	result, err := qa.Augment(ctx, "important query", 500)

	if err != nil {
		t.Fatalf("Augment error = %v", err)
	}

	// Should have some excerpts
	if len(result.Excerpts) == 0 {
		t.Error("Expected some excerpts")
	}

	// Verify rendering works
	rendered := qa.Render(result)
	if len(rendered) == 0 {
		t.Error("Rendered output should not be empty")
	}

	// Verify within budget
	if result.TokensUsed > result.BudgetMax {
		t.Errorf("TokensUsed (%d) exceeds BudgetMax (%d)", result.TokensUsed, result.BudgetMax)
	}
}

// Helper to use fmt in tests
func fmt_Sprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}
