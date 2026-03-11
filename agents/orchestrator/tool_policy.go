package orchestrator

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func orchestratorMutatingSkillNames() []string {
	return []string{
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
	}
}

func orchestratorVisibleSkillNames() []string {
	return []string{
		"query_task",
		"query_workflow",
		"push_status",
		"ingest_plan",
		"execute_dag",
		"escalate_to_architect",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	}
}

func orchestratorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "orchestrator",
		CapabilityScope:  "orchestrator.default",
		Registry:         registry,
		VisibleByDefault: orchestratorVisibleSkillNames(),
		Mutating:         orchestratorMutatingSkillNames(),
	})
}
