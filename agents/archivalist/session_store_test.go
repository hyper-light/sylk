package archivalist

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionStore_StoreAndQuery(t *testing.T) {
	store := NewStore(StoreConfig{})
	cache := NewQueryCache(DefaultQueryCacheConfig(), nil)

	sessionOne := NewSessionStore(store, nil, cache, "session-1")
	sessionTwo := NewSessionStore(store, nil, cache, "session-2")

	_, err := sessionOne.StoreEntry(&Entry{
		Category: CategoryDecision,
		Content:  "Decision A",
		Source:   SourceModelClaudeOpus45,
	})
	require.NoError(t, err)

	_, err = sessionTwo.StoreEntry(&Entry{
		Category: CategoryDecision,
		Content:  "Decision B",
		Source:   SourceModelClaudeOpus45,
	})
	require.NoError(t, err)

	entriesOne, err := sessionOne.Query(ArchiveQuery{Categories: []Category{CategoryDecision}})
	require.NoError(t, err)
	require.Len(t, entriesOne, 1)
	assert.Equal(t, "Decision A", entriesOne[0].Content)

	entriesTwo, err := sessionTwo.Query(ArchiveQuery{Categories: []Category{CategoryDecision}})
	require.NoError(t, err)
	require.Len(t, entriesTwo, 1)
	assert.Equal(t, "Decision B", entriesTwo[0].Content)
}

func TestSessionStore_CacheStats(t *testing.T) {
	cache := NewQueryCache(DefaultQueryCacheConfig(), nil)
	store := NewStore(StoreConfig{})
	sessionStore := NewSessionStore(store, nil, cache, "session-1")

	stats := sessionStore.CacheStats()
	assert.Equal(t, int64(0), stats.CacheHits)
	assert.Equal(t, 0, stats.CachedResponses)
}
