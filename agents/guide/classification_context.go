package guide

import (
	"context"
	"strings"
)

type classifierContextKey struct{}

// ClassificationContext carries dynamic routing context for LLM classification.
type ClassificationContext struct {
	SessionID               string
	ActiveConversationAgent string
	ActiveConversationTurns int
	ActiveConversationAge   int
	ActiveConversationScore float64
}

func withClassificationContext(ctx context.Context, value ClassificationContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized := normalizeClassificationContext(value)
	if !normalized.hasRuntimeSignals() {
		return ctx
	}
	return context.WithValue(ctx, classifierContextKey{}, normalized)
}

func classificationContextFromContext(ctx context.Context) (ClassificationContext, bool) {
	if ctx == nil {
		return ClassificationContext{}, false
	}
	value, ok := ctx.Value(classifierContextKey{}).(ClassificationContext)
	if !ok {
		return ClassificationContext{}, false
	}
	normalized := normalizeClassificationContext(value)
	return normalized, normalized.hasRuntimeSignals()
}

func classificationPromptRuntimeFromContext(ctx context.Context) *ClassificationPromptRuntime {
	value, ok := classificationContextFromContext(ctx)
	if !ok {
		return nil
	}
	return value.promptRuntime()
}

func normalizeClassificationContext(value ClassificationContext) ClassificationContext {
	value.SessionID = strings.TrimSpace(value.SessionID)
	value.ActiveConversationAgent = strings.TrimSpace(value.ActiveConversationAgent)
	if value.ActiveConversationTurns < 0 {
		value.ActiveConversationTurns = 0
	}
	if value.ActiveConversationAge < 0 {
		value.ActiveConversationAge = 0
	}
	if value.ActiveConversationScore < 0 {
		value.ActiveConversationScore = 0
	}
	if value.ActiveConversationScore > 1 {
		value.ActiveConversationScore = 1
	}
	return value
}

func (c ClassificationContext) hasRuntimeSignals() bool {
	return c.SessionID != "" || c.ActiveConversationAgent != ""
}

func (c ClassificationContext) promptRuntime() *ClassificationPromptRuntime {
	if !c.hasRuntimeSignals() {
		return nil
	}
	return &ClassificationPromptRuntime{
		SessionID:                c.SessionID,
		ActiveConversationAgent:  c.ActiveConversationAgent,
		ActiveConversationTurns:  c.ActiveConversationTurns,
		ActiveConversationAgeSec: c.ActiveConversationAge,
		ActiveConversationScore:  c.ActiveConversationScore,
	}
}
