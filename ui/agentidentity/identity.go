package agentidentity

import "strings"

const replicaRuntimeDelimiter = "#replica-"

func NormalizeAgentID(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	return normalized
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func LogicalPipelineID(pipelineID, taskID string) string {
	if trimmed := strings.TrimSpace(taskID); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(pipelineID)
}

func IsPipelineWorkerType(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case "engineer", "designer", "inspector-pipeline", "tester-pipeline":
		return true
	default:
		return false
	}
}

func ParseTaskScopedPipelineAgentID(agentID string) (taskID, agentType string, ok bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", "", false
	}
	parts := strings.SplitN(agentID, "__", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	taskID = strings.TrimSpace(parts[0])
	agentType = strings.TrimSpace(parts[1])
	if taskID == "" || !IsPipelineWorkerType(agentType) {
		return "", "", false
	}
	return taskID, agentType, true
}

func ParseCanonicalPipelinePanelAgentID(agentID string) (pipelineID, agentType string, ok bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", "", false
	}
	for _, candidateType := range []string{"inspector-pipeline", "tester-pipeline", "engineer", "designer"} {
		suffix := ":" + candidateType
		if !strings.HasSuffix(agentID, suffix) {
			continue
		}
		pipelineID = strings.TrimSpace(strings.TrimSuffix(agentID, suffix))
		if pipelineID == "" {
			return "", "", false
		}
		return pipelineID, candidateType, true
	}
	return "", "", false
}

func CanonicalPipelinePanelID(agentType, pipelineID string) string {
	agentType = strings.TrimSpace(agentType)
	pipelineID = strings.TrimSpace(pipelineID)
	if !IsPipelineWorkerType(agentType) || pipelineID == "" {
		return ""
	}
	return pipelineID + ":" + agentType
}

func stripReplicaRuntimeID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if idx := strings.Index(agentID, replicaRuntimeDelimiter); idx > 0 {
		return agentID[:idx]
	}
	return agentID
}

func VisibleAgentID(agentID, canonicalAgentID, agentType, pipelineID, taskID string) string {
	candidateID := strings.TrimSpace(FirstNonEmpty(canonicalAgentID, agentID))
	agentType = strings.TrimSpace(agentType)
	pipelineID = LogicalPipelineID(pipelineID, taskID)

	if scopedPipelineID, scopedAgentType, ok := ParseTaskScopedPipelineAgentID(candidateID); ok {
		if pipelineID == "" {
			pipelineID = scopedPipelineID
		}
		if agentType == "" {
			agentType = scopedAgentType
		}
	}
	if canonicalPipelineID, canonicalAgentType, ok := ParseCanonicalPipelinePanelAgentID(candidateID); ok {
		if pipelineID == "" {
			pipelineID = canonicalPipelineID
		}
		if agentType == "" {
			agentType = canonicalAgentType
		}
	}
	if placeholder := CanonicalPipelinePanelID(agentType, pipelineID); placeholder != "" {
		return NormalizeAgentID(placeholder)
	}
	if agentType != "" {
		return NormalizeAgentID(agentType)
	}
	return NormalizeAgentID(stripReplicaRuntimeID(candidateID))
}

func RuntimeAgentID(agentID, explicitRuntimeID string) string {
	runtimeID := FirstNonEmpty(explicitRuntimeID, agentID)
	return NormalizeAgentID(runtimeID)
}
