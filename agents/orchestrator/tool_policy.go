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

func orchestratorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "orchestrator",
		CapabilityScope:  "orchestrator.default",
		Registry:         registry,
		VisibleByDefault: orchestratorPinnedSkillNames(),
		Mutating:         orchestratorMutatingSkillNames(),
	})
}
