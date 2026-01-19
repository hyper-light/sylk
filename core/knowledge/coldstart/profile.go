package coldstart

// =============================================================================
// Codebase Profile (MD.9.2)
// =============================================================================

// CodebaseProfile captures aggregate statistics about the entire codebase,
// computed after indexing. Used to contextualize cold-start signals.
type CodebaseProfile struct {
	// Node counts
	TotalNodes int

	// Entity type distribution
	EntityCounts map[string]int

	// Domain distribution
	DomainCounts map[int]int

	// Structural statistics
	AvgInDegree   float64
	AvgOutDegree  float64
	MaxPageRank   float64
	PageRankSum   float64
	BetweennessMax float64
}

// NewCodebaseProfile creates a new CodebaseProfile with initialized maps.
func NewCodebaseProfile() *CodebaseProfile {
	return &CodebaseProfile{
		EntityCounts: make(map[string]int),
		DomainCounts: make(map[int]int),
	}
}

// AddEntity increments the count for the given entity type.
func (cp *CodebaseProfile) AddEntity(entityType string) {
	cp.EntityCounts[entityType]++
	cp.TotalNodes++
}

// AddDomain increments the count for the given domain.
func (cp *CodebaseProfile) AddDomain(domain int) {
	cp.DomainCounts[domain]++
}

// ComputeAverages computes average structural metrics from accumulated values.
// Should be called after all entities have been added and before persisting.
func (cp *CodebaseProfile) ComputeAverages() {
	if cp.TotalNodes == 0 {
		return
	}
	// Note: AvgInDegree, AvgOutDegree should be accumulated as totals
	// and then divided here. This assumes they were summed during indexing.
	if cp.AvgInDegree > 0 {
		cp.AvgInDegree = cp.AvgInDegree / float64(cp.TotalNodes)
	}
	if cp.AvgOutDegree > 0 {
		cp.AvgOutDegree = cp.AvgOutDegree / float64(cp.TotalNodes)
	}
}
