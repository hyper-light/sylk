package archivalist

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchivalistHandleStorePreservesCrossAgentProvenance(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := context.Background()

	_, err := a.handleStore(ctx, &guide.ForwardedRequest{
		Input:           "Engineer summary about authentication retries",
		Domain:          guide.DomainLearnings,
		SourceAgentID:   "scribe-engineer-1",
		SourceAgentName: "scribe-engineer",
		Metadata: map[string]any{
			"source_type":  "scribe",
			"parent_agent": "engineer",
		},
	})
	require.NoError(t, err)

	entries, err := a.Query(ctx, ArchiveQuery{
		Categories: []Category{CategoryInsight},
		Limit:      5,
	})
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	entry := entries[0]
	require.NotNil(t, entry.Metadata)
	assert.Equal(t, true, entry.Metadata[crossAgentIngestedKey])
	assert.Equal(t, "engineer", entry.Metadata[originAgentTypeKey])
	assert.Equal(t, "scribe", entry.Metadata[originSourceTypeKey])
	assert.Equal(t, "scribe-engineer-1", entry.Metadata[ingestAgentIDKey])
	assert.Equal(t, "scribe-engineer", entry.Metadata[ingestAgentNameKey])
}

func TestArchivalistQueryBoundsCrossAgentResults(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := context.Background()

	baseTime := time.Now().Add(-10 * time.Minute)
	for i := 0; i < 6; i++ {
		entry := makeEntryWithTitle(
			CategoryInsight,
			"Authentication primary memory",
			"Primary archival memory for auth token handling",
			SourceModelArchivalist,
		)
		entry.CreatedAt = baseTime.Add(time.Duration(i) * time.Minute)
		result := a.StoreEntry(ctx, entry)
		require.True(t, result.Success, "store primary %d", i)
	}

	for i := 0; i < 4; i++ {
		entry := makeEntryWithTitle(
			CategoryInsight,
			"Authentication engineer note",
			"Engineer commentary for auth token handling",
			SourceModelArchivalist,
		)
		entry.CreatedAt = baseTime.Add(time.Duration(20+i) * time.Minute)
		entry.Metadata = normalizeCrossAgentMetadata(map[string]any{
			"origin_agent_type":  "engineer",
			"origin_source_type": "scribe",
		}, "scribe-engineer-1", "scribe-engineer")
		result := a.StoreEntry(ctx, entry)
		require.True(t, result.Success, "store cross-agent %d", i)
	}

	results, err := a.Query(ctx, ArchiveQuery{
		SearchText: "auth token",
		Limit:      5,
	})
	require.NoError(t, err)
	require.Len(t, results, 5)

	crossAgentCount := 0
	for _, entry := range results {
		if isCrossAgentEntry(entry) {
			crossAgentCount++
		}
	}

	assert.Equal(t, 1, crossAgentCount, "history queries should only surface a bounded amount of cross-agent material")
}

func TestArchivalistQuerySearchTextMatchesMetadata(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := context.Background()

	entry := makeEntry(CategoryInsight, "General planning note", SourceModelArchivalist)
	entry.Metadata = map[string]any{
		"research_slug": "auth-pipeline-hardening",
	}
	result := a.StoreEntry(ctx, entry)
	require.True(t, result.Success)

	other := makeEntry(CategoryInsight, "Unrelated note", SourceModelArchivalist)
	result = a.StoreEntry(ctx, other)
	require.True(t, result.Success)

	results, err := a.Query(ctx, ArchiveQuery{
		SearchText: "auth-pipeline-hardening",
		Limit:      10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, entry.Metadata["research_slug"], results[0].Metadata["research_slug"])
}

func TestKnowledgeQuerySearchFeedbackUsesResultIDs(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := context.Background()

	entry := makeEntry(CategoryInsight, "Engineer note about retry backoff", SourceModelArchivalist)
	entry.Metadata = normalizeCrossAgentMetadata(map[string]any{
		"origin_agent_type": "engineer",
	}, "engineer-1", "engineer")
	storeResult := a.StoreEntry(ctx, entry)
	require.True(t, storeResult.Success)

	input := []byte(`{"action":"search_feedback","result_ids":["` + storeResult.ID + `"],"was_useful":true}`)
	result := a.skills.Invoke(ctx, "knowledge_query", input)
	require.NoError(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, data["recorded"])

	recordedFor, ok := data["recorded_for"].([]string)
	require.True(t, ok)
	assert.Contains(t, recordedFor, "engineer")
	assert.Greater(t, a.crossAgentWeights.WeighResult("engineer"), 0.5)
}

func TestArchiveSearchTextUsesRelationalFiltering(t *testing.T) {
	store, archive := newTestStoreWithArchive(t)
	session := store.GetCurrentSession()
	require.NotNil(t, session)
	require.NoError(t, archive.SaveSession(session))

	authEntry := makeEntryWithTitle(CategoryInsight, "Authentication", "JWT retry guidance", SourceModelArchivalist)
	authEntry.ID = "archived-auth"
	authEntry.SessionID = session.ID
	authEntry.CreatedAt = time.Now().Add(-time.Minute)
	authEntry.UpdatedAt = authEntry.CreatedAt
	require.NoError(t, archive.ArchiveEntry(authEntry))

	otherEntry := makeEntryWithTitle(CategoryInsight, "Database", "Connection pooling guidance", SourceModelArchivalist)
	otherEntry.ID = "archived-db"
	otherEntry.SessionID = session.ID
	otherEntry.CreatedAt = time.Now()
	otherEntry.UpdatedAt = otherEntry.CreatedAt
	require.NoError(t, archive.ArchiveEntry(otherEntry))

	results, err := archive.SearchText("auth", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Authentication", results[0].Title)
}

func TestHandoffQueryByPatternIncludesExplicitCrossAgentArtifacts(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		entry := &Entry{
			ID:        "handoff-auth-" + string(rune('A'+i)),
			Category:  BuildHandoffCategory("engineer"),
			Title:     BuildHandoffTitle("engineer", i+1),
			Content:   "Authentication retry work is ready for pickup",
			Source:    SourceModelArchivalist,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Minute),
			Metadata: normalizeCrossAgentMetadata(map[string]any{
				"handoff_reason":     "auth pipeline update",
				"origin_agent_type":  "engineer",
				"origin_source_type": "handoff",
			}, "", "engineer"),
		}
		result := a.StoreEntry(ctx, entry)
		require.True(t, result.Success)
	}

	records, err := a.Query(ctx, ArchiveQuery{
		Categories:       handoffCategories(),
		SearchText:       "authentication retry",
		Limit:            5,
		CrossAgentPolicy: CrossAgentPolicyInclude,
	})
	require.NoError(t, err)
	require.Len(t, records, 2)
}
