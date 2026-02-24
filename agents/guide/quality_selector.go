package guide

import (
	"math/rand/v2"
)

// QualityEndpoint represents an agent endpoint with quality and weight
// information for routing decisions.
type QualityEndpoint struct {
	AgentID   string
	AgentType string
	Quality   float64
	StdDev    float64
	Weight    float64
}

// QualityAwareSelector selects among multiple endpoints using weighted
// random selection proportional to each endpoint's routing Weight.
type QualityAwareSelector struct {
	rng *rand.Rand
}

// NewQualityAwareSelector creates a selector with the given random source.
// If src is nil, a default source is used.
func NewQualityAwareSelector(src rand.Source) *QualityAwareSelector {
	var rng *rand.Rand
	if src != nil {
		rng = rand.New(src)
	} else {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	return &QualityAwareSelector{rng: rng}
}

// Select picks a single endpoint using weighted random selection.
// Single endpoint: returned directly (zero-alloc fast path).
// Multiple: weighted random proportional to Weight.
// Returns the zero value if endpoints is empty.
func (s *QualityAwareSelector) Select(endpoints []QualityEndpoint) QualityEndpoint {
	switch len(endpoints) {
	case 0:
		return QualityEndpoint{}
	case 1:
		return endpoints[0]
	}

	// Sum weights.
	var totalWeight float64
	for i := range endpoints {
		totalWeight += endpoints[i].Weight
	}

	// All weights zero — fall through to first endpoint.
	if totalWeight <= 0 {
		return endpoints[0]
	}

	// Weighted random selection.
	r := s.rng.Float64() * totalWeight
	var cumulative float64
	for i := range endpoints {
		cumulative += endpoints[i].Weight
		if r <= cumulative {
			return endpoints[i]
		}
	}

	// Floating-point rounding edge case.
	return endpoints[len(endpoints)-1]
}
