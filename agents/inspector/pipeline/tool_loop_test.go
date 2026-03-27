package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	inspectorshared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

type scriptedPipelineProvider struct {
	mu        sync.Mutex
	responses []*providers.Response
	calls     int
}

func (p *scriptedPipelineProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.calls >= len(p.responses) {
		return nil, fmt.Errorf("unexpected provider call %d", p.calls+1)
	}
	resp := p.responses[p.calls]
	p.calls++

	if resp == nil {
		return nil, fmt.Errorf("nil scripted response at call %d", p.calls)
	}

	cloned := *resp
	if len(resp.ToolCalls) > 0 {
		cloned.ToolCalls = append([]providers.ToolCall(nil), resp.ToolCalls...)
	}
	return &cloned, nil
}

func TestHandle_AllowsGraceTurnForFinalizePipelineHandoffToOT(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	provider := &scriptedPipelineProvider{
		responses: []*providers.Response{
			{
				ToolCalls: []providers.ToolCall{{
					ID:        "tool-1",
					Name:      "get_validation_status",
					Arguments: `{}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-2",
					Name: "finalize_pipeline",
					Arguments: `{
						"summary":"Tester-backed audit passed.",
						"evidence_refs":["artifact:inspector"]
					}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-3",
					Name: "handoff_to_ot",
					Arguments: `{
						"summary":"Ready for OT merge.",
						"evidence_refs":["artifact:tester"]
					}`,
				}},
			},
		},
	}

	pi, err := New(inspectorshared.PipelineInspectorConfig{
		AgentID:        "inspector-pipeline",
		SessionID:      "sess-1",
		MaxToolRuns:    1,
		DefaultTimeout: 5 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	t.Cleanup(func() {
		_ = pi.Close()
	})
	pi.SetProvider(provider)
	pi.bus = bus
	pi.state.CurrentTaskID = "task-1"

	updateCh := make(chan map[string]any, 1)
	sub, err := bus.SubscribeAsync("pipeline.update."+agentShared.PipelineAgentInspector, func(msg *guide.Message) error {
		payload, ok := msg.Payload.(map[string]any)
		if !ok {
			return nil
		}
		select {
		case updateCh <- payload:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeAsync(): %v", err)
	}
	defer sub.Unsubscribe()

	task := &agentShared.PipelineTaskInput{
		NodeID:        "task-1",
		DAGID:         "dag-1",
		TaskID:        "task-1",
		AgentType:     agentShared.PipelineAgentInspector,
		TargetAgentID: "inspector-pipeline",
		Prompt:        "Audit the completed task.",
		Context: map[string]any{
			"pipeline_stage": "execute",
			"pipeline_protocol": agentShared.PipelineProtocolSnapshotMap(&agentShared.PipelineProtocolSnapshot{
				PendingValidation: &agentShared.PipelineValidationRecord{
					ChallengeID:         "challenge-ready",
					RequestingAgent:     agentShared.PipelineAgentInspector,
					RespondingAgent:     agentShared.PipelineAgentTester,
					Status:              string(agentShared.PipelineValidationPassed),
					Summary:             "tester accepted the audit",
					ChallengeReferences: []string{"finalize_pipeline_verification"},
					EvidenceRefs:        []string{"artifact:tester"},
				},
			}),
		},
		SessionID: "sess-1",
	}
	input, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Marshal(task): %v", err)
	}

	_, err = pi.Handle(context.Background(), &guide.ForwardedRequest{
		CorrelationID: "pipe-test-1",
		Input:         string(input),
		SourceAgentID: "orchestrator",
		TargetAgentID: "inspector-pipeline",
		SessionID:     "sess-1",
	})
	if err != nil {
		t.Fatalf("Handle(): %v", err)
	}

	select {
	case update := <-updateCh:
		if got := update["status"]; got != "succeeded" {
			t.Fatalf("status = %#v, want succeeded", got)
		}
		if got := update["task_id"]; got != "task-1" {
			t.Fatalf("task_id = %#v, want task-1", got)
		}
		if got := update["agent_type"]; got != agentShared.PipelineAgentInspector {
			t.Fatalf("agent_type = %#v, want %q", got, agentShared.PipelineAgentInspector)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inspector OT success update")
	}

	if provider.calls != 3 {
		t.Fatalf("provider calls = %d, want 3", provider.calls)
	}
}
