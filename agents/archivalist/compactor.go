package archivalist

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Compactor manages the compaction of entries into facts and summaries
type Compactor struct {
	store            *Store
	archive          *Archive
	factExtractor    *FactExtractor
	summaryGenerator *SummaryGenerator

	// Configuration
	tokenThreshold      int  // Trigger compaction when hot memory exceeds this
	minEntriesForSpan   int  // Minimum entries before creating span summary
	autoArchiveComplete bool // Auto-archive entries after compaction
}

// CompactorConfig configures the compactor
type CompactorConfig struct {
	Store               *Store
	Archive             *Archive
	Client              *Client
	TokenThreshold      int  // Default: 500000 (500K tokens)
	MinEntriesForSpan   int  // Default: 10
	AutoArchiveComplete bool // Default: true
}

// NewCompactor creates a new compactor
func NewCompactor(cfg CompactorConfig) *Compactor {
	tokenThreshold := cfg.TokenThreshold
	if tokenThreshold == 0 {
		tokenThreshold = 500000
	}

	minEntries := cfg.MinEntriesForSpan
	if minEntries == 0 {
		minEntries = 10
	}

	sessionID := ""
	if cfg.Store != nil {
		session := cfg.Store.GetCurrentSession()
		if session != nil {
			sessionID = session.ID
		}
	}

	return &Compactor{
		store:               cfg.Store,
		archive:             cfg.Archive,
		factExtractor:       NewFactExtractor(sessionID),
		summaryGenerator:    NewSummaryGenerator(SummaryGeneratorConfig{
			Client:  cfg.Client,
			Archive: cfg.Archive,
		}),
		tokenThreshold:      tokenThreshold,
		minEntriesForSpan:   minEntries,
		autoArchiveComplete: cfg.AutoArchiveComplete,
	}
}

// CompactionResult contains the results of a compaction operation
type CompactionResult struct {
	EntriesProcessed   int
	FactsExtracted     *ExtractedFacts
	SummariesGenerated []*CompactedSummary
	TokensReclaimed    int
	EntriesArchived    int
	Duration           time.Duration
	Error              error
}

// ShouldCompact returns true if compaction is recommended
func (c *Compactor) ShouldCompact() bool {
	stats := c.store.Stats()
	return stats.HotMemoryTokens > c.tokenThreshold
}

// Compact performs a full compaction cycle
func (c *Compactor) Compact(ctx context.Context) *CompactionResult {
	startTime := time.Now()
	result := &CompactionResult{}

	// Get entries eligible for compaction (completed, not recently accessed)
	entries := c.getCompactionCandidates()
	if len(entries) < c.minEntriesForSpan {
		result.Duration = time.Since(startTime)
		return result
	}

	result.EntriesProcessed = len(entries)

	// Extract facts from entries
	facts := c.factExtractor.ExtractAll(entries)
	fileChanges := c.factExtractor.ExtractFileChanges(entries)
	facts.FileChanges = fileChanges
	result.FactsExtracted = facts

	// Save facts to archive
	if c.archive != nil {
		if err := c.saveFacts(facts); err != nil {
			result.Error = fmt.Errorf("failed to save facts: %w", err)
			result.Duration = time.Since(startTime)
			return result
		}
	}

	// Generate span summaries
	spans := c.summaryGenerator.CompactEntries(entries)
	sessionID := c.store.GetCurrentSession().ID

	for _, span := range spans {
		if len(span) < c.minEntriesForSpan {
			continue
		}

		summary, err := c.summaryGenerator.GenerateSpanSummary(ctx, span, sessionID)
		if err != nil {
			// Log error but continue with other spans
			continue
		}

		result.SummariesGenerated = append(result.SummariesGenerated, summary)

		// Save summary to archive
		if c.archive != nil {
			if err := c.archive.SaveSummary(summary); err != nil {
				// Log error but continue
				continue
			}
		}

		// Calculate tokens reclaimed (original entries - summary)
		for _, e := range span {
			result.TokensReclaimed += e.TokensEstimate
		}
		result.TokensReclaimed -= summary.TokensEstimate
	}

	// Archive original entries if configured
	if c.autoArchiveComplete && c.archive != nil {
		if err := c.archive.ArchiveEntries(entries); err == nil {
			result.EntriesArchived = len(entries)
			// Remove from hot storage
			for _, e := range entries {
				c.store.MarkArchived(e.ID)
			}
		}
	}

	result.Duration = time.Since(startTime)
	return result
}

// CompactSession performs end-of-session compaction
func (c *Compactor) CompactSession(ctx context.Context, session *Session) (*CompactionResult, error) {
	startTime := time.Now()
	result := &CompactionResult{}

	// Get all entries for the session
	entries, err := c.store.Query(ArchiveQuery{
		SessionIDs: []string{session.ID},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query session entries: %w", err)
	}

	result.EntriesProcessed = len(entries)

	// Extract facts
	facts := c.factExtractor.ExtractAll(entries)
	fileChanges := c.factExtractor.ExtractFileChanges(entries)
	facts.FileChanges = fileChanges
	result.FactsExtracted = facts

	// Save facts
	if c.archive != nil {
		if err := c.saveFacts(facts); err != nil {
			return nil, fmt.Errorf("failed to save facts: %w", err)
		}
	}

	// Generate span summaries first
	spans := c.summaryGenerator.CompactEntries(entries)
	var spanSummaries []*CompactedSummary

	for _, span := range spans {
		if len(span) < 3 { // Lower threshold for session compaction
			continue
		}

		summary, err := c.summaryGenerator.GenerateSpanSummary(ctx, span, session.ID)
		if err != nil {
			continue
		}

		spanSummaries = append(spanSummaries, summary)
		result.SummariesGenerated = append(result.SummariesGenerated, summary)

		if c.archive != nil {
			c.archive.SaveSummary(summary)
		}
	}

	// Generate session summary from span summaries
	sessionSummary, err := c.summaryGenerator.GenerateSessionSummary(ctx, session, entries, spanSummaries)
	if err == nil {
		result.SummariesGenerated = append(result.SummariesGenerated, sessionSummary)
		if c.archive != nil {
			c.archive.SaveSummary(sessionSummary)
		}
	}

	// Calculate tokens reclaimed
	for _, e := range entries {
		result.TokensReclaimed += e.TokensEstimate
	}
	for _, s := range result.SummariesGenerated {
		result.TokensReclaimed -= s.TokensEstimate
	}

	// Archive all session entries
	if c.archive != nil {
		if err := c.archive.ArchiveEntries(entries); err == nil {
			result.EntriesArchived = len(entries)
		}
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// getCompactionCandidates returns entries eligible for compaction
func (c *Compactor) getCompactionCandidates() []*Entry {
	// Get entries that are:
	// 1. In completed/closed state (for tasks)
	// 2. Older than a threshold (not recently accessed)
	// 3. Not already archived

	cutoff := time.Now().Add(-30 * time.Minute) // Entries older than 30 min

	allEntries, _ := c.store.Query(ArchiveQuery{
		IncludeArchived: false,
	})

	var candidates []*Entry
	for _, e := range allEntries {
		if e.CreatedAt.Before(cutoff) && e.ArchivedAt == nil {
			candidates = append(candidates, e)
		}
	}

	// Sort by creation time
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})

	return candidates
}

// saveFacts saves all extracted facts to the archive
func (c *Compactor) saveFacts(facts *ExtractedFacts) error {
	for _, d := range facts.Decisions {
		if err := c.archive.SaveFactDecision(d); err != nil {
			return err
		}
	}

	for _, p := range facts.Patterns {
		if err := c.archive.SaveFactPattern(p); err != nil {
			return err
		}
	}

	for _, f := range facts.Failures {
		if err := c.archive.SaveFactFailure(f); err != nil {
			return err
		}
	}

	for _, fc := range facts.FileChanges {
		if err := c.archive.SaveFactFileChange(fc); err != nil {
			return err
		}
	}

	return nil
}

// GetRetrievalContext builds context for a query from facts and summaries
func (c *Compactor) GetRetrievalContext(ctx context.Context, query string, maxTokens int) (*RetrievalContext, error) {
	if c.archive == nil {
		return nil, fmt.Errorf("archive not available")
	}

	result := &RetrievalContext{
		Query:     query,
		MaxTokens: maxTokens,
	}

	currentTokens := 0

	// 1. Search for relevant summaries first (most compressed)
	summaries, err := c.archive.SearchSummaries(query, 10)
	if err == nil {
		for _, s := range summaries {
			if currentTokens+s.TokensEstimate > maxTokens {
				break
			}
			result.Summaries = append(result.Summaries, s)
			currentTokens += s.TokensEstimate
		}
	}

	// 2. Get relevant facts (very compressed structured data)
	// Decision facts
	decisions, _ := c.archive.QueryFactDecisions("", 20)
	for _, d := range decisions {
		tokens := EstimateTokens(d.Choice + d.Rationale)
		if currentTokens+tokens > maxTokens {
			break
		}
		result.Decisions = append(result.Decisions, d)
		currentTokens += tokens
	}

	// Pattern facts
	patterns, _ := c.archive.QueryFactPatterns("", 20)
	for _, p := range patterns {
		tokens := EstimateTokens(p.Pattern + p.Example)
		if currentTokens+tokens > maxTokens {
			break
		}
		result.Patterns = append(result.Patterns, p)
		currentTokens += tokens
	}

	// Failure facts
	failures, _ := c.archive.QueryFactFailures("", 20)
	for _, f := range failures {
		tokens := EstimateTokens(f.Approach + f.Reason + f.Resolution)
		if currentTokens+tokens > maxTokens {
			break
		}
		result.Failures = append(result.Failures, f)
		currentTokens += tokens
	}

	// 3. If we still have budget, add recent raw entries
	if currentTokens < maxTokens {
		entries, _ := c.archive.SearchText(query, 20)
		for _, e := range entries {
			if currentTokens+e.TokensEstimate > maxTokens {
				break
			}
			result.Entries = append(result.Entries, e)
			currentTokens += e.TokensEstimate
		}
	}

	result.TotalTokens = currentTokens
	return result, nil
}

// RetrievalContext holds context assembled for a query
type RetrievalContext struct {
	Query       string
	MaxTokens   int
	TotalTokens int
	Summaries   []*CompactedSummary
	Decisions   []*FactDecision
	Patterns    []*FactPattern
	Failures    []*FactFailure
	Entries     []*Entry
}

// ToPromptContext formats the retrieval context for inclusion in a prompt
func (rc *RetrievalContext) ToPromptContext() string {
	var sb strings.Builder

	if len(rc.Summaries) > 0 {
		sb.WriteString("=== Previous Context ===\n\n")
		for _, s := range rc.Summaries {
			sb.WriteString(fmt.Sprintf("[%s Summary]\n%s\n\n", s.Level, s.Content))
		}
	}

	if len(rc.Decisions) > 0 {
		sb.WriteString("=== Key Decisions ===\n")
		for _, d := range rc.Decisions {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", d.Choice, d.Rationale))
		}
		sb.WriteString("\n")
	}

	if len(rc.Patterns) > 0 {
		sb.WriteString("=== Established Patterns ===\n")
		for _, p := range rc.Patterns {
			sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", p.Category, p.Name, p.Pattern))
		}
		sb.WriteString("\n")
	}

	if len(rc.Failures) > 0 {
		sb.WriteString("=== Learned Failures ===\n")
		for _, f := range rc.Failures {
			line := fmt.Sprintf("- %s: %s", f.Approach, f.Reason)
			if f.Resolution != "" {
				line += fmt.Sprintf(" (resolved: %s)", f.Resolution)
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
