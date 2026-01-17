package concurrency

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrWaitTimeout            = errors.New("wait for state timed out")
	ErrAlreadyInState         = errors.New("already in requested state")
)

// LifecycleState represents the state of a runner.
type LifecycleState int32

const (
	StateCreated LifecycleState = iota
	StateStarting
	StateRunning
	StateStopping
	StateStopped
	StateFailed
)

// stateNames maps lifecycle states to their string representation.
var stateNames = map[LifecycleState]string{
	StateCreated:  "created",
	StateStarting: "starting",
	StateRunning:  "running",
	StateStopping: "stopping",
	StateStopped:  "stopped",
	StateFailed:   "failed",
}

// String returns the string representation of a lifecycle state.
func (s LifecycleState) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return "unknown"
}

// validTransitions defines allowed state transitions.
var validTransitions = map[LifecycleState][]LifecycleState{
	StateCreated:  {StateStarting, StateFailed},
	StateStarting: {StateRunning, StateFailed, StateStopping},
	StateRunning:  {StateStopping, StateFailed},
	StateStopping: {StateStopped, StateFailed},
	StateStopped:  {},
	StateFailed:   {},
}

// StateChangeEvent represents a state transition event.
type StateChangeEvent struct {
	RunnerID  string
	FromState LifecycleState
	ToState   LifecycleState
	Timestamp time.Time
	Error     error
}

// StateChangeCallback is called when state changes.
type StateChangeCallback func(event StateChangeEvent)

// Lifecycle manages state transitions for runners.
type Lifecycle struct {
	id        string
	state     atomic.Int32
	callbacks []StateChangeCallback
	waiters   map[LifecycleState][]chan struct{}
	mu        sync.Mutex
}

// NewLifecycle creates a new lifecycle manager.
func NewLifecycle(id string) *Lifecycle {
	lc := &Lifecycle{
		id:      id,
		waiters: make(map[LifecycleState][]chan struct{}),
	}
	lc.state.Store(int32(StateCreated))
	return lc
}

// State returns the current state.
func (lc *Lifecycle) State() LifecycleState {
	return LifecycleState(lc.state.Load())
}

// ID returns the lifecycle ID.
func (lc *Lifecycle) ID() string {
	return lc.id
}

// OnStateChange registers a callback for state changes.
func (lc *Lifecycle) OnStateChange(cb StateChangeCallback) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.callbacks = append(lc.callbacks, cb)
}

// TransitionTo attempts to transition to a new state.
func (lc *Lifecycle) TransitionTo(newState LifecycleState) error {
	return lc.transitionWithError(newState, nil)
}

// TransitionToFailed transitions to failed state with an error.
func (lc *Lifecycle) TransitionToFailed(err error) error {
	return lc.transitionWithError(StateFailed, err)
}

func (lc *Lifecycle) transitionWithError(newState LifecycleState, transitionErr error) error {
	currentState := lc.State()
	if !lc.isValidTransition(currentState, newState) {
		return ErrInvalidStateTransition
	}

	if !lc.state.CompareAndSwap(int32(currentState), int32(newState)) {
		return ErrInvalidStateTransition
	}

	lc.notifyStateChange(currentState, newState, transitionErr)
	lc.notifyWaiters(newState)
	return nil
}

func (lc *Lifecycle) isValidTransition(from, to LifecycleState) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	return slices.Contains(allowed, to)
}

func (lc *Lifecycle) notifyStateChange(from, to LifecycleState, err error) {
	lc.mu.Lock()
	callbacks := make([]StateChangeCallback, len(lc.callbacks))
	copy(callbacks, lc.callbacks)
	lc.mu.Unlock()

	event := StateChangeEvent{
		RunnerID:  lc.id,
		FromState: from,
		ToState:   to,
		Timestamp: time.Now(),
		Error:     err,
	}

	for _, cb := range callbacks {
		cb(event)
	}
}

func (lc *Lifecycle) notifyWaiters(state LifecycleState) {
	lc.mu.Lock()
	waiters := lc.waiters[state]
	delete(lc.waiters, state)
	lc.mu.Unlock()

	for _, ch := range waiters {
		close(ch)
	}
}

func (lc *Lifecycle) WaitForState(target LifecycleState, timeout time.Duration) error {
	if lc.State() == target {
		return nil
	}

	ch := make(chan struct{})
	lc.mu.Lock()
	lc.waiters[target] = append(lc.waiters[target], ch)
	lc.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
		return nil
	case <-timer.C:
		lc.removeWaiter(target, ch)
		return ErrWaitTimeout
	}
}

func (lc *Lifecycle) removeWaiter(target LifecycleState, ch chan struct{}) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	waiters := lc.waiters[target]
	for i, w := range waiters {
		if w == ch {
			lc.waiters[target] = append(waiters[:i], waiters[i+1:]...)
			return
		}
	}
}

// IsTerminal returns true if the lifecycle is in a terminal state.
func (lc *Lifecycle) IsTerminal() bool {
	state := lc.State()
	return state == StateStopped || state == StateFailed
}

// SignalBusPublisher is the interface for publishing to a signal bus.
type SignalBusPublisher interface {
	Broadcast(msg any) error
}

// ConnectToSignalBus registers a callback that publishes state changes to the signal bus.
func (lc *Lifecycle) ConnectToSignalBus(publisher SignalBusPublisher, createMsg func(event StateChangeEvent) any) {
	lc.OnStateChange(func(event StateChangeEvent) {
		msg := createMsg(event)
		_ = publisher.Broadcast(msg)
	})
}
