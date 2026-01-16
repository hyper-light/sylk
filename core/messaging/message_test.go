package messaging

import (
	"testing"
	"time"
)

// =============================================================================
// Message[T] Tests
// =============================================================================

func TestNew(t *testing.T) {
	type TestPayload struct {
		Value string
	}

	msg := New(TypeRequest, "test-agent", &TestPayload{Value: "hello"})

	if msg.ID == "" {
		t.Error("expected ID to be generated")
	}
	if msg.Source != "test-agent" {
		t.Errorf("expected source 'test-agent', got %s", msg.Source)
	}
	if msg.Type != TypeRequest {
		t.Errorf("expected type 'request', got %s", msg.Type)
	}
	if msg.Status != StatusQueued {
		t.Errorf("expected status 'queued', got %s", msg.Status)
	}
	if msg.Attempt != 1 {
		t.Errorf("expected attempt 1, got %d", msg.Attempt)
	}
	if msg.Priority != PriorityNormal {
		t.Errorf("expected priority normal, got %d", msg.Priority)
	}
	if msg.Payload.Value != "hello" {
		t.Errorf("expected payload value 'hello', got %s", msg.Payload.Value)
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

	msg := New(TypeRequest, "source", "payload").
		WithCorrelation("corr-123").
		WithParent("parent-456").
		WithTarget("target-agent").
		WithDeadline(deadline).
		WithTTL(30 * time.Second).
		WithPriority(PriorityHigh).
		WithMaxAttempts(5).
		WithMetadata("key1", "value1").
		WithMetadata("key2", 42)

	if msg.CorrelationID != "corr-123" {
		t.Errorf("expected correlation 'corr-123', got %s", msg.CorrelationID)
	}
	if msg.ParentID != "parent-456" {
		t.Errorf("expected parent 'parent-456', got %s", msg.ParentID)
	}
	if msg.Target != "target-agent" {
		t.Errorf("expected target 'target-agent', got %s", msg.Target)
	}
	if msg.Deadline == nil || !msg.Deadline.Equal(deadline) {
		t.Error("expected deadline to be set")
	}
	if msg.TTL != 30*time.Second {
		t.Errorf("expected TTL 30s, got %v", msg.TTL)
	}
	if msg.Priority != PriorityHigh {
		t.Errorf("expected priority high, got %d", msg.Priority)
	}
	if msg.MaxAttempts != 5 {
		t.Errorf("expected max attempts 5, got %d", msg.MaxAttempts)
	}
	if msg.Metadata["key1"] != "value1" {
		t.Errorf("expected metadata key1='value1', got %v", msg.Metadata["key1"])
	}
	if msg.Metadata["key2"] != 42 {
		t.Errorf("expected metadata key2=42, got %v", msg.Metadata["key2"])
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
	// Can retry - not terminal, not expired, under max attempts
	msg := New(TypeRequest, "source", "payload").
		WithMaxAttempts(3)
	if !msg.CanRetry() {
		t.Error("expected message to be retryable")
	}

	// Cannot retry - terminal status
	msg.MarkCompleted()
	if msg.CanRetry() {
		t.Error("expected completed message not to be retryable")
	}

	// Cannot retry - expired
	msg2 := New(TypeRequest, "source", "payload")
	msg2.Timestamp = time.Now().Add(-2 * time.Hour)
	msg2.TTL = 1 * time.Hour
	if msg2.CanRetry() {
		t.Error("expected expired message not to be retryable")
	}

	// Cannot retry - max attempts reached
	msg3 := New(TypeRequest, "source", "payload").
		WithMaxAttempts(3)
	msg3.Attempt = 3
	if msg3.CanRetry() {
		t.Error("expected message at max attempts not to be retryable")
	}
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
	// Valid message
	msg := New(TypeRequest, "source", "payload")
	if err := msg.Validate(); err != nil {
		t.Errorf("expected valid message, got error: %v", err)
	}

	// Missing ID
	msg2 := New(TypeRequest, "source", "payload")
	msg2.ID = ""
	if err := msg2.Validate(); err == nil {
		t.Error("expected error for missing ID")
	}

	// Missing Source
	msg3 := New(TypeRequest, "", "payload")
	if err := msg3.Validate(); err == nil {
		t.Error("expected error for missing source")
	}

	// Missing Type
	msg4 := New("", "source", "payload")
	if err := msg4.Validate(); err == nil {
		t.Error("expected error for missing type")
	}

	// Invalid Attempt
	msg5 := New(TypeRequest, "source", "payload")
	msg5.Attempt = 0
	if err := msg5.Validate(); err == nil {
		t.Error("expected error for attempt < 1")
	}

	// Deadline before timestamp
	msg6 := New(TypeRequest, "source", "payload")
	deadline := msg6.Timestamp.Add(-1 * time.Hour)
	msg6.Deadline = &deadline
	if err := msg6.Validate(); err == nil {
		t.Error("expected error for deadline before timestamp")
	}

	// Negative TTL
	msg7 := New(TypeRequest, "source", "payload")
	msg7.TTL = -1 * time.Second
	if err := msg7.Validate(); err == nil {
		t.Error("expected error for negative TTL")
	}
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

	// New ID
	if clone.ID == original.ID {
		t.Error("expected clone to have new ID")
	}

	// Preserved fields
	if clone.CorrelationID != original.CorrelationID {
		t.Error("expected correlation to be preserved")
	}
	if clone.Target != original.Target {
		t.Error("expected target to be preserved")
	}
	if clone.Priority != original.Priority {
		t.Error("expected priority to be preserved")
	}

	// Reset fields
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

	// Deep copy metadata
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
