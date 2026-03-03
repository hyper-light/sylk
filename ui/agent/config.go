package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// agentsYAMLKey is the top-level key under which per-agent model configs live.
const agentsYAMLKey = "agents"

// modelYAMLKey is the key within each agent's mapping that stores the model ID.
const modelYAMLKey = "model"

// providerYAMLKey is the key within each agent's mapping that stores the provider.
const providerYAMLKey = "provider"

// AgentConfigEntry stores the provider and model for a single agent.
type AgentConfigEntry struct {
	Provider string // "anthropic", "openai", "google"
	Model    string // e.g. "claude-sonnet-4-6"
}

// AgentModelStore persists per-agent model selections in a YAML config file.
// Thread-safe; caches entries in-memory and writes atomically to disk.
type AgentModelStore struct {
	mu         sync.Mutex
	entries    map[string]AgentConfigEntry // agentType → config entry
	configPath string
}

// NewAgentModelStore loads agent model selections from the given config file.
// Returns a store with an empty map if the file or agents key is missing.
func NewAgentModelStore(configPath string) *AgentModelStore {
	s := &AgentModelStore{
		entries:    make(map[string]AgentConfigEntry),
		configPath: configPath,
	}
	s.loadFromDisk()
	return s
}

// ModelFor returns the cached model ID for agentType, or "" if absent.
func (s *AgentModelStore) ModelFor(agentType string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[agentType].Model
}

// ProviderFor returns the cached provider for agentType, or "" if absent.
func (s *AgentModelStore) ProviderFor(agentType string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[agentType].Provider
}

// EntryFor returns the full config entry for agentType.
func (s *AgentModelStore) EntryFor(agentType string) AgentConfigEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[agentType]
}

// SetEntry updates both the provider and model for agentType and writes to disk.
func (s *AgentModelStore) SetEntry(agentType, provider, modelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[agentType] = AgentConfigEntry{Provider: provider, Model: modelID}
	return s.writeToDisk()
}

// SetModel updates the cached model for agentType, deriving the provider from
// the model ID prefix. Writes to disk atomically.
func (s *AgentModelStore) SetModel(agentType, modelID string) error {
	return s.SetEntry(agentType, deriveProvider(modelID), modelID)
}

// EnsureDefaults adds default entries for any agent types not already present.
// Performs a single batch write. No-op if all defaults are already cached.
func (s *AgentModelStore) EnsureDefaults(defaults map[string]AgentConfigEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dirty := false
	for agentType, entry := range defaults {
		if _, exists := s.entries[agentType]; !exists {
			s.entries[agentType] = entry
			dirty = true
		}
	}
	if !dirty {
		return nil
	}
	return s.writeToDisk()
}

// DeriveProvider returns the provider string for a model ID based on prefix.
// Returns "" for unrecognized prefixes.
func DeriveProvider(modelID string) string {
	return deriveProvider(modelID)
}

// deriveProvider returns the provider string for a model ID based on prefix.
func deriveProvider(modelID string) string {
	switch {
	case strings.HasPrefix(modelID, "claude-"):
		return "anthropic"
	case strings.HasPrefix(modelID, "gemini-"):
		return "google"
	case strings.HasPrefix(modelID, "gpt-"):
		return "openai"
	default:
		return ""
	}
}

// loadFromDisk reads the config file and populates the in-memory cache.
// Silently returns on any error (missing file, parse error, etc.).
// Backward compatible: derives provider from model ID when the provider key is absent.
func (s *AgentModelStore) loadFromDisk() {
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		return
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return
	}

	agentsNode := findAgentMappingValue(&root, agentsYAMLKey)
	if agentsNode == nil {
		return
	}

	// agentsNode is a MappingNode: key=agentType, value=MappingNode{model: id, provider: name}
	if agentsNode.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(agentsNode.Content); i += 2 {
		agentType := agentsNode.Content[i].Value
		agentCfg := agentsNode.Content[i+1]

		var entry AgentConfigEntry
		if modelNode := findAgentMappingValue(agentCfg, modelYAMLKey); modelNode != nil && modelNode.Kind == yaml.ScalarNode {
			entry.Model = modelNode.Value
		}
		if providerNode := findAgentMappingValue(agentCfg, providerYAMLKey); providerNode != nil && providerNode.Kind == yaml.ScalarNode {
			entry.Provider = providerNode.Value
		}
		// Backward compat: derive provider from model ID when absent.
		if entry.Provider == "" && entry.Model != "" {
			entry.Provider = deriveProvider(entry.Model)
		}
		if entry.Model != "" {
			s.entries[agentType] = entry
		}
	}
}

// writeToDisk reads the existing YAML, patches the agents section, and
// writes atomically via temp → fsync → rename.
func (s *AgentModelStore) writeToDisk() error {
	root := s.loadOrCreateRoot()
	mapping := root.Content[0]

	// Find or create the "agents" key.
	agentsNode := findAgentMappingValue(mapping, agentsYAMLKey)
	if agentsNode == nil {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: agentsYAMLKey},
			&yaml.Node{Kind: yaml.MappingNode},
		)
		agentsNode = mapping.Content[len(mapping.Content)-1]
	}

	// Patch each agent's model and provider entries.
	for agentType, entry := range s.entries {
		agentCfg := findAgentMappingValue(agentsNode, agentType)
		if agentCfg == nil {
			agentCfg = &yaml.Node{Kind: yaml.MappingNode}
			agentsNode.Content = append(agentsNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: agentType},
				agentCfg,
			)
		}
		setAgentMappingValue(agentCfg, providerYAMLKey, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: entry.Provider,
		})
		setAgentMappingValue(agentCfg, modelYAMLKey, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: entry.Model,
		})
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("agent config: marshal: %w", err)
	}

	return s.atomicWrite(out)
}

// loadOrCreateRoot reads the existing YAML file into a document node,
// or creates a fresh document→mapping structure.
func (s *AgentModelStore) loadOrCreateRoot() yaml.Node {
	data, _ := os.ReadFile(s.configPath)
	if len(data) > 0 {
		var root yaml.Node
		if err := yaml.Unmarshal(data, &root); err == nil {
			if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
				return root
			}
		}
	}
	root := yaml.Node{Kind: yaml.DocumentNode}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	return root
}

// atomicWrite writes data to configPath via temp → fsync → rename.
func (s *AgentModelStore) atomicWrite(data []byte) error {
	dir := filepath.Dir(s.configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("agent config: mkdir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "config.*.tmp")
	if err != nil {
		return fmt.Errorf("agent config: temp file: %w", err)
	}
	tmpPath := tmp.Name()

	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("agent config: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("agent config: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agent config: close: %w", err)
	}
	if err := os.Rename(tmpPath, s.configPath); err != nil {
		return fmt.Errorf("agent config: rename: %w", err)
	}

	success = true
	return nil
}

// findAgentMappingValue searches a mapping node for the given key.
// Returns the value node or nil.
func findAgentMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
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

// setAgentMappingValue sets or replaces a key-value pair in a mapping node.
func setAgentMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
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
