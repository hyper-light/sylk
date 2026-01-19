package bleve

import (
	"testing"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDomainQuery(t *testing.T) {
	dq := NewDomainQuery("test query")

	assert.Equal(t, "test query", dq.TextQuery)
	assert.Nil(t, dq.AllowedDomains)
	assert.Nil(t, dq.ExcludeDomains)
}

func TestDomainQuery_WithAllowedDomains(t *testing.T) {
	dq := NewDomainQuery("test").
		WithAllowedDomains(domain.DomainLibrarian, domain.DomainAcademic)

	require.Len(t, dq.AllowedDomains, 2)
	assert.Equal(t, domain.DomainLibrarian, dq.AllowedDomains[0])
	assert.Equal(t, domain.DomainAcademic, dq.AllowedDomains[1])
}

func TestDomainQuery_WithExcludedDomains(t *testing.T) {
	dq := NewDomainQuery("test").
		WithExcludedDomains(domain.DomainTester)

	require.Len(t, dq.ExcludeDomains, 1)
	assert.Equal(t, domain.DomainTester, dq.ExcludeDomains[0])
}

func TestDomainQuery_Chaining(t *testing.T) {
	dq := NewDomainQuery("test").
		WithAllowedDomains(domain.DomainLibrarian).
		WithExcludedDomains(domain.DomainTester)

	assert.Equal(t, "test", dq.TextQuery)
	require.Len(t, dq.AllowedDomains, 1)
	require.Len(t, dq.ExcludeDomains, 1)
}

func TestDomainQuery_Build_NoFilters(t *testing.T) {
	dq := NewDomainQuery("test query")
	q := dq.Build()

	_, isQueryString := q.(*query.QueryStringQuery)
	assert.True(t, isQueryString, "should return QueryStringQuery when no filters")
}

func TestDomainQuery_Build_SingleAllowedDomain(t *testing.T) {
	dq := NewDomainQuery("test").
		WithAllowedDomains(domain.DomainLibrarian)
	q := dq.Build()

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok, "should return BooleanQuery with filters")
	require.NotNil(t, boolQ)
}

func TestDomainQuery_Build_MultipleAllowedDomains(t *testing.T) {
	dq := NewDomainQuery("test").
		WithAllowedDomains(domain.DomainLibrarian, domain.DomainAcademic, domain.DomainArchivalist)
	q := dq.Build()

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok, "should return BooleanQuery")
	require.NotNil(t, boolQ)
}

func TestDomainQuery_Build_ExcludedDomains(t *testing.T) {
	dq := NewDomainQuery("test").
		WithExcludedDomains(domain.DomainTester, domain.DomainInspector)
	q := dq.Build()

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok, "should return BooleanQuery")
	require.NotNil(t, boolQ)
}

func TestDomainQuery_Build_BothAllowedAndExcluded(t *testing.T) {
	dq := NewDomainQuery("test").
		WithAllowedDomains(domain.DomainLibrarian, domain.DomainAcademic).
		WithExcludedDomains(domain.DomainTester)
	q := dq.Build()

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok, "should return BooleanQuery")
	require.NotNil(t, boolQ)
}

func TestBuildDomainQuery_NoDomainsReturnsQueryString(t *testing.T) {
	q := BuildDomainQuery("test", nil)

	_, isQueryString := q.(*query.QueryStringQuery)
	assert.True(t, isQueryString)
}

func TestBuildDomainQuery_EmptyDomainsReturnsQueryString(t *testing.T) {
	q := BuildDomainQuery("test", []domain.Domain{})

	_, isQueryString := q.(*query.QueryStringQuery)
	assert.True(t, isQueryString)
}

func TestBuildDomainQuery_WithDomains(t *testing.T) {
	q := BuildDomainQuery("test", []domain.Domain{domain.DomainLibrarian})

	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool, "should return BooleanQuery with domains")
}

func TestBuildDomainExcludeQuery_NoDomains(t *testing.T) {
	q := BuildDomainExcludeQuery("test", nil)

	_, isQueryString := q.(*query.QueryStringQuery)
	assert.True(t, isQueryString)
}

func TestBuildDomainExcludeQuery_WithDomains(t *testing.T) {
	q := BuildDomainExcludeQuery("test", []domain.Domain{domain.DomainTester})

	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool)
}

func TestBuildSingleDomainQuery(t *testing.T) {
	q := BuildSingleDomainQuery("test", domain.DomainLibrarian)

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok)
	require.NotNil(t, boolQ)
}

func TestBuildKnowledgeDomainsQuery(t *testing.T) {
	q := BuildKnowledgeDomainsQuery("test")

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok)
	require.NotNil(t, boolQ)
}

func TestBuildPipelineDomainsQuery(t *testing.T) {
	q := BuildPipelineDomainsQuery("test")

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok)
	require.NotNil(t, boolQ)
}

func TestDomainQuery_AllKnowledgeDomains(t *testing.T) {
	knowledgeDomains := domain.KnowledgeDomains()
	dq := NewDomainQuery("search term").WithAllowedDomains(knowledgeDomains...)
	q := dq.Build()

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool)
}

func TestDomainQuery_AllPipelineDomains(t *testing.T) {
	pipelineDomains := domain.PipelineDomains()
	dq := NewDomainQuery("search term").WithAllowedDomains(pipelineDomains...)
	q := dq.Build()

	require.NotNil(t, q)
	_, isBool := q.(*query.BooleanQuery)
	assert.True(t, isBool)
}

func TestDomainQuery_EmptyTextQuery(t *testing.T) {
	dq := NewDomainQuery("")
	q := dq.Build()

	require.NotNil(t, q)
}

func TestDomainQuery_SpecialCharactersInQuery(t *testing.T) {
	specialQueries := []string{
		"func()",
		"path/to/file",
		"error != nil",
		"type *struct",
		"import \"fmt\"",
	}

	for _, textQuery := range specialQueries {
		t.Run(textQuery, func(t *testing.T) {
			dq := NewDomainQuery(textQuery).
				WithAllowedDomains(domain.DomainLibrarian)
			q := dq.Build()
			require.NotNil(t, q)
		})
	}
}

func TestDomainQuery_AllDomainsFilter(t *testing.T) {
	allDomains := domain.ValidDomains()
	dq := NewDomainQuery("test").WithAllowedDomains(allDomains...)
	q := dq.Build()

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok)
	require.NotNil(t, boolQ)
}

func TestDomainQuery_MultipleBuildCalls(t *testing.T) {
	dq := NewDomainQuery("test").
		WithAllowedDomains(domain.DomainLibrarian)

	q1 := dq.Build()
	q2 := dq.Build()

	require.NotNil(t, q1)
	require.NotNil(t, q2)
}

func TestDomainQuery_ModifyAfterBuild(t *testing.T) {
	dq := NewDomainQuery("test")
	q1 := dq.Build()

	dq.WithAllowedDomains(domain.DomainLibrarian)
	q2 := dq.Build()

	_, isQueryString := q1.(*query.QueryStringQuery)
	_, isBool := q2.(*query.BooleanQuery)

	assert.True(t, isQueryString)
	assert.True(t, isBool)
}

func TestBuildSingleDomainQuery_AllDomains(t *testing.T) {
	domains := domain.ValidDomains()

	for _, d := range domains {
		t.Run(d.String(), func(t *testing.T) {
			q := BuildSingleDomainQuery("test", d)
			require.NotNil(t, q)

			_, isBool := q.(*query.BooleanQuery)
			assert.True(t, isBool)
		})
	}
}

func TestBuildDomainQuery_DomainStringInQuery(t *testing.T) {
	q := BuildDomainQuery("librarian search", []domain.Domain{domain.DomainLibrarian})

	boolQ, ok := q.(*query.BooleanQuery)
	require.True(t, ok)
	require.NotNil(t, boolQ)
}

func TestDomainQuery_Integration_WithIndex(t *testing.T) {
	tmpDir := t.TempDir()
	manager := NewIndexManager(tmpDir + "/test.bleve")
	require.NoError(t, manager.Open())
	defer manager.Close()

	q := BuildSingleDomainQuery("test content", domain.DomainLibrarian)
	require.NotNil(t, q)

	searchReq := bleve.NewSearchRequest(q)
	searchReq.Size = 10

	result, err := manager.index.Search(searchReq)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, uint64(0), result.Total)
}
