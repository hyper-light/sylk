package shared

import (
	"context"
	"fmt"
	"strings"
)

// PipelineProtocolStatePreamble projects the relevant slice of the
// current pipeline protocol state into a prose block the LLM can
// read at reasoning time.
//
// Why this exists: `PipelineProtocolStateFromContext(ctx)` only
// surfaces inside skill handlers. The LLM, deciding which skill to
// call, does NOT see it. For challenge-driven decisions (the
// tester's finalize_pipeline target, the inspector's post-validation
// choice) the LLM needs to know the pending challenge's ID and
// RequestingAgent BEFORE it calls the skill — otherwise it applies
// general-world reasoning and picks the wrong target.
//
// The preamble is deliberately terse. It names only the protocol-
// actionable state (pending challenge, pending validation, required
// action) and formats each line in a way the LLM can pattern-match
// against the recovery-action strings in typed errors, so the
// language is consistent between "what you're supposed to do"
// guidance and "what went wrong" diagnostics.
//
// Returns an empty string when no state is attached OR when the
// state is at its neutral starting point (no pending challenge, no
// pending validation, no required action). Callers should concat
// the preamble with a separator only when non-empty.
func PipelineProtocolStatePreamble(ctx context.Context) string {
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return ""
	}
	proj := state.Projection()
	if proj == nil {
		return ""
	}
	lines := collectPipelineStatePreambleLines(proj)
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[PIPELINE STATE]\n")
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

// collectPipelineStatePreambleLines renders the three state
// dimensions the LLM actually needs to see. Kept as a separate
// helper so tests can exercise the content rules without touching
// the ctx plumbing.
func collectPipelineStatePreambleLines(proj *PipelineProjection) []string {
	var lines []string
	if line := renderPendingChallengeLine(proj); line != "" {
		lines = append(lines, line)
	}
	if line := renderPendingValidationLine(proj); line != "" {
		lines = append(lines, line)
	}
	if line := renderRequiredActionLine(proj); line != "" {
		lines = append(lines, line)
	}
	return lines
}

// renderPendingChallengeLine surfaces the active challenge the LLM
// must answer. This is THE line that prevents the
// challenge_target_mismatch bug — the tester's finalize_pipeline
// target must be the RequestingAgent named here.
func renderPendingChallengeLine(proj *PipelineProjection) string {
	if proj == nil || proj.PendingChallenge == nil {
		return ""
	}
	id := strings.TrimSpace(proj.PendingChallenge.ID)
	requester := strings.TrimSpace(proj.PendingChallenge.RequestingAgent)
	if id == "" || requester == "" {
		return ""
	}
	return fmt.Sprintf(
		"pending_challenge: id=%q from=%q — finalize_pipeline MUST target %q, then end the turn with validate_work(challenge_id=%q, requesting_agent=%q, status:\"...\", summary:\"...\")",
		id, requester, requester, id, requester,
	)
}

// renderPendingValidationLine names an outstanding validation the
// LLM is expected to process (inspector-pipeline after tester
// answers a challenge).
func renderPendingValidationLine(proj *PipelineProjection) string {
	if proj == nil || proj.PendingValidation == nil {
		return ""
	}
	challengeID := strings.TrimSpace(proj.PendingValidation.ChallengeID)
	responder := strings.TrimSpace(proj.PendingValidation.RespondingAgent)
	if challengeID == "" && responder == "" {
		return ""
	}
	return fmt.Sprintf(
		"pending_validation: challenge_id=%q responder=%q — process_validation is the required terminal action",
		challengeID, responder,
	)
}

// renderRequiredActionLine echoes any pipeline-level required
// action that a validation or ready-for-ot step has locked in.
// Not every turn has one; when present the LLM should treat it as
// the only legal terminal action for this turn.
func renderRequiredActionLine(proj *PipelineProjection) string {
	if proj == nil {
		return ""
	}
	action := strings.TrimSpace(proj.RequiredAction)
	reason := strings.TrimSpace(proj.RequiredActionReason)
	if action == "" {
		return ""
	}
	if reason == "" {
		return fmt.Sprintf("required_terminal_action: %s", action)
	}
	return fmt.Sprintf("required_terminal_action: %s — %s", action, reason)
}
