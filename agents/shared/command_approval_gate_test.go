package shared

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/providers"
)

func TestGuardianCommandGateAuthorizeIgnoresStreamUntilTerminalResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "agent-1" },
		SourceAgentType:        "engineer",
		ApprovalRequestTimeout: 2 * time.Second,
	})

	resultCh := make(chan commandapproval.Evaluation, 1)
	errCh := make(chan error, 1)
	go func() {
		eval, authErr := gate.Authorize(context.Background(), commandapproval.Request{
			Command:  "python hello.py",
			ToolName: "run_command",
		})
		if authErr != nil {
			errCh <- authErr
			return
		}
		resultCh <- eval
	}()

	var routeReq *guide.RouteRequest
	select {
	case routeReq = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	responseTopic := guide.TopicResponses("engineer", "agent-1")
	streamMsg := &guide.Message{
		ID:            "stream-msg",
		CorrelationID: routeReq.CorrelationID,
		Type:          guide.MessageTypeStream,
		SourceAgentID: "guardian",
		Payload: &guide.StreamResponse{
			CorrelationID:     routeReq.CorrelationID,
			RespondingAgentID: "guardian",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventComplete,
			},
		},
		Timestamp: time.Now(),
	}
	if err := bus.Publish(responseTopic, streamMsg); err != nil {
		t.Fatalf("publish stream response: %v", err)
	}

	resp := &guide.RouteResponse{
		CorrelationID:       routeReq.CorrelationID,
		Success:             true,
		RespondingAgentID:   "guardian",
		RespondingAgentName: "guardian",
		Data: commandapproval.Evaluation{
			Decision: commandapproval.DecisionAllow,
			Reason:   "approved by user",
		},
	}
	if err := bus.Publish(responseTopic, guide.NewResponseMessage("resp-msg", resp)); err != nil {
		t.Fatalf("publish terminal response: %v", err)
	}

	select {
	case authErr := <-errCh:
		t.Fatalf("Authorize returned error: %v", authErr)
	case eval := <-resultCh:
		if eval.Decision != commandapproval.DecisionAllow {
			t.Fatalf("decision = %s, want %s", eval.Decision, commandapproval.DecisionAllow)
		}
		if eval.Reason != "approved by user" {
			t.Fatalf("reason = %q, want %q", eval.Reason, "approved by user")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval result")
	}
}

func TestGuardianCommandGateAuthorize_SetsReplyToTopic(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.Message, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if msg == nil {
			return nil
		}
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- msg:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "academic" },
		SourceAgentType:        "academic",
		ApprovalRequestTimeout: 2 * time.Second,
	})

	errCh := make(chan error, 1)
	go func() {
		_, authErr := gate.Authorize(context.Background(), commandapproval.Request{
			Command:  "https://web.dev/articles/lcp",
			ToolName: "web_fetch",
			Domain:   "web.dev",
		})
		errCh <- authErr
	}()

	var requestMsg *guide.Message
	select {
	case requestMsg = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	if requestMsg.ReplyTo == nil {
		t.Fatal("expected reply-to on approval route request")
	}
	wantTopic := guide.TopicResponses("academic", "academic")
	if requestMsg.ReplyTo.Topic != wantTopic {
		t.Fatalf("reply-to topic = %q, want %q", requestMsg.ReplyTo.Topic, wantTopic)
	}

	req, ok := requestMsg.GetRouteRequest()
	if !ok || req == nil {
		t.Fatalf("expected route request payload, got %#v", requestMsg.Payload)
	}
	resp := &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             true,
		RespondingAgentID:   "guardian",
		RespondingAgentName: "guardian",
		Data: commandapproval.Evaluation{
			Decision: commandapproval.DecisionAllow,
			Reason:   "approved by user",
		},
	}
	if err := bus.Publish(requestMsg.ReplyTo.Topic, guide.NewResponseMessage("resp-msg", resp)); err != nil {
		t.Fatalf("publish approval response: %v", err)
	}

	select {
	case authErr := <-errCh:
		if authErr != nil {
			t.Fatalf("Authorize returned error: %v", authErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval result")
	}
}

func TestGuardianCommandGateAuthorize_RefreshesWaitLeaseWithoutRepublish(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.RouteRequest, 2)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "agent-1" },
		SourceAgentType:        "engineer",
		ApprovalRequestTimeout: 20 * time.Millisecond,
	})

	resultCh := make(chan commandapproval.Evaluation, 1)
	errCh := make(chan error, 1)
	go func() {
		eval, authErr := gate.Authorize(context.Background(), commandapproval.Request{
			Command:  "python hello.py",
			ToolName: "run_command",
		})
		if authErr != nil {
			errCh <- authErr
			return
		}
		resultCh <- eval
	}()

	var routeReq *guide.RouteRequest
	select {
	case routeReq = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	go func(correlationID string) {
		time.Sleep(25 * time.Millisecond)
		resp := &guide.RouteResponse{
			CorrelationID:       correlationID,
			Success:             true,
			RespondingAgentID:   "guardian",
			RespondingAgentName: "guardian",
			Data: commandapproval.Evaluation{
				Decision: commandapproval.DecisionAllow,
				Reason:   "approved after renewed wait",
			},
		}
		_ = bus.Publish(guide.TopicResponses("engineer", "agent-1"), guide.NewResponseMessage("resp-msg", resp))
	}(routeReq.CorrelationID)

	select {
	case authErr := <-errCh:
		t.Fatalf("Authorize returned error: %v", authErr)
	case eval := <-resultCh:
		if eval.Decision != commandapproval.DecisionAllow {
			t.Fatalf("decision = %s, want %s", eval.Decision, commandapproval.DecisionAllow)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval result")
	}

	select {
	case req := <-requestCh:
		t.Fatalf("unexpected republished approval request: %#v", req)
	default:
	}
}

func TestGuardianCommandGateAuthorize_WrapsDeniedResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "agent-1" },
		SourceAgentType:        "engineer",
		ApprovalRequestTimeout: 2 * time.Second,
	})

	errCh := make(chan error, 1)
	go func() {
		_, authErr := gate.Authorize(context.Background(), commandapproval.Request{
			Command:  "python -m pytest",
			ToolName: "run_command",
		})
		errCh <- authErr
	}()

	var routeReq *guide.RouteRequest
	select {
	case routeReq = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	resp := &guide.RouteResponse{
		CorrelationID:       routeReq.CorrelationID,
		Success:             true,
		RespondingAgentID:   "guardian",
		RespondingAgentName: "guardian",
		Data: commandapproval.Evaluation{
			Decision: commandapproval.DecisionDeny,
			Reason:   "user denied this command",
		},
	}
	if err := bus.Publish(guide.TopicResponses("engineer", "agent-1"), guide.NewResponseMessage("resp-msg", resp)); err != nil {
		t.Fatalf("publish deny response: %v", err)
	}

	select {
	case authErr := <-errCh:
		if !errors.Is(authErr, commandapproval.ErrApprovalDenied) {
			t.Fatalf("expected ErrApprovalDenied, got %v", authErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for denied approval result")
	}
}

func TestGuardianCommandGateAuthorize_ResearchContinuationAllowsSynthesizeAsIsDecision(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "academic" },
		SourceAgentType:        "academic",
		ApprovalRequestTimeout: 2 * time.Second,
	})

	resultCh := make(chan commandapproval.Evaluation, 1)
	errCh := make(chan error, 1)
	go func() {
		eval, authErr := gate.Authorize(context.Background(), commandapproval.Request{
			Kind:     commandapproval.ApprovalKindResearchContinuation,
			Command:  "Topic: OAuth token validation",
			ToolName: "author_research_paper",
		})
		if authErr != nil {
			errCh <- authErr
			return
		}
		resultCh <- eval
	}()

	var routeReq *guide.RouteRequest
	select {
	case routeReq = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	resp := &guide.RouteResponse{
		CorrelationID:       routeReq.CorrelationID,
		Success:             true,
		RespondingAgentID:   "guardian",
		RespondingAgentName: "guardian",
		Data: commandapproval.Evaluation{
			Decision:     commandapproval.DecisionDeny,
			UserDecision: "synthesize_as_is",
			Reason:       "user chose to synthesize current findings",
		},
	}
	if err := bus.Publish(guide.TopicResponses("academic", "academic"), guide.NewResponseMessage("resp-msg", resp)); err != nil {
		t.Fatalf("publish response: %v", err)
	}

	select {
	case authErr := <-errCh:
		t.Fatalf("Authorize returned error: %v", authErr)
	case eval := <-resultCh:
		if eval.Decision != commandapproval.DecisionDeny {
			t.Fatalf("decision = %s, want %s", eval.Decision, commandapproval.DecisionDeny)
		}
		if eval.UserDecision != "synthesize_as_is" {
			t.Fatalf("user decision = %q, want synthesize_as_is", eval.UserDecision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for research continuation result")
	}
}

func TestGuardianCommandGateAuthorize_SynthesizesApprovalBranchMetadataFromStreamContext(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "agent-1" },
		SourceAgentType:        "engineer",
		ApprovalRequestTimeout: 2 * time.Second,
	})

	errCh := make(chan error, 1)
	go func() {
		_, authErr := gate.Authorize(WithStreamContext(context.Background(), "corr-parent", "tui"), commandapproval.Request{
			Command:  "python -m pytest",
			ToolName: "run_command",
		})
		errCh <- authErr
	}()

	var routeReq *guide.RouteRequest
	select {
	case routeReq = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	if routeReq.ParentCorrelationID != "corr-parent" {
		t.Fatalf("parent_correlation_id = %q, want %q", routeReq.ParentCorrelationID, "corr-parent")
	}
	if nested, _ := routeReq.Metadata[streamMetadataNestedBranch].(bool); !nested {
		t.Fatalf("chat_nested_branch = %#v, want true", routeReq.Metadata[streamMetadataNestedBranch])
	}
	if got, _ := routeReq.Metadata[streamMetadataInterAgentKind].(string); got != InterAgentToolEventKindApproval {
		t.Fatalf("chat_inter_agent_kind = %q, want %q", got, InterAgentToolEventKindApproval)
	}
	if got, _ := routeReq.Metadata[streamMetadataParentToolCallKey].(string); got == "" {
		t.Fatal("expected synthesized approval parent tool call key")
	}

	resp := &guide.RouteResponse{
		CorrelationID:       routeReq.CorrelationID,
		Success:             true,
		RespondingAgentID:   "guardian",
		RespondingAgentName: "guardian",
		Data: commandapproval.Evaluation{
			Decision: commandapproval.DecisionAllow,
			Reason:   "approved by user",
		},
	}
	if err := bus.Publish(guide.TopicResponses("engineer", "agent-1"), guide.NewResponseMessage("resp-msg", resp)); err != nil {
		t.Fatalf("publish approval response: %v", err)
	}

	select {
	case authErr := <-errCh:
		if authErr != nil {
			t.Fatalf("Authorize returned error: %v", authErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval result")
	}
}

func TestGuardianCommandGateAuthorize_SynthesizesApprovalBranchFromActiveToolCall(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "academic" },
		SourceAgentType:        "academic",
		ApprovalRequestTimeout: 2 * time.Second,
	})

	ctx := WithStreamContext(context.Background(), "corr-parent-active", "tui")
	ctx = WithActiveToolCall(ctx, providers.ToolCall{
		ID:        "fetch-1",
		Name:      "fetch_document",
		Arguments: `{"url":"https://example.com/spec","reason":"verify the official API reference"}`,
	})

	errCh := make(chan error, 1)
	go func() {
		_, authErr := gate.Authorize(ctx, commandapproval.Request{
			Command:  "https://example.com/spec",
			ToolName: "fetch_document",
			Domain:   "example.com",
		})
		errCh <- authErr
	}()

	var routeReq *guide.RouteRequest
	select {
	case routeReq = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	if routeReq.ParentCorrelationID != "corr-parent-active" {
		t.Fatalf("parent_correlation_id = %q, want %q", routeReq.ParentCorrelationID, "corr-parent-active")
	}
	if got, _ := routeReq.Metadata[streamMetadataInterAgentKind].(string); got != InterAgentToolEventKindApproval {
		t.Fatalf("chat_inter_agent_kind = %q, want %q", got, InterAgentToolEventKindApproval)
	}
	if got, _ := routeReq.Metadata[streamMetadataParentToolCallKey].(string); got == "" {
		t.Fatal("expected synthesized approval parent tool call key")
	}

	resp := &guide.RouteResponse{
		CorrelationID:       routeReq.CorrelationID,
		Success:             true,
		RespondingAgentID:   "guardian",
		RespondingAgentName: "guardian",
		Data: commandapproval.Evaluation{
			Decision: commandapproval.DecisionAllow,
			Reason:   "approved by user",
		},
	}
	if err := bus.Publish(guide.TopicResponses("academic", "academic"), guide.NewResponseMessage("resp-msg", resp)); err != nil {
		t.Fatalf("publish approval response: %v", err)
	}

	select {
	case authErr := <-errCh:
		if authErr != nil {
			t.Fatalf("Authorize returned error: %v", authErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval result")
	}
}

func TestGuardianCommandGateAuthorize_PublishesResolvedProgressAfterApproval(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	progressCh := make(chan string, 8)
	sourceChannels := guide.NewAgentChannels("academic", "academic")
	progressSub, err := bus.SubscribeAsync(sourceChannels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventProgress {
			return nil
		}
		progress, _ := stream.Event.Data.(*guide.ProgressData)
		if progress == nil {
			return nil
		}
		select {
		case progressCh <- progress.Message:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe source responses: %v", err)
	}
	defer progressSub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "academic" },
		SourceAgentType:        "academic",
		ApprovalRequestTimeout: 2 * time.Second,
	})

	ctx := WithStreamContext(context.Background(), "corr-parent", "tui")
	ctx = WithProgressPublisher(ctx, &ProgressPublisher{
		Bus:           bus,
		Channels:      sourceChannels,
		AgentID:       "academic",
		CorrelationID: "corr-parent",
		SourceAgentID: "tui",
	})

	errCh := make(chan error, 1)
	go func() {
		_, authErr := gate.Authorize(ctx, commandapproval.Request{
			Command:  "https://raw.githubusercontent.com/withastro/docs/main/src/content/docs/en/concepts/why-astro/index.mdx",
			ToolName: "fetch_document",
			Domain:   "raw.githubusercontent.com",
		})
		errCh <- authErr
	}()

	var routeReq *guide.RouteRequest
	select {
	case routeReq = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	waitingSeen := false
	for !waitingSeen {
		select {
		case progress := <-progressCh:
			if progress == "Waiting for Guardian approval for raw.githubusercontent.com" {
				waitingSeen = true
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for approval keepalive progress")
		}
	}

	resp := &guide.RouteResponse{
		CorrelationID:       routeReq.CorrelationID,
		Success:             true,
		RespondingAgentID:   "guardian",
		RespondingAgentName: "guardian",
		Data: commandapproval.Evaluation{
			Decision: commandapproval.DecisionAllow,
			Reason:   "approved by user",
		},
	}
	if err := bus.Publish(sourceChannels.Responses, guide.NewResponseMessage("resp-msg", resp)); err != nil {
		t.Fatalf("publish approval response: %v", err)
	}

	resolvedSeen := false
	resultReady := false
	for !resolvedSeen {
		select {
		case progress := <-progressCh:
			if progress == "Guardian approval received for raw.githubusercontent.com" {
				resolvedSeen = true
			}
		case authErr := <-errCh:
			if authErr != nil {
				t.Fatalf("Authorize returned error before resolved progress: %v", authErr)
			}
			resultReady = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for approval resolved progress")
		}
	}

	if !resultReady {
		select {
		case authErr := <-errCh:
			if authErr != nil {
				t.Fatalf("Authorize returned error: %v", authErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for approval result")
		}
	}
}

func TestGuardianCommandGateAuthorize_CreatesImmediateApprovalBranchWhenAlreadyNested(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	requestCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requestCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	gate := NewGuardianCommandGate(GuardianCommandGateConfig{
		BusProvider:            func() guide.EventBus { return bus },
		SourceAgentID:          func() string { return "academic" },
		SourceAgentType:        "academic",
		ApprovalRequestTimeout: 2 * time.Second,
	})

	ctx := WithStreamContext(context.Background(), "corr-child-academic", "architect")
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		streamMetadataNestedBranch:      true,
		streamMetadataParentCorrelation: "corr-parent-architect",
		streamMetadataParentToolCallKey: "consult-1",
		streamMetadataInterAgentKind:    InterAgentToolEventKindConsult,
	})
	ctx = WithToolCallEmitter(ctx, func(ToolCallEvent) {})

	errCh := make(chan error, 1)
	go func() {
		_, authErr := gate.Authorize(ctx, commandapproval.Request{
			Command:  "https://example.com/spec",
			ToolName: "web_fetch",
			Domain:   "example.com",
		})
		errCh <- authErr
	}()

	var routeReq *guide.RouteRequest
	select {
	case routeReq = <-requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	if routeReq.Metadata == nil {
		t.Fatal("expected approval route request metadata")
	}
	if got, _ := routeReq.Metadata[streamMetadataParentCorrelation].(string); got != "corr-child-academic" {
		t.Fatalf("parent correlation = %q, want %q", got, "corr-child-academic")
	}
	if got, _ := routeReq.Metadata[streamMetadataInterAgentKind].(string); got != InterAgentToolEventKindApproval {
		t.Fatalf("branch kind = %q, want %q", got, InterAgentToolEventKindApproval)
	}
	toolCallKey, _ := routeReq.Metadata[streamMetadataParentToolCallKey].(string)
	if strings.TrimSpace(toolCallKey) == "" {
		t.Fatal("expected immediate approval branch tool_call_key")
	}
	if toolCallKey == "consult-1" {
		t.Fatalf("approval branch should not reuse ancestor consult tool call key %q", toolCallKey)
	}

	resp := &guide.RouteResponse{
		CorrelationID:       routeReq.CorrelationID,
		Success:             true,
		RespondingAgentID:   "guardian",
		RespondingAgentName: "guardian",
		Data: commandapproval.Evaluation{
			Decision: commandapproval.DecisionAllow,
			Reason:   "approved by user",
		},
	}
	if err := bus.Publish(guide.TopicResponses("academic", "academic"), guide.NewResponseMessage("resp-msg", resp)); err != nil {
		t.Fatalf("publish approval response: %v", err)
	}

	select {
	case authErr := <-errCh:
		if authErr != nil {
			t.Fatalf("Authorize returned error: %v", authErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval result")
	}
}
