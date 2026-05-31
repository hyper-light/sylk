package orchestrator

import (
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func orchestratorMutatingSkillNames() []string {
	return shared.AppendMemoryForestMutatingSkillNames([]string{
		"push_status",
		"report_failure",
		"submit_task_event",
		"archivalist_request",
		"escalate_to_architect",
		"broadcast_status",
		"execute_dag",
		"cancel_dag",
		"modify_dag",
		"ingest_plan",
		"validate_work",
		"cancel_orchestration",
		"resume_orchestration",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
		"carry_forward",
	})
}

func orchestratorVisibleSkillNames() []string {
	base := shared.AppendMemoryForestVisibleSkillNames([]string{
		"query_task",
		"query_workflow",
		"query_dag_status",
		"query_pipeline_state",
		"query_buffer",
		"plan_state_query",
		"cancel_orchestration",
		"resume_orchestration",
		"generate_summary",
		"push_status",
		"ingest_plan",
		"execute_dag",
		"validate_work",
		"escalate_to_architect",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"diff_workspace_file",
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
	}, "orchestrator")
	return fabric.AppendFabricAwarenessSkillNames(base)
}

func orchestratorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "orchestrator",
		CapabilityScope:  "orchestrator.default",
		Registry:         registry,
		VisibleByDefault: shared.FilterRegisteredSkillNames(registry, orchestratorVisibleSkillNames()),
		Mutating:         shared.FilterRegisteredSkillNames(registry, orchestratorMutatingSkillNames()),
	})
}
