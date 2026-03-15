package shared

import (
	"strings"

	"github.com/google/uuid"
)

var pipelineWorkerIdentityNamespace = uuid.MustParse("7f5e9e9d-2b65-4ce3-9c8d-1d43d6d9b7a2")

// PipelineWorkerAgentID returns the canonical stable short worker ID used for
// task-scoped pipeline workers.
func PipelineWorkerAgentID(taskID, agentType string) string {
	taskID = sanitizePipelineIdentityPart(taskID)
	agentType = sanitizePipelineIdentityPart(agentType)
	if taskID == "" || agentType == "" {
		return ""
	}
	sum := uuid.NewSHA1(pipelineWorkerIdentityNamespace, []byte(taskID+":"+agentType))
	return sum.String()[:8]
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
