package archivalist

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func archivalistVisibleSkillNames() []string {
	return []string{
		ToolGetBriefing,
		ToolQueryPatterns,
		ToolQueryFailures,
		ToolQueryContext,
		ToolQueryFileState,
		"consult",
		ToolRecordPattern,
		ToolRecordFailure,
		ToolUpdateFileState,
		ToolDeclareIntent,
		ToolCompleteIntent,
		ToolGetConflicts,
	}
}

func archivalistMutatingSkillNames() []string {
	return []string{
		"store",
		ToolRecordPattern,
		ToolRecordFailure,
		ToolUpdateFileState,
		ToolDeclareIntent,
		ToolCompleteIntent,
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
