package bridge

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	uimsg "github.com/adalundhe/sylk/ui/msg"
)

type recordingProgram struct {
	messages []any
}

func (r *recordingProgram) Send(m any) {
	r.messages = append(r.messages, m)
}

func TestGuideBridgeDispatch_ForwardsErrorMessageAsStreamError(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	errMsg := guide.NewErrorMessage("", "corr-123", "guide", "route failed")
	b.dispatch(errMsg, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(program.messages))
	}
	streamErr, ok := program.messages[0].(uimsg.StreamErrorMsg)
	if !ok {
		t.Fatalf("expected StreamErrorMsg, got %T", program.messages[0])
	}
	if streamErr.SessionID != "session-1" {
		t.Fatalf("expected session id session-1, got %q", streamErr.SessionID)
	}
	if streamErr.CorrelationID != "corr-123" {
		t.Fatalf("expected correlation id corr-123, got %q", streamErr.CorrelationID)
	}
	if streamErr.Err == nil || streamErr.Err.Error() != "route failed" {
		t.Fatalf("expected error \"route failed\", got %v", streamErr.Err)
	}
}

func TestToGuideMsg_SerializesStructuredPayload(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-456",
		RespondingAgentID:   "architect",
		RespondingAgentName: "Architect",
		Success:             true,
		Data: map[string]any{
			"plan":  "ship oauth",
			"steps": 3,
		},
	}

	msg := toGuideMsg(resp)
	if msg.Content == "" {
		t.Fatal("expected non-empty content for structured payload")
	}
	if !strings.Contains(msg.Content, "\"plan\": \"ship oauth\"") {
		t.Fatalf("expected json payload, got %q", msg.Content)
	}
}
