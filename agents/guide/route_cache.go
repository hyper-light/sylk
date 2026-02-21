package guide

import (
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/dgraph-io/ristretto"
)

// =============================================================================
// Route Cache
// =============================================================================
//
// RouteCache provides a hybrid cache strategy:
// 1. Exact normalized lookup
// 2. Hot-path in-memory admission cache (Ristretto)
// 3. Semantic fallback based on token overlap

type RouteCache struct {
	mu      sync.RWMutex
	entries map[string]*CachedRoute
	index   map[string]map[string]struct{}

	maxSize               int
	ttl                   time.Duration
	enableSemantic        bool
	semanticThreshold     float64
	maxSemanticCandidates int

	hot *ristretto.Cache

	hits           int64
	misses         int64
	hotHits        int64
	similarityHits int64
}

type CachedRoute struct {
	Input string `json:"input"`

	TargetAgentID string  `json:"target_agent_id"`
	Intent        Intent  `json:"intent"`
	Domain        Domain  `json:"domain"`
	Confidence    float64 `json:"confidence"`

	CachedAt  time.Time `json:"cached_at"`
	ExpiresAt time.Time `json:"expires_at"`
	HitCount  int64     `json:"hit_count"`

	ClassificationCount int64 `json:"classification_count"`
}

type RouteCacheConfig struct {
	MaxSize int           // Maximum entries (default: 10000)
	TTL     time.Duration // Entry lifetime (default: 1 hour)

	EnableSemantic        bool
	SemanticThreshold     float64
	MaxSemanticCandidates int

	EnableHotCache bool
	HotNumCounters int64
	HotMaxCost     int64
	HotBufferItems int64
}

func DefaultRouteCacheConfig() RouteCacheConfig {
	return RouteCacheConfig{
		MaxSize:               10000,
		TTL:                   1 * time.Hour,
		EnableSemantic:        true,
		SemanticThreshold:     0.86,
		MaxSemanticCandidates: 32,
		EnableHotCache:        true,
		HotNumCounters:        100000,
		HotMaxCost:            10000,
		HotBufferItems:        64,
	}
}

func NewRouteCache(cfg RouteCacheConfig) *RouteCache {
	cfg = normalizeRouteCacheConfig(cfg)

	cache := &RouteCache{
		entries:               make(map[string]*CachedRoute),
		index:                 make(map[string]map[string]struct{}),
		maxSize:               cfg.MaxSize,
		ttl:                   cfg.TTL,
		enableSemantic:        cfg.EnableSemantic,
		semanticThreshold:     cfg.SemanticThreshold,
		maxSemanticCandidates: cfg.MaxSemanticCandidates,
	}
	cache.hot = newHotRouteCache(cfg)
	return cache
}

func normalizeRouteCacheConfig(cfg RouteCacheConfig) RouteCacheConfig {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 10000
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 1 * time.Hour
	}
	if cfg.SemanticThreshold <= 0 || cfg.SemanticThreshold > 1 {
		cfg.SemanticThreshold = 0.86
	}
	if cfg.MaxSemanticCandidates <= 0 {
		cfg.MaxSemanticCandidates = 32
	}
	if cfg.HotNumCounters <= 0 {
		cfg.HotNumCounters = int64(cfg.MaxSize * 10)
	}
	if cfg.HotMaxCost <= 0 {
		cfg.HotMaxCost = int64(cfg.MaxSize)
	}
	if cfg.HotBufferItems <= 0 {
		cfg.HotBufferItems = 64
	}
	return cfg
}

func newHotRouteCache(cfg RouteCacheConfig) *ristretto.Cache {
	if !cfg.EnableHotCache {
		return nil
	}
	hot, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: cfg.HotNumCounters,
		MaxCost:     cfg.HotMaxCost,
		BufferItems: cfg.HotBufferItems,
	})
	if err != nil {
		return nil
	}
	return hot
}

// =============================================================================
// Cache Operations
// =============================================================================

func (c *RouteCache) Get(input string) *CachedRoute {
	normalized := normalizeInput(input)
	if entry := c.getHot(normalized); entry != nil {
		c.recordHotHit()
		c.recordHit()
		return entry
	}
	if entry := c.getExact(normalized); entry != nil {
		c.promoteHot(normalized, entry)
		c.recordHit()
		return entry
	}
	if entry := c.getSemantic(normalized); entry != nil {
		c.promoteHot(normalized, entry)
		c.recordSimilarityHit()
		c.recordHit()
		return entry
	}
	c.recordMiss()
	return nil
}

func (c *RouteCache) getHot(key string) *CachedRoute {
	if c == nil || c.hot == nil {
		return nil
	}
	value, ok := c.hot.Get(key)
	if !ok {
		return nil
	}
	entry, ok := value.(*CachedRoute)
	if !ok {
		return nil
	}
	if !c.isEntryValid(entry) {
		c.Invalidate(key)
		return nil
	}
	c.bumpHitCount(entry)
	return entry
}

func (c *RouteCache) getExact(key string) *CachedRoute {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	if !c.isEntryValid(entry) {
		c.Invalidate(key)
		return nil
	}
	c.bumpHitCount(entry)
	return entry
}

func (c *RouteCache) getSemantic(key string) *CachedRoute {
	if !c.enableSemantic {
		return nil
	}
	candidates := c.semanticCandidates(key)
	if len(candidates) == 0 {
		return nil
	}
	entry, score := c.bestSemanticMatch(key, candidates)
	if entry == nil || score < c.semanticThreshold {
		return nil
	}
	c.bumpHitCount(entry)
	return entry
}

func (c *RouteCache) semanticCandidates(key string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.index) == 0 {
		return nil
	}
	collected := make(map[string]struct{})
	for _, bucket := range semanticBuckets(key) {
		for candidate := range c.index[bucket] {
			collected[candidate] = struct{}{}
			if len(collected) >= c.maxSemanticCandidates {
				return mapKeys(collected)
			}
		}
	}
	return mapKeys(collected)
}

func (c *RouteCache) bestSemanticMatch(key string, candidates []string) (*CachedRoute, float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	bestScore := 0.0
	var best *CachedRoute
	for _, candidate := range candidates {
		entry := c.entries[candidate]
		if !c.isEntryValid(entry) {
			continue
		}
		score := semanticSimilarity(key, entry.Input)
		if score <= bestScore {
			continue
		}
		bestScore = score
		best = entry
	}
	return best, bestScore
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func (c *RouteCache) bumpHitCount(entry *CachedRoute) {
	if entry == nil {
		return
	}
	c.mu.Lock()
	entry.HitCount++
	c.mu.Unlock()
}

func (c *RouteCache) isEntryValid(entry *CachedRoute) bool {
	if entry == nil {
		return false
	}
	return time.Now().Before(entry.ExpiresAt)
}

func (c *RouteCache) Set(input string, result *RouteResult) {
	if result == nil {
		return
	}
	entry := c.newEntry(normalizeInput(input), result)
	c.setEntry(entry)
}

func (c *RouteCache) SetFromRoute(input string, targetAgentID string, intent Intent, domain Domain) {
	normalized := normalizeInput(input)
	now := time.Now()
	entry := &CachedRoute{
		Input:               normalized,
		TargetAgentID:       targetAgentID,
		Intent:              intent,
		Domain:              domain,
		Confidence:          1.0,
		CachedAt:            now,
		ExpiresAt:           now.Add(c.ttl),
		HitCount:            0,
		ClassificationCount: 1,
	}
	c.setEntry(entry)
}

func (c *RouteCache) newEntry(normalized string, result *RouteResult) *CachedRoute {
	now := time.Now()
	entry := &CachedRoute{
		Input:               normalized,
		TargetAgentID:       string(result.TargetAgent),
		Intent:              result.Intent,
		Domain:              result.Domain,
		Confidence:          result.Confidence,
		CachedAt:            now,
		ExpiresAt:           now.Add(c.ttl),
		HitCount:            0,
		ClassificationCount: 1,
	}
	return entry
}

func (c *RouteCache) setEntry(entry *CachedRoute) {
	if entry == nil {
		return
	}
	c.mu.Lock()
	if existing := c.entries[entry.Input]; existing != nil {
		entry.HitCount = existing.HitCount
		entry.ClassificationCount = existing.ClassificationCount + 1
	}
	c.ensureCapacity(entry.Input)
	c.entries[entry.Input] = entry
	c.indexEntry(entry.Input)
	c.mu.Unlock()
	c.promoteHot(entry.Input, entry)
}

func (c *RouteCache) ensureCapacity(keepKey string) {
	if len(c.entries) < c.maxSize {
		return
	}
	if _, exists := c.entries[keepKey]; exists {
		return
	}
	c.evictOldest()
}

func (c *RouteCache) evictOldest() {
	oldestKey := ""
	oldestTime := time.Time{}
	for key, entry := range c.entries {
		if oldestKey == "" || entry.CachedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.CachedAt
		}
	}
	if oldestKey != "" {
		c.removeEntry(oldestKey)
	}
}

func (c *RouteCache) promoteHot(key string, entry *CachedRoute) {
	if c == nil || c.hot == nil || entry == nil {
		return
	}
	c.hot.Set(key, entry, 1)
}

func (c *RouteCache) indexEntry(key string) {
	if !c.enableSemantic {
		return
	}
	for _, bucket := range semanticBuckets(key) {
		set := c.index[bucket]
		if set == nil {
			set = map[string]struct{}{}
			c.index[bucket] = set
		}
		set[key] = struct{}{}
	}
}

func (c *RouteCache) unindexEntry(key string) {
	if !c.enableSemantic {
		return
	}
	for _, bucket := range semanticBuckets(key) {
		set := c.index[bucket]
		if len(set) == 0 {
			continue
		}
		delete(set, key)
		if len(set) == 0 {
			delete(c.index, bucket)
		}
	}
}

func (c *RouteCache) Invalidate(input string) {
	normalized := normalizeInput(input)
	c.mu.Lock()
	c.removeEntry(normalized)
	c.mu.Unlock()
}

func (c *RouteCache) removeEntry(key string) {
	delete(c.entries, key)
	c.unindexEntry(key)
	if c.hot != nil {
		c.hot.Del(key)
	}
}

func (c *RouteCache) InvalidateForAgent(agentID string) {
	c.mu.Lock()
	keys := make([]string, 0)
	for key, entry := range c.entries {
		if entry.TargetAgentID == agentID {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		c.removeEntry(key)
	}
	c.mu.Unlock()
}

func (c *RouteCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]*CachedRoute)
	c.index = make(map[string]map[string]struct{})
	c.mu.Unlock()
	if c.hot != nil {
		c.hot.Clear()
	}
}

// =============================================================================
// Cache Maintenance
// =============================================================================

func (c *RouteCache) Cleanup() int {
	now := time.Now()
	c.mu.Lock()
	keys := make([]string, 0)
	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		c.removeEntry(key)
	}
	c.mu.Unlock()
	return len(keys)
}

// =============================================================================
// Stats
// =============================================================================

type RouteCacheStats struct {
	Size    int     `json:"size"`
	MaxSize int     `json:"max_size"`
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	HitRate float64 `json:"hit_rate"`
	TTL     string  `json:"ttl"`

	HotHits        int64 `json:"hot_hits"`
	SimilarityHits int64 `json:"similarity_hits"`
}

func (c *RouteCache) Stats() RouteCacheStats {
	c.mu.RLock()
	size := len(c.entries)
	c.mu.RUnlock()

	hits := atomic.LoadInt64(&c.hits)
	misses := atomic.LoadInt64(&c.misses)
	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return RouteCacheStats{
		Size:           size,
		MaxSize:        c.maxSize,
		Hits:           hits,
		Misses:         misses,
		HitRate:        hitRate,
		TTL:            c.ttl.String(),
		HotHits:        atomic.LoadInt64(&c.hotHits),
		SimilarityHits: atomic.LoadInt64(&c.similarityHits),
	}
}

func (c *RouteCache) recordHit() {
	atomic.AddInt64(&c.hits, 1)
}

func (c *RouteCache) recordMiss() {
	atomic.AddInt64(&c.misses, 1)
}

func (c *RouteCache) recordHotHit() {
	atomic.AddInt64(&c.hotHits, 1)
}

func (c *RouteCache) recordSimilarityHit() {
	atomic.AddInt64(&c.similarityHits, 1)
}

// =============================================================================
// Input Normalization
// =============================================================================

func normalizeInput(input string) string {
	lowered := lowerASCII(input)
	start, end := trimWhitespaceBounds(lowered)
	return string(lowered[start:end])
}

func lowerASCII(input string) []byte {
	result := make([]byte, len(input))
	for i := 0; i < len(input); i++ {
		result[i] = lowerASCIIByte(input[i])
	}
	return result
}

func lowerASCIIByte(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + 32
	}
	return value
}

func trimWhitespaceBounds(value []byte) (int, int) {
	start := trimLeftWhitespace(value)
	end := trimRightWhitespace(value, start)
	return start, end
}

func trimLeftWhitespace(value []byte) int {
	start := 0
	for start < len(value) && isWhitespace(value[start]) {
		start++
	}
	return start
}

func trimRightWhitespace(value []byte, start int) int {
	end := len(value)
	for end > start && isWhitespace(value[end-1]) {
		end--
	}
	return end
}

func isWhitespace(value byte) bool {
	switch value {
	case ' ', '\t', '\n':
		return true
	default:
		return false
	}
}

func semanticBuckets(input string) []string {
	tokens := semanticTokens(input)
	if len(tokens) == 0 {
		return nil
	}
	buckets := make([]string, 0, len(tokens)+1)
	for _, token := range tokens {
		buckets = append(buckets, "tok:"+token)
	}
	if len(tokens) >= 2 {
		buckets = append(buckets, "bigram:"+tokens[0]+"_"+tokens[1])
	}
	return buckets
}

func semanticTokens(input string) []string {
	parts := splitSemanticWords(input)
	if len(parts) == 0 {
		return nil
	}
	uniq := uniqueSemanticTokens(parts)
	sort.Strings(uniq)
	if len(uniq) > 12 {
		return uniq[:12]
	}
	return uniq
}

func splitSemanticWords(input string) []string {
	return strings.FieldsFunc(input, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '/'
	})
}

func uniqueSemanticTokens(tokens []string) []string {
	seen := map[string]struct{}{}
	uniq := make([]string, 0, len(tokens))
	for _, token := range tokens {
		t := strings.TrimSpace(strings.ToLower(token))
		if len(t) < 2 {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		uniq = append(uniq, t)
	}
	return uniq
}

func semanticSimilarity(a string, b string) float64 {
	left := semanticTokens(a)
	right := semanticTokens(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	setLeft := toSemanticSet(left)
	setRight := toSemanticSet(right)
	intersect := intersectionSize(setLeft, setRight)
	union := len(setLeft) + len(setRight) - intersect
	if union == 0 {
		return 0
	}
	base := float64(intersect) / float64(union)
	prefix := prefixBonus(a, b)
	return math.Min(1.0, base+prefix)
}

func toSemanticSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func intersectionSize(left map[string]struct{}, right map[string]struct{}) int {
	count := 0
	for value := range left {
		if _, ok := right[value]; ok {
			count++
		}
	}
	return count
}

func prefixBonus(a string, b string) float64 {
	if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
		return 0.1
	}
	return 0
}

// =============================================================================
// Bulk Operations
// =============================================================================

func (c *RouteCache) GetAll() []*CachedRoute {
	c.mu.RLock()
	defer c.mu.RUnlock()

	routes := make([]*CachedRoute, 0, len(c.entries))
	for _, entry := range c.entries {
		routes = append(routes, entry)
	}
	return routes
}

func (c *RouteCache) SetBulk(routes []*CachedRoute) {
	for _, route := range routes {
		if route == nil {
			continue
		}
		c.setEntry(route)
	}
}
