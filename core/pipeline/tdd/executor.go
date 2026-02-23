package tdd

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/inspector"
	"github.com/adalundhe/sylk/agents/tester"
	"github.com/adalundhe/sylk/core/concurrency"
)

const (
	phaseTDDLoop concurrency.PipelinePhase = "tdd_loop"

	defaultPhaseTimeout    = 5 * time.Minute
	defaultMaxLoops        = 5
	validationGoroutines   = 2
	validationTimeout      = 10 * time.Minute
	scopeShutdownGrace     = 10 * time.Second
	scopeShutdownHard      = 30 * time.Second
)

// TDDExecutor runs the TDD loop: define → test → implement → validate → loop.
type TDDExecutor struct {
	pipeline     *Pipeline
	bus          *PipelineBus
	runner       *concurrency.PipelineRunner
	inspector    *inspector.Inspector
	tester       *tester.Tester
	worker       WorkerAgent
	phaseTimeout time.Duration
	maxLoops     int
	onEvent      func(PipelineEvent)
	logger       *slog.Logger
	mu           sync.RWMutex
}

// TDDExecutorConfig holds parameters for creating a TDDExecutor.
type TDDExecutorConfig struct {
	Pipeline     *Pipeline
	Bus          *PipelineBus
	Inspector    *inspector.Inspector
	Tester       *tester.Tester
	Worker       WorkerAgent
	PhaseTimeout time.Duration
	MaxLoops     int
	OnEvent      func(PipelineEvent)
	Logger       *slog.Logger
}

// NewTDDExecutor creates a TDDExecutor that manages the TDD loop.
func NewTDDExecutor(cfg TDDExecutorConfig) *TDDExecutor {
	phaseTimeout := cfg.PhaseTimeout
	if phaseTimeout == 0 {
		phaseTimeout = defaultPhaseTimeout
	}
	maxLoops := cfg.MaxLoops
	if maxLoops <= 0 {
		maxLoops = defaultMaxLoops
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	runner := concurrency.NewPipelineRunner(concurrency.PipelineRunnerConfig{
		ID: fmt.Sprintf("tdd-%s", cfg.Pipeline.ID),
	})
	runner.SetPhaseOrder([]concurrency.PipelinePhase{phaseTDDLoop})

	e := &TDDExecutor{
		pipeline:     cfg.Pipeline,
		bus:          cfg.Bus,
		runner:       runner,
		inspector:    cfg.Inspector,
		tester:       cfg.Tester,
		worker:       cfg.Worker,
		phaseTimeout: phaseTimeout,
		maxLoops:     maxLoops,
		onEvent:      cfg.OnEvent,
		logger:       logger,
	}

	runner.RegisterPhase(phaseTDDLoop, e.tddLoop)
	return e
}

// Start begins the TDD loop execution.
func (e *TDDExecutor) Start(ctx context.Context) error {
	e.mu.Lock()
	e.pipeline.StartedAt = time.Now()
	e.mu.Unlock()
	return e.runner.Start(ctx)
}

// Stop gracefully stops the executor.
func (e *TDDExecutor) Stop() error {
	return e.runner.Stop()
}

// WaitDone blocks until the runner reaches a terminal state or the timeout elapses.
func (e *TDDExecutor) WaitDone(timeout time.Duration) error {
	type result struct{ err error }
	ch := make(chan result, 2)

	go func() { ch <- result{e.runner.WaitForState(concurrency.StateStopped, timeout)} }()
	go func() { ch <- result{e.runner.WaitForState(concurrency.StateFailed, timeout)} }()

	r := <-ch
	if r.err == nil {
		return e.runner.Error()
	}
	// First wait failed (timeout), try the second.
	r2 := <-ch
	if r2.err == nil {
		return e.runner.Error()
	}
	return r.err
}

// Pipeline returns the current pipeline state (read-locked).
func (e *TDDExecutor) Pipeline() Pipeline {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return *e.pipeline
}

// tddLoop is the single phase registered with PipelineRunner.
func (e *TDDExecutor) tddLoop(ctx context.Context) error {
	for e.currentLoop() < e.maxLoops {
		e.incrementLoop()

		if err := e.phaseDefineCriteria(ctx); err != nil {
			return e.fail(err)
		}
		if err := e.phaseCreateTests(ctx); err != nil {
			return e.fail(err)
		}
		if err := e.phaseWorkerExecute(ctx); err != nil {
			return e.fail(err)
		}
		passed, err := e.phaseValidate(ctx)
		if err != nil {
			return e.fail(err)
		}
		if passed {
			return e.complete()
		}
		// Loop back to DefiningCriteria.
	}

	return e.fail(ErrMaxLoopsExhausted)
}

func (e *TDDExecutor) phaseDefineCriteria(ctx context.Context) error {
	if err := e.transitionStatus(StatusDefiningCriteria); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	e.mu.RLock()
	taskID := e.pipeline.TaskID
	existing := e.pipeline.InspectorCriteria
	e.mu.RUnlock()

	if existing == nil {
		return fmt.Errorf("no initial criteria provided for task %s", taskID)
	}

	e.inspector.DefineCriteria(taskID, existing)
	return nil
}

func (e *TDDExecutor) phaseCreateTests(ctx context.Context) error {
	if err := e.transitionStatus(StatusCreatingTests); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	e.mu.RLock()
	taskID := e.pipeline.TaskID
	e.mu.RUnlock()

	req := &tester.TesterRequest{
		Intent: tester.IntentRunTests,
		Files:  []string{taskID},
	}
	resp, err := e.tester.Handle(ctx, req)
	if err != nil {
		return fmt.Errorf("tester create tests: %w", err)
	}

	e.mu.Lock()
	e.pipeline.TesterTests = resp.SuiteResult
	e.pipeline.TesterResult = resp
	e.mu.Unlock()

	return nil
}

func (e *TDDExecutor) phaseWorkerExecute(ctx context.Context) error {
	if err := e.transitionStatus(StatusExecuting); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	e.mu.RLock()
	criteria := e.pipeline.InspectorCriteria
	inspResult := e.pipeline.InspectorResult
	testerResult := e.pipeline.TesterResult
	loop := e.pipeline.LoopCount
	e.mu.RUnlock()

	var inspFb *InspectorFeedback
	if inspResult != nil && loop > 1 {
		inspFb = &InspectorFeedback{
			Criteria: criteria,
			Feedback: &inspector.InspectorFeedback{
				Loop:   inspResult.LoopCount,
				Passed: inspResult.Passed,
				Issues: inspResult.Issues,
			},
		}
	}

	var testFb *TesterFeedback
	if testerResult != nil && testerResult.SuiteResult != nil && loop > 1 {
		failed := failedTestNames(testerResult.SuiteResult)
		if len(failed) > 0 {
			testFb = &TesterFeedback{
				Response:    testerResult,
				FailedTests: failed,
			}
		}
	}

	result, err := e.worker.Execute(ctx, criteria, inspFb, testFb)
	if err != nil {
		return fmt.Errorf("worker execute: %w", err)
	}

	e.mu.Lock()
	e.pipeline.WorkerOutput = result
	e.mu.Unlock()

	return nil
}

func (e *TDDExecutor) phaseValidate(ctx context.Context) (bool, error) {
	if err := e.transitionStatus(StatusValidating); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	e.mu.RLock()
	taskID := e.pipeline.TaskID
	workerOutput := e.pipeline.WorkerOutput
	e.mu.RUnlock()

	files := workerOutput.ChangedFiles

	scope := concurrency.NewGoroutineScope(ctx, fmt.Sprintf("tdd-validate-%s", e.pipeline.ID), nil)

	var (
		inspResult   *inspector.InspectorResult
		testerResult *tester.TesterResponse
		inspErr      error
		testerErr    error
		resultMu     sync.Mutex
	)

	// Inspector validation goroutine.
	if err := scope.Go("inspector-validate", validationTimeout, func(ctx context.Context) error {
		r, err := e.inspector.ValidateAgainstCriteria(ctx, taskID, files)
		resultMu.Lock()
		inspResult = r
		inspErr = err
		resultMu.Unlock()
		return err
	}); err != nil {
		return false, fmt.Errorf("spawn inspector validation: %w", err)
	}

	// Tester validation goroutine.
	if err := scope.Go("tester-validate", validationTimeout, func(ctx context.Context) error {
		req := &tester.TesterRequest{
			Intent: tester.IntentRunTests,
			Files:  files,
		}
		r, err := e.tester.Handle(ctx, req)
		resultMu.Lock()
		testerResult = r
		testerErr = err
		resultMu.Unlock()
		return err
	}); err != nil {
		return false, fmt.Errorf("spawn tester validation: %w", err)
	}

	// Wait for both goroutines to complete.
	if err := scope.Shutdown(scopeShutdownGrace, scopeShutdownHard); err != nil {
		return false, fmt.Errorf("validation shutdown: %w", err)
	}

	resultMu.Lock()
	defer resultMu.Unlock()

	// Store results.
	e.mu.Lock()
	e.pipeline.InspectorResult = inspResult
	e.pipeline.TesterResult = testerResult
	e.mu.Unlock()

	if inspErr != nil {
		return false, fmt.Errorf("inspector validation: %w", inspErr)
	}
	if testerErr != nil {
		return false, fmt.Errorf("tester validation: %w", testerErr)
	}

	inspPassed := inspResult != nil && inspResult.Passed
	testerPassed := testerResult != nil && testerResult.Success
	return inspPassed && testerPassed, nil
}

func (e *TDDExecutor) transitionStatus(to PipelineStatus) error {
	e.mu.Lock()
	old := e.pipeline.Status
	if err := ValidateTransition(old, to); err != nil {
		e.mu.Unlock()
		return err
	}
	e.pipeline.Status = to
	loop := e.pipeline.LoopCount
	e.mu.Unlock()

	e.emitEvent(old, to, loop, "")
	return nil
}

func (e *TDDExecutor) complete() error {
	e.mu.Lock()
	old := e.pipeline.Status
	e.pipeline.Status = StatusCompleted
	e.pipeline.CompletedAt = time.Now()
	loop := e.pipeline.LoopCount
	e.mu.Unlock()

	e.emitEvent(old, StatusCompleted, loop, "")
	return nil
}

func (e *TDDExecutor) fail(err error) error {
	e.mu.Lock()
	old := e.pipeline.Status
	if !IsTerminalStatus(old) {
		e.pipeline.Status = StatusFailed
		e.pipeline.LastError = err.Error()
		e.pipeline.CompletedAt = time.Now()
	}
	loop := e.pipeline.LoopCount
	e.mu.Unlock()

	if !IsTerminalStatus(old) {
		e.emitEvent(old, StatusFailed, loop, err.Error())
	}
	return err
}

// markCancelled sets the pipeline to cancelled if not already terminal (thread-safe).
func (e *TDDExecutor) markCancelled() {
	e.mu.Lock()
	old := e.pipeline.Status
	if !IsTerminalStatus(old) {
		e.pipeline.Status = StatusCancelled
		e.pipeline.CompletedAt = time.Now()
	}
	e.mu.Unlock()
}

func (e *TDDExecutor) currentLoop() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.pipeline.LoopCount
}

func (e *TDDExecutor) incrementLoop() {
	e.mu.Lock()
	e.pipeline.LoopCount++
	e.mu.Unlock()
}

func (e *TDDExecutor) emitEvent(old, new PipelineStatus, loop int, errMsg string) {
	if e.onEvent == nil {
		return
	}
	e.mu.RLock()
	evt := PipelineEvent{
		PipelineID: e.pipeline.ID,
		TaskID:     e.pipeline.TaskID,
		SessionID:  e.pipeline.SessionID,
		OldStatus:  old,
		NewStatus:  new,
		LoopCount:  loop,
		Timestamp:  time.Now(),
		Error:      errMsg,
	}
	e.mu.RUnlock()
	e.onEvent(evt)
}

func failedTestNames(suite *tester.TestSuiteResult) []string {
	if suite == nil {
		return nil
	}
	var names []string
	for _, r := range suite.Results {
		if r.Status == tester.StatusFailed {
			names = append(names, r.Name)
		}
	}
	return names
}
