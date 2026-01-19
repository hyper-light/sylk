package events

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// EventType Tests
// =============================================================================

func TestEventType_String(t *testing.T) {
	tests := []struct {
		eventType EventType
		expected  string
	}{
		{EventTypeUserPrompt, "user_prompt"},
		{EventTypeUserClarification, "user_clarification"},
		{EventTypeAgentAction, "agent_action"},
		{EventTypeAgentDecision, "agent_decision"},
		{EventTypeAgentError, "agent_error"},
		{EventTypeToolCall, "tool_call"},
		{EventTypeToolResult, "tool_result"},
		{EventTypeToolTimeout, "tool_timeout"},
		{EventTypeLLMRequest, "llm_request"},
		{EventTypeLLMResponse, "llm_response"},
		{EventTypeIndexStart, "index_start"},
		{EventTypeIndexComplete, "index_complete"},
		{EventTypeIndexFileAdded, "index_file_added"},
		{EventTypeContextEviction, "context_eviction"},
		{EventTypeContextRestore, "context_restore"},
		{EventTypeSuccess, "success"},
		{EventTypeFailure, "failure"},
		{EventType(999), "event_type(999)"},
	}

	for _, tt := range tests {
		if got := tt.eventType.String(); got != tt.expected {
			t.Errorf("EventType(%d).String() = %q, want %q", tt.eventType, got, tt.expected)
		}
	}
}

func TestParseEventType(t *testing.T) {
	tests := []struct {
		input    string
		expected EventType
		ok       bool
	}{
		{"user_prompt", EventTypeUserPrompt, true},
		{"user_clarification", EventTypeUserClarification, true},
		{"agent_action", EventTypeAgentAction, true},
		{"agent_decision", EventTypeAgentDecision, true},
		{"agent_error", EventTypeAgentError, true},
		{"tool_call", EventTypeToolCall, true},
		{"tool_result", EventTypeToolResult, true},
		{"tool_timeout", EventTypeToolTimeout, true},
		{"llm_request", EventTypeLLMRequest, true},
		{"llm_response", EventTypeLLMResponse, true},
		{"index_start", EventTypeIndexStart, true},
		{"index_complete", EventTypeIndexComplete, true},
		{"index_file_added", EventTypeIndexFileAdded, true},
		{"context_eviction", EventTypeContextEviction, true},
		{"context_restore", EventTypeContextRestore, true},
		{"success", EventTypeSuccess, true},
		{"failure", EventTypeFailure, true},
		{"invalid", EventType(0), false},
	}

	for _, tt := range tests {
		got, ok := ParseEventType(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseEventType(%q) ok = %v, want %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("ParseEventType(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestEventType_IsValid(t *testing.T) {
	tests := []struct {
		eventType EventType
		valid     bool
	}{
		{EventTypeUserPrompt, true},
		{EventTypeFailure, true},
		{EventType(999), false},
		{EventType(-1), false},
	}

	for _, tt := range tests {
		if got := tt.eventType.IsValid(); got != tt.valid {
			t.Errorf("EventType(%d).IsValid() = %v, want %v", tt.eventType, got, tt.valid)
		}
	}
}

func TestEventType_JSON(t *testing.T) {
	tests := []struct {
		eventType EventType
		expected  string
	}{
		{EventTypeUserPrompt, `"user_prompt"`},
		{EventTypeAgentDecision, `"agent_decision"`},
		{EventTypeToolCall, `"tool_call"`},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.eventType)
		if err != nil {
			t.Errorf("json.Marshal(%v) error = %v", tt.eventType, err)
			continue
		}
		if string(data) != tt.expected {
			t.Errorf("json.Marshal(%v) = %s, want %s", tt.eventType, data, tt.expected)
		}

		var eventType EventType
		if err := json.Unmarshal(data, &eventType); err != nil {
			t.Errorf("json.Unmarshal(%s) error = %v", data, err)
			continue
		}
		if eventType != tt.eventType {
			t.Errorf("json.Unmarshal(%s) = %v, want %v", data, eventType, tt.eventType)
		}
	}
}

// =============================================================================
// EventOutcome Tests
// =============================================================================

func TestEventOutcome_String(t *testing.T) {
	tests := []struct {
		outcome  EventOutcome
		expected string
	}{
		{OutcomePending, "pending"},
		{OutcomeSuccess, "success"},
		{OutcomeFailure, "failure"},
		{EventOutcome(999), "outcome(999)"},
	}

	for _, tt := range tests {
		if got := tt.outcome.String(); got != tt.expected {
			t.Errorf("EventOutcome(%d).String() = %q, want %q", tt.outcome, got, tt.expected)
		}
	}
}

func TestParseEventOutcome(t *testing.T) {
	tests := []struct {
		input    string
		expected EventOutcome
		ok       bool
	}{
		{"pending", OutcomePending, true},
		{"success", OutcomeSuccess, true},
		{"failure", OutcomeFailure, true},
		{"invalid", EventOutcome(0), false},
	}

	for _, tt := range tests {
		got, ok := ParseEventOutcome(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseEventOutcome(%q) ok = %v, want %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("ParseEventOutcome(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestEventOutcome_IsValid(t *testing.T) {
	tests := []struct {
		outcome EventOutcome
		valid   bool
	}{
		{OutcomePending, true},
		{OutcomeSuccess, true},
		{OutcomeFailure, true},
		{EventOutcome(999), false},
	}

	for _, tt := range tests {
		if got := tt.outcome.IsValid(); got != tt.valid {
			t.Errorf("EventOutcome(%d).IsValid() = %v, want %v", tt.outcome, got, tt.valid)
		}
	}
}

func TestEventOutcome_JSON(t *testing.T) {
	tests := []struct {
		outcome  EventOutcome
		expected string
	}{
		{OutcomePending, `"pending"`},
		{OutcomeSuccess, `"success"`},
		{OutcomeFailure, `"failure"`},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.outcome)
		if err != nil {
			t.Errorf("json.Marshal(%v) error = %v", tt.outcome, err)
			continue
		}
		if string(data) != tt.expected {
			t.Errorf("json.Marshal(%v) = %s, want %s", tt.outcome, data, tt.expected)
		}

		var outcome EventOutcome
		if err := json.Unmarshal(data, &outcome); err != nil {
			t.Errorf("json.Unmarshal(%s) error = %v", data, err)
			continue
		}
		if outcome != tt.outcome {
			t.Errorf("json.Unmarshal(%s) = %v, want %v", data, outcome, tt.outcome)
		}
	}
}

// =============================================================================
// ActivityEvent Tests
// =============================================================================

func TestNewActivityEvent(t *testing.T) {
	sessionID := "test-session-123"
	content := "Test event content"

	event := NewActivityEvent(EventTypeUserPrompt, sessionID, content)

	if event.ID == "" {
		t.Error("NewActivityEvent() event.ID should not be empty")
	}
	if event.EventType != EventTypeUserPrompt {
		t.Errorf("NewActivityEvent() event.EventType = %v, want %v", event.EventType, EventTypeUserPrompt)
	}
	if event.SessionID != sessionID {
		t.Errorf("NewActivityEvent() event.SessionID = %q, want %q", event.SessionID, sessionID)
	}
	if event.Content != content {
		t.Errorf("NewActivityEvent() event.Content = %q, want %q", event.Content, content)
	}
	if event.Outcome != OutcomePending {
		t.Errorf("NewActivityEvent() event.Outcome = %v, want %v", event.Outcome, OutcomePending)
	}
	if event.Importance <= 0 {
		t.Errorf("NewActivityEvent() event.Importance = %f, want > 0", event.Importance)
	}
	if event.Data == nil {
		t.Error("NewActivityEvent() event.Data should not be nil")
	}
	if event.Timestamp.IsZero() {
		t.Error("NewActivityEvent() event.Timestamp should not be zero")
	}
}

func TestDefaultImportance(t *testing.T) {
	tests := []struct {
		eventType EventType
		expected  float64
	}{
		{EventTypeAgentDecision, 0.90},
		{EventTypeFailure, 0.85},
		{EventTypeAgentError, 0.85},
		{EventTypeToolTimeout, 0.85},
		{EventTypeSuccess, 0.75},
		{EventTypeUserPrompt, 0.70},
		{EventTypeUserClarification, 0.70},
		{EventTypeToolCall, 0.65},
		{EventTypeAgentAction, 0.60},
		{EventTypeIndexComplete, 0.55},
		{EventTypeToolResult, 0.50},
		{EventTypeLLMResponse, 0.50},
		{EventTypeContextEviction, 0.45},
		{EventTypeContextRestore, 0.45},
		{EventTypeLLMRequest, 0.40},
		{EventTypeIndexStart, 0.35},
		{EventTypeIndexFileAdded, 0.30},
	}

	for _, tt := range tests {
		event := NewActivityEvent(tt.eventType, "session", "content")
		if event.Importance != tt.expected {
			t.Errorf("defaultImportance(%v) = %f, want %f", tt.eventType, event.Importance, tt.expected)
		}
	}
}

func TestActivityEvent_JSON(t *testing.T) {
	event := &ActivityEvent{
		ID:         "event-123",
		EventType:  EventTypeAgentAction,
		Timestamp:  time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		SessionID:  "session-456",
		AgentID:    "agent-789",
		Content:    "Test action content",
		Summary:    "Brief summary",
		Category:   "testing",
		FilePaths:  []string{"/path/to/file1.go", "/path/to/file2.go"},
		Keywords:   []string{"test", "action"},
		RelatedIDs: []string{"related-1", "related-2"},
		Outcome:    OutcomeSuccess,
		Importance: 0.85,
		Data: map[string]any{
			"key1": "value1",
			"key2": 42,
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(ActivityEvent) error = %v", err)
	}

	var decoded ActivityEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ActivityEvent) error = %v", err)
	}

	if decoded.ID != event.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, event.ID)
	}
	if decoded.EventType != event.EventType {
		t.Errorf("EventType = %v, want %v", decoded.EventType, event.EventType)
	}
	if !decoded.Timestamp.Equal(event.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", decoded.Timestamp, event.Timestamp)
	}
	if decoded.SessionID != event.SessionID {
		t.Errorf("SessionID = %q, want %q", decoded.SessionID, event.SessionID)
	}
	if decoded.AgentID != event.AgentID {
		t.Errorf("AgentID = %q, want %q", decoded.AgentID, event.AgentID)
	}
	if decoded.Content != event.Content {
		t.Errorf("Content = %q, want %q", decoded.Content, event.Content)
	}
	if decoded.Summary != event.Summary {
		t.Errorf("Summary = %q, want %q", decoded.Summary, event.Summary)
	}
	if decoded.Category != event.Category {
		t.Errorf("Category = %q, want %q", decoded.Category, event.Category)
	}
	if len(decoded.FilePaths) != len(event.FilePaths) {
		t.Errorf("len(FilePaths) = %d, want %d", len(decoded.FilePaths), len(event.FilePaths))
	}
	if len(decoded.Keywords) != len(event.Keywords) {
		t.Errorf("len(Keywords) = %d, want %d", len(decoded.Keywords), len(event.Keywords))
	}
	if len(decoded.RelatedIDs) != len(event.RelatedIDs) {
		t.Errorf("len(RelatedIDs) = %d, want %d", len(decoded.RelatedIDs), len(event.RelatedIDs))
	}
	if decoded.Outcome != event.Outcome {
		t.Errorf("Outcome = %v, want %v", decoded.Outcome, event.Outcome)
	}
	if decoded.Importance != event.Importance {
		t.Errorf("Importance = %f, want %f", decoded.Importance, event.Importance)
	}
}

func TestActivityEvent_MinimalFields(t *testing.T) {
	event := &ActivityEvent{
		ID:        "minimal-event",
		EventType: EventTypeSuccess,
		Timestamp: time.Now(),
		SessionID: "session-1",
		Content:   "Minimal event",
		Outcome:   OutcomeSuccess,
		Importance: 0.75,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(minimal ActivityEvent) error = %v", err)
	}

	var decoded ActivityEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(minimal ActivityEvent) error = %v", err)
	}

	if decoded.ID != event.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, event.ID)
	}
	if decoded.EventType != event.EventType {
		t.Errorf("EventType = %v, want %v", decoded.EventType, event.EventType)
	}
}
