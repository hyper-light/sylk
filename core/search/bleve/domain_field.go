package bleve

import (
	"strings"

	"github.com/adalundhe/sylk/core/domain"
)

// DomainMapping maps DocumentType patterns to Domain values for inference.
var DomainMapping = map[string]domain.Domain{
	"source_code":  domain.DomainLibrarian,
	"markdown":     domain.DomainLibrarian,
	"config":       domain.DomainLibrarian,
	"llm_prompt":   domain.DomainArchivalist,
	"llm_response": domain.DomainArchivalist,
	"web_fetch":    domain.DomainAcademic,
	"note":         domain.DomainArchivalist,
	"git_commit":   domain.DomainArchivalist,
}

// PathPrefixMapping maps path prefixes to Domain values for inference.
var PathPrefixMapping = map[string]domain.Domain{
	"agents/":     domain.DomainLibrarian,
	"core/":       domain.DomainLibrarian,
	"docs/":       domain.DomainAcademic,
	"research/":   domain.DomainAcademic,
	"sessions/":   domain.DomainArchivalist,
	"history/":    domain.DomainArchivalist,
	"decisions/":  domain.DomainArchivalist,
	"design/":     domain.DomainArchitect,
	"architect/":  domain.DomainArchitect,
	"workflows/":  domain.DomainOrchestrator,
	"pipelines/":  domain.DomainOrchestrator,
	"tests/":      domain.DomainTester,
	"test/":       domain.DomainTester,
	"inspection/": domain.DomainInspector,
	"review/":     domain.DomainInspector,
	"ui/":         domain.DomainDesigner,
	"frontend/":   domain.DomainDesigner,
	"styles/":     domain.DomainDesigner,
}

// InferDomainFromPath infers a Domain from a file path using prefix matching.
// Returns DomainLibrarian as default if no prefix matches.
func InferDomainFromPath(path string) domain.Domain {
	normalizedPath := strings.ToLower(path)
	for prefix, d := range PathPrefixMapping {
		if strings.HasPrefix(normalizedPath, prefix) {
			return d
		}
	}
	return domain.DomainLibrarian
}

// InferDomainFromType infers a Domain from a document type string.
// Returns DomainLibrarian as default if no type matches.
func InferDomainFromType(docType string) domain.Domain {
	normalizedType := strings.ToLower(docType)
	if d, ok := DomainMapping[normalizedType]; ok {
		return d
	}
	return domain.DomainLibrarian
}

// InferDomain infers the most appropriate Domain for a document.
// Priority: explicit domain > path-based > type-based > default (Librarian).
func InferDomain(path, docType string, explicit domain.Domain) domain.Domain {
	if explicit.IsValid() && explicit != domain.DomainLibrarian {
		return explicit
	}

	if pathDomain := InferDomainFromPath(path); pathDomain != domain.DomainLibrarian {
		return pathDomain
	}

	return InferDomainFromType(docType)
}

// DomainToString converts a Domain to its string representation for indexing.
func DomainToString(d domain.Domain) string {
	return d.String()
}

// StringToDomain converts a string to a Domain value.
// Returns DomainLibrarian and false if the string is not a valid domain.
func StringToDomain(s string) (domain.Domain, bool) {
	return domain.ParseDomain(s)
}

// ValidDomainStrings returns all valid domain strings for validation.
func ValidDomainStrings() []string {
	domains := domain.ValidDomains()
	result := make([]string, len(domains))
	for i, d := range domains {
		result[i] = d.String()
	}
	return result
}
