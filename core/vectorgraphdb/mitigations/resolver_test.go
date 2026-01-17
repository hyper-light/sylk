package mitigations

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntentCache_GetSet(t *testing.T) {
	cache := NewIntentCache(5*time.Minute, 100)

	resolution := &QueryResolution{
		Query:      "test query",
		TokensUsed: 100,
	}

	cache.Set("test query", resolution)

	retrieved, ok := cache.Get("test query")

	assert.True(t, ok)
	assert.Equal(t, resolution.Query, retrieved.Query)
	assert.Equal(t, resolution.TokensUsed, retrieved.TokensUsed)
}

func TestIntentCache_GetMiss(t *testing.T) {
	cache := NewIntentCache(5*time.Minute, 100)

	retrieved, ok := cache.Get("nonexistent")

	assert.False(t, ok)
	assert.Nil(t, retrieved)
}

func TestIntentCache_Expiration(t *testing.T) {
	cache := NewIntentCache(1*time.Millisecond, 100)

	resolution := &QueryResolution{Query: "test"}
	cache.Set("test", resolution)

	time.Sleep(10 * time.Millisecond)

	_, ok := cache.Get("test")

	assert.False(t, ok)
}

func TestIntentCache_Eviction(t *testing.T) {
	cache := NewIntentCache(5*time.Minute, 2)

	cache.Set("q1", &QueryResolution{Query: "q1"})
	cache.Set("q2", &QueryResolution{Query: "q2"})
	cache.Set("q3", &QueryResolution{Query: "q3"})

	assert.LessOrEqual(t, len(cache.entries), 2)
}

func TestUnifiedResolver_Resolve(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	resolver := NewUnifiedResolver(db, nil, nil, DefaultResolverConfig())

	resolution, err := resolver.Resolve(context.Background(), "test code query", nil)

	require.NoError(t, err)
	assert.Equal(t, "test code query", resolution.Query)
	assert.NotNil(t, resolution.Metrics)
	assert.False(t, resolution.CacheHit)
}

func TestUnifiedResolver_Resolve_CacheHit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	resolver := NewUnifiedResolver(db, nil, nil, DefaultResolverConfig())

	opts := &ResolveOptions{
		TokenBudget: 4000,
		CacheResult: true,
	}

	_, err := resolver.Resolve(context.Background(), "cached query", opts)
	require.NoError(t, err)

	resolution, err := resolver.Resolve(context.Background(), "cached query", opts)

	require.NoError(t, err)
	assert.True(t, resolution.CacheHit)
}

func TestUnifiedResolver_DetectIntent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	resolver := NewUnifiedResolver(db, nil, nil, DefaultResolverConfig())
	metrics := &PipelineMetrics{}

	tests := []struct {
		query    string
		expected QueryIntent
	}{
		{"find the code for authentication", IntentCode},
		{"implement user login function", IntentCode},
		{"what was the previous decision", IntentHistory},
		{"show me history of changes", IntentHistory},
		{"best practice for error handling", IntentAcademic},
		{"documentation for API", IntentAcademic},
		{"general question", IntentHybrid},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			intent := resolver.detectIntent(tt.query, metrics)
			assert.Equal(t, tt.expected, intent)
		})
	}
}

func TestUnifiedResolver_SelectDomains(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	resolver := NewUnifiedResolver(db, nil, nil, DefaultResolverConfig())

	tests := []struct {
		name      string
		requested []vectorgraphdb.Domain
		intent    QueryIntent
		expected  []vectorgraphdb.Domain
	}{
		{
			"explicit_domains",
			[]vectorgraphdb.Domain{vectorgraphdb.DomainCode},
			IntentHybrid,
			[]vectorgraphdb.Domain{vectorgraphdb.DomainCode},
		},
		{
			"code_intent",
			nil,
			IntentCode,
			[]vectorgraphdb.Domain{vectorgraphdb.DomainCode},
		},
		{
			"history_intent",
			nil,
			IntentHistory,
			[]vectorgraphdb.Domain{vectorgraphdb.DomainHistory},
		},
		{
			"academic_intent",
			nil,
			IntentAcademic,
			[]vectorgraphdb.Domain{vectorgraphdb.DomainAcademic},
		},
		{
			"hybrid_intent",
			nil,
			IntentHybrid,
			[]vectorgraphdb.Domain{
				vectorgraphdb.DomainCode,
				vectorgraphdb.DomainHistory,
				vectorgraphdb.DomainAcademic,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domains := resolver.selectDomains(tt.requested, tt.intent)
			assert.Equal(t, tt.expected, domains)
		})
	}
}

func TestUnifiedResolver_GetMetrics(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	resolver := NewUnifiedResolver(db, nil, nil, DefaultResolverConfig())

	metrics := resolver.GetMetrics()

	assert.NotNil(t, metrics)
	assert.Equal(t, 0, metrics.CacheSize)
}

func TestUnifiedResolver_ClearCache(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	resolver := NewUnifiedResolver(db, nil, nil, DefaultResolverConfig())

	resolver.intentCache.Set("test", &QueryResolution{Query: "test"})
	assert.Equal(t, 1, len(resolver.intentCache.entries))

	resolver.ClearCache()

	assert.Equal(t, 0, len(resolver.intentCache.entries))
}

func TestDefaultResolveOptions(t *testing.T) {
	opts := DefaultResolveOptions()

	assert.Equal(t, 4000, opts.TokenBudget)
	assert.Equal(t, TrustLLMInference, opts.MinTrust)
	assert.Equal(t, 30*24*time.Hour, opts.MinFreshness)
	assert.True(t, opts.IncludeConflicts)
	assert.Equal(t, 2, opts.MaxGraphDepth)
	assert.True(t, opts.CacheResult)
}

func TestDefaultResolverConfig(t *testing.T) {
	config := DefaultResolverConfig()

	assert.Equal(t, 5*time.Minute, config.CacheTTL)
	assert.Equal(t, 1000, config.CacheMaxSize)
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		s        string
		keywords []string
		expected bool
	}{
		{"hello world", []string{"world"}, true},
		{"hello world", []string{"foo", "bar"}, false},
		{"hello world", []string{"foo", "world"}, true},
		{"", []string{"test"}, false},
		{"test", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			result := containsAny(tt.s, tt.keywords)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUnifiedResolver_Resolve_WithOptions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	resolver := NewUnifiedResolver(db, nil, nil, DefaultResolverConfig())

	opts := &ResolveOptions{
		Domains:          []vectorgraphdb.Domain{vectorgraphdb.DomainCode},
		TokenBudget:      2000,
		MinTrust:         TrustOfficialDocs,
		MinFreshness:     7 * 24 * time.Hour,
		IncludeConflicts: false,
		MaxGraphDepth:    1,
		CacheResult:      false,
	}

	resolution, err := resolver.Resolve(context.Background(), "test", opts)

	require.NoError(t, err)
	assert.Equal(t, 2000, resolution.TokenBudget)
}

func TestUnifiedResolver_Context_Cancellation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	resolver := NewUnifiedResolver(db, nil, nil, DefaultResolverConfig())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := resolver.Resolve(ctx, "test", nil)

	assert.NoError(t, err)
}

func TestPipelineMetrics(t *testing.T) {
	metrics := &PipelineMetrics{
		IntentDetect: 1 * time.Millisecond,
		CacheCheck:   2 * time.Millisecond,
		Embedding:    10 * time.Millisecond,
		VectorSearch: 50 * time.Millisecond,
		Total:        100 * time.Millisecond,
	}

	assert.Equal(t, 1*time.Millisecond, metrics.IntentDetect)
	assert.Equal(t, 100*time.Millisecond, metrics.Total)
}
