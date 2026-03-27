package guardian

import (
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// =============================================================================
// Topic Constants
// =============================================================================

const (
	// topicGuardianStream carries streamed chunks from Guardian to the Guide.
	topicGuardianStream = "guardian.stream"
)

// =============================================================================
// Message Builders
// =============================================================================

// newProposalMessage creates a proposal message for the Guide to present to the user.
func newProposalMessage(correlationID string, proposal *GitMutationProposal) *guide.Message {
	return &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeProposal,
		SourceAgentID: "guardian",
		Payload:       proposal,
		Timestamp:     time.Now(),
	}
}

// newStreamMessage creates a stream event message for the Guide.
func newStreamMessage(correlationID, agentID string, metadata map[string]any, event *guide.StreamEvent) *guide.Message {
	return &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeStream,
		SourceAgentID: agentID,
		Payload: &guide.StreamResponse{
			CorrelationID:     correlationID,
			RespondingAgentID: agentID,
			Metadata:          metadata,
			Event:             event,
		},
		Timestamp: time.Now(),
	}
}

// newResponseMessage creates a route response message.
func newResponseMessage(correlationID, agentID string, resp *guide.RouteResponse) *guide.Message {
	return &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeResponse,
		SourceAgentID: agentID,
		Payload:       resp,
		Timestamp:     time.Now(),
	}
}

// newRerouteMessage creates a reroute request message.
func newRerouteMessage(agentID string, reroute *guide.RerouteRequest) *guide.Message {
	return &guide.Message{
		ID:            uuid.New().String(),
		Type:          guide.MessageTypeRequest,
		SourceAgentID: agentID,
		Payload:       reroute,
		Timestamp:     time.Now(),
	}
}
