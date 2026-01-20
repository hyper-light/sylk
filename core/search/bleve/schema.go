// Package bleve provides Bleve index configuration and document mappings for the
// Sylk Document Search System. It defines schemas for indexing source code,
// markdown, configuration files, LLM communications, web fetches, notes, and git commits.
package bleve

import (
	"github.com/blevesearch/bleve/v2/mapping"

	// Import custom analyzers to register them with the Bleve registry
	_ "github.com/adalundhe/sylk/core/search/analyzer"
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

// globalConfigLoader is the default configuration loader for schema building.
var globalConfigLoader = NewSchemaConfigLoader()

// GetConfigLoader returns the global schema configuration loader.
func GetConfigLoader() *SchemaConfigLoader {
	return globalConfigLoader
}

// BuildDocumentMapping creates a DocumentMapping for indexing Document structs.
// It uses the global configuration loader to determine field mappings.
// Returns a configured DocumentMapping ready for use in an IndexMapping.
func BuildDocumentMapping() *mapping.DocumentMapping {
	return BuildDocumentMappingFromConfig(globalConfigLoader.GetConfig())
}

// BuildDocumentMappingFromConfig creates a DocumentMapping from a SchemaConfig.
// It configures appropriate analyzers for each field type based on configuration.
// Returns a configured DocumentMapping ready for use in an IndexMapping.
func BuildDocumentMappingFromConfig(config *SchemaConfig) *mapping.DocumentMapping {
	docMapping := mapping.NewDocumentMapping()
	docMapping.Dynamic = config.DynamicMapping

	for _, field := range config.Fields {
		fieldMapping := buildFieldFromConfig(&field)
		docMapping.AddFieldMappingsAt(field.Name, fieldMapping)
	}

	return docMapping
}

// BuildIndexMapping creates a complete IndexMapping for the document search index.
// It uses the global configuration loader for settings.
// Returns a configured IndexMapping ready for index creation.
func BuildIndexMapping() (*mapping.IndexMappingImpl, error) {
	return BuildIndexMappingFromConfig(globalConfigLoader.GetConfig())
}

// BuildIndexMappingFromConfig creates an IndexMapping from a SchemaConfig.
// It configures the document type mapping, default analyzer, and dynamic mapping.
// Returns a configured IndexMapping ready for index creation.
func BuildIndexMappingFromConfig(config *SchemaConfig) (*mapping.IndexMappingImpl, error) {
	indexMapping := mapping.NewIndexMapping()

	// Set default analyzer for any unmapped text fields
	indexMapping.DefaultAnalyzer = config.DefaultAnalyzer

	// Set dynamic mapping from config
	indexMapping.DefaultMapping.Dynamic = config.DynamicMapping

	// Add document type mapping
	indexMapping.AddDocumentMapping(config.TypeName, BuildDocumentMappingFromConfig(config))

	// Set default document type
	indexMapping.DefaultType = config.TypeName

	return indexMapping, nil
}

// buildFieldFromConfig creates a FieldMapping from a FieldConfig.
func buildFieldFromConfig(fc *FieldMappingConfig) *mapping.FieldMapping {
	switch fc.Type {
	case FieldTypeDateTime:
		return buildDateTimeFieldFromConfig(fc)
	case FieldTypeDisabled:
		return buildDisabledFieldFromConfig(fc)
	default:
		return buildTextFieldFromConfig(fc)
	}
}

// buildTextFieldFromConfig creates a text field mapping from config.
func buildTextFieldFromConfig(fc *FieldMappingConfig) *mapping.FieldMapping {
	fieldMapping := mapping.NewTextFieldMapping()
	fieldMapping.Analyzer = fc.Analyzer
	fieldMapping.Store = fc.Store
	fieldMapping.Index = fc.Index
	fieldMapping.IncludeInAll = fc.IncludeInAll
	return fieldMapping
}

// buildDateTimeFieldFromConfig creates a datetime field mapping from config.
func buildDateTimeFieldFromConfig(fc *FieldMappingConfig) *mapping.FieldMapping {
	fieldMapping := mapping.NewDateTimeFieldMapping()
	fieldMapping.Store = fc.Store
	fieldMapping.Index = fc.Index
	fieldMapping.IncludeInAll = fc.IncludeInAll
	return fieldMapping
}

// buildDisabledFieldFromConfig creates a disabled field mapping from config.
func buildDisabledFieldFromConfig(fc *FieldMappingConfig) *mapping.FieldMapping {
	fieldMapping := mapping.NewTextFieldMapping()
	fieldMapping.Store = fc.Store
	fieldMapping.Index = false
	fieldMapping.IncludeInAll = false
	return fieldMapping
}

// =============================================================================
// Domain Field Constants
// =============================================================================

// DomainFieldName is the field name used for domain filtering in the index.
const DomainFieldName = "domain"

// =============================================================================
// Legacy Field Mapping Builders (for backwards compatibility)
// =============================================================================

// buildDisabledField creates a field mapping that stores but does not index the field.
// Used for fields like ID and Checksum that are needed for retrieval but not search.
// Deprecated: Use BuildDocumentMappingFromConfig with a SchemaConfig instead.
func buildDisabledField() *mapping.FieldMapping {
	return buildDisabledFieldFromConfig(&FieldMappingConfig{Store: true, Index: false})
}

// buildKeywordField creates a field mapping using the keyword analyzer.
// Used for exact-match fields like path, type, and language.
// Deprecated: Use BuildDocumentMappingFromConfig with a SchemaConfig instead.
func buildKeywordField() *mapping.FieldMapping {
	return buildTextFieldFromConfig(&FieldMappingConfig{
		Analyzer:     KeywordAnalyzerName,
		Store:        true,
		Index:        true,
		IncludeInAll: false,
	})
}

// buildCodeField creates a field mapping using the code analyzer.
// Used for source code content with camelCase/snake_case tokenization.
// Deprecated: Use BuildDocumentMappingFromConfig with a SchemaConfig instead.
func buildCodeField() *mapping.FieldMapping {
	return buildTextFieldFromConfig(&FieldMappingConfig{
		Analyzer:     "code",
		Store:        true,
		Index:        true,
		IncludeInAll: true,
	})
}

// buildSymbolField creates a field mapping using the symbol analyzer.
// Used for symbol names where case preservation is important.
// Deprecated: Use BuildDocumentMappingFromConfig with a SchemaConfig instead.
func buildSymbolField() *mapping.FieldMapping {
	return buildTextFieldFromConfig(&FieldMappingConfig{
		Analyzer:     "symbol",
		Store:        true,
		Index:        true,
		IncludeInAll: true,
	})
}

// buildCommentField creates a field mapping using the comment analyzer.
// Used for code comments with stemming for natural language search.
// Deprecated: Use BuildDocumentMappingFromConfig with a SchemaConfig instead.
func buildCommentField() *mapping.FieldMapping {
	return buildTextFieldFromConfig(&FieldMappingConfig{
		Analyzer:     "comment",
		Store:        true,
		Index:        true,
		IncludeInAll: true,
	})
}

// buildDateTimeField creates a field mapping for datetime fields.
// Used for ModifiedAt and IndexedAt timestamps.
// Deprecated: Use BuildDocumentMappingFromConfig with a SchemaConfig instead.
func buildDateTimeField() *mapping.FieldMapping {
	return buildDateTimeFieldFromConfig(&FieldMappingConfig{
		Store:        true,
		Index:        true,
		IncludeInAll: false,
	})
}
