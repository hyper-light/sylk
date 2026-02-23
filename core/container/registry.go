package container

import (
	"errors"
	"sync"
)

var (
	ErrContainerNotFound  = errors.New("container not found")
	ErrContainerExists    = errors.New("container already registered")
	ErrPodNotFound        = errors.New("pod not found")
)

// ContainerRegistry provides thread-safe lookup of active containers.
// Containers are indexed by ID, agent type, and pod ID for efficient queries.
type ContainerRegistry struct {
	mu        sync.RWMutex
	byID      map[ContainerID]*Container
	byType    map[string]map[ContainerID]struct{} // agentType → set of IDs
	byPod     map[PodID]map[ContainerID]struct{}  // podID → set of IDs
	pods      map[PodID]*Pod
}

// NewContainerRegistry creates an empty registry.
func NewContainerRegistry() *ContainerRegistry {
	return &ContainerRegistry{
		byID:   make(map[ContainerID]*Container),
		byType: make(map[string]map[ContainerID]struct{}),
		byPod:  make(map[PodID]map[ContainerID]struct{}),
		pods:   make(map[PodID]*Pod),
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
	if c.pod == nil {
		return
	}
	podID := c.pod.id
	if r.byPod[podID] == nil {
		r.byPod[podID] = make(map[ContainerID]struct{})
	}
	r.byPod[podID][c.id] = struct{}{}
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
	if c.pod == nil {
		return
	}
	podSet := r.byPod[c.pod.id]
	if podSet == nil {
		return
	}
	delete(podSet, c.id)
	if len(podSet) == 0 {
		delete(r.byPod, c.pod.id)
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

// RegisterPod adds a pod to the registry.
func (r *ContainerRegistry) RegisterPod(p *Pod) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pods[p.id] = p
}

// UnregisterPod removes a pod from the registry.
func (r *ContainerRegistry) UnregisterPod(id PodID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pods, id)
}

// GetPod returns the pod with the given ID, or ErrPodNotFound.
func (r *ContainerRegistry) GetPod(id PodID) (*Pod, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pods[id]
	if !ok {
		return nil, ErrPodNotFound
	}
	return p, nil
}

// PodCount returns the number of registered pods.
func (r *ContainerRegistry) PodCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.pods)
}
