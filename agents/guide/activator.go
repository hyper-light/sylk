package guide

import "context"

// AgentActivator provides on-demand agent activation capability.
// Implementations must be safe for concurrent use.
type AgentActivator interface {
	// EnsureActive guarantees the agent of the given type is running (TierHot).
	// Concurrent calls for the same type coalesce on a single activation.
	EnsureActive(ctx context.Context, agentType string) error

	// TouchActivity resets the idle timer for the given agent type,
	// preventing demotion during active conversation flow.
	TouchActivity(agentType string)
}

// noopActivator satisfies AgentActivator with no-ops.
// Used as a nil-safe default and in tests.
type noopActivator struct{}

func (noopActivator) EnsureActive(context.Context, string) error { return nil }
func (noopActivator) TouchActivity(string)                       {}

// NoopActivator returns an AgentActivator that does nothing.
func NoopActivator() AgentActivator { return noopActivator{} }
