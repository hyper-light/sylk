package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	inspectorshared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

type scriptedPipelineProvider struct {
	mu             sync.Mutex
	responses      []*providers.Response
	requestInspect map[int]func(*providers.Request) error
	calls          int
}

func (p *scriptedPipelineProvider) Complete(_ context.Context, req *providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if inspect := p.requestInspect[p.calls]; inspect != nil {
		if err := inspect(req); err != nil {
			return nil, err
		}
	}
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
	sessionDir := t.TempDir()
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
		Factory: newTestFactory(t),
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
			"session_dir":    sessionDir,
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

func TestHandle_UsesFinalizePipelineToolResultToDriveImmediateHandoffToOT(t *testing.T) {
	sessionDir := t.TempDir()
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	provider := &scriptedPipelineProvider{
		responses: []*providers.Response{
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-finalize",
					Name: "finalize_pipeline",
					Arguments: `{
						"summary":"Tester-backed audit passed.",
						"evidence_refs":["artifact:inspector"]
					}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-ot",
					Name: "handoff_to_ot",
					Arguments: `{
						"summary":"Ready for OT merge.",
						"evidence_refs":["artifact:inspector","artifact:tester"]
					}`,
				}},
			},
		},
		requestInspect: map[int]func(*providers.Request) error{
			0: func(req *providers.Request) error {
				if req == nil {
					return fmt.Errorf("request is nil")
				}
				if !req.DisableParallelToolUse {
					return fmt.Errorf("DisableParallelToolUse = false, want true")
				}
				return nil
			},
			1: func(req *providers.Request) error {
				if req == nil {
					return fmt.Errorf("request is nil")
				}
				last := req.Messages[len(req.Messages)-1]
				if last.Role != providers.RoleTool {
					return fmt.Errorf("last message role = %q, want tool", last.Role)
				}
				if !strings.Contains(last.Content, "handoff_to_ot") {
					return fmt.Errorf("last tool result = %q, want handoff_to_ot guidance", last.Content)
				}
				return nil
			},
		},
	}

	pi, err := New(inspectorshared.PipelineInspectorConfig{
		Factory: newTestFactory(t),
		AgentID:        "inspector-pipeline",
		SessionID:      "sess-2",
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
	pi.state.CurrentTaskID = "task-2"

	task := &agentShared.PipelineTaskInput{
		NodeID:        "task-2",
		DAGID:         "dag-2",
		TaskID:        "task-2",
		AgentType:     agentShared.PipelineAgentInspector,
		TargetAgentID: "inspector-pipeline",
		Prompt:        "Audit the completed task.",
		Context: map[string]any{
			"session_dir":    sessionDir,
			"pipeline_stage": "execute",
			"pipeline_protocol": agentShared.PipelineProtocolSnapshotMap(&agentShared.PipelineProtocolSnapshot{
				PendingValidation: &agentShared.PipelineValidationRecord{
					ChallengeID:         "challenge-ready-2",
					RequestingAgent:     agentShared.PipelineAgentInspector,
					RespondingAgent:     agentShared.PipelineAgentTester,
					Status:              string(agentShared.PipelineValidationPassed),
					Summary:             "tester accepted the audit",
					ChallengeReferences: []string{"finalize_pipeline_verification"},
					EvidenceRefs:        []string{"artifact:tester"},
				},
			}),
		},
		SessionID: "sess-2",
	}
	input, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Marshal(task): %v", err)
	}

	if _, err := pi.Handle(context.Background(), &guide.ForwardedRequest{
		CorrelationID: "pipe-test-2",
		Input:         string(input),
		SourceAgentID: "orchestrator",
		TargetAgentID: "inspector-pipeline",
		SessionID:     "sess-2",
	}); err != nil {
		t.Fatalf("Handle(): %v", err)
	}

	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
}

func TestHandle_PostValidationAuditContinuesFromToolResultsWithoutInjectedUserPrompts(t *testing.T) {
	sessionDir := t.TempDir()
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	provider := &scriptedPipelineProvider{
		responses: []*providers.Response{
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-process",
					Name: "process_validation",
					Arguments: `{
						"challenge_id":"challenge-post-validation",
						"decision":"accept",
						"summary":"Accepted tester validation and will perform a direct audit before closure."
					}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:        "tool-inspect",
					Name:      "inspect_workspace_state",
					Arguments: `{"path":"examples/hello-py/pyproject.toml"}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-finalize",
					Name: "finalize_pipeline",
					Arguments: `{
						"summary":"The direct audit confirms the implementation is correct and the remaining failures were environmental.",
						"evidence_refs":["examples/hello-py/pyproject.toml","examples/hello-py/tests/test_pyproject.py"]
					}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-ot",
					Name: "handoff_to_ot",
					Arguments: `{
						"summary":"Ready for OT merge.",
						"evidence_refs":["examples/hello-py/pyproject.toml","examples/hello-py/tests/test_pyproject.py"]
					}`,
				}},
			},
		},
		requestInspect: map[int]func(*providers.Request) error{
			0: func(req *providers.Request) error {
				if req == nil {
					return fmt.Errorf("request is nil")
				}
				if !req.DisableParallelToolUse {
					return fmt.Errorf("DisableParallelToolUse = false, want true")
				}
				return nil
			},
			1: func(req *providers.Request) error {
				if req == nil {
					return fmt.Errorf("request is nil")
				}
				last := req.Messages[len(req.Messages)-1]
				if last.Role != providers.RoleTool {
					return fmt.Errorf("last message role = %q, want tool after process_validation", last.Role)
				}
				if last.ToolName != "process_validation" {
					return fmt.Errorf("last tool name = %q, want process_validation", last.ToolName)
				}
				return nil
			},
			2: func(req *providers.Request) error {
				if req == nil {
					return fmt.Errorf("request is nil")
				}
				last := req.Messages[len(req.Messages)-1]
				if last.Role != providers.RoleTool {
					return fmt.Errorf("last message role = %q, want tool after direct audit", last.Role)
				}
				if last.ToolName != "inspect_workspace_state" {
					return fmt.Errorf("last tool name = %q, want inspect_workspace_state", last.ToolName)
				}
				return nil
			},
		},
	}

	pi, err := New(inspectorshared.PipelineInspectorConfig{
		Factory: newTestFactory(t),
		AgentID:        "inspector-pipeline",
		SessionID:      "sess-3",
		MaxToolRuns:    3,
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
	pi.state.CurrentTaskID = "task-3"

	task := &agentShared.PipelineTaskInput{
		NodeID:        "task-3",
		DAGID:         "dag-3",
		TaskID:        "task-3",
		AgentType:     agentShared.PipelineAgentInspector,
		TargetAgentID: "inspector-pipeline",
		Prompt:        "Audit the completed task.",
		Context: map[string]any{
			"session_dir":    sessionDir,
			"pipeline_stage": "execute",
			"pipeline_protocol": agentShared.PipelineProtocolSnapshotMap(&agentShared.PipelineProtocolSnapshot{
				PendingValidation: &agentShared.PipelineValidationRecord{
					ChallengeID:         "challenge-post-validation",
					RequestingAgent:     agentShared.PipelineAgentInspector,
					RespondingAgent:     agentShared.PipelineAgentTester,
					Status:              string(agentShared.PipelineValidationPartial),
					Summary:             "Tester accepted the audit, but canonical execution remained partially blocked by environmental caveats.",
					ChallengeReferences: []string{"finalize_pipeline_verification"},
					EvidenceRefs:        []string{"artifact:tester"},
				},
			}),
		},
		SessionID: "sess-3",
	}
	input, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Marshal(task): %v", err)
	}

	if _, err := pi.Handle(context.Background(), &guide.ForwardedRequest{
		CorrelationID: "pipe-test-3",
		Input:         string(input),
		SourceAgentID: "orchestrator",
		TargetAgentID: "inspector-pipeline",
		SessionID:     "sess-3",
	}); err != nil {
		t.Fatalf("Handle(): %v", err)
	}

	if provider.calls != 4 {
		t.Fatalf("provider calls = %d, want 4", provider.calls)
	}
}
