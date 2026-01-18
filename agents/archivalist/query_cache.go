package archivalist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// QueryType classifies queries for TTL determination
type QueryType int

const (
	QueryTypePattern QueryType = iota
	QueryTypeFailure
	QueryTypeFileState
	QueryTypeResumeState
	QueryTypeBriefing
	QueryTypeContext
)

// QueryCacheConfig configures the query cache
type QueryCacheConfig struct {
	// Similarity threshold for cache hit (0.0-1.0)
	HitThreshold float64

	// Maximum cached queries
	MaxQueries int

	// Default TTL for cached responses
	DefaultTTL time.Duration

	// Enable embedding-based similarity
	UseEmbeddings bool
}

// DefaultQueryCacheConfig returns sensible defaults
func DefaultQueryCacheConfig() QueryCacheConfig {
	return QueryCacheConfig{
		HitThreshold:  0.95,
		MaxQueries:    10000,
		DefaultTTL:    15 * time.Minute,
		UseEmbeddings: true,
	}
}

// QueryEmbedding stores an embedded query for similarity matching
type QueryEmbedding struct {
	QueryHash string
	Embedding []float32
	CreatedAt time.Time
	TTL       time.Duration
	SessionID string    // For session-scoped caching
	QueryType QueryType // For TTL determination
}

// CachedResponse stores a cached response
type CachedResponse struct {
	Response   []byte
	QueryText  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	HitCount   int64
	LastHitAt  time.Time
	QueryType  QueryType
	SessionID  string
	SourceType string // "cache", "synthesis"
}

// IsExpired checks if the cached response has expired
func (cr *CachedResponse) IsExpired() bool {
	return time.Now().After(cr.ExpiresAt)
}

// SimilarityMatch represents a match from the cache
type SimilarityMatch struct {
	QueryHash  string
	Similarity float64
}

// QueryCache provides query similarity caching for 90%+ token savings
type QueryCache struct {
	mu sync.RWMutex

	embeddings []QueryEmbedding
	responses  map[string]*CachedResponse
	embedder   Embedder
	config     QueryCacheConfig

	hits   int64
	misses int64

	sessionHits   sync.Map
	sessionMisses sync.Map
}

// NewQueryCache creates a new query cache
func NewQueryCache(cfg QueryCacheConfig, embedder Embedder) *QueryCache {
	if cfg.HitThreshold == 0 {
		cfg = DefaultQueryCacheConfig()
	}

	return &QueryCache{
		embeddings: make([]QueryEmbedding, 0),
		responses:  make(map[string]*CachedResponse),
		embedder:   embedder,
		config:     cfg,
	}
}

// Get attempts to find a cached response for a similar query
func (qc *QueryCache) Get(ctx context.Context, query string, sessionID string) (*CachedResponse, bool) {
	qc.mu.RLock()
	defer qc.mu.RUnlock()

	if response := qc.tryExactMatch(query, sessionID); response != nil {
		return response, true
	}
	match, ok := qc.trySimilarMatch(ctx, query, sessionID)
	return match, ok
}

func (qc *QueryCache) tryExactMatch(query string, sessionID string) *CachedResponse {
	queryHash := hashQuery(query)
	response, ok := qc.responses[queryHash]
	if !ok || response.IsExpired() {
		return nil
	}
	// Check session isolation
	if response.SessionID != sessionID {
		return nil
	}
	qc.recordHit(response)
	return response
}

func (qc *QueryCache) trySimilarMatch(ctx context.Context, query string, sessionID string) (*CachedResponse, bool) {
	if !qc.config.UseEmbeddings || qc.embedder == nil {
		qc.recordMiss()
		qc.recordMissForSession(sessionID)
		return nil, false
	}

	queryEmbed, err := qc.embedder.Embed(ctx, query)
	if err != nil {
		qc.recordMiss()
		qc.recordMissForSession(sessionID)
		return nil, false
	}

	bestMatch := qc.findMostSimilarLocked(queryEmbed, sessionID)
	if bestMatch == nil || bestMatch.Similarity < qc.config.HitThreshold {
		qc.recordMiss()
		qc.recordMissForSession(sessionID)
		return nil, false
	}

	return qc.getMatchedResponse(bestMatch.QueryHash, sessionID)
}

func (qc *QueryCache) getMatchedResponse(hash string, sessionID string) (*CachedResponse, bool) {
	response, ok := qc.responses[hash]
	if !ok || response.IsExpired() {
		qc.recordMiss()
		qc.recordMissForSession(sessionID)
		return nil, false
	}
	qc.recordHit(response)
	return response, true
}

func (qc *QueryCache) recordHit(response *CachedResponse) {
	atomic.AddInt64(&response.HitCount, 1)
	response.LastHitAt = time.Now()
	atomic.AddInt64(&qc.hits, 1)
	if response.SessionID != "" {
		qc.incrementSessionCount(&qc.sessionHits, response.SessionID)
	}
}

func (qc *QueryCache) recordMiss() {
	atomic.AddInt64(&qc.misses, 1)
}

func (qc *QueryCache) recordMissForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	qc.incrementSessionCount(&qc.sessionMisses, sessionID)
}

func (qc *QueryCache) incrementSessionCount(store *sync.Map, sessionID string) {
	value, _ := store.LoadOrStore(sessionID, new(int64))
	atomic.AddInt64(value.(*int64), 1)
}

func (qc *QueryCache) sessionCount(store *sync.Map, sessionID string) int64 {
	value, ok := store.Load(sessionID)
	if !ok {
		return 0
	}
	return atomic.LoadInt64(value.(*int64))
}

// Store caches a response for a query
func (qc *QueryCache) Store(ctx context.Context, query string, sessionID string, response []byte, queryType QueryType) error {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	queryHash := hashQuery(query)
	ttl := getTTLForQueryType(queryType)

	// Store the response
	qc.responses[queryHash] = &CachedResponse{
		Response:   response,
		QueryText:  query,
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(ttl),
		HitCount:   0,
		QueryType:  queryType,
		SessionID:  sessionID,
		SourceType: "synthesis",
	}

	// If embeddings enabled, store embedding for similarity matching
	if qc.config.UseEmbeddings && qc.embedder != nil {
		queryEmbed, err := qc.embedder.Embed(ctx, query)
		if err == nil {
			qc.embeddings = append(qc.embeddings, QueryEmbedding{
				QueryHash: queryHash,
				Embedding: queryEmbed,
				CreatedAt: time.Now(),
				TTL:       ttl,
				SessionID: sessionID,
				QueryType: queryType,
			})
		}
	}

	// Evict old entries if over capacity
	if len(qc.responses) > qc.config.MaxQueries {
		qc.evictOldestLocked()
	}

	return nil
}

// Invalidate removes a cached response
func (qc *QueryCache) Invalidate(queryHash string) {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	delete(qc.responses, queryHash)

	// Remove from embeddings
	newEmbeddings := make([]QueryEmbedding, 0, len(qc.embeddings))
	for _, e := range qc.embeddings {
		if e.QueryHash != queryHash {
			newEmbeddings = append(newEmbeddings, e)
		}
	}
	qc.embeddings = newEmbeddings
}

// InvalidateBySession removes all cached responses for a session
func (qc *QueryCache) InvalidateBySession(sessionID string) {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	// Remove responses
	for hash, resp := range qc.responses {
		if resp.SessionID == sessionID {
			delete(qc.responses, hash)
		}
	}

	// Remove embeddings
	newEmbeddings := make([]QueryEmbedding, 0, len(qc.embeddings))
	for _, e := range qc.embeddings {
		if e.SessionID != sessionID {
			newEmbeddings = append(newEmbeddings, e)
		}
	}
	qc.embeddings = newEmbeddings
}

// InvalidateByType removes all cached responses of a specific type
func (qc *QueryCache) InvalidateByType(queryType QueryType) {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	// Remove responses
	for hash, resp := range qc.responses {
		if resp.QueryType == queryType {
			delete(qc.responses, hash)
		}
	}

	// Remove embeddings
	newEmbeddings := make([]QueryEmbedding, 0, len(qc.embeddings))
	for _, e := range qc.embeddings {
		if e.QueryType != queryType {
			newEmbeddings = append(newEmbeddings, e)
		}
	}
	qc.embeddings = newEmbeddings
}

// Cleanup removes expired entries
func (qc *QueryCache) Cleanup() {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	now := time.Now()

	// Remove expired responses
	for hash, resp := range qc.responses {
		if resp.IsExpired() {
			delete(qc.responses, hash)
		}
	}

	// Remove expired embeddings
	newEmbeddings := make([]QueryEmbedding, 0, len(qc.embeddings))
	for _, e := range qc.embeddings {
		if now.Before(e.CreatedAt.Add(e.TTL)) {
			newEmbeddings = append(newEmbeddings, e)
		}
	}
	qc.embeddings = newEmbeddings
}

// Stats returns cache statistics
func (qc *QueryCache) Stats() QueryCacheStats {
	qc.mu.RLock()
	defer qc.mu.RUnlock()

	hits := atomic.LoadInt64(&qc.hits)
	misses := atomic.LoadInt64(&qc.misses)
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return QueryCacheStats{
		TotalQueries:     total,
		CacheHits:        hits,
		CacheMisses:      misses,
		HitRate:          hitRate,
		CachedResponses:  len(qc.responses),
		CachedEmbeddings: len(qc.embeddings),
	}
}

func (qc *QueryCache) StatsBySession(sessionID string) QueryCacheStats {
	qc.mu.RLock()
	defer qc.mu.RUnlock()

	var hits int64
	var responses int
	var embeddings int

	for _, resp := range qc.responses {
		if resp.SessionID != sessionID {
			continue
		}
		hits += resp.HitCount
		responses++
	}

	for _, embed := range qc.embeddings {
		if embed.SessionID == sessionID {
			embeddings++
		}
	}

	misses := qc.sessionCount(&qc.sessionMisses, sessionID)
	total := hits + misses
	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return QueryCacheStats{
		TotalQueries:     total,
		CacheHits:        hits,
		CacheMisses:      misses,
		HitRate:          hitRate,
		CachedResponses:  responses,
		CachedEmbeddings: embeddings,
	}
}

// QueryCacheStats contains cache statistics
type QueryCacheStats struct {
	TotalQueries     int64   `json:"total_queries"`
	CacheHits        int64   `json:"cache_hits"`
	CacheMisses      int64   `json:"cache_misses"`
	HitRate          float64 `json:"hit_rate"`
	CachedResponses  int     `json:"cached_responses"`
	CachedEmbeddings int     `json:"cached_embeddings"`
}

// findMostSimilarLocked finds the most similar cached query (must be called with lock held)
func (qc *QueryCache) findMostSimilarLocked(queryEmbed []float32, sessionID string) *SimilarityMatch {
	var best *SimilarityMatch
	now := time.Now()

	for _, cached := range qc.embeddings {
		// Check TTL
		if now.After(cached.CreatedAt.Add(cached.TTL)) {
			continue
		}

		// Check session scope (empty session means global)
		if cached.SessionID != "" && cached.SessionID != sessionID {
			continue
		}

		// Compute cosine similarity
		sim := cosineSimilarity(queryEmbed, cached.Embedding)
		if best == nil || sim > best.Similarity {
			best = &SimilarityMatch{
				QueryHash:  cached.QueryHash,
				Similarity: sim,
			}
		}
	}

	return best
}

// cacheEntry holds cache entry metadata for sorting
type cacheEntry struct {
	hash      string
	createdAt time.Time
}

// evictOldestLocked removes oldest entries to make room (must be called with lock held)
func (qc *QueryCache) evictOldestLocked() {
	toRemove := qc.calculateEvictionCount()
	entries := qc.collectEntries()
	qc.sortEntriesByAge(entries)
	removedHashes := qc.removeOldestResponses(entries, toRemove)
	qc.cleanEmbeddings(removedHashes)
}

func (qc *QueryCache) calculateEvictionCount() int {
	toRemove := len(qc.responses) / 10
	if toRemove < 1 {
		return 1
	}
	return toRemove
}

func (qc *QueryCache) collectEntries() []cacheEntry {
	entries := make([]cacheEntry, 0, len(qc.responses))
	for hash, resp := range qc.responses {
		entries = append(entries, cacheEntry{hash, resp.CreatedAt})
	}
	return entries
}

func (qc *QueryCache) sortEntriesByAge(entries []cacheEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].createdAt.Before(entries[j].createdAt)
	})
}

func (qc *QueryCache) removeOldestResponses(entries []cacheEntry, toRemove int) map[string]bool {
	removed := make(map[string]bool)
	for i := 0; i < toRemove && i < len(entries); i++ {
		delete(qc.responses, entries[i].hash)
		removed[entries[i].hash] = true
	}
	return removed
}

func (qc *QueryCache) cleanEmbeddings(removedHashes map[string]bool) {
	newEmbeddings := make([]QueryEmbedding, 0, len(qc.embeddings))
	for _, e := range qc.embeddings {
		if !removedHashes[e.QueryHash] {
			newEmbeddings = append(newEmbeddings, e)
		}
	}
	qc.embeddings = newEmbeddings
}

// getTTLForQueryType returns the appropriate TTL for a query type
func getTTLForQueryType(queryType QueryType) time.Duration {
	switch queryType {
	case QueryTypePattern:
		// Patterns change slowly
		return 30 * time.Minute

	case QueryTypeFailure:
		// Failures + resolutions relatively stable
		return 20 * time.Minute

	case QueryTypeFileState:
		// File state changes frequently
		return 5 * time.Minute

	case QueryTypeResumeState:
		// Resume state changes constantly
		return 1 * time.Minute

	case QueryTypeBriefing:
		// Briefings are session-specific
		return 2 * time.Minute

	case QueryTypeContext:
		// General context queries
		return 10 * time.Minute

	default:
		return 10 * time.Minute
	}
}

// hashQuery creates a hash of the query for exact matching
func hashQuery(query string) string {
	hash := sha256.Sum256([]byte(query))
	return hex.EncodeToString(hash[:])
}

func (qc *QueryCache) Name() string {
	return "query_cache"
}

func (qc *QueryCache) Size() int64 {
	qc.mu.RLock()
	defer qc.mu.RUnlock()
	return qc.calculateSizeLocked()
}

func (qc *QueryCache) calculateSizeLocked() int64 {
	responseBytes := int64(len(qc.responses)) * 1024
	embeddingBytes := int64(len(qc.embeddings)) * 256 * 4
	return responseBytes + embeddingBytes
}

func (qc *QueryCache) EvictPercent(percent float64) int64 {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	beforeSize := qc.calculateSizeLocked()
	target := int(float64(len(qc.responses)) * percent)
	if target < 1 && len(qc.responses) > 0 {
		target = 1
	}

	entries := qc.collectEntries()
	qc.sortEntriesByAge(entries)
	removedHashes := qc.removeOldestResponses(entries, target)
	qc.cleanEmbeddings(removedHashes)

	return beforeSize - qc.calculateSizeLocked()
}
