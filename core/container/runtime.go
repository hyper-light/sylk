package container

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	csecurity "github.com/adalundhe/sylk/core/container/security"
	"github.com/google/uuid"
)

var (
	runtimeDebugLog     *slog.Logger
	runtimeDebugLogOnce sync.Once
)

func runtimeFileLog() *slog.Logger {
	runtimeDebugLogOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		_ = os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "ui_events.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			runtimeDebugLog = slog.Default()
			return
		}
		runtimeDebugLog = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	return runtimeDebugLog
}

var (
	ErrRuntimeClosed = errors.New("container runtime is closed")
)

// ContainerStatus is a snapshot of a container's current state.
type ContainerStatus struct {
	ID        ContainerID
	State     concurrency.LifecycleState
	Ready     bool
	Alive     bool
	Restarts  int64
	StartTime time.Time
	StopTime  time.Time
}

// AgentCreator creates an agent of the given type. Injected by the caller
// to decouple from the handoff package.
type AgentCreator func(ctx context.Context, agentType string) (ContainerAgent, error)

// ContainerRuntime is the container runtime interface (CRI equivalent).
type ContainerRuntime interface {
	CreateContainer(ctx context.Context, spec ContainerSpec) (*Container, error)
	StartContainer(ctx context.Context, c *Container) error
	StopContainer(ctx context.Context, c *Container) error
	PauseContainer(ctx context.Context, c *Container) error
	ResumeContainer(ctx context.Context, c *Container) error
	RemoveContainer(ctx context.Context, c *Container) error
	ContainerStatus(c *Container) *ContainerStatus

	// Batch methods for pod-level activation.
	CreateContainersForPod(ctx context.Context, podID PodID, specs []ContainerSpec) ([]*Container, error)
	StartContainers(ctx context.Context, containers []*Container) error
}

// ProbeFactory builds probe specs for a container after its agent has been
// created. Called by CreateContainer between agent creation and NewContainer.
// This resolves the ordering problem where probes need the live agent
// reference but specs are mutated before agent creation.
type ProbeFactory func(agent ContainerAgent, spec *ContainerSpec) []ProbeSpec

// DefaultRuntimeConfig provides configuration for the default runtime.
type DefaultRuntimeConfig struct {
	Budget          *concurrency.GoroutineBudget
	Registry        *ContainerRegistry
	Quota           *ResourceQuota
	Admission       *AdmissionController
	CreateAgent     AgentCreator
	ProbeFactory    ProbeFactory
	ParentCtx       context.Context
}

// DefaultRuntime is the container runtime that wires existing primitives.
type DefaultRuntime struct {
	budget       *concurrency.GoroutineBudget
	registry     *ContainerRegistry
	quota        *ResourceQuota
	admission    *AdmissionController
	createAgent  AgentCreator
	probeFactory ProbeFactory
	parentCtx    context.Context
	closed       atomic.Bool
}

// NewDefaultRuntime creates the default container runtime.
func NewDefaultRuntime(cfg DefaultRuntimeConfig) *DefaultRuntime {
	ctx := cfg.ParentCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return &DefaultRuntime{
		budget:       cfg.Budget,
		registry:     cfg.Registry,
		quota:        cfg.Quota,
		admission:    cfg.Admission,
		createAgent:  cfg.CreateAgent,
		probeFactory: cfg.ProbeFactory,
		parentCtx:    ctx,
	}
}

// CreateContainer creates a container from a spec. Runs admission control,
// quota checks, creates the agent, GoroutineScope, and SecurityContext.
func (rt *DefaultRuntime) CreateContainer(ctx context.Context, spec ContainerSpec) (*Container, error) {
	if rt.closed.Load() {
		return nil, ErrRuntimeClosed
	}

	if err := rt.admitSpec(&spec); err != nil {
		return nil, fmt.Errorf("admission: %w", err)
	}

	if err := rt.checkQuota(&spec); err != nil {
		return nil, fmt.Errorf("quota: %w", err)
	}

	agentStart := time.Now()
	runtimeFileLog().Info("DEBUG: runtime_create_agent_start", "agent_type", spec.AgentType)
	agent, err := rt.createAgentForSpec(ctx, &spec)
	runtimeFileLog().Info("DEBUG: runtime_create_agent_done",
		"agent_type", spec.AgentType,
		"elapsed_ms", time.Since(agentStart).Milliseconds(),
		"error", err)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	// Wire probes after agent creation — the ProbeFactory needs the live
	// agent to construct liveness/readiness handlers.
	if rt.probeFactory != nil && agent != nil {
		spec.Probes = rt.probeFactory(agent, &spec)
	}

	id := ContainerID(uuid.New().String())
	scope := rt.createScope(id, spec.AgentType)
	secCtx := rt.createSecurityContext(id, agent, &spec)

	c := NewContainer(ContainerConfig{
		ID:     id,
		Spec:   spec,
		Scope:  scope,
		SecCtx: secCtx,
		Agent:  agent,
	})

	if rt.quota != nil {
		rt.quota.Reserve(&spec)
	}

	return c, rt.registry.Register(c)
}

func (rt *DefaultRuntime) admitSpec(spec *ContainerSpec) error {
	if rt.admission != nil {
		return rt.admission.Admit(spec)
	}
	return ValidateContainerSpec(spec)
}

func (rt *DefaultRuntime) checkQuota(spec *ContainerSpec) error {
	if rt.quota != nil {
		return rt.quota.CheckContainerFits(spec)
	}
	return nil
}

func (rt *DefaultRuntime) createAgentForSpec(ctx context.Context, spec *ContainerSpec) (ContainerAgent, error) {
	if rt.createAgent == nil {
		return nil, nil
	}
	return rt.createAgent(ctx, spec.AgentType)
}

func (rt *DefaultRuntime) createScope(id ContainerID, agentType string) *concurrency.GoroutineScope {
	agentID := string(id)
	if rt.budget != nil {
		rt.budget.RegisterAgent(agentID, agentType)
	}
	return concurrency.NewGoroutineScope(rt.parentCtx, agentID, rt.budget)
}

func (rt *DefaultRuntime) createSecurityContext(id ContainerID, agent ContainerAgent, spec *ContainerSpec) *csecurity.SecurityContext {
	agentID := ""
	if agent != nil {
		agentID = agent.AgentID()
	}
	caps := csecurity.NewCapabilitySet(
		spec.Security.Capabilities.Signals,
		spec.Security.Capabilities.PublishTopics,
		spec.Security.Capabilities.SubscribeTopics,
		spec.Security.Capabilities.PathRead,
		spec.Security.Capabilities.PathWrite,
		spec.Security.Capabilities.CanEscalate,
	)
	return csecurity.NewSecurityContext(csecurity.SecurityContextConfig{
		ContainerID:  string(id),
		AgentID:      agentID,
		Role:         spec.Security.Role,
		Capabilities: caps,
		ReadOnly:     spec.Security.RunAsReadOnly,
	})
}

// StartContainer transitions the container to Running.
func (rt *DefaultRuntime) StartContainer(ctx context.Context, c *Container) error {
	return c.Start(ctx)
}

// StopContainer stops a running container.
func (rt *DefaultRuntime) StopContainer(ctx context.Context, c *Container) error {
	return c.Stop(ctx)
}

// PauseContainer pauses a running container. Probes are stopped but the
// GoroutineScope stays alive for fast resume.
func (rt *DefaultRuntime) PauseContainer(_ context.Context, c *Container) error {
	return c.Pause()
}

// ResumeContainer resumes a paused container. Probes are restarted.
func (rt *DefaultRuntime) ResumeContainer(_ context.Context, c *Container) error {
	return c.Resume()
}

// RemoveContainer removes a container from the runtime. Must be stopped.
func (rt *DefaultRuntime) RemoveContainer(_ context.Context, c *Container) error {
	rt.registry.Unregister(c.ID())

	if rt.quota != nil {
		spec := c.Spec()
		rt.quota.Release(&spec)
	}

	if rt.budget != nil {
		rt.budget.UnregisterAgent(string(c.ID()))
	}
	return nil
}

// ContainerStatus returns a status snapshot.
func (rt *DefaultRuntime) ContainerStatus(c *Container) *ContainerStatus {
	return &ContainerStatus{
		ID:        c.ID(),
		State:     c.State(),
		Ready:     c.IsReady(),
		Alive:     c.IsAlive(),
		Restarts:  c.Metrics().RestartCount.Load(),
		StartTime: c.Metrics().StartTime,
		StopTime:  c.Metrics().StopTime,
	}
}

// CreateContainersForPod batch-creates containers for a managed pod,
// setting the pod ID on each container for registry indexing.
func (rt *DefaultRuntime) CreateContainersForPod(ctx context.Context, podID PodID, specs []ContainerSpec) ([]*Container, error) {
	if rt.closed.Load() {
		return nil, ErrRuntimeClosed
	}
	containers := make([]*Container, 0, len(specs))
	for i := range specs {
		c, err := rt.CreateContainer(ctx, specs[i])
		if err != nil {
			// Clean up already-created containers on failure.
			for _, created := range containers {
				_ = rt.RemoveContainer(ctx, created)
			}
			return nil, fmt.Errorf("container %q: %w", specs[i].Name, err)
		}
		c.SetPodID(podID)
		containers = append(containers, c)
	}
	return containers, nil
}

// StartContainers starts multiple containers. Stops and returns error on first failure.
func (rt *DefaultRuntime) StartContainers(ctx context.Context, containers []*Container) error {
	for _, c := range containers {
		if err := rt.StartContainer(ctx, c); err != nil {
			return fmt.Errorf("start %q: %w", c.Spec().Name, err)
		}
	}
	return nil
}

// Registry returns the container registry.
func (rt *DefaultRuntime) Registry() *ContainerRegistry {
	return rt.registry
}

// Close marks the runtime as closed. No new containers or pods can be created.
func (rt *DefaultRuntime) Close() {
	rt.closed.Store(true)
}
