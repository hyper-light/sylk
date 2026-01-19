package coldstart

import "fmt"

// =============================================================================
// Cold Prior Weights Configuration (MD.9.3)
// =============================================================================

// ColdPriorWeights defines the relative importance of different signal types
// when computing cold-start priors. Weights are organized into three categories:
// structural, content, and distributional.
type ColdPriorWeights struct {
	// Structural category (0.40 total)
	StructuralWeight float64
	PageRankWeight   float64
	DegreeWeight     float64
	ClusterWeight    float64

	// Content category (0.35 total)
	ContentWeight     float64
	EntityTypeWeight  float64
	NameWeight        float64
	DocWeight         float64

	// Distributional category (0.25 total)
	DistributionalWeight float64
	FrequencyWeight      float64
	RarityWeight         float64
}

// DefaultColdPriorWeights returns empirically-tuned default weights.
// These weights have been calibrated to produce valid priors in [0,1].
func DefaultColdPriorWeights() *ColdPriorWeights {
	return &ColdPriorWeights{
		// Structural category
		StructuralWeight: 0.40,
		PageRankWeight:   0.50,
		DegreeWeight:     0.30,
		ClusterWeight:    0.20,

		// Content category
		ContentWeight:    0.35,
		EntityTypeWeight: 0.40,
		NameWeight:       0.35,
		DocWeight:        0.25,

		// Distributional category
		DistributionalWeight: 0.25,
		FrequencyWeight:      0.60,
		RarityWeight:         0.40,
	}
}

// Validate ensures that weights are properly configured and sum correctly
// per category. Returns an error if validation fails.
func (w *ColdPriorWeights) Validate() error {
	// Check category weights sum to 1.0 (with tolerance)
	categorySum := w.StructuralWeight + w.ContentWeight + w.DistributionalWeight
	if !approxEqual(categorySum, 1.0, 0.01) {
		return fmt.Errorf(
			"category weights must sum to 1.0, got %.3f",
			categorySum,
		)
	}

	// Check structural sub-weights sum to 1.0
	structuralSum := w.PageRankWeight + w.DegreeWeight + w.ClusterWeight
	if !approxEqual(structuralSum, 1.0, 0.01) {
		return fmt.Errorf(
			"structural sub-weights must sum to 1.0, got %.3f",
			structuralSum,
		)
	}

	// Check content sub-weights sum to 1.0
	contentSum := w.EntityTypeWeight + w.NameWeight + w.DocWeight
	if !approxEqual(contentSum, 1.0, 0.01) {
		return fmt.Errorf(
			"content sub-weights must sum to 1.0, got %.3f",
			contentSum,
		)
	}

	// Check distributional sub-weights sum to 1.0
	distribSum := w.FrequencyWeight + w.RarityWeight
	if !approxEqual(distribSum, 1.0, 0.01) {
		return fmt.Errorf(
			"distributional sub-weights must sum to 1.0, got %.3f",
			distribSum,
		)
	}

	return nil
}

// approxEqual checks if two floats are approximately equal within tolerance.
func approxEqual(a, b, tolerance float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}
