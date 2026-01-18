package parser

import (
	"strings"
	"testing"
)

// =============================================================================
// ConfigParser Tests
// =============================================================================

func TestConfigParser_Extensions(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	extensions := parser.Extensions()

	expected := []string{".json", ".yaml", ".yml", ".toml"}
	if len(extensions) != len(expected) {
		t.Errorf("expected %d extensions, got %d", len(expected), len(extensions))
	}

	for _, ext := range expected {
		found := false
		for _, got := range extensions {
			if got == ext {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected extension %s not found", ext)
		}
	}
}

func TestConfigParser_EmptyContent(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()

	tests := []struct {
		name string
		path string
	}{
		{"json", "config.json"},
		{"yaml", "config.yaml"},
		{"toml", "config.toml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := parser.Parse([]byte{}, tt.path)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("result should not be nil")
			}
			if len(result.Symbols) != 0 {
				t.Error("empty content should have no symbols")
			}
		})
	}
}

// =============================================================================
// JSON Parser Tests
// =============================================================================

func TestConfigParser_ParseJSON(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `{
	"name": "myapp",
	"version": "1.0.0",
	"debug": true,
	"port": 8080
}`
	result, err := parser.Parse([]byte(content), "config.json")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result.Symbols) < 4 {
		t.Errorf("expected at least 4 keys, got %d", len(result.Symbols))
	}

	// Check for specific keys
	name := findSymbolByName(result.Symbols, "name")
	if name == nil {
		t.Error("'name' key not found")
	} else if name.Signature != "string" {
		t.Errorf("expected string type, got %s", name.Signature)
	}

	debug := findSymbolByName(result.Symbols, "debug")
	if debug == nil {
		t.Error("'debug' key not found")
	} else if debug.Signature != "bool" {
		t.Errorf("expected bool type, got %s", debug.Signature)
	}

	port := findSymbolByName(result.Symbols, "port")
	if port == nil {
		t.Error("'port' key not found")
	} else if port.Signature != "number" {
		t.Errorf("expected number type, got %s", port.Signature)
	}
}

func TestConfigParser_ParseJSONNested(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `{
	"database": {
		"host": "localhost",
		"port": 5432
	},
	"cache": {
		"enabled": true
	}
}`
	result, err := parser.Parse([]byte(content), "config.json")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should include nested keys
	dbHost := findSymbolByName(result.Symbols, "database.host")
	if dbHost == nil {
		t.Error("'database.host' key not found")
	}

	cacheEnabled := findSymbolByName(result.Symbols, "cache.enabled")
	if cacheEnabled == nil {
		t.Error("'cache.enabled' key not found")
	}
}

func TestConfigParser_ParseJSONArray(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `{
	"hosts": ["localhost", "remote"],
	"ports": [8080, 8081]
}`
	result, err := parser.Parse([]byte(content), "config.json")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	hosts := findSymbolByName(result.Symbols, "hosts")
	if hosts == nil {
		t.Error("'hosts' key not found")
	} else if hosts.Signature != "array" {
		t.Errorf("expected array type, got %s", hosts.Signature)
	}
}

func TestConfigParser_ParseJSONMalformed(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `{
	"name": "test",
	"broken":
}`
	result, err := parser.Parse([]byte(content), "config.json")

	// Should not error, but return partial result
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil for malformed JSON")
	}
}

func TestConfigParser_ParseJSONMetadata(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	result, _ := parser.Parse([]byte(`{"key": "value"}`), "config.json")

	if result.Metadata["format"] != "json" {
		t.Errorf("expected format 'json', got %q", result.Metadata["format"])
	}
	if result.Metadata["type"] != "config" {
		t.Errorf("expected type 'config', got %q", result.Metadata["type"])
	}
}

// =============================================================================
// YAML Parser Tests
// =============================================================================

func TestConfigParser_ParseYAML(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `
name: myapp
version: "1.0.0"
debug: true
port: 8080
`
	result, err := parser.Parse([]byte(content), "config.yaml")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result.Symbols) < 4 {
		t.Errorf("expected at least 4 keys, got %d", len(result.Symbols))
	}

	name := findSymbolByName(result.Symbols, "name")
	if name == nil {
		t.Error("'name' key not found")
	}

	version := findSymbolByName(result.Symbols, "version")
	if version == nil {
		t.Error("'version' key not found")
	}
}

func TestConfigParser_ParseYAMLNested(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `
database:
  host: localhost
  port: 5432
cache:
  enabled: true
`
	result, err := parser.Parse([]byte(content), "config.yaml")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	dbHost := findSymbolByName(result.Symbols, "database.host")
	if dbHost == nil {
		t.Error("'database.host' key not found")
	}
}

func TestConfigParser_ParseYAMLComments(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `
# This is a configuration file
name: myapp  # app name
# Another comment
version: "1.0"
`
	result, err := parser.Parse([]byte(content), "config.yaml")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.Comments == "" {
		t.Error("comments should not be empty")
	}
	if !strings.Contains(result.Comments, "configuration file") {
		t.Error("comments should contain 'configuration file'")
	}
}

func TestConfigParser_ParseYAMLMetadata(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"yaml", "config.yaml", "yaml"},
		{"yml", "config.yml", "yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, _ := parser.Parse([]byte("key: value"), tt.path)
			if result.Metadata["format"] != tt.expected {
				t.Errorf("expected format %q, got %q", tt.expected, result.Metadata["format"])
			}
		})
	}
}

// =============================================================================
// TOML Parser Tests
// =============================================================================

func TestConfigParser_ParseTOML(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `
name = "myapp"
version = "1.0.0"
debug = true
port = 8080
`
	result, err := parser.Parse([]byte(content), "config.toml")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(result.Symbols) < 4 {
		t.Errorf("expected at least 4 keys, got %d", len(result.Symbols))
	}

	name := findSymbolByName(result.Symbols, "name")
	if name == nil {
		t.Error("'name' key not found")
	}
}

func TestConfigParser_ParseTOMLTables(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `
[database]
host = "localhost"
port = 5432

[cache]
enabled = true
`
	result, err := parser.Parse([]byte(content), "config.toml")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have table entries
	database := findSymbolByName(result.Symbols, "database")
	if database == nil {
		t.Error("'database' table not found")
	} else if database.Signature != "table" {
		t.Errorf("expected table signature, got %s", database.Signature)
	}

	// Should have nested keys
	dbHost := findSymbolByName(result.Symbols, "database.host")
	if dbHost == nil {
		t.Error("'database.host' key not found")
	}
}

func TestConfigParser_ParseTOMLComments(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `
# Configuration file
name = "myapp"  # application name
# Another comment
`
	result, err := parser.Parse([]byte(content), "config.toml")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.Comments == "" {
		t.Error("comments should not be empty")
	}
	if !strings.Contains(result.Comments, "Configuration file") {
		t.Error("comments should contain 'Configuration file'")
	}
}

func TestConfigParser_ParseTOMLMetadata(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	result, _ := parser.Parse([]byte("key = \"value\""), "config.toml")

	if result.Metadata["format"] != "toml" {
		t.Errorf("expected format 'toml', got %q", result.Metadata["format"])
	}
	if result.Metadata["type"] != "config" {
		t.Errorf("expected type 'config', got %q", result.Metadata["type"])
	}
}

func TestConfigParser_ParseTOMLNestedTables(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()
	content := `
[servers]

[servers.alpha]
ip = "10.0.0.1"

[servers.beta]
ip = "10.0.0.2"
`
	result, err := parser.Parse([]byte(content), "config.toml")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have nested table entries
	alpha := findSymbolByName(result.Symbols, "servers.alpha")
	if alpha == nil {
		t.Error("'servers.alpha' table not found")
	}

	alphaIP := findSymbolByName(result.Symbols, "servers.alpha.ip")
	if alphaIP == nil {
		t.Error("'servers.alpha.ip' key not found")
	}
}

// =============================================================================
// All Symbols Exported Tests
// =============================================================================

func TestConfigParser_AllSymbolsExported(t *testing.T) {
	t.Parallel()

	parser := NewConfigParser()

	tests := []struct {
		name    string
		path    string
		content string
	}{
		{"json", "config.json", `{"key": "value"}`},
		{"yaml", "config.yaml", "key: value"},
		{"toml", "config.toml", `key = "value"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, _ := parser.Parse([]byte(tt.content), tt.path)

			for _, sym := range result.Symbols {
				if !sym.Exported {
					t.Errorf("all config symbols should be exported, %q is not", sym.Name)
				}
			}
		})
	}
}

// =============================================================================
// Helper Type Detection Tests
// =============================================================================

func TestGetValueType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    interface{}
		expected string
	}{
		{"string", "hello", "string"},
		{"bool true", true, "bool"},
		{"bool false", false, "bool"},
		{"float64", 3.14, "number"},
		{"int", 42, "number"},
		{"array", []interface{}{1, 2, 3}, "array"},
		{"object", map[string]interface{}{"key": "val"}, "object"},
		{"nil", nil, "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getValueType(tt.value)
			if got != tt.expected {
				t.Errorf("getValueType(%v) = %q, want %q", tt.value, got, tt.expected)
			}
		})
	}
}
