package archivalist

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestArchiveWithFacts(t *testing.T) (*Archive, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	archive, err := NewArchive(ArchiveConfig{Path: tmpDir + "/test.db"})
	require.NoError(t, err)

	// Create a test session
	session := &Session{
		ID:        "test-session",
		StartedAt: time.Now(),
	}
	err = archive.SaveSession(session)
	require.NoError(t, err)

	return archive, func() { archive.Close() }
}

func TestArchive_SaveAndQueryFactDecision(t *testing.T) {
	archive, cleanup := newTestArchiveWithFacts(t)
	defer cleanup()

	fact := &FactDecision{
		ID:             uuid.New().String(),
		Choice:         "Use PostgreSQL",
		Rationale:      "Better JSON support",
		Context:        "Database selection",
		Alternatives:   []string{"MySQL", "SQLite"},
		Confidence:     0.95,
		SourceEntryIDs: []string{"entry-1", "entry-2"},
		SessionID:      "test-session",
		ExtractedAt:    time.Now(),
	}

	err := archive.SaveFactDecision(fact)
	require.NoError(t, err)

	// Query back
	facts, err := archive.QueryFactDecisions("test-session", 10)
	require.NoError(t, err)
	require.Len(t, facts, 1)

	retrieved := facts[0]
	assert.Equal(t, fact.ID, retrieved.ID)
	assert.Equal(t, fact.Choice, retrieved.Choice)
	assert.Equal(t, fact.Rationale, retrieved.Rationale)
	assert.Equal(t, fact.Alternatives, retrieved.Alternatives)
	assert.Equal(t, fact.Confidence, retrieved.Confidence)
	assert.Equal(t, fact.SourceEntryIDs, retrieved.SourceEntryIDs)
}

func TestArchive_SaveAndQueryFactPattern(t *testing.T) {
	archive, cleanup := newTestArchiveWithFacts(t)
	defer cleanup()

	fact := &FactPattern{
		ID:             uuid.New().String(),
		Category:       "error-handling",
		Name:           "wrap errors",
		Pattern:        "Always wrap errors with context",
		Example:        `fmt.Errorf("failed: %w", err)`,
		Rationale:      "Provides stack trace",
		UsageCount:     5,
		SourceEntryIDs: []string{"entry-1"},
		SessionID:      "test-session",
		ExtractedAt:    time.Now(),
	}

	err := archive.SaveFactPattern(fact)
	require.NoError(t, err)

	// Query back
	facts, err := archive.QueryFactPatterns("error-handling", 10)
	require.NoError(t, err)
	require.Len(t, facts, 1)

	retrieved := facts[0]
	assert.Equal(t, fact.ID, retrieved.ID)
	assert.Equal(t, fact.Category, retrieved.Category)
	assert.Equal(t, fact.Name, retrieved.Name)
	assert.Equal(t, fact.Pattern, retrieved.Pattern)
	assert.Equal(t, fact.Example, retrieved.Example)
	assert.Equal(t, fact.UsageCount, retrieved.UsageCount)
}

func TestArchive_SaveAndQueryFactFailure(t *testing.T) {
	archive, cleanup := newTestArchiveWithFacts(t)
	defer cleanup()

	fact := &FactFailure{
		ID:             uuid.New().String(),
		Approach:       "Recursive file walking",
		Reason:         "Memory exhaustion without depth limit",
		Context:        "File processing",
		Resolution:     "Added max depth of 10",
		SourceEntryIDs: []string{"entry-1"},
		SessionID:      "test-session",
		ExtractedAt:    time.Now(),
	}

	err := archive.SaveFactFailure(fact)
	require.NoError(t, err)

	// Query back
	facts, err := archive.QueryFactFailures("test-session", 10)
	require.NoError(t, err)
	require.Len(t, facts, 1)

	retrieved := facts[0]
	assert.Equal(t, fact.ID, retrieved.ID)
	assert.Equal(t, fact.Approach, retrieved.Approach)
	assert.Equal(t, fact.Reason, retrieved.Reason)
	assert.Equal(t, fact.Resolution, retrieved.Resolution)
}

func TestArchive_SaveAndQueryFactFileChange(t *testing.T) {
	archive, cleanup := newTestArchiveWithFacts(t)
	defer cleanup()

	fact := &FactFileChange{
		ID:             uuid.New().String(),
		Path:           "/src/process.go",
		ChangeType:     FileChangeTypeModified,
		Description:    "Added error handling",
		LineStart:      100,
		LineEnd:        120,
		SourceEntryIDs: []string{"entry-1"},
		SessionID:      "test-session",
		ExtractedAt:    time.Now(),
	}

	err := archive.SaveFactFileChange(fact)
	require.NoError(t, err)

	// Query back
	facts, err := archive.QueryFactFileChanges("/src/process.go", 10)
	require.NoError(t, err)
	require.Len(t, facts, 1)

	retrieved := facts[0]
	assert.Equal(t, fact.ID, retrieved.ID)
	assert.Equal(t, fact.Path, retrieved.Path)
	assert.Equal(t, fact.ChangeType, retrieved.ChangeType)
	assert.Equal(t, fact.LineStart, retrieved.LineStart)
	assert.Equal(t, fact.LineEnd, retrieved.LineEnd)
}

func TestArchive_SaveAndQuerySummary(t *testing.T) {
	archive, cleanup := newTestArchiveWithFacts(t)
	defer cleanup()

	now := time.Now()
	timeStart := now.Add(-1 * time.Hour)
	timeEnd := now

	summary := &CompactedSummary{
		ID:             uuid.New().String(),
		Level:          SummaryLevelSpan,
		Scope:          SummaryScopeGeneral,
		ScopeID:        "",
		Content:        "This span covered database setup and initial API design.",
		KeyPoints:      []string{"Selected PostgreSQL", "Designed REST API", "Added error handling"},
		TokensEstimate: 150,
		SourceEntryIDs: []string{"entry-1", "entry-2", "entry-3"},
		SessionID:      "test-session",
		TimeStart:      &timeStart,
		TimeEnd:        &timeEnd,
		CreatedAt:      now,
	}

	err := archive.SaveSummary(summary)
	require.NoError(t, err)

	// Query back
	summaries, err := archive.QuerySummaries(SummaryQuery{
		Levels:     []SummaryLevel{SummaryLevelSpan},
		SessionIDs: []string{"test-session"},
	})
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	retrieved := summaries[0]
	assert.Equal(t, summary.ID, retrieved.ID)
	assert.Equal(t, summary.Level, retrieved.Level)
	assert.Equal(t, summary.Scope, retrieved.Scope)
	assert.Equal(t, summary.Content, retrieved.Content)
	assert.Equal(t, summary.KeyPoints, retrieved.KeyPoints)
	assert.Equal(t, summary.TokensEstimate, retrieved.TokensEstimate)
	assert.Equal(t, summary.SourceEntryIDs, retrieved.SourceEntryIDs)
}

func TestArchive_GetSummary(t *testing.T) {
	archive, cleanup := newTestArchiveWithFacts(t)
	defer cleanup()

	summaryID := uuid.New().String()
	summary := &CompactedSummary{
		ID:             summaryID,
		Level:          SummaryLevelSession,
		Scope:          SummaryScopeTask,
		ScopeID:        "task-123",
		Content:        "Session summary content",
		TokensEstimate: 100,
		SourceEntryIDs: []string{"entry-1"},
		SessionID:      "test-session",
		CreatedAt:      time.Now(),
	}

	err := archive.SaveSummary(summary)
	require.NoError(t, err)

	// Get by ID
	retrieved, err := archive.GetSummary(summaryID)
	require.NoError(t, err)
	assert.Equal(t, summaryID, retrieved.ID)
	assert.Equal(t, "task-123", retrieved.ScopeID)
}

func TestArchive_GetLatestSummary(t *testing.T) {
	archive, cleanup := newTestArchiveWithFacts(t)
	defer cleanup()

	// Create two summaries at different times
	summary1 := &CompactedSummary{
		ID:             uuid.New().String(),
		Level:          SummaryLevelSpan,
		Scope:          SummaryScopeGeneral,
		Content:        "First summary",
		SourceEntryIDs: []string{"entry-1"},
		SessionID:      "test-session",
		CreatedAt:      time.Now().Add(-1 * time.Hour),
	}

	summary2 := &CompactedSummary{
		ID:             uuid.New().String(),
		Level:          SummaryLevelSpan,
		Scope:          SummaryScopeGeneral,
		Content:        "Second summary",
		SourceEntryIDs: []string{"entry-2"},
		SessionID:      "test-session",
		CreatedAt:      time.Now(),
	}

	err := archive.SaveSummary(summary1)
	require.NoError(t, err)
	err = archive.SaveSummary(summary2)
	require.NoError(t, err)

	// Get latest
	latest, err := archive.GetLatestSummary(SummaryLevelSpan, SummaryScopeGeneral, "")
	require.NoError(t, err)
	assert.Equal(t, "Second summary", latest.Content)
}

func TestArchive_QueryFactPatterns_OrderByUsage(t *testing.T) {
	archive, cleanup := newTestArchiveWithFacts(t)
	defer cleanup()

	// Create patterns with different usage counts
	patterns := []*FactPattern{
		{ID: uuid.New().String(), Category: "test", Name: "low", Pattern: "p1", UsageCount: 1, SourceEntryIDs: []string{"e1"}, SessionID: "test-session", ExtractedAt: time.Now()},
		{ID: uuid.New().String(), Category: "test", Name: "high", Pattern: "p2", UsageCount: 10, SourceEntryIDs: []string{"e2"}, SessionID: "test-session", ExtractedAt: time.Now()},
		{ID: uuid.New().String(), Category: "test", Name: "medium", Pattern: "p3", UsageCount: 5, SourceEntryIDs: []string{"e3"}, SessionID: "test-session", ExtractedAt: time.Now()},
	}

	for _, p := range patterns {
		err := archive.SaveFactPattern(p)
		require.NoError(t, err)
	}

	// Query - should be ordered by usage count descending
	results, err := archive.QueryFactPatterns("test", 10)
	require.NoError(t, err)
	require.Len(t, results, 3)

	assert.Equal(t, "high", results[0].Name)
	assert.Equal(t, "medium", results[1].Name)
	assert.Equal(t, "low", results[2].Name)
}
