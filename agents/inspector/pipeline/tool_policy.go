package pipeline

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func pipelineInspectorVisibleSkillNames() []string {
	return []string{
		"run_linter",
		"run_type_checker",
		"run_security_scan",
		"read_file",
		"glob",
		"grep",
		"define_criteria",
		"validate_criteria",
	}
}

func pipelineInspectorMutatingSkillNames() []string {
	return []string{
		"define_criteria",
		"request_correction",
		"request_override",
		"reroute_request",
	}
}

func pipelineInspectorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "inspector-pipeline",
		CapabilityScope:  "inspector.pipeline",
		Registry:         registry,
		VisibleByDefault: pipelineInspectorVisibleSkillNames(),
		Mutating:         pipelineInspectorMutatingSkillNames(),
	})
}
