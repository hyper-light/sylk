package academic

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func academicVisibleSkillNames() []string {
	return []string{
		"consult",
		"web_search",
		"web_fetch",
		"fetch_document",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	}
}

func academicMutatingSkillNames() []string {
	return []string{
		"author_research_paper",
		"clone_via_librarian",
		"reroute_request",
	}
}

func academicRecursiveSkillNames() []string {
	return []string{
		"research_topic",
		"find_best_practices",
		"compare_approaches",
		"recommend_solution",
		"author_research_paper",
	}
}

func academicToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	manifest := toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "academic",
		CapabilityScope:  "academic.default",
		Registry:         registry,
		VisibleByDefault: academicVisibleSkillNames(),
		Mutating:         academicMutatingSkillNames(),
	})
	for _, name := range academicRecursiveSkillNames() {
		policy, ok := manifest.Tools[name]
		if !ok {
			continue
		}
		policy.VisibleByDefault = false
		policy.Searchable = false
		manifest.Tools[name] = policy
	}
	return manifest
}
