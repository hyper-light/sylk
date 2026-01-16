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
		store:         cfg.Store,
		archive:       cfg.Archive,
		factExtractor: NewFactExtractor(sessionID),
		summaryGenerator: NewSummaryGenerator(SummaryGeneratorConfig{
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
	result := c.newCompactionResult()

	entries := c.getCompactionCandidates()
	if !c.hasMinEntries(entries) {
		return c.finalizeCompaction(result, startTime)
	}

	result.EntriesProcessed = len(entries)
	facts := c.extractFacts(entries)
	result.FactsExtracted = facts

	if err := c.saveFactsIfAvailable(facts); err != nil {
		return c.failCompaction(result, startTime, fmt.Errorf("failed to save facts: %w", err))
	}

	summaries, tokens := c.generateSpanSummaries(ctx, entries, c.minEntriesForSpan)
	result.SummariesGenerated = summaries
	result.TokensReclaimed += tokens

	c.archiveEntriesIfEnabled(entries, result)

	return c.finalizeCompaction(result, startTime)
}

func (c *Compactor) newCompactionResult() *CompactionResult {
	return &CompactionResult{}
}

func (c *Compactor) hasMinEntries(entries []*Entry) bool {
	return len(entries) >= c.minEntriesForSpan
}

func (c *Compactor) extractFacts(entries []*Entry) *ExtractedFacts {
	facts := c.factExtractor.ExtractAll(entries)
	facts.FileChanges = c.factExtractor.ExtractFileChanges(entries)
	return facts
}

func (c *Compactor) saveFactsIfAvailable(facts *ExtractedFacts) error {
	if c.archive == nil {
		return nil
	}
	return c.saveFacts(facts)
}

func (c *Compactor) generateSpanSummaries(ctx context.Context, entries []*Entry, minEntries int) ([]*CompactedSummary, int) {
	spans := c.summaryGenerator.CompactEntries(entries)
	sessionID := c.store.GetCurrentSession().ID
	var summaries []*CompactedSummary
	var tokensReclaimed int

	for _, span := range spans {
		if len(span) < minEntries {
			continue
		}
		summary, err := c.summaryGenerator.GenerateSpanSummary(ctx, span, sessionID)
		if err != nil {
			continue
		}
		summaries = append(summaries, summary)
		tokensReclaimed += spanTokens(span) - summary.TokensEstimate
		c.saveSummaryIfAvailable(summary)
	}

	return summaries, tokensReclaimed
}

func spanTokens(entries []*Entry) int {
	total := 0
	for _, entry := range entries {
		total += entry.TokensEstimate
	}
	return total
}

func (c *Compactor) saveSummaryIfAvailable(summary *CompactedSummary) {
	if c.archive == nil {
		return
	}
	_ = c.archive.SaveSummary(summary)
}

func (c *Compactor) archiveEntriesIfEnabled(entries []*Entry, result *CompactionResult) {
	if !c.autoArchiveComplete || c.archive == nil {
		return
	}
	if err := c.archive.ArchiveEntries(entries); err != nil {
		return
	}
	result.EntriesArchived = len(entries)
	c.markArchived(entries)
}

func (c *Compactor) markArchived(entries []*Entry) {
	for _, entry := range entries {
		c.store.MarkArchived(entry.ID)
	}
}

func (c *Compactor) failCompaction(result *CompactionResult, startTime time.Time, err error) *CompactionResult {
	result.Error = err
	return c.finalizeCompaction(result, startTime)
}

func (c *Compactor) finalizeCompaction(result *CompactionResult, startTime time.Time) *CompactionResult {
	result.Duration = time.Since(startTime)
	return result
}

// CompactSession performs end-of-session compaction
func (c *Compactor) CompactSession(ctx context.Context, session *Session) (*CompactionResult, error) {
	startTime := time.Now()
	result := c.newCompactionResult()

	entries, err := c.querySessionEntries(session.ID)
	if err != nil {
		return nil, err
	}

	result.EntriesProcessed = len(entries)
	facts := c.extractFacts(entries)
	result.FactsExtracted = facts

	if err := c.saveFactsIfAvailable(facts); err != nil {
		return nil, fmt.Errorf("failed to save facts: %w", err)
	}

	spanSummaries, tokens := c.generateSpanSummaries(ctx, entries, 3)
	result.SummariesGenerated = append(result.SummariesGenerated, spanSummaries...)
	result.TokensReclaimed += tokens

	c.appendSessionSummary(ctx, session, entries, spanSummaries, result)
	c.appendEntryTokens(entries, result)
	c.archiveSessionEntries(entries, result)

	result.Duration = time.Since(startTime)
	return result, nil
}

func (c *Compactor) querySessionEntries(sessionID string) ([]*Entry, error) {
	entries, err := c.store.Query(ArchiveQuery{SessionIDs: []string{sessionID}})
	if err != nil {
		return nil, fmt.Errorf("failed to query session entries: %w", err)
	}
	return entries, nil
}

func (c *Compactor) appendSessionSummary(ctx context.Context, session *Session, entries []*Entry, spanSummaries []*CompactedSummary, result *CompactionResult) {
	summary, err := c.summaryGenerator.GenerateSessionSummary(ctx, session, entries, spanSummaries)
	if err != nil {
		return
	}
	result.SummariesGenerated = append(result.SummariesGenerated, summary)
	c.saveSummaryIfAvailable(summary)
}

func (c *Compactor) appendEntryTokens(entries []*Entry, result *CompactionResult) {
	for _, entry := range entries {
		result.TokensReclaimed += entry.TokensEstimate
	}
	for _, summary := range result.SummariesGenerated {
		result.TokensReclaimed -= summary.TokensEstimate
	}
}

func (c *Compactor) archiveSessionEntries(entries []*Entry, result *CompactionResult) {
	if c.archive == nil {
		return
	}
	if err := c.archive.ArchiveEntries(entries); err != nil {
		return
	}
	result.EntriesArchived = len(entries)
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
	savers := []func(*ExtractedFacts) error{
		c.saveDecisionFacts,
		c.savePatternFacts,
		c.saveFailureFacts,
		c.saveFileChangeFacts,
	}
	return runFactSavers(facts, savers)
}

func runFactSavers(facts *ExtractedFacts, savers []func(*ExtractedFacts) error) error {
	for _, saver := range savers {
		if err := saver(facts); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compactor) saveDecisionFacts(facts *ExtractedFacts) error {
	return saveFactDecisions(c.archive, facts.Decisions)
}

func (c *Compactor) savePatternFacts(facts *ExtractedFacts) error {
	return saveFactPatterns(c.archive, facts.Patterns)
}

func (c *Compactor) saveFailureFacts(facts *ExtractedFacts) error {
	return saveFactFailures(c.archive, facts.Failures)
}

func (c *Compactor) saveFileChangeFacts(facts *ExtractedFacts) error {
	return saveFactFileChanges(c.archive, facts.FileChanges)
}

func saveFactDecisions(archive *Archive, decisions []*FactDecision) error {
	for _, decision := range decisions {
		if err := archive.SaveFactDecision(decision); err != nil {
			return err
		}
	}
	return nil
}

func saveFactPatterns(archive *Archive, patterns []*FactPattern) error {
	for _, pattern := range patterns {
		if err := archive.SaveFactPattern(pattern); err != nil {
			return err
		}
	}
	return nil
}

func saveFactFailures(archive *Archive, failures []*FactFailure) error {
	for _, failure := range failures {
		if err := archive.SaveFactFailure(failure); err != nil {
			return err
		}
	}
	return nil
}

func saveFactFileChanges(archive *Archive, changes []*FactFileChange) error {
	for _, change := range changes {
		if err := archive.SaveFactFileChange(change); err != nil {
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

	result := newRetrievalContext(query, maxTokens)
	budget := newTokenBudget(maxTokens)

	c.addSummaries(result, query, budget)
	c.addDecisionFacts(result, budget)
	c.addPatternFacts(result, budget)
	c.addFailureFacts(result, budget)
	c.addRecentEntries(result, query, budget)

	result.TotalTokens = budget.tokens
	return result, nil
}

type tokenBudget struct {
	max    int
	tokens int
}

func newTokenBudget(maxTokens int) *tokenBudget {
	return &tokenBudget{max: maxTokens}
}

func (b *tokenBudget) canAdd(tokens int) bool {
	return b.tokens+tokens <= b.max
}

func (b *tokenBudget) add(tokens int) {
	b.tokens += tokens
}

func newRetrievalContext(query string, maxTokens int) *RetrievalContext {
	return &RetrievalContext{
		Query:     query,
		MaxTokens: maxTokens,
	}
}

func (c *Compactor) addSummaries(result *RetrievalContext, query string, budget *tokenBudget) {
	summaries, err := c.archive.SearchSummaries(query, 10)
	if err != nil {
		return
	}
	for _, summary := range summaries {
		if !budget.canAdd(summary.TokensEstimate) {
			return
		}
		result.Summaries = append(result.Summaries, summary)
		budget.add(summary.TokensEstimate)
	}
}

func (c *Compactor) addDecisionFacts(result *RetrievalContext, budget *tokenBudget) {
	decisions, _ := c.archive.QueryFactDecisions("", 20)
	for _, decision := range decisions {
		tokens := EstimateTokens(decision.Choice + decision.Rationale)
		if !budget.canAdd(tokens) {
			return
		}
		result.Decisions = append(result.Decisions, decision)
		budget.add(tokens)
	}
}

func (c *Compactor) addPatternFacts(result *RetrievalContext, budget *tokenBudget) {
	patterns, _ := c.archive.QueryFactPatterns("", 20)
	for _, pattern := range patterns {
		tokens := EstimateTokens(pattern.Pattern + pattern.Example)
		if !budget.canAdd(tokens) {
			return
		}
		result.Patterns = append(result.Patterns, pattern)
		budget.add(tokens)
	}
}

func (c *Compactor) addFailureFacts(result *RetrievalContext, budget *tokenBudget) {
	failures, _ := c.archive.QueryFactFailures("", 20)
	for _, failure := range failures {
		tokens := EstimateTokens(failure.Approach + failure.Reason + failure.Resolution)
		if !budget.canAdd(tokens) {
			return
		}
		result.Failures = append(result.Failures, failure)
		budget.add(tokens)
	}
}

func (c *Compactor) addRecentEntries(result *RetrievalContext, query string, budget *tokenBudget) {
	if budget.tokens >= budget.max {
		return
	}
	entries, _ := c.archive.SearchText(query, 20)
	for _, entry := range entries {
		if !budget.canAdd(entry.TokensEstimate) {
			return
		}
		result.Entries = append(result.Entries, entry)
		budget.add(entry.TokensEstimate)
	}
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
	appendSummaryContext(&sb, rc.Summaries)
	appendDecisionContext(&sb, rc.Decisions)
	appendPatternContext(&sb, rc.Patterns)
	appendFailureContext(&sb, rc.Failures)
	return sb.String()
}

func appendSummaryContext(sb *strings.Builder, summaries []*CompactedSummary) {
	if len(summaries) == 0 {
		return
	}
	sb.WriteString("=== Previous Context ===\n\n")
	for _, summary := range summaries {
		sb.WriteString(fmt.Sprintf("[%s Summary]\n%s\n\n", summary.Level, summary.Content))
	}
}

func appendDecisionContext(sb *strings.Builder, decisions []*FactDecision) {
	if len(decisions) == 0 {
		return
	}
	sb.WriteString("=== Key Decisions ===\n")
	for _, decision := range decisions {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", decision.Choice, decision.Rationale))
	}
	sb.WriteString("\n")
}

func appendPatternContext(sb *strings.Builder, patterns []*FactPattern) {
	if len(patterns) == 0 {
		return
	}
	sb.WriteString("=== Established Patterns ===\n")
	for _, pattern := range patterns {
		sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", pattern.Category, pattern.Name, pattern.Pattern))
	}
	sb.WriteString("\n")
}

func appendFailureContext(sb *strings.Builder, failures []*FactFailure) {
	if len(failures) == 0 {
		return
	}
	sb.WriteString("=== Learned Failures ===\n")
	for _, failure := range failures {
		line := failureSummaryLine(failure)
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
}

func failureSummaryLine(failure *FactFailure) string {
	line := fmt.Sprintf("- %s: %s", failure.Approach, failure.Reason)
	if failure.Resolution == "" {
		return line
	}
	return line + fmt.Sprintf(" (resolved: %s)", failure.Resolution)
}
