package dag

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Node Dispatcher Interface
// =============================================================================

// NodeDispatcher dispatches nodes to agents for execution
type NodeDispatcher interface {
	// Dispatch executes a node and returns the result
	Dispatch(ctx context.Context, node *Node, parentResults map[string]*NodeResult) (*NodeResult, error)
}

// =============================================================================
// Executor
// =============================================================================

// Executor executes DAGs with parallel layer execution
type Executor struct {
	mu sync.RWMutex

	// Configuration
	policy ExecutionPolicy

	// Current execution state
	dag           *DAG
	state         DAGState
	currentLayer  int
	startTime     time.Time
	nodeResults   map[string]*NodeResult
	nodesRunning  int32
	cancelled     atomic.Bool

	// Control
	ctx    context.Context
	cancel context.CancelFunc

	// Semaphore for concurrency limiting
	sem chan struct{}

	// Event handlers
	handlersMu sync.RWMutex
	handlers   []EventHandler

	// Dispatcher
	dispatcher NodeDispatcher

	// Closed
	closed atomic.Bool
}

// NewExecutor creates a new DAG executor
func NewExecutor(policy ExecutionPolicy) *Executor {
	return &Executor{
		policy:      policy,
		nodeResults: make(map[string]*NodeResult),
		handlers:    make([]EventHandler, 0),
	}
}

// =============================================================================
// Execution
// =============================================================================

// Execute runs a DAG to completion
func (e *Executor) Execute(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (*DAGResult, error) {
	if e.closed.Load() {
		return nil, ErrExecutorClosed
	}

	// Validate DAG if not already done
	if !dag.IsValidated() {
		if err := ValidateDAG(dag); err != nil {
			return nil, err
		}
	}

	e.mu.Lock()
	if e.state == DAGStateRunning {
		e.mu.Unlock()
		return nil, ErrDAGAlreadyRunning
	}

	// Initialize execution state
	e.dag = dag
	e.state = DAGStateRunning
	e.currentLayer = 0
	e.startTime = time.Now()
	e.nodeResults = make(map[string]*NodeResult)
	e.dispatcher = dispatcher
	e.cancelled.Store(false)

	// Create cancellable context
	e.ctx, e.cancel = context.WithCancel(ctx)

	// Create semaphore for concurrency limiting
	if e.policy.MaxConcurrency > 0 {
		e.sem = make(chan struct{}, e.policy.MaxConcurrency)
	}

	// Reset node states
	dag.ResetNodeStates()
	e.mu.Unlock()

	// Emit start event
	e.emitEvent(&Event{
		Type:      EventDAGStarted,
		DAGID:     dag.ID(),
		Timestamp: time.Now(),
	})

	// Execute layers
	result := e.executeLayers()

	// Emit completion event
	var eventType EventType
	switch result.State {
	case DAGStateSucceeded:
		eventType = EventDAGCompleted
	case DAGStateFailed:
		eventType = EventDAGFailed
	case DAGStateCancelled:
		eventType = EventDAGCancelled
	default:
		eventType = EventDAGCompleted
	}

	e.emitEvent(&Event{
		Type:      eventType,
		DAGID:     dag.ID(),
		Timestamp: time.Now(),
		Data: map[string]any{
			"duration":   result.Duration,
			"succeeded":  result.NodesSucceeded,
			"failed":     result.NodesFailed,
			"skipped":    result.NodesSkipped,
		},
	})

	return result, nil
}

// executeLayers executes all layers in order
func (e *Executor) executeLayers() *DAGResult {
	layers := e.dag.ExecutionOrder()

	for layerIdx, layer := range layers {
		if e.cancelled.Load() {
			break
		}

		e.mu.Lock()
		e.currentLayer = layerIdx
		e.mu.Unlock()

		// Emit layer start event
		e.emitEvent(&Event{
			Type:      EventLayerStarted,
			DAGID:     e.dag.ID(),
			Layer:     layerIdx,
			Timestamp: time.Now(),
			Data: map[string]any{
				"node_count": len(layer),
			},
		})

		// Execute nodes in this layer
		if err := e.executeLayer(layer); err != nil {
			// Check if we should fail fast
			if e.policy.FailurePolicy == FailurePolicyFailFast {
				e.cancelRemainingNodes()
				break
			}
		}

		// Emit layer complete event
		e.emitEvent(&Event{
			Type:      EventLayerCompleted,
			DAGID:     e.dag.ID(),
			Layer:     layerIdx,
			Timestamp: time.Now(),
		})
	}

	return e.buildResult()
}

// executeLayer executes all nodes in a layer in parallel
func (e *Executor) executeLayer(nodeIDs []string) error {
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	for _, nodeID := range nodeIDs {
		if e.cancelled.Load() {
			break
		}

		node, ok := e.dag.GetNode(nodeID)
		if !ok {
			continue
		}

		// Check if node is blocked by failed dependencies
		nodeStates := e.dag.GetNodeStates()
		if node.IsBlocked(nodeStates) {
			e.markNodeBlocked(node)
			continue
		}

		// Check if node is ready (all deps succeeded)
		if !node.IsReady(nodeStates) {
			// This shouldn't happen if layers are computed correctly
			e.markNodeBlocked(node)
			continue
		}

		// Acquire semaphore if concurrency limited
		if e.sem != nil {
			select {
			case e.sem <- struct{}{}:
			case <-e.ctx.Done():
				return e.ctx.Err()
			}
		}

		wg.Add(1)
		atomic.AddInt32(&e.nodesRunning, 1)

		go func(n *Node) {
			defer wg.Done()
			defer atomic.AddInt32(&e.nodesRunning, -1)
			defer func() {
				if e.sem != nil {
					<-e.sem
				}
			}()

			err := e.executeNode(n)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
				})
			}
		}(node)
	}

	wg.Wait()
	return firstErr
}

// executeNode executes a single node with retries
func (e *Executor) executeNode(node *Node) error {
	nodeID := node.ID()

	// Get parent results for context
	parentResults := e.getParentResults(node)

	// Determine timeout
	timeout := node.Timeout()
	if timeout == 0 {
		timeout = e.policy.DefaultTimeout
	}

	// Determine max retries
	maxRetries := node.Retries()
	if maxRetries == 0 {
		maxRetries = e.policy.DefaultRetries
	}

	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if e.cancelled.Load() {
			e.markNodeCancelled(node)
			return ErrDAGCancelled
		}

		// Emit retry event if retrying
		if attempt > 0 {
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

			// Backoff between retries
			select {
			case <-time.After(e.policy.RetryBackoff):
			case <-e.ctx.Done():
				e.markNodeCancelled(node)
				return e.ctx.Err()
			}
		}

		// Execute the node
		result, err := e.dispatchNode(node, parentResults, timeout)

		if err == nil && result.IsSuccess() {
			e.recordNodeResult(node, result)
			return nil
		}

		lastErr = err
		if result != nil && result.Error != nil {
			lastErr = result.Error
		}
	}

	// All retries exhausted
	result := &NodeResult{
		NodeID:   nodeID,
		State:    NodeStateFailed,
		Error:    lastErr,
		EndTime:  time.Now(),
		Duration: time.Since(node.StartTime()),
		Retries:  node.RetryCount(),
	}

	e.recordNodeResult(node, result)
	return lastErr
}

// dispatchNode dispatches a node for execution
func (e *Executor) dispatchNode(node *Node, parentResults map[string]*NodeResult, timeout time.Duration) (*NodeResult, error) {
	nodeID := node.ID()

	// Mark node as queued
	node.SetState(NodeStateQueued)
	e.emitEvent(&Event{
		Type:      EventNodeQueued,
		DAGID:     e.dag.ID(),
		NodeID:    nodeID,
		Timestamp: time.Now(),
	})

	// Mark node as running
	node.SetState(NodeStateRunning)
	node.SetStartTime(time.Now())
	e.emitEvent(&Event{
		Type:      EventNodeStarted,
		DAGID:     e.dag.ID(),
		NodeID:    nodeID,
		Timestamp: time.Now(),
	})

	// Create context with timeout
	ctx, cancel := context.WithTimeout(e.ctx, timeout)
	defer cancel()

	// Dispatch to agent
	if e.dispatcher == nil {
		return &NodeResult{
			NodeID:  nodeID,
			State:   NodeStateFailed,
			Error:   ErrNodeNotFound,
			EndTime: time.Now(),
		}, ErrNodeNotFound
	}

	result, err := e.dispatcher.Dispatch(ctx, node, parentResults)

	// Check for timeout
	if ctx.Err() == context.DeadlineExceeded {
		return &NodeResult{
			NodeID:   nodeID,
			State:    NodeStateFailed,
			Error:    ErrNodeTimeout,
			EndTime:  time.Now(),
			Duration: time.Since(node.StartTime()),
		}, ErrNodeTimeout
	}

	return result, err
}

// =============================================================================
// Helper Methods
// =============================================================================

// getParentResults returns results from dependency nodes
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

// recordNodeResult records a node's result
func (e *Executor) recordNodeResult(node *Node, result *NodeResult) {
	e.mu.Lock()
	e.nodeResults[node.ID()] = result
	e.mu.Unlock()

	node.SetResult(result)

	// Emit appropriate event
	var eventType EventType
	switch result.State {
	case NodeStateSucceeded:
		eventType = EventNodeCompleted
	case NodeStateFailed:
		eventType = EventNodeFailed
	case NodeStateSkipped:
		eventType = EventNodeSkipped
	case NodeStateCancelled:
		eventType = EventNodeCancelled
	default:
		eventType = EventNodeCompleted
	}

	e.emitEvent(&Event{
		Type:      eventType,
		DAGID:     e.dag.ID(),
		NodeID:    node.ID(),
		Timestamp: time.Now(),
		Data: map[string]any{
			"duration": result.Duration,
		},
	})
}

// markNodeBlocked marks a node as blocked
func (e *Executor) markNodeBlocked(node *Node) {
	result := &NodeResult{
		NodeID:  node.ID(),
		State:   NodeStateBlocked,
		EndTime: time.Now(),
	}
	e.recordNodeResult(node, result)
}

// markNodeCancelled marks a node as cancelled
func (e *Executor) markNodeCancelled(node *Node) {
	result := &NodeResult{
		NodeID:  node.ID(),
		State:   NodeStateCancelled,
		Error:   ErrDAGCancelled,
		EndTime: time.Now(),
	}
	e.recordNodeResult(node, result)
}

// markNodeSkipped marks a node as skipped
func (e *Executor) markNodeSkipped(node *Node) {
	result := &NodeResult{
		NodeID:  node.ID(),
		State:   NodeStateSkipped,
		EndTime: time.Now(),
	}
	e.recordNodeResult(node, result)

	e.emitEvent(&Event{
		Type:      EventNodeSkipped,
		DAGID:     e.dag.ID(),
		NodeID:    node.ID(),
		Timestamp: time.Now(),
	})
}

// cancelRemainingNodes cancels all pending nodes
func (e *Executor) cancelRemainingNodes() {
	e.cancelled.Store(true)
	if e.cancel != nil {
		e.cancel()
	}

	// Mark all pending nodes as cancelled
	for _, node := range e.dag.Nodes() {
		state := node.State()
		if !state.IsTerminal() && state != NodeStateRunning {
			e.markNodeCancelled(node)
		}
	}
}

// buildResult builds the final DAG result
func (e *Executor) buildResult() *DAGResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	endTime := time.Now()
	result := &DAGResult{
		ID:          e.dag.ID(),
		NodeResults: make(map[string]*NodeResult),
		StartTime:   e.startTime,
		EndTime:     endTime,
		Duration:    endTime.Sub(e.startTime),
	}

	// Copy node results and count states
	for nodeID, nodeResult := range e.nodeResults {
		result.NodeResults[nodeID] = nodeResult

		switch nodeResult.State {
		case NodeStateSucceeded:
			result.NodesSucceeded++
		case NodeStateFailed:
			result.NodesFailed++
		case NodeStateSkipped, NodeStateBlocked, NodeStateCancelled:
			result.NodesSkipped++
		}
	}

	// Determine overall state
	if e.cancelled.Load() {
		result.State = DAGStateCancelled
		result.Error = ErrDAGCancelled
	} else if result.NodesFailed > 0 {
		result.State = DAGStateFailed
	} else {
		result.State = DAGStateSucceeded
	}

	e.state = result.State
	return result
}

// =============================================================================
// Control Methods
// =============================================================================

// Cancel cancels the current execution
func (e *Executor) Cancel() error {
	e.mu.RLock()
	if e.state != DAGStateRunning {
		e.mu.RUnlock()
		return ErrDAGNotRunning
	}
	e.mu.RUnlock()

	e.cancelRemainingNodes()
	return nil
}

// Status returns the current execution status
func (e *Executor) Status() *DAGStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.dag == nil {
		return nil
	}

	nodeStates := e.dag.GetNodeStates()
	nodesCompleted := 0
	for _, state := range nodeStates {
		if state.IsTerminal() {
			nodesCompleted++
		}
	}

	totalNodes := e.dag.NodeCount()
	progress := float64(0)
	if totalNodes > 0 {
		progress = float64(nodesCompleted) / float64(totalNodes)
	}

	return &DAGStatus{
		ID:             e.dag.ID(),
		State:          e.state,
		CurrentLayer:   e.currentLayer,
		TotalLayers:    e.dag.LayerCount(),
		Progress:       progress,
		NodesTotal:     totalNodes,
		NodesCompleted: nodesCompleted,
		NodesRunning:   int(atomic.LoadInt32(&e.nodesRunning)),
		NodeStates:     nodeStates,
		StartTime:      e.startTime,
	}
}

// =============================================================================
// Event Handling
// =============================================================================

// Subscribe registers an event handler
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

// emitEvent emits an event to all handlers
func (e *Executor) emitEvent(event *Event) {
	e.handlersMu.RLock()
	handlers := make([]EventHandler, len(e.handlers))
	copy(handlers, e.handlers)
	e.handlersMu.RUnlock()

	for _, handler := range handlers {
		if handler != nil {
			go handler(event)
		}
	}
}

// =============================================================================
// Lifecycle
// =============================================================================

// Close closes the executor
func (e *Executor) Close() error {
	if e.closed.Swap(true) {
		return ErrExecutorClosed
	}

	// Cancel any running execution
	if e.cancel != nil {
		e.cancel()
	}

	return nil
}
