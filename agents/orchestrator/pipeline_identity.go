package orchestrator

import (
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
)

// PipelineWorkerAgentID returns the stable short worker ID used for a
// task-scoped pipeline worker. The task identity and worker role remain
// separate from the worker's actual agent ID.
func PipelineWorkerAgentID(taskID, agentType string) string {
	return agentshared.PipelineWorkerAgentID(taskID, agentType)
}

// TaskScopedAgentID is the deprecated compatibility alias for the real
// pipeline worker agent ID. New code should use PipelineWorkerAgentID.
func TaskScopedAgentID(taskID, agentType string) string {
	return PipelineWorkerAgentID(taskID, agentType)
}

// TaskScopedRoutingName returns a human-readable but unique routing name for a
// task-scoped worker. The task identity is kept first so explicit pipeline
// handoffs can address the already-registered pod member directly.
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
