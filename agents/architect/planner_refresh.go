package architect

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
