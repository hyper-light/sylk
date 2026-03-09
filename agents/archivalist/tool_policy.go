package archivalist

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func archivalistVisibleSkillNames() []string {
	return []string{
		"store",
		"query",
		"briefing",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	}
}

func archivalistMutatingSkillNames() []string {
	return []string{
		"store",
		"store_research_paper",
		"route_to",
		"reply_to",
		"knowledge_memory",
		"reroute_request",
	}
}

func archivalistToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "archivalist",
		CapabilityScope:  "archivalist.default",
		Registry:         registry,
		VisibleByDefault: archivalistVisibleSkillNames(),
		Mutating:         archivalistMutatingSkillNames(),
	})
}
