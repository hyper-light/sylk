package shared

// FabricAwarenessSkillNames returns the canonical visible-by-default
// skill names for every pipeline agent's fabric awareness surface.
//
// These skills MUST be on every agent's VisibleByDefault list so the
// LLM sees them in its tool catalog without keyword matching. The
// fabric is "ambient" only when the agent can reach for it without
// surprise — which means the skills must be visible whenever the
// agent has a tool surface at all.
//
// Per the inciting screenshot review: agents were defaulting to the
// older `query_decisions` and `coord_query_view` skills because the
// new fabric skills weren't in the visible-by-default catalog. The
// loader's keyword-based progressive loading rarely matched on
// "fabric" or "awareness," so the LLM never knew the skills existed.
func FabricAwarenessSkillNames() []string {
	return []string{
		"query_peer_activity",
		"causal_trace",
		"find_related_activity",
		"inspect_open_conflicts",
		"consult_peer",
		"challenge_peer",
		"recall_my_history",
	}
}

// FabricInspectorAuditSkillNames returns the inspector-specific
// audit skill names that must be visible by default on inspector
// agents (pipeline inspector + global inspector).
func FabricInspectorAuditSkillNames() []string {
	return []string{
		"inspect_open_activity",
	}
}

// AppendFabricAwarenessSkillNames appends the canonical fabric
// awareness skill names to an agent's visible-by-default list.
// Convenience wrapper to keep the per-agent visibility files small.
func AppendFabricAwarenessSkillNames(base []string) []string {
	out := make([]string, 0, len(base)+len(FabricAwarenessSkillNames()))
	out = append(out, base...)
	out = append(out, FabricAwarenessSkillNames()...)
	return out
}

// AppendFabricInspectorSkillNames appends both the awareness skills
// and the inspector audit skills. Use only on inspector agents.
func AppendFabricInspectorSkillNames(base []string) []string {
	out := AppendFabricAwarenessSkillNames(base)
	return append(out, FabricInspectorAuditSkillNames()...)
}
