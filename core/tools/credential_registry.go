package tools

import (
	"errors"
	"slices"
	"sync"
)

var ErrToolNotFound = errors.New("tool not found")

type ToolCredentialMetadata struct {
	Name                string
	RequiredCredentials []string
}

type ToolCredentialRegistry struct {
	mu    sync.RWMutex
	tools map[string]*ToolCredentialMetadata
}

var defaultRegistry = NewToolCredentialRegistry()

func NewToolCredentialRegistry() *ToolCredentialRegistry {
	r := &ToolCredentialRegistry{
		tools: make(map[string]*ToolCredentialMetadata),
	}
	r.loadDefaults()
	return r
}

func (r *ToolCredentialRegistry) loadDefaults() {
	defaults := defaultToolCredentials()
	for _, meta := range defaults {
		r.tools[meta.Name] = meta
	}
}

func defaultToolCredentials() []*ToolCredentialMetadata {
	return []*ToolCredentialMetadata{
		{Name: "generate_embeddings", RequiredCredentials: []string{"openai"}},
		{Name: "chat_completion", RequiredCredentials: []string{"openai", "anthropic"}},
		{Name: "create_pr", RequiredCredentials: []string{"github"}},
		{Name: "create_issue", RequiredCredentials: []string{"github"}},
		{Name: "list_prs", RequiredCredentials: []string{"github"}},
		{Name: "merge_pr", RequiredCredentials: []string{"github"}},
		{Name: "web_search", RequiredCredentials: []string{"serpapi"}},
		{Name: "fetch_url", RequiredCredentials: []string{}},
		{Name: "read_file", RequiredCredentials: []string{}},
		{Name: "write_file", RequiredCredentials: []string{}},
		{Name: "list_directory", RequiredCredentials: []string{}},
		{Name: "execute_command", RequiredCredentials: []string{}},
		{Name: "grep_search", RequiredCredentials: []string{}},
		{Name: "find_files", RequiredCredentials: []string{}},
		{Name: "git_status", RequiredCredentials: []string{}},
		{Name: "git_diff", RequiredCredentials: []string{}},
		{Name: "git_commit", RequiredCredentials: []string{}},
		{Name: "ask_user", RequiredCredentials: []string{}},
		{Name: "think", RequiredCredentials: []string{}},
	}
}

func (r *ToolCredentialRegistry) Register(meta *ToolCredentialMetadata) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[meta.Name] = meta
}

func (r *ToolCredentialRegistry) GetMetadata(toolName string) *ToolCredentialMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[toolName]
}

func (r *ToolCredentialRegistry) GetRequiredCredentials(toolName string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if meta, ok := r.tools[toolName]; ok {
		result := make([]string, len(meta.RequiredCredentials))
		copy(result, meta.RequiredCredentials)
		return result
	}
	return nil
}

func (r *ToolCredentialRegistry) RequiresCredentials(toolName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if meta, ok := r.tools[toolName]; ok {
		return len(meta.RequiredCredentials) > 0
	}
	return false
}

func (r *ToolCredentialRegistry) ListTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var names []string
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

func (r *ToolCredentialRegistry) ListToolsRequiringCredential(provider string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var tools []string
	for name, meta := range r.tools {
		if slices.Contains(meta.RequiredCredentials, provider) {
			tools = append(tools, name)
		}
	}
	return tools
}

func GetToolCredentialMetadata(toolName string) *ToolCredentialMetadata {
	return defaultRegistry.GetMetadata(toolName)
}

func GetRequiredCredentials(toolName string) []string {
	return defaultRegistry.GetRequiredCredentials(toolName)
}

func RequiresCredentials(toolName string) bool {
	return defaultRegistry.RequiresCredentials(toolName)
}

func RegisterToolCredentials(meta *ToolCredentialMetadata) {
	defaultRegistry.Register(meta)
}
