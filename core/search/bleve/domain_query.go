package bleve

import (
	"github.com/adalundhe/sylk/core/domain"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

// DomainQuery wraps a text query with domain filtering capabilities.
type DomainQuery struct {
	TextQuery      string
	AllowedDomains []domain.Domain
	ExcludeDomains []domain.Domain
}

// NewDomainQuery creates a new DomainQuery with the given text query.
func NewDomainQuery(textQuery string) *DomainQuery {
	return &DomainQuery{
		TextQuery:      textQuery,
		AllowedDomains: nil,
		ExcludeDomains: nil,
	}
}

// WithAllowedDomains sets the allowed domains for the query.
func (dq *DomainQuery) WithAllowedDomains(domains ...domain.Domain) *DomainQuery {
	dq.AllowedDomains = domains
	return dq
}

// WithExcludedDomains sets the excluded domains for the query.
func (dq *DomainQuery) WithExcludedDomains(domains ...domain.Domain) *DomainQuery {
	dq.ExcludeDomains = domains
	return dq
}

// Build constructs a Bleve query combining text search with domain filtering.
func (dq *DomainQuery) Build() query.Query {
	textQuery := bleve.NewQueryStringQuery(dq.TextQuery)

	if len(dq.AllowedDomains) == 0 && len(dq.ExcludeDomains) == 0 {
		return textQuery
	}

	return dq.buildFilteredQuery(textQuery)
}

func (dq *DomainQuery) buildFilteredQuery(textQuery query.Query) query.Query {
	boolQuery := bleve.NewBooleanQuery()
	boolQuery.AddMust(textQuery)

	if len(dq.AllowedDomains) > 0 {
		domainQuery := dq.buildAllowedDomainsQuery()
		boolQuery.AddMust(domainQuery)
	}

	if len(dq.ExcludeDomains) > 0 {
		for _, d := range dq.ExcludeDomains {
			excludeQuery := bleve.NewTermQuery(d.String())
			excludeQuery.SetField(DomainFieldName)
			boolQuery.AddMustNot(excludeQuery)
		}
	}

	return boolQuery
}

func (dq *DomainQuery) buildAllowedDomainsQuery() query.Query {
	if len(dq.AllowedDomains) == 1 {
		termQuery := bleve.NewTermQuery(dq.AllowedDomains[0].String())
		termQuery.SetField(DomainFieldName)
		return termQuery
	}

	disjunction := bleve.NewDisjunctionQuery()
	for _, d := range dq.AllowedDomains {
		termQuery := bleve.NewTermQuery(d.String())
		termQuery.SetField(DomainFieldName)
		disjunction.AddQuery(termQuery)
	}
	return disjunction
}

// BuildDomainQuery creates a Bleve query combining text search with domain filtering.
// This is a convenience function for simple use cases.
func BuildDomainQuery(textQuery string, allowedDomains []domain.Domain) query.Query {
	dq := NewDomainQuery(textQuery)
	if len(allowedDomains) > 0 {
		dq.WithAllowedDomains(allowedDomains...)
	}
	return dq.Build()
}

// BuildDomainExcludeQuery creates a Bleve query that excludes specified domains.
func BuildDomainExcludeQuery(textQuery string, excludeDomains []domain.Domain) query.Query {
	dq := NewDomainQuery(textQuery)
	if len(excludeDomains) > 0 {
		dq.WithExcludedDomains(excludeDomains...)
	}
	return dq.Build()
}

// BuildSingleDomainQuery creates a query filtered to a single domain.
// Optimized for the common case of querying a single agent's domain.
func BuildSingleDomainQuery(textQuery string, d domain.Domain) query.Query {
	boolQuery := bleve.NewBooleanQuery()
	boolQuery.AddMust(bleve.NewQueryStringQuery(textQuery))

	termQuery := bleve.NewTermQuery(d.String())
	termQuery.SetField(DomainFieldName)
	boolQuery.AddMust(termQuery)

	return boolQuery
}

// BuildKnowledgeDomainsQuery creates a query filtered to all knowledge agent domains.
func BuildKnowledgeDomainsQuery(textQuery string) query.Query {
	return BuildDomainQuery(textQuery, domain.KnowledgeDomains())
}

// BuildPipelineDomainsQuery creates a query filtered to all pipeline agent domains.
func BuildPipelineDomainsQuery(textQuery string) query.Query {
	return BuildDomainQuery(textQuery, domain.PipelineDomains())
}
