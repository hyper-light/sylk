package academic

import (
	"log/slog"
	"testing"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAcademicDomainFilter(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	require.NotNil(t, filter)
	assert.Equal(t, domain.DomainAcademic, filter.TargetDomain())
}

func TestNewAcademicDomainFilter_WithLogger(t *testing.T) {
	logger := slog.Default()
	filter := NewAcademicDomainFilter(logger)

	require.NotNil(t, filter)
	assert.Equal(t, domain.DomainAcademic, filter.TargetDomain())
}

func TestAcademicDomainFilter_ConsultationDomains(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	domains := filter.ConsultationDomains()

	require.Len(t, domains, 1)
	assert.Equal(t, domain.DomainLibrarian, domains[0])
}

func TestAcademicDomainFilter_WrapQuery(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	q := filter.WrapQuery("test query")

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool, "should return BooleanQuery with domain filter")
}

func TestAcademicDomainFilter_WrapQuery_EmptyQuery(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	q := filter.WrapQuery("")

	require.NotNil(t, q)
}

func TestAcademicDomainFilter_WrapQueryWithConsultation(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	q := filter.WrapQueryWithConsultation("test query")

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool)
}

func TestAcademicDomainFilter_WrapQueryWithDomains_NoDomains(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	q := filter.WrapQueryWithDomains("test", nil)

	require.NotNil(t, q)
}

func TestAcademicDomainFilter_WrapQueryWithDomains_WithDomains(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	q := filter.WrapQueryWithDomains("test", []domain.Domain{domain.DomainArchivalist})

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool)
}

func TestAcademicDomainFilter_FilterResults(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "docs/README.md", Type: "markdown"},
		{Path: "agents/librarian/lib.go", Type: "source_code"},
		{Path: "research/papers/algo.md", Type: "markdown"},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 2)
	assert.Equal(t, "docs/README.md", filtered[0].Path)
	assert.Equal(t, "research/papers/algo.md", filtered[1].Path)
}

func TestAcademicDomainFilter_FilterResults_Empty(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	filtered := filter.FilterResults(nil)
	assert.Empty(t, filtered)

	filtered = filter.FilterResults([]ScoredDocument{})
	assert.Empty(t, filtered)
}

func TestAcademicDomainFilter_FilterResults_AllFiltered(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "agents/librarian/lib.go", Type: "source_code"},
		{Path: "core/search/bleve.go", Type: "source_code"},
	}

	filtered := filter.FilterResults(docs)
	assert.Empty(t, filtered)
}

func TestAcademicDomainFilter_ShouldInclude(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	tests := []struct {
		path     string
		docType  string
		expected bool
	}{
		{"docs/README.md", "markdown", true},
		{"research/paper.md", "markdown", true},
		{"agents/librarian/lib.go", "source_code", false},
		{"sessions/session.json", "config", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := filter.ShouldInclude(tc.path, tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestAcademicDomainFilter_IsResearchContent(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	tests := []struct {
		path     string
		expected bool
	}{
		{"docs/README.md", true},
		{"research/paper.md", true},
		{"agents/librarian/lib.go", false},
		{"core/search/bleve.go", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := filter.IsResearchContent(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestAcademicDomainFilter_NeedsCodebaseConsultation(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	tests := []struct {
		query    string
		expected bool
	}{
		{"best practices for error handling", false},
		{"how is error handling implemented in our codebase", true},
		{"current approach to logging", true},
		{"general logging patterns", false},
		{"implementation details of search system", true},
		{"research on search algorithms", false},
		{"how we handle concurrency", true},
	}

	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			result := filter.NeedsCodebaseConsultation(tc.query)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestAcademicDomainFilter_ResearchContentPatterns(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	patterns := filter.ResearchContentPatterns()

	require.NotEmpty(t, patterns)
	assert.Contains(t, patterns, "docs/")
	assert.Contains(t, patterns, "research/")
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

func TestToLower(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"HELLO", "hello"},
		{"Hello World", "hello world"},
		{"already lower", "already lower"},
		{"MiXeD", "mixed"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := toLower(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestContainsSubstring(t *testing.T) {
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

func TestDomainNames(t *testing.T) {
	domains := []domain.Domain{domain.DomainAcademic, domain.DomainLibrarian}

	names := domainNames(domains)

	require.Len(t, names, 2)
	assert.Equal(t, "academic", names[0])
	assert.Equal(t, "librarian", names[1])
}

func TestAcademicDomainFilter_FilterResults_PreservesOrder(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	docs := []ScoredDocument{
		{Path: "docs/a.md", Type: "markdown", Score: 1.0},
		{Path: "core/b.go", Type: "source_code", Score: 0.9},
		{Path: "docs/c.md", Type: "markdown", Score: 0.8},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 2)
	assert.Equal(t, 1.0, filtered[0].Score)
	assert.Equal(t, 0.8, filtered[1].Score)
}

func TestAcademicDomainFilter_FilterResults_PreservesAllFields(t *testing.T) {
	filter := NewAcademicDomainFilter(nil)

	docs := []ScoredDocument{
		{
			ID:      "test-id",
			Path:    "docs/test.md",
			Type:    "markdown",
			Content: "# Test",
			Score:   0.95,
		},
	}

	filtered := filter.FilterResults(docs)

	require.Len(t, filtered, 1)
	assert.Equal(t, "test-id", filtered[0].ID)
	assert.Equal(t, "# Test", filtered[0].Content)
	assert.Equal(t, 0.95, filtered[0].Score)
}
