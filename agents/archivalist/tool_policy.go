package archivalist

import (
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func archivalistVisibleSkillNames() []string {
	// Post-consolidation visible surface:
	//   archivalist_query   — briefing / patterns / failures / context /
	//                         file_state / conflicts (6 verbs → 1)
	//   archivalist_record  — pattern / failure (2 verbs → 1)
	//   archivalist_intent  — declare / complete (2 verbs → 1)
	// The legacy per-verb names (ToolGetBriefing, ToolQueryPatterns, …)
	// remain registered skills so internal callers and tests can keep
	// exercising them by name; they are intentionally not exposed to
	// the LLM catalog — the LLM picks kind/action on the façade.
	return []string{
		ToolArchivalistQuery,
		ToolArchivalistRecord,
		ToolArchivalistIntent,
		ToolUpdateFileState,
		"knowledge_query",
		"knowledge_memory",
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"query_board",
		"recall_forward",
		"inspect_claim_conflicts",
		"traverse",
		"carry_forward",
		"claims_projection_health",
		"claims_operations_status",
		"rebuild_claims_projections",
	}
}

func archivalistVisibleSkillNamesForRegistry(registry *skills.Registry) []string {
	withForest := shared.AppendMemoryForestVisibleSkillNames(archivalistVisibleSkillNames(), "archivalist")
	withFabric := fabric.AppendFabricAwarenessSkillNames(withForest)
	return shared.FilterRegisteredSkillNames(registry, withFabric)
}

func archivalistMutatingSkillNames() []string {
	return shared.AppendMemoryForestMutatingSkillNames([]string{
		"store",
		ToolArchivalistRecord,
		ToolArchivalistIntent,
		ToolUpdateFileState,
		"store_research_paper",
		"route_to",
		"reply_to",
		"knowledge_memory",
		"reroute_request",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
		"carry_forward",
		"rebuild_claims_projections",
		"claims_operations_repair",
	})
}

func archivalistToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "archivalist",
		CapabilityScope:  "archivalist.default",
		Registry:         registry,
		VisibleByDefault: archivalistVisibleSkillNamesForRegistry(registry),
		Mutating:         shared.FilterRegisteredSkillNames(registry, archivalistMutatingSkillNames()),
	})
}
