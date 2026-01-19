package classifier

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

const (
	llmPriority        = 30
	llmMethodName      = "llm"
	defaultLLMTimeout  = 500 * time.Millisecond
	llmMinConfidence   = 0.50
	llmCacheDefaultTTL = 5 * time.Minute
)

// LLMClassificationResponse represents the expected JSON response from LLM.
type LLMClassificationResponse struct {
	Domains []LLMDomainScore `json:"domains"`
}

// LLMDomainScore represents a domain classification from LLM.
type LLMDomainScore struct {
	Domain     string  `json:"domain"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning,omitempty"`
}

// LLMProvider executes LLM classification requests.
type LLMProvider interface {
	ClassifyDomain(ctx context.Context, query string, prompt string) (string, error)
}

// LLMClassifier uses LLM inference for domain classification.
type LLMClassifier struct {
	provider LLMProvider
	timeout  time.Duration
	prompt   string
	cache    map[string]*cachedResult
	cacheTTL time.Duration
	mu       sync.RWMutex
}

type cachedResult struct {
	result    *StageResult
	expiresAt time.Time
}

// LLMClassifierConfig configures the LLM classifier.
type LLMClassifierConfig struct {
	Timeout  time.Duration
	Prompt   string
	CacheTTL time.Duration
}

// NewLLMClassifier creates a new LLMClassifier.
func NewLLMClassifier(
	provider LLMProvider,
	config *LLMClassifierConfig,
) *LLMClassifier {
	lc := &LLMClassifier{
		provider: provider,
		timeout:  defaultLLMTimeout,
		prompt:   defaultClassificationPrompt(),
		cache:    make(map[string]*cachedResult),
		cacheTTL: llmCacheDefaultTTL,
	}

	if config != nil {
		lc.applyConfig(config)
	}

	return lc
}

func (l *LLMClassifier) applyConfig(config *LLMClassifierConfig) {
	if config.Timeout > 0 {
		l.timeout = config.Timeout
	}
	if config.Prompt != "" {
		l.prompt = config.Prompt
	}
	if config.CacheTTL > 0 {
		l.cacheTTL = config.CacheTTL
	}
}

func defaultClassificationPrompt() string {
	return `Classify the user query into one or more domains.
Return JSON with domains and confidence scores (0-1).

Available domains:
- librarian: Questions about THIS codebase, finding code, understanding our implementation
- academic: Questions about best practices, documentation, standards, external knowledge
- archivalist: Questions about past decisions, session history, what we did before
- architect: Questions about planning, task breakdown, workflow design
- engineer: Requests to implement, fix, or modify code
- designer: Questions about UI/UX, components, styling
- inspector: Requests to review or validate code quality
- tester: Questions about testing, coverage, test cases
- orchestrator: Questions about coordination, parallel tasks, pipelines
- guide: General help, clarification, routing queries

Format: {"domains": [{"domain": "...", "confidence": 0.X, "reasoning": "..."}]}`
}

func (l *LLMClassifier) Classify(
	ctx context.Context,
	query string,
	_ concurrency.AgentType,
) (*StageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := NewStageResult()
	result.SetMethod(llmMethodName)

	if query == "" || l.provider == nil {
		return result, nil
	}

	if cached := l.checkCache(query); cached != nil {
		return cached.Clone(), nil
	}

	llmCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	response, err := l.provider.ClassifyDomain(llmCtx, query, l.prompt)
	if err != nil {
		return result, nil // Non-fatal: return empty result
	}

	l.parseResponse(result, response)
	l.cacheResult(query, result)

	return result, nil
}

func (l *LLMClassifier) checkCache(query string) *StageResult {
	l.mu.RLock()
	defer l.mu.RUnlock()

	key := normalizeQuery(query)
	cached, ok := l.cache[key]
	if !ok || time.Now().After(cached.expiresAt) {
		return nil
	}
	return cached.result
}

func normalizeQuery(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}

func (l *LLMClassifier) cacheResult(query string, result *StageResult) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := normalizeQuery(query)
	l.cache[key] = &cachedResult{
		result:    result.Clone(),
		expiresAt: time.Now().Add(l.cacheTTL),
	}
}

func (l *LLMClassifier) parseResponse(result *StageResult, response string) {
	var parsed LLMClassificationResponse
	if err := json.Unmarshal([]byte(response), &parsed); err != nil {
		return
	}

	for _, ds := range parsed.Domains {
		d, ok := domain.ParseDomain(ds.Domain)
		if !ok || ds.Confidence < llmMinConfidence {
			continue
		}
		signals := l.buildSignals(ds)
		result.AddDomain(d, ds.Confidence, signals)
	}
}

func (l *LLMClassifier) buildSignals(ds LLMDomainScore) []string {
	signals := []string{"llm_inference"}
	if ds.Reasoning != "" {
		signals = append(signals, ds.Reasoning)
	}
	return signals
}

func (l *LLMClassifier) Name() string {
	return llmMethodName
}

func (l *LLMClassifier) Priority() int {
	return llmPriority
}

// UpdateConfig updates the classifier configuration.
func (l *LLMClassifier) UpdateConfig(config *LLMClassifierConfig) {
	if config == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.applyConfig(config)
}

// ClearCache removes all cached results.
func (l *LLMClassifier) ClearCache() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cache = make(map[string]*cachedResult)
}

// CacheSize returns the number of cached results.
func (l *LLMClassifier) CacheSize() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.cache)
}

// GetTimeout returns the current LLM timeout.
func (l *LLMClassifier) GetTimeout() time.Duration {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.timeout
}

// SetPrompt updates the classification prompt.
func (l *LLMClassifier) SetPrompt(prompt string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prompt = prompt
}
