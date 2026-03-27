package architect

import (
	"strings"
	"testing"
)

func TestBuildResearchPlanningQuery_UsesResearchDepthHints(t *testing.T) {
	query := buildResearchPlanningQuery(&readResearchPaperParams{
		ResearchSlug:        "distributed-cache",
		Summary:             "Prefer cache-aside behind a repository abstraction.",
		RecommendedOptionID: "rec-1",
		PrototypeSketch:     "Prototype a single repository-backed cache path before widening scope.",
		SystemDesignNotes: []string{
			"Preserve write-path correctness.",
			"Make invalidation ownership explicit.",
		},
	}, "# Research Proposal")

	for _, want := range []string{
		"Convert research proposal 'distributed-cache' into execution plan.",
		"Summary: Prefer cache-aside behind a repository abstraction.",
		"Recommended option: rec-1",
		"Prototype sketch: Prototype a single repository-backed cache path before widening scope.",
		"System design notes: Preserve write-path correctness.; Make invalidation ownership explicit.",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("planning query missing %q\n%s", want, query)
		}
	}
}
