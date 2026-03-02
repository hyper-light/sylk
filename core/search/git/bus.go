package git

import (
	"sync"
	"sync/atomic"
	"time"
)

// GitEventHandler is a callback for bus events.
// Handlers MUST be non-blocking; long-running work should be dispatched
// asynchronously by the handler itself.
type GitEventHandler func(event *GitEvent)

// subscription pairs a handler with an optional op filter.
type subscription struct {
	ops     map[GitOp]struct{} // nil = wildcard (all ops)
	handler GitEventHandler
}

// PreMutationGate is called before mutating git operations. If it returns
// a non-nil error, the operation is blocked. Used by the Guardian agent
// to gate protected-branch mutations behind user approval.
type PreMutationGate func(op GitOp, params any) error

// GitBus wraps a GitClient, routing every operation through a centralised
// dispatch that emits pre/post events to subscribers.
type GitBus struct {
	client *GitClient

	mu     sync.RWMutex
	subs   map[uint64]subscription
	nextID atomic.Uint64

	gateMu sync.RWMutex
	gate   PreMutationGate
}

// NewGitBus creates a bus that wraps the given client.
func NewGitBus(client *GitClient) *GitBus {
	return &GitBus{
		client: client,
		subs:   make(map[uint64]subscription),
	}
}

// Client returns the underlying GitClient for direct access when the bus
// abstraction is not needed (e.g. StatusWatcher construction).
func (b *GitBus) Client() *GitClient { return b.client }

// SetPreMutationGate installs a gate that is called before mutating operations.
// Pass nil to remove the gate.
func (b *GitBus) SetPreMutationGate(gate PreMutationGate) {
	b.gateMu.Lock()
	b.gate = gate
	b.gateMu.Unlock()
}

// checkGate invokes the pre-mutation gate for mutating operations.
// Returns nil if no gate is set or the operation is non-mutating.
func (b *GitBus) checkGate(op GitOp, params any) error {
	if !IsMutating(op) {
		return nil
	}
	b.gateMu.RLock()
	gate := b.gate
	b.gateMu.RUnlock()
	if gate == nil {
		return nil
	}
	return gate(op, params)
}

// Subscribe registers a handler for specific ops.
// Pass nil for ops to subscribe to all operations (wildcard).
// Returns an unsubscribe function that is safe to call multiple times.
func (b *GitBus) Subscribe(ops []GitOp, handler GitEventHandler) func() {
	id := b.nextID.Add(1)

	var opSet map[GitOp]struct{}
	if len(ops) > 0 {
		opSet = make(map[GitOp]struct{}, len(ops))
		for _, op := range ops {
			opSet[op] = struct{}{}
		}
	}

	b.mu.Lock()
	b.subs[id] = subscription{ops: opSet, handler: handler}
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
		})
	}
}

// emit snapshots matching handlers under RLock, then dispatches without
// the lock held.  Matches the snapshot-and-dispatch pattern used by
// core/session/manager.go emitEvent.
func (b *GitBus) emit(event *GitEvent) {
	b.mu.RLock()
	n := len(b.subs)
	if n == 0 {
		b.mu.RUnlock()
		return
	}
	handlers := make([]GitEventHandler, 0, n)
	for _, sub := range b.subs {
		if sub.ops != nil {
			if _, ok := sub.ops[event.Op]; !ok {
				continue
			}
		}
		handlers = append(handlers, sub.handler)
	}
	b.mu.RUnlock()

	for _, h := range handlers {
		h(event)
	}
}

// hasSubscribers returns true when at least one subscription exists.
// Used by dispatch functions to skip event construction on the hot path.
func (b *GitBus) hasSubscribers() bool {
	b.mu.RLock()
	n := len(b.subs)
	b.mu.RUnlock()
	return n > 0
}

// ---------------------------------------------------------------------------
// Generic dispatch functions
// ---------------------------------------------------------------------------

// dispatch handles (R, error) returns — covers the majority of methods.
func dispatch[R any](b *GitBus, op GitOp, params any, fn func() (R, error)) (R, error) {
	if err := b.checkGate(op, params); err != nil {
		var zero R
		return zero, err
	}
	if !b.hasSubscribers() {
		return fn()
	}
	start := time.Now()
	b.emit(&GitEvent{Op: op, Phase: PhasePre, Params: params, Timestamp: start})
	result, err := fn()
	b.emit(&GitEvent{
		Op: op, Phase: PhasePost, Params: params,
		Result: result, Err: err,
		Timestamp: time.Now(), Duration: time.Since(start),
	})
	return result, err
}

// dispatchVoid handles error-only returns — covers mutating void methods.
func dispatchVoid(b *GitBus, op GitOp, params any, fn func() error) error {
	if err := b.checkGate(op, params); err != nil {
		return err
	}
	if !b.hasSubscribers() {
		return fn()
	}
	start := time.Now()
	b.emit(&GitEvent{Op: op, Phase: PhasePre, Params: params, Timestamp: start})
	err := fn()
	b.emit(&GitEvent{
		Op: op, Phase: PhasePost, Params: params,
		Err: err,
		Timestamp: time.Now(), Duration: time.Since(start),
	})
	return err
}

// dispatchPure handles R-only returns (no error) — covers accessor methods.
func dispatchPure[R any](b *GitBus, op GitOp, params any, fn func() R) R {
	if !b.hasSubscribers() {
		return fn()
	}
	start := time.Now()
	b.emit(&GitEvent{Op: op, Phase: PhasePre, Params: params, Timestamp: start})
	result := fn()
	b.emit(&GitEvent{
		Op: op, Phase: PhasePost, Params: params,
		Result: result,
		Timestamp: time.Now(), Duration: time.Since(start),
	})
	return result
}

// dispatchTriple handles (A, B, error) returns — covers paginated methods.
func dispatchTriple[A, B any](b *GitBus, op GitOp, params any, fn func() (A, B, error)) (A, B, error) {
	if !b.hasSubscribers() {
		return fn()
	}
	start := time.Now()
	b.emit(&GitEvent{Op: op, Phase: PhasePre, Params: params, Timestamp: start})
	a, extra, err := fn()
	b.emit(&GitEvent{
		Op: op, Phase: PhasePost, Params: params,
		Result: a, Err: err,
		Timestamp: time.Now(), Duration: time.Since(start),
	})
	return a, extra, err
}
