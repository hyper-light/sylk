package academic

import (
	"github.com/adalundhe/sylk/core/fabric"
	contextskills "github.com/adalundhe/sylk/core/context/skills"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func academicVisibleSkillNames() []string {
	base := []string{
		"knowledge_query",
		"consult",
		"web_search",
		"ground_source",
		"web_fetch",
		"fetch_document",
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"query_board",
		"inspect_claim_conflicts",
		"traverse",
	}
	return fabric.AppendFabricAwarenessSkillNames(base)
}

func academicMutatingSkillNames() []string {
	return []string{
		"author_research_paper",
		"clone_via_librarian",
		"reroute_request",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
	}
}

func academicRecursiveSkillNames() []string {
	return []string{
		"research_topic",
		"find_best_practices",
		"compare_approaches",
		"recommend_solution",
		"author_research_paper",
	}
}

func academicToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	visible := appendRegisteredSkillNames(academicVisibleSkillNames(), registry, contextskills.ForestSkillNamesForAgent("academic")...)
	mutating := appendRegisteredSkillNames(academicMutatingSkillNames(), registry, contextskills.ForestMutatingSkillNames()...)
	// Filter the visible list against the registry. Necessary because
	// academicVisibleSkillNames() embeds the fabric-awareness list
	// (which statically includes consult_peer / challenge_peer), but
	// those skills are intentionally unregistered for the academic —
	// a reactive knowledge agent per its authority profile. Without
	// this filter the manifest builder errors with
	// `tool "consult_peer" is declared in policy but not registered`.
	// See docs/COMMS_MATRIX.md.
	visible = filterAgainstRegistry(visible, registry)
	mutating = filterAgainstRegistry(mutating, registry)
	manifest := toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "academic",
		CapabilityScope:  "academic.default",
		Registry:         registry,
		VisibleByDefault: visible,
		Mutating:         mutating,
	})
	for _, name := range academicRecursiveSkillNames() {
		policy, ok := manifest.Tools[name]
		if !ok {
			continue
		}
		policy.VisibleByDefault = false
		policy.Searchable = false
		manifest.Tools[name] = policy
	}
	return manifest
}

// filterAgainstRegistry drops any name from the list that isn't
// registered on the registry. Used to make the static skill-name
// helpers (fabric.AppendFabricAwarenessSkillNames and friends)
// tolerate agents that intentionally omit some of those skills per
// their authority profile.
func filterAgainstRegistry(names []string, registry *skills.Registry) []string {
	if registry == nil {
		return names
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if registry.Get(name) == nil {
			continue
		}
		out = append(out, name)
	}
	return out
}

func appendRegisteredSkillNames(base []string, registry *skills.Registry, names ...string) []string {
	if registry == nil {
		return append([]string(nil), base...)
	}
	result := append([]string(nil), base...)
	for _, name := range names {
		if registry.Get(name) == nil {
			continue
		}
		result = append(result, name)
	}
	return result
}
