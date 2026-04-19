package archivalist

import (
	"github.com/adalundhe/sylk/agents/shared"
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
		"knowledge_query",
		"knowledge_memory",
	}
}

func archivalistVisibleSkillNamesForRegistry(registry *skills.Registry) []string {
	withForest := shared.AppendMemoryForestVisibleSkillNames(archivalistVisibleSkillNames(), "archivalist")
	withFabric := shared.AppendFabricAwarenessSkillNames(withForest)
	return shared.FilterRegisteredSkillNames(registry, withFabric)
}

func archivalistMutatingSkillNames() []string {
	return shared.AppendMemoryForestMutatingSkillNames([]string{
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
	})
}

func archivalistToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "archivalist",
		CapabilityScope:  "archivalist.default",
		Registry:         registry,
		VisibleByDefault: archivalistVisibleSkillNamesForRegistry(registry),
		Mutating:         shared.FilterRegisteredSkillNames(registry, archivalistMutatingSkillNames()),
	})
}
