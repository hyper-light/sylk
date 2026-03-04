package container

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	csecurity "github.com/adalundhe/sylk/core/container/security"
)

var (
	ErrContainerNotRunning = errors.New("container is not running")
	ErrContainerStopped    = errors.New("container has stopped")
)

// ContainerAgent is the interface the container runtime uses to interact
// with the underlying agent. Deliberately minimal to decouple from handoff.
type ContainerAgent interface {
	AgentID() string
	AgentType() string
	Terminate(ctx context.Context) error
}

// ContainerMetrics captures runtime metrics for a container.
type ContainerMetrics struct {
	StartTime      time.Time
	StopTime       time.Time
	RestartCount   atomic.Int64
	ProbeSuccesses atomic.Int64
	ProbeFailures  atomic.Int64
}

// Uptime returns the duration the container has been running.
// Returns zero if not started or already stopped.
func (m *ContainerMetrics) Uptime() time.Duration {
	if m.StartTime.IsZero() {
		return 0
	}
	if !m.StopTime.IsZero() {
		return m.StopTime.Sub(m.StartTime)
	}
	return time.Since(m.StartTime)
}

// Container is a running agent wrapped in an isolated execution environment.
// It composes existing concurrency primitives (Lifecycle, GoroutineScope,
// AgentSupervisor) with the declarative ContainerSpec.
type Container struct {
	id          ContainerID
	spec        ContainerSpec
	lifecycle   *concurrency.Lifecycle
	scope       *concurrency.GoroutineScope
	secCtx      *csecurity.SecurityContext
	probeRunner *ProbeRunner
	hookRunner  *HookRunner
	restartCtl  *RestartController
	agent       ContainerAgent
	podID       PodID // owning pod ID (empty if standalone)
	metrics     *ContainerMetrics

	mu      sync.RWMutex
	exitErr error // set when container stops
}

// ContainerConfig provides construction parameters for a Container.
type ContainerConfig struct {
	ID        ContainerID
	Spec      ContainerSpec
	Scope     *concurrency.GoroutineScope
	SecCtx    *csecurity.SecurityContext
	Agent     ContainerAgent
	PodID     PodID
}

// NewContainer creates a container in the Created state. The container
// is not started — call Start() to begin the lifecycle.
func NewContainer(cfg ContainerConfig) *Container {
	lc := concurrency.NewLifecycle(string(cfg.ID))

	restartCfg := DefaultRestartControllerConfig(cfg.Spec.RestartPolicy)
	rc := NewRestartController(restartCfg)

	probeRunner := NewProbeRunner(cfg.Scope, nil)
	hookRunner := NewHookRunner(cfg.Scope, cfg.Spec.Hooks)

	c := &Container{
		id:          cfg.ID,
		spec:        cfg.Spec,
		lifecycle:   lc,
		scope:       cfg.Scope,
		secCtx:      cfg.SecCtx,
		probeRunner: probeRunner,
		hookRunner:  hookRunner,
		restartCtl:  rc,
		agent:       cfg.Agent,
		podID:       cfg.PodID,
		metrics:     &ContainerMetrics{},
	}

	probeRunner.onResult = c.recordProbeResult
	hookRunner.SetContainer(c)
	return c
}

func (c *Container) recordProbeResult(_ ProbeType, result ProbeResult) {
	if result.Success {
		c.metrics.ProbeSuccesses.Add(1)
	} else {
		c.metrics.ProbeFailures.Add(1)
	}
}

// Start transitions the container from Created → Starting → Running.
// Runs pre-start hook, starts probes, and fires post-start hook.
func (c *Container) Start(ctx context.Context) error {
	if err := c.lifecycle.TransitionTo(concurrency.StateStarting); err != nil {
		return err
	}

	if err := c.hookRunner.RunPreStart(ctx); err != nil {
		_ = c.lifecycle.TransitionToFailed(err)
		return err
	}

	if err := c.lifecycle.TransitionTo(concurrency.StateRunning); err != nil {
		return err
	}

	c.metrics.StartTime = time.Now()

	if err := c.probeRunner.Start(c.spec.Probes); err != nil {
		_ = c.lifecycle.TransitionToFailed(err)
		return err
	}

	_ = c.hookRunner.RunPostStart()
	return nil
}

// Stop transitions the container through Stopping → Stopped. Runs pre-stop
// hook, terminates the agent, shuts down the scope, then runs post-stop hook.
func (c *Container) Stop(ctx context.Context) error {
	if err := c.lifecycle.TransitionTo(concurrency.StateStopping); err != nil {
		return err
	}

	_ = c.hookRunner.RunPreStop(ctx)

	if c.agent != nil {
		_ = c.agent.Terminate(ctx)
	}

	_ = c.scope.Shutdown(c.spec.GracefulStop, c.spec.GracefulStop*2)

	c.metrics.StopTime = time.Now()
	_ = c.hookRunner.RunPostStop(ctx)

	return c.lifecycle.TransitionTo(concurrency.StateStopped)
}

// Fail transitions the container to Failed state with the given error.
func (c *Container) Fail(err error) error {
	c.mu.Lock()
	c.exitErr = err
	c.mu.Unlock()
	c.metrics.StopTime = time.Now()
	return c.lifecycle.TransitionToFailed(err)
}

// ID returns the container's unique identifier.
func (c *Container) ID() ContainerID {
	return c.id
}

// Spec returns a copy of the container's specification.
func (c *Container) Spec() ContainerSpec {
	return c.spec
}

// State returns the current lifecycle state.
func (c *Container) State() concurrency.LifecycleState {
	return c.lifecycle.State()
}

// Lifecycle returns the underlying lifecycle for external state observation.
func (c *Container) Lifecycle() *concurrency.Lifecycle {
	return c.lifecycle
}

// IsRunning reports whether the container is in the Running state.
func (c *Container) IsRunning() bool {
	return c.lifecycle.State() == concurrency.StateRunning
}

// IsTerminal reports whether the container is in a terminal state.
func (c *Container) IsTerminal() bool {
	return c.lifecycle.IsTerminal()
}

// IsReady reports whether the container's readiness probe is healthy.
func (c *Container) IsReady() bool {
	return c.IsRunning() && c.probeRunner.IsReady()
}

// IsAlive reports whether the container's liveness probe is healthy.
func (c *Container) IsAlive() bool {
	return c.IsRunning() && c.probeRunner.IsAlive()
}

// Agent returns the underlying agent.
func (c *Container) Agent() ContainerAgent {
	return c.agent
}

// SecurityContext returns the container's security context.
func (c *Container) SecurityContext() *csecurity.SecurityContext {
	return c.secCtx
}

// Metrics returns the container's runtime metrics.
func (c *Container) Metrics() *ContainerMetrics {
	return c.metrics
}

// RestartController returns the container's restart controller.
func (c *Container) RestartController() *RestartController {
	return c.restartCtl
}

// ProbeRunner returns the container's probe runner.
func (c *Container) ProbeRunner() *ProbeRunner {
	return c.probeRunner
}

// PodID returns the owning pod's ID, or empty for standalone containers.
func (c *Container) PodID() PodID {
	return c.podID
}

// SetPodID sets the owning pod's ID. Used by ManagedPod during container adoption.
func (c *Container) SetPodID(id PodID) {
	c.podID = id
}

// Scope returns the container's goroutine scope.
func (c *Container) Scope() *concurrency.GoroutineScope {
	return c.scope
}

// ExitError returns the error that caused the container to exit, if any.
func (c *Container) ExitError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.exitErr
}

// Pause transitions the container from Running -> Pausing -> Paused.
// Stops probes but keeps the GoroutineScope alive. The agent remains
// in memory — this is for Warm tier (minimal overhead, fast resume).
func (c *Container) Pause() error {
	if err := c.lifecycle.TransitionTo(concurrency.StatePausing); err != nil {
		return err
	}
	c.probeRunner.Stop()
	return c.lifecycle.TransitionTo(concurrency.StatePaused)
}

// Resume transitions the container from Paused -> Resuming -> Running.
// Restarts probes. The GoroutineScope must still be alive (not shut down).
func (c *Container) Resume() error {
	if err := c.lifecycle.TransitionTo(concurrency.StateResuming); err != nil {
		return err
	}
	c.probeRunner.Reset()
	if err := c.probeRunner.Start(c.spec.Probes); err != nil {
		_ = c.lifecycle.TransitionToFailed(err)
		return err
	}
	return c.lifecycle.TransitionTo(concurrency.StateRunning)
}

// IsPaused reports whether the container is in the Paused state.
func (c *Container) IsPaused() bool {
	return c.lifecycle.State() == concurrency.StatePaused
}
