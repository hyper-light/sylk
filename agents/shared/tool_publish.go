package shared

import (
	"fmt"
	"strings"
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
	agentID string,
	correlationID string,
	sourceAgentID string,
	event ToolCallEvent,
) {
	if bus == nil || channels == nil {
		return
	}
	event.ToolName = canonicalizeInterAgentToolName(event.ToolName, event.FullArgs, event.Output, event.StreamMetadata)
	event.InterAgent = NormalizeInterAgentToolEventForEmit(
		event.ToolName,
		event.FullArgs,
		event.Output,
		event.Phase,
		event.Success,
		event.ErrorMsg,
		event.InterAgent,
		event.StreamMetadata,
	)

	streamEvent := &guide.StreamEvent{
		Type: guide.StreamEventToolCall,
		Data: map[string]any{
			"tool_call_key": event.ToolCallKey,
			"tool_name":     event.ToolName,
			"args_summary":  event.ArgsSummary,
			"full_args":     event.FullArgs,
			"output":        event.Output,
			"error_msg":     event.ErrorMsg,
			"phase":         int(event.Phase),
			"started_at":    event.StartedAt.Format(time.RFC3339Nano),
			"duration":      event.Duration.String(),
			"success":       event.Success,
		},
		Timestamp: time.Now(),
	}
	if event.InterAgent != nil {
		streamEvent.Data.(map[string]any)["inter_agent"] = map[string]any{
			"kind":          strings.TrimSpace(event.InterAgent.Kind),
			"agent_types":   append([]string(nil), event.InterAgent.AgentTypes...),
			"summary":       strings.TrimSpace(event.InterAgent.Summary),
			"thread_key":    strings.TrimSpace(event.InterAgent.ThreadKey),
			"status":        strings.TrimSpace(event.InterAgent.Status),
			"update_origin": event.InterAgent.UpdateOrigin,
		}
	}

	stream := &guide.StreamResponse{
		CorrelationID:     correlationID,
		RespondingAgentID: agentID,
		TargetAgentID:     sourceAgentID,
		Metadata:          cloneStreamMetadata(event.StreamMetadata),
		Event:             streamEvent,
	}

	msg := &guide.Message{
		ID:            fmt.Sprintf("tc_%s_%s", agentID, uuid.New().String()[:8]),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: agentID,
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
	agentID string,
	correlationID string,
	sourceAgentID string,
) ToolCallEmitter {
	return func(event ToolCallEvent) {
		PublishToolCallStreamEvent(bus, channels, agentID, correlationID, sourceAgentID, event)
	}
}
