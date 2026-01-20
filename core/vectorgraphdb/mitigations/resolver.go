package mitigations

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

type UnifiedResolver struct {
	db          *vectorgraphdb.VectorGraphDB
	hnsw        HNSWSimilaritySearcher
	embedder    Embedder
	intentCache *IntentCache
	firewall    *HallucinationFirewall
	freshness   *FreshnessTracker
	provenance  *ProvenanceTracker
	trust       *TrustHierarchy
	conflicts   *ConflictDetector
	scorer      *ContextQualityScorer
	prompter    *LLMContextBuilder
}

type ResolveOptions struct {
	Domains          []vectorgraphdb.Domain
	TokenBudget      int
	MinTrust         TrustLevel
	MinFreshness     time.Duration
	IncludeConflicts bool
	MaxGraphDepth    int
	CacheResult      bool
}

func DefaultResolveOptions() ResolveOptions {
	return ResolveOptions{
		TokenBudget:      4000,
		MinTrust:         TrustLLMInference,
		MinFreshness:     30 * 24 * time.Hour,
		IncludeConflicts: true,
		MaxGraphDepth:    2,
		CacheResult:      true,
	}
}

type QueryResolution struct {
	Query       string
	Context     *LLMContext
	Items       []*ContextItem
	Conflicts   []*Conflict
	Metrics     *PipelineMetrics
	TokensUsed  int
	TokenBudget int
	CacheHit    bool
}

type PipelineMetrics struct {
	IntentDetect time.Duration
	CacheCheck   time.Duration
	Embedding    time.Duration
	VectorSearch time.Duration
	GraphExpand  time.Duration
	Freshness    time.Duration
	Trust        time.Duration
	Conflict     time.Duration
	Scoring      time.Duration
	ContextBuild time.Duration
	Total        time.Duration
}

type IntentCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	maxSize int
}

type cacheEntry struct {
	resolution *QueryResolution
	createdAt  time.Time
}

func NewIntentCache(ttl time.Duration, maxSize int) *IntentCache {
	return &IntentCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

func (c *IntentCache) Get(query string) (*QueryResolution, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[query]
	if !ok {
		return nil, false
	}

	if time.Since(entry.createdAt) > c.ttl {
		delete(c.entries, query)
		return nil, false
	}

	return entry.resolution, true
}

func (c *IntentCache) Set(query string, resolution *QueryResolution) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[query] = &cacheEntry{
		resolution: resolution,
		createdAt:  time.Now(),
	}
}

func (c *IntentCache) evictOldest() {
	oldestKey := c.findOldestKey()
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func (c *IntentCache) findOldestKey() string {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range c.entries {
		if oldestKey == "" || entry.createdAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.createdAt
		}
	}
	return oldestKey
}

type ResolverConfig struct {
	CacheTTL     time.Duration
	CacheMaxSize int
}

func DefaultResolverConfig() ResolverConfig {
	return ResolverConfig{
		CacheTTL:     5 * time.Minute,
		CacheMaxSize: 1000,
	}
}

// ResolverError represents an error during resolver operations.
type ResolverError struct {
	Op  string
	Err error
}

func (e *ResolverError) Error() string {
	return fmt.Sprintf("resolver %s: %v", e.Op, e.Err)
}

func (e *ResolverError) Unwrap() error {
	return e.Err
}

// NewUnifiedResolver creates a new UnifiedResolver with all dependencies.
// Returns an error if the database is nil or config is invalid.
func NewUnifiedResolver(
	db *vectorgraphdb.VectorGraphDB,
	hnsw HNSWSimilaritySearcher,
	embedder Embedder,
	config ResolverConfig,
) (*UnifiedResolver, error) {
	if err := validateResolverConfig(db, config); err != nil {
		return nil, err
	}

	freshness := NewFreshnessTracker(db, DefaultDecayConfig())
	provenance := NewProvenanceTracker(db)
	trust := NewTrustHierarchy(db, provenance, freshness)
	conflicts := NewConflictDetector(db, hnsw, DefaultConflictDetectorConfig())
	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), freshness, trust)
	prompter := NewLLMContextBuilder(scorer, trust, conflicts, provenance)

	return &UnifiedResolver{
		db:          db,
		hnsw:        hnsw,
		embedder:    embedder,
		intentCache: NewIntentCache(config.CacheTTL, config.CacheMaxSize),
		freshness:   freshness,
		provenance:  provenance,
		trust:       trust,
		conflicts:   conflicts,
		scorer:      scorer,
		prompter:    prompter,
	}, nil
}

func validateResolverConfig(db *vectorgraphdb.VectorGraphDB, config ResolverConfig) error {
	if db == nil {
		return &ResolverError{Op: "init", Err: fmt.Errorf("database is required")}
	}
	if config.CacheTTL <= 0 {
		return &ResolverError{Op: "init", Err: fmt.Errorf("cache TTL must be positive")}
	}
	if config.CacheMaxSize <= 0 {
		return &ResolverError{Op: "init", Err: fmt.Errorf("cache max size must be positive")}
	}
	return nil
}

func (r *UnifiedResolver) Resolve(ctx context.Context, query string, opts *ResolveOptions) (*QueryResolution, error) {
	opts = r.ensureOptions(opts)

	if cached, ok := r.checkCache(query, &PipelineMetrics{}); ok {
		return cached, nil
	}

	return r.resolveUncached(ctx, query, opts)
}

func (r *UnifiedResolver) ensureOptions(opts *ResolveOptions) *ResolveOptions {
	if opts == nil {
		defaultOpts := DefaultResolveOptions()
		return &defaultOpts
	}
	return opts
}

func (r *UnifiedResolver) resolveUncached(ctx context.Context, query string, opts *ResolveOptions) (*QueryResolution, error) {
	metrics := &PipelineMetrics{}
	start := time.Now()

	intent := r.detectIntent(query, metrics)

	embedding, err := r.generateEmbedding(ctx, query, metrics)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}

	searchResults := r.runSearchPipeline(embedding, opts, intent, metrics)
	conflicts := r.detectConflictsIfEnabled(ctx, searchResults, opts.IncludeConflicts, metrics)
	items, remaining := r.scoreAndSelect(searchResults, opts.TokenBudget, metrics)

	llmContext, err := r.buildContext(items, metrics)
	if err != nil {
		return nil, fmt.Errorf("build context: %w", err)
	}

	metrics.Total = time.Since(start)

	resolution := r.buildResolution(query, llmContext, items, conflicts, metrics, opts, remaining)
	r.cacheIfEnabled(query, resolution, opts.CacheResult)

	return resolution, nil
}

func (r *UnifiedResolver) runSearchPipeline(embedding []float32, opts *ResolveOptions, intent QueryIntent, metrics *PipelineMetrics) []vectorgraphdb.SearchResult {
	results := r.executeSearch(embedding, opts, intent, metrics)
	results = r.applyFreshness(results, metrics)
	return r.applyTrust(results, opts.MinTrust, metrics)
}

func (r *UnifiedResolver) buildResolution(query string, ctx *LLMContext, items []*ContextItem, conflicts []*Conflict, metrics *PipelineMetrics, opts *ResolveOptions, remaining int) *QueryResolution {
	return &QueryResolution{
		Query:       query,
		Context:     ctx,
		Items:       items,
		Conflicts:   conflicts,
		Metrics:     metrics,
		TokensUsed:  opts.TokenBudget - remaining,
		TokenBudget: opts.TokenBudget,
		CacheHit:    false,
	}
}

func (r *UnifiedResolver) cacheIfEnabled(query string, resolution *QueryResolution, cacheResult bool) {
	if cacheResult {
		r.intentCache.Set(query, resolution)
	}
}

func (r *UnifiedResolver) detectIntent(query string, metrics *PipelineMetrics) QueryIntent {
	start := time.Now()
	defer func() { metrics.IntentDetect = time.Since(start) }()

	lowerQuery := strings.ToLower(query)

	if containsAny(lowerQuery, codeKeywords) {
		return IntentCode
	}
	if containsAny(lowerQuery, historyKeywords) {
		return IntentHistory
	}
	if containsAny(lowerQuery, academicKeywords) {
		return IntentAcademic
	}
	return IntentHybrid
}

var codeKeywords = []string{"code", "function", "file", "implement"}
var historyKeywords = []string{"history", "decision", "previous", "last time"}
var academicKeywords = []string{"best practice", "documentation", "how to", "tutorial"}

func containsAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

func (r *UnifiedResolver) checkCache(query string, metrics *PipelineMetrics) (*QueryResolution, bool) {
	start := time.Now()
	defer func() { metrics.CacheCheck = time.Since(start) }()

	cached, ok := r.intentCache.Get(query)
	if ok {
		cached.CacheHit = true
		return cached, true
	}
	return nil, false
}

func (r *UnifiedResolver) generateEmbedding(ctx context.Context, query string, metrics *PipelineMetrics) ([]float32, error) {
	start := time.Now()
	defer func() { metrics.Embedding = time.Since(start) }()

	if r.embedder == nil {
		return make([]float32, 768), nil
	}

	return r.embedder.Embed(query)
}

func (r *UnifiedResolver) executeSearch(
	embedding []float32,
	opts *ResolveOptions,
	intent QueryIntent,
	metrics *PipelineMetrics,
) []vectorgraphdb.SearchResult {
	start := time.Now()
	defer func() { metrics.VectorSearch = time.Since(start) }()

	if r.hnsw == nil {
		return nil
	}

	domains := r.selectDomains(opts.Domains, intent)
	filter := &vectorgraphdb.HNSWSearchFilter{
		Domains:       domains,
		MinSimilarity: 0.5,
	}

	hnswResults := r.hnsw.Search(embedding, 50, filter)
	return r.convertResults(hnswResults)
}

var intentToDomains = map[QueryIntent][]vectorgraphdb.Domain{
	IntentCode:     {vectorgraphdb.DomainCode},
	IntentHistory:  {vectorgraphdb.DomainHistory},
	IntentAcademic: {vectorgraphdb.DomainAcademic},
}

var allDomains = []vectorgraphdb.Domain{
	vectorgraphdb.DomainCode,
	vectorgraphdb.DomainHistory,
	vectorgraphdb.DomainAcademic,
}

func (r *UnifiedResolver) selectDomains(requested []vectorgraphdb.Domain, intent QueryIntent) []vectorgraphdb.Domain {
	if len(requested) > 0 {
		return requested
	}
	if domains, ok := intentToDomains[intent]; ok {
		return domains
	}
	return allDomains
}

func (r *UnifiedResolver) convertResults(hnswResults []vectorgraphdb.HNSWSearchResult) []vectorgraphdb.SearchResult {
	ns := vectorgraphdb.NewNodeStore(r.db, nil)
	results := make([]vectorgraphdb.SearchResult, 0, len(hnswResults))

	for _, hr := range hnswResults {
		node, err := ns.GetNode(hr.ID)
		if err != nil {
			continue
		}
		results = append(results, vectorgraphdb.SearchResult{
			Node:       node,
			Similarity: hr.Similarity,
		})
	}

	return results
}

func (r *UnifiedResolver) applyFreshness(results []vectorgraphdb.SearchResult, metrics *PipelineMetrics) []vectorgraphdb.SearchResult {
	start := time.Now()
	defer func() { metrics.Freshness = time.Since(start) }()

	if r.freshness == nil {
		return results
	}

	return r.freshness.ApplyDecay(results)
}

func (r *UnifiedResolver) applyTrust(results []vectorgraphdb.SearchResult, minTrust TrustLevel, metrics *PipelineMetrics) []vectorgraphdb.SearchResult {
	start := time.Now()
	defer func() { metrics.Trust = time.Since(start) }()

	if r.trust == nil {
		return results
	}

	results = r.trust.ApplyTrust(results)
	return r.trust.FilterByMinTrust(results, minTrust)
}

func (r *UnifiedResolver) detectConflictsIfEnabled(ctx context.Context, results []vectorgraphdb.SearchResult, include bool, metrics *PipelineMetrics) []*Conflict {
	if !include {
		return nil
	}
	return r.detectConflicts(ctx, results, metrics)
}

func (r *UnifiedResolver) detectConflicts(ctx context.Context, results []vectorgraphdb.SearchResult, metrics *PipelineMetrics) []*Conflict {
	start := time.Now()
	defer func() { metrics.Conflict = time.Since(start) }()

	if r.conflicts == nil {
		return nil
	}

	return r.collectUnresolvedConflicts(results)
}

func (r *UnifiedResolver) collectUnresolvedConflicts(results []vectorgraphdb.SearchResult) []*Conflict {
	allConflicts := make([]*Conflict, 0)
	for _, result := range results {
		for _, c := range r.conflicts.GetConflictsForNode(result.Node.ID) {
			if c.Resolution == nil {
				allConflicts = append(allConflicts, c)
			}
		}
	}
	return allConflicts
}

func (r *UnifiedResolver) scoreAndSelect(results []vectorgraphdb.SearchResult, budget int, metrics *PipelineMetrics) ([]*ContextItem, int) {
	start := time.Now()
	defer func() { metrics.Scoring = time.Since(start) }()

	if r.scorer == nil {
		return nil, budget
	}

	items, remaining, _ := r.scorer.SelectContext(results, budget)
	return items, remaining
}

func (r *UnifiedResolver) buildContext(items []*ContextItem, metrics *PipelineMetrics) (*LLMContext, error) {
	start := time.Now()
	defer func() { metrics.ContextBuild = time.Since(start) }()

	if r.prompter == nil {
		return &LLMContext{}, nil
	}

	return r.prompter.Build(items)
}

type QueryIntent int

const (
	IntentHybrid QueryIntent = iota
	IntentCode
	IntentHistory
	IntentAcademic
)

func (r *UnifiedResolver) GetMetrics() *ResolverMetrics {
	return &ResolverMetrics{
		CacheSize:       r.intentCache.Size(),
		StaleNodes:      r.freshness.GetStaleCount(),
		ActiveConflicts: len(r.conflicts.GetActiveConflicts(100)),
	}
}

func (c *IntentCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

type ResolverMetrics struct {
	CacheSize       int `json:"cache_size"`
	StaleNodes      int `json:"stale_nodes"`
	ActiveConflicts int `json:"active_conflicts"`
}

func (r *UnifiedResolver) ClearCache() {
	r.intentCache.mu.Lock()
	r.intentCache.entries = make(map[string]*cacheEntry)
	r.intentCache.mu.Unlock()
}

func (r *UnifiedResolver) SetEmbedder(embedder Embedder) {
	r.embedder = embedder
}

func (r *UnifiedResolver) SetFirewall(firewall *HallucinationFirewall) {
	r.firewall = firewall
}

type closeable interface {
	Close()
}

func (r *UnifiedResolver) Close() {
	r.ClearCache()
	closeIfNotNil(r.freshness)
	closeIfNotNil(r.trust)
	closeIfNotNil(r.provenance)
	closeIfNotNil(r.conflicts)
	closeIfNotNil(r.firewall)
}

func closeIfNotNil(c closeable) {
	if c != nil {
		c.Close()
	}
}
