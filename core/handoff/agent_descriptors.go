package handoff

import (
	"sync"

	"github.com/adalundhe/sylk/core/llmruntime"
)

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
		{AgentType: "librarian", ModelID: "sonnet-4.5-1m", ContextWindow: contextWindow1M, Category: CategoryKnowledge, RuntimeProfiles: llmruntime.AgentProfiles("librarian"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("librarian")},
		{AgentType: "archivalist", ModelID: "sonnet-4.5-1m", ContextWindow: contextWindow1M, Category: CategoryKnowledge, RuntimeProfiles: llmruntime.AgentProfiles("archivalist"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("archivalist")},
		{AgentType: "academic", ModelID: "opus-4.5-200k", ContextWindow: contextWindow200K, Category: CategoryKnowledge, RuntimeProfiles: llmruntime.AgentProfiles("academic"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("academic")},
		{AgentType: "architect", ModelID: "claude-opus-4-6", ContextWindow: contextWindow200K, Category: CategoryKnowledge, RuntimeProfiles: llmruntime.AgentProfiles("architect"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("architect")},
		{AgentType: "guide", ModelID: "haiku-4.5-200k", ContextWindow: contextWindow200K, Category: CategoryStandalone, RuntimeProfiles: llmruntime.AgentProfiles("guide"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("guide")},
		{AgentType: "orchestrator", ModelID: "gemini-3-flash-preview", ContextWindow: contextWindow1M, Category: CategoryStandalone, RuntimeProfiles: llmruntime.AgentProfiles("orchestrator"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("orchestrator")},
		{AgentType: "engineer", ModelID: "gpt-5.4-pro", ReasoningEffort: "xhigh", ContextWindow: contextWindow272K, Category: CategoryPipeline, RuntimeProfiles: llmruntime.AgentProfiles("engineer"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("engineer")},
		{AgentType: "designer", ModelID: "gemini-3.1-pro-preview", ReasoningEffort: "high", ContextWindow: contextWindow1M, Category: CategoryPipeline, RuntimeProfiles: llmruntime.AgentProfiles("designer"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("designer")},
		{AgentType: "inspector", ModelID: "opus-4.6", ContextWindow: contextWindow200K, Category: CategoryStandalone, RuntimeProfiles: llmruntime.AgentProfiles("inspector"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("inspector")},
		{AgentType: "inspector-pipeline", ModelID: "opus-4.6", ContextWindow: contextWindow200K, Category: CategoryPipeline, RuntimeProfiles: llmruntime.AgentProfiles("inspector-pipeline"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("inspector-pipeline")},
		{AgentType: "tester", ModelID: "gpt-5.4-pro", ReasoningEffort: "xhigh", ContextWindow: contextWindow272K, Category: CategoryStandalone, RuntimeProfiles: llmruntime.AgentProfiles("tester"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("tester")},
		{AgentType: "tester-pipeline", ModelID: "gpt-5.4-pro", ReasoningEffort: "xhigh", ContextWindow: contextWindow272K, Category: CategoryPipeline, RuntimeProfiles: llmruntime.AgentProfiles("tester-pipeline"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("tester-pipeline")},
		{AgentType: "guardian", ModelID: "gpt-5.4-pro", ReasoningEffort: "high", ContextWindow: contextWindow272K, Category: CategoryStandalone, RuntimeProfiles: llmruntime.AgentProfiles("guardian"), DefaultRuntimeProfile: llmruntime.AgentDefaultProfile("guardian")},
	}
}
