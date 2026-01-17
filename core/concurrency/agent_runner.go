package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

var (
	ErrRunnerAlreadyStarted = errors.New("runner already started")
	ErrRunnerNotRunning     = errors.New("runner not running")
	ErrShutdownTimeout      = errors.New("shutdown timed out")
)

type AgentType string

const (
	AgentGuide        AgentType = "guide"
	AgentArchitect    AgentType = "architect"
	AgentOrchestrator AgentType = "orchestrator"
	AgentLibrarian    AgentType = "librarian"
	AgentArchivalist  AgentType = "archivalist"
	AgentAcademic     AgentType = "academic"
)

const DefaultShutdownTimeout = 30 * time.Second

type AgentFunc func(ctx context.Context) error

type PanicHandler func(runnerID string, recovered any)

type AgentRunnerConfig struct {
	ID              string
	AgentType       AgentType
	ShutdownTimeout time.Duration
	OnPanic         PanicHandler
}

type AgentRunner struct {
	config    AgentRunnerConfig
	lifecycle *Lifecycle
	agentFunc AgentFunc
	cancel    context.CancelFunc
	done      chan struct{}
	runErr    atomic.Pointer[error]
}

func NewAgentRunner(config AgentRunnerConfig, fn AgentFunc) *AgentRunner {
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = DefaultShutdownTimeout
	}
	return &AgentRunner{
		config:    config,
		lifecycle: NewLifecycle(config.ID),
		agentFunc: fn,
		done:      make(chan struct{}),
	}
}

func (r *AgentRunner) ID() string {
	return r.config.ID
}

func (r *AgentRunner) AgentType() AgentType {
	return r.config.AgentType
}

func (r *AgentRunner) Lifecycle() *Lifecycle {
	return r.lifecycle
}

func (r *AgentRunner) IsRunning() bool {
	return r.lifecycle.State() == StateRunning
}

func (r *AgentRunner) Error() error {
	if ptr := r.runErr.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

func (r *AgentRunner) Start(ctx context.Context) error {
	if r.lifecycle.State() != StateCreated {
		return ErrRunnerAlreadyStarted
	}

	if err := r.lifecycle.TransitionTo(StateStarting); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	go r.run(runCtx)

	return r.lifecycle.TransitionTo(StateRunning)
}

func (r *AgentRunner) Stop() error {
	if err := r.initiateStop(); err != nil {
		return err
	}
	r.cancel()
	return r.awaitShutdown()
}

func (r *AgentRunner) initiateStop() error {
	state := r.lifecycle.State()
	if state == StateStopped || state == StateFailed {
		return nil
	}
	if state != StateRunning {
		return ErrRunnerNotRunning
	}
	return r.lifecycle.TransitionTo(StateStopping)
}

func (r *AgentRunner) awaitShutdown() error {
	timer := time.NewTimer(r.config.ShutdownTimeout)
	defer timer.Stop()

	select {
	case <-r.done:
		return nil
	case <-timer.C:
		_ = r.lifecycle.TransitionToFailed(ErrShutdownTimeout)
		return ErrShutdownTimeout
	}
}

func (r *AgentRunner) run(ctx context.Context) {
	var runErr error

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("panic in agent %s: %v", r.config.ID, recovered)
				if r.config.OnPanic != nil {
					r.config.OnPanic(r.config.ID, recovered)
				}
			}
		}()
		runErr = r.agentFunc(ctx)
	}()

	r.completeRun(runErr)
}

func (r *AgentRunner) completeRun(err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		r.runErr.Store(&err)
		_ = r.lifecycle.TransitionToFailed(err)
	} else if r.lifecycle.State() == StateStopping {
		_ = r.lifecycle.TransitionTo(StateStopped)
	}

	close(r.done)
}

func (r *AgentRunner) WaitForState(state LifecycleState, timeout time.Duration) error {
	return r.lifecycle.WaitForState(state, timeout)
}
