# Plan: DAG Execution Controller with Reconciliation Pattern

## Overview

Implement pipeline spinup via a Kubernetes-style reconciliation controller that:
- Executes DAG layers, dispatching tasks to pipeline agents with **3 automatic retries** per node
- After each layer, classifies failures as **critical** (blocks entire DAG) or **non-critical** (other paths remain viable)
- Non-critical failures: emits activity event (`"<name> failed! Non-blocking and continuing."`) and proceeds
- Critical failures: pauses the DAG, emits a rich activity event, and blocks until the user responds with retry or abort
- User retry resets failed nodes and re-executes the layer with fresh retries

## Architecture

```
Executor (layer loop)
  └→ executeLayer(nodeIDs)        ← all nodes run concurrently, wg.Wait()
      └→ dispatchNode → BusNodeDispatcher → TaskRouter → Guide → Pipeline Agent
  └→ LayerGate (DAGController)    ← fires AFTER entire layer completes
      ├→ classifyFailures()
      │   ├→ non-critical → publishActivity(), return nil (continue)
      │   └→ critical → DAGStatePaused, publishActivity(), block on DecisionGate
      └→ DecisionGate.Await()
          ├→ DecisionRetry → return ErrRetryLayer (executor replays layer)
          └→ DecisionAbort → return ErrDAGCancelled (executor stops)

User types "retry" / "abort"
  → Guide routes to Orchestrator
  → Orchestrator LLM calls retry_dag / abort_dag skill
  → skill looks up DecisionGate, submits decision
  → LayerGate unblocks
```

## Files

### New: `core/dag/decision.go`

Decision types and the DecisionGate used to park the executor while awaiting user input.

```go
// Decision represents a user's response to a critical DAG failure.
type Decision int

const (
    DecisionRetry Decision = iota
    DecisionAbort
)

// FailedNodeInfo describes a single failed node for notification purposes.
type FailedNodeInfo struct {
    NodeID       string
    NodeName     string
    Error        string
    Attempt      int
    MaxAttempts  int
    BlockedCount int // number of transitive dependents that would be blocked
}

// DecisionRequest is the context passed to the notifier when a critical failure pauses the DAG.
type DecisionRequest struct {
    DAGID       string
    LayerIdx    int
    FailedNodes []FailedNodeInfo
    TotalBlocked int
}

// DecisionGate is a typed, context-aware channel for receiving a user decision.
// The executor parks on Await(); orchestrator skills call Submit().
type DecisionGate struct {
    ch  chan Decision
    req atomic.Pointer[DecisionRequest] // latest pending request
}

func NewDecisionGate() *DecisionGate { ... }

// Await blocks until a decision arrives or the context is cancelled.
func (g *DecisionGate) Await(ctx context.Context) (Decision, error) { ... }

// Submit sends a decision. Non-blocking; returns false if no one is waiting.
func (g *DecisionGate) Submit(d Decision) bool { ... }

// PendingRequest returns the current decision request, or nil.
func (g *DecisionGate) PendingRequest() *DecisionRequest { ... }
```

### New: `core/dag/controller.go`

The DAGController acts as a LayerGate callback. It classifies failures after each layer and either continues or pauses.

```go
// NodeFailureNotifier is called by the controller to notify about failures.
// The bridge implementation converts these to ActivityEvents.
type NodeFailureNotifier func(dagID string, failed []FailedNodeInfo, critical bool)

// DAGController implements the reconciliation pattern for DAG execution.
type DAGController struct {
    dag      *DAG
    gate     *DecisionGate
    notifier NodeFailureNotifier
}

func NewDAGController(d *DAG, notifier NodeFailureNotifier) *DAGController { ... }

// Gate returns the DecisionGate for external signaling.
func (c *DAGController) Gate() *DecisionGate { ... }

// LayerGateFunc returns a LayerGate closure wired to this controller.
func (c *DAGController) LayerGateFunc() LayerGate { ... }

// --- internal ---

// evaluateLayer is the LayerGate callback. Called after each layer completes.
func (c *DAGController) evaluateLayer(ctx context.Context, dagID string, layerIdx int, results map[string]*NodeResult) error {
    failedNodes := c.collectLayerFailures(layerIdx, results)
    if len(failedNodes) == 0 { return nil }

    critical := c.wouldBlockEntireDAG(failedNodes, results)
    c.notifier(dagID, failedNodes, critical)

    if !critical { return nil }

    // Park: set pending request, await decision
    c.gate.setPendingRequest(&DecisionRequest{...})
    decision, err := c.gate.Await(ctx)
    if err != nil { return err }

    switch decision {
    case DecisionRetry:  return ErrRetryLayer
    case DecisionAbort:  return ErrDAGCancelled
    default:             return ErrDAGCancelled
    }
}

// collectLayerFailures returns FailedNodeInfo for each failed node in the given layer.
func (c *DAGController) collectLayerFailures(layerIdx int, results map[string]*NodeResult) []FailedNodeInfo { ... }

// wouldBlockEntireDAG returns true if, after blocking all transitive dependents
// of the failed nodes, no pending nodes remain viable.
func (c *DAGController) wouldBlockEntireDAG(failed []FailedNodeInfo, results map[string]*NodeResult) bool {
    blocked := make(map[string]struct{})
    for _, f := range failed {
        blocked[f.NodeID] = struct{}{}
        for dep := range c.computeTransitiveDependents(f.NodeID) {
            blocked[dep] = struct{}{}
        }
    }
    // If ANY node is not terminal and not in the blocked set, DAG can still progress.
    for _, node := range c.dag.Nodes() {
        if node.State().IsTerminal() { continue }
        if _, isBlocked := blocked[node.ID()]; !isBlocked { return false }
    }
    return true
}

// computeTransitiveDependents returns all node IDs transitively dependent on nodeID (BFS).
func (c *DAGController) computeTransitiveDependents(nodeID string) map[string]struct{} { ... }

// nodeDisplayName extracts the human-readable name from node metadata.
func (c *DAGController) nodeDisplayName(nodeID string) string { ... }
```

### Modified: `core/dag/types.go`

```go
// Add to DAGState enum:
DAGStatePaused  // DAG is paused due to critical failure, awaiting user decision

// Add to EventType enum:
EventDAGPaused
EventNodeNonCriticalFailure

// Add sentinel error:
var ErrRetryLayer = errors.New("retry current layer")

// Update DefaultExecutionPolicy:
func DefaultExecutionPolicy() ExecutionPolicy {
    return ExecutionPolicy{
        ...
        DefaultRetries: 3,   // was 0
        ...
    }
}
```

### Modified: `core/dag/node.go`

Add a method to reset a node for user-initiated retry:

```go
// ResetForRetry resets the node to Pending with a fresh retry counter.
// Used when the user explicitly requests a layer retry after a critical failure.
func (n *Node) ResetForRetry() {
    n.mu.Lock()
    defer n.mu.Unlock()
    n.state = NodeStatePending
    n.result = nil
    n.retryCount = 0
    n.startTime = time.Time{}
}
```

### Modified: `core/dag/executor.go`

Three changes:

**1. Layer verdict enum and gate-first control flow in `executeAndCheckLayer`:**

When a LayerGate is set, it becomes the sole authority for layer progression. The FailurePolicy short-circuit is bypassed — the gate subsumes it.

```go
type layerVerdict int

const (
    layerVerdictContinue layerVerdict = iota
    layerVerdictStop
    layerVerdictRetry
)

func (e *Executor) executeAndCheckLayer(layerIdx int, layer []string) layerVerdict {
    e.mu.Lock()
    e.currentLayer = layerIdx
    e.mu.Unlock()

    e.emitLayerStarted(layerIdx, len(layer))
    err := e.executeLayer(layer)
    e.emitLayerCompleted(layerIdx)

    // Gate-first: when a LayerGate is set, delegate ALL layer decisions to it.
    if e.layerGate != nil {
        gateErr := e.invokeLayerGate(layerIdx)
        if errors.Is(gateErr, ErrRetryLayer) {
            return layerVerdictRetry
        }
        if gateErr != nil {
            return layerVerdictStop
        }
        return layerVerdictContinue
    }

    // Legacy: no gate, use policy-based short-circuit.
    if err != nil && e.policy.FailurePolicy == FailurePolicyFailFast {
        return layerVerdictStop
    }
    return layerVerdictContinue
}
```

**2. Layer retry loop in `executeLayers`:**

```go
func (e *Executor) executeLayers() *DAGResult {
    layers := e.dag.ExecutionOrder()
    layerIdx := 0
    for layerIdx < len(layers) {
        if e.cancelled.Load() {
            break
        }
        verdict := e.executeAndCheckLayer(layerIdx, layers[layerIdx])
        switch verdict {
        case layerVerdictStop:
            return e.buildResult()
        case layerVerdictRetry:
            e.resetLayerForRetry(layers[layerIdx])
            continue // same layerIdx
        default:
            layerIdx++
        }
    }
    return e.buildResult()
}
```

**3. `resetLayerForRetry` — reset failed nodes, preserve succeeded ones:**

```go
func (e *Executor) resetLayerForRetry(nodeIDs []string) {
    e.mu.Lock()
    defer e.mu.Unlock()

    for _, nodeID := range nodeIDs {
        node, ok := e.dag.GetNode(nodeID)
        if !ok {
            continue
        }
        // Only reset non-succeeded nodes — succeeded nodes keep their results.
        if node.State() == NodeStateSucceeded {
            continue
        }
        node.ResetForRetry()
        delete(e.nodeResults, nodeID)
    }
}
```

**4. Skip already-terminal nodes in `nodeForLayer`:**

```go
func (e *Executor) nodeForLayer(nodeID string) *Node {
    node, ok := e.dag.GetNode(nodeID)
    if !ok {
        return nil
    }

    // Skip nodes already in a terminal state (succeeded from a prior attempt in this layer).
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
```

### Modified: `core/dag/scheduler.go`

Add `ExecutionOption` to pass the LayerGate through `Submit`:

```go
// ExecutionOption configures an Executor created by the Scheduler.
type ExecutionOption func(*Executor)

// WithLayerGate sets a LayerGate on the executor.
func WithLayerGate(gate LayerGate) ExecutionOption {
    return func(e *Executor) { e.SetLayerGate(gate) }
}

// Submit now accepts options:
func (s *Scheduler) Submit(ctx context.Context, dag *DAG, dispatcher NodeDispatcher, opts ...ExecutionOption) (string, error) {
    ...
    executor := s.createExecutor(dag)
    for _, opt := range opts {
        opt(executor)
    }
    ...
}
```

Also update `SchedulerService` interface to match.

### Modified: `agents/orchestrator/dag_bridge.go`

Wire the controller in `Execute()`:

```go
// In ActiveDAGMeta, add:
Gate *dag.DecisionGate

// In Execute():
func (b *DAGBridge) Execute(ctx context.Context, d *dag.DAG, planID, sessionID string) (string, error) {
    // ... existing WAL + SQLite + dispatcher creation ...

    // Create controller with activity notifier
    controller := dag.NewDAGController(d, b.nodeFailureNotifier(planID))
    gate := controller.Gate()

    // Track active DAG (add gate)
    b.mu.Lock()
    b.activeDAGs[d.ID()] = &ActiveDAGMeta{
        PlanID:     planID,
        SessionID:  sessionID,
        Dispatcher: dispatcher,
        CancelFunc: dagCancel,
        StartedAt:  time.Now(),
        Gate:       gate,
    }
    b.mu.Unlock()

    // Submit with LayerGate option
    _, err := b.scheduler.Submit(dagCtx, d, dispatcher, dag.WithLayerGate(controller.LayerGateFunc()))
    // ...
}

// SubmitDecision resolves a paused DAG.
func (b *DAGBridge) SubmitDecision(dagID string, decision dag.Decision) error {
    b.mu.RLock()
    meta, ok := b.activeDAGs[dagID]
    b.mu.RUnlock()
    if !ok || meta.Gate == nil {
        return fmt.Errorf("no paused DAG: %s", dagID)
    }
    if !meta.Gate.Submit(decision) {
        return fmt.Errorf("DAG %s is not awaiting a decision", dagID)
    }
    return nil
}

// nodeFailureNotifier returns a closure that publishes activity events.
func (b *DAGBridge) nodeFailureNotifier(planID string) dag.NodeFailureNotifier {
    return func(dagID string, failed []dag.FailedNodeInfo, critical bool) {
        if critical {
            b.publishCriticalFailure(dagID, planID, failed)
        } else {
            for _, f := range failed {
                b.publishActivity(events.EventTypeAgentError,
                    fmt.Sprintf("%s failed! Non-blocking and continuing.", f.NodeName))
            }
        }
    }
}

func (b *DAGBridge) publishCriticalFailure(dagID, planID string, failed []dag.FailedNodeInfo) {
    // Build a rich, user-readable message.
    names := make([]string, len(failed))
    for i, f := range failed {
        names[i] = fmt.Sprintf("%q", f.NodeName)
    }
    totalBlocked := 0
    for _, f := range failed {
        totalBlocked += f.BlockedCount
    }

    msg := fmt.Sprintf("Task %s failed after %d retries. %d downstream tasks blocked — DAG execution paused. "+
        "You can ask me to retry the failed task or abort the plan.",
        strings.Join(names, " and "), failed[0].MaxAttempts, totalBlocked)

    b.publishActivity(events.EventTypeFailure, msg)
}
```

### Modified: `agents/orchestrator/skills_ingest.go`

Set `DefaultRetries: 3` in `buildDAGFromHandoff`:

```go
policy := dag.ExecutionPolicy{
    ...
    DefaultRetries: 3,  // was 1
    ...
}
```

### Modified: `agents/orchestrator/dag_bridge.go` (config)

Update `DefaultDAGBridgeConfig`:

```go
func DefaultDAGBridgeConfig() DAGBridgeConfig {
    return DAGBridgeConfig{
        ...
        DefaultRetries: 3,  // was 1
        ...
    }
}
```

### New: `agents/orchestrator/skills_decision.go`

Two new skills for the orchestrator's LLM to call:

```go
func retryDAGSkill(o *Orchestrator) *skills.Skill {
    // "retry_dag" — retries failed nodes in a paused DAG.
    // Looks up active DAG, submits DecisionRetry to its DecisionGate.
    // Handler: o.dagBridge.SubmitDecision(dagID, dag.DecisionRetry)
}

func abortDAGSkill(o *Orchestrator) *skills.Skill {
    // "abort_dag" — aborts a paused DAG.
    // Looks up active DAG, submits DecisionAbort to its DecisionGate.
    // Handler: o.dagBridge.SubmitDecision(dagID, dag.DecisionAbort)
}
```

### Modified: `agents/orchestrator/skills.go`

Register the new skills:

```go
func (o *Orchestrator) registerCoreSkills() {
    // ... existing registrations ...
    o.skills.Register(retryDAGSkill(o))
    o.skills.Register(abortDAGSkill(o))
}
```

## Key Design Decisions

1. **Gate-first control flow**: When a LayerGate is set, it is the sole authority. The FailurePolicy short-circuit is bypassed entirely. This means the gate always fires, even on failure, giving the controller full visibility.

2. **`FailurePolicyContinue` semantics within a layer**: The existing executor already runs all nodes in a layer concurrently via `wg.Wait()`. Failures within a layer don't cancel sibling nodes. The policy only affects between-layer behavior, which the gate now controls.

3. **Sentinel error for retry**: `ErrRetryLayer` signals the executor to replay the current layer. This preserves the `LayerGate` interface (`func(...) error`) and is checked via `errors.Is()`.

4. **`ResetForRetry`**: Resets node state to Pending, clears result, zeros retry count. Only applied to non-succeeded nodes in the layer — succeeded nodes keep their results and are skipped on replay.

5. **No UI changes**: All user communication flows through the existing `ActivityEventBus` → `ActivityBridge` → `chat.Model` pipeline. The user responds via natural language, the Guide routes to the orchestrator, and the LLM calls the appropriate skill.

6. **DecisionGate channel capacity**: Size 1, non-blocking submit. If no one is waiting (DAG already proceeded), the decision is dropped harmlessly.

## Test Plan

1. **Controller classification tests** (`core/dag/controller_test.go`):
   - Linear DAG: A→B→C, A fails → critical (B and C blocked, nothing viable)
   - Diamond DAG: A→C, B→C, A fails → non-critical (B still viable)
   - Wide DAG: A, B, C independent — A fails → non-critical
   - All-fail: A and B both fail, C depends on both → critical

2. **DecisionGate tests** (`core/dag/decision_test.go`):
   - Await returns when Submit is called
   - Await returns error when context cancelled
   - Submit returns false when no one is waiting

3. **Executor retry tests** (`core/dag/executor_test.go`):
   - `ErrRetryLayer` causes layer replay
   - Succeeded nodes are not re-dispatched on retry
   - Failed nodes get fresh retry counts on replay
   - Normal flow (no gate) unaffected

4. **Integration: skill → gate** (`agents/orchestrator/`):
   - `retry_dag` skill unblocks paused DAG
   - `abort_dag` skill cancels paused DAG
   - Skill on non-paused DAG returns error

5. **Existing tests**: `go test ./core/dag/... ./agents/orchestrator/...` — verify no regressions
