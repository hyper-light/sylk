package messaging

import (
	"testing"
	"time"
)

func TestNewRequestMessage(t *testing.T) {
	req := &RouteRequest{
		Input:         "test input",
		SourceAgentID: "source-agent",
		TargetAgentID: "target-agent",
	}

	msg := NewRequestMessage("source-agent", req)

	if msg.Type != TypeRequest {
		t.Errorf("expected type 'request', got %s", msg.Type)
	}
	if msg.Source != "source-agent" {
		t.Errorf("expected source 'source-agent', got %s", msg.Source)
	}
	if msg.Target != "target-agent" {
		t.Errorf("expected target 'target-agent', got %s", msg.Target)
	}
	if msg.Payload.Input != "test input" {
		t.Errorf("expected payload input 'test input', got %s", msg.Payload.Input)
	}
}

func TestNewRequestMessageWithCorrelation(t *testing.T) {
	req := &RouteRequest{Input: "test"}
	msg := NewRequestMessageWithCorrelation("source", "corr-123", req)

	if msg.CorrelationID != "corr-123" {
		t.Errorf("expected correlation 'corr-123', got %s", msg.CorrelationID)
	}
}

func TestNewForwardMessage(t *testing.T) {
	fwd := &ForwardedRequest{
		Input:         "test input",
		Intent:        "recall",
		Domain:        "patterns",
		SourceAgentID: "guide",
		Confidence:    0.95,
	}

	msg := NewForwardMessage("guide", "archivalist", fwd)

	if msg.Type != TypeForward {
		t.Errorf("expected type 'forward', got %s", msg.Type)
	}
	if msg.Source != "guide" {
		t.Errorf("expected source 'guide', got %s", msg.Source)
	}
	if msg.Target != "archivalist" {
		t.Errorf("expected target 'archivalist', got %s", msg.Target)
	}
	if msg.Payload.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", msg.Payload.Confidence)
	}
}

func TestNewResponseMessage(t *testing.T) {
	resp := &RouteResponse{
		Success:           true,
		Data:              map[string]string{"result": "ok"},
		RespondingAgentID: "archivalist",
		ProcessingTime:    100 * time.Millisecond,
	}

	msg := NewResponseMessage("archivalist", "corr-123", resp)

	if msg.Type != TypeResponse {
		t.Errorf("expected type 'response', got %s", msg.Type)
	}
	if msg.CorrelationID != "corr-123" {
		t.Errorf("expected correlation 'corr-123', got %s", msg.CorrelationID)
	}
	if !msg.Payload.Success {
		t.Error("expected success=true")
	}
}

func TestNewSuccessResponse(t *testing.T) {
	msg := NewSuccessResponse("agent", "corr-123", "result data", 50*time.Millisecond)

	if !msg.Payload.Success {
		t.Error("expected success=true")
	}
	if msg.Payload.Data != "result data" {
		t.Errorf("expected data 'result data', got %v", msg.Payload.Data)
	}
	if msg.Payload.ProcessingTime != 50*time.Millisecond {
		t.Errorf("expected processing time 50ms, got %v", msg.Payload.ProcessingTime)
	}
}

func TestNewErrorResponse(t *testing.T) {
	msg := NewErrorResponse("agent", "corr-123", "something failed")

	if msg.Payload.Success {
		t.Error("expected success=false")
	}
	if msg.Payload.Error != "something failed" {
		t.Errorf("expected error 'something failed', got %s", msg.Payload.Error)
	}
}

func TestNewActionMessage(t *testing.T) {
	action := &ActionRequest{
		SourceAgentID: "guide",
		TargetAgentID: "archivalist",
		Action:        "store",
		Data:          map[string]string{"content": "test"},
	}

	msg := NewActionMessage("guide", "archivalist", action)

	if msg.Type != TypeAction {
		t.Errorf("expected type 'action', got %s", msg.Type)
	}
	if msg.Target != "archivalist" {
		t.Errorf("expected target 'archivalist', got %s", msg.Target)
	}
}

func TestNewAckMessage(t *testing.T) {
	msg := NewAckMessage("archivalist", "corr-123")

	if msg.Type != TypeAck {
		t.Errorf("expected type 'ack', got %s", msg.Type)
	}
	if msg.CorrelationID != "corr-123" {
		t.Errorf("expected correlation 'corr-123', got %s", msg.CorrelationID)
	}
	if !msg.Payload.Received {
		t.Error("expected received=true")
	}
	if msg.Payload.Timestamp == 0 {
		t.Error("expected timestamp to be set")
	}
}

func TestNewNackMessage(t *testing.T) {
	msg := NewNackMessage("archivalist", "corr-123", "rejected because reasons")

	if msg.Payload.Received {
		t.Error("expected received=false")
	}
	if msg.Payload.Message != "rejected because reasons" {
		t.Errorf("expected rejection reason, got %s", msg.Payload.Message)
	}
}

func TestNewErrorMessage(t *testing.T) {
	msg := NewErrorMessage("agent", "corr-123", &ErrorPayload{
		Code:    "E001",
		Message: "something broke",
		Details: map[string]string{"field": "value"},
	})

	if msg.Type != TypeError {
		t.Errorf("expected type 'error', got %s", msg.Type)
	}
	if msg.Payload.Code != "E001" {
		t.Errorf("expected code 'E001', got %s", msg.Payload.Code)
	}
	if msg.Error != "something broke" {
		t.Errorf("expected envelope error to be set, got %s", msg.Error)
	}
}

func TestNewSimpleErrorMessage(t *testing.T) {
	msg := NewSimpleErrorMessage("agent", "corr-123", "simple error")

	if msg.Payload.Message != "simple error" {
		t.Errorf("expected message 'simple error', got %s", msg.Payload.Message)
	}
}

func TestNewHeartbeatMessage(t *testing.T) {
	msg := NewHeartbeatMessage("agent-id", "Agent Name", "healthy")

	if msg.Type != TypeHeartbeat {
		t.Errorf("expected type 'heartbeat', got %s", msg.Type)
	}
	if msg.Payload.AgentID != "agent-id" {
		t.Errorf("expected agent ID 'agent-id', got %s", msg.Payload.AgentID)
	}
	if msg.Payload.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %s", msg.Payload.Status)
	}
}

func TestMessageOptionPattern(t *testing.T) {
	deadline := time.Now().Add(1 * time.Hour)
	message := buildOptionMessage(deadline)
	assertOptionMessage(t, message, deadline)
}

func buildOptionMessage(deadline time.Time) *Message[string] {
	return NewMessage(
		TypeRequest,
		"source",
		"payload",
		WithCorrelationOpt[string]("corr-123"),
		WithParentOpt[string]("parent-456"),
		WithTargetOpt[string]("target"),
		WithPriorityOpt[string](PriorityHigh),
		WithDeadlineOpt[string](deadline),
		WithTTLOpt[string](30*time.Second),
		WithMaxAttemptsOpt[string](5),
		WithMetadataOpt[string]("key", "value"),
	)
}

func assertOptionMessage(t *testing.T, message *Message[string], deadline time.Time) {
	assertOptionIdentifiers(t, message)
	assertOptionTiming(t, message, deadline)
	assertOptionPriority(t, message)
	assertOptionMetadata(t, message)
}

func assertOptionIdentifiers(t *testing.T, message *Message[string]) {
	if message.CorrelationID != "corr-123" {
		t.Errorf("expected correlation 'corr-123', got %s", message.CorrelationID)
	}
	if message.ParentID != "parent-456" {
		t.Errorf("expected parent 'parent-456', got %s", message.ParentID)
	}
	if message.Target != "target" {
		t.Errorf("expected target 'target', got %s", message.Target)
	}
}

func assertOptionTiming(t *testing.T, message *Message[string], deadline time.Time) {
	if message.Deadline == nil || !message.Deadline.Equal(deadline) {
		t.Error("expected deadline to be set")
	}
	if message.TTL != 30*time.Second {
		t.Errorf("expected TTL 30s, got %v", message.TTL)
	}
}

func assertOptionPriority(t *testing.T, message *Message[string]) {
	if message.Priority != PriorityHigh {
		t.Errorf("expected priority high, got %d", message.Priority)
	}
	if message.MaxAttempts != 5 {
		t.Errorf("expected max attempts 5, got %d", message.MaxAttempts)
	}
}

func assertOptionMetadata(t *testing.T, message *Message[string]) {
	if message.Metadata["key"] != "value" {
		t.Errorf("expected metadata key='value', got %v", message.Metadata["key"])
	}
}

func TestReply(t *testing.T) {
	original := New(TypeRequest, "agent-a", "request payload")
	original.CorrelationID = "original-corr"

	reply := Reply(original, "agent-b", TypeResponse, "response payload")

	if reply.CorrelationID != "original-corr" {
		t.Errorf("expected correlation from original, got %s", reply.CorrelationID)
	}
	if reply.ParentID != original.ID {
		t.Errorf("expected parent ID to be original ID, got %s", reply.ParentID)
	}
	if reply.Target != "agent-a" {
		t.Errorf("expected target to be original source, got %s", reply.Target)
	}
	if reply.Payload != "response payload" {
		t.Errorf("expected payload 'response payload', got %s", reply.Payload)
	}
}

func TestReply_NoCorrelation(t *testing.T) {
	original := New(TypeRequest, "agent-a", "request payload")
	// No correlation ID set

	reply := Reply(original, "agent-b", TypeResponse, "response")

	// Should use original message ID as correlation
	if reply.CorrelationID != original.ID {
		t.Errorf("expected correlation to be original ID, got %s", reply.CorrelationID)
	}
}

func TestReplySuccess(t *testing.T) {
	original := New(TypeRequest, "requester", "request")
	original.CorrelationID = "corr-123"

	reply := ReplySuccess(original, "responder", "success data", 100*time.Millisecond)

	if reply.Type != TypeResponse {
		t.Errorf("expected type response, got %s", reply.Type)
	}
	if !reply.Payload.Success {
		t.Error("expected success=true")
	}
	if reply.Payload.Data != "success data" {
		t.Errorf("expected data 'success data', got %v", reply.Payload.Data)
	}
	if reply.Target != "requester" {
		t.Errorf("expected target 'requester', got %s", reply.Target)
	}
}

func TestReplyError(t *testing.T) {
	original := New(TypeRequest, "requester", "request")
	original.CorrelationID = "corr-123"

	reply := ReplyError(original, "responder", "something failed")

	if reply.Payload.Success {
		t.Error("expected success=false")
	}
	if reply.Payload.Error != "something failed" {
		t.Errorf("expected error message, got %s", reply.Payload.Error)
	}
}
