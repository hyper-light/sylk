package librarian

import (
	"log/slog"
	"testing"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLibrarianDomainFilter(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	require.NotNil(t, filter)
	assert.Equal(t, domain.DomainLibrarian, filter.TargetDomain())
}

func TestNewLibrarianDomainFilter_WithLogger(t *testing.T) {
	logger := slog.Default()
	filter := NewLibrarianDomainFilter(logger)

	require.NotNil(t, filter)
	assert.Equal(t, domain.DomainLibrarian, filter.TargetDomain())
}

func TestLibrarianDomainFilter_WrapQuery(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	q := filter.WrapQuery("test query")

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool, "should return BooleanQuery with domain filter")
}

func TestLibrarianDomainFilter_WrapQuery_EmptyQuery(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	q := filter.WrapQuery("")

	require.NotNil(t, q)
}

func TestLibrarianDomainFilter_WrapQueryWithDomains_NoDomains(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	q := filter.WrapQueryWithDomains("test", nil)

	require.NotNil(t, q)
}

func TestLibrarianDomainFilter_WrapQueryWithDomains_WithDomains(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	q := filter.WrapQueryWithDomains("test", []domain.Domain{domain.DomainAcademic})

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool)
}

func TestLibrarianDomainFilter_FilterResults(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "agents/librarian/lib.go", Type: "source_code"},
		{Path: "docs/README.md", Type: "markdown"},
		{Path: "core/search/bleve/schema.go", Type: "source_code"},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 2)
	assert.Equal(t, "agents/librarian/lib.go", filtered[0].Path)
	assert.Equal(t, "core/search/bleve/schema.go", filtered[1].Path)
}

func TestLibrarianDomainFilter_FilterResults_Empty(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	filtered := filter.FilterResults(nil)
	assert.Empty(t, filtered)

	filtered = filter.FilterResults([]ScoredDocument{})
	assert.Empty(t, filtered)
}

func TestLibrarianDomainFilter_FilterResults_AllFiltered(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "docs/README.md", Type: "markdown"},
		{Path: "research/paper.md", Type: "markdown"},
	}

	filtered := filter.FilterResults(docs)
	assert.Empty(t, filtered)
}

func TestLibrarianDomainFilter_ShouldInclude(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	tests := []struct {
		path     string
		docType  string
		expected bool
	}{
		{"agents/librarian/lib.go", "source_code", true},
		{"core/search/bleve/schema.go", "source_code", true},
		{"docs/README.md", "markdown", false},
		{"sessions/session.json", "config", false},
		{"unknown/file.go", "source_code", true},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := filter.ShouldInclude(tc.path, tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestLibrarianDomainFilter_IsCodeContent(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	tests := []struct {
		path     string
		expected bool
	}{
		{"agents/librarian/lib.go", true},
		{"core/search/bleve/schema.go", true},
		{"docs/README.md", false},
		{"tests/unit_test.go", false},
		{"unknown/file.go", true},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := filter.IsCodeContent(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestLibrarianDomainFilter_CodeContentPatterns(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	patterns := filter.CodeContentPatterns()

	require.NotEmpty(t, patterns)
	assert.Contains(t, patterns, "agents/")
	assert.Contains(t, patterns, "core/")
}

func TestTruncateQuery(t *testing.T) {
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

func TestLibrarianDomainFilter_FilterResults_PreservesOrder(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "core/a.go", Type: "source_code", Score: 1.0},
		{Path: "docs/b.md", Type: "markdown", Score: 0.9},
		{Path: "core/c.go", Type: "source_code", Score: 0.8},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 2)
	assert.Equal(t, 1.0, filtered[0].Score)
	assert.Equal(t, 0.8, filtered[1].Score)
}

func TestLibrarianDomainFilter_FilterResults_PreservesAllFields(t *testing.T) {
	filter := NewLibrarianDomainFilter(nil)

	docs := []ScoredDocument{
		{
			ID:      "test-id",
			Path:    "core/test.go",
			Type:    "source_code",
			Content: "package test",
			Score:   0.95,
			Line:    10,
		},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 1)
	assert.Equal(t, "test-id", filtered[0].ID)
	assert.Equal(t, "package test", filtered[0].Content)
	assert.Equal(t, 0.95, filtered[0].Score)
	assert.Equal(t, 10, filtered[0].Line)
}
