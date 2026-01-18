package tools

import (
	"context"
	"sync"
)

// Context keys for extracting agent and session IDs from context.
type executorContextKey struct{ name string }

var (
	// AgentIDKey is the context key for the agent ID.
	AgentIDKey = executorContextKey{name: "agentID"}
	// SessionIDKey is the context key for the session ID.
	SessionIDKey = executorContextKey{name: "sessionID"}
)

// ToolSignalEmitter defines the interface for emitting tool completion signals.
// This interface allows for mockery compatibility and dependency injection.
type ToolSignalEmitter interface {
	EmitToolCompleted(agentID, sessionID, toolName, target string)
}

// signalEmitterHolder holds the signal emitter with thread-safe access.
type signalEmitterHolder struct {
	mu      sync.RWMutex
	emitter ToolSignalEmitter
}

// SetSignalEmitter sets the signal emitter for the ToolExecutor.
// This method is thread-safe and can be called at any time.
func (e *ToolExecutor) SetSignalEmitter(emitter ToolSignalEmitter) {
	if e.signalHolder == nil {
		e.signalHolder = &signalEmitterHolder{}
	}
	e.signalHolder.mu.Lock()
	e.signalHolder.emitter = emitter
	e.signalHolder.mu.Unlock()
}

// GetSignalEmitter returns the current signal emitter.
// Returns nil if no emitter is set. This method is thread-safe.
func (e *ToolExecutor) GetSignalEmitter() ToolSignalEmitter {
	if e.signalHolder == nil {
		return nil
	}
	e.signalHolder.mu.RLock()
	emitter := e.signalHolder.emitter
	e.signalHolder.mu.RUnlock()
	return emitter
}

// emitToolSignal emits a tool completion signal if a signal emitter is configured.
// This is a no-op if no emitter is set, making it safe to call unconditionally.
func (e *ToolExecutor) emitToolSignal(ctx context.Context, inv ToolInvocation, result *ToolResult) {
	emitter := e.GetSignalEmitter()
	if emitter == nil {
		return
	}

	agentID, sessionID := extractContextIDs(ctx)
	toolName, target := extractToolInfo(inv)

	emitter.EmitToolCompleted(agentID, sessionID, toolName, target)
}

// extractContextIDs extracts agent and session IDs from the context.
// Returns empty strings if the values are not present or not strings.
func extractContextIDs(ctx context.Context) (agentID, sessionID string) {
	agentID = extractStringFromContext(ctx, AgentIDKey)
	sessionID = extractStringFromContext(ctx, SessionIDKey)
	return agentID, sessionID
}

// extractStringFromContext safely extracts a string value from context.
// Returns empty string if the value is not present or not a string.
func extractStringFromContext(ctx context.Context, key executorContextKey) string {
	val := ctx.Value(key)
	if val == nil {
		return ""
	}
	str, ok := val.(string)
	if !ok {
		return ""
	}
	return str
}

// extractToolInfo extracts the tool name and target from a ToolInvocation.
// Falls back to command as tool name and working directory as target if not specified.
func extractToolInfo(inv ToolInvocation) (toolName, target string) {
	toolName = inv.Tool
	if toolName == "" {
		toolName = inv.Command
	}

	target = inv.WorkingDir
	if target == "" && len(inv.Args) > 0 {
		target = inv.Args[0]
	}

	return toolName, target
}

// ExecuteWithSignals executes a tool invocation and emits a completion signal.
// This wraps the standard Execute method to add signal emission.
// The signal is emitted regardless of whether the execution succeeded or failed.
func (e *ToolExecutor) ExecuteWithSignals(ctx context.Context, inv ToolInvocation) (*ToolResult, error) {
	result, err := e.Execute(ctx, inv)

	// Emit signal regardless of success/failure
	// Use a nil-safe result for signal emission
	e.emitToolSignal(ctx, inv, result)

	return result, err
}

// WithAgentID returns a context with the agent ID set.
func WithAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, AgentIDKey, agentID)
}

// WithSessionID returns a context with the session ID set.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, SessionIDKey, sessionID)
}

// WithExecutorContext returns a context with both agent and session IDs set.
func WithExecutorContext(ctx context.Context, agentID, sessionID string) context.Context {
	ctx = WithAgentID(ctx, agentID)
	ctx = WithSessionID(ctx, sessionID)
	return ctx
}
