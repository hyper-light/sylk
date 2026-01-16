package archivalist

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrossSessionIndex_QueryCrossSession(t *testing.T) {
	store := NewStore(StoreConfig{})
	index := NewCrossSessionIndex(store, nil, nil)

	_, err := store.InsertEntryInSession("session-1", &Entry{
		Category: CategoryDecision,
		Content:  "Decision A",
		Source:   SourceModelClaudeOpus45,
	})
	require.NoError(t, err)

	_, err = store.InsertEntryInSession("session-2", &Entry{
		Category: CategoryDecision,
		Content:  "Decision B",
		Source:   SourceModelClaudeOpus45,
	})
	require.NoError(t, err)

	results, err := index.QueryCrossSession(ArchiveQuery{Categories: []Category{CategoryDecision}})
	require.NoError(t, err)
	require.Len(t, results, 2)

	ids := map[string]bool{}
	for _, result := range results {
		ids[result.SessionID] = true
	}

	assert.True(t, ids["session-1"])
	assert.True(t, ids["session-2"])
}

func TestCrossSessionIndex_QuerySessions(t *testing.T) {
	store := NewStore(StoreConfig{})
	archive := (*Archive)(nil)
	index := NewCrossSessionIndex(store, archive, nil)

	_, err := store.InsertEntryInSession("session-1", &Entry{
		Category: CategoryDecision,
		Content:  "Decision A",
		Source:   SourceModelClaudeOpus45,
	})
	require.NoError(t, err)

	sessions, err := index.QuerySessions(ArchiveQuery{Categories: []Category{CategoryDecision}})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "session-1", sessions[0].ID)
}
