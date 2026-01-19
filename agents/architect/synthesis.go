package architect

import (
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/messaging"
)

// UnifiedResult represents the synthesized output from multiple domain results.
type UnifiedResult struct {
	Content         string                    `json:"content"`
	Sources         []SourceAttribution       `json:"sources"`
	Deduplication   DeduplicationSummary      `json:"deduplication"`
	Conflicts       []messaging.ConflictInfo  `json:"conflicts,omitempty"`
	DomainCoverage  map[domain.Domain]float64 `json:"domain_coverage"`
	SynthesisMethod string                    `json:"synthesis_method"`
	SynthesizedAt   time.Time                 `json:"synthesized_at"`
}

// SourceAttribution tracks the origin domain for each piece of content.
type SourceAttribution struct {
	Domain     domain.Domain `json:"domain"`
	SourceID   string        `json:"source_id"`
	Content    string        `json:"content"`
	Score      float64       `json:"score"`
	StartIndex int           `json:"start_index"`
	EndIndex   int           `json:"end_index"`
}

// DeduplicationSummary tracks what was removed during deduplication.
type DeduplicationSummary struct {
	OriginalCount  int      `json:"original_count"`
	FinalCount     int      `json:"final_count"`
	RemovedCount   int      `json:"removed_count"`
	RemovedReasons []string `json:"removed_reasons,omitempty"`
}

// ResultSynthesizer combines and deduplicates multi-domain results.
type ResultSynthesizer struct {
	similarityThreshold float64
	conflictThreshold   float64
	maxContentLength    int
}

// SynthesizerConfig configures the ResultSynthesizer.
type SynthesizerConfig struct {
	SimilarityThreshold float64
	ConflictThreshold   float64
	MaxContentLength    int
}

// NewResultSynthesizer creates a new synthesizer with the given config.
func NewResultSynthesizer(config *SynthesizerConfig) *ResultSynthesizer {
	cfg := applySynthesizerDefaults(config)
	return &ResultSynthesizer{
		similarityThreshold: cfg.SimilarityThreshold,
		conflictThreshold:   cfg.ConflictThreshold,
		maxContentLength:    cfg.MaxContentLength,
	}
}

func applySynthesizerDefaults(config *SynthesizerConfig) *SynthesizerConfig {
	defaults := &SynthesizerConfig{
		SimilarityThreshold: 0.8,
		ConflictThreshold:   0.3,
		MaxContentLength:    10000,
	}

	if config == nil {
		return defaults
	}

	if config.SimilarityThreshold > 0 {
		defaults.SimilarityThreshold = config.SimilarityThreshold
	}
	if config.ConflictThreshold > 0 {
		defaults.ConflictThreshold = config.ConflictThreshold
	}
	if config.MaxContentLength > 0 {
		defaults.MaxContentLength = config.MaxContentLength
	}

	return defaults
}

// SynthesizeResults combines results from multiple domains into a unified result.
func (s *ResultSynthesizer) SynthesizeResults(results []DomainResult) *UnifiedResult {
	if len(results) == 0 {
		return s.emptyResult()
	}

	successful := filterSuccessful(results)
	if len(successful) == 0 {
		return s.emptyResult()
	}

	deduplicated, summary := s.deduplicateResults(successful)
	conflicts := s.detectConflicts(deduplicated)
	sources := s.buildSourceAttributions(deduplicated)
	coverage := s.calculateCoverage(deduplicated)
	content := s.mergeContent(deduplicated)

	return &UnifiedResult{
		Content:         content,
		Sources:         sources,
		Deduplication:   summary,
		Conflicts:       conflicts,
		DomainCoverage:  coverage,
		SynthesisMethod: s.determineSynthesisMethod(deduplicated),
		SynthesizedAt:   time.Now(),
	}
}

func (s *ResultSynthesizer) emptyResult() *UnifiedResult {
	return &UnifiedResult{
		Sources:        []SourceAttribution{},
		DomainCoverage: make(map[domain.Domain]float64),
		Deduplication:  DeduplicationSummary{},
		SynthesizedAt:  time.Now(),
	}
}

func filterSuccessful(results []DomainResult) []DomainResult {
	successful := make([]DomainResult, 0, len(results))
	for _, r := range results {
		if r.Error == nil && r.ErrorMsg == "" && r.Content != "" {
			successful = append(successful, r)
		}
	}
	return successful
}

func (s *ResultSynthesizer) deduplicateResults(results []DomainResult) ([]DomainResult, DeduplicationSummary) {
	summary := DeduplicationSummary{
		OriginalCount: len(results),
	}

	if len(results) <= 1 {
		summary.FinalCount = len(results)
		return results, summary
	}

	sortByScore(results)
	deduplicated := make([]DomainResult, 0, len(results))
	removed := make([]string, 0)

	for _, r := range results {
		if s.isDuplicate(r, deduplicated) {
			removed = append(removed, "duplicate from "+r.Domain.String())
			continue
		}
		deduplicated = append(deduplicated, r)
	}

	summary.FinalCount = len(deduplicated)
	summary.RemovedCount = summary.OriginalCount - summary.FinalCount
	summary.RemovedReasons = removed

	return deduplicated, summary
}

func sortByScore(results []DomainResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}

func (s *ResultSynthesizer) isDuplicate(candidate DomainResult, existing []DomainResult) bool {
	for _, e := range existing {
		if s.isSimilar(candidate.Content, e.Content) {
			return true
		}
	}
	return false
}

func (s *ResultSynthesizer) isSimilar(a, b string) bool {
	if a == b {
		return true
	}

	similarity := jaccardSimilarity(a, b)
	return similarity >= s.similarityThreshold
}

func jaccardSimilarity(a, b string) float64 {
	wordsA := tokenize(a)
	wordsB := tokenize(b)

	if len(wordsA) == 0 && len(wordsB) == 0 {
		return 1.0
	}

	intersection := 0
	setB := makeSet(wordsB)
	for _, w := range wordsA {
		if setB[w] {
			intersection++
		}
	}

	union := len(wordsA) + len(wordsB) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	words := strings.Fields(s)
	return words
}

func makeSet(words []string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

func (s *ResultSynthesizer) detectConflicts(results []DomainResult) []messaging.ConflictInfo {
	if len(results) < 2 {
		return nil
	}

	conflicts := make([]messaging.ConflictInfo, 0)

	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if conflict := s.checkConflict(results[i], results[j]); conflict != nil {
				conflicts = append(conflicts, *conflict)
			}
		}
	}

	return conflicts
}

func (s *ResultSynthesizer) checkConflict(a, b DomainResult) *messaging.ConflictInfo {
	similarity := jaccardSimilarity(a.Content, b.Content)

	inConflictRange := similarity > s.conflictThreshold && similarity < s.similarityThreshold
	if !inConflictRange {
		return nil
	}

	return &messaging.ConflictInfo{
		Domains:     []domain.Domain{a.Domain, b.Domain},
		Description: "potentially conflicting information between domains",
		Severity:    s.determineSeverity(similarity),
	}
}

func (s *ResultSynthesizer) determineSeverity(similarity float64) messaging.ConflictSeverity {
	midpoint := (s.conflictThreshold + s.similarityThreshold) / 2
	if similarity < midpoint {
		return messaging.SeverityHigh
	}
	return messaging.SeverityMedium
}

func (s *ResultSynthesizer) buildSourceAttributions(results []DomainResult) []SourceAttribution {
	sources := make([]SourceAttribution, 0, len(results))
	currentIndex := 0

	for _, r := range results {
		contentLen := len(r.Content)
		sources = append(sources, SourceAttribution{
			Domain:     r.Domain,
			SourceID:   r.Source,
			Content:    r.Content,
			Score:      r.Score,
			StartIndex: currentIndex,
			EndIndex:   currentIndex + contentLen,
		})
		currentIndex += contentLen + 2
	}

	return sources
}

func (s *ResultSynthesizer) calculateCoverage(results []DomainResult) map[domain.Domain]float64 {
	coverage := make(map[domain.Domain]float64)
	if len(results) == 0 {
		return coverage
	}

	totalScore := 0.0
	for _, r := range results {
		totalScore += r.Score
	}

	if totalScore == 0 {
		for _, r := range results {
			coverage[r.Domain] = 1.0 / float64(len(results))
		}
		return coverage
	}

	for _, r := range results {
		coverage[r.Domain] += r.Score / totalScore
	}

	return coverage
}

func (s *ResultSynthesizer) mergeContent(results []DomainResult) string {
	if len(results) == 0 {
		return ""
	}

	if len(results) == 1 {
		return s.truncateContent(results[0].Content)
	}

	var builder strings.Builder
	for i, r := range results {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString("[")
		builder.WriteString(r.Domain.String())
		builder.WriteString("] ")
		builder.WriteString(r.Content)

		if builder.Len() > s.maxContentLength {
			break
		}
	}

	return s.truncateContent(builder.String())
}

func (s *ResultSynthesizer) truncateContent(content string) string {
	if len(content) <= s.maxContentLength {
		return content
	}
	return content[:s.maxContentLength] + "..."
}

func (s *ResultSynthesizer) determineSynthesisMethod(results []DomainResult) string {
	if len(results) == 0 {
		return "empty"
	}
	if len(results) == 1 {
		return "single_domain"
	}

	domains := make(map[domain.Domain]bool)
	for _, r := range results {
		domains[r.Domain] = true
	}

	if len(domains) == 1 {
		return "single_domain_multi_result"
	}

	return "multi_domain_merge"
}

// SynthesizeFromCrossDomain synthesizes results from a CrossDomainResult.
func (s *ResultSynthesizer) SynthesizeFromCrossDomain(result *CrossDomainResult) *UnifiedResult {
	if result == nil {
		return s.emptyResult()
	}
	return s.SynthesizeResults(result.DomainResults)
}

// SimilarityThreshold returns the configured similarity threshold.
func (s *ResultSynthesizer) SimilarityThreshold() float64 {
	return s.similarityThreshold
}

// ConflictThreshold returns the configured conflict threshold.
func (s *ResultSynthesizer) ConflictThreshold() float64 {
	return s.conflictThreshold
}

// MaxContentLength returns the configured max content length.
func (s *ResultSynthesizer) MaxContentLength() int {
	return s.maxContentLength
}
