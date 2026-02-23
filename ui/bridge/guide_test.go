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

func TestToGuideMsg_HumanizesStructuredPayload(t *testing.T) {
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
	if !strings.Contains(msg.Content, "plan: ship oauth") {
		t.Fatalf("expected humanized payload, got %q", msg.Content)
	}
}

func TestToGuideMsg_HumanizesArchitectPlanEnvelope(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-plan",
		RespondingAgentID:   "architect",
		RespondingAgentName: "Architect",
		Success:             true,
		Data: map[string]any{
			"ID":      "response-1",
			"Success": true,
			"Data": map[string]any{
				"Status": 6,
				"Requirements": map[string]any{
					"Scope": "oauth",
				},
				"Tasks": []any{
					map[string]any{"Name": "Define auth interfaces", "AgentType": "engineer"},
					map[string]any{"Name": "Implement token refresh", "AgentType": "engineer"},
				},
				"Workflow": map[string]any{
					"CompletedTasks":  0,
					"FailedTasks":     0,
					"EstimatedTokens": 5000,
				},
				"Consultations": map[string]any{
					"librarian":   map[string]any{"Success": true},
					"archivalist": map[string]any{"Success": true},
				},
			},
		},
	}

	msg := toGuideMsg(resp)
	if !strings.Contains(msg.Content, "I drafted a concrete plan for oauth.") {
		t.Fatalf("expected architect summary, got %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "Define auth interfaces") {
		t.Fatalf("expected first task mention, got %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "Should I refine this further, or package it for execution?") {
		t.Fatalf("expected conversational follow-up, got %q", msg.Content)
	}
}

func TestToGuideMsg_HumanizesArchitectClarificationEnvelope(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-clarify",
		RespondingAgentID:   "architect",
		RespondingAgentName: "Architect",
		Success:             true,
		Data: map[string]any{
			"ID":      "response-2",
			"Success": true,
			"Data": map[string]any{
				"ClarificationNeeded": true,
				"UserResponse":        "I recommend starting with Google and Entra for phase 1 enterprise SSO.",
				"ClarificationQuestions": []any{
					"Which OAuth providers are in scope for phase 1?",
					"Which client surfaces are in scope (web/mobile/CLI)?",
				},
			},
		},
	}

	msg := toGuideMsg(resp)
	if msg.Content != "I recommend starting with Google and Entra for phase 1 enterprise SSO." {
		t.Fatalf("expected explicit user response, got %q", msg.Content)
	}
}

func TestToGuideMsg_HumanizesAgentRegistryPayload(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-789",
		RespondingAgentID:   "guide",
		RespondingAgentName: "Guide",
		Success:             true,
		Data: []guide.AgentRegistration{
			{ID: "architect"},
			{ID: "guide"},
		},
	}

	msg := toGuideMsg(resp)
	if msg.Content != "Registered agents (2): architect, guide" {
		t.Fatalf("content = %q", msg.Content)
	}
}

func TestToGuideMsg_HumanizesPendingPayload(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-999",
		RespondingAgentID:   "guide",
		RespondingAgentName: "Guide",
		Success:             true,
		Data: map[string]any{
			"pending": 3,
		},
	}

	msg := toGuideMsg(resp)
	if msg.Content != "Guide pending requests: 3." {
		t.Fatalf("content = %q", msg.Content)
	}
}

func TestGuideBridgeDispatchStream_CompleteEmitsChunkFromStructuredPayload(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-stream",
		RespondingAgentID: "architect",
		Event: &guide.StreamEvent{
			Type: guide.StreamEventComplete,
			Data: map[string]any{
				"ID":      "response-1",
				"Success": true,
				"Data": map[string]any{
					"Status": 6,
					"Requirements": map[string]any{
						"Scope": "oauth",
					},
					"Tasks": []any{
						map[string]any{"Name": "Define auth interfaces", "AgentType": "engineer"},
					},
					"Workflow": map[string]any{
						"CompletedTasks": 0,
						"FailedTasks":    0,
					},
				},
			},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 message (complete with authoritative text), got %d", len(program.messages))
	}
	complete, ok := program.messages[0].(uimsg.StreamCompleteMsg)
	if !ok {
		t.Fatalf("expected StreamCompleteMsg, got %T", program.messages[0])
	}
	if !strings.Contains(complete.AuthoritativeText, "I drafted a concrete plan for oauth.") {
		t.Fatalf("expected authoritative text with plan summary, got %q", complete.AuthoritativeText)
	}
}

func TestGuideBridgeDispatchStream_DataUsesFallbackPayloadText(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-stream",
		RespondingAgentID: "guide",
		Event: &guide.StreamEvent{
			Type: guide.StreamEventData,
			Data: map[string]any{"text": "partial reply"},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(program.messages))
	}
	chunk, ok := program.messages[0].(uimsg.StreamChunkMsg)
	if !ok {
		t.Fatalf("expected StreamChunkMsg, got %T", program.messages[0])
	}
	if chunk.Text != "partial reply" {
		t.Fatalf("chunk text = %q", chunk.Text)
	}
}

func TestGuideBridgeDispatchStream_ProgressEmitsProgressMsg(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-progress",
		RespondingAgentID: "architect",
		Event: &guide.StreamEvent{
			Type: guide.StreamEventProgress,
			Data: map[string]any{
				"current": 3,
				"total":   6,
				"message": "Designing architecture options...",
			},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(program.messages))
	}
	progress, ok := program.messages[0].(uimsg.StreamProgressMsg)
	if !ok {
		t.Fatalf("expected StreamProgressMsg, got %T", program.messages[0])
	}
	if progress.Current != 3 || progress.Total != 6 {
		t.Fatalf("unexpected progress values: %+v", progress)
	}
	if progress.AgentID != "architect" {
		t.Fatalf("unexpected progress agent id: %q", progress.AgentID)
	}
	if progress.Message != "Designing architecture options..." {
		t.Fatalf("unexpected progress message: %q", progress.Message)
	}
}

func TestSummarizeRetryError_ExtractsJSONMessage(t *testing.T) {
	raw := `google code assist generate: code assist generate HTTP 429: {
  "error": {
    "code": 429,
    "message": "You have exhausted your capacity on this model. Your quota will reset after 55s.",
    "status": "RESOURCE_EXHAUSTED",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.ErrorInfo",
        "reason": "RATE_LIMIT_EXCEEDED",
        "domain": "cloudcode-pa.googleapis.com",
        "metadata": {
          "uiMessage": "true",
          "model": "gemini-3.1-pro-preview"
        }
      }
    ]
  }
}`
	got := summarizeRetryError(raw)
	want := "You have exhausted your capacity on this model. Your quota will reset after 55s."
	if got != want {
		t.Fatalf("summarizeRetryError:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestSummarizeRetryError_FallsBackToPrefix(t *testing.T) {
	raw := `provider error HTTP 502: {invalid json`
	got := summarizeRetryError(raw)
	want := "provider error HTTP 502:"
	if got != want {
		t.Fatalf("summarizeRetryError:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestSummarizeRetryError_PlainError(t *testing.T) {
	raw := "connection timeout after 30s"
	got := summarizeRetryError(raw)
	if got != raw {
		t.Fatalf("summarizeRetryError:\n  got:  %q\n  want: %q", got, raw)
	}
}
