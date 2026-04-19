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
//
// Returns the underlying bus.Publish error (or nil) so callers — and
// ultimately the agent's tool loop — can observe a failed tool-event delivery
// and react to it (e.g., surface to the user, abort the in-flight tool).
// Returns nil when bus or channels are missing (no destination configured).
func PublishToolCallStreamEvent(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	agentID string,
	correlationID string,
	sourceAgentID string,
	event ToolCallEvent,
) error {
	if bus == nil || channels == nil {
		return nil
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

	if err := bus.Publish(channels.Responses, msg); err != nil {
		return fmt.Errorf("publish tool call event %s phase=%d: %w", event.ToolName, event.Phase, err)
	}
	return nil
}

// NewToolCallEmitter creates a ToolCallEmitter that publishes events via the
// shared PublishToolCallStreamEvent helper. This is the standard way to wire
// tool call visibility into any agent's request handler. The returned closure
// propagates the publish error so EmitToolCall can surface it to the agent's
// loop.
func NewToolCallEmitter(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	agentID string,
	correlationID string,
	sourceAgentID string,
) ToolCallEmitter {
	return func(event ToolCallEvent) error {
		return PublishToolCallStreamEvent(bus, channels, agentID, correlationID, sourceAgentID, event)
	}
}
