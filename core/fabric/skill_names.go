package fabric

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
		// Claims-aware fabric lenses (only visible when registered).
		"fabric_query_claims_board",
		"fabric_query_peer_claims",
		"fabric_inspect_claim_conflicts",
		"query_advisories",
		"query_challenge_history",
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

// ClaimsAwarenessSkillNames returns the claims-specific awareness skill
// names that should be visible by default on agents participating in
// claims-based execution. These supplement the base Fabric awareness
// skills — agents on claims-based pipelines get both.
func ClaimsAwarenessSkillNames() []string {
	return []string{
		"query_claims_board",
		"post_action",
		"inspect_claim_conflicts",
	}
}

// ClaimsSubjectSkillNames returns the skills that claims subjects
// (Engineer, Designer, Tester-during-implementation) use.
func ClaimsSubjectSkillNames() []string {
	return []string{
		"submit_testaments",
		"update_claim_progress",
	}
}

// ClaimsIssuerSkillNames returns the skills that claims issuers
// (Inspector, Tester-during-validation) use.
func ClaimsIssuerSkillNames() []string {
	return []string{
		"evaluate_validation",
		"post_remediation_claims",
	}
}

// AppendClaimsAwarenessSkillNames appends claims awareness skills to
// a base list. Use on all agents participating in claims-based execution.
func AppendClaimsAwarenessSkillNames(base []string) []string {
	out := make([]string, 0, len(base)+len(ClaimsAwarenessSkillNames()))
	out = append(out, base...)
	out = append(out, ClaimsAwarenessSkillNames()...)
	return out
}

// AppendClaimsSubjectSkillNames appends claims subject skills to the
// base + awareness list.
func AppendClaimsSubjectSkillNames(base []string) []string {
	out := AppendClaimsAwarenessSkillNames(base)
	return append(out, ClaimsSubjectSkillNames()...)
}

// AppendClaimsIssuerSkillNames appends claims issuer skills to the
// base + awareness list.
func AppendClaimsIssuerSkillNames(base []string) []string {
	out := AppendClaimsAwarenessSkillNames(base)
	return append(out, ClaimsIssuerSkillNames()...)
}
