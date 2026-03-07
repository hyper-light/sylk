package handoff

import "sync"

// DescriptorRegistry maps agent type strings to their AgentDescriptor.
// It is pre-populated with known agent types and supports runtime registration.
type DescriptorRegistry struct {
	mu          sync.RWMutex
	descriptors map[string]AgentDescriptor
}

// NewDescriptorRegistry creates a registry pre-populated with all known agent types.
func NewDescriptorRegistry() *DescriptorRegistry {
	r := &DescriptorRegistry{
		descriptors: make(map[string]AgentDescriptor, 12),
	}
	for _, desc := range defaultDescriptors() {
		r.descriptors[desc.AgentType] = desc
	}
	return r
}

// Get returns the descriptor for the given agent type, if registered.
func (r *DescriptorRegistry) Get(agentType string) (AgentDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.descriptors[agentType]
	return d, ok
}

// Register adds or replaces a descriptor for the given agent type.
func (r *DescriptorRegistry) Register(desc AgentDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.descriptors[desc.AgentType] = desc
}

// All returns a snapshot of all registered descriptors.
func (r *DescriptorRegistry) All() []AgentDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]AgentDescriptor, 0, len(r.descriptors))
	for _, d := range r.descriptors {
		all = append(all, d)
	}
	return all
}

// Len returns the number of registered descriptors.
func (r *DescriptorRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.descriptors)
}

// contextWindow1M is the context window for models with 1M token support.
const contextWindow1M = 1_000_000

// contextWindow200K is the context window for models with 200K token support.
const contextWindow200K = 200_000

// contextWindow272K is the context window for GPT-5.4 Pro.
const contextWindow272K = 272_000

// defaultDescriptors returns the pre-populated agent descriptors.
func defaultDescriptors() []AgentDescriptor {
	return []AgentDescriptor{
		{AgentType: "librarian", ModelID: "sonnet-4.5-1m", ContextWindow: contextWindow1M, Category: CategoryKnowledge},
		{AgentType: "archivalist", ModelID: "sonnet-4.5-1m", ContextWindow: contextWindow1M, Category: CategoryKnowledge},
		{AgentType: "academic", ModelID: "opus-4.5-200k", ContextWindow: contextWindow200K, Category: CategoryKnowledge},
		{AgentType: "architect", ModelID: "opus-4.5-200k", ContextWindow: contextWindow200K, Category: CategoryKnowledge},
		{AgentType: "guide", ModelID: "haiku-4.5-200k", ContextWindow: contextWindow200K, Category: CategoryStandalone},
		{AgentType: "orchestrator", ModelID: "haiku-4.5-200k", ContextWindow: contextWindow200K, Category: CategoryStandalone},
		{AgentType: "engineer", ModelID: "gpt-5.4-pro", ReasoningEffort: "xhigh", ContextWindow: contextWindow272K, Category: CategoryPipeline},
		{AgentType: "designer", ModelID: "gemini-3.1-pro-preview", ReasoningEffort: "high", ContextWindow: contextWindow1M, Category: CategoryPipeline},
		{AgentType: "inspector", ModelID: "opus-4.6", ContextWindow: contextWindow200K, Category: CategoryStandalone},
		{AgentType: "inspector-pipeline", ModelID: "opus-4.6", ContextWindow: contextWindow200K, Category: CategoryPipeline},
		{AgentType: "tester", ModelID: "gpt-5.4-pro", ReasoningEffort: "xhigh", ContextWindow: contextWindow272K, Category: CategoryStandalone},
		{AgentType: "tester-pipeline", ModelID: "gpt-5.4-pro", ReasoningEffort: "xhigh", ContextWindow: contextWindow272K, Category: CategoryPipeline},
		{AgentType: "guardian", ModelID: "gpt-5.4-pro", ReasoningEffort: "high", ContextWindow: contextWindow272K, Category: CategoryStandalone},
	}
}
