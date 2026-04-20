package shared

import (
	"context"
	"strings"
	"testing"
)

// TestPipelineProtocolStatePreamble_PendingChallenge is the core
// fundamental-fix test: the preamble surfaces challenge_id +
// requesting_agent before the LLM makes any tool call, so the
// tester's finalize_pipeline target decision has all the
// information it needs at reasoning time. Without this, the LLM
// guesses the wrong target and the strict error fires.
func TestPipelineProtocolStatePreamble_PendingChallenge(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "task_2-challenge-abc12345",
			RequestingAgent: "inspector-pipeline",
		},
	})
	ctx := WithPipelineProtocolState(context.Background(), state)

	got := PipelineProtocolStatePreamble(ctx)
	for _, want := range []string{
		"[PIPELINE STATE]",
		"pending_challenge:",
		`id="task_2-challenge-abc12345"`,
		`from="inspector-pipeline"`,
		`finalize_pipeline MUST target "inspector-pipeline"`,
		`validate_work(challenge_id="task_2-challenge-abc12345", requesting_agent="inspector-pipeline"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("preamble %q missing substring %q", got, want)
		}
	}
}

// TestPipelineProtocolStatePreamble_NoStateReturnsEmpty covers the
// neutral case — a fresh turn with no pending challenge / no
// validation / no required action. The preamble should be empty so
// callers can concat it with a separator only when non-empty.
func TestPipelineProtocolStatePreamble_NoStateReturnsEmpty(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{})
	ctx := WithPipelineProtocolState(context.Background(), state)

	if got := PipelineProtocolStatePreamble(ctx); got != "" {
		t.Errorf("empty-state preamble = %q, want empty", got)
	}
}

// TestPipelineProtocolStatePreamble_NilContextReturnsEmpty protects
// the call-site pattern where the preamble is requested before the
// protocol state has been attached (edge case during bootstrap).
func TestPipelineProtocolStatePreamble_NilContextReturnsEmpty(t *testing.T) {
	if got := PipelineProtocolStatePreamble(context.Background()); got != "" {
		t.Errorf("no-state context preamble = %q, want empty", got)
	}
}

// TestPipelineProtocolStatePreamble_RequiredActionWithReason
// ensures the required_terminal_action line includes the reason so
// the LLM knows WHY the action is forced, not just WHAT it must be.
func TestPipelineProtocolStatePreamble_RequiredActionWithReason(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{})
	ctx := WithPipelineProtocolState(context.Background(), state)

	// Mutate the projection indirectly by attaching via the state's
	// required-action setter path — no direct snapshot mutation
	// because Snapshot() returns a clone. We construct a preamble-
	// only projection and assert against the renderer directly.
	line := renderRequiredActionLine(&PipelineProjection{
		RequiredAction:       "handoff_to_ot",
		RequiredActionReason: "finalize_pipeline locked ready_for_ot",
	})
	want := "required_terminal_action: handoff_to_ot — finalize_pipeline locked ready_for_ot"
	if line != want {
		t.Errorf("required-action line = %q, want %q", line, want)
	}

	// And an action-only line (no reason) also renders cleanly.
	onlyAction := renderRequiredActionLine(&PipelineProjection{
		RequiredAction: "validate_work",
	})
	if onlyAction != "required_terminal_action: validate_work" {
		t.Errorf("action-only line = %q", onlyAction)
	}

	_ = ctx
}

// TestPipelineProtocolStatePreamble_BothChallengeAndValidation
// documents the stacking order: challenge first, then pending
// validation, then required action. The LLM's reading order should
// match the protocol's priority order — challenge is always the
// most immediate concern.
func TestPipelineProtocolStatePreamble_BothChallengeAndValidation(t *testing.T) {
	proj := &PipelineProjection{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "c1",
			RequestingAgent: "inspector-pipeline",
		},
		PendingValidation: &PipelineValidationRecord{
			ChallengeID:     "c0",
			RespondingAgent: "tester-pipeline",
		},
		RequiredAction: "validate_work",
	}
	lines := collectPipelineStatePreambleLines(proj)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (challenge + validation + required)", len(lines))
	}
	if !strings.HasPrefix(lines[0], "pending_challenge") {
		t.Errorf("line[0] order wrong: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "pending_validation") {
		t.Errorf("line[1] order wrong: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "required_terminal_action") {
		t.Errorf("line[2] order wrong: %q", lines[2])
	}
}
