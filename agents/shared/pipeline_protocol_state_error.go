package shared

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// enrichPipelineProtocolError wraps a delegate's returned error with the
// current pipeline protocol state snapshot and the set of actions that
// would pass their state preconditions against that snapshot. The goal:
// when the LLM invokes `pipeline_protocol(action=X)` and X is rejected
// because the pipeline isn't in the right state, the error tells the
// model exactly what state the pipeline IS in AND which actions ARE
// legal right now — so the next turn can recover without having to
// query_pipeline_state separately.
//
// Only ProtocolError (scope=pipeline) rejections are enriched. Bare
// fmt.Errorf errors pass through untouched because those are either
// system faults (state not available, missing callback) that the LLM
// cannot recover from, or schema errors already caught by the façade's
// pre-validation step. Either way, appending a state snapshot to them
// would be noise.
//
// Invariant: the returned error wraps the original so `errors.As` still
// recovers the ProtocolError, and `errors.Is(err, ErrProtocolViolation)`
// still succeeds. Callers that type-assert on ProtocolError keep
// working.
func enrichPipelineProtocolError(
	ctx context.Context,
	cfg PipelineProtocolSkillConfig,
	attemptedAction string,
	err error,
) error {
	if err == nil {
		return nil
	}
	pe, ok := AsProtocolError(err)
	if !ok || pe.Scope != "pipeline" {
		return err
	}
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return err
	}
	snapshot := state.Snapshot()
	if snapshot == nil {
		return err
	}
	stateSummary := summarizePipelineStateForError(snapshot)
	legal := legalPipelineProtocolActions(snapshot, cfg)
	augmented := *pe
	augmented.RecoveryAction = appendStateSectionsToRecovery(
		pe.RecoveryAction,
		stateSummary,
		legal,
		attemptedAction,
	)
	return &augmented
}

// summarizePipelineStateForError renders a compact multi-line summary of
// the pipeline state snapshot focused on the fields that determine which
// action is legal: pending challenge, pending validation response,
// required terminal action. Fields that don't affect action legality
// (audit lock phase, roster, recent events) are elided so the error
// stays readable.
func summarizePipelineStateForError(snapshot *PipelineProtocolSnapshot) string {
	var sb strings.Builder
	sb.WriteString("\nCurrent pipeline state:")
	if snapshot.PendingChallenge == nil {
		sb.WriteString("\n  pending_challenge: none")
	} else {
		challenge := snapshot.PendingChallenge
		fmt.Fprintf(&sb, "\n  pending_challenge: id=%s requester=%s targets=%v",
			strings.TrimSpace(challenge.ID),
			strings.TrimSpace(challenge.RequestingAgent),
			challenge.TargetAgents)
	}
	if snapshot.PendingValidation == nil {
		sb.WriteString("\n  pending_validation: none")
	} else {
		fmt.Fprintf(&sb, "\n  pending_validation: challenge_id=%s from=%s",
			strings.TrimSpace(snapshot.PendingValidation.ChallengeID),
			strings.TrimSpace(snapshot.PendingValidation.RespondingAgent))
	}
	if snapshot.CurrentRequest != "" {
		fmt.Fprintf(&sb, "\n  current_request: %s", truncatePipelineStateValue(snapshot.CurrentRequest, 160))
	}
	if snapshot.Mode != "" {
		fmt.Fprintf(&sb, "\n  mode: %s", snapshot.Mode)
	}
	return sb.String()
}

// truncatePipelineStateValue shortens long values for error display so
// the enriched error doesn't blow out a single LLM turn budget.
func truncatePipelineStateValue(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// legalPipelineProtocolActions enumerates the pipeline_protocol actions
// that would pass their state preconditions against the given snapshot
// and the current façade configuration. The list mirrors the handler-
// side state checks (`PendingChallenge != nil` for validate,
// `PendingValidation != nil` for process_validation, etc.) so the LLM's
// "what can I do right now?" answer is consistent with what the
// delegates would actually accept.
//
// The returned list is sorted to give a stable error payload.
func legalPipelineProtocolActions(
	snapshot *PipelineProtocolSnapshot,
	cfg PipelineProtocolSkillConfig,
) []string {
	if snapshot == nil {
		return nil
	}
	legal := map[string]struct{}{}
	// validate: requires PendingChallenge.
	if snapshot.PendingChallenge != nil {
		legal["validate"] = struct{}{}
	}
	// process_validation: requires PendingValidation.
	if snapshot.PendingValidation != nil {
		legal["process_validation"] = struct{}{}
	}
	// challenge: can always be raised if there's no pending
	// challenge awaiting the caller's response (i.e., the caller
	// isn't already in debt to someone else). A pending validation
	// response from a peer to this agent doesn't block issuing a
	// new challenge.
	if snapshot.PendingChallenge == nil {
		legal["challenge"] = struct{}{}
	}
	// handoff: ordinary phase progression. Blocked when the caller
	// owes a validation response (PendingChallenge != nil) — the
	// caller should validate first and let the protocol dispatch
	// the next owner.
	if snapshot.PendingChallenge == nil {
		legal["handoff"] = struct{}{}
	}
	// finalize: available only when this agent has a finalize variant.
	// The specific finalize (inspector OT vs tester verification) has
	// additional internal preconditions that the handler enforces;
	// we list it as legal here to avoid silently hiding the option
	// from the LLM when one of the finer-grained checks later rejects.
	if cfg.InspectorOT || cfg.TesterFinalize != nil {
		legal["finalize"] = struct{}{}
	}
	out := make([]string, 0, len(legal))
	for action := range legal {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

// appendStateSectionsToRecovery returns a RecoveryAction string that
// contains the delegate's original recovery hint (if any), followed by
// the current-state summary, followed by a "currently legal actions"
// line. The sections are separated by blank lines and terminated with a
// call-to-action pointing at query_pipeline_state so the LLM knows how
// to re-inspect state before its next attempt.
func appendStateSectionsToRecovery(
	original string,
	stateSummary string,
	legal []string,
	attemptedAction string,
) string {
	var sb strings.Builder
	trimmed := strings.TrimSpace(original)
	if trimmed != "" {
		sb.WriteString(trimmed)
	}
	sb.WriteString(stateSummary)
	if len(legal) > 0 {
		fmt.Fprintf(&sb, "\nCurrently legal actions: %s", strings.Join(legal, ", "))
		attempted := strings.TrimSpace(attemptedAction)
		if attempted != "" {
			inLegal := false
			for _, a := range legal {
				if a == attempted {
					inLegal = true
					break
				}
			}
			if !inLegal {
				fmt.Fprintf(&sb, " (action=%s is NOT in this list — pick one of the listed actions)", attempted)
			}
		}
	}
	sb.WriteString("\nUse query_pipeline_state to re-inspect the authoritative protocol state before choosing your next action.")
	return sb.String()
}
