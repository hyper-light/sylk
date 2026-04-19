package shared

import (
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// EmitDispatchACK publishes an ACK message to the topic specified in the
// forwarded request's metadata. This is the shared helper all pipeline
// agents call upon receiving a task dispatch.
//
// Returns true if an ACK was emitted, false if no ack_topic was present.
func EmitDispatchACK(bus guide.EventBus, metadata map[string]any, agentID, agentType, correlationID string) bool {
	if metadata == nil {
		return false
	}

	ackTopic, ok := metadata["ack_topic"].(string)
	if !ok || ackTopic == "" {
		return false
	}

	msg := &guide.Message{
		ID:            uuid.New().String(),
		Type:          guide.MessageTypeAck,
		SourceAgentID: agentID,
		CorrelationID: correlationID,
		Payload: map[string]any{
			"responding_agent_id": agentID,
			"agent_type":          agentType,
			"acked_at":            time.Now(),
		},
		Timestamp: time.Now(),
	}

	bus.Publish(ackTopic, msg)
	return true
}
