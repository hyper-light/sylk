package coldstart

// =============================================================================
// Node Cold-Start Signals (MD.9.1)
// =============================================================================

// NodeColdStartSignals contains all signals used to compute cold-start priors
// for a node in the knowledge graph. These signals are populated during indexing
// and capture structural, content, and distributional characteristics.
type NodeColdStartSignals struct {
	// Node identification
	NodeID     string
	EntityType string
	Domain     int

	// Structural signals - graph topology metrics
	InDegree       int     // Number of incoming edges
	OutDegree      int     // Number of outgoing edges
	PageRank       float64 // Node importance in graph
	ClusterCoeff   float64 // Local clustering coefficient
	Betweenness    float64 // Betweenness centrality

	// Content signals - node intrinsic properties
	NameSalience float64 // Salience of entity name
	DocCoverage  float64 // Documentation coverage
	Complexity   float64 // Code complexity metric

	// Distributional signals - corpus-level statistics
	TypeFrequency float64 // Frequency of this entity type
	TypeRarity    float64 // Rarity score (inverse frequency)
}

// NewNodeColdStartSignals creates a new NodeColdStartSignals instance with
// the provided node identification information.
func NewNodeColdStartSignals(nodeID, entityType string, domain int) *NodeColdStartSignals {
	return &NodeColdStartSignals{
		NodeID:     nodeID,
		EntityType: entityType,
		Domain:     domain,
	}
}
