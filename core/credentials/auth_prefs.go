package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/core/storage"
	"gopkg.in/yaml.v3"
)

const (
	authPrefsFile = "auth_prefs.yaml"
	authYAMLKey   = "auth"
	methodYAMLKey = "method"
)

type authPrefsData struct {
	Providers map[string]string `yaml:"providers"`
}

// LoadAuthPref returns the stored auth mode for a provider (e.g. "oauth",
// "api_key"). Project config (.sylk/config.yaml) is the source of truth.
// Legacy auth_prefs.yaml is only used as a backward-compatible fallback.
func LoadAuthPref(provider string) string {
	provider = normalizeAuthProvider(provider)
	if provider == "" {
		return ""
	}
	if value := loadAuthPrefFromProjectConfig(provider); value != "" {
		return value
	}
	return loadLegacyAuthPref(provider)
}

// SaveAuthPref persists the auth mode preference for a provider into the
// project's .sylk/config.yaml, preserving other config keys. If no project
// config can be resolved, falls back to the legacy auth_prefs file.
func SaveAuthPref(provider, authMode string) error {
	provider = normalizeAuthProvider(provider)
	authMode = CanonicalAuthMethod(provider, authMode)
	if provider == "" || authMode == "" {
		return nil
	}

	if configPath := resolveProjectConfigPath(); configPath != "" {
		return saveAuthPrefToProjectConfig(configPath, provider, authMode)
	}
	return saveLegacyAuthPref(provider, authMode)
}

func normalizeAuthProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func resolveProjectConfigPath() string {
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return ""
	}

	root := cwd
	if gitRoot := findNearestGitRoot(cwd); gitRoot != "" {
		root = gitRoot
	} else if sylkRoot := findNearestSylkRoot(cwd); sylkRoot != "" {
		root = sylkRoot
	}

	return storage.ResolveProjectDirs(root).Config
}

func findNearestSylkRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, ".sylk")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func findNearestGitRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func loadAuthPrefFromProjectConfig(provider string) string {
	path := resolveProjectConfigPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return ""
	}
	doc := documentMappingNode(&root)
	if doc == nil {
		return ""
	}
	authNode := findMappingValue(doc, authYAMLKey)
	if authNode == nil || authNode.Kind != yaml.MappingNode {
		return ""
	}
	providerNode := findMappingValue(authNode, provider)
	if providerNode == nil {
		return ""
	}
	if providerNode.Kind == yaml.ScalarNode {
		return CanonicalAuthMethod(provider, providerNode.Value)
	}
	if providerNode.Kind != yaml.MappingNode {
		return ""
	}
	methodNode := findMappingValue(providerNode, methodYAMLKey)
	if methodNode == nil || methodNode.Kind != yaml.ScalarNode {
		return ""
	}
	return CanonicalAuthMethod(provider, methodNode.Value)
}

func saveAuthPrefToProjectConfig(path, provider, authMode string) error {
	root := loadOrCreateYAMLRoot(path)
	doc := documentMappingNode(&root)
	if doc == nil {
		return fmt.Errorf("auth config: invalid yaml root")
	}

	authNode := findMappingValue(doc, authYAMLKey)
	if authNode == nil {
		authNode = &yaml.Node{Kind: yaml.MappingNode}
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: authYAMLKey},
			authNode,
		)
	}
	if authNode.Kind != yaml.MappingNode {
		authNode.Kind = yaml.MappingNode
		authNode.Content = nil
	}

	providerNode := findMappingValue(authNode, provider)
	if providerNode == nil {
		providerNode = &yaml.Node{Kind: yaml.MappingNode}
		authNode.Content = append(authNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: provider},
			providerNode,
		)
	}
	if providerNode.Kind != yaml.MappingNode {
		providerNode.Kind = yaml.MappingNode
		providerNode.Content = nil
	}

	setMappingValue(providerNode, methodYAMLKey, &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: authMode,
	})

	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("auth config: marshal: %w", err)
	}
	return atomicWriteYAML(path, out)
}

func resolveLegacyPrefsPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "sylk", authPrefsFile)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "sylk", authPrefsFile)
}

func loadLegacyAuthPref(provider string) string {
	path := resolveLegacyPrefsPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var prefs authPrefsData
	if yaml.Unmarshal(data, &prefs) != nil || prefs.Providers == nil {
		return ""
	}
	return CanonicalAuthMethod(provider, prefs.Providers[provider])
}

func saveLegacyAuthPref(provider, authMode string) error {
	path := resolveLegacyPrefsPath()
	if path == "" {
		return nil
	}
	var prefs authPrefsData
	if existing, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(existing, &prefs)
	}
	if prefs.Providers == nil {
		prefs.Providers = make(map[string]string)
	}
	prefs.Providers[provider] = authMode

	out, err := yaml.Marshal(&prefs)
	if err != nil {
		return err
	}
	return atomicWriteYAML(path, out)
}

func loadOrCreateYAMLRoot(path string) yaml.Node {
	data, _ := os.ReadFile(path)
	if len(data) > 0 {
		var root yaml.Node
		if err := yaml.Unmarshal(data, &root); err == nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
			return root
		}
	}
	root := yaml.Node{Kind: yaml.DocumentNode}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	return root
}

func documentMappingNode(root *yaml.Node) *yaml.Node {
	if root == nil {
		return nil
	}
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
		}
		if root.Content[0].Kind != yaml.MappingNode {
			root.Content[0] = &yaml.Node{Kind: yaml.MappingNode}
		}
		return root.Content[0]
	}
	if root.Kind == yaml.MappingNode {
		return root
	}
	return nil
}

func findMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
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

func atomicWriteYAML(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "config.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	success = true
	return nil
}
