package guide

import "testing"

func TestNormalizeChannelOwnedMessage_ResponseUsesChannelOwnerIdentity(t *testing.T) {
	g := &Guide{}
	msg := NewResponseMessage("", &RouteResponse{
		CorrelationID:     "corr-1",
		Success:           true,
		RespondingAgentID: "tester",
	})
	msg.SourceAgentID = "tester"

	normalized := g.normalizeChannelOwnedMessage("worker-123", msg)
	if normalized == nil {
		t.Fatal("expected normalized message")
	}
	if normalized.SourceAgentID != "worker-123" {
		t.Fatalf("SourceAgentID = %q, want worker-123", normalized.SourceAgentID)
	}
	resp, ok := normalized.GetRouteResponse()
	if !ok || resp == nil {
		t.Fatal("expected route response payload")
	}
	if resp.RespondingAgentID != "worker-123" {
		t.Fatalf("RespondingAgentID = %q, want worker-123", resp.RespondingAgentID)
	}
}

func TestNormalizeChannelOwnedMessage_StreamUsesChannelOwnerIdentity(t *testing.T) {
	g := &Guide{}
	msg := &Message{
		ID:            "stream-1",
		CorrelationID: "corr-1",
		Type:          MessageTypeStream,
		SourceAgentID: "tester-pipeline",
		Payload: &StreamResponse{
			CorrelationID:     "corr-1",
			RespondingAgentID: "tester-pipeline",
			Event: &StreamEvent{
				Type: StreamEventProgress,
			},
		},
	}

	normalized := g.normalizeChannelOwnedMessage("worker-456", msg)
	if normalized == nil {
		t.Fatal("expected normalized message")
	}
	if normalized.SourceAgentID != "worker-456" {
		t.Fatalf("SourceAgentID = %q, want worker-456", normalized.SourceAgentID)
	}
	stream, ok := normalized.GetStreamResponse()
	if !ok || stream == nil {
		t.Fatal("expected stream response payload")
	}
	if stream.RespondingAgentID != "worker-456" {
		t.Fatalf("RespondingAgentID = %q, want worker-456", stream.RespondingAgentID)
	}
}

func TestNormalizeRoutingInfo_RequiresMatchingRegistrationID(t *testing.T) {
	_, err := normalizeRoutingInfo(&AgentRoutingInfo{
		ID:   "worker-1",
		Type: "engineer",
		Name: "Engineer",
		Registration: &AgentRegistration{
			ID:   "worker-2",
			Name: "Engineer",
		},
	})
	if err == nil {
		t.Fatal("expected routing info mismatch to fail")
	}
}
