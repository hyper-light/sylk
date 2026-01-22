// Package stitch implements efficient graph merging for Vamana indices.
// It enables combining independently-built graphs (e.g., from large batch ingests)
// without full index rebuild by connecting boundary nodes between graphs.
//
// Key insight: Graph navigability means you don't need all nodes connected across
// segments - just enough boundary nodes for search to "discover" the other graph.
// Cost is O(√N) searches instead of O(N).
package stitch

import (
	"math"
	"math/rand/v2"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

// BoundaryStrategy defines how boundary nodes are selected for stitching.
type BoundaryStrategy int

const (
	// StrategyRandom selects nodes uniformly at random.
	// Fast O(k) but may miss important hub nodes.
	StrategyRandom BoundaryStrategy = iota

	// StrategyHighDegree selects nodes with the most neighbors.
	// Hub nodes make good bridges. O(N) scan required.
	StrategyHighDegree

	// StrategyHybrid combines random and high-degree selection.
	// Half random, half highest-degree nodes.
	StrategyHybrid
)

// BoundaryConfig configures boundary node sampling.
type BoundaryConfig struct {
	// Strategy determines how boundary nodes are selected.
	Strategy BoundaryStrategy

	// K is the number of boundary nodes to sample.
	// If 0, defaults to √N where N is graph size.
	K int

	// Seed for random sampling reproducibility.
	// If 0, uses a random seed.
	Seed uint64
}

// DefaultBoundaryConfig returns sensible defaults for boundary sampling.
func DefaultBoundaryConfig() BoundaryConfig {
	return BoundaryConfig{
		Strategy: StrategyHybrid,
		K:        0, // Will use √N
		Seed:     0, // Random seed
	}
}

// SampleBoundary selects representative boundary nodes from a graph.
// These nodes will be connected to another graph during stitching.
//
// The number of nodes returned is min(k, graphSize).
// If config.K is 0, k defaults to √N.
func SampleBoundary(graph *storage.GraphStore, config BoundaryConfig) []uint32 {
	n := int(graph.Count())
	if n == 0 {
		return nil
	}

	k := config.K
	if k <= 0 {
		k = int(math.Sqrt(float64(n)))
		if k < 1 {
			k = 1
		}
	}
	if k > n {
		k = n
	}

	switch config.Strategy {
	case StrategyRandom:
		return sampleRandom(graph, k, config.Seed)
	case StrategyHighDegree:
		return sampleHighDegree(graph, k)
	case StrategyHybrid:
		return sampleHybrid(graph, k, config.Seed)
	default:
		return sampleRandom(graph, k, config.Seed)
	}
}

// sampleRandom selects k nodes uniformly at random using Fisher-Yates.
func sampleRandom(graph *storage.GraphStore, k int, seed uint64) []uint32 {
	n := int(graph.Count())
	if k >= n {
		result := make([]uint32, n)
		for i := range n {
			result[i] = uint32(i)
		}
		return result
	}

	var rng *rand.Rand
	if seed != 0 {
		rng = rand.New(rand.NewPCG(seed, seed^0xDEADBEEF))
	} else {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}

	// Fisher-Yates partial shuffle
	indices := make([]uint32, n)
	for i := range n {
		indices[i] = uint32(i)
	}

	for i := range k {
		j := i + rng.IntN(n-i)
		indices[i], indices[j] = indices[j], indices[i]
	}

	return indices[:k]
}

// sampleHighDegree selects the k nodes with highest degree (most neighbors).
// Hub nodes tend to be well-connected and make good bridges.
func sampleHighDegree(graph *storage.GraphStore, k int) []uint32 {
	n := int(graph.Count())

	type nodeDegree struct {
		id     uint32
		degree int
	}

	// Collect all degrees
	degrees := make([]nodeDegree, n)
	for i := range n {
		neighbors := graph.GetNeighbors(uint32(i))
		degrees[i] = nodeDegree{id: uint32(i), degree: len(neighbors)}
	}

	// Partial sort to find top k (using selection)
	// For small k, this is faster than full sort
	for i := range k {
		maxIdx := i
		for j := i + 1; j < n; j++ {
			if degrees[j].degree > degrees[maxIdx].degree {
				maxIdx = j
			}
		}
		degrees[i], degrees[maxIdx] = degrees[maxIdx], degrees[i]
	}

	result := make([]uint32, k)
	for i := range k {
		result[i] = degrees[i].id
	}
	return result
}

// sampleHybrid selects half high-degree nodes and half random nodes.
// Combines coverage guarantees of hubs with diversity of random sampling.
func sampleHybrid(graph *storage.GraphStore, k int, seed uint64) []uint32 {
	if k <= 1 {
		return sampleHighDegree(graph, k)
	}

	halfK := k / 2
	otherHalf := k - halfK

	// Get high-degree nodes
	highDegree := sampleHighDegree(graph, halfK)

	// Build set of already-selected nodes
	selected := make(map[uint32]bool, halfK)
	for _, id := range highDegree {
		selected[id] = true
	}

	// Random sample from remaining nodes
	n := int(graph.Count())
	remaining := make([]uint32, 0, n-halfK)
	for i := range n {
		if !selected[uint32(i)] {
			remaining = append(remaining, uint32(i))
		}
	}

	var rng *rand.Rand
	if seed != 0 {
		rng = rand.New(rand.NewPCG(seed, seed^0xDEADBEEF))
	} else {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}

	// Fisher-Yates on remaining
	for i := range min(otherHalf, len(remaining)) {
		j := i + rng.IntN(len(remaining)-i)
		remaining[i], remaining[j] = remaining[j], remaining[i]
	}

	// Combine results
	result := make([]uint32, 0, k)
	result = append(result, highDegree...)
	result = append(result, remaining[:min(otherHalf, len(remaining))]...)

	return result
}
