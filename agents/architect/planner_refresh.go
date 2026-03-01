package architect

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/providers"
)

// RefreshPlannerAuth clears cached planner state so the next planning call
// re-resolves credentials from secure storage/environment.
func (a *Architect) RefreshPlannerAuth() {
	if a == nil {
		return
	}
	a.plannerMu.Lock()
	a.config.AnthropicAPIKey = ""
	a.planner = nil
	a.plannerMu.Unlock()
}

// PlannerAvailable reports whether the Anthropic planner is initialized.
func (a *Architect) PlannerAvailable() bool {
	if a == nil {
		return false
	}
	return a.currentPlanner() != nil
}

// ProviderType implements container.AuthRefreshable.
func (a *Architect) ProviderType() string { return "anthropic" }

// RefreshProvider implements container.AuthRefreshable.
// Delegates to RefreshPlannerAuth which clears the cached planner so
// the next call re-resolves credentials. The authMethod parameter is
// accepted for interface compliance; the planner lazy-resolves its
// own credentials on next use.
func (a *Architect) RefreshProvider(_ context.Context, _ string) error {
	a.RefreshPlannerAuth()
	return nil
}

// SwapModel implements container.ModelSwappable.
// Builds a new planner around the pre-built, gateway-wrapped provider and
// installs it. Thread-safe via plannerMu.
func (a *Architect) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	if a == nil {
		return nil
	}
	sp, ok := provider.(plannerStreamProvider)
	if !ok {
		return fmt.Errorf("architect swap model: provider does not satisfy plannerStreamProvider")
	}
	planner := newPlannerFromProvider(sp, a.config, a.logger)
	a.plannerMu.Lock()
	a.config.Model = modelID
	a.planner = planner
	a.plannerMu.Unlock()
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (a *Architect) CurrentModel() string {
	if a == nil {
		return ""
	}
	a.plannerMu.RLock()
	defer a.plannerMu.RUnlock()
	return a.config.Model
}

// SupportedModels implements container.ModelSwappable.
func (a *Architect) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
		{ID: "gpt-5.3-codex", DisplayName: "GPT-5.3 Codex"},
	}
}
