package hnsw

import (
	"sync"
	"testing"
)

// TestW12_38_LayerConnectionRace verifies that concurrent layer operations
// do not cause race conditions. This test must be run with -race flag.
// W12.38: Tests the fix for layer connection race conditions.
func TestW12_38_LayerConnectionRace(t *testing.T) {
	l := newLayer()

	for i := 0; i < 10; i++ {
		l.addNode(uint32(i))
	}

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeID := uint32(i % 10)
			neighborID := uint32((i + 1) % 10)
			l.addNeighbor(nodeID, neighborID, float32(i)*0.01, 20)
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeID := uint32(i % 10)
			_ = l.getNeighbors(nodeID)
		}(i)
	}

	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeID := uint32(i % 10)
			neighborID := uint32((i + 2) % 10)
			l.removeNeighbor(nodeID, neighborID)
		}(i)
	}

	wg.Wait()
}

// TestW12_38_LayerSetNeighborsRace verifies that setNeighbors is race-free.
// W12.38: Tests concurrent setNeighbors operations.
func TestW12_38_LayerSetNeighborsRace(t *testing.T) {
	l := newLayer()

	for i := 0; i < 5; i++ {
		l.addNode(uint32(i))
	}

	var wg sync.WaitGroup
	numGoroutines := 30

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeID := uint32(i % 5)
			neighbors := []uint32{uint32((i + 1) % 5), uint32((i + 2) % 5)}
			distances := []float32{float32(i) * 0.1, float32(i) * 0.2}
			l.setNeighbors(nodeID, neighbors, distances)
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeID := uint32(i % 5)
			_ = l.getNeighborsWithDistances(nodeID)
		}(i)
	}

	wg.Wait()
}

// TestW12_38_LayerAddRemoveNodeRace verifies concurrent node add/remove with
// neighbor operations is race-free.
// W12.38: Tests the most challenging race scenario.
func TestW12_38_LayerAddRemoveNodeRace(t *testing.T) {
	l := newLayer()

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.addNode(uint32(i))
		}(i)
	}

	wg.Wait()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(3)

		go func(i int) {
			defer wg.Done()
			nodeID := uint32(i % numGoroutines)
			neighborID := uint32((i + 1) % numGoroutines)
			l.addNeighbor(nodeID, neighborID, float32(i)*0.01, 20)
		}(i)

		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				l.removeNode(uint32(i))
			}
		}(i)

		go func(i int) {
			defer wg.Done()
			nodeID := uint32(i % numGoroutines)
			_ = l.getNeighbors(nodeID)
		}(i)
	}

	wg.Wait()
}

// TestW12_38_ConcurrentFindNodesPointingTo verifies findNodesPointingTo is race-free.
// W12.38: Tests concurrent reverse lookups.
func TestW12_38_ConcurrentFindNodesPointingTo(t *testing.T) {
	l := newLayer()

	for i := 0; i < 20; i++ {
		l.addNode(uint32(i))
	}
	for i := 0; i < 20; i++ {
		for j := 0; j < 3; j++ {
			neighborIdx := (i + j + 1) % 20
			l.addNeighbor(uint32(i), uint32(neighborIdx), 0.1, 10)
		}
	}

	var wg sync.WaitGroup
	numGoroutines := 30

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			targetID := uint32(i % 20)
			_ = l.findNodesPointingTo(targetID)
		}(i)
	}

	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeID := uint32(i % 20)
			neighborID := uint32((i + 5) % 20)
			l.addNeighbor(nodeID, neighborID, float32(i)*0.05, 10)
		}(i)
	}

	wg.Wait()
}
