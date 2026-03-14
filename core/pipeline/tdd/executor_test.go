package tdd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/engineer"
	inspPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester"
	testerpipeline "github.com/adalundhe/sylk/agents/tester/pipeline"
)

type scriptedInspector struct {
	turns int32
	run   func(ctx context.Context, task *agentShared.PipelineTaskInput, turn int) (*inspPipeline.InspectionStageResult, error)
}

func (s *scriptedInspector) RunTask(ctx context.Context, task *agentShared.PipelineTaskInput) (*inspPipeline.InspectionStageResult, error) {
	return s.run(ctx, task, int(atomic.AddInt32(&s.turns, 1)))
}

func (s *scriptedInspector) Close() error { return nil }

type scriptedTester struct {
	turns int32
	run   func(ctx context.Context, task *agentShared.PipelineTaskInput, turn int) (*testerpipeline.TaskStageResult, error)
}

func (s *scriptedTester) TestTask(ctx context.Context, task *agentShared.PipelineTaskInput) (*testerpipeline.TaskStageResult, error) {
	return s.run(ctx, task, int(atomic.AddInt32(&s.turns, 1)))
}

func (s *scriptedTester) Close() error { return nil }

type mockWorker struct {
	executeFn func(ctx context.Context, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error)
	closed    atomic.Bool
	task      *agentShared.PipelineTaskInput
	prompt    string
	prior     *WorkerResult
}

func (m *mockWorker) Execute(ctx context.Context, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error) {
	if m.executeFn != nil {
		return m.executeFn(ctx, criteria, inspFb, testFb)
	}
	return &WorkerResult{ChangedFiles: []string{"file.go"}}, nil
}

func (m *mockWorker) Close() error {
	m.closed.Store(true)
	return nil
}

func (m *mockWorker) SetPipelineTask(task *agentShared.PipelineTaskInput) { m.task = task }
func (m *mockWorker) SetTaskPrompt(prompt string)                         { m.prompt = prompt }
func (m *mockWorker) SetPriorOutput(result *WorkerResult)                 { m.prior = result }

func newTestPipeline(taskID string) *Pipeline {
	return &Pipeline{
		ID:         "test-pipeline-1",
		TaskID:     taskID,
		TaskSlug:   "auth-checkout",
		SessionID:  "test-session",
		DAGNodeID:  "test-node",
		Status:     StatusPending,
		WorkerType: WorkerEngineer,
		MaxLoops:   defaultMaxLoops,
		InspectorCriteria: &inspShared.InspectorCriteria{
			TaskID: taskID,
			SuccessCriteria: []inspShared.SuccessCriterion{
				{ID: "sc1", Description: "criteria exist", Verifiable: true},
			},
		},
		CreatedAt: time.Now(),
	}
}

func invokeProtocolSkill(t *testing.T, ctx context.Context, cfg agentShared.PipelineProtocolSkillConfig, name string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal skill payload: %v", err)
	}
	for _, skill := range agentShared.PipelineProtocolSkills(cfg) {
		if skill.Name != name {
			continue
		}
		if _, err := skill.Handler(ctx, raw); err != nil {
			t.Fatalf("skill %s: %v", name, err)
		}
		return
	}
	t.Fatalf("skill %s not found", name)
}

func TestTDDExecutor_ProtocolLoopCompletes(t *testing.T) {
	pipeline := newTestPipeline("task-protocol")

	inspector := &scriptedInspector{
		run: func(ctx context.Context, _ *agentShared.PipelineTaskInput, turn int) (*inspPipeline.InspectionStageResult, error) {
			switch turn {
			case 1:
				invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
					AgentType:   func() string { return agentShared.PipelineAgentInspector },
					InspectorOT: true,
				}, "handoff_next", map[string]any{
					"target_agents":   []string{"tester"},
					"reason":          "criteria are ready for testability review",
					"request":         "Challenge the criteria and validate the required tests for this task.",
					"required_output": []string{"testability verdict"},
				})
			case 2:
				invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
					AgentType:   func() string { return agentShared.PipelineAgentInspector },
					InspectorOT: true,
				}, "process_validation", map[string]any{
					"challenge_id": "task-protocol-challenge-1",
					"decision":     "accept",
					"summary":      "Tester confirmed the task is ready for merge.",
				})
				invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
					AgentType:   func() string { return agentShared.PipelineAgentInspector },
					InspectorOT: true,
				}, "handoff_to_ot", map[string]any{
					"summary":       "Criteria and testing are satisfied.",
					"evidence_refs": []string{"tests/auth_test.go"},
				})
			default:
				t.Fatalf("unexpected inspector turn %d", turn)
			}
			return &inspPipeline.InspectionStageResult{
				Criteria: &inspShared.InspectorCriteria{TaskID: pipeline.TaskID},
				Result:   &inspShared.InspectorResult{Passed: turn > 1},
			}, nil
		},
	}

	testerAgent := &scriptedTester{
		run: func(ctx context.Context, _ *agentShared.PipelineTaskInput, turn int) (*testerpipeline.TaskStageResult, error) {
			if turn != 1 {
				t.Fatalf("unexpected tester turn %d", turn)
			}
			invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
				AgentType: func() string { return agentShared.PipelineAgentTester },
			}, "validate_work", map[string]any{
				"challenge_id":            "task-protocol-challenge-1",
				"requesting_agent":        "inspector",
				"status":                  "passed",
				"summary":                 "The criteria are testable and the current tests cover them.",
				"evidence_refs":           []string{"tests/auth_test.go"},
				"recommended_next_agents": []string{"inspector"},
			})
			return &testerpipeline.TaskStageResult{
				SuiteResult: &tester.TestSuiteResult{},
			}, nil
		},
	}

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Inspector:    inspector,
		Tester:       testerAgent,
		Worker:       &mockWorker{},
		PhaseTimeout: time.Second,
		MaxLoops:     2,
	})

	if err := exec.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := exec.WaitDone(3 * time.Second); err != nil {
		t.Fatalf("wait done: %v", err)
	}

	snap := exec.Pipeline()
	if snap.Status != StatusCompleted {
		t.Fatalf("status = %s, want %s", snap.Status, StatusCompleted)
	}
	if snap.LoopCount != 1 {
		t.Fatalf("loop_count = %d, want 1", snap.LoopCount)
	}
}

func TestTDDExecutor_ExecuteCohortReturnsToInspector(t *testing.T) {
	pipeline := newTestPipeline("task-execute")
	pipeline.WorkerType = WorkerEngineer
	pipeline.CoWorkerTypes = []WorkerType{WorkerDesigner}

	inspector := &scriptedInspector{
		run: func(ctx context.Context, _ *agentShared.PipelineTaskInput, turn int) (*inspPipeline.InspectionStageResult, error) {
			switch turn {
			case 1:
				invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
					AgentType:   func() string { return agentShared.PipelineAgentInspector },
					InspectorOT: true,
				}, "handoff_next", map[string]any{
					"target_agents": []string{"tester"},
					"reason":        "Need tester to define and validate the red-phase work.",
					"request":       "Author and validate the tests that should drive implementation.",
				})
			case 2:
				invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
					AgentType:   func() string { return agentShared.PipelineAgentInspector },
					InspectorOT: true,
				}, "process_validation", map[string]any{
					"challenge_id": "task-execute-challenge-1",
					"decision":     "accept",
					"summary":      "Tester made the execute work concrete enough to implement.",
				})
				invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
					AgentType:   func() string { return agentShared.PipelineAgentInspector },
					InspectorOT: true,
				}, "handoff_next", map[string]any{
					"target_agents":   []string{"engineer", "designer"},
					"mode":            "cohort",
					"reason":          "Implementation and design should proceed together.",
					"request":         "Implement the requested behavior and design changes against the tests.",
					"required_output": []string{"code changes", "design changes"},
				})
			case 3:
				invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
					AgentType:   func() string { return agentShared.PipelineAgentInspector },
					InspectorOT: true,
				}, "handoff_to_ot", map[string]any{
					"summary":       "Implementation cohort returned the work for merge.",
					"evidence_refs": []string{"app/main.go", "ui/button.tsx"},
				})
			default:
				t.Fatalf("unexpected inspector turn %d", turn)
			}
			return &inspPipeline.InspectionStageResult{
				Criteria: &inspShared.InspectorCriteria{TaskID: pipeline.TaskID},
				Result:   &inspShared.InspectorResult{Passed: turn > 2},
			}, nil
		},
	}

	testerAgent := &scriptedTester{
		run: func(ctx context.Context, _ *agentShared.PipelineTaskInput, turn int) (*testerpipeline.TaskStageResult, error) {
			if turn != 1 {
				t.Fatalf("unexpected tester turn %d", turn)
			}
			invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
				AgentType: func() string { return agentShared.PipelineAgentTester },
			}, "validate_work", map[string]any{
				"challenge_id":     "task-execute-challenge-1",
				"requesting_agent": "inspector",
				"status":           "passed",
				"summary":          "Tests are in place and define the execute work clearly.",
			})
			return &testerpipeline.TaskStageResult{
				SuiteResult: &tester.TestSuiteResult{},
			}, nil
		},
	}

	engineerWorker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
				AgentType: func() string { return agentShared.PipelineAgentEngineer },
			}, "handoff_next", map[string]any{
				"target_agents": []string{"inspector"},
				"reason":        "Implementation is complete for this iteration.",
				"request":       "Re-inspect the combined execute output.",
			})
			return &WorkerResult{
				ChangedFiles: []string{"app/main.go"},
				TaskResult:   &engineer.TaskResult{TaskID: "task-execute", Success: true, Output: "engineer complete"},
			}, nil
		},
	}
	designerWorker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
				AgentType: func() string { return agentShared.PipelineAgentDesigner },
			}, "handoff_next", map[string]any{
				"target_agents": []string{"inspector"},
				"reason":        "Design work is complete for this iteration.",
				"request":       "Re-inspect the combined execute output.",
			})
			return &WorkerResult{
				ChangedFiles: []string{"ui/button.tsx"},
				WorkerType:   WorkerDesigner,
				TaskResult:   &engineer.TaskResult{TaskID: "task-execute", Success: true, Output: "designer complete"},
			}, nil
		},
	}

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Inspector:    inspector,
		Tester:       testerAgent,
		Worker:       engineerWorker,
		CoWorkers:    []WorkerAgent{designerWorker},
		PhaseTimeout: time.Second,
		MaxLoops:     2,
	})

	if err := exec.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := exec.WaitDone(3 * time.Second); err != nil {
		t.Fatalf("wait done: %v", err)
	}

	snap := exec.Pipeline()
	if snap.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", snap.Status)
	}
	if snap.WorkerOutput == nil || len(snap.WorkerOutput.ChangedFiles) != 1 {
		t.Fatalf("primary worker output missing: %#v", snap.WorkerOutput)
	}
	if len(snap.CoWorkerOutputs) != 1 || snap.CoWorkerOutputs[0].WorkerType != WorkerDesigner {
		t.Fatalf("co-worker outputs = %#v", snap.CoWorkerOutputs)
	}
	if engineerWorker.task == nil || designerWorker.task == nil {
		t.Fatal("expected execute workers to receive structured pipeline task input")
	}
	if protocol := engineerWorker.task.Context["pipeline_protocol"]; protocol == nil {
		t.Fatal("engineer task missing pipeline protocol context")
	}
}

func TestTDDExecutor_MaxLoopsExhausted(t *testing.T) {
	pipeline := newTestPipeline("task-loop")

	inspector := &scriptedInspector{
		run: func(ctx context.Context, _ *agentShared.PipelineTaskInput, turn int) (*inspPipeline.InspectionStageResult, error) {
			invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
				AgentType:   func() string { return agentShared.PipelineAgentInspector },
				InspectorOT: true,
			}, "handoff_next", map[string]any{
				"target_agents": []string{"engineer"},
				"reason":        fmt.Sprintf("loop %d needs more implementation", turn),
				"request":       "Implement the next requested change.",
			})
			return &inspPipeline.InspectionStageResult{
				Criteria: &inspShared.InspectorCriteria{TaskID: pipeline.TaskID},
			}, nil
		},
	}

	engineerWorker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
				AgentType: func() string { return agentShared.PipelineAgentEngineer },
			}, "handoff_next", map[string]any{
				"target_agents": []string{"inspector"},
				"reason":        "Need another inspection cycle.",
				"request":       "Inspect the latest execute output and decide again.",
			})
			return &WorkerResult{ChangedFiles: []string{"app/main.go"}}, nil
		},
	}

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:  pipeline,
		Inspector: inspector,
		Tester: &scriptedTester{run: func(_ context.Context, _ *agentShared.PipelineTaskInput, _ int) (*testerpipeline.TaskStageResult, error) {
			return nil, errors.New("unexpected tester turn")
		}},
		Worker:       engineerWorker,
		PhaseTimeout: time.Second,
		MaxLoops:     1,
	})

	if err := exec.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := exec.WaitDone(3 * time.Second)
	if err == nil || !strings.Contains(err.Error(), ErrMaxLoopsExhausted.Error()) {
		t.Fatalf("wait done err = %v, want max loops exhausted", err)
	}

	snap := exec.Pipeline()
	if snap.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", snap.Status)
	}
}

func TestTDDExecutor_EventsEmitted(t *testing.T) {
	pipeline := newTestPipeline("task-events")
	var events []PipelineEvent

	inspector := &scriptedInspector{
		run: func(ctx context.Context, _ *agentShared.PipelineTaskInput, turn int) (*inspPipeline.InspectionStageResult, error) {
			if turn == 1 {
				invokeProtocolSkill(t, ctx, agentShared.PipelineProtocolSkillConfig{
					AgentType:   func() string { return agentShared.PipelineAgentInspector },
					InspectorOT: true,
				}, "handoff_to_ot", map[string]any{"summary": "done"})
			}
			return &inspPipeline.InspectionStageResult{
				Criteria: &inspShared.InspectorCriteria{TaskID: pipeline.TaskID},
			}, nil
		},
	}

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:  pipeline,
		Inspector: inspector,
		Tester: &scriptedTester{run: func(_ context.Context, _ *agentShared.PipelineTaskInput, _ int) (*testerpipeline.TaskStageResult, error) {
			return nil, errors.New("unexpected tester turn")
		}},
		Worker:       &mockWorker{},
		PhaseTimeout: time.Second,
		MaxLoops:     1,
		OnEvent: func(evt PipelineEvent) {
			events = append(events, evt)
		},
	})

	if err := exec.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := exec.WaitDone(3 * time.Second); err != nil {
		t.Fatalf("wait done: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("events = %d, want at least 2", len(events))
	}
	if events[0].Stage != pipelineStageInspect {
		t.Fatalf("first stage = %q, want %q", events[0].Stage, pipelineStageInspect)
	}
	if events[len(events)-1].NewStatus != StatusCompleted {
		t.Fatalf("last status = %s, want %s", events[len(events)-1].NewStatus, StatusCompleted)
	}
}

func TestTDDExecutor_ResolveTaskPrompt(t *testing.T) {
	pipeline := newTestPipeline("task-prompt")
	pipeline.TaskPrompt = "fallback"
	pipeline.AgentPrompts = map[WorkerType]string{
		WorkerEngineer: "engineer prompt",
		WorkerDesigner: "designer prompt",
	}
	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:  pipeline,
		Inspector: &scriptedInspector{},
		Tester:    &scriptedTester{},
		Worker:    &mockWorker{},
	})

	if got := exec.resolveTaskPrompt(WorkerEngineer); got != "engineer prompt" {
		t.Fatalf("engineer prompt = %q, want engineer prompt", got)
	}
	if got := exec.resolveTaskPrompt(WorkerDesigner); got != "designer prompt" {
		t.Fatalf("designer prompt = %q, want designer prompt", got)
	}
	if got := exec.resolveTaskPrompt("unknown"); got != "fallback" {
		t.Fatalf("fallback prompt = %q, want fallback", got)
	}
}

func TestTDDExecutor_ApplyWorkerPrompt(t *testing.T) {
	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:  newTestPipeline("task-prompt-apply"),
		Inspector: &scriptedInspector{},
		Tester:    &scriptedTester{},
		Worker:    &mockWorker{},
	})
	worker := &mockWorker{}
	prior := &WorkerResult{ChangedFiles: []string{"main.go"}}
	exec.applyWorkerPrompt(worker, "scoped prompt", prior)
	if worker.prompt != "scoped prompt" {
		t.Fatalf("prompt = %q, want scoped prompt", worker.prompt)
	}
	if worker.prior == nil || len(worker.prior.ChangedFiles) != 1 {
		t.Fatalf("prior output not applied: %#v", worker.prior)
	}
}
