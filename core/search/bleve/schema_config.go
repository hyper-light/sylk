// Package bleve provides Bleve index configuration and document mappings.
//
// # Schema Configuration
//
// The schema configuration allows customizing Bleve field mappings without code
// changes. Configuration can be loaded from JSON or YAML files.
//
// # Configuration Format
//
// JSON example:
//
//	{
//	  "type_name": "document",
//	  "default_analyzer": "standard",
//	  "dynamic_mapping": false,
//	  "fields": [
//	    {
//	      "name": "content",
//	      "type": "text",
//	      "analyzer": "code",
//	      "store": true,
//	      "index": true,
//	      "include_in_all": true
//	    },
//	    {
//	      "name": "modified_at",
//	      "type": "datetime",
//	      "store": true,
//	      "index": true
//	    },
//	    {
//	      "name": "checksum",
//	      "type": "disabled",
//	      "store": true
//	    }
//	  ]
//	}
//
// YAML example:
//
//	type_name: document
//	default_analyzer: standard
//	dynamic_mapping: false
//	fields:
//	  - name: content
//	    type: text
//	    analyzer: code
//	    store: true
//	    index: true
//	    include_in_all: true
//	  - name: modified_at
//	    type: datetime
//	    store: true
//	    index: true
//
// # Field Types
//
//   - text: Text fields with optional analyzer (default). Supports full-text search.
//   - datetime: Timestamp fields for date/time range queries.
//   - disabled: Stored-only fields (not indexed). Useful for IDs, checksums.
//
// # Available Analyzers
//
//   - keyword: Exact match, no tokenization
//   - standard: Bleve's standard analyzer
//   - code: Custom analyzer for source code (camelCase/snake_case splitting)
//   - symbol: Case-preserving code analyzer for identifiers
//   - comment: Natural language analyzer with stemming
//
// # Loading Configuration
//
// Load from file with automatic fallback to defaults:
//
//	loader := bleve.GetConfigLoader()
//	err := loader.LoadFromFile("/path/to/schema.json")
//
// Load from bytes:
//
//	loader.LoadFromBytes([]byte(configData), "yaml")
//
// Build index with custom configuration:
//
//	config := &bleve.SchemaConfig{...}
//	indexMapping, err := bleve.BuildIndexMappingFromConfig(config)
//
// Reset to defaults:
//
//	loader.Reset()
package bleve

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// =============================================================================
// Configuration Errors
// =============================================================================

var (
	// ErrInvalidFieldType indicates an unknown field type was specified.
	ErrInvalidFieldType = errors.New("invalid field type")

	// ErrEmptyFieldName indicates a field configuration has no name.
	ErrEmptyFieldName = errors.New("field name cannot be empty")

	// ErrInvalidAnalyzer indicates an unknown analyzer was specified.
	ErrInvalidAnalyzer = errors.New("invalid analyzer name")
)

// =============================================================================
// Field Mapping Configuration Types
// =============================================================================

// FieldMappingType represents the type of a field mapping.
type FieldMappingType string

const (
	// FieldTypeText is for text fields with analyzers.
	FieldTypeText FieldMappingType = "text"

	// FieldTypeDateTime is for timestamp fields.
	FieldTypeDateTime FieldMappingType = "datetime"

	// FieldTypeDisabled is for stored-only fields (not indexed).
	FieldTypeDisabled FieldMappingType = "disabled"
)

// FieldMappingConfig defines the configuration for a single field mapping.
type FieldMappingConfig struct {
	// Name is the field name in the document.
	Name string `json:"name" yaml:"name"`

	// Type specifies the field type (text, datetime, disabled).
	Type FieldMappingType `json:"type" yaml:"type"`

	// Analyzer is the analyzer to use for text fields.
	Analyzer string `json:"analyzer,omitempty" yaml:"analyzer,omitempty"`

	// Store indicates whether to store the field value.
	Store bool `json:"store" yaml:"store"`

	// Index indicates whether to index the field for searching.
	Index bool `json:"index" yaml:"index"`

	// IncludeInAll indicates whether to include in the _all field.
	IncludeInAll bool `json:"include_in_all" yaml:"include_in_all"`
}

// Validate checks if the field configuration is valid.
func (fc *FieldMappingConfig) Validate() error {
	if fc.Name == "" {
		return ErrEmptyFieldName
	}

	switch fc.Type {
	case FieldTypeText, FieldTypeDateTime, FieldTypeDisabled:
		// Valid types
	case "":
		return fmt.Errorf("%w: type is required for field %q", ErrInvalidFieldType, fc.Name)
	default:
		return fmt.Errorf("%w: %q for field %q", ErrInvalidFieldType, fc.Type, fc.Name)
	}

	return nil
}

// =============================================================================
// Schema Configuration
// =============================================================================

// SchemaConfig defines the complete schema configuration for the Bleve index.
type SchemaConfig struct {
	// TypeName is the document type name in the index.
	TypeName string `json:"type_name" yaml:"type_name"`

	// DefaultAnalyzer is the fallback analyzer for unmapped fields.
	DefaultAnalyzer string `json:"default_analyzer" yaml:"default_analyzer"`

	// DynamicMapping controls whether unmapped fields are indexed.
	DynamicMapping bool `json:"dynamic_mapping" yaml:"dynamic_mapping"`

	// Fields contains the field mapping configurations.
	Fields []FieldMappingConfig `json:"fields" yaml:"fields"`
}

// Validate checks if the schema configuration is valid.
func (sc *SchemaConfig) Validate() error {
	if sc.TypeName == "" {
		return fmt.Errorf("type_name is required")
	}

	if sc.DefaultAnalyzer == "" {
		return fmt.Errorf("default_analyzer is required")
	}

	for i := range sc.Fields {
		if err := sc.Fields[i].Validate(); err != nil {
			return fmt.Errorf("field %d: %w", i, err)
		}
	}

	return nil
}

// GetField returns the field configuration by name, or nil if not found.
func (sc *SchemaConfig) GetField(name string) *FieldMappingConfig {
	for i := range sc.Fields {
		if sc.Fields[i].Name == name {
			return &sc.Fields[i]
		}
	}
	return nil
}

// =============================================================================
// Default Configuration
// =============================================================================

// DefaultSchemaConfig returns the default schema configuration.
// This provides sensible defaults for all document fields.
func DefaultSchemaConfig() *SchemaConfig {
	return &SchemaConfig{
		TypeName:        DefaultTypeName,
		DefaultAnalyzer: DefaultAnalyzerName,
		DynamicMapping:  false,
		Fields:          defaultFieldMappingConfigs(),
	}
}

// defaultFieldMappingConfigs returns the default field configurations.
func defaultFieldMappingConfigs() []FieldMappingConfig {
	return []FieldMappingConfig{
		{Name: "id", Type: FieldTypeDisabled, Store: true, Index: false, IncludeInAll: false},
		{Name: "path", Type: FieldTypeText, Analyzer: KeywordAnalyzerName, Store: true, Index: true, IncludeInAll: false},
		{Name: "type", Type: FieldTypeText, Analyzer: KeywordAnalyzerName, Store: true, Index: true, IncludeInAll: false},
		{Name: "language", Type: FieldTypeText, Analyzer: KeywordAnalyzerName, Store: true, Index: true, IncludeInAll: false},
		{Name: "content", Type: FieldTypeText, Analyzer: "code", Store: true, Index: true, IncludeInAll: true},
		{Name: "symbols", Type: FieldTypeText, Analyzer: "symbol", Store: true, Index: true, IncludeInAll: true},
		{Name: "comments", Type: FieldTypeText, Analyzer: "comment", Store: true, Index: true, IncludeInAll: true},
		{Name: "imports", Type: FieldTypeText, Analyzer: KeywordAnalyzerName, Store: true, Index: true, IncludeInAll: false},
		{Name: "checksum", Type: FieldTypeDisabled, Store: true, Index: false, IncludeInAll: false},
		{Name: "modified_at", Type: FieldTypeDateTime, Store: true, Index: true, IncludeInAll: false},
		{Name: "indexed_at", Type: FieldTypeDateTime, Store: true, Index: true, IncludeInAll: false},
		{Name: "git_commit", Type: FieldTypeText, Analyzer: KeywordAnalyzerName, Store: true, Index: true, IncludeInAll: false},
		{Name: DomainFieldName, Type: FieldTypeText, Analyzer: KeywordAnalyzerName, Store: true, Index: true, IncludeInAll: false},
	}
}

// =============================================================================
// Configuration Loading
// =============================================================================

// SchemaConfigLoader handles loading and caching of schema configurations.
type SchemaConfigLoader struct {
	mu     sync.RWMutex
	config *SchemaConfig
}

// NewSchemaConfigLoader creates a new configuration loader with default config.
func NewSchemaConfigLoader() *SchemaConfigLoader {
	return &SchemaConfigLoader{
		config: DefaultSchemaConfig(),
	}
}

// LoadFromFile loads configuration from a JSON or YAML file.
// Falls back to defaults if the file doesn't exist.
func (l *SchemaConfigLoader) LoadFromFile(path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			l.config = DefaultSchemaConfig()
			return nil
		}
		return fmt.Errorf("failed to read config file: %w", err)
	}

	return l.parseConfig(data, path)
}

// parseConfig parses configuration data based on file extension.
func (l *SchemaConfigLoader) parseConfig(data []byte, path string) error {
	var config SchemaConfig

	if isYAMLFile(path) {
		if err := yaml.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse YAML config: %w", err)
		}
	} else {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse JSON config: %w", err)
		}
	}

	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	l.config = mergeWithDefaults(&config)
	return nil
}

// isYAMLFile checks if the path has a YAML extension.
func isYAMLFile(path string) bool {
	ext := getExtension(path)
	return ext == ".yaml" || ext == ".yml"
}

// getExtension returns the file extension from a path.
func getExtension(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' || path[i] == '\\' {
			break
		}
	}
	return ""
}

// mergeWithDefaults merges user config with defaults for missing fields.
func mergeWithDefaults(userConfig *SchemaConfig) *SchemaConfig {
	defaults := DefaultSchemaConfig()

	if userConfig.TypeName == "" {
		userConfig.TypeName = defaults.TypeName
	}
	if userConfig.DefaultAnalyzer == "" {
		userConfig.DefaultAnalyzer = defaults.DefaultAnalyzer
	}

	// Add missing default fields
	existingFields := make(map[string]bool)
	for _, f := range userConfig.Fields {
		existingFields[f.Name] = true
	}

	for _, defaultField := range defaults.Fields {
		if !existingFields[defaultField.Name] {
			userConfig.Fields = append(userConfig.Fields, defaultField)
		}
	}

	return userConfig
}

// LoadFromBytes loads configuration from JSON or YAML bytes.
// The format parameter should be "json" or "yaml".
func (l *SchemaConfigLoader) LoadFromBytes(data []byte, format string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var config SchemaConfig

	switch format {
	case "yaml", "yml":
		if err := yaml.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse YAML config: %w", err)
		}
	case "json", "":
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse JSON config: %w", err)
		}
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}

	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	l.config = mergeWithDefaults(&config)
	return nil
}

// GetConfig returns the current schema configuration.
func (l *SchemaConfigLoader) GetConfig() *SchemaConfig {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.config
}

// SetConfig sets the schema configuration directly.
func (l *SchemaConfigLoader) SetConfig(config *SchemaConfig) error {
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.config = config
	return nil
}

// Reset resets the configuration to defaults.
func (l *SchemaConfigLoader) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.config = DefaultSchemaConfig()
}
