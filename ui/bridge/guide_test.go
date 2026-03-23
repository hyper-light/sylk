package bridge

import (
	"errors"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/providers"
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

func TestGuideBridgeDispatch_ForwardsCommandApprovalProposal(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	proposal := &commandapproval.Proposal{
		CorrelationID: "approval-1",
		Command:       "mkdir -p src/hello_cli",
		PersistLabel:  "mkdir inside workspace",
	}
	b.dispatch(&guide.Message{
		CorrelationID: "approval-1",
		Type:          guide.MessageTypeProposal,
		Payload:       proposal,
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(program.messages))
	}
	request, ok := program.messages[0].(uimsg.CommandApprovalRequestMsg)
	if !ok {
		t.Fatalf("expected CommandApprovalRequestMsg, got %T", program.messages[0])
	}
	if request.Proposal == nil || request.Proposal.Command != proposal.Command {
		t.Fatalf("expected proposal command %q, got %#v", proposal.Command, request.Proposal)
	}
}

func TestGuideBridgeDispatch_ForwardsLayerDecisionRequest(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatch(&guide.Message{
		CorrelationID: "decision-1",
		Type:          guide.MessageTypeLayerDecision,
		Payload: map[string]any{
			"dag_id":    "dag-1",
			"layer_idx": 2,
			"failed_nodes": []any{
				map[string]any{
					"node_id":    "node-1",
					"node_name":  "tester-pipeline",
					"agent_type": "tester-pipeline",
					"error":      "tests failed",
				},
			},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(program.messages))
	}
	request, ok := program.messages[0].(uimsg.LayerDecisionMsg)
	if !ok {
		t.Fatalf("expected LayerDecisionMsg, got %T", program.messages[0])
	}
	if request.DAGID != "dag-1" || request.LayerIdx != 2 {
		t.Fatalf("unexpected layer decision payload: %#v", request)
	}
	if len(request.FailedNodes) != 1 || request.FailedNodes[0].Error != "tests failed" {
		t.Fatalf("unexpected failed node payload: %#v", request.FailedNodes)
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
				"Status":       "clarifying",
				"UserResponse": "I recommend starting with Google and Entra for phase 1 enterprise SSO.",
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

func TestToGuideMsg_HumanizesTesterStagePayload(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-tester-stage",
		RespondingAgentID:   "tester-pipeline",
		RespondingAgentName: "Pipeline Tester",
		Success:             true,
		Data: map[string]any{
			"CreatedFiles": []any{"tests/test_cli.py"},
			"SuiteResult": map[string]any{
				"total_tests": 1,
				"passed":      0,
				"failed":      1,
				"skipped":     0,
				"errors":      0,
			},
		},
	}

	msg := toGuideMsg(resp)
	if !strings.Contains(msg.Content, "Created test artifacts: tests/test_cli.py.") {
		t.Fatalf("expected created files summary, got %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "Latest test run: 0 passed, 1 failed, 0 skipped, 0 errors out of 1.") {
		t.Fatalf("expected suite summary, got %q", msg.Content)
	}
}

func TestParsePlanTaskSnapshot_ParsesExamplesAndGuidelines(t *testing.T) {
	snap := parsePlanTaskSnapshot(map[string]any{
		"ID":                  "task-1",
		"Name":                "Create CLI",
		"ImplementationGuide": "Step 1: add the command.",
		"Guidelines":          []any{"Follow existing CLI naming."},
		"Examples": []any{
			map[string]any{
				"Label":       "CLI usage",
				"Language":    "sh",
				"Code":        "sylk plan approve --plan-id plan_123",
				"Explanation": "Shows the approval entrypoint.",
			},
		},
	})

	if len(snap.Guidelines) != 1 || snap.Guidelines[0] != "Follow existing CLI naming." {
		t.Fatalf("unexpected guidelines: %#v", snap.Guidelines)
	}
	if len(snap.Examples) != 1 {
		t.Fatalf("expected 1 parsed example, got %d", len(snap.Examples))
	}
	if snap.Examples[0].Language != "sh" {
		t.Fatalf("example language = %q, want sh", snap.Examples[0].Language)
	}
}

func TestToGuideMsg_HumanizesInspectorStagePayload(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-inspector-stage",
		RespondingAgentID:   "inspector-pipeline",
		RespondingAgentName: "Pipeline Inspector",
		Success:             true,
		Data: map[string]any{
			"Criteria": map[string]any{
				"success_criteria": []any{
					map[string]any{"id": "SC-01"},
					map[string]any{"id": "SC-02"},
				},
				"quality_gates": []any{
					map[string]any{"name": "coverage"},
				},
				"constraints": []any{
					map[string]any{"type": "dependency"},
					map[string]any{"type": "architecture"},
				},
			},
		},
	}

	msg := toGuideMsg(resp)
	if strings.Contains(msg.Content, "\"success_criteria\"") || strings.Contains(msg.Content, "\"quality_gates\"") {
		t.Fatalf("expected humanized inspector payload, got raw JSON %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "Defined criteria contract: 2 success criteria, 1 quality gates, 2 constraints.") {
		t.Fatalf("expected criteria summary, got %q", msg.Content)
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

func TestGuideBridgeDispatchStream_CompleteHumanizesPipelineTesterEnvelope(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-tester-stream",
		RespondingAgentID: "tester-pipeline",
		Event: &guide.StreamEvent{
			Type: guide.StreamEventComplete,
			Data: map[string]any{
				"result": map[string]any{
					"created_files": []any{"tests/test_cli.py"},
					"suite_result": map[string]any{
						"total_tests": 1,
						"passed":      0,
						"failed":      1,
						"skipped":     0,
						"errors":      0,
					},
				},
				"action": map[string]any{
					"type": "validate",
				},
			},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(program.messages))
	}
	complete, ok := program.messages[0].(uimsg.StreamCompleteMsg)
	if !ok {
		t.Fatalf("expected StreamCompleteMsg, got %T", program.messages[0])
	}
	if strings.Contains(complete.AuthoritativeText, "\"created_files\"") || strings.Contains(complete.AuthoritativeText, "\"suite_result\"") {
		t.Fatalf("expected humanized tester payload, got raw JSON %q", complete.AuthoritativeText)
	}
	if !strings.Contains(complete.AuthoritativeText, "Created test artifacts: tests/test_cli.py.") {
		t.Fatalf("expected created files summary, got %q", complete.AuthoritativeText)
	}
}

func TestGuideBridgeDispatchStream_CompleteHumanizesPipelineInspectorEnvelope(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-inspector-stream",
		RespondingAgentID: "inspector-pipeline",
		Event: &guide.StreamEvent{
			Type: guide.StreamEventComplete,
			Data: map[string]any{
				"result": map[string]any{
					"Criteria": map[string]any{
						"success_criteria": []any{
							map[string]any{"id": "SC-01"},
							map[string]any{"id": "SC-02"},
							map[string]any{"id": "SC-03"},
						},
						"quality_gates": []any{
							map[string]any{"name": "blocking_issues"},
							map[string]any{"name": "coverage"},
						},
						"constraints": []any{
							map[string]any{"type": "dependency"},
						},
					},
					"Result": nil,
				},
				"action": map[string]any{
					"type": "handoff",
				},
			},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(program.messages))
	}
	complete, ok := program.messages[0].(uimsg.StreamCompleteMsg)
	if !ok {
		t.Fatalf("expected StreamCompleteMsg, got %T", program.messages[0])
	}
	if strings.Contains(complete.AuthoritativeText, "\"Criteria\"") || strings.Contains(complete.AuthoritativeText, "\"success_criteria\"") {
		t.Fatalf("expected humanized inspector payload, got raw JSON %q", complete.AuthoritativeText)
	}
	if !strings.Contains(complete.AuthoritativeText, "Defined criteria contract: 3 success criteria, 2 quality gates, 1 constraints.") {
		t.Fatalf("expected criteria summary, got %q", complete.AuthoritativeText)
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

func TestGuideBridgeDispatchStream_PreservesPipelineMetadata(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-pipeline",
		RespondingAgentID: "dc484039",
		Metadata: map[string]any{
			"agent_type":  "designer",
			"agent_name":  "Designer",
			"pipeline_id": "task_auth_checkout",
			"task_id":     "task_auth_checkout",
			"task_slug":   "auth-checkout",
		},
		Event: &guide.StreamEvent{Type: guide.StreamEventStart},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(program.messages))
	}
	start, ok := program.messages[0].(uimsg.StreamStartMsg)
	if !ok {
		t.Fatalf("expected StreamStartMsg, got %T", program.messages[0])
	}
	if start.AgentType != "designer" || start.PipelineID != "task_auth_checkout" || start.TaskSlug != "auth-checkout" {
		t.Fatalf("unexpected pipeline metadata: %+v", start)
	}
}

func TestFriendlyRetryError_ExtractsJSONMessage(t *testing.T) {
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
	got := providers.FriendlyErrorMessage(errors.New(raw))
	want := "You have exhausted your capacity on this model. Your quota will reset after 55s."
	if got != want {
		t.Fatalf("FriendlyErrorMessage:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestFriendlyRetryError_FallsBackToPrefix(t *testing.T) {
	raw := `provider error HTTP 502: {invalid json`
	got := providers.FriendlyErrorMessage(errors.New(raw))
	want := raw
	if got != want {
		t.Fatalf("FriendlyErrorMessage:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestFriendlyRetryError_PlainError(t *testing.T) {
	raw := "connection timeout after 30s"
	got := providers.FriendlyErrorMessage(errors.New(raw))
	if got != raw {
		t.Fatalf("FriendlyErrorMessage:\n  got:  %q\n  want: %q", got, raw)
	}
}

func TestFormatConversationResult_PascalCaseKeys(t *testing.T) {
	// Architect's ConversationResult has no JSON tags → PascalCase keys.
	payload := map[string]any{
		"Response":      "Plan dispatched to the orchestrator.",
		"Intent":        "execute",
		"HandoffTarget": "orchestrator",
	}
	text, ok := formatConversationResult(payload)
	if !ok {
		t.Fatal("expected formatConversationResult to succeed for PascalCase keys")
	}
	if text != "Plan dispatched to the orchestrator." {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestFormatConversationResult_LowercaseKeys(t *testing.T) {
	// Orchestrator's ConversationResult has JSON tags → lowercase keys.
	payload := map[string]any{
		"response": "Plan ingested. DAG started.",
		"intent":   "ingestion_ack",
	}
	text, ok := formatConversationResult(payload)
	if !ok {
		t.Fatal("expected formatConversationResult to succeed for lowercase keys")
	}
	if text != "Plan ingested. DAG started." {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestFormatConversationResult_EmptyResponse(t *testing.T) {
	payload := map[string]any{
		"response": "",
		"intent":   "chat",
	}
	_, ok := formatConversationResult(payload)
	if ok {
		t.Fatal("expected formatConversationResult to return false for empty response")
	}
}

func TestFormatConversationResult_MissingIntent(t *testing.T) {
	payload := map[string]any{
		"response": "some text",
	}
	_, ok := formatConversationResult(payload)
	if ok {
		t.Fatal("expected formatConversationResult to return false when intent is missing")
	}
}
