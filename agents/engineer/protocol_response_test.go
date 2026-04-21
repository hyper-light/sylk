package engineer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	shared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

type scriptedEngineerProvider struct {
	responses []*providers.Response
	calls     int
}

func (p *scriptedEngineerProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	if p.calls >= len(p.responses) {
		return &providers.Response{Content: "", StopReason: providers.StopReasonEndTurn}, nil
	}
	resp := p.responses[p.calls]
	p.calls++
	return resp, nil
}

func TestHandleBusRequest_PipelineResponsePreservesRecordedAction(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	provider := &scriptedEngineerProvider{
		responses: []*providers.Response{
			{
				StopReason: providers.StopReasonToolUse,
				ToolCalls: []providers.ToolCall{
					{
						ID:   "call-1",
						Name: "pipeline_protocol",
						Arguments: `{
							"action":"handoff",
							"target_agents":["inspector"],
							"reason":"implementation is complete and ready for audit",
							"request":"Inspect the implementation changes and validate them against the criteria.",
							"required_output":["implementation audit"],
							"references":["hello-cli/pyproject.toml"]
						}`,
					},
				},
			},
		},
	}

	e, err := New(Config{Factory: newTestFactory(t), SessionID: "sess-1"}, provider)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close()
	if err := e.Start(bus); err != nil {
		t.Fatalf("Start: %v", err)
	}

	respCh := make(chan *guide.RouteResponse, 1)
	respSub, err := bus.SubscribeAsync(e.channels.Responses, func(msg *guide.Message) error {
		resp, ok := msg.GetRouteResponse()
		if !ok || resp == nil || resp.RespondingAgentID != e.id || resp.CorrelationID != "corr-engineer" {
			return nil
		}
		select {
		case respCh <- resp:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeAsync responses: %v", err)
	}
	defer respSub.Unsubscribe()

	routeCh := make(chan *guide.RouteRequest, 1)
	routeSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.ParentCorrelationID != "corr-engineer" {
			return nil
		}
		select {
		case routeCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeAsync guide requests: %v", err)
	}
	defer routeSub.Unsubscribe()

	task := &shared.PipelineTaskInput{
		NodeID:    "node-1",
		DAGID:     "dag-1",
		TaskID:    "task-1",
		AgentType: shared.PipelineAgentEngineer,
		Prompt:    "Create pyproject.toml and package init module",
		SessionID: "sess-1",
		Context: map[string]any{
			"task_slug": "create-pyproject-and-package-init",
			"pipeline_protocol": shared.PipelineProtocolSnapshotMap(&shared.PipelineProtocolSnapshot{
				Roster: []shared.PipelineProtocolAgent{
					{AgentType: shared.PipelineAgentInspector},
					{AgentType: shared.PipelineAgentEngineer},
				},
				ActiveAgents:   []string{shared.PipelineAgentEngineer},
				CurrentRequest: "Implement the requested production changes.",
			}),
		},
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal task: %v", err)
	}

	msg := guide.NewForwardMessage("", &guide.ForwardedRequest{
		CorrelationID: "corr-engineer",
		Input:         string(payload),
		Intent:        guide.IntentComplete,
		Domain:        guide.DomainCode,
		SourceAgentID: "orchestrator",
		TargetAgentID: e.id,
		SessionID:     "sess-1",
		Metadata: map[string]any{
			"task_id":   "task-1",
			"task_slug": "create-pyproject-and-package-init",
		},
	})

	if err := e.handleBusRequest(msg); err != nil {
		t.Fatalf("handleBusRequest: %v", err)
	}

	select {
	case resp := <-respCh:
		turnResp, err := shared.DecodePipelineTurnResponse(resp.Data)
		if err != nil {
			t.Fatalf("DecodePipelineTurnResponse: %v", err)
		}
		if turnResp.Action == nil {
			t.Fatal("expected pipeline turn response to include a recorded action")
		}
		if turnResp.Action.Type != shared.PipelineProtocolActionHandoff {
			t.Fatalf("action type = %q, want %q", turnResp.Action.Type, shared.PipelineProtocolActionHandoff)
		}
		if len(turnResp.Action.TargetAgents) != 1 || turnResp.Action.TargetAgents[0] != shared.PipelineAgentInspector {
			t.Fatalf("target agents = %#v", turnResp.Action.TargetAgents)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for engineer route response")
	}

	select {
	case req := <-routeCh:
		var nextTask shared.PipelineTaskInput
		if err := json.Unmarshal([]byte(req.Input), &nextTask); err != nil {
			t.Fatalf("decode next task: %v", err)
		}
		if nextTask.AgentType != shared.PipelineAgentInspector {
			t.Fatalf("next agent_type = %q, want %q", nextTask.AgentType, shared.PipelineAgentInspector)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for engineer handoff route")
	}
}
