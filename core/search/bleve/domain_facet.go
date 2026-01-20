package bleve

import (
	"sort"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/search"
	"github.com/blevesearch/bleve/v2"
)

// DomainFacetName is the facet name used for domain aggregation in search requests.
const DomainFacetName = "domain_facet"

// DomainDistribution maps domains to their document counts.
type DomainDistribution map[domain.Domain]int

// DomainFacetResult contains the domain distribution from a faceted search.
type DomainFacetResult struct {
	Distribution DomainDistribution
	TotalHits    int64
	OtherCount   int
}

// Total returns the total count across all domains.
func (dfr *DomainFacetResult) Total() int {
	total := 0
	for _, count := range dfr.Distribution {
		total += count
	}
	return total + dfr.OtherCount
}

// TopDomains returns domains sorted by count in descending order.
func (dfr *DomainFacetResult) TopDomains(limit int) []DomainCount {
	if limit <= 0 {
		return nil
	}

	counts := dfr.toSortedCounts()
	if len(counts) <= limit {
		return counts
	}
	return counts[:limit]
}

func (dfr *DomainFacetResult) toSortedCounts() []DomainCount {
	counts := make([]DomainCount, 0, len(dfr.Distribution))
	for d, c := range dfr.Distribution {
		counts = append(counts, DomainCount{Domain: d, Count: c})
	}
	sortDomainCounts(counts)
	return counts
}

// DomainCount pairs a domain with its count.
type DomainCount struct {
	Domain domain.Domain
	Count  int
}

func sortDomainCounts(counts []DomainCount) {
	sort.Slice(counts, func(i, j int) bool {
		return counts[i].Count > counts[j].Count
	})
}

// HasDomain returns true if the domain has at least one document.
func (dfr *DomainFacetResult) HasDomain(d domain.Domain) bool {
	return dfr.Distribution[d] > 0
}

// DominantDomain returns the domain with the most documents, or DomainLibrarian if empty.
func (dfr *DomainFacetResult) DominantDomain() domain.Domain {
	if len(dfr.Distribution) == 0 {
		return domain.DomainLibrarian
	}

	var maxDomain domain.Domain
	maxCount := -1
	for d, c := range dfr.Distribution {
		if c > maxCount {
			maxDomain = d
			maxCount = c
		}
	}
	return maxDomain
}

// NewDomainFacetRequest creates a facet request for domain distribution.
func NewDomainFacetRequest() *bleve.FacetRequest {
	return bleve.NewFacetRequest(DomainFieldName, len(domain.ValidDomains()))
}

// AddDomainFacet adds a domain facet to the search request.
func AddDomainFacet(searchReq *bleve.SearchRequest) {
	if searchReq.Facets == nil {
		searchReq.Facets = make(bleve.FacetsRequest)
	}
	searchReq.Facets[DomainFacetName] = NewDomainFacetRequest()
}

// ExtractDomainFacet extracts the domain distribution from search results.
func ExtractDomainFacet(result *bleve.SearchResult) *DomainFacetResult {
	dfr := &DomainFacetResult{
		Distribution: make(DomainDistribution),
		TotalHits:    int64(result.Total),
	}

	facetResult := result.Facets[DomainFacetName]
	if facetResult == nil {
		return dfr
	}

	for _, term := range facetResult.Terms.Terms() {
		d, ok := domain.ParseDomain(term.Term)
		if ok {
			dfr.Distribution[d] = term.Count
		}
	}
	dfr.OtherCount = facetResult.Other

	return dfr
}

// GetDomainDistribution computes domain distribution from scored documents.
// Use this when faceting wasn't enabled in the original search.
func GetDomainDistribution(docs []search.ScoredDocument) DomainDistribution {
	dist := make(DomainDistribution)
	for _, doc := range docs {
		d := InferDomainFromPath(doc.Path)
		dist[d]++
	}
	return dist
}

// GetDomainDistributionFromResult extracts domain distribution from a SearchResult.
func GetDomainDistributionFromResult(result *search.SearchResult) DomainDistribution {
	return GetDomainDistribution(result.Documents)
}

// FilterByDomain filters documents to only those matching the given domain.
func FilterByDomain(docs []search.ScoredDocument, d domain.Domain) []search.ScoredDocument {
	filtered := make([]search.ScoredDocument, 0, len(docs))
	for _, doc := range docs {
		if InferDomainFromPath(doc.Path) == d {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

// GroupByDomain groups documents by their inferred domain.
func GroupByDomain(docs []search.ScoredDocument) map[domain.Domain][]search.ScoredDocument {
	groups := make(map[domain.Domain][]search.ScoredDocument)
	for _, doc := range docs {
		d := InferDomainFromPath(doc.Path)
		groups[d] = append(groups[d], doc)
	}
	return groups
}

// DomainCoverage calculates what percentage of results are in each domain.
type DomainCoverage map[domain.Domain]float64

// CalculateCoverage computes the coverage percentage for each domain.
func CalculateCoverage(dist DomainDistribution) DomainCoverage {
	coverage := make(DomainCoverage)
	total := 0
	for _, count := range dist {
		total += count
	}

	if total == 0 {
		return coverage
	}

	for d, count := range dist {
		coverage[d] = float64(count) / float64(total) * 100.0
	}
	return coverage
}

// IsCrossDomain returns true if results span multiple domains.
func IsCrossDomain(dist DomainDistribution) bool {
	nonZeroCount := 0
	for _, count := range dist {
		if count > 0 {
			nonZeroCount++
		}
		if nonZeroCount > 1 {
			return true
		}
	}
	return false
}

// DominantDomainFromDistribution returns the domain with the highest count.
func DominantDomainFromDistribution(dist DomainDistribution) domain.Domain {
	var maxDomain domain.Domain
	maxCount := -1
	for d, c := range dist {
		if c > maxCount {
			maxDomain = d
			maxCount = c
		}
	}
	if maxCount <= 0 {
		return domain.DomainLibrarian
	}
	return maxDomain
}
