package orchestrator

import (
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

// PipelineWorkerRoutingTarget returns the stable routing target for a
// task-scoped pipeline worker pod member.
func PipelineWorkerRoutingTarget(taskID, agentType string) string {
	return agentshared.PipelineWorkerRoutingTarget(taskID, agentType)
}

// TaskScopedRoutingName returns a human-readable but unique routing name for a
// task-scoped worker. The task identity is kept first so explicit pipeline
// handoffs can address the already-registered pod member directly.
func TaskScopedRoutingName(taskSlug, taskID, agentType string) string {
	return agentshared.TaskScopedRoutingName(taskSlug, taskID, agentType)
}

func sanitizePipelineIdentityPart(value string) string {
	return agentshared.SanitizePipelineIdentityPart(value)
}
