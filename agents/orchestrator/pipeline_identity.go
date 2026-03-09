package orchestrator

import (
	"strings"
)

// TaskScopedAgentID returns the stable per-task worker identity used for
// pipeline agents. task_id is the canonical pipeline identity; agent type is
// preserved as the worker role.
func TaskScopedAgentID(taskID, agentType string) string {
	taskID = sanitizePipelineIdentityPart(taskID)
	agentType = sanitizePipelineIdentityPart(agentType)
	if taskID == "" {
		return agentType
	}
	if agentType == "" {
		return taskID
	}
	return taskID + "__" + agentType
}

// TaskScopedRoutingName returns a human-readable but unique routing name for a
// task-scoped worker. It intentionally avoids the bare agent type so task
// workers do not collide with the canonical conversational singleton.
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
	return agentType + "-" + base
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
