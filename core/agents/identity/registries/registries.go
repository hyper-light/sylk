// Package registries provides adapter implementations of
// identity.ModelRegistry and identity.PodRegistry backed by the
// canonical sources already used elsewhere in the runtime:
//
//   - handoff.DescriptorRegistry is the authoritative source for
//     agent-type → model / window / category mapping.
//   - A static map captures the pod-id → PodType registration wired
//     in cmd/tui.go.
//
// These adapters deliberately live in a subpackage so that the core
// identity package itself remains free of any import into handoff,
// llmruntime, providers, or container/pod — that kept identity at
// the bottom of the dependency graph and broke the cycle that would
// otherwise form via providers → identity → pod → container →
// network → events → identity.
package registries

import (
	"sync"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/handoff"
)

// HandoffModelRegistry implements identity.ModelRegistry against a
// handoff.DescriptorRegistry. Model overrides (e.g. when the user
// swaps a model mid-session) are recorded in an in-memory overlay;
// descriptor lookups go to the registry first and fall back to the
// overlay when descriptors don't exist yet.
type HandoffModelRegistry struct {
	source *handoff.DescriptorRegistry

	mu        sync.RWMutex
	overrides map[identity.AgentType][]identity.ModelID
	windows   map[modelKey]identity.ContextWindow
}

type modelKey struct {
	kind  identity.AgentType
	model identity.ModelID
}

// NewHandoffModelRegistry builds an adapter against source. Panics
// when source is nil — the adapter has no operating mode otherwise.
func NewHandoffModelRegistry(source *handoff.DescriptorRegistry) *HandoffModelRegistry {
	if source == nil {
		panic("registries: nil handoff.DescriptorRegistry")
	}
	return &HandoffModelRegistry{
		source:    source,
		overrides: make(map[identity.AgentType][]identity.ModelID),
		windows:   make(map[modelKey]identity.ContextWindow),
	}
}

// AllowedModels implements identity.ModelRegistry. Returns the
// descriptor's ModelID as the default plus any overrides recorded via
// RegisterOverride.
func (r *HandoffModelRegistry) AllowedModels(kind identity.AgentType) []identity.ModelID {
	out := make([]identity.ModelID, 0, 2)
	if desc, ok := r.source.Get(kind.String()); ok && desc.ModelID != "" {
		out = append(out, identity.ModelID(desc.ModelID))
	}
	r.mu.RLock()
	if extras, ok := r.overrides[kind]; ok {
		out = append(out, extras...)
	}
	r.mu.RUnlock()
	return out
}

// Category implements identity.ModelRegistry.
func (r *HandoffModelRegistry) Category(kind identity.AgentType) (identity.Category, bool) {
	if kind == identity.AgentTypeSystem {
		return identity.CategorySystem, true
	}
	desc, ok := r.source.Get(kind.String())
	if !ok {
		return identity.CategoryUnspecified, false
	}
	return mapCategory(desc.Category), true
}

// ContextWindow implements identity.ModelRegistry.
func (r *HandoffModelRegistry) ContextWindow(kind identity.AgentType, model identity.ModelID) identity.ContextWindow {
	r.mu.RLock()
	if w, ok := r.windows[modelKey{kind, model}]; ok {
		r.mu.RUnlock()
		return w
	}
	r.mu.RUnlock()
	if desc, ok := r.source.Get(kind.String()); ok && identity.ModelID(desc.ModelID) == model {
		return identity.ContextWindow(desc.ContextWindow)
	}
	return 0
}

// RegisterOverride appends an additional model to the allowed set for
// kind and records its context window. Used when agents switch
// models at runtime (SwapModel) to a value the descriptor registry
// does not yet know about.
func (r *HandoffModelRegistry) RegisterOverride(kind identity.AgentType, model identity.ModelID, window identity.ContextWindow) {
	r.mu.Lock()
	r.overrides[kind] = append(r.overrides[kind], model)
	r.windows[modelKey{kind, model}] = window
	r.mu.Unlock()
}

// mapCategory converts handoff.AgentCategory to identity.Category.
// Both enums mirror each other but must be converted explicitly —
// identity cannot import handoff without forming an import cycle.
func mapCategory(c handoff.AgentCategory) identity.Category {
	switch c {
	case handoff.CategoryKnowledge:
		return identity.CategoryKnowledge
	case handoff.CategoryStandalone:
		return identity.CategoryStandalone
	case handoff.CategoryPipeline:
		return identity.CategoryPipeline
	default:
		return identity.CategoryUnspecified
	}
}

// StaticPodRegistry is an identity.PodRegistry backed by a fixed map
// captured at session construction time. Pod registration changes
// infrequently (only at bootstrap); dynamic reconfiguration is not
// required.
type StaticPodRegistry struct {
	pods     map[identity.PodID]identity.PodType
	pipeline identity.PodID
}

// NewStaticPodRegistry builds a registry from a pod-id → pod-type map
// and the designated pipeline pod id. Panics on nil map.
func NewStaticPodRegistry(pods map[identity.PodID]identity.PodType, pipelinePodID identity.PodID) *StaticPodRegistry {
	if pods == nil {
		panic("registries: nil pod map")
	}
	// Defensive copy so callers can't mutate after construction.
	cp := make(map[identity.PodID]identity.PodType, len(pods))
	for k, v := range pods {
		cp[k] = v
	}
	return &StaticPodRegistry{pods: cp, pipeline: pipelinePodID}
}

// Lookup implements identity.PodRegistry.
func (r *StaticPodRegistry) Lookup(id identity.PodID) (identity.PodType, bool) {
	t, ok := r.pods[id]
	return t, ok
}

// PipelinePodID implements identity.PodRegistry.
func (r *StaticPodRegistry) PipelinePodID() identity.PodID { return r.pipeline }

// Compile-time interface checks.
var (
	_ identity.ModelRegistry = (*HandoffModelRegistry)(nil)
	_ identity.PodRegistry   = (*StaticPodRegistry)(nil)
)
