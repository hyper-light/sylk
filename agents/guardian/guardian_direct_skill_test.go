package guardian

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
)

func TestGuardianDirectSkillPublishesStream(t *testing.T) {
	if !guardianDirectSkillPublishesStream("tool_execution_control") {
		t.Fatal("expected tool_execution_control stream publishing to remain enabled")
	}
	if !guardianDirectSkillPublishesStream("command_execution_control") {
		t.Fatal("expected command_execution_control stream publishing to remain enabled")
	}
	if !guardianDirectSkillPublishesStream("other_skill") {
		t.Fatal("expected other guardian direct skills to keep stream publishing")
	}
}

func TestGuardianForwardedCommandApprovalDirectSkillPublishesLifecycle(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	g := newTestGuardian(t, &stubGuardianProvider{})
	if err := g.Start(bus); err != nil {
		t.Fatalf("guardian start: %v", err)
	}
	defer func() {
		if err := g.Stop(); err != nil {
			t.Fatalf("guardian stop: %v", err)
		}
	}()

	streamCh := make(chan *guide.StreamResponse, 16)
	respCh := make(chan *guide.RouteResponse, 1)
	sub, err := bus.SubscribeAsync(g.channels.Responses, func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			select {
			case streamCh <- stream:
			default:
			}
			return nil
		}
		if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
			select {
			case respCh <- resp:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guardian responses: %v", err)
	}
	defer sub.Unsubscribe()

	input, err := json.Marshal(commandapproval.Request{
		Command:       "go test ./...",
		WorkspaceRoot: "/workspace/repo",
		ToolName:      "run_command",
		AgentID:       "engineer-1",
		AgentType:     "engineer",
		SessionID:     "sess-1",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	fwd := &guide.ForwardedRequest{
		CorrelationID:   "corr-guardian-direct-approval",
		Input:           string(input),
		SourceAgentID:   "academic",
		SourceAgentName: "Academic",
		TargetAgentID:   "guardian",
		SessionID:       "sess-1",
		Metadata: map[string]any{
			"direct_skill": "command_execution_control",
			"agent_type":   "guardian",
			"agent_name":   "Guardian",
		},
	}
	if err := bus.Publish(g.channels.Requests, guide.NewForwardMessage("", fwd)); err != nil {
		t.Fatalf("publish forwarded request: %v", err)
	}

	deadline := time.After(2 * time.Second)
	var sawStart, sawToolStart, sawToolComplete, sawComplete, sawResponse bool
	for !(sawStart && sawToolStart && sawToolComplete && sawComplete && sawResponse) {
		select {
		case stream := <-streamCh:
			if stream == nil || stream.CorrelationID != fwd.CorrelationID || stream.Event == nil {
				continue
			}
			switch stream.Event.Type {
			case guide.StreamEventStart:
				sawStart = true
			case guide.StreamEventToolCall:
				data, ok := stream.Event.Data.(map[string]any)
				if !ok {
					continue
				}
				if got, _ := data["tool_name"].(string); got != "command_execution_control" {
					continue
				}
				switch phase := data["phase"].(type) {
				case int:
					if phase == 0 {
						sawToolStart = true
					}
					if phase == 1 {
						sawToolComplete = true
						if got, _ := data["output"].(string); got != "Command approval allowed" {
							t.Fatalf("tool output = %q, want %q", got, "Command approval allowed")
						}
					}
				case float64:
					if int(phase) == 0 {
						sawToolStart = true
					}
					if int(phase) == 1 {
						sawToolComplete = true
						if got, _ := data["output"].(string); got != "Command approval allowed" {
							t.Fatalf("tool output = %q, want %q", got, "Command approval allowed")
						}
					}
				}
			case guide.StreamEventComplete:
				sawComplete = true
				if got := stream.Event.Text; got != "Command approval allowed" {
					t.Fatalf("stream completion text = %q, want %q", got, "Command approval allowed")
				}
			}
		case resp := <-respCh:
			if resp == nil || resp.CorrelationID != fwd.CorrelationID {
				continue
			}
			sawResponse = true
			if !resp.Success {
				t.Fatalf("route response success = false, error=%q", resp.Error)
			}
			eval, ok := resp.Data.(commandapproval.Evaluation)
			if !ok {
				t.Fatalf("route response data = %T, want commandapproval.Evaluation", resp.Data)
			}
			if eval.Decision != commandapproval.DecisionAllow {
				t.Fatalf("decision = %q, want %q", eval.Decision, commandapproval.DecisionAllow)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for direct-skill lifecycle; start=%v toolStart=%v toolComplete=%v complete=%v response=%v", sawStart, sawToolStart, sawToolComplete, sawComplete, sawResponse)
		}
	}
}

func TestFormatGuardianDirectSkillOutput_ApprovalEvaluationUsesFriendlyText(t *testing.T) {
	got := formatGuardianDirectSkillOutput(commandapproval.Evaluation{
		Decision: commandapproval.DecisionAllow,
		Analysis: commandapproval.Analysis{
			Program:     "fetch",
			TemplateKey: "fetch|domain=example.com",
		},
	})
	if got != "Fetch approval allowed" {
		t.Fatalf("friendly fetch approval text = %q, want %q", got, "Fetch approval allowed")
	}
}
