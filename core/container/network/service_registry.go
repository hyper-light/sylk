package network

import (
	"sort"
	"sync"
	"time"
)

// ServiceEndpoint represents a reachable agent endpoint.
type ServiceEndpoint struct {
	AgentID   string
	AgentType string
	Ready     bool
	Healthy   bool
	Labels    map[string]string
	LastSeen  time.Time

	// Quality is the GP-predicted quality score in [0,1]. 0 = unknown.
	Quality float64
	// StdDev is the GP uncertainty (standard deviation of the prediction).
	StdDev float64
	// Weight is the routing weight during overlap in [0,1]. 1.0 = normal.
	Weight float64
}

// ServiceRegistry maintains a health-aware directory of agent endpoints.
// The Guide (service mesh) queries this to route traffic to healthy agents.
type ServiceRegistry struct {
	mu        sync.RWMutex
	endpoints map[string]*ServiceEndpoint   // agentID → endpoint
	byType    map[string]map[string]struct{} // agentType → set of agentIDs
}

// NewServiceRegistry creates an empty service registry.
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		endpoints: make(map[string]*ServiceEndpoint),
		byType:    make(map[string]map[string]struct{}),
	}
}

// Register adds or updates an endpoint. Thread-safe.
func (sr *ServiceRegistry) Register(ep ServiceEndpoint) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	ep.LastSeen = time.Now()
	if ep.Weight == 0 {
		ep.Weight = 1.0
	}
	sr.endpoints[ep.AgentID] = &ep
	sr.indexType(ep.AgentID, ep.AgentType)
}

func (sr *ServiceRegistry) indexType(agentID, agentType string) {
	if sr.byType[agentType] == nil {
		sr.byType[agentType] = make(map[string]struct{})
	}
	sr.byType[agentType][agentID] = struct{}{}
}

// Deregister removes an endpoint. Thread-safe.
func (sr *ServiceRegistry) Deregister(agentID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	ep, exists := sr.endpoints[agentID]
	if !exists {
		return
	}
	delete(sr.endpoints, agentID)
	sr.removeTypeIndex(agentID, ep.AgentType)
}

func (sr *ServiceRegistry) removeTypeIndex(agentID, agentType string) {
	typeSet := sr.byType[agentType]
	if typeSet == nil {
		return
	}
	delete(typeSet, agentID)
	if len(typeSet) == 0 {
		delete(sr.byType, agentType)
	}
}

// UpdateHealth updates the health/ready status of an endpoint.
func (sr *ServiceRegistry) UpdateHealth(agentID string, healthy, ready bool) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	ep, exists := sr.endpoints[agentID]
	if !exists {
		return
	}
	ep.Healthy = healthy
	ep.Ready = ready
	ep.LastSeen = time.Now()
}

// GetEndpoint returns the endpoint for the given agent, if registered.
func (sr *ServiceRegistry) GetEndpoint(agentID string) (*ServiceEndpoint, bool) {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	ep, ok := sr.endpoints[agentID]
	if !ok {
		return nil, false
	}
	cp := *ep
	return &cp, true
}

// GetHealthyEndpoints returns all healthy and ready endpoints of the given type.
func (sr *ServiceRegistry) GetHealthyEndpoints(agentType string) []ServiceEndpoint {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	ids := sr.byType[agentType]
	result := make([]ServiceEndpoint, 0, len(ids))
	for id := range ids {
		ep := sr.endpoints[id]
		if ep != nil && ep.Healthy && ep.Ready {
			result = append(result, *ep)
		}
	}
	return result
}

// HasHealthyEndpoints reports whether at least one healthy+ready endpoint
// exists for the given agent type. Avoids allocating a slice when only
// a boolean check is needed.
func (sr *ServiceRegistry) HasHealthyEndpoints(agentType string) bool {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	for id := range sr.byType[agentType] {
		ep := sr.endpoints[id]
		if ep != nil && ep.Healthy && ep.Ready {
			return true
		}
	}
	return false
}

// GetEndpointsByType returns all endpoints of the given type regardless of health.
func (sr *ServiceRegistry) GetEndpointsByType(agentType string) []ServiceEndpoint {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	ids := sr.byType[agentType]
	result := make([]ServiceEndpoint, 0, len(ids))
	for id := range ids {
		ep := sr.endpoints[id]
		if ep != nil {
			result = append(result, *ep)
		}
	}
	return result
}

// Count returns the total number of registered endpoints.
func (sr *ServiceRegistry) Count() int {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	return len(sr.endpoints)
}

// HealthyCount returns the number of healthy+ready endpoints.
func (sr *ServiceRegistry) HealthyCount() int {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	count := 0
	for _, ep := range sr.endpoints {
		if ep.Healthy && ep.Ready {
			count++
		}
	}
	return count
}

// UpdateQuality sets the GP-predicted quality and uncertainty for an endpoint.
func (sr *ServiceRegistry) UpdateQuality(agentID string, quality, stdDev float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	ep, exists := sr.endpoints[agentID]
	if !exists {
		return
	}
	ep.Quality = quality
	ep.StdDev = stdDev
	ep.LastSeen = time.Now()
}

// UpdateWeight sets the routing weight for an endpoint during overlap.
func (sr *ServiceRegistry) UpdateWeight(agentID string, weight float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	ep, exists := sr.endpoints[agentID]
	if !exists {
		return
	}
	ep.Weight = weight
	ep.LastSeen = time.Now()
}

// GetWeightedEndpoints returns healthy+ready endpoints for the given type,
// sorted by Weight descending. Used by the Guide for quality-aware routing.
func (sr *ServiceRegistry) GetWeightedEndpoints(agentType string) []ServiceEndpoint {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	ids := sr.byType[agentType]
	result := make([]ServiceEndpoint, 0, len(ids))
	for id := range ids {
		ep := sr.endpoints[id]
		if ep != nil && ep.Healthy && ep.Ready {
			result = append(result, *ep)
		}
	}

	// Sort by Weight descending for predictable ordering.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Weight > result[j].Weight
	})

	return result
}
