package skills

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderActionBlock_PropagatesLoadBearingRequirements is the
// regression guard against the class of bug where enum-dispatched
// façades silently drop delegate Requirements. The pipeline_protocol
// façade wedged because finalize's "if ready_for_ot is true, call
// handoff_to_ot next" Requirement never reached the LLM's tool
// catalog. Every field type on the delegate must render under its own
// tagged line so the LLM binds each rule to the action enum value.
func TestRenderActionBlock_PropagatesLoadBearingRequirements(t *testing.T) {
	t.Parallel()

	delegate := NewSkill("finalize_pipeline").
		Description("Run the inspector closure gate. Returns ready_for_ot when accepted.").
		Domain("pipeline").
		Usage("Invoke only after the audit is settled and any challenge responses have been consumed.").
		Requirement("If this tool returns ready_for_ot: true, the next terminal action must be handoff_to_ot.").
		Requirement("Do not end the turn or continue with other work until handoff_to_ot has been invoked.").
		Satisfies("Produces the closure verdict that gates OT handoff.").
		Avoid("Do not use as a substitute for a targeted challenge when returned work is still unclear.").
		BestPractice("Pass the strongest criteria and challenge evidence into the call.").
		StringParam("summary", "Closure summary", true).
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil }).
		Build()

	block := RenderActionBlock(delegate, "finalize")
	if block == "" {
		t.Fatal("expected a non-empty action block")
	}

	// Every instructional field type must render.
	mustContain := []string{
		"- finalize:",
		"purpose:",
		"required: summary",
		"usage:",
		// The load-bearing post-condition — the exact bug this test
		// guards. If this assertion ever fails, the LLM stops seeing
		// the "must call handoff_to_ot next" rule.
		"requirement: If this tool returns ready_for_ot: true, the next terminal action must be handoff_to_ot.",
		"requirement: Do not end the turn or continue with other work until handoff_to_ot has been invoked.",
		"satisfies: Produces the closure verdict that gates OT handoff.",
		"avoid: Do not use as a substitute for a targeted challenge when returned work is still unclear.",
		"best_practice: Pass the strongest criteria and challenge evidence into the call.",
	}
	for _, want := range mustContain {
		if !strings.Contains(block, want) {
			t.Errorf("rendered block missing %q\n\nfull block:\n%s", want, block)
		}
	}
}

func TestRenderActionBlock_NilDelegateReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := RenderActionBlock(nil, "anything"); got != "" {
		t.Fatalf("expected empty string for nil delegate, got %q", got)
	}
}

func TestRenderActionBlock_BlankActionReturnsEmpty(t *testing.T) {
	t.Parallel()
	delegate := NewSkill("x").Description("y").
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil }).Build()
	if got := RenderActionBlock(delegate, "   "); got != "" {
		t.Fatalf("expected empty string for blank action, got %q", got)
	}
}

// TestRenderActionBlock_CollapsesMultilineGuidance ensures multi-line
// Requirement / Avoid / BestPractice strings don't spill across block
// lines — otherwise the next field looks like a continuation of the
// previous rule when the LLM reads the description.
func TestRenderActionBlock_CollapsesMultilineGuidance(t *testing.T) {
	t.Parallel()
	delegate := NewSkill("x").
		Description("test").
		Requirement("Line one\nLine two\n\tindented line three").
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil }).
		Build()

	block := RenderActionBlock(delegate, "test")
	// Requirement line must be on one line — no raw newlines from the
	// input string survived into the rendered block (outside the
	// terminating newline each field adds).
	trimmed := strings.TrimRight(block, "\n")
	lines := strings.Split(trimmed, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "requirement:") {
			if strings.Contains(line, "\n") {
				t.Fatalf("requirement line contained embedded newline: %q", line)
			}
			if !strings.Contains(line, "Line one Line two indented line three") {
				t.Fatalf("requirement line did not collapse whitespace: %q", line)
			}
		}
	}
}
