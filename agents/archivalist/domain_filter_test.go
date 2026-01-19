package archivalist

import (
	"log/slog"
	"testing"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewArchivalistDomainFilter(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	require.NotNil(t, filter)
	assert.Equal(t, domain.DomainArchivalist, filter.TargetDomain())
}

func TestNewArchivalistDomainFilter_WithLogger(t *testing.T) {
	logger := slog.Default()
	filter := NewArchivalistDomainFilter(logger)

	require.NotNil(t, filter)
	assert.Equal(t, domain.DomainArchivalist, filter.TargetDomain())
}

func TestArchivalistDomainFilter_WrapQuery(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	q := filter.WrapQuery("test query")

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool, "should return BooleanQuery with domain filter")
}

func TestArchivalistDomainFilter_WrapQuery_EmptyQuery(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	q := filter.WrapQuery("")

	require.NotNil(t, q)
}

func TestArchivalistDomainFilter_WrapQueryWithDomains_NoDomains(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	q := filter.WrapQueryWithDomains("test", nil)

	require.NotNil(t, q)
}

func TestArchivalistDomainFilter_WrapQueryWithDomains_WithDomains(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	q := filter.WrapQueryWithDomains("test", []domain.Domain{domain.DomainLibrarian})

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool)
}

func TestArchivalistDomainFilter_FilterResults(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "sessions/session_001.json", Type: "config"},
		{Path: "agents/librarian/lib.go", Type: "source_code"},
		{Path: "history/decisions/arch.md", Type: "markdown"},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 2)
	assert.Equal(t, "sessions/session_001.json", filtered[0].Path)
	assert.Equal(t, "history/decisions/arch.md", filtered[1].Path)
}

func TestArchivalistDomainFilter_FilterResults_Empty(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	filtered := filter.FilterResults(nil)
	assert.Empty(t, filtered)

	filtered = filter.FilterResults([]ScoredDocument{})
	assert.Empty(t, filtered)
}

func TestArchivalistDomainFilter_FilterResults_AllFiltered(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "agents/librarian/lib.go", Type: "source_code"},
		{Path: "docs/README.md", Type: "markdown"},
	}

	filtered := filter.FilterResults(docs)
	assert.Empty(t, filtered)
}

func TestArchivalistDomainFilter_ShouldInclude(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	tests := []struct {
		path     string
		docType  string
		expected bool
	}{
		{"sessions/session_001.json", "config", true},
		{"history/decisions/arch.md", "markdown", true},
		{"decisions/api.md", "markdown", true},
		{"agents/librarian/lib.go", "source_code", false},
		{"docs/README.md", "markdown", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := filter.ShouldInclude(tc.path, tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestArchivalistDomainFilter_IsHistoricalContent(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	tests := []struct {
		path     string
		expected bool
	}{
		{"sessions/session_001.json", true},
		{"history/timeline.md", true},
		{"decisions/arch.md", true},
		{"agents/librarian/lib.go", false},
		{"docs/README.md", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := filter.IsHistoricalContent(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestArchivalistDomainFilter_HistoricalContentPatterns(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	patterns := filter.HistoricalContentPatterns()

	require.NotEmpty(t, patterns)
	assert.Contains(t, patterns, "sessions/")
	assert.Contains(t, patterns, "history/")
	assert.Contains(t, patterns, "decisions/")
}

func TestArchivalistDomainFilter_HistoricalContentCategories(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	categories := filter.HistoricalContentCategories()

	require.NotEmpty(t, categories)
	assert.Contains(t, categories, CategoryDecision)
	assert.Contains(t, categories, CategoryTimeline)
	assert.Contains(t, categories, CategoryTaskState)
}

func TestArchivalistDomainFilter_IsSessionContent(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	tests := []struct {
		docType  string
		expected bool
	}{
		{"llm_prompt", true},
		{"llm_response", true},
		{"note", true},
		{"git_commit", true},
		{"source_code", false},
		{"markdown", false},
	}

	for _, tc := range tests {
		t.Run(tc.docType, func(t *testing.T) {
			result := filter.IsSessionContent(tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestArchivalistDomainFilter_FilterByCategory(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "decisions/api.md", Type: "markdown"},
		{Path: "timeline/events.json", Type: "config"},
		{Path: "sessions/session.json", Type: "config"},
	}

	filtered := filter.FilterByCategory(docs, []Category{CategoryDecision})

	require.Len(t, filtered, 1)
	assert.Equal(t, "decisions/api.md", filtered[0].Path)
}

func TestArchivalistDomainFilter_FilterByCategory_EmptyCategories(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "decisions/api.md", Type: "markdown"},
		{Path: "timeline/events.json", Type: "config"},
	}

	filtered := filter.FilterByCategory(docs, nil)

	assert.Len(t, filtered, 2)
}

func TestArchivalistDomainFilter_FilterByCategory_MultipleCategories(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "decisions/api.md", Type: "markdown"},
		{Path: "timeline/events.json", Type: "config"},
		{Path: "sessions/session.json", Type: "config"},
	}

	filtered := filter.FilterByCategory(docs, []Category{CategoryDecision, CategoryTimeline})

	assert.Len(t, filtered, 2)
}

func TestArchivalistDomainFilter_InferCategory(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	tests := []struct {
		path     string
		docType  string
		expected Category
	}{
		{"decisions/api.md", "markdown", CategoryDecision},
		{"timeline/events.json", "config", CategoryTimeline},
		{"sessions/session.json", "config", CategoryTaskState},
		{"unknown/path.txt", "llm_prompt", CategoryTimeline},
		{"unknown/path.txt", "note", CategoryUserVoice},
		{"unknown/path.txt", "config", CategoryGeneral},
	}

	for _, tc := range tests {
		t.Run(tc.path+"_"+tc.docType, func(t *testing.T) {
			result := filter.inferCategory(tc.path, tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDomainFilterTruncateQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"short", "short"},
		{"", ""},
		{"a very long query that exceeds the maximum length for logging purposes", "a very long query that exceeds the maximum length ..."},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := truncateQuery(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDomainFilterContainsSubstring(t *testing.T) {
	tests := []struct {
		s        string
		substr   string
		expected bool
	}{
		{"hello world", "world", true},
		{"hello world", "hello", true},
		{"hello world", "xyz", false},
		{"", "test", false},
		{"test", "", true},
		{"ab", "abc", false},
	}

	for _, tc := range tests {
		t.Run(tc.s+"_"+tc.substr, func(t *testing.T) {
			result := containsSubstring(tc.s, tc.substr)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestArchivalistDomainFilter_FilterResults_PreservesOrder(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "sessions/a.json", Type: "config", Score: 1.0},
		{Path: "core/b.go", Type: "source_code", Score: 0.9},
		{Path: "sessions/c.json", Type: "config", Score: 0.8},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 2)
	assert.Equal(t, 1.0, filtered[0].Score)
	assert.Equal(t, 0.8, filtered[1].Score)
}

func TestArchivalistDomainFilter_FilterResults_PreservesAllFields(t *testing.T) {
	filter := NewArchivalistDomainFilter(nil)

	docs := []ScoredDocument{
		{
			ID:      "test-id",
			Path:    "sessions/test.json",
			Type:    "config",
			Content: "{}",
			Score:   0.95,
		},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 1)
	assert.Equal(t, "test-id", filtered[0].ID)
	assert.Equal(t, "{}", filtered[0].Content)
	assert.Equal(t, 0.95, filtered[0].Score)
}
