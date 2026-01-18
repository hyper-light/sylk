// Package analyzer provides custom Bleve analyzers for code-aware text search.
// It includes tokenizers and filters optimized for source code, handling constructs
// like camelCase identifiers, snake_case variables, operators, and string literals.
package analyzer

import (
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/registry"

	// Import required analyzers and filters from Bleve
	_ "github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/en"
	_ "github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	_ "github.com/blevesearch/bleve/v2/analysis/token/porter"
)

// Analyzer names for registry.
const (
	CodeAnalyzerName    = "code"
	CommentAnalyzerName = "comment"
	SymbolAnalyzerName  = "symbol"
)

// Built-in Bleve component names.
const (
	lowercaseFilterName  = "to_lower"
	porterStemmerName    = "stemmer_porter"
	standardAnalyzerName = "standard"
)

func init() {
	registry.RegisterAnalyzer(CodeAnalyzerName, NewCodeAnalyzerConstructor)
	registry.RegisterAnalyzer(CommentAnalyzerName, NewCommentAnalyzerConstructor)
	registry.RegisterAnalyzer(SymbolAnalyzerName, NewSymbolAnalyzerConstructor)
}

// NewCodeAnalyzerConstructor creates a CodeAnalyzer from config for Bleve registry.
// Chain: code_tokenizer -> camel_case -> snake_case -> lowercase
func NewCodeAnalyzerConstructor(
	config map[string]interface{},
	cache *registry.Cache,
) (analysis.Analyzer, error) {
	return NewCodeAnalyzer(cache)
}

// NewCodeAnalyzer creates an analyzer for source code content.
// The processing chain is: code_tokenizer -> camel_case -> snake_case -> lowercase.
// This analyzer is designed for general source code search where case-insensitive
// matching is desired.
func NewCodeAnalyzer(cache *registry.Cache) (*analysis.DefaultAnalyzer, error) {
	tokenizer, err := cache.TokenizerNamed(CodeTokenizerName)
	if err != nil {
		return nil, err
	}

	filters, err := buildCodeFilters(cache, true)
	if err != nil {
		return nil, err
	}

	return &analysis.DefaultAnalyzer{
		Tokenizer:    tokenizer,
		TokenFilters: filters,
	}, nil
}

// NewCommentAnalyzerConstructor creates a CommentAnalyzer from config for Bleve registry.
// Uses standard tokenizer with lowercase and porter stemmer for natural language comments.
func NewCommentAnalyzerConstructor(
	config map[string]interface{},
	cache *registry.Cache,
) (analysis.Analyzer, error) {
	return NewCommentAnalyzer(cache)
}

// NewCommentAnalyzer creates an analyzer for code comments (natural language).
// The processing chain uses the standard analyzer's tokenizer with lowercase
// and porter stemmer filters. This is appropriate for analyzing comments which
// typically contain natural language text.
func NewCommentAnalyzer(cache *registry.Cache) (*analysis.DefaultAnalyzer, error) {
	standardAnalyzer, err := cache.AnalyzerNamed(standardAnalyzerName)
	if err != nil {
		return nil, err
	}

	// Extract tokenizer from standard analyzer
	defaultAnalyzer, ok := standardAnalyzer.(*analysis.DefaultAnalyzer)
	if !ok {
		return nil, errStandardAnalyzerNotDefault
	}

	filters, err := buildCommentFilters(cache)
	if err != nil {
		return nil, err
	}

	return &analysis.DefaultAnalyzer{
		Tokenizer:    defaultAnalyzer.Tokenizer,
		TokenFilters: filters,
	}, nil
}

// NewSymbolAnalyzerConstructor creates a SymbolAnalyzer from config for Bleve registry.
// Chain: code_tokenizer -> camel_case -> snake_case (NO lowercase for exact matching)
func NewSymbolAnalyzerConstructor(
	config map[string]interface{},
	cache *registry.Cache,
) (analysis.Analyzer, error) {
	return NewSymbolAnalyzer(cache)
}

// NewSymbolAnalyzer creates an analyzer for symbol names (identifiers).
// The processing chain is: code_tokenizer -> camel_case -> snake_case.
// Unlike CodeAnalyzer, this analyzer preserves case for exact symbol matching.
func NewSymbolAnalyzer(cache *registry.Cache) (*analysis.DefaultAnalyzer, error) {
	tokenizer, err := cache.TokenizerNamed(CodeTokenizerName)
	if err != nil {
		return nil, err
	}

	filters, err := buildCodeFilters(cache, false)
	if err != nil {
		return nil, err
	}

	return &analysis.DefaultAnalyzer{
		Tokenizer:    tokenizer,
		TokenFilters: filters,
	}, nil
}

// buildCodeFilters creates the filter chain for code analysis.
// If includeLowercase is true, adds the lowercase filter at the end.
func buildCodeFilters(cache *registry.Cache, includeLowercase bool) ([]analysis.TokenFilter, error) {
	camelFilter, err := cache.TokenFilterNamed(CamelCaseFilterName)
	if err != nil {
		return nil, err
	}

	snakeFilter, err := cache.TokenFilterNamed(SnakeCaseFilterName)
	if err != nil {
		return nil, err
	}

	if !includeLowercase {
		return []analysis.TokenFilter{camelFilter, snakeFilter}, nil
	}

	lowercaseFilter, err := cache.TokenFilterNamed(lowercaseFilterName)
	if err != nil {
		return nil, err
	}

	return []analysis.TokenFilter{camelFilter, snakeFilter, lowercaseFilter}, nil
}

// buildCommentFilters creates the filter chain for comment analysis.
// Returns lowercase and porter stemmer filters for natural language processing.
func buildCommentFilters(cache *registry.Cache) ([]analysis.TokenFilter, error) {
	lowercaseFilter, err := cache.TokenFilterNamed(lowercaseFilterName)
	if err != nil {
		return nil, err
	}

	stemmerFilter, err := cache.TokenFilterNamed(porterStemmerName)
	if err != nil {
		return nil, err
	}

	return []analysis.TokenFilter{lowercaseFilter, stemmerFilter}, nil
}

// errStandardAnalyzerNotDefault is returned when the standard analyzer
// cannot be cast to DefaultAnalyzer.
var errStandardAnalyzerNotDefault = &analyzerCastError{analyzer: standardAnalyzerName}

// analyzerCastError represents an error when an analyzer cannot be cast
// to the expected type.
type analyzerCastError struct {
	analyzer string
}

func (e *analyzerCastError) Error() string {
	return "analyzer " + e.analyzer + " is not a DefaultAnalyzer"
}
