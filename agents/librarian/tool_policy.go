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
		"read_workspace_file",
		"glob",
		"workspace_glob",
		"grep",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
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
