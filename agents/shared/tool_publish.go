package shared

import (
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/google/uuid"
)

// PublishToolCallStreamEvent publishes a tool call event as a stream message
// on the agent's response channel. This is the shared helper all agents use
// instead of duplicating the publishing logic.
func PublishToolCallStreamEvent(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	agentName string,
	correlationID string,
	sourceAgentID string,
	event ToolCallEvent,
) {
	if bus == nil || channels == nil {
		return
	}

	streamEvent := &guide.StreamEvent{
		Type: guide.StreamEventToolCall,
		Data: map[string]any{
			"tool_name":    event.ToolName,
			"args_summary": event.ArgsSummary,
			"full_args":    event.FullArgs,
			"output":       event.Output,
			"error_msg":    event.ErrorMsg,
			"phase":        int(event.Phase),
			"started_at":   event.StartedAt.Format(time.RFC3339Nano),
			"duration":     event.Duration.String(),
			"success":      event.Success,
		},
		Timestamp: time.Now(),
	}

	stream := &guide.StreamResponse{
		CorrelationID:     correlationID,
		RespondingAgentID: agentName,
		TargetAgentID:     sourceAgentID,
		Event:             streamEvent,
	}

	msg := &guide.Message{
		ID:            fmt.Sprintf("%s_tc_%s", agentName, uuid.New().String()[:8]),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: agentName,
		TargetAgentID: sourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}

	_ = bus.Publish(channels.Responses, msg)
}

// NewToolCallEmitter creates a ToolCallEmitter that publishes events via the
// shared PublishToolCallStreamEvent helper. This is the standard way to wire
// tool call visibility into any agent's request handler.
func NewToolCallEmitter(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	agentName string,
	correlationID string,
	sourceAgentID string,
) ToolCallEmitter {
	return func(event ToolCallEvent) {
		PublishToolCallStreamEvent(bus, channels, agentName, correlationID, sourceAgentID, event)
	}
}
