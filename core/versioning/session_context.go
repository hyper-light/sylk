package versioning

import (
	"context"
	"strings"
)

type sessionIDContextKey struct{}

// WithSessionID attaches the active Sylk session ID to a context so shared
// infrastructure such as file-access routers can resolve session-scoped state.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
}

// SessionIDFromContext returns the active Sylk session ID stored in ctx.
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sessionID, _ := ctx.Value(sessionIDContextKey{}).(string)
	return strings.TrimSpace(sessionID)
}
