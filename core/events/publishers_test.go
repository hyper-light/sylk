package events

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// testSubscriber implements EventSubscriber for capturing events in tests.
type testSubscriber struct {
	id         string
	eventTypes []EventType
	events     chan *ActivityEvent
	mu         sync.Mutex
	received   []*ActivityEvent
}

func newTestSubscriber(id string, eventTypes ...EventType) *testSubscriber {
	return &testSubscriber{
		id:         id,
		eventTypes: eventTypes,
		events:     make(chan *ActivityEvent, 100),
		received:   make([]*ActivityEvent, 0),
	}
}

func (s *testSubscriber) ID() string {
	return s.id
}

func (s *testSubscriber) EventTypes() []EventType {
	return s.eventTypes
}

func (s *testSubscriber) OnEvent(event *ActivityEvent) error {
	s.mu.Lock()
	s.received = append(s.received, event)
	s.mu.Unlock()

	select {
	case s.events <- event:
	default:
	}
	return nil
}

func (s *testSubscriber) getEvents() []*ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*ActivityEvent{}, s.received...)
}

func (s *testSubscriber) waitForEvent(timeout time.Duration) *ActivityEvent {
	select {
	case event := <-s.events:
		return event
	case <-time.After(timeout):
		return nil
	}
}

// setupTestBus creates a test bus and returns it along with a wildcard subscriber.
func setupTestBus() (*ActivityEventBus, *testSubscriber) {
	bus := NewActivityEventBus(100)
	bus.Start()

	sub := newTestSubscriber("test-subscriber")
	bus.Subscribe(sub)

	return bus, sub
}

// =============================================================================
// Guide Publisher Tests
// =============================================================================

func TestGuidePublisher_New(t *testing.T) {
	t.Parallel()

	bus := NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewGuidePublisher(bus, "session-123")

	if publisher == nil {
		t.Fatal("Expected non-nil publisher")
	}

	if publisher.bus != bus {
		t.Error("Expected bus to be set")
	}

	if publisher.sessionID != "session-123" {
		t.Errorf("Expected sessionID 'session-123', got '%s'", publisher.sessionID)
	}
}

func TestGuidePublisher_PublishUserPrompt(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewGuidePublisher(bus, "session-123")

	err := publisher.PublishUserPrompt("What is the weather today?")
	if err != nil {
		t.Fatalf("PublishUserPrompt returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeUserPrompt {
		t.Errorf("Expected EventTypeUserPrompt, got %s", event.EventType)
	}

	// Verify session ID
	if event.SessionID != "session-123" {
		t.Errorf("Expected sessionID 'session-123', got '%s'", event.SessionID)
	}

	// Verify content
	if event.Content != "What is the weather today?" {
		t.Errorf("Expected content 'What is the weather today?', got '%s'", event.Content)
	}

	// Verify category
	if event.Category != "user" {
		t.Errorf("Expected category 'user', got '%s'", event.Category)
	}

	// Verify data
	if event.Data["prompt"] != "What is the weather today?" {
		t.Errorf("Expected prompt in data, got '%v'", event.Data["prompt"])
	}

	// Verify ID is set
	if event.ID == "" {
		t.Error("Expected non-empty ID")
	}

	// Verify timestamp is set
	if event.Timestamp.IsZero() {
		t.Error("Expected non-zero timestamp")
	}
}

func TestGuidePublisher_PublishRoutingDecision(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewGuidePublisher(bus, "session-123")

	err := publisher.PublishRoutingDecision("guide", "coder", "User requested code generation")
	if err != nil {
		t.Fatalf("PublishRoutingDecision returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeAgentDecision {
		t.Errorf("Expected EventTypeAgentDecision, got %s", event.EventType)
	}

	// Verify data contains routing info
	if event.Data["fromAgent"] != "guide" {
		t.Errorf("Expected fromAgent 'guide', got '%v'", event.Data["fromAgent"])
	}

	if event.Data["toAgent"] != "coder" {
		t.Errorf("Expected toAgent 'coder', got '%v'", event.Data["toAgent"])
	}

	if event.Data["reason"] != "User requested code generation" {
		t.Errorf("Expected reason 'User requested code generation', got '%v'", event.Data["reason"])
	}

	// Verify category
	if event.Category != "routing" {
		t.Errorf("Expected category 'routing', got '%s'", event.Category)
	}
}

func TestGuidePublisher_PublishClarificationRequest(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewGuidePublisher(bus, "session-123")

	err := publisher.PublishClarificationRequest("Which file would you like me to modify?")
	if err != nil {
		t.Fatalf("PublishClarificationRequest returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeUserClarification {
		t.Errorf("Expected EventTypeUserClarification, got %s", event.EventType)
	}

	// Verify content
	if event.Content != "Which file would you like me to modify?" {
		t.Errorf("Expected content 'Which file would you like me to modify?', got '%s'", event.Content)
	}

	// Verify data
	if event.Data["question"] != "Which file would you like me to modify?" {
		t.Errorf("Expected question in data, got '%v'", event.Data["question"])
	}

	// Verify category
	if event.Category != "clarification" {
		t.Errorf("Expected category 'clarification', got '%s'", event.Category)
	}
}

func TestGuidePublisher_NilBus(t *testing.T) {
	t.Parallel()

	publisher := NewGuidePublisher(nil, "session-123")

	// Should not panic and should return ErrNilBus error
	if err := publisher.PublishUserPrompt("test"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishRoutingDecision("a", "b", "c"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishClarificationRequest("test"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}
}

// =============================================================================
// Tool Publisher Tests
// =============================================================================

func TestToolPublisher_New(t *testing.T) {
	t.Parallel()

	bus := NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewToolPublisher(bus, "session-123", "agent-1")

	if publisher == nil {
		t.Fatal("Expected non-nil publisher")
	}

	if publisher.bus != bus {
		t.Error("Expected bus to be set")
	}

	if publisher.sessionID != "session-123" {
		t.Errorf("Expected sessionID 'session-123', got '%s'", publisher.sessionID)
	}

	if publisher.agentID != "agent-1" {
		t.Errorf("Expected agentID 'agent-1', got '%s'", publisher.agentID)
	}
}

func TestToolPublisher_PublishToolCall(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewToolPublisher(bus, "session-123", "agent-1")

	params := map[string]any{
		"path":    "/src/main.go",
		"content": "package main",
	}

	err := publisher.PublishToolCall("write_file", params)
	if err != nil {
		t.Fatalf("PublishToolCall returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeToolCall {
		t.Errorf("Expected EventTypeToolCall, got %s", event.EventType)
	}

	// Verify agent ID
	if event.AgentID != "agent-1" {
		t.Errorf("Expected agentID 'agent-1', got '%s'", event.AgentID)
	}

	// Verify data
	if event.Data["tool_name"] != "write_file" {
		t.Errorf("Expected tool_name 'write_file', got '%v'", event.Data["tool_name"])
	}

	capturedParams, ok := event.Data["params"].(map[string]any)
	if !ok {
		t.Fatal("Expected params to be map[string]any")
	}

	if capturedParams["path"] != "/src/main.go" {
		t.Errorf("Expected path '/src/main.go', got '%v'", capturedParams["path"])
	}

	// Verify category
	if event.Category != "tool" {
		t.Errorf("Expected category 'tool', got '%s'", event.Category)
	}
}

func TestToolPublisher_PublishToolResult_Success(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewToolPublisher(bus, "session-123", "agent-1")

	result := map[string]any{
		"bytes_written": 1024,
		"file_path":     "/src/main.go",
	}

	err := publisher.PublishToolResult("write_file", result, true)
	if err != nil {
		t.Fatalf("PublishToolResult returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeToolResult {
		t.Errorf("Expected EventTypeToolResult, got %s", event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeSuccess {
		t.Errorf("Expected OutcomeSuccess, got %s", event.Outcome)
	}

	// Verify data
	if event.Data["tool_name"] != "write_file" {
		t.Errorf("Expected tool_name 'write_file', got '%v'", event.Data["tool_name"])
	}

	if event.Data["outcome"] != "success" {
		t.Errorf("Expected outcome 'success', got '%v'", event.Data["outcome"])
	}

	capturedResult, ok := event.Data["result"].(map[string]any)
	if !ok {
		t.Fatal("Expected result to be map[string]any")
	}

	if capturedResult["bytes_written"] != 1024 {
		t.Errorf("Expected bytes_written 1024, got '%v'", capturedResult["bytes_written"])
	}
}

func TestToolPublisher_PublishToolResult_Failure(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewToolPublisher(bus, "session-123", "agent-1")

	err := publisher.PublishToolResult("read_file", "file not found", false)
	if err != nil {
		t.Fatalf("PublishToolResult returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify outcome
	if event.Outcome != OutcomeFailure {
		t.Errorf("Expected OutcomeFailure, got %s", event.Outcome)
	}

	if event.Data["outcome"] != "failure" {
		t.Errorf("Expected outcome 'failure', got '%v'", event.Data["outcome"])
	}
}

func TestToolPublisher_PublishToolTimeout(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewToolPublisher(bus, "session-123", "agent-1")

	timeout := 30 * time.Second

	err := publisher.PublishToolTimeout("long_running_tool", timeout)
	if err != nil {
		t.Fatalf("PublishToolTimeout returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeToolTimeout {
		t.Errorf("Expected EventTypeToolTimeout, got %s", event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeFailure {
		t.Errorf("Expected OutcomeFailure, got %s", event.Outcome)
	}

	// Verify data
	if event.Data["tool_name"] != "long_running_tool" {
		t.Errorf("Expected tool_name 'long_running_tool', got '%v'", event.Data["tool_name"])
	}

	if event.Data["timeout_duration"] != "30s" {
		t.Errorf("Expected timeout_duration '30s', got '%v'", event.Data["timeout_duration"])
	}

	if event.Data["timeout_ms"] != int64(30000) {
		t.Errorf("Expected timeout_ms 30000, got '%v'", event.Data["timeout_ms"])
	}
}

func TestToolPublisher_NilBus(t *testing.T) {
	t.Parallel()

	publisher := NewToolPublisher(nil, "session-123", "agent-1")

	// Should not panic and should return ErrNilBus error
	if err := publisher.PublishToolCall("test", nil); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishToolResult("test", nil, true); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishToolTimeout("test", time.Second); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}
}

// =============================================================================
// Agent Publisher Tests
// =============================================================================

func TestAgentPublisher_New(t *testing.T) {
	t.Parallel()

	bus := NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "coder-agent")

	if publisher == nil {
		t.Fatal("Expected non-nil publisher")
	}

	if publisher.bus != bus {
		t.Error("Expected bus to be set")
	}

	if publisher.sessionID != "session-123" {
		t.Errorf("Expected sessionID 'session-123', got '%s'", publisher.sessionID)
	}

	if publisher.agentID != "coder-agent" {
		t.Errorf("Expected agentID 'coder-agent', got '%s'", publisher.agentID)
	}
}

func TestAgentPublisher_PublishAgentAction(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "coder-agent")

	err := publisher.PublishAgentAction("file_edit", "Modified main.go to add error handling")
	if err != nil {
		t.Fatalf("PublishAgentAction returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeAgentAction {
		t.Errorf("Expected EventTypeAgentAction, got %s", event.EventType)
	}

	// Verify agent ID
	if event.AgentID != "coder-agent" {
		t.Errorf("Expected agentID 'coder-agent', got '%s'", event.AgentID)
	}

	// Verify data
	if event.Data["action"] != "file_edit" {
		t.Errorf("Expected action 'file_edit', got '%v'", event.Data["action"])
	}

	if event.Data["details"] != "Modified main.go to add error handling" {
		t.Errorf("Expected details 'Modified main.go to add error handling', got '%v'", event.Data["details"])
	}

	// Verify category
	if event.Category != "agent" {
		t.Errorf("Expected category 'agent', got '%s'", event.Category)
	}
}

func TestAgentPublisher_PublishAgentDecision(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "coder-agent")

	err := publisher.PublishAgentDecision("refactor", "Code duplication detected in utils package")
	if err != nil {
		t.Fatalf("PublishAgentDecision returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeAgentDecision {
		t.Errorf("Expected EventTypeAgentDecision, got %s", event.EventType)
	}

	// Verify data
	if event.Data["decision"] != "refactor" {
		t.Errorf("Expected decision 'refactor', got '%v'", event.Data["decision"])
	}

	if event.Data["rationale"] != "Code duplication detected in utils package" {
		t.Errorf("Expected rationale 'Code duplication detected in utils package', got '%v'", event.Data["rationale"])
	}
}

func TestAgentPublisher_PublishAgentError(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "coder-agent")

	testErr := errors.New("syntax error in generated code")
	err := publisher.PublishAgentError(testErr, "Code generation for authentication module")
	if err != nil {
		t.Fatalf("PublishAgentError returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeAgentError {
		t.Errorf("Expected EventTypeAgentError, got %s", event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeFailure {
		t.Errorf("Expected OutcomeFailure, got %s", event.Outcome)
	}

	// Verify data
	if event.Data["error_message"] != "syntax error in generated code" {
		t.Errorf("Expected error_message 'syntax error in generated code', got '%v'", event.Data["error_message"])
	}

	if event.Data["context"] != "Code generation for authentication module" {
		t.Errorf("Expected context 'Code generation for authentication module', got '%v'", event.Data["context"])
	}
}

func TestAgentPublisher_PublishAgentError_NilError(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "coder-agent")

	// Should handle nil error gracefully
	err := publisher.PublishAgentError(nil, "Some context")
	if err != nil {
		t.Fatalf("PublishAgentError returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify error_message is empty string
	if event.Data["error_message"] != "" {
		t.Errorf("Expected empty error_message for nil error, got '%v'", event.Data["error_message"])
	}
}

func TestAgentPublisher_PublishSuccess(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "coder-agent")

	err := publisher.PublishSuccess("Successfully completed code refactoring")
	if err != nil {
		t.Fatalf("PublishSuccess returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeSuccess {
		t.Errorf("Expected EventTypeSuccess, got %s", event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeSuccess {
		t.Errorf("Expected OutcomeSuccess, got %s", event.Outcome)
	}

	// Verify content
	if event.Content != "Successfully completed code refactoring" {
		t.Errorf("Expected content 'Successfully completed code refactoring', got '%s'", event.Content)
	}

	// Verify data
	if event.Data["summary"] != "Successfully completed code refactoring" {
		t.Errorf("Expected summary in data, got '%v'", event.Data["summary"])
	}

	// Verify category
	if event.Category != "outcome" {
		t.Errorf("Expected category 'outcome', got '%s'", event.Category)
	}
}

func TestAgentPublisher_PublishFailure(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "coder-agent")

	testErr := errors.New("compilation failed")
	err := publisher.PublishFailure(testErr, "Build process failed for main package")
	if err != nil {
		t.Fatalf("PublishFailure returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeFailure {
		t.Errorf("Expected EventTypeFailure, got %s", event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeFailure {
		t.Errorf("Expected OutcomeFailure, got %s", event.Outcome)
	}

	// Verify data
	if event.Data["error"] != "compilation failed" {
		t.Errorf("Expected error 'compilation failed', got '%v'", event.Data["error"])
	}

	if event.Data["summary"] != "Build process failed for main package" {
		t.Errorf("Expected summary 'Build process failed for main package', got '%v'", event.Data["summary"])
	}
}

func TestAgentPublisher_PublishFailure_NilError(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "coder-agent")

	// Should handle nil error gracefully
	err := publisher.PublishFailure(nil, "Unknown failure")
	if err != nil {
		t.Fatalf("PublishFailure returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify error is empty string
	if event.Data["error"] != "" {
		t.Errorf("Expected empty error for nil error, got '%v'", event.Data["error"])
	}
}

func TestAgentPublisher_NilBus(t *testing.T) {
	t.Parallel()

	publisher := NewAgentPublisher(nil, "session-123", "agent-1")

	// Should not panic and should return ErrNilBus error
	if err := publisher.PublishAgentAction("test", "test"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishAgentDecision("test", "test"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishAgentError(errors.New("test"), "test"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishSuccess("test"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishFailure(errors.New("test"), "test"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}
}

// =============================================================================
// LLM Publisher Tests
// =============================================================================

func TestLLMPublisher_New(t *testing.T) {
	t.Parallel()

	bus := NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewLLMPublisher(bus, "session-123", "coder-agent")

	if publisher == nil {
		t.Fatal("Expected non-nil publisher")
	}

	if publisher.bus != bus {
		t.Error("Expected bus to be set")
	}

	if publisher.sessionID != "session-123" {
		t.Errorf("Expected sessionID 'session-123', got '%s'", publisher.sessionID)
	}

	if publisher.agentID != "coder-agent" {
		t.Errorf("Expected agentID 'coder-agent', got '%s'", publisher.agentID)
	}
}

func TestLLMPublisher_PublishLLMRequest(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewLLMPublisher(bus, "session-123", "coder-agent")

	err := publisher.PublishLLMRequest("gpt-4", 1500)
	if err != nil {
		t.Fatalf("PublishLLMRequest returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeLLMRequest {
		t.Errorf("Expected EventTypeLLMRequest, got %s", event.EventType)
	}

	// Verify agent ID
	if event.AgentID != "coder-agent" {
		t.Errorf("Expected agentID 'coder-agent', got '%s'", event.AgentID)
	}

	// Verify data
	if event.Data["model"] != "gpt-4" {
		t.Errorf("Expected model 'gpt-4', got '%v'", event.Data["model"])
	}

	if event.Data["input_tokens"] != 1500 {
		t.Errorf("Expected input_tokens 1500, got '%v'", event.Data["input_tokens"])
	}

	// Verify category
	if event.Category != "llm" {
		t.Errorf("Expected category 'llm', got '%s'", event.Category)
	}
}

func TestLLMPublisher_PublishLLMResponse(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewLLMPublisher(bus, "session-123", "coder-agent")

	duration := 2 * time.Second
	err := publisher.PublishLLMResponse("gpt-4", 1500, 500, duration)
	if err != nil {
		t.Fatalf("PublishLLMResponse returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeLLMResponse {
		t.Errorf("Expected EventTypeLLMResponse, got %s", event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeSuccess {
		t.Errorf("Expected OutcomeSuccess, got %s", event.Outcome)
	}

	// Verify data
	if event.Data["model"] != "gpt-4" {
		t.Errorf("Expected model 'gpt-4', got '%v'", event.Data["model"])
	}

	if event.Data["input_tokens"] != 1500 {
		t.Errorf("Expected input_tokens 1500, got '%v'", event.Data["input_tokens"])
	}

	if event.Data["output_tokens"] != 500 {
		t.Errorf("Expected output_tokens 500, got '%v'", event.Data["output_tokens"])
	}

	if event.Data["duration_ms"] != int64(2000) {
		t.Errorf("Expected duration_ms 2000, got '%v'", event.Data["duration_ms"])
	}

	// Expected tokens_per_sec: 500 tokens / 2 seconds = 250 tokens/sec
	tokensPerSec, ok := event.Data["tokens_per_sec"].(float64)
	if !ok {
		t.Fatal("Expected tokens_per_sec to be float64")
	}

	if tokensPerSec != 250.0 {
		t.Errorf("Expected tokens_per_sec 250.0, got %v", tokensPerSec)
	}
}

func TestLLMPublisher_PublishLLMResponse_ZeroDuration(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewLLMPublisher(bus, "session-123", "coder-agent")

	// Zero duration should not cause division by zero
	err := publisher.PublishLLMResponse("gpt-4", 1500, 500, 0)
	if err != nil {
		t.Fatalf("PublishLLMResponse returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// tokens_per_sec should be 0 when duration is 0
	tokensPerSec, ok := event.Data["tokens_per_sec"].(float64)
	if !ok {
		t.Fatal("Expected tokens_per_sec to be float64")
	}

	if tokensPerSec != 0.0 {
		t.Errorf("Expected tokens_per_sec 0.0 for zero duration, got %v", tokensPerSec)
	}
}

func TestLLMPublisher_PublishLLMError(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewLLMPublisher(bus, "session-123", "coder-agent")

	testErr := errors.New("rate limit exceeded")
	err := publisher.PublishLLMError("gpt-4", testErr, "Token generation")
	if err != nil {
		t.Fatalf("PublishLLMError returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify event type
	if event.EventType != EventTypeLLMResponse {
		t.Errorf("Expected EventTypeLLMResponse, got %s", event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeFailure {
		t.Errorf("Expected OutcomeFailure, got %s", event.Outcome)
	}

	// Verify data
	if event.Data["model"] != "gpt-4" {
		t.Errorf("Expected model 'gpt-4', got '%v'", event.Data["model"])
	}

	if event.Data["error"] != "rate limit exceeded" {
		t.Errorf("Expected error 'rate limit exceeded', got '%v'", event.Data["error"])
	}

	if event.Data["error_context"] != "Token generation" {
		t.Errorf("Expected error_context 'Token generation', got '%v'", event.Data["error_context"])
	}
}

func TestLLMPublisher_PublishLLMError_NilError(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewLLMPublisher(bus, "session-123", "coder-agent")

	// Should handle nil error gracefully
	err := publisher.PublishLLMError("gpt-4", nil, "Unknown error")
	if err != nil {
		t.Fatalf("PublishLLMError returned error: %v", err)
	}

	event := sub.waitForEvent(200 * time.Millisecond)
	if event == nil {
		t.Fatal("Expected to receive event")
	}

	// Verify error is "unknown error" when nil error is passed
	if event.Data["error"] != "unknown error" {
		t.Errorf("Expected 'unknown error' for nil error, got '%v'", event.Data["error"])
	}
}

func TestLLMPublisher_NilBus(t *testing.T) {
	t.Parallel()

	publisher := NewLLMPublisher(nil, "session-123", "agent-1")

	// Should not panic and should return ErrNilBus error
	if err := publisher.PublishLLMRequest("gpt-4", 100); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishLLMResponse("gpt-4", 100, 50, time.Second); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}

	if err := publisher.PublishLLMError("gpt-4", errors.New("test"), "test"); !errors.Is(err, ErrNilBus) {
		t.Errorf("Expected ErrNilBus for nil bus, got %v", err)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestIntegration_MultiplePublishersSameBus(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	// Create multiple publishers
	guidePublisher := NewGuidePublisher(bus, "session-123")
	toolPublisher := NewToolPublisher(bus, "session-123", "agent-1")
	agentPublisher := NewAgentPublisher(bus, "session-123", "agent-1")
	llmPublisher := NewLLMPublisher(bus, "session-123", "agent-1")

	// Publish events from all publishers
	_ = guidePublisher.PublishUserPrompt("Hello")
	_ = toolPublisher.PublishToolCall("test_tool", nil)
	_ = agentPublisher.PublishAgentAction("test_action", "details")
	_ = llmPublisher.PublishLLMRequest("gpt-4", 100)

	// Wait for all events
	time.Sleep(300 * time.Millisecond)

	events := sub.getEvents()

	// Should have received at least 4 events (some may be debounced if same signature)
	if len(events) < 4 {
		t.Errorf("Expected at least 4 events, got %d", len(events))
	}

	// Verify all event types are present
	eventTypes := make(map[EventType]bool)
	for _, e := range events {
		eventTypes[e.EventType] = true
	}

	expectedTypes := []EventType{
		EventTypeUserPrompt,
		EventTypeToolCall,
		EventTypeAgentAction,
		EventTypeLLMRequest,
	}

	for _, et := range expectedTypes {
		if !eventTypes[et] {
			t.Errorf("Expected event type %s not found", et)
		}
	}
}

func TestIntegration_EventsDeliveredToSubscribers(t *testing.T) {
	t.Parallel()

	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create specific-type subscriber
	toolSub := newTestSubscriber("tool-sub", EventTypeToolCall, EventTypeToolResult)
	bus.Subscribe(toolSub)

	// Create wildcard subscriber
	wildcardSub := newTestSubscriber("wildcard-sub")
	bus.Subscribe(wildcardSub)

	publisher := NewToolPublisher(bus, "session-123", "agent-1")

	_ = publisher.PublishToolCall("test_tool", nil)
	_ = publisher.PublishToolResult("test_tool", "success", true)

	// Wait for delivery
	time.Sleep(300 * time.Millisecond)

	toolEvents := toolSub.getEvents()
	wildcardEvents := wildcardSub.getEvents()

	// Tool subscriber should receive tool events
	if len(toolEvents) < 2 {
		t.Errorf("Tool subscriber expected at least 2 events, got %d", len(toolEvents))
	}

	// Wildcard subscriber should also receive events
	if len(wildcardEvents) < 2 {
		t.Errorf("Wildcard subscriber expected at least 2 events, got %d", len(wildcardEvents))
	}
}

func TestIntegration_EventsHaveUniqueIDs(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewGuidePublisher(bus, "session-123")

	// Publish multiple events
	for i := 0; i < 5; i++ {
		_ = publisher.PublishUserPrompt("prompt")
		// Small delay to avoid debouncing
		time.Sleep(150 * time.Millisecond)
	}

	// Wait for delivery
	time.Sleep(200 * time.Millisecond)

	events := sub.getEvents()

	// Check that all IDs are unique
	ids := make(map[string]bool)
	for _, e := range events {
		if e.ID == "" {
			t.Error("Event has empty ID")
			continue
		}
		if ids[e.ID] {
			t.Errorf("Duplicate event ID found: %s", e.ID)
		}
		ids[e.ID] = true
	}
}

func TestIntegration_TimestampsAreSet(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	before := time.Now()

	publisher := NewAgentPublisher(bus, "session-123", "agent-1")
	_ = publisher.PublishAgentAction("test", "test")

	// Wait for delivery
	time.Sleep(200 * time.Millisecond)

	after := time.Now()

	events := sub.getEvents()
	if len(events) == 0 {
		t.Fatal("Expected at least one event")
	}

	event := events[0]

	// Timestamp should be between before and after
	if event.Timestamp.Before(before) {
		t.Errorf("Timestamp %v is before test start %v", event.Timestamp, before)
	}

	if event.Timestamp.After(after) {
		t.Errorf("Timestamp %v is after test end %v", event.Timestamp, after)
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestEdgeCase_EmptyStringParameters(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	guidePublisher := NewGuidePublisher(bus, "session-123")
	toolPublisher := NewToolPublisher(bus, "session-123", "agent-1")
	agentPublisher := NewAgentPublisher(bus, "session-123", "agent-1")
	llmPublisher := NewLLMPublisher(bus, "session-123", "agent-1")

	// Test with empty strings - should not panic
	_ = guidePublisher.PublishUserPrompt("")
	_ = guidePublisher.PublishRoutingDecision("", "", "")
	_ = guidePublisher.PublishClarificationRequest("")
	_ = toolPublisher.PublishToolCall("", nil)
	_ = toolPublisher.PublishToolResult("", nil, true)
	_ = toolPublisher.PublishToolTimeout("", 0)
	_ = agentPublisher.PublishAgentAction("", "")
	_ = agentPublisher.PublishAgentDecision("", "")
	_ = agentPublisher.PublishAgentError(nil, "")
	_ = agentPublisher.PublishSuccess("")
	_ = agentPublisher.PublishFailure(nil, "")
	_ = llmPublisher.PublishLLMRequest("", 0)
	_ = llmPublisher.PublishLLMResponse("", 0, 0, 0)
	_ = llmPublisher.PublishLLMError("", nil, "")

	// Wait for delivery
	time.Sleep(300 * time.Millisecond)

	// All events should have been published (though may be debounced)
	events := sub.getEvents()
	if len(events) == 0 {
		t.Error("Expected at least some events to be published")
	}
}

func TestEdgeCase_ZeroTokenCounts(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewLLMPublisher(bus, "session-123", "agent-1")

	err := publisher.PublishLLMRequest("gpt-4", 0)
	if err != nil {
		t.Fatalf("PublishLLMRequest with zero tokens returned error: %v", err)
	}

	err = publisher.PublishLLMResponse("gpt-4", 0, 0, time.Second)
	if err != nil {
		t.Fatalf("PublishLLMResponse with zero tokens returned error: %v", err)
	}

	// Wait for delivery
	time.Sleep(300 * time.Millisecond)

	events := sub.getEvents()
	if len(events) < 2 {
		t.Errorf("Expected at least 2 events, got %d", len(events))
	}

	// Verify zero values are captured
	for _, e := range events {
		if e.EventType == EventTypeLLMRequest {
			if e.Data["input_tokens"] != 0 {
				t.Errorf("Expected input_tokens 0, got %v", e.Data["input_tokens"])
			}
		}
		if e.EventType == EventTypeLLMResponse {
			if e.Data["input_tokens"] != 0 {
				t.Errorf("Expected input_tokens 0, got %v", e.Data["input_tokens"])
			}
			if e.Data["output_tokens"] != 0 {
				t.Errorf("Expected output_tokens 0, got %v", e.Data["output_tokens"])
			}
		}
	}
}

func TestEdgeCase_NilParams(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewToolPublisher(bus, "session-123", "agent-1")

	// nil params should be handled gracefully
	err := publisher.PublishToolCall("test_tool", nil)
	if err != nil {
		t.Fatalf("PublishToolCall with nil params returned error: %v", err)
	}

	// Wait for delivery
	time.Sleep(200 * time.Millisecond)

	events := sub.getEvents()
	if len(events) == 0 {
		t.Fatal("Expected at least one event")
	}

	// params should be nil or handled gracefully in the event data
	params := events[0].Data["params"]
	if params != nil {
		// If not nil, it should be an empty or nil map
		if paramMap, ok := params.(map[string]any); ok && len(paramMap) > 0 {
			t.Errorf("Expected nil or empty params in event data, got %v", events[0].Data["params"])
		}
	}
}

func TestEdgeCase_ConcurrentPublishing(t *testing.T) {
	t.Parallel()

	bus, sub := setupTestBus()
	defer bus.Close()

	publisher := NewAgentPublisher(bus, "session-123", "agent-1")

	// Concurrent publishing from multiple goroutines
	var wg sync.WaitGroup
	goroutines := 10
	eventsPerGoroutine := 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				_ = publisher.PublishAgentAction("action", "details")
			}
		}(i)
	}

	wg.Wait()

	// Wait for delivery
	time.Sleep(500 * time.Millisecond)

	// Should receive at least some events (may be debounced)
	events := sub.getEvents()
	if len(events) == 0 {
		t.Error("Expected to receive at least some events")
	}
}

// =============================================================================
// Table-Driven Tests
// =============================================================================

func TestGuidePublisher_EventTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		publishFunc   func(*GuidePublisher) error
		expectedType  EventType
		expectedCat   string
	}{
		{
			name: "UserPrompt",
			publishFunc: func(p *GuidePublisher) error {
				return p.PublishUserPrompt("test prompt")
			},
			expectedType: EventTypeUserPrompt,
			expectedCat:  "user",
		},
		{
			name: "RoutingDecision",
			publishFunc: func(p *GuidePublisher) error {
				return p.PublishRoutingDecision("from", "to", "reason")
			},
			expectedType: EventTypeAgentDecision,
			expectedCat:  "routing",
		},
		{
			name: "ClarificationRequest",
			publishFunc: func(p *GuidePublisher) error {
				return p.PublishClarificationRequest("question?")
			},
			expectedType: EventTypeUserClarification,
			expectedCat:  "clarification",
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bus, sub := setupTestBus()
			defer bus.Close()

			publisher := NewGuidePublisher(bus, "session-123")

			err := tt.publishFunc(publisher)
			if err != nil {
				t.Fatalf("Publish function returned error: %v", err)
			}

			event := sub.waitForEvent(200 * time.Millisecond)
			if event == nil {
				t.Fatal("Expected to receive event")
			}

			if event.EventType != tt.expectedType {
				t.Errorf("Expected event type %s, got %s", tt.expectedType, event.EventType)
			}

			if event.Category != tt.expectedCat {
				t.Errorf("Expected category %s, got %s", tt.expectedCat, event.Category)
			}
		})
	}
}

func TestToolPublisher_EventTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		publishFunc     func(*ToolPublisher) error
		expectedType    EventType
		expectedOutcome EventOutcome
	}{
		{
			name: "ToolCall",
			publishFunc: func(p *ToolPublisher) error {
				return p.PublishToolCall("tool", nil)
			},
			expectedType:    EventTypeToolCall,
			expectedOutcome: OutcomePending,
		},
		{
			name: "ToolResult_Success",
			publishFunc: func(p *ToolPublisher) error {
				return p.PublishToolResult("tool", "result", true)
			},
			expectedType:    EventTypeToolResult,
			expectedOutcome: OutcomeSuccess,
		},
		{
			name: "ToolResult_Failure",
			publishFunc: func(p *ToolPublisher) error {
				return p.PublishToolResult("tool", "error", false)
			},
			expectedType:    EventTypeToolResult,
			expectedOutcome: OutcomeFailure,
		},
		{
			name: "ToolTimeout",
			publishFunc: func(p *ToolPublisher) error {
				return p.PublishToolTimeout("tool", time.Second)
			},
			expectedType:    EventTypeToolTimeout,
			expectedOutcome: OutcomeFailure,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bus, sub := setupTestBus()
			defer bus.Close()

			publisher := NewToolPublisher(bus, "session-123", "agent-1")

			err := tt.publishFunc(publisher)
			if err != nil {
				t.Fatalf("Publish function returned error: %v", err)
			}

			event := sub.waitForEvent(200 * time.Millisecond)
			if event == nil {
				t.Fatal("Expected to receive event")
			}

			if event.EventType != tt.expectedType {
				t.Errorf("Expected event type %s, got %s", tt.expectedType, event.EventType)
			}

			if event.Outcome != tt.expectedOutcome {
				t.Errorf("Expected outcome %s, got %s", tt.expectedOutcome, event.Outcome)
			}
		})
	}
}

func TestAgentPublisher_EventTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		publishFunc     func(*AgentPublisher) error
		expectedType    EventType
		expectedOutcome EventOutcome
	}{
		{
			name: "AgentAction",
			publishFunc: func(p *AgentPublisher) error {
				return p.PublishAgentAction("action", "details")
			},
			expectedType:    EventTypeAgentAction,
			expectedOutcome: OutcomePending,
		},
		{
			name: "AgentDecision",
			publishFunc: func(p *AgentPublisher) error {
				return p.PublishAgentDecision("decision", "rationale")
			},
			expectedType:    EventTypeAgentDecision,
			expectedOutcome: OutcomePending,
		},
		{
			name: "AgentError",
			publishFunc: func(p *AgentPublisher) error {
				return p.PublishAgentError(errors.New("test"), "context")
			},
			expectedType:    EventTypeAgentError,
			expectedOutcome: OutcomeFailure,
		},
		{
			name: "Success",
			publishFunc: func(p *AgentPublisher) error {
				return p.PublishSuccess("summary")
			},
			expectedType:    EventTypeSuccess,
			expectedOutcome: OutcomeSuccess,
		},
		{
			name: "Failure",
			publishFunc: func(p *AgentPublisher) error {
				return p.PublishFailure(errors.New("error"), "summary")
			},
			expectedType:    EventTypeFailure,
			expectedOutcome: OutcomeFailure,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bus, sub := setupTestBus()
			defer bus.Close()

			publisher := NewAgentPublisher(bus, "session-123", "agent-1")

			err := tt.publishFunc(publisher)
			if err != nil {
				t.Fatalf("Publish function returned error: %v", err)
			}

			event := sub.waitForEvent(200 * time.Millisecond)
			if event == nil {
				t.Fatal("Expected to receive event")
			}

			if event.EventType != tt.expectedType {
				t.Errorf("Expected event type %s, got %s", tt.expectedType, event.EventType)
			}

			if event.Outcome != tt.expectedOutcome {
				t.Errorf("Expected outcome %s, got %s", tt.expectedOutcome, event.Outcome)
			}
		})
	}
}

func TestLLMPublisher_EventTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		publishFunc     func(*LLMPublisher) error
		expectedType    EventType
		expectedOutcome EventOutcome
	}{
		{
			name: "LLMRequest",
			publishFunc: func(p *LLMPublisher) error {
				return p.PublishLLMRequest("gpt-4", 100)
			},
			expectedType:    EventTypeLLMRequest,
			expectedOutcome: OutcomePending,
		},
		{
			name: "LLMResponse",
			publishFunc: func(p *LLMPublisher) error {
				return p.PublishLLMResponse("gpt-4", 100, 50, time.Second)
			},
			expectedType:    EventTypeLLMResponse,
			expectedOutcome: OutcomeSuccess,
		},
		{
			name: "LLMError",
			publishFunc: func(p *LLMPublisher) error {
				return p.PublishLLMError("gpt-4", errors.New("error"), "context")
			},
			expectedType:    EventTypeLLMResponse,
			expectedOutcome: OutcomeFailure,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bus, sub := setupTestBus()
			defer bus.Close()

			publisher := NewLLMPublisher(bus, "session-123", "agent-1")

			err := tt.publishFunc(publisher)
			if err != nil {
				t.Fatalf("Publish function returned error: %v", err)
			}

			event := sub.waitForEvent(200 * time.Millisecond)
			if event == nil {
				t.Fatal("Expected to receive event")
			}

			if event.EventType != tt.expectedType {
				t.Errorf("Expected event type %s, got %s", tt.expectedType, event.EventType)
			}

			if event.Outcome != tt.expectedOutcome {
				t.Errorf("Expected outcome %s, got %s", tt.expectedOutcome, event.Outcome)
			}
		})
	}
}
