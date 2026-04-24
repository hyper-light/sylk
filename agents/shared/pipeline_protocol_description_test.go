package shared

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

// TestPipelineProtocolUnifiedSkill_DescriptionCarriesFinalizePostCondition
// is the regression guard against the exact bug that wedged a live
// pipeline: the `finalize_pipeline` delegate's load-bearing
// post-condition (*after finalize returns ready_for_ot, call
// handoff_to_ot next*) was silently dropped when the unified skill's
// description was assembled, because the builder only rendered
// purpose + required + first-usage-sentence.
//
// If this assertion ever fails, the LLM will again stop seeing the
// must-handoff-to-ot rule and the pipeline will wedge the same way.
// Do not "fix" this test by removing it — fix the description builder.
func TestPipelineProtocolUnifiedSkill_DescriptionCarriesFinalizePostCondition(t *testing.T) {
	t.Parallel()

	unified := pipelineProtocolUnifiedDescription(t)

	if !strings.Contains(unified, "- finalize:") {
		t.Fatalf("unified description missing `- finalize:` action block\n\nfull description:\n%s", unified)
	}

	mustContain := []string{
		"ready_for_ot: true",
		"handoff_to_ot",
		"next terminal protocol action in this turn must be",
	}
	for _, want := range mustContain {
		if !strings.Contains(unified, want) {
			t.Errorf("unified description missing load-bearing phrase %q\n\nfull description:\n%s", want, unified)
		}
	}
}

// TestPipelineProtocolUnifiedSkill_PropagatesDelegateConstraints is the
// structural guarantee: for every delegate that carries Requirement /
// Satisfies / Avoid / BestPractice strings, every one of those strings
// appears in the façade description under its action's block. The
// earlier builder only rendered first-sentence summaries, which
// dropped delegate Requirements and wedged the protocol. If any
// delegate's instructional surface goes missing again, this fails.
func TestPipelineProtocolUnifiedSkill_PropagatesDelegateConstraints(t *testing.T) {
	t.Parallel()

	cfg := PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		InspectorOT: true,
	}
	unified := pipelineProtocolUnifiedDescription(t)

	cases := []struct {
		action   string
		delegate *skills.Skill
	}{
		{"challenge", pipelineChallengeAgentSkill(cfg)},
		{"handoff", pipelineHandoffNextSkill(cfg)},
		{"validate", pipelineValidateWorkSkill(cfg)},
		{"process_validation", pipelineProcessValidationSkill(cfg)},
		{"finalize", pipelineFinalizePipelineSkill(cfg)},
	}

	for _, tc := range cases {
		block := isolateActionBlock(unified, tc.action)
		if block == "" {
			t.Errorf("action block %q not rendered", tc.action)
			continue
		}

		for _, req := range tc.delegate.Requirements {
			if strings.TrimSpace(req) == "" {
				continue
			}
			if !blockContainsFragment(block, req) {
				t.Errorf("action %q block missing Requirement %q", tc.action, shortPhrase(req))
			}
		}
		for _, sat := range tc.delegate.Satisfies {
			if strings.TrimSpace(sat) == "" {
				continue
			}
			if !blockContainsFragment(block, sat) {
				t.Errorf("action %q block missing Satisfies %q", tc.action, shortPhrase(sat))
			}
		}
		for _, avoid := range tc.delegate.Avoids {
			if strings.TrimSpace(avoid) == "" {
				continue
			}
			if !blockContainsFragment(block, avoid) {
				t.Errorf("action %q block missing Avoid %q", tc.action, shortPhrase(avoid))
			}
		}
		for _, practice := range tc.delegate.BestPractices {
			if strings.TrimSpace(practice) == "" {
				continue
			}
			if !blockContainsFragment(block, practice) {
				t.Errorf("action %q block missing BestPractice %q", tc.action, shortPhrase(practice))
			}
		}
	}
}

// pipelineProtocolUnifiedDescription builds the unified skill via the
// production PipelineProtocolSkills path and returns the façade's
// Description for assertion.
func pipelineProtocolUnifiedDescription(t *testing.T) string {
	t.Helper()
	built := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		InspectorOT: true,
	})
	for _, s := range built {
		if s.Name == "pipeline_protocol" {
			return s.Description
		}
	}
	t.Fatal("pipeline_protocol skill not found in assembled skill list")
	return ""
}

// isolateActionBlock returns the slice of the description bounded to
// the requested action block. Each block starts with "- <action>:"
// and ends at the next top-level bullet or the end of the string.
func isolateActionBlock(description, action string) string {
	header := "- " + action + ":"
	idx := strings.Index(description, header)
	if idx < 0 {
		return ""
	}
	rest := description[idx+len(header):]
	if nextIdx := strings.Index(rest, "\n- "); nextIdx > 0 {
		rest = rest[:nextIdx]
	}
	return rest
}

// blockContainsFragment returns true when the action block carries the
// delegate string, after normalizing whitespace on both sides so
// multi-line delegate strings don't produce false negatives.
func blockContainsFragment(block, fragment string) bool {
	normBlock := strings.Join(strings.Fields(block), " ")
	normFragment := strings.Join(strings.Fields(fragment), " ")
	return strings.Contains(normBlock, normFragment)
}

// shortPhrase produces a truncated fragment for error output.
func shortPhrase(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}
