package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrPipelineAlreadyStarted = errors.New("pipeline already started")
	ErrPipelineNotRunning     = errors.New("pipeline not running")
)

// PipelinePhase represents a phase in pipeline execution.
type PipelinePhase string

const (
	PhaseInspector PipelinePhase = "inspector"
	PhaseTester    PipelinePhase = "tester"
	PhaseWorker    PipelinePhase = "worker"
	PhaseVerify    PipelinePhase = "verify"
)

// DefaultPipelinePhases defines the standard pipeline execution order.
var DefaultPipelinePhases = []PipelinePhase{
	PhaseInspector,
	PhaseTester,
	PhaseWorker,
	PhaseVerify,
}

// PhaseFunc is the function signature for a pipeline phase.
type PhaseFunc func(ctx context.Context) error

// PipelineRunnerConfig configures a pipeline runner.
type PipelineRunnerConfig struct {
	ID              string
	ShutdownTimeout time.Duration
	OnPanic         PanicHandler
}

// PipelineRunner manages a single pipeline goroutine lifecycle.
type PipelineRunner struct {
	config    PipelineRunnerConfig
	lifecycle *Lifecycle
	phases    map[PipelinePhase]PhaseFunc
	order     []PipelinePhase
	current   PipelinePhase
	cancel    context.CancelFunc
	done      chan struct{}
	runErr    error
	mu        sync.RWMutex
}

// NewPipelineRunner creates a new pipeline runner.
func NewPipelineRunner(config PipelineRunnerConfig) *PipelineRunner {
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = DefaultShutdownTimeout
	}
	return &PipelineRunner{
		config:    config,
		lifecycle: NewLifecycle(config.ID),
		phases:    make(map[PipelinePhase]PhaseFunc),
		order:     DefaultPipelinePhases,
		done:      make(chan struct{}),
	}
}

// ID returns the runner ID.
func (r *PipelineRunner) ID() string {
	return r.config.ID
}

// Lifecycle returns the lifecycle manager.
func (r *PipelineRunner) Lifecycle() *Lifecycle {
	return r.lifecycle
}

// SetPhaseOrder sets the order of phase execution.
func (r *PipelineRunner) SetPhaseOrder(order []PipelinePhase) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = order
}

// RegisterPhase registers a function for a specific phase.
func (r *PipelineRunner) RegisterPhase(phase PipelinePhase, fn PhaseFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phases[phase] = fn
}

// CurrentPhase returns the currently executing phase.
func (r *PipelineRunner) CurrentPhase() PipelinePhase {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// IsRunning returns true if the runner is in running state.
func (r *PipelineRunner) IsRunning() bool {
	return r.lifecycle.State() == StateRunning
}

// Error returns the error that caused the runner to fail.
func (r *PipelineRunner) Error() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runErr
}

// Start spawns the pipeline goroutine.
func (r *PipelineRunner) Start(ctx context.Context) error {
	if r.lifecycle.State() != StateCreated {
		return ErrPipelineAlreadyStarted
	}

	if err := r.lifecycle.TransitionTo(StateStarting); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	go r.run(runCtx)

	return r.lifecycle.TransitionTo(StateRunning)
}

// Stop gracefully stops the pipeline with timeout.
func (r *PipelineRunner) Stop() error {
	if r.isAlreadyStopped() {
		return nil
	}

	if err := r.validateStopState(); err != nil {
		return err
	}

	if err := r.lifecycle.TransitionTo(StateStopping); err != nil {
		return err
	}

	r.cancel()
	return r.waitForDone()
}

func (r *PipelineRunner) isAlreadyStopped() bool {
	state := r.lifecycle.State()
	return state == StateStopped || state == StateFailed
}

func (r *PipelineRunner) validateStopState() error {
	if r.lifecycle.State() != StateRunning {
		return ErrPipelineNotRunning
	}
	return nil
}

func (r *PipelineRunner) waitForDone() error {
	select {
	case <-r.done:
		return nil
	case <-time.After(r.config.ShutdownTimeout):
		_ = r.lifecycle.TransitionToFailed(ErrShutdownTimeout)
		return ErrShutdownTimeout
	}
}

func (r *PipelineRunner) run(ctx context.Context) {
	defer r.handleCompletion()
	defer r.recoverPanic()

	err := r.executePhases(ctx)
	r.setError(err)
}

func (r *PipelineRunner) executePhases(ctx context.Context) error {
	r.mu.RLock()
	order := r.order
	r.mu.RUnlock()

	for _, phase := range order {
		if err := r.executePhase(ctx, phase); err != nil {
			return err
		}
	}
	return nil
}

func (r *PipelineRunner) executePhase(ctx context.Context, phase PipelinePhase) error {
	r.mu.RLock()
	fn, ok := r.phases[phase]
	r.mu.RUnlock()

	if !ok {
		return nil // Skip unregistered phases
	}

	r.setCurrentPhase(phase)

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fn(ctx)
	}
}

func (r *PipelineRunner) setCurrentPhase(phase PipelinePhase) {
	r.mu.Lock()
	r.current = phase
	r.mu.Unlock()
}

func (r *PipelineRunner) recoverPanic() {
	if recovered := recover(); recovered != nil {
		err := fmt.Errorf("panic in pipeline %s phase %s: %v", r.config.ID, r.current, recovered)
		r.setError(err)
		if r.config.OnPanic != nil {
			r.config.OnPanic(r.config.ID, recovered)
		}
	}
}

func (r *PipelineRunner) handleCompletion() {
	close(r.done)
	r.finalizeState()
}

func (r *PipelineRunner) finalizeState() {
	if r.shouldTransitionToFailed() {
		_ = r.lifecycle.TransitionToFailed(r.Error())
		return
	}

	r.transitionToStopped()
}

func (r *PipelineRunner) shouldTransitionToFailed() bool {
	return r.Error() != nil && r.lifecycle.State() != StateFailed
}

func (r *PipelineRunner) transitionToStopped() {
	state := r.lifecycle.State()
	if state == StateRunning {
		_ = r.lifecycle.TransitionTo(StateStopping)
	}
	if r.lifecycle.State() == StateStopping {
		_ = r.lifecycle.TransitionTo(StateStopped)
	}
}

func (r *PipelineRunner) setError(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	r.mu.Lock()
	r.runErr = err
	r.mu.Unlock()
}

// WaitForState blocks until the runner reaches the specified state.
func (r *PipelineRunner) WaitForState(state LifecycleState, timeout time.Duration) error {
	return r.lifecycle.WaitForState(state, timeout)
}
