package shared

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// LLMCallContext identifies the LLM round-trip currently running on
// ctx. Mirrors the ActiveToolCallContext pattern so that every
// tool-call record emitted *during* an LLM round-trip can capture the
// parent llm_call_id — the crucial link for reconstructing "which
// model response generated this action" during debugging.
type LLMCallContext struct {
	ID        string
	Model     string
	Provider  string
	AgentID   string
	AgentType string
}

type llmCallContextKey struct{}

// WithLLMCall stamps an LLMCallContext onto ctx. If the provided ID is
// empty, a UUID-prefixed "llm-" ID is generated so downstream records
// always have a non-empty key to emit. The returned context should be
// passed to every emit-side call (tool dispatch, stream publication)
// that happens inside the LLM round-trip.
func WithLLMCall(ctx context.Context, call LLMCallContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(call.ID) == "" {
		call.ID = "llm-" + uuid.NewString()[:12]
	}
	return context.WithValue(ctx, llmCallContextKey{}, call)
}

// LLMCallFromContext returns the LLM round-trip currently active on
// ctx, or (zero, false) if none. The boolean return distinguishes a
// missing context value from a present-but-empty one.
func LLMCallFromContext(ctx context.Context) (LLMCallContext, bool) {
	if ctx == nil {
		return LLMCallContext{}, false
	}
	call, ok := ctx.Value(llmCallContextKey{}).(LLMCallContext)
	if !ok || strings.TrimSpace(call.ID) == "" {
		return LLMCallContext{}, false
	}
	return call, true
}

// LLMCallIDFromContext is a convenience accessor for the most common
// caller need: just the ID for stamping onto a log record. Returns ""
// when no call is active.
func LLMCallIDFromContext(ctx context.Context) string {
	call, ok := LLMCallFromContext(ctx)
	if !ok {
		return ""
	}
	return call.ID
}
