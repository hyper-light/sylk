package versioning

import (
	"context"
	"strings"
)

type sessionIDContextKey struct{}
type taskIDContextKey struct{}

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

// WithTaskID attaches the active Sylk task ID to a context so shared
// infrastructure can resolve task-scoped state.
func WithTaskID(ctx context.Context, taskID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ctx
	}
	return context.WithValue(ctx, taskIDContextKey{}, taskID)
}

// TaskIDFromContext returns the active Sylk task ID stored in ctx.
func TaskIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	taskID, _ := ctx.Value(taskIDContextKey{}).(string)
	return strings.TrimSpace(taskID)
}
