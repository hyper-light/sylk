package archivalist

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

// ArchivalistDomainFilter provides domain filtering for Archivalist queries.
// All queries are automatically filtered to DomainArchivalist content.
type ArchivalistDomainFilter struct {
	targetDomain domain.Domain
	logger       *slog.Logger
}

// NewArchivalistDomainFilter creates a new domain filter for Archivalist.
func NewArchivalistDomainFilter(logger *slog.Logger) *ArchivalistDomainFilter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ArchivalistDomainFilter{
		targetDomain: domain.DomainArchivalist,
		logger:       logger,
	}
}

// TargetDomain returns the domain this filter targets.
func (f *ArchivalistDomainFilter) TargetDomain() domain.Domain {
	return f.targetDomain
}

// WrapQuery wraps a text query with domain filtering for Archivalist.
func (f *ArchivalistDomainFilter) WrapQuery(textQuery string) blevequery.Query {
	return bleve.BuildSingleDomainQuery(textQuery, f.targetDomain)
}

// WrapQueryWithDomains wraps a query allowing additional domains.
func (f *ArchivalistDomainFilter) WrapQueryWithDomains(textQuery string, additionalDomains []domain.Domain) blevequery.Query {
	domains := []domain.Domain{f.targetDomain}

	if len(additionalDomains) > 0 {
		f.logCrossDomainQuery(textQuery, additionalDomains)
		domains = append(domains, additionalDomains...)
	}

	return bleve.BuildDomainQuery(textQuery, domains)
}

func (f *ArchivalistDomainFilter) logCrossDomainQuery(query string, additionalDomains []domain.Domain) {
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

// FilterResults filters results to only include Archivalist domain content.
func (f *ArchivalistDomainFilter) FilterResults(docs []ScoredDocument) []ScoredDocument {
	filtered := make([]ScoredDocument, 0, len(docs))
	for _, doc := range docs {
		if f.isArchivalistContent(doc.Path, doc.Type) {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

func (f *ArchivalistDomainFilter) isArchivalistContent(path, docType string) bool {
	inferred := bleve.InferDomain(path, docType, domain.DomainLibrarian)
	return inferred == domain.DomainArchivalist
}

// ShouldInclude returns true if the document belongs to Archivalist domain.
func (f *ArchivalistDomainFilter) ShouldInclude(path, docType string) bool {
	return f.isArchivalistContent(path, docType)
}

// IsHistoricalContent returns true if the path represents historical content.
func (f *ArchivalistDomainFilter) IsHistoricalContent(path string) bool {
	return bleve.InferDomainFromPath(path) == domain.DomainArchivalist
}

// HistoricalContentPatterns returns path patterns that match historical content.
func (f *ArchivalistDomainFilter) HistoricalContentPatterns() []string {
	return []string{
		"sessions/",
		"history/",
		"decisions/",
		"handoffs/",
		"timeline/",
		"chronicle/",
	}
}

// HistoricalContentCategories returns categories handled by Archivalist.
func (f *ArchivalistDomainFilter) HistoricalContentCategories() []Category {
	return []Category{
		CategoryTaskState,
		CategoryDecision,
		CategoryIssue,
		CategoryInsight,
		CategoryUserVoice,
		CategoryHypothesis,
		CategoryOpenThread,
		CategoryTimeline,
	}
}

// IsSessionContent returns true if the document type is session-related.
func (f *ArchivalistDomainFilter) IsSessionContent(docType string) bool {
	sessionTypes := map[string]bool{
		"llm_prompt":   true,
		"llm_response": true,
		"note":         true,
		"git_commit":   true,
	}
	return sessionTypes[docType]
}

// FilterByCategory filters results to only include specific categories.
func (f *ArchivalistDomainFilter) FilterByCategory(docs []ScoredDocument, categories []Category) []ScoredDocument {
	if len(categories) == 0 {
		return docs
	}

	categorySet := make(map[Category]bool, len(categories))
	for _, c := range categories {
		categorySet[c] = true
	}

	filtered := make([]ScoredDocument, 0, len(docs))
	for _, doc := range docs {
		if f.docMatchesCategories(doc, categorySet) {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

func (f *ArchivalistDomainFilter) docMatchesCategories(doc ScoredDocument, categories map[Category]bool) bool {
	inferredCategory := f.inferCategory(doc.Path, doc.Type)
	return categories[inferredCategory]
}

func (f *ArchivalistDomainFilter) inferCategory(path, docType string) Category {
	if containsSubstring(path, "decisions/") {
		return CategoryDecision
	}
	if containsSubstring(path, "timeline/") {
		return CategoryTimeline
	}
	if containsSubstring(path, "sessions/") {
		return CategoryTaskState
	}
	if docType == "llm_prompt" || docType == "llm_response" {
		return CategoryTimeline
	}
	if docType == "note" {
		return CategoryUserVoice
	}
	return CategoryGeneral
}

func truncateQuery(query string) string {
	const maxLen = 50
	if len(query) <= maxLen {
		return query
	}
	return query[:maxLen] + "..."
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
