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
		VisibleByDefault: registeredLibrarianSkillNames(registry, librarianVisibleSkillNames()),
		Mutating:         registeredLibrarianSkillNames(registry, librarianMutatingSkillNames()),
	})
}

func registeredLibrarianSkillNames(registry *skills.Registry, names []string) []string {
	if registry == nil || len(names) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if registry.Get(name) != nil {
			filtered = append(filtered, name)
		}
	}
	return filtered
}
