// Package skills provides AR.8.3: Archivalist Retrieval Skills.
//
// The Archivalist agent specializes in session history and past interactions.
// These skills provide:
// - Session search and navigation
// - Historical solution retrieval
// - Session summarization
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// ArchivalistDomain is the skill domain for archivalist skills.
	ArchivalistDomain = "archivalist"

	// DefaultSessionSearchLimit is the default max sessions to return.
	DefaultSessionSearchLimit = 10

	// DefaultSolutionSearchLimit is the default max solutions to return.
	DefaultSolutionSearchLimit = 5

	// DefaultSummaryLength is the default max tokens for summaries.
	DefaultSummaryLength = 500
)

// =============================================================================
// Interfaces
// =============================================================================

// SessionSearcher searches across sessions.
type SessionSearcher interface {
	SearchSessions(
		ctx context.Context,
		query string,
		opts *SessionSearchOptions,
	) ([]*SessionSearchResult, error)
}

// SessionSummarizer generates session summaries.
type SessionSummarizer interface {
	GetSessionSummary(
		ctx context.Context,
		sessionID string,
		opts *SummaryOptions,
	) (*SessionSummary, error)
}

// SolutionFinder finds similar solutions from history.
type SolutionFinder interface {
	FindSimilarSolutions(
		ctx context.Context,
		problem string,
		opts *SolutionSearchOptions,
	) ([]*HistoricalSolution, error)
}

// ArchivalistContentStore is the interface for content store operations needed by archivalist.
type ArchivalistContentStore interface {
	Search(query string, filters *ctxpkg.SearchFilters, limit int) ([]*ctxpkg.ContentEntry, error)
}

// ArchivalistSearcher is the interface for search operations needed by archivalist.
type ArchivalistSearcher interface {
	SearchWithBudget(ctx context.Context, query string, budget time.Duration) *ctxpkg.TieredSearchResult
}

// =============================================================================
// Types
// =============================================================================

// SessionSearchOptions configures session search.
type SessionSearchOptions struct {
	MaxResults int
	FromTime   time.Time
	ToTime     time.Time
	AgentTypes []string
}

// SessionSearchResult represents a session search hit.
type SessionSearchResult struct {
	SessionID   string    `json:"session_id"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	TurnCount   int       `json:"turn_count"`
	Score       float64   `json:"score"`
	Snippet     string    `json:"snippet"`
	AgentTypes  []string  `json:"agent_types"`
	PrimaryGoal string    `json:"primary_goal"`
}

// SummaryOptions configures summary generation.
type SummaryOptions struct {
	MaxTokens       int
	IncludeOutcomes bool
	IncludeErrors   bool
}

// SessionSummary represents a session summary.
type SessionSummary struct {
	SessionID    string        `json:"session_id"`
	Summary      string        `json:"summary"`
	Goals        []string      `json:"goals"`
	Outcomes     []string      `json:"outcomes,omitempty"`
	Errors       []string      `json:"errors,omitempty"`
	KeyDecisions []string      `json:"key_decisions"`
	Duration     time.Duration `json:"duration"`
	TurnCount    int           `json:"turn_count"`
}

// SolutionSearchOptions configures solution search.
type SolutionSearchOptions struct {
	MaxResults     int
	MinSimilarity  float64
	SuccessfulOnly bool
	Languages      []string
}

// HistoricalSolution represents a past solution.
type HistoricalSolution struct {
	SessionID   string   `json:"session_id"`
	Problem     string   `json:"problem"`
	Solution    string   `json:"solution"`
	Outcome     string   `json:"outcome"`
	Similarity  float64  `json:"similarity"`
	Successful  bool     `json:"successful"`
	FilesChanged []string `json:"files_changed"`
}

// =============================================================================
// Dependencies
// =============================================================================

// ArchivalistDependencies holds dependencies for archivalist skills.
type ArchivalistDependencies struct {
	SessionSearcher   SessionSearcher
	SessionSummarizer SessionSummarizer
	SolutionFinder    SolutionFinder
	ContentStore      ArchivalistContentStore
	Searcher          ArchivalistSearcher
}

// =============================================================================
// Input/Output Types
// =============================================================================

// SearchSessionsInput is input for archivalist_search_sessions.
type SearchSessionsInput struct {
	Query      string    `json:"query"`
	MaxResults int       `json:"max_results,omitempty"`
	FromTime   time.Time `json:"from_time,omitempty"`
	ToTime     time.Time `json:"to_time,omitempty"`
	AgentTypes []string  `json:"agent_types,omitempty"`
}

// SearchSessionsOutput is output for archivalist_search_sessions.
type SearchSessionsOutput struct {
	Sessions   []*SessionSearchResult `json:"sessions"`
	TotalFound int                    `json:"total_found"`
}

// GetSessionSummaryInput is input for archivalist_get_session_summary.
type GetSessionSummaryInput struct {
	SessionID       string `json:"session_id"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	IncludeOutcomes bool   `json:"include_outcomes,omitempty"`
	IncludeErrors   bool   `json:"include_errors,omitempty"`
}

// GetSessionSummaryOutput is output for archivalist_get_session_summary.
type GetSessionSummaryOutput struct {
	Found   bool            `json:"found"`
	Summary *SessionSummary `json:"summary,omitempty"`
}

// FindSimilarSolutionsInput is input for archivalist_find_similar_solutions.
type FindSimilarSolutionsInput struct {
	Problem        string   `json:"problem"`
	MaxResults     int      `json:"max_results,omitempty"`
	MinSimilarity  float64  `json:"min_similarity,omitempty"`
	SuccessfulOnly bool     `json:"successful_only,omitempty"`
	Languages      []string `json:"languages,omitempty"`
}

// FindSimilarSolutionsOutput is output for archivalist_find_similar_solutions.
type FindSimilarSolutionsOutput struct {
	Solutions []*HistoricalSolution `json:"solutions"`
}

// =============================================================================
// Skill Constructors
// =============================================================================

// NewSearchSessionsSkill creates the archivalist_search_sessions skill.
func NewSearchSessionsSkill(deps *ArchivalistDependencies) *skills.Skill {
	return skills.NewSkill("archivalist_search_sessions").
		Domain(ArchivalistDomain).
		Description("Search across sessions for relevant history").
		Keywords("session", "search", "history", "past").
		Handler(createSearchSessionsHandler(deps)).
		Build()
}

// NewGetSessionSummarySkill creates the archivalist_get_session_summary skill.
func NewGetSessionSummarySkill(deps *ArchivalistDependencies) *skills.Skill {
	return skills.NewSkill("archivalist_get_session_summary").
		Domain(ArchivalistDomain).
		Description("Get a summary of a specific session").
		Keywords("session", "summary", "overview").
		Handler(createGetSessionSummaryHandler(deps)).
		Build()
}

// NewFindSimilarSolutionsSkill creates the archivalist_find_similar_solutions skill.
func NewFindSimilarSolutionsSkill(deps *ArchivalistDependencies) *skills.Skill {
	return skills.NewSkill("archivalist_find_similar_solutions").
		Domain(ArchivalistDomain).
		Description("Find similar problems and solutions from history").
		Keywords("solution", "similar", "history", "problem").
		Handler(createFindSimilarSolutionsHandler(deps)).
		Build()
}

// =============================================================================
// Handlers
// =============================================================================

func createSearchSessionsHandler(deps *ArchivalistDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in SearchSessionsInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		maxResults := in.MaxResults
		if maxResults <= 0 {
			maxResults = DefaultSessionSearchLimit
		}

		// Use dedicated searcher if available
		if deps.SessionSearcher != nil {
			return searchWithSessionSearcher(ctx, deps, in, maxResults)
		}

		// Fall back to content store search
		if deps.ContentStore != nil {
			return searchSessionsWithContentStore(ctx, deps, in, maxResults)
		}

		return nil, fmt.Errorf("no session searcher or content store configured")
	}
}

func searchWithSessionSearcher(
	ctx context.Context,
	deps *ArchivalistDependencies,
	in SearchSessionsInput,
	maxResults int,
) (*SearchSessionsOutput, error) {
	opts := &SessionSearchOptions{
		MaxResults: maxResults,
		FromTime:   in.FromTime,
		ToTime:     in.ToTime,
		AgentTypes: in.AgentTypes,
	}

	sessions, err := deps.SessionSearcher.SearchSessions(ctx, in.Query, opts)
	if err != nil {
		return nil, fmt.Errorf("session search failed: %w", err)
	}

	return &SearchSessionsOutput{
		Sessions:   sessions,
		TotalFound: len(sessions),
	}, nil
}

func searchSessionsWithContentStore(
	_ context.Context,
	deps *ArchivalistDependencies,
	in SearchSessionsInput,
	maxResults int,
) (*SearchSessionsOutput, error) {
	filters := &ctxpkg.SearchFilters{
		ContentTypes: []ctxpkg.ContentType{
			ctxpkg.ContentTypeUserPrompt,
			ctxpkg.ContentTypeAssistantReply,
		},
	}

	if !in.FromTime.IsZero() {
		filters.TimeRange[0] = in.FromTime
	}
	if !in.ToTime.IsZero() {
		filters.TimeRange[1] = in.ToTime
	}

	entries, err := deps.ContentStore.Search(in.Query, filters, maxResults*10)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	// Group by session and build results
	sessionMap := make(map[string]*SessionSearchResult)
	for _, entry := range entries {
		result := getOrCreateSessionResult(sessionMap, entry)
		updateSessionResult(result, entry, in.Query)
	}

	return buildSessionSearchOutput(sessionMap, maxResults), nil
}

func getOrCreateSessionResult(
	sessionMap map[string]*SessionSearchResult,
	entry *ctxpkg.ContentEntry,
) *SessionSearchResult {
	if result, exists := sessionMap[entry.SessionID]; exists {
		return result
	}

	result := &SessionSearchResult{
		SessionID:  entry.SessionID,
		StartTime:  entry.Timestamp,
		EndTime:    entry.Timestamp,
		TurnCount:  0,
		AgentTypes: make([]string, 0),
	}
	sessionMap[entry.SessionID] = result
	return result
}

func updateSessionResult(
	result *SessionSearchResult,
	entry *ctxpkg.ContentEntry,
	query string,
) {
	result.TurnCount++
	if entry.Timestamp.Before(result.StartTime) {
		result.StartTime = entry.Timestamp
	}
	if entry.Timestamp.After(result.EndTime) {
		result.EndTime = entry.Timestamp
	}

	// Track agent types
	if entry.AgentType != "" && !containsString(result.AgentTypes, entry.AgentType) {
		result.AgentTypes = append(result.AgentTypes, entry.AgentType)
	}

	// Update snippet if content matches query better
	if result.Snippet == "" && strings.Contains(
		strings.ToLower(entry.Content),
		strings.ToLower(query),
	) {
		result.Snippet = generateSnippet(entry.Content, query)
	}
}

func buildSessionSearchOutput(
	sessionMap map[string]*SessionSearchResult,
	maxResults int,
) *SearchSessionsOutput {
	sessions := make([]*SessionSearchResult, 0, len(sessionMap))
	for _, result := range sessionMap {
		sessions = append(sessions, result)
	}

	// Limit results
	if len(sessions) > maxResults {
		sessions = sessions[:maxResults]
	}

	return &SearchSessionsOutput{
		Sessions:   sessions,
		TotalFound: len(sessionMap),
	}
}

func createGetSessionSummaryHandler(deps *ArchivalistDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetSessionSummaryInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.SessionID == "" {
			return nil, fmt.Errorf("session_id is required")
		}

		maxTokens := in.MaxTokens
		if maxTokens <= 0 {
			maxTokens = DefaultSummaryLength
		}

		// Use dedicated summarizer if available
		if deps.SessionSummarizer != nil {
			return getSummaryWithSummarizer(ctx, deps, in, maxTokens)
		}

		// Fall back to building summary from content store
		if deps.ContentStore != nil {
			return buildSessionSummaryFromStore(ctx, deps, in, maxTokens)
		}

		return nil, fmt.Errorf("no session summarizer or content store configured")
	}
}

func getSummaryWithSummarizer(
	ctx context.Context,
	deps *ArchivalistDependencies,
	in GetSessionSummaryInput,
	maxTokens int,
) (*GetSessionSummaryOutput, error) {
	opts := &SummaryOptions{
		MaxTokens:       maxTokens,
		IncludeOutcomes: in.IncludeOutcomes,
		IncludeErrors:   in.IncludeErrors,
	}

	summary, err := deps.SessionSummarizer.GetSessionSummary(ctx, in.SessionID, opts)
	if err != nil {
		return &GetSessionSummaryOutput{Found: false}, nil
	}

	return &GetSessionSummaryOutput{
		Found:   true,
		Summary: summary,
	}, nil
}

func buildSessionSummaryFromStore(
	_ context.Context,
	deps *ArchivalistDependencies,
	in GetSessionSummaryInput,
	maxTokens int,
) (*GetSessionSummaryOutput, error) {
	filters := &ctxpkg.SearchFilters{
		SessionID: in.SessionID,
	}

	entries, err := deps.ContentStore.Search("", filters, 1000)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch session content: %w", err)
	}

	if len(entries) == 0 {
		return &GetSessionSummaryOutput{Found: false}, nil
	}

	summary := buildSummaryFromEntries(entries, in, maxTokens)
	return &GetSessionSummaryOutput{
		Found:   true,
		Summary: summary,
	}, nil
}

func buildSummaryFromEntries(
	entries []*ctxpkg.ContentEntry,
	in GetSessionSummaryInput,
	maxTokens int,
) *SessionSummary {
	summary := &SessionSummary{
		SessionID:    in.SessionID,
		Goals:        make([]string, 0),
		Outcomes:     make([]string, 0),
		Errors:       make([]string, 0),
		KeyDecisions: make([]string, 0),
	}

	var startTime, endTime time.Time
	var contentBuilder strings.Builder

	for _, entry := range entries {
		// Track time range
		if startTime.IsZero() || entry.Timestamp.Before(startTime) {
			startTime = entry.Timestamp
		}
		if endTime.IsZero() || entry.Timestamp.After(endTime) {
			endTime = entry.Timestamp
		}

		// Collect errors if requested
		if in.IncludeErrors && isErrorContent(entry) {
			summary.Errors = append(summary.Errors, extractErrorMessage(entry))
		}

		contentBuilder.WriteString(entry.Content)
		contentBuilder.WriteString(" ")
	}

	summary.TurnCount = len(entries)
	summary.Duration = endTime.Sub(startTime)
	summary.Summary = truncateSummaryToTokens(contentBuilder.String(), maxTokens)

	return summary
}

func isErrorContent(entry *ctxpkg.ContentEntry) bool {
	// Check for error indicators in content (no ContentTypeError exists)
	content := strings.ToLower(entry.Content)
	return strings.Contains(content, "error:") || strings.Contains(content, "failed:")
}

func extractErrorMessage(entry *ctxpkg.ContentEntry) string {
	// Extract first line or first 100 chars
	lines := strings.SplitN(entry.Content, "\n", 2)
	msg := lines[0]
	if len(msg) > 100 {
		msg = msg[:100] + "..."
	}
	return msg
}

func createFindSimilarSolutionsHandler(deps *ArchivalistDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in FindSimilarSolutionsInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.Problem == "" {
			return nil, fmt.Errorf("problem is required")
		}

		maxResults := in.MaxResults
		if maxResults <= 0 {
			maxResults = DefaultSolutionSearchLimit
		}

		minSimilarity := in.MinSimilarity
		if minSimilarity <= 0 {
			minSimilarity = 0.5
		}

		// Use dedicated finder if available
		if deps.SolutionFinder != nil {
			return findWithSolutionFinder(ctx, deps, in, maxResults, minSimilarity)
		}

		// Fall back to semantic search
		if deps.Searcher != nil {
			return findSolutionsWithSearcher(ctx, deps, in, maxResults)
		}

		return nil, fmt.Errorf("no solution finder or searcher configured")
	}
}

func findWithSolutionFinder(
	ctx context.Context,
	deps *ArchivalistDependencies,
	in FindSimilarSolutionsInput,
	maxResults int,
	minSimilarity float64,
) (*FindSimilarSolutionsOutput, error) {
	opts := &SolutionSearchOptions{
		MaxResults:     maxResults,
		MinSimilarity:  minSimilarity,
		SuccessfulOnly: in.SuccessfulOnly,
		Languages:      in.Languages,
	}

	solutions, err := deps.SolutionFinder.FindSimilarSolutions(ctx, in.Problem, opts)
	if err != nil {
		return nil, fmt.Errorf("solution search failed: %w", err)
	}

	return &FindSimilarSolutionsOutput{Solutions: solutions}, nil
}

func findSolutionsWithSearcher(
	ctx context.Context,
	deps *ArchivalistDependencies,
	in FindSimilarSolutionsInput,
	maxResults int,
) (*FindSimilarSolutionsOutput, error) {
	// Search for similar problems in history
	results := deps.Searcher.SearchWithBudget(ctx, in.Problem, ctxpkg.TierFullBudget)

	solutions := make([]*HistoricalSolution, 0, maxResults)
	for i, entry := range results.Results {
		if len(solutions) >= maxResults {
			break
		}

		// Only include assistant reply content (solutions/answers)
		if entry.ContentType != ctxpkg.ContentTypeAssistantReply {
			continue
		}

		// Calculate position-based similarity
		similarity := 1.0 - float64(i)*0.1
		if similarity < 0.3 {
			similarity = 0.3
		}

		solution := &HistoricalSolution{
			SessionID:  entry.SessionID,
			Problem:    extractProblemFromContent(entry.Content),
			Solution:   extractSolutionFromContent(entry.Content),
			Similarity: similarity,
			Successful: !strings.Contains(strings.ToLower(entry.Content), "error"),
		}

		if in.SuccessfulOnly && !solution.Successful {
			continue
		}

		solutions = append(solutions, solution)
	}

	return &FindSimilarSolutionsOutput{Solutions: solutions}, nil
}

// =============================================================================
// Helper Functions
// =============================================================================

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func generateSnippet(content, query string) string {
	const snippetLen = 150

	lowerContent := strings.ToLower(content)
	lowerQuery := strings.ToLower(query)

	idx := strings.Index(lowerContent, lowerQuery)
	if idx < 0 {
		// Query not found, return start of content
		if len(content) <= snippetLen {
			return content
		}
		return content[:snippetLen] + "..."
	}

	// Center snippet around query
	start := idx - snippetLen/2
	if start < 0 {
		start = 0
	}

	end := start + snippetLen
	if end > len(content) {
		end = len(content)
	}

	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}

	return snippet
}

func extractProblemFromContent(content string) string {
	// Extract problem description (first 200 chars or first paragraph)
	lines := strings.SplitN(content, "\n\n", 2)
	problem := lines[0]
	if len(problem) > 200 {
		problem = problem[:200] + "..."
	}
	return problem
}

func extractSolutionFromContent(content string) string {
	// Extract solution (content after first paragraph)
	parts := strings.SplitN(content, "\n\n", 2)
	if len(parts) < 2 {
		return content
	}

	solution := parts[1]
	if len(solution) > 500 {
		solution = solution[:500] + "..."
	}
	return solution
}

// truncateSummaryToTokens truncates content to fit within token budget.
func truncateSummaryToTokens(content string, maxTokens int) string {
	// Rough estimate: 1 token ≈ 4 characters
	maxChars := maxTokens * 4
	if len(content) <= maxChars {
		return content
	}

	// Find a good break point
	breakPoint := maxChars
	for i := maxChars - 1; i > maxChars-100 && i > 0; i-- {
		if content[i] == ' ' || content[i] == '\n' {
			breakPoint = i
			break
		}
	}

	return content[:breakPoint] + "..."
}

// =============================================================================
// Registration
// =============================================================================

// RegisterArchivalistSkills registers all archivalist skills with the registry.
func RegisterArchivalistSkills(
	registry *skills.Registry,
	deps *ArchivalistDependencies,
) error {
	skillsToRegister := []*skills.Skill{
		NewSearchSessionsSkill(deps),
		NewGetSessionSummarySkill(deps),
		NewFindSimilarSolutionsSkill(deps),
	}

	for _, skill := range skillsToRegister {
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register %s: %w", skill.Name, err)
		}
	}

	return nil
}
