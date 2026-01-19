// Package context provides adaptive context management for multi-agent systems.
// AR.1.1: Adaptive State Types
package context

import (
	"encoding/json"
	"math"
	"math/rand"
	"sync"
	"time"
)

// TaskContext represents a discovered context classification.
// Contexts are embedding-based clusters that emerge from query patterns.
type TaskContext string

// RewardWeights contains the sampled weights for reward calculation.
type RewardWeights struct {
	TaskSuccess     float64 `json:"task_success"`
	RelevanceBonus  float64 `json:"relevance_bonus"`
	StrugglePenalty float64 `json:"struggle_penalty"`
	WastePenalty    float64 `json:"waste_penalty"`
}

// ThresholdConfig contains sampled thresholds for context operations.
type ThresholdConfig struct {
	Confidence float64 `json:"confidence"`
	Excerpt    float64 `json:"excerpt"`
	Budget     float64 `json:"budget"`
}

// UpdateConfig controls the behavior of distribution updates.
type UpdateConfig struct {
	DecayFactor         float64 `json:"decay_factor"`
	OutlierSigma        float64 `json:"outlier_sigma"`
	MinEffectiveSamples float64 `json:"min_effective_samples"`
	DriftRate           float64 `json:"drift_rate"`
}

// DefaultUpdateConfig returns the default configuration for updates.
func DefaultUpdateConfig() *UpdateConfig {
	return &UpdateConfig{
		DecayFactor:         0.999,
		OutlierSigma:        3.0,
		MinEffectiveSamples: 10.0,
		DriftRate:           0.001,
	}
}

// RobustWeightDistribution handles outliers, non-stationarity, and cold start.
// Uses Beta distribution for Thompson Sampling.
type RobustWeightDistribution struct {
	Alpha            float64 `json:"alpha"`
	Beta             float64 `json:"beta"`
	EffectiveSamples float64 `json:"effective_samples"`
	PriorAlpha       float64 `json:"prior_alpha"`
	PriorBeta        float64 `json:"prior_beta"`
}

// Mean returns the expected value of the distribution.
func (w *RobustWeightDistribution) Mean() float64 {
	sum := w.Alpha + w.Beta
	if sum == 0 {
		return 0.5
	}
	return w.Alpha / sum
}

// Variance returns the variance of the distribution.
func (w *RobustWeightDistribution) Variance() float64 {
	sum := w.Alpha + w.Beta
	if sum == 0 || sum+1 == 0 {
		return 0
	}
	return (w.Alpha * w.Beta) / (sum * sum * (sum + 1))
}

// Sample draws a random value using Thompson Sampling from Beta distribution.
func (w *RobustWeightDistribution) Sample() float64 {
	return betaSample(w.Alpha, w.Beta)
}

// Confidence returns a measure of how confident the distribution is.
func (w *RobustWeightDistribution) Confidence() float64 {
	return w.EffectiveSamples / (w.EffectiveSamples + 10.0)
}

// CredibleInterval returns the [low, high] credible interval at given level.
func (w *RobustWeightDistribution) CredibleInterval(level float64) (float64, float64) {
	tail := (1.0 - level) / 2.0
	low := betaQuantile(w.Alpha, w.Beta, tail)
	high := betaQuantile(w.Alpha, w.Beta, 1.0-tail)
	return low, high
}

// Update performs a robust Bayesian update with outlier rejection and decay.
func (w *RobustWeightDistribution) Update(observation float64, config *UpdateConfig) {
	if config == nil {
		config = DefaultUpdateConfig()
	}

	if !w.shouldAcceptObservation(observation, config) {
		return
	}

	w.applyDecay(config.DecayFactor)
	w.addObservation(observation)
	w.applyColdStartProtection(config)
	w.applyPriorDrift(config.DriftRate)
}

func (w *RobustWeightDistribution) shouldAcceptObservation(obs float64, cfg *UpdateConfig) bool {
	stddev := math.Sqrt(w.Variance())
	if stddev <= 0 {
		return true
	}
	zScore := math.Abs(obs-w.Mean()) / stddev
	return zScore <= cfg.OutlierSigma
}

func (w *RobustWeightDistribution) applyDecay(factor float64) {
	w.Alpha *= factor
	w.Beta *= factor
	w.EffectiveSamples *= factor
}

func (w *RobustWeightDistribution) addObservation(observation float64) {
	w.Alpha += observation
	w.Beta += (1 - observation)
	w.EffectiveSamples++
}

func (w *RobustWeightDistribution) applyColdStartProtection(cfg *UpdateConfig) {
	if w.EffectiveSamples >= cfg.MinEffectiveSamples {
		return
	}
	scale := cfg.MinEffectiveSamples / w.EffectiveSamples
	w.Alpha = w.PriorAlpha + (w.Alpha-w.PriorAlpha)/scale
	w.Beta = w.PriorBeta + (w.Beta-w.PriorBeta)/scale
}

func (w *RobustWeightDistribution) applyPriorDrift(driftRate float64) {
	w.Alpha = w.Alpha*(1-driftRate) + w.PriorAlpha*driftRate
	w.Beta = w.Beta*(1-driftRate) + w.PriorBeta*driftRate
}

// EpisodeObservation captures full episode for learning.
type EpisodeObservation struct {
	Timestamp         time.Time       `json:"timestamp"`
	Position          int64           `json:"position"`
	SampledWeights    RewardWeights   `json:"sampled_weights"`
	SampledThresholds ThresholdConfig `json:"sampled_thresholds"`
	TaskContext       TaskContext     `json:"task_context"`
	QueryEmbedding    []float32       `json:"query_embedding"`
	TaskCompleted     bool            `json:"task_completed"`
	FollowUpCount     int             `json:"follow_up_count"`
	ToolCallCount     int             `json:"tool_call_count"`
	UserEdits         int             `json:"user_edits"`
	HedgingDetected   bool            `json:"hedging_detected"`
	SessionDuration   time.Duration   `json:"session_duration"`
	ExplicitSignals   []string        `json:"explicit_signals"`
	PrefetchedIDs     []string        `json:"prefetched_ids"`
	UsedIDs           []string        `json:"used_ids"`
	SearchedAfter     []string        `json:"searched_after"`
}

// InferSatisfaction derives satisfaction score from behavioral signals.
func (e *EpisodeObservation) InferSatisfaction() float64 {
	var score float64

	score += e.completionScore()
	score += e.followUpScore()
	score += e.searchScore()
	score += e.hedgingScore()
	score += e.prefetchScore()

	return score
}

func (e *EpisodeObservation) completionScore() float64 {
	if !e.TaskCompleted {
		return -0.4
	}
	if e.FollowUpCount == 0 {
		return 0.5
	}
	return 0.2
}

func (e *EpisodeObservation) followUpScore() float64 {
	return float64(e.FollowUpCount) * -0.1
}

func (e *EpisodeObservation) searchScore() float64 {
	return float64(len(e.SearchedAfter)) * -0.15
}

func (e *EpisodeObservation) hedgingScore() float64 {
	if e.HedgingDetected {
		return -0.2
	}
	return 0
}

func (e *EpisodeObservation) prefetchScore() float64 {
	unused := len(e.PrefetchedIDs) - len(e.UsedIDs)
	if unused < 0 {
		unused = 0
	}
	return float64(unused) * -0.02
}

// ContextBias represents learned adjustment multipliers for a context.
type ContextBias struct {
	RelevanceMult    float64 `json:"relevance_mult"`
	StruggleMult     float64 `json:"struggle_mult"`
	WasteMult        float64 `json:"waste_mult"`
	ObservationCount int     `json:"observation_count"`
}

// Adjust applies the context bias to base reward weights.
func (b *ContextBias) Adjust(base RewardWeights) RewardWeights {
	return RewardWeights{
		TaskSuccess:     base.TaskSuccess,
		RelevanceBonus:  base.RelevanceBonus * b.RelevanceMult,
		StrugglePenalty: base.StrugglePenalty * b.StruggleMult,
		WastePenalty:    base.WastePenalty * b.WasteMult,
	}
}

// UserWeightProfile learns individual preferences from behavior.
type UserWeightProfile struct {
	PrefersThorough     float64   `json:"prefers_thorough"`
	ToleratesSearches   float64   `json:"tolerates_searches"`
	WastePenaltyMult    float64   `json:"waste_penalty_mult"`
	StrugglePenaltyMult float64   `json:"struggle_penalty_mult"`
	ObservationCount    int       `json:"observation_count"`
	LastUpdated         time.Time `json:"last_updated"`
}

// Adjust applies user profile biases to base weights.
func (p *UserWeightProfile) Adjust(base RewardWeights) RewardWeights {
	if p.ObservationCount < 5 {
		return base
	}
	return RewardWeights{
		TaskSuccess:     base.TaskSuccess,
		RelevanceBonus:  base.RelevanceBonus,
		StrugglePenalty: base.StrugglePenalty * p.StrugglePenaltyMult,
		WastePenalty:    base.WastePenalty * p.WastePenaltyMult,
	}
}

// ContextCentroid represents a discovered context cluster.
type ContextCentroid struct {
	ID        TaskContext `json:"id"`
	Embedding []float32   `json:"embedding"`
	Count     int64       `json:"count"`
	Bias      ContextBias `json:"bias"`
}

// ContextDiscovery manages embedding-based context classification.
type ContextDiscovery struct {
	Centroids    []ContextCentroid `json:"centroids"`
	MaxContexts  int               `json:"max_contexts"`
	keywordCache sync.Map          `json:"-"`
}

// GetBias returns the bias for a given context, or nil if not found.
func (c *ContextDiscovery) GetBias(ctx TaskContext) *ContextBias {
	for i := range c.Centroids {
		if c.Centroids[i].ID == ctx {
			return &c.Centroids[i].Bias
		}
	}
	return nil
}

// AdaptiveState stores sufficient statistics for adaptive learning.
type AdaptiveState struct {
	Weights struct {
		TaskSuccess     RobustWeightDistribution `json:"task_success"`
		RelevanceBonus  RobustWeightDistribution `json:"relevance_bonus"`
		StrugglePenalty RobustWeightDistribution `json:"struggle_penalty"`
		WastePenalty    RobustWeightDistribution `json:"waste_penalty"`
	} `json:"weights"`

	Thresholds struct {
		Confidence RobustWeightDistribution `json:"confidence"`
		Excerpt    RobustWeightDistribution `json:"excerpt"`
		Budget     RobustWeightDistribution `json:"budget"`
	} `json:"thresholds"`

	ContextDiscovery  *ContextDiscovery `json:"context_discovery"`
	UserProfile       UserWeightProfile `json:"user_profile"`
	TotalObservations int64             `json:"total_observations"`
	LastUpdated       time.Time         `json:"last_updated"`
	Version           int               `json:"version"`

	config *UpdateConfig `json:"-"`
	mu     sync.RWMutex  `json:"-"`
}

// NewAdaptiveState creates a new state with initial priors.
func NewAdaptiveState() *AdaptiveState {
	state := &AdaptiveState{
		ContextDiscovery: &ContextDiscovery{MaxContexts: 10},
		UserProfile: UserWeightProfile{
			WastePenaltyMult:    1.0,
			StrugglePenaltyMult: 1.0,
		},
		config:  DefaultUpdateConfig(),
		Version: 1,
	}

	state.initWeights()
	state.initThresholds()

	return state
}

func (a *AdaptiveState) initWeights() {
	a.Weights.TaskSuccess = newRobustDist(8, 2)
	a.Weights.RelevanceBonus = newRobustDist(3, 7)
	a.Weights.StrugglePenalty = newRobustDist(4, 6)
	a.Weights.WastePenalty = newRobustDist(1, 9)
}

func (a *AdaptiveState) initThresholds() {
	a.Thresholds.Confidence = newRobustDist(8.5, 1.5)
	a.Thresholds.Excerpt = newRobustDist(9, 1)
	a.Thresholds.Budget = newRobustDist(1, 9)
}

func newRobustDist(alpha, beta float64) RobustWeightDistribution {
	return RobustWeightDistribution{
		Alpha:      alpha,
		Beta:       beta,
		PriorAlpha: alpha,
		PriorBeta:  beta,
	}
}

// SampleWeights draws from current distributions using Thompson Sampling.
func (a *AdaptiveState) SampleWeights(ctx TaskContext) RewardWeights {
	a.mu.RLock()
	defer a.mu.RUnlock()

	weights := RewardWeights{
		TaskSuccess:     a.Weights.TaskSuccess.Sample(),
		RelevanceBonus:  a.Weights.RelevanceBonus.Sample(),
		StrugglePenalty: a.Weights.StrugglePenalty.Sample(),
		WastePenalty:    a.Weights.WastePenalty.Sample(),
	}

	weights = a.applyUserProfile(weights)
	weights = a.applyContextBias(weights, ctx)

	return weights
}

func (a *AdaptiveState) applyUserProfile(weights RewardWeights) RewardWeights {
	if a.UserProfile.ObservationCount >= 5 {
		return a.UserProfile.Adjust(weights)
	}
	return weights
}

func (a *AdaptiveState) applyContextBias(weights RewardWeights, ctx TaskContext) RewardWeights {
	if a.ContextDiscovery == nil {
		return weights
	}
	if bias := a.ContextDiscovery.GetBias(ctx); bias != nil {
		return bias.Adjust(weights)
	}
	return weights
}

// MarshalBinary serializes the state to json format.
func (a *AdaptiveState) MarshalBinary() ([]byte, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return json.Marshal(a)
}

// UnmarshalBinary deserializes the state from json format.
func (a *AdaptiveState) UnmarshalBinary(data []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := json.Unmarshal(data, a); err != nil {
		return err
	}
	a.config = DefaultUpdateConfig()
	return nil
}

// betaSample generates a random sample from Beta(alpha, beta) distribution.
// Uses the gamma function relationship: Beta(a,b) = Gamma(a) / (Gamma(a) + Gamma(b))
func betaSample(alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return 0.5
	}
	x := gammaSample(alpha)
	y := gammaSample(beta)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// gammaSample generates a random sample from Gamma(shape, 1) distribution.
// Uses Marsaglia and Tsang's method for shape >= 1.
func gammaSample(shape float64) float64 {
	if shape < 1 {
		return gammaSample(shape+1) * math.Pow(rand.Float64(), 1.0/shape)
	}

	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		var x, v float64
		for {
			x = rand.NormFloat64()
			v = 1 + c*x
			if v > 0 {
				break
			}
		}
		v = v * v * v
		u := rand.Float64()

		if u < 1-0.0331*(x*x)*(x*x) {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}

// betaQuantile approximates the quantile function of Beta distribution.
// Uses simple bisection method for approximation.
func betaQuantile(alpha, beta, p float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}

	low, high := 0.0, 1.0
	for i := 0; i < 50; i++ {
		mid := (low + high) / 2
		cdf := betaIncomplete(alpha, beta, mid)
		if cdf < p {
			low = mid
		} else {
			high = mid
		}
	}
	return (low + high) / 2
}

// betaIncomplete computes the regularized incomplete beta function.
func betaIncomplete(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}

	bt := math.Exp(lgamma(a+b) - lgamma(a) - lgamma(b) +
		a*math.Log(x) + b*math.Log(1-x))

	if x < (a+1)/(a+b+2) {
		return bt * betaCF(a, b, x) / a
	}
	return 1 - bt*betaCF(b, a, 1-x)/b
}

// betaCF computes continued fraction for incomplete beta function.
func betaCF(a, b, x float64) float64 {
	const maxIter = 100
	const eps = 3e-7

	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < 1e-30 {
		d = 1e-30
	}
	d = 1 / d
	h := d

	for m := 1; m <= maxIter; m++ {
		m2 := 2 * m
		aa := float64(m) * (b - float64(m)) * x / ((qam + float64(m2)) * (a + float64(m2)))
		d = 1 + aa*d
		if math.Abs(d) < 1e-30 {
			d = 1e-30
		}
		c = 1 + aa/c
		if math.Abs(c) < 1e-30 {
			c = 1e-30
		}
		d = 1 / d
		h *= d * c

		aa = -(a + float64(m)) * (qab + float64(m)) * x / ((a + float64(m2)) * (qap + float64(m2)))
		d = 1 + aa*d
		if math.Abs(d) < 1e-30 {
			d = 1e-30
		}
		c = 1 + aa/c
		if math.Abs(c) < 1e-30 {
			c = 1e-30
		}
		d = 1 / d
		del := d * c
		h *= del

		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// lgamma returns the natural log of Gamma(x).
func lgamma(x float64) float64 {
	lg, _ := math.Lgamma(x)
	return lg
}
