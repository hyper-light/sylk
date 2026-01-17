package messaging

import (
	"time"
)

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

type RouteResponse struct {
	Success             bool          `json:"success"`
	Data                any           `json:"data,omitempty"`
	Error               string        `json:"error,omitempty"`
	RespondingAgentID   string        `json:"responding_agent_id"`
	RespondingAgentName string        `json:"responding_agent_name,omitempty"`
	ProcessingTime      time.Duration `json:"processing_time,omitempty"`
}

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

type ActionRequest struct {
	SourceAgentID   string    `json:"source_agent_id"`
	SourceAgentName string    `json:"source_agent_name,omitempty"`
	TargetAgentID   string    `json:"target_agent_id"`
	Action          string    `json:"action"`
	Data            any       `json:"data,omitempty"`
	FireAndForget   bool      `json:"fire_and_forget,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

type AckPayload struct {
	Received  bool   `json:"received"`
	Message   string `json:"message,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type ErrorPayload struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type HeartbeatPayload struct {
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name,omitempty"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Metrics   any       `json:"metrics,omitempty"`
}

func NewRequestMessage(source string, req *RouteRequest) *Message[*RouteRequest] {
	msg := New(TypeRequest, source, req)
	if req.SessionID != "" {
		msg.SessionID = req.SessionID
	}
	if req.TargetAgentID != "" {
		msg.Target = req.TargetAgentID
	}
	return msg
}

func NewRequestMessageWithCorrelation(source, correlationID string, req *RouteRequest) *Message[*RouteRequest] {
	msg := NewRequestMessage(source, req)
	msg.CorrelationID = correlationID
	return msg
}

func NewForwardMessage(source, target string, fwd *ForwardedRequest) *Message[*ForwardedRequest] {
	msg := New(TypeForward, source, fwd)
	msg.Target = target
	return msg
}

func NewForwardMessageWithCorrelation(source, target, correlationID string, fwd *ForwardedRequest) *Message[*ForwardedRequest] {
	msg := NewForwardMessage(source, target, fwd)
	msg.CorrelationID = correlationID
	return msg
}

func NewResponseMessage(source, correlationID string, resp *RouteResponse) *Message[*RouteResponse] {
	msg := New(TypeResponse, source, resp)
	msg.CorrelationID = correlationID
	return msg
}

func NewSuccessResponse(source, correlationID string, data any, processingTime time.Duration) *Message[*RouteResponse] {
	return NewResponseMessage(source, correlationID, &RouteResponse{
		Success:           true,
		Data:              data,
		RespondingAgentID: source,
		ProcessingTime:    processingTime,
	})
}

func NewErrorResponse(source, correlationID, errorMsg string) *Message[*RouteResponse] {
	return NewResponseMessage(source, correlationID, &RouteResponse{
		Success:           false,
		Error:             errorMsg,
		RespondingAgentID: source,
	})
}

func NewActionMessage(source, target string, action *ActionRequest) *Message[*ActionRequest] {
	msg := New(TypeAction, source, action)
	msg.Target = target
	return msg
}

func NewActionMessageWithCorrelation(source, target, correlationID string, action *ActionRequest) *Message[*ActionRequest] {
	msg := NewActionMessage(source, target, action)
	msg.CorrelationID = correlationID
	return msg
}

func NewAckMessage(source, correlationID string) *Message[*AckPayload] {
	msg := New(TypeAck, source, &AckPayload{
		Received:  true,
		Timestamp: time.Now().UnixNano(),
	})
	msg.CorrelationID = correlationID
	return msg
}

func NewNackMessage(source, correlationID, reason string) *Message[*AckPayload] {
	msg := New(TypeAck, source, &AckPayload{
		Received:  false,
		Message:   reason,
		Timestamp: time.Now().UnixNano(),
	})
	msg.CorrelationID = correlationID
	return msg
}

func NewErrorMessage(source, correlationID string, err *ErrorPayload) *Message[*ErrorPayload] {
	msg := New(TypeError, source, err)
	msg.CorrelationID = correlationID
	msg.Error = err.Message
	return msg
}

func NewSimpleErrorMessage(source, correlationID, errorMsg string) *Message[*ErrorPayload] {
	return NewErrorMessage(source, correlationID, &ErrorPayload{
		Message: errorMsg,
	})
}

func NewHeartbeatMessage(agentID, agentName, status string) *Message[*HeartbeatPayload] {
	return New(TypeHeartbeat, agentID, &HeartbeatPayload{
		AgentID:   agentID,
		AgentName: agentName,
		Status:    status,
		Timestamp: time.Now(),
	})
}

type MessageOption[T any] func(*Message[T])

func WithCorrelationOpt[T any](correlationID string) MessageOption[T] {
	return func(m *Message[T]) {
		m.CorrelationID = correlationID
	}
}

func WithParentOpt[T any](parentID string) MessageOption[T] {
	return func(m *Message[T]) {
		m.ParentID = parentID
	}
}

func WithTargetOpt[T any](target string) MessageOption[T] {
	return func(m *Message[T]) {
		m.Target = target
	}
}

func WithPriorityOpt[T any](priority Priority) MessageOption[T] {
	return func(m *Message[T]) {
		m.Priority = priority
	}
}

func WithDeadlineOpt[T any](deadline time.Time) MessageOption[T] {
	return func(m *Message[T]) {
		m.Deadline = &deadline
	}
}

func WithTTLOpt[T any](ttl time.Duration) MessageOption[T] {
	return func(m *Message[T]) {
		m.TTL = ttl
	}
}

func WithMaxAttemptsOpt[T any](max int) MessageOption[T] {
	return func(m *Message[T]) {
		m.MaxAttempts = max
	}
}

func WithMetadataOpt[T any](key string, value any) MessageOption[T] {
	return func(m *Message[T]) {
		if m.Metadata == nil {
			m.Metadata = make(map[string]any)
		}
		m.Metadata[key] = value
	}
}

func NewMessage[T any](msgType MessageType, source string, payload T, opts ...MessageOption[T]) *Message[T] {
	msg := New(msgType, source, payload)
	for _, opt := range opts {
		opt(msg)
	}
	return msg
}

func Reply[T any, R any](original *Message[T], source string, msgType MessageType, payload R) *Message[R] {
	reply := New(msgType, source, payload)
	reply.SessionID = original.SessionID
	reply.CorrelationID = original.CorrelationID
	if reply.CorrelationID == "" {
		reply.CorrelationID = original.ID
	}
	reply.ParentID = original.ID
	reply.Target = original.Source
	return reply
}

func ReplySuccess[T any](original *Message[T], source string, data any, processingTime time.Duration) *Message[*RouteResponse] {
	return Reply(original, source, TypeResponse, &RouteResponse{
		Success:           true,
		Data:              data,
		RespondingAgentID: source,
		ProcessingTime:    processingTime,
	})
}

func ReplyError[T any](original *Message[T], source, errorMsg string) *Message[*RouteResponse] {
	return Reply(original, source, TypeResponse, &RouteResponse{
		Success:           false,
		Error:             errorMsg,
		RespondingAgentID: source,
	})
}
