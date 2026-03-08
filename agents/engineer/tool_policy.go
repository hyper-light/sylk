package engineer

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func engineerVisibleSkillNames() []string {
	return []string{
		"read_file",
		"edit_file",
		"write_file",
		"glob",
		"grep",
		"run_command",
	}
}

func engineerMutatingSkillNames() []string {
	return []string{
		"write_file",
		"edit_file",
		"run_command",
		"format",
		"consult",
		"signal_orchestrator",
		"report_confidence",
		"reroute_request",
	}
}

func engineerToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "engineer",
		CapabilityScope:  "engineer.default",
		Registry:         registry,
		VisibleByDefault: engineerVisibleSkillNames(),
		Mutating:         engineerMutatingSkillNames(),
	})
}
