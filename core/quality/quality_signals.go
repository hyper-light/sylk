package quality

import "fmt"

// SignalKind enumerates the quality signals the scoring layer folds into a
// final score. Each signal has a weight in QualityWeights; the ordering
// here mirrors QualityWeights.MeanVector / SampleVector exactly so a
// linear scorer can zip-multiply the two slices without a lookup table.
//
// Signal categories and priorities come from SCORING.md §1 "Quality Signal
// Taxonomy." The taxonomy names the *inputs*; LearnedWeight fields name
// the *weights* the scorer applies to those inputs.
type SignalKind uint8

const (
	// SignalVector is the similarity score from the vector (HNSW) index —
	// the core semantic retrieval signal. Priority: High.
	SignalVector SignalKind = iota

	// SignalGraph is the graph-centrality / relational signal: how many
	// incoming citations, path-based relevance from anchor nodes, etc.
	// Priority: High.
	SignalGraph

	// SignalBleve is the full-text match signal from Bleve — the lexical
	// retrieval score. Useful for exact-term queries. Priority: High.
	SignalBleve

	// SignalTrustLevel rolls the source's TrustLevel (verified_code,
	// recent_history, official_docs, etc.) into a trust contribution.
	// Priority: Medium (source authority).
	SignalTrustLevel

	// SignalStaleness penalizes content that has decayed past its
	// useful-life window. Domain-specific: code decays fast, academic
	// papers decay slowly. Priority: High (temporal quality).
	SignalStaleness

	// SignalUsageFeedback consumes prior-query helpfulness — "did a past
	// session find this result useful?" Priority: Highest (SCORING.md §1).
	SignalUsageFeedback

	// SignalAuthority is the source-authority contribution for external
	// content (peer-reviewed > blog > StackOverflow). N/A for local code.
	// Priority: Medium.
	SignalAuthority

	// SignalCrossValidation is a bonus when multiple retrieval paths
	// (vector + bleve + graph) agree on the result — a diversity signal.
	// Priority: High (relational quality).
	SignalCrossValidation

	// signalCount is the sentinel count used for sizing vectors and
	// iteration. It must stay last; changing the order requires
	// coordinated updates to every MeanVector/SampleVector consumer.
	signalCount
)

// SignalCount returns the number of signal kinds. Exported for
// tests / sizing consumers that would otherwise hardcode 8.
func SignalCount() int { return int(signalCount) }

// String returns a stable, human-readable identifier for the signal. Used
// in WAL entries, diagnostics, and debug logs. The returned strings are
// contract — changing one breaks replay of older WALs.
func (k SignalKind) String() string {
	switch k {
	case SignalVector:
		return "vector"
	case SignalGraph:
		return "graph"
	case SignalBleve:
		return "bleve"
	case SignalTrustLevel:
		return "trust_level"
	case SignalStaleness:
		return "staleness"
	case SignalUsageFeedback:
		return "usage_feedback"
	case SignalAuthority:
		return "authority"
	case SignalCrossValidation:
		return "cross_validation"
	default:
		return fmt.Sprintf("signal_unknown_%d", k)
	}
}

// SignalKindFromString parses the inverse of String(). Returns ok=false for
// unknown labels so WAL replay can skip entries from unrecognized signal
// versions instead of panicking.
func SignalKindFromString(s string) (SignalKind, bool) {
	switch s {
	case "vector":
		return SignalVector, true
	case "graph":
		return SignalGraph, true
	case "bleve":
		return SignalBleve, true
	case "trust_level":
		return SignalTrustLevel, true
	case "staleness":
		return SignalStaleness, true
	case "usage_feedback":
		return SignalUsageFeedback, true
	case "authority":
		return SignalAuthority, true
	case "cross_validation":
		return SignalCrossValidation, true
	default:
		return 0, false
	}
}

// SignalObservation is a single (signal, success?, weight) tuple fed into
// the Bayesian learner. The session manager consumes these and folds them
// into the appropriate LearnedWeight field.
type SignalObservation struct {
	Kind   SignalKind
	// Success is true when the signal's contribution helped the user
	// (measured via downstream feedback, turn outcome, or citation use).
	Success bool
	// EffectiveWeight scales how much the observation moves the posterior.
	// Typically 1.0 for a single turn; fractional weights come from
	// confidence-aware reducers.
	EffectiveWeight float64
}

// QualityScoreComponents captures the per-signal contribution that produced
// a final quality score. Returned alongside the scalar score so scoring
// decisions are inspectable (why did this result rank high?) and
// attributable for feedback-update routing.
type QualityScoreComponents struct {
	// Inputs[k] is the raw signal value in [0, 1] for signal k before the
	// weight is applied. An out-of-range value is clamped to [0, 1] by
	// the scorer, not here.
	Inputs [signalCount]float64
	// Weights[k] is the weight applied (typically Mean() or Sample()).
	Weights [signalCount]float64
}

// WeightedSum returns Σ (Inputs[k] * Weights[k]) across all signal kinds.
// The scoring layer normalizes the result if desired — the raw sum is
// returned here because different consumers (ranking vs. gating) prefer
// different normalizations.
func (c QualityScoreComponents) WeightedSum() float64 {
	var total float64
	for k := SignalKind(0); k < signalCount; k++ {
		total += c.Inputs[k] * c.Weights[k]
	}
	return total
}
