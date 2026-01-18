# Goroutine & Operation Lifecycle Management System

**Zero-leak guarantee, user-controllable, boundary-enforced**

---

## Design Principles

1. **Zero leaks** - Goroutines cannot leak; all are tracked, bounded, and terminable
2. **User control** - Stop/Pause/Kill commands work immediately and completely
3. **Boundary enforcement** - All external operations (LLM, tools, files, network) go through tracked interceptors
4. **Graceful degradation** - Escalating termination: cancel → force-close → orphan-track → restart

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    OPERATION LIFECYCLE MANAGEMENT SYSTEM                             │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  USER COMMANDS                                                                      │
│  ─────────────────────────────────────────────────────────────────────────────────  │
│  STOP:  Signal completion → wait for clean shutdown → verify zero operations       │
│  PAUSE: Checkpoint state → stop new work → suspend in-flight → verify frozen       │
│  KILL:  Cancel all contexts → force deadline → orphan tracking → restart if stuck  │
│                                                                                     │
│  ┌───────────────────────────────────────────────────────────────────────────────┐ │
│  │                        PIPELINE CONTROLLER                                     │ │
│  │   Receives user commands, orchestrates response across all agents              │ │
│  └───────────────────────────────────────────────────────────────────────────────┘ │
│                                       │                                             │
│           ┌───────────────────────────┼───────────────────────────────────────┐    │
│           ▼                           ▼                           ▼            │    │
│  ┌─────────────────────┐   ┌─────────────────────┐   ┌─────────────────────┐  │    │
│  │   AGENT SUPERVISOR  │   │   AGENT SUPERVISOR  │   │   AGENT SUPERVISOR  │  │    │
│  │   (per agent)       │   │   (per agent)       │   │   (per agent)       │  │    │
│  │                     │   │                     │   │                     │  │    │
│  │   ┌─────────────┐   │   │   ┌─────────────┐   │   │   ┌─────────────┐   │  │    │
│  │   │ Goroutine   │   │   │   │ Goroutine   │   │   │   │ Goroutine   │   │  │    │
│  │   │ Scope       │   │   │   │ Scope       │   │   │   │ Scope       │   │  │    │
│  │   └─────────────┘   │   │   └─────────────┘   │   │   └─────────────┘   │  │    │
│  │   ┌─────────────┐   │   │   ┌─────────────┐   │   │   ┌─────────────┐   │  │    │
│  │   │ Operation   │   │   │   │ Operation   │   │   │   │ Operation   │   │  │    │
│  │   │ Tracker     │   │   │   │ Tracker     │   │   │   │ Tracker     │   │  │    │
│  │   └─────────────┘   │   │   └─────────────┘   │   │   └─────────────┘   │  │    │
│  │   ┌─────────────┐   │   │   ┌─────────────┐   │   │   ┌─────────────┐   │  │    │
│  │   │ Resource    │   │   │   │ Resource    │   │   │   │ Resource    │   │  │    │
│  │   │ Tracker     │   │   │   │ Tracker     │   │   │   │ Tracker     │   │  │    │
│  │   └─────────────┘   │   │   └─────────────┘   │   │   └─────────────┘   │  │    │
│  └─────────────────────┘   └─────────────────────┘   └─────────────────────┘  │    │
│           │                           │                           │            │    │
│           └───────────────────────────┼───────────────────────────┘            │    │
│                                       ▼                                         │    │
│  ┌───────────────────────────────────────────────────────────────────────────────┐ │
│  │                      BOUNDARY INTERCEPTORS                                     │ │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐          │ │
│  │  │ LLM Client   │ │ Tool Executor│ │ VFS Layer    │ │ Network Pool │          │ │
│  │  │              │ │              │ │              │ │              │          │ │
│  │  │ • Timeout    │ │ • Subprocess │ │ • Handle     │ │ • Conn       │          │ │
│  │  │ • Cancel     │ │ • Timeout    │ │   tracking   │ │   tracking   │          │ │
│  │  │ • Track      │ │ • Sandbox    │ │ • Force      │ │ • Force      │          │ │
│  │  │              │ │ • Track      │ │   close      │ │   close      │          │ │
│  │  └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘          │ │
│  └───────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  TERMINATION ESCALATION:                                                            │
│  ─────────────────────────────────────────────────────────────────────────────────  │
│  1. Cancel context (signal all operations to stop)                    [0-100ms]    │
│  2. Wait for voluntary termination                                    [100ms-1s]   │
│  3. Force-close all boundary resources (connections, handles)         [1s-2s]      │
│  4. If still stuck: mark agent tainted, isolate, restart             [2s-5s]      │
│  5. If restart fails: orphan tracking, continue degraded             [5s+]        │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Layer 1: Compile-Time Enforcement

**Custom linter rule** forbids raw `go` statements:

```yaml
# .golangci.yml
linters-settings:
  forbidigo:
    forbid:
      - pattern: "^go\\s+func"
        msg: "raw 'go' statements forbidden - use scope.Go() instead"
      - pattern: "^go\\s+\\w+\\("
        msg: "raw 'go' statements forbidden - use scope.Go() instead"
```

Custom analyzer for CI:

```go
// cmd/nogo/main.go
package main

import (
    "go/ast"
    "strings"
    "golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
    Name: "nogo",
    Doc:  "forbids raw go statements outside of approved packages",
    Run:  run,
}

func run(pass *analysis.Pass) (interface{}, error) {
    // Whitelist: only core/concurrency can use raw go
    if strings.Contains(pass.Pkg.Path(), "core/concurrency") {
        return nil, nil
    }

    for _, file := range pass.Files {
        ast.Inspect(file, func(n ast.Node) bool {
            if goStmt, ok := n.(*ast.GoStmt); ok {
                pass.Reportf(goStmt.Pos(),
                    "raw 'go' statement forbidden - use scope.Go() from core/concurrency")
            }
            return true
        })
    }
    return nil, nil
}
```

---

## Layer 2: Goroutine Scope (Structural Cooperation)

```go
// WorkFunc is the ONLY signature accepted - forces context awareness
type WorkFunc func(ctx context.Context) error

// GoroutineScope manages all goroutines for an agent
type GoroutineScope struct {
    agentID     string
    parentCtx   context.Context
    cancel      context.CancelFunc

    mu          sync.Mutex
    workers     map[uint64]*worker
    nextID      uint64

    wg          sync.WaitGroup
    budget      *GoroutineBudget

    maxLifetime time.Duration  // Absolute maximum goroutine lifetime

    shutdownMu       sync.Mutex
    shutdownStarted  bool
    shutdownComplete chan struct{}
}

type worker struct {
    id          uint64
    ctx         context.Context
    cancel      context.CancelFunc
    startedAt   time.Time
    deadline    time.Time
    description string
    done        chan struct{}
    err         error
}

func NewGoroutineScope(parentCtx context.Context, agentID string, budget *GoroutineBudget) *GoroutineScope {
    ctx, cancel := context.WithCancel(parentCtx)
    return &GoroutineScope{
        agentID:          agentID,
        parentCtx:        ctx,
        cancel:           cancel,
        workers:          make(map[uint64]*worker),
        budget:           budget,
        maxLifetime:      5 * time.Minute,
        shutdownComplete: make(chan struct{}),
    }
}

// Go spawns a tracked, bounded goroutine.
// The function MUST return when ctx is cancelled.
func (s *GoroutineScope) Go(description string, timeout time.Duration, fn WorkFunc) error {
    s.mu.Lock()
    if s.shutdownStarted {
        s.mu.Unlock()
        return ErrScopeShutdown
    }

    // Enforce maximum lifetime
    if timeout > s.maxLifetime || timeout == 0 {
        timeout = s.maxLifetime
    }

    // Budget check
    if err := s.budget.Acquire(s.agentID); err != nil {
        s.mu.Unlock()
        return err
    }

    id := s.nextID
    s.nextID++

    // Create worker context with deadline
    workerCtx, workerCancel := context.WithTimeout(s.parentCtx, timeout)

    w := &worker{
        id:          id,
        ctx:         workerCtx,
        cancel:      workerCancel,
        startedAt:   time.Now(),
        deadline:    time.Now().Add(timeout),
        description: description,
        done:        make(chan struct{}),
    }
    s.workers[id] = w
    s.wg.Add(1)
    s.mu.Unlock()

    go s.runWorker(w, fn)

    return nil
}

func (s *GoroutineScope) runWorker(w *worker, fn WorkFunc) {
    defer func() {
        if r := recover(); r != nil {
            w.err = fmt.Errorf("panic: %v\n%s", r, captureStack(2))
        }

        w.cancel()
        close(w.done)

        s.mu.Lock()
        delete(s.workers, w.id)
        s.mu.Unlock()

        s.budget.Release(s.agentID)
        s.wg.Done()
    }()

    w.err = fn(w.ctx)
}

// Shutdown terminates all goroutines and BLOCKS until confirmed.
func (s *GoroutineScope) Shutdown(gracePeriod, hardDeadline time.Duration) error {
    s.shutdownMu.Lock()
    if s.shutdownStarted {
        s.shutdownMu.Unlock()
        <-s.shutdownComplete
        return nil
    }
    s.shutdownStarted = true
    s.shutdownMu.Unlock()

    defer close(s.shutdownComplete)

    // Phase 1: Signal all workers to stop
    s.cancel()

    // Phase 2: Wait for graceful termination
    graceDone := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(graceDone)
    }()

    select {
    case <-graceDone:
        return nil

    case <-time.After(gracePeriod):
        // Phase 3: Force cancel individual workers
        s.mu.Lock()
        for _, w := range s.workers {
            w.cancel()
        }
        remaining := len(s.workers)
        s.mu.Unlock()

        if remaining == 0 {
            return nil
        }

        // Phase 4: Wait for hard deadline
        select {
        case <-graceDone:
            return nil

        case <-time.After(hardDeadline - gracePeriod):
            // CRITICAL: Goroutines failed to terminate
            s.mu.Lock()
            leaked := make([]string, 0, len(s.workers))
            for _, w := range s.workers {
                leaked = append(leaked, fmt.Sprintf(
                    "worker[%d] desc=%q started=%v deadline=%v",
                    w.id, w.description, w.startedAt, w.deadline,
                ))
            }
            s.mu.Unlock()

            return &GoroutineLeakError{
                AgentID:     s.agentID,
                LeakedCount: len(leaked),
                Workers:     leaked,
                StackDump:   captureAllStacks(),
            }
        }
    }
}

var ErrScopeShutdown = errors.New("goroutine scope is shutting down")

type GoroutineLeakError struct {
    AgentID     string
    LeakedCount int
    Workers     []string
    StackDump   string
}

func (e *GoroutineLeakError) Error() string {
    return fmt.Sprintf("CRITICAL: %d goroutines leaked in agent %s: %v",
        e.LeakedCount, e.AgentID, e.Workers)
}
```

---

## Layer 3: Dynamic Goroutine Budget

```go
type GoroutineBudget struct {
    mu sync.RWMutex

    agents        map[string]*agentBudgetState
    totalActive   int64
    systemLimit   int64

    // Dynamic adjustment via memory pressure
    pressureLevel    *atomic.Int32
    burstMultiplier  float64

    // Agent type weights
    typeWeights map[string]float64

    // Callbacks
    onWarning func(agentID string, active, softLimit int)
    onBlocked func(agentID string, active, hardLimit int)
}

type agentBudgetState struct {
    agentID   string
    agentType string
    active    int64
    peak      int64
    softLimit int64
    hardLimit int64
    waiters   int32
    cond      *sync.Cond
}

func NewGoroutineBudget(pressureLevel *atomic.Int32) *GoroutineBudget {
    systemLimit := int64(runtime.GOMAXPROCS(0)) * 1000

    return &GoroutineBudget{
        agents:          make(map[string]*agentBudgetState),
        systemLimit:     systemLimit,
        pressureLevel:   pressureLevel,
        burstMultiplier: 1.5,
        typeWeights: map[string]float64{
            "engineer":    1.0,
            "architect":   0.5,
            "librarian":   0.3,
            "archivalist": 0.3,
            "inspector":   0.5,
            "tester":      0.8,
            "guide":       0.2,
        },
    }
}

// Acquire requests a goroutine slot (blocks if over hard limit)
func (b *GoroutineBudget) Acquire(agentID string) error {
    b.mu.RLock()
    state, exists := b.agents[agentID]
    b.mu.RUnlock()

    if !exists {
        return fmt.Errorf("agent %s not registered", agentID)
    }

    state.cond.L.Lock()
    defer state.cond.L.Unlock()

    // Block if at hard limit
    for atomic.LoadInt64(&state.active) >= state.hardLimit {
        if b.onBlocked != nil {
            b.onBlocked(agentID, int(state.active), int(state.hardLimit))
        }
        atomic.AddInt32(&state.waiters, 1)
        state.cond.Wait()
        atomic.AddInt32(&state.waiters, -1)
    }

    newActive := atomic.AddInt64(&state.active, 1)
    atomic.AddInt64(&b.totalActive, 1)

    // Warn if over soft limit
    if newActive > state.softLimit && b.onWarning != nil {
        b.onWarning(agentID, int(newActive), int(state.softLimit))
    }

    return nil
}

func (b *GoroutineBudget) Release(agentID string) {
    b.mu.RLock()
    state, exists := b.agents[agentID]
    b.mu.RUnlock()

    if !exists {
        return
    }

    atomic.AddInt64(&state.active, -1)
    atomic.AddInt64(&b.totalActive, -1)

    if atomic.LoadInt32(&state.waiters) > 0 {
        state.cond.L.Lock()
        state.cond.Signal()
        state.cond.L.Unlock()
    }
}

// OnPressureChange adjusts limits based on memory pressure
func (b *GoroutineBudget) OnPressureChange(level PressureLevel) {
    b.pressureLevel.Store(int32(level))
    b.mu.Lock()
    defer b.mu.Unlock()
    b.recalculateLimitsLocked()
}

func (b *GoroutineBudget) recalculateLimitsLocked() {
    if len(b.agents) == 0 {
        return
    }

    var totalWeight float64
    for _, state := range b.agents {
        weight := b.typeWeights[state.agentType]
        if weight == 0 {
            weight = 0.5
        }
        totalWeight += weight
    }

    // Pressure multiplier
    pressureMultiplier := 1.0
    switch PressureLevel(b.pressureLevel.Load()) {
    case PressureElevated:
        pressureMultiplier = 0.75
    case PressureHigh:
        pressureMultiplier = 0.50
    case PressureCritical:
        pressureMultiplier = 0.25
    }

    for _, state := range b.agents {
        weight := b.typeWeights[state.agentType]
        if weight == 0 {
            weight = 0.5
        }

        baseBudget := float64(b.systemLimit) * (weight / totalWeight)
        effectiveBudget := baseBudget * pressureMultiplier

        state.softLimit = int64(effectiveBudget)
        state.hardLimit = int64(effectiveBudget * b.burstMultiplier)

        if state.softLimit < 10 {
            state.softLimit = 10
        }
        if state.hardLimit < 15 {
            state.hardLimit = 15
        }
    }
}
```

---

## Layer 4: Safe Blocking Primitives

All blocking operations must be context-aware:

```go
package safechan

// Send sends on channel, respecting context cancellation
func Send[T any](ctx context.Context, ch chan<- T, value T) error {
    select {
    case ch <- value:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

// Recv receives from channel, respecting context cancellation
func Recv[T any](ctx context.Context, ch <-chan T) (T, error) {
    select {
    case v, ok := <-ch:
        if !ok {
            var zero T
            return zero, ErrChannelClosed
        }
        return v, nil
    case <-ctx.Done():
        var zero T
        return zero, ctx.Err()
    }
}

// Sleep sleeps, but wakes on context cancellation
func Sleep(ctx context.Context, d time.Duration) error {
    select {
    case <-time.After(d):
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

var ErrChannelClosed = errors.New("channel closed")
```

```go
package safelock

// Mutex is a context-aware mutex
type Mutex struct {
    ch chan struct{}
}

func NewMutex() *Mutex {
    ch := make(chan struct{}, 1)
    ch <- struct{}{}
    return &Mutex{ch: ch}
}

func (m *Mutex) Lock(ctx context.Context) error {
    select {
    case <-m.ch:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

func (m *Mutex) Unlock() {
    m.ch <- struct{}{}
}
```

---

## Agent Supervisor

```go
// AgentSupervisor manages all operations for a single agent
type AgentSupervisor struct {
    agentID       string
    agentType     string
    pipelineID    string
    sessionID     string

    ctx           context.Context
    cancel        context.CancelFunc
    state         atomic.Int32

    scope         *GoroutineScope
    operations    map[string]*Operation
    operationsMu  sync.RWMutex
    resources     *ResourceTracker

    pauseBarrier  *PauseBarrier
    checkpointer  Checkpointer

    config        AgentSupervisorConfig
}

type AgentSupervisorConfig struct {
    MaxConcurrentOps    int
    DefaultOpTimeout    time.Duration
    MaxOpTimeout        time.Duration
    GracePeriod         time.Duration
    ForceCloseDeadline  time.Duration
}

// BeginOperation starts tracking a new operation
func (s *AgentSupervisor) BeginOperation(
    opType OperationType,
    description string,
    timeout time.Duration,
) (*Operation, error) {
    // Block if paused
    if err := s.pauseBarrier.Wait(s.ctx); err != nil {
        return nil, err
    }

    if timeout == 0 || timeout > s.config.MaxOpTimeout {
        timeout = s.config.MaxOpTimeout
    }

    opCtx, opCancel := context.WithTimeout(s.ctx, timeout)

    op := &Operation{
        ID:          generateOpID(),
        Type:        opType,
        AgentID:     s.agentID,
        Description: description,
        StartedAt:   time.Now(),
        Deadline:    time.Now().Add(timeout),
        ctx:         opCtx,
        cancel:      opCancel,
        pauseCh:     make(chan struct{}),
        resumeCh:    make(chan struct{}),
        done:        make(chan struct{}),
    }

    s.operationsMu.Lock()
    if len(s.operations) >= s.config.MaxConcurrentOps {
        s.operationsMu.Unlock()
        opCancel()
        return nil, ErrTooManyOperations
    }
    s.operations[op.ID] = op
    s.operationsMu.Unlock()

    return op, nil
}

// EndOperation completes an operation and releases resources
func (s *AgentSupervisor) EndOperation(op *Operation, result any, err error) {
    op.result = result
    op.err = err
    op.cancel()

    op.resourcesMu.Lock()
    for _, res := range op.resources {
        s.resources.Release(res)
    }
    op.resources = nil
    op.resourcesMu.Unlock()

    close(op.done)

    s.operationsMu.Lock()
    delete(s.operations, op.ID)
    s.operationsMu.Unlock()
}

// CancelAll cancels all operation contexts
func (s *AgentSupervisor) CancelAll() {
    s.cancel()

    s.operationsMu.RLock()
    for _, op := range s.operations {
        op.cancel()
    }
    s.operationsMu.RUnlock()
}

// ForceCloseResources forcefully closes all tracked resources
func (s *AgentSupervisor) ForceCloseResources() {
    s.operationsMu.RLock()
    for _, op := range s.operations {
        op.resourcesMu.Lock()
        for _, res := range op.resources {
            res.ForceClose()
        }
        op.resourcesMu.Unlock()
    }
    s.operationsMu.RUnlock()

    s.resources.ForceCloseAll()
}
```

---

## Pipeline Controller (User Commands)

```go
// PipelineController handles user stop/pause/kill commands
type PipelineController struct {
    pipelineID    string
    supervisors   map[string]*AgentSupervisor
    supervisorsMu sync.RWMutex
    state         atomic.Int32
    config        PipelineControllerConfig
}

type PipelineControllerConfig struct {
    StopGracePeriod    time.Duration  // Default: 30s
    PauseTimeout       time.Duration  // Default: 5s
    KillGracePeriod    time.Duration  // Default: 2s
    KillHardDeadline   time.Duration  // Default: 5s
}

// Stop gracefully stops - waits for operations to complete
func (c *PipelineController) Stop(ctx context.Context) error {
    c.state.Store(int32(PipelineStateStopping))

    c.supervisorsMu.RLock()
    supervisors := make([]*AgentSupervisor, 0, len(c.supervisors))
    for _, s := range c.supervisors {
        supervisors = append(supervisors, s)
    }
    c.supervisorsMu.RUnlock()

    // Stop accepting new work
    for _, s := range supervisors {
        s.StopAcceptingWork()
    }

    // Wait for completion
    deadline := time.Now().Add(c.config.StopGracePeriod)
    for _, s := range supervisors {
        waitCtx, cancel := context.WithDeadline(ctx, deadline)
        err := s.WaitForCompletion(waitCtx)
        cancel()
        if err != nil {
            return err
        }
    }

    c.state.Store(int32(PipelineStateStopped))
    return nil
}

// Pause freezes pipeline - checkpoints and suspends
func (c *PipelineController) Pause(ctx context.Context) error {
    c.state.Store(int32(PipelineStatePausing))

    c.supervisorsMu.RLock()
    supervisors := make([]*AgentSupervisor, 0, len(c.supervisors))
    for _, s := range c.supervisors {
        supervisors = append(supervisors, s)
    }
    c.supervisorsMu.RUnlock()

    // Signal pause
    for _, s := range supervisors {
        s.SignalPause()
    }

    // Wait for pause
    deadline := time.Now().Add(c.config.PauseTimeout)
    for _, s := range supervisors {
        waitCtx, cancel := context.WithDeadline(ctx, deadline)
        s.WaitForPause(waitCtx)
        cancel()
    }

    c.state.Store(int32(PipelineStatePaused))
    return nil
}

// Resume continues paused pipeline
func (c *PipelineController) Resume(ctx context.Context) error {
    c.supervisorsMu.RLock()
    for _, s := range c.supervisors {
        s.SignalResume()
    }
    c.supervisorsMu.RUnlock()

    c.state.Store(int32(PipelineStateRunning))
    return nil
}

// Kill forcefully terminates - used for immediate stop
func (c *PipelineController) Kill(ctx context.Context) error {
    c.state.Store(int32(PipelineStateKilling))

    c.supervisorsMu.RLock()
    supervisors := make([]*AgentSupervisor, 0, len(c.supervisors))
    for _, s := range c.supervisors {
        supervisors = append(supervisors, s)
    }
    c.supervisorsMu.RUnlock()

    // Phase 1: Cancel all contexts
    for _, s := range supervisors {
        s.CancelAll()
    }

    // Phase 2: Wait briefly for voluntary termination
    allDone := make(chan struct{})
    go func() {
        for _, s := range supervisors {
            s.WaitForTermination(c.config.KillGracePeriod)
        }
        close(allDone)
    }()

    select {
    case <-allDone:
        c.state.Store(int32(PipelineStateKilled))
        return nil
    case <-time.After(c.config.KillGracePeriod):
    }

    // Phase 3: Force-close resources
    for _, s := range supervisors {
        s.ForceCloseResources()
    }

    // Phase 4: Wait for hard deadline
    select {
    case <-allDone:
        c.state.Store(int32(PipelineStateKilled))
        return nil
    case <-time.After(c.config.KillHardDeadline - c.config.KillGracePeriod):
    }

    // Phase 5: Mark orphans
    var orphans []OrphanedOperation
    for _, s := range supervisors {
        orphans = append(orphans, s.MarkOrphansAndReport()...)
    }

    c.state.Store(int32(PipelineStateKilled))

    if len(orphans) > 0 {
        return &PipelineKillOrphansError{PipelineID: c.pipelineID, Orphans: orphans}
    }
    return nil
}
```

---

## Boundary Interceptors

### LLM Client

```go
type TrackedLLMClient struct {
    underlying LLMClient
}

func (c *TrackedLLMClient) Complete(
    supervisor *AgentSupervisor,
    req *CompletionRequest,
) (*CompletionResponse, error) {
    op, err := supervisor.BeginOperation(
        OpTypeLLMCall,
        fmt.Sprintf("llm model=%s", req.Model),
        req.Timeout,
    )
    if err != nil {
        return nil, err
    }
    defer supervisor.EndOperation(op, nil, nil)

    resp, err := c.underlying.CompleteWithContext(op.Context(), req)
    supervisor.EndOperation(op, resp, err)
    return resp, err
}
```

### Tool Executor (Sandbox-Agnostic)

The tool executor works regardless of whether sandbox is enabled (sandbox is OFF by default).
It uses direct OS signals for process termination, with sandbox providing additional guarantees when enabled.

```go
type TrackedToolExecutor struct {
    permissionMgr *security.PermissionManager  // Always active
    sandboxMgr    *security.SandboxManager     // May be nil or disabled
    auditLogger   *security.AuditLogger        // Always active
    maxTimeout    time.Duration
}

func (e *TrackedToolExecutor) Execute(
    supervisor *AgentSupervisor,
    tool Tool,
    args map[string]any,
) (any, error) {
    timeout := tool.Timeout()
    if timeout > e.maxTimeout {
        timeout = e.maxTimeout
    }

    op, err := supervisor.BeginOperation(
        OpTypeToolExecution,
        fmt.Sprintf("tool=%s", tool.Name()),
        timeout,
    )
    if err != nil {
        return nil, err
    }

    // Permission check (ALWAYS - even without sandbox)
    perm := e.permissionMgr.CheckPermission(
        supervisor.agentID,
        security.ActionTypeCommand,
        tool.Command(),
    )
    if !perm.Allowed {
        supervisor.EndOperation(op, nil, ErrPermissionDenied)
        return nil, ErrPermissionDenied
    }

    // Audit log (ALWAYS)
    e.auditLogger.Log(security.AuditEvent{
        AgentID:   supervisor.agentID,
        Action:    "tool_execute",
        Tool:      tool.Name(),
        Timestamp: time.Now(),
    })

    // Create tracked process (sandbox-agnostic)
    proc, err := NewTrackedProcess(op.Context(), tool.Command(), tool.ArgsFor(args), e.sandboxMgr)
    if err != nil {
        supervisor.EndOperation(op, nil, err)
        return nil, err
    }

    // Track resource (ForceClose works regardless of sandbox)
    supervisor.TrackResource(op, proc)

    // Execute
    output, err := proc.Run()
    if err != nil {
        supervisor.EndOperation(op, nil, err)
        return nil, err
    }

    result, err := tool.ParseOutput(output)
    supervisor.EndOperation(op, result, err)
    return result, err
}
```

### TrackedProcess (Sandbox-Agnostic)

Works with or without sandbox using OS signals directly. Uses process groups for killing child processes.

```go
type ProcessState int32

const (
    ProcessStateCreated ProcessState = iota
    ProcessStateRunning
    ProcessStateTerminating
    ProcessStateTerminated
)

type TrackedProcess struct {
    cmd       *exec.Cmd
    pid       int
    pgid      int                          // Process group ID for killing children
    startedAt time.Time
    sandbox   *security.SandboxManager     // nil if sandbox disabled

    mu        sync.Mutex
    state     ProcessState
    doneCh    chan struct{}
    exitErr   error
}

func NewTrackedProcess(
    ctx context.Context,
    command string,
    args []string,
    sandbox *security.SandboxManager,
) (*TrackedProcess, error) {
    cmd := exec.CommandContext(ctx, command, args...)

    // CRITICAL: Set process group for child process isolation
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true,  // Create new process group
    }

    // Apply sandbox if enabled
    if sandbox != nil && sandbox.Enabled() {
        sandbox.ConfigureCommand(cmd)
    }

    return &TrackedProcess{
        cmd:     cmd,
        sandbox: sandbox,
        state:   ProcessStateCreated,
        doneCh:  make(chan struct{}),
    }, nil
}

func (p *TrackedProcess) Run() ([]byte, error) {
    p.mu.Lock()
    if p.state != ProcessStateCreated {
        p.mu.Unlock()
        return nil, errors.New("process already started")
    }
    p.state = ProcessStateRunning
    p.startedAt = time.Now()
    p.mu.Unlock()

    // Start process
    if err := p.cmd.Start(); err != nil {
        return nil, err
    }

    p.mu.Lock()
    p.pid = p.cmd.Process.Pid
    // Get process group ID
    pgid, err := syscall.Getpgid(p.pid)
    if err == nil {
        p.pgid = pgid
    } else {
        p.pgid = p.pid  // Fallback to PID
    }
    p.mu.Unlock()

    // Wait for completion in background
    go func() {
        p.exitErr = p.cmd.Wait()
        p.mu.Lock()
        p.state = ProcessStateTerminated
        p.mu.Unlock()
        close(p.doneCh)
    }()

    // Wait for done
    <-p.doneCh

    if p.exitErr != nil {
        return nil, p.exitErr
    }

    return p.cmd.Output()
}

func (p *TrackedProcess) Type() string { return "process" }
func (p *TrackedProcess) ID() string   { return fmt.Sprintf("proc-%d", p.pid) }

// ForceClose terminates the process - works with or without sandbox
func (p *TrackedProcess) ForceClose() error {
    p.mu.Lock()
    if p.state == ProcessStateTerminated {
        p.mu.Unlock()
        return nil
    }
    p.state = ProcessStateTerminating
    p.mu.Unlock()

    // Try sandbox-level kill first if available (provides additional guarantees)
    if p.sandbox != nil && p.sandbox.Enabled() {
        if err := p.sandbox.ForceKill(p.pid); err == nil {
            return p.waitTermination(500 * time.Millisecond)
        }
        // Fall through to direct OS signals
    }

    // Direct OS-level kill sequence (always works)
    return p.signalTerminationSequence()
}

// signalTerminationSequence implements 3-phase kill: SIGINT → SIGTERM → SIGKILL
func (p *TrackedProcess) signalTerminationSequence() error {
    // Phase 1: SIGINT (allow graceful shutdown)
    if err := p.signalGroup(syscall.SIGINT); err != nil {
        // Process may already be dead
        return nil
    }

    if err := p.waitTermination(100 * time.Millisecond); err == nil {
        return nil
    }

    // Phase 2: SIGTERM
    if err := p.signalGroup(syscall.SIGTERM); err != nil {
        return nil
    }

    if err := p.waitTermination(500 * time.Millisecond); err == nil {
        return nil
    }

    // Phase 3: SIGKILL (force)
    if err := p.signalGroup(syscall.SIGKILL); err != nil {
        return err
    }

    return p.waitTermination(100 * time.Millisecond)
}

// signalGroup sends signal to entire process group (kills child processes too)
func (p *TrackedProcess) signalGroup(sig syscall.Signal) error {
    p.mu.Lock()
    pgid := p.pgid
    p.mu.Unlock()

    if pgid == 0 {
        return errors.New("no process group")
    }

    // Negative PID signals entire process group
    return syscall.Kill(-pgid, sig)
}

func (p *TrackedProcess) waitTermination(timeout time.Duration) error {
    select {
    case <-p.doneCh:
        return nil
    case <-time.After(timeout):
        return errors.New("termination timeout")
    }
}
```

---

## Resource Tracker

```go
type TrackedResource interface {
    Type() string
    ID() string
    ForceClose() error
}

type ResourceTracker struct {
    mu        sync.RWMutex
    resources map[string]TrackedResource
    counts    map[string]int64
}

func (t *ResourceTracker) Track(res TrackedResource) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.resources[res.ID()] = res
    t.counts[res.Type()]++
}

func (t *ResourceTracker) Release(res TrackedResource) {
    t.mu.Lock()
    defer t.mu.Unlock()
    delete(t.resources, res.ID())
    t.counts[res.Type()]--
}

func (t *ResourceTracker) ForceCloseAll() []error {
    t.mu.Lock()
    defer t.mu.Unlock()

    var errors []error
    for id, res := range t.resources {
        if err := res.ForceClose(); err != nil {
            errors = append(errors, fmt.Errorf("force close %s: %w", id, err))
        }
    }

    t.resources = make(map[string]TrackedResource)
    t.counts = make(map[string]int64)
    return errors
}
```

---

## Pause Barrier

```go
type PauseBarrier struct {
    mu      sync.RWMutex
    engaged bool
    cond    *sync.Cond
}

func NewPauseBarrier() *PauseBarrier {
    pb := &PauseBarrier{}
    pb.cond = sync.NewCond(&pb.mu)
    return pb
}

func (pb *PauseBarrier) Engage() {
    pb.mu.Lock()
    pb.engaged = true
    pb.mu.Unlock()
}

func (pb *PauseBarrier) Release() {
    pb.mu.Lock()
    pb.engaged = false
    pb.cond.Broadcast()
    pb.mu.Unlock()
}

func (pb *PauseBarrier) Wait(ctx context.Context) error {
    pb.mu.Lock()
    for pb.engaged {
        select {
        case <-ctx.Done():
            pb.mu.Unlock()
            return ctx.Err()
        default:
        }
        pb.cond.Wait()
    }
    pb.mu.Unlock()
    return nil
}
```

---

## Summary

### Zero-Leak Guarantees

| Layer | Guarantee |
|-------|-----------|
| **Compile-time** | Raw `go` forbidden by linter |
| **Structural** | All goroutines MUST accept context |
| **Timeouts** | All goroutines have max lifetime |
| **Primitives** | All blocking ops context-aware |
| **Termination** | Shutdown blocks until verified |
| **Escalation** | Leak → force-close → orphan-track → restart |

### Termination Escalation

| Phase | Action | Timeout |
|-------|--------|---------|
| 1 | Cancel contexts | 0-100ms |
| 2 | Wait for voluntary termination | 100ms-1s |
| 3 | Force-close resources | 1s-2s |
| 4 | Mark tainted, restart agent | 2s-5s |
| 5 | Orphan tracking (last resort) | 5s+ |

### User Commands

| Command | Behavior | Use Case |
|---------|----------|----------|
| **Stop** | Wait for completion | Graceful end of work |
| **Pause** | Checkpoint + freeze | Temporary suspension |
| **Resume** | Continue from checkpoint | After pause |
| **Kill** | Immediate termination | Emergency stop |

---

## Integration Points (Sandbox-Agnostic)

This system is designed to work **regardless of sandbox state** (sandbox is OFF by default).

### Capability Matrix

| Capability | Sandbox OFF | Sandbox ON |
|------------|-------------|------------|
| **Process termination** | OS signals (SIGINT→SIGTERM→SIGKILL) | Sandbox kill + OS signals fallback |
| **Child process killing** | Process groups (`-pgid`) | Container/namespace + process groups |
| **Permission checks** | PermissionManager | PermissionManager + sandbox policies |
| **Audit logging** | AuditLogger | AuditLogger + sandbox audit trail |
| **Resource tracking** | ResourceTracker | ResourceTracker + sandbox quotas |
| **File handle tracking** | TrackedFile | VFS layer + TrackedFile |
| **Network connection tracking** | TrackedConn | Network proxy + TrackedConn |

### Integration Point 1: AgentRunner (core/concurrency/agent_runner.go)

Replace raw goroutine spawning with GoroutineScope:

```go
// BEFORE (line ~97)
go r.run(runCtx)

// AFTER
func (r *AgentRunner) Start(ctx context.Context) error {
    r.supervisor = NewAgentSupervisor(ctx, r.agentID, r.agentType, r.pipelineID)

    return r.supervisor.scope.Go("agent-main-loop", 0, func(ctx context.Context) error {
        return r.run(ctx)
    })
}
```

### Integration Point 2: Lifecycle State Machine (core/concurrency/lifecycle.go)

Extend existing states with pause support:

```go
// Existing states (keep)
const (
    StateCreated LifecycleState = iota
    StateStarting
    StateRunning
    StateStopping
    StateStopped
    StateFailed
)

// New states (add)
const (
    StatePausing  LifecycleState = 10 + iota
    StatePaused
    StateResuming
    StateKilling
    StateKilled
)

// New transitions (add to ValidTransitions)
{StatePausing, StatePaused}:    true,
{StatePaused, StateResuming}:   true,
{StateResuming, StateRunning}:  true,
{StateRunning, StateKilling}:   true,
{StateKilling, StateKilled}:    true,
```

### Integration Point 3: KillSequenceManager (core/tools/kill_sequence.go)

Extend 3-phase to 5-phase with resource cleanup:

```go
// Existing phases (keep)
const (
    KillPhaseSIGINT KillPhase = iota
    KillPhaseSIGTERM
    KillPhaseSIGKILL
)

// New phases (add)
const (
    KillPhaseForceCloseResources KillPhase = 10 + iota
    KillPhaseOrphanTracking
)

// Extend ExecuteKillSequence
func (m *KillSequenceManager) ExecuteKillSequence(proc *TrackedProcess) error {
    // Phase 1-3: Existing signal sequence
    if err := m.signalSequence(proc); err == nil {
        return nil
    }

    // Phase 4: Force-close resources
    proc.ForceCloseResources()
    if err := proc.waitTermination(1 * time.Second); err == nil {
        return nil
    }

    // Phase 5: Mark as orphan, continue degraded
    m.orphanTracker.Track(proc)
    return &OrphanedProcessError{PID: proc.pid}
}
```

### Integration Point 4: PermissionManager (core/security/permission_manager.go)

Always active - permission checks happen regardless of sandbox:

```go
// Used by TrackedToolExecutor.Execute()
perm := e.permissionMgr.CheckPermission(
    supervisor.agentID,
    security.ActionTypeCommand,
    tool.Command(),
)
// Blocks execution if not allowed - no sandbox needed
```

### Integration Point 5: Signal Handling (core/signal/agent_handler.go)

Connect user commands (Ctrl+C, etc.) to PipelineController:

```go
func (h *AgentHandler) handleSignal(sig os.Signal) {
    switch sig {
    case syscall.SIGINT:
        // First SIGINT: graceful stop
        if !h.interruptReceived {
            h.interruptReceived = true
            h.pipelineController.Stop(context.Background())
        } else {
            // Second SIGINT: force kill
            h.pipelineController.Kill(context.Background())
        }

    case syscall.SIGTERM:
        h.pipelineController.Kill(context.Background())

    case syscall.SIGTSTP:  // Ctrl+Z
        h.pipelineController.Pause(context.Background())
    }
}
```

### Integration Point 6: UI Command Handler

Connect terminal UI commands to PipelineController:

```go
// In UI command handler
func (ui *TerminalUI) handleCommand(cmd string) {
    switch cmd {
    case "stop", "q":
        go ui.pipelineController.Stop(ui.ctx)
        ui.showStatus("Stopping...")

    case "pause", "p":
        go ui.pipelineController.Pause(ui.ctx)
        ui.showStatus("Pausing...")

    case "resume", "r":
        go ui.pipelineController.Resume(ui.ctx)
        ui.showStatus("Resuming...")

    case "kill", "k":
        go ui.pipelineController.Kill(ui.ctx)
        ui.showStatus("Killing...")
    }
    // Commands execute in background - UI remains responsive < 16ms
}
```

### Integration Point 7: Memory Pressure System (MEMORY.md)

Connect goroutine budget to memory pressure levels:

```go
// In memory pressure monitor
func (m *MemoryMonitor) onPressureChange(level PressureLevel) {
    // Notify goroutine budget
    m.goroutineBudget.OnPressureChange(level)

    // Existing memory pressure handlers...
}
```

### Integration Point 8: Existing Tool Executor (core/tools/executor.go)

Wrap with TrackedToolExecutor:

```go
// Factory function
func NewToolExecutor(cfg Config, security *SecurityManager) *TrackedToolExecutor {
    return &TrackedToolExecutor{
        permissionMgr: security.PermissionManager(),  // Always active
        sandboxMgr:    security.SandboxManager(),     // May be nil/disabled
        auditLogger:   security.AuditLogger(),        // Always active
        maxTimeout:    cfg.MaxToolTimeout,
    }
}
```

### Summary: What's Always Active vs Optional

| Component | Always Active | Optional (Sandbox ON) |
|-----------|--------------|----------------------|
| **GoroutineScope** | ✓ | - |
| **AgentSupervisor** | ✓ | - |
| **PipelineController** | ✓ | - |
| **TrackedProcess** | ✓ (OS signals) | ✓ (sandbox kill) |
| **PermissionManager** | ✓ | - |
| **AuditLogger** | ✓ | - |
| **ResourceTracker** | ✓ | - |
| **GoroutineBudget** | ✓ | - |
| **Process Groups** | ✓ (`Setpgid`) | - |
| **VFS Layer** | - | ✓ |
| **Network Proxy** | - | ✓ |
| **Syscall Filtering** | - | ✓ |
