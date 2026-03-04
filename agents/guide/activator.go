package guide

import "context"

// PodActivator provides on-demand pod activation capability.
// Implementations must be safe for concurrent use.
type PodActivator interface {
	// EnsurePodActive guarantees the pod is at TierHot.
	// Concurrent calls for the same pod coalesce on a single activation.
	EnsurePodActive(ctx context.Context, podID string) error

	// TouchPodActivity resets the idle timer for the given pod,
	// preventing demotion during active conversation flow.
	TouchPodActivity(podID string)

	// HoldPodActive activates the pod and acquires a demotion guard.
	// Returns an idempotent release function.
	HoldPodActive(ctx context.Context, podID string) (func(), error)

	// PodForAgent resolves an agent type to its owning pod ID.
	// Returns the agentType itself if no mapping exists (singleton pods).
	PodForAgent(agentType string) string
}

// noopPodActivator satisfies PodActivator with no-ops.
type noopPodActivator struct{}

func (noopPodActivator) EnsurePodActive(context.Context, string) error         { return nil }
func (noopPodActivator) TouchPodActivity(string)                               {}
func (noopPodActivator) HoldPodActive(context.Context, string) (func(), error) { return func() {}, nil }
func (noopPodActivator) PodForAgent(agentType string) string                   { return agentType }

// NoopPodActivator returns a PodActivator that does nothing.
func NoopPodActivator() PodActivator { return noopPodActivator{} }

// AgentActivator is the legacy interface alias. Deprecated: use PodActivator.
type AgentActivator = PodActivator

// NoopActivator returns a legacy-compatible no-op activator.
// Deprecated: use NoopPodActivator.
func NoopActivator() AgentActivator { return NoopPodActivator() }
