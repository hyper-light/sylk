package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// EventType Enum
// =============================================================================

// EventType represents the type of activity event in the system.
type EventType int

const (
	// User interaction events
	EventTypeUserPrompt        EventType = 0
	EventTypeUserClarification EventType = 1

	// Agent activity events
	EventTypeAgentAction   EventType = 2
	EventTypeAgentDecision EventType = 3
	EventTypeAgentError    EventType = 4

	// Tool execution events
	EventTypeToolCall    EventType = 5
	EventTypeToolResult  EventType = 6
	EventTypeToolTimeout EventType = 7

	// LLM interaction events
	EventTypeLLMRequest  EventType = 8
	EventTypeLLMResponse EventType = 9

	// Indexing events
	EventTypeIndexStart     EventType = 10
	EventTypeIndexComplete  EventType = 11
	EventTypeIndexFileAdded EventType = 12

	// Context management events
	EventTypeContextEviction EventType = 13
	EventTypeContextRestore  EventType = 14

	// Outcome events
	EventTypeSuccess EventType = 15
	EventTypeFailure EventType = 16
)

// ValidEventTypes returns all valid EventType values.
func ValidEventTypes() []EventType {
	return []EventType{
		EventTypeUserPrompt,
		EventTypeUserClarification,
		EventTypeAgentAction,
		EventTypeAgentDecision,
		EventTypeAgentError,
		EventTypeToolCall,
		EventTypeToolResult,
		EventTypeToolTimeout,
		EventTypeLLMRequest,
		EventTypeLLMResponse,
		EventTypeIndexStart,
		EventTypeIndexComplete,
		EventTypeIndexFileAdded,
		EventTypeContextEviction,
		EventTypeContextRestore,
		EventTypeSuccess,
		EventTypeFailure,
	}
}

// IsValid returns true if the event type is a recognized value.
func (et EventType) IsValid() bool {
	for _, valid := range ValidEventTypes() {
		if et == valid {
			return true
		}
	}
	return false
}

func (et EventType) String() string {
	switch et {
	case EventTypeUserPrompt:
		return "user_prompt"
	case EventTypeUserClarification:
		return "user_clarification"
	case EventTypeAgentAction:
		return "agent_action"
	case EventTypeAgentDecision:
		return "agent_decision"
	case EventTypeAgentError:
		return "agent_error"
	case EventTypeToolCall:
		return "tool_call"
	case EventTypeToolResult:
		return "tool_result"
	case EventTypeToolTimeout:
		return "tool_timeout"
	case EventTypeLLMRequest:
		return "llm_request"
	case EventTypeLLMResponse:
		return "llm_response"
	case EventTypeIndexStart:
		return "index_start"
	case EventTypeIndexComplete:
		return "index_complete"
	case EventTypeIndexFileAdded:
		return "index_file_added"
	case EventTypeContextEviction:
		return "context_eviction"
	case EventTypeContextRestore:
		return "context_restore"
	case EventTypeSuccess:
		return "success"
	case EventTypeFailure:
		return "failure"
	default:
		return fmt.Sprintf("event_type(%d)", et)
	}
}

func ParseEventType(value string) (EventType, bool) {
	switch value {
	case "user_prompt":
		return EventTypeUserPrompt, true
	case "user_clarification":
		return EventTypeUserClarification, true
	case "agent_action":
		return EventTypeAgentAction, true
	case "agent_decision":
		return EventTypeAgentDecision, true
	case "agent_error":
		return EventTypeAgentError, true
	case "tool_call":
		return EventTypeToolCall, true
	case "tool_result":
		return EventTypeToolResult, true
	case "tool_timeout":
		return EventTypeToolTimeout, true
	case "llm_request":
		return EventTypeLLMRequest, true
	case "llm_response":
		return EventTypeLLMResponse, true
	case "index_start":
		return EventTypeIndexStart, true
	case "index_complete":
		return EventTypeIndexComplete, true
	case "index_file_added":
		return EventTypeIndexFileAdded, true
	case "context_eviction":
		return EventTypeContextEviction, true
	case "context_restore":
		return EventTypeContextRestore, true
	case "success":
		return EventTypeSuccess, true
	case "failure":
		return EventTypeFailure, true
	default:
		return EventType(0), false
	}
}

func (et EventType) MarshalJSON() ([]byte, error) {
	return json.Marshal(et.String())
}

func (et *EventType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseEventType(asString); ok {
			*et = parsed
			return nil
		}
		return fmt.Errorf("invalid event type: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*et = EventType(asInt)
		return nil
	}

	return fmt.Errorf("invalid event type")
}

// =============================================================================
// EventOutcome Enum
// =============================================================================

// EventOutcome represents the outcome status of an event.
type EventOutcome int

const (
	OutcomePending EventOutcome = 0
	OutcomeSuccess EventOutcome = 1
	OutcomeFailure EventOutcome = 2
)

// ValidEventOutcomes returns all valid EventOutcome values.
func ValidEventOutcomes() []EventOutcome {
	return []EventOutcome{
		OutcomePending,
		OutcomeSuccess,
		OutcomeFailure,
	}
}

// IsValid returns true if the event outcome is a recognized value.
func (eo EventOutcome) IsValid() bool {
	for _, valid := range ValidEventOutcomes() {
		if eo == valid {
			return true
		}
	}
	return false
}

func (eo EventOutcome) String() string {
	switch eo {
	case OutcomePending:
		return "pending"
	case OutcomeSuccess:
		return "success"
	case OutcomeFailure:
		return "failure"
	default:
		return fmt.Sprintf("outcome(%d)", eo)
	}
}

func ParseEventOutcome(value string) (EventOutcome, bool) {
	switch value {
	case "pending":
		return OutcomePending, true
	case "success":
		return OutcomeSuccess, true
	case "failure":
		return OutcomeFailure, true
	default:
		return EventOutcome(0), false
	}
}

func (eo EventOutcome) MarshalJSON() ([]byte, error) {
	return json.Marshal(eo.String())
}

func (eo *EventOutcome) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseEventOutcome(asString); ok {
			*eo = parsed
			return nil
		}
		return fmt.Errorf("invalid event outcome: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*eo = EventOutcome(asInt)
		return nil
	}

	return fmt.Errorf("invalid event outcome")
}

// =============================================================================
// ActivityEvent Type
// =============================================================================

// ActivityEvent represents a single activity event in the system.
type ActivityEvent struct {
	ID         string               `json:"id"`
	EventType  EventType            `json:"event_type"`
	Timestamp  time.Time            `json:"timestamp"`
	SessionID  string               `json:"session_id"`
	AgentID    string               `json:"agent_id,omitempty"`
	Content    string               `json:"content"`
	Summary    string               `json:"summary,omitempty"`
	Category   string               `json:"category,omitempty"`
	FilePaths  []string             `json:"file_paths,omitempty"`
	Keywords   []string             `json:"keywords,omitempty"`
	RelatedIDs []string             `json:"related_ids,omitempty"`
	Outcome    EventOutcome         `json:"outcome"`
	Importance float64              `json:"importance"`
	Data       map[string]any       `json:"data,omitempty"`
}

// NewActivityEvent creates a new ActivityEvent with the given type, session ID, and content.
// The event is assigned a unique ID, current timestamp, and default importance based on type.
func NewActivityEvent(eventType EventType, sessionID, content string) *ActivityEvent {
	return &ActivityEvent{
		ID:         uuid.New().String(),
		EventType:  eventType,
		Timestamp:  time.Now(),
		SessionID:  sessionID,
		Content:    content,
		Outcome:    OutcomePending,
		Importance: defaultImportance(eventType),
		Data:       make(map[string]any),
	}
}

// defaultImportance returns the default importance score for an event type.
// Higher scores indicate more important events that should be prioritized.
func defaultImportance(eventType EventType) float64 {
	switch eventType {
	case EventTypeAgentDecision:
		return 0.90
	case EventTypeFailure, EventTypeAgentError, EventTypeToolTimeout:
		return 0.85
	case EventTypeSuccess:
		return 0.75
	case EventTypeUserPrompt, EventTypeUserClarification:
		return 0.70
	case EventTypeToolCall:
		return 0.65
	case EventTypeAgentAction:
		return 0.60
	case EventTypeIndexComplete:
		return 0.55
	case EventTypeToolResult, EventTypeLLMResponse:
		return 0.50
	case EventTypeContextEviction, EventTypeContextRestore:
		return 0.45
	case EventTypeLLMRequest:
		return 0.40
	case EventTypeIndexStart:
		return 0.35
	case EventTypeIndexFileAdded:
		return 0.30
	default:
		return 0.50
	}
}

