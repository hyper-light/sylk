package container

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/benbjohnson/immutable"
)

var (
	ErrContainerNotFound = errors.New("container not found")
	ErrContainerExists   = errors.New("container already registered")
	ErrPodNotFound       = errors.New("pod not found")
)

// registrySnapshot is the immutable view of the registry. Reads load
// the current snapshot pointer atomically and traverse it without any
// lock; writers build a new snapshot off the current one (HAMT
// structural sharing reuses every untouched subtree) and publish it
// with a single atomic.Pointer.Store.
//
// All four indexes are immutable.Map: O(log N) Set/Delete, O(log N)
// Get, no full-map clone on mutation. The byType and byPod indexes
// hold inner immutable.Map[ContainerID, struct{}] sets so adding or
// removing a container from a type/pod bucket also avoids cloning the
// whole bucket.
type registrySnapshot struct {
	byID   *immutable.Map[ContainerID, *Container]
	byType *immutable.Map[string, *immutable.Map[ContainerID, struct{}]]
	byPod  *immutable.Map[PodID, *immutable.Map[ContainerID, struct{}]]
	pods   *immutable.Map[PodID, struct{}]
}

func newEmptyRegistrySnapshot() *registrySnapshot {
	return &registrySnapshot{
		byID:   immutable.NewMap[ContainerID, *Container](nil),
		byType: immutable.NewMap[string, *immutable.Map[ContainerID, struct{}]](nil),
		byPod:  immutable.NewMap[PodID, *immutable.Map[ContainerID, struct{}]](nil),
		pods:   immutable.NewMap[PodID, struct{}](nil),
	}
}

// ContainerRegistry provides thread-safe lookup of active containers.
// Reads (Get/ListByType/ListByPod/Count/All/HasPod/PodCount) are
// lock-free: load the snapshot pointer once, traverse the immutable
// index. Writes serialize on writeMu so only one mutator builds the
// next snapshot at a time; concurrent readers that loaded the prior
// snapshot continue to see a consistent view until they finish.
type ContainerRegistry struct {
	writeMu sync.Mutex
	state   atomic.Pointer[registrySnapshot]
}

// NewContainerRegistry creates an empty registry.
func NewContainerRegistry() *ContainerRegistry {
	r := &ContainerRegistry{}
	r.state.Store(newEmptyRegistrySnapshot())
	return r
}

// Register adds a container to the registry. Returns ErrContainerExists
// if a container with the same ID is already registered.
func (r *ContainerRegistry) Register(c *Container) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	cur := r.state.Load()
	if _, exists := cur.byID.Get(c.id); exists {
		return ErrContainerExists
	}

	next := &registrySnapshot{
		byID:   cur.byID.Set(c.id, c),
		byType: addToBucket(cur.byType, c.spec.AgentType, c.id),
		byPod:  cur.byPod,
		pods:   cur.pods,
	}
	if c.podID != "" {
		next.byPod = addToBucket(cur.byPod, c.podID, c.id)
	}
	r.state.Store(next)
	return nil
}

// Unregister removes a container from the registry. No-op if the
// container ID is not present.
func (r *ContainerRegistry) Unregister(id ContainerID) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	cur := r.state.Load()
	c, exists := cur.byID.Get(id)
	if !exists {
		return
	}

	next := &registrySnapshot{
		byID:   cur.byID.Delete(id),
		byType: removeFromBucket(cur.byType, c.spec.AgentType, c.id),
		byPod:  cur.byPod,
		pods:   cur.pods,
	}
	if c.podID != "" {
		next.byPod = removeFromBucket(cur.byPod, c.podID, c.id)
	}
	r.state.Store(next)
}

// Get returns the container with the given ID, or ErrContainerNotFound.
func (r *ContainerRegistry) Get(id ContainerID) (*Container, error) {
	snap := r.state.Load()
	c, ok := snap.byID.Get(id)
	if !ok {
		return nil, ErrContainerNotFound
	}
	return c, nil
}

// ListByType returns all containers of the given agent type.
func (r *ContainerRegistry) ListByType(agentType string) []*Container {
	snap := r.state.Load()
	bucket, ok := snap.byType.Get(agentType)
	if !ok {
		return nil
	}
	return collectFromBucket(snap, bucket)
}

// ListByPod returns all containers belonging to the given pod.
func (r *ContainerRegistry) ListByPod(podID PodID) []*Container {
	snap := r.state.Load()
	bucket, ok := snap.byPod.Get(podID)
	if !ok {
		return nil
	}
	return collectFromBucket(snap, bucket)
}

// Count returns the total number of registered containers.
func (r *ContainerRegistry) Count() int {
	return r.state.Load().byID.Len()
}

// All returns a snapshot of all registered containers.
func (r *ContainerRegistry) All() []*Container {
	snap := r.state.Load()
	out := make([]*Container, 0, snap.byID.Len())
	iter := snap.byID.Iterator()
	for !iter.Done() {
		_, c, _ := iter.Next()
		out = append(out, c)
	}
	return out
}

// RegisterPod records a pod ID in the registry.
func (r *ContainerRegistry) RegisterPod(id PodID) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	cur := r.state.Load()
	next := *cur
	next.pods = cur.pods.Set(id, struct{}{})
	r.state.Store(&next)
}

// UnregisterPod removes a pod ID from the registry.
func (r *ContainerRegistry) UnregisterPod(id PodID) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	cur := r.state.Load()
	next := *cur
	next.pods = cur.pods.Delete(id)
	r.state.Store(&next)
}

// HasPod returns true if the pod ID is registered.
func (r *ContainerRegistry) HasPod(id PodID) bool {
	_, ok := r.state.Load().pods.Get(id)
	return ok
}

// PodCount returns the number of registered pods.
func (r *ContainerRegistry) PodCount() int {
	return r.state.Load().pods.Len()
}

// addToBucket returns a new index map with id added to the bucket
// keyed by k. The bucket is created (as an empty inner map) if it
// does not yet exist. Both the outer and inner maps share structure
// with their predecessors.
func addToBucket[K comparable](idx *immutable.Map[K, *immutable.Map[ContainerID, struct{}]], k K, id ContainerID) *immutable.Map[K, *immutable.Map[ContainerID, struct{}]] {
	bucket, ok := idx.Get(k)
	if !ok {
		bucket = immutable.NewMap[ContainerID, struct{}](nil)
	}
	bucket = bucket.Set(id, struct{}{})
	return idx.Set(k, bucket)
}

// removeFromBucket returns a new index map with id removed from the
// bucket keyed by k. If the bucket becomes empty after removal, the
// bucket entry itself is deleted from the outer map so iteration over
// agent types / pods stays cheap.
func removeFromBucket[K comparable](idx *immutable.Map[K, *immutable.Map[ContainerID, struct{}]], k K, id ContainerID) *immutable.Map[K, *immutable.Map[ContainerID, struct{}]] {
	bucket, ok := idx.Get(k)
	if !ok {
		return idx
	}
	bucket = bucket.Delete(id)
	if bucket.Len() == 0 {
		return idx.Delete(k)
	}
	return idx.Set(k, bucket)
}

// collectFromBucket reads container pointers out of the snapshot's
// byID map for every entry in bucket. Reading from the same snapshot
// that produced the bucket guarantees a consistent view: a container
// observed in the bucket is also observable in byID.
func collectFromBucket(snap *registrySnapshot, bucket *immutable.Map[ContainerID, struct{}]) []*Container {
	out := make([]*Container, 0, bucket.Len())
	iter := bucket.Iterator()
	for !iter.Done() {
		id, _, _ := iter.Next()
		if c, ok := snap.byID.Get(id); ok {
			out = append(out, c)
		}
	}
	return out
}
