package global

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func globalInspectorVisibleSkillNames() []string {
	return []string{
		"run_linter",
		"run_type_checker",
		"run_security_scan",
		"read_file",
		"glob",
		"grep",
		"audit_layer",
	}
}

func globalInspectorMutatingSkillNames() []string {
	return []string{
		"request_architect_research",
		"request_user_clarification",
		"escalate_findings",
		"reroute_request",
	}
}

func globalInspectorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "inspector",
		CapabilityScope:  "inspector.global",
		Registry:         registry,
		VisibleByDefault: globalInspectorVisibleSkillNames(),
		Mutating:         globalInspectorMutatingSkillNames(),
	})
}
