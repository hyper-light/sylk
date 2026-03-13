package orchestrator

import "strings"

func concreteAgentID(candidate, agentType string) string {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" || strings.EqualFold(trimmed, strings.TrimSpace(agentType)) {
		return ""
	}
	return trimmed
}

func (o *Orchestrator) knownConcreteAgentIDByType(agentType string) string {
	if o == nil {
		return ""
	}
	targetType := strings.TrimSpace(agentType)
	if targetType == "" {
		return ""
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, ann := range o.knownAgents {
		if ann == nil || ann.AgentType != targetType {
			continue
		}
		if agentID := concreteAgentID(ann.AgentID, targetType); agentID != "" {
			return agentID
		}
	}
	return ""
}

func (o *Orchestrator) resolveArchitectTargetAgentID(
	receipt *PlanHandoffReceiptRecord,
	sourceAgentID string,
) string {
	if agentID := strings.TrimSpace(sourceAgentID); agentID != "" {
		return agentID
	}
	if receipt != nil {
		if agentID := strings.TrimSpace(receipt.RequesterAgentID); agentID != "" {
			return agentID
		}
	}
	if agentID := o.knownConcreteAgentIDByType("architect"); agentID != "" {
		return agentID
	}
	return "architect"
}
