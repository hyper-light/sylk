// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.3.2: ContextDiscovery - unsupervised task context clustering.
package context

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultMaxContexts is the default maximum number of discovered contexts.
const DefaultMaxContexts = 10

// DefaultClassifyThreshold is the similarity threshold for context classification.
const DefaultClassifyThreshold = 0.7

// DefaultUpdateThreshold is the similarity threshold for centroid updates.
const DefaultUpdateThreshold = 0.8

// DefaultCentroidEMA is the exponential moving average factor for centroid updates.
const DefaultCentroidEMA = 0.1

// =============================================================================
// Embedder Interface
// =============================================================================

// Embedder generates embeddings for text.
type Embedder interface {
	Embed(text string) ([]float32, error)
}

// =============================================================================
// Context Discovery Configuration
// =============================================================================

// ContextDiscoveryConfig holds configuration for context discovery.
type ContextDiscoveryConfig struct {
	// MaxContexts is the maximum number of discovered contexts.
	MaxContexts int

	// ClassifyThreshold is the minimum similarity for classification.
	ClassifyThreshold float64

	// UpdateThreshold is the minimum similarity for centroid update.
	UpdateThreshold float64

	// CentroidEMA is the exponential moving average factor.
	CentroidEMA float64
}

// DefaultContextDiscoveryConfig returns the default configuration.
func DefaultContextDiscoveryConfig() ContextDiscoveryConfig {
	return ContextDiscoveryConfig{
		MaxContexts:       DefaultMaxContexts,
		ClassifyThreshold: DefaultClassifyThreshold,
		UpdateThreshold:   DefaultUpdateThreshold,
		CentroidEMA:       DefaultCentroidEMA,
	}
}

// =============================================================================
// Context Discovery Manager
// =============================================================================

// ContextDiscoveryManager provides full context discovery functionality.
type ContextDiscoveryManager struct {
	mu sync.RWMutex

	discovery *ContextDiscovery
	config    ContextDiscoveryConfig
	nextID    int
}

// NewContextDiscoveryManager creates a new context discovery manager.
func NewContextDiscoveryManager(config ContextDiscoveryConfig) *ContextDiscoveryManager {
	if config.MaxContexts <= 0 {
		config.MaxContexts = DefaultMaxContexts
	}
	if config.ClassifyThreshold <= 0 {
		config.ClassifyThreshold = DefaultClassifyThreshold
	}
	if config.UpdateThreshold <= 0 {
		config.UpdateThreshold = DefaultUpdateThreshold
	}
	if config.CentroidEMA <= 0 {
		config.CentroidEMA = DefaultCentroidEMA
	}

	return &ContextDiscoveryManager{
		discovery: &ContextDiscovery{
			Centroids:   make([]ContextCentroid, 0),
			MaxContexts: config.MaxContexts,
		},
		config: config,
	}
}

// NewDefaultContextDiscoveryManager creates a manager with default config.
func NewDefaultContextDiscoveryManager() *ContextDiscoveryManager {
	return NewContextDiscoveryManager(DefaultContextDiscoveryConfig())
}

// =============================================================================
// Classification Methods
// =============================================================================

// ClassifyQuery classifies a query to a discovered context.
// Returns the most similar context if above threshold, or creates a new one.
func (m *ContextDiscoveryManager) ClassifyQuery(query string, embedding []float32) TaskContext {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check keyword cache first
	if ctx := m.checkKeywordCache(query); ctx != "" {
		return ctx
	}

	// Find best matching context
	bestCtx, bestSim := m.findBestMatch(embedding)

	if bestSim >= m.config.ClassifyThreshold {
		return m.handleMatchFound(bestCtx, embedding, query, bestSim)
	}

	return m.handleNoMatch(embedding, query)
}

// checkKeywordCache checks the keyword cache for a matching context.
// If found, it also increments the count for that context.
func (m *ContextDiscoveryManager) checkKeywordCache(query string) TaskContext {
	normalized := normalizeQuery(query)
	if val, ok := m.discovery.keywordCache.Load(normalized); ok {
		if ctx, ok := val.(TaskContext); ok {
			// Increment count for cache hit
			m.incrementContextCount(ctx)
			return ctx
		}
	}
	return ""
}

// incrementContextCount increments the count for a context.
func (m *ContextDiscoveryManager) incrementContextCount(ctx TaskContext) {
	for i := range m.discovery.Centroids {
		if m.discovery.Centroids[i].ID == ctx {
			m.discovery.Centroids[i].Count++
			return
		}
	}
}

// findBestMatch finds the most similar centroid to the embedding.
func (m *ContextDiscoveryManager) findBestMatch(embedding []float32) (TaskContext, float64) {
	var bestCtx TaskContext
	bestSim := -1.0

	for _, centroid := range m.discovery.Centroids {
		sim := cosineSimilarity(embedding, centroid.Embedding)
		if sim > bestSim {
			bestSim = sim
			bestCtx = centroid.ID
		}
	}

	return bestCtx, bestSim
}

// handleMatchFound handles the case when a matching context is found.
func (m *ContextDiscoveryManager) handleMatchFound(ctx TaskContext, embedding []float32, query string, similarity float64) TaskContext {
	// Update centroid if similarity is high enough
	if similarity >= m.config.UpdateThreshold {
		m.updateCentroid(ctx, embedding)
	}

	// Cache the keyword mapping
	m.cacheKeyword(query, ctx)

	return ctx
}

// handleNoMatch handles the case when no matching context is found.
func (m *ContextDiscoveryManager) handleNoMatch(embedding []float32, query string) TaskContext {
	// Create new context if we have room
	if len(m.discovery.Centroids) < m.config.MaxContexts {
		return m.createNewContext(embedding, query)
	}

	// Otherwise, replace weakest context
	return m.replaceWeakestContext(embedding, query)
}

// =============================================================================
// Centroid Management
// =============================================================================

// updateCentroid updates a centroid using exponential moving average.
func (m *ContextDiscoveryManager) updateCentroid(ctx TaskContext, embedding []float32) {
	for i := range m.discovery.Centroids {
		if m.discovery.Centroids[i].ID == ctx {
			m.updateCentroidEmbedding(&m.discovery.Centroids[i], embedding)
			m.discovery.Centroids[i].Count++
			return
		}
	}
}

// updateCentroidEmbedding applies EMA to update the centroid embedding.
func (m *ContextDiscoveryManager) updateCentroidEmbedding(centroid *ContextCentroid, embedding []float32) {
	alpha := m.config.CentroidEMA

	if len(centroid.Embedding) != len(embedding) {
		centroid.Embedding = make([]float32, len(embedding))
		copy(centroid.Embedding, embedding)
		return
	}

	for i := range centroid.Embedding {
		centroid.Embedding[i] = float32(1-alpha)*centroid.Embedding[i] + float32(alpha)*embedding[i]
	}

	// Normalize to unit length
	normalizef32(centroid.Embedding)
}

// createNewContext creates a new context centroid.
func (m *ContextDiscoveryManager) createNewContext(embedding []float32, seedQuery string) TaskContext {
	m.nextID++
	ctx := TaskContext(fmt.Sprintf("ctx_%d", m.nextID))

	centroid := ContextCentroid{
		ID:        ctx,
		Embedding: make([]float32, len(embedding)),
		Count:     1,
		Bias: ContextBias{
			RelevanceMult: 1.0,
			StruggleMult:  1.0,
			WasteMult:     1.0,
		},
	}
	copy(centroid.Embedding, embedding)

	m.discovery.Centroids = append(m.discovery.Centroids, centroid)
	m.cacheKeyword(seedQuery, ctx)

	return ctx
}

// replaceWeakestContext replaces the least-used context with a new one.
func (m *ContextDiscoveryManager) replaceWeakestContext(embedding []float32, seedQuery string) TaskContext {
	weakestIdx := m.findWeakestContext()
	if weakestIdx < 0 {
		return TaskContext("default")
	}

	// Remove from keyword cache
	m.removeFromCache(m.discovery.Centroids[weakestIdx].ID)

	// Create new context in place of weakest
	m.nextID++
	ctx := TaskContext(fmt.Sprintf("ctx_%d", m.nextID))

	m.discovery.Centroids[weakestIdx] = ContextCentroid{
		ID:        ctx,
		Embedding: make([]float32, len(embedding)),
		Count:     1,
		Bias: ContextBias{
			RelevanceMult: 1.0,
			StruggleMult:  1.0,
			WasteMult:     1.0,
		},
	}
	copy(m.discovery.Centroids[weakestIdx].Embedding, embedding)
	m.cacheKeyword(seedQuery, ctx)

	return ctx
}

// findWeakestContext finds the index of the least-used context.
func (m *ContextDiscoveryManager) findWeakestContext() int {
	if len(m.discovery.Centroids) == 0 {
		return -1
	}

	minIdx := 0
	minCount := m.discovery.Centroids[0].Count

	for i, centroid := range m.discovery.Centroids {
		if centroid.Count < minCount {
			minCount = centroid.Count
			minIdx = i
		}
	}

	return minIdx
}

// =============================================================================
// Cache Management
// =============================================================================

// cacheKeyword adds a query-context mapping to the cache.
func (m *ContextDiscoveryManager) cacheKeyword(query string, ctx TaskContext) {
	normalized := normalizeQuery(query)
	m.discovery.keywordCache.Store(normalized, ctx)
}

// removeFromCache removes all cache entries for a context.
func (m *ContextDiscoveryManager) removeFromCache(ctx TaskContext) {
	m.discovery.keywordCache.Range(func(key, value interface{}) bool {
		if value == ctx {
			m.discovery.keywordCache.Delete(key)
		}
		return true
	})
}

// =============================================================================
// Bias Management
// =============================================================================

// GetBias returns the bias for a context.
func (m *ContextDiscoveryManager) GetBias(ctx TaskContext) *ContextBias {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.discovery.GetBias(ctx)
}

// UpdateBias updates the bias for a context based on an observation.
func (m *ContextDiscoveryManager) UpdateBias(ctx TaskContext, obs *EpisodeObservation) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.discovery.Centroids {
		if m.discovery.Centroids[i].ID == ctx {
			m.applyBiasUpdate(&m.discovery.Centroids[i].Bias, obs)
			return
		}
	}
}

// applyBiasUpdate applies an observation to update bias values.
func (m *ContextDiscoveryManager) applyBiasUpdate(bias *ContextBias, obs *EpisodeObservation) {
	satisfaction := obs.InferSatisfaction()
	learningRate := 0.1

	updateMult := func(mult *float64, shouldIncrease bool) {
		delta := learningRate * satisfaction
		if shouldIncrease {
			*mult *= (1 + delta)
		} else {
			*mult *= (1 - delta)
		}
		*mult = clampMultiplier(*mult)
	}

	// Update relevance multiplier based on searches after
	updateMult(&bias.RelevanceMult, len(obs.SearchedAfter) > 0)

	// Update struggle multiplier based on follow-ups
	updateMult(&bias.StruggleMult, obs.FollowUpCount > 2)

	// Update waste multiplier based on prefetch waste
	prefetchWaste := len(obs.PrefetchedIDs) - len(obs.UsedIDs)
	updateMult(&bias.WasteMult, prefetchWaste > 3)

	bias.ObservationCount++
}

// clampMultiplier clamps a multiplier to reasonable bounds.
func clampMultiplier(mult float64) float64 {
	if mult < 0.5 {
		return 0.5
	}
	if mult > 2.0 {
		return 2.0
	}
	return mult
}

// =============================================================================
// Query Methods
// =============================================================================

// GetAllContexts returns all discovered contexts.
func (m *ContextDiscoveryManager) GetAllContexts() []TaskContext {
	m.mu.RLock()
	defer m.mu.RUnlock()

	contexts := make([]TaskContext, len(m.discovery.Centroids))
	for i, c := range m.discovery.Centroids {
		contexts[i] = c.ID
	}
	return contexts
}

// GetContextCount returns the number of discovered contexts.
func (m *ContextDiscoveryManager) GetContextCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.discovery.Centroids)
}

// GetContextStats returns statistics for all contexts.
func (m *ContextDiscoveryManager) GetContextStats() []ContextStat {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make([]ContextStat, len(m.discovery.Centroids))
	for i, c := range m.discovery.Centroids {
		stats[i] = ContextStat{
			ID:               c.ID,
			Count:            c.Count,
			ObservationCount: c.Bias.ObservationCount,
			RelevanceMult:    c.Bias.RelevanceMult,
			StruggleMult:     c.Bias.StruggleMult,
			WasteMult:        c.Bias.WasteMult,
		}
	}
	return stats
}

// ContextStat contains statistics for a context.
type ContextStat struct {
	ID               TaskContext
	Count            int64
	ObservationCount int
	RelevanceMult    float64
	StruggleMult     float64
	WasteMult        float64
}

// FindSimilarContexts returns contexts similar to the given embedding.
func (m *ContextDiscoveryManager) FindSimilarContexts(embedding []float32, threshold float64) []ContextMatch {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var matches []ContextMatch
	for _, centroid := range m.discovery.Centroids {
		sim := cosineSimilarity(embedding, centroid.Embedding)
		if sim >= threshold {
			matches = append(matches, ContextMatch{
				Context:    centroid.ID,
				Similarity: sim,
			})
		}
	}

	// Sort by similarity descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Similarity > matches[j].Similarity
	})

	return matches
}

// ContextMatch represents a context matching result.
type ContextMatch struct {
	Context    TaskContext
	Similarity float64
}

// =============================================================================
// Persistence
// =============================================================================

// GetDiscovery returns the underlying ContextDiscovery for serialization.
func (m *ContextDiscoveryManager) GetDiscovery() *ContextDiscovery {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a clone to avoid data races
	clone := &ContextDiscovery{
		Centroids:   make([]ContextCentroid, len(m.discovery.Centroids)),
		MaxContexts: m.discovery.MaxContexts,
	}
	for i, c := range m.discovery.Centroids {
		clone.Centroids[i] = ContextCentroid{
			ID:        c.ID,
			Embedding: make([]float32, len(c.Embedding)),
			Count:     c.Count,
			Bias:      c.Bias,
		}
		copy(clone.Centroids[i].Embedding, c.Embedding)
	}
	return clone
}

// RestoreFromDiscovery restores state from a ContextDiscovery.
func (m *ContextDiscoveryManager) RestoreFromDiscovery(discovery *ContextDiscovery) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.discovery = discovery
	if m.discovery.MaxContexts <= 0 {
		m.discovery.MaxContexts = m.config.MaxContexts
	}

	// Find highest ID to set nextID
	maxID := 0
	for _, c := range m.discovery.Centroids {
		var id int
		if _, err := fmt.Sscanf(string(c.ID), "ctx_%d", &id); err == nil {
			if id > maxID {
				maxID = id
			}
		}
	}
	m.nextID = maxID
}

// =============================================================================
// Utility Functions
// =============================================================================

// normalizeQuery normalizes a query for cache lookup.
func normalizeQuery(query string) string {
	// Lowercase and trim whitespace
	normalized := strings.ToLower(strings.TrimSpace(query))

	// Remove extra whitespace
	words := strings.Fields(normalized)
	return strings.Join(words, " ")
}

// cosineSimilarity computes cosine similarity between two embeddings.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// normalizef32 normalizes a float32 slice to unit length in place.
func normalizef32(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	scale := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= scale
	}
}
