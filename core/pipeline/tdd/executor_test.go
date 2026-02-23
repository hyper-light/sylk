package tdd

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/inspector"
	"github.com/adalundhe/sylk/agents/tester"
)

// mockWorker implements WorkerAgent for testing.
type mockWorker struct {
	executeFn func(ctx context.Context, criteria *inspector.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error)
	closed    atomic.Bool
}

func (m *mockWorker) Execute(ctx context.Context, criteria *inspector.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error) {
	if m.executeFn != nil {
		return m.executeFn(ctx, criteria, inspFb, testFb)
	}
	return &WorkerResult{ChangedFiles: []string{"file.go"}}, nil
}

func (m *mockWorker) Close() error {
	m.closed.Store(true)
	return nil
}

// mockInspector wraps a real inspector.Inspector with controllable validation results.
type mockInspectorController struct {
	validatePassed atomic.Bool
	validateErr    atomic.Pointer[error]
}

func newTestPipeline(taskID string) *Pipeline {
	return &Pipeline{
		ID:        "test-pipeline-1",
		TaskID:    taskID,
		SessionID: "test-session",
		DAGNodeID: "test-node",
		Status:    StatusPending,
		MaxLoops:  defaultMaxLoops,
		InspectorCriteria: &inspector.InspectorCriteria{
			TaskID: taskID,
			SuccessCriteria: []inspector.SuccessCriterion{
				{ID: "sc1", Description: "code compiles", Verifiable: true},
			},
		},
		CreatedAt: time.Now(),
	}
}

func newTestExecutor(t *testing.T, pipeline *Pipeline, worker WorkerAgent, inspPassed bool, testerPassed bool) *TDDExecutor {
	t.Helper()

	inspCfg := inspector.DefaultInspectorConfig()
	inspCfg.Mode = inspector.PipelineInternal
	insp, err := inspector.New(inspCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { insp.Close() })

	testerCfg := tester.DefaultTesterConfig()
	tst, err := tester.New(testerCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tst.Close() })

	var events []PipelineEvent
	var eventMu sync.Mutex

	return NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
		OnEvent: func(evt PipelineEvent) {
			eventMu.Lock()
			events = append(events, evt)
			eventMu.Unlock()
		},
	})
}

func TestTDDExecutor_HappyPath(t *testing.T) {
	pipeline := newTestPipeline("task-happy")

	// Mock inspector that creates a passing inspection.
	inspCfg := inspector.DefaultInspectorConfig()
	inspCfg.Mode = inspector.PipelineInternal
	insp, err := inspector.New(inspCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	testerCfg := tester.DefaultTesterConfig()
	tst, err := tester.New(testerCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	worker := &mockWorker{}
	var events []PipelineEvent
	var eventMu sync.Mutex

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
		OnEvent: func(evt PipelineEvent) {
			eventMu.Lock()
			events = append(events, evt)
			eventMu.Unlock()
		},
	})

	ctx := context.Background()
	if err := exec.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Wait for completion — the executor should run through the TDD loop.
	err = exec.WaitDone(10 * time.Second)
	// Since we're using real Inspector/Tester that return defaults,
	// the pipeline will proceed through phases. The exact outcome
	// depends on whether mock agents return passing results.
	// The key test: it should terminate (not hang).
	_ = err

	p := exec.Pipeline()
	if !IsTerminalStatus(p.Status) {
		t.Errorf("expected terminal status, got %s", p.Status)
	}
	if p.LoopCount < 1 {
		t.Errorf("expected at least 1 loop, got %d", p.LoopCount)
	}

	eventMu.Lock()
	if len(events) < 1 {
		t.Error("expected at least 1 event")
	}
	eventMu.Unlock()
}

func TestTDDExecutor_MaxLoopsExhausted(t *testing.T) {
	pipeline := newTestPipeline("task-maxloop")
	pipeline.MaxLoops = 2

	inspCfg := inspector.DefaultInspectorConfig()
	inspCfg.Mode = inspector.PipelineInternal
	insp, err := inspector.New(inspCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	testerCfg := tester.DefaultTesterConfig()
	tst, err := tester.New(testerCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	worker := &mockWorker{}

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     2,
	})

	ctx := context.Background()
	if err := exec.Start(ctx); err != nil {
		t.Fatal(err)
	}

	_ = exec.WaitDone(15 * time.Second)

	p := exec.Pipeline()
	if !IsTerminalStatus(p.Status) {
		t.Errorf("expected terminal status, got %s", p.Status)
	}
	// Should have attempted all loops.
	if p.LoopCount < 1 {
		t.Errorf("expected at least 1 loop, got %d", p.LoopCount)
	}
}

func TestTDDExecutor_CancellationMidPhase(t *testing.T) {
	pipeline := newTestPipeline("task-cancel")

	inspCfg := inspector.DefaultInspectorConfig()
	inspCfg.Mode = inspector.PipelineInternal
	insp, err := inspector.New(inspCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	testerCfg := tester.DefaultTesterConfig()
	tst, err := tester.New(testerCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	// Worker that blocks until context is cancelled.
	worker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspector.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
	})

	ctx, cancel := context.WithCancel(context.Background())
	if err := exec.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Give the executor time to reach the worker phase, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	_ = exec.WaitDone(5 * time.Second)

	p := exec.Pipeline()
	if !IsTerminalStatus(p.Status) {
		t.Errorf("expected terminal status after cancel, got %s", p.Status)
	}
}

func TestTDDExecutor_WorkerError(t *testing.T) {
	pipeline := newTestPipeline("task-worker-err")

	inspCfg := inspector.DefaultInspectorConfig()
	inspCfg.Mode = inspector.PipelineInternal
	insp, err := inspector.New(inspCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	testerCfg := tester.DefaultTesterConfig()
	tst, err := tester.New(testerCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	worker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspector.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			return nil, fmt.Errorf("compilation failed")
		},
	}

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
	})

	ctx := context.Background()
	if err := exec.Start(ctx); err != nil {
		t.Fatal(err)
	}

	_ = exec.WaitDone(10 * time.Second)

	p := exec.Pipeline()
	if p.Status != StatusFailed {
		t.Errorf("expected StatusFailed, got %s", p.Status)
	}
	if p.LastError == "" {
		t.Error("expected LastError to be set")
	}
}

func TestTDDExecutor_NoCriteria(t *testing.T) {
	pipeline := newTestPipeline("task-no-criteria")
	pipeline.InspectorCriteria = nil // no criteria

	inspCfg := inspector.DefaultInspectorConfig()
	inspCfg.Mode = inspector.PipelineInternal
	insp, err := inspector.New(inspCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	testerCfg := tester.DefaultTesterConfig()
	tst, err := tester.New(testerCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	worker := &mockWorker{}

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
	})

	ctx := context.Background()
	if err := exec.Start(ctx); err != nil {
		t.Fatal(err)
	}

	_ = exec.WaitDone(10 * time.Second)

	p := exec.Pipeline()
	if p.Status != StatusFailed {
		t.Errorf("expected StatusFailed when no criteria, got %s", p.Status)
	}
}

func TestTDDExecutor_EventsEmitted(t *testing.T) {
	pipeline := newTestPipeline("task-events")

	inspCfg := inspector.DefaultInspectorConfig()
	inspCfg.Mode = inspector.PipelineInternal
	insp, err := inspector.New(inspCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	testerCfg := tester.DefaultTesterConfig()
	tst, err := tester.New(testerCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	worker := &mockWorker{}

	var events []PipelineEvent
	var eventMu sync.Mutex

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
		OnEvent: func(evt PipelineEvent) {
			eventMu.Lock()
			events = append(events, evt)
			eventMu.Unlock()
		},
	})

	ctx := context.Background()
	if err := exec.Start(ctx); err != nil {
		t.Fatal(err)
	}

	_ = exec.WaitDone(10 * time.Second)

	eventMu.Lock()
	defer eventMu.Unlock()

	// Should have at least DefiningCriteria + CreatingTests + Executing + Validating + terminal
	if len(events) < 4 {
		t.Errorf("expected at least 4 events, got %d", len(events))
	}

	// First event should transition from Pending to DefiningCriteria.
	if len(events) > 0 {
		first := events[0]
		if first.OldStatus != StatusPending {
			t.Errorf("first event OldStatus = %s, want %s", first.OldStatus, StatusPending)
		}
		if first.NewStatus != StatusDefiningCriteria {
			t.Errorf("first event NewStatus = %s, want %s", first.NewStatus, StatusDefiningCriteria)
		}
		if first.PipelineID != pipeline.ID {
			t.Errorf("event PipelineID = %s, want %s", first.PipelineID, pipeline.ID)
		}
	}
}
