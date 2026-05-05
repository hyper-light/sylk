package shared

import (
	"context"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

// AgentActivityState is the canonical taxonomy of agent activity
// states an agent narrates explicitly from its tool loop.
//
// Per docs/CLAIMS_UI.md, the rich, responsive UI sylk-clone delivers
// comes from agents pushing transitions to the bus from their tool
// loops — not from passive instrumentation. Same principle applies to
// the claims plane: every transition is an explicit RecordAgentState
// call by the agent, writing both the claim's mutable Context (the
// "what's now" handle) and an agent_state artifact (the immutable
// transition trace).
//
// The taxonomy mirrors sylk-clone's guide.AgentActivityState set
// adapted to claim-relative semantics. Adding new states is fine —
// keep the value short, lowercase-with-underscores, and document
// what the agent should be doing in that state.
type AgentActivityState string

const (
	// Reasoning: the agent is thinking — between tool calls, during
	// LLM dispatch, post-tool synthesis. Default state for "the
	// agent is busy but not waiting on a peer".
	AgentStateReasoning AgentActivityState = "reasoning"

	// ToolExecuting: a local tool call is in flight. Detail names
	// the tool ("running rg --files", "applying patch to foo.go").
	AgentStateToolExecuting AgentActivityState = "tool_executing"

	// DispatchingToPeer: a peer dispatch is being prepared / sent.
	// Use immediately before the actual transport call. Pairs with
	// PeerRef carrying the target.
	AgentStateDispatchingToPeer AgentActivityState = "dispatching_to_peer"

	// AwaitingPeerResponse: the agent is parked waiting for a peer's
	// response (sync wait or yielded continuation). Pairs with
	// PeerRef. Persists until Receiving fires.
	AgentStateAwaitingPeerResponse AgentActivityState = "awaiting_peer_response"

	// ConsultingPeer: shorthand for "in the middle of a consult"
	// when finer DispatchingToPeer/AwaitingPeerResponse distinction
	// isn't useful at the call site.
	AgentStateConsultingPeer AgentActivityState = "consulting_peer"

	// ChallengingPeer: shorthand for "in the middle of a challenge".
	AgentStateChallengingPeer AgentActivityState = "challenging_peer"

	// AwaitingGuardian: tool runtime is waiting for a guardian-check
	// grant. Pairs with PeerRef pointing at the guardian.
	AgentStateAwaitingGuardian AgentActivityState = "awaiting_guardian"

	// Receiving: a peer's response (or guardian's grant) just
	// arrived; the agent is integrating it before continuing.
	AgentStateReceiving AgentActivityState = "receiving"

	// Synthesizing: the agent is composing its final output —
	// drafting plan text, writing testament summary, finalizing
	// response. Often paired with concurrent
	// accumulator.SetContext() updates that narrate the developing
	// conclusion.
	AgentStateSynthesizing AgentActivityState = "synthesizing"

	// Complete: the agent has finished its work for this claim.
	// Terminal. Sealed via the claim's status transition.
	AgentStateComplete AgentActivityState = "complete"

	// Errored: the agent's work for this claim failed. Detail
	// carries the error summary. Terminal.
	AgentStateErrored AgentActivityState = "errored"
)

// PeerRef identifies the peer involved in a state transition. Optional
// — only set when the State is ConsultingPeer / DispatchingToPeer /
// AwaitingPeerResponse / ChallengingPeer / AwaitingGuardian /
// Receiving (when the source is a peer).
//
// AgentType is the slug ("librarian", "guardian", "tester-pipeline").
// CorrelationID is the peer's request correlation if known. ClaimID
// is the peer-side claim being processed if known.
type PeerRef struct {
	AgentType     string
	CorrelationID string
	ClaimID       string
}

// RecordAgentState is the canonical entry point every agent uses to
// narrate a state transition. It writes BOTH:
//
//  1. board.SetClaimContext(claimID, detail) — replace-in-place
//     mutable narrative ("what is this claim's owner doing right
//     now?"). UI consumes the resulting ClaimContextDelta to refresh
//     the row's status text without creating a new row.
//
//  2. acc.RecordArtifact(Kind=ArtifactKindAgentState, Reference=detail,
//     Metadata={state, peer_*, at}) — immutable transition history,
//     attached to the in-flight testament. Replayable for audit /
//     time-travel debugging. Renders in detail-view as a chronological
//     transition trace.
//
// Agents should call this from their tool loops at every transition
// — entry, before each tool, before each peer dispatch, during waits,
// on resume, during synthesis, on terminal. See docs/CLAIMS_UI.md
// "Agent narration discipline" for the canonical push points.
//
// All arguments are best-effort — missing board, missing accumulator,
// missing claimID just skip the corresponding write. Detail is the
// human-readable narrative ("Awaiting librarian response"); state is
// the categorical kind for UI rendering / replay; peer is optional.
func RecordAgentState(
	ctx context.Context,
	board *claims.ClaimsBoard,
	claimID string,
	detail string,
	state AgentActivityState,
	peer *PeerRef,
) {
	claimID = strings.TrimSpace(claimID)
	detail = strings.TrimSpace(detail)

	// Update the claim's mutable Context — the "what's now" handle.
	if board != nil && claimID != "" {
		_ = board.SetClaimContext(ctx, claimID, detail)
	}

	// Append an agent_state artifact to the in-flight accumulator —
	// the immutable transition history.
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return
	}
	metadata := map[string]any{
		"state": string(state),
		"at":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if peer != nil {
		if v := strings.TrimSpace(peer.AgentType); v != "" {
			metadata["peer_agent_type"] = v
		}
		if v := strings.TrimSpace(peer.CorrelationID); v != "" {
			metadata["peer_correlation_id"] = v
		}
		if v := strings.TrimSpace(peer.ClaimID); v != "" {
			metadata["peer_claim_id"] = v
		}
	}
	if claimID != "" {
		metadata["claim_id"] = claimID
	}
	acc.RecordArtifact(&claims.Artifact{
		ID:        uuid.NewString(),
		AgentID:   acc.AgentID(),
		SessionID: acc.SessionID(),
		Kind:      claims.ArtifactKindAgentState,
		Reference: detail,
		Metadata:  metadata,
		Ephemeral: false,
	})
}

// RecordResumeReceiving pushes a Receiving state on the issuer's
// claim summarizing the resumed peer-consult/challenge/guardian
// results. Called by each agent's ResumeFn right after accumulator
// restoration but before tool-loop re-entry. The push narrates
// "Received from <peer>" so the UI's row status text reflects the
// resume even before the resumed LLM turn produces visible output.
//
// Best-effort — no-ops on missing snapshot, board, or claim
// attribution. The peer summary aggregates across `results`: for a
// single resolution, names the responder; for multiple, summarizes
// the count.
func RecordResumeReceiving(
	ctx context.Context,
	board *claims.ClaimsBoard,
	snapshot *TurnSnapshot,
	results map[string]*claims.ConsultResolvedDelta,
) {
	if snapshot == nil || board == nil {
		return
	}
	claimID := strings.TrimSpace(snapshot.AccumulatorState.ClaimID)
	if claimID == "" {
		return
	}
	detail := summarizeResumeDetail(results)
	var peer *PeerRef
	if len(results) == 1 {
		for _, r := range results {
			if r == nil {
				continue
			}
			peer = &PeerRef{AgentType: strings.TrimSpace(r.ResponderAgentID)}
			break
		}
	}
	RecordAgentState(ctx, board, claimID, detail, AgentStateReceiving, peer)
}

func summarizeResumeDetail(results map[string]*claims.ConsultResolvedDelta) string {
	if len(results) == 0 {
		return "Resuming"
	}
	if len(results) == 1 {
		for _, r := range results {
			if r != nil && strings.TrimSpace(r.ResponderAgentID) != "" {
				return "Received from " + r.ResponderAgentID
			}
		}
		return "Received peer response"
	}
	count := len(results)
	if count == 2 {
		return "Received 2 peer responses"
	}
	return "Received multiple peer responses"
}
