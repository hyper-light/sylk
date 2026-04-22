package pipeline

import (
	"context"

	"github.com/adalundhe/sylk/core/claims"
)

type claimsBoardContextKey struct{}

// withClaimsBoardContext stores the claims board on the context so
// skills and the tool loop can access it.
func withClaimsBoardContext(ctx context.Context, board *claims.ClaimsBoard) context.Context {
	if board == nil {
		return ctx
	}
	return context.WithValue(ctx, claimsBoardContextKey{}, board)
}

// claimsBoardFromContext retrieves the claims board from context.
func claimsBoardFromContext(ctx context.Context) *claims.ClaimsBoard {
	if ctx == nil {
		return nil
	}
	board, _ := ctx.Value(claimsBoardContextKey{}).(*claims.ClaimsBoard)
	return board
}
