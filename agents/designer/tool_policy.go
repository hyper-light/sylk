package designer

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func designerVisibleSkillNames() []string {
	return []string{
		"component_search",
		"component_create",
		"component_modify",
		"ask_user_clarification",
	}
}

func designerMutatingSkillNames() []string {
	return []string{
		"component_create",
		"component_modify",
		"request_engineer_review",
		"request_inspector_check",
		"request_tester_validation",
		"ask_user_clarification",
		"report_to_engineer",
		"report_to_orchestrator",
		"reroute_request",
	}
}

func designerToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "designer",
		CapabilityScope:  "designer.default",
		Registry:         registry,
		VisibleByDefault: designerVisibleSkillNames(),
		Mutating:         designerMutatingSkillNames(),
	})
}
