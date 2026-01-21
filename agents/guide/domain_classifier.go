package guide

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/domain/cache"
	"github.com/adalundhe/sylk/core/domain/classifier"
)

// DomainClassifier integrates the domain classification cascade with the Guide agent.
// It provides high-level domain detection for routing queries to appropriate agents.
type DomainClassifier struct {
	cascade *classifier.ClassificationCascade
	cache   *cache.DomainCache
	keyGen  *cache.KeyGenerator
	config  *domain.DomainConfig
	enabled bool
	mu      sync.RWMutex
}

// DomainClassifierConfig configures the domain classifier.
type DomainClassifierConfig struct {
	Config         *domain.DomainConfig
	CacheConfig    *cache.CacheConfig
	EnableCache    bool
	CacheNamespace string
}

// NewDomainClassifier creates a new DomainClassifier.
func NewDomainClassifier(config *DomainClassifierConfig) (*DomainClassifier, error) {
	if config == nil {
		config = defaultDomainClassifierConfig()
	}

	domainConfig := config.Config
	if domainConfig == nil {
		domainConfig = domain.DefaultDomainConfig()
	}

	dc := &DomainClassifier{
		config:  domainConfig,
		enabled: true,
	}

	dc.cascade = dc.buildCascade(domainConfig)

	if config.EnableCache {
		if err := dc.initCache(config); err != nil {
			return nil, err
		}
	}

	return dc, nil
}

func defaultDomainClassifierConfig() *DomainClassifierConfig {
	return &DomainClassifierConfig{
		Config:      domain.DefaultDomainConfig(),
		EnableCache: true,
	}
}

func (dc *DomainClassifier) buildCascade(config *domain.DomainConfig) *classifier.ClassificationCascade {
	cascade := classifier.NewClassificationCascade(config, nil)

	lexical := classifier.NewLexicalClassifier(config)
	cascade.AddStage(lexical)

	return cascade
}

func (dc *DomainClassifier) initCache(config *DomainClassifierConfig) error {
	c, err := cache.NewDomainCache(config.CacheConfig)
	if err != nil {
		return err
	}
	dc.cache = c
	dc.keyGen = cache.NewKeyGenerator(config.CacheNamespace)
	return nil
}

// Classify classifies a query and returns the domain context.
func (dc *DomainClassifier) Classify(
	ctx context.Context,
	query string,
	sessionID string,
) (*domain.DomainContext, error) {
	dc.mu.RLock()
	if !dc.enabled {
		dc.mu.RUnlock()
		return dc.disabledResult(query), nil
	}
	dc.mu.RUnlock()

	if dc.cache != nil {
		if cached := dc.checkCache(query, sessionID); cached != nil {
			return cached, nil
		}
	}

	result, err := dc.cascade.Classify(ctx, query, concurrency.AgentGuide)
	if err != nil {
		return nil, err
	}

	if dc.cache != nil {
		dc.cacheResult(query, sessionID, result)
	}

	return result, nil
}

func (dc *DomainClassifier) disabledResult(query string) *domain.DomainContext {
	ctx := domain.NewDomainContext(query)
	ctx.ClassificationMethod = "disabled"
	return ctx
}

func (dc *DomainClassifier) checkCache(query, sessionID string) *domain.DomainContext {
	key := dc.generateKey(query, sessionID)
	if cached, found := dc.cache.Get(key); found {
		return cached
	}
	return nil
}

func (dc *DomainClassifier) generateKey(query, sessionID string) string {
	if sessionID != "" {
		return dc.keyGen.GenerateWithContext(query, sessionID)
	}
	return dc.keyGen.Generate(query)
}

func (dc *DomainClassifier) cacheResult(query, sessionID string, result *domain.DomainContext) {
	key := dc.generateKey(query, sessionID)
	dc.cache.Set(key, result)
}

// ClassifyWithCaller classifies with a specific caller agent type.
func (dc *DomainClassifier) ClassifyWithCaller(
	ctx context.Context,
	query string,
	caller concurrency.AgentType,
) (*domain.DomainContext, error) {
	dc.mu.RLock()
	if !dc.enabled {
		dc.mu.RUnlock()
		return dc.disabledResult(query), nil
	}
	dc.mu.RUnlock()

	return dc.cascade.Classify(ctx, query, caller)
}

// AddEmbeddingClassifier adds the embedding classifier stage.
func (dc *DomainClassifier) AddEmbeddingClassifier(embedder classifier.EmbeddingProvider, threshold float64) {
	embedding := classifier.NewEmbeddingClassifier(embedder, threshold)
	dc.cascade.AddStage(embedding)
}

// AddContextClassifier adds the session context classifier stage.
func (dc *DomainClassifier) AddContextClassifier(provider classifier.SessionHistoryProvider) {
	contextClassifier := classifier.NewContextClassifier(provider, nil)
	dc.cascade.AddStage(contextClassifier)
}

// AddLLMClassifier adds the LLM fallback classifier stage.
func (dc *DomainClassifier) AddLLMClassifier(provider classifier.LLMProvider) {
	llmClassifier := classifier.NewLLMClassifier(provider, nil)
	dc.cascade.AddStage(llmClassifier)
}

// Enable enables the domain classifier.
func (dc *DomainClassifier) Enable() {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.enabled = true
}

// Disable disables the domain classifier.
func (dc *DomainClassifier) Disable() {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.enabled = false
}

// IsEnabled returns whether the classifier is enabled.
func (dc *DomainClassifier) IsEnabled() bool {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.enabled
}

// CacheStats returns cache statistics if caching is enabled.
func (dc *DomainClassifier) CacheStats() *cache.CacheStats {
	if dc.cache == nil {
		return nil
	}
	return dc.cache.Stats()
}

// ClearCache clears the classification cache.
func (dc *DomainClassifier) ClearCache() {
	if dc.cache != nil {
		dc.cache.Clear()
	}
}

// WaitForCache waits for pending cache writes to complete.
func (dc *DomainClassifier) WaitForCache() {
	if dc.cache != nil {
		dc.cache.Wait()
	}
}

// Close closes the domain classifier and releases resources.
func (dc *DomainClassifier) Close() {
	if dc.cache != nil {
		dc.cache.Close()
	}
}

// GetConfig returns the domain configuration.
func (dc *DomainClassifier) GetConfig() *domain.DomainConfig {
	return dc.config
}

// StageCount returns the number of classification stages.
func (dc *DomainClassifier) StageCount() int {
	return dc.cascade.StageCount()
}
