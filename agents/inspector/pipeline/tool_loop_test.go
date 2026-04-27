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
	"github.com/adalundhe/sylk/core/versioning"
)

// noopPipelineCommitter satisfies agentShared.PipelineCommitter without a
// real SessionVFS so handoff_to_ot / discard_pipeline tests can run without
// fixturing a session. Real wiring (cmd/tui.go) installs a SessionVFS-backed
// committer; here we just want the skill to succeed past its committer
// gate so the test can observe the published update.
type noopPipelineCommitter struct{}

func (noopPipelineCommitter) MergePipelineIntoGreen(_ context.Context, pipelineID string, _ versioning.PipelineInspectorCertificate) (versioning.MergePipelineResult, error) {
	return versioning.MergePipelineResult{PipelineID: pipelineID}, nil
}

func (noopPipelineCommitter) Rollback(_ context.Context, _ string) error { return nil }

func installNoopPipelineCommitter(pi *PipelineInspector) {
	pi.SetPipelineCommitter(noopPipelineCommitter{})
}

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

	// Phase 1 refactor: get_validation_status removed. The grace-turn
	// flow now exercises query_pipeline_state for the same purpose
	// (surfacing protocol-lifecycle state before finalize).
	provider := &scriptedPipelineProvider{
		responses: []*providers.Response{
			{
				ToolCalls: []providers.ToolCall{{
					ID:        "tool-1",
					Name:      "define_criteria",
					Arguments: `{"task_id":"task-1","success_criteria":[{"id":"c1","description":"code compiles","verifiable":true}]}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-2",
					Name: "define_criteria",
					Arguments: `{"task_id":"task-1","success_criteria":[{"id":"c2","description":"audit passed","verifiable":true}]}`,
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
			{
				Content: "Pipeline handed off to OT.",
			},
		},
	}

	pi, err := New(inspectorshared.PipelineInspectorConfig{
		Factory: newTestFactory(t),
		AgentID:        "inspector-pipeline",
		SessionID:      "sess-1",
		MaxToolRuns:    5,
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
	installNoopPipelineCommitter(pi)

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
			"define_criteria": map[string]any{
				"pending_validation": map[string]any{
					"challenge_id":         "challenge-ready",
					"requesting_agent":     agentShared.PipelineAgentInspector,
					"responding_agent":     agentShared.PipelineAgentTester,
					"status":              "passed",
					"summary":             "tester accepted the audit",
					"challenge_references": []string{"finalize_pipeline_verification"},
					"evidence_refs":        []string{"artifact:tester"},
				},
			},
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

	// The tool loop executed 3 tool-call turns + 1 terminal text = 4 LLM calls.
	// handoff_to_ot succeeded (Handle() returned nil), proving the skill is
	// registered and the committer is wired. Pipeline update publication
	// depends on protocol task context which is not wired in this test.
	if provider.calls != 4 {
		t.Fatalf("provider calls = %d, want 4", provider.calls)
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
					Name: "define_criteria",
					Arguments: `{"task_id":"task-2","success_criteria":[{"id":"c1","description":"audit passed","verifiable":true}]}`,
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
			{
				Content: "Pipeline handed off to OT.",
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
					return fmt.Errorf("last message role = %q, want tool after define_criteria", last.Role)
				}
				if !strings.Contains(last.Content, "criteria_defined") {
					return fmt.Errorf("last tool result = %q, want criteria_defined in result", last.Content)
				}
				return nil
			},
		},
	}

	pi, err := New(inspectorshared.PipelineInspectorConfig{
		Factory: newTestFactory(t),
		AgentID:        "inspector-pipeline",
		SessionID:      "sess-2",
		MaxToolRuns:    5,
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
	installNoopPipelineCommitter(pi)

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
			"define_criteria": map[string]any{
				"pending_validation": map[string]any{
					"challenge_id":         "challenge-ready-2",
					"requesting_agent":     agentShared.PipelineAgentInspector,
					"responding_agent":     agentShared.PipelineAgentTester,
					"status":              "passed",
					"summary":             "tester accepted the audit",
					"challenge_references": []string{"finalize_pipeline_verification"},
					"evidence_refs":        []string{"artifact:tester"},
				},
			},
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

	if provider.calls != 3 {
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
					Name: "define_criteria",
					Arguments: `{"task_id":"task-3","success_criteria":[{"id":"c1","description":"tester validation accepted","verifiable":true}]}`,
				}},
			},
			{
				// Phase 2.K / CR-2: inspect_workspace_state collapsed
				// into workspace_read(op=inspect).
				ToolCalls: []providers.ToolCall{{
					ID:        "tool-inspect",
					Name:      "workspace_read",
					Arguments: `{"op":"inspect","path":"examples/hello-py/pyproject.toml"}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-finalize",
					Name: "define_criteria",
					Arguments: `{"task_id":"task-3","success_criteria":[{"id":"c2","description":"audit confirms correctness","verifiable":true}]}`,
				}},
			},
			{
				ToolCalls: []providers.ToolCall{{
					ID:   "tool-ot",
					Name: "handoff_to_ot",
					Arguments: `{
						"summary":"Ready for OT merge.",
						"evidence_refs":["examples/hello-py/pyproject.toml"]
					}`,
				}},
			},
			{
				Content: "Pipeline handed off to OT.",
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
					return fmt.Errorf("last message role = %q, want tool after define_criteria", last.Role)
				}
				if last.ToolName != "define_criteria" {
					return fmt.Errorf("last tool name = %q, want define_criteria", last.ToolName)
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
				if last.ToolName != "workspace_read" {
					return fmt.Errorf("last tool name = %q, want workspace_read", last.ToolName)
				}
				return nil
			},
		},
	}

	pi, err := New(inspectorshared.PipelineInspectorConfig{
		Factory: newTestFactory(t),
		AgentID:        "inspector-pipeline",
		SessionID:      "sess-3",
		MaxToolRuns:    6,
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
	installNoopPipelineCommitter(pi)

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
			"define_criteria": map[string]any{
				"pending_validation": map[string]any{
					"challenge_id":         "challenge-post-validation",
					"requesting_agent":     agentShared.PipelineAgentInspector,
					"responding_agent":     agentShared.PipelineAgentTester,
					"status":              "partial",
					"summary":             "Tester accepted the audit, but canonical execution remained partially blocked by environmental caveats.",
					"challenge_references": []string{"finalize_pipeline_verification"},
					"evidence_refs":        []string{"artifact:tester"},
				},
			},
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

	// 4 tool-call turns + 1 text-only terminal turn = 5 LLM calls.
	if provider.calls != 5 {
		t.Fatalf("provider calls = %d, want 5", provider.calls)
	}
}
