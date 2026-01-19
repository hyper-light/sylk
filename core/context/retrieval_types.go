// Package context provides lossless context virtualization types for the Sylk
// multi-agent coding application. This file defines retrieval-related types
// used for augmented query responses, search tiering, and budget management.
package context

import (
	"sync/atomic"
	"time"
)

// SearchTier represents the cache/search tier used for retrieval.
// Lower tiers are faster but may have less complete results.
type SearchTier int

const (
	// TierNone indicates no search tier was used.
	TierNone SearchTier = 0
	// TierHotCache is the fastest tier (< 1ms) using in-memory hot content.
	TierHotCache SearchTier = 1
	// TierWarmIndex is a medium tier (< 10ms) using in-memory Bleve subset.
	TierWarmIndex SearchTier = 2
	// TierFullSearch is the slowest tier (< 200ms) using full Bleve + VectorDB.
	TierFullSearch SearchTier = 3
)

// searchTierNames maps SearchTier values to their string representations.
var searchTierNames = map[SearchTier]string{
	TierNone:       "none",
	TierHotCache:   "hot_cache",
	TierWarmIndex:  "warm_index",
	TierFullSearch: "full_search",
}

// String returns a human-readable name for the search tier.
func (t SearchTier) String() string {
	if name, ok := searchTierNames[t]; ok {
		return name
	}
	return "unknown"
}

// Excerpt represents a full content excerpt (Tier A) with high confidence.
// Excerpts contain actual code or content that is highly relevant to the query.
type Excerpt struct {
	// ID is a unique identifier for this excerpt.
	ID string `json:"id"`

	// Content is the actual excerpt text.
	Content string `json:"content"`

	// Source is the file path or document ID.
	Source string `json:"source"`

	// Confidence indicates retrieval confidence (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// TokenCount is the token count for this excerpt.
	TokenCount int `json:"token_count"`

	// LineRange contains [start, end] line numbers.
	LineRange [2]int `json:"line_range"`

	// Relevance is the overall relevance score.
	Relevance float64 `json:"relevance"`
}

// Summary represents a one-line content hint (Tier B) with moderate confidence.
// Summaries provide awareness of related content without including full content.
type Summary struct {
	// ID is a unique identifier for this summary.
	ID string `json:"id"`

	// Text is a one-line description of the content.
	Text string `json:"text"`

	// Source is the file path or document ID.
	Source string `json:"source"`

	// Confidence indicates retrieval confidence (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// TokenCount is the estimated token count for the source content.
	TokenCount int `json:"token_count"`
}

// AugmentedQuery contains the result of query augmentation with pre-fetched
// context. It includes tiered excerpts and summaries that are injected into
// the prompt before the LLM sees the query.
type AugmentedQuery struct {
	// OriginalQuery is the user's input query.
	OriginalQuery string `json:"original_query"`

	// Excerpts contains Tier A: full content excerpts.
	Excerpts []Excerpt `json:"excerpts"`

	// Summaries contains Tier B: one-line hints.
	Summaries []Summary `json:"summaries"`

	// TokensUsed is the total tokens consumed by excerpts and summaries.
	TokensUsed int `json:"tokens_used"`

	// BudgetMax is the maximum token budget allowed.
	BudgetMax int `json:"budget_max"`

	// TierSource indicates which search tier provided results.
	TierSource SearchTier `json:"tier_source"`

	// PrefetchDuration is the time spent prefetching.
	PrefetchDuration time.Duration `json:"prefetch_duration"`
}

// RetrievalResult contains the result of a context retrieval operation.
// It includes the retrieved content entries and metadata about the retrieval.
type RetrievalResult struct {
	Entries     []*ContentEntry `json:"entries"`
	TotalTokens int             `json:"total_tokens"`
	Truncated   bool            `json:"truncated"` // true if results were truncated
	Query       string          `json:"query"`
	Source      string          `json:"source"` // "reference", "search", "direct"
}

// RetrievalBudget tracks and enforces retrieval resource limits per session
// or agent. It provides atomic operations for concurrent budget management.
type RetrievalBudget struct {
	MaxTokensPerQuery int          `json:"max_tokens_per_query"`
	MaxQueriesPerTurn int          `json:"max_queries_per_turn"`
	MaxTotalTokens    int          `json:"max_total_tokens"`
	UsedTokens        atomic.Int64 `json:"-"` // not serialized
	UsedQueries       atomic.Int64 `json:"-"` // not serialized
}

// CanQuery returns true if both token and query budgets allow another query.
func (b *RetrievalBudget) CanQuery() bool {
	tokensOK := b.UsedTokens.Load() < int64(b.MaxTotalTokens)
	queriesOK := b.UsedQueries.Load() < int64(b.MaxQueriesPerTurn)
	return tokensOK && queriesOK
}

// RecordUsage atomically records token usage and increments the query count.
func (b *RetrievalBudget) RecordUsage(tokens int) {
	b.UsedTokens.Add(int64(tokens))
	b.UsedQueries.Add(1)
}

// Note: ContentType and ContentEntry are defined in content_entry.go
// Additional ContentType constants for retrieval:
const (
	// ContentTypeAgentResponse represents agent-generated responses.
	ContentTypeAgentResponse ContentType = "agent_response"
	// ContentTypeAgentMessage represents inter-agent communication.
	ContentTypeAgentMessage ContentType = "agent_message"
	// ContentTypeTestResult represents test execution results.
	ContentTypeTestResult ContentType = "test_result"
	// ContentTypeInspectorFinding represents code inspection findings.
	ContentTypeInspectorFinding ContentType = "inspector_finding"
)
