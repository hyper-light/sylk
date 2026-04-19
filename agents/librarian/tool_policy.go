package librarian

import (
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func librarianVisibleSkillNames() []string {
	base := shared.AppendMemoryForestVisibleSkillNames([]string{
		"search_codebase",
		"find_pattern",
		"consult",
		"find_symbol",
		// Layered reads only — every read tool requires a `view` parameter
		// (disk / global / pipeline) so layer attribution is captured at
		// the tool boundary. The bare read_file / glob / grep skills used
		// to live alongside these as disk-only convenience wrappers, but
		// LLM tool-name bias defeated the prompt's "use workspace_* for
		// in-flight state" guidance every time and caused phantom
		// "file not found" reports for files staged in the pipeline VFS.
		// Removing the bare skills makes the wrong choice unrepresentable.
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
		"knowledge_search",
	}, "librarian")
	return shared.AppendFabricAwarenessSkillNames(base)
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
