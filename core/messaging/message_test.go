package messaging

import (
	"testing"
	"time"
)

// =============================================================================
// Message[T] Tests
// =============================================================================

func TestNew(t *testing.T) {
	msg := New(TypeRequest, "test-agent", &testPayload{Value: "hello"})

	assertMessageID(t, msg)
	assertMessageSource(t, msg, "test-agent")
	assertMessageType(t, msg, TypeRequest)
	assertMessageStatus(t, msg, StatusQueued)
	assertMessageAttempt(t, msg, 1)
	assertMessagePriority(t, msg, PriorityNormal)
	assertMessagePayloadValue(t, msg, "hello")
}

type testPayload struct {
	Value string
}

func assertMessageID(t *testing.T, message *Message[*testPayload]) {
	if message.ID == "" {
		t.Error("expected ID to be generated")
	}
}

func assertMessageSource(t *testing.T, message *Message[*testPayload], expected string) {
	if message.Source != expected {
		t.Errorf("expected source '%s', got %s", expected, message.Source)
	}
}

func assertMessageType(t *testing.T, message *Message[*testPayload], expected MessageType) {
	if message.Type != expected {
		t.Errorf("expected type '%s', got %s", expected, message.Type)
	}
}

func assertMessageStatus(t *testing.T, message *Message[*testPayload], expected MessageStatus) {
	if message.Status != expected {
		t.Errorf("expected status '%s', got %s", expected, message.Status)
	}
}

func assertMessageAttempt(t *testing.T, message *Message[*testPayload], expected int) {
	if message.Attempt != expected {
		t.Errorf("expected attempt %d, got %d", expected, message.Attempt)
	}
}

func assertMessagePriority(t *testing.T, message *Message[*testPayload], expected Priority) {
	if message.Priority != expected {
		t.Errorf("expected priority %d, got %d", expected, message.Priority)
	}
}

func assertMessagePayloadValue(t *testing.T, message *Message[*testPayload], expected string) {
	if message.Payload.Value != expected {
		t.Errorf("expected payload value '%s', got %s", expected, message.Payload.Value)
	}
}

func TestNewWithID(t *testing.T) {
	msg := NewWithID("custom-id", TypeResponse, "agent", "payload")

	if msg.ID != "custom-id" {
		t.Errorf("expected ID 'custom-id', got %s", msg.ID)
	}
}

func TestMessage_BuilderPattern(t *testing.T) {
	deadline := time.Now().Add(1 * time.Hour)
	message := buildMessageWithMetadata(deadline)
	assertBuilderMessage(t, message, deadline)
}

func buildMessageWithMetadata(deadline time.Time) *Message[string] {
	return New(TypeRequest, "source", "payload").
		WithCorrelation("corr-123").
		WithParent("parent-456").
		WithTarget("target-agent").
		WithDeadline(deadline).
		WithTTL(30*time.Second).
		WithPriority(PriorityHigh).
		WithMaxAttempts(5).
		WithMetadata("key1", "value1").
		WithMetadata("key2", 42)
}

func assertBuilderMessage(t *testing.T, message *Message[string], deadline time.Time) {
	assertBuilderIdentifiers(t, message)
	assertBuilderTiming(t, message, deadline)
	assertBuilderPriority(t, message)
	assertBuilderMetadata(t, message)
}

func assertBuilderIdentifiers(t *testing.T, message *Message[string]) {
	if message.CorrelationID != "corr-123" {
		t.Errorf("expected correlation 'corr-123', got %s", message.CorrelationID)
	}
	if message.ParentID != "parent-456" {
		t.Errorf("expected parent 'parent-456', got %s", message.ParentID)
	}
	if message.Target != "target-agent" {
		t.Errorf("expected target 'target-agent', got %s", message.Target)
	}
}

func assertBuilderTiming(t *testing.T, message *Message[string], deadline time.Time) {
	if message.Deadline == nil || !message.Deadline.Equal(deadline) {
		t.Error("expected deadline to be set")
	}
	if message.TTL != 30*time.Second {
		t.Errorf("expected TTL 30s, got %v", message.TTL)
	}
}

func assertBuilderPriority(t *testing.T, message *Message[string]) {
	if message.Priority != PriorityHigh {
		t.Errorf("expected priority high, got %d", message.Priority)
	}
	if message.MaxAttempts != 5 {
		t.Errorf("expected max attempts 5, got %d", message.MaxAttempts)
	}
}

func assertBuilderMetadata(t *testing.T, message *Message[string]) {
	if message.Metadata["key1"] != "value1" {
		t.Errorf("expected metadata key1='value1', got %v", message.Metadata["key1"])
	}
	if message.Metadata["key2"] != 42 {
		t.Errorf("expected metadata key2=42, got %v", message.Metadata["key2"])
	}
}

func TestMessage_ExpiresAt_Deadline(t *testing.T) {
	deadline := time.Now().Add(1 * time.Hour)
	msg := New(TypeRequest, "source", "payload").
		WithDeadline(deadline).
		WithTTL(30 * time.Second) // TTL should be ignored when Deadline is set

	exp := msg.ExpiresAt()
	if exp == nil {
		t.Fatal("expected expiration time")
	}
	if !exp.Equal(deadline) {
		t.Errorf("expected deadline %v, got %v", deadline, *exp)
	}
}

func TestMessage_ExpiresAt_TTL(t *testing.T) {
	msg := New(TypeRequest, "source", "payload").
		WithTTL(1 * time.Hour)

	exp := msg.ExpiresAt()
	if exp == nil {
		t.Fatal("expected expiration time")
	}

	expected := msg.Timestamp.Add(1 * time.Hour)
	if !exp.Equal(expected) {
		t.Errorf("expected expiration %v, got %v", expected, *exp)
	}
}

func TestMessage_ExpiresAt_None(t *testing.T) {
	msg := New(TypeRequest, "source", "payload")

	exp := msg.ExpiresAt()
	if exp != nil {
		t.Error("expected nil expiration when no deadline or TTL")
	}
}

func TestMessage_IsExpired(t *testing.T) {
	// Not expired
	msg := New(TypeRequest, "source", "payload").
		WithTTL(1 * time.Hour)
	if msg.IsExpired() {
		t.Error("expected message not to be expired")
	}

	// Expired
	msg2 := New(TypeRequest, "source", "payload")
	msg2.Timestamp = time.Now().Add(-2 * time.Hour)
	msg2.TTL = 1 * time.Hour
	if !msg2.IsExpired() {
		t.Error("expected message to be expired")
	}
}

func TestMessage_RemainingTTL(t *testing.T) {
	msg := New(TypeRequest, "source", "payload").
		WithTTL(1 * time.Hour)

	remaining := msg.RemainingTTL()
	if remaining < 59*time.Minute || remaining > 1*time.Hour {
		t.Errorf("expected remaining TTL ~1h, got %v", remaining)
	}

	// Expired message
	msg2 := New(TypeRequest, "source", "payload")
	msg2.Timestamp = time.Now().Add(-2 * time.Hour)
	msg2.TTL = 1 * time.Hour
	if msg2.RemainingTTL() != 0 {
		t.Error("expected 0 remaining TTL for expired message")
	}

	// No expiration
	msg3 := New(TypeRequest, "source", "payload")
	if msg3.RemainingTTL() != 0 {
		t.Error("expected 0 remaining TTL when no expiration set")
	}
}

func TestMessage_StatusTransitions(t *testing.T) {
	msg := New(TypeRequest, "source", "payload")

	// Initial state
	if msg.Status != StatusQueued {
		t.Errorf("expected initial status 'queued', got %s", msg.Status)
	}

	// Mark processing
	msg.MarkProcessing()
	if msg.Status != StatusProcessing {
		t.Errorf("expected status 'processing', got %s", msg.Status)
	}

	// Mark completed
	msg.MarkCompleted()
	if msg.Status != StatusCompleted {
		t.Errorf("expected status 'completed', got %s", msg.Status)
	}
	if msg.ProcessedAt == nil {
		t.Error("expected ProcessedAt to be set")
	}
}

func TestMessage_MarkFailed(t *testing.T) {
	msg := New(TypeRequest, "source", "payload")
	msg.MarkFailed("something went wrong")

	if msg.Status != StatusFailed {
		t.Errorf("expected status 'failed', got %s", msg.Status)
	}
	if msg.Error != "something went wrong" {
		t.Errorf("expected error message, got %s", msg.Error)
	}
	if msg.ProcessedAt == nil {
		t.Error("expected ProcessedAt to be set")
	}
}

func TestMessage_MarkExpired(t *testing.T) {
	msg := New(TypeRequest, "source", "payload")
	msg.MarkExpired()

	if msg.Status != StatusExpired {
		t.Errorf("expected status 'expired', got %s", msg.Status)
	}
}

func TestMessage_MarkCancelled(t *testing.T) {
	msg := New(TypeRequest, "source", "payload")
	msg.MarkCancelled()

	if msg.Status != StatusCancelled {
		t.Errorf("expected status 'cancelled', got %s", msg.Status)
	}
}

func TestMessageStatus_IsTerminal(t *testing.T) {
	terminal := []MessageStatus{StatusCompleted, StatusFailed, StatusExpired, StatusCancelled}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}

	nonTerminal := []MessageStatus{StatusQueued, StatusProcessing}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("expected %s to NOT be terminal", s)
		}
	}
}

func TestMessage_CanRetry(t *testing.T) {
	assertCanRetry(t, newRetryableMessage())
	assertCannotRetryTerminal(t, completedMessage())
	assertCannotRetryExpired(t, expiredMessage())
	assertCannotRetryMaxAttempts(t, maxAttemptsMessage())
}

func newRetryableMessage() *Message[string] {
	return New(TypeRequest, "source", "payload").WithMaxAttempts(3)
}

func completedMessage() *Message[string] {
	msg := New(TypeRequest, "source", "payload")
	msg.MarkCompleted()
	return msg
}

func expiredMessage() *Message[string] {
	msg := New(TypeRequest, "source", "payload")
	msg.Timestamp = time.Now().Add(-2 * time.Hour)
	msg.TTL = 1 * time.Hour
	return msg
}

func maxAttemptsMessage() *Message[string] {
	msg := New(TypeRequest, "source", "payload").WithMaxAttempts(3)
	msg.Attempt = 3
	return msg
}

func assertCanRetry(t *testing.T, msg *Message[string]) {
	if !msg.CanRetry() {
		t.Error("expected message to be retryable")
	}
}

func assertCannotRetryTerminal(t *testing.T, msg *Message[string]) {
	if msg.CanRetry() {
		t.Error("expected completed message not to be retryable")
	}
}

func assertCannotRetryExpired(t *testing.T, msg *Message[string]) {
	if msg.CanRetry() {
		t.Error("expected expired message not to be retryable")
	}
}

func assertCannotRetryMaxAttempts(t *testing.T, msg *Message[string]) {
	if msg.CanRetry() {
		t.Error("expected message at max attempts not to be retryable")
	}
}

func assertValidMessage(t *testing.T, msg *Message[string]) {
	if err := msg.Validate(); err != nil {
		t.Errorf("expected valid message, got error: %v", err)
	}
}

func assertInvalidMessage(t *testing.T, msg *Message[string], context string) {
	if err := msg.Validate(); err == nil {
		t.Errorf("expected error for %s", context)
	}
}

func missingIDMessage() *Message[string] {
	msg := New(TypeRequest, "source", "payload")
	msg.ID = ""
	return msg
}

func missingSourceMessage() *Message[string] {
	return New(TypeRequest, "", "payload")
}

func missingTypeMessage() *Message[string] {
	return New("", "source", "payload")
}

func invalidAttemptMessage() *Message[string] {
	msg := New(TypeRequest, "source", "payload")
	msg.Attempt = 0
	return msg
}

func invalidDeadlineMessage() *Message[string] {
	msg := New(TypeRequest, "source", "payload")
	deadline := msg.Timestamp.Add(-1 * time.Hour)
	msg.Deadline = &deadline
	return msg
}

func negativeTTLMessage() *Message[string] {
	msg := New(TypeRequest, "source", "payload")
	msg.TTL = -1 * time.Second
	return msg
}

func TestMessage_IncrementAttempt(t *testing.T) {
	msg := New(TypeRequest, "source", "payload")
	msg.MarkFailed("error")

	msg.Status = StatusQueued // Reset for retry simulation
	msg.IncrementAttempt()

	if msg.Attempt != 2 {
		t.Errorf("expected attempt 2, got %d", msg.Attempt)
	}
	if msg.Status != StatusQueued {
		t.Errorf("expected status reset to queued, got %s", msg.Status)
	}
}

func TestMessage_Validate(t *testing.T) {
	assertValidMessage(t, New(TypeRequest, "source", "payload"))
	assertInvalidMessage(t, missingIDMessage(), "missing ID")
	assertInvalidMessage(t, missingSourceMessage(), "missing source")
	assertInvalidMessage(t, missingTypeMessage(), "missing type")
	assertInvalidMessage(t, invalidAttemptMessage(), "attempt < 1")
	assertInvalidMessage(t, invalidDeadlineMessage(), "deadline before timestamp")
	assertInvalidMessage(t, negativeTTLMessage(), "negative TTL")
}

func TestMessage_ValidateForRouting(t *testing.T) {
	// Valid request
	msg := New(TypeRequest, "source", "payload")
	if err := msg.ValidateForRouting(); err != nil {
		t.Errorf("expected valid message for routing, got: %v", err)
	}

	// Response without correlation
	msg2 := New(TypeResponse, "source", "payload")
	if err := msg2.ValidateForRouting(); err == nil {
		t.Error("expected error for response without correlation ID")
	}

	// Response with correlation - valid
	msg3 := New(TypeResponse, "source", "payload")
	msg3.CorrelationID = "corr-123"
	if err := msg3.ValidateForRouting(); err != nil {
		t.Errorf("expected valid response, got: %v", err)
	}

	// Expired message
	msg4 := New(TypeRequest, "source", "payload")
	msg4.Timestamp = time.Now().Add(-2 * time.Hour)
	msg4.TTL = 1 * time.Hour
	if err := msg4.ValidateForRouting(); err == nil {
		t.Error("expected error for expired message")
	}
}

func TestMessage_Clone(t *testing.T) {
	original := New(TypeRequest, "source", "payload").
		WithCorrelation("corr-123").
		WithTarget("target").
		WithPriority(PriorityHigh).
		WithMetadata("key", "value")
	original.MarkProcessing()

	clone := original.Clone()

	assertCloneIdentity(t, clone, original)
	assertClonePreservesFields(t, clone, original)
	assertCloneResetsFields(t, clone)
	assertCloneMetadata(t, clone, original)
}

func assertCloneIdentity(t *testing.T, clone *Message[string], original *Message[string]) {
	if clone.ID == original.ID {
		t.Error("expected clone to have new ID")
	}
}

func assertClonePreservesFields(t *testing.T, clone *Message[string], original *Message[string]) {
	if clone.CorrelationID != original.CorrelationID {
		t.Error("expected correlation to be preserved")
	}
	if clone.Target != original.Target {
		t.Error("expected target to be preserved")
	}
	if clone.Priority != original.Priority {
		t.Error("expected priority to be preserved")
	}
}

func assertCloneResetsFields(t *testing.T, clone *Message[string]) {
	if clone.Status != StatusQueued {
		t.Error("expected status reset to queued")
	}
	if clone.Attempt != 1 {
		t.Error("expected attempt reset to 1")
	}
	if clone.ProcessedAt != nil {
		t.Error("expected ProcessedAt reset to nil")
	}
	if clone.Error != "" {
		t.Error("expected Error reset to empty")
	}
}

func assertCloneMetadata(t *testing.T, clone *Message[string], original *Message[string]) {
	if clone.Metadata["key"] != "value" {
		t.Error("expected metadata to be copied")
	}
	clone.Metadata["key"] = "modified"
	if original.Metadata["key"] != "value" {
		t.Error("expected original metadata to be unchanged")
	}
}

func TestMessage_Age(t *testing.T) {
	msg := New(TypeRequest, "source", "payload")
	time.Sleep(10 * time.Millisecond)

	age := msg.Age()
	if age < 10*time.Millisecond {
		t.Errorf("expected age >= 10ms, got %v", age)
	}
}

func TestMessage_ProcessingDuration(t *testing.T) {
	msg := New(TypeRequest, "source", "payload")

	// Not processed yet
	if msg.ProcessingDuration() != 0 {
		t.Error("expected 0 duration for unprocessed message")
	}

	time.Sleep(10 * time.Millisecond)
	msg.MarkCompleted()

	duration := msg.ProcessingDuration()
	if duration < 10*time.Millisecond {
		t.Errorf("expected duration >= 10ms, got %v", duration)
	}
}

func TestPriority_String(t *testing.T) {
	tests := []struct {
		priority Priority
		expected string
	}{
		{PriorityCritical, "critical"},
		{PriorityHigh, "high"},
		{PriorityNormal, "normal"},
		{PriorityLow, "low"},
		{PriorityBackground, "background"},
		{Priority(42), "custom"},
	}

	for _, tc := range tests {
		if tc.priority.String() != tc.expected {
			t.Errorf("expected %s, got %s", tc.expected, tc.priority.String())
		}
	}
}
