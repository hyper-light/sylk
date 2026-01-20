package bleve

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// FieldMappingConfig Tests
// =============================================================================

func TestFieldMappingConfig_Validate_ValidTextType(t *testing.T) {
	fc := &FieldMappingConfig{
		Name:     "content",
		Type:     FieldTypeText,
		Analyzer: "standard",
		Store:    true,
		Index:    true,
	}

	err := fc.Validate()

	assert.NoError(t, err)
}

func TestFieldMappingConfig_Validate_ValidDateTimeType(t *testing.T) {
	fc := &FieldMappingConfig{
		Name:  "created_at",
		Type:  FieldTypeDateTime,
		Store: true,
		Index: true,
	}

	err := fc.Validate()

	assert.NoError(t, err)
}

func TestFieldMappingConfig_Validate_ValidDisabledType(t *testing.T) {
	fc := &FieldMappingConfig{
		Name:  "internal_id",
		Type:  FieldTypeDisabled,
		Store: true,
		Index: false,
	}

	err := fc.Validate()

	assert.NoError(t, err)
}

func TestFieldMappingConfig_Validate_EmptyName(t *testing.T) {
	fc := &FieldMappingConfig{
		Name: "",
		Type: FieldTypeText,
	}

	err := fc.Validate()

	assert.ErrorIs(t, err, ErrEmptyFieldName)
}

func TestFieldMappingConfig_Validate_EmptyType(t *testing.T) {
	fc := &FieldMappingConfig{
		Name: "field",
		Type: "",
	}

	err := fc.Validate()

	assert.ErrorIs(t, err, ErrInvalidFieldType)
}

func TestFieldMappingConfig_Validate_InvalidType(t *testing.T) {
	fc := &FieldMappingConfig{
		Name: "field",
		Type: "unknown_type",
	}

	err := fc.Validate()

	assert.ErrorIs(t, err, ErrInvalidFieldType)
}

// =============================================================================
// SchemaConfig Tests
// =============================================================================

func TestSchemaConfig_Validate_ValidConfig(t *testing.T) {
	sc := &SchemaConfig{
		TypeName:        "document",
		DefaultAnalyzer: "standard",
		Fields: []FieldMappingConfig{
			{Name: "content", Type: FieldTypeText, Analyzer: "standard"},
		},
	}

	err := sc.Validate()

	assert.NoError(t, err)
}

func TestSchemaConfig_Validate_EmptyTypeName(t *testing.T) {
	sc := &SchemaConfig{
		TypeName:        "",
		DefaultAnalyzer: "standard",
	}

	err := sc.Validate()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type_name is required")
}

func TestSchemaConfig_Validate_EmptyDefaultAnalyzer(t *testing.T) {
	sc := &SchemaConfig{
		TypeName:        "document",
		DefaultAnalyzer: "",
	}

	err := sc.Validate()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "default_analyzer is required")
}

func TestSchemaConfig_Validate_InvalidField(t *testing.T) {
	sc := &SchemaConfig{
		TypeName:        "document",
		DefaultAnalyzer: "standard",
		Fields: []FieldMappingConfig{
			{Name: "", Type: FieldTypeText}, // Empty name
		},
	}

	err := sc.Validate()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "field 0")
}

func TestSchemaConfig_GetField_Found(t *testing.T) {
	sc := &SchemaConfig{
		TypeName:        "document",
		DefaultAnalyzer: "standard",
		Fields: []FieldMappingConfig{
			{Name: "content", Type: FieldTypeText, Analyzer: "code"},
			{Name: "path", Type: FieldTypeText, Analyzer: "keyword"},
		},
	}

	field := sc.GetField("content")

	require.NotNil(t, field)
	assert.Equal(t, "content", field.Name)
	assert.Equal(t, "code", field.Analyzer)
}

func TestSchemaConfig_GetField_NotFound(t *testing.T) {
	sc := &SchemaConfig{
		TypeName:        "document",
		DefaultAnalyzer: "standard",
		Fields: []FieldMappingConfig{
			{Name: "content", Type: FieldTypeText},
		},
	}

	field := sc.GetField("nonexistent")

	assert.Nil(t, field)
}

// =============================================================================
// DefaultSchemaConfig Tests
// =============================================================================

func TestDefaultSchemaConfig_ReturnsValidConfig(t *testing.T) {
	config := DefaultSchemaConfig()

	require.NotNil(t, config)
	assert.Equal(t, DefaultTypeName, config.TypeName)
	assert.Equal(t, DefaultAnalyzerName, config.DefaultAnalyzer)
	assert.False(t, config.DynamicMapping)
}

func TestDefaultSchemaConfig_HasAllExpectedFields(t *testing.T) {
	config := DefaultSchemaConfig()

	expectedFields := []string{
		"id", "path", "type", "language", "content", "symbols",
		"comments", "imports", "checksum", "modified_at",
		"indexed_at", "git_commit", "domain",
	}

	for _, fieldName := range expectedFields {
		field := config.GetField(fieldName)
		assert.NotNil(t, field, "expected field %q to exist", fieldName)
	}
}

func TestDefaultSchemaConfig_ContentFieldUsesCodeAnalyzer(t *testing.T) {
	config := DefaultSchemaConfig()

	field := config.GetField("content")

	require.NotNil(t, field)
	assert.Equal(t, "code", field.Analyzer)
	assert.True(t, field.IncludeInAll)
}

func TestDefaultSchemaConfig_PathFieldUsesKeywordAnalyzer(t *testing.T) {
	config := DefaultSchemaConfig()

	field := config.GetField("path")

	require.NotNil(t, field)
	assert.Equal(t, KeywordAnalyzerName, field.Analyzer)
	assert.False(t, field.IncludeInAll)
}

func TestDefaultSchemaConfig_IDFieldIsDisabled(t *testing.T) {
	config := DefaultSchemaConfig()

	field := config.GetField("id")

	require.NotNil(t, field)
	assert.Equal(t, FieldTypeDisabled, field.Type)
	assert.False(t, field.Index)
	assert.True(t, field.Store)
}

func TestDefaultSchemaConfig_DateTimeFieldsHaveCorrectType(t *testing.T) {
	config := DefaultSchemaConfig()

	for _, fieldName := range []string{"modified_at", "indexed_at"} {
		field := config.GetField(fieldName)
		require.NotNil(t, field, "field %q should exist", fieldName)
		assert.Equal(t, FieldTypeDateTime, field.Type, "field %q should be datetime", fieldName)
	}
}

// =============================================================================
// SchemaConfigLoader Tests
// =============================================================================

func TestNewSchemaConfigLoader_HasDefaultConfig(t *testing.T) {
	loader := NewSchemaConfigLoader()

	config := loader.GetConfig()

	require.NotNil(t, config)
	assert.Equal(t, DefaultTypeName, config.TypeName)
}

func TestSchemaConfigLoader_LoadFromFile_NonExistent_FallsBackToDefault(t *testing.T) {
	loader := NewSchemaConfigLoader()

	err := loader.LoadFromFile("/nonexistent/path/config.json")

	assert.NoError(t, err)
	config := loader.GetConfig()
	assert.Equal(t, DefaultTypeName, config.TypeName)
}

func TestSchemaConfigLoader_LoadFromFile_ValidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	jsonContent := `{
		"type_name": "custom_doc",
		"default_analyzer": "simple",
		"dynamic_mapping": false,
		"fields": [
			{"name": "title", "type": "text", "analyzer": "standard", "store": true, "index": true}
		]
	}`

	err := os.WriteFile(configPath, []byte(jsonContent), 0644)
	require.NoError(t, err)

	loader := NewSchemaConfigLoader()
	err = loader.LoadFromFile(configPath)

	assert.NoError(t, err)
	config := loader.GetConfig()
	assert.Equal(t, "custom_doc", config.TypeName)
	assert.Equal(t, "simple", config.DefaultAnalyzer)

	// Custom field exists
	titleField := config.GetField("title")
	require.NotNil(t, titleField)
	assert.Equal(t, "standard", titleField.Analyzer)

	// Default fields are merged
	idField := config.GetField("id")
	require.NotNil(t, idField, "default id field should be merged")
}

func TestSchemaConfigLoader_LoadFromFile_ValidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
type_name: yaml_doc
default_analyzer: simple
dynamic_mapping: true
fields:
  - name: body
    type: text
    analyzer: standard
    store: true
    index: true
`

	err := os.WriteFile(configPath, []byte(yamlContent), 0644)
	require.NoError(t, err)

	loader := NewSchemaConfigLoader()
	err = loader.LoadFromFile(configPath)

	assert.NoError(t, err)
	config := loader.GetConfig()
	assert.Equal(t, "yaml_doc", config.TypeName)
	assert.True(t, config.DynamicMapping)
}

func TestSchemaConfigLoader_LoadFromFile_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	err := os.WriteFile(configPath, []byte("invalid json"), 0644)
	require.NoError(t, err)

	loader := NewSchemaConfigLoader()
	err = loader.LoadFromFile(configPath)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse JSON config")
}

func TestSchemaConfigLoader_LoadFromFile_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	err := os.WriteFile(configPath, []byte("invalid: yaml: content:"), 0644)
	require.NoError(t, err)

	loader := NewSchemaConfigLoader()
	err = loader.LoadFromFile(configPath)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse YAML config")
}

func TestSchemaConfigLoader_LoadFromFile_InvalidConfigContent(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Valid JSON but invalid config (missing type_name)
	jsonContent := `{
		"default_analyzer": "standard",
		"fields": []
	}`

	err := os.WriteFile(configPath, []byte(jsonContent), 0644)
	require.NoError(t, err)

	loader := NewSchemaConfigLoader()
	err = loader.LoadFromFile(configPath)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid configuration")
}

func TestSchemaConfigLoader_LoadFromBytes_JSON(t *testing.T) {
	loader := NewSchemaConfigLoader()

	jsonContent := `{
		"type_name": "bytes_doc",
		"default_analyzer": "standard",
		"fields": []
	}`

	err := loader.LoadFromBytes([]byte(jsonContent), "json")

	assert.NoError(t, err)
	assert.Equal(t, "bytes_doc", loader.GetConfig().TypeName)
}

func TestSchemaConfigLoader_LoadFromBytes_YAML(t *testing.T) {
	loader := NewSchemaConfigLoader()

	yamlContent := `
type_name: yaml_bytes_doc
default_analyzer: standard
fields: []
`

	err := loader.LoadFromBytes([]byte(yamlContent), "yaml")

	assert.NoError(t, err)
	assert.Equal(t, "yaml_bytes_doc", loader.GetConfig().TypeName)
}

func TestSchemaConfigLoader_LoadFromBytes_UnsupportedFormat(t *testing.T) {
	loader := NewSchemaConfigLoader()

	err := loader.LoadFromBytes([]byte("content"), "xml")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format")
}

func TestSchemaConfigLoader_SetConfig_Valid(t *testing.T) {
	loader := NewSchemaConfigLoader()
	newConfig := &SchemaConfig{
		TypeName:        "set_doc",
		DefaultAnalyzer: "standard",
		Fields:          []FieldMappingConfig{},
	}

	err := loader.SetConfig(newConfig)

	assert.NoError(t, err)
	assert.Equal(t, "set_doc", loader.GetConfig().TypeName)
}

func TestSchemaConfigLoader_SetConfig_Invalid(t *testing.T) {
	loader := NewSchemaConfigLoader()
	newConfig := &SchemaConfig{
		TypeName: "", // Invalid
	}

	err := loader.SetConfig(newConfig)

	assert.Error(t, err)
	// Original config should be unchanged
	assert.Equal(t, DefaultTypeName, loader.GetConfig().TypeName)
}

func TestSchemaConfigLoader_Reset(t *testing.T) {
	loader := NewSchemaConfigLoader()

	// Set custom config
	loader.SetConfig(&SchemaConfig{
		TypeName:        "custom",
		DefaultAnalyzer: "standard",
	})

	loader.Reset()

	assert.Equal(t, DefaultTypeName, loader.GetConfig().TypeName)
}

func TestSchemaConfigLoader_ConcurrentAccess(t *testing.T) {
	loader := NewSchemaConfigLoader()
	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			config := loader.GetConfig()
			_ = config.TypeName
		}()
	}

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			loader.Reset()
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// mergeWithDefaults Tests
// =============================================================================

func TestMergeWithDefaults_AddsDefaultTypeName(t *testing.T) {
	userConfig := &SchemaConfig{
		DefaultAnalyzer: "simple",
		Fields:          []FieldMappingConfig{},
	}

	merged := mergeWithDefaults(userConfig)

	assert.Equal(t, DefaultTypeName, merged.TypeName)
}

func TestMergeWithDefaults_AddsDefaultAnalyzer(t *testing.T) {
	userConfig := &SchemaConfig{
		TypeName: "custom",
		Fields:   []FieldMappingConfig{},
	}

	merged := mergeWithDefaults(userConfig)

	assert.Equal(t, DefaultAnalyzerName, merged.DefaultAnalyzer)
}

func TestMergeWithDefaults_AddsMissingDefaultFields(t *testing.T) {
	userConfig := &SchemaConfig{
		TypeName:        "custom",
		DefaultAnalyzer: "standard",
		Fields: []FieldMappingConfig{
			{Name: "custom_field", Type: FieldTypeText, Analyzer: "standard"},
		},
	}

	merged := mergeWithDefaults(userConfig)

	// Custom field preserved
	customField := merged.GetField("custom_field")
	assert.NotNil(t, customField)

	// Default fields added
	idField := merged.GetField("id")
	assert.NotNil(t, idField)
	assert.Equal(t, FieldTypeDisabled, idField.Type)
}

func TestMergeWithDefaults_PreservesUserOverrides(t *testing.T) {
	userConfig := &SchemaConfig{
		TypeName:        "custom",
		DefaultAnalyzer: "standard",
		Fields: []FieldMappingConfig{
			// Override default content field
			{Name: "content", Type: FieldTypeText, Analyzer: "custom_analyzer", Store: true, Index: true},
		},
	}

	merged := mergeWithDefaults(userConfig)

	contentField := merged.GetField("content")
	require.NotNil(t, contentField)
	assert.Equal(t, "custom_analyzer", contentField.Analyzer)
}

// =============================================================================
// isYAMLFile Tests
// =============================================================================

func TestIsYAMLFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/path/to/config.yaml", true},
		{"/path/to/config.yml", true},
		{"/path/to/config.json", false},
		{"/path/to/config", false},
		{"config.yaml", true},
		{"config.yml", true},
		{"config.json", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := isYAMLFile(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// =============================================================================
// getExtension Tests
// =============================================================================

func TestGetExtension(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/path/to/file.json", ".json"},
		{"/path/to/file.yaml", ".yaml"},
		{"/path/to/file.yml", ".yml"},
		{"/path/to/file", ""},
		{"file.txt", ".txt"},
		{"noextension", ""},
		{"/path.with.dots/file.json", ".json"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := getExtension(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// =============================================================================
// BuildDocumentMappingFromConfig Tests
// =============================================================================

func TestBuildDocumentMappingFromConfig_UsesConfigDynamicMapping(t *testing.T) {
	config := &SchemaConfig{
		TypeName:        "test",
		DefaultAnalyzer: "standard",
		DynamicMapping:  true,
		Fields:          []FieldMappingConfig{},
	}

	docMapping := BuildDocumentMappingFromConfig(config)

	assert.True(t, docMapping.Dynamic)
}

func TestBuildDocumentMappingFromConfig_CreatesAllFields(t *testing.T) {
	config := &SchemaConfig{
		TypeName:        "test",
		DefaultAnalyzer: "standard",
		Fields: []FieldMappingConfig{
			{Name: "field1", Type: FieldTypeText, Analyzer: "standard", Store: true, Index: true},
			{Name: "field2", Type: FieldTypeDateTime, Store: true, Index: true},
			{Name: "field3", Type: FieldTypeDisabled, Store: true},
		},
	}

	docMapping := BuildDocumentMappingFromConfig(config)

	assert.Contains(t, docMapping.Properties, "field1")
	assert.Contains(t, docMapping.Properties, "field2")
	assert.Contains(t, docMapping.Properties, "field3")
}

func TestBuildIndexMappingFromConfig_SetsDefaultAnalyzer(t *testing.T) {
	config := &SchemaConfig{
		TypeName:        "test",
		DefaultAnalyzer: "custom_analyzer",
		Fields:          []FieldMappingConfig{},
	}

	indexMapping, err := BuildIndexMappingFromConfig(config)

	require.NoError(t, err)
	assert.Equal(t, "custom_analyzer", indexMapping.DefaultAnalyzer)
}

func TestBuildIndexMappingFromConfig_SetsDefaultType(t *testing.T) {
	config := &SchemaConfig{
		TypeName:        "custom_type",
		DefaultAnalyzer: "standard",
		Fields:          []FieldMappingConfig{},
	}

	indexMapping, err := BuildIndexMappingFromConfig(config)

	require.NoError(t, err)
	assert.Equal(t, "custom_type", indexMapping.DefaultType)
}

// =============================================================================
// GetConfigLoader Tests
// =============================================================================

func TestGetConfigLoader_ReturnsGlobalLoader(t *testing.T) {
	loader := GetConfigLoader()

	assert.NotNil(t, loader)
	assert.Equal(t, globalConfigLoader, loader)
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkDefaultSchemaConfig(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = DefaultSchemaConfig()
	}
}

func BenchmarkSchemaConfigLoader_GetConfig(b *testing.B) {
	loader := NewSchemaConfigLoader()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = loader.GetConfig()
	}
}

func BenchmarkBuildDocumentMappingFromConfig(b *testing.B) {
	config := DefaultSchemaConfig()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = BuildDocumentMappingFromConfig(config)
	}
}

func BenchmarkBuildIndexMappingFromConfig(b *testing.B) {
	config := DefaultSchemaConfig()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = BuildIndexMappingFromConfig(config)
	}
}
