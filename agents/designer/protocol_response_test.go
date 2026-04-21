package designer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	shared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

type scriptedDesignerProvider struct {
	responses []*providers.Response
	calls     int
}

func (p *scriptedDesignerProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
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

	provider := &scriptedDesignerProvider{
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
							"reason":"design updates are complete and ready for review",
							"request":"Inspect the design implementation and validate it against the pipeline criteria.",
							"required_output":["design audit"],
							"references":["hello-cli/__init__.py"]
						}`,
					},
				},
			},
		},
	}

	d, err := New(Config{Factory: newTestFactory(t), SessionID: "sess-1"}, provider)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()
	if err := d.Start(bus); err != nil {
		t.Fatalf("Start: %v", err)
	}

	respCh := make(chan *guide.RouteResponse, 1)
	respSub, err := bus.SubscribeAsync(d.channels.Responses, func(msg *guide.Message) error {
		resp, ok := msg.GetRouteResponse()
		if !ok || resp == nil || resp.RespondingAgentID != d.id || resp.CorrelationID != "corr-designer" {
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
		if !ok || req == nil || req.ParentCorrelationID != "corr-designer" {
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
		AgentType: shared.PipelineAgentDesigner,
		Prompt:    "Create pyproject.toml and package init module",
		SessionID: "sess-1",
		Context: map[string]any{
			"task_slug": "create-pyproject-and-package-init",
			"pipeline_protocol": shared.PipelineProtocolSnapshotMap(&shared.PipelineProtocolSnapshot{
				Roster: []shared.PipelineProtocolAgent{
					{AgentType: shared.PipelineAgentInspector},
					{AgentType: shared.PipelineAgentDesigner},
				},
				ActiveAgents:   []string{shared.PipelineAgentDesigner},
				CurrentRequest: "Implement the requested design changes.",
			}),
		},
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal task: %v", err)
	}

	msg := guide.NewForwardMessage("", &guide.ForwardedRequest{
		CorrelationID: "corr-designer",
		Input:         string(payload),
		Intent:        guide.IntentDesign,
		Domain:        guide.DomainCode,
		SourceAgentID: "orchestrator",
		TargetAgentID: d.id,
		SessionID:     "sess-1",
		Metadata: map[string]any{
			"task_id":   "task-1",
			"task_slug": "create-pyproject-and-package-init",
		},
	})

	if err := d.handleBusRequest(msg); err != nil {
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
		t.Fatal("timed out waiting for designer route response")
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
		t.Fatal("timed out waiting for designer handoff route")
	}
}
