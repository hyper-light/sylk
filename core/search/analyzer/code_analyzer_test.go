package analyzer

import (
	"testing"

	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// CodeAnalyzer Tests
// =============================================================================

func TestNewCodeAnalyzer(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewCodeAnalyzer(cache)

	require.NoError(t, err)
	require.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.Tokenizer)
	assert.Len(t, analyzer.TokenFilters, 3) // camel_case, snake_case, lowercase
}

func TestCodeAnalyzer_TokenizationChain(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewCodeAnalyzer(cache)
	require.NoError(t, err)

	tests := []struct {
		name     string
		input    string
		contains []string // tokens that should be present in output
	}{
		{
			name:     "camelCase identifier",
			input:    "handleError",
			contains: []string{"handleerror", "handle", "error"},
		},
		{
			name:     "PascalCase identifier",
			input:    "HandleError",
			contains: []string{"handleerror", "handle", "error"},
		},
		{
			name:     "snake_case identifier",
			input:    "handle_error",
			contains: []string{"handle_error", "handle", "error"},
		},
		{
			name:     "mixed case acronym",
			input:    "HTTPRequest",
			contains: []string{"httprequest", "http", "request"},
		},
		{
			name:     "complex identifier",
			input:    "getHTTPResponseCode",
			contains: []string{"gethttpresponsecode", "get", "http", "response", "code"},
		},
		{
			name:     "mixed snake and camel",
			input:    "get_HTTPResponse",
			contains: []string{"get", "httpresponse", "http", "response"},
		},
		{
			name:     "multiple identifiers",
			input:    "handleError parseXML",
			contains: []string{"handleerror", "handle", "error", "parsexml", "parse", "xml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := analyzer.Analyze([]byte(tt.input))
			terms := extractTermStrings(tokens)

			for _, expected := range tt.contains {
				assert.Contains(t, terms, expected,
					"expected term %q in output for input %q, got %v",
					expected, tt.input, terms)
			}
		})
	}
}

func TestCodeAnalyzer_Lowercase(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewCodeAnalyzer(cache)
	require.NoError(t, err)

	// All tokens should be lowercased
	tokens := analyzer.Analyze([]byte("HandleHTTPError"))
	terms := extractTermStrings(tokens)

	for _, term := range terms {
		assert.Equal(t, term, toLowerCase(term),
			"term %q should be lowercase", term)
	}
}

func TestCodeAnalyzer_Registration(t *testing.T) {
	cache := registry.NewCache()

	analyzer, err := cache.AnalyzerNamed(CodeAnalyzerName)
	require.NoError(t, err)
	require.NotNil(t, analyzer)

	// Verify it works
	tokens := analyzer.Analyze([]byte("handleError"))
	assert.NotEmpty(t, tokens)
}

func TestCodeAnalyzerName(t *testing.T) {
	assert.Equal(t, "code", CodeAnalyzerName)
}

// =============================================================================
// CommentAnalyzer Tests
// =============================================================================

func TestNewCommentAnalyzer(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewCommentAnalyzer(cache)

	require.NoError(t, err)
	require.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.Tokenizer)
	assert.Len(t, analyzer.TokenFilters, 2) // lowercase, stemmer
}

func TestCommentAnalyzer_Stemming(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewCommentAnalyzer(cache)
	require.NoError(t, err)

	tests := []struct {
		name     string
		input    string
		contains []string // expected stemmed tokens
	}{
		{
			name:     "verb forms",
			input:    "running runs runner",
			contains: []string{"run"},
		},
		{
			name:     "plural nouns",
			input:    "users files documents",
			contains: []string{"user", "file", "document"},
		},
		{
			name:     "mixed comment",
			input:    "This function handles errors gracefully",
			contains: []string{"function", "handl", "error", "gracefulli"},
		},
		{
			name:     "programming terms",
			input:    "parsing tokenizing analyzing",
			contains: []string{"pars", "token", "analyz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := analyzer.Analyze([]byte(tt.input))
			terms := extractTermStrings(tokens)

			for _, expected := range tt.contains {
				assert.Contains(t, terms, expected,
					"expected stemmed term %q in output for input %q, got %v",
					expected, tt.input, terms)
			}
		})
	}
}

func TestCommentAnalyzer_Lowercase(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewCommentAnalyzer(cache)
	require.NoError(t, err)

	// All tokens should be lowercased
	tokens := analyzer.Analyze([]byte("This Is A COMMENT"))
	terms := extractTermStrings(tokens)

	for _, term := range terms {
		assert.Equal(t, term, toLowerCase(term),
			"term %q should be lowercase", term)
	}
}

func TestCommentAnalyzer_Registration(t *testing.T) {
	cache := registry.NewCache()

	analyzer, err := cache.AnalyzerNamed(CommentAnalyzerName)
	require.NoError(t, err)
	require.NotNil(t, analyzer)

	// Verify it works
	tokens := analyzer.Analyze([]byte("This is a comment"))
	assert.NotEmpty(t, tokens)
}

func TestCommentAnalyzerName(t *testing.T) {
	assert.Equal(t, "comment", CommentAnalyzerName)
}

// =============================================================================
// SymbolAnalyzer Tests
// =============================================================================

func TestNewSymbolAnalyzer(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewSymbolAnalyzer(cache)

	require.NoError(t, err)
	require.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.Tokenizer)
	assert.Len(t, analyzer.TokenFilters, 2) // camel_case, snake_case (NO lowercase)
}

func TestSymbolAnalyzer_PreservesCase(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewSymbolAnalyzer(cache)
	require.NoError(t, err)

	tests := []struct {
		name     string
		input    string
		contains []string // expected tokens with preserved case
	}{
		{
			name:     "camelCase preserves case",
			input:    "handleError",
			contains: []string{"handleError", "handle", "Error"},
		},
		{
			name:     "PascalCase preserves case",
			input:    "HandleError",
			contains: []string{"HandleError", "Handle", "Error"},
		},
		{
			name:     "acronym preserves case",
			input:    "HTTPRequest",
			contains: []string{"HTTPRequest", "HTTP", "Request"},
		},
		{
			name:     "snake_case preserves case",
			input:    "HTTP_STATUS_CODE",
			contains: []string{"HTTP_STATUS_CODE", "HTTP", "STATUS", "CODE"},
		},
		{
			name:     "mixed preserves case",
			input:    "getHTTPResponse",
			contains: []string{"getHTTPResponse", "get", "HTTP", "Response"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := analyzer.Analyze([]byte(tt.input))
			terms := extractTermStrings(tokens)

			for _, expected := range tt.contains {
				assert.Contains(t, terms, expected,
					"expected term %q with preserved case in output for input %q, got %v",
					expected, tt.input, terms)
			}
		})
	}
}

func TestSymbolAnalyzer_NoCaseChange(t *testing.T) {
	cache := registry.NewCache()
	analyzer, err := NewSymbolAnalyzer(cache)
	require.NoError(t, err)

	// Uppercase letters should remain uppercase
	tokens := analyzer.Analyze([]byte("HTTPRequest"))
	terms := extractTermStrings(tokens)

	// Should contain "HTTP" and "Request" with original case
	assert.Contains(t, terms, "HTTP")
	assert.Contains(t, terms, "Request")
	assert.Contains(t, terms, "HTTPRequest")

	// Should NOT contain lowercased versions
	assert.NotContains(t, terms, "http")
	assert.NotContains(t, terms, "request")
	assert.NotContains(t, terms, "httprequest")
}

func TestSymbolAnalyzer_Registration(t *testing.T) {
	cache := registry.NewCache()

	analyzer, err := cache.AnalyzerNamed(SymbolAnalyzerName)
	require.NoError(t, err)
	require.NotNil(t, analyzer)

	// Verify it works and preserves case
	tokens := analyzer.Analyze([]byte("HandleError"))
	terms := extractTermStrings(tokens)

	assert.Contains(t, terms, "HandleError")
	assert.Contains(t, terms, "Handle")
	assert.Contains(t, terms, "Error")
}

func TestSymbolAnalyzerName(t *testing.T) {
	assert.Equal(t, "symbol", SymbolAnalyzerName)
}

// =============================================================================
// Registry Integration Tests
// =============================================================================

func TestAllAnalyzersRegistered(t *testing.T) {
	cache := registry.NewCache()

	analyzers := []struct {
		name     string
		constant string
	}{
		{CodeAnalyzerName, "code"},
		{CommentAnalyzerName, "comment"},
		{SymbolAnalyzerName, "symbol"},
	}

	for _, tc := range analyzers {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.constant, tc.name)

			analyzer, err := cache.AnalyzerNamed(tc.name)
			require.NoError(t, err, "analyzer %q should be registered", tc.name)
			require.NotNil(t, analyzer)
		})
	}
}

func TestAnalyzerConstructors(t *testing.T) {
	cache := registry.NewCache()

	t.Run("CodeAnalyzerConstructor", func(t *testing.T) {
		analyzer, err := NewCodeAnalyzerConstructor(nil, cache)
		require.NoError(t, err)
		require.NotNil(t, analyzer)
	})

	t.Run("CommentAnalyzerConstructor", func(t *testing.T) {
		analyzer, err := NewCommentAnalyzerConstructor(nil, cache)
		require.NoError(t, err)
		require.NotNil(t, analyzer)
	})

	t.Run("SymbolAnalyzerConstructor", func(t *testing.T) {
		analyzer, err := NewSymbolAnalyzerConstructor(nil, cache)
		require.NoError(t, err)
		require.NotNil(t, analyzer)
	})
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestAnalyzers_EmptyInput(t *testing.T) {
	cache := registry.NewCache()

	t.Run("CodeAnalyzer", func(t *testing.T) {
		analyzer, err := NewCodeAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte(""))
		assert.Empty(t, tokens)
	})

	t.Run("CommentAnalyzer", func(t *testing.T) {
		analyzer, err := NewCommentAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte(""))
		assert.Empty(t, tokens)
	})

	t.Run("SymbolAnalyzer", func(t *testing.T) {
		analyzer, err := NewSymbolAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte(""))
		assert.Empty(t, tokens)
	})
}

func TestAnalyzers_WhitespaceOnly(t *testing.T) {
	cache := registry.NewCache()

	t.Run("CodeAnalyzer", func(t *testing.T) {
		analyzer, err := NewCodeAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("   \t\n  "))
		assert.Empty(t, tokens)
	})

	t.Run("CommentAnalyzer", func(t *testing.T) {
		analyzer, err := NewCommentAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("   \t\n  "))
		assert.Empty(t, tokens)
	})

	t.Run("SymbolAnalyzer", func(t *testing.T) {
		analyzer, err := NewSymbolAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("   \t\n  "))
		assert.Empty(t, tokens)
	})
}

func TestAnalyzers_SingleCharacter(t *testing.T) {
	cache := registry.NewCache()

	t.Run("CodeAnalyzer", func(t *testing.T) {
		analyzer, err := NewCodeAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("x"))
		assert.NotEmpty(t, tokens)
	})

	t.Run("SymbolAnalyzer", func(t *testing.T) {
		analyzer, err := NewSymbolAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("X"))
		terms := extractTermStrings(tokens)
		assert.Contains(t, terms, "X") // Case preserved
	})
}

func TestAnalyzers_UnicodeIdentifiers(t *testing.T) {
	cache := registry.NewCache()

	t.Run("CodeAnalyzer with unicode", func(t *testing.T) {
		analyzer, err := NewCodeAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("用户Handler"))
		assert.NotEmpty(t, tokens)
	})

	t.Run("SymbolAnalyzer with unicode", func(t *testing.T) {
		analyzer, err := NewSymbolAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("用户Handler"))
		assert.NotEmpty(t, tokens)
	})
}

func TestAnalyzers_NumericContent(t *testing.T) {
	cache := registry.NewCache()

	t.Run("CodeAnalyzer with numbers", func(t *testing.T) {
		analyzer, err := NewCodeAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("http2Request version3"))
		terms := extractTermStrings(tokens)
		assert.NotEmpty(t, terms)
	})

	t.Run("SymbolAnalyzer with numbers", func(t *testing.T) {
		analyzer, err := NewSymbolAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze([]byte("HTTP2Request"))
		terms := extractTermStrings(tokens)
		assert.Contains(t, terms, "HTTP2Request")
	})
}

func TestAnalyzers_SpecialCharacters(t *testing.T) {
	cache := registry.NewCache()

	t.Run("CodeAnalyzer handles operators", func(t *testing.T) {
		analyzer, err := NewCodeAnalyzer(cache)
		require.NoError(t, err)

		// Operators should be stripped, identifiers should remain
		tokens := analyzer.Analyze([]byte("foo + bar == baz"))
		terms := extractTermStrings(tokens)
		assert.Contains(t, terms, "foo")
		assert.Contains(t, terms, "bar")
		assert.Contains(t, terms, "baz")
	})
}

// =============================================================================
// Error Handling Tests
// =============================================================================

func TestAnalyzerCastError(t *testing.T) {
	err := &analyzerCastError{analyzer: "test_analyzer"}
	assert.Equal(t, "analyzer test_analyzer is not a DefaultAnalyzer", err.Error())
}

// =============================================================================
// Comparative Tests (Code vs Symbol)
// =============================================================================

func TestCodeVsSymbolAnalyzer_CaseDifference(t *testing.T) {
	cache := registry.NewCache()

	codeAnalyzer, err := NewCodeAnalyzer(cache)
	require.NoError(t, err)

	symbolAnalyzer, err := NewSymbolAnalyzer(cache)
	require.NoError(t, err)

	// Note: Use separate byte slices because Bleve's lowercase filter mutates
	// the token bytes in place, which can corrupt the input if reused.
	codeInput := []byte("HandleHTTPError")
	symbolInput := []byte("HandleHTTPError")

	codeTokens := codeAnalyzer.Analyze(codeInput)
	symbolTokens := symbolAnalyzer.Analyze(symbolInput)

	codeTerms := extractTermStrings(codeTokens)
	symbolTerms := extractTermStrings(symbolTokens)

	// Code analyzer should have lowercased terms
	assert.Contains(t, codeTerms, "handlehttperror")
	assert.Contains(t, codeTerms, "handle")
	assert.Contains(t, codeTerms, "http")
	assert.Contains(t, codeTerms, "error")

	// Symbol analyzer should have original case
	assert.Contains(t, symbolTerms, "HandleHTTPError")
	assert.Contains(t, symbolTerms, "Handle")
	assert.Contains(t, symbolTerms, "HTTP")
	assert.Contains(t, symbolTerms, "Error")
}

func TestCodeVsCommentAnalyzer_TokenizerDifference(t *testing.T) {
	cache := registry.NewCache()

	codeAnalyzer, err := NewCodeAnalyzer(cache)
	require.NoError(t, err)

	commentAnalyzer, err := NewCommentAnalyzer(cache)
	require.NoError(t, err)

	// Code with identifiers - code analyzer should handle camelCase
	codeInput := []byte("handleError")
	codeTokens := codeAnalyzer.Analyze(codeInput)
	codeTerms := extractTermStrings(codeTokens)

	// Code analyzer splits camelCase
	assert.Contains(t, codeTerms, "handle")
	assert.Contains(t, codeTerms, "error")

	// Natural language - comment analyzer should stem
	commentInput := []byte("handling errors")
	commentTokens := commentAnalyzer.Analyze(commentInput)
	commentTerms := extractTermStrings(commentTokens)

	// Comment analyzer stems words
	assert.Contains(t, commentTerms, "handl") // "handling" stemmed
	assert.Contains(t, commentTerms, "error") // "errors" stemmed
}

// =============================================================================
// Large Input Tests
// =============================================================================

func TestAnalyzers_LargeInput(t *testing.T) {
	cache := registry.NewCache()

	// Generate large input
	var largeInput []byte
	for i := 0; i < 100; i++ {
		largeInput = append(largeInput, []byte("handleHTTPError parseXMLDocument getUserByID ")...)
	}

	t.Run("CodeAnalyzer handles large input", func(t *testing.T) {
		analyzer, err := NewCodeAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze(largeInput)
		assert.NotEmpty(t, tokens)
	})

	t.Run("SymbolAnalyzer handles large input", func(t *testing.T) {
		analyzer, err := NewSymbolAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze(largeInput)
		assert.NotEmpty(t, tokens)
	})

	t.Run("CommentAnalyzer handles large input", func(t *testing.T) {
		analyzer, err := NewCommentAnalyzer(cache)
		require.NoError(t, err)

		tokens := analyzer.Analyze(largeInput)
		assert.NotEmpty(t, tokens)
	})
}

// =============================================================================
// Helper Functions
// =============================================================================

// extractTermStrings extracts term strings from a token stream.
func extractTermStrings(tokens analysis.TokenStream) []string {
	terms := make([]string, len(tokens))
	for i, token := range tokens {
		terms[i] = string(token.Term)
	}
	return terms
}

// toLowerCase converts a string to lowercase for comparison.
func toLowerCase(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}
