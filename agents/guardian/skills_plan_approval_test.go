package guardian

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/planapproval"
)

// TestPlanApprovalGate_PublishesProposalToTUI pins the gate's
// publication contract: plan_approval_gate publishes a
// planapproval.Proposal as a MessageTypeProposal on the TUI's
// response topic, with PlanID + CorrelationID + PlanText preserved.
// This is what triggers the UI dialog substitution; if the gate
// stops publishing, the dialog never appears and the architect's
// continuation hangs.
func TestPlanApprovalGate_PublishesProposalToTUI(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	g := &Guardian{
		id:               "guardian-test",
		bus:              bus,
		pendingApprovals: make(map[string]chan ApprovalResult),
	}

	tuiTopic := guide.TopicResponses("tui", "tui")
	received := make(chan *guide.Message, 1)
	sub, err := bus.Subscribe(tuiTopic, func(msg *guide.Message) error {
		select {
		case received <- msg:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe TUI topic: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := &planApprovalRequest{
		PlanID:    "plan-test-123",
		SessionID: "ses-test",
		PlanName:  "Build hello CLI",
		PlanText:  "1. Project config\n2. Package init\n3. CLI logic",
	}

	resultCh := make(chan struct{}, 1)
	go func() {
		// Run the gate in a goroutine; we're testing the publish
		// happens, not the verdict round-trip (covered separately).
		_, _ = g.requestPlanApproval(ctx, req)
		resultCh <- struct{}{}
	}()

	select {
	case msg := <-received:
		if msg == nil || msg.Type != guide.MessageTypeProposal {
			t.Fatalf("expected MessageTypeProposal, got %+v", msg)
		}
		proposal, ok := msg.Payload.(*planapproval.Proposal)
		if !ok {
			t.Fatalf("expected *planapproval.Proposal payload, got %T", msg.Payload)
		}
		if proposal.PlanID != req.PlanID {
			t.Fatalf("proposal.PlanID = %q, want %q", proposal.PlanID, req.PlanID)
		}
		if proposal.PlanText != req.PlanText {
			t.Fatalf("proposal.PlanText mismatch")
		}
		if proposal.CorrelationID == "" {
			t.Fatal("proposal.CorrelationID must be set so the dialog response can route back")
		}
	case <-time.After(time.Second):
		t.Fatal("plan approval proposal was not published to TUI within 1s")
	}
}

// TestPlanApprovalGate_RoundTripVerdict pins the verdict round-trip:
// when a Decision response arrives on Guardian's response topic with
// the matching CorrelationID, the gate decodes the verdict and
// returns it to the architect's caller. Covers all three verdict
// values to catch any regressions in the decision-decoding path.
func TestPlanApprovalGate_RoundTripVerdict(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision string
		want     planapproval.Verdict
	}{
		{"approve", "approve", planapproval.VerdictApprove},
		{"modify", "modify", planapproval.VerdictModify},
		{"reject", "reject", planapproval.VerdictReject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
			t.Cleanup(func() { _ = bus.Close() })

			g := &Guardian{
				id:               "guardian-test",
				bus:              bus,
				pendingApprovals: make(map[string]chan ApprovalResult),
			}

			tuiTopic := guide.TopicResponses("tui", "tui")
			seen := make(chan *guide.Message, 1)
			sub, err := bus.Subscribe(tuiTopic, func(msg *guide.Message) error {
				select {
				case seen <- msg:
				default:
				}
				return nil
			})
			if err != nil {
				t.Fatalf("subscribe TUI topic: %v", err)
			}
			t.Cleanup(func() { _ = sub.Unsubscribe() })

			req := &planApprovalRequest{
				PlanID:   "plan-rt-1",
				PlanText: "test plan",
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			resultCh := make(chan *planApprovalResult, 1)
			errCh := make(chan error, 1)
			go func() {
				res, err := g.requestPlanApproval(ctx, req)
				if err != nil {
					errCh <- err
					return
				}
				resultCh <- res
			}()

			var proposalMsg *guide.Message
			select {
			case proposalMsg = <-seen:
			case <-time.After(time.Second):
				t.Fatal("proposal was not published")
			}
			proposal := proposalMsg.Payload.(*planapproval.Proposal)

			// Simulate the UI sending the user's decision back via
			// the same path Guardian's handleBusResponse expects.
			payload := map[string]any{
				"decision": tc.decision,
				"approved": tc.decision == "approve",
			}
			rawPayload, _ := json.Marshal(payload)
			var decoded map[string]any
			_ = json.Unmarshal(rawPayload, &decoded)
			_ = g.handleBusResponse(&guide.Message{
				CorrelationID: proposal.CorrelationID,
				Type:          guide.MessageTypeResponse,
				Payload:       decoded,
			})

			select {
			case res := <-resultCh:
				if res.Verdict != tc.want {
					t.Fatalf("verdict = %q, want %q", res.Verdict, tc.want)
				}
				if res.PlanID != req.PlanID {
					t.Fatalf("plan_id roundtrip mismatch")
				}
			case err := <-errCh:
				t.Fatalf("requestPlanApproval returned error: %v", err)
			case <-time.After(time.Second):
				t.Fatal("requestPlanApproval did not return after decision response")
			}
		})
	}
}
