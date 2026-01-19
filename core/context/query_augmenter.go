// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.5.4: QueryAugmenter - transforms search results into
// prompt-injectable context with weight-based prioritization and token budget management.
package context

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// DefaultTokenBudget is the default maximum tokens for augmentation.
	DefaultTokenBudget = 4000

	// DefaultMinConfidence is the minimum confidence threshold for excerpts.
	DefaultMinConfidence = 0.3

	// DefaultMaxExcerpts is the default maximum number of excerpts.
	DefaultMaxExcerpts = 10

	// DefaultMaxSummaries is the default maximum number of summaries.
	DefaultMaxSummaries = 5

	// TokensPerChar is the approximate token to character ratio.
	TokensPerChar = 0.25

	// SummaryMaxLength is the maximum length of a summary in characters.
	SummaryMaxLength = 200
)

// =============================================================================
// QueryAugmenter Configuration
// =============================================================================

// QueryAugmenterConfig holds configuration for the QueryAugmenter.
type QueryAugmenterConfig struct {
	// Searcher is the tiered searcher for search execution.
	Searcher *TieredSearcher

	// AdaptiveState provides sampled weights for excerpt selection.
	AdaptiveState *AdaptiveState

	// SearchBudget is the latency budget for search operations.
	SearchBudget time.Duration

	// DefaultTokenBudget is the default token budget.
	DefaultTokenBudget int

	// MinConfidence is the minimum confidence for including excerpts.
	MinConfidence float64

	// MaxExcerpts is the maximum number of excerpts to include.
	MaxExcerpts int

	// MaxSummaries is the maximum number of summaries to include.
	MaxSummaries int
}

// =============================================================================
// QueryAugmenter
// =============================================================================

// QueryAugmenter transforms search results into prompt-injectable context.
// It applies learned weights to prioritize excerpt selection and respects
// token budgets for efficient context injection.
type QueryAugmenter struct {
	searcher      *TieredSearcher
	adaptiveState *AdaptiveState

	searchBudget       time.Duration
	defaultTokenBudget int
	minConfidence      float64
	maxExcerpts        int
	maxSummaries       int

	// stats
	mu            sync.RWMutex
	augmentCount  int64
	totalExcerpts int64
	totalTokens   int64
}

// NewQueryAugmenter creates a new query augmenter with the given configuration.
func NewQueryAugmenter(config QueryAugmenterConfig) *QueryAugmenter {
	searchBudget := config.SearchBudget
	if searchBudget <= 0 {
		searchBudget = TierFullBudget
	}

	defaultTokenBudget := config.DefaultTokenBudget
	if defaultTokenBudget <= 0 {
		defaultTokenBudget = DefaultTokenBudget
	}

	minConfidence := config.MinConfidence
	if minConfidence <= 0 {
		minConfidence = DefaultMinConfidence
	}

	maxExcerpts := config.MaxExcerpts
	if maxExcerpts <= 0 {
		maxExcerpts = DefaultMaxExcerpts
	}

	maxSummaries := config.MaxSummaries
	if maxSummaries <= 0 {
		maxSummaries = DefaultMaxSummaries
	}

	return &QueryAugmenter{
		searcher:           config.Searcher,
		adaptiveState:      config.AdaptiveState,
		searchBudget:       searchBudget,
		defaultTokenBudget: defaultTokenBudget,
		minConfidence:      minConfidence,
		maxExcerpts:        maxExcerpts,
		maxSummaries:       maxSummaries,
	}
}

// =============================================================================
// Core Operations
// =============================================================================

// Augment performs synchronous search and transforms results into an AugmentedQuery.
// It respects the given token budget and uses adaptive weights for prioritization.
func (qa *QueryAugmenter) Augment(
	ctx context.Context,
	query string,
	tokenBudget int,
) (*AugmentedQuery, error) {
	start := time.Now()

	if tokenBudget <= 0 {
		tokenBudget = qa.defaultTokenBudget
	}

	// Execute search
	searchResult := qa.executeSearch(ctx, query)

	// Get sampled weights if available
	weights := qa.getSampledWeights()

	// Convert to scored excerpts
	scoredExcerpts := qa.scoreExcerpts(searchResult, weights)

	// Select excerpts within budget
	excerpts, summaries, tokensUsed := qa.selectWithinBudget(scoredExcerpts, tokenBudget, weights)

	qa.recordStats(len(excerpts), tokensUsed)

	return &AugmentedQuery{
		OriginalQuery:    query,
		Excerpts:         excerpts,
		Summaries:        summaries,
		TokensUsed:       tokensUsed,
		BudgetMax:        tokenBudget,
		TierSource:       searchResult.Tier,
		PrefetchDuration: time.Since(start),
	}, nil
}

func (qa *QueryAugmenter) executeSearch(ctx context.Context, query string) *TieredSearchResult {
	if qa.searcher == nil {
		return &TieredSearchResult{
			Results: make([]*ContentEntry, 0),
		}
	}
	return qa.searcher.SearchWithBudget(ctx, query, qa.searchBudget)
}

// AugmenterWeights holds sampled weights for the query augmenter.
// This extends RewardWeights with augmenter-specific parameters.
type AugmenterWeights struct {
	RelevanceBonus      float64
	WastePenalty        float64
	ConfidenceThreshold float64
	ExcerptRatio        float64
	SummaryRatio        float64
}

func (qa *QueryAugmenter) getSampledWeights() AugmenterWeights {
	if qa.adaptiveState == nil {
		return AugmenterWeights{
			RelevanceBonus:      0.5,
			WastePenalty:        0.5,
			ConfidenceThreshold: qa.minConfidence,
			ExcerptRatio:        0.7,
			SummaryRatio:        0.3,
		}
	}

	// Sample from adaptive state with empty context (will use global weights)
	rewardWeights := qa.adaptiveState.SampleWeights(TaskContext(""))

	return AugmenterWeights{
		RelevanceBonus:      rewardWeights.RelevanceBonus,
		WastePenalty:        rewardWeights.WastePenalty,
		ConfidenceThreshold: qa.minConfidence,
		ExcerptRatio:        0.7,
		SummaryRatio:        0.3,
	}
}

// =============================================================================
// Scoring
// =============================================================================

// scoredExcerpt holds an excerpt with its computed score.
type scoredExcerpt struct {
	entry      *ContentEntry
	confidence float64
	score      float64
}

func (qa *QueryAugmenter) scoreExcerpts(
	result *TieredSearchResult,
	weights AugmenterWeights,
) []scoredExcerpt {
	if result == nil || len(result.Results) == 0 {
		return nil
	}

	scored := make([]scoredExcerpt, 0, len(result.Results))

	for i, entry := range result.Results {
		if entry == nil {
			continue
		}

		// Calculate confidence based on position and content
		confidence := qa.calculateConfidence(entry, i, len(result.Results))

		// Skip low confidence results based on threshold
		if confidence < weights.ConfidenceThreshold {
			continue
		}

		// Calculate score using weights
		score := qa.calculateScore(confidence, weights)

		scored = append(scored, scoredExcerpt{
			entry:      entry,
			confidence: confidence,
			score:      score,
		})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	return scored
}

func (qa *QueryAugmenter) calculateConfidence(
	entry *ContentEntry,
	position int,
	total int,
) float64 {
	// Base confidence from position (earlier = higher), max 0.9 to leave room for bonuses
	positionFactor := 0.9 * (1.0 - (float64(position) / float64(total+1)))

	// Content quality factors (additive bonuses)
	bonus := 0.0
	if len(entry.Content) >= 50 {
		bonus += 0.05 // Bonus for sufficient content length
	}
	if len(entry.Keywords) > 0 {
		bonus += 0.05 // Bonus for keyword-rich content
	}

	return clamp(positionFactor+bonus, 0.0, 1.0)
}

func (qa *QueryAugmenter) calculateScore(
	confidence float64,
	weights AugmenterWeights,
) float64 {
	// Higher RelevanceBonus → favor including more excerpts
	// Higher WastePenalty → favor only high-confidence excerpts
	relevanceFactor := weights.RelevanceBonus
	wasteFactor := weights.WastePenalty

	// Score formula: confidence weighted by relevance, penalized by waste sensitivity
	// More waste penalty → need higher confidence to achieve same score
	score := confidence * (1.0 + relevanceFactor) * (1.0 - wasteFactor*0.5)

	return score
}

// =============================================================================
// Budget Management
// =============================================================================

func (qa *QueryAugmenter) selectWithinBudget(
	scored []scoredExcerpt,
	tokenBudget int,
	weights AugmenterWeights,
) ([]Excerpt, []Summary, int) {
	excerpts := make([]Excerpt, 0)
	summaries := make([]Summary, 0)
	tokensUsed := 0

	// Calculate budget allocation based on weights
	excerptBudget := int(float64(tokenBudget) * weights.ExcerptRatio)
	_ = tokenBudget - excerptBudget // summaryBudget for future expansion

	// Select excerpts
	for _, se := range scored {
		if len(excerpts) >= qa.maxExcerpts {
			break
		}

		excerpt := qa.entryToExcerpt(se)
		excerptTokens := qa.estimateTokens(excerpt.Content)

		if tokensUsed+excerptTokens > excerptBudget {
			// Try to truncate to fit
			remaining := excerptBudget - tokensUsed
			if remaining > 100 { // Minimum useful size
				excerpt = qa.truncateExcerpt(excerpt, remaining)
				excerptTokens = qa.estimateTokens(excerpt.Content)
			} else {
				// Convert to summary instead
				continue
			}
		}

		excerpts = append(excerpts, excerpt)
		tokensUsed += excerptTokens
	}

	// Generate summaries for remaining high-value content
	summaryStartIdx := len(excerpts)
	for i := summaryStartIdx; i < len(scored) && len(summaries) < qa.maxSummaries; i++ {
		se := scored[i]
		summary := qa.entryToSummary(se)
		summaryTokens := qa.estimateTokens(summary.Text)

		if tokensUsed+summaryTokens > tokenBudget {
			break
		}

		summaries = append(summaries, summary)
		tokensUsed += summaryTokens
	}

	return excerpts, summaries, tokensUsed
}

func (qa *QueryAugmenter) entryToExcerpt(se scoredExcerpt) Excerpt {
	return Excerpt{
		ID:         se.entry.ID,
		Content:    se.entry.Content,
		Source:     se.entry.ID,
		Confidence: se.confidence,
		TokenCount: qa.estimateTokens(se.entry.Content),
		Relevance:  se.score,
	}
}

func (qa *QueryAugmenter) entryToSummary(se scoredExcerpt) Summary {
	text := qa.generateSummaryText(se.entry)
	return Summary{
		ID:         se.entry.ID,
		Text:       text,
		Source:     se.entry.ID,
		Confidence: se.confidence,
		TokenCount: qa.estimateTokens(se.entry.Content),
	}
}

func (qa *QueryAugmenter) truncateExcerpt(excerpt Excerpt, maxTokens int) Excerpt {
	maxChars := int(float64(maxTokens) / TokensPerChar)
	if maxChars >= len(excerpt.Content) {
		return excerpt
	}

	// Truncate at word boundary
	truncated := excerpt.Content[:maxChars]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxChars/2 {
		truncated = truncated[:lastSpace]
	}
	truncated += "..."

	excerpt.Content = truncated
	excerpt.TokenCount = qa.estimateTokens(truncated)
	return excerpt
}

func (qa *QueryAugmenter) generateSummaryText(entry *ContentEntry) string {
	// Generate a brief summary from the content
	content := entry.Content
	if len(content) > SummaryMaxLength {
		// Find a good truncation point
		truncated := content[:SummaryMaxLength]
		lastPeriod := strings.LastIndex(truncated, ".")
		lastNewline := strings.LastIndex(truncated, "\n")
		cutPoint := max(lastPeriod, lastNewline)
		if cutPoint > SummaryMaxLength/2 {
			truncated = truncated[:cutPoint+1]
		} else {
			truncated = strings.TrimSpace(truncated) + "..."
		}
		content = truncated
	}

	// Format as one-line summary
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.Join(strings.Fields(content), " ")

	return content
}

func (qa *QueryAugmenter) estimateTokens(text string) int {
	return int(float64(len(text)) * TokensPerChar)
}

// =============================================================================
// Rendering
// =============================================================================

// Render produces markdown with [AUTO-RETRIEVED] markers for prompt injection.
func (qa *QueryAugmenter) Render(augmented *AugmentedQuery) string {
	if augmented == nil {
		return ""
	}

	var sb strings.Builder

	// Render excerpts
	if len(augmented.Excerpts) > 0 {
		sb.WriteString("[AUTO-RETRIEVED]\n")
		for i, excerpt := range augmented.Excerpts {
			qa.renderExcerpt(&sb, excerpt, i+1)
		}
	}

	// Render summaries
	if len(augmented.Summaries) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("[SUMMARIES]\n")
		for _, summary := range augmented.Summaries {
			qa.renderSummary(&sb, summary)
		}
	}

	return sb.String()
}

func (qa *QueryAugmenter) renderExcerpt(sb *strings.Builder, excerpt Excerpt, index int) {
	sb.WriteString(fmt.Sprintf("\n### [EXCERPT %d] %s (confidence: %.2f)\n",
		index, excerpt.Source, excerpt.Confidence))

	if excerpt.LineRange[0] > 0 && excerpt.LineRange[1] > 0 {
		sb.WriteString(fmt.Sprintf("Lines %d-%d:\n", excerpt.LineRange[0], excerpt.LineRange[1]))
	}

	sb.WriteString("```\n")
	sb.WriteString(excerpt.Content)
	if !strings.HasSuffix(excerpt.Content, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("```\n")
}

func (qa *QueryAugmenter) renderSummary(sb *strings.Builder, summary Summary) {
	sb.WriteString(fmt.Sprintf("- **%s**: %s (tokens: %d)\n",
		summary.Source, summary.Text, summary.TokenCount))
}

// =============================================================================
// Statistics
// =============================================================================

func (qa *QueryAugmenter) recordStats(excerptCount, tokensUsed int) {
	qa.mu.Lock()
	qa.augmentCount++
	qa.totalExcerpts += int64(excerptCount)
	qa.totalTokens += int64(tokensUsed)
	qa.mu.Unlock()
}

// AugmenterStats holds augmenter statistics.
type AugmenterStats struct {
	AugmentCount  int64
	TotalExcerpts int64
	TotalTokens   int64
	AvgExcerpts   float64
	AvgTokens     float64
}

// Stats returns current augmenter statistics.
func (qa *QueryAugmenter) Stats() AugmenterStats {
	qa.mu.RLock()
	defer qa.mu.RUnlock()

	var avgExcerpts, avgTokens float64
	if qa.augmentCount > 0 {
		avgExcerpts = float64(qa.totalExcerpts) / float64(qa.augmentCount)
		avgTokens = float64(qa.totalTokens) / float64(qa.augmentCount)
	}

	return AugmenterStats{
		AugmentCount:  qa.augmentCount,
		TotalExcerpts: qa.totalExcerpts,
		TotalTokens:   qa.totalTokens,
		AvgExcerpts:   avgExcerpts,
		AvgTokens:     avgTokens,
	}
}

// ResetStats resets statistics counters.
func (qa *QueryAugmenter) ResetStats() {
	qa.mu.Lock()
	qa.augmentCount = 0
	qa.totalExcerpts = 0
	qa.totalTokens = 0
	qa.mu.Unlock()
}

// =============================================================================
// Helper Functions
// =============================================================================

func clamp(value, minVal, maxVal float64) float64 {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
