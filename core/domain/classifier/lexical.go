package classifier

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

const (
	lexicalPriority       = 10
	lexicalMethodName     = "lexical"
	highConfidenceExit    = 0.85
	maxKeywordsPerPattern = 5
)

type LexicalClassifier struct {
	config   *domain.DomainConfig
	patterns map[domain.Domain][]*regexp.Regexp
	mu       sync.RWMutex
}

func NewLexicalClassifier(config *domain.DomainConfig) *LexicalClassifier {
	lc := &LexicalClassifier{
		config:   config,
		patterns: make(map[domain.Domain][]*regexp.Regexp),
	}
	lc.compilePatterns()
	return lc
}

func (l *LexicalClassifier) compilePatterns() {
	if l.config == nil || l.config.LexicalKeywords == nil {
		return
	}

	for d, keywords := range l.config.LexicalKeywords {
		l.patterns[d] = compileKeywordPatterns(keywords)
	}
}

func compileKeywordPatterns(keywords []string) []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, 0, len(keywords))
	for _, kw := range keywords {
		escaped := regexp.QuoteMeta(strings.ToLower(kw))
		pattern := `(?i)\b` + escaped + `\b`
		re, err := regexp.Compile(pattern)
		if err == nil {
			patterns = append(patterns, re)
		}
	}
	return patterns
}

func (l *LexicalClassifier) Classify(
	ctx context.Context,
	query string,
	_ concurrency.AgentType,
) (*StageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := NewStageResult()
	result.SetMethod(lexicalMethodName)

	if query == "" {
		return result, nil
	}

	queryLower := strings.ToLower(query)
	l.mu.RLock()
	defer l.mu.RUnlock()

	scores := l.scoreAllDomains(queryLower)
	l.populateResult(result, scores)

	if l.shouldTerminate(result) {
		result.MarkComplete()
	}

	return result, nil
}

func (l *LexicalClassifier) scoreAllDomains(query string) map[domain.Domain]domainScore {
	scores := make(map[domain.Domain]domainScore)

	for d, patterns := range l.patterns {
		matches := countMatches(query, patterns)
		if matches > 0 {
			scores[d] = domainScore{
				confidence: calculateConfidence(matches, len(patterns)),
				matches:    matches,
			}
		}
	}

	return scores
}

type domainScore struct {
	confidence float64
	matches    int
}

func countMatches(query string, patterns []*regexp.Regexp) int {
	count := 0
	for _, p := range patterns {
		if p.MatchString(query) {
			count++
		}
	}
	return count
}

func calculateConfidence(matches, totalPatterns int) float64 {
	if totalPatterns == 0 {
		return 0
	}

	baseScore := float64(matches) / float64(totalPatterns)
	return normalizeScore(baseScore, matches)
}

func normalizeScore(base float64, matches int) float64 {
	bonus := float64(matches) * 0.05
	score := base + bonus
	if score > 1.0 {
		return 1.0
	}
	return score
}

func (l *LexicalClassifier) populateResult(
	result *StageResult,
	scores map[domain.Domain]domainScore,
) {
	for d, s := range scores {
		signals := l.getMatchedKeywords(d, s.matches)
		result.AddDomain(d, s.confidence, signals)
	}
}

func (l *LexicalClassifier) getMatchedKeywords(d domain.Domain, maxSignals int) []string {
	if l.config == nil || l.config.LexicalKeywords == nil {
		return nil
	}

	keywords := l.config.LexicalKeywords[d]
	limit := maxSignals
	if limit > maxKeywordsPerPattern {
		limit = maxKeywordsPerPattern
	}
	if limit > len(keywords) {
		limit = len(keywords)
	}

	return keywords[:limit]
}

func (l *LexicalClassifier) shouldTerminate(result *StageResult) bool {
	if result.DomainCount() != 1 {
		return false
	}

	_, maxConf := result.HighestConfidence()
	return maxConf >= highConfidenceExit
}

func (l *LexicalClassifier) Name() string {
	return lexicalMethodName
}

func (l *LexicalClassifier) Priority() int {
	return lexicalPriority
}

func (l *LexicalClassifier) UpdateConfig(config *domain.DomainConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.config = config
	l.patterns = make(map[domain.Domain][]*regexp.Regexp)
	l.compilePatterns()
}
