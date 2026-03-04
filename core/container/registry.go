package container

import (
	"errors"
	"sync"
)

var (
	ErrContainerNotFound  = errors.New("container not found")
	ErrContainerExists    = errors.New("container already registered")
	ErrPodNotFound = errors.New("pod not found")
)

// ContainerRegistry provides thread-safe lookup of active containers.
// Containers are indexed by ID, agent type, and pod ID for efficient queries.
type ContainerRegistry struct {
	mu        sync.RWMutex
	byID      map[ContainerID]*Container
	byType    map[string]map[ContainerID]struct{} // agentType → set of IDs
	byPod     map[PodID]map[ContainerID]struct{}  // podID → set of IDs
	pods      map[PodID]struct{}
}

// NewContainerRegistry creates an empty registry.
func NewContainerRegistry() *ContainerRegistry {
	return &ContainerRegistry{
		byID:   make(map[ContainerID]*Container),
		byType: make(map[string]map[ContainerID]struct{}),
		byPod:  make(map[PodID]map[ContainerID]struct{}),
		pods:   make(map[PodID]struct{}),
	}
}

// Register adds a container to the registry. Returns ErrContainerExists
// if a container with the same ID is already registered.
func (r *ContainerRegistry) Register(c *Container) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[c.id]; exists {
		return ErrContainerExists
	}

	r.byID[c.id] = c
	r.indexByType(c)
	r.indexByPod(c)
	return nil
}

func (r *ContainerRegistry) indexByType(c *Container) {
	agentType := c.spec.AgentType
	if r.byType[agentType] == nil {
		r.byType[agentType] = make(map[ContainerID]struct{})
	}
	r.byType[agentType][c.id] = struct{}{}
}

func (r *ContainerRegistry) indexByPod(c *Container) {
	if c.podID == "" {
		return
	}
	if r.byPod[c.podID] == nil {
		r.byPod[c.podID] = make(map[ContainerID]struct{})
	}
	r.byPod[c.podID][c.id] = struct{}{}
}

// Unregister removes a container from the registry.
func (r *ContainerRegistry) Unregister(id ContainerID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	c, exists := r.byID[id]
	if !exists {
		return
	}

	delete(r.byID, id)
	r.removeTypeIndex(c)
	r.removePodIndex(c)
}

func (r *ContainerRegistry) removeTypeIndex(c *Container) {
	typeSet := r.byType[c.spec.AgentType]
	if typeSet == nil {
		return
	}
	delete(typeSet, c.id)
	if len(typeSet) == 0 {
		delete(r.byType, c.spec.AgentType)
	}
}

func (r *ContainerRegistry) removePodIndex(c *Container) {
	if c.podID == "" {
		return
	}
	podSet := r.byPod[c.podID]
	if podSet == nil {
		return
	}
	delete(podSet, c.id)
	if len(podSet) == 0 {
		delete(r.byPod, c.podID)
	}
}

// Get returns the container with the given ID, or ErrContainerNotFound.
func (r *ContainerRegistry) Get(id ContainerID) (*Container, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, ErrContainerNotFound
	}
	return c, nil
}

// ListByType returns all containers of the given agent type.
func (r *ContainerRegistry) ListByType(agentType string) []*Container {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.byType[agentType]
	result := make([]*Container, 0, len(ids))
	for id := range ids {
		if c, ok := r.byID[id]; ok {
			result = append(result, c)
		}
	}
	return result
}

// ListByPod returns all containers belonging to the given pod.
func (r *ContainerRegistry) ListByPod(podID PodID) []*Container {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.byPod[podID]
	result := make([]*Container, 0, len(ids))
	for id := range ids {
		if c, ok := r.byID[id]; ok {
			result = append(result, c)
		}
	}
	return result
}

// Count returns the total number of registered containers.
func (r *ContainerRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

// All returns a snapshot of all registered containers.
func (r *ContainerRegistry) All() []*Container {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*Container, 0, len(r.byID))
	for _, c := range r.byID {
		result = append(result, c)
	}
	return result
}

// RegisterPod records a pod ID in the registry.
func (r *ContainerRegistry) RegisterPod(id PodID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pods[id] = struct{}{}
}

// UnregisterPod removes a pod ID from the registry.
func (r *ContainerRegistry) UnregisterPod(id PodID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pods, id)
}

// HasPod returns true if the pod ID is registered.
func (r *ContainerRegistry) HasPod(id PodID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.pods[id]
	return ok
}

// PodCount returns the number of registered pods.
func (r *ContainerRegistry) PodCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.pods)
}
