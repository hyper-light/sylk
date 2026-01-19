package context

import (
	"github.com/adalundhe/sylk/core/knowledge/coldstart"
)

// MD.9.6: ColdStartBuilder

// ColdStartBuilder accumulates graph signals during indexing to compute
// cold-start priors for nodes before ACT-R parameters are learned.
type ColdStartBuilder struct {
	nodeSignals  map[string]*coldstart.NodeColdStartSignals
	entityCounts map[string]int
	domainCounts map[int]int
	totalNodes   int

	inDegrees  map[string]int
	outDegrees map[string]int
	edges      []coldstart.Edge
}

// NewColdStartBuilder creates a new ColdStartBuilder instance.
func NewColdStartBuilder() *ColdStartBuilder {
	return &ColdStartBuilder{
		nodeSignals:  make(map[string]*coldstart.NodeColdStartSignals),
		entityCounts: make(map[string]int),
		domainCounts: make(map[int]int),
		inDegrees:    make(map[string]int),
		outDegrees:   make(map[string]int),
		edges:        make([]coldstart.Edge, 0),
	}
}

// AddNode registers a node and accumulates its entity type and domain.
// ACCEPTANCE: Signals accumulated correctly during indexing
func (b *ColdStartBuilder) AddNode(nodeID, entityType string, domain int) {
	if _, exists := b.nodeSignals[nodeID]; !exists {
		b.nodeSignals[nodeID] = &coldstart.NodeColdStartSignals{
			NodeID:     nodeID,
			EntityType: entityType,
		}
		b.totalNodes++
	}

	b.entityCounts[entityType]++
	b.domainCounts[domain]++
}

// AddEdge registers an edge and accumulates in/out degree counts.
func (b *ColdStartBuilder) AddEdge(sourceID, targetID string) {
	b.edges = append(b.edges, coldstart.Edge{
		SourceID: sourceID,
		TargetID: targetID,
	})
	b.outDegrees[sourceID]++
	b.inDegrees[targetID]++
}

// GetSignals returns the accumulated signals for a node.
func (b *ColdStartBuilder) GetSignals(nodeID string) *coldstart.NodeColdStartSignals {
	return b.nodeSignals[nodeID]
}

// BuildProfile computes the final CodebaseProfile with all normalized signals.
func (b *ColdStartBuilder) BuildProfile() *coldstart.CodebaseProfile {
	b.computeDegrees()
	b.computePageRank()
	b.computeClustering()
	b.computeTypeSignals()

	return b.createProfile()
}

func (b *ColdStartBuilder) computeDegrees() {
	for nodeID, signals := range b.nodeSignals {
		signals.InDegree = b.inDegrees[nodeID]
		signals.OutDegree = b.outDegrees[nodeID]
	}
}

func (b *ColdStartBuilder) computePageRank() {
	graph := b.buildGraph()
	ranks := coldstart.ComputePageRank(graph, 0.85, 100)

	for nodeID, rank := range ranks {
		if signals, exists := b.nodeSignals[nodeID]; exists {
			signals.PageRank = rank
		}
	}
}

func (b *ColdStartBuilder) buildGraph() map[string][]string {
	graph := make(map[string][]string)
	for _, edge := range b.edges {
		graph[edge.SourceID] = append(graph[edge.SourceID], edge.TargetID)
	}
	return graph
}

func (b *ColdStartBuilder) computeClustering() {
	graph := b.buildGraph()
	coeffs := coldstart.ComputeClusteringCoefficients(graph)

	for nodeID, coeff := range coeffs {
		if signals, exists := b.nodeSignals[nodeID]; exists {
			signals.ClusterCoeff = coeff
		}
	}
}

func (b *ColdStartBuilder) computeTypeSignals() {
	for _, signals := range b.nodeSignals {
		if b.totalNodes > 0 {
			count := b.entityCounts[signals.EntityType]
			signals.TypeFrequency = float64(count) / float64(b.totalNodes)
			signals.TypeRarity = 1.0 - signals.TypeFrequency
		}
	}
}

func (b *ColdStartBuilder) createProfile() *coldstart.CodebaseProfile {
	avgIn, avgOut := b.computeAverageDegrees()
	maxPR, sumPR := b.computePageRankStats()

	return &coldstart.CodebaseProfile{
		TotalNodes:     b.totalNodes,
		AvgInDegree:    avgIn,
		AvgOutDegree:   avgOut,
		MaxPageRank:    maxPR,
		PageRankSum:    sumPR,
		BetweennessMax: b.computeMaxBetweenness(),
		EntityCounts:   b.copyEntityCounts(),
		DomainCounts:   b.copyDomainCounts(),
	}
}

func (b *ColdStartBuilder) computeAverageDegrees() (float64, float64) {
	if b.totalNodes == 0 {
		return 0, 0
	}

	totalIn, totalOut := 0, 0
	for _, inDeg := range b.inDegrees {
		totalIn += inDeg
	}
	for _, outDeg := range b.outDegrees {
		totalOut += outDeg
	}

	return float64(totalIn) / float64(b.totalNodes),
		float64(totalOut) / float64(b.totalNodes)
}

func (b *ColdStartBuilder) computePageRankStats() (float64, float64) {
	maxPR, sumPR := 0.0, 0.0
	for _, signals := range b.nodeSignals {
		if signals.PageRank > maxPR {
			maxPR = signals.PageRank
		}
		sumPR += signals.PageRank
	}
	return maxPR, sumPR
}

func (b *ColdStartBuilder) computeMaxBetweenness() float64 {
	maxBetw := 0.0
	for _, signals := range b.nodeSignals {
		if signals.Betweenness > maxBetw {
			maxBetw = signals.Betweenness
		}
	}
	return maxBetw
}

func (b *ColdStartBuilder) copyEntityCounts() map[string]int {
	copy := make(map[string]int, len(b.entityCounts))
	for k, v := range b.entityCounts {
		copy[k] = v
	}
	return copy
}

func (b *ColdStartBuilder) copyDomainCounts() map[int]int {
	copy := make(map[int]int, len(b.domainCounts))
	for k, v := range b.domainCounts {
		copy[k] = v
	}
	return copy
}
