package tdd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
)

const (
	phaseTDDLoop concurrency.PipelinePhase = "tdd_loop"

	defaultPhaseTimeout     = 5 * time.Minute
	defaultMaxLoops         = 5
	maxProtocolEventHistory = 8
)

const (
	pipelineStageInspect = "inspect"
	pipelineStageTest    = "test"
	pipelineStageExecute = "execute"
)

type protocolTurn struct {
	Stage            string
	ActiveAgents     []string
	RequestedBy      string
	Mode             agentShared.PipelineTurnMode
	Request          string
	PendingChallenge *agentShared.PipelineProtocolChallenge
}

type turnOutcome struct {
	Action    *agentShared.PipelineTurnAction
	Processed []agentShared.PipelineValidationProcessing
}

type workerTurnResult struct {
	agentType string
	output    *WorkerResult
	action    *agentShared.PipelineTurnAction
	processed []agentShared.PipelineValidationProcessing
	err       error
}

// domainFromWorkerType maps a WorkerType to its corresponding ValidationDomain.
func domainFromWorkerType(wt WorkerType) inspShared.ValidationDomain {
	if wt == WorkerDesigner {
		return inspShared.DomainDesign
	}
	return inspShared.DomainCode
}

// TDDExecutor runs the pipeline runtime with a deterministic inspector entry
// point and agent-directed handoffs inside the pipeline.
type TDDExecutor struct {
	pipeline     *Pipeline
	runner       *concurrency.PipelineRunner
	inspector    InspectorAgent
	tester       TesterAgent
	worker       WorkerAgent
	coWorkers    []WorkerAgent
	activityPub  events.ActivityPublisher
	phaseTimeout time.Duration
	maxLoops     int
	onEvent      func(PipelineEvent)
	logger       *slog.Logger
	mu           sync.RWMutex

	currentTurn       protocolTurn
	pendingValidation *agentShared.PipelineValidationRecord
	protocolHistory   []agentShared.PipelineProtocolEvent
	lastTurnStage     string
	challengeSeq      int
}

// TDDExecutorConfig holds parameters for creating a TDDExecutor.
type TDDExecutorConfig struct {
	Pipeline     *Pipeline
	Inspector    InspectorAgent
	Tester       TesterAgent
	Worker       WorkerAgent
	CoWorkers    []WorkerAgent
	ActivityPub  events.ActivityPublisher
	PhaseTimeout time.Duration
	MaxLoops     int
	OnEvent      func(PipelineEvent)
	Logger       *slog.Logger
}

// NewTDDExecutor creates a TDDExecutor that manages the pipeline runtime.
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
		currentTurn: protocolTurn{
			Stage:        pipelineStageInspect,
			ActiveAgents: []string{agentShared.PipelineAgentInspector},
			RequestedBy:  agentShared.PipelineAgentInspector,
			Mode:         agentShared.PipelineTurnModeSingle,
			Request:      "Inspect the task, define or refine the criteria, and decide who should act next.",
		},
	}

	runner.RegisterPhase(phaseTDDLoop, e.tddLoop)
	return e
}

// Start begins pipeline execution.
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
	if err := e.setActiveTurn(e.currentTurn.Stage, e.currentTurn.ActiveAgents, "pipeline inspector bootstrapping task"); err != nil {
		return e.fail(err)
	}

	for {
		if ctx.Err() != nil {
			return e.fail(ctx.Err())
		}

		turn := e.snapshotTurn()
		if e.shouldIncrementLoop(turn.Stage) {
			e.incrementLoop()
			if e.currentLoop() > e.maxLoops {
				return e.fail(ErrMaxLoopsExhausted)
			}
		}

		var (
			outcome *turnOutcome
			err     error
		)
		switch turn.Stage {
		case pipelineStageInspect:
			outcome, err = e.runInspectorTurn(ctx, turn)
		case pipelineStageTest:
			outcome, err = e.runTesterTurn(ctx, turn)
		case pipelineStageExecute:
			outcome, err = e.runWorkerTurn(ctx, turn)
		default:
			err = fmt.Errorf("unknown pipeline stage %q", turn.Stage)
		}
		if err != nil {
			return e.fail(err)
		}
		if outcome == nil || outcome.Action == nil {
			return e.fail(fmt.Errorf("pipeline turn at stage %s completed without a protocol action", turn.Stage))
		}
		if err := e.applyTurnOutcome(outcome); err != nil {
			return e.fail(err)
		}
		if outcome.Action.Type == agentShared.PipelineProtocolActionOT {
			return e.complete()
		}
	}
}

func (e *TDDExecutor) runInspectorTurn(ctx context.Context, turn protocolTurn) (*turnOutcome, error) {
	e.emitAgentActivity(events.EventTypeAgentAction, agentShared.PipelineAgentInspector, "Inspector is evaluating the task and deciding the next handoff")
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	state := agentShared.NewPipelineProtocolState(e.protocolSnapshot(turn))
	ctx = agentShared.WithPipelineProtocolState(ctx, state)
	task := e.pipelineTaskInput(turn, agentShared.PipelineAgentInspector)
	inspection, err := e.inspector.RunTask(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("inspector turn: %w", err)
	}

	e.mu.Lock()
	if inspection != nil {
		if inspection.Criteria != nil {
			e.pipeline.InspectorCriteria = inspection.Criteria
		}
		if inspection.Result != nil {
			e.pipeline.InspectorResult = inspection.Result
		}
	}
	e.mu.Unlock()

	return &turnOutcome{
		Action:    state.TerminalAction(),
		Processed: state.ProcessedValidations(),
	}, nil
}

func (e *TDDExecutor) runTesterTurn(ctx context.Context, turn protocolTurn) (*turnOutcome, error) {
	e.emitAgentActivity(events.EventTypeAgentAction, agentShared.PipelineAgentTester, "Tester is responding to the current inspection challenge")
	ctx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
	defer cancel()

	state := agentShared.NewPipelineProtocolState(e.protocolSnapshot(turn))
	ctx = agentShared.WithPipelineProtocolState(ctx, state)
	task := e.pipelineTaskInput(turn, agentShared.PipelineAgentTester)
	stage, err := e.tester.TestTask(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("tester turn: %w", err)
	}

	e.mu.Lock()
	if stage != nil {
		e.pipeline.TesterTests = stage.SuiteResult
		success := stage.SuiteResult != nil && stage.SuiteResult.Failed == 0 && stage.SuiteResult.Errors == 0
		e.pipeline.TesterResult = &tester.TesterResponse{
			Success:      success,
			SuiteResult:  stage.SuiteResult,
			CreatedFiles: append([]string(nil), stage.CreatedFiles...),
			Timestamp:    time.Now(),
		}
	}
	e.mu.Unlock()

	return &turnOutcome{
		Action:    state.TerminalAction(),
		Processed: state.ProcessedValidations(),
	}, nil
}

func (e *TDDExecutor) runWorkerTurn(ctx context.Context, turn protocolTurn) (*turnOutcome, error) {
	criteria, inspFb, testFb := e.workerInputs()
	activeWorkers, err := e.workerAgentsForTurn(turn.ActiveAgents)
	if err != nil {
		return nil, err
	}

	resultsCh := make(chan workerTurnResult, len(activeWorkers))
	for _, entry := range activeWorkers {
		go func(agentType string, worker WorkerAgent) {
			turnCtx, cancel := context.WithTimeout(ctx, e.phaseTimeout)
			defer cancel()

			state := agentShared.NewPipelineProtocolState(e.protocolSnapshot(turn))
			turnCtx = agentShared.WithPipelineProtocolState(turnCtx, state)
			task := e.pipelineTaskInput(turn, agentType)
			e.applyWorkerPrompt(worker, e.resolveTaskPrompt(WorkerType(agentType)), e.primaryWorkerOutput())
			e.applyWorkerTask(worker, task)

			output, execErr := worker.Execute(turnCtx, criteria, inspFb, testFb)
			resultsCh <- workerTurnResult{
				agentType: agentType,
				output:    output,
				action:    state.TerminalAction(),
				processed: state.ProcessedValidations(),
				err:       execErr,
			}
		}(entry.agentType, entry.worker)
	}

	results := make([]workerTurnResult, 0, len(activeWorkers))
	for range activeWorkers {
		result := <-resultsCh
		if result.err != nil {
			return nil, fmt.Errorf("%s turn: %w", result.agentType, result.err)
		}
		if result.action == nil {
			return nil, fmt.Errorf("%s ended its turn without a protocol action", result.agentType)
		}
		results = append(results, result)
	}

	e.storeWorkerOutputs(results)
	return e.mergeWorkerTurnResults(results), nil
}

type workerBinding struct {
	agentType string
	worker    WorkerAgent
}

func (e *TDDExecutor) workerAgentsForTurn(activeAgents []string) ([]workerBinding, error) {
	if len(activeAgents) == 0 {
		return nil, fmt.Errorf("execute turn has no active workers")
	}
	coWorkerMap := make(map[WorkerType]WorkerAgent, len(e.pipeline.CoWorkerTypes))
	for idx, wt := range e.pipeline.CoWorkerTypes {
		if idx < len(e.coWorkers) {
			coWorkerMap[wt] = e.coWorkers[idx]
		}
	}

	bindings := make([]workerBinding, 0, len(activeAgents))
	for _, agentType := range activeAgents {
		switch WorkerType(agentType) {
		case e.pipeline.WorkerType:
			bindings = append(bindings, workerBinding{agentType: agentType, worker: e.worker})
		case WorkerEngineer, WorkerDesigner:
			worker := coWorkerMap[WorkerType(agentType)]
			if worker == nil {
				return nil, fmt.Errorf("worker %s is not registered in this pipeline", agentType)
			}
			bindings = append(bindings, workerBinding{agentType: agentType, worker: worker})
		default:
			return nil, fmt.Errorf("agent %s is not an execute-stage worker", agentType)
		}
	}
	return bindings, nil
}

func (e *TDDExecutor) storeWorkerOutputs(results []workerTurnResult) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var primary *WorkerResult
	coOutputs := make([]*WorkerResult, 0, len(results))
	for _, result := range results {
		if result.output == nil {
			continue
		}
		result.output.WorkerType = WorkerType(result.agentType)
		if WorkerType(result.agentType) == e.pipeline.WorkerType {
			primary = result.output
			continue
		}
		coOutputs = append(coOutputs, result.output)
	}
	e.pipeline.WorkerOutput = primary
	e.pipeline.CoWorkerOutputs = coOutputs
}

func (e *TDDExecutor) mergeWorkerTurnResults(results []workerTurnResult) *turnOutcome {
	if len(results) == 1 {
		return &turnOutcome{
			Action:    results[0].action,
			Processed: append([]agentShared.PipelineValidationProcessing(nil), results[0].processed...),
		}
	}

	processed := make([]agentShared.PipelineValidationProcessing, 0, len(results))
	handoffs := make([]*agentShared.PipelineTurnAction, 0, len(results))
	validations := make([]*agentShared.PipelineTurnAction, 0, len(results))
	for _, result := range results {
		processed = append(processed, result.processed...)
		switch result.action.Type {
		case agentShared.PipelineProtocolActionHandoff:
			handoffs = append(handoffs, result.action)
		case agentShared.PipelineProtocolActionValidate:
			validations = append(validations, result.action)
		default:
			handoffs = append(handoffs, result.action)
		}
	}

	if len(validations) == 1 && len(handoffs) == 0 {
		return &turnOutcome{Action: validations[0], Processed: processed}
	}
	if len(validations) == 0 && len(handoffs) == 1 {
		return &turnOutcome{Action: handoffs[0], Processed: processed}
	}
	if len(validations) == 0 && len(handoffs) > 1 && equivalentHandoffs(handoffs) {
		return &turnOutcome{Action: handoffs[0], Processed: processed}
	}

	summary := "execute cohort reported divergent next steps; inspector must reconcile the next handoff"
	return &turnOutcome{
		Action: &agentShared.PipelineTurnAction{
			Type:         agentShared.PipelineProtocolActionHandoff,
			AgentType:    "execute-cohort",
			TargetAgents: []string{agentShared.PipelineAgentInspector},
			Mode:         agentShared.PipelineTurnModeSingle,
			Reason:       "execute cohort requires inspector arbitration",
			Request:      summary,
		},
		Processed: processed,
	}
}

func equivalentHandoffs(actions []*agentShared.PipelineTurnAction) bool {
	if len(actions) < 2 {
		return true
	}
	base := actions[0]
	for _, action := range actions[1:] {
		if action == nil || base == nil {
			return false
		}
		if strings.TrimSpace(base.Request) != strings.TrimSpace(action.Request) ||
			strings.TrimSpace(base.Reason) != strings.TrimSpace(action.Reason) ||
			string(base.Mode) != string(action.Mode) ||
			!equalStringSlices(base.TargetAgents, action.TargetAgents) {
			return false
		}
	}
	return true
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (e *TDDExecutor) applyTurnOutcome(outcome *turnOutcome) error {
	if outcome == nil || outcome.Action == nil {
		return fmt.Errorf("pipeline turn outcome is required")
	}

	e.mu.Lock()
	e.lastTurnStage = e.currentTurn.Stage
	e.mu.Unlock()
	e.applyProcessedValidations(outcome.Processed)
	action := outcome.Action
	e.appendProtocolEvent(action)

	switch action.Type {
	case agentShared.PipelineProtocolActionHandoff:
		challenge := &agentShared.PipelineProtocolChallenge{
			ID:              e.nextChallengeID(),
			RequestingAgent: action.AgentType,
			TargetAgents:    append([]string(nil), action.TargetAgents...),
			Mode:            string(action.Mode),
			Reason:          action.Reason,
			Request:         action.Request,
			RequiredOutput:  append([]string(nil), action.RequiredOutput...),
			References:      append([]string(nil), action.References...),
		}
		e.pendingValidation = nil
		e.currentTurn = protocolTurn{
			Stage:            stageForAgents(action.TargetAgents),
			ActiveAgents:     append([]string(nil), action.TargetAgents...),
			RequestedBy:      action.AgentType,
			Mode:             action.Mode,
			Request:          action.Request,
			PendingChallenge: challenge,
		}
		return e.setActiveTurn(e.currentTurn.Stage, e.currentTurn.ActiveAgents, pipelineStageMessage(e.currentTurn.Stage, e.currentTurn.ActiveAgents))
	case agentShared.PipelineProtocolActionValidate:
		if action.Validation == nil {
			return fmt.Errorf("validation action missing validation record")
		}
		e.currentTurn = protocolTurn{
			Stage:        stageForAgents([]string{action.Validation.RequestingAgent}),
			ActiveAgents: []string{action.Validation.RequestingAgent},
			RequestedBy:  action.Validation.RespondingAgent,
			Mode:         agentShared.PipelineTurnModeSingle,
			Request:      fmt.Sprintf("Process validation response for challenge %s and decide the next handoff.", action.Validation.ChallengeID),
		}
		e.pendingValidation = cloneValidationRecord(action.Validation)
		return e.setActiveTurn(e.currentTurn.Stage, e.currentTurn.ActiveAgents, pipelineStageMessage(e.currentTurn.Stage, e.currentTurn.ActiveAgents))
	case agentShared.PipelineProtocolActionOT:
		e.mu.Lock()
		e.pipeline.LastMessage = strings.TrimSpace(action.Summary)
		e.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("unknown protocol action %q", action.Type)
	}
}

func (e *TDDExecutor) applyProcessedValidations(entries []agentShared.PipelineValidationProcessing) {
	if len(entries) == 0 || e.pendingValidation == nil {
		return
	}
	for _, entry := range entries {
		e.protocolHistory = appendBoundedProtocolEvent(e.protocolHistory, agentShared.PipelineProtocolEvent{
			Type:      "process_validation",
			AgentType: entry.AgentType,
			Targets:   append([]string(nil), entry.NextTargets...),
			Summary:   strings.TrimSpace(entry.Summary),
		})
		if strings.TrimSpace(entry.ChallengeID) == strings.TrimSpace(e.pendingValidation.ChallengeID) {
			e.pendingValidation = nil
		}
	}
}

func (e *TDDExecutor) appendProtocolEvent(action *agentShared.PipelineTurnAction) {
	if action == nil {
		return
	}
	summary := strings.TrimSpace(action.Request)
	if summary == "" {
		summary = strings.TrimSpace(action.Summary)
	}
	targets := append([]string(nil), action.TargetAgents...)
	if action.Validation != nil {
		targets = []string{action.Validation.RequestingAgent}
		summary = strings.TrimSpace(action.Validation.Summary)
	}
	e.protocolHistory = appendBoundedProtocolEvent(e.protocolHistory, agentShared.PipelineProtocolEvent{
		Type:      string(action.Type),
		AgentType: action.AgentType,
		Targets:   targets,
		Summary:   summary,
	})
}

func appendBoundedProtocolEvent(history []agentShared.PipelineProtocolEvent, evt agentShared.PipelineProtocolEvent) []agentShared.PipelineProtocolEvent {
	history = append(history, evt)
	if len(history) <= maxProtocolEventHistory {
		return history
	}
	return append([]agentShared.PipelineProtocolEvent(nil), history[len(history)-maxProtocolEventHistory:]...)
}

func (e *TDDExecutor) shouldIncrementLoop(stage string) bool {
	stage = strings.TrimSpace(stage)
	if stage != pipelineStageInspect {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.pipeline.LoopCount == 0 || e.lastTurnStage == pipelineStageExecute
}

func (e *TDDExecutor) snapshotTurn() protocolTurn {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := e.currentTurn
	out.ActiveAgents = append([]string(nil), e.currentTurn.ActiveAgents...)
	if e.currentTurn.PendingChallenge != nil {
		challenge := *e.currentTurn.PendingChallenge
		challenge.TargetAgents = append([]string(nil), e.currentTurn.PendingChallenge.TargetAgents...)
		challenge.RequiredOutput = append([]string(nil), e.currentTurn.PendingChallenge.RequiredOutput...)
		challenge.References = append([]string(nil), e.currentTurn.PendingChallenge.References...)
		out.PendingChallenge = &challenge
	}
	return out
}

func stageForAgents(agentTypes []string) string {
	for _, agentType := range agentTypes {
		switch strings.TrimSpace(agentType) {
		case agentShared.PipelineAgentInspector:
			return pipelineStageInspect
		case agentShared.PipelineAgentTester:
			return pipelineStageTest
		case agentShared.PipelineAgentEngineer, agentShared.PipelineAgentDesigner:
			return pipelineStageExecute
		}
	}
	return pipelineStageInspect
}

func pipelineStageMessage(stage string, activeAgents []string) string {
	switch strings.TrimSpace(stage) {
	case pipelineStageInspect:
		return "inspector evaluating the task"
	case pipelineStageTest:
		return "tester responding to the current challenge"
	case pipelineStageExecute:
		if len(activeAgents) > 1 {
			return "execute cohort implementing the current request"
		}
		return "worker implementing the current request"
	default:
		return "pipeline running"
	}
}

func (e *TDDExecutor) protocolSnapshot(turn protocolTurn) *agentShared.PipelineProtocolSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	roster := []agentShared.PipelineProtocolAgent{
		{AgentType: agentShared.PipelineAgentInspector, Role: "entrypoint and final acceptance"},
		{AgentType: agentShared.PipelineAgentTester, Role: "test authoring and execution"},
		{AgentType: string(e.pipeline.WorkerType), Role: "implementation"},
	}
	for _, wt := range e.pipeline.CoWorkerTypes {
		roster = append(roster, agentShared.PipelineProtocolAgent{
			AgentType: string(wt),
			Role:      "execute cohort peer",
		})
	}

	snapshot := &agentShared.PipelineProtocolSnapshot{
		Iteration:      e.pipeline.LoopCount,
		Roster:         roster,
		ActiveAgents:   append([]string(nil), turn.ActiveAgents...),
		RequestedBy:    turn.RequestedBy,
		Mode:           string(turn.Mode),
		CurrentRequest: strings.TrimSpace(turn.Request),
		RecentEvents:   append([]agentShared.PipelineProtocolEvent(nil), e.protocolHistory...),
	}
	if turn.PendingChallenge != nil {
		challenge := *turn.PendingChallenge
		challenge.TargetAgents = append([]string(nil), turn.PendingChallenge.TargetAgents...)
		challenge.RequiredOutput = append([]string(nil), turn.PendingChallenge.RequiredOutput...)
		challenge.References = append([]string(nil), turn.PendingChallenge.References...)
		snapshot.PendingChallenge = &challenge
	}
	if e.pendingValidation != nil {
		snapshot.PendingValidation = cloneValidationRecord(e.pendingValidation)
	}
	return snapshot
}

func cloneValidationRecord(record *agentShared.PipelineValidationRecord) *agentShared.PipelineValidationRecord {
	if record == nil {
		return nil
	}
	out := *record
	out.EvidenceRefs = append([]string(nil), record.EvidenceRefs...)
	out.MissingInputs = append([]string(nil), record.MissingInputs...)
	out.RecommendedNextAgents = append([]string(nil), record.RecommendedNextAgents...)
	return &out
}

func (e *TDDExecutor) workerInputs() (*inspShared.InspectorCriteria, *InspectorFeedback, *TesterFeedback) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.pipeline.InspectorCriteria, &InspectorFeedback{
			Criteria:   e.pipeline.InspectorCriteria,
			WorkerType: e.pipeline.WorkerType,
		}, &TesterFeedback{
			Response:    e.pipeline.TesterResult,
			FailedTests: failedTestNames(e.pipeline.TesterTests),
			WorkerType:  e.pipeline.WorkerType,
		}
}

// resolveTaskPrompt returns the scoped prompt for a worker type,
// falling back to the pipeline task prompt.
func (e *TDDExecutor) resolveTaskPrompt(wt WorkerType) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.pipeline.AgentPrompts != nil {
		if prompt := strings.TrimSpace(e.pipeline.AgentPrompts[wt]); prompt != "" {
			return prompt
		}
	}
	return strings.TrimSpace(e.pipeline.TaskPrompt)
}

// applyWorkerPrompt sets the task prompt and optional prior output on a worker.
func (e *TDDExecutor) applyWorkerPrompt(w WorkerAgent, prompt string, priorOutput *WorkerResult) {
	if setter, ok := w.(TaskPromptSetter); ok {
		setter.SetTaskPrompt(prompt)
	}
	if setter, ok := w.(PriorOutputSetter); ok {
		setter.SetPriorOutput(priorOutput)
	}
}

func (e *TDDExecutor) applyWorkerTask(w WorkerAgent, task *agentShared.PipelineTaskInput) {
	if setter, ok := w.(PipelineTaskSetter); ok {
		setter.SetPipelineTask(task)
	}
}

func (e *TDDExecutor) primaryWorkerOutput() *WorkerResult {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.pipeline.WorkerOutput
}

func (e *TDDExecutor) pipelineFiles() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]string(nil), e.pipeline.Files...)
}

func (e *TDDExecutor) pipelineTaskInput(turn protocolTurn, agentType string) *agentShared.PipelineTaskInput {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ctx := map[string]any{
		"pipeline_stage":    turn.Stage,
		"agent_type":        string(e.pipeline.WorkerType),
		"pipeline_protocol": agentShared.PipelineProtocolSnapshotMap(e.protocolSnapshot(turn)),
	}
	if e.pipeline.TaskSlug != "" {
		ctx["task_slug"] = e.pipeline.TaskSlug
	}
	if len(e.pipeline.Files) > 0 {
		ctx["affected_files"] = append([]string(nil), e.pipeline.Files...)
	}

	return &agentShared.PipelineTaskInput{
		NodeID:        e.pipeline.DAGNodeID,
		DAGID:         e.pipeline.DAGID,
		TaskID:        e.pipeline.TaskID,
		AgentType:     agentType,
		Prompt:        e.pipeline.TaskPrompt,
		Context:       ctx,
		ParentResults: pipelineParentResults(e.pipeline),
		SessionID:     e.pipeline.SessionID,
	}
}

func pipelineParentResults(p *Pipeline) map[string]any {
	if p == nil {
		return nil
	}
	results := map[string]any{}
	if p.WorkerOutput != nil {
		results["execute"] = map[string]any{
			"state": "succeeded",
			"output": map[string]any{
				"changed_files": append([]string(nil), p.WorkerOutput.ChangedFiles...),
			},
		}
	}
	if p.InspectorResult != nil {
		results["inspect"] = map[string]any{
			"state": "succeeded",
			"output": map[string]any{
				"passed":          p.InspectorResult.Passed,
				"criteria_failed": append([]string(nil), p.InspectorResult.CriteriaFailed...),
			},
		}
	}
	if p.TesterResult != nil {
		results["test"] = map[string]any{
			"state": "succeeded",
			"output": map[string]any{
				"passed": p.TesterResult.Success,
			},
		}
	}
	if len(results) == 0 {
		return nil
	}
	return results
}

func (e *TDDExecutor) setActiveTurn(stage string, activeAgents []string, message string) error {
	e.mu.Lock()
	old := e.pipeline.Status
	if old == StatusPending {
		if err := ValidateTransition(old, StatusActive); err != nil {
			e.mu.Unlock()
			return err
		}
		e.pipeline.Status = StatusActive
	}
	e.pipeline.CurrentStage = strings.TrimSpace(stage)
	e.pipeline.ActiveAgents = append([]string(nil), activeAgents...)
	e.pipeline.LastMessage = strings.TrimSpace(message)
	loop := e.pipeline.LoopCount
	e.mu.Unlock()

	e.emitEvent(old, StatusActive, loop, "", stage, activeAgents, message)
	return nil
}

func (e *TDDExecutor) complete() error {
	e.mu.Lock()
	old := e.pipeline.Status
	e.pipeline.Status = StatusCompleted
	e.pipeline.CompletedAt = time.Now()
	loop := e.pipeline.LoopCount
	stage := e.pipeline.CurrentStage
	activeAgents := append([]string(nil), e.pipeline.ActiveAgents...)
	message := strings.TrimSpace(e.pipeline.LastMessage)
	e.mu.Unlock()

	e.emitEvent(old, StatusCompleted, loop, "", stage, activeAgents, message)
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
	stage := e.pipeline.CurrentStage
	activeAgents := append([]string(nil), e.pipeline.ActiveAgents...)
	message := strings.TrimSpace(e.pipeline.LastMessage)
	e.mu.Unlock()

	if !IsTerminalStatus(old) {
		e.emitEvent(old, StatusFailed, loop, err.Error(), stage, activeAgents, message)
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
		_ = cw.Close()
	}
}

// CloseAgents closes all pipeline-scoped agents owned by this executor.
func (e *TDDExecutor) CloseAgents() {
	if e.inspector != nil {
		_ = e.inspector.Close()
	}
	if e.tester != nil {
		_ = e.tester.Close()
	}
	if e.worker != nil {
		_ = e.worker.Close()
	}
	e.CloseCoWorkers()
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

func (e *TDDExecutor) nextChallengeID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.challengeSeq++
	return fmt.Sprintf("%s-challenge-%d", strings.TrimSpace(e.pipeline.TaskID), e.challengeSeq)
}

func (e *TDDExecutor) emitEvent(old, new PipelineStatus, loop int, errMsg, stage string, activeAgents []string, message string) {
	if e.onEvent == nil {
		return
	}
	e.mu.RLock()
	panelPipelineID := logicalPipelineID(e.pipeline.TaskID, e.pipeline.ID)
	evt := PipelineEvent{
		PipelineID:        panelPipelineID,
		RuntimePipelineID: e.pipeline.ID,
		TaskID:            e.pipeline.TaskID,
		TaskSlug:          e.pipeline.TaskSlug,
		SessionID:         e.pipeline.SessionID,
		DAGID:             e.pipeline.DAGID,
		DAGNodeID:         e.pipeline.DAGNodeID,
		OldStatus:         old,
		NewStatus:         new,
		WorkerType:        e.pipeline.WorkerType,
		LoopCount:         loop,
		MaxLoops:          e.maxLoops,
		Stage:             strings.TrimSpace(stage),
		ActiveAgents:      append([]string(nil), activeAgents...),
		Message:           strings.TrimSpace(message),
		Timestamp:         time.Now(),
		Error:             errMsg,
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
	pipelineID := logicalPipelineID(e.pipeline.TaskID, e.pipeline.ID)
	taskID := e.pipeline.TaskID
	taskSlug := e.pipeline.TaskSlug
	runtimePipelineID := e.pipeline.ID
	workerType := string(e.pipeline.WorkerType)
	e.mu.RUnlock()

	evt := events.NewActivityEvent(eventType, sessionID, content)
	evt.AgentID = agentName
	evt.Category = "pipeline"
	evt.Data["pipeline_id"] = pipelineID
	evt.Data["task_id"] = taskID
	if taskSlug != "" {
		evt.Data["task_slug"] = taskSlug
	}
	if runtimePipelineID != "" && runtimePipelineID != pipelineID {
		evt.Data["runtime_pipeline_id"] = runtimePipelineID
	}
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
