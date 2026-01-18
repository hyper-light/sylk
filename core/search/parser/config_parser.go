package parser

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigParser extracts top-level keys from JSON, YAML, and TOML config files.
type ConfigParser struct {
	tomlKeyPattern  *regexp.Regexp
	tomlTablePattern *regexp.Regexp
	commentPatterns map[string]*regexp.Regexp
}

// NewConfigParser creates a new config file parser instance.
func NewConfigParser() *ConfigParser {
	return &ConfigParser{
		tomlKeyPattern:   regexp.MustCompile(`^[\t ]*(\w+)\s*=`),
		tomlTablePattern: regexp.MustCompile(`^\[([^\]]+)\]`),
		commentPatterns: map[string]*regexp.Regexp{
			".toml": regexp.MustCompile(`#(.*)$`),
			".yaml": regexp.MustCompile(`#(.*)$`),
			".yml":  regexp.MustCompile(`#(.*)$`),
		},
	}
}

// Extensions returns the file extensions handled by this parser.
func (p *ConfigParser) Extensions() []string {
	return []string{".json", ".yaml", ".yml", ".toml"}
}

// Parse extracts keys and structure from config files.
func (p *ConfigParser) Parse(content []byte, path string) (*ParseResult, error) {
	if len(content) == 0 {
		return NewParseResult(), nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	result := NewParseResult()

	switch ext {
	case ".json":
		p.parseJSON(content, result)
	case ".yaml", ".yml":
		p.parseYAML(content, result)
	case ".toml":
		p.parseTOML(content, result)
	}

	p.setMetadata(ext, result)
	return result, nil
}

// parseJSON extracts keys from JSON content.
func (p *ConfigParser) parseJSON(content []byte, result *ParseResult) {
	var data map[string]interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		// Try to parse as array or invalid JSON
		p.parseJSONPartial(content, result)
		return
	}

	p.extractJSONKeys(data, "", 1, result)
}

// parseJSONPartial attempts partial extraction from malformed JSON.
func (p *ConfigParser) parseJSONPartial(content []byte, result *ParseResult) {
	// Simple regex-based fallback for malformed JSON
	keyPattern := regexp.MustCompile(`"(\w+)"\s*:`)
	lines := strings.Split(string(content), "\n")

	for i, line := range lines {
		matches := keyPattern.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			if len(match) > 1 {
				result.Symbols = append(result.Symbols, Symbol{
					Name:     match[1],
					Kind:     KindKey,
					Line:     i + 1,
					Exported: true,
				})
			}
		}
	}
}

// extractJSONKeys recursively extracts keys from a JSON object.
func (p *ConfigParser) extractJSONKeys(data map[string]interface{}, prefix string, line int, result *ParseResult) {
	for key, value := range data {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		result.Symbols = append(result.Symbols, Symbol{
			Name:      fullKey,
			Kind:      KindKey,
			Signature: getValueType(value),
			Line:      line,
			Exported:  true,
		})

		// Recurse into nested objects (limit depth to avoid complexity)
		if nested, ok := value.(map[string]interface{}); ok && len(prefix) < 50 {
			p.extractJSONKeys(nested, fullKey, line, result)
		}
	}
}

// parseYAML extracts keys from YAML content.
func (p *ConfigParser) parseYAML(content []byte, result *ParseResult) {
	var data map[string]interface{}
	if err := yaml.Unmarshal(content, &data); err != nil {
		// Fall back to line-based extraction
		p.parseYAMLPartial(content, result)
		return
	}

	p.extractYAMLKeys(data, "", 1, result)
	p.extractYAMLComments(content, result)
}

// parseYAMLPartial extracts keys using simple line parsing.
func (p *ConfigParser) parseYAMLPartial(content []byte, result *ParseResult) {
	keyPattern := regexp.MustCompile(`^[\t ]*(\w+)\s*:`)
	lines := strings.Split(string(content), "\n")

	for i, line := range lines {
		match := keyPattern.FindStringSubmatch(line)
		if len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{
				Name:     match[1],
				Kind:     KindKey,
				Line:     i + 1,
				Exported: true,
			})
		}
	}
}

// extractYAMLKeys recursively extracts keys from a YAML map.
func (p *ConfigParser) extractYAMLKeys(data map[string]interface{}, prefix string, line int, result *ParseResult) {
	for key, value := range data {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		result.Symbols = append(result.Symbols, Symbol{
			Name:      fullKey,
			Kind:      KindKey,
			Signature: getValueType(value),
			Line:      line,
			Exported:  true,
		})

		// Recurse into nested maps (limit depth to avoid complexity)
		if nested, ok := value.(map[string]interface{}); ok && len(prefix) < 50 {
			p.extractYAMLKeys(nested, fullKey, line, result)
		}
	}
}

// extractYAMLComments extracts # comments from YAML.
func (p *ConfigParser) extractYAMLComments(content []byte, result *ParseResult) {
	commentPat := p.commentPatterns[".yaml"]
	lines := strings.Split(string(content), "\n")
	var comments []string

	for _, line := range lines {
		match := commentPat.FindStringSubmatch(line)
		if len(match) > 1 {
			comment := strings.TrimSpace(match[1])
			if comment != "" {
				comments = append(comments, comment)
			}
		}
	}

	result.Comments = strings.Join(comments, "\n")
}

// parseTOML extracts keys and tables from TOML content.
func (p *ConfigParser) parseTOML(content []byte, result *ParseResult) {
	lines := strings.Split(string(content), "\n")
	currentTable := ""
	var comments []string

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Skip empty lines
		if trimmed == "" {
			continue
		}

		// Extract comments
		if commentMatch := p.commentPatterns[".toml"].FindStringSubmatch(line); len(commentMatch) > 1 {
			comment := strings.TrimSpace(commentMatch[1])
			if comment != "" {
				comments = append(comments, comment)
			}
		}

		// Extract table headers
		if tableMatch := p.tomlTablePattern.FindStringSubmatch(trimmed); len(tableMatch) > 1 {
			currentTable = tableMatch[1]
			p.addTOMLTable(currentTable, lineNum, result)
			continue
		}

		// Extract key-value pairs
		if keyMatch := p.tomlKeyPattern.FindStringSubmatch(trimmed); len(keyMatch) > 1 {
			p.addTOMLKey(keyMatch[1], currentTable, lineNum, result)
		}
	}

	result.Comments = strings.Join(comments, "\n")
}

// addTOMLTable adds a table section as a symbol.
func (p *ConfigParser) addTOMLTable(name string, lineNum int, result *ParseResult) {
	result.Symbols = append(result.Symbols, Symbol{
		Name:      name,
		Kind:      KindKey,
		Signature: "table",
		Line:      lineNum,
		Exported:  true,
	})
}

// addTOMLKey adds a key as a symbol.
func (p *ConfigParser) addTOMLKey(key, table string, lineNum int, result *ParseResult) {
	fullKey := key
	if table != "" {
		fullKey = table + "." + key
	}

	result.Symbols = append(result.Symbols, Symbol{
		Name:     fullKey,
		Kind:     KindKey,
		Line:     lineNum,
		Exported: true,
	})
}

// setMetadata sets config-specific metadata.
func (p *ConfigParser) setMetadata(ext string, result *ParseResult) {
	switch ext {
	case ".json":
		result.Metadata["format"] = "json"
	case ".yaml", ".yml":
		result.Metadata["format"] = "yaml"
	case ".toml":
		result.Metadata["format"] = "toml"
	}
	result.Metadata["type"] = "config"
}

// =============================================================================
// Helper Functions
// =============================================================================

// getValueType returns a string describing the type of a value.
func getValueType(value interface{}) string {
	switch v := value.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case float64:
		return "number"
	case int:
		return "number"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	case nil:
		return "null"
	default:
		_ = v // satisfy unused variable check
		return "unknown"
	}
}

// init registers the Config parser in the default registry.
func init() {
	RegisterParser(NewConfigParser())
}
