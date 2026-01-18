package concurrency

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// ResourceType represents the type of tracked resource.
type ResourceType string

const (
	ResourceTypeFile       ResourceType = "file"
	ResourceTypeConnection ResourceType = "connection"
	ResourceTypeProcess    ResourceType = "process"
)

// TrackedResource represents a resource that can be force-closed.
type TrackedResource interface {
	ID() string
	Type() ResourceType
	ForceClose() error
	IsClosed() bool
}

// ResourceMetrics contains metrics about tracked resources.
type ResourceMetrics struct {
	TotalTracked     int64
	TotalReleased    int64
	TotalForceClosed int64
	ActiveByType     map[ResourceType]int64
}

// ResourceTracker tracks resources for an agent.
type ResourceTracker struct {
	agentID   string
	mu        sync.RWMutex
	resources map[string]TrackedResource
	metrics   *resourceMetricsState
}

// resourceMetricsState holds atomic counters for metrics.
type resourceMetricsState struct {
	totalTracked     atomic.Int64
	totalReleased    atomic.Int64
	totalForceClosed atomic.Int64
	activeByType     map[ResourceType]*atomic.Int64
	typeMu           sync.RWMutex
}

// NewResourceTracker creates a new resource tracker for an agent.
func NewResourceTracker(agentID string) *ResourceTracker {
	return &ResourceTracker{
		agentID:   agentID,
		resources: make(map[string]TrackedResource),
		metrics:   newResourceMetricsState(),
	}
}

func newResourceMetricsState() *resourceMetricsState {
	return &resourceMetricsState{
		activeByType: make(map[ResourceType]*atomic.Int64),
	}
}

// Track adds a resource to tracking.
func (rt *ResourceTracker) Track(resource TrackedResource) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	id := resource.ID()
	if _, exists := rt.resources[id]; exists {
		return
	}

	rt.resources[id] = resource
	rt.metrics.totalTracked.Add(1)
	rt.incrementTypeCount(resource.Type())
}

func (rt *ResourceTracker) incrementTypeCount(resType ResourceType) {
	rt.metrics.typeMu.Lock()
	defer rt.metrics.typeMu.Unlock()

	counter, exists := rt.metrics.activeByType[resType]
	if !exists {
		counter = &atomic.Int64{}
		rt.metrics.activeByType[resType] = counter
	}
	counter.Add(1)
}

// Release removes a resource from tracking (normal close).
func (rt *ResourceTracker) Release(resource TrackedResource) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	id := resource.ID()
	if _, exists := rt.resources[id]; !exists {
		return
	}

	delete(rt.resources, id)
	rt.metrics.totalReleased.Add(1)
	rt.decrementTypeCount(resource.Type())
}

func (rt *ResourceTracker) decrementTypeCount(resType ResourceType) {
	rt.metrics.typeMu.Lock()
	defer rt.metrics.typeMu.Unlock()

	if counter, exists := rt.metrics.activeByType[resType]; exists {
		counter.Add(-1)
	}
}

// ForceCloseAll force closes all tracked resources and returns any errors.
func (rt *ResourceTracker) ForceCloseAll() []error {
	rt.mu.Lock()
	resources := rt.copyResources()
	rt.mu.Unlock()

	return rt.forceCloseResources(resources, true)
}

func (rt *ResourceTracker) copyResources() []TrackedResource {
	resources := make([]TrackedResource, 0, len(rt.resources))
	for _, res := range rt.resources {
		resources = append(resources, res)
	}
	return resources
}

func (rt *ResourceTracker) forceCloseResources(resources []TrackedResource, clearAll bool) []error {
	var errs []error
	for _, res := range resources {
		if err := rt.forceCloseResource(res, clearAll); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (rt *ResourceTracker) forceCloseResource(res TrackedResource, clearFromMap bool) error {
	if res.IsClosed() {
		rt.removeAndUpdateMetrics(res, clearFromMap)
		return nil
	}

	err := res.ForceClose()
	rt.removeAndUpdateMetrics(res, clearFromMap)
	rt.metrics.totalForceClosed.Add(1)

	if err != nil {
		return fmt.Errorf("force close %s: %w", res.ID(), err)
	}
	return nil
}

func (rt *ResourceTracker) removeAndUpdateMetrics(res TrackedResource, clearFromMap bool) {
	if clearFromMap {
		rt.mu.Lock()
		delete(rt.resources, res.ID())
		rt.mu.Unlock()
	}
	rt.decrementTypeCount(res.Type())
}

// ForceCloseByType force closes all resources of the specified type.
func (rt *ResourceTracker) ForceCloseByType(resourceType ResourceType) []error {
	rt.mu.Lock()
	resources := rt.filterResourcesByType(resourceType)
	rt.mu.Unlock()

	return rt.forceCloseResources(resources, true)
}

func (rt *ResourceTracker) filterResourcesByType(resourceType ResourceType) []TrackedResource {
	var resources []TrackedResource
	for _, res := range rt.resources {
		if res.Type() == resourceType {
			resources = append(resources, res)
		}
	}
	return resources
}

// Count returns the number of currently tracked resources.
func (rt *ResourceTracker) Count() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.resources)
}

// CountByType returns the count of resources of the specified type.
func (rt *ResourceTracker) CountByType(resourceType ResourceType) int {
	rt.metrics.typeMu.RLock()
	defer rt.metrics.typeMu.RUnlock()

	if counter, exists := rt.metrics.activeByType[resourceType]; exists {
		return int(counter.Load())
	}
	return 0
}

// GetMetrics returns a snapshot of the current metrics.
func (rt *ResourceTracker) GetMetrics() ResourceMetrics {
	activeByType := rt.copyActiveByType()

	return ResourceMetrics{
		TotalTracked:     rt.metrics.totalTracked.Load(),
		TotalReleased:    rt.metrics.totalReleased.Load(),
		TotalForceClosed: rt.metrics.totalForceClosed.Load(),
		ActiveByType:     activeByType,
	}
}

func (rt *ResourceTracker) copyActiveByType() map[ResourceType]int64 {
	rt.metrics.typeMu.RLock()
	defer rt.metrics.typeMu.RUnlock()

	activeByType := make(map[ResourceType]int64)
	for resType, counter := range rt.metrics.activeByType {
		activeByType[resType] = counter.Load()
	}
	return activeByType
}

// List returns a copy of all tracked resources.
func (rt *ResourceTracker) List() []TrackedResource {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.copyResources()
}

// AgentID returns the agent ID associated with this tracker.
func (rt *ResourceTracker) AgentID() string {
	return rt.agentID
}
