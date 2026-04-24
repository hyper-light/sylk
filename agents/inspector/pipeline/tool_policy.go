package pipeline

import (
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineInspectorVisibleSkillNames() []string {
	base := agentShared.AppendMemoryForestVisibleSkillNames([]string{
		"search_skills",
		// Phase 2.K / CR-2: workspace read-side collapsed into workspace_read.
		// Inspector does not get workspace_write or prepare_write_context —
		// inspector's role is read + audit, not mutation. The safety hook
		// in skills.go blocks mutation attempts.
		"workspace_read",
		"bash",
		"define_criteria",
		// Phase 1 refactor: get_validation_status removed — derive from
		// query_peer_activity(kinds=["validation_*"]) + query_pipeline_state.
		// Phase 2.K / GT-4 + GI-5: collapsed into dependency(action=…).
		"dependency",
		// challenge_agent / handoff_next / validate_work /
		// process_validation / finalize_pipeline collapsed into
		// pipeline_protocol(action=challenge|handoff|validate|
		// process_validation|finalize). handoff_to_ot remains distinct —
		// it's inspector-only, the terminal commit-to-green action.
		"pipeline_protocol",
		"handoff_to_ot",
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"inspect_claim_conflicts",
		"traverse",
	}, "inspector-pipeline")
	// Inspector gets BOTH the awareness skills AND the audit-time
	// inspect_open_activity. The audit skill is what makes the
	// inspector's role at finalize time actually inspect open
	// fabric activity (challenges, holds, hot scopes, decisions).
	return fabric.AppendFabricInspectorSkillNames(base)
}

func pipelineInspectorMutatingSkillNames() []string {
	return agentShared.AppendMemoryForestMutatingSkillNames([]string{
		"bash",
		"define_criteria",
		// Phase 1 refactor: request_correction removed. Use
		// challenge_peer(target_activity_id=<failing artifact/decision>).
		"request_override",
		"dependency",
		"challenge_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		"finalize_pipeline",
		"handoff_to_ot",
		"reroute_request",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
	})
}


func pipelineInspectorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "inspector-pipeline",
		CapabilityScope:  "inspector.pipeline",
		Registry:         registry,
		VisibleByDefault: agentShared.FilterRegisteredSkillNames(registry, pipelineInspectorVisibleSkillNames()),
		Mutating:         agentShared.FilterRegisteredSkillNames(registry, pipelineInspectorMutatingSkillNames()),
	})
}
