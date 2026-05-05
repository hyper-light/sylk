package ui

import (
	"strings"

	"github.com/adalundhe/sylk/ui/agentidentity"
)

func normalizeAgentID(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return guideAgentID
	}
	return normalized
}

func normalizeRuntimeAgentID(panelAgentID, runtimeAgentID string) string {
	runtimeAgentID = normalizeAgentID(runtimeAgentID)
	if runtimeAgentID != "" {
		return runtimeAgentID
	}
	return normalizeAgentID(panelAgentID)
}

func runtimeContextKey(panelAgentID, runtimeAgentID string) string {
	panelAgentID = normalizeAgentID(panelAgentID)
	runtimeAgentID = normalizeRuntimeAgentID(panelAgentID, runtimeAgentID)
	if panelAgentID == "" {
		return runtimeAgentID
	}
	return panelAgentID + "|" + runtimeAgentID
}

func isEphemeralReplicaRuntime(runtimeAgentID string) bool {
	return strings.Contains(strings.TrimSpace(runtimeAgentID), "#replica-")
}

func parseCanonicalPipelinePanelAgentID(agentID string) (pipelineID, agentType string, ok bool) {
	return agentidentity.ParseCanonicalPipelinePanelAgentID(agentID)
}

func parseTaskScopedPipelineAgentID(agentID string) (taskID, agentType string, ok bool) {
	return agentidentity.ParseTaskScopedPipelineAgentID(agentID)
}

func isPipelineWorkerType(agentType string) bool {
	return agentidentity.IsPipelineWorkerType(agentType)
}

func canonicalVisibleAgentID(agentID, agentType, pipelineID, taskID string) string {
	return agentidentity.VisibleAgentID(agentID, "", agentType, pipelineID, taskID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
