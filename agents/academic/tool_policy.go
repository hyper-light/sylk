package academic

import (
	"github.com/adalundhe/sylk/core/fabric"
	contextskills "github.com/adalundhe/sylk/core/context/skills"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func academicVisibleSkillNames() []string {
	base := []string{
		"knowledge_query",
		"consult",
		"web_search",
		"ground_source",
		"web_fetch",
		"fetch_document",
	}
	return fabric.AppendFabricAwarenessSkillNames(base)
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
	visible := appendRegisteredSkillNames(academicVisibleSkillNames(), registry, contextskills.ForestSkillNamesForAgent("academic")...)
	mutating := appendRegisteredSkillNames(academicMutatingSkillNames(), registry, contextskills.ForestMutatingSkillNames()...)
	manifest := toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "academic",
		CapabilityScope:  "academic.default",
		Registry:         registry,
		VisibleByDefault: visible,
		Mutating:         mutating,
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

func appendRegisteredSkillNames(base []string, registry *skills.Registry, names ...string) []string {
	if registry == nil {
		return append([]string(nil), base...)
	}
	result := append([]string(nil), base...)
	for _, name := range names {
		if registry.Get(name) == nil {
			continue
		}
		result = append(result, name)
	}
	return result
}
