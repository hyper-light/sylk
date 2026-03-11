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
		"write_file",
		"edit_file",
		"create_directory",
		"read_workspace_file",
		"glob",
		"workspace_glob",
		"grep",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"audit_layer",
	}
}

func globalInspectorMutatingSkillNames() []string {
	return []string{
		"write_file",
		"edit_file",
		"create_directory",
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
