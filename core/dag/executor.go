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

	sem chan struct{}

	handlersMu sync.RWMutex
	handlers   []EventHandler
	dispatcher NodeDispatcher
	closed     atomic.Bool

	layerGate    LayerGate
	layerRetries map[int]int

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
	e.mu.Unlock()

	e.emitLayerStarted(layerIdx, len(layer))
	err := e.executeLayer(layer)
	e.emitLayerCompleted(layerIdx)

	if err != nil && e.policy.FailurePolicy == FailurePolicyFailFast {
		return layerOutcomeStop
	}

	return e.evaluateGateResult(layerIdx)
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

func (e *Executor) resetLayerForRetry(layer []string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, nodeID := range layer {
		result, ok := e.nodeResults[nodeID]
		if !ok || result.State != NodeStateFailed {
			continue
		}
		node, exists := e.dag.GetNode(nodeID)
		if !exists {
			continue
		}
		node.SetState(NodeStatePending)
		node.ResetRetryCount()
		delete(e.nodeResults, nodeID)
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

	select {
	case e.sem <- struct{}{}:
		return nil
	case <-e.ctx.Done():
		return e.ctx.Err()
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
	select {
	case <-time.After(e.policy.RetryBackoff):
		return nil
	case <-e.ctx.Done():
		e.markNodeCancelled(node)
		return e.ctx.Err()
	}
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
	if timeout <= 0 || timeout == NoTimeout {
		return e.ctx, func() {}
	}
	return context.WithTimeout(e.ctx, timeout)
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
	NodeStateSucceeded: EventNodeCompleted,
	NodeStateFailed:    EventNodeFailed,
	NodeStateBlocked:   EventNodeFailed,
	NodeStateSkipped:   EventNodeSkipped,
	NodeStateCancelled: EventNodeCancelled,
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

	e.applyResultState(result)
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

func (e *Executor) applyResultState(result *DAGResult) {
	if e.cancelled.Load() {
		result.State = DAGStateCancelled
		result.Error = ErrDAGCancelled
		return
	}
	if result.NodesFailed > 0 {
		result.State = DAGStateFailed
		result.Error = buildNodeFailureError(result.NodeResults)
		return
	}
	result.State = DAGStateSucceeded
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
