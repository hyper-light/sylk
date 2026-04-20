package fabric

import (
	"strings"

	coreskills "github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// AppendFabricAwarenessToolPolicies appends ToolPolicy entries for the
// uniform fabric awareness skills (query_peer_activity, causal_trace,
// find_related_activity, inspect_open_conflicts) plus recall_my_history.
// Mirrors AppendMemoryForestToolPolicies — used by agents that build
// their tool manifest with NewToolPolicy directly (architect uses this
// pattern; other agents use *VisibleSkillNames() helpers).
//
// All fabric awareness skills are read-only and visible-by-default —
// they MUST appear in the LLM's catalog without keyword priming, or
// the LLM never reaches for them (per the user's repeated complaint
// that agents avoid the fabric).
//
// When `registry` is non-nil, only skills that are actually registered
// on the registry are appended — keeps unwired agents from declaring
// phantom tools.
func AppendFabricAwarenessToolPolicies(
	base []toolruntime.ToolPolicy,
	registry *coreskills.Registry,
) []toolruntime.ToolPolicy {
	policies := append([]toolruntime.ToolPolicy(nil), base...)
	seen := policyNameSet(base)

	for _, name := range FabricAwarenessSkillNames() {
		if registry != nil && registry.Get(name) == nil {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		policies = append(policies, toolruntime.NewToolPolicy(
			name,
			toolruntime.EffectReadOnly,
			toolruntime.DomainKnowledge,
			toolruntime.ExecutionModeLocal,
			toolruntime.WithVisibleByDefault(),
		))
	}
	return policies
}

// AppendFabricInspectorToolPolicies extends AppendFabricAwarenessToolPolicies
// with the inspector audit skill (inspect_open_activity). Use only on
// inspector agents.
func AppendFabricInspectorToolPolicies(
	base []toolruntime.ToolPolicy,
	registry *coreskills.Registry,
) []toolruntime.ToolPolicy {
	policies := AppendFabricAwarenessToolPolicies(base, registry)
	seen := policyNameSet(policies)

	for _, name := range FabricInspectorAuditSkillNames() {
		if registry != nil && registry.Get(name) == nil {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		policies = append(policies, toolruntime.NewToolPolicy(
			name,
			toolruntime.EffectReadOnly,
			toolruntime.DomainKnowledge,
			toolruntime.ExecutionModeLocal,
			toolruntime.WithVisibleByDefault(),
		))
	}
	return policies
}

func policyNameSet(policies []toolruntime.ToolPolicy) map[string]struct{} {
	out := make(map[string]struct{}, len(policies))
	for _, p := range policies {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}
