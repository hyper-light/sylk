package guide

import (
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/llmruntime"
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

// PanicCounter is an optional capability of Subscription implementations that
// recover handler panics. Bus operators and tests type-assert to observe
// chronic handler failures without scraping logs. A subscription exposes this
// when its worker keeps running after panics (which is the default for
// ChannelBus).
type PanicCounter interface {
	PanicCount() uint64
}

// DropCounter is an optional capability of Subscription implementations that
// bound their internal queue. Callers type-assert to observe overflow drops
// on a per-subscription basis.
type DropCounter interface {
	DroppedCount() uint64
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

	// ReplyTo carries the return address so agents respond directly
	// to the originator's session topic, removing Guide from the
	// response hot path. Guide can still observe via ObserverTopics.
	ReplyTo *ReplyTo `json:"reply_to,omitempty"`

	// FabricTrace is the W3C-Trace-Context-style causal carrier the
	// Activity Fabric propagates with every cross-agent message. The
	// publisher copies its current FabricContext (TraceID, SpanID,
	// CausalChain, Baggage) into the message; the receiver restores
	// it onto the handler's context.Context so any chokepoint or
	// per-system amplifier emission downstream from the message is
	// causally linked to the publisher's span.
	//
	// Empty FabricTrace is fine — the receiver just doesn't inherit a
	// trace and emits a fresh root chain. See docs/FABRIC.md
	// §"FabricContext as ambient causality".
	FabricTrace *FabricTrace `json:"fabric_trace,omitempty"`
}

// FabricTrace is the over-the-wire serialization of an
// activity.FabricContext attached to a bus message. Kept as a flat
// JSON-friendly shape so it survives bus reroutes and queue durability.
type FabricTrace struct {
	TraceID      string            `json:"trace_id,omitempty"`
	SpanID       string            `json:"span_id,omitempty"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	CausalChain  []string          `json:"causal_chain,omitempty"`
	Baggage      map[string]string `json:"baggage,omitempty"`
}

// maxObserverTopics caps the number of observer topics on ReplyTo
// to prevent unbounded growth.
const maxObserverTopics = 4

// ReplyTo carries the return address for direct agent-to-originator
// response routing. Agents publish their response to Topic; Guide
// (or other observers) can subscribe to ObserverTopics for
// correlation/telemetry without being on the response hot path.
type ReplyTo struct {
	// Topic is the primary response destination.
	Topic string `json:"topic"`

	// ObserverTopics are additional topics that receive a copy of the
	// response for observation (e.g. Guide correlation, telemetry).
	// Capped at maxObserverTopics.
	ObserverTopics []string `json:"observer_topics,omitempty"`
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

// WithReplyTo sets the return address for direct response routing.
// observers are optional topics (capped at maxObserverTopics) that
// receive a copy of the response.
func (m *Message) WithReplyTo(topic string, observers ...string) *Message {
	if len(observers) > maxObserverTopics {
		observers = observers[:maxObserverTopics]
	}
	m.ReplyTo = &ReplyTo{
		Topic:          topic,
		ObserverTopics: observers,
	}
	return m
}

// MessageType indicates what kind of message this is
type MessageType string

const (
	// MessageTypeRequest is a request to be routed
	MessageTypeRequest MessageType = "request"

	// MessageTypeForward is a classified request forwarded to target
	MessageTypeForward MessageType = "forward"

	MessageTypeResponse MessageType = "response"
	MessageTypeStream   MessageType = "stream"

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

	MessageTypeAgentReady MessageType = "agent_ready"

	MessageTypeDAGExecute   MessageType = "dag_execute"
	MessageTypeDAGStatus    MessageType = "dag_status"
	MessageTypeDAGCancel    MessageType = "dag_cancel"
	MessageTypeTaskDispatch MessageType = "task_dispatch"
	MessageTypeTaskComplete MessageType = "task_complete"
	MessageTypeTaskFailed   MessageType = "task_failed"
	MessageTypeTaskHelp     MessageType = "task_help"
	// MessageTypeUserEscalation signals the Guide could not route/handle a
	// request autonomously and needs user intervention — reroute budget
	// exhausted, consensus failure, or explicit ask-user.
	MessageTypeUserEscalation MessageType = "user_escalation"
	// MessageTypeAuditResult carries audit findings from the inspector
	MessageTypeAuditResult MessageType = "audit_result"
	// MessageTypeClarificationRequest is a request for user clarification
	MessageTypeClarificationRequest MessageType = "clarification_request"
	// MessageTypeClarificationResponse is a response to a clarification request
	MessageTypeClarificationResponse MessageType = "clarification_response"
	// MessageTypeDirectConsultation is a direct agent-to-agent consultation
	MessageTypeDirectConsultation MessageType = "direct_consultation"
	// MessageTypeVariantRequest is a request for a variant resolution
	MessageTypeVariantRequest        MessageType = "variant_request"
	MessageTypeValidateTask          MessageType = "validate_task"
	MessageTypeValidationResult      MessageType = "validation_result"
	MessageTypeValidationFull        MessageType = "validation_full"
	MessageTypeValidationCorrections MessageType = "validation_corrections"
	MessageTypeTestPlanRequest       MessageType = "test_plan_request"
	MessageTypeTestPlanResponse      MessageType = "test_plan_response"
	MessageTypeTestDAGRequest        MessageType = "test_dag_request"
	MessageTypeTestsReady            MessageType = "tests_ready"
	MessageTypeTestResults           MessageType = "test_results"
	MessageTypeTestCorrections       MessageType = "test_corrections"
	MessageTypeUserOverride          MessageType = "user_override"
	MessageTypeUserInterrupt         MessageType = "user_interrupt"
	MessageTypeWorkflowComplete      MessageType = "workflow_complete"
	MessageTypeProposal              MessageType = "proposal"
	MessageTypeReroute               MessageType = "reroute"

	// Pipeline and DAG coordination message types
	MessageTypePipelineUpdate    MessageType = "pipeline_update"
	MessageTypePipelineState     MessageType = "pipeline_state"
	MessageTypePipelineQuery     MessageType = "pipeline_query"
	MessageTypePipelineQueryResp MessageType = "pipeline_query_response"
	MessageTypeDAGModify         MessageType = "dag_modify"

	// MessageTypeAuthChanged is published when credential availability changes.
	MessageTypeAuthChanged MessageType = "auth_changed"

	// MessageTypeLayerDecision is published when a DAG layer has a blocking failure
	// requiring a user decision (retry, skip, or abort).
	MessageTypeLayerDecision MessageType = "layer_decision"

	// MessageTypeLayerDecisionResponse carries the user's decision for a blocking layer.
	MessageTypeLayerDecisionResponse MessageType = "layer_decision_response"

	// MessageTypeActivity wraps an ActivityEvent routed through the ChannelBus.
	MessageTypeActivity MessageType = "activity"

	// MessageTypeSignal wraps a signal.SignalMessage routed through the ChannelBus.
	MessageTypeSignal MessageType = "signal"

	// MessageTypeSignalAck wraps a signal.SignalAck routed through the ChannelBus.
	MessageTypeSignalAck MessageType = "signal_ack"

	// MessageTypeProtocolProjection wraps a typed protocol projection
	// snapshot (pipeline or global review) emitted whenever a protocol
	// state mutates. Carried on TopicProtocolProjection for UI /
	// telemetry consumption.
	MessageTypeProtocolProjection MessageType = "protocol_projection"
)

// =============================================================================
// Topic Helpers
// =============================================================================

// TopicSignal returns the ChannelBus topic for a specific signal type.
func TopicSignal(signalType string) string {
	return TopicSignalPrefix + signalType
}

// UserResponseTopic returns the response topic for a user session.
func UserResponseTopic(sessionID string) string {
	return "response." + UserAgentType + "." + sessionID
}

// UserErrorTopic returns the error topic for a user session.
func UserErrorTopic(sessionID string) string {
	return "error." + UserAgentType + "." + sessionID
}

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
	AgentType string `json:"agent_type"`
	Requests  string `json:"requests"`  // request.<agent_type>.<agent_id>
	Responses string `json:"responses"` // response.<agent_type>.<agent_id>
	Errors    string `json:"errors"`    // error.<agent_type>.<agent_id>
}

// NewAgentChannels creates channel names for an agent
func NewAgentChannels(agentType, agentID string) *AgentChannels {
	return &AgentChannels{
		AgentID:   agentID,
		AgentType: agentType,
		Requests:  "request." + agentType + "." + agentID,
		Responses: "response." + agentType + "." + agentID,
		Errors:    "error." + agentType + "." + agentID,
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

	// TopicAuthCredentials is where the AuthRegistry broadcasts credential
	// availability changes. DaemonSet agents subscribe directly; on-demand
	// agents are refreshed via the container-walk refresher.
	TopicAuthCredentials = "auth.credentials"

	// TopicActivity is where activity events are published after migration
	// from the standalone ActivityEventBus to the Guide ChannelBus.
	TopicActivity = "activity.events"

	// TopicProtocolProjection carries typed protocol projection snapshots
	// (pipeline + global review). Consumers that need live protocol state
	// — the UI chat panel for challenge/validation rows, telemetry, the
	// long-lived protocol observer — subscribe here. Producers are the
	// per-task protocol states: each mutation fires a fresh projection
	// through SubscribeProjection, an adapter forwards it here. Using one
	// topic instead of parallel InterAgentToolEvent streams eliminates the
	// drift class that produced phantom-challenge / missing-snapshot bugs.
	TopicProtocolProjection = "protocol.projection"

	// TopicSignalPrefix is the prefix for signal topics routed through the ChannelBus.
	TopicSignalPrefix = "signal."

	// TopicSignalAck is the topic for signal acknowledgment messages.
	TopicSignalAck = "signal.ack"

	// TopicLogIngest is where agents broadcast JSONL log entries for
	// archivalist ingest into its living history.
	TopicLogIngest = "logs.ingest"

	// TopicKnowledgeReady is published by KnowledgeStore when a knowledge
	// backend layer is promoted (partial or full). Active knowledge agents
	// subscribe for cache warming and telemetry.
	TopicKnowledgeReady = "knowledge.ready"

	// TopicUserEscalation is published when a request cannot be handled
	// autonomously and requires user intervention — e.g. reroute budget
	// exhausted, low-confidence chain, or explicit "ask user" escalation.
	// The TUI subscribes to surface the escalation to the user.
	TopicUserEscalation = "guide.user_escalation"

	// UserAgentType is the agent type used for user session service endpoints.
	UserAgentType = "user"
)

// UserEscalationPayload describes a user-intervention request routed through
// TopicUserEscalation. Carries the full reroute chain (if any) and the
// confidence history so the UI can render actionable context.
type UserEscalationPayload struct {
	SessionID             string                  `json:"session_id"`
	CorrelationID         string                  `json:"correlation_id"`
	OriginalCorrelationID string                  `json:"original_correlation_id,omitempty"`
	OriginalInput         string                  `json:"original_input"`
	Reason                string                  `json:"reason"`
	SourceAgentID         string                  `json:"source_agent_id,omitempty"`
	RerouteHistory        []messaging.RerouteHop  `json:"reroute_history,omitempty"`
	ConfidenceChain       []messaging.ConfidenceEntry `json:"confidence_chain,omitempty"`
}

// AgentTopic returns the topic for a specific agent and channel type
func AgentTopic(agentType, agentID string, channelType ChannelType) string {
	var prefix string
	switch channelType {
	case ChannelTypeRequests:
		prefix = "request"
	case ChannelTypeResponses:
		prefix = "response"
	case ChannelTypeErrors:
		prefix = "error"
	default:
		prefix = string(channelType)
	}
	return prefix + "." + agentType + "." + agentID
}

// TopicRequests returns the request topic for an agent
func TopicRequests(agentType, agentID string) string {
	return AgentTopic(agentType, agentID, ChannelTypeRequests)
}

// TopicResponses returns the response topic for an agent
func TopicResponses(agentType, agentID string) string {
	return AgentTopic(agentType, agentID, ChannelTypeResponses)
}

// TopicErrors returns the error topic for an agent
func TopicErrors(agentType, agentID string) string {
	return AgentTopic(agentType, agentID, ChannelTypeErrors)
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

// NewBridgeMessage creates a message from raw network bridge delivery.
// The payload is the original wire bytes; downstream handlers decode as needed.
func NewBridgeMessage(sourceAgentID, targetAgentID string, payload []byte) *Message {
	return &Message{
		ID:            uuid.New().String(),
		Type:          MessageTypeForward,
		SourceAgentID: sourceAgentID,
		TargetAgentID: targetAgentID,
		Payload:       payload,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// AgentAnnouncement is the payload for agent registration/unregistration events
type AgentAnnouncement struct {
	// Agent identity
	AgentID   string   `json:"agent_id"`
	AgentType string   `json:"agent_type"`
	AgentName string   `json:"agent_name"`
	Aliases   []string `json:"aliases,omitempty"`

	// Agent channels (for registered events)
	Channels *AgentChannels `json:"channels,omitempty"`

	// What the agent handles (for registered events)
	Capabilities          *AgentCapabilities        `json:"capabilities,omitempty"`
	Constraints           *AgentConstraints         `json:"constraints,omitempty"`
	Description           string                    `json:"description,omitempty"`
	RuntimeProfiles       []llmruntime.StageProfile `json:"runtime_profiles,omitempty"`
	DefaultRuntimeProfile string                    `json:"default_runtime_profile,omitempty"`

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
		AgentType:       info.Type,
		AgentName:       info.Name,
		Aliases:         info.Aliases,
		Channels:        NewAgentChannels(info.Type, info.ID),
		ActionShortcuts: info.ActionShortcuts,
	}

	if info.Registration != nil {
		announcement.Capabilities = &info.Registration.Capabilities
		announcement.Constraints = &info.Registration.Constraints
		announcement.Description = info.Registration.Description
		announcement.RuntimeProfiles = append([]llmruntime.StageProfile(nil), info.Registration.RuntimeProfiles...)
		announcement.DefaultRuntimeProfile = info.Registration.DefaultRuntimeProfile
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
		AgentType: info.Type,
		AgentName: info.Name,
		Aliases:   info.Aliases,
		Channels:  NewAgentChannels(info.Type, info.ID),
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

type UserInterruptScope string

const (
	UserInterruptScopeRequest UserInterruptScope = "request"
	UserInterruptScopeSession UserInterruptScope = "session"
)

// UserInterruptRequest represents a user-driven cancellation for an in-flight
// routed request, or for all in-flight requests in a session.
type UserInterruptRequest struct {
	CorrelationID string             `json:"correlation_id"`
	SessionID     string             `json:"session_id,omitempty"`
	SourceAgentID string             `json:"source_agent_id"`
	Scope         UserInterruptScope `json:"scope,omitempty"`
	Reason        string             `json:"reason,omitempty"`
	Timestamp     time.Time          `json:"timestamp"`
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

// NewUserInterruptMessage creates a message for a user interruption request.
func NewUserInterruptMessage(id string, req *UserInterruptRequest) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: req.CorrelationID,
		Type:          MessageTypeUserInterrupt,
		Payload:       req,
		SourceAgentID: req.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityHigh,
	}
}

// GetActionRequest extracts ActionRequest from message payload
func (m *Message) GetActionRequest() (*ActionRequest, bool) {
	req, ok := m.Payload.(*ActionRequest)
	return req, ok
}

// GetUserInterruptRequest extracts UserInterruptRequest from message payload.
func (m *Message) GetUserInterruptRequest() (*UserInterruptRequest, bool) {
	req, ok := m.Payload.(*UserInterruptRequest)
	return req, ok
}

// PreserveRequest is the data structure for context preservation actions.
// Agents send this to archivalist when nearing context limits.
type PreserveRequest struct {
	// What to preserve
	Summaries   []string       `json:"summaries,omitempty"`
	KeyInsights []string       `json:"key_insights,omitempty"`
	Decisions   []string       `json:"decisions,omitempty"`
	Context     map[string]any `json:"context,omitempty"`

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

func (m *Message) GetStreamResponse() (*StreamResponse, bool) {
	resp, ok := m.Payload.(*StreamResponse)
	return resp, ok
}

// GetError extracts error string from message payload
func (m *Message) GetError() (string, bool) {
	err, ok := m.Payload.(string)
	return err, ok
}

// NewRerouteMessage creates a message for rerouting a request through Guide.
func NewRerouteMessage(id string, reroute *RerouteRequest) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: reroute.OriginalCorrelationID,
		Type:          MessageTypeReroute,
		Payload:       reroute,
		SourceAgentID: reroute.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityHigh,
	}
}

// GetRerouteRequest extracts RerouteRequest from message payload.
func (m *Message) GetRerouteRequest() (*RerouteRequest, bool) {
	req, ok := m.Payload.(*RerouteRequest)
	return req, ok
}

// =============================================================================
// Auth Changed Messages
// =============================================================================

// AuthEventPayload is the bus payload for credential availability changes.
type AuthEventPayload struct {
	ProviderType string `json:"provider_type"` // "google", "anthropic", "openai"
	AuthMethod   string `json:"auth_method"`   // "oauth", "api_key", etc.
	Available    bool   `json:"available"`
}

// NewAuthChangedMessage creates a message announcing a credential change.
func NewAuthChangedMessage(id string, event AuthEventPayload) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		Type:          MessageTypeAuthChanged,
		Payload:       &event,
		SourceAgentID: "auth_registry",
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityHigh,
	}
}

// GetAuthEvent extracts AuthEventPayload from message payload.
func (m *Message) GetAuthEvent() (*AuthEventPayload, bool) {
	ev, ok := m.Payload.(*AuthEventPayload)
	return ev, ok
}

// =============================================================================
// Activity Messages (for ActivityEventBus → ChannelBus migration)
// =============================================================================

// NewActivityMessage wraps an ActivityEvent as a ChannelBus message.
func NewActivityMessage(sourceAgentID string, event *events.ActivityEvent) *Message {
	return &Message{
		ID:            uuid.New().String(),
		Type:          MessageTypeActivity,
		Payload:       event,
		SourceAgentID: sourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityLow,
	}
}

// GetActivityEvent extracts an ActivityEvent from the message payload.
func (m *Message) GetActivityEvent() (*events.ActivityEvent, bool) {
	ev, ok := m.Payload.(*events.ActivityEvent)
	return ev, ok
}

// =============================================================================
// Signal Messages (for SignalBus → ChannelBus migration)
// =============================================================================

// SignalPayload wraps a signal for ChannelBus transport.
type SignalPayload struct {
	Signal      string `json:"signal"`
	TargetID    string `json:"target_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Payload     any    `json:"payload,omitempty"`
	RequiresAck bool   `json:"requires_ack"`
}

// SignalAckPayload wraps a signal acknowledgment for ChannelBus transport.
type SignalAckPayload struct {
	SignalID     string `json:"signal_id"`
	SubscriberID string `json:"subscriber_id"`
	AgentID      string `json:"agent_id"`
	State        string `json:"state,omitempty"`
	Checkpoint   any    `json:"checkpoint,omitempty"`
}

// NewSignalMessage creates a ChannelBus message wrapping a signal broadcast.
func NewSignalBusMessage(id string, payload *SignalPayload) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: id,
		Type:          MessageTypeSignal,
		Payload:       payload,
		SourceAgentID: "signal_adapter",
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityHigh,
	}
}

// NewSignalAckMessage creates a ChannelBus message wrapping a signal ACK.
func NewSignalAckMessage(id string, ack *SignalAckPayload) *Message {
	if id == "" {
		id = uuid.New().String()
	}
	return &Message{
		ID:            id,
		CorrelationID: ack.SignalID,
		Type:          MessageTypeSignalAck,
		Payload:       ack,
		SourceAgentID: ack.AgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// GetSignalPayload extracts SignalPayload from message payload.
func (m *Message) GetSignalPayload() (*SignalPayload, bool) {
	sp, ok := m.Payload.(*SignalPayload)
	return sp, ok
}

// GetSignalAckPayload extracts SignalAckPayload from message payload.
func (m *Message) GetSignalAckPayload() (*SignalAckPayload, bool) {
	ap, ok := m.Payload.(*SignalAckPayload)
	return ap, ok
}

// =============================================================================
// Knowledge Ready Messages
// =============================================================================

// KnowledgeReadyPayload is the bus payload for knowledge layer promotions.
type KnowledgeReadyPayload struct {
	Level     int      `json:"level"`     // ReadinessLevel (0=none, 1=partial, 2=full)
	Searchers []string `json:"searchers"` // from coordinator.ReadySearchers()
}

// NewKnowledgeReadyMessage creates a message announcing a knowledge promotion.
func NewKnowledgeReadyMessage(payload *KnowledgeReadyPayload) *Message {
	return &Message{
		ID:            uuid.New().String(),
		Type:          MessageTypeActivity,
		Payload:       payload,
		SourceAgentID: "knowledge_store",
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
}

// GetKnowledgeReadyPayload extracts KnowledgeReadyPayload from message payload.
func (m *Message) GetKnowledgeReadyPayload() (*KnowledgeReadyPayload, bool) {
	p, ok := m.Payload.(*KnowledgeReadyPayload)
	return p, ok
}
