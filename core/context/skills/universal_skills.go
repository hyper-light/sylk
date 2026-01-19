// Package skills provides AR.8.1: Universal Retrieval Skills - core skills available
// to all knowledge agents for on-demand context retrieval.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// Skill domain for retrieval skills
	RetrievalDomain = "retrieval"

	// Default values
	DefaultMaxTokens  = 2000
	DefaultMaxResults = 10
	DefaultTTLTurns   = 5
)

// =============================================================================
// Retrieval Dependencies
// =============================================================================

// RetrievalDependencies contains dependencies for universal retrieval skills.
type RetrievalDependencies struct {
	// TieredSearcher for query-based retrieval
	Searcher *ctxpkg.TieredSearcher

	// HotCache for promoting content
	HotCache *ctxpkg.HotCache

	// ContentStore for historical search
	ContentStore *ctxpkg.UniversalContentStore

	// ReferenceResolver for ref_id lookups
	ReferenceResolver ReferenceResolver
}

// ReferenceResolver resolves context reference IDs to content.
type ReferenceResolver interface {
	// Resolve returns the content for a reference ID.
	Resolve(ctx context.Context, refID string, maxTokens int) (*ctxpkg.ContentEntry, error)
}

// =============================================================================
// retrieve_context Skill
// =============================================================================

// RetrieveContextInput defines the input for retrieve_context skill.
type RetrieveContextInput struct {
	// Query is a natural language query for what to retrieve (optional)
	Query string `json:"query,omitempty"`

	// RefID is a specific reference ID from CTX-REF marker (optional)
	RefID string `json:"ref_id,omitempty"`

	// MaxTokens is the maximum tokens to retrieve (default: 2000)
	MaxTokens int `json:"max_tokens,omitempty"`
}

// RetrieveContextOutput defines the output for retrieve_context skill.
type RetrieveContextOutput struct {
	// Entries are the retrieved content entries
	Entries []*ctxpkg.ContentEntry `json:"entries"`

	// TotalTokens is the total tokens in results
	TotalTokens int `json:"total_tokens"`

	// Truncated indicates if results were truncated due to max_tokens
	Truncated bool `json:"truncated"`
}

// NewRetrieveContextSkill creates the retrieve_context skill.
func NewRetrieveContextSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("retrieve_context").
		Description("Retrieve evicted context by query, reference ID, or content ID. Use when you see a [CTX-REF:...] marker and need the full content.").
		Domain(RetrievalDomain).
		Keywords("context", "retrieve", "CTX-REF", "expand", "recall").
		Priority(100). // High priority - always loaded
		StringParam("query", "Natural language query for what to retrieve", false).
		StringParam("ref_id", "Specific reference ID to expand (from CTX-REF marker)", false).
		IntParam("max_tokens", "Maximum tokens to retrieve (default: 2000)", false).
		Handler(createRetrieveContextHandler(deps)).
		Build()
}

func createRetrieveContextHandler(deps *RetrievalDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var params RetrieveContextInput
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		return executeRetrieveContext(ctx, params, deps)
	}
}

func executeRetrieveContext(
	ctx context.Context,
	params RetrieveContextInput,
	deps *RetrievalDependencies,
) (*RetrieveContextOutput, error) {
	maxTokens := params.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}

	// Handle ref_id lookup
	if params.RefID != "" {
		return retrieveByRefID(ctx, params.RefID, maxTokens, deps)
	}

	// Handle query-based retrieval
	if params.Query != "" {
		return retrieveByQuery(ctx, params.Query, maxTokens, deps)
	}

	return nil, fmt.Errorf("either query or ref_id required")
}

func retrieveByRefID(
	ctx context.Context,
	refID string,
	maxTokens int,
	deps *RetrievalDependencies,
) (*RetrieveContextOutput, error) {
	if deps.ReferenceResolver == nil {
		return nil, fmt.Errorf("reference resolver not configured")
	}

	entry, err := deps.ReferenceResolver.Resolve(ctx, refID, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ref_id: %w", err)
	}

	if entry == nil {
		return &RetrieveContextOutput{
			Entries:     []*ctxpkg.ContentEntry{},
			TotalTokens: 0,
			Truncated:   false,
		}, nil
	}

	tokenCount := estimateTokens(entry.Content)
	truncated := tokenCount > maxTokens

	if truncated {
		entry.Content = truncateToTokens(entry.Content, maxTokens)
		tokenCount = maxTokens
	}

	return &RetrieveContextOutput{
		Entries:     []*ctxpkg.ContentEntry{entry},
		TotalTokens: tokenCount,
		Truncated:   truncated,
	}, nil
}

func retrieveByQuery(
	ctx context.Context,
	query string,
	maxTokens int,
	deps *RetrievalDependencies,
) (*RetrieveContextOutput, error) {
	if deps.Searcher == nil {
		return nil, fmt.Errorf("searcher not configured")
	}

	results := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)
	return buildRetrieveOutput(results, maxTokens), nil
}

func buildRetrieveOutput(results *ctxpkg.TieredSearchResult, maxTokens int) *RetrieveContextOutput {
	output := &RetrieveContextOutput{
		Entries:     make([]*ctxpkg.ContentEntry, 0),
		TotalTokens: 0,
		Truncated:   false,
	}

	for _, result := range results.Results {
		entry := &ctxpkg.ContentEntry{
			ID:          result.ID,
			Content:     result.Content,
			ContentType: result.ContentType,
			Timestamp:   result.Timestamp,
		}

		tokens := estimateTokens(entry.Content)
		if output.TotalTokens+tokens > maxTokens {
			output.Truncated = true
			remaining := maxTokens - output.TotalTokens
			if remaining > 0 {
				entry.Content = truncateToTokens(entry.Content, remaining)
				output.Entries = append(output.Entries, entry)
				output.TotalTokens = maxTokens
			}
			break
		}

		output.Entries = append(output.Entries, entry)
		output.TotalTokens += tokens
	}

	return output
}

// =============================================================================
// search_history Skill
// =============================================================================

// SearchHistoryInput defines the input for search_history skill.
type SearchHistoryInput struct {
	// Query is the search query (required)
	Query string `json:"query"`

	// ContentTypes filters by content type (optional)
	ContentTypes []string `json:"content_types,omitempty"`

	// SessionIDs filters to specific sessions (optional)
	SessionIDs []string `json:"session_ids,omitempty"`

	// CrossSession enables cross-session search (default: false)
	CrossSession bool `json:"cross_session,omitempty"`

	// TimeRange filters by time range (optional)
	TimeRange *TimeRangeFilter `json:"time_range,omitempty"`

	// MaxResults is the maximum results to return (default: 10)
	MaxResults int `json:"max_results,omitempty"`
}

// TimeRangeFilter defines a time range filter.
type TimeRangeFilter struct {
	From time.Time `json:"from,omitempty"`
	To   time.Time `json:"to,omitempty"`
}

// SearchHistoryOutput defines the output for search_history skill.
type SearchHistoryOutput struct {
	// Entries are the matching content entries
	Entries []*ContentSummary `json:"entries"`

	// TotalMatches is the total number of matches (may exceed returned)
	TotalMatches int `json:"total_matches"`
}

// ContentSummary provides a summary of a content entry.
type ContentSummary struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Summary    string    `json:"summary"`
	SessionID  string    `json:"session_id,omitempty"`
	TurnNumber int       `json:"turn_number,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// NewSearchHistorySkill creates the search_history skill.
func NewSearchHistorySkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("search_history").
		Description("Search all historical context across current and past sessions").
		Domain(RetrievalDomain).
		Keywords("search", "history", "find", "past", "previous").
		Priority(100). // High priority - always loaded
		StringParam("query", "Search query", true).
		ArrayParam("content_types", "Filter by content type (user_prompt, agent_response, code_file, etc.)", "string", false).
		ArrayParam("session_ids", "Limit to specific sessions", "string", false).
		BoolParam("cross_session", "Enable cross-session search (default: false)", false).
		IntParam("max_results", "Maximum results to return (default: 10)", false).
		Handler(createSearchHistoryHandler(deps)).
		Build()
}

func createSearchHistoryHandler(deps *RetrievalDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var params SearchHistoryInput
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if params.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		return executeSearchHistory(ctx, params, deps)
	}
}

func executeSearchHistory(
	_ context.Context,
	params SearchHistoryInput,
	deps *RetrievalDependencies,
) (*SearchHistoryOutput, error) {
	if deps.ContentStore == nil {
		return nil, fmt.Errorf("content store not configured")
	}

	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}

	// Build search filters
	filters := buildSearchFilters(params)

	// Execute search
	results, err := deps.ContentStore.Search(params.Query, filters, maxResults)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	return buildSearchHistoryOutput(results, maxResults), nil
}

func buildSearchFilters(params SearchHistoryInput) *ctxpkg.SearchFilters {
	filters := &ctxpkg.SearchFilters{}

	if len(params.ContentTypes) > 0 {
		// Convert strings to ContentType
		contentTypes := make([]ctxpkg.ContentType, len(params.ContentTypes))
		for i, ct := range params.ContentTypes {
			contentTypes[i] = ctxpkg.ContentType(ct)
		}
		filters.ContentTypes = contentTypes
	}

	if len(params.SessionIDs) > 0 {
		filters.SessionID = params.SessionIDs[0] // Use first for now
	}

	if params.TimeRange != nil {
		if !params.TimeRange.From.IsZero() {
			filters.TimeRange[0] = params.TimeRange.From
		}
		if !params.TimeRange.To.IsZero() {
			filters.TimeRange[1] = params.TimeRange.To
		}
	}

	return filters
}

func buildSearchHistoryOutput(results []*ctxpkg.ContentEntry, maxResults int) *SearchHistoryOutput {
	output := &SearchHistoryOutput{
		Entries:      make([]*ContentSummary, 0, len(results)),
		TotalMatches: len(results),
	}

	limit := maxResults
	if limit > len(results) {
		limit = len(results)
	}

	for i := 0; i < limit; i++ {
		entry := results[i]
		summary := &ContentSummary{
			ID:         entry.ID,
			Type:       string(entry.ContentType),
			Summary:    generateSummary(entry.Content, 200),
			SessionID:  entry.SessionID,
			TurnNumber: entry.TurnNumber,
			Timestamp:  entry.Timestamp,
		}
		output.Entries = append(output.Entries, summary)
	}

	return output
}

// =============================================================================
// promote_to_hot Skill
// =============================================================================

// PromoteToHotInput defines the input for promote_to_hot skill.
type PromoteToHotInput struct {
	// ContentIDs are the IDs of content to promote (required)
	ContentIDs []string `json:"content_ids"`

	// TTLTurns is how many turns the content should stay in hot cache (default: 5)
	TTLTurns int `json:"ttl_turns,omitempty"`
}

// PromoteToHotOutput defines the output for promote_to_hot skill.
type PromoteToHotOutput struct {
	// Promoted is the count of successfully promoted entries
	Promoted int `json:"promoted"`

	// Failed is the count of entries that failed to promote
	Failed int `json:"failed"`

	// Errors lists any errors that occurred
	Errors []string `json:"errors,omitempty"`
}

// NewPromoteToHotSkill creates the promote_to_hot skill.
func NewPromoteToHotSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("promote_to_hot").
		Description("Manually promote content to hot cache for faster access. Use when you know you'll need content again soon.").
		Domain(RetrievalDomain).
		Keywords("promote", "cache", "hot", "pin", "keep").
		Priority(90). // Slightly lower than retrieval
		ArrayParam("content_ids", "IDs of content to promote to hot cache", "string", true).
		IntParam("ttl_turns", "How many turns content should stay in hot cache (default: 5)", false).
		Handler(createPromoteToHotHandler(deps)).
		Build()
}

func createPromoteToHotHandler(deps *RetrievalDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var params PromoteToHotInput
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if len(params.ContentIDs) == 0 {
			return nil, fmt.Errorf("content_ids is required")
		}

		return executePromoteToHot(ctx, params, deps)
	}
}

func executePromoteToHot(
	_ context.Context,
	params PromoteToHotInput,
	deps *RetrievalDependencies,
) (*PromoteToHotOutput, error) {
	if deps.HotCache == nil {
		return nil, fmt.Errorf("hot cache not configured")
	}

	ttlTurns := params.TTLTurns
	if ttlTurns <= 0 {
		ttlTurns = DefaultTTLTurns
	}

	output := &PromoteToHotOutput{
		Promoted: 0,
		Failed:   0,
		Errors:   make([]string, 0),
	}

	for _, contentID := range params.ContentIDs {
		err := promoteContent(contentID, deps)
		if err != nil {
			output.Failed++
			output.Errors = append(output.Errors, fmt.Sprintf("%s: %s", contentID, err.Error()))
		} else {
			output.Promoted++
		}
	}

	return output, nil
}

func promoteContent(contentID string, deps *RetrievalDependencies) error {
	// First, get the content from content store
	if deps.ContentStore == nil {
		return fmt.Errorf("content store not configured")
	}

	entries, err := deps.ContentStore.GetByIDs([]string{contentID})
	if err != nil {
		return fmt.Errorf("failed to get content: %w", err)
	}

	if len(entries) == 0 {
		return fmt.Errorf("content not found")
	}

	// Promote to hot cache
	entry := entries[0]
	deps.HotCache.Add(entry.ID, entry)

	return nil
}

// =============================================================================
// Helper Functions
// =============================================================================

// estimateTokens provides a rough token estimate (4 chars per token).
func estimateTokens(content string) int {
	return (len(content) + 3) / 4
}

// truncateToTokens truncates content to fit within token budget.
func truncateToTokens(content string, maxTokens int) string {
	maxChars := maxTokens * 4
	if len(content) <= maxChars {
		return content
	}

	// Find a good break point
	truncated := content[:maxChars]
	if lastSpace := findLastBreakPoint(truncated); lastSpace > maxChars/2 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

func findLastBreakPoint(s string) int {
	lastSpace := -1
	lastNewline := -1

	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' && lastNewline == -1 {
			lastNewline = i
		}
		if s[i] == ' ' && lastSpace == -1 {
			lastSpace = i
		}
		if lastSpace != -1 && lastNewline != -1 {
			break
		}
	}

	// Prefer newline over space
	if lastNewline > 0 {
		return lastNewline
	}
	return lastSpace
}

// generateSummary creates a brief summary of content.
func generateSummary(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	// Find a good break point
	summary := content[:maxLen]
	if lastBreak := findLastBreakPoint(summary); lastBreak > maxLen/2 {
		summary = summary[:lastBreak]
	}

	return summary + "..."
}

// =============================================================================
// Skill Registration
// =============================================================================

// RegisterUniversalSkills registers all universal retrieval skills.
func RegisterUniversalSkills(registry *skills.Registry, deps *RetrievalDependencies) error {
	// retrieve_context
	retrieveSkill := NewRetrieveContextSkill(deps)
	if err := registry.Register(retrieveSkill); err != nil {
		return fmt.Errorf("failed to register retrieve_context: %w", err)
	}

	// search_history
	searchSkill := NewSearchHistorySkill(deps)
	if err := registry.Register(searchSkill); err != nil {
		return fmt.Errorf("failed to register search_history: %w", err)
	}

	// promote_to_hot
	promoteSkill := NewPromoteToHotSkill(deps)
	if err := registry.Register(promoteSkill); err != nil {
		return fmt.Errorf("failed to register promote_to_hot: %w", err)
	}

	// Load all skills (TIER 1 - always loaded)
	registry.Load("retrieve_context")
	registry.Load("search_history")
	registry.Load("promote_to_hot")

	return nil
}
