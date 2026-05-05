package shared

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
)

// boardAwareHandoffGuard is the canonical activity.HandoffGuard
// implementation: it looks up the session's claims board and runs
// claims.HandoffEligible against the publishing agent. Returns the
// typed *HandoffNotEligibleError on rejection so callers can inspect
// the offending claims for diagnostics.
//
// UI_DESIGN.md §4.7.1 — Fabric publish-side handoff guard.
type boardAwareHandoffGuard struct {
	registry *claims.SessionBoardRegistry
}

// InstallActivityHandoffGuard registers the canonical handoff guard
// against the activity package. Returns the previous guard so callers
// can defer-restore in tests.
func InstallActivityHandoffGuard(registry *claims.SessionBoardRegistry) activity.HandoffGuard {
	g := &boardAwareHandoffGuard{registry: registry}
	return activity.SetHandoffGuard(g)
}

// Verify implements activity.HandoffGuard.
func (g *boardAwareHandoffGuard) Verify(ctx context.Context, agentID, sessionID, predecessorClaimID string) error {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" || sessionID == "" || g == nil || g.registry == nil {
		// Insufficient identity to enforce — allow propagation. The
		// board guard catches the same class of bypass via
		// PostAction(ActionTypeHandoff). Defensive.
		return nil
	}
	board := g.registry.Lookup(sessionID)
	if board == nil {
		return nil
	}
	if err := claims.HandoffEligible(board, agentID); err != nil {
		// Surface the rejection back to the publishing agent's
		// accumulator (when one is on ctx) as a structured artifact.
		// The activity itself is dropped at the Append site; this
		// breadcrumb makes the rejection observable in the agent's
		// testament instead of vanishing silently.
		recordHandoffRejectionArtifact(ctx, agentID, sessionID, predecessorClaimID, err)
		var nee *claims.HandoffNotEligibleError
		if errors.As(err, &nee) {
			slog.Warn("handoff_publish_guard_rejected",
				"agent_id", agentID, "session_id", sessionID,
				"predecessor", predecessorClaimID,
				"open_child_claims", nee.OpenChildClaims,
				"open_as_subject", nee.OpenAsSubjectFromIssuers,
				"in_flight_artifacts", nee.InFlightStartedArtifacts,
			)
		} else {
			slog.Warn("handoff_publish_guard_rejected_untyped", "agent_id", agentID, "error", err.Error())
		}
		return err
	}
	return nil
}

// recordHandoffRejectionArtifact appends a structured rejection
// artifact onto the publishing agent's accumulator (if one is on
// ctx). UI_DESIGN.md §4.7.1 — the rejection MUST be observable to
// the agent so it can correct course rather than silently failing.
func recordHandoffRejectionArtifact(ctx context.Context, agentID, sessionID, predecessorClaimID string, err error) {
	if ctx == nil {
		return
	}
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return
	}
	acc.RecordArtifact(&claims.Artifact{
		AgentID: agentID,
		Kind:    "handoff_rejected_by_publish_guard",
		Reference: predecessorClaimID,
		Metadata: map[string]any{
			"agent_id":    agentID,
			"session_id":  sessionID,
			"predecessor": predecessorClaimID,
			"error":       err.Error(),
		},
		Ephemeral: true,
	})
}
