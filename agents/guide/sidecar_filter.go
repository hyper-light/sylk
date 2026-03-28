package guide

import "strings"

func isInternalSidecarAgentValue(value string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return false
	}
	return trimmed == "scribe" || strings.HasPrefix(trimmed, "scribe-")
}

func isInternalSidecarRegistration(reg *AgentRegistration) bool {
	if reg == nil {
		return false
	}
	return isInternalSidecarAgentValue(reg.ID) ||
		isInternalSidecarAgentValue(reg.Name)
}

func isInternalSidecarRoutingInfo(info *AgentRoutingInfo) bool {
	if info == nil {
		return false
	}
	if isInternalSidecarAgentValue(info.ID) ||
		isInternalSidecarAgentValue(info.Type) ||
		isInternalSidecarAgentValue(info.Name) {
		return true
	}
	return isInternalSidecarRegistration(info.Registration)
}
