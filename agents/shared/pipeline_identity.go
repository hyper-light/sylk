package shared

import "strings"

// TaskScopedRoutingName returns the deterministic routing name for a
// task-scoped pipeline worker. This is a stable address for the task pod,
// distinct from the worker's runtime-generated agent ID.
func TaskScopedRoutingName(taskSlug, taskID, agentType string) string {
	base := sanitizePipelineIdentityPart(taskSlug)
	if base == "" {
		base = sanitizePipelineIdentityPart(taskID)
	}
	agentType = sanitizePipelineIdentityPart(agentType)
	if base == "" {
		return agentType
	}
	if agentType == "" {
		return base
	}
	return base + "-" + agentType
}

// PipelineWorkerRoutingTarget returns the explicit routing target for a
// task-scoped worker when a task identity is available.
func PipelineWorkerRoutingTarget(taskID, agentType string) string {
	taskID = strings.TrimSpace(taskID)
	agentType = strings.TrimSpace(agentType)
	if taskID == "" {
		return agentType
	}
	switch agentType {
	case PipelineAgentInspector, PipelineAgentTester, PipelineAgentEngineer, PipelineAgentDesigner:
		if target := TaskScopedRoutingName("", taskID, agentType); target != "" {
			return target
		}
	}
	return agentType
}

func SanitizePipelineIdentityPart(value string) string {
	return sanitizePipelineIdentityPart(value)
}

func sanitizePipelineIdentityPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		case r == '-', r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
