package librarian

import (
	"log/slog"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/search/bleve"
	blevequery "github.com/blevesearch/bleve/v2/search/query"
)

// LibrarianDomainFilter provides domain filtering for Librarian queries.
// All queries are automatically filtered to DomainLibrarian content.
type LibrarianDomainFilter struct {
	targetDomain domain.Domain
	logger       *slog.Logger
}

// NewLibrarianDomainFilter creates a new domain filter for Librarian.
func NewLibrarianDomainFilter(logger *slog.Logger) *LibrarianDomainFilter {
	if logger == nil {
		logger = slog.Default()
	}
	return &LibrarianDomainFilter{
		targetDomain: domain.DomainLibrarian,
		logger:       logger,
	}
}

// TargetDomain returns the domain this filter targets.
func (f *LibrarianDomainFilter) TargetDomain() domain.Domain {
	return f.targetDomain
}

// WrapQuery wraps a text query with domain filtering for Librarian.
func (f *LibrarianDomainFilter) WrapQuery(textQuery string) blevequery.Query {
	return bleve.BuildSingleDomainQuery(textQuery, f.targetDomain)
}

// WrapQueryWithDomains wraps a query allowing additional domains.
// Logs a warning if cross-domain query is detected.
func (f *LibrarianDomainFilter) WrapQueryWithDomains(textQuery string, additionalDomains []domain.Domain) blevequery.Query {
	domains := []domain.Domain{f.targetDomain}

	if len(additionalDomains) > 0 {
		f.logCrossDomainQuery(textQuery, additionalDomains)
		domains = append(domains, additionalDomains...)
	}

	return bleve.BuildDomainQuery(textQuery, domains)
}

func (f *LibrarianDomainFilter) logCrossDomainQuery(query string, additionalDomains []domain.Domain) {
	domainNames := make([]string, len(additionalDomains))
	for i, d := range additionalDomains {
		domainNames[i] = d.String()
	}

	f.logger.Info("cross-domain query received",
		"query", truncateQuery(query),
		"target_domain", f.targetDomain.String(),
		"additional_domains", domainNames,
	)
}

// FilterResults filters results to only include Librarian domain content.
func (f *LibrarianDomainFilter) FilterResults(docs []ScoredDocument) []ScoredDocument {
	filtered := make([]ScoredDocument, 0, len(docs))
	for _, doc := range docs {
		if f.isLibrarianContent(doc.Path, doc.Type) {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

func (f *LibrarianDomainFilter) isLibrarianContent(path, docType string) bool {
	inferred := bleve.InferDomain(path, docType, domain.DomainLibrarian)
	return inferred == domain.DomainLibrarian
}

// ShouldInclude returns true if the document belongs to Librarian domain.
func (f *LibrarianDomainFilter) ShouldInclude(path, docType string) bool {
	return f.isLibrarianContent(path, docType)
}

// IsCodeContent returns true if the path represents code content.
func (f *LibrarianDomainFilter) IsCodeContent(path string) bool {
	return bleve.InferDomainFromPath(path) == domain.DomainLibrarian
}

// CodeContentPatterns returns path patterns that match code content.
func (f *LibrarianDomainFilter) CodeContentPatterns() []string {
	return []string{
		"agents/",
		"core/",
		"cmd/",
		"pkg/",
		"internal/",
		"lib/",
		"src/",
	}
}

// truncateQuery truncates long queries for logging.
func truncateQuery(query string) string {
	const maxLen = 50
	if len(query) <= maxLen {
		return query
	}
	return query[:maxLen] + "..."
}
