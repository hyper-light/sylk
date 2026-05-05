package runtime

import "context"

// Context keys for invocation metadata. Agents stamp these onto ctx before
// invoking a compiled MCP tool's handler so the runtime can pull them out
// without redesigning the skill handler signature.
type ctxKey int

const (
	ctxKeyAgentID ctxKey = iota + 1
	ctxKeyCorrelationID
	ctxKeyCapabilityScope
)

// WithAgentID stamps the invoking agent id onto ctx.
func WithAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, ctxKeyAgentID, agentID)
}

// WithCorrelationID stamps the correlation id onto ctx.
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, ctxKeyCorrelationID, correlationID)
}

// WithCapabilityScope stamps the capability scope onto ctx.
func WithCapabilityScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, ctxKeyCapabilityScope, scope)
}

// agentIDFromContext returns the value stamped on ctx by WithAgentID, or
// "agent" as a sentinel placeholder. The placeholder is used only when an
// agent forgot to stamp the id; the runtime still records the operation.
func agentIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyAgentID).(string); ok && v != "" {
		return v
	}
	return "agent"
}

func correlationIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyCorrelationID).(string); ok && v != "" {
		return v
	}
	return ""
}

func capabilityScopeFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyCapabilityScope).(string); ok && v != "" {
		return v
	}
	return ""
}
