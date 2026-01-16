package messaging

import (
	"time"
)

// =============================================================================
// Typed Message Builders
// =============================================================================
//
// These builders provide type-safe construction of common message types.
// Each builder returns a properly typed Message[T] with sensible defaults.

// =============================================================================
// Request/Response Types (to be used with Message[T])
// =============================================================================

// RouteRequest represents a request to be routed by the Guide
type RouteRequest struct {
	Input           string    `json:"input"`
	SourceAgentID   string    `json:"source_agent_id"`
	SourceAgentName string    `json:"source_agent_name,omitempty"`
	TargetAgentID   string    `json:"target_agent_id,omitempty"`
	FireAndForget   bool      `json:"fire_and_forget,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	RequestID       string    `json:"request_id,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

// RouteResponse represents a response from a target agent
type RouteResponse struct {
	Success             bool          `json:"success"`
	Data                any           `json:"data,omitempty"`
	Error               string        `json:"error,omitempty"`
	RespondingAgentID   string        `json:"responding_agent_id"`
	RespondingAgentName string        `json:"responding_agent_name,omitempty"`
	ProcessingTime      time.Duration `json:"processing_time,omitempty"`
}

// ForwardedRequest is what the Guide sends to target agents
type ForwardedRequest struct {
	Input                string         `json:"input"`
	Intent               string         `json:"intent"`
	Domain               string         `json:"domain"`
	Entities             map[string]any `json:"entities,omitempty"`
	SourceAgentID        string         `json:"source_agent_id"`
	SourceAgentName      string         `json:"source_agent_name,omitempty"`
	FireAndForget        bool           `json:"fire_and_forget,omitempty"`
	Confidence           float64        `json:"confidence"`
	ClassificationMethod string         `json:"classification_method"`
}

// ActionRequest represents a programmatic action request
type ActionRequest struct {
	SourceAgentID   string    `json:"source_agent_id"`
	SourceAgentName string    `json:"source_agent_name,omitempty"`
	TargetAgentID   string    `json:"target_agent_id"`
	Action          string    `json:"action"`
	Data            any       `json:"data,omitempty"`
	FireAndForget   bool      `json:"fire_and_forget,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

// AckPayload is a lightweight acknowledgment
type AckPayload struct {
	Received  bool   `json:"received"`
	Message   string `json:"message,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// ErrorPayload contains error details
type ErrorPayload struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// HeartbeatPayload is a health check payload
type HeartbeatPayload struct {
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name,omitempty"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Metrics   any       `json:"metrics,omitempty"`
}

// =============================================================================
// Request Builders
// =============================================================================

// NewRequestMessage creates a new request message
func NewRequestMessage(source string, req *RouteRequest) *Message[*RouteRequest] {
	msg := New(TypeRequest, source, req)
	if req.TargetAgentID != "" {
		msg.Target = req.TargetAgentID
	}
	return msg
}

// NewRequestMessageWithCorrelation creates a request with a specific correlation ID
func NewRequestMessageWithCorrelation(source, correlationID string, req *RouteRequest) *Message[*RouteRequest] {
	msg := NewRequestMessage(source, req)
	msg.CorrelationID = correlationID
	return msg
}

// =============================================================================
// Forward Builders
// =============================================================================

// NewForwardMessage creates a forward message to a target agent
func NewForwardMessage(source, target string, fwd *ForwardedRequest) *Message[*ForwardedRequest] {
	msg := New(TypeForward, source, fwd)
	msg.Target = target
	return msg
}

// NewForwardMessageWithCorrelation creates a forward with correlation
func NewForwardMessageWithCorrelation(source, target, correlationID string, fwd *ForwardedRequest) *Message[*ForwardedRequest] {
	msg := NewForwardMessage(source, target, fwd)
	msg.CorrelationID = correlationID
	return msg
}

// =============================================================================
// Response Builders
// =============================================================================

// NewResponseMessage creates a response message
func NewResponseMessage(source, correlationID string, resp *RouteResponse) *Message[*RouteResponse] {
	msg := New(TypeResponse, source, resp)
	msg.CorrelationID = correlationID
	return msg
}

// NewSuccessResponse creates a successful response
func NewSuccessResponse(source, correlationID string, data any, processingTime time.Duration) *Message[*RouteResponse] {
	return NewResponseMessage(source, correlationID, &RouteResponse{
		Success:           true,
		Data:              data,
		RespondingAgentID: source,
		ProcessingTime:    processingTime,
	})
}

// NewErrorResponse creates an error response
func NewErrorResponse(source, correlationID, errorMsg string) *Message[*RouteResponse] {
	return NewResponseMessage(source, correlationID, &RouteResponse{
		Success:           false,
		Error:             errorMsg,
		RespondingAgentID: source,
	})
}

// =============================================================================
// Action Builders
// =============================================================================

// NewActionMessage creates an action message
func NewActionMessage(source, target string, action *ActionRequest) *Message[*ActionRequest] {
	msg := New(TypeAction, source, action)
	msg.Target = target
	return msg
}

// NewActionMessageWithCorrelation creates an action with correlation
func NewActionMessageWithCorrelation(source, target, correlationID string, action *ActionRequest) *Message[*ActionRequest] {
	msg := NewActionMessage(source, target, action)
	msg.CorrelationID = correlationID
	return msg
}

// =============================================================================
// Ack Builders
// =============================================================================

// NewAckMessage creates an acknowledgment message
func NewAckMessage(source, correlationID string) *Message[*AckPayload] {
	msg := New(TypeAck, source, &AckPayload{
		Received:  true,
		Timestamp: time.Now().UnixNano(),
	})
	msg.CorrelationID = correlationID
	return msg
}

// NewNackMessage creates a negative acknowledgment (rejection)
func NewNackMessage(source, correlationID, reason string) *Message[*AckPayload] {
	msg := New(TypeAck, source, &AckPayload{
		Received:  false,
		Message:   reason,
		Timestamp: time.Now().UnixNano(),
	})
	msg.CorrelationID = correlationID
	return msg
}

// =============================================================================
// Error Builders
// =============================================================================

// NewErrorMessage creates an error message
func NewErrorMessage(source, correlationID string, err *ErrorPayload) *Message[*ErrorPayload] {
	msg := New(TypeError, source, err)
	msg.CorrelationID = correlationID
	msg.Error = err.Message
	return msg
}

// NewSimpleErrorMessage creates an error with just a message
func NewSimpleErrorMessage(source, correlationID, errorMsg string) *Message[*ErrorPayload] {
	return NewErrorMessage(source, correlationID, &ErrorPayload{
		Message: errorMsg,
	})
}

// =============================================================================
// Heartbeat Builders
// =============================================================================

// NewHeartbeatMessage creates a heartbeat message
func NewHeartbeatMessage(agentID, agentName, status string) *Message[*HeartbeatPayload] {
	return New(TypeHeartbeat, agentID, &HeartbeatPayload{
		AgentID:   agentID,
		AgentName: agentName,
		Status:    status,
		Timestamp: time.Now(),
	})
}

// =============================================================================
// Generic Builder with Options Pattern
// =============================================================================

// MessageOption is a function that configures a message
type MessageOption[T any] func(*Message[T])

// WithCorrelationOpt returns an option that sets correlation ID
func WithCorrelationOpt[T any](correlationID string) MessageOption[T] {
	return func(m *Message[T]) {
		m.CorrelationID = correlationID
	}
}

// WithParentOpt returns an option that sets parent ID
func WithParentOpt[T any](parentID string) MessageOption[T] {
	return func(m *Message[T]) {
		m.ParentID = parentID
	}
}

// WithTargetOpt returns an option that sets target
func WithTargetOpt[T any](target string) MessageOption[T] {
	return func(m *Message[T]) {
		m.Target = target
	}
}

// WithPriorityOpt returns an option that sets priority
func WithPriorityOpt[T any](priority Priority) MessageOption[T] {
	return func(m *Message[T]) {
		m.Priority = priority
	}
}

// WithDeadlineOpt returns an option that sets deadline
func WithDeadlineOpt[T any](deadline time.Time) MessageOption[T] {
	return func(m *Message[T]) {
		m.Deadline = &deadline
	}
}

// WithTTLOpt returns an option that sets TTL
func WithTTLOpt[T any](ttl time.Duration) MessageOption[T] {
	return func(m *Message[T]) {
		m.TTL = ttl
	}
}

// WithMaxAttemptsOpt returns an option that sets max attempts
func WithMaxAttemptsOpt[T any](max int) MessageOption[T] {
	return func(m *Message[T]) {
		m.MaxAttempts = max
	}
}

// WithMetadataOpt returns an option that adds metadata
func WithMetadataOpt[T any](key string, value any) MessageOption[T] {
	return func(m *Message[T]) {
		if m.Metadata == nil {
			m.Metadata = make(map[string]any)
		}
		m.Metadata[key] = value
	}
}

// NewMessage creates a message with the given options
func NewMessage[T any](msgType MessageType, source string, payload T, opts ...MessageOption[T]) *Message[T] {
	msg := New(msgType, source, payload)
	for _, opt := range opts {
		opt(msg)
	}
	return msg
}

// =============================================================================
// Reply Helper
// =============================================================================

// Reply creates a response message that inherits routing info from the original
func Reply[T any, R any](original *Message[T], source string, msgType MessageType, payload R) *Message[R] {
	reply := New(msgType, source, payload)
	reply.CorrelationID = original.CorrelationID
	if reply.CorrelationID == "" {
		reply.CorrelationID = original.ID
	}
	reply.ParentID = original.ID
	reply.Target = original.Source
	return reply
}

// ReplySuccess creates a successful response to a message
func ReplySuccess[T any](original *Message[T], source string, data any, processingTime time.Duration) *Message[*RouteResponse] {
	return Reply(original, source, TypeResponse, &RouteResponse{
		Success:           true,
		Data:              data,
		RespondingAgentID: source,
		ProcessingTime:    processingTime,
	})
}

// ReplyError creates an error response to a message
func ReplyError[T any](original *Message[T], source, errorMsg string) *Message[*RouteResponse] {
	return Reply(original, source, TypeResponse, &RouteResponse{
		Success:           false,
		Error:             errorMsg,
		RespondingAgentID: source,
	})
}
