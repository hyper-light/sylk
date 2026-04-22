package versioning

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// DispatchGate is the pipeline-dispatch backpressure primitive. It
// caps the number of non-terminal merges in the session's commit
// queue; pipeline dispatches block at Acquire until capacity is
// available. Per docs/PARALLEL_GLOBAL_VFS.md §5.1.
//
// Why capacity = non-terminal commit-queue depth (and not e.g. the
// number of in-flight audits): the correctness invariant is that the
// system must be able to resolve every enqueued merge. A merge that
// has landed but not yet committed / superseded / abandoned is
// consuming a ReplicaVFS slot and a retention ref; letting the
// backlog grow unbounded allows the ReplicaVFS chain to grow without
// bound (§5). Capping non-terminal depth caps both the chain depth
// and the architect's in-flight remediation surface area.
//
// Semantics:
//   - MaxInFlight = 0 means no cap (bypass).
//   - Acquire(ctx) blocks until non-terminal depth < MaxInFlight, the
//     ctx is cancelled, or Stop is called. Returns nil on success,
//     ctx.Err() on cancel, ErrDispatchGateStopped on Stop.
//   - The gate is signal-only: Acquire does NOT reserve a slot. The
//     reservation happens when the caller proceeds to BeginPipeline
//     and the subsequent MergePipelineIntoGreen enqueues. A tight race
//     (Acquire returns below cap, two callers both proceed, one
//     merge takes the last slot) is acceptable — the next caller's
//     Acquire loop sees the cap exceeded and re-waits. Over-commit is
//     bounded by the number of concurrent callers.
//   - The gate subscribes to CommitQueue events internally; every
//     non-terminal → terminal transition wakes waiters.
//
// Concurrency: Acquire is wait-free in the fast path (direct depth
// check). The slow path uses a channel-close-reopen broadcast so
// arbitrarily many concurrent waiters wake in parallel without
// per-waiter goroutines (CLAUDE.md "no untracked goroutines"). The
// single subscriber-loop goroutine runs under a GoroutineScope
// sourced from the Start ctx; Stop waits for it to exit before
// returning.
type DispatchGate struct {
	session     *SessionVFS
	maxInFlight int
	scope       *concurrency.GoroutineScope

	mu        sync.Mutex
	broadcast chan struct{}
	stopped   atomic.Bool
	stopCh    chan struct{}
	watchDone chan struct{}
	unsub     func()
}

// ErrDispatchGateStopped is returned from Acquire when the gate has
// been Stop()'d.
var ErrDispatchGateStopped = fmt.Errorf("dispatch gate: stopped")

// DispatchGateConfig configures the gate.
type DispatchGateConfig struct {
	// Session is the SessionVFS whose commit queue drives the gate.
	// Required.
	Session *SessionVFS
	// MaxInFlight is the non-terminal commit-queue depth ceiling.
	// Zero disables backpressure (gate is a pass-through).
	MaxInFlight int
	// Scope, when non-nil, is used to run the gate's subscriber
	// goroutine. When nil, Start reads the scope from the supplied
	// ctx; an unscoped ctx with MaxInFlight > 0 is rejected by Start
	// so we never silently spawn an untracked goroutine.
	Scope *concurrency.GoroutineScope
}

// NewDispatchGate constructs a DispatchGate. Call Start to activate.
func NewDispatchGate(cfg DispatchGateConfig) *DispatchGate {
	return &DispatchGate{
		session:     cfg.Session,
		maxInFlight: cfg.MaxInFlight,
		scope:       cfg.Scope,
		broadcast:   make(chan struct{}),
		stopCh:      make(chan struct{}),
	}
}

// Start attaches the gate to the session's commit-queue event stream.
// Idempotent. Returns an error when MaxInFlight > 0 and no
// GoroutineScope is available (config.Scope nil AND no scope on ctx)
// — the gate needs a tracked scope to run its subscriber loop.
func (g *DispatchGate) Start(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if g.session == nil {
		return fmt.Errorf("dispatch gate: nil session")
	}
	if g.maxInFlight <= 0 {
		// Pass-through configuration — no subscriber needed. Start
		// is idempotent and always returns nil for uncapped gates.
		return nil
	}
	queue := g.session.CommitQueue()
	if queue == nil {
		// No commit queue (strict-RAM test config) — gate degrades
		// to pass-through. Caller's Acquire already short-circuits.
		return nil
	}

	g.mu.Lock()
	if g.watchDone != nil {
		g.mu.Unlock()
		return nil
	}
	scope := g.scope
	if scope == nil {
		scope = concurrency.ScopeFromContext(ctx)
	}
	if scope == nil {
		g.mu.Unlock()
		return fmt.Errorf("dispatch gate: Start requires a GoroutineScope (in config or on ctx) — untracked goroutines are forbidden")
	}
	ch, unsub := queue.Subscribe()
	done := make(chan struct{})
	g.watchDone = done
	g.unsub = unsub
	g.mu.Unlock()

	return scope.Go("dispatch-gate:watch", 0, func(ctx context.Context) error {
		defer close(done)
		g.runWatch(ctx, ch)
		return nil
	})
}

// runWatch drains the commit-queue event channel, broadcasting on
// every event so Acquire waiters re-check capacity. Exits when the
// subscription closes, ctx cancels, or Stop closes stopCh.
func (g *DispatchGate) runWatch(ctx context.Context, ch <-chan CommitQueueEvent) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
			g.signal()
		case <-ctx.Done():
			return
		case <-g.stopCh:
			return
		}
	}
}

// signal wakes every Acquire waiting on the current broadcast
// channel by closing it and installing a fresh one.
func (g *DispatchGate) signal() {
	g.mu.Lock()
	old := g.broadcast
	g.broadcast = make(chan struct{})
	g.mu.Unlock()
	close(old)
}

// Stop unsubscribes from the commit queue, wakes all waiters, and
// waits for the subscriber goroutine to exit. Idempotent.
func (g *DispatchGate) Stop() {
	if g == nil {
		return
	}
	if !g.stopped.CompareAndSwap(false, true) {
		return
	}
	g.mu.Lock()
	unsub := g.unsub
	done := g.watchDone
	g.unsub = nil
	g.watchDone = nil
	g.mu.Unlock()

	close(g.stopCh)
	if unsub != nil {
		unsub()
	}
	// Wake every Acquire waiter so they observe stopped=true.
	g.signal()
	// Wait for the subscriber goroutine to drain. Bounded wait:
	// scope.Go has a maxLifetime ceiling and the watcher exits on
	// close(stopCh) promptly, but we cap the wait so a misbehaving
	// subscriber never blocks Close forever.
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
}

// Acquire blocks until the commit queue has room for another
// non-terminal entry (i.e. depth < MaxInFlight), ctx cancels, or the
// gate is stopped.
func (g *DispatchGate) Acquire(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if g.maxInFlight <= 0 {
		return ctx.Err()
	}
	if g.session == nil || g.session.CommitQueue() == nil {
		return ctx.Err()
	}
	for {
		if g.stopped.Load() {
			return ErrDispatchGateStopped
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if g.session.CommitQueueDepth() < g.maxInFlight {
			return nil
		}
		// Snapshot the broadcast channel under mu, then wait on it
		// outside the lock. The watcher's signal() replaces the
		// channel and closes the old one, so our wait unblocks even
		// if signal fires between our snapshot and the select.
		g.mu.Lock()
		broadcast := g.broadcast
		g.mu.Unlock()
		select {
		case <-broadcast:
			// Re-check the loop condition.
		case <-ctx.Done():
			return ctx.Err()
		case <-g.stopCh:
			return ErrDispatchGateStopped
		}
	}
}

// Depth reports the current non-terminal commit-queue depth. Used by
// observability and tests to assert on gate state without coupling to
// internal counters.
func (g *DispatchGate) Depth() int {
	if g == nil || g.session == nil {
		return 0
	}
	return g.session.CommitQueueDepth()
}

// MaxInFlight returns the configured cap. Zero means uncapped.
func (g *DispatchGate) MaxInFlight() int {
	if g == nil {
		return 0
	}
	return g.maxInFlight
}
