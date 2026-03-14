package orchestrator

import (
	"strings"

	"github.com/google/uuid"
)

var pipelineWorkerIdentityNamespace = uuid.MustParse("7f5e9e9d-2b65-4ce3-9c8d-1d43d6d9b7a2")

// PipelineWorkerAgentID returns the stable short worker ID used for a
// task-scoped pipeline worker. The task identity and worker role remain
// separate from the worker's actual agent ID.
func PipelineWorkerAgentID(taskID, agentType string) string {
	taskID = sanitizePipelineIdentityPart(taskID)
	agentType = sanitizePipelineIdentityPart(agentType)
	if taskID == "" || agentType == "" {
		return ""
	}
	sum := uuid.NewSHA1(pipelineWorkerIdentityNamespace, []byte(taskID+":"+agentType))
	return sum.String()[:8]
}

// TaskScopedAgentID is the deprecated compatibility alias for the real
// pipeline worker agent ID. New code should use PipelineWorkerAgentID.
func TaskScopedAgentID(taskID, agentType string) string {
	return PipelineWorkerAgentID(taskID, agentType)
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
