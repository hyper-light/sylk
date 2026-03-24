package shared

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
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
