package academic

import (
	"log/slog"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/search/bleve"
	blevequery "github.com/blevesearch/bleve/v2/search/query"
)

// ScoredDocument represents a document with its search relevance score.
type ScoredDocument struct {
	ID      string  `json:"id"`
	Path    string  `json:"path"`
	Type    string  `json:"type"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// AcademicDomainFilter provides domain filtering for Academic queries.
// All queries are automatically filtered to DomainAcademic content.
type AcademicDomainFilter struct {
	targetDomain        domain.Domain
	consultationDomains []domain.Domain
	logger              *slog.Logger
}

// NewAcademicDomainFilter creates a new domain filter for Academic.
func NewAcademicDomainFilter(logger *slog.Logger) *AcademicDomainFilter {
	if logger == nil {
		logger = slog.Default()
	}
	return &AcademicDomainFilter{
		targetDomain:        domain.DomainAcademic,
		consultationDomains: []domain.Domain{domain.DomainLibrarian},
		logger:              logger,
	}
}

// TargetDomain returns the domain this filter targets.
func (f *AcademicDomainFilter) TargetDomain() domain.Domain {
	return f.targetDomain
}

// ConsultationDomains returns domains Academic can consult for additional context.
func (f *AcademicDomainFilter) ConsultationDomains() []domain.Domain {
	return f.consultationDomains
}

// WrapQuery wraps a text query with domain filtering for Academic.
func (f *AcademicDomainFilter) WrapQuery(textQuery string) blevequery.Query {
	return bleve.BuildSingleDomainQuery(textQuery, f.targetDomain)
}

// WrapQueryWithConsultation wraps a query including consultation domains.
// Used when Academic needs to cross-reference with Librarian's codebase knowledge.
func (f *AcademicDomainFilter) WrapQueryWithConsultation(textQuery string) blevequery.Query {
	domains := append([]domain.Domain{f.targetDomain}, f.consultationDomains...)

	f.logger.Debug("cross-domain consultation query",
		"query", truncateQuery(textQuery),
		"domains", domainNames(domains),
	)

	return bleve.BuildDomainQuery(textQuery, domains)
}

// WrapQueryWithDomains wraps a query allowing additional domains.
func (f *AcademicDomainFilter) WrapQueryWithDomains(textQuery string, additionalDomains []domain.Domain) blevequery.Query {
	domains := []domain.Domain{f.targetDomain}

	if len(additionalDomains) > 0 {
		f.logCrossDomainQuery(textQuery, additionalDomains)
		domains = append(domains, additionalDomains...)
	}

	return bleve.BuildDomainQuery(textQuery, domains)
}

func (f *AcademicDomainFilter) logCrossDomainQuery(query string, additionalDomains []domain.Domain) {
	f.logger.Info("cross-domain query received",
		"query", truncateQuery(query),
		"target_domain", f.targetDomain.String(),
		"additional_domains", domainNames(additionalDomains),
	)
}

// FilterResults filters results to only include Academic domain content.
func (f *AcademicDomainFilter) FilterResults(docs []ScoredDocument) []ScoredDocument {
	filtered := make([]ScoredDocument, 0, len(docs))
	for _, doc := range docs {
		if f.isAcademicContent(doc.Path, doc.Type) {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

func (f *AcademicDomainFilter) isAcademicContent(path, docType string) bool {
	inferred := bleve.InferDomain(path, docType, domain.DomainLibrarian)
	return inferred == domain.DomainAcademic
}

// ShouldInclude returns true if the document belongs to Academic domain.
func (f *AcademicDomainFilter) ShouldInclude(path, docType string) bool {
	return f.isAcademicContent(path, docType)
}

// IsResearchContent returns true if the path represents research content.
func (f *AcademicDomainFilter) IsResearchContent(path string) bool {
	return bleve.InferDomainFromPath(path) == domain.DomainAcademic
}

// NeedsCodebaseConsultation returns true if the query likely needs codebase context.
func (f *AcademicDomainFilter) NeedsCodebaseConsultation(query string) bool {
	codebaseIndicators := []string{
		"implementation",
		"codebase",
		"existing code",
		"current approach",
		"how we",
		"our code",
		"project",
	}

	lowerQuery := toLower(query)
	for _, indicator := range codebaseIndicators {
		if containsSubstring(lowerQuery, indicator) {
			return true
		}
	}
	return false
}

// ResearchContentPatterns returns path patterns that match research content.
func (f *AcademicDomainFilter) ResearchContentPatterns() []string {
	return []string{
		"docs/",
		"research/",
		"papers/",
		"articles/",
		"references/",
	}
}

func domainNames(domains []domain.Domain) []string {
	names := make([]string, len(domains))
	for i, d := range domains {
		names[i] = d.String()
	}
	return names
}

func truncateQuery(query string) string {
	const maxLen = 50
	if len(query) <= maxLen {
		return query
	}
	return query[:maxLen] + "..."
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func containsSubstring(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
