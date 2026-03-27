package academic

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestAcademicExternalResearchDisciplinePrompt_UsesExplicitResearchWorkflow(t *testing.T) {
	prompt := academicExternalResearchDisciplinePrompt()
	for _, needle := range []string{
		"1. Plan",
		"2. Search",
		"3. Ground",
		"4. Consult",
		"5. Synthesize",
		"`web_search`",
		"`ground_source`",
		"`web_fetch`, `fetch_document`, or one bounded `crawl_links`",
		"must be grounded first with `ground_source` or an equivalent fetch skill",
		"stop only when more searching is unlikely to materially change the conclusion",
		"prefer primary empirical evidence and grounded quantitative data whenever feasible",
		"Do not repeat benchmark numbers, percentages, or study findings unless you have inspected the source itself",
		"say the evidence is qualitative, limited, or stale instead of implying false precision",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("discipline prompt missing %q:\n%s", needle, prompt)
		}
	}
}

func TestAcademicForwardedResearchPrompt_ArchitectRequiresGroundedNumbers(t *testing.T) {
	prompt := academicForwardedResearchPrompt(&guide.ForwardedRequest{
		SourceAgentID: "architect",
		Intent:        guide.IntentRecall,
	})
	for _, needle := range []string{
		"Any link surfaced via `web_search` that you plan to cite, quote, or list as a source must be grounded first.",
		"back it with grounded statistics, benchmark context, standards, or study results whenever credible data exists",
		"Do not cite benchmark numbers, percentages, or study conclusions without grounding the source first",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("forwarded research prompt missing %q:\n%s", needle, prompt)
		}
	}
}

func TestAcademicForwardedResearchExtraTools_ArchitectGetsResearchArtifactSurface(t *testing.T) {
	tools := academicForwardedResearchExtraTools(&guide.ForwardedRequest{
		SourceAgentID: "architect",
		Intent:        guide.IntentRecall,
	})
	want := map[string]bool{
		"author_research_paper": true,
		"clone_via_librarian":   true,
		"crawl_links":           true,
	}
	for _, tool := range tools {
		delete(want, tool)
	}
	if len(want) != 0 {
		t.Fatalf("missing expected architect research tools: %#v", want)
	}
}

func TestAcademicForwardedResearchExtraTools_SkipsUserFacingArchitectHandoff(t *testing.T) {
	tools := academicForwardedResearchExtraTools(&guide.ForwardedRequest{
		SourceAgentID: "architect",
		Intent:        guide.IntentRecall,
		Metadata: map[string]any{
			"user_facing_handoff": true,
		},
	})
	if len(tools) != 0 {
		t.Fatalf("user-facing architect handoff should not get worker-only extra tools, got %#v", tools)
	}
}
