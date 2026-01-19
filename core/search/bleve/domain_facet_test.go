package bleve

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/search"
	"github.com/blevesearch/bleve/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainFacetResultTotal(t *testing.T) {
	dfr := &DomainFacetResult{
		Distribution: DomainDistribution{
			domain.DomainLibrarian: 10,
			domain.DomainAcademic:  5,
		},
		OtherCount: 2,
	}

	assert.Equal(t, 17, dfr.Total())
}

func TestDomainFacetResultTotal_Empty(t *testing.T) {
	dfr := &DomainFacetResult{
		Distribution: make(DomainDistribution),
	}

	assert.Equal(t, 0, dfr.Total())
}

func TestDomainFacetResultTopDomains(t *testing.T) {
	dfr := &DomainFacetResult{
		Distribution: DomainDistribution{
			domain.DomainLibrarian:   10,
			domain.DomainAcademic:    5,
			domain.DomainArchivalist: 15,
		},
	}

	top := dfr.TopDomains(2)
	require.Len(t, top, 2)
	assert.Equal(t, domain.DomainArchivalist, top[0].Domain)
	assert.Equal(t, 15, top[0].Count)
	assert.Equal(t, domain.DomainLibrarian, top[1].Domain)
	assert.Equal(t, 10, top[1].Count)
}

func TestDomainFacetResultTopDomains_ZeroLimit(t *testing.T) {
	dfr := &DomainFacetResult{
		Distribution: DomainDistribution{
			domain.DomainLibrarian: 10,
		},
	}

	assert.Nil(t, dfr.TopDomains(0))
	assert.Nil(t, dfr.TopDomains(-1))
}

func TestDomainFacetResultTopDomains_LimitExceedsCount(t *testing.T) {
	dfr := &DomainFacetResult{
		Distribution: DomainDistribution{
			domain.DomainLibrarian: 10,
			domain.DomainAcademic:  5,
		},
	}

	top := dfr.TopDomains(10)
	assert.Len(t, top, 2)
}

func TestDomainFacetResultHasDomain(t *testing.T) {
	dfr := &DomainFacetResult{
		Distribution: DomainDistribution{
			domain.DomainLibrarian: 10,
		},
	}

	assert.True(t, dfr.HasDomain(domain.DomainLibrarian))
	assert.False(t, dfr.HasDomain(domain.DomainAcademic))
}

func TestDomainFacetResultDominantDomain(t *testing.T) {
	dfr := &DomainFacetResult{
		Distribution: DomainDistribution{
			domain.DomainLibrarian:   10,
			domain.DomainAcademic:    5,
			domain.DomainArchivalist: 15,
		},
	}

	assert.Equal(t, domain.DomainArchivalist, dfr.DominantDomain())
}

func TestDomainFacetResultDominantDomain_Empty(t *testing.T) {
	dfr := &DomainFacetResult{
		Distribution: make(DomainDistribution),
	}

	assert.Equal(t, domain.DomainLibrarian, dfr.DominantDomain())
}

func TestNewDomainFacetRequest(t *testing.T) {
	req := NewDomainFacetRequest()

	require.NotNil(t, req)
	assert.Equal(t, DomainFieldName, req.Field)
	assert.Equal(t, len(domain.ValidDomains()), req.Size)
}

func TestAddDomainFacet(t *testing.T) {
	searchReq := bleve.NewSearchRequest(bleve.NewMatchAllQuery())
	require.Nil(t, searchReq.Facets)

	AddDomainFacet(searchReq)

	require.NotNil(t, searchReq.Facets)
	require.Contains(t, searchReq.Facets, DomainFacetName)
}

func TestAddDomainFacet_PreexistingFacets(t *testing.T) {
	searchReq := bleve.NewSearchRequest(bleve.NewMatchAllQuery())
	searchReq.Facets = make(bleve.FacetsRequest)
	searchReq.Facets["other_facet"] = bleve.NewFacetRequest("type", 5)

	AddDomainFacet(searchReq)

	require.Contains(t, searchReq.Facets, DomainFacetName)
	require.Contains(t, searchReq.Facets, "other_facet")
}

func TestGetDomainDistribution(t *testing.T) {
	docs := []search.ScoredDocument{
		{Document: search.Document{Path: "agents/librarian/lib.go"}},
		{Document: search.Document{Path: "agents/guide/guide.go"}},
		{Document: search.Document{Path: "docs/README.md"}},
		{Document: search.Document{Path: "docs/API.md"}},
	}

	dist := GetDomainDistribution(docs)

	assert.Equal(t, 2, dist[domain.DomainLibrarian])
	assert.Equal(t, 2, dist[domain.DomainAcademic])
}

func TestGetDomainDistribution_Empty(t *testing.T) {
	dist := GetDomainDistribution(nil)
	assert.Empty(t, dist)

	dist = GetDomainDistribution([]search.ScoredDocument{})
	assert.Empty(t, dist)
}

func TestGetDomainDistributionFromResult(t *testing.T) {
	result := &search.SearchResult{
		Documents: []search.ScoredDocument{
			{Document: search.Document{Path: "core/search/bleve/schema.go"}},
			{Document: search.Document{Path: "docs/architecture.md"}},
		},
	}

	dist := GetDomainDistributionFromResult(result)

	assert.Equal(t, 1, dist[domain.DomainLibrarian])
	assert.Equal(t, 1, dist[domain.DomainAcademic])
}

func TestFilterByDomain(t *testing.T) {
	docs := []search.ScoredDocument{
		{Document: search.Document{Path: "agents/librarian/lib.go"}},
		{Document: search.Document{Path: "docs/README.md"}},
		{Document: search.Document{Path: "core/search/types.go"}},
	}

	filtered := FilterByDomain(docs, domain.DomainLibrarian)

	require.Len(t, filtered, 2)
	assert.Equal(t, "agents/librarian/lib.go", filtered[0].Path)
	assert.Equal(t, "core/search/types.go", filtered[1].Path)
}

func TestFilterByDomain_NoMatches(t *testing.T) {
	docs := []search.ScoredDocument{
		{Document: search.Document{Path: "agents/librarian/lib.go"}},
	}

	filtered := FilterByDomain(docs, domain.DomainAcademic)
	assert.Empty(t, filtered)
}

func TestGroupByDomain(t *testing.T) {
	docs := []search.ScoredDocument{
		{Document: search.Document{Path: "agents/librarian/lib.go"}},
		{Document: search.Document{Path: "docs/README.md"}},
		{Document: search.Document{Path: "core/search/types.go"}},
	}

	groups := GroupByDomain(docs)

	require.Len(t, groups[domain.DomainLibrarian], 2)
	require.Len(t, groups[domain.DomainAcademic], 1)
}

func TestGroupByDomain_Empty(t *testing.T) {
	groups := GroupByDomain(nil)
	assert.Empty(t, groups)
}

func TestCalculateCoverage(t *testing.T) {
	dist := DomainDistribution{
		domain.DomainLibrarian: 75,
		domain.DomainAcademic:  25,
	}

	coverage := CalculateCoverage(dist)

	assert.InDelta(t, 75.0, coverage[domain.DomainLibrarian], 0.01)
	assert.InDelta(t, 25.0, coverage[domain.DomainAcademic], 0.01)
}

func TestCalculateCoverage_Empty(t *testing.T) {
	coverage := CalculateCoverage(make(DomainDistribution))
	assert.Empty(t, coverage)
}

func TestIsCrossDomain(t *testing.T) {
	tests := []struct {
		name     string
		dist     DomainDistribution
		expected bool
	}{
		{
			name:     "single domain",
			dist:     DomainDistribution{domain.DomainLibrarian: 10},
			expected: false,
		},
		{
			name: "multiple domains",
			dist: DomainDistribution{
				domain.DomainLibrarian: 10,
				domain.DomainAcademic:  5,
			},
			expected: true,
		},
		{
			name:     "empty",
			dist:     make(DomainDistribution),
			expected: false,
		},
		{
			name: "one zero one nonzero",
			dist: DomainDistribution{
				domain.DomainLibrarian: 10,
				domain.DomainAcademic:  0,
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, IsCrossDomain(tc.dist))
		})
	}
}

func TestDominantDomainFromDistribution(t *testing.T) {
	tests := []struct {
		name     string
		dist     DomainDistribution
		expected domain.Domain
	}{
		{
			name: "single highest",
			dist: DomainDistribution{
				domain.DomainLibrarian: 10,
				domain.DomainAcademic:  5,
			},
			expected: domain.DomainLibrarian,
		},
		{
			name:     "empty returns librarian",
			dist:     make(DomainDistribution),
			expected: domain.DomainLibrarian,
		},
		{
			name: "all zeros returns librarian",
			dist: DomainDistribution{
				domain.DomainAcademic: 0,
			},
			expected: domain.DomainLibrarian,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, DominantDomainFromDistribution(tc.dist))
		})
	}
}

func TestDomainFacetNameConstant(t *testing.T) {
	assert.Equal(t, "domain_facet", DomainFacetName)
}

func TestSortDomainCounts(t *testing.T) {
	counts := []DomainCount{
		{Domain: domain.DomainLibrarian, Count: 5},
		{Domain: domain.DomainAcademic, Count: 15},
		{Domain: domain.DomainArchivalist, Count: 10},
	}

	sortDomainCounts(counts)

	assert.Equal(t, 15, counts[0].Count)
	assert.Equal(t, 10, counts[1].Count)
	assert.Equal(t, 5, counts[2].Count)
}

func TestDomainFacet_Integration_WithIndex(t *testing.T) {
	tmpDir := t.TempDir()
	manager := NewIndexManager(tmpDir + "/test.bleve")
	require.NoError(t, manager.Open())
	defer manager.Close()

	searchReq := bleve.NewSearchRequest(bleve.NewMatchAllQuery())
	AddDomainFacet(searchReq)

	result, err := manager.index.Search(searchReq)
	require.NoError(t, err)

	dfr := ExtractDomainFacet(result)
	require.NotNil(t, dfr)
	assert.Equal(t, int64(0), dfr.TotalHits)
}

func createTestDocs() []search.ScoredDocument {
	return []search.ScoredDocument{
		{
			Document: search.Document{
				ID:        "1",
				Path:      "agents/librarian/librarian.go",
				Type:      search.DocTypeSourceCode,
				Content:   "package librarian",
				Checksum:  "abc123",
				IndexedAt: time.Now(),
			},
			Score: 1.0,
		},
		{
			Document: search.Document{
				ID:        "2",
				Path:      "docs/architecture.md",
				Type:      search.DocTypeMarkdown,
				Content:   "# Architecture",
				Checksum:  "def456",
				IndexedAt: time.Now(),
			},
			Score: 0.9,
		},
		{
			Document: search.Document{
				ID:        "3",
				Path:      "sessions/session_001.json",
				Type:      search.DocTypeConfig,
				Content:   "{}",
				Checksum:  "ghi789",
				IndexedAt: time.Now(),
			},
			Score: 0.8,
		},
	}
}

func TestGetDomainDistribution_MixedDomains(t *testing.T) {
	docs := createTestDocs()
	dist := GetDomainDistribution(docs)

	assert.Equal(t, 1, dist[domain.DomainLibrarian])
	assert.Equal(t, 1, dist[domain.DomainAcademic])
	assert.Equal(t, 1, dist[domain.DomainArchivalist])
}

func TestFilterByDomain_PreservesScores(t *testing.T) {
	docs := createTestDocs()
	filtered := FilterByDomain(docs, domain.DomainLibrarian)

	require.Len(t, filtered, 1)
	assert.Equal(t, 1.0, filtered[0].Score)
	assert.Equal(t, "1", filtered[0].ID)
}

func TestGroupByDomain_PreservesDocuments(t *testing.T) {
	docs := createTestDocs()
	groups := GroupByDomain(docs)

	for _, groupDocs := range groups {
		for _, doc := range groupDocs {
			assert.NotEmpty(t, doc.ID)
			assert.NotEmpty(t, doc.Path)
		}
	}
}
