package gitpanel

import (
	"os"

	"gopkg.in/yaml.v3"
)

// GitPanelConfig holds the persisted column configuration for all tabs.
type GitPanelConfig struct {
	Commits     *TabColumnConfig `yaml:"commits,omitempty"`
	Branches    *TabColumnConfig `yaml:"branches,omitempty"`
	Tags        *TabColumnConfig `yaml:"tags,omitempty"`
	Uncommitted *TabColumnConfig `yaml:"uncommitted,omitempty"`
}

// TabColumnConfig holds column order, visibility, and sort for one tab.
type TabColumnConfig struct {
	Columns []ColumnEntry `yaml:"columns"`
	Sort    []SortEntry   `yaml:"sort,omitempty"`
	// Legacy single-column sort (read-only for backward compatibility).
	SortCol string `yaml:"sort_col,omitempty"`
	SortDir string `yaml:"sort_dir,omitempty"`
}

// SortEntry holds a single sort key in a multi-column sort specification.
type SortEntry struct {
	Col string `yaml:"col"`
	Dir string `yaml:"dir"`
}

// ColumnEntry holds the persisted state for a single column.
type ColumnEntry struct {
	ID      string `yaml:"id"`
	Visible bool   `yaml:"visible"`
	Width   int    `yaml:"width,omitempty"` // user-resized width; 0 = auto
}

// configYAMLKey is the top-level YAML key path for git panel config.
const configUIKey = "ui"
const configGitPanelKey = "git_panel"

// LoadColumnConfig reads the git panel column config from the given YAML file.
// Returns an empty config (not nil) if the file doesn't exist or the key is
// absent.
func LoadColumnConfig(path string) *GitPanelConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return &GitPanelConfig{}
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return &GitPanelConfig{}
	}

	uiNode := findMappingValue(&root, configUIKey)
	if uiNode == nil {
		return &GitPanelConfig{}
	}

	gpNode := findMappingValue(uiNode, configGitPanelKey)
	if gpNode == nil {
		return &GitPanelConfig{}
	}

	var cfg GitPanelConfig
	if err := gpNode.Decode(&cfg); err != nil {
		return &GitPanelConfig{}
	}
	return &cfg
}

// SaveColumnConfig writes the git panel column config to the given YAML file,
// preserving all other keys and comments in the file.
func SaveColumnConfig(path string, cfg *GitPanelConfig) error {
	// Read existing file (may not exist).
	data, _ := os.ReadFile(path)

	var root yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			// Corrupted file — start fresh with a document node.
			root = yaml.Node{Kind: yaml.DocumentNode}
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
		}
	} else {
		root = yaml.Node{Kind: yaml.DocumentNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	}

	// Ensure document node wraps a mapping.
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		// Work with the mapping inside the document.
	} else {
		root = yaml.Node{Kind: yaml.DocumentNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	}

	mapping := root.Content[0]

	// Find or create the "ui" key.
	uiNode := findMappingValue(mapping, configUIKey)
	if uiNode == nil {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: configUIKey},
			&yaml.Node{Kind: yaml.MappingNode},
		)
		uiNode = mapping.Content[len(mapping.Content)-1]
	}

	// Encode the git_panel config as a YAML node.
	var gpEncoded yaml.Node
	if err := gpEncoded.Encode(cfg); err != nil {
		return err
	}

	// Set or replace the "git_panel" key under "ui".
	setMappingValue(uiNode, configGitPanelKey, &gpEncoded)

	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, 0644)
}

// findMappingValue searches a mapping node for the given key and returns the
// value node, or nil if not found.
func findMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	// Unwrap document node.
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// setMappingValue sets or replaces a key-value pair in a mapping node.
func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	if mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		value,
	)
}
