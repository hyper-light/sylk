package guide

import (
	"time"

	"github.com/adalundhe/sylk/core/messaging"
	"github.com/google/uuid"
)

// =============================================================================
// Event Bus Interface
// =============================================================================
//
// The EventBus provides async message passing between agents. All inter-agent
// communication flows through the bus, enabling:
// - Loose coupling (agents don't reference each other)
// - True async fire-and-forget
// - Scalability (add agents without changing Guide)
// - Observability (tap topics for monitoring)
//
// Topic naming convention:
// - requests.guide     : All requests to Guide for routing
// - requests.{agent}   : Requests forwarded to specific agent
// - responses.guide    : All responses back to Guide for correlation
// - responses.{agent}  : Responses forwarded to specific agent

// EventBus defines the messaging interface for inter-agent communication
type EventBus interface {
	// Publish sends a message to a topic.
	// Returns immediately - delivery is async.
	Publish(topic string, msg *Message) error

	// Subscribe registers a handler for a topic.
	// Handler is called for each message published to the topic.
	// Returns a Subscription that can be used to unsubscribe.
	Subscribe(topic string, handler MessageHandler) (Subscription, error)

	// SubscribeAsync is like Subscribe but handler runs in a goroutine.
	// Use for handlers that may block or take time.
	SubscribeAsync(topic string, handler MessageHandler) (Subscription, error)

	// Close shuts down the bus and all subscriptions.
	Close() error
}

// Subscription represents an active subscription to a topic
type Subscription interface {
	// Topic returns the subscribed topic
	Topic() string

	// Unsubscribe removes this subscription
	Unsubscribe() error

	// IsActive returns true if subscription is still active
	IsActive() bool
}

// MessageHandler processes a message from a topic
type MessageHandler func(msg *Message) error

// =============================================================================
// Message Types
// =============================================================================

// Message is the envelope for all bus communication.
// This struct acts as the full message envelope with routing, temporal,
// status, and priority metadata.
type Message struct {
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

	// Type indicates the message kind (request, response, forward, etc.)
	Type MessageType `json:"type"`

	// SourceAgentID is the agent that created this message
	SourceAgentID string `json:"source_agent_id,omitempty"`

	// TargetAgentID is the intended recipient agent (empty for broadcasts)
	TargetAgentID string `json:"target_agent_id,omitempty"`

	// ==========================================================================
	// Payload
	// ==========================================================================

	// Payload is the message content (RouteRequest, ForwardedRequest, etc.)
	Payload any `json:"payload"`

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

	// Status is the current lifecycle state (queued, processing, completed, failed, expired)
	Status messaging.MessageStatus `json:"status"`

	// Attempt is the delivery/processing attempt number (1-indexed)
	Attempt int `json:"attempt"`

	// MaxAttempts is the maximum number of attempts before giving up (0 = system default)
	MaxAttempts int `json:"max_attempts,omitempty"`

	// Error contains error information if Status is Failed
	Error string `json:"error,omitempty"`

	// ProcessedAt is when the message finished processing
	ProcessedAt *time.Time `json:"processed_at,omitempty"`

	// ==========================================================================
	// Priority
	// ==========================================================================

	// Priority determines processing order (higher = processed first)
	Priority messaging.Priority `json:"priority"`

	// ==========================================================================
	// Metadata
	// ==========================================================================

	// Metadata for extensibility (custom key-value pairs)
	Metadata map[string]any `json:"metadata,omitempty"`
}

// =============================================================================
// Message Envelope Methods
// =============================================================================

// ExpiresAt returns the absolute expiration time.
// Returns Deadline if set, otherwise Timestamp + TTL.
// Returns nil if neither is set (never expires).
func (m *Message) ExpiresAt() *time.Time {
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
func (m *Message) IsExpired() bool {
	exp := m.ExpiresAt()
	if exp == nil {
		return false
	}
	return time.Now().After(*exp)
}

// RemainingTTL returns the time remaining until expiration.
// Returns 0 if expired or no expiration set.
func (m *Message) RemainingTTL() time.Duration {
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

// MarkProcessing transitions status to Processing
func (m *Message) MarkProcessing() {
	m.Status = messaging.StatusProcessing
}

// MarkCompleted transitions status to Completed
func (m *Message) MarkCompleted() {
	m.Status = messaging.StatusCompleted
	now := time.Now()
	m.ProcessedAt = &now
}

// MarkFailed transitions status to Failed with error
func (m *Message) MarkFailed(err string) {
	m.Status = messaging.StatusFailed
	m.Error = err
	now := time.Now()
	m.ProcessedAt = &now
}

// MarkExpired transitions status to Expired
func (m *Message) MarkExpired() {
	m.Status = messaging.StatusExpired
	now := time.Now()
	m.ProcessedAt = &now
}

// CanRetry returns true if the message can be retried
func (m *Message) CanRetry() bool {
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
func (m *Message) IncrementAttempt() {
	m.Attempt++
	m.Status = messaging.StatusQueued
}

// WithDeadline sets an absolute deadline
func (m *Message) WithDeadline(deadline time.Time) *Message {
	m.Deadline = &deadline
	return m
}

// WithTTL sets a relative time-to-live
func (m *Message) WithTTL(ttl time.Duration) *Message {
	m.TTL = ttl
	return m
}

// WithPriority sets the message priority
func (m *Message) WithPriority(priority messaging.Priority) *Message {
	m.Priority = priority
	return m
}

// WithMaxAttempts sets the maximum retry attempts
func (m *Message) WithMaxAttempts(max int) *Message {
	m.MaxAttempts = max
	return m
}

// WithMetadata adds metadata key-value pairs
func (m *Message) WithMetadata(key string, value any) *Message {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
	return m
}

// MessageType indicates what kind of message this is
type MessageType string

const (
	// MessageTypeRequest is a request to be routed
	MessageTypeRequest MessageType = "request"

	// MessageTypeForward is a classified request forwarded to target
	MessageTypeForward MessageType = "forward"

	// MessageTypeResponse is a response from a target agent
	MessageTypeResponse MessageType = "response"

	// MessageTypeAck is a lightweight acknowledgment for fire-and-forget requests.
	// Sent immediately upon receipt, before processing begins.
	MessageTypeAck MessageType = "ack"

	// MessageTypeError is an error message
	MessageTypeError MessageType = "error"

	// MessageTypeAgentRegistered is published when an agent registers with the Guide
	MessageTypeAgentRegistered MessageType = "agent_registered"

	// MessageTypeAgentUnregistered is published when an agent unregisters from the Guide
	MessageTypeAgentUnregistered MessageType = "agent_unregistered"

	// MessageTypeRoutelearned is published when Guide learns a new route (for cache sync)
	MessageTypeRouteLearned MessageType = "route_learned"

	// MessageTypeAction is a programmatic action request (not NL-based routing)
	MessageTypeAction MessageType = "action"

	// MessageTypeHeartbeat is a health check heartbeat
	MessageTypeHeartbeat MessageType = "heartbeat"

	// MessageTypeAgentReady signals that an agent has completed initialization
	// and is ready to receive requests. Sent after registration is complete.
	MessageTypeAgentReady MessageType = "agent_ready"
)

// =============================================================================
// Topic Helpers
// =============================================================================

// =============================================================================
// Channel Types
// =============================================================================

// ChannelType represents the type of channel an agent can have
type ChannelType string

const (
	// ChannelTypeRequests is for incoming requests to an agent
	ChannelTypeRequests ChannelType = "requests"

	// ChannelTypeResponses is for outgoing responses from an agent
	ChannelTypeResponses ChannelType = "responses"

	// ChannelTypeErrors is for error responses from an agent
	ChannelTypeErrors ChannelType = "errors"
)

// AllChannelTypes returns the three required channel types
func AllChannelTypes() []ChannelType {
	return []ChannelType{ChannelTypeRequests, ChannelTypeResponses, ChannelTypeErrors}
}

// AgentChannels represents the three channels owned by an agent
type AgentChannels struct {
	AgentID   string `json:"agent_id"`
	Requests  string `json:"requests"`  // <agent>.requests
	Responses string `json:"responses"` // <agent>.responses
	Errors    string `json:"errors"`    // <agent>.errors
}

// NewAgentChannels creates channel names for an agent
func NewAgentChannels(agentID string) *AgentChannels {
	return &AgentChannels{
		AgentID:   agentID,
		Requests:  agentID + ".requests",
		Responses: agentID + ".responses",
		Errors:    agentID + ".errors",
	}
}

// =============================================================================
// Topic Constants and Helpers
// =============================================================================

// Topic constants for the Guide's own channels
const (
	// TopicGuideRequests is where agents send requests for routing
	TopicGuideRequests = "guide.requests"

	// TopicGuideResponses is where the Guide publishes responses (rarely used directly)
	TopicGuideResponses = "guide.responses"

	// TopicGuideErrors is where the Guide publishes errors
	TopicGuideErrors = "guide.errors"

	// TopicAgentRegistry is where agent registration/unregistration events are published.
	// All agents can subscribe to this topic to stay informed about available agents.
	TopicAgentRegistry = "agents.registry"

	// TopicRoutesLearned is where Guide broadcasts newly learned routes.
	// Agents can subscribe to sync their local route caches.
	TopicRoutesLearned = "routes.learned"
)

// AgentTopic returns the topic for a specific agent and channel type
func AgentTopic(agentID string, channelType ChannelType) string {
	return agentID + "." + string(channelType)
}

// TopicRequests returns the request topic for an agent
func TopicRequests(agentID string) string {
	return AgentTopic(agentID, ChannelTypeRequests)
}

// TopicResponses returns the response topic for an agent
func TopicResponses(agentID string) string {
	return AgentTopic(agentID, ChannelTypeResponses)
}

// TopicErrors returns the error topic for an agent
func TopicErrors(agentID string) string {
	return AgentTopic(agentID, ChannelTypeErrors)
}

// =============================================================================
// Message Builders
// =============================================================================

// NewRequestMessage creates a new request message
func NewRequestMessage(id string, req *RouteRequest) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: req.CorrelationID,
		Type:          MessageTypeRequest,
		Payload:       req,
		SourceAgentID: req.SourceAgentID,
		TargetAgentID: req.TargetAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// NewForwardMessage creates a new forward message for target agent
func NewForwardMessage(id string, fwd *ForwardedRequest) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: fwd.CorrelationID,
		Type:          MessageTypeForward,
		Payload:       fwd,
		SourceAgentID: fwd.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// NewResponseMessage creates a new response message
func NewResponseMessage(id string, resp *RouteResponse) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: resp.CorrelationID,
		Type:          MessageTypeResponse,
		Payload:       resp,
		SourceAgentID: resp.RespondingAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// NewErrorMessage creates a new error message
func NewErrorMessage(id, correlationID, sourceAgentID, errorMsg string) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: correlationID,
		Type:          MessageTypeError,
		Payload:       errorMsg,
		SourceAgentID: sourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityHigh, // Errors get high priority
	}
}

// AgentAnnouncement is the payload for agent registration/unregistration events
type AgentAnnouncement struct {
	// Agent identity
	AgentID   string   `json:"agent_id"`
	AgentName string   `json:"agent_name"`
	Aliases   []string `json:"aliases,omitempty"`

	// Agent channels (for registered events)
	Channels *AgentChannels `json:"channels,omitempty"`

	// What the agent handles (for registered events)
	Capabilities *AgentCapabilities `json:"capabilities,omitempty"`
	Constraints  *AgentConstraints  `json:"constraints,omitempty"`
	Description  string             `json:"description,omitempty"`

	// Available action shortcuts (for registered events)
	ActionShortcuts []ActionShortcut `json:"action_shortcuts,omitempty"`

	// Ready indicates the agent has completed initialization.
	// Other agents should wait for Ready=true before routing to this agent.
	Ready bool `json:"ready,omitempty"`

	// ReadyAt is when the agent became ready
	ReadyAt time.Time `json:"ready_at,omitempty"`
}

// NewAgentRegisteredMessage creates a message announcing an agent registration
func NewAgentRegisteredMessage(id string, info *AgentRoutingInfo) *Message {
	announcement := &AgentAnnouncement{
		AgentID:         info.ID,
		AgentName:       info.Name,
		Aliases:         info.Aliases,
		Channels:        NewAgentChannels(info.ID),
		ActionShortcuts: info.ActionShortcuts,
	}

	if info.Registration != nil {
		announcement.Capabilities = &info.Registration.Capabilities
		announcement.Constraints = &info.Registration.Constraints
		announcement.Description = info.Registration.Description
	}

	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		Type:          MessageTypeAgentRegistered,
		Payload:       announcement,
		SourceAgentID: "guide",
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityHigh, // Registration events are important
	}
}

// NewAgentUnregisteredMessage creates a message announcing an agent unregistration
func NewAgentUnregisteredMessage(id, agentID, agentName string) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	announcement := &AgentAnnouncement{
		AgentID:   agentID,
		AgentName: agentName,
	}

	return &Message{
		ID:            id,
		Type:          MessageTypeAgentUnregistered,
		Payload:       announcement,
		SourceAgentID: "guide",
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityHigh,
	}
}

// GetAgentAnnouncement extracts AgentAnnouncement from message payload
func (m *Message) GetAgentAnnouncement() (*AgentAnnouncement, bool) {
	ann, ok := m.Payload.(*AgentAnnouncement)
	return ann, ok
}

// NewAgentReadyMessage creates a message announcing an agent is ready to receive requests.
// This should be sent after the agent has completed all initialization (subscriptions, etc.)
func NewAgentReadyMessage(id string, info *AgentRoutingInfo) *Message {
	announcement := &AgentAnnouncement{
		AgentID:   info.ID,
		AgentName: info.Name,
		Aliases:   info.Aliases,
		Channels:  NewAgentChannels(info.ID),
		Ready:     true,
		ReadyAt:   time.Now(),
	}

	if info.Registration != nil {
		announcement.Capabilities = &info.Registration.Capabilities
		announcement.Constraints = &info.Registration.Constraints
		announcement.Description = info.Registration.Description
	}

	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		Type:          MessageTypeAgentReady,
		Payload:       announcement,
		SourceAgentID: info.ID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityHigh,
	}
}

// =============================================================================
// ACK Messages (for fire-and-forget acknowledgment)
// =============================================================================

// AckPayload is the lightweight payload for acknowledgments
type AckPayload struct {
	Received  bool   `json:"received"`
	Message   string `json:"message,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// NewAckMessage creates an acknowledgment message for fire-and-forget requests
func NewAckMessage(id, correlationID, sourceAgentID string) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: correlationID,
		Type:          MessageTypeAck,
		Payload: &AckPayload{
			Received:  true,
			Timestamp: time.Now().UnixNano(),
		},
		SourceAgentID: sourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// NewAckErrorMessage creates a negative acknowledgment (request rejected)
func NewAckErrorMessage(id, correlationID, sourceAgentID, errorMsg string) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: correlationID,
		Type:          MessageTypeAck,
		Payload: &AckPayload{
			Received:  false,
			Message:   errorMsg,
			Timestamp: time.Now().UnixNano(),
		},
		SourceAgentID: sourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// GetAckPayload extracts AckPayload from message payload
func (m *Message) GetAckPayload() (*AckPayload, bool) {
	ack, ok := m.Payload.(*AckPayload)
	return ack, ok
}

// IsAck returns true if this is an acknowledgment message
func (m *Message) IsAck() bool {
	return m.Type == MessageTypeAck
}

// =============================================================================
// Route Learned Messages (for cache synchronization)
// =============================================================================

// LearnedRoute represents a route that Guide learned and is broadcasting
type LearnedRoute struct {
	Input         string  `json:"input"`
	TargetAgentID string  `json:"target_agent_id"`
	Intent        Intent  `json:"intent"`
	Domain        Domain  `json:"domain"`
	Confidence    float64 `json:"confidence"`
}

// NewRouteLearnedMessage creates a message announcing a learned route
func NewRouteLearnedMessage(id string, route *LearnedRoute) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		Type:          MessageTypeRouteLearned,
		Payload:       route,
		SourceAgentID: "guide",
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityLow, // Cache sync is low priority
	}
}

// GetLearnedRoute extracts LearnedRoute from message payload
func (m *Message) GetLearnedRoute() (*LearnedRoute, bool) {
	route, ok := m.Payload.(*LearnedRoute)
	return route, ok
}

// =============================================================================
// Action Messages (for programmatic agent-to-agent triggers)
// =============================================================================

// ActionRequest represents a programmatic action request.
// Unlike RouteRequest, this is for direct agent-to-agent triggers,
// not natural language routing.
type ActionRequest struct {
	CorrelationID       string    `json:"correlation_id"`
	ParentCorrelationID string    `json:"parent_correlation_id,omitempty"`
	SourceAgentID       string    `json:"source_agent_id"`
	SourceAgentName     string    `json:"source_agent_name,omitempty"`
	TargetAgentID       string    `json:"target_agent_id"`
	Action              string    `json:"action"`
	Data                any       `json:"data,omitempty"`
	FireAndForget       bool      `json:"fire_and_forget,omitempty"`
	Timestamp           time.Time `json:"timestamp"`
}

// NewActionMessage creates a message for an action request
func NewActionMessage(id string, req *ActionRequest) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: req.CorrelationID,
		ParentID:      req.ParentCorrelationID,
		Type:          MessageTypeAction,
		Payload:       req,
		SourceAgentID: req.SourceAgentID,
		TargetAgentID: req.TargetAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// GetActionRequest extracts ActionRequest from message payload
func (m *Message) GetActionRequest() (*ActionRequest, bool) {
	req, ok := m.Payload.(*ActionRequest)
	return req, ok
}

// PreserveRequest is the data structure for context preservation actions.
// Agents send this to archivalist when nearing context limits.
type PreserveRequest struct {
	// What to preserve
	Summaries    []string       `json:"summaries,omitempty"`
	KeyInsights  []string       `json:"key_insights,omitempty"`
	Decisions    []string       `json:"decisions,omitempty"`
	Context      map[string]any `json:"context,omitempty"`

	// Metadata
	SourceAgentID string    `json:"source_agent_id"`
	SessionID     string    `json:"session_id,omitempty"`
	Priority      int       `json:"priority,omitempty"` // Higher = more important
	Timestamp     time.Time `json:"timestamp"`
}

// =============================================================================
// Message Accessors
// =============================================================================

// GetRouteRequest extracts RouteRequest from message payload
func (m *Message) GetRouteRequest() (*RouteRequest, bool) {
	req, ok := m.Payload.(*RouteRequest)
	return req, ok
}

// GetForwardedRequest extracts ForwardedRequest from message payload
func (m *Message) GetForwardedRequest() (*ForwardedRequest, bool) {
	fwd, ok := m.Payload.(*ForwardedRequest)
	return fwd, ok
}

// GetRouteResponse extracts RouteResponse from message payload
func (m *Message) GetRouteResponse() (*RouteResponse, bool) {
	resp, ok := m.Payload.(*RouteResponse)
	return resp, ok
}

// GetError extracts error string from message payload
func (m *Message) GetError() (string, bool) {
	err, ok := m.Payload.(string)
	return err, ok
}
