package analyzer

import (
	"reflect"
	"testing"

	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/registry"
)

// =============================================================================
// splitCamelCase Tests
// =============================================================================

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		// Basic camelCase
		{
			name:     "simple camelCase",
			input:    "handleError",
			expected: []string{"handle", "Error"},
		},
		{
			name:     "two word camelCase",
			input:    "userName",
			expected: []string{"user", "Name"},
		},
		{
			name:     "three word camelCase",
			input:    "handleHTTPError",
			expected: []string{"handle", "HTTP", "Error"},
		},

		// PascalCase
		{
			name:     "simple PascalCase",
			input:    "HandleError",
			expected: []string{"Handle", "Error"},
		},
		{
			name:     "PascalCase with acronym",
			input:    "HTTPRequest",
			expected: []string{"HTTP", "Request"},
		},

		// Consecutive uppercase (acronyms)
		{
			name:     "consecutive uppercase at start",
			input:    "XMLParser",
			expected: []string{"XML", "Parser"},
		},
		{
			name:     "consecutive uppercase in middle",
			input:    "parseXMLDocument",
			expected: []string{"parse", "XML", "Document"},
		},
		{
			name:     "multiple consecutive uppercase groups",
			input:    "XMLHTTPRequest",
			expected: []string{"XMLHTTP", "Request"}, // Without dictionary, consecutive acronyms merge
		},
		{
			name:     "triple acronym",
			input:    "HTMLXMLJSONParser",
			expected: []string{"HTMLXMLJSON", "Parser"}, // Without dictionary, consecutive acronyms merge
		},

		// No split cases
		{
			name:     "all lowercase",
			input:    "handle",
			expected: []string{"handle"},
		},
		{
			name:     "all uppercase",
			input:    "HTTP",
			expected: []string{"HTTP"},
		},
		{
			name:     "single lowercase char",
			input:    "a",
			expected: []string{"a"},
		},
		{
			name:     "single uppercase char",
			input:    "A",
			expected: []string{"A"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},

		// Numbers
		{
			name:     "number at end",
			input:    "HTTP2Request",
			expected: []string{"HTTP2", "Request"},
		},
		{
			name:     "number in middle",
			input:    "parse2XML",
			expected: []string{"parse2", "XML"},
		},
		{
			name:     "number at start",
			input:    "2Handle",
			expected: []string{"2", "Handle"},
		},
		{
			name:     "all numbers",
			input:    "12345",
			expected: []string{"12345"},
		},

		// Edge cases with two characters
		{
			name:     "two lowercase",
			input:    "ab",
			expected: []string{"ab"},
		},
		{
			name:     "two uppercase",
			input:    "AB",
			expected: []string{"AB"},
		},
		{
			name:     "lower then upper",
			input:    "aB",
			expected: []string{"a", "B"},
		},
		{
			name:     "upper then lower",
			input:    "Ab",
			expected: []string{"Ab"},
		},

		// Complex real-world examples
		{
			name:     "getHTTPResponseCode",
			input:    "getHTTPResponseCode",
			expected: []string{"get", "HTTP", "Response", "Code"},
		},
		{
			name:     "parseJSONToXML",
			input:    "parseJSONToXML",
			expected: []string{"parse", "JSON", "To", "XML"},
		},
		{
			name:     "iOS specific",
			input:    "iOSDevice",
			expected: []string{"i", "OS", "Device"},
		},
		{
			name:     "URLEncoder",
			input:    "URLEncoder",
			expected: []string{"URL", "Encoder"},
		},

		// Unicode
		{
			name:     "unicode lowercase",
			input:    "handleError",
			expected: []string{"handle", "Error"},
		},
		{
			name:     "simple unicode with accents",
			input:    "CaféHandler",
			expected: []string{"Café", "Handler"},
		},
		{
			name:     "greek letters uppercase",
			input:    "handleΑΒΓError",
			expected: []string{"handle", "ΑΒΓ", "Error"}, // Greek uppercase letters are detected
		},

		// Underscores and special chars (should not split)
		{
			name:     "with underscore",
			input:    "handle_error",
			expected: []string{"handle_error"},
		},
		{
			name:     "with hyphen",
			input:    "handle-error",
			expected: []string{"handle-error"},
		},

		// Long strings
		{
			name:     "very long identifier",
			input:    "thisIsAVeryLongIdentifierNameForTesting",
			expected: []string{"this", "Is", "A", "Very", "Long", "Identifier", "Name", "For", "Testing"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitCamelCase(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("splitCamelCase(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// CamelCaseFilter.Filter Tests
// =============================================================================

func TestCamelCaseFilter_Filter(t *testing.T) {
	filter := NewCamelCaseFilter()

	tests := []struct {
		name           string
		input          []string
		expectedTerms  []string
		expectedCounts int // expected number of output tokens
	}{
		{
			name:           "simple camelCase",
			input:          []string{"handleError"},
			expectedTerms:  []string{"handleError", "handle", "Error"},
			expectedCounts: 3,
		},
		{
			name:           "consecutive uppercase",
			input:          []string{"HTTPRequest"},
			expectedTerms:  []string{"HTTPRequest", "HTTP", "Request"},
			expectedCounts: 3,
		},
		{
			name:           "multiple consecutive uppercase groups",
			input:          []string{"XMLHTTPRequest"},
			expectedTerms:  []string{"XMLHTTPRequest", "XMLHTTP", "Request"}, // Without dictionary, consecutive acronyms merge
			expectedCounts: 3,
		},
		{
			name:           "no split needed - all lowercase",
			input:          []string{"handle"},
			expectedTerms:  []string{"handle"},
			expectedCounts: 1,
		},
		{
			name:           "no split needed - all uppercase",
			input:          []string{"HTTP"},
			expectedTerms:  []string{"HTTP"},
			expectedCounts: 1,
		},
		{
			name:           "multiple input tokens",
			input:          []string{"handleError", "parseXML"},
			expectedTerms:  []string{"handleError", "handle", "Error", "parseXML", "parse", "XML"},
			expectedCounts: 6,
		},
		{
			name:           "empty input stream",
			input:          []string{},
			expectedTerms:  []string{},
			expectedCounts: 0,
		},
		{
			name:           "single character tokens",
			input:          []string{"a", "B"},
			expectedTerms:  []string{"a", "B"},
			expectedCounts: 2,
		},
		{
			name:           "handleHTTPError full split",
			input:          []string{"handleHTTPError"},
			expectedTerms:  []string{"handleHTTPError", "handle", "HTTP", "Error"},
			expectedCounts: 4,
		},
		{
			name:           "mixed tokens with numbers",
			input:          []string{"HTTP2Request", "Version3"},
			expectedTerms:  []string{"HTTP2Request", "HTTP2", "Request", "Version3"}, // Version3 has no split
			expectedCounts: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := createTokenStream(tt.input)
			result := filter.Filter(input)

			if len(result) != tt.expectedCounts {
				t.Errorf("expected %d tokens, got %d", tt.expectedCounts, len(result))
			}

			terms := extractTerms(result)
			if !reflect.DeepEqual(terms, tt.expectedTerms) {
				t.Errorf("expected terms %v, got %v", tt.expectedTerms, terms)
			}
		})
	}
}

// =============================================================================
// Token Metadata Preservation Tests
// =============================================================================

func TestCamelCaseFilter_PreservesTokenMetadata(t *testing.T) {
	filter := NewCamelCaseFilter()

	original := &analysis.Token{
		Term:     []byte("handleError"),
		Start:    10,
		End:      21,
		Position: 5,
		Type:     analysis.AlphaNumeric,
		KeyWord:  true,
	}

	input := analysis.TokenStream{original}
	result := filter.Filter(input)

	if len(result) != 3 {
		t.Fatalf("expected 3 tokens, got %d", len(result))
	}

	// Check original token is preserved
	if string(result[0].Term) != "handleError" {
		t.Errorf("first token should be original, got %s", string(result[0].Term))
	}

	// Check split tokens inherit metadata
	for i, token := range result {
		if token.Start != 10 {
			t.Errorf("token[%d].Start = %d, want 10", i, token.Start)
		}
		if token.End != 21 {
			t.Errorf("token[%d].End = %d, want 21", i, token.End)
		}
		if token.Position != 5 {
			t.Errorf("token[%d].Position = %d, want 5", i, token.Position)
		}
		if token.Type != analysis.AlphaNumeric {
			t.Errorf("token[%d].Type = %v, want AlphaNumeric", i, token.Type)
		}
		if !token.KeyWord {
			t.Errorf("token[%d].KeyWord should be true", i)
		}
	}
}

// =============================================================================
// Registration Tests
// =============================================================================

func TestCamelCaseFilter_Registration(t *testing.T) {
	cache := registry.NewCache()

	// The filter should be registered via init()
	tokenFilter, err := cache.TokenFilterNamed(CamelCaseFilterName)
	if err != nil {
		t.Fatalf("failed to get registered filter: %v", err)
	}

	if tokenFilter == nil {
		t.Fatal("tokenFilter should not be nil")
	}

	// Verify it works
	input := createTokenStream([]string{"handleError"})
	result := tokenFilter.Filter(input)

	if len(result) != 3 {
		t.Errorf("expected 3 tokens, got %d", len(result))
	}
}

func TestCamelCaseFilterConstructor(t *testing.T) {
	cache := registry.NewCache()

	filter, err := CamelCaseFilterConstructor(nil, cache)
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	if filter == nil {
		t.Fatal("filter should not be nil")
	}

	// Verify type
	_, ok := filter.(*CamelCaseFilter)
	if !ok {
		t.Error("filter should be *CamelCaseFilter")
	}
}

func TestCamelCaseFilterName(t *testing.T) {
	if CamelCaseFilterName != "camel_case" {
		t.Errorf("CamelCaseFilterName = %q, want %q", CamelCaseFilterName, "camel_case")
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestCamelCaseFilter_EdgeCases(t *testing.T) {
	filter := NewCamelCaseFilter()

	tests := []struct {
		name          string
		input         string
		expectedTerms []string
	}{
		{
			name:          "consecutive same transitions",
			input:         "AaBbCc",
			expectedTerms: []string{"AaBbCc", "Aa", "Bb", "Cc"},
		},
		{
			name:          "single uppercase followed by many lowercase",
			input:         "Aaaaaa",
			expectedTerms: []string{"Aaaaaa"},
		},
		{
			name:          "alternating case",
			input:         "aAbBcC",
			expectedTerms: []string{"aAbBcC", "a", "Ab", "Bc", "C"},
		},
		{
			name:          "ending with uppercase sequence",
			input:         "handleXML",
			expectedTerms: []string{"handleXML", "handle", "XML"},
		},
		{
			name:          "starting lowercase ending uppercase",
			input:         "goHTTP",
			expectedTerms: []string{"goHTTP", "go", "HTTP"},
		},
		{
			name:          "unicode mixed with ascii",
			input:         "GetUtf8String",
			expectedTerms: []string{"GetUtf8String", "Get", "Utf8", "String"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := createTokenStream([]string{tt.input})
			result := filter.Filter(input)
			terms := extractTerms(result)

			if !reflect.DeepEqual(terms, tt.expectedTerms) {
				t.Errorf("expected %v, got %v", tt.expectedTerms, terms)
			}
		})
	}
}

// =============================================================================
// Performance and Nil Safety Tests
// =============================================================================

func TestCamelCaseFilter_NilInput(t *testing.T) {
	filter := NewCamelCaseFilter()

	// nil input should not panic
	result := filter.Filter(nil)
	if result == nil {
		t.Error("result should not be nil, expected empty slice")
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d tokens", len(result))
	}
}

func TestCamelCaseFilter_LargeInput(t *testing.T) {
	filter := NewCamelCaseFilter()

	// Create a large input stream
	input := make(analysis.TokenStream, 1000)
	for i := 0; i < 1000; i++ {
		input[i] = &analysis.Token{
			Term:     []byte("handleHTTPError"),
			Position: i,
		}
	}

	result := filter.Filter(input)

	// Each token should produce 4 tokens (original + 3 parts)
	expectedLen := 4000
	if len(result) != expectedLen {
		t.Errorf("expected %d tokens, got %d", expectedLen, len(result))
	}
}

// =============================================================================
// isBoundary Tests
// =============================================================================

func TestIsBoundary(t *testing.T) {
	tests := []struct {
		name     string
		runes    []rune
		index    int
		expected bool
	}{
		{
			name:     "lowercase to uppercase transition",
			runes:    []rune("handleError"),
			index:    6, // 'E' after 'e'
			expected: true,
		},
		{
			name:     "uppercase to lowercase after uppercase",
			runes:    []rune("XMLParser"),
			index:    3, // 'P' is preceded by 'L' which is uppercase
			expected: true,
		},
		{
			name:     "consecutive lowercase",
			runes:    []rune("handle"),
			index:    2,
			expected: false,
		},
		{
			name:     "consecutive uppercase",
			runes:    []rune("HTTP"),
			index:    2,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBoundary(tt.runes, tt.index)
			if result != tt.expected {
				t.Errorf("isBoundary(%q, %d) = %v, want %v",
					string(tt.runes), tt.index, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// findBoundaries Tests
// =============================================================================

func TestFindBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []int
	}{
		{
			name:     "simple camelCase",
			input:    "handleError",
			expected: []int{0, 6},
		},
		{
			name:     "XMLParser",
			input:    "XMLParser",
			expected: []int{0, 3},
		},
		{
			name:     "XMLHTTPRequest",
			input:    "XMLHTTPRequest",
			expected: []int{0, 7}, // Without dictionary, consecutive acronyms can't be split
		},
		{
			name:     "all lowercase",
			input:    "handle",
			expected: []int{0},
		},
		{
			name:     "all uppercase",
			input:    "HTTP",
			expected: []int{0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runes := []rune(tt.input)
			result := findBoundaries(runes)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("findBoundaries(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// NewCamelCaseFilter Tests
// =============================================================================

func TestNewCamelCaseFilter(t *testing.T) {
	filter := NewCamelCaseFilter()
	if filter == nil {
		t.Fatal("NewCamelCaseFilter() returned nil")
	}
}

// =============================================================================
// isASCII Tests
// =============================================================================

func TestIsASCII(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"hello", true},
		{"Hello123", true},
		{"", true},
		{"café", false},
		{"hello世界", false},
		{"αβγ", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isASCII(tt.input)
			if result != tt.expected {
				t.Errorf("isASCII(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func createTokenStream(terms []string) analysis.TokenStream {
	stream := make(analysis.TokenStream, len(terms))
	for i, term := range terms {
		stream[i] = &analysis.Token{
			Term:     []byte(term),
			Position: i + 1,
			Start:    0,
			End:      len(term),
			Type:     analysis.AlphaNumeric,
		}
	}
	return stream
}

func extractTerms(stream analysis.TokenStream) []string {
	terms := make([]string, len(stream))
	for i, token := range stream {
		terms[i] = string(token.Term)
	}
	return terms
}
