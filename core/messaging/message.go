package messaging

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Message[T] - Generic Message Envelope
// =============================================================================
//
// Message[T] is the unified envelope for all inter-agent communication.
// It wraps any payload type T with routing metadata, temporal controls,
// status tracking, and priority information.
//
// Design:
// - Generic over payload type T for type safety
// - Contains all envelope metadata (routing, timing, status, priority)
// - Supports both TTL (relative) and Deadline (absolute) temporal controls
// - Status is persisted for tracking message lifecycle
// - Priority enables intelligent routing decisions

// Message is the generic envelope for all bus communication.
// T is the payload type (e.g., *RouteRequest, *RouteResponse, etc.)
type Message[T any] struct {
	// ==========================================================================
	// Identity
	// ==========================================================================

	// ID is the unique message identifier (UUID)
	ID string `json:"id"`

	// CorrelationID links requests to responses
	CorrelationID string `json:"correlation_id,omitempty"`

	// ParentID enables request chaining/tracing (parent correlation ID)
	ParentID string `json:"parent_id,omitempty"`

	// ==========================================================================
	// Routing
	// ==========================================================================

	// Source is the agent that created this message
	Source string `json:"source"`

	// Target is the intended recipient agent (empty for broadcasts)
	Target string `json:"target,omitempty"`

	// Type indicates the message kind (request, response, forward, etc.)
	Type MessageType `json:"type"`

	// ==========================================================================
	// Payload
	// ==========================================================================

	// Payload is the typed message content
	Payload T `json:"payload"`

	// ==========================================================================
	// Temporal
	// ==========================================================================

	// Timestamp is when the message was created
	Timestamp time.Time `json:"timestamp"`

	// Deadline is the absolute time by which the message must be processed.
	// If set, message expires and should be rejected after this time.
	// Takes precedence over TTL if both are set.
	Deadline *time.Time `json:"deadline,omitempty"`

	// TTL (Time-To-Live) is the relative duration from Timestamp.
	// Message expires after Timestamp + TTL.
	// Ignored if Deadline is set.
	TTL time.Duration `json:"ttl,omitempty"`

	// ==========================================================================
	// Status & Tracking
	// ==========================================================================

	// Status is the current lifecycle state (persisted)
	Status MessageStatus `json:"status"`

	// Attempt is the delivery/processing attempt number (1-indexed)
	// Incremented on each retry
	Attempt int `json:"attempt"`

	// MaxAttempts is the maximum number of attempts before giving up
	// 0 means use system default
	MaxAttempts int `json:"max_attempts,omitempty"`

	// ==========================================================================
	// Priority
	// ==========================================================================

	// Priority determines processing order
	Priority Priority `json:"priority"`

	// ==========================================================================
	// Metadata
	// ==========================================================================

	// Metadata for extensibility (custom key-value pairs)
	Metadata map[string]any `json:"metadata,omitempty"`

	// Error contains error information if Status is Failed
	Error string `json:"error,omitempty"`

	// ProcessedAt is when the message finished processing (success or failure)
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// =============================================================================
// MessageType
// =============================================================================

// MessageType indicates the kind of message
type MessageType string

const (
	// Request is a request to be routed
	TypeRequest MessageType = "request"

	// Forward is a classified request forwarded to target
	TypeForward MessageType = "forward"

	// Response is a response from a target agent
	TypeResponse MessageType = "response"

	// Ack is a lightweight acknowledgment for fire-and-forget requests
	TypeAck MessageType = "ack"

	// Error is an error message
	TypeError MessageType = "error"

	// AgentRegistered is published when an agent registers
	TypeAgentRegistered MessageType = "agent_registered"

	// AgentUnregistered is published when an agent unregisters
	TypeAgentUnregistered MessageType = "agent_unregistered"

	// AgentReady signals agent has completed initialization
	TypeAgentReady MessageType = "agent_ready"

	// RouteLearned is published when Guide learns a new route
	TypeRouteLearned MessageType = "route_learned"

	// Action is a programmatic action request
	TypeAction MessageType = "action"

	// Heartbeat is a health check
	TypeHeartbeat MessageType = "heartbeat"
)

// =============================================================================
// MessageStatus
// =============================================================================

// MessageStatus represents the lifecycle state of a message
type MessageStatus string

const (
	// StatusQueued means the message is waiting to be processed
	StatusQueued MessageStatus = "queued"

	// StatusProcessing means the message is currently being handled
	StatusProcessing MessageStatus = "processing"

	// StatusCompleted means the message was successfully processed
	StatusCompleted MessageStatus = "completed"

	// StatusFailed means processing failed (see Error field)
	StatusFailed MessageStatus = "failed"

	// StatusExpired means the message exceeded its TTL or Deadline
	StatusExpired MessageStatus = "expired"

	// StatusCancelled means the message was explicitly cancelled
	StatusCancelled MessageStatus = "cancelled"
)

// IsTerminal returns true if this is a final state
func (s MessageStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusExpired || s == StatusCancelled
}

// =============================================================================
// Priority
// =============================================================================

// Priority determines message processing order.
// Higher values = higher priority = processed first.
type Priority int

const (
	// PriorityBackground is for non-urgent background tasks
	PriorityBackground Priority = 0

	// PriorityLow is for low-priority messages
	PriorityLow Priority = 25

	// PriorityNormal is the default priority
	PriorityNormal Priority = 50

	// PriorityHigh is for important messages
	PriorityHigh Priority = 75

	// PriorityCritical is for urgent, time-sensitive messages
	PriorityCritical Priority = 100
)

// String returns the priority name
func (p Priority) String() string {
	switch p {
	case PriorityCritical:
		return "critical"
	case PriorityHigh:
		return "high"
	case PriorityNormal:
		return "normal"
	case PriorityLow:
		return "low"
	case PriorityBackground:
		return "background"
	default:
		return "custom"
	}
}

// =============================================================================
// Constructors
// =============================================================================

// New creates a new Message with the given payload.
// Generates a UUID, sets timestamp to now, status to queued, attempt to 1.
func New[T any](msgType MessageType, source string, payload T) *Message[T] {
	return &Message[T]{
		ID:        uuid.New().String(),
		Source:    source,
		Type:      msgType,
		Payload:   payload,
		Timestamp: time.Now(),
		Status:    StatusQueued,
		Attempt:   1,
		Priority:  PriorityNormal,
	}
}

// NewWithID creates a new Message with a specific ID
func NewWithID[T any](id string, msgType MessageType, source string, payload T) *Message[T] {
	return &Message[T]{
		ID:        id,
		Source:    source,
		Type:      msgType,
		Payload:   payload,
		Timestamp: time.Now(),
		Status:    StatusQueued,
		Attempt:   1,
		Priority:  PriorityNormal,
	}
}

// =============================================================================
// Builder Pattern
// =============================================================================

// WithCorrelation sets the correlation ID (for request-response linking)
func (m *Message[T]) WithCorrelation(correlationID string) *Message[T] {
	m.CorrelationID = correlationID
	return m
}

// WithParent sets the parent ID (for request chaining)
func (m *Message[T]) WithParent(parentID string) *Message[T] {
	m.ParentID = parentID
	return m
}

// WithTarget sets the target agent
func (m *Message[T]) WithTarget(target string) *Message[T] {
	m.Target = target
	return m
}

// WithDeadline sets an absolute deadline
func (m *Message[T]) WithDeadline(deadline time.Time) *Message[T] {
	m.Deadline = &deadline
	return m
}

// WithTTL sets a relative time-to-live
func (m *Message[T]) WithTTL(ttl time.Duration) *Message[T] {
	m.TTL = ttl
	return m
}

// WithPriority sets the message priority
func (m *Message[T]) WithPriority(priority Priority) *Message[T] {
	m.Priority = priority
	return m
}

// WithMaxAttempts sets the maximum retry attempts
func (m *Message[T]) WithMaxAttempts(max int) *Message[T] {
	m.MaxAttempts = max
	return m
}

// WithMetadata adds metadata key-value pairs
func (m *Message[T]) WithMetadata(key string, value any) *Message[T] {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
	return m
}

// =============================================================================
// Temporal Methods
// =============================================================================

// ExpiresAt returns the absolute expiration time.
// Returns Deadline if set, otherwise Timestamp + TTL.
// Returns nil if neither is set (never expires).
func (m *Message[T]) ExpiresAt() *time.Time {
	if m.Deadline != nil {
		return m.Deadline
	}
	if m.TTL > 0 {
		exp := m.Timestamp.Add(m.TTL)
		return &exp
	}
	return nil
}

// IsExpired returns true if the message has exceeded its deadline or TTL
func (m *Message[T]) IsExpired() bool {
	exp := m.ExpiresAt()
	if exp == nil {
		return false
	}
	return time.Now().After(*exp)
}

// RemainingTTL returns the time remaining until expiration.
// Returns 0 if expired or no expiration set.
func (m *Message[T]) RemainingTTL() time.Duration {
	exp := m.ExpiresAt()
	if exp == nil {
		return 0
	}
	remaining := time.Until(*exp)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// =============================================================================
// Status Methods
// =============================================================================

// MarkProcessing transitions status to Processing
func (m *Message[T]) MarkProcessing() {
	m.Status = StatusProcessing
}

// MarkCompleted transitions status to Completed
func (m *Message[T]) MarkCompleted() {
	m.Status = StatusCompleted
	now := time.Now()
	m.ProcessedAt = &now
}

// MarkFailed transitions status to Failed with error
func (m *Message[T]) MarkFailed(err string) {
	m.Status = StatusFailed
	m.Error = err
	now := time.Now()
	m.ProcessedAt = &now
}

// MarkExpired transitions status to Expired
func (m *Message[T]) MarkExpired() {
	m.Status = StatusExpired
	now := time.Now()
	m.ProcessedAt = &now
}

// MarkCancelled transitions status to Cancelled
func (m *Message[T]) MarkCancelled() {
	m.Status = StatusCancelled
	now := time.Now()
	m.ProcessedAt = &now
}

// CanRetry returns true if the message can be retried
func (m *Message[T]) CanRetry() bool {
	if m.Status.IsTerminal() {
		return false
	}
	if m.IsExpired() {
		return false
	}
	if m.MaxAttempts > 0 && m.Attempt >= m.MaxAttempts {
		return false
	}
	return true
}

// IncrementAttempt increments the attempt counter and resets status to Queued
func (m *Message[T]) IncrementAttempt() {
	m.Attempt++
	m.Status = StatusQueued
}

// =============================================================================
// Validation
// =============================================================================

// ValidationError contains details about validation failures
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// Validate checks that the message envelope is complete and valid.
// Returns nil if valid, or a ValidationError describing the issue.
func (m *Message[T]) Validate() error {
	if m.ID == "" {
		return &ValidationError{Field: "id", Message: "required"}
	}
	if m.Source == "" {
		return &ValidationError{Field: "source", Message: "required"}
	}
	if m.Type == "" {
		return &ValidationError{Field: "type", Message: "required"}
	}
	if m.Timestamp.IsZero() {
		return &ValidationError{Field: "timestamp", Message: "required"}
	}
	if m.Attempt < 1 {
		return &ValidationError{Field: "attempt", Message: "must be >= 1"}
	}

	// Validate temporal consistency
	if m.Deadline != nil && m.Deadline.Before(m.Timestamp) {
		return &ValidationError{Field: "deadline", Message: "cannot be before timestamp"}
	}
	if m.TTL < 0 {
		return &ValidationError{Field: "ttl", Message: "cannot be negative"}
	}

	// Validate status
	if m.Status == "" {
		return &ValidationError{Field: "status", Message: "required"}
	}

	return nil
}

// ValidateForRouting performs validation specific to routable messages.
// Includes base validation plus routing-specific checks.
func (m *Message[T]) ValidateForRouting() error {
	if err := m.Validate(); err != nil {
		return err
	}

	// Request and Forward types should have a target or correlation
	switch m.Type {
	case TypeRequest, TypeForward:
		// Target can be empty if this is going to Guide for classification
	case TypeResponse:
		if m.CorrelationID == "" {
			return &ValidationError{Field: "correlation_id", Message: "required for response"}
		}
	}

	// Check expiration
	if m.IsExpired() {
		return &ValidationError{Field: "deadline/ttl", Message: "message has expired"}
	}

	return nil
}

// =============================================================================
// Utility Methods
// =============================================================================

// Clone creates a deep copy of the message with a new ID.
// Useful for creating retries or forks.
func (m *Message[T]) Clone() *Message[T] {
	clone := *m
	clone.ID = uuid.New().String()
	clone.Timestamp = time.Now()
	clone.Attempt = 1
	clone.Status = StatusQueued
	clone.ProcessedAt = nil
	clone.Error = ""

	// Deep copy metadata
	if m.Metadata != nil {
		clone.Metadata = make(map[string]any, len(m.Metadata))
		for k, v := range m.Metadata {
			clone.Metadata[k] = v
		}
	}

	// Copy deadline if set
	if m.Deadline != nil {
		deadline := *m.Deadline
		clone.Deadline = &deadline
	}

	return &clone
}

// Age returns how long since the message was created
func (m *Message[T]) Age() time.Duration {
	return time.Since(m.Timestamp)
}

// ProcessingDuration returns how long processing took.
// Returns 0 if not yet processed.
func (m *Message[T]) ProcessingDuration() time.Duration {
	if m.ProcessedAt == nil {
		return 0
	}
	return m.ProcessedAt.Sub(m.Timestamp)
}

// =============================================================================
// Error Helpers
// =============================================================================

// Common validation errors
var (
	ErrMissingID        = errors.New("message id is required")
	ErrMissingSource    = errors.New("message source is required")
	ErrMissingType      = errors.New("message type is required")
	ErrMessageExpired   = errors.New("message has expired")
	ErrInvalidDeadline  = errors.New("deadline cannot be before timestamp")
	ErrMaxAttemptsReached = errors.New("maximum attempts reached")
)
