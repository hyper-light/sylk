package providers

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Message type constants for event bus routing
const (
	MessageTypeStream        = "stream"
	MessageTypeStreamControl = "stream_control"
)

// StreamContext tracks correlation and routing for stream chunks.
// Links all chunks from one generation request for proper event bus routing.
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

	// StartedAt is when the stream was initiated
	StartedAt time.Time `json:"started_at"`
}

// NewStreamContext creates a new StreamContext with a generated CorrelationID.
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

// StreamMessage wraps a StreamChunk for event bus transmission.
type StreamMessage struct {
	// ID uniquely identifies this message
	ID string `json:"id"`

	// Type is always "stream" for stream messages
	Type string `json:"type"`

	// CorrelationID links this message to its stream context
	CorrelationID string `json:"correlation_id"`

	// ParentID is the RequestID that initiated the stream
	ParentID string `json:"parent_id"`

	// Payload contains the actual stream chunk
	Payload *StreamChunk `json:"payload"`

	// Metadata contains additional routing and tracking info
	Metadata StreamMessageMetadata `json:"metadata"`

	// Priority for message ordering
	Priority int `json:"priority"`

	// Timestamp when this message was created
	Timestamp time.Time `json:"timestamp"`
}

// StreamMessageMetadata contains routing and tracking information.
type StreamMessageMetadata struct {
	// Model being used for generation
	Model string `json:"model"`

	// AgentID of the requesting agent
	AgentID string `json:"agent_id"`

	// ChunkIndex is the sequence number of the chunk
	ChunkIndex int `json:"chunk_index"`

	// ChunkType indicates the type of chunk content
	ChunkType StreamChunkType `json:"chunk_type"`

	// ProviderName identifies the LLM provider
	ProviderName string `json:"provider_name"`

	// SessionID identifies the session
	SessionID string `json:"session_id"`
}

// WrapStreamChunk creates a StreamMessage from a StreamChunk and context.
func WrapStreamChunk(chunk *StreamChunk, ctx *StreamContext) *StreamMessage {
	return &StreamMessage{
		ID:            generateID(),
		Type:          MessageTypeStream,
		CorrelationID: ctx.CorrelationID,
		ParentID:      ctx.RequestID,
		Payload:       chunk,
		Metadata:      buildMetadata(chunk, ctx),
		Priority:      ctx.Priority,
		Timestamp:     time.Now(),
	}
}

// buildMetadata creates StreamMessageMetadata from chunk and context.
func buildMetadata(chunk *StreamChunk, ctx *StreamContext) StreamMessageMetadata {
	return StreamMessageMetadata{
		Model:        ctx.Model,
		AgentID:      ctx.AgentID,
		ChunkIndex:   chunk.Index,
		ChunkType:    chunk.Type,
		ProviderName: ctx.ProviderName,
		SessionID:    ctx.SessionID,
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
