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
		// Workspace-aware reads reach the in-flight global VFS overlay.
		// Disk-only read_file/glob/grep stay above for committed state.
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
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
