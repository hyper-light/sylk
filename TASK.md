# Wave 2 - Parallel Group 2A (Max Parallel Order)

This plan orders tasks to maximize parallel execution. Each track can run in parallel with other tracks. Tasks within a track are sequential.

## Parallel Order

### Track A: 0.2 DAG Engine
**Architecture match**: `ARCHITECTURE.md#L32844` (DAG Planning) and `ARCHITECTURE.md#L32898` (DAG Executor Signaling Layer)
**Code references**: `core/dag/dag.go`, `core/dag/executor.go`, `core/dag/scheduler.go`, `core/dag/validator.go`, `core/dag/types.go`, `core/dag/interfaces.go`

**A1. Define DAG schema and validation**
- Implementation example:
  ```go
  // core/dag/types.go
  type DAG struct {
      ID        string
      SessionID string
      Nodes     []*Node
      Layers    [][]string
      Policy    ExecutionPolicy
  }

  func (d *DAG) Validate() error {
      if d.ID == "" || d.SessionID == "" {
          return ErrInvalidDAG
      }
      return ValidateNoCycles(d.Nodes)
  }
  ```
- Acceptance criteria:
  - DAG fields match `ARCHITECTURE.md#L32846` (id, session_id, nodes, execution_order, policy).
  - Validation rejects missing IDs and cyclic graphs.
  - Unit tests cover: missing IDs, cyclic DAG, empty nodes, valid DAG.
- Dependencies: none.

**A2. Compute layered execution order**
- Implementation example:
  ```go
  // core/dag/validator.go
  func ComputeLayers(nodes []*Node) ([][]string, error) {
      order, err := TopoSort(nodes)
      if err != nil {
          return nil, err
      }
      return BuildLayeredOrder(order, nodes), nil
  }
  ```
- Acceptance criteria:
  - Independent nodes appear in the same layer.
  - Ordering is deterministic within each layer.
  - Tests cover linear, branching, and diamond DAGs.
- Dependencies: A1.

**A3. Execute DAG layers with signaling callbacks**
- Implementation example:
  ```go
  // core/dag/executor.go
  type Executor struct {
      callbacks DAGExecutorCallbacks
  }

  func (e *Executor) Execute(ctx context.Context, dag *DAG) error {
      for layerIdx, layer := range dag.Layers {
          if err := e.runLayer(ctx, dag, layer); err != nil {
              return err
          }
          _ = e.callbacks.OnLayerComplete(ctx, &LayerCompleteSignal{
              WorkflowID: dag.ID,
              LayerIndex: layerIdx,
              TotalLayers: len(dag.Layers),
          })
      }
      return nil
  }
  ```
- Acceptance criteria:
  - Tasks in a layer run in parallel; layers run sequentially.
  - Signals are emitted per `ARCHITECTURE.md#L32909` on task completion, failure, layer completion, and workflow completion.
  - Retry and fail_fast policies respected.
  - Tests cover: fail_fast cancellation, retries exhausted, signal emission.
- Dependencies: A1, A2.

### Track B: 0.3 Worker Pool Enhancements
**Architecture match**: `ARCHITECTURE.md#L31881` (Single-Worker Pipeline Architecture) and `ARCHITECTURE.md#L5118` (Goroutine Model)
**Code references**: `agents/guide/worker_pool.go`, `agents/guide/worker_pool_test.go`, `core/concurrency/pipeline_runner.go`

**B1. Worker pool interface contract and stats**
- Implementation example:
  ```go
  // agents/guide/worker_pool.go
  type WorkerPool interface {
      Submit(ctx context.Context, job Job) error
      Shutdown(ctx context.Context) error
      Stats() PoolStats
  }
  ```
- Acceptance criteria:
  - Fixed worker count, bounded concurrency.
  - Submit honors context cancellation and timeouts.
  - Stats include submitted, completed, failed, dropped.
  - Tests cover saturation, cancellation, and shutdown.
- Dependencies: none.

**B2. Enforce single worker type per pipeline**
- Implementation example:
  ```go
  // core/concurrency/pipeline_runner.go
  func (r *PipelineRunner) Start(ctx context.Context, p *Pipeline) error {
      if p.WorkerType == "" {
          return ErrNoWorkerType
      }
      return r.runTDDLoop(ctx, p)
  }
  ```
- Acceptance criteria:
  - Pipeline requires exactly one WorkerType (Designer or Engineer).
  - Inspector/Tester/Worker execute sequentially in one goroutine.
  - Tests cover missing worker type and rejection of multiple workers.
- Dependencies: none.

### Track C: 0.17 Goroutine Model & Agent Lifecycle
**Architecture match**: `ARCHITECTURE.md#L5118` (Goroutine Model)
**Code references**: `core/concurrency/lifecycle.go`, `core/concurrency/agent_runner.go`, `core/concurrency/pipeline_runner.go`

**C1. Lifecycle state machine**
- Implementation example:
  ```go
  // core/concurrency/lifecycle.go
  type Lifecycle struct {
      state State
      mu    sync.Mutex
  }

  func (l *Lifecycle) Transition(to State) error {
      if !validTransition(l.state, to) {
          return ErrInvalidTransition
      }
      l.state = to
      return nil
  }
  ```
- Acceptance criteria:
  - Valid transitions enforced: Created→Starting→Running→Stopping→Stopped or Failed.
  - Invalid transitions return error without mutation.
  - Tests cover all valid/invalid transitions and concurrent calls.
- Dependencies: none.

**C2. Agent runner with panic recovery and shutdown**
- Implementation example:
  ```go
  // core/concurrency/agent_runner.go
  func (r *AgentRunner) Run(ctx context.Context, fn func(ctx context.Context) error) error {
      defer r.recoverPanic()
      return fn(ctx)
  }
  ```
- Acceptance criteria:
  - Panic transitions to Failed and returns error.
  - Shutdown honors context deadline.
  - Tests cover cancellation, panic recovery, and timeout.
- Dependencies: C1.

### Track D: 0.18 Pipeline Scheduler
**Architecture match**: `ARCHITECTURE.md#L5223` (Orchestrator: Pipeline Scheduling)
**Code references**: `core/concurrency/scheduler.go`, `core/concurrency/priority_queue.go`, `core/session/pipeline_scheduler.go`

**D1. Local pipeline scheduler (single session)**
- Implementation example:
  ```go
  // core/concurrency/scheduler.go
  func (s *PipelineScheduler) Schedule(p *Pipeline) {
      if s.dependenciesSatisfied(p) {
          s.ready.Push(p)
          s.tryDispatch()
      } else {
          s.waiting[p.ID] = p
      }
  }
  ```
- Acceptance criteria:
  - Enforces `runtime.NumCPU()` concurrent limit.
  - Priority ordering: higher priority first, then earlier spawn time.
  - Eager scheduling on completion.
  - Tests cover dependency waiting, fairness, and max concurrency.
- Dependencies: none.

**D2. Global scheduler fairness across sessions**
- Implementation example:
  ```go
  // core/session/pipeline_scheduler.go
  func (s *GlobalPipelineScheduler) Schedule(sessionID string, p *Pipeline) {
      s.globalQueue.Push(sessionID, p)
      s.tryDispatch()
  }
  ```
- Acceptance criteria:
  - Per-session slot allocations enforced.
  - No starvation across sessions.
  - Tests cover cross-session preemption and slot rebalance.
- Dependencies: D1.

### Track E: 0.21 Adaptive Channels
**Architecture match**: `ARCHITECTURE.md#L5616` (Channel Architecture)
**Code references**: `core/concurrency/adaptive_channel.go`, `core/concurrency/adaptive_channel_test.go`

**E1. Unbounded channel for user input**
- Implementation example:
  ```go
  // core/concurrency/adaptive_channel.go
  type UnboundedChannel struct {
      ch       chan Message
      overflow []Message
      mu       sync.Mutex
      cond     *sync.Cond
  }
  ```
- Acceptance criteria:
  - Send never blocks and never drops user messages.
  - Receive honors context cancellation.
  - Tests cover overflow drain ordering and cancellation behavior.
- Dependencies: none.

**E2. Adaptive channel resizing**
- Implementation example:
  ```go
  // core/concurrency/adaptive_channel.go
  func (ac *AdaptiveChannel) adaptLoop() {
      // Grow after 3 checks > 80%, shrink after 10 checks < 20%
  }
  ```
- Acceptance criteria:
  - Initial size 64, min 16, max 4096.
  - Grows after 3 consecutive >80% utilization ticks.
  - Shrinks after 10 consecutive <20% utilization ticks.
  - Send uses timeout then overflows; only fails on ctx cancel.
  - Tests cover grow/shrink, timeout overflow, and concurrency.
- Dependencies: E1.
