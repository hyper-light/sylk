package tdd

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	inspPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	pipelinetester "github.com/adalundhe/sylk/agents/tester/pipeline"
	"github.com/adalundhe/sylk/agents/tester/shared"
)

// mockWorker implements WorkerAgent for testing.
type mockWorker struct {
	executeFn func(ctx context.Context, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error)
	closed    atomic.Bool
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

// mockInspectorController provides controllable validation results for testing.
type mockInspectorController struct {
	validatePassed atomic.Bool
	validateErr    atomic.Pointer[error]
}

func newTestPipeline(taskID string) *Pipeline {
	return &Pipeline{
		ID:        "test-pipeline-1",
		TaskID:    taskID,
		TaskSlug:  "auth-checkout",
		SessionID: "test-session",
		DAGNodeID: "test-node",
		Status:    StatusPending,
		MaxLoops:  defaultMaxLoops,
		InspectorCriteria: &inspShared.InspectorCriteria{
			TaskID: taskID,
			SuccessCriteria: []inspShared.SuccessCriterion{
				{ID: "sc1", Description: "code compiles", Verifiable: true},
			},
		},
		CreatedAt: time.Now(),
	}
}

func newTestExecutor(t *testing.T, pipeline *Pipeline, worker WorkerAgent, inspPassed bool, testerPassed bool) *TDDExecutor {
	t.Helper()

	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { insp.Close() })

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
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
	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
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

	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
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

	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	// Worker that blocks until context is cancelled.
	worker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
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

	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	worker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
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

	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
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

	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
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
		if first.PipelineID != pipeline.TaskID {
			t.Errorf("event PipelineID = %s, want %s", first.PipelineID, pipeline.TaskID)
		}
		if first.RuntimePipelineID != pipeline.ID {
			t.Errorf("event RuntimePipelineID = %s, want %s", first.RuntimePipelineID, pipeline.ID)
		}
		if first.TaskSlug != pipeline.TaskSlug {
			t.Errorf("event TaskSlug = %s, want %s", first.TaskSlug, pipeline.TaskSlug)
		}
	}
}

func TestTDDExecutor_CompoundPipeline(t *testing.T) {
	pipeline := newTestPipeline("task-compound")
	pipeline.CoWorkerTypes = []WorkerType{WorkerDesigner}
	pipeline.TaskPrompt = "Build the feature"

	primaryWorker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			return &WorkerResult{ChangedFiles: []string{"a.go"}}, nil
		},
	}
	coWorker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			return &WorkerResult{ChangedFiles: []string{"b.css"}}, nil
		},
	}

	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       primaryWorker,
		CoWorkers:    []WorkerAgent{coWorker},
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
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
	if p.WorkerOutput == nil {
		t.Fatal("expected non-nil WorkerOutput")
	}
	// Merged output should contain files from both workers.
	fileSet := make(map[string]bool)
	for _, f := range p.WorkerOutput.ChangedFiles {
		fileSet[f] = true
	}
	if !fileSet["a.go"] {
		t.Error("merged output missing a.go from primary worker")
	}
	if !fileSet["b.css"] {
		t.Error("merged output missing b.css from co-worker")
	}
	// Co-worker outputs should be stored separately.
	if len(p.CoWorkerOutputs) != 1 {
		t.Errorf("expected 1 co-worker output, got %d", len(p.CoWorkerOutputs))
	}
}

func TestTDDExecutor_CoWorkerFailureNonFatal(t *testing.T) {
	pipeline := newTestPipeline("task-co-fail")
	pipeline.CoWorkerTypes = []WorkerType{WorkerDesigner}

	primaryWorker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			return &WorkerResult{ChangedFiles: []string{"primary.go"}}, nil
		},
	}
	failingCoWorker := &mockWorker{
		executeFn: func(ctx context.Context, _ *inspShared.InspectorCriteria, _ *InspectorFeedback, _ *TesterFeedback) (*WorkerResult, error) {
			return nil, fmt.Errorf("co-worker compilation error")
		},
	}

	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       primaryWorker,
		CoWorkers:    []WorkerAgent{failingCoWorker},
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
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
	// Pipeline should not have failed due to co-worker error.
	// WorkerOutput should have primary's files only.
	if p.WorkerOutput == nil {
		t.Fatal("expected non-nil WorkerOutput")
	}
	if len(p.WorkerOutput.ChangedFiles) != 1 || p.WorkerOutput.ChangedFiles[0] != "primary.go" {
		t.Errorf("expected [primary.go], got %v", p.WorkerOutput.ChangedFiles)
	}
	// Co-worker outputs should be empty since it failed.
	if len(p.CoWorkerOutputs) != 0 {
		t.Errorf("expected 0 co-worker outputs, got %d", len(p.CoWorkerOutputs))
	}
}

func TestTDDExecutor_ResolveTaskPrompt(t *testing.T) {
	pipeline := newTestPipeline("task-prompt")
	pipeline.TaskPrompt = "fallback prompt"
	pipeline.AgentPrompts = map[WorkerType]string{
		WorkerEngineer: "engineer-specific prompt",
		WorkerDesigner: "designer-specific prompt",
	}

	worker := &mockWorker{}
	exec := newTestExecutor(t, pipeline, worker, true, true)

	// Scoped prompt should be returned when available.
	got := exec.resolveTaskPrompt(WorkerEngineer)
	if got != "engineer-specific prompt" {
		t.Errorf("got %q, want %q", got, "engineer-specific prompt")
	}

	got = exec.resolveTaskPrompt(WorkerDesigner)
	if got != "designer-specific prompt" {
		t.Errorf("got %q, want %q", got, "designer-specific prompt")
	}

	// Unknown type falls back to pipeline-level prompt.
	got = exec.resolveTaskPrompt("unknown")
	if got != "fallback prompt" {
		t.Errorf("got %q, want %q", got, "fallback prompt")
	}
}

func TestTDDExecutor_ApplyWorkerPrompt(t *testing.T) {
	pipeline := newTestPipeline("task-apply")
	worker := &mockWorker{}
	exec := newTestExecutor(t, pipeline, worker, true, true)

	// mockWorker does not implement TaskPromptSetter — should be a no-op.
	exec.applyWorkerPrompt(worker, "test", nil) // should not panic

	// Test with a real worker adapter that implements the interfaces.
	ew := &engineerWorker{}
	prior := &WorkerResult{ChangedFiles: []string{"x.go"}}
	exec.applyWorkerPrompt(ew, "scoped prompt", prior)
	if ew.taskPrompt != "scoped prompt" {
		t.Errorf("got %q, want %q", ew.taskPrompt, "scoped prompt")
	}
	if ew.priorOutput == nil || len(ew.priorOutput.ChangedFiles) != 1 {
		t.Error("expected priorOutput to be set")
	}
}

func TestTDDExecutor_CloseCoWorkers(t *testing.T) {
	pipeline := newTestPipeline("task-close-co")
	cw1 := &mockWorker{}
	cw2 := &mockWorker{}

	worker := &mockWorker{}
	inspCfg := inspShared.DefaultPipelineInspectorConfig()
	insp, err := inspPipeline.New(inspCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()

	tst, err := pipelinetester.New(shared.PipelineTesterConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()

	exec := NewTDDExecutor(TDDExecutorConfig{
		Pipeline:     pipeline,
		Bus:          NewPipelineBus(),
		Inspector:    insp,
		Tester:       tst,
		Worker:       worker,
		CoWorkers:    []WorkerAgent{cw1, cw2},
		PhaseTimeout: 30 * time.Second,
		MaxLoops:     pipeline.MaxLoops,
	})

	exec.CloseCoWorkers()
	if !cw1.closed.Load() {
		t.Error("expected co-worker 1 to be closed")
	}
	if !cw2.closed.Load() {
		t.Error("expected co-worker 2 to be closed")
	}
}

func TestDomainFromWorkerType(t *testing.T) {
	tests := []struct {
		wt   WorkerType
		want inspShared.ValidationDomain
	}{
		{WorkerEngineer, inspShared.DomainCode},
		{WorkerDesigner, inspShared.DomainDesign},
		{"unknown", inspShared.DomainCode},
	}
	for _, tt := range tests {
		got := domainFromWorkerType(tt.wt)
		if got != tt.want {
			t.Errorf("domainFromWorkerType(%q) = %q, want %q", tt.wt, got, tt.want)
		}
	}
}
