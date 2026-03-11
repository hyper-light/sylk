package librarian

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func librarianVisibleSkillNames() []string {
	return []string{
		"search_codebase",
		"find_pattern",
		"find_symbol",
		"read_file",
		"glob",
		"grep",
		"knowledge_search",
	}
}

func librarianMutatingSkillNames() []string {
	return []string{
		"clone_repository",
		"remove_package",
		"reroute_request",
	}
}

func librarianToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "librarian",
		CapabilityScope:  "librarian.default",
		Registry:         registry,
		VisibleByDefault: librarianVisibleSkillNames(),
		Mutating:         librarianMutatingSkillNames(),
	})
}
