package shared

import (
	"context"

	"github.com/adalundhe/sylk/core/claims"
)

// TurnAccumulator wraps an interactive-turn handler with a
// TestamentAccumulator so the agent's final response is captured on
// the claims board the same way a claim-triggered turn is. Returns
// the augmented ctx and a flush function the caller defers:
//
//	ctx, flush := shared.TurnAccumulator(ctx, agentID, sessionID, board, scope)
//	defer flush()
//
// The board is the canonical record of every meaningful agent action.
// Without this wrap, a forwarded bus request runs through
// ExecuteTurnLoop, produces an answer, replies on the bus — and
// emits zero testaments. Every downstream consumer (TUI tree,
// archivalist persistence, audit, validation reconciliation) then
// loses the signal and has to bolt on side channels to compensate.
//
// Idempotent across nested turns: when ctx already carries an
// accumulator (e.g. an interactive handler that delegates to a
// claim-driven sub-turn, or a re-entrant dispatcher), the existing
// accumulator continues to receive observations and the outer
// caller owns the flush. The returned cleanup is a no-op in that
// case so deferred flushes don't double up.
//
// Identity inheritance: when ctx carries a parent claim ID (via
// claims.WithParentClaimID, which shared.WireClaimsIntake stamps for
// claim-driven dispatches), a new accumulator created here inherits
// the claim ID automatically — making the testament's
// RelationshipClaim relation tie back to the originating claim
// without each call site having to thread the ID through.
func TurnAccumulator(
	ctx context.Context,
	agentID, sessionID string,
	board *claims.ClaimsBoard,
	scope claims.ScopeProvider,
) (context.Context, func()) {
	if claims.AccumulatorFromContext(ctx) != nil {
		return ctx, func() {}
	}
	acc := claims.NewTestamentAccumulator(agentID, sessionID)
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	return ctx, func() {
		acc.Flush(ctx, board, scope)
	}
}
