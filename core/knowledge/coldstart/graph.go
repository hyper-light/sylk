package coldstart

import (
	"math"
)

// MD.9.7: PageRank & Centrality Computation

// Edge represents a directed edge in the graph.
type Edge struct {
	SourceID string
	TargetID string
}

// buildAdjacencyGraph constructs an adjacency list from edges.
func buildAdjacencyGraph(edges []Edge) map[string][]string {
	graph := make(map[string][]string)
	for _, edge := range edges {
		graph[edge.SourceID] = append(graph[edge.SourceID], edge.TargetID)
	}
	return graph
}

// ComputePageRank calculates PageRank scores using power iteration.
// Parameters: damping=0.85, maxIter=100
// ACCEPTANCE: PageRank converges, scores in [0,1]
func ComputePageRank(graph map[string][]string, damping float64, maxIter int) map[string]float64 {
	nodes := extractNodes(graph)
	n := len(nodes)
	if n == 0 {
		return make(map[string]float64)
	}

	ranks := initializeRanks(nodes, n)
	teleport := (1.0 - damping) / float64(n)

	for i := 0; i < maxIter; i++ {
		newRanks := computeIteration(graph, ranks, nodes, damping, teleport, n)
		if hasConverged(ranks, newRanks) {
			return newRanks
		}
		ranks = newRanks
	}

	return ranks
}

func extractNodes(graph map[string][]string) []string {
	nodeSet := make(map[string]bool)
	for src, targets := range graph {
		nodeSet[src] = true
		for _, tgt := range targets {
			nodeSet[tgt] = true
		}
	}
	nodes := make([]string, 0, len(nodeSet))
	for node := range nodeSet {
		nodes = append(nodes, node)
	}
	return nodes
}

func initializeRanks(nodes []string, n int) map[string]float64 {
	ranks := make(map[string]float64, len(nodes))
	initial := 1.0 / float64(n)
	for _, node := range nodes {
		ranks[node] = initial
	}
	return ranks
}

func computeIteration(graph map[string][]string, ranks map[string]float64, nodes []string, damping, teleport float64, n int) map[string]float64 {
	newRanks := make(map[string]float64, len(nodes))
	for _, node := range nodes {
		newRanks[node] = teleport
	}

	for src, targets := range graph {
		if len(targets) == 0 {
			continue
		}
		contribution := damping * ranks[src] / float64(len(targets))
		for _, tgt := range targets {
			newRanks[tgt] += contribution
		}
	}

	return newRanks
}

func hasConverged(old, new map[string]float64) bool {
	const epsilon = 1e-6
	for node, newVal := range new {
		if math.Abs(newVal-old[node]) > epsilon {
			return false
		}
	}
	return true
}

// ComputeClusteringCoefficients calculates clustering coefficient for each node.
// ACCEPTANCE: Clustering coefficients in [0,1]
func ComputeClusteringCoefficients(graph map[string][]string) map[string]float64 {
	coeffs := make(map[string]float64)

	for node, neighbors := range graph {
		if len(neighbors) < 2 {
			coeffs[node] = 0.0
			continue
		}
		coeffs[node] = calculateCoefficient(neighbors, graph)
	}

	return coeffs
}

func calculateCoefficient(neighbors []string, graph map[string][]string) float64 {
	k := len(neighbors)
	links := countLinks(neighbors, graph)
	maxLinks := k * (k - 1) / 2
	if maxLinks == 0 {
		return 0.0
	}
	return float64(links) / float64(maxLinks)
}

func countLinks(neighbors []string, graph map[string][]string) int {
	neighborSet := make(map[string]bool)
	for _, n := range neighbors {
		neighborSet[n] = true
	}

	links := 0
	for _, n1 := range neighbors {
		for _, n2 := range graph[n1] {
			if neighborSet[n2] {
				links++
			}
		}
	}
	// Divide by 2 because each undirected edge is counted twice
	return links / 2
}
