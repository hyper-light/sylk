package container

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	csecurity "github.com/adalundhe/sylk/core/container/security"
	"github.com/google/uuid"
)

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

// PodStatus is a snapshot of a pod's current state.
type PodStatus struct {
	ID         PodID
	Phase      PodPhase
	Conditions map[PodConditionType]*PodCondition
	Containers []ContainerStatus
}

// AgentCreator creates an agent of the given type. Injected by the caller
// to decouple from the handoff package.
type AgentCreator func(ctx context.Context, agentType string) (ContainerAgent, error)

// ContainerRuntime is the container runtime interface (CRI equivalent).
type ContainerRuntime interface {
	CreateContainer(ctx context.Context, spec ContainerSpec, pod *Pod) (*Container, error)
	StartContainer(ctx context.Context, c *Container) error
	StopContainer(ctx context.Context, c *Container) error
	PauseContainer(ctx context.Context, c *Container) error
	ResumeContainer(ctx context.Context, c *Container) error
	RemoveContainer(ctx context.Context, c *Container) error
	ContainerStatus(c *Container) *ContainerStatus

	CreatePod(ctx context.Context, spec PodSpec) (*Pod, error)
	StartPod(ctx context.Context, p *Pod) error
	StopPod(ctx context.Context, p *Pod) error
	RemovePod(ctx context.Context, p *Pod) error
	PodStatus(p *Pod) *PodStatus
}

// DefaultRuntimeConfig provides configuration for the default runtime.
type DefaultRuntimeConfig struct {
	Budget          *concurrency.GoroutineBudget
	Registry        *ContainerRegistry
	Quota           *ResourceQuota
	Admission       *AdmissionController
	CreateAgent     AgentCreator
	ParentCtx       context.Context
}

// DefaultRuntime is the container runtime that wires existing primitives.
type DefaultRuntime struct {
	budget      *concurrency.GoroutineBudget
	registry    *ContainerRegistry
	quota       *ResourceQuota
	admission   *AdmissionController
	createAgent AgentCreator
	parentCtx   context.Context
	closed      atomic.Bool
}

// NewDefaultRuntime creates the default container runtime.
func NewDefaultRuntime(cfg DefaultRuntimeConfig) *DefaultRuntime {
	ctx := cfg.ParentCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return &DefaultRuntime{
		budget:      cfg.Budget,
		registry:    cfg.Registry,
		quota:       cfg.Quota,
		admission:   cfg.Admission,
		createAgent: cfg.CreateAgent,
		parentCtx:   ctx,
	}
}

// CreateContainer creates a container from a spec. Runs admission control,
// quota checks, creates the agent, GoroutineScope, and SecurityContext.
func (rt *DefaultRuntime) CreateContainer(ctx context.Context, spec ContainerSpec, pod *Pod) (*Container, error) {
	if rt.closed.Load() {
		return nil, ErrRuntimeClosed
	}

	if err := rt.admitSpec(&spec); err != nil {
		return nil, fmt.Errorf("admission: %w", err)
	}

	if err := rt.checkQuota(&spec); err != nil {
		return nil, fmt.Errorf("quota: %w", err)
	}

	agent, err := rt.createAgentForSpec(ctx, &spec)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
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
		Pod:    pod,
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

// CreatePod creates a pod with all its containers.
func (rt *DefaultRuntime) CreatePod(ctx context.Context, spec PodSpec) (*Pod, error) {
	if rt.closed.Load() {
		return nil, ErrRuntimeClosed
	}

	if err := ValidatePodSpec(&spec); err != nil {
		return nil, fmt.Errorf("pod validation: %w", err)
	}

	DefaultPodSpec(&spec)

	podID := PodID(uuid.New().String())
	scope := concurrency.NewGoroutineScope(rt.parentCtx, string(podID), rt.budget)

	pod := NewPod(PodConfig{
		ID:    podID,
		Spec:  spec,
		Scope: scope,
	})

	initContainers, err := rt.createContainerList(ctx, spec.InitContainers, pod)
	if err != nil {
		return nil, fmt.Errorf("init containers: %w", err)
	}
	pod.SetInitContainers(initContainers)

	mainContainers, err := rt.createContainerList(ctx, spec.MainContainers, pod)
	if err != nil {
		return nil, fmt.Errorf("main containers: %w", err)
	}
	pod.SetMainContainers(mainContainers)

	sidecarContainers, err := rt.createContainerList(ctx, spec.SidecarContainers, pod)
	if err != nil {
		return nil, fmt.Errorf("sidecar containers: %w", err)
	}
	pod.SetSidecarContainers(sidecarContainers)

	rt.registry.RegisterPod(pod)
	return pod, nil
}

func (rt *DefaultRuntime) createContainerList(ctx context.Context, specs []ContainerSpec, pod *Pod) ([]*Container, error) {
	containers := make([]*Container, 0, len(specs))
	for i := range specs {
		c, err := rt.CreateContainer(ctx, specs[i], pod)
		if err != nil {
			return nil, fmt.Errorf("container %q: %w", specs[i].Name, err)
		}
		containers = append(containers, c)
	}
	return containers, nil
}

// StartPod starts init containers sequentially, then main+sidecar concurrently.
func (rt *DefaultRuntime) StartPod(ctx context.Context, p *Pod) error {
	if err := p.Lifecycle().TransitionTo(concurrency.StateStarting); err != nil {
		return err
	}

	if err := rt.runInitContainers(ctx, p); err != nil {
		p.SetPhase(PodFailed)
		_ = p.Lifecycle().TransitionToFailed(err)
		return fmt.Errorf("init containers: %w", err)
	}
	p.SetCondition(PodCondition{Type: PodInitialized, Status: true})

	if err := rt.startMainAndSidecars(ctx, p); err != nil {
		p.SetPhase(PodFailed)
		_ = p.Lifecycle().TransitionToFailed(err)
		return fmt.Errorf("start main/sidecars: %w", err)
	}

	p.SetPhase(PodRunning)
	p.SetCondition(PodCondition{Type: PodContainersReady, Status: true})
	p.SetCondition(PodCondition{Type: PodReady, Status: true})
	return p.Lifecycle().TransitionTo(concurrency.StateRunning)
}

func (rt *DefaultRuntime) runInitContainers(ctx context.Context, p *Pod) error {
	for _, c := range p.InitContainers() {
		if err := rt.StartContainer(ctx, c); err != nil {
			return fmt.Errorf("init %q start: %w", c.Spec().Name, err)
		}
		if err := rt.waitForContainerStop(ctx, c); err != nil {
			return fmt.Errorf("init %q: %w", c.Spec().Name, err)
		}
	}
	return nil
}

func (rt *DefaultRuntime) waitForContainerStop(ctx context.Context, c *Container) error {
	waitTimeout := c.Spec().GracefulStop * 2
	if waitTimeout == 0 {
		waitTimeout = defaultGracefulStopDuration * 2
	}
	err := c.Lifecycle().WaitForState(concurrency.StateStopped, waitTimeout)
	if err != nil {
		return err
	}
	if c.State() == concurrency.StateFailed {
		return errors.New("container failed")
	}
	return nil
}

func (rt *DefaultRuntime) startMainAndSidecars(ctx context.Context, p *Pod) error {
	for _, c := range p.SidecarContainers() {
		if err := rt.StartContainer(ctx, c); err != nil {
			return fmt.Errorf("sidecar %q: %w", c.Spec().Name, err)
		}
	}
	for _, c := range p.MainContainers() {
		if err := rt.StartContainer(ctx, c); err != nil {
			return fmt.Errorf("main %q: %w", c.Spec().Name, err)
		}
	}
	return nil
}

// StopPod stops all containers in reverse order: main, then sidecars.
func (rt *DefaultRuntime) StopPod(ctx context.Context, p *Pod) error {
	if err := p.Lifecycle().TransitionTo(concurrency.StateStopping); err != nil {
		return err
	}

	rt.stopContainerList(ctx, p.MainContainers())
	rt.stopContainerList(ctx, p.SidecarContainers())

	p.SetPhase(p.DerivePhaseFromContainers())
	return p.Lifecycle().TransitionTo(concurrency.StateStopped)
}

func (rt *DefaultRuntime) stopContainerList(ctx context.Context, containers []*Container) {
	for _, c := range containers {
		if c.IsRunning() {
			_ = rt.StopContainer(ctx, c)
		}
	}
}

// RemovePod removes a pod and all its containers.
func (rt *DefaultRuntime) RemovePod(ctx context.Context, p *Pod) error {
	for _, c := range p.AllContainers() {
		_ = rt.RemoveContainer(ctx, c)
	}
	rt.registry.UnregisterPod(p.ID())
	return nil
}

// PodStatus returns a status snapshot.
func (rt *DefaultRuntime) PodStatus(p *Pod) *PodStatus {
	containers := p.AllContainers()
	statuses := make([]ContainerStatus, 0, len(containers))
	for _, c := range containers {
		statuses = append(statuses, *rt.ContainerStatus(c))
	}
	return &PodStatus{
		ID:         p.ID(),
		Phase:      p.Phase(),
		Conditions: p.conditionsSnapshot(),
		Containers: statuses,
	}
}

func (p *Pod) conditionsSnapshot() map[PodConditionType]*PodCondition {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cp := make(map[PodConditionType]*PodCondition, len(p.conditions))
	for k, v := range p.conditions {
		cond := *v
		cp[k] = &cond
	}
	return cp
}

// Registry returns the container registry.
func (rt *DefaultRuntime) Registry() *ContainerRegistry {
	return rt.registry
}

// Close marks the runtime as closed. No new containers or pods can be created.
func (rt *DefaultRuntime) Close() {
	rt.closed.Store(true)
}
