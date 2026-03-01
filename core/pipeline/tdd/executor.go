package tdd

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	inspPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/tester"
	pipelinetester "github.com/adalundhe/sylk/agents/tester/pipeline"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
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

// domainFromWorkerType maps a WorkerType to its corresponding ValidationDomain.
func domainFromWorkerType(wt WorkerType) inspShared.ValidationDomain {
	if wt == WorkerDesigner {
		return inspShared.DomainDesign
	}
	return inspShared.DomainCode
}

// TDDExecutor runs the TDD loop: define → test → implement → validate → loop.
type TDDExecutor struct {
	pipeline     *Pipeline
	bus          *PipelineBus
	runner       *concurrency.PipelineRunner
	inspector    *inspPipeline.PipelineInspector
	tester       *pipelinetester.PipelineTester
	worker       WorkerAgent
	coWorkers    []WorkerAgent
	activityPub  events.ActivityPublisher
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
	Inspector    *inspPipeline.PipelineInspector
	Tester       *pipelinetester.PipelineTester
	Worker       WorkerAgent
	CoWorkers    []WorkerAgent
	ActivityPub  events.ActivityPublisher
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
		coWorkers:    cfg.CoWorkers,
		activityPub:  cfg.ActivityPub,
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
	e.emitAgentActivity(events.EventTypeAgentAction, "inspector-pipeline", "Defining validation criteria")
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	e.mu.RLock()
	taskID := e.pipeline.TaskID
	existing := e.pipeline.InspectorCriteria
	e.mu.RUnlock()

	if existing == nil {
		return fmt.Errorf("no initial criteria provided for task %s", taskID)
	}

	if existing.Domain == "" {
		existing.Domain = domainFromWorkerType(e.pipeline.WorkerType)
	}

	e.inspector.DefineCriteria(taskID, existing)
	return nil
}

func (e *TDDExecutor) phaseCreateTests(ctx context.Context) error {
	if err := e.transitionStatus(StatusCreatingTests); err != nil {
		return err
	}
	e.emitAgentActivity(events.EventTypeAgentAction, "tester-pipeline", "Creating test suite")
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	e.mu.RLock()
	taskID := e.pipeline.TaskID
	workerType := string(e.pipeline.WorkerType)
	e.mu.RUnlock()

	req := &tester.TesterRequest{
		Intent:     tester.IntentRunTests,
		Files:      []string{taskID},
		WorkerType: workerType,
	}
	resp, err := e.tester.HandleRequest(ctx, req)
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
	workerAgent := string(e.pipeline.WorkerType)
	e.emitAgentActivity(events.EventTypeAgentAction, workerAgent, "Executing implementation")
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	criteria, inspFb, testFb := e.workerInputs()

	e.mu.RLock()
	wt := e.pipeline.WorkerType
	e.mu.RUnlock()

	// Set task prompt on primary worker.
	taskPrompt := e.resolveTaskPrompt(wt)
	e.applyWorkerPrompt(e.worker, taskPrompt, nil)

	// Execute primary worker.
	result, err := e.worker.Execute(ctx, criteria, inspFb, testFb)
	if err != nil {
		return fmt.Errorf("worker execute: %w", err)
	}
	result.WorkerType = wt

	// Execute co-workers sequentially.
	coResults := e.executeCoWorkers(ctx, criteria, inspFb, testFb, result)

	// Merge results.
	merged := mergeWorkerResults(result, coResults)

	e.mu.Lock()
	e.pipeline.WorkerOutput = merged
	e.pipeline.CoWorkerOutputs = coResults
	e.mu.Unlock()

	return nil
}

// workerInputs extracts criteria and feedback under RLock.
func (e *TDDExecutor) workerInputs() (*inspShared.InspectorCriteria, *InspectorFeedback, *TesterFeedback) {
	e.mu.RLock()
	criteria := e.pipeline.InspectorCriteria
	inspResult := e.pipeline.InspectorResult
	testerResult := e.pipeline.TesterResult
	loop := e.pipeline.LoopCount
	wt := e.pipeline.WorkerType
	e.mu.RUnlock()

	var inspFb *InspectorFeedback
	if inspResult != nil && loop > 1 {
		inspFb = &InspectorFeedback{
			Criteria:   criteria,
			WorkerType: wt,
			Feedback: &inspShared.InspectorFeedback{
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
				WorkerType:  wt,
			}
		}
	}

	return criteria, inspFb, testFb
}

// executeCoWorkers runs each co-worker sequentially, passing the primary
// result as context. Co-worker failures are non-fatal.
func (e *TDDExecutor) executeCoWorkers(ctx context.Context, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback, primaryResult *WorkerResult) []*WorkerResult {
	if len(e.coWorkers) == 0 {
		return nil
	}

	e.mu.RLock()
	coTypes := e.pipeline.CoWorkerTypes
	e.mu.RUnlock()

	results := make([]*WorkerResult, 0, len(e.coWorkers))
	for i, cw := range e.coWorkers {
		var cwType WorkerType
		if i < len(coTypes) {
			cwType = coTypes[i]
		}

		taskPrompt := e.resolveTaskPrompt(cwType)
		e.applyWorkerPrompt(cw, taskPrompt, primaryResult)

		r, err := cw.Execute(ctx, criteria, inspFb, testFb)
		if err != nil {
			e.logger.Warn("co-worker failed", "type", cwType, "error", err)
			continue // Non-fatal: primary result is authoritative.
		}
		r.WorkerType = cwType
		results = append(results, r)
	}
	return results
}

// resolveTaskPrompt returns the scoped prompt for a worker type,
// falling back to the pipeline-level task prompt.
func (e *TDDExecutor) resolveTaskPrompt(wt WorkerType) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.pipeline.AgentPrompts != nil {
		if prompt, ok := e.pipeline.AgentPrompts[wt]; ok {
			return prompt
		}
	}
	return e.pipeline.TaskPrompt
}

// applyWorkerPrompt sets the task prompt and optional prior output on a worker
// via the TaskPromptSetter and PriorOutputSetter interfaces.
func (e *TDDExecutor) applyWorkerPrompt(w WorkerAgent, prompt string, priorOutput *WorkerResult) {
	if setter, ok := w.(TaskPromptSetter); ok {
		setter.SetTaskPrompt(prompt)
	}
	if setter, ok := w.(PriorOutputSetter); ok {
		setter.SetPriorOutput(priorOutput)
	}
}

func (e *TDDExecutor) phaseValidate(ctx context.Context) (bool, error) {
	if err := e.transitionStatus(StatusValidating); err != nil {
		return false, err
	}
	e.emitAgentActivity(events.EventTypeAgentAction, "inspector-pipeline", "Validating results")
	e.emitAgentActivity(events.EventTypeAgentAction, "tester-pipeline", "Running validation tests")
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	e.mu.RLock()
	taskID := e.pipeline.TaskID
	workerOutput := e.pipeline.WorkerOutput
	e.mu.RUnlock()

	files := workerOutput.ChangedFiles

	scope := concurrency.NewGoroutineScope(ctx, fmt.Sprintf("tdd-validate-%s", e.pipeline.ID), nil)

	var (
		inspResult   *inspShared.InspectorResult
		testerResult *tester.TesterResponse
		inspErr      error
		testerErr    error
		resultMu     sync.Mutex
	)

	workerType := string(e.pipeline.WorkerType)

	// Inspector validation goroutine.
	if err := scope.Go("inspector-validate", validationTimeout, func(ctx context.Context) error {
		r, err := e.inspector.ValidateAgainstCriteria(ctx, taskID, files, workerType)
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
			Intent:     tester.IntentRunTests,
			Files:      files,
			WorkerType: workerType,
		}
		r, err := e.tester.HandleRequest(ctx, req)
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
	e.emitAgentActivity(events.EventTypeSuccess, string(e.pipeline.WorkerType), "Pipeline completed successfully")
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
		e.emitAgentActivity(events.EventTypeFailure, string(e.pipeline.WorkerType), fmt.Sprintf("Pipeline failed: %s", err.Error()))
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

// CloseCoWorkers closes all co-worker agents.
func (e *TDDExecutor) CloseCoWorkers() {
	for _, cw := range e.coWorkers {
		cw.Close()
	}
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
		WorkerType: e.pipeline.WorkerType,
		LoopCount:  loop,
		MaxLoops:   e.maxLoops,
		Timestamp:  time.Now(),
		Error:      errMsg,
	}
	e.mu.RUnlock()
	e.onEvent(evt)
}

// emitAgentActivity publishes a synthetic activity event for a pipeline agent
// phase transition to the ActivityPublisher. No-op when activityPub is nil.
func (e *TDDExecutor) emitAgentActivity(eventType events.EventType, agentName, content string) {
	if e.activityPub == nil {
		return
	}
	e.mu.RLock()
	sessionID := e.pipeline.SessionID
	pipelineID := e.pipeline.ID
	workerType := string(e.pipeline.WorkerType)
	e.mu.RUnlock()

	evt := events.NewActivityEvent(eventType, sessionID, content)
	evt.AgentID = agentName
	evt.Category = "pipeline"
	evt.Data["pipeline_id"] = pipelineID
	evt.Data["agent_type"] = agentName
	evt.Data["worker_type"] = workerType
	e.activityPub.PublishActivity(evt)
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
