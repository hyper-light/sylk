package providers

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/adalundhe/sylk/core/messaging"
)

const (
	MessageTypeStream        = "stream"
	MessageTypeStreamControl = "stream_control"
)

type StreamContext struct {
	// CorrelationID uniquely identifies all chunks from one generation
	CorrelationID string `json:"correlation_id"`

	// RequestID is the parent request that triggered this stream
	RequestID string `json:"request_id"`

	// SessionID identifies the session
	SessionID string `json:"session_id"`

	// ProviderName identifies the LLM provider
	ProviderName string `json:"provider_name"`

	// TargetAgent specifies which agent should receive these chunks
	TargetAgent string `json:"target_agent"`

	// Model is the model being used
	Model string `json:"model"`

	// AgentID identifies the agent making the request
	AgentID string `json:"agent_id"`

	// Priority for message ordering
	Priority int `json:"priority"`

	StartedAt time.Time `json:"started_at"`
}

func NewStreamContext(sessionID, providerName, model, agentID string) *StreamContext {
	return &StreamContext{
		CorrelationID: generateID(),
		SessionID:     sessionID,
		ProviderName:  providerName,
		Model:         model,
		AgentID:       agentID,
		StartedAt:     time.Now(),
	}
}

func WrapStreamChunk(chunk *StreamChunk, ctx *StreamContext) *messaging.Message[StreamChunk] {
	if chunk == nil {
		return nil
	}

	msg := messaging.New(messaging.MessageType(MessageTypeStream), ctx.AgentID, *chunk)
	msg.SessionID = ctx.SessionID
	msg.CorrelationID = ctx.CorrelationID
	msg.ParentID = ctx.RequestID
	msg.Target = ctx.TargetAgent
	msg.Priority = messaging.Priority(ctx.Priority)
	msg.Metadata = buildMetadata(chunk, ctx)
	return msg
}

func buildMetadata(chunk *StreamChunk, ctx *StreamContext) map[string]any {
	return map[string]any{
		"model":         ctx.Model,
		"agent_id":      ctx.AgentID,
		"chunk_index":   chunk.Index,
		"chunk_type":    chunk.Type,
		"provider_name": ctx.ProviderName,
		"session_id":    ctx.SessionID,
		"request_id":    ctx.RequestID,
	}
}

// StreamControlMessage is a control signal for stream management.
type StreamControlMessage struct {
	// ID uniquely identifies this control message
	ID string `json:"id"`

	// Type is always "stream_control"
	Type string `json:"type"`

	// CorrelationID identifies the stream to control
	CorrelationID string `json:"correlation_id"`

	// Action is the control action: "cancel", "pause", "resume"
	Action string `json:"action"`

	// Reason explains why the action was taken
	Reason string `json:"reason"`

	// Timestamp when this control message was created
	Timestamp time.Time `json:"timestamp"`
}

// NewStreamControlMessage creates a new control message for a stream.
func NewStreamControlMessage(correlationID, action, reason string) *StreamControlMessage {
	return &StreamControlMessage{
		ID:            generateID(),
		Type:          MessageTypeStreamControl,
		CorrelationID: correlationID,
		Action:        action,
		Reason:        reason,
		Timestamp:     time.Now(),
	}
}

// generateID creates a unique identifier using crypto/rand.
func generateID() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
