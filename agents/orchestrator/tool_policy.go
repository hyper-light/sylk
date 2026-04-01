package orchestrator

import (
	"github.com/adalundhe/sylk/agents/shared"
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
		"validate_global_review",
	})
}

func orchestratorVisibleSkillNames() []string {
	return shared.AppendMemoryForestVisibleSkillNames([]string{
		"query_task",
		"query_workflow",
		"query_dag_status",
		"query_pipeline_state",
		"query_buffer",
		"generate_summary",
		"push_status",
		"ingest_plan",
		"execute_dag",
		"validate_global_review",
		"escalate_to_architect",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"diff_workspace_file",
	}, "orchestrator")
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
