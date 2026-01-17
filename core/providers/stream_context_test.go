package providers

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestNewStreamContext(t *testing.T) {
	tests := []struct {
		name         string
		sessionID    string
		providerName string
		model        string
		agentID      string
	}{
		{
			name:         "creates context with all fields",
			sessionID:    "session-123",
			providerName: "anthropic",
			model:        "claude-3-opus",
			agentID:      "agent-456",
		},
		{
			name:         "creates context with empty strings",
			sessionID:    "",
			providerName: "",
			model:        "",
			agentID:      "",
		},
		{
			name:         "creates context with special characters",
			sessionID:    "session/with:special@chars",
			providerName: "provider-name_test",
			model:        "model.v1.2.3",
			agentID:      "agent#1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			ctx := NewStreamContext(tt.sessionID, tt.providerName, tt.model, tt.agentID)
			after := time.Now()

			if ctx == nil {
				t.Fatal("expected non-nil StreamContext")
			}

			if ctx.SessionID != tt.sessionID {
				t.Errorf("SessionID = %q, want %q", ctx.SessionID, tt.sessionID)
			}
			if ctx.ProviderName != tt.providerName {
				t.Errorf("ProviderName = %q, want %q", ctx.ProviderName, tt.providerName)
			}
			if ctx.Model != tt.model {
				t.Errorf("Model = %q, want %q", ctx.Model, tt.model)
			}
			if ctx.AgentID != tt.agentID {
				t.Errorf("AgentID = %q, want %q", ctx.AgentID, tt.agentID)
			}

			// CorrelationID should be non-empty
			if ctx.CorrelationID == "" {
				t.Error("CorrelationID should not be empty")
			}

			// CorrelationID should be 32 hex characters (16 bytes)
			if len(ctx.CorrelationID) != 32 {
				t.Errorf("CorrelationID length = %d, want 32", len(ctx.CorrelationID))
			}

			// StartedAt should be between before and after
			if ctx.StartedAt.Before(before) || ctx.StartedAt.After(after) {
				t.Errorf("StartedAt = %v, want between %v and %v", ctx.StartedAt, before, after)
			}
		})
	}
}

func TestNewStreamContext_UniqueCorrelationIDs(t *testing.T) {
	ids := make(map[string]bool)
	count := 1000

	for i := 0; i < count; i++ {
		ctx := NewStreamContext("session", "provider", "model", "agent")
		if ids[ctx.CorrelationID] {
			t.Errorf("duplicate CorrelationID found: %s", ctx.CorrelationID)
		}
		ids[ctx.CorrelationID] = true
	}

	if len(ids) != count {
		t.Errorf("expected %d unique IDs, got %d", count, len(ids))
	}
}

func TestNewStreamContext_Concurrent(t *testing.T) {
	var wg sync.WaitGroup
	ids := make(chan string, 100)
	goroutines := 10
	iterations := 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				ctx := NewStreamContext("session", "provider", "model", "agent")
				ids <- ctx.CorrelationID
			}
		}()
	}

	go func() {
		wg.Wait()
		close(ids)
	}()

	seen := make(map[string]bool)
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate CorrelationID in concurrent test: %s", id)
		}
		seen[id] = true
	}

	expected := goroutines * iterations
	if len(seen) != expected {
		t.Errorf("expected %d unique IDs, got %d", expected, len(seen))
	}
}

func TestWrapStreamChunk(t *testing.T) {
	ctx := &StreamContext{
		CorrelationID: "corr-123",
		RequestID:     "req-456",
		SessionID:     "session-789",
		ProviderName:  "anthropic",
		TargetAgent:   "engineer",
		Model:         "claude-3-opus",
		AgentID:       "agent-001",
		Priority:      5,
		StartedAt:     time.Now(),
	}

	chunk := &StreamChunk{
		Index:     42,
		Text:      "Hello, world!",
		Type:      ChunkTypeText,
		Timestamp: time.Now(),
	}

	before := time.Now()
	msg := WrapStreamChunk(chunk, ctx)
	after := time.Now()

	if msg == nil {
		t.Fatal("expected non-nil StreamMessage")
	}

	// Verify ID is generated
	if msg.ID == "" {
		t.Error("ID should not be empty")
	}
	if len(msg.ID) != 32 {
		t.Errorf("ID length = %d, want 32", len(msg.ID))
	}

	// Verify Type
	if msg.Type != MessageTypeStream {
		t.Errorf("Type = %q, want %q", msg.Type, MessageTypeStream)
	}

	// Verify correlation
	if msg.CorrelationID != ctx.CorrelationID {
		t.Errorf("CorrelationID = %q, want %q", msg.CorrelationID, ctx.CorrelationID)
	}

	// Verify parent ID
	if msg.ParentID != ctx.RequestID {
		t.Errorf("ParentID = %q, want %q", msg.ParentID, ctx.RequestID)
	}

	// Verify payload
	if msg.Payload != chunk {
		t.Error("Payload should be the original chunk")
	}

	// Verify priority
	if msg.Priority != ctx.Priority {
		t.Errorf("Priority = %d, want %d", msg.Priority, ctx.Priority)
	}

	// Verify timestamp
	if msg.Timestamp.Before(before) || msg.Timestamp.After(after) {
		t.Errorf("Timestamp = %v, want between %v and %v", msg.Timestamp, before, after)
	}

	// Verify metadata
	if msg.Metadata.Model != ctx.Model {
		t.Errorf("Metadata.Model = %q, want %q", msg.Metadata.Model, ctx.Model)
	}
	if msg.Metadata.AgentID != ctx.AgentID {
		t.Errorf("Metadata.AgentID = %q, want %q", msg.Metadata.AgentID, ctx.AgentID)
	}
	if msg.Metadata.ChunkIndex != chunk.Index {
		t.Errorf("Metadata.ChunkIndex = %d, want %d", msg.Metadata.ChunkIndex, chunk.Index)
	}
	if msg.Metadata.ChunkType != chunk.Type {
		t.Errorf("Metadata.ChunkType = %q, want %q", msg.Metadata.ChunkType, chunk.Type)
	}
	if msg.Metadata.ProviderName != ctx.ProviderName {
		t.Errorf("Metadata.ProviderName = %q, want %q", msg.Metadata.ProviderName, ctx.ProviderName)
	}
	if msg.Metadata.SessionID != ctx.SessionID {
		t.Errorf("Metadata.SessionID = %q, want %q", msg.Metadata.SessionID, ctx.SessionID)
	}
}

func TestWrapStreamChunk_AllChunkTypes(t *testing.T) {
	ctx := NewStreamContext("session", "provider", "model", "agent")

	chunkTypes := []StreamChunkType{
		ChunkTypeText,
		ChunkTypeToolStart,
		ChunkTypeToolDelta,
		ChunkTypeToolEnd,
		ChunkTypeStart,
		ChunkTypeEnd,
		ChunkTypeError,
	}

	for _, ct := range chunkTypes {
		t.Run(string(ct), func(t *testing.T) {
			chunk := &StreamChunk{
				Index: 1,
				Type:  ct,
			}

			msg := WrapStreamChunk(chunk, ctx)

			if msg.Metadata.ChunkType != ct {
				t.Errorf("ChunkType = %q, want %q", msg.Metadata.ChunkType, ct)
			}
		})
	}
}

func TestNewStreamControlMessage(t *testing.T) {
	tests := []struct {
		name          string
		correlationID string
		action        string
		reason        string
	}{
		{
			name:          "cancel action",
			correlationID: "corr-123",
			action:        "cancel",
			reason:        "user requested cancellation",
		},
		{
			name:          "pause action",
			correlationID: "corr-456",
			action:        "pause",
			reason:        "rate limit reached",
		},
		{
			name:          "resume action",
			correlationID: "corr-789",
			action:        "resume",
			reason:        "rate limit cleared",
		},
		{
			name:          "empty reason",
			correlationID: "corr-000",
			action:        "cancel",
			reason:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			msg := NewStreamControlMessage(tt.correlationID, tt.action, tt.reason)
			after := time.Now()

			if msg == nil {
				t.Fatal("expected non-nil StreamControlMessage")
			}

			// Verify ID is generated
			if msg.ID == "" {
				t.Error("ID should not be empty")
			}
			if len(msg.ID) != 32 {
				t.Errorf("ID length = %d, want 32", len(msg.ID))
			}

			// Verify Type
			if msg.Type != MessageTypeStreamControl {
				t.Errorf("Type = %q, want %q", msg.Type, MessageTypeStreamControl)
			}

			// Verify fields
			if msg.CorrelationID != tt.correlationID {
				t.Errorf("CorrelationID = %q, want %q", msg.CorrelationID, tt.correlationID)
			}
			if msg.Action != tt.action {
				t.Errorf("Action = %q, want %q", msg.Action, tt.action)
			}
			if msg.Reason != tt.reason {
				t.Errorf("Reason = %q, want %q", msg.Reason, tt.reason)
			}

			// Verify timestamp
			if msg.Timestamp.Before(before) || msg.Timestamp.After(after) {
				t.Errorf("Timestamp = %v, want between %v and %v", msg.Timestamp, before, after)
			}
		})
	}
}

func TestStreamContext_JSONMarshal(t *testing.T) {
	ctx := &StreamContext{
		CorrelationID: "corr-123",
		RequestID:     "req-456",
		SessionID:     "session-789",
		ProviderName:  "anthropic",
		TargetAgent:   "engineer",
		Model:         "claude-3-opus",
		AgentID:       "agent-001",
		Priority:      5,
		StartedAt:     time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded StreamContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.CorrelationID != ctx.CorrelationID {
		t.Errorf("CorrelationID mismatch after roundtrip")
	}
	if decoded.RequestID != ctx.RequestID {
		t.Errorf("RequestID mismatch after roundtrip")
	}
	if decoded.SessionID != ctx.SessionID {
		t.Errorf("SessionID mismatch after roundtrip")
	}
	if decoded.ProviderName != ctx.ProviderName {
		t.Errorf("ProviderName mismatch after roundtrip")
	}
	if decoded.TargetAgent != ctx.TargetAgent {
		t.Errorf("TargetAgent mismatch after roundtrip")
	}
	if decoded.Model != ctx.Model {
		t.Errorf("Model mismatch after roundtrip")
	}
	if decoded.AgentID != ctx.AgentID {
		t.Errorf("AgentID mismatch after roundtrip")
	}
	if decoded.Priority != ctx.Priority {
		t.Errorf("Priority mismatch after roundtrip")
	}
}

func TestStreamMessage_JSONMarshal(t *testing.T) {
	chunk := &StreamChunk{
		Index: 10,
		Text:  "test content",
		Type:  ChunkTypeText,
	}

	msg := &StreamMessage{
		ID:            "msg-123",
		Type:          MessageTypeStream,
		CorrelationID: "corr-456",
		ParentID:      "parent-789",
		Payload:       chunk,
		Metadata: StreamMessageMetadata{
			Model:        "claude-3-opus",
			AgentID:      "agent-001",
			ChunkIndex:   10,
			ChunkType:    ChunkTypeText,
			ProviderName: "anthropic",
			SessionID:    "session-123",
		},
		Priority:  3,
		Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded StreamMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != msg.ID {
		t.Errorf("ID mismatch after roundtrip")
	}
	if decoded.Type != msg.Type {
		t.Errorf("Type mismatch after roundtrip")
	}
	if decoded.CorrelationID != msg.CorrelationID {
		t.Errorf("CorrelationID mismatch after roundtrip")
	}
	if decoded.Metadata.ChunkType != msg.Metadata.ChunkType {
		t.Errorf("Metadata.ChunkType mismatch after roundtrip")
	}
}

func TestStreamControlMessage_JSONMarshal(t *testing.T) {
	msg := &StreamControlMessage{
		ID:            "ctrl-123",
		Type:          MessageTypeStreamControl,
		CorrelationID: "corr-456",
		Action:        "cancel",
		Reason:        "test reason",
		Timestamp:     time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded StreamControlMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != msg.ID {
		t.Errorf("ID mismatch after roundtrip")
	}
	if decoded.Type != msg.Type {
		t.Errorf("Type mismatch after roundtrip")
	}
	if decoded.Action != msg.Action {
		t.Errorf("Action mismatch after roundtrip")
	}
	if decoded.Reason != msg.Reason {
		t.Errorf("Reason mismatch after roundtrip")
	}
}

func TestMessageTypeConstants(t *testing.T) {
	if MessageTypeStream != "stream" {
		t.Errorf("MessageTypeStream = %q, want %q", MessageTypeStream, "stream")
	}
	if MessageTypeStreamControl != "stream_control" {
		t.Errorf("MessageTypeStreamControl = %q, want %q", MessageTypeStreamControl, "stream_control")
	}
}

func TestGenerateID_Length(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := generateID()
		if len(id) != 32 {
			t.Errorf("generateID() length = %d, want 32", len(id))
		}
	}
}

func TestGenerateID_HexChars(t *testing.T) {
	hexChars := "0123456789abcdef"
	for i := 0; i < 100; i++ {
		id := generateID()
		for _, c := range id {
			found := false
			for _, h := range hexChars {
				if c == h {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("generateID() contains non-hex character: %c", c)
			}
		}
	}
}

func TestWrapStreamChunk_NilChunkFields(t *testing.T) {
	ctx := NewStreamContext("session", "provider", "model", "agent")

	// Chunk with nil ToolCall and Usage
	chunk := &StreamChunk{
		Index:    0,
		Text:     "",
		Type:     ChunkTypeStart,
		ToolCall: nil,
		Usage:    nil,
	}

	msg := WrapStreamChunk(chunk, ctx)

	if msg == nil {
		t.Fatal("expected non-nil StreamMessage")
	}

	if msg.Payload.ToolCall != nil {
		t.Error("expected nil ToolCall in payload")
	}
	if msg.Payload.Usage != nil {
		t.Error("expected nil Usage in payload")
	}
}

func TestStreamContext_ZeroValues(t *testing.T) {
	ctx := &StreamContext{}

	// Zero values should be valid
	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("failed to marshal zero-value context: %v", err)
	}

	var decoded StreamContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal zero-value context: %v", err)
	}

	if decoded.Priority != 0 {
		t.Errorf("Priority = %d, want 0", decoded.Priority)
	}
}
