package pod

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/container"
)

var (
	ErrPodReleased  = errors.New("pod has been released")
	ErrPodNotHot    = errors.New("pod is not at TierHot")
	ErrUnknownAgent = errors.New("unknown agent type in pod")
)

// GuardEntry tracks the demotion guards held for a single node.
type GuardEntry struct {
	AgentTypes []string
	Releases   []func()
	AcquiredAt time.Time
}

// ManagedPodConfig provides construction parameters for a ManagedPod.
type ManagedPodConfig struct {
	ID            PodID
	Policy        *PodPolicy
	Runtime       container.ContainerRuntime
	Logger        *slog.Logger
	VolumeManager *VolumeManager
}

// ManagedPod owns the lifecycle of a group of containers that activate
// and deactivate as a unit. It manages tier transitions, demotion guards,
// member containers, and resource tracking.
type ManagedPod struct {
	id      PodID
	podType PodType
	policy  *PodPolicy
	runtime container.ContainerRuntime
	logger  *slog.Logger
	volumes *VolumeManager

	// Tier state (Cold → Cool → Warm → Hot).
	tier atomic.Int32

	// Member containers — keyed by agent type.
	containersMu sync.RWMutex
	containers   map[string]*container.Container

	// Demotion guards — per-node (pipeline) or per-request (global).
	guardsMu   sync.Mutex
	nodeGuards map[string]*GuardEntry
	released   bool

	// Activity tracking.
	lastActive     atomic.Int64
	activeRequests atomic.Int64

	// Metrics.
	metrics PodMetrics

	specsMu sync.RWMutex
	specs   []container.ContainerSpec
}

// NewManagedPod creates a pod with the given configuration.
func NewManagedPod(cfg ManagedPodConfig) *ManagedPod {
	p := &ManagedPod{
		id:         cfg.ID,
		podType:    cfg.Policy.PodType,
		policy:     cfg.Policy,
		runtime:    cfg.Runtime,
		logger:     cfg.Logger,
		volumes:    cfg.VolumeManager,
		containers: make(map[string]*container.Container),
		nodeGuards: make(map[string]*GuardEntry),
	}
	p.tier.Store(int32(TierCold))
	p.touchActivity()
	return p
}

// ID returns the pod's unique identifier.
func (p *ManagedPod) ID() PodID { return p.id }

// Type returns the pod type.
func (p *ManagedPod) Type() PodType { return p.podType }

// Policy returns the pod's policy.
func (p *ManagedPod) Policy() *PodPolicy { return p.policy }

// LoadTier returns the current activation tier.
func (p *ManagedPod) LoadTier() ActivationTier {
	return ActivationTier(p.tier.Load())
}

// StoreTier sets the activation tier atomically.
func (p *ManagedPod) StoreTier(t ActivationTier) {
	p.tier.Store(int32(t))
}

// Metrics returns the pod's metrics.
func (p *ManagedPod) Metrics() *PodMetrics { return &p.metrics }

// touchActivity updates the last-active timestamp.
func (p *ManagedPod) touchActivity() {
	p.lastActive.Store(time.Now().UnixNano())
}

// TouchActivity is the public API for resetting the idle timer.
func (p *ManagedPod) TouchActivity() {
	p.touchActivity()
}

// IdleDuration returns how long the pod has been idle.
func (p *ManagedPod) IdleDuration() time.Duration {
	last := p.lastActive.Load()
	if last == 0 {
		return 0
	}
	return time.Since(time.Unix(0, last))
}

// HasActiveRequests returns true if any guard is currently held.
func (p *ManagedPod) HasActiveRequests() bool {
	return p.activeRequests.Load() > 0
}

// ActiveRequestCount returns the number of active request guards.
func (p *ManagedPod) ActiveRequestCount() int64 {
	return p.activeRequests.Load()
}

// IsReleased returns true if the pod has been released.
func (p *ManagedPod) IsReleased() bool {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	return p.released
}

// MemberTypes returns the pod's configured agent types.
func (p *ManagedPod) MemberTypes() []string {
	return p.policy.MemberTypes
}

// ---------- Container management ----------

// SetContainer associates a container with an agent type in this pod.
func (p *ManagedPod) SetContainer(agentType string, c *container.Container) {
	p.containersMu.Lock()
	defer p.containersMu.Unlock()
	p.containers[agentType] = c
}

// ContainerFor returns the container for the given agent type, or nil.
func (p *ManagedPod) ContainerFor(agentType string) *container.Container {
	p.containersMu.RLock()
	defer p.containersMu.RUnlock()
	return p.containers[agentType]
}

// AllContainers returns a snapshot of all member containers.
func (p *ManagedPod) AllContainers() []*container.Container {
	p.containersMu.RLock()
	defer p.containersMu.RUnlock()
	result := make([]*container.Container, 0, len(p.containers))
	for _, c := range p.containers {
		result = append(result, c)
	}
	return result
}

// ClearContainers removes all container references.
func (p *ManagedPod) ClearContainers() {
	p.containersMu.Lock()
	defer p.containersMu.Unlock()
	clear(p.containers)
}

// ContainerCount returns the number of registered containers.
func (p *ManagedPod) ContainerCount() int {
	p.containersMu.RLock()
	defer p.containersMu.RUnlock()
	return len(p.containers)
}

// ---------- Tier transitions ----------

// Promote brings the pod to TierHot from its current tier.
// Creates and starts containers for all member types using the runtime.
// Caller must provide ContainerSpecs matching the pod's member types.
func (p *ManagedPod) Promote(ctx context.Context, specs []container.ContainerSpec) error {
	p.cacheSpecs(specs)
	current := p.LoadTier()
	if current == TierHot {
		p.metrics.HotHits.Add(1)
		p.touchActivity()
		return nil
	}

	switch current {
	case TierCold:
		return p.promoteFromCold(ctx, specs)
	case TierCool:
		return p.promoteFromCool(ctx, specs)
	case TierWarm:
		return p.promoteFromWarm(ctx)
	default:
		return fmt.Errorf("unknown tier %d", current)
	}
}

func (p *ManagedPod) promoteFromCold(ctx context.Context, specs []container.ContainerSpec) error {
	specs = p.resolveSpecs(specs)
	if err := p.mountVolumes(ctx); err != nil {
		return fmt.Errorf("pod %s: mount volumes: %w", p.id, err)
	}
	containers, err := p.runtime.CreateContainersForPod(ctx, container.PodID(p.id), specs)
	if err != nil {
		p.unmountVolumes(ctx)
		return fmt.Errorf("pod %s: create containers: %w", p.id, err)
	}
	p.indexContainers(containers, specs)
	p.injectFileAccess()

	if err := p.runtime.StartContainers(ctx, containers); err != nil {
		p.cleanupContainers(ctx, containers)
		p.ClearContainers()
		p.unmountVolumes(ctx)
		return fmt.Errorf("pod %s: start containers: %w", p.id, err)
	}

	p.StoreTier(TierHot)
	p.touchActivity()
	p.metrics.ColdStarts.Add(1)
	p.metrics.PromotionsTotal.Add(1)

	if p.logger != nil {
		p.logger.Info("pod promoted", "pod_id", p.id, "from", "cold", "to", "hot",
			"members", len(containers))
	}
	return nil
}

func (p *ManagedPod) promoteFromCool(ctx context.Context, specs []container.ContainerSpec) error {
	specs = p.resolveSpecs(specs)
	if err := p.mountVolumes(ctx); err != nil {
		return fmt.Errorf("pod %s: mount volumes from cool: %w", p.id, err)
	}
	containers, err := p.runtime.CreateContainersForPod(ctx, container.PodID(p.id), specs)
	if err != nil {
		p.unmountVolumes(ctx)
		return fmt.Errorf("pod %s: create containers from cool: %w", p.id, err)
	}
	p.indexContainers(containers, specs)
	p.injectFileAccess()

	if err := p.runtime.StartContainers(ctx, containers); err != nil {
		p.cleanupContainers(ctx, containers)
		p.ClearContainers()
		p.unmountVolumes(ctx)
		return fmt.Errorf("pod %s: start containers from cool: %w", p.id, err)
	}

	p.StoreTier(TierHot)
	p.touchActivity()
	p.metrics.CoolStarts.Add(1)
	p.metrics.PromotionsTotal.Add(1)

	if p.logger != nil {
		p.logger.Info("pod promoted", "pod_id", p.id, "from", "cool", "to", "hot",
			"members", len(containers))
	}
	return nil
}

func (p *ManagedPod) promoteFromWarm(ctx context.Context) error {
	// Warm→Hot: resume paused containers.
	for agentType, c := range p.allContainersSnapshot() {
		if err := p.runtime.ResumeContainer(ctx, c); err != nil {
			return fmt.Errorf("pod %s: resume %s: %w", p.id, agentType, err)
		}
	}

	p.StoreTier(TierHot)
	p.touchActivity()
	p.metrics.WarmStarts.Add(1)
	p.metrics.PromotionsTotal.Add(1)

	if p.logger != nil {
		p.logger.Info("pod promoted", "pod_id", p.id, "from", "warm", "to", "hot")
	}
	return nil
}

// Demote transitions the pod to the target tier.
func (p *ManagedPod) Demote(ctx context.Context, target ActivationTier) error {
	current := p.LoadTier()
	if current <= target {
		return nil // already at or below target
	}

	// Step down one tier at a time.
	for current > target {
		var err error
		switch current {
		case TierHot:
			err = p.demoteHotToWarm(ctx)
		case TierWarm:
			err = p.demoteWarmToCool(ctx)
		case TierCool:
			err = p.demoteCoolToCold(ctx)
		}
		if err != nil {
			return err
		}
		current = p.LoadTier()
	}
	return nil
}

func (p *ManagedPod) demoteHotToWarm(ctx context.Context) error {
	for agentType, c := range p.allContainersSnapshot() {
		if err := p.runtime.PauseContainer(ctx, c); err != nil {
			return fmt.Errorf("pod %s: pause %s: %w", p.id, agentType, err)
		}
	}

	p.StoreTier(TierWarm)
	p.metrics.DemotionsToWarm.Add(1)

	if p.logger != nil {
		p.logger.Info("pod demoted", "pod_id", p.id, "from", "hot", "to", "warm")
	}
	return nil
}

func (p *ManagedPod) demoteWarmToCool(ctx context.Context) error {
	for agentType, c := range p.allContainersSnapshot() {
		if err := p.runtime.StopContainer(ctx, c); err != nil {
			if p.logger != nil {
				p.logger.Warn("pod: stop container failed during demotion",
					"pod_id", p.id, "agent_type", agentType, "error", err)
			}
		}
		if err := p.runtime.RemoveContainer(ctx, c); err != nil {
			if p.logger != nil {
				p.logger.Warn("pod: remove container failed during demotion",
					"pod_id", p.id, "agent_type", agentType, "error", err)
			}
		}
	}

	p.ClearContainers()
	p.unmountVolumes(ctx)
	p.StoreTier(TierCool)
	p.metrics.DemotionsToCool.Add(1)

	if p.logger != nil {
		p.logger.Info("pod demoted", "pod_id", p.id, "from", "warm", "to", "cool")
	}
	return nil
}

func (p *ManagedPod) demoteCoolToCold(ctx context.Context) error {
	_ = ctx
	p.StoreTier(TierCold)
	p.metrics.DemotionsToCold.Add(1)

	if p.logger != nil {
		p.logger.Info("pod demoted", "pod_id", p.id, "from", "cool", "to", "cold")
	}
	return nil
}

// ---------- Guard management ----------

// AcquireRequestGuard increments the active request count and returns
// an idempotent release function.
func (p *ManagedPod) AcquireRequestGuard() func() {
	p.activeRequests.Add(1)
	p.touchActivity()
	p.metrics.GuardAcquisitions.Add(1)

	released := atomic.Bool{}
	return func() {
		if released.CompareAndSwap(false, true) {
			p.activeRequests.Add(-1)
			p.metrics.GuardReleases.Add(1)
		}
	}
}

// HoldForNode atomically acquires demotion guards for the given agent types
// under a node ID. On failure, all guards acquired for this node are released.
func (p *ManagedPod) HoldForNode(ctx context.Context, nodeID string, agentTypes []string) error {
	_ = ctx // guards are local bookkeeping, no I/O needed

	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()

	if p.released {
		return ErrPodReleased
	}

	releases := make([]func(), 0, len(agentTypes))
	for range agentTypes {
		release := p.acquireGuardLocked()
		releases = append(releases, release)
	}

	p.nodeGuards[nodeID] = &GuardEntry{
		AgentTypes: agentTypes,
		Releases:   releases,
		AcquiredAt: time.Now(),
	}
	return nil
}

// ReleaseForNode releases all guards held for the given node ID.
func (p *ManagedPod) ReleaseForNode(nodeID string) {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()

	entry, ok := p.nodeGuards[nodeID]
	if !ok {
		return
	}
	for _, release := range entry.Releases {
		release()
	}
	delete(p.nodeGuards, nodeID)
}

// Release releases all guards and marks the pod as released.
func (p *ManagedPod) Release() {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()

	if p.released {
		return
	}
	p.released = true

	for nodeID, entry := range p.nodeGuards {
		for _, release := range entry.Releases {
			release()
		}
		delete(p.nodeGuards, nodeID)
	}
}

// ActiveGuardCount returns the number of active node guard entries.
func (p *ManagedPod) ActiveGuardCount() int {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	return len(p.nodeGuards)
}

// acquireGuardLocked increments the active request counter and returns a
// release function. Must be called with guardsMu held.
func (p *ManagedPod) acquireGuardLocked() func() {
	p.activeRequests.Add(1)
	p.touchActivity()
	p.metrics.GuardAcquisitions.Add(1)

	released := atomic.Bool{}
	return func() {
		if released.CompareAndSwap(false, true) {
			p.activeRequests.Add(-1)
			p.metrics.GuardReleases.Add(1)
		}
	}
}

// ---------- Internal helpers ----------

// indexContainers maps containers to their agent types based on spec order.
func (p *ManagedPod) indexContainers(containers []*container.Container, specs []container.ContainerSpec) {
	p.containersMu.Lock()
	defer p.containersMu.Unlock()
	for i, c := range containers {
		if i < len(specs) {
			p.containers[specs[i].AgentType] = c
		}
	}
}

func (p *ManagedPod) injectFileAccess() {
	if p.volumes == nil {
		return
	}
	p.volumes.InjectFileAccess(p.allContainersSnapshot())
}

func (p *ManagedPod) mountVolumes(ctx context.Context) error {
	if p.volumes == nil {
		return nil
	}
	return p.volumes.MountAll(ctx)
}

func (p *ManagedPod) unmountVolumes(ctx context.Context) {
	if p.volumes == nil {
		return
	}
	_ = p.volumes.UnmountAll(ctx)
}

func (p *ManagedPod) cacheSpecs(specs []container.ContainerSpec) {
	if len(specs) == 0 {
		return
	}
	p.specsMu.Lock()
	defer p.specsMu.Unlock()
	p.specs = append(p.specs[:0], specs...)
}

func (p *ManagedPod) resolveSpecs(specs []container.ContainerSpec) []container.ContainerSpec {
	if len(specs) > 0 {
		return specs
	}
	p.specsMu.RLock()
	defer p.specsMu.RUnlock()
	if len(p.specs) == 0 {
		return nil
	}
	return append([]container.ContainerSpec(nil), p.specs...)
}

// SetSpecs primes the pod with the specs needed for a future lazy promotion.
func (p *ManagedPod) SetSpecs(specs []container.ContainerSpec) {
	p.cacheSpecs(specs)
}

// allContainersSnapshot returns a copy of the containers map.
func (p *ManagedPod) allContainersSnapshot() map[string]*container.Container {
	p.containersMu.RLock()
	defer p.containersMu.RUnlock()
	snap := make(map[string]*container.Container, len(p.containers))
	for k, v := range p.containers {
		snap[k] = v
	}
	return snap
}

// cleanupContainers removes a slice of containers on promotion failure.
func (p *ManagedPod) cleanupContainers(ctx context.Context, containers []*container.Container) {
	for _, c := range containers {
		_ = p.runtime.RemoveContainer(ctx, c)
	}
}
