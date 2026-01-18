package recovery

import (
	"sync"
	"time"
)

type DeadlockType int

const (
	DeadlockNone DeadlockType = iota
	DeadlockCircular
	DeadlockDeadHolder
)

type WaitEdge struct {
	WaiterID     string
	HolderID     string
	ResourceType string
	ResourceID   string
	WaitingSince time.Time
}

type DeadlockResult struct {
	Detected      bool
	Type          DeadlockType
	Cycle         []string
	DeadHolder    string
	WaitingAgents []string
	ResourceType  string
	ResourceID    string
}

type AgentStatusFunc func(agentID string) bool

type DeadlockDetector struct {
	waitGraph   map[string][]WaitEdge
	agentStatus AgentStatusFunc
	mu          sync.RWMutex
}

func NewDeadlockDetector(statusFn AgentStatusFunc) *DeadlockDetector {
	return &DeadlockDetector{
		waitGraph:   make(map[string][]WaitEdge),
		agentStatus: statusFn,
	}
}

func (d *DeadlockDetector) RegisterWait(waiter, holder, resourceType, resourceID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.waitGraph[waiter] = append(d.waitGraph[waiter], WaitEdge{
		WaiterID:     waiter,
		HolderID:     holder,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		WaitingSince: time.Now(),
	})
}

func (d *DeadlockDetector) ClearWait(waiter, resourceID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	edges := d.waitGraph[waiter]
	filtered := edges[:0]
	for _, e := range edges {
		if e.ResourceID != resourceID {
			filtered = append(filtered, e)
		}
	}
	d.waitGraph[waiter] = filtered
}

func (d *DeadlockDetector) Check() []DeadlockResult {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var results []DeadlockResult
	results = append(results, d.findCircularDeadlocks()...)
	results = append(results, d.findDeadHolderDeadlocks()...)
	return results
}

func (d *DeadlockDetector) findCircularDeadlocks() []DeadlockResult {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	var results []DeadlockResult

	for agentID := range d.waitGraph {
		cycle := d.findCycleDFS(agentID, visited, recStack, nil)
		if len(cycle) > 0 {
			results = append(results, DeadlockResult{
				Detected: true,
				Type:     DeadlockCircular,
				Cycle:    cycle,
			})
		}
	}
	return results
}

func (d *DeadlockDetector) findDeadHolderDeadlocks() []DeadlockResult {
	var results []DeadlockResult

	for waiter, edges := range d.waitGraph {
		for _, edge := range edges {
			if !d.agentStatus(edge.HolderID) {
				results = append(results, DeadlockResult{
					Detected:      true,
					Type:          DeadlockDeadHolder,
					DeadHolder:    edge.HolderID,
					WaitingAgents: []string{waiter},
					ResourceType:  edge.ResourceType,
					ResourceID:    edge.ResourceID,
				})
			}
		}
	}
	return results
}

func (d *DeadlockDetector) findCycleDFS(node string, visited, recStack map[string]bool, path []string) []string {
	if recStack[node] {
		return d.extractCycle(node, path)
	}
	if visited[node] {
		return nil
	}

	visited[node] = true
	recStack[node] = true
	path = append(path, node)

	cycle := d.searchEdgesForCycle(node, visited, recStack, path)
	recStack[node] = false
	return cycle
}

func (d *DeadlockDetector) searchEdgesForCycle(node string, visited, recStack map[string]bool, path []string) []string {
	for _, edge := range d.waitGraph[node] {
		if cycle := d.findCycleDFS(edge.HolderID, visited, recStack, path); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

func (d *DeadlockDetector) extractCycle(node string, path []string) []string {
	cycleStart := -1
	for i, n := range path {
		if n == node {
			cycleStart = i
			break
		}
	}
	if cycleStart >= 0 {
		return append(path[cycleStart:], node)
	}
	return nil
}

func (d *DeadlockDetector) ClearAgent(agentID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.waitGraph, agentID)
}
