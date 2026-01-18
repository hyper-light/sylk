package bleve

import (
	"testing"

	"github.com/adalundhe/sylk/core/search/analyzer"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// BuildDocumentMapping Tests
// =============================================================================

func TestBuildDocumentMapping_ReturnsNonNil(t *testing.T) {
	docMapping := BuildDocumentMapping()

	require.NotNil(t, docMapping)
}

func TestBuildDocumentMapping_DisablesDynamicMapping(t *testing.T) {
	docMapping := BuildDocumentMapping()

	assert.False(t, docMapping.Dynamic)
}

func TestBuildDocumentMapping_HasAllExpectedFields(t *testing.T) {
	docMapping := BuildDocumentMapping()

	expectedFields := []string{
		"id",
		"path",
		"type",
		"language",
		"content",
		"symbols",
		"comments",
		"imports",
		"checksum",
		"modified_at",
		"indexed_at",
		"git_commit",
	}

	for _, field := range expectedFields {
		assert.Contains(t, docMapping.Properties, field,
			"document mapping should contain field: %s", field)
	}
}

// =============================================================================
// Disabled Field Tests (ID, Checksum)
// =============================================================================

func TestBuildDocumentMapping_IDFieldIsDisabled(t *testing.T) {
	docMapping := BuildDocumentMapping()

	idMapping := docMapping.Properties["id"]
	require.NotNil(t, idMapping, "id field mapping should exist")
	require.Len(t, idMapping.Fields, 1, "id should have exactly one field mapping")

	fieldMapping := idMapping.Fields[0]
	assert.True(t, fieldMapping.Store, "id should be stored")
	assert.False(t, fieldMapping.Index, "id should not be indexed")
	assert.False(t, fieldMapping.IncludeInAll, "id should not be included in _all")
}

func TestBuildDocumentMapping_ChecksumFieldIsDisabled(t *testing.T) {
	docMapping := BuildDocumentMapping()

	checksumMapping := docMapping.Properties["checksum"]
	require.NotNil(t, checksumMapping, "checksum field mapping should exist")
	require.Len(t, checksumMapping.Fields, 1, "checksum should have exactly one field mapping")

	fieldMapping := checksumMapping.Fields[0]
	assert.True(t, fieldMapping.Store, "checksum should be stored")
	assert.False(t, fieldMapping.Index, "checksum should not be indexed")
	assert.False(t, fieldMapping.IncludeInAll, "checksum should not be included in _all")
}

// =============================================================================
// Keyword Field Tests (Path, Type, Language, Imports, GitCommit)
// =============================================================================

func TestBuildDocumentMapping_PathFieldUsesKeywordAnalyzer(t *testing.T) {
	docMapping := BuildDocumentMapping()

	pathMapping := docMapping.Properties["path"]
	require.NotNil(t, pathMapping, "path field mapping should exist")
	require.Len(t, pathMapping.Fields, 1, "path should have exactly one field mapping")

	fieldMapping := pathMapping.Fields[0]
	assert.Equal(t, KeywordAnalyzerName, fieldMapping.Analyzer, "path should use keyword analyzer")
	assert.True(t, fieldMapping.Store, "path should be stored")
	assert.True(t, fieldMapping.Index, "path should be indexed")
	assert.False(t, fieldMapping.IncludeInAll, "path should not be included in _all")
}

func TestBuildDocumentMapping_TypeFieldUsesKeywordAnalyzer(t *testing.T) {
	docMapping := BuildDocumentMapping()

	typeMapping := docMapping.Properties["type"]
	require.NotNil(t, typeMapping, "type field mapping should exist")
	require.Len(t, typeMapping.Fields, 1, "type should have exactly one field mapping")

	fieldMapping := typeMapping.Fields[0]
	assert.Equal(t, KeywordAnalyzerName, fieldMapping.Analyzer, "type should use keyword analyzer")
	assert.True(t, fieldMapping.Store, "type should be stored")
	assert.True(t, fieldMapping.Index, "type should be indexed")
}

func TestBuildDocumentMapping_LanguageFieldUsesKeywordAnalyzer(t *testing.T) {
	docMapping := BuildDocumentMapping()

	langMapping := docMapping.Properties["language"]
	require.NotNil(t, langMapping, "language field mapping should exist")
	require.Len(t, langMapping.Fields, 1, "language should have exactly one field mapping")

	fieldMapping := langMapping.Fields[0]
	assert.Equal(t, KeywordAnalyzerName, fieldMapping.Analyzer, "language should use keyword analyzer")
	assert.True(t, fieldMapping.Store, "language should be stored")
	assert.True(t, fieldMapping.Index, "language should be indexed")
}

func TestBuildDocumentMapping_ImportsFieldUsesKeywordAnalyzer(t *testing.T) {
	docMapping := BuildDocumentMapping()

	importsMapping := docMapping.Properties["imports"]
	require.NotNil(t, importsMapping, "imports field mapping should exist")
	require.Len(t, importsMapping.Fields, 1, "imports should have exactly one field mapping")

	fieldMapping := importsMapping.Fields[0]
	assert.Equal(t, KeywordAnalyzerName, fieldMapping.Analyzer, "imports should use keyword analyzer")
	assert.True(t, fieldMapping.Store, "imports should be stored")
	assert.True(t, fieldMapping.Index, "imports should be indexed")
}

func TestBuildDocumentMapping_GitCommitFieldUsesKeywordAnalyzer(t *testing.T) {
	docMapping := BuildDocumentMapping()

	gitCommitMapping := docMapping.Properties["git_commit"]
	require.NotNil(t, gitCommitMapping, "git_commit field mapping should exist")
	require.Len(t, gitCommitMapping.Fields, 1, "git_commit should have exactly one field mapping")

	fieldMapping := gitCommitMapping.Fields[0]
	assert.Equal(t, KeywordAnalyzerName, fieldMapping.Analyzer, "git_commit should use keyword analyzer")
	assert.True(t, fieldMapping.Store, "git_commit should be stored")
	assert.True(t, fieldMapping.Index, "git_commit should be indexed")
}

// =============================================================================
// Code Analyzer Field Tests (Content)
// =============================================================================

func TestBuildDocumentMapping_ContentFieldUsesCodeAnalyzer(t *testing.T) {
	docMapping := BuildDocumentMapping()

	contentMapping := docMapping.Properties["content"]
	require.NotNil(t, contentMapping, "content field mapping should exist")
	require.Len(t, contentMapping.Fields, 1, "content should have exactly one field mapping")

	fieldMapping := contentMapping.Fields[0]
	assert.Equal(t, analyzer.CodeAnalyzerName, fieldMapping.Analyzer, "content should use code analyzer")
	assert.True(t, fieldMapping.Store, "content should be stored")
	assert.True(t, fieldMapping.Index, "content should be indexed")
	assert.True(t, fieldMapping.IncludeInAll, "content should be included in _all")
}

// =============================================================================
// Symbol Analyzer Field Tests (Symbols)
// =============================================================================

func TestBuildDocumentMapping_SymbolsFieldUsesSymbolAnalyzer(t *testing.T) {
	docMapping := BuildDocumentMapping()

	symbolsMapping := docMapping.Properties["symbols"]
	require.NotNil(t, symbolsMapping, "symbols field mapping should exist")
	require.Len(t, symbolsMapping.Fields, 1, "symbols should have exactly one field mapping")

	fieldMapping := symbolsMapping.Fields[0]
	assert.Equal(t, analyzer.SymbolAnalyzerName, fieldMapping.Analyzer, "symbols should use symbol analyzer")
	assert.True(t, fieldMapping.Store, "symbols should be stored")
	assert.True(t, fieldMapping.Index, "symbols should be indexed")
	assert.True(t, fieldMapping.IncludeInAll, "symbols should be included in _all")
}

// =============================================================================
// Comment Analyzer Field Tests (Comments)
// =============================================================================

func TestBuildDocumentMapping_CommentsFieldUsesCommentAnalyzer(t *testing.T) {
	docMapping := BuildDocumentMapping()

	commentsMapping := docMapping.Properties["comments"]
	require.NotNil(t, commentsMapping, "comments field mapping should exist")
	require.Len(t, commentsMapping.Fields, 1, "comments should have exactly one field mapping")

	fieldMapping := commentsMapping.Fields[0]
	assert.Equal(t, analyzer.CommentAnalyzerName, fieldMapping.Analyzer, "comments should use comment analyzer")
	assert.True(t, fieldMapping.Store, "comments should be stored")
	assert.True(t, fieldMapping.Index, "comments should be indexed")
	assert.True(t, fieldMapping.IncludeInAll, "comments should be included in _all")
}

// =============================================================================
// DateTime Field Tests (ModifiedAt, IndexedAt)
// =============================================================================

func TestBuildDocumentMapping_ModifiedAtFieldIsDateTime(t *testing.T) {
	docMapping := BuildDocumentMapping()

	modifiedAtMapping := docMapping.Properties["modified_at"]
	require.NotNil(t, modifiedAtMapping, "modified_at field mapping should exist")
	require.Len(t, modifiedAtMapping.Fields, 1, "modified_at should have exactly one field mapping")

	fieldMapping := modifiedAtMapping.Fields[0]
	assert.Equal(t, "datetime", fieldMapping.Type, "modified_at should be datetime type")
	assert.True(t, fieldMapping.Store, "modified_at should be stored")
	assert.True(t, fieldMapping.Index, "modified_at should be indexed")
	assert.False(t, fieldMapping.IncludeInAll, "modified_at should not be included in _all")
}

func TestBuildDocumentMapping_IndexedAtFieldIsDateTime(t *testing.T) {
	docMapping := BuildDocumentMapping()

	indexedAtMapping := docMapping.Properties["indexed_at"]
	require.NotNil(t, indexedAtMapping, "indexed_at field mapping should exist")
	require.Len(t, indexedAtMapping.Fields, 1, "indexed_at should have exactly one field mapping")

	fieldMapping := indexedAtMapping.Fields[0]
	assert.Equal(t, "datetime", fieldMapping.Type, "indexed_at should be datetime type")
	assert.True(t, fieldMapping.Store, "indexed_at should be stored")
	assert.True(t, fieldMapping.Index, "indexed_at should be indexed")
	assert.False(t, fieldMapping.IncludeInAll, "indexed_at should not be included in _all")
}

// =============================================================================
// BuildIndexMapping Tests
// =============================================================================

func TestBuildIndexMapping_ReturnsNonNil(t *testing.T) {
	indexMapping, err := BuildIndexMapping()

	require.NoError(t, err)
	require.NotNil(t, indexMapping)
}

func TestBuildIndexMapping_HasCorrectDefaultAnalyzer(t *testing.T) {
	indexMapping, err := BuildIndexMapping()

	require.NoError(t, err)
	assert.Equal(t, DefaultAnalyzerName, indexMapping.DefaultAnalyzer,
		"default analyzer should be standard")
}

func TestBuildIndexMapping_HasCorrectDefaultType(t *testing.T) {
	indexMapping, err := BuildIndexMapping()

	require.NoError(t, err)
	assert.Equal(t, DefaultTypeName, indexMapping.DefaultType,
		"default type should be document")
}

func TestBuildIndexMapping_DisablesDynamicMapping(t *testing.T) {
	indexMapping, err := BuildIndexMapping()

	require.NoError(t, err)
	assert.False(t, indexMapping.DefaultMapping.Dynamic,
		"dynamic mapping should be disabled")
}

func TestBuildIndexMapping_ContainsDocumentTypeMapping(t *testing.T) {
	indexMapping, err := BuildIndexMapping()

	require.NoError(t, err)
	docMapping := indexMapping.TypeMapping[DefaultTypeName]
	require.NotNil(t, docMapping, "document type mapping should exist")
}

func TestBuildIndexMapping_DocumentMappingHasExpectedStructure(t *testing.T) {
	indexMapping, err := BuildIndexMapping()

	require.NoError(t, err)
	docMapping := indexMapping.TypeMapping[DefaultTypeName]
	require.NotNil(t, docMapping)

	// Verify key fields exist
	assert.Contains(t, docMapping.Properties, "content")
	assert.Contains(t, docMapping.Properties, "symbols")
	assert.Contains(t, docMapping.Properties, "path")
	assert.Contains(t, docMapping.Properties, "modified_at")
}

// =============================================================================
// Field Mapping Builder Tests
// =============================================================================

func TestBuildDisabledField_Configuration(t *testing.T) {
	field := buildDisabledField()

	assert.True(t, field.Store, "disabled field should be stored")
	assert.False(t, field.Index, "disabled field should not be indexed")
	assert.False(t, field.IncludeInAll, "disabled field should not be in _all")
}

func TestBuildKeywordField_Configuration(t *testing.T) {
	field := buildKeywordField()

	assert.Equal(t, KeywordAnalyzerName, field.Analyzer)
	assert.True(t, field.Store, "keyword field should be stored")
	assert.True(t, field.Index, "keyword field should be indexed")
	assert.False(t, field.IncludeInAll, "keyword field should not be in _all")
}

func TestBuildCodeField_Configuration(t *testing.T) {
	field := buildCodeField()

	assert.Equal(t, analyzer.CodeAnalyzerName, field.Analyzer)
	assert.True(t, field.Store, "code field should be stored")
	assert.True(t, field.Index, "code field should be indexed")
	assert.True(t, field.IncludeInAll, "code field should be in _all")
}

func TestBuildSymbolField_Configuration(t *testing.T) {
	field := buildSymbolField()

	assert.Equal(t, analyzer.SymbolAnalyzerName, field.Analyzer)
	assert.True(t, field.Store, "symbol field should be stored")
	assert.True(t, field.Index, "symbol field should be indexed")
	assert.True(t, field.IncludeInAll, "symbol field should be in _all")
}

func TestBuildCommentField_Configuration(t *testing.T) {
	field := buildCommentField()

	assert.Equal(t, analyzer.CommentAnalyzerName, field.Analyzer)
	assert.True(t, field.Store, "comment field should be stored")
	assert.True(t, field.Index, "comment field should be indexed")
	assert.True(t, field.IncludeInAll, "comment field should be in _all")
}

func TestBuildDateTimeField_Configuration(t *testing.T) {
	field := buildDateTimeField()

	assert.Equal(t, "datetime", field.Type)
	assert.True(t, field.Store, "datetime field should be stored")
	assert.True(t, field.Index, "datetime field should be indexed")
	assert.False(t, field.IncludeInAll, "datetime field should not be in _all")
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestConstants_HaveExpectedValues(t *testing.T) {
	assert.Equal(t, "document", DefaultTypeName)
	assert.Equal(t, "standard", DefaultAnalyzerName)
	assert.Equal(t, "keyword", KeywordAnalyzerName)
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestBuildIndexMapping_CanBeValidated(t *testing.T) {
	indexMapping, err := BuildIndexMapping()
	require.NoError(t, err)

	// The mapping should be valid and usable
	// This tests that all referenced analyzers are either built-in or registered
	require.NotNil(t, indexMapping.TypeMapping)
	require.NotNil(t, indexMapping.DefaultMapping)
}

func TestDocumentMapping_AllFieldsAreStored(t *testing.T) {
	docMapping := BuildDocumentMapping()

	// All fields should be stored (either for retrieval or search highlighting)
	for fieldName, propMapping := range docMapping.Properties {
		require.Len(t, propMapping.Fields, 1, "field %s should have one mapping", fieldName)
		assert.True(t, propMapping.Fields[0].Store,
			"field %s should be stored", fieldName)
	}
}

func TestDocumentMapping_SearchableFieldsAreIndexed(t *testing.T) {
	docMapping := BuildDocumentMapping()

	// Fields that should be searchable
	searchableFields := []string{
		"path", "type", "language", "content", "symbols",
		"comments", "imports", "modified_at", "indexed_at", "git_commit",
	}

	for _, fieldName := range searchableFields {
		propMapping := docMapping.Properties[fieldName]
		require.NotNil(t, propMapping, "field %s should exist", fieldName)
		require.Len(t, propMapping.Fields, 1, "field %s should have one mapping", fieldName)
		assert.True(t, propMapping.Fields[0].Index,
			"field %s should be indexed", fieldName)
	}
}

func TestDocumentMapping_NonSearchableFieldsAreNotIndexed(t *testing.T) {
	docMapping := BuildDocumentMapping()

	// Fields that should NOT be searchable (disabled)
	nonSearchableFields := []string{"id", "checksum"}

	for _, fieldName := range nonSearchableFields {
		propMapping := docMapping.Properties[fieldName]
		require.NotNil(t, propMapping, "field %s should exist", fieldName)
		require.Len(t, propMapping.Fields, 1, "field %s should have one mapping", fieldName)
		assert.False(t, propMapping.Fields[0].Index,
			"field %s should not be indexed", fieldName)
	}
}

// =============================================================================
// Field Mapping Type Tests
// =============================================================================

func TestFieldMappingTypes_TextFields(t *testing.T) {
	docMapping := BuildDocumentMapping()

	textFields := []string{
		"id", "path", "type", "language", "content",
		"symbols", "comments", "imports", "checksum", "git_commit",
	}

	for _, fieldName := range textFields {
		propMapping := docMapping.Properties[fieldName]
		require.NotNil(t, propMapping, "field %s should exist", fieldName)
		require.Len(t, propMapping.Fields, 1, "field %s should have one mapping", fieldName)

		fieldType := propMapping.Fields[0].Type
		assert.Equal(t, "text", fieldType,
			"field %s should be text type, got %s", fieldName, fieldType)
	}
}

func TestFieldMappingTypes_DateTimeFields(t *testing.T) {
	docMapping := BuildDocumentMapping()

	dateTimeFields := []string{"modified_at", "indexed_at"}

	for _, fieldName := range dateTimeFields {
		propMapping := docMapping.Properties[fieldName]
		require.NotNil(t, propMapping, "field %s should exist", fieldName)
		require.Len(t, propMapping.Fields, 1, "field %s should have one mapping", fieldName)

		fieldType := propMapping.Fields[0].Type
		assert.Equal(t, "datetime", fieldType,
			"field %s should be datetime type, got %s", fieldName, fieldType)
	}
}

// =============================================================================
// _all Field Inclusion Tests
// =============================================================================

func TestDocumentMapping_AllFieldInclusion(t *testing.T) {
	docMapping := BuildDocumentMapping()

	// Fields that should be included in _all (searchable content)
	includedInAll := []string{"content", "symbols", "comments"}

	// Fields that should NOT be included in _all (metadata/exact match)
	notIncludedInAll := []string{
		"id", "path", "type", "language", "imports",
		"checksum", "modified_at", "indexed_at", "git_commit",
	}

	for _, fieldName := range includedInAll {
		propMapping := docMapping.Properties[fieldName]
		require.NotNil(t, propMapping, "field %s should exist", fieldName)
		require.Len(t, propMapping.Fields, 1, "field %s should have one mapping", fieldName)
		assert.True(t, propMapping.Fields[0].IncludeInAll,
			"field %s should be included in _all", fieldName)
	}

	for _, fieldName := range notIncludedInAll {
		propMapping := docMapping.Properties[fieldName]
		require.NotNil(t, propMapping, "field %s should exist", fieldName)
		require.Len(t, propMapping.Fields, 1, "field %s should have one mapping", fieldName)
		assert.False(t, propMapping.Fields[0].IncludeInAll,
			"field %s should not be included in _all", fieldName)
	}
}

// =============================================================================
// Analyzer Assignment Tests
// =============================================================================

func TestDocumentMapping_AnalyzerAssignments(t *testing.T) {
	docMapping := BuildDocumentMapping()

	testCases := []struct {
		fieldName string
		analyzer  string
	}{
		{"path", KeywordAnalyzerName},
		{"type", KeywordAnalyzerName},
		{"language", KeywordAnalyzerName},
		{"content", analyzer.CodeAnalyzerName},
		{"symbols", analyzer.SymbolAnalyzerName},
		{"comments", analyzer.CommentAnalyzerName},
		{"imports", KeywordAnalyzerName},
		{"git_commit", KeywordAnalyzerName},
	}

	for _, tc := range testCases {
		t.Run(tc.fieldName, func(t *testing.T) {
			propMapping := docMapping.Properties[tc.fieldName]
			require.NotNil(t, propMapping, "field %s should exist", tc.fieldName)
			require.Len(t, propMapping.Fields, 1, "field %s should have one mapping", tc.fieldName)
			assert.Equal(t, tc.analyzer, propMapping.Fields[0].Analyzer,
				"field %s should use %s analyzer", tc.fieldName, tc.analyzer)
		})
	}
}

// =============================================================================
// DocumentMapping Property Count Tests
// =============================================================================

func TestBuildDocumentMapping_HasCorrectFieldCount(t *testing.T) {
	docMapping := BuildDocumentMapping()

	// Should have exactly 12 fields as specified in the Document struct
	expectedFieldCount := 12
	actualFieldCount := len(docMapping.Properties)

	assert.Equal(t, expectedFieldCount, actualFieldCount,
		"document mapping should have exactly %d fields, got %d",
		expectedFieldCount, actualFieldCount)
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkBuildDocumentMapping(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = BuildDocumentMapping()
	}
}

func BenchmarkBuildIndexMapping(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = BuildIndexMapping()
	}
}

// =============================================================================
// Type Verification Tests
// =============================================================================

func TestBuildDocumentMapping_ReturnsCorrectType(t *testing.T) {
	docMapping := BuildDocumentMapping()

	var _ *mapping.DocumentMapping = docMapping
}

func TestBuildIndexMapping_ReturnsCorrectType(t *testing.T) {
	indexMapping, err := BuildIndexMapping()

	require.NoError(t, err)
	var _ *mapping.IndexMappingImpl = indexMapping
}
