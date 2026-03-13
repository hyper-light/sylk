package architect

import "strings"

func concreteAgentID(candidate, agentType string) string {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" || strings.EqualFold(trimmed, strings.TrimSpace(agentType)) {
		return ""
	}
	return trimmed
}

func (a *Architect) planHandoffTargetAgentID(plan *DesignPlan) string {
	if plan != nil && plan.PendingWork != nil {
		if targetAgentID := strings.TrimSpace(plan.PendingWork.TargetAgentID); targetAgentID != "" {
			return targetAgentID
		}
	}
	if targetAgentID := concreteAgentID(a.knownAgentIDByType("orchestrator", ""), "orchestrator"); targetAgentID != "" {
		return targetAgentID
	}
	return "orchestrator"
}

func (a *Architect) requirePlanHandoffTargetAgentID(
	plan *DesignPlan,
	_ string,
	_ string,
) (string, error) {
	return a.planHandoffTargetAgentID(plan), nil
}
