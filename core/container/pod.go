package container

import (
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// PodPhase represents the current phase of a pod's lifecycle.
type PodPhase int

const (
	PodPending   PodPhase = iota // Init containers not yet complete
	PodRunning                   // All init succeeded, ≥1 main container running
	PodSucceeded                 // All main containers stopped cleanly
	PodFailed                    // Any container failed, restart policy exhausted
)

// podPhaseNames maps pod phases to their string representation.
var podPhaseNames = map[PodPhase]string{
	PodPending:   "pending",
	PodRunning:   "running",
	PodSucceeded: "succeeded",
	PodFailed:    "failed",
}

// String returns the string representation of a pod phase.
func (p PodPhase) String() string {
	if name, ok := podPhaseNames[p]; ok {
		return name
	}
	return "unknown"
}

// PodConditionType identifies a pod condition.
type PodConditionType int

const (
	PodInitialized     PodConditionType = iota // All init containers succeeded
	PodReady                                   // Pod is serving traffic
	PodContainersReady                         // All containers healthy
	PodScheduled                               // Pod has been scheduled
)

// PodCondition tracks a boolean condition with last transition time.
type PodCondition struct {
	Type               PodConditionType
	Status             bool
	LastTransitionTime time.Time
	Reason             string
	Message            string
}

// Pod is a group of containers that share a network namespace and volumes.
type Pod struct {
	id                PodID
	spec              PodSpec
	phase             PodPhase
	conditions        map[PodConditionType]*PodCondition
	initContainers    []*Container
	mainContainers    []*Container
	sidecarContainers []*Container
	scope             *concurrency.GoroutineScope
	lifecycle         *concurrency.Lifecycle
	mu                sync.RWMutex
}

// PodConfig provides construction parameters for a Pod.
type PodConfig struct {
	ID    PodID
	Spec  PodSpec
	Scope *concurrency.GoroutineScope
}

// NewPod creates a pod in the Pending phase.
func NewPod(cfg PodConfig) *Pod {
	lc := concurrency.NewLifecycle(string(cfg.ID))
	return &Pod{
		id:         cfg.ID,
		spec:       cfg.Spec,
		phase:      PodPending,
		conditions: make(map[PodConditionType]*PodCondition, podConditionTypeCount),
		scope:      cfg.Scope,
		lifecycle:  lc,
	}
}

// podConditionTypeCount is the number of distinct condition types.
const podConditionTypeCount = 4

// ID returns the pod's unique identifier.
func (p *Pod) ID() PodID {
	return p.id
}

// Spec returns a copy of the pod specification.
func (p *Pod) Spec() PodSpec {
	return p.spec
}

// Phase returns the current pod phase.
func (p *Pod) Phase() PodPhase {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.phase
}

// Lifecycle returns the underlying lifecycle for state observation.
func (p *Pod) Lifecycle() *concurrency.Lifecycle {
	return p.lifecycle
}

// SetPhase transitions the pod to a new phase. Thread-safe.
func (p *Pod) SetPhase(phase PodPhase) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = phase
}

// SetCondition updates a pod condition. Thread-safe.
func (p *Pod) SetCondition(cond PodCondition) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cond.LastTransitionTime = time.Now()
	p.conditions[cond.Type] = &cond
}

// Condition returns the current value of a condition type.
func (p *Pod) Condition(condType PodConditionType) *PodCondition {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.conditions[condType]
}

// SetInitContainers sets the init container list.
func (p *Pod) SetInitContainers(containers []*Container) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initContainers = containers
}

// SetMainContainers sets the main container list.
func (p *Pod) SetMainContainers(containers []*Container) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mainContainers = containers
}

// SetSidecarContainers sets the sidecar container list.
func (p *Pod) SetSidecarContainers(containers []*Container) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sidecarContainers = containers
}

// InitContainers returns the init containers.
func (p *Pod) InitContainers() []*Container {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.initContainers
}

// MainContainers returns the main containers.
func (p *Pod) MainContainers() []*Container {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mainContainers
}

// SidecarContainers returns the sidecar containers.
func (p *Pod) SidecarContainers() []*Container {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sidecarContainers
}

// AllContainers returns all containers in the pod (init + main + sidecar).
func (p *Pod) AllContainers() []*Container {
	p.mu.RLock()
	defer p.mu.RUnlock()
	total := len(p.initContainers) + len(p.mainContainers) + len(p.sidecarContainers)
	all := make([]*Container, 0, total)
	all = append(all, p.initContainers...)
	all = append(all, p.mainContainers...)
	all = append(all, p.sidecarContainers...)
	return all
}

// IsTerminal returns true if the pod is in a terminal phase.
func (p *Pod) IsTerminal() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.phase == PodSucceeded || p.phase == PodFailed
}

// Scope returns the pod's goroutine scope.
func (p *Pod) Scope() *concurrency.GoroutineScope {
	return p.scope
}

// DerivePhaseFromContainers computes the pod phase from container states.
func (p *Pod) DerivePhaseFromContainers() PodPhase {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.derivePhase()
}

func (p *Pod) derivePhase() PodPhase {
	if p.hasFailedContainers() {
		return PodFailed
	}
	if !p.allInitCompleted() {
		return PodPending
	}
	if p.allMainStopped() {
		return PodSucceeded
	}
	if p.anyMainRunning() {
		return PodRunning
	}
	return PodPending
}

func (p *Pod) hasFailedContainers() bool {
	for _, c := range p.initContainers {
		if c.State() == concurrency.StateFailed {
			return true
		}
	}
	for _, c := range p.mainContainers {
		if c.State() == concurrency.StateFailed {
			return true
		}
	}
	return false
}

func (p *Pod) allInitCompleted() bool {
	for _, c := range p.initContainers {
		if !c.IsTerminal() {
			return false
		}
		if c.State() != concurrency.StateStopped {
			return false
		}
	}
	return true
}

func (p *Pod) allMainStopped() bool {
	for _, c := range p.mainContainers {
		if c.State() != concurrency.StateStopped {
			return false
		}
	}
	return len(p.mainContainers) > 0
}

func (p *Pod) anyMainRunning() bool {
	for _, c := range p.mainContainers {
		if c.IsRunning() {
			return true
		}
	}
	return false
}
