package quality

// SCORE-06 scoring bridge. The hierarchical Bayesian QualityWeights is the
// canonical home for quality-scoring state (priors + posteriors + WAL +
// checkpoint). The legacy scorer in core/vectorgraphdb/mitigations takes
// a flat 5-float weight vector (Relevance/Freshness/Trust/Density/
// Redundancy) — this file projects the hierarchical posteriors onto that
// schema so existing scorers keep working while their canonical state
// lives in core/quality.
//
// The projection is one-directional by design: hierarchical → scalar. The
// inverse would require guessing how a single Trust weight was split
// between TrustLevel and Authority, which has no stable answer. New
// scoring code should consume *QualityWeights directly; legacy scorers
// consume ScalarWeights computed here.

// ScalarWeights is the flat 5-float weight vector legacy scorers
// (ContextQualityScorer in core/vectorgraphdb/mitigations) consume. It
// aggregates the 8 hierarchical signals into 5 scorer slots.
type ScalarWeights struct {
	// Relevance sums the retrieval-core signals (vector similarity,
	// lexical match, graph centrality, cross-validation agreement).
	Relevance float64
	// Freshness is driven solely by staleness.
	Freshness float64
	// Trust combines source trust level and authority.
	Trust float64
	// Density proxies "content helpfulness" via usage feedback.
	Density float64
	// Redundancy is intentionally not derivable from posterior weights
	// — it's computed scorer-side per-result based on similarity to
	// already-selected items. Remains 0 here; the scorer layer
	// populates it at scoring time.
	Redundancy float64
}

// DefaultScalarWeights returns the scorer defaults the legacy mitigations
// package ships. Preserved here so callers migrating from mitigations
// have a zero-churn default to pass in.
func DefaultScalarWeights() ScalarWeights {
	return ScalarWeights{
		Relevance:  0.35,
		Freshness:  0.20,
		Trust:      0.25,
		Density:    0.15,
		Redundancy: 0.05,
	}
}

// ComputeScalarWeights projects a hierarchical QualityWeights onto the
// legacy 5-float schema using posterior means. Called at scoring time so
// the scorer sees the session's current posterior, not a stale snapshot.
//
// Mapping rationale:
//   - Relevance: retrieval + diversity signals
//   - Freshness: staleness only (the decay signal)
//   - Trust: source trust + authority (both are source-quality signals)
//   - Density: usage feedback (historical helpfulness is the density proxy)
//   - Redundancy: not a weight; stays 0 so the scorer populates per-result
//
// Nil-safe: passing nil returns DefaultScalarWeights so callers without a
// weight manager configured get sensible behavior.
func ComputeScalarWeights(q *QualityWeights) ScalarWeights {
	if q == nil {
		return DefaultScalarWeights()
	}
	return ScalarWeights{
		Relevance:  q.VectorWeight.Mean() + q.BleveWeight.Mean() + q.GraphWeight.Mean() + q.CrossValidationBonus.Mean(),
		Freshness:  q.StalenessWeight.Mean(),
		Trust:      q.TrustLevelWeight.Mean() + q.AuthorityWeight.Mean(),
		Density:    q.UsageFeedbackWeight.Mean(),
		Redundancy: 0,
	}
}

// SampleScalarWeights is the Thompson-sampling variant of
// ComputeScalarWeights. Draws each hierarchical weight once and sums
// them into the 5-float schema — different retrievals explore different
// weight combinations.
func SampleScalarWeights(q *QualityWeights) ScalarWeights {
	if q == nil {
		return DefaultScalarWeights()
	}
	return ScalarWeights{
		Relevance:  q.VectorWeight.Sample() + q.BleveWeight.Sample() + q.GraphWeight.Sample() + q.CrossValidationBonus.Sample(),
		Freshness:  q.StalenessWeight.Sample(),
		Trust:      q.TrustLevelWeight.Sample() + q.AuthorityWeight.Sample(),
		Density:    q.UsageFeedbackWeight.Sample(),
		Redundancy: 0,
	}
}

// Normalize scales the weights to sum to 1.0. Useful when a downstream
// scorer expects a probability-like weighting rather than raw sums. Does
// not modify the receiver — returns a new ScalarWeights.
func (w ScalarWeights) Normalize() ScalarWeights {
	total := w.Relevance + w.Freshness + w.Trust + w.Density + w.Redundancy
	if total <= 0 {
		return w
	}
	return ScalarWeights{
		Relevance:  w.Relevance / total,
		Freshness:  w.Freshness / total,
		Trust:      w.Trust / total,
		Density:    w.Density / total,
		Redundancy: w.Redundancy / total,
	}
}
