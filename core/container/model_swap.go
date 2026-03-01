package container

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/core/providers"
)

// ProviderType identifies which LLM provider a model belongs to.
type ProviderType string

const (
	ProviderAnthropic ProviderType = "anthropic"
	ProviderGoogle    ProviderType = "google"
	ProviderOpenAI    ProviderType = "openai"
)

// SwapMaxTokens is the default MaxTokens used when creating a provider for
// a cross-provider model swap. Derived from the bootstrap MaxTokens used by
// all agents in cmd/tui.go.
const SwapMaxTokens = 16384

// ProviderForModel returns the provider type for a model ID based on its prefix.
func ProviderForModel(modelID string) ProviderType {
	switch {
	case strings.HasPrefix(modelID, "claude-"):
		return ProviderAnthropic
	case strings.HasPrefix(modelID, "gemini-"):
		return ProviderGoogle
	case strings.HasPrefix(modelID, "gpt-"):
		return ProviderOpenAI
	default:
		return ""
	}
}

// ModelOption describes a single LLM model an agent supports.
type ModelOption struct {
	ID          string // Provider model ID (e.g. "claude-opus-4-6").
	DisplayName string // Human-friendly label (e.g. "Claude Opus 4.6").
}

// ModelSwappable is implemented by agents that support runtime LLM model
// switching. Agents may swap across providers (e.g. OpenAI → Anthropic).
//
// The model swapper in cmd/tui.go creates the provider with the correct
// gateway wrapper and passes it to SwapModel. Agents never construct
// providers internally — they receive fully-wrapped providers.
type ModelSwappable interface {
	// SwapModel installs provider as the agent's LLM backend and records
	// modelID as the active model. The provider is pre-constructed and
	// gateway-wrapped by the caller. Thread-safe.
	SwapModel(ctx context.Context, modelID string, provider providers.ProviderAdapter) error

	// CurrentModel returns the model ID currently in use.
	CurrentModel() string

	// SupportedModels returns the ordered list of models this agent can
	// switch between. The first entry is the default. The UI uses this
	// to populate the model selector per-agent.
	SupportedModels() []ModelOption
}
