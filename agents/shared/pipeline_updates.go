package shared

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// PipelineTaskAttempt returns the current protocol iteration for a routed
// pipeline task. Missing or malformed protocol state defaults to zero.
func PipelineTaskAttempt(task *PipelineTaskInput) int {
	snapshot, err := PipelineProtocolSnapshotFromTask(task)
	if err != nil || snapshot == nil {
		return 0
	}
	if snapshot.Iteration < 0 {
		return 0
	}
	return snapshot.Iteration
}

// PublishPipelineTaskRunningUpdate publishes a non-terminal pipeline update for
// the task's current assignee.
func PublishPipelineTaskRunningUpdate(
	bus guide.EventBus,
	sourceAgentID string,
	task *PipelineTaskInput,
	message string,
	attempt int,
) {
	publishPipelineTaskUpdate(bus, sourceAgentID, task, "running", message, nil, "", attempt)
}

// PublishPipelineTaskSuccessUpdate publishes a terminal success update for the
// current pipeline task.
func PublishPipelineTaskSuccessUpdate(
	bus guide.EventBus,
	sourceAgentID string,
	task *PipelineTaskInput,
	message string,
	output any,
	attempt int,
) {
	publishPipelineTaskUpdate(bus, sourceAgentID, task, "succeeded", message, output, "", attempt)
}

// PublishPipelineTaskFailureUpdate publishes a terminal failure update for the
// current pipeline task.
func PublishPipelineTaskFailureUpdate(
	bus guide.EventBus,
	sourceAgentID string,
	task *PipelineTaskInput,
	errorText string,
	attempt int,
) {
	errorText = strings.TrimSpace(errorText)
	publishPipelineTaskUpdate(bus, sourceAgentID, task, "failed", errorText, nil, errorText, attempt)
}

// PublishPipelineTaskCancelledUpdate publishes a terminal cancelled update for
// the current pipeline task.
func PublishPipelineTaskCancelledUpdate(
	bus guide.EventBus,
	sourceAgentID string,
	task *PipelineTaskInput,
	message string,
	attempt int,
) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "request interrupted"
	}
	publishPipelineTaskUpdate(bus, sourceAgentID, task, "cancelled", message, nil, message, attempt)
}

// PublishPipelineTaskTerminalErrorUpdate publishes the correct terminal update
// for an execution error. User-requested cancellations stay cancelled; all
// other errors remain failures.
func PublishPipelineTaskTerminalErrorUpdate(
	bus guide.EventBus,
	sourceAgentID string,
	task *PipelineTaskInput,
	err error,
	attempt int,
) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		PublishPipelineTaskCancelledUpdate(bus, sourceAgentID, task, "request interrupted", attempt)
		return
	}
	PublishPipelineTaskFailureUpdate(bus, sourceAgentID, task, err.Error(), attempt)
}

func publishPipelineTaskUpdate(
	bus guide.EventBus,
	sourceAgentID string,
	task *PipelineTaskInput,
	status string,
	message string,
	output any,
	errorText string,
	attempt int,
) {
	if bus == nil || task == nil {
		return
	}
	agentType := normalizePipelineAgentType(task.AgentType)
	if agentType == "" {
		return
	}
	stage := pipelineTaskStage(task)
	update := map[string]any{
		"dag_id":     strings.TrimSpace(task.DAGID),
		"node_id":    strings.TrimSpace(task.NodeID),
		"task_id":    strings.TrimSpace(task.TaskID),
		"agent_id":   pipelineTaskTargetAgentID(task),
		"agent_type": agentType,
		"status":     strings.TrimSpace(status),
		"stage":      stage,
		"progress":   pipelineTaskProgress(status, stage),
		"message":    strings.TrimSpace(message),
		"attempt":    attempt,
		"timestamp":  time.Now().UTC(),
	}
	if output != nil {
		update["output"] = output
	}
	if strings.TrimSpace(errorText) != "" {
		update["error"] = strings.TrimSpace(errorText)
	}

	msg := &guide.Message{
		ID:            "pipe_update_" + uuid.NewString()[:8],
		Type:          guide.MessageTypePipelineUpdate,
		SourceAgentID: strings.TrimSpace(sourceAgentID),
		Payload:       update,
		Timestamp:     time.Now().UTC(),
	}
	if err := bus.Publish("pipeline.update."+agentType, msg); err != nil {
		// Pipeline updates drive UI panel state. A delivery failure means
		// the UI misses a status transition. Log unconditionally so the gap
		// is observable even though this function has no ctx/agent identity
		// to attribute the log more precisely.
		pipelineUpdatePublishErrorLogger(sourceAgentID, agentType, status, err)
	}
}

// pipelineUpdatePublishErrorLogger is overridable for tests; the default
// implementation writes to the standard slog.
var pipelineUpdatePublishErrorLogger = defaultPipelineUpdatePublishErrorLogger

func defaultPipelineUpdatePublishErrorLogger(sourceAgentID, agentType, status string, err error) {
	slog.Warn("pipeline_update_publish_failed",
		"source_agent_id", sourceAgentID,
		"agent_type", agentType,
		"status", status,
		"error", err.Error(),
	)
}

func pipelineTaskStage(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}
	if stage := pipelineTaskContextString(task.Context, "pipeline_stage"); stage != "" {
		return stage
	}
	return pipelineStageForAgents([]string{task.AgentType})
}

func pipelineTaskTargetAgentID(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}
	targetAgentID := strings.TrimSpace(task.TargetAgentID)
	if targetAgentID != "" {
		return targetAgentID
	}
	return pipelineProtocolTargetAgentID(task.TaskID, task.AgentType)
}

func pipelineTaskProgress(status string, stage string) float64 {
	switch strings.TrimSpace(status) {
	case "succeeded", "failed", "cancelled", "timed_out":
		return 1
	case "running":
		switch strings.TrimSpace(stage) {
		case "inspect":
			return 0.15
		case "test":
			return 0.4
		case "execute":
			return 0.7
		}
	}
	return 0
}
