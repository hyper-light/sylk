package concurrency

import (
	"context"
	"sync"
	"time"
)

type PipelinePriority int

const (
	PriorityLow PipelinePriority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

type SchedulablePipeline struct {
	ID           string
	Priority     PipelinePriority
	SpawnTime    time.Time
	Dependencies []string
	Runner       *PipelineRunner
	cancel       context.CancelFunc // Set by scheduler when pipeline starts
}

type PipelinePriorityQueue struct {
	items []*SchedulablePipeline
	index map[string]int
	mu    sync.RWMutex
}

func NewPipelinePriorityQueue() *PipelinePriorityQueue {
	return &PipelinePriorityQueue{
		items: make([]*SchedulablePipeline, 0),
		index: make(map[string]int),
	}
}

func (pq *PipelinePriorityQueue) Push(p *SchedulablePipeline) {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	pq.items = append(pq.items, p)
	pq.index[p.ID] = len(pq.items) - 1
	pq.heapUp(len(pq.items) - 1)
}

func (pq *PipelinePriorityQueue) Pop() *SchedulablePipeline {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if len(pq.items) == 0 {
		return nil
	}

	top := pq.items[0]
	pq.removeAtIndex(0)
	return top
}

func (pq *PipelinePriorityQueue) Peek() *SchedulablePipeline {
	pq.mu.RLock()
	defer pq.mu.RUnlock()

	if len(pq.items) == 0 {
		return nil
	}
	return pq.items[0]
}

func (pq *PipelinePriorityQueue) Remove(id string) bool {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	idx, exists := pq.index[id]
	if !exists {
		return false
	}

	pq.removeAtIndex(idx)
	return true
}

func (pq *PipelinePriorityQueue) Len() int {
	pq.mu.RLock()
	defer pq.mu.RUnlock()
	return len(pq.items)
}

func (pq *PipelinePriorityQueue) Contains(id string) bool {
	pq.mu.RLock()
	defer pq.mu.RUnlock()
	_, exists := pq.index[id]
	return exists
}

func (pq *PipelinePriorityQueue) All() []*SchedulablePipeline {
	pq.mu.RLock()
	defer pq.mu.RUnlock()
	result := make([]*SchedulablePipeline, len(pq.items))
	copy(result, pq.items)
	return result
}

func (pq *PipelinePriorityQueue) removeAtIndex(idx int) {
	lastIdx := len(pq.items) - 1
	delete(pq.index, pq.items[idx].ID)

	if idx != lastIdx {
		pq.items[idx] = pq.items[lastIdx]
		pq.index[pq.items[idx].ID] = idx
	}

	pq.items = pq.items[:lastIdx]

	if idx < len(pq.items) {
		pq.heapDown(idx)
		pq.heapUp(idx)
	}
}

func (pq *PipelinePriorityQueue) heapUp(idx int) {
	for idx > 0 {
		parent := (idx - 1) / 2
		if !pq.less(idx, parent) {
			break
		}
		pq.swap(idx, parent)
		idx = parent
	}
}

func (pq *PipelinePriorityQueue) heapDown(idx int) {
	for {
		smallest := idx
		left := 2*idx + 1
		right := 2*idx + 2

		smallest = pq.findSmallest(smallest, left)
		smallest = pq.findSmallest(smallest, right)

		if smallest == idx {
			break
		}

		pq.swap(idx, smallest)
		idx = smallest
	}
}

func (pq *PipelinePriorityQueue) findSmallest(current, candidate int) int {
	if candidate < len(pq.items) && pq.less(candidate, current) {
		return candidate
	}
	return current
}

func (pq *PipelinePriorityQueue) less(i, j int) bool {
	if pq.items[i].Priority != pq.items[j].Priority {
		return pq.items[i].Priority > pq.items[j].Priority
	}
	return pq.items[i].SpawnTime.Before(pq.items[j].SpawnTime)
}

func (pq *PipelinePriorityQueue) swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.index[pq.items[i].ID] = i
	pq.index[pq.items[j].ID] = j
}
