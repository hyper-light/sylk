package designer

import (
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func designerVisibleSkillNames() []string {
	base := shared.AppendMemoryForestVisibleSkillNames([]string{
		"search_skills",
		// Phase 2.K / CR-2: 12 workspace skills collapsed into 3 verbs.
		"workspace_read",
		"workspace_write",
		"component_search",
		"bash",
		"component_create",
		"component_modify",
		// Phase 2.K / GT-4 + GI-5: collapsed into dependency(action=…).
		"dependency",
		"ask_user_clarification",
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"query_board",
		"recall_forward",
		"inspect_claim_conflicts",
		"traverse",
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
		"carry_forward",
	}, "designer")
	return fabric.AppendFabricAwarenessSkillNames(base)
}

func designerMutatingSkillNames() []string {
	return shared.AppendMemoryForestMutatingSkillNames([]string{
		// Phase 2.K / CR-2: 4 write skills collapsed into workspace_write.
		"workspace_write",
		"bash",
		"dependency",
		"ask_user_clarification",
		"reroute_request",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
		"carry_forward",
	})
}

func designerToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "designer",
		CapabilityScope:  "designer.default",
		Registry:         registry,
		VisibleByDefault: shared.FilterRegisteredSkillNames(registry, designerVisibleSkillNames()),
		Mutating:         shared.FilterRegisteredSkillNames(registry, designerMutatingSkillNames()),
	})
}
