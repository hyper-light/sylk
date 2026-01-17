package archivalist

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SummaryGenerator creates hierarchical summaries from entries
type SummaryGenerator struct {
	client              *Client
	archive             *Archive
	factExtractor       *FactExtractor
	maxEntriesPerSpan   int
	targetTokensPerSpan int
}

// SummaryGeneratorConfig configures the summary generator
type SummaryGeneratorConfig struct {
	Client              *Client
	Archive             *Archive
	MaxEntriesPerSpan   int // Default: 20
	TargetTokensPerSpan int // Default: 2000
}

// NewSummaryGenerator creates a new summary generator
func NewSummaryGenerator(cfg SummaryGeneratorConfig) *SummaryGenerator {
	maxEntries := cfg.MaxEntriesPerSpan
	if maxEntries == 0 {
		maxEntries = 20
	}

	targetTokens := cfg.TargetTokensPerSpan
	if targetTokens == 0 {
		targetTokens = 2000
	}

	return &SummaryGenerator{
		client:              cfg.Client,
		archive:             cfg.Archive,
		maxEntriesPerSpan:   maxEntries,
		targetTokensPerSpan: targetTokens,
	}
}

// GenerateSpanSummary creates a summary from a span of entries
func (g *SummaryGenerator) GenerateSpanSummary(ctx context.Context, entries []*Entry, sessionID string) (*CompactedSummary, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("no entries to summarize")
	}

	// Sort entries by creation time
	sortedEntries := make([]*Entry, len(entries))
	copy(sortedEntries, entries)
	sort.Slice(sortedEntries, func(i, j int) bool {
		return sortedEntries[i].CreatedAt.Before(sortedEntries[j].CreatedAt)
	})

	// Build prompt
	prompt := g.buildSpanPrompt(sortedEntries)

	// Generate summary via LLM
	generated, err := g.client.GenerateSummary(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate span summary: %w", err)
	}

	// Extract source entry IDs
	sourceIDs := make([]string, len(entries))
	for i, e := range entries {
		sourceIDs[i] = e.ID
	}

	// Determine time range
	timeStart := sortedEntries[0].CreatedAt
	timeEnd := sortedEntries[len(sortedEntries)-1].CreatedAt

	summary := &CompactedSummary{
		ID:             uuid.New().String(),
		Level:          SummaryLevelSpan,
		Scope:          SummaryScopeGeneral,
		Content:        generated.Content,
		KeyPoints:      extractKeyPoints(generated.Content),
		TokensEstimate: generated.TokensUsed,
		SourceEntryIDs: sourceIDs,
		SessionID:      sessionID,
		TimeStart:      &timeStart,
		TimeEnd:        &timeEnd,
		CreatedAt:      time.Now(),
	}

	return summary, nil
}

// GenerateSessionSummary creates a summary for an entire session
func (g *SummaryGenerator) GenerateSessionSummary(ctx context.Context, session *Session, entries []*Entry, spanSummaries []*CompactedSummary) (*CompactedSummary, error) {
	// Build prompt from span summaries if available, otherwise from entries
	var prompt string
	var sourceIDs []string
	var sourceSummaryIDs []string

	if len(spanSummaries) > 0 {
		prompt = g.buildSessionPromptFromSpans(session, spanSummaries)
		for _, s := range spanSummaries {
			sourceSummaryIDs = append(sourceSummaryIDs, s.ID)
			sourceIDs = append(sourceIDs, s.SourceEntryIDs...)
		}
	} else {
		prompt = g.buildSessionPromptFromEntries(session, entries)
		for _, e := range entries {
			sourceIDs = append(sourceIDs, e.ID)
		}
	}

	// Generate summary
	generated, err := g.client.GenerateSummary(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate session summary: %w", err)
	}

	summary := &CompactedSummary{
		ID:               uuid.New().String(),
		Level:            SummaryLevelSession,
		Scope:            SummaryScopeGeneral,
		ScopeID:          session.ID,
		Content:          generated.Content,
		KeyPoints:        extractKeyPoints(generated.Content),
		TokensEstimate:   generated.TokensUsed,
		SourceEntryIDs:   deduplicateStrings(sourceIDs),
		SourceSummaryIDs: sourceSummaryIDs,
		SessionID:        session.ID,
		TimeStart:        &session.StartedAt,
		TimeEnd:          session.EndedAt,
		CreatedAt:        time.Now(),
	}

	return summary, nil
}

// GenerateTaskSummary creates a summary focused on a specific task
func (g *SummaryGenerator) GenerateTaskSummary(ctx context.Context, taskEntries []*Entry, taskID string, sessionID string) (*CompactedSummary, error) {
	if len(taskEntries) == 0 {
		return nil, fmt.Errorf("no entries for task")
	}

	generated, err := g.generateTaskSummary(ctx, taskEntries)
	if err != nil {
		return nil, err
	}

	sourceIDs := taskEntryIDs(taskEntries)
	timeStart, timeEnd := taskEntryTimeRange(taskEntries)

	summary := &CompactedSummary{
		ID:             uuid.New().String(),
		Level:          SummaryLevelSpan,
		Scope:          SummaryScopeTask,
		ScopeID:        taskID,
		Content:        generated.Content,
		KeyPoints:      extractKeyPoints(generated.Content),
		TokensEstimate: generated.TokensUsed,
		SourceEntryIDs: sourceIDs,
		SessionID:      sessionID,
		TimeStart:      &timeStart,
		TimeEnd:        &timeEnd,
		CreatedAt:      time.Now(),
	}

	return summary, nil
}

func (g *SummaryGenerator) generateTaskSummary(ctx context.Context, taskEntries []*Entry) (*GeneratedSummary, error) {
	prompt := g.buildTaskPrompt(taskEntries)
	generated, err := g.client.GenerateSummary(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate task summary: %w", err)
	}
	return generated, nil
}

func taskEntryIDs(entries []*Entry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func taskEntryTimeRange(entries []*Entry) (time.Time, time.Time) {
	var timeStart, timeEnd time.Time
	for _, entry := range entries {
		timeStart = pickEarlierTime(timeStart, entry.CreatedAt)
		timeEnd = pickLaterTime(timeEnd, entry.CreatedAt)
	}
	return timeStart, timeEnd
}

func pickEarlierTime(current time.Time, candidate time.Time) time.Time {
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

func pickLaterTime(current time.Time, candidate time.Time) time.Time {
	if current.IsZero() || candidate.After(current) {
		return candidate
	}
	return current
}

// Prompt building helpers

func (g *SummaryGenerator) buildSpanPrompt(entries []*Entry) string {
	var sb strings.Builder
	sb.WriteString("Summarize the following chronicle entries into a concise summary. Focus on:\n")
	sb.WriteString("1. Key decisions made\n")
	sb.WriteString("2. Important learnings or failures\n")
	sb.WriteString("3. Progress on tasks\n")
	sb.WriteString("4. Patterns or conventions established\n\n")
	sb.WriteString("Entries:\n\n")

	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", e.Category, e.CreatedAt.Format(time.RFC3339)))
		if e.Title != "" {
			sb.WriteString(fmt.Sprintf("Title: %s\n", e.Title))
		}
		sb.WriteString(fmt.Sprintf("Content: %s\n\n", truncateContent(e.Content, 500)))
	}

	return sb.String()
}

func (g *SummaryGenerator) buildSessionPromptFromSpans(session *Session, spans []*CompactedSummary) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Create a comprehensive session summary for session %s.\n", session.ID))
	if session.PrimaryFocus != "" {
		sb.WriteString(fmt.Sprintf("Primary focus: %s\n", session.PrimaryFocus))
	}
	sb.WriteString("\nThe session consisted of the following work spans:\n\n")

	for i, span := range spans {
		sb.WriteString(fmt.Sprintf("--- Span %d ---\n", i+1))
		if span.TimeStart != nil && span.TimeEnd != nil {
			sb.WriteString(fmt.Sprintf("Time: %s to %s\n", span.TimeStart.Format(time.Kitchen), span.TimeEnd.Format(time.Kitchen)))
		}
		sb.WriteString(fmt.Sprintf("Summary: %s\n\n", span.Content))
	}

	sb.WriteString("\nProvide a unified session summary that captures the overall progress, key decisions, and important learnings.")

	return sb.String()
}

func (g *SummaryGenerator) buildSessionPromptFromEntries(session *Session, entries []*Entry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Create a comprehensive session summary for session %s.\n", session.ID))
	if session.PrimaryFocus != "" {
		sb.WriteString(fmt.Sprintf("Primary focus: %s\n", session.PrimaryFocus))
	}
	sb.WriteString(fmt.Sprintf("Entry count: %d\n\n", len(entries)))

	// Group by category for better context
	byCategory := make(map[Category][]*Entry)
	for _, e := range entries {
		byCategory[e.Category] = append(byCategory[e.Category], e)
	}

	for cat, catEntries := range byCategory {
		sb.WriteString(fmt.Sprintf("\n== %s (%d entries) ==\n", cat, len(catEntries)))
		for _, e := range catEntries[:min(len(catEntries), 5)] {
			sb.WriteString(fmt.Sprintf("- %s\n", truncateContent(e.Content, 200)))
		}
		if len(catEntries) > 5 {
			sb.WriteString(fmt.Sprintf("... and %d more\n", len(catEntries)-5))
		}
	}

	return sb.String()
}

func (g *SummaryGenerator) buildTaskPrompt(entries []*Entry) string {
	var sb strings.Builder
	sb.WriteString("Summarize the following task-related entries. Focus on:\n")
	sb.WriteString("1. What was the task objective\n")
	sb.WriteString("2. What steps were taken\n")
	sb.WriteString("3. What was the outcome\n")
	sb.WriteString("4. Any blockers or issues encountered\n\n")

	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", e.Category, e.CreatedAt.Format(time.Kitchen)))
		sb.WriteString(fmt.Sprintf("%s\n\n", truncateContent(e.Content, 300)))
	}

	return sb.String()
}

// CompactEntries determines which entries can be compacted into summaries
func (g *SummaryGenerator) CompactEntries(entries []*Entry) [][]*Entry {
	if len(entries) <= g.maxEntriesPerSpan {
		return [][]*Entry{entries}
	}

	var spans [][]*Entry
	var currentSpan []*Entry
	currentTokens := 0

	for _, entry := range entries {
		entryTokens := entry.TokensEstimate
		if entryTokens == 0 {
			entryTokens = EstimateTokens(entry.Content)
		}

		// Start new span if current would exceed limits
		if len(currentSpan) >= g.maxEntriesPerSpan || currentTokens+entryTokens > g.targetTokensPerSpan*2 {
			if len(currentSpan) > 0 {
				spans = append(spans, currentSpan)
			}
			currentSpan = nil
			currentTokens = 0
		}

		currentSpan = append(currentSpan, entry)
		currentTokens += entryTokens
	}

	if len(currentSpan) > 0 {
		spans = append(spans, currentSpan)
	}

	return spans
}

// Helper functions

func extractKeyPoints(content string) []string {
	var keyPoints []string

	// Split by sentences and take first few substantial ones
	sentences := strings.Split(content, ". ")
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if len(s) > 20 && len(keyPoints) < 5 {
			keyPoints = append(keyPoints, s)
		}
	}

	return keyPoints
}

func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}

func deduplicateStrings(strs []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
