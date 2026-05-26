package librarian

import (
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func librarianVisibleSkillNames() []string {
	base := shared.AppendMemoryForestVisibleSkillNames([]string{
		// Unified search verb covers search_codebase / find_pattern /
		// find_symbol. The underlying builders stay registered for
		// internal callers; the LLM picks `kind` to select the engine.
		"search",
		"consult",
		// Layered reads only — workspace_read always requires an explicit
		// `view` parameter (disk / global / pipeline), so layer attribution
		// is captured at the tool boundary. The bare read_file / glob /
		// grep skills used to live alongside the per-op workspace reads
		// as disk-only convenience wrappers, but LLM tool-name bias
		// defeated the prompt's "use workspace_* for in-flight state"
		// guidance every time and caused phantom "file not found"
		// reports for files staged in the pipeline VFS. Collapsing the
		// per-op surfaces into a single workspace_read verb makes the
		// layerless choice unrepresentable.
		"workspace_read",
		"knowledge_search",
		// Unified package verb covers clone_repository / list_packages /
		// remove_package. `action` selects clone/list/remove.
		"package",
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"query_board",
		"recall_forward",
		"inspect_claim_conflicts",
		"traverse",
		"carry_forward",
	}, "librarian")
	return fabric.AppendFabricAwarenessSkillNames(base)
}

func librarianMutatingSkillNames() []string {
	return shared.AppendMemoryForestMutatingSkillNames([]string{
		// The `package` façade's clone/remove actions write to disk;
		// `list` does not, but the unified verb is marked mutating to
		// match the most-permissive underlying action.
		"package",
		"reroute_request",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
		"carry_forward",
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
