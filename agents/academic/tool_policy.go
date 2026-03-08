package academic

import (
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func academicVisibleSkillNames() []string {
	return []string{
		"research_topic",
		"find_best_practices",
		"web_fetch",
	}
}

func academicMutatingSkillNames() []string {
	return []string{
		"author_research_paper",
		"clone_via_librarian",
		"reroute_request",
	}
}

func academicToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "academic",
		CapabilityScope:  "academic.default",
		Registry:         registry,
		VisibleByDefault: academicVisibleSkillNames(),
		Mutating:         academicMutatingSkillNames(),
	})
}
