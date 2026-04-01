package shared

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/pod"
)

func (p *AgentPod) Policy() *pod.PodPolicy {
	return p.policy
}

func (p *AgentPod) Type() pod.PodType {
	if p.policy == nil {
		return pod.PodTypePipeline
	}
	return p.policy.PodType
}

func (p *AgentPod) LoadTier() pod.ActivationTier {
	return pod.ActivationTier(p.tier.Load())
}

func (p *AgentPod) StoreTier(t pod.ActivationTier) {
	p.tier.Store(t)
}

func (p *AgentPod) Metrics() *pod.PodMetrics {
	return &p.metrics
}

func (p *AgentPod) touchActivity() {
	p.lastActive.Store(time.Now().UnixNano())
}

func (p *AgentPod) TouchActivity() {
	p.touchActivity()
}

func (p *AgentPod) IdleDuration() time.Duration {
	last := p.lastActive.Load()
	if last == 0 {
		return 0
	}
	return time.Since(time.Unix(0, last))
}

func (p *AgentPod) HasActiveRequests() bool {
	return p.activeReqs.Load() > 0
}

func (p *AgentPod) ActiveRequestCount() int64 {
	return p.activeReqs.Load()
}

func (p *AgentPod) IsReleased() bool {
	p.guardsMu.Lock()
	defer p.guardsMu.Unlock()
	return p.released
}

func (p *AgentPod) SetContainer(agentType string, c *container.Container) {
	p.containersMu.Lock()
	defer p.containersMu.Unlock()
	p.containers[agentType] = c
}

func (p *AgentPod) ContainerFor(agentType string) *container.Container {
	p.containersMu.RLock()
	defer p.containersMu.RUnlock()
	return p.containers[agentType]
}

func (p *AgentPod) AllContainers() []*container.Container {
	p.containersMu.RLock()
	defer p.containersMu.RUnlock()
	result := make([]*container.Container, 0, len(p.containers))
	for _, c := range p.containers {
		result = append(result, c)
	}
	return result
}

func (p *AgentPod) ClearContainers() {
	p.containersMu.Lock()
	defer p.containersMu.Unlock()
	clear(p.containers)
}

func (p *AgentPod) ContainerCount() int {
	p.containersMu.RLock()
	defer p.containersMu.RUnlock()
	return len(p.containers)
}

func (p *AgentPod) SetSpecs(specs []container.ContainerSpec) {
	if len(specs) == 0 {
		return
	}
	p.specsMu.Lock()
	defer p.specsMu.Unlock()
	p.specs = append(p.specs[:0], specs...)
}

func (p *AgentPod) Promote(ctx context.Context, specs []container.ContainerSpec) error {
	if p.runtime == nil {
		return nil
	}

	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	if p.IsReleased() {
		return pod.ErrPodReleased
	}

	p.cacheSpecs(specs)
	current := p.LoadTier()
	if current == pod.TierHot {
		if err := p.ensureVolumesReady(ctx); err != nil {
			return err
		}
		p.metrics.HotHits.Add(1)
		p.touchActivity()
		return nil
	}

	switch current {
	case pod.TierCold:
		return p.promoteFromCold(ctx, specs)
	case pod.TierCool:
		return p.promoteFromCool(ctx, specs)
	case pod.TierWarm:
		return p.promoteFromWarm(ctx)
	default:
		return fmt.Errorf("unknown tier %d", current)
	}
}

func (p *AgentPod) Demote(ctx context.Context, target pod.ActivationTier) error {
	if p.runtime == nil {
		return nil
	}

	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	current := p.LoadTier()
	if current <= target {
		return nil
	}

	for current > target {
		var err error
		switch current {
		case pod.TierHot:
			err = p.demoteHotToWarm(ctx)
		case pod.TierWarm:
			err = p.demoteWarmToCool(ctx)
		case pod.TierCool:
			err = p.demoteCoolToCold(ctx)
		}
		if err != nil {
			return err
		}
		current = p.LoadTier()
	}
	return nil
}

func (p *AgentPod) AcquireRequestGuard() func() {
	p.activeReqs.Add(1)
	p.touchActivity()
	p.metrics.GuardAcquisitions.Add(1)

	released := atomic.Bool{}
	return func() {
		if released.CompareAndSwap(false, true) {
			p.activeReqs.Add(-1)
			p.metrics.GuardReleases.Add(1)
		}
	}
}

func (p *AgentPod) promoteFromCold(ctx context.Context, specs []container.ContainerSpec) error {
	specs = p.resolveSpecs(specs)
	if err := p.mountVolumes(ctx); err != nil {
		return fmt.Errorf("pod %s: mount volumes: %w", p.podID, err)
	}
	containers, err := p.runtime.CreateContainersForPod(ctx, container.PodID(p.podID), specs)
	if err != nil {
		p.unmountVolumes(ctx)
		return fmt.Errorf("pod %s: create containers: %w", p.podID, err)
	}
	p.indexContainers(containers, specs)
	p.injectFileAccess()

	if err := p.runtime.StartContainers(ctx, containers); err != nil {
		p.cleanupContainers(ctx, containers)
		p.ClearContainers()
		p.unmountVolumes(ctx)
		return fmt.Errorf("pod %s: start containers: %w", p.podID, err)
	}

	p.StoreTier(pod.TierHot)
	p.touchActivity()
	p.metrics.ColdStarts.Add(1)
	p.metrics.PromotionsTotal.Add(1)
	return nil
}

func (p *AgentPod) promoteFromCool(ctx context.Context, specs []container.ContainerSpec) error {
	specs = p.resolveSpecs(specs)
	if err := p.mountVolumes(ctx); err != nil {
		return fmt.Errorf("pod %s: mount volumes from cool: %w", p.podID, err)
	}
	containers, err := p.runtime.CreateContainersForPod(ctx, container.PodID(p.podID), specs)
	if err != nil {
		p.unmountVolumes(ctx)
		return fmt.Errorf("pod %s: create containers from cool: %w", p.podID, err)
	}
	p.indexContainers(containers, specs)
	p.injectFileAccess()

	if err := p.runtime.StartContainers(ctx, containers); err != nil {
		p.cleanupContainers(ctx, containers)
		p.ClearContainers()
		p.unmountVolumes(ctx)
		return fmt.Errorf("pod %s: start containers from cool: %w", p.podID, err)
	}

	p.StoreTier(pod.TierHot)
	p.touchActivity()
	p.metrics.CoolStarts.Add(1)
	p.metrics.PromotionsTotal.Add(1)
	return nil
}

func (p *AgentPod) promoteFromWarm(ctx context.Context) error {
	if err := p.ensureVolumesReady(ctx); err != nil {
		return fmt.Errorf("pod %s: ensure volumes from warm: %w", p.podID, err)
	}
	for agentType, c := range p.allContainersSnapshot() {
		if err := p.runtime.ResumeContainer(ctx, c); err != nil {
			return fmt.Errorf("pod %s: resume %s: %w", p.podID, agentType, err)
		}
	}

	p.StoreTier(pod.TierHot)
	p.touchActivity()
	p.metrics.WarmStarts.Add(1)
	p.metrics.PromotionsTotal.Add(1)
	return nil
}

func (p *AgentPod) demoteHotToWarm(ctx context.Context) error {
	for agentType, c := range p.allContainersSnapshot() {
		if err := p.runtime.PauseContainer(ctx, c); err != nil {
			return fmt.Errorf("pod %s: pause %s: %w", p.podID, agentType, err)
		}
	}

	p.StoreTier(pod.TierWarm)
	p.metrics.DemotionsToWarm.Add(1)
	return nil
}

func (p *AgentPod) demoteWarmToCool(ctx context.Context) error {
	for agentType, c := range p.allContainersSnapshot() {
		if err := p.runtime.StopContainer(ctx, c); err != nil && p.logger != nil {
			p.logger.Warn("pod: stop container failed during demotion", "pod_id", p.podID, "agent_type", agentType, "error", err)
		}
		if err := p.runtime.RemoveContainer(ctx, c); err != nil && p.logger != nil {
			p.logger.Warn("pod: remove container failed during demotion", "pod_id", p.podID, "agent_type", agentType, "error", err)
		}
	}

	p.ClearContainers()
	p.unmountVolumes(ctx)
	p.StoreTier(pod.TierCool)
	p.metrics.DemotionsToCool.Add(1)
	return nil
}

func (p *AgentPod) demoteCoolToCold(context.Context) error {
	p.StoreTier(pod.TierCold)
	p.metrics.DemotionsToCold.Add(1)
	return nil
}

func (p *AgentPod) indexContainers(containers []*container.Container, specs []container.ContainerSpec) {
	p.containersMu.Lock()
	defer p.containersMu.Unlock()
	for i, c := range containers {
		if i < len(specs) {
			p.containers[specs[i].AgentType] = c
		}
	}
}

func (p *AgentPod) injectFileAccess() {
	if p.volumes == nil {
		return
	}
	p.volumes.InjectFileAccess(p.allContainersSnapshot())
}

func (p *AgentPod) mountVolumes(ctx context.Context) error {
	if p.volumes == nil {
		return nil
	}
	return p.volumes.MountAll(ctx)
}

func (p *AgentPod) ensureVolumesReady(ctx context.Context) error {
	if p.volumes == nil {
		return nil
	}
	return p.volumes.EnsureReady(ctx)
}

func (p *AgentPod) unmountVolumes(ctx context.Context) {
	if p.volumes == nil {
		return
	}
	_ = p.volumes.UnmountAll(ctx)
}

func (p *AgentPod) cacheSpecs(specs []container.ContainerSpec) {
	if len(specs) == 0 {
		return
	}
	p.specsMu.Lock()
	defer p.specsMu.Unlock()
	p.specs = append(p.specs[:0], specs...)
}

func (p *AgentPod) resolveSpecs(specs []container.ContainerSpec) []container.ContainerSpec {
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

func (p *AgentPod) allContainersSnapshot() map[string]*container.Container {
	p.containersMu.RLock()
	defer p.containersMu.RUnlock()
	snapshot := make(map[string]*container.Container, len(p.containers))
	for agentType, c := range p.containers {
		snapshot[agentType] = c
	}
	return snapshot
}

func (p *AgentPod) cleanupContainers(ctx context.Context, containers []*container.Container) {
	for _, c := range containers {
		_ = p.runtime.RemoveContainer(ctx, c)
	}
}
