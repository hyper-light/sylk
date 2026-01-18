// Package analyzer provides custom Bleve analyzers and token filters for
// code-aware document search in the Sylk Document Search System.
package analyzer

import (
	"strings"

	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/registry"
)

// SnakeCaseFilterName is the registered name for this token filter.
const SnakeCaseFilterName = "snake_case"

func init() {
	registry.RegisterTokenFilter(SnakeCaseFilterName, NewSnakeCaseFilter)
}

// SnakeCaseFilter splits snake_case and kebab-case identifiers into their
// component parts while preserving the original token.
//
// Examples:
//   - "get_user_by_id" -> ["get", "user", "by", "id", "get_user_by_id"]
//   - "__init__" -> ["init", "__init__"]
//   - "get-user-id" -> ["get", "user", "id", "get-user-id"]
type SnakeCaseFilter struct{}

// NewSnakeCaseFilter creates a new SnakeCaseFilter instance.
// The config and cache parameters are required by the Bleve registry interface
// but are not used by this filter.
func NewSnakeCaseFilter(
	config map[string]interface{},
	cache *registry.Cache,
) (analysis.TokenFilter, error) {
	return &SnakeCaseFilter{}, nil
}

// Filter processes the input token stream, splitting snake_case and kebab-case
// tokens into their component parts. For each token containing underscores or
// hyphens, the filter emits the split parts first, followed by the original
// token.
func (f *SnakeCaseFilter) Filter(input analysis.TokenStream) analysis.TokenStream {
	var output analysis.TokenStream

	for _, token := range input {
		term := string(token.Term)
		parts := splitOnSeparators(term)
		output = appendSplitParts(output, parts, term, token)
		output = append(output, token)
	}

	return output
}

// splitOnSeparators splits a string on underscores and hyphens,
// returning non-empty parts with leading/trailing underscores stripped.
func splitOnSeparators(term string) []string {
	// Replace hyphens with underscores for uniform splitting
	normalized := strings.ReplaceAll(term, "-", "_")
	rawParts := strings.Split(normalized, "_")

	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// appendSplitParts adds the split parts to the output stream if the parts
// differ from the original term. This handles both multi-part splits
// (get_user -> get, user) and single-part extractions (_private -> private).
func appendSplitParts(
	output analysis.TokenStream,
	parts []string,
	originalTerm string,
	original *analysis.Token,
) analysis.TokenStream {
	if !shouldEmitParts(parts, originalTerm) {
		return output
	}

	for _, part := range parts {
		newToken := &analysis.Token{
			Term:     []byte(part),
			Position: original.Position,
			Start:    original.Start,
			End:      original.End,
			Type:     original.Type,
		}
		output = append(output, newToken)
	}
	return output
}

// shouldEmitParts determines if the split parts should be emitted as tokens.
// Parts are emitted when there are multiple parts, or when a single part
// differs from the original term (e.g., _private -> private).
func shouldEmitParts(parts []string, originalTerm string) bool {
	if len(parts) == 0 {
		return false
	}
	if len(parts) > 1 {
		return true
	}
	// Single part: emit only if it differs from original
	return parts[0] != originalTerm
}
