package container

import "context"

// AuthRefreshable is implemented by on-demand agents that can receive
// credential updates at runtime. DaemonSet agents (Guide, Orchestrator)
// subscribe directly to the bus topic instead.
//
// The container-walk refresher iterates the ContainerRegistry, type-asserts
// each agent to AuthRefreshable, and calls RefreshProvider for matching
// ProviderType.
type AuthRefreshable interface {
	// ProviderType returns the credential provider this agent depends on
	// (e.g. "google", "anthropic", "openai").
	ProviderType() string

	// RefreshProvider re-resolves credentials and replaces the LLM provider.
	// Returns nil on success or if the agent is already using valid credentials.
	RefreshProvider(ctx context.Context) error
}
