package architect

import "context"

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
// the next call re-resolves credentials.
func (a *Architect) RefreshProvider(_ context.Context) error {
	a.RefreshPlannerAuth()
	return nil
}
