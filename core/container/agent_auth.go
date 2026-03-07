package container

import (
	"context"

	"github.com/adalundhe/sylk/core/providers"
)

// AuthRefreshable is implemented by on-demand agents that can receive
// credential updates at runtime. DaemonSet agents (Guide, Orchestrator)
// subscribe directly to the bus topic instead.
//
// The container-walk refresher iterates the ContainerRegistry, type-asserts
// each agent to AuthRefreshable, and calls RefreshProvider for matching
// ProviderType.
type AuthRefreshable interface {
	// ProviderType returns the credential provider this agent depends on
	// (e.g. "google", "anthropic", "openai"). Must reflect the current
	// model so the container walker routes credential events correctly.
	ProviderType() string

	// RefreshProvider re-resolves credentials and replaces the LLM provider.
	// authMethod is the canonical auth mode (e.g. "api_key", "oauth", "chatgpt")
	// from the AuthRegistry event. Returns nil on success or if the agent is
	// already using valid credentials.
	RefreshProvider(ctx context.Context, authMethod string) error
}

// ProviderRefresher creates a fresh provider for the given model and auth
// method, wrapped with the correct gateway. Set by cmd/tui.go at bootstrap
// so agents don't need gateway references.
type ProviderRefresher func(ctx context.Context, modelID, authMethod string) (providers.ProviderAdapter, error)
