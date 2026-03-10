package orchestrator

import (
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/pipeline/taskstate"
)

type taskPipelineRef struct {
	TaskID    string
	TaskLabel string
	Stage     string
	AgentType string
}

func pipelinePhaseStatus(stage string) taskstate.Status {
	switch strings.TrimSpace(stage) {
	case string(StageInspect):
		return taskstate.StatusDefiningCriteria
	case string(StageTest):
		return taskstate.StatusCreatingTests
	case string(StageExecute):
		return taskstate.StatusExecuting
	default:
		return ""
	}
}

func pipelineStatusForActivity(data map[string]any) string {
	if data == nil {
		return ""
	}
	status, _ := data["pipeline_status"].(string)
	return strings.TrimSpace(status)
}

func publishTaskPipelineState(
	bus guide.EventBus,
	sourceAgentID string,
	pipelineID string,
	taskLabel string,
	status taskstate.Status,
	workerType string,
) {
	if bus == nil || strings.TrimSpace(pipelineID) == "" || status == "" {
		return
	}
	evt := &taskstate.Event{
		PipelineID: strings.TrimSpace(pipelineID),
		TaskID:     strings.TrimSpace(pipelineID),
		TaskLabel:  strings.TrimSpace(taskLabel),
		Status:     status,
		WorkerType: strings.TrimSpace(workerType),
		Timestamp:  time.Now(),
	}
	msg := &guide.Message{
		ID:            generateMessageID(),
		Type:          guide.MessageTypePipelineState,
		SourceAgentID: sourceAgentID,
		Payload:       evt,
		Timestamp:     evt.Timestamp,
	}
	_ = bus.Publish(taskstate.Topic, msg)
}
