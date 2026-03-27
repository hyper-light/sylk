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
		"2. Retrieve",
		"3. Ground",
		"4. Synthesize",
		"`web_search`",
		"`web_fetch`, `fetch_document`, or a bounded `crawl_links` call",
		"stop only when more searching is unlikely to materially change the conclusion",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("discipline prompt missing %q:\n%s", needle, prompt)
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
