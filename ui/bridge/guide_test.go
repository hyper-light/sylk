package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/fetch"
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
		SourceAgentID: "guardian",
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

func TestGuideBridgeDispatch_ForwardsFetchApprovalProposal(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatch(&guide.Message{
		CorrelationID: "approval-fetch-1",
		Type:          guide.MessageTypeProposal,
		SourceAgentID: "guardian",
		Timestamp:     time.Now(),
		Payload: &fetch.FetchProposal{
			URL:            "https://web.dev/articles/lcp",
			Domain:         "web.dev",
			ToolName:       "web_fetch",
			SourceAgent:    "academic",
			Reason:         "verify the official performance guidance",
			RiskAssessment: "external content requires explicit approval",
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(program.messages))
	}
	request, ok := program.messages[0].(uimsg.CommandApprovalRequestMsg)
	if !ok {
		t.Fatalf("expected CommandApprovalRequestMsg, got %T", program.messages[0])
	}
	if request.Proposal == nil {
		t.Fatal("expected fetch proposal")
	}
	if request.Proposal.TargetAgentID != "guardian" {
		t.Fatalf("target agent id = %q, want guardian", request.Proposal.TargetAgentID)
	}
	if request.Proposal.CorrelationID != "approval-fetch-1" {
		t.Fatalf("correlation id = %q, want approval-fetch-1", request.Proposal.CorrelationID)
	}
	if request.Proposal.AgentType != "academic" {
		t.Fatalf("agent type = %q, want academic", request.Proposal.AgentType)
	}
	if request.Proposal.Command != "https://web.dev/articles/lcp" {
		t.Fatalf("command = %q, want fetch url", request.Proposal.Command)
	}
	if request.Proposal.ToolName != "web_fetch" {
		t.Fatalf("tool name = %q, want web_fetch", request.Proposal.ToolName)
	}
	if !request.Proposal.IsFetchApproval() {
		t.Fatalf("expected fetch approval proposal, got %#v", request.Proposal)
	}
}

func TestGuideBridgeDispatch_ForwardsFetchApprovalMapPayload(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatch(&guide.Message{
		CorrelationID: "approval-fetch-map",
		Type:          guide.MessageTypeProposal,
		SourceAgentID: "guardian",
		Payload: map[string]any{
			"url":             "https://docs.python.org/3/library/pathlib.html",
			"domain":          "docs.python.org",
			"source_agent":    "tester",
			"reason":          "confirm the documented pathlib behavior",
			"risk_assessment": "external content requires approval",
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(program.messages))
	}
	request, ok := program.messages[0].(uimsg.CommandApprovalRequestMsg)
	if !ok {
		t.Fatalf("expected CommandApprovalRequestMsg, got %T", program.messages[0])
	}
	if request.Proposal == nil {
		t.Fatal("expected fetch proposal")
	}
	if request.Proposal.ToolName != "web_fetch" {
		t.Fatalf("tool name = %q, want default web_fetch", request.Proposal.ToolName)
	}
	if request.Proposal.Command != "https://docs.python.org/3/library/pathlib.html" {
		t.Fatalf("command = %q, want fetch url", request.Proposal.Command)
	}
	if request.Proposal.TargetAgentID != "guardian" {
		t.Fatalf("target agent id = %q, want guardian", request.Proposal.TargetAgentID)
	}
	if request.Proposal.AgentType != "tester" {
		t.Fatalf("agent type = %q, want tester", request.Proposal.AgentType)
	}
	if !request.Proposal.IsFetchApproval() {
		t.Fatalf("expected fetch approval proposal, got %#v", request.Proposal)
	}
}

func TestGuideBridgePriorityQueue_ApprovalProposalSurvivesStreamFlood(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")

	for i := 0; i < cap(b.buffer); i++ {
		if err := b.onMessage(&guide.Message{
			ID:            fmt.Sprintf("stream-%d", i),
			CorrelationID: fmt.Sprintf("corr-stream-%d", i),
			Type:          guide.MessageTypeStream,
			Payload: &guide.StreamResponse{
				CorrelationID: fmt.Sprintf("corr-stream-%d", i),
				Event: &guide.StreamEvent{
					Type: guide.StreamEventProgress,
					Text: "busy",
				},
			},
		}); err != nil {
			t.Fatalf("enqueue stream flood event %d: %v", i, err)
		}
	}

	proposal := &commandapproval.Proposal{
		CorrelationID: "approval-1",
		Command:       "curl https://example.com",
	}
	if err := b.onMessage(&guide.Message{
		CorrelationID: proposal.CorrelationID,
		Type:          guide.MessageTypeProposal,
		Payload:       proposal,
	}); err != nil {
		t.Fatalf("enqueue approval proposal: %v", err)
	}

	got, ok := b.nextMessage(context.Background())
	if !ok {
		t.Fatal("expected queued approval proposal")
	}
	if got == nil || got.Type != guide.MessageTypeProposal {
		t.Fatalf("first dequeued message = %#v, want approval proposal", got)
	}
}

func TestGuideBridgePriorityQueue_StreamCompleteSurvivesStreamFlood(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")

	for i := 0; i < cap(b.buffer); i++ {
		if err := b.onMessage(&guide.Message{
			ID:            fmt.Sprintf("data-%d", i),
			CorrelationID: fmt.Sprintf("corr-data-%d", i),
			Type:          guide.MessageTypeStream,
			Payload: &guide.StreamResponse{
				CorrelationID: fmt.Sprintf("corr-data-%d", i),
				Event: &guide.StreamEvent{
					Type: guide.StreamEventData,
					Text: "chunk",
				},
			},
		}); err != nil {
			t.Fatalf("enqueue stream data %d: %v", i, err)
		}
	}

	if err := b.onMessage(&guide.Message{
		CorrelationID: "corr-terminal",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID: "corr-terminal",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventComplete,
			},
		},
	}); err != nil {
		t.Fatalf("enqueue stream complete: %v", err)
	}

	got, ok := b.nextMessage(context.Background())
	if !ok {
		t.Fatal("expected queued terminal event")
	}
	stream, streamOK := got.GetStreamResponse()
	if !streamOK || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventComplete {
		t.Fatalf("first dequeued message = %#v, want stream complete", got)
	}
}

func TestGuideBridgePriorityQueue_InterAgentToolCallSurvivesStreamFlood(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")

	for i := 0; i < cap(b.buffer); i++ {
		if err := b.onMessage(&guide.Message{
			ID:            fmt.Sprintf("progress-%d", i),
			CorrelationID: fmt.Sprintf("corr-progress-%d", i),
			Type:          guide.MessageTypeStream,
			Payload: &guide.StreamResponse{
				CorrelationID: fmt.Sprintf("corr-progress-%d", i),
				Event: &guide.StreamEvent{
					Type: guide.StreamEventProgress,
					Data: &guide.ProgressData{Message: "busy"},
				},
			},
		}); err != nil {
			t.Fatalf("enqueue stream progress %d: %v", i, err)
		}
	}

	if err := b.onMessage(&guide.Message{
		CorrelationID: "corr-inter-agent-tool",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID: "corr-inter-agent-tool",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventToolCall,
				Data: map[string]any{
					"tool_call_key": "consult-lib-1",
					"tool_name":     "consult_librarian",
					"phase":         0,
					"inter_agent": map[string]any{
						"kind":        "consult",
						"agent_types": []string{"librarian"},
						"status":      "pending",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("enqueue inter-agent tool call: %v", err)
	}

	got, ok := b.nextMessage(context.Background())
	if !ok {
		t.Fatal("expected queued inter-agent tool call")
	}
	stream, streamOK := got.GetStreamResponse()
	if !streamOK || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventToolCall {
		t.Fatalf("first dequeued message = %#v, want inter-agent tool call", got)
	}
}

func TestGuideBridgePriorityQueue_GenericToolCallSurvivesStreamFlood(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")

	for i := 0; i < cap(b.buffer); i++ {
		if err := b.onMessage(&guide.Message{
			ID:            fmt.Sprintf("progress-generic-%d", i),
			CorrelationID: fmt.Sprintf("corr-progress-generic-%d", i),
			Type:          guide.MessageTypeStream,
			Payload: &guide.StreamResponse{
				CorrelationID: fmt.Sprintf("corr-progress-generic-%d", i),
				Event: &guide.StreamEvent{
					Type: guide.StreamEventProgress,
					Data: &guide.ProgressData{Message: "busy"},
				},
			},
		}); err != nil {
			t.Fatalf("enqueue stream progress %d: %v", i, err)
		}
	}

	if err := b.onMessage(&guide.Message{
		CorrelationID: "corr-generic-tool",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID: "corr-generic-tool",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventToolCall,
				Data: map[string]any{
					"tool_call_key": "read-1",
					"tool_name":     "read_file",
					"phase":         0,
				},
			},
		},
	}); err != nil {
		t.Fatalf("enqueue generic tool call: %v", err)
	}

	got, ok := b.nextMessage(context.Background())
	if !ok {
		t.Fatal("expected queued generic tool call")
	}
	stream, streamOK := got.GetStreamResponse()
	if !streamOK || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventToolCall {
		t.Fatalf("first dequeued message = %#v, want generic tool call", got)
	}
}

func TestGuideBridgePriorityQueue_ApprovalToolCallSurvivesStreamFlood(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")

	for i := 0; i < cap(b.buffer); i++ {
		if err := b.onMessage(&guide.Message{
			ID:            fmt.Sprintf("progress-approval-%d", i),
			CorrelationID: fmt.Sprintf("corr-progress-approval-%d", i),
			Type:          guide.MessageTypeStream,
			Payload: &guide.StreamResponse{
				CorrelationID: fmt.Sprintf("corr-progress-approval-%d", i),
				Event: &guide.StreamEvent{
					Type: guide.StreamEventProgress,
					Data: &guide.ProgressData{Message: "busy"},
				},
			},
		}); err != nil {
			t.Fatalf("enqueue stream progress %d: %v", i, err)
		}
	}

	if err := b.onMessage(&guide.Message{
		CorrelationID: "corr-inter-agent-approval-tool",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID: "corr-inter-agent-approval-tool",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventToolCall,
				Data: map[string]any{
					"tool_call_key": "approval-guardian-1",
					"tool_name":     "approval_guardian",
					"phase":         0,
					"inter_agent": map[string]any{
						"kind":        "approval",
						"agent_types": []string{"guardian"},
						"status":      "pending",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("enqueue inter-agent approval tool call: %v", err)
	}

	got, ok := b.nextMessage(context.Background())
	if !ok {
		t.Fatal("expected queued inter-agent approval tool call")
	}
	stream, streamOK := got.GetStreamResponse()
	if !streamOK || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventToolCall {
		t.Fatalf("first dequeued message = %#v, want inter-agent approval tool call", got)
	}
}

func TestGuideBridgePriorityQueue_NestedBranchProgressSurvivesStreamFlood(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")

	for i := 0; i < cap(b.buffer); i++ {
		if err := b.onMessage(&guide.Message{
			ID:            fmt.Sprintf("data-%d", i),
			CorrelationID: fmt.Sprintf("corr-data-%d", i),
			Type:          guide.MessageTypeStream,
			Payload: &guide.StreamResponse{
				CorrelationID: fmt.Sprintf("corr-data-%d", i),
				Event: &guide.StreamEvent{
					Type: guide.StreamEventData,
					Text: "chunk",
				},
			},
		}); err != nil {
			t.Fatalf("enqueue stream data %d: %v", i, err)
		}
	}

	if err := b.onMessage(&guide.Message{
		CorrelationID: "corr-nested-progress",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID: "corr-nested-progress",
			Metadata: map[string]any{
				"chat_nested_branch":         true,
				"chat_parent_correlation_id": "corr-academic",
				"chat_parent_tool_call_key":  "consult-1",
				"chat_inter_agent_kind":      "consult",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Searching project history."},
			},
		},
	}); err != nil {
		t.Fatalf("enqueue nested branch progress: %v", err)
	}

	got, ok := b.nextMessage(context.Background())
	if !ok {
		t.Fatal("expected queued nested branch progress")
	}
	stream, streamOK := got.GetStreamResponse()
	if !streamOK || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventProgress {
		t.Fatalf("first dequeued message = %#v, want nested branch progress", got)
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

	msg := toGuideMsg(resp, nil)
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

	msg := toGuideMsg(resp, nil)
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

	msg := toGuideMsg(resp, nil)
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

	msg := toGuideMsg(resp, nil)
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

	msg := toGuideMsg(resp, nil)
	if msg.Content != "Guide pending requests: 3." {
		t.Fatalf("content = %q", msg.Content)
	}
}

func TestToGuideMsg_PreservesInterAgentBranchMetadata(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-nested",
		RespondingAgentID:   "librarian",
		RespondingAgentName: "Librarian",
		Success:             true,
		Data:                "Found a prior pattern.",
	}

	msg := toGuideMsg(resp, map[string]any{
		"agent_type":                  "librarian",
		"chat_nested_branch":          true,
		"chat_parent_correlation_id":  "corr-parent",
		"chat_parent_tool_call_key":   "consult-1",
		"chat_inter_agent_kind":       "consult",
		"chat_inter_agent_thread_key": "",
	})
	if msg.AgentType != "librarian" {
		t.Fatalf("AgentType = %q, want librarian", msg.AgentType)
	}
	if msg.BranchRef == nil {
		t.Fatal("expected branch metadata to be preserved on guide response")
	}
	if msg.BranchRef.ParentCorrelationID != "corr-parent" || msg.BranchRef.ParentToolCallKey != "consult-1" {
		t.Fatalf("unexpected branch ref: %+v", msg.BranchRef)
	}
}

func TestParseInterAgentBranchRefFromMetadata_AcceptsStringifiedNestedFlag(t *testing.T) {
	ref := parseInterAgentBranchRefFromMetadata(map[string]any{
		"chat_nested_branch":          "true",
		"chat_parent_correlation_id":  "corr-parent",
		"chat_parent_tool_call_key":   "",
		"chat_inter_agent_kind":       "consult",
		"chat_inter_agent_thread_key": "thread-1",
	})
	if ref == nil {
		t.Fatal("expected stringified nested flag to parse into a branch ref")
	}
	if ref.ParentCorrelationID != "corr-parent" {
		t.Fatalf("parent correlation id = %q, want corr-parent", ref.ParentCorrelationID)
	}
	if ref.ThreadKey != "thread-1" {
		t.Fatalf("thread key = %q, want thread-1", ref.ThreadKey)
	}
	if ref.Kind != "consult" {
		t.Fatalf("kind = %q, want consult", ref.Kind)
	}
}

func TestParseToolCallEventMsg_UsesEventStreamMetadataFallbackForBranchRef(t *testing.T) {
	stream := &guide.StreamResponse{
		CorrelationID:     "corr-tool-fallback",
		RespondingAgentID: "academic",
		Event: &guide.StreamEvent{
			Type: guide.StreamEventToolCall,
			Data: map[string]any{
				"tool_name":     "consult",
				"tool_call_key": "consult-1",
				"phase":         0,
				"stream_metadata": map[string]any{
					"agent_type":                  "academic",
					"chat_nested_branch":          true,
					"chat_parent_correlation_id":  "corr-parent",
					"chat_parent_tool_call_key":   "consult-root-1",
					"chat_inter_agent_kind":       "consult",
					"chat_inter_agent_thread_key": "thread-1",
				},
			},
		},
	}

	msg := parseToolCallEventMsg("session-1", "corr-tool-fallback", stream)
	if msg.AgentType != "academic" {
		t.Fatalf("AgentType = %q, want academic", msg.AgentType)
	}
	if msg.BranchRef == nil {
		t.Fatal("expected branch ref from event stream_metadata fallback")
	}
	if msg.BranchRef.ParentCorrelationID != "corr-parent" || msg.BranchRef.ParentToolCallKey != "consult-root-1" {
		t.Fatalf("unexpected branch ref: %+v", msg.BranchRef)
	}
	if msg.BranchRef.ThreadKey != "thread-1" {
		t.Fatalf("thread key = %q, want thread-1", msg.BranchRef.ThreadKey)
	}
}

func TestParseToolCallEventMsg_UsesEventMetadataFallbackForBranchRef(t *testing.T) {
	stream := &guide.StreamResponse{
		CorrelationID:     "corr-tool-metadata-fallback",
		RespondingAgentID: "librarian",
		Event: &guide.StreamEvent{
			Type: guide.StreamEventToolCall,
			Data: map[string]any{
				"tool_name":     "web_search",
				"tool_call_key": "ws_1",
				"phase":         0,
				"metadata": map[string]any{
					"agent_type":                 "librarian",
					"chat_nested_branch":         "true",
					"chat_parent_correlation_id": "corr-parent",
					"chat_parent_tool_call_key":  "consult-lib-1",
					"chat_inter_agent_kind":      "consult",
				},
			},
		},
	}

	msg := parseToolCallEventMsg("session-1", "corr-tool-metadata-fallback", stream)
	if msg.AgentType != "librarian" {
		t.Fatalf("AgentType = %q, want librarian", msg.AgentType)
	}
	if msg.BranchRef == nil {
		t.Fatal("expected branch ref from event metadata fallback")
	}
	if msg.BranchRef.ParentCorrelationID != "corr-parent" || msg.BranchRef.ParentToolCallKey != "consult-lib-1" {
		t.Fatalf("unexpected branch ref: %+v", msg.BranchRef)
	}
}

func TestParseToolCallEventMsg_EventMetadataOverridesStaleStreamMetadata(t *testing.T) {
	stream := &guide.StreamResponse{
		CorrelationID:     "corr-tool-event-override",
		RespondingAgentID: "academic",
		Metadata: map[string]any{
			"agent_type":                  "academic",
			"chat_nested_branch":          true,
			"chat_parent_correlation_id":  "corr-stale-parent",
			"chat_parent_tool_call_key":   "consult-stale-1",
			"chat_inter_agent_kind":       "consult",
			"chat_inter_agent_thread_key": "thread-stale",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventToolCall,
			Data: map[string]any{
				"tool_name":     "consult",
				"tool_call_key": "consult-lib-1",
				"phase":         0,
				"stream_metadata": map[string]any{
					"agent_type":                  "academic",
					"chat_nested_branch":          true,
					"chat_parent_correlation_id":  "corr-child-parent",
					"chat_parent_tool_call_key":   "consult-child-1",
					"chat_inter_agent_kind":       "consult",
					"chat_inter_agent_thread_key": "thread-child",
				},
			},
		},
	}

	msg := parseToolCallEventMsg("session-1", "corr-tool-event-override", stream)
	if msg.BranchRef == nil {
		t.Fatal("expected branch ref from event metadata override")
	}
	if msg.BranchRef.ParentCorrelationID != "corr-child-parent" || msg.BranchRef.ParentToolCallKey != "consult-child-1" {
		t.Fatalf("unexpected branch ref: %+v", msg.BranchRef)
	}
	if msg.BranchRef.ThreadKey != "thread-child" {
		t.Fatalf("thread key = %q, want thread-child", msg.BranchRef.ThreadKey)
	}
}

func TestGuideBridgeDispatch_UsesEnvelopeMetadataForNestedStreamStart(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatch(&guide.Message{
		CorrelationID: "corr-envelope-start",
		Type:          guide.MessageTypeStream,
		Metadata: map[string]any{
			"agent_type":                  "academic",
			"chat_nested_branch":          true,
			"chat_parent_correlation_id":  "corr-parent",
			"chat_parent_tool_call_key":   "consult-root-1",
			"chat_inter_agent_kind":       "consult",
			"chat_inter_agent_thread_key": "thread-1",
		},
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-envelope-start",
			RespondingAgentID: "academic",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventStart,
			},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(program.messages))
	}
	start, ok := program.messages[0].(uimsg.StreamStartMsg)
	if !ok {
		t.Fatalf("expected StreamStartMsg, got %T", program.messages[0])
	}
	if start.AgentType != "academic" {
		t.Fatalf("AgentType = %q, want academic", start.AgentType)
	}
	if start.BranchRef == nil {
		t.Fatal("expected branch ref from stream envelope metadata")
	}
	if start.BranchRef.ParentCorrelationID != "corr-parent" || start.BranchRef.ParentToolCallKey != "consult-root-1" {
		t.Fatalf("unexpected branch ref: %+v", start.BranchRef)
	}
	if start.BranchRef.ThreadKey != "thread-1" {
		t.Fatalf("thread key = %q, want thread-1", start.BranchRef.ThreadKey)
	}
}

func TestGuideBridgeDispatch_UsesEnvelopeMetadataForNestedToolCall(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatch(&guide.Message{
		CorrelationID: "corr-envelope-tool",
		Type:          guide.MessageTypeStream,
		Metadata: map[string]any{
			"agent_type":                  "academic",
			"chat_nested_branch":          true,
			"chat_parent_correlation_id":  "corr-parent",
			"chat_parent_tool_call_key":   "consult-root-1",
			"chat_inter_agent_kind":       "consult",
			"chat_inter_agent_thread_key": "thread-1",
		},
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-envelope-tool",
			RespondingAgentID: "academic",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventToolCall,
				Data: map[string]any{
					"tool_name":     "web_search",
					"tool_call_key": "ws_1",
					"phase":         0,
				},
			},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(program.messages))
	}
	ev, ok := program.messages[0].(uimsg.ToolCallEventMsg)
	if !ok {
		t.Fatalf("expected ToolCallEventMsg, got %T", program.messages[0])
	}
	if ev.AgentType != "academic" {
		t.Fatalf("AgentType = %q, want academic", ev.AgentType)
	}
	if ev.BranchRef == nil {
		t.Fatal("expected branch ref from stream envelope metadata")
	}
	if ev.BranchRef.ParentCorrelationID != "corr-parent" || ev.BranchRef.ParentToolCallKey != "consult-root-1" {
		t.Fatalf("unexpected branch ref: %+v", ev.BranchRef)
	}
	if ev.BranchRef.ThreadKey != "thread-1" {
		t.Fatalf("thread key = %q, want thread-1", ev.BranchRef.ThreadKey)
	}
}

func TestToGuideMsg_PreservesApprovalBranchMetadata(t *testing.T) {
	resp := &guide.RouteResponse{
		CorrelationID:       "corr-approval",
		RespondingAgentID:   "guardian",
		RespondingAgentName: "Guardian",
		Success:             true,
		Data:                map[string]any{"approved": true},
	}

	msg := toGuideMsg(resp, map[string]any{
		"agent_type":                  "guardian",
		"chat_nested_branch":          true,
		"chat_parent_correlation_id":  "corr-parent",
		"chat_parent_tool_call_key":   "approval-1",
		"chat_inter_agent_kind":       "approval",
		"chat_inter_agent_thread_key": "",
	})
	if msg.BranchRef == nil {
		t.Fatal("expected approval branch metadata to be preserved on guide response")
	}
	if msg.BranchRef.Kind != "approval" {
		t.Fatalf("branch kind = %q, want approval", msg.BranchRef.Kind)
	}
	if msg.BranchRef.ParentCorrelationID != "corr-parent" || msg.BranchRef.ParentToolCallKey != "approval-1" {
		t.Fatalf("unexpected branch ref: %+v", msg.BranchRef)
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

	msg := toGuideMsg(resp, nil)
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

	msg := toGuideMsg(resp, nil)
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

func TestParseStreamMessages_PreferMetadataAgentName(t *testing.T) {
	stream := &guide.StreamResponse{
		CorrelationID:     "corr-pipeline-agent-name",
		RespondingAgentID: "8a7d3b2c",
		Metadata: map[string]any{
			"agent_name":  "Pipeline Inspector",
			"agent_type":  "inspector-pipeline",
			"pipeline_id": "task_auth_checkout",
			"task_id":     "task_auth_checkout",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventProgress,
			Data: &guide.ProgressData{Message: "Inspecting criteria."},
		},
	}

	start := parseStreamStartMsg("session-1", "corr-pipeline-agent-name", stream)
	if start.AgentName != "Pipeline Inspector" {
		t.Fatalf("start.AgentName = %q, want Pipeline Inspector", start.AgentName)
	}
	if start.AgentType != "inspector-pipeline" {
		t.Fatalf("start.AgentType = %q, want inspector-pipeline", start.AgentType)
	}

	progress := toStreamProgressMsg("session-1", "corr-pipeline-agent-name", stream)
	if progress.AgentName != "Pipeline Inspector" {
		t.Fatalf("progress.AgentName = %q, want Pipeline Inspector", progress.AgentName)
	}
	if progress.AgentType != "inspector-pipeline" {
		t.Fatalf("progress.AgentType = %q, want inspector-pipeline", progress.AgentType)
	}

	complete := parseStreamCompleteMsg("session-1", "corr-pipeline-agent-name", stream)
	if complete.AgentName != "Pipeline Inspector" {
		t.Fatalf("complete.AgentName = %q, want Pipeline Inspector", complete.AgentName)
	}
	if complete.AgentType != "inspector-pipeline" {
		t.Fatalf("complete.AgentType = %q, want inspector-pipeline", complete.AgentType)
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

func TestFormatAnswerPayload_ContentField(t *testing.T) {
	payload := map[string]any{
		"type":    "recall",
		"content": "Use a Go API with a thin React frontend.",
	}

	text, ok := formatAnswerPayload(payload)
	if !ok {
		t.Fatal("expected formatAnswerPayload to succeed for content field")
	}
	if text != "Use a Go API with a thin React frontend." {
		t.Fatalf("text = %q", text)
	}
}
