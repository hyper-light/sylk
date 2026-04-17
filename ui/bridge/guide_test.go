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
	"github.com/adalundhe/sylk/core/events"
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

type emptyResponseTextPayload struct{}

func (emptyResponseTextPayload) ResponseText() string { return "" }

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

func TestGuideBridgePriorityQueue_HoldsTerminalUntilEarlierSamePhaseProgressDequeues(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")

	progress := &guide.Message{
		CorrelationID: "corr-ordered-terminal",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-ordered-terminal",
			RespondingAgentID: "inspector",
			Metadata: map[string]any{
				"agent_type": "inspector",
				"task_id":    "task_1",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Working through this with challenge agent."},
			},
		},
	}
	complete := &guide.Message{
		CorrelationID: "corr-ordered-terminal",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-ordered-terminal",
			RespondingAgentID: "inspector",
			Metadata: map[string]any{
				"agent_type": "inspector",
				"task_id":    "task_1",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventComplete,
			},
		},
	}

	if err := b.onMessage(progress); err != nil {
		t.Fatalf("enqueue progress: %v", err)
	}
	if err := b.onMessage(complete); err != nil {
		t.Fatalf("enqueue complete: %v", err)
	}

	first, ok := b.nextMessage(context.Background())
	if !ok {
		t.Fatal("expected first queued message")
	}
	firstStream, ok := first.GetStreamResponse()
	if !ok || firstStream == nil || firstStream.Event == nil || firstStream.Event.Type != guide.StreamEventProgress {
		t.Fatalf("first dequeued message = %#v, want same-phase progress", first)
	}

	second, ok := b.nextMessage(context.Background())
	if !ok {
		t.Fatal("expected held terminal after progress drains")
	}
	secondStream, ok := second.GetStreamResponse()
	if !ok || secondStream == nil || secondStream.Event == nil || secondStream.Event.Type != guide.StreamEventComplete {
		t.Fatalf("second dequeued message = %#v, want held stream complete", second)
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

func TestGuideBridgeDispatch_PreservesRememberedTaskIdentityAcrossSparseProgress(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatch(&guide.Message{
		CorrelationID: "corr-inspector-sparse-progress",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-inspector-sparse-progress",
			RespondingAgentID: "runtime-inspector",
			Metadata: map[string]any{
				"agent_type": "inspector-pipeline",
				"task_id":    "task_3",
				"task_name":  "Create hello.py CLI entrypoint",
				"task_slug":  "create-cli-entrypoint",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Initial inspector progress."},
			},
		},
	}, program)

	program.messages = nil

	b.dispatch(&guide.Message{
		CorrelationID: "corr-inspector-sparse-progress",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-inspector-sparse-progress",
			RespondingAgentID: "runtime-inspector",
			Metadata: map[string]any{
				"agent_type": "inspector-pipeline",
				"task_id":    "",
				"task_name":  "",
				"task_slug":  "",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Sparse follow-up progress."},
			},
		},
	}, program)

	if len(program.messages) == 0 {
		t.Fatal("expected follow-up progress messages")
	}
	progress, ok := program.messages[len(program.messages)-1].(uimsg.StreamProgressMsg)
	if !ok {
		t.Fatalf("expected final dispatched message to be StreamProgressMsg, got %T", program.messages[len(program.messages)-1])
	}
	if progress.TaskID != "task_3" {
		t.Fatalf("progress TaskID = %q, want remembered task_3", progress.TaskID)
	}
	if progress.TaskName != "Create hello.py CLI entrypoint" {
		t.Fatalf("progress TaskName = %q, want remembered task label", progress.TaskName)
	}
	if progress.TaskSlug != "create-cli-entrypoint" {
		t.Fatalf("progress TaskSlug = %q, want remembered create-cli-entrypoint", progress.TaskSlug)
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

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + tool event, got %d forwarded messages", len(program.messages))
	}
	start, ok := program.messages[0].(uimsg.StreamStartMsg)
	if !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	if start.BranchRef == nil {
		t.Fatal("expected branch ref on synthetic nested start")
	}
	ev, ok := program.messages[1].(uimsg.ToolCallEventMsg)
	if !ok {
		t.Fatalf("expected second message to be ToolCallEventMsg, got %T", program.messages[1])
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

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + complete with authoritative text, got %d messages", len(program.messages))
	}
	if _, ok := program.messages[0].(uimsg.StreamStartMsg); !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	complete, ok := program.messages[1].(uimsg.StreamCompleteMsg)
	if !ok {
		t.Fatalf("expected second message to be StreamCompleteMsg, got %T", program.messages[1])
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
			"agent_name":                 "Pipeline Inspector",
			"agent_type":                 "inspector-pipeline",
			"pipeline_id":                "task_auth_checkout",
			"task_id":                    "task_auth_checkout",
			"chat_top_level_transfer":    true,
			"chat_parent_correlation_id": "corr-parent-top-level",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventProgress,
			Data: &guide.ProgressData{Message: "Inspecting criteria.", ToolDerived: true},
		},
	}

	start := parseStreamStartMsg("session-1", "corr-pipeline-agent-name", stream)
	if start.AgentID != "8a7d3b2c" {
		t.Fatalf("start.AgentID = %q, want 8a7d3b2c", start.AgentID)
	}
	if start.AgentName != "Pipeline Inspector" {
		t.Fatalf("start.AgentName = %q, want Pipeline Inspector", start.AgentName)
	}
	if start.AgentType != "inspector-pipeline" {
		t.Fatalf("start.AgentType = %q, want inspector-pipeline", start.AgentType)
	}
	if start.RuntimeAgentID != "8a7d3b2c" {
		t.Fatalf("start.RuntimeAgentID = %q, want 8a7d3b2c", start.RuntimeAgentID)
	}
	if start.ParentCorrelationID != "corr-parent-top-level" {
		t.Fatalf("start.ParentCorrelationID = %q, want corr-parent-top-level", start.ParentCorrelationID)
	}
	if !start.TopLevelTransfer {
		t.Fatal("expected top-level transfer marker on parsed start")
	}
	if start.Visibility != events.VisibilityUser {
		t.Fatalf("start.Visibility = %v, want %v", start.Visibility, events.VisibilityUser)
	}
	if start.BranchRef != nil {
		t.Fatalf("start.BranchRef = %+v, want nil for top-level transfer metadata", start.BranchRef)
	}

	progress := toStreamProgressMsg("session-1", "corr-pipeline-agent-name", stream)
	if progress.AgentID != "8a7d3b2c" {
		t.Fatalf("progress.AgentID = %q, want 8a7d3b2c", progress.AgentID)
	}
	if progress.AgentName != "Pipeline Inspector" {
		t.Fatalf("progress.AgentName = %q, want Pipeline Inspector", progress.AgentName)
	}
	if progress.AgentType != "inspector-pipeline" {
		t.Fatalf("progress.AgentType = %q, want inspector-pipeline", progress.AgentType)
	}
	if progress.RuntimeAgentID != "8a7d3b2c" {
		t.Fatalf("progress.RuntimeAgentID = %q, want 8a7d3b2c", progress.RuntimeAgentID)
	}
	if progress.ParentCorrelationID != "corr-parent-top-level" {
		t.Fatalf("progress.ParentCorrelationID = %q, want corr-parent-top-level", progress.ParentCorrelationID)
	}
	if !progress.TopLevelTransfer {
		t.Fatal("expected top-level transfer marker on parsed progress")
	}
	if !progress.ToolDerived {
		t.Fatal("expected tool-derived progress flag to survive bridge parsing")
	}
	if progress.BranchRef != nil {
		t.Fatalf("progress.BranchRef = %+v, want nil for top-level transfer metadata", progress.BranchRef)
	}

	tool := parseToolCallEventMsg("session-1", "corr-pipeline-agent-name", &guide.StreamResponse{
		CorrelationID:     "corr-pipeline-agent-name",
		RespondingAgentID: "8a7d3b2c",
		Metadata: map[string]any{
			"agent_name":                 "Pipeline Inspector",
			"agent_type":                 "inspector-pipeline",
			"pipeline_id":                "task_auth_checkout",
			"task_id":                    "task_auth_checkout",
			"chat_top_level_transfer":    true,
			"chat_parent_correlation_id": "corr-parent-top-level",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventToolCall,
			Data: map[string]any{
				"phase":         0,
				"tool_name":     "coord_publish_artifact",
				"tool_call_key": "tool-1",
			},
		},
	})
	if tool.AgentID != "8a7d3b2c" {
		t.Fatalf("tool.AgentID = %q, want 8a7d3b2c", tool.AgentID)
	}
	if tool.ParentCorrelationID != "corr-parent-top-level" {
		t.Fatalf("tool.ParentCorrelationID = %q, want corr-parent-top-level", tool.ParentCorrelationID)
	}
	if !tool.TopLevelTransfer {
		t.Fatal("expected top-level transfer marker on parsed tool call")
	}
	if tool.BranchRef != nil {
		t.Fatalf("tool.BranchRef = %+v, want nil for top-level transfer metadata", tool.BranchRef)
	}

	complete := parseStreamCompleteMsg("session-1", "corr-pipeline-agent-name", stream)
	if complete.AgentID != "8a7d3b2c" {
		t.Fatalf("complete.AgentID = %q, want 8a7d3b2c", complete.AgentID)
	}
	if complete.AgentName != "Pipeline Inspector" {
		t.Fatalf("complete.AgentName = %q, want Pipeline Inspector", complete.AgentName)
	}
	if complete.AgentType != "inspector-pipeline" {
		t.Fatalf("complete.AgentType = %q, want inspector-pipeline", complete.AgentType)
	}
	if complete.RuntimeAgentID != "8a7d3b2c" {
		t.Fatalf("complete.RuntimeAgentID = %q, want 8a7d3b2c", complete.RuntimeAgentID)
	}
	if complete.ParentCorrelationID != "corr-parent-top-level" {
		t.Fatalf("complete.ParentCorrelationID = %q, want corr-parent-top-level", complete.ParentCorrelationID)
	}
	if !complete.TopLevelTransfer {
		t.Fatal("expected top-level transfer marker on parsed complete")
	}
	if complete.Visibility != events.VisibilityUser {
		t.Fatalf("complete.Visibility = %v, want %v", complete.Visibility, events.VisibilityUser)
	}
	if complete.BranchRef != nil {
		t.Fatalf("complete.BranchRef = %+v, want nil for top-level transfer metadata", complete.BranchRef)
	}
}

func TestGuideBridgeDispatch_RemembersNestedBranchMetadataAcrossIncompleteEvents(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatch(&guide.Message{
		CorrelationID: "corr-remembered-nested",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-remembered-nested",
			RespondingAgentID: "runtime-tester",
			Metadata: map[string]any{
				"agent_type":                 "tester-pipeline",
				"task_id":                    "task_1",
				"chat_nested_branch":         true,
				"chat_parent_correlation_id": "corr-parent",
				"chat_parent_tool_call_key":  "challenge-1",
				"chat_inter_agent_kind":      "challenge",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Working the challenge."},
			},
		},
	}, program)

	b.dispatch(&guide.Message{
		CorrelationID: "corr-remembered-nested",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-remembered-nested",
			RespondingAgentID: "runtime-tester",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventToolCall,
				Data: map[string]any{
					"phase":         0,
					"tool_name":     "run_test_suite",
					"tool_call_key": "tool-1",
				},
			},
		},
	}, program)

	if len(program.messages) != 3 {
		t.Fatalf("expected synthetic start + progress + remembered tool event, got %d messages", len(program.messages))
	}
	start, ok := program.messages[0].(uimsg.StreamStartMsg)
	if !ok || start.BranchRef == nil {
		t.Fatalf("expected nested synthetic start, got %#v", program.messages[0])
	}
	tool, ok := program.messages[2].(uimsg.ToolCallEventMsg)
	if !ok {
		t.Fatalf("expected remembered tool event, got %#v", program.messages[2])
	}
	if tool.BranchRef == nil {
		t.Fatal("expected remembered nested branch metadata on later tool event")
	}
	if tool.BranchRef.ParentCorrelationID != "corr-parent" || tool.BranchRef.ParentToolCallKey != "challenge-1" {
		t.Fatalf("tool.BranchRef = %+v, want remembered parent correlation/tool key", tool.BranchRef)
	}
	if tool.TopLevelTransfer {
		t.Fatal("did not expect top-level transfer marker on remembered nested event")
	}
}

func TestMergeStreamMetadata_TopLevelTransferClearsNestedBranch(t *testing.T) {
	merged := mergeStreamMetadata(
		map[string]any{
			"agent_type":                  "tester-pipeline",
			"chat_nested_branch":          true,
			"chat_parent_correlation_id":  "corr-nested-parent",
			"chat_parent_tool_call_key":   "challenge-1",
			"chat_inter_agent_thread_key": "pipeline:task_1-challenge-1",
			"chat_inter_agent_kind":       "challenge",
		},
		map[string]any{
			"chat_top_level_transfer":    true,
			"chat_parent_correlation_id": "corr-top-level-parent",
		},
	)

	if merged == nil {
		t.Fatal("expected merged metadata")
	}
	if got, _ := merged["chat_top_level_transfer"].(bool); !got {
		t.Fatalf("chat_top_level_transfer = %#v, want true", merged["chat_top_level_transfer"])
	}
	if got, _ := merged["chat_parent_correlation_id"].(string); got != "corr-top-level-parent" {
		t.Fatalf("chat_parent_correlation_id = %q, want corr-top-level-parent", got)
	}
	if _, exists := merged["chat_nested_branch"]; exists {
		t.Fatalf("chat_nested_branch = %#v, want absent", merged["chat_nested_branch"])
	}
	if _, exists := merged["chat_parent_tool_call_key"]; exists {
		t.Fatalf("chat_parent_tool_call_key = %#v, want absent", merged["chat_parent_tool_call_key"])
	}
	if _, exists := merged["chat_inter_agent_thread_key"]; exists {
		t.Fatalf("chat_inter_agent_thread_key = %#v, want absent", merged["chat_inter_agent_thread_key"])
	}
	if _, exists := merged["chat_inter_agent_kind"]; exists {
		t.Fatalf("chat_inter_agent_kind = %#v, want absent", merged["chat_inter_agent_kind"])
	}
}

func TestMergeStreamMetadata_NestedBranchClearsInheritedTopLevelTransfer(t *testing.T) {
	merged := mergeStreamMetadata(
		map[string]any{
			"agent_type":                 "inspector",
			"chat_top_level_transfer":    true,
			"chat_parent_correlation_id": "corr-top-level-parent",
		},
		map[string]any{
			"chat_nested_branch":          true,
			"chat_parent_correlation_id":  "corr-nested-parent",
			"chat_parent_tool_call_key":   "consult-1",
			"chat_inter_agent_thread_key": "thread-1",
			"chat_inter_agent_kind":       "consult",
		},
	)

	if merged == nil {
		t.Fatal("expected merged metadata")
	}
	if got, _ := merged["chat_nested_branch"].(bool); !got {
		t.Fatalf("chat_nested_branch = %#v, want true", merged["chat_nested_branch"])
	}
	if got, _ := merged["chat_parent_correlation_id"].(string); got != "corr-nested-parent" {
		t.Fatalf("chat_parent_correlation_id = %q, want corr-nested-parent", got)
	}
	if got, _ := merged["chat_parent_tool_call_key"].(string); got != "consult-1" {
		t.Fatalf("chat_parent_tool_call_key = %q, want consult-1", got)
	}
	if got, _ := merged["chat_inter_agent_thread_key"].(string); got != "thread-1" {
		t.Fatalf("chat_inter_agent_thread_key = %q, want thread-1", got)
	}
	if got, _ := merged["chat_inter_agent_kind"].(string); got != "consult" {
		t.Fatalf("chat_inter_agent_kind = %q, want consult", got)
	}
	if _, exists := merged["chat_top_level_transfer"]; exists {
		t.Fatalf("chat_top_level_transfer = %#v, want absent", merged["chat_top_level_transfer"])
	}
}

func TestParseInterAgentBranchRefFromMetadata_TopLevelTransferWins(t *testing.T) {
	ref := parseInterAgentBranchRefFromMetadata(map[string]any{
		"chat_nested_branch":         true,
		"chat_top_level_transfer":    true,
		"chat_parent_correlation_id": "corr-parent",
		"chat_parent_tool_call_key":  "challenge-1",
		"chat_inter_agent_kind":      "challenge",
	})
	if ref != nil {
		t.Fatalf("parseInterAgentBranchRefFromMetadata() = %+v, want nil for explicit top-level transfer", ref)
	}
}

func TestGuideBridgeDispatch_TopLevelTransferOverridesRememberedNestedMetadata(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatch(&guide.Message{
		CorrelationID: "corr-remembered-transition",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-remembered-transition",
			RespondingAgentID: "runtime-tester",
			Metadata: map[string]any{
				"agent_type":                 "tester-pipeline",
				"task_id":                    "task_1",
				"chat_nested_branch":         true,
				"chat_parent_correlation_id": "corr-nested-parent",
				"chat_parent_tool_call_key":  "challenge-1",
				"chat_inter_agent_kind":      "challenge",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Working the nested branch."},
			},
		},
	}, program)

	b.dispatch(&guide.Message{
		CorrelationID: "corr-remembered-transition",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-remembered-transition",
			RespondingAgentID: "runtime-tester",
			Metadata: map[string]any{
				"agent_type":                 "tester-pipeline",
				"task_id":                    "task_1",
				"chat_top_level_transfer":    true,
				"chat_parent_correlation_id": "corr-top-level-parent",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Returning to the top-level turn."},
			},
		},
	}, program)

	if len(program.messages) != 3 {
		t.Fatalf("expected synthetic start + first progress + second progress, got %d messages", len(program.messages))
	}
	progress, ok := program.messages[2].(uimsg.StreamProgressMsg)
	if !ok {
		t.Fatalf("expected final message to be StreamProgressMsg, got %#v", program.messages[2])
	}
	if progress.BranchRef != nil {
		t.Fatalf("progress.BranchRef = %+v, want nil after explicit top-level transfer", progress.BranchRef)
	}
	if !progress.TopLevelTransfer {
		t.Fatal("expected top-level transfer marker on remembered metadata transition")
	}
	if progress.ParentCorrelationID != "corr-top-level-parent" {
		t.Fatalf("progress.ParentCorrelationID = %q, want corr-top-level-parent", progress.ParentCorrelationID)
	}
}

func TestParseStreamMessages_PreserveRawKnowledgeReplicaIdentity(t *testing.T) {
	stream := &guide.StreamResponse{
		CorrelationID:     "corr-knowledge-replica",
		RespondingAgentID: "librarian#replica-corr-1",
		Metadata: map[string]any{
			"agent_name":       "Librarian",
			"agent_type":       "librarian",
			"runtime_agent_id": "librarian#replica-corr-1",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventProgress,
			Data: &guide.ProgressData{Message: "Searching history."},
		},
	}

	start := parseStreamStartMsg("session-1", "corr-knowledge-replica", stream)
	if start.AgentID != "librarian#replica-corr-1" {
		t.Fatalf("start.AgentID = %q, want librarian#replica-corr-1", start.AgentID)
	}
	if start.RuntimeAgentID != "librarian#replica-corr-1" {
		t.Fatalf("start.RuntimeAgentID = %q, want librarian#replica-corr-1", start.RuntimeAgentID)
	}

	progress := toStreamProgressMsg("session-1", "corr-knowledge-replica", stream)
	if progress.AgentID != "librarian#replica-corr-1" {
		t.Fatalf("progress.AgentID = %q, want librarian#replica-corr-1", progress.AgentID)
	}
	if progress.RuntimeAgentID != "librarian#replica-corr-1" {
		t.Fatalf("progress.RuntimeAgentID = %q, want librarian#replica-corr-1", progress.RuntimeAgentID)
	}

	complete := parseStreamCompleteMsg("session-1", "corr-knowledge-replica", stream)
	if complete.AgentID != "librarian#replica-corr-1" {
		t.Fatalf("complete.AgentID = %q, want librarian#replica-corr-1", complete.AgentID)
	}
	if complete.RuntimeAgentID != "librarian#replica-corr-1" {
		t.Fatalf("complete.RuntimeAgentID = %q, want librarian#replica-corr-1", complete.RuntimeAgentID)
	}
}

func TestParseStreamLifecyclePreservesSystemVisibility(t *testing.T) {
	stream := &guide.StreamResponse{
		CorrelationID:     "corr-system-store",
		RespondingAgentID: "archivalist",
		Metadata: map[string]any{
			"agent_name": "Archivalist",
			"agent_type": "archivalist",
		},
		Event: &guide.StreamEvent{
			Type:       guide.StreamEventStart,
			Visibility: events.VisibilitySystem,
		},
	}

	start := parseStreamStartMsg("session-1", "corr-system-store", stream)
	if start.Visibility != events.VisibilitySystem {
		t.Fatalf("start.Visibility = %v, want %v", start.Visibility, events.VisibilitySystem)
	}

	stream.Event = &guide.StreamEvent{
		Type:       guide.StreamEventComplete,
		Visibility: events.VisibilitySystem,
	}
	complete := parseStreamCompleteMsg("session-1", "corr-system-store", stream)
	if complete.Visibility != events.VisibilitySystem {
		t.Fatalf("complete.Visibility = %v, want %v", complete.Visibility, events.VisibilitySystem)
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

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + complete, got %d messages", len(program.messages))
	}
	if _, ok := program.messages[0].(uimsg.StreamStartMsg); !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	complete, ok := program.messages[1].(uimsg.StreamCompleteMsg)
	if !ok {
		t.Fatalf("expected second message to be StreamCompleteMsg, got %T", program.messages[1])
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

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + complete, got %d messages", len(program.messages))
	}
	if _, ok := program.messages[0].(uimsg.StreamStartMsg); !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	complete, ok := program.messages[1].(uimsg.StreamCompleteMsg)
	if !ok {
		t.Fatalf("expected second message to be StreamCompleteMsg, got %T", program.messages[1])
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

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + chunk, got %d messages", len(program.messages))
	}
	if _, ok := program.messages[0].(uimsg.StreamStartMsg); !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	chunk, ok := program.messages[1].(uimsg.StreamChunkMsg)
	if !ok {
		t.Fatalf("expected second message to be StreamChunkMsg, got %T", program.messages[1])
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

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + progress, got %d messages", len(program.messages))
	}
	start, ok := program.messages[0].(uimsg.StreamStartMsg)
	if !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	if start.CorrelationID != "corr-progress" {
		t.Fatalf("unexpected start correlation id: %q", start.CorrelationID)
	}
	progress, ok := program.messages[1].(uimsg.StreamProgressMsg)
	if !ok {
		t.Fatalf("expected second message to be StreamProgressMsg, got %T", program.messages[1])
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

func TestGuideBridgeDispatchStream_RetryDoesNotSynthesizeStart(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-retry",
		RespondingAgentID: "orchestrator",
		Event: &guide.StreamEvent{
			Type: guide.StreamEventRetry,
			Data: guide.RetryStatus{
				Attempt:     1,
				MaxAttempts: 3,
				Delay:       2 * time.Second,
				Err:         errors.New("read: connection reset by peer"),
			},
		},
	}, program)

	if len(program.messages) != 1 {
		t.Fatalf("expected retry status only, got %d messages", len(program.messages))
	}
	retry, ok := program.messages[0].(uimsg.RetryStatusMsg)
	if !ok {
		t.Fatalf("expected RetryStatusMsg, got %T", program.messages[0])
	}
	if retry.CorrelationID != "corr-retry" {
		t.Fatalf("unexpected retry correlation id: %q", retry.CorrelationID)
	}
	if retry.Attempt != 1 || retry.MaxAttempts != 3 {
		t.Fatalf("unexpected retry status: %+v", retry)
	}
	if retry.Error == "" || !strings.Contains(strings.ToLower(retry.Error), "connection reset by peer") {
		t.Fatalf("unexpected retry error text: %q", retry.Error)
	}
}

func TestGuideBridgeDispatchStream_ToolCallSynthesizesStartBeforeToolEvent(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-tool",
		RespondingAgentID: "inspector-pipeline",
		Metadata: map[string]any{
			"agent_type": "inspector-pipeline",
			"task_id":    "task_1",
			"task_slug":  "create-pyproject",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventToolCall,
			Data: map[string]any{
				"tool_name":     "process_validation",
				"tool_call_key": "process-validation-1",
				"phase":         0,
			},
		},
	}, program)

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + tool event, got %d messages", len(program.messages))
	}
	start, ok := program.messages[0].(uimsg.StreamStartMsg)
	if !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	if start.CorrelationID != "corr-tool" || start.TaskID != "task_1" {
		t.Fatalf("unexpected synthetic start: %+v", start)
	}
	tool, ok := program.messages[1].(uimsg.ToolCallEventMsg)
	if !ok {
		t.Fatalf("expected second message to be ToolCallEventMsg, got %T", program.messages[1])
	}
	if tool.ToolName != "process_validation" || tool.ToolCallKey != "process-validation-1" {
		t.Fatalf("unexpected tool event: %+v", tool)
	}
}

func TestGuideBridgeDispatchStream_SuppressesDuplicateRealStartAfterSyntheticStart(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	progressStream := &guide.StreamResponse{
		CorrelationID:     "corr-dup-start",
		RespondingAgentID: "inspector-pipeline",
		Metadata: map[string]any{
			"agent_type": "inspector-pipeline",
			"task_id":    "task_1",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventProgress,
			Data: map[string]any{
				"message": "Processing returned challenge evidence.",
			},
		},
	}
	b.dispatchStream(progressStream, program)

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-dup-start",
		RespondingAgentID: "inspector-pipeline",
		Metadata: map[string]any{
			"agent_type": "inspector-pipeline",
			"task_id":    "task_1",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventStart,
		},
	}, program)

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + progress only, got %d messages", len(program.messages))
	}
	if _, ok := program.messages[0].(uimsg.StreamStartMsg); !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	if _, ok := program.messages[1].(uimsg.StreamProgressMsg); !ok {
		t.Fatalf("expected second message to be StreamProgressMsg, got %T", program.messages[1])
	}
}

func TestGuideBridgeDispatchStream_DropsLateProgressAfterComplete(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	complete := &guide.StreamResponse{
		CorrelationID:     "corr-complete-then-late-progress",
		RespondingAgentID: "inspector",
		Metadata: map[string]any{
			"agent_type": "inspector",
			"task_id":    "task_1",
		},
		Event: &guide.StreamEvent{Type: guide.StreamEventComplete},
	}
	b.dispatchStream(complete, program)

	if len(program.messages) != 2 {
		t.Fatalf("expected synthetic start + complete, got %d messages", len(program.messages))
	}
	program.messages = nil

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-complete-then-late-progress",
		RespondingAgentID: "inspector",
		Metadata: map[string]any{
			"agent_type": "inspector",
			"task_id":    "task_1",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventProgress,
			Data: &guide.ProgressData{Message: "Working through this with challenge agent."},
		},
	}, program)

	if len(program.messages) != 0 {
		t.Fatalf("expected stale late progress to be dropped, got %d messages", len(program.messages))
	}
}

func TestGuideBridgeDispatchStream_ExplicitStartReopensCompletedPhase(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-reopen-completed-phase",
		RespondingAgentID: "inspector",
		Metadata: map[string]any{
			"agent_type": "inspector",
			"task_id":    "task_1",
		},
		Event: &guide.StreamEvent{Type: guide.StreamEventComplete},
	}, program)
	program.messages = nil

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-reopen-completed-phase",
		RespondingAgentID: "inspector",
		Metadata: map[string]any{
			"agent_type": "inspector",
			"task_id":    "task_1",
		},
		Event: &guide.StreamEvent{Type: guide.StreamEventStart},
	}, program)
	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-reopen-completed-phase",
		RespondingAgentID: "inspector",
		Metadata: map[string]any{
			"agent_type": "inspector",
			"task_id":    "task_1",
		},
		Event: &guide.StreamEvent{
			Type: guide.StreamEventProgress,
			Data: &guide.ProgressData{Message: "Resumed inspector work."},
		},
	}, program)

	if len(program.messages) != 2 {
		t.Fatalf("expected explicit restart + progress, got %d messages", len(program.messages))
	}
	if _, ok := program.messages[0].(uimsg.StreamStartMsg); !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	progress, ok := program.messages[1].(uimsg.StreamProgressMsg)
	if !ok {
		t.Fatalf("expected second message to be StreamProgressMsg, got %T", program.messages[1])
	}
	if progress.Message != "Resumed inspector work." {
		t.Fatalf("progress message = %q, want resumed inspector work", progress.Message)
	}
}

func TestGuideBridgeDispatchStream_AllowsResponderTransitionOnSameCorrelation(t *testing.T) {
	b := NewGuideBridge(nil, nil, "session-1")
	program := &recordingProgram{}

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-same-correlation-transfer",
		RespondingAgentID: "guide",
		Metadata: map[string]any{
			"agent_type": "guide",
			"agent_name": "Guide",
		},
		Event: &guide.StreamEvent{Type: guide.StreamEventStart},
	}, program)

	b.dispatchStream(&guide.StreamResponse{
		CorrelationID:     "corr-same-correlation-transfer",
		RespondingAgentID: "architect",
		Metadata: map[string]any{
			"agent_type": "architect",
			"agent_name": "Architect",
		},
		Event: &guide.StreamEvent{Type: guide.StreamEventStart},
	}, program)

	if len(program.messages) != 2 {
		t.Fatalf("expected guide start + architect start, got %d messages", len(program.messages))
	}
	first, ok := program.messages[0].(uimsg.StreamStartMsg)
	if !ok {
		t.Fatalf("expected first message to be StreamStartMsg, got %T", program.messages[0])
	}
	second, ok := program.messages[1].(uimsg.StreamStartMsg)
	if !ok {
		t.Fatalf("expected second message to be StreamStartMsg, got %T", program.messages[1])
	}
	if first.AgentID != "guide" {
		t.Fatalf("first.AgentID = %q, want guide", first.AgentID)
	}
	if second.AgentID != "architect" {
		t.Fatalf("second.AgentID = %q, want architect", second.AgentID)
	}
	if second.AgentType != "architect" {
		t.Fatalf("second.AgentType = %q, want architect", second.AgentType)
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

func TestRouteResponseContent_SuppressesEmptyResponseTextPayload(t *testing.T) {
	resp := &guide.RouteResponse{
		RespondingAgentID: "inspector",
		Data:              emptyResponseTextPayload{},
	}
	if got := routeResponseContent(resp); got != "" {
		t.Fatalf("routeResponseContent() = %q, want empty string", got)
	}
}

func TestRouteResponseContent_SuppressesStringifiedWrappedEmptyConversationResult(t *testing.T) {
	resp := &guide.RouteResponse{
		RespondingAgentID: "tester",
		Data: `{
			"result": {
				"response": "",
				"intent": "check"
			},
			"action": {
				"Type": "challenge",
				"AgentType": "inspector",
				"TargetAgent": "architect"
			}
		}`,
	}
	if got := routeResponseContent(resp); got != "" {
		t.Fatalf("routeResponseContent() = %q, want empty string", got)
	}
}

func TestStreamCompleteContent_SuppressesStringifiedWrappedEmptyConversationResult(t *testing.T) {
	payload := `{
		"result": {
			"response": "",
			"intent": "check"
		},
		"action": {
			"Type": "challenge",
			"AgentType": "inspector",
			"TargetAgent": "architect"
		}
	}`
	if got := streamCompleteContent("tester", payload); got != "" {
		t.Fatalf("streamCompleteContent() = %q, want empty string", got)
	}
}

func TestRouteResponseContent_RendersInnerStringifiedWrappedConversationResult(t *testing.T) {
	resp := &guide.RouteResponse{
		RespondingAgentID: "tester",
		Data: `{
			"result": {
				"response": "Checkpoint is acceptable as-is.",
				"intent": "check"
			},
			"action": {
				"Type": "challenge",
				"AgentType": "inspector",
				"TargetAgent": "architect"
			}
		}`,
	}
	if got := routeResponseContent(resp); got != "Checkpoint is acceptable as-is." {
		t.Fatalf("routeResponseContent() = %q, want inner response text", got)
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
