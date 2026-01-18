// Package bleve provides Bleve index configuration and document mappings for the
// Sylk Document Search System. It defines schemas for indexing source code,
// markdown, configuration files, LLM communications, web fetches, notes, and git commits.
package bleve

import (
	"github.com/blevesearch/bleve/v2/mapping"

	// Import custom analyzers to register them with the Bleve registry
	"github.com/adalundhe/sylk/core/search/analyzer"
)

// Default mapping configuration constants.
const (
	// DefaultTypeName is the type name used for all documents in the index.
	DefaultTypeName = "document"

	// DefaultAnalyzerName is the fallback analyzer for unmapped fields.
	DefaultAnalyzerName = "standard"

	// KeywordAnalyzerName is used for exact-match fields like path, type, language.
	KeywordAnalyzerName = "keyword"
)

// BuildDocumentMapping creates a DocumentMapping for indexing Document structs.
// It configures appropriate analyzers for each field type:
//   - Content: code analyzer for source code search
//   - Symbols: symbol analyzer preserving case
//   - Comments: comment analyzer with stemming
//   - Path, Type, Language: keyword analyzer for exact matching
//   - ModifiedAt, IndexedAt: datetime mappings
//
// Returns a configured DocumentMapping ready for use in an IndexMapping.
func BuildDocumentMapping() *mapping.DocumentMapping {
	docMapping := mapping.NewDocumentMapping()
	docMapping.Dynamic = false

	// ID field - disabled (used only for retrieval, not search)
	docMapping.AddFieldMappingsAt("id", buildDisabledField())

	// Path field - keyword analyzer for exact path matching
	docMapping.AddFieldMappingsAt("path", buildKeywordField())

	// Type field - keyword analyzer for filtering by document type
	docMapping.AddFieldMappingsAt("type", buildKeywordField())

	// Language field - keyword analyzer for filtering by programming language
	docMapping.AddFieldMappingsAt("language", buildKeywordField())

	// Content field - code analyzer for source code search
	docMapping.AddFieldMappingsAt("content", buildCodeField())

	// Symbols field - symbol analyzer preserving case for exact symbol matching
	docMapping.AddFieldMappingsAt("symbols", buildSymbolField())

	// Comments field - comment analyzer with stemming for natural language
	docMapping.AddFieldMappingsAt("comments", buildCommentField())

	// Imports field - keyword analyzer for exact import path matching
	docMapping.AddFieldMappingsAt("imports", buildKeywordField())

	// Checksum field - disabled (used only for change detection, not search)
	docMapping.AddFieldMappingsAt("checksum", buildDisabledField())

	// ModifiedAt field - datetime for temporal queries
	docMapping.AddFieldMappingsAt("modified_at", buildDateTimeField())

	// IndexedAt field - datetime for temporal queries
	docMapping.AddFieldMappingsAt("indexed_at", buildDateTimeField())

	// GitCommit field - keyword analyzer for exact commit hash matching
	docMapping.AddFieldMappingsAt("git_commit", buildKeywordField())

	return docMapping
}

// BuildIndexMapping creates a complete IndexMapping for the document search index.
// It configures the document type mapping, default analyzer, and disables dynamic
// mapping for optimal performance.
//
// Returns a configured IndexMapping ready for index creation.
func BuildIndexMapping() (*mapping.IndexMappingImpl, error) {
	indexMapping := mapping.NewIndexMapping()

	// Set default analyzer for any unmapped text fields
	indexMapping.DefaultAnalyzer = DefaultAnalyzerName

	// Disable dynamic mapping - only explicitly mapped fields are indexed
	indexMapping.DefaultMapping.Dynamic = false

	// Add document type mapping
	indexMapping.AddDocumentMapping(DefaultTypeName, BuildDocumentMapping())

	// Set default document type
	indexMapping.DefaultType = DefaultTypeName

	return indexMapping, nil
}

// =============================================================================
// Field Mapping Builders
// =============================================================================

// buildDisabledField creates a field mapping that stores but does not index the field.
// Used for fields like ID and Checksum that are needed for retrieval but not search.
func buildDisabledField() *mapping.FieldMapping {
	fieldMapping := mapping.NewTextFieldMapping()
	fieldMapping.Store = true
	fieldMapping.Index = false
	fieldMapping.IncludeInAll = false
	return fieldMapping
}

// buildKeywordField creates a field mapping using the keyword analyzer.
// Used for exact-match fields like path, type, and language.
func buildKeywordField() *mapping.FieldMapping {
	fieldMapping := mapping.NewTextFieldMapping()
	fieldMapping.Analyzer = KeywordAnalyzerName
	fieldMapping.Store = true
	fieldMapping.Index = true
	fieldMapping.IncludeInAll = false
	return fieldMapping
}

// buildCodeField creates a field mapping using the code analyzer.
// Used for source code content with camelCase/snake_case tokenization.
func buildCodeField() *mapping.FieldMapping {
	fieldMapping := mapping.NewTextFieldMapping()
	fieldMapping.Analyzer = analyzer.CodeAnalyzerName
	fieldMapping.Store = true
	fieldMapping.Index = true
	fieldMapping.IncludeInAll = true
	return fieldMapping
}

// buildSymbolField creates a field mapping using the symbol analyzer.
// Used for symbol names where case preservation is important.
func buildSymbolField() *mapping.FieldMapping {
	fieldMapping := mapping.NewTextFieldMapping()
	fieldMapping.Analyzer = analyzer.SymbolAnalyzerName
	fieldMapping.Store = true
	fieldMapping.Index = true
	fieldMapping.IncludeInAll = true
	return fieldMapping
}

// buildCommentField creates a field mapping using the comment analyzer.
// Used for code comments with stemming for natural language search.
func buildCommentField() *mapping.FieldMapping {
	fieldMapping := mapping.NewTextFieldMapping()
	fieldMapping.Analyzer = analyzer.CommentAnalyzerName
	fieldMapping.Store = true
	fieldMapping.Index = true
	fieldMapping.IncludeInAll = true
	return fieldMapping
}

// buildDateTimeField creates a field mapping for datetime fields.
// Used for ModifiedAt and IndexedAt timestamps.
func buildDateTimeField() *mapping.FieldMapping {
	fieldMapping := mapping.NewDateTimeFieldMapping()
	fieldMapping.Store = true
	fieldMapping.Index = true
	fieldMapping.IncludeInAll = false
	return fieldMapping
}
