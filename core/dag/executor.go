package dag

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

type NodeDispatcher interface {
	Dispatch(ctx context.Context, node *Node, parentResults map[string]*NodeResult) (*NodeResult, error)
}

type budgetRetryWaiter interface {
	WaitForBudget(ctx context.Context, retryAt time.Time) error
}

type Executor struct {
	mu sync.RWMutex

	policy       ExecutionPolicy
	dag          *DAG
	state        DAGState
	currentLayer int
	startTime    time.Time
	nodeResults  map[string]*NodeResult
	nodesRunning int32
	cancelled    atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc

	// layerCtx is derived from ctx for the currently-executing layer.
	// When a node in the layer fails unrecoverably and the failure would
	// block downstream work, the mid-layer classifier cancels layerCtx to
	// halt sibling nodes that are still executing — so the user-facing
	// retry/abort/skip dialog fires promptly instead of waiting for every
	// sibling to finish (potentially 10+ minutes of wasted engineer/tester
	// work per layer). Sibling goroutines observe layerCtx.Done() via
	// nodeContext() / acquireLayerSlot() / prepareAttempt() and exit with
	// a context-cancelled NodeResult, which resetLayerForRetry treats the
	// same as a natural failure on retry.
	//
	// layerCtx is reset (fresh ctx + cancel) at the start of each layer
	// iteration in executeAndCheckLayer. It is never shared across layers.
	layerCtx    context.Context
	layerCancel context.CancelFunc

	sem chan struct{}

	handlersMu sync.RWMutex
	handlers   []EventHandler
	dispatcher NodeDispatcher
	closed     atomic.Bool

	layerGate    LayerGate
	layerRetries map[int]int
	budgetRetry  map[string]time.Time

	scope *concurrency.GoroutineScope
}

// SetLayerGate assigns a gate function invoked between layers.
func (e *Executor) SetLayerGate(gate LayerGate) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.layerGate = gate
}

func NewExecutor(policy ExecutionPolicy, scope *concurrency.GoroutineScope) *Executor {
	return &Executor{
		policy:      policy,
		nodeResults: make(map[string]*NodeResult),
		budgetRetry: make(map[string]time.Time),
		handlers:    make([]EventHandler, 0),
		scope:       scope,
	}
}

func (e *Executor) Execute(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (*DAGResult, error) {
	if e.closed.Load() {
		return nil, ErrExecutorClosed
	}
	if err := e.validateExecution(dag); err != nil {
		return nil, err
	}
	if err := e.initializeExecution(ctx, dag, dispatcher); err != nil {
		return nil, err
	}

	e.emitEvent(&Event{
		Type:      EventDAGStarted,
		DAGID:     dag.ID(),
		Timestamp: time.Now(),
	})

	result := e.executeLayers()
	e.emitCompletionEvent(dag, result)
	// Terminal DAG outcomes are represented in result.State/result.Error.
	// The returned error is reserved for setup faults where no valid result
	// could be produced.
	return result, nil
}

func (e *Executor) validateExecution(dag *DAG) error {
	if dag.IsValidated() {
		return nil
	}
	return ValidateDAG(dag)
}

func (e *Executor) initializeExecution(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state == DAGStateRunning {
		return ErrDAGAlreadyRunning
	}

	e.dag = dag
	e.state = DAGStateRunning
	e.currentLayer = 0
	e.startTime = time.Now()
	e.nodeResults = make(map[string]*NodeResult)
	e.budgetRetry = make(map[string]time.Time)
	e.layerRetries = make(map[int]int)
	e.dispatcher = dispatcher
	e.cancelled.Store(false)

	e.ctx, e.cancel = context.WithCancel(ctx)
	e.configureSemaphore()
	dag.ResetNodeStates()
	return nil
}

func (e *Executor) configureSemaphore() {
	if e.policy.MaxConcurrency > 0 {
		e.sem = make(chan struct{}, e.policy.MaxConcurrency)
	}
}

func (e *Executor) emitCompletionEvent(dag *DAG, result *DAGResult) {
	eventType := resultEventType(result.State)
	data := map[string]any{
		"duration":  result.Duration,
		"succeeded": result.NodesSucceeded,
		"failed":    result.NodesFailed,
		"skipped":   result.NodesSkipped,
	}
	if result.Error != nil {
		data["error"] = result.Error.Error()
	}
	e.emitEvent(&Event{
		Type:      eventType,
		DAGID:     dag.ID(),
		Timestamp: time.Now(),
		Data:      data,
	})
}

// layerOutcome is the result of processing a layer, including its gate.
type layerOutcome int

const (
	layerOutcomeContinue layerOutcome = iota
	layerOutcomeStop
	layerOutcomeRetry
)

// maxLayerRetries is the maximum number of retries per layer.
const maxLayerRetries = 3

func resultEventType(state DAGState) EventType {
	switch state {
	case DAGStateSucceeded:
		return EventDAGCompleted
	case DAGStateFailed:
		return EventDAGFailed
	case DAGStateCancelled:
		return EventDAGCancelled
	default:
		return EventDAGCompleted
	}
}

func (e *Executor) executeLayers() *DAGResult {
	layers := e.dag.ExecutionOrder()

	for layerIdx := 0; layerIdx < len(layers); layerIdx++ {
		if e.cancelled.Load() {
			break
		}
		outcome := e.executeAndCheckLayer(layerIdx, layers[layerIdx])
		if outcome == layerOutcomeStop {
			break
		}
		if outcome == layerOutcomeRetry {
			e.resetLayerForRetry(layers[layerIdx])
			layerIdx-- // loop re-increments
		}
	}

	return e.buildResult()
}

func (e *Executor) executeAndCheckLayer(layerIdx int, layer []string) layerOutcome {
	e.mu.Lock()
	e.currentLayer = layerIdx
	// Fresh layer context derived from the DAG ctx so mid-layer
	// cancellation (triggered by an unrecoverable blocking failure)
	// halts in-flight siblings without polluting e.ctx for the rest
	// of the DAG. The previous layer's cancel was already deferred at
	// the end of this function's last invocation, so no stale state.
	e.layerCtx, e.layerCancel = context.WithCancel(e.ctx)
	layerCancel := e.layerCancel
	e.mu.Unlock()
	defer func() {
		// Release layerCtx resources. Doing this at end-of-layer is safe
		// because the next iteration stamps a new pair before any node
		// inspects them, and invokeLayerGate (called via
		// evaluateGateResult below) uses e.ctx not layerCtx for its
		// gate invocation.
		layerCancel()
		e.mu.Lock()
		e.layerCtx, e.layerCancel = nil, nil
		e.mu.Unlock()
	}()

	e.emitLayerStarted(layerIdx, len(layer))
	err := e.executeLayer(layer)
	// If the layer was cancelled mid-flight by
	// cancelLayerOnBlockingFailure (because a sibling's unrecoverable
	// failure blocks downstream), sweep any same-layer nodes that
	// didn't get a chance to record a result. Marking them Cancelled
	// keeps the DAGResult counters (NodesSucceeded + NodesFailed +
	// NodesSkipped + NodesCancelled) consistent with len(layer) and
	// makes their final state visible to the decision-gate dialog so
	// the user knows what was interrupted.
	e.recordMidLayerCancellations(layer)
	e.emitLayerCompleted(layerIdx)

	if err != nil && e.policy.FailurePolicy == FailurePolicyFailFast {
		return layerOutcomeStop
	}
	if deferred := e.handleBudgetDeferredLayer(layer); deferred != layerOutcomeContinue {
		return deferred
	}

	return e.evaluateGateResult(layerIdx)
}

// recordMidLayerCancellations walks the layer at end-of-execution and
// marks any node that never recorded a result as Cancelled. This
// happens when cancelLayerOnBlockingFailure fires: sibling nodes that
// hadn't acquired their semaphore slot yet observe layerCtx.Done() in
// acquireLayerSlot and exit without recording a NodeResult. Without
// this sweep, those nodes stay Pending forever, the counters drift,
// and the decision gate's FailedNodes list misses them.
//
// Nodes with an existing terminal result (Completed, Failed, Skipped,
// Cancelled) are left untouched — we only rescue the ones that truly
// never ran.
func (e *Executor) recordMidLayerCancellations(layer []string) {
	e.mu.RLock()
	snapshot := make(map[string]*NodeResult, len(e.nodeResults))
	for k, v := range e.nodeResults {
		snapshot[k] = v
	}
	e.mu.RUnlock()
	for _, nodeID := range layer {
		if existing, ok := snapshot[nodeID]; ok && existing != nil {
			continue
		}
		node, ok := e.dag.GetNode(nodeID)
		if !ok {
			continue
		}
		if node.State().IsTerminal() {
			continue
		}
		e.markNodeCancelled(node)
	}
}

func (e *Executor) evaluateGateResult(layerIdx int) layerOutcome {
	gateErr := e.invokeLayerGate(layerIdx)
	if gateErr == nil {
		return layerOutcomeContinue
	}
	if errors.Is(gateErr, ErrLayerRetry) {
		return e.checkLayerRetryBudget(layerIdx)
	}
	return layerOutcomeStop
}

func (e *Executor) checkLayerRetryBudget(layerIdx int) layerOutcome {
	e.mu.Lock()
	e.layerRetries[layerIdx]++
	count := e.layerRetries[layerIdx]
	e.mu.Unlock()

	if count > maxLayerRetries {
		return layerOutcomeStop
	}
	return layerOutcomeRetry
}

func (e *Executor) handleBudgetDeferredLayer(layer []string) layerOutcome {
	nextRetry, hasDeferred := e.nextBudgetRetry(layer)
	if !hasDeferred {
		return layerOutcomeContinue
	}
	if err := e.waitForBudgetRetry(nextRetry); err != nil {
		return layerOutcomeStop
	}
	return layerOutcomeRetry
}

func (e *Executor) nextBudgetRetry(layer []string) (time.Time, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var (
		next time.Time
		ok   bool
	)
	for _, nodeID := range layer {
		result, exists := e.nodeResults[nodeID]
		if !exists || result == nil || result.State != NodeStateWaitingBudget {
			continue
		}
		retryAt, hasRetry := e.budgetRetry[nodeID]
		if !hasRetry {
			retryAt = time.Now().Add(e.policy.RetryBackoff)
		}
		if !ok || retryAt.Before(next) {
			next = retryAt
			ok = true
		}
	}
	return next, ok
}

func (e *Executor) waitUntil(deadline time.Time) error {
	if deadline.IsZero() || !deadline.After(time.Now()) {
		return nil
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-e.ctx.Done():
		return e.ctx.Err()
	}
}

func (e *Executor) waitForBudgetRetry(deadline time.Time) error {
	if waiter, ok := e.dispatcher.(budgetRetryWaiter); ok {
		return waiter.WaitForBudget(e.ctx, deadline)
	}
	return e.waitUntil(deadline)
}

func (e *Executor) resetLayerForRetry(layer []string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, nodeID := range layer {
		result, ok := e.nodeResults[nodeID]
		if !ok || (result.State != NodeStateFailed && result.State != NodeStateWaitingBudget) {
			continue
		}
		node, exists := e.dag.GetNode(nodeID)
		if !exists {
			continue
		}
		node.SetState(NodeStatePending)
		if result.State == NodeStateFailed {
			node.ResetRetryCount()
		}
		delete(e.nodeResults, nodeID)
		delete(e.budgetRetry, nodeID)
	}
}

func (e *Executor) invokeLayerGate(layerIdx int) error {
	e.mu.RLock()
	gate := e.layerGate
	e.mu.RUnlock()

	if gate == nil {
		return nil
	}

	e.mu.RLock()
	results := make(map[string]*NodeResult, len(e.nodeResults))
	for k, v := range e.nodeResults {
		results[k] = v
	}
	dagID := e.dag.ID()
	e.mu.RUnlock()

	return gate(e.ctx, dagID, layerIdx, results)
}

func (e *Executor) emitLayerStarted(layerIdx, nodeCount int) {
	e.emitEvent(&Event{
		Type:      EventLayerStarted,
		DAGID:     e.dag.ID(),
		Layer:     layerIdx,
		Timestamp: time.Now(),
		Data: map[string]any{
			"node_count": nodeCount,
		},
	})
}

func (e *Executor) emitLayerCompleted(layerIdx int) {
	e.emitEvent(&Event{
		Type:      EventLayerCompleted,
		DAGID:     e.dag.ID(),
		Layer:     layerIdx,
		Timestamp: time.Now(),
	})
}

type layerAction int

const (
	layerActionRun layerAction = iota
	layerActionSkip
	layerActionStop
)

type layerErrorState struct {
	errOnce  sync.Once
	firstErr *error
}

func (s *layerErrorState) record(err error) {
	if err == nil {
		return
	}
	s.errOnce.Do(func() {
		*s.firstErr = err
	})
}

func (e *Executor) executeLayer(nodeIDs []string) error {
	var wg sync.WaitGroup
	var firstErr error
	errState := &layerErrorState{firstErr: &firstErr}

	for _, nodeID := range nodeIDs {
		stop, err := e.processLayerNode(nodeID, &wg, errState)
		if err != nil {
			return err
		}
		if stop {
			break
		}
	}

	wg.Wait()
	return firstErr
}

func (e *Executor) processLayerNode(nodeID string, wg *sync.WaitGroup, errState *layerErrorState) (bool, error) {
	action, node, err := e.layerAction(nodeID)
	if err != nil {
		return true, err
	}
	if action == layerActionStop {
		return true, nil
	}
	if action == layerActionRun {
		e.launchNodeExecution(wg, node, errState)
	}
	return false, nil
}

func (e *Executor) layerAction(nodeID string) (layerAction, *Node, error) {
	if e.cancelled.Load() {
		return layerActionStop, nil, nil
	}

	node := e.nodeForLayer(nodeID)
	if node == nil {
		return layerActionSkip, nil, nil
	}

	if err := e.acquireLayerSlot(); err != nil {
		return layerActionStop, nil, err
	}

	return layerActionRun, node, nil
}

func (e *Executor) nodeForLayer(nodeID string) *Node {
	node, ok := e.dag.GetNode(nodeID)
	if !ok {
		return nil
	}

	// Skip already-succeeded nodes during layer replay.
	if node.State().IsTerminal() {
		return nil
	}

	nodeStates := e.dag.GetNodeStates()
	if node.IsBlocked(nodeStates) {
		e.markNodeBlocked(node)
		return nil
	}

	if !node.IsReady(nodeStates) {
		e.markNodeBlocked(node)
		return nil
	}

	return node
}

func (e *Executor) acquireLayerSlot() error {
	if e.sem == nil {
		return nil
	}

	parent := e.activeNodeParentContext()
	// Fast-path cancellation check: if the layer is already cancelled,
	// never claim a slot. This is load-bearing for the mid-layer
	// cancel semantics — without it, a sibling that was already
	// blocked on `e.sem <- struct{}{}` when cancellation fires has a
	// 50/50 chance of winning the select (Go picks pseudo-randomly
	// when multiple cases are ready). Checking up-front closes that
	// race window for the already-cancelled case.
	if err := parent.Err(); err != nil {
		return err
	}

	select {
	case e.sem <- struct{}{}:
		// Re-check after acquiring: the layer may have been cancelled
		// between the fast-path check above and the select winning.
		// Release the slot back and report the cancellation so the
		// slot is never held by a cancelled node.
		if err := parent.Err(); err != nil {
			<-e.sem
			return err
		}
		return nil
	case <-parent.Done():
		return parent.Err()
	}
}

func (e *Executor) releaseLayerSlot() {
	if e.sem != nil {
		<-e.sem
	}
}

func (e *Executor) launchNodeExecution(wg *sync.WaitGroup, node *Node, errState *layerErrorState) {
	if e.scope == nil {
		e.executeNodeInline(wg, node, errState)
		return
	}
	e.executeNodeScoped(wg, node, errState)
}

func (e *Executor) executeNodeInline(wg *sync.WaitGroup, node *Node, errState *layerErrorState) {
	wg.Add(1)
	atomic.AddInt32(&e.nodesRunning, 1)
	defer wg.Done()
	defer atomic.AddInt32(&e.nodesRunning, -1)
	defer e.releaseLayerSlot()

	errState.record(e.executeNode(node))
	e.cancelLayerOnBlockingFailure(node)
}

func (e *Executor) executeNodeScoped(wg *sync.WaitGroup, node *Node, errState *layerErrorState) {
	wg.Add(1)
	atomic.AddInt32(&e.nodesRunning, 1)

	timeout := e.resolveNodeTimeout(node)

	err := e.scope.Go("dag.executor.node", e.scopeNodeTimeout(timeout), func(ctx context.Context) error {
		defer wg.Done()
		defer atomic.AddInt32(&e.nodesRunning, -1)
		defer e.releaseLayerSlot()

		errState.record(e.executeNode(node))
		e.cancelLayerOnBlockingFailure(node)
		return nil
	})
	if err == nil {
		return
	}

	// If the scoped launch is rejected, fail the node immediately instead of
	// leaking the waitgroup and hanging the entire DAG.
	wg.Done()
	atomic.AddInt32(&e.nodesRunning, -1)
	e.releaseLayerSlot()
	e.recordNodeResult(node, failedDispatchResult(node.ID(), err, 0))
	errState.record(err)
	e.cancelLayerOnBlockingFailure(node)
}

// cancelLayerOnBlockingFailure checks whether the just-finished node
// left the DAG in a state where remaining work is structurally blocked.
// When it did, the layer-scoped cancel signal fires so sibling
// goroutines still executing (or retrying) wake up and exit promptly
// instead of burning wall-clock time on work the user will have to
// re-approve or discard anyway.
//
// Classification semantics:
//
//   - The just-finished node must be in NodeStateFailed — success,
//     cancellation-already-observed, or budget-deferred nodes don't
//     warrant escalation.
//   - We classify against the full nodeResults snapshot using the
//     existing ClassifyLayerFailures. Mid-layer siblings that are
//     still running aren't in nodeResults yet, which is fine: the
//     classifier only looks at whether DOWNSTREAM layers remain
//     viable. A blocking failure of the current node propagates to
//     downstream regardless of same-layer siblings' eventual
//     outcomes, so the classification is stable.
//   - FailureClassNone / FailureClassNonBlocking: don't cancel. The
//     remaining siblings may still produce useful work, and the
//     end-of-layer gate will handle non-blocking failures on its own
//     via the existing onNonBlocking callback path.
//
// Thread-safety: the nodeResults read uses e.mu for consistency with
// the rest of the executor. layerCancel is idempotent — multiple
// sibling failures all calling it produce the same observable effect.
func (e *Executor) cancelLayerOnBlockingFailure(node *Node) {
	if node == nil {
		return
	}
	e.mu.RLock()
	result, ok := e.nodeResults[node.ID()]
	cancel := e.layerCancel
	dag := e.dag
	layerIdx := e.currentLayer
	resultsCopy := make(map[string]*NodeResult, len(e.nodeResults))
	for k, v := range e.nodeResults {
		resultsCopy[k] = v
	}
	e.mu.RUnlock()
	if !ok || result == nil || result.State != NodeStateFailed {
		return
	}
	if cancel == nil || dag == nil {
		return
	}
	class := ClassifyLayerFailures(dag, resultsCopy, layerIdx)
	if class != FailureClassBlocking {
		return
	}
	cancel()
}

func (e *Executor) executeNode(node *Node) error {
	nodeID := node.ID()
	parentResults := e.getParentResults(node)
	timeout := e.resolveNodeTimeout(node)
	maxRetries := e.resolveNodeRetries(node)

	result, err := e.executeNodeWithRetry(node, nodeID, parentResults, timeout, maxRetries)
	if result != nil {
		e.recordNodeResult(node, result)
	}
	return err
}

func (e *Executor) executeNodeWithRetry(node *Node, nodeID string, parentResults map[string]*NodeResult, timeout time.Duration, maxRetries int) (*NodeResult, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := e.prepareAttempt(node, nodeID, attempt); err != nil {
			return nil, err
		}

		result, err := e.dispatchNode(node, parentResults, timeout)
		if deferred, retryAt := deferredDispatch(result, err); deferred {
			e.recordBudgetRetry(nodeID, retryAt)
			return result, nil
		}
		success, dispatchErr := e.handleDispatchResult(result, err)
		lastErr = dispatchErr
		if success {
			return result, nil
		}
	}

	return e.failedNodeResult(node, nodeID, lastErr), lastErr
}

func (e *Executor) prepareAttempt(node *Node, nodeID string, attempt int) error {
	if e.cancelled.Load() {
		e.markNodeCancelled(node)
		return ErrDAGCancelled
	}
	if attempt == 0 {
		return nil
	}

	node.IncrementRetryCount()
	e.emitEvent(&Event{
		Type:      EventNodeRetrying,
		DAGID:     e.dag.ID(),
		NodeID:    nodeID,
		Timestamp: time.Now(),
		Data: map[string]any{
			"attempt": attempt,
		},
	})

	return e.waitRetryBackoff(node)
}

func (e *Executor) waitRetryBackoff(node *Node) error {
	// Wait on the layer-aware context so that mid-layer cancellation
	// (triggered by a sibling's unrecoverable failure) breaks us out of
	// a retry backoff without waiting for the RetryBackoff deadline.
	parent := e.activeNodeParentContext()
	select {
	case <-time.After(e.policy.RetryBackoff):
		return nil
	case <-parent.Done():
		e.markNodeCancelled(node)
		return parent.Err()
	}
}

func (e *Executor) recordBudgetRetry(nodeID string, retryAt time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if retryAt.IsZero() || retryAt.Before(time.Now()) {
		retryAt = time.Now().Add(e.policy.RetryBackoff)
	}
	e.budgetRetry[nodeID] = retryAt
}

func (e *Executor) resolveNodeTimeout(node *Node) time.Duration {
	timeout := node.Timeout()
	if timeout == NoTimeout {
		return NoTimeout
	}
	if timeout == 0 {
		return e.policy.DefaultTimeout
	}
	return timeout
}

func (e *Executor) scopeNodeTimeout(timeout time.Duration) time.Duration {
	if timeout == NoTimeout {
		return 0
	}
	return timeout
}

func (e *Executor) resolveNodeRetries(node *Node) int {
	maxRetries := node.Retries()
	if maxRetries == 0 {
		return e.policy.DefaultRetries
	}
	return maxRetries
}

func (e *Executor) handleDispatchResult(result *NodeResult, err error) (bool, error) {
	if result != nil && result.State == NodeStateCancelled {
		// A cancelled node result has three possible origins:
		//
		//   (1) external DAG cancel — user abort, parent ctx cancel,
		//       global shutdown. Propagate: mark e.cancelled, cancel
		//       e.ctx, short-circuit remaining layers.
		//   (2) dispatcher unilaterally declared the node cancelled
		//       (without any ctx cancellation observed on its side) —
		//       this is a deliberate external signal that the work must
		//       not proceed. Historical behavior: propagate as a DAG
		//       cancel. Preserved to avoid surprising existing callers.
		//   (3) mid-layer halt — the node's dispatcher observed
		//       layerCtx.Done() because cancelLayerOnBlockingFailure
		//       fired when a SIBLING failed unrecoverably with blocking
		//       downstream. In this case the DAG is NOT being cancelled
		//       — we WANT the executor to proceed to the next layer so
		//       downstream nodes are marked Blocked and the
		//       decision-gate dialog can surface. Promoting this to a
		//       DAG cancel would skip those layers entirely.
		//
		// Distinguishing case (3) from (1)+(2) requires checking BOTH
		// e.ctx (if cancelled → case 1) AND layerCtx (if cancelled but
		// e.ctx is not → case 3). If neither is cancelled, it's case 2.
		if e.isMidLayerHalt() {
			return true, nil
		}
		e.cancelled.Store(true)
		if e.cancel != nil {
			e.cancel()
		}
		return true, nil
	}
	if errors.Is(err, ErrDAGCancelled) {
		e.cancelled.Store(true)
		if e.cancel != nil {
			e.cancel()
		}
		return true, nil
	}
	if isDispatchSuccess(result, err) {
		return true, nil
	}
	return false, dispatchError(err, result)
}

func isDispatchSuccess(result *NodeResult, err error) bool {
	if err != nil {
		return false
	}
	if result == nil {
		return false
	}
	return result.IsSuccess()
}

func dispatchError(err error, result *NodeResult) error {
	if result == nil {
		return err
	}
	if result.Error == nil {
		return err
	}
	return result.Error
}

func (e *Executor) failedNodeResult(node *Node, nodeID string, err error) *NodeResult {
	return &NodeResult{
		NodeID:   nodeID,
		State:    NodeStateFailed,
		Error:    err,
		EndTime:  time.Now(),
		Duration: time.Since(node.StartTime()),
		Retries:  node.RetryCount(),
	}
}

func (e *Executor) dispatchNode(node *Node, parentResults map[string]*NodeResult, timeout time.Duration) (*NodeResult, error) {
	nodeID := node.ID()
	e.markNodeQueued(node, nodeID)
	e.markNodeDispatched(node, nodeID)
	e.markNodeStarted(node, nodeID)

	ctx, cancel := e.nodeContext(timeout)
	defer cancel()

	if e.dispatcher == nil {
		return failedDispatchResult(nodeID, ErrNodeNotFound, 0), ErrNodeNotFound
	}

	result, err := e.dispatcher.Dispatch(ctx, node, parentResults)
	if err == nil {
		return result, nil
	}
	var deferredErr *NodeDeferredError
	if errors.As(err, &deferredErr) {
		return deferredDispatchResult(nodeID, deferredErr), deferredErr
	}
	if errors.Is(err, context.DeadlineExceeded) || (result == nil && ctx.Err() == context.DeadlineExceeded) {
		return failedDispatchResult(nodeID, ErrNodeTimeout, time.Since(node.StartTime())), ErrNodeTimeout
	}

	return result, err
}

func (e *Executor) markNodeQueued(node *Node, nodeID string) {
	node.SetState(NodeStateQueued)
	e.emitEvent(&Event{
		Type:      EventNodeQueued,
		DAGID:     e.dag.ID(),
		NodeID:    nodeID,
		Timestamp: time.Now(),
	})
}

func (e *Executor) markNodeDispatched(node *Node, nodeID string) {
	node.SetState(NodeStateDispatched)
	e.emitEvent(&Event{
		Type:      EventNodeDispatched,
		DAGID:     e.dag.ID(),
		NodeID:    nodeID,
		Timestamp: time.Now(),
	})
}

// MarkNodeAcked transitions a node to the Acked state. Called externally
// by the dispatcher when an agent ACK is received.
func (e *Executor) MarkNodeAcked(nodeID, agentID string) {
	node, ok := e.dag.GetNode(nodeID)
	if !ok {
		return
	}
	node.SetState(NodeStateAcked)
	e.emitEvent(&Event{
		Type:      EventNodeAcked,
		DAGID:     e.dag.ID(),
		NodeID:    nodeID,
		Timestamp: time.Now(),
		Data: map[string]any{
			"agent_id": agentID,
		},
	})
}

// MarkNodeRunning transitions a node to the Running state. Called externally
// by the dispatcher after ACK processing completes.
func (e *Executor) MarkNodeRunning(nodeID string) {
	node, ok := e.dag.GetNode(nodeID)
	if !ok {
		return
	}
	node.SetState(NodeStateRunning)
	node.SetStartTime(time.Now())
	e.emitEvent(&Event{
		Type:      EventNodeStarted,
		DAGID:     e.dag.ID(),
		NodeID:    nodeID,
		Timestamp: time.Now(),
	})
}

func (e *Executor) markNodeStarted(node *Node, nodeID string) {
	node.SetState(NodeStateRunning)
	node.SetStartTime(time.Now())
	e.emitEvent(&Event{
		Type:      EventNodeStarted,
		DAGID:     e.dag.ID(),
		NodeID:    nodeID,
		Timestamp: time.Now(),
	})
}

func (e *Executor) nodeContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	parent := e.activeNodeParentContext()
	if timeout <= 0 || timeout == NoTimeout {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

// activeNodeParentContext returns the context that node-level waits
// should derive from. During layer execution it's layerCtx (so mid-
// layer cancellation can halt siblings); outside layer execution (e.g.
// during budget deferrals, gate evaluation, or initialization), it
// falls back to the DAG-scoped e.ctx. Always non-nil once the executor
// is initialized.
func (e *Executor) activeNodeParentContext() context.Context {
	e.mu.RLock()
	layer := e.layerCtx
	e.mu.RUnlock()
	if layer != nil {
		return layer
	}
	return e.ctx
}

// isMidLayerHalt reports whether the current dispatched node's
// cancelled outcome was caused by the mid-layer halt machinery
// (cancelLayerOnBlockingFailure fired on layerCtx) rather than an
// external DAG cancel or a dispatcher's unilateral Cancel return.
//
// True IFF layerCtx exists and is cancelled AND e.ctx is still alive.
// The two-pointer check is critical: layerCtx is derived from e.ctx,
// so if e.ctx is cancelled layerCtx will also show Err() != nil. We
// must distinguish "layer halted, DAG still alive" from "DAG being
// shut down (cancels layer as a side effect)."
func (e *Executor) isMidLayerHalt() bool {
	if e.ctx != nil && e.ctx.Err() != nil {
		return false
	}
	e.mu.RLock()
	layer := e.layerCtx
	e.mu.RUnlock()
	if layer == nil {
		return false
	}
	return layer.Err() != nil
}

func failedDispatchResult(nodeID string, err error, duration time.Duration) *NodeResult {
	result := &NodeResult{
		NodeID:  nodeID,
		State:   NodeStateFailed,
		Error:   err,
		EndTime: time.Now(),
	}
	if duration > 0 {
		result.Duration = duration
	}
	return result
}

func deferredDispatch(result *NodeResult, err error) (bool, time.Time) {
	var deferredErr *NodeDeferredError
	if !errors.As(err, &deferredErr) {
		return false, time.Time{}
	}
	if result != nil && result.State == NodeStateWaitingBudget {
		if deferredErr.RetryAfter > 0 {
			return true, time.Now().Add(deferredErr.RetryAfter)
		}
	}
	if deferredErr.RetryAfter > 0 {
		return true, time.Now().Add(deferredErr.RetryAfter)
	}
	return true, time.Now()
}

func deferredDispatchResult(nodeID string, deferred *NodeDeferredError) *NodeResult {
	result := &NodeResult{
		NodeID:   nodeID,
		State:    NodeStateWaitingBudget,
		Error:    deferred,
		EndTime:  time.Now(),
		Metadata: map[string]any{},
	}
	if deferred != nil && deferred.RetryAfter > 0 {
		result.Metadata["retry_after_ms"] = deferred.RetryAfter.Milliseconds()
	}
	return result
}

func (e *Executor) getParentResults(node *Node) map[string]*NodeResult {
	e.mu.RLock()
	defer e.mu.RUnlock()

	deps := node.Dependencies()
	results := make(map[string]*NodeResult, len(deps))

	for _, depID := range deps {
		if result, ok := e.nodeResults[depID]; ok {
			results[depID] = result
		}
	}

	return results
}

func (e *Executor) recordNodeResult(node *Node, result *NodeResult) {
	e.mu.Lock()
	e.nodeResults[node.ID()] = result
	e.mu.Unlock()

	node.SetResult(result)
	e.emitNodeResultEvent(node, result)
}

func (e *Executor) emitNodeResultEvent(node *Node, result *NodeResult) {
	data := map[string]any{
		"duration": result.Duration,
	}
	if errText := nodeResultErrorText(result); errText != "" {
		data["error"] = errText
	}
	if result != nil && result.Metadata != nil {
		if retryAfter, ok := result.Metadata["retry_after_ms"]; ok {
			data["retry_after_ms"] = retryAfter
		}
	}
	e.emitEvent(&Event{
		Type:      nodeResultEventType(result.State),
		DAGID:     e.dag.ID(),
		NodeID:    node.ID(),
		Timestamp: time.Now(),
		Data:      data,
	})
}

func nodeResultErrorText(result *NodeResult) string {
	if result == nil || result.Error == nil {
		return ""
	}
	return result.Error.Error()
}

var nodeStateToEventType = map[NodeState]EventType{
	NodeStateSucceeded:     EventNodeCompleted,
	NodeStateFailed:        EventNodeFailed,
	NodeStateBlocked:       EventNodeFailed,
	NodeStateSkipped:       EventNodeSkipped,
	NodeStateCancelled:     EventNodeCancelled,
	NodeStateWaitingBudget: EventNodeBudgetDeferred,
}

func nodeResultEventType(state NodeState) EventType {
	if et, ok := nodeStateToEventType[state]; ok {
		return et
	}
	return EventNodeCompleted
}

func (e *Executor) markNodeBlocked(node *Node) {
	result := &NodeResult{
		NodeID:  node.ID(),
		State:   NodeStateBlocked,
		EndTime: time.Now(),
	}
	e.recordNodeResult(node, result)
}

func (e *Executor) markNodeCancelled(node *Node) {
	result := &NodeResult{
		NodeID:  node.ID(),
		State:   NodeStateCancelled,
		Error:   ErrDAGCancelled,
		EndTime: time.Now(),
	}
	e.recordNodeResult(node, result)
}

func (e *Executor) cancelRemainingNodes(markCancelled bool) {
	if markCancelled {
		e.cancelled.Store(true)
	}
	if e.cancel != nil {
		e.cancel()
	}
	e.cancelNonTerminalNodes()
}

func (e *Executor) cancelNonTerminalNodes() {
	for _, node := range e.dag.Nodes() {
		if !node.State().IsTerminal() {
			e.markNodeCancelled(node)
		}
	}
}

func (e *Executor) buildResult() *DAGResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	result := e.newResultSnapshot()
	for nodeID, nodeResult := range e.nodeResults {
		result.NodeResults[nodeID] = nodeResult
		e.tallyNodeResult(result, nodeResult)
	}

	e.applyResultState(result, e.incompleteNodeCountLocked())
	e.state = result.State
	return result
}

func (e *Executor) newResultSnapshot() *DAGResult {
	endTime := time.Now()
	return &DAGResult{
		ID:          e.dag.ID(),
		NodeResults: make(map[string]*NodeResult),
		StartTime:   e.startTime,
		EndTime:     endTime,
		Duration:    endTime.Sub(e.startTime),
	}
}

func (e *Executor) tallyNodeResult(result *DAGResult, nodeResult *NodeResult) {
	switch nodeResult.State {
	case NodeStateSucceeded:
		result.NodesSucceeded++
	case NodeStateFailed, NodeStateBlocked:
		result.NodesFailed++
	case NodeStateSkipped, NodeStateCancelled:
		result.NodesSkipped++
	}
}

func (e *Executor) applyResultState(result *DAGResult, incompleteNodes int) {
	if e.cancelled.Load() || errors.Is(e.ctx.Err(), context.Canceled) {
		result.State = DAGStateCancelled
		result.Error = ErrDAGCancelled
		return
	}
	if err := e.ctx.Err(); err != nil {
		result.State = DAGStateFailed
		result.Error = err
		return
	}
	if result.NodesFailed > 0 {
		result.State = DAGStateFailed
		result.Error = buildNodeFailureError(result.NodeResults)
		return
	}
	if incompleteNodes > 0 {
		result.State = DAGStateFailed
		result.Error = fmt.Errorf("dag incomplete: %d nodes unfinished", incompleteNodes)
		return
	}
	result.State = DAGStateSucceeded
}

func (e *Executor) incompleteNodeCountLocked() int {
	count := 0
	for _, node := range e.dag.Nodes() {
		if !node.State().IsTerminal() {
			count++
		}
	}
	return count
}

func buildNodeFailureError(nodeResults map[string]*NodeResult) error {
	errs := make([]error, 0, len(nodeResults))
	for _, nr := range nodeResults {
		if nr.State == NodeStateFailed {
			errs = append(errs, wrapNodeFailure(nr))
		}
	}
	if len(errs) == 0 {
		return errors.New("dag failed")
	}
	return errors.Join(errs...)
}

func wrapNodeFailure(nr *NodeResult) error {
	if nr.Error != nil {
		return fmt.Errorf("%s: %w", nr.NodeID, nr.Error)
	}
	return fmt.Errorf("%s: unknown error", nr.NodeID)
}

func (e *Executor) Cancel() error {
	e.mu.RLock()
	if e.state != DAGStateRunning {
		e.mu.RUnlock()
		return ErrDAGNotRunning
	}
	e.mu.RUnlock()

	e.cancelRemainingNodes(true)
	return nil
}

func (e *Executor) Status() *DAGStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.dag == nil {
		return nil
	}

	nodeStates := e.dag.GetNodeStates()
	nodesCompleted := countTerminalNodes(nodeStates)
	totalNodes := e.dag.NodeCount()

	return &DAGStatus{
		ID:             e.dag.ID(),
		State:          e.state,
		CurrentLayer:   e.currentLayer,
		TotalLayers:    e.dag.LayerCount(),
		Progress:       calcProgress(nodesCompleted, totalNodes),
		NodesTotal:     totalNodes,
		NodesCompleted: nodesCompleted,
		NodesRunning:   int(atomic.LoadInt32(&e.nodesRunning)),
		NodeStates:     nodeStates,
		StartTime:      e.startTime,
	}
}

func countTerminalNodes(nodeStates map[string]NodeState) int {
	count := 0
	for _, state := range nodeStates {
		if state.IsTerminal() {
			count++
		}
	}
	return count
}

func calcProgress(completed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(completed) / float64(total)
}

func (e *Executor) Subscribe(handler EventHandler) func() {
	e.handlersMu.Lock()
	e.handlers = append(e.handlers, handler)
	index := len(e.handlers) - 1
	e.handlersMu.Unlock()

	return func() {
		e.handlersMu.Lock()
		defer e.handlersMu.Unlock()
		if index < len(e.handlers) {
			e.handlers[index] = nil
		}
	}
}

func (e *Executor) emitEvent(event *Event) {
	e.handlersMu.RLock()
	handlers := make([]EventHandler, len(e.handlers))
	copy(handlers, e.handlers)
	e.handlersMu.RUnlock()

	for _, handler := range handlers {
		if handler != nil {
			handler(event)
		}
	}
}

func (e *Executor) Close() error {
	if e.closed.Swap(true) {
		return ErrExecutorClosed
	}

	if e.cancel != nil {
		e.cancel()
	}

	return nil
}
