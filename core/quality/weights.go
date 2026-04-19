// Package quality implements the Bayesian quality scoring stack described in
// SCORING.md §2. Nothing is hardcoded — every weight, decay rate, and
// threshold is a posterior over observations, not a point estimate. At query
// time the system samples from the posterior (Thompson sampling) to balance
// exploitation and exploration.
//
// Ownership: this package owns the core scoring types (LearnedWeight,
// QualityWeights, LearnedDecayRate) and the priors/signals catalog. Sessions
// hold isolated copy-on-write overlays of these types via SessionWeightManager;
// a session's learning never bleeds into a sibling session's retrieval.
//
// Thread-safety contract: LearnedWeight and QualityWeights values are
// immutable. Update() returns a new value rather than mutating the receiver
// so concurrent readers cannot observe a torn half-updated posterior. The
// session manager (SCORE-02) performs RCU-style atomic swaps of entire
// QualityWeights on each observation.
package quality

import (
	"fmt"
	"math"
	"math/rand/v2"

	"gonum.org/v1/gonum/stat/distuv"
)

// LearnedWeight is a Beta(α, β) posterior over a [0, 1] quantity — typically
// the weight a signal carries in the final quality score. It is NOT a point
// estimate: the mean reports the current expected value, the variance
// reports remaining uncertainty, and Sample() draws a stochastic value for
// Thompson-sampling-based exploration.
//
// Prior* fields persist across updates so callers can detect drift against
// the original prior and apply bounded-rationality guardrails (e.g. reject
// posteriors that drifted too far too fast).
type LearnedWeight struct {
	Alpha            float64 `json:"alpha"`
	Beta             float64 `json:"beta"`
	EffectiveSamples float64 `json:"effective_samples"`
	PriorAlpha       float64 `json:"prior_alpha"`
	PriorBeta        float64 `json:"prior_beta"`
}

// NewLearnedWeight constructs a LearnedWeight with the given prior. The
// prior establishes the initial belief; subsequent Update calls shift the
// posterior as observations arrive. A prior with (Alpha, Beta) = (1, 1) is
// uniform; Alpha << Beta expresses a low-weight prior with high variance.
func NewLearnedWeight(alpha, beta float64) *LearnedWeight {
	if alpha <= 0 || beta <= 0 {
		// Invalid priors would crash Beta sampling downstream — fall back
		// to a minimally-informative symmetric prior rather than panic.
		// SCORING.md §2 uses weakly-informative priors precisely because
		// noise on either end is safer than a degenerate distribution.
		alpha, beta = 1, 1
	}
	return &LearnedWeight{
		Alpha:      alpha,
		Beta:       beta,
		PriorAlpha: alpha,
		PriorBeta:  beta,
	}
}

// Mean returns the posterior mean E[w] = α/(α+β). Nil-safe: a nil pointer
// returns 0, which is the correct "no signal contribution" value for
// scoring — uninitialized weights should not vote.
func (w *LearnedWeight) Mean() float64 {
	if w == nil {
		return 0
	}
	sum := w.Alpha + w.Beta
	if sum <= 0 {
		return 0
	}
	return w.Alpha / sum
}

// Variance returns the posterior variance Var[w] = αβ / ((α+β)²(α+β+1)).
// Tighter distributions (many effective samples) produce lower variance,
// which the confidence score converts into a [0, 1] confidence measure.
func (w *LearnedWeight) Variance() float64 {
	if w == nil {
		return 0
	}
	sum := w.Alpha + w.Beta
	if sum <= 0 {
		return 0
	}
	return (w.Alpha * w.Beta) / (sum * sum * (sum + 1))
}

// Sample draws a Thompson-sampling value from the posterior using gonum's
// Beta distribution. The caller should call Sample() once per query so
// different retrieval calls explore different weight combinations.
func (w *LearnedWeight) Sample() float64 {
	if w == nil {
		return 0
	}
	alpha, beta := w.Alpha, w.Beta
	if alpha <= 0 || beta <= 0 {
		return w.Mean()
	}
	dist := distuv.Beta{Alpha: alpha, Beta: beta}
	return dist.Rand()
}

// Confidence returns a [0, 1) measure of how confident the posterior is,
// derived from EffectiveSamples: samples → ∞ implies confidence → 1.
// The scoring layer uses this to choose between the learned posterior and
// the prior when a weight has seen too few observations to trust.
func (w *LearnedWeight) Confidence() float64 {
	if w == nil {
		return 0
	}
	return 1.0 - 1.0/(1.0+w.EffectiveSamples/10.0)
}

// Update folds a binary observation (success or failure) into the posterior
// and returns a new LearnedWeight. The receiver is never mutated — callers
// that want in-place semantics assign the result back. Immutability lets
// concurrent readers continue sampling the pre-update distribution without
// locks.
//
// The effectiveSampleWeight argument is typically 1.0 for a single
// observation. Decay toward prior as effective-sample count accumulates is
// handled by the session manager's periodic decay step, not here.
func (w *LearnedWeight) Update(success bool, effectiveSampleWeight float64) *LearnedWeight {
	if w == nil {
		return nil
	}
	if effectiveSampleWeight <= 0 {
		effectiveSampleWeight = 1
	}
	next := *w
	if success {
		next.Alpha += effectiveSampleWeight
	} else {
		next.Beta += effectiveSampleWeight
	}
	next.EffectiveSamples += effectiveSampleWeight
	return &next
}

// Decay shrinks the distance of the posterior from the prior toward the
// prior by the fractional rate r ∈ (0, 1]. r=0.01 means "drift 1% of the
// way back to prior." Periodic decay prevents stale observations from
// dominating the posterior forever; SCORING.md §3 specifies a 30-day
// half-life at the project level and 90-day at the global level.
func (w *LearnedWeight) Decay(r float64) *LearnedWeight {
	if w == nil || r <= 0 {
		return w
	}
	if r > 1 {
		r = 1
	}
	next := *w
	next.Alpha = blend(w.Alpha, w.PriorAlpha, r)
	next.Beta = blend(w.Beta, w.PriorBeta, r)
	next.EffectiveSamples = w.EffectiveSamples * (1 - r)
	return &next
}

// Clone returns a deep-equal copy. QualityWeights.Clone relies on this so
// session overlays can fork from a base snapshot without sharing memory.
func (w *LearnedWeight) Clone() *LearnedWeight {
	if w == nil {
		return nil
	}
	copied := *w
	return &copied
}

// String returns a compact debug representation. Diagnostics only.
func (w *LearnedWeight) String() string {
	if w == nil {
		return "LearnedWeight<nil>"
	}
	return fmt.Sprintf("LW(α=%.2f β=%.2f n=%.1f mean=%.3f)", w.Alpha, w.Beta, w.EffectiveSamples, w.Mean())
}

// QualityWeights bundles the seven Beta-posterior weights that drive the
// quality score. Fields correspond to SCORING.md §2 "Quality Weight
// Configuration."
type QualityWeights struct {
	VectorWeight         *LearnedWeight `json:"vector_weight"`
	GraphWeight          *LearnedWeight `json:"graph_weight"`
	BleveWeight          *LearnedWeight `json:"bleve_weight"`
	TrustLevelWeight     *LearnedWeight `json:"trust_level_weight"`
	StalenessWeight      *LearnedWeight `json:"staleness_weight"`
	UsageFeedbackWeight  *LearnedWeight `json:"usage_feedback_weight"`
	AuthorityWeight      *LearnedWeight `json:"authority_weight"`
	CrossValidationBonus *LearnedWeight `json:"cross_validation_bonus"`
}

// Clone returns a deep-equal copy so session overlays can fork from a base
// snapshot without sharing memory with sibling sessions.
func (q *QualityWeights) Clone() *QualityWeights {
	if q == nil {
		return nil
	}
	return &QualityWeights{
		VectorWeight:         q.VectorWeight.Clone(),
		GraphWeight:          q.GraphWeight.Clone(),
		BleveWeight:          q.BleveWeight.Clone(),
		TrustLevelWeight:     q.TrustLevelWeight.Clone(),
		StalenessWeight:      q.StalenessWeight.Clone(),
		UsageFeedbackWeight:  q.UsageFeedbackWeight.Clone(),
		AuthorityWeight:      q.AuthorityWeight.Clone(),
		CrossValidationBonus: q.CrossValidationBonus.Clone(),
	}
}

// MeanVector returns the posterior means in a canonical ordering suitable
// for feeding into a linear scorer. The ordering matches iterWeights so
// updates keyed by SignalKind and the weighted sum consume identical
// positions.
func (q *QualityWeights) MeanVector() []float64 {
	if q == nil {
		return nil
	}
	return []float64{
		q.VectorWeight.Mean(),
		q.GraphWeight.Mean(),
		q.BleveWeight.Mean(),
		q.TrustLevelWeight.Mean(),
		q.StalenessWeight.Mean(),
		q.UsageFeedbackWeight.Mean(),
		q.AuthorityWeight.Mean(),
		q.CrossValidationBonus.Mean(),
	}
}

// SampleVector returns Thompson-sampled weights in the same canonical
// ordering as MeanVector. Callers sample once per query so different
// retrievals explore different weight mixes.
func (q *QualityWeights) SampleVector() []float64 {
	if q == nil {
		return nil
	}
	return []float64{
		q.VectorWeight.Sample(),
		q.GraphWeight.Sample(),
		q.BleveWeight.Sample(),
		q.TrustLevelWeight.Sample(),
		q.StalenessWeight.Sample(),
		q.UsageFeedbackWeight.Sample(),
		q.AuthorityWeight.Sample(),
		q.CrossValidationBonus.Sample(),
	}
}

// LearnedDecayRate is a Gamma(α, β) posterior over a non-negative rate
// parameter — staleness decay, traffic-shift rate, or any count-domain
// quantity. λ ~ Gamma(α, β) has E[λ] = α/β and Var[λ] = α/β².
type LearnedDecayRate struct {
	Alpha            float64 `json:"alpha"`
	Beta             float64 `json:"beta"`
	EffectiveSamples float64 `json:"effective_samples"`
	PriorAlpha       float64 `json:"prior_alpha"`
	PriorBeta        float64 `json:"prior_beta"`
}

// NewLearnedDecayRate constructs a Gamma posterior. Priors (α, β) encode
// the mean α/β with variance α/β². An α=β=1 prior is Exp(1) — memoryless
// with mean 1.
func NewLearnedDecayRate(alpha, beta float64) *LearnedDecayRate {
	if alpha <= 0 || beta <= 0 {
		alpha, beta = 1, 1
	}
	return &LearnedDecayRate{
		Alpha:      alpha,
		Beta:       beta,
		PriorAlpha: alpha,
		PriorBeta:  beta,
	}
}

// Mean returns E[λ] = α/β.
func (r *LearnedDecayRate) Mean() float64 {
	if r == nil || r.Beta <= 0 {
		return 0
	}
	return r.Alpha / r.Beta
}

// Variance returns Var[λ] = α/β².
func (r *LearnedDecayRate) Variance() float64 {
	if r == nil || r.Beta <= 0 {
		return 0
	}
	return r.Alpha / (r.Beta * r.Beta)
}

// Sample draws a Thompson-sampling rate from the Gamma posterior.
func (r *LearnedDecayRate) Sample() float64 {
	if r == nil {
		return 0
	}
	if r.Alpha <= 0 || r.Beta <= 0 {
		return r.Mean()
	}
	dist := distuv.Gamma{Alpha: r.Alpha, Beta: r.Beta}
	return dist.Rand()
}

// Update folds a count-domain observation (the observed number of events
// over the observation interval) into the Gamma posterior and returns a
// fresh LearnedDecayRate. Immutable for the same RCU reason as
// LearnedWeight.Update.
func (r *LearnedDecayRate) Update(eventCount float64, intervalLength float64) *LearnedDecayRate {
	if r == nil || intervalLength <= 0 {
		return r
	}
	next := *r
	next.Alpha += eventCount
	next.Beta += intervalLength
	next.EffectiveSamples += intervalLength
	return &next
}

// Clone returns a deep-equal copy.
func (r *LearnedDecayRate) Clone() *LearnedDecayRate {
	if r == nil {
		return nil
	}
	copied := *r
	return &copied
}

// blend returns linear interpolation between current and target with
// fraction f ∈ [0, 1]. f=0 keeps current; f=1 snaps to target.
func blend(current, target, f float64) float64 {
	if math.IsNaN(current) || math.IsNaN(target) {
		return current
	}
	return current*(1-f) + target*f
}

// SeededRand returns a rand.Rand seeded from the given uint64 so tests can
// generate deterministic Thompson samples. Production code uses distuv's
// default source (math/rand/v2's global).
func SeededRand(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
}
