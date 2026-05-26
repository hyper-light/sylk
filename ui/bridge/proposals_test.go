package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/google/uuid"
)

type proposalProgram struct {
	ch chan any
}

func (p *proposalProgram) Send(m any) {
	p.ch <- m
}

func TestProposalBridge_ForwardsPlanAndCommandProposals(t *testing.T) {
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 16})
	t.Cleanup(func() { _ = bus.Close() })
	scope := concurrency.NewGoroutineScope(context.Background(), "proposal-bridge-test", nil)
	t.Cleanup(func() { _ = scope.Shutdown(100*time.Millisecond, time.Second) })

	bridge := NewProposalBridge("test.proposals", bus, scope)
	program := &proposalProgram{ch: make(chan any, 4)}
	if err := bridge.Start(program); err != nil {
		t.Fatalf("start proposal bridge: %v", err)
	}
	t.Cleanup(bridge.Stop)

	planProposal := &planapproval.Proposal{
		PlanID:        "plan-1",
		CorrelationID: "corr-plan",
		PlanText:      "Create a CLI.",
		Timestamp:     time.Now(),
	}
	commandProposal := &commandapproval.Proposal{
		CorrelationID: "corr-command",
		TargetAgentID: "guardian",
		Command:       "go test ./...",
		Timestamp:     time.Now(),
	}

	publishProposal(t, bus, planProposal.CorrelationID, planProposal)
	publishProposal(t, bus, commandProposal.CorrelationID, commandProposal)

	seenPlan := false
	seenCommand := false
	deadline := time.After(time.Second)
	for !seenPlan || !seenCommand {
		select {
		case raw := <-program.ch:
			switch typed := raw.(type) {
			case msg.PlanApprovalRequestMsg:
				if typed.Proposal == nil || typed.Proposal.PlanID != planProposal.PlanID {
					t.Fatalf("unexpected plan proposal: %+v", typed.Proposal)
				}
				if typed.Proposal.Metadata["target_agent_id"] != "guardian-session" {
					t.Fatalf("target_agent_id metadata = %#v, want guardian-session", typed.Proposal.Metadata["target_agent_id"])
				}
				seenPlan = true
			case msg.CommandApprovalRequestMsg:
				if typed.Proposal == nil || typed.Proposal.Command != commandProposal.Command {
					t.Fatalf("unexpected command proposal: %+v", typed.Proposal)
				}
				seenCommand = true
			default:
				t.Fatalf("unexpected bridge message type %T", raw)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for proposal UI messages; seenPlan=%t seenCommand=%t", seenPlan, seenCommand)
		}
	}
}

func TestProposalBridge_DecodesMapPayload(t *testing.T) {
	raw := map[string]any{
		"plan_id":        "plan-map",
		"correlation_id": "corr-map",
		"plan_text":      "Map-backed plan.",
	}
	out, ok := proposalUIMessage(raw)
	if !ok {
		t.Fatal("expected map payload to decode")
	}
	typed, ok := out.(msg.PlanApprovalRequestMsg)
	if !ok {
		t.Fatalf("expected plan approval request, got %T", out)
	}
	if typed.Proposal == nil || typed.Proposal.PlanID != "plan-map" {
		t.Fatalf("decoded proposal mismatch: %+v", typed.Proposal)
	}
}

func publishProposal(t *testing.T, bus guide.EventBus, correlationID string, payload any) {
	t.Helper()
	if err := bus.Publish(guide.TopicResponses("tui", "tui"), &guide.Message{
		ID:            uuid.NewString(),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeProposal,
		SourceAgentID: "guardian-session",
		Payload:       payload,
		Timestamp:     time.Now(),
	}); err != nil {
		t.Fatalf("publish proposal: %v", err)
	}
}
