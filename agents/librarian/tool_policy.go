package librarian

import (
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func librarianVisibleSkillNames() []string {
	return shared.AppendMemoryForestVisibleSkillNames([]string{
		"search_codebase",
		"find_pattern",
		"consult",
		"find_symbol",
		"read_file",
		"glob",
		"grep",
		"knowledge_search",
	}, "librarian")
}

func librarianMutatingSkillNames() []string {
	return shared.AppendMemoryForestMutatingSkillNames([]string{
		"clone_repository",
		"remove_package",
		"reroute_request",
	})
}

func librarianToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "librarian",
		CapabilityScope:  "librarian.default",
		Registry:         registry,
		VisibleByDefault: shared.FilterRegisteredSkillNames(registry, librarianVisibleSkillNames()),
		Mutating:         shared.FilterRegisteredSkillNames(registry, librarianMutatingSkillNames()),
	})
}
