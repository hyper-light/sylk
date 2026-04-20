package shared

import (
	"strings"
	"testing"
)

// TestPipelineFinalizeTargetsSummary_RendersProvidedTargets verifies
// the diagnostic string included in the challenge_target_mismatch
// error's HumanMessage. The LLM's retry reasoning reads "you
// provided targets=[engineer, designer]" off the error prose; a
// regression that dropped target names would leave the retry advice
// abstract.
func TestPipelineFinalizeTargetsSummary_RendersProvidedTargets(t *testing.T) {
	specs := []PipelineTesterFinalizeTargetSpec{
		{Target: "engineer"},
		{Target: "designer"},
	}
	got := pipelineFinalizeTargetsSummary(specs)
	want := "targets=[engineer, designer]"
	if got != want {
		t.Errorf("pipelineFinalizeTargetsSummary = %q, want %q", got, want)
	}
}

// TestPipelineFinalizeTargetsSummary_NormalizesAliases proves the
// summary echoes the canonical name the validator uses, not the
// LLM's raw input. "inspector" is spelled inspector-pipeline in the
// protocol; echoing the canonical form keeps the LLM's mental model
// aligned with the internal state.
func TestPipelineFinalizeTargetsSummary_NormalizesAliases(t *testing.T) {
	specs := []PipelineTesterFinalizeTargetSpec{{Target: "inspector"}}
	got := pipelineFinalizeTargetsSummary(specs)
	want := "targets=[inspector-pipeline]"
	if got != want {
		t.Errorf("pipelineFinalizeTargetsSummary = %q, want %q", got, want)
	}
}

// TestPipelineFinalizeTargetsSummary_EmptyAndBlank covers the two
// edge cases: zero targets (an LLM that forgot targets entirely)
// and blank-target entries. Both must produce stable text rather
// than crashing or producing "targets=[]" for a blank-entry case.
func TestPipelineFinalizeTargetsSummary_EmptyAndBlank(t *testing.T) {
	if got := pipelineFinalizeTargetsSummary(nil); got != "targets=[]" {
		t.Errorf("nil specs = %q, want targets=[]", got)
	}
	specs := []PipelineTesterFinalizeTargetSpec{{Target: "   "}}
	got := pipelineFinalizeTargetsSummary(specs)
	want := "targets=[(empty)]"
	if got != want {
		t.Errorf("blank target = %q, want %q", got, want)
	}
}

// TestChallengeTargetMismatchErrorIncludesChallengeID locks the
// diagnostic content for the challenge_target_mismatch error.
// The pre-fix error told the LLM "retry with validate_work(...)"
// but never surfaced the active challenge_id, forcing the LLM
// into a discovery loop (find_related_activity, inspect_open_conflicts)
// that never terminated. Both the human message and the recovery
// action must echo the active id so the next retry can succeed
// without further state queries.
func TestChallengeTargetMismatchErrorIncludesChallengeID(t *testing.T) {
	err := NewPipelineProtocolError(
		"pipeline.finalize.challenge_target_mismatch",
		"finalize_pipeline",
		"a pending challenge (challenge_id=\"task_2-challenge-abc12345\") from \"inspector-pipeline\" is awaiting your response — finalize_pipeline must list exactly one targets entry for \"inspector-pipeline\" so the verification artifact threads into validate_work; you provided targets=[engineer, designer]",
		"retry finalize_pipeline with targets=[{target:\"inspector-pipeline\", summary:\"...\", evidence_refs:[...]}] and then end the turn with validate_work(challenge_id=\"task_2-challenge-abc12345\", requesting_agent=\"inspector-pipeline\", status:\"...\", summary:\"...\"). Call query_pipeline_state if you need to re-confirm the active challenge.",
	)
	rendered := err.Error()
	for _, want := range []string{
		"task_2-challenge-abc12345",
		"validate_work(challenge_id=",
		"query_pipeline_state",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered error %q missing substring %q", rendered, want)
		}
	}
}

// TestValidateWorkMissingChallengeIDErrorSurfacesActiveID locks the
// fix for the validate_work missing/wrong challenge_id error path.
// The pre-fix error said "challenge_id is required" with no
// reference to the actual active id, so an LLM that called
// validate_work blind had no way to recover except by guessing.
// The new typed error must carry the active id in both fields so
// the immediate retry can succeed.
func TestValidateWorkMissingChallengeIDErrorSurfacesActiveID(t *testing.T) {
	err := NewPipelineProtocolError(
		"pipeline.validate_work.missing_challenge_id",
		"validate_work",
		"validate_work requires challenge_id; the active pipeline challenge is \"task_2-challenge-abc12345\" from \"inspector-pipeline\"",
		"retry validate_work with challenge_id=\"task_2-challenge-abc12345\", requesting_agent=\"inspector-pipeline\", status:\"...\", summary:\"...\"",
	)
	rendered := err.Error()
	for _, want := range []string{
		"task_2-challenge-abc12345",
		"inspector-pipeline",
		"missing_challenge_id",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered error %q missing substring %q", rendered, want)
		}
	}
}
