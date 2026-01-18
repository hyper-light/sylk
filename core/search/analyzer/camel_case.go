// Package analyzer provides custom text analysis components for the Sylk
// Document Search System, including token filters for code-aware tokenization.
package analyzer

import (
	"unicode"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/registry"
)

// CamelCaseFilterName is the registered name for this filter in Bleve.
const CamelCaseFilterName = "camel_case"

// CamelCaseFilter splits camelCase and PascalCase tokens into their component
// parts while preserving the original token. For example:
//   - handleHTTPError -> ["handleHTTPError", "handle", "HTTP", "Error"]
//   - XMLHTTPRequest  -> ["XMLHTTPRequest", "XML", "HTTP", "Request"]
type CamelCaseFilter struct{}

// NewCamelCaseFilter creates a new CamelCaseFilter instance.
func NewCamelCaseFilter() *CamelCaseFilter {
	return &CamelCaseFilter{}
}

// Filter processes the input token stream and returns a new stream with
// original tokens plus their camelCase split parts.
func (f *CamelCaseFilter) Filter(input analysis.TokenStream) analysis.TokenStream {
	result := make(analysis.TokenStream, 0, len(input)*2)

	for _, token := range input {
		result = f.processToken(result, token)
	}

	return result
}

// processToken handles a single token, appending the original and any split parts.
func (f *CamelCaseFilter) processToken(result analysis.TokenStream, token *analysis.Token) analysis.TokenStream {
	result = append(result, token)

	parts := splitCamelCase(string(token.Term))
	if len(parts) <= 1 {
		return result
	}

	for _, part := range parts {
		result = append(result, f.createPartToken(token, part))
	}

	return result
}

// createPartToken creates a new token for a split part, copying metadata from original.
func (f *CamelCaseFilter) createPartToken(original *analysis.Token, part string) *analysis.Token {
	return &analysis.Token{
		Term:     []byte(part),
		Start:    original.Start,
		End:      original.End,
		Position: original.Position,
		Type:     original.Type,
		KeyWord:  original.KeyWord,
	}
}

// splitCamelCase splits a camelCase or PascalCase string into its component words.
// It handles consecutive uppercase letters correctly: XMLParser -> ["XML", "Parser"].
func splitCamelCase(s string) []string {
	if s == "" {
		return nil
	}

	runes := []rune(s)
	if len(runes) == 1 {
		return []string{s}
	}

	boundaries := findBoundaries(runes)
	return extractParts(runes, boundaries)
}

// findBoundaries identifies the indices where splits should occur in the rune slice.
func findBoundaries(runes []rune) []int {
	boundaries := []int{0}

	for i := 1; i < len(runes); i++ {
		if isBoundary(runes, i) {
			boundaries = append(boundaries, i)
		}
	}

	return boundaries
}

// isBoundary determines if position i is a word boundary based on case transitions.
// A boundary exists at position i when:
// 1. prev is non-uppercase and curr is uppercase: handleError -> handle|Error
// 2. prev is uppercase, curr is uppercase, next is lowercase: XMLParser -> XML|Parser
//    (boundary is before the last uppercase in a run that precedes lowercase)
func isBoundary(runes []rune, i int) bool {
	curr := runes[i]
	prev := runes[i-1]

	// Case 1: Transition from non-uppercase to uppercase
	if !unicode.IsUpper(prev) && unicode.IsUpper(curr) {
		return true
	}

	// Case 2: Current is uppercase and followed by lowercase (end of uppercase run)
	// XMLParser: at i=3 (P), prev=L is upper, curr=P is upper, next=a is lower -> boundary at 3
	if unicode.IsUpper(prev) && unicode.IsUpper(curr) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
		return true
	}

	return false
}

// extractParts creates string slices from the runes based on boundary indices.
func extractParts(runes []rune, boundaries []int) []string {
	if len(boundaries) <= 1 {
		return []string{string(runes)}
	}

	parts := make([]string, 0, len(boundaries))
	for i := 0; i < len(boundaries); i++ {
		start := boundaries[i]
		end := len(runes)
		if i+1 < len(boundaries) {
			end = boundaries[i+1]
		}
		parts = append(parts, string(runes[start:end]))
	}

	return parts
}

// CamelCaseFilterConstructor creates a CamelCaseFilter from config for Bleve registry.
func CamelCaseFilterConstructor(config map[string]interface{}, cache *registry.Cache) (analysis.TokenFilter, error) {
	return NewCamelCaseFilter(), nil
}

func init() {
	registry.RegisterTokenFilter(CamelCaseFilterName, CamelCaseFilterConstructor)
}

// isASCII returns true if the string contains only ASCII characters.
// Used for fast-path optimization in tokenization.
func isASCII(s string) bool {
	return utf8.RuneCountInString(s) == len(s)
}
