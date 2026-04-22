package designer

import (
	"context"

	"github.com/adalundhe/sylk/core/claims"
)

type designerClaimsBoardContextKey struct{}

func withClaimsBoardContext(ctx context.Context, board *claims.ClaimsBoard) context.Context {
	if board == nil {
		return ctx
	}
	return context.WithValue(ctx, designerClaimsBoardContextKey{}, board)
}
