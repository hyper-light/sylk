// Package query provides hybrid query execution for the knowledge system.
// HybridScorer implements MD.9.10 combining multiple scoring signals with cold-start support.
package query

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/handoff"
)

// =============================================================================
// MD.9.10 Signal Types
// =============================================================================

// SignalType identifies the type of scoring signal.
type SignalType int

const (
	// SignalRRF is the RRF fusion score from hybrid search.
	SignalRRF SignalType = iota

	// SignalMemoryDecay is the ACT-R memory activation/decay score.
	SignalMemoryDecay

	// SignalColdStartPrior is the cold-start prior score for new nodes.
	SignalColdStartPrior

	// SignalPageRank is the PageRank centrality score.
	SignalPageRank

	// SignalClusterCoeff is the clustering coefficient score.
	SignalClusterCoeff

	// SignalRecency is the temporal recency score.
	SignalRecency

	// SignalFrequency is the access frequency score.
	SignalFrequency
)

// String returns the signal type name.
func (st SignalType) String() string {
	switch st {
	case SignalRRF:
		return "rrf"
	case SignalMemoryDecay:
		return "memory_decay"
	case SignalColdStartPrior:
		return "cold_start_prior"
	case SignalPageRank:
		return "pagerank"
	case SignalClusterCoeff:
		return "cluster_coeff"
	case SignalRecency:
		return "recency"
	case SignalFrequency:
		return "frequency"
	default:
		return "unknown"
	}
}

// AllSignalTypes returns all defined signal types.
func AllSignalTypes() []SignalType {
	return []SignalType{
		SignalRRF,
		SignalMemoryDecay,
		SignalColdStartPrior,
		SignalPageRank,
		SignalClusterCoeff,
		SignalRecency,
		SignalFrequency,
	}
}

// =============================================================================
// MD.9.10 Interfaces for Dependencies (to avoid import cycles)
// =============================================================================

// MemoryAccessor provides access to ACT-R memory data.
type MemoryAccessor interface {
	// GetActivation returns the activation level for a node at the given time.
	// Returns negative infinity if node has no memory data.
	GetActivation(ctx context.Context, nodeID string, d domain.Domain, now time.Time) (float64, error)

	// GetAccessCount returns the number of access traces for a node.
	GetAccessCount(ctx context.Context, nodeID string, d domain.Domain) (int, error)

	// GetLastAccessTime returns the last access time for a node.
	// Returns zero time if node has no access history.
	GetLastAccessTime(ctx context.Context, nodeID string, d domain.Domain) (time.Time, error)
}

// ColdStartPriorCalculator computes cold-start priors for nodes.
type ColdStartPriorCalculator interface {
	// ComputePrior computes the cold-start prior for a node.
	// Returns a value in [0,1] representing node importance.
	ComputePrior(nodeID string) float64
}

// GraphSignalsProvider provides graph topology signals for nodes.
type GraphSignalsProvider interface {
	// GetPageRank returns the PageRank score for a node (normalized to [0,1]).
	GetPageRank(nodeID string) float64

	// GetClusteringCoefficient returns the clustering coefficient for a node.
	GetClusteringCoefficient(nodeID string) float64
}

// =============================================================================
// MD.9.10 ScoreResult
// =============================================================================

// SignalContribution captures a single signal's contribution to the final score.
type SignalContribution struct {
	// Signal identifies which signal contributed.
	Signal SignalType `json:"signal"`

	// RawValue is the unweighted signal value.
	RawValue float64 `json:"raw_value"`

	// Weight is the weight applied to this signal.
	Weight float64 `json:"weight"`

	// Contribution is the weighted contribution (RawValue * Weight).
	Contribution float64 `json:"contribution"`
}

// ScoreResult contains a scored result with breakdown of signal contributions.
type ScoreResult struct {
	// Result is the original hybrid result.
	Result HybridResult `json:"result"`

	// FinalScore is the combined score from all signals.
	FinalScore float64 `json:"final_score"`

	// Contributions breaks down each signal's contribution.
	Contributions []SignalContribution `json:"contributions"`

	// IsColdStart indicates if cold-start priors were used.
	IsColdStart bool `json:"is_cold_start"`

	// BlendRatio is the ratio of warm to cold scoring (0=pure cold, 1=pure warm).
	BlendRatio float64 `json:"blend_ratio"`

	// AccessCount is the number of access traces for this node.
	AccessCount int `json:"access_count"`

	// Explanation provides a human-readable explanation.
	Explanation string `json:"explanation,omitempty"`
}

// =============================================================================
// MD.9.10 Signal Weights Configuration
// =============================================================================

// SignalWeights defines weights for each scoring signal.
type SignalWeights struct {
	// RRFWeight is the weight for RRF fusion scores.
	RRFWeight float64 `json:"rrf_weight"`

	// MemoryDecayWeight is the weight for memory activation/decay.
	MemoryDecayWeight float64 `json:"memory_decay_weight"`

	// ColdStartPriorWeight is the weight for cold-start priors.
	ColdStartPriorWeight float64 `json:"cold_start_prior_weight"`

	// PageRankWeight is the weight for PageRank scores.
	PageRankWeight float64 `json:"pagerank_weight"`

	// ClusterCoeffWeight is the weight for clustering coefficient.
	ClusterCoeffWeight float64 `json:"cluster_coeff_weight"`

	// RecencyWeight is the weight for recency scores.
	RecencyWeight float64 `json:"recency_weight"`

	// FrequencyWeight is the weight for frequency scores.
	FrequencyWeight float64 `json:"frequency_weight"`
}

// DefaultSignalWeights returns empirically-tuned default weights.
func DefaultSignalWeights() *SignalWeights {
	return &SignalWeights{
		RRFWeight:            0.35,
		MemoryDecayWeight:    0.25,
		ColdStartPriorWeight: 0.15,
		PageRankWeight:       0.10,
		ClusterCoeffWeight:   0.05,
		RecencyWeight:        0.05,
		FrequencyWeight:      0.05,
	}
}

// Normalize adjusts weights to sum to 1.0.
func (sw *SignalWeights) Normalize() *SignalWeights {
	sum := sw.RRFWeight + sw.MemoryDecayWeight + sw.ColdStartPriorWeight +
		sw.PageRankWeight + sw.ClusterCoeffWeight + sw.RecencyWeight + sw.FrequencyWeight

	if sum == 0 {
		return DefaultSignalWeights()
	}

	return &SignalWeights{
		RRFWeight:            sw.RRFWeight / sum,
		MemoryDecayWeight:    sw.MemoryDecayWeight / sum,
		ColdStartPriorWeight: sw.ColdStartPriorWeight / sum,
		PageRankWeight:       sw.PageRankWeight / sum,
		ClusterCoeffWeight:   sw.ClusterCoeffWeight / sum,
		RecencyWeight:        sw.RecencyWeight / sum,
		FrequencyWeight:      sw.FrequencyWeight / sum,
	}
}

// ToMap converts weights to a map keyed by signal type.
func (sw *SignalWeights) ToMap() map[SignalType]float64 {
	return map[SignalType]float64{
		SignalRRF:            sw.RRFWeight,
		SignalMemoryDecay:    sw.MemoryDecayWeight,
		SignalColdStartPrior: sw.ColdStartPriorWeight,
		SignalPageRank:       sw.PageRankWeight,
		SignalClusterCoeff:   sw.ClusterCoeffWeight,
		SignalRecency:        sw.RecencyWeight,
		SignalFrequency:      sw.FrequencyWeight,
	}
}

// Clone creates a copy of the weights.
func (sw *SignalWeights) Clone() *SignalWeights {
	return &SignalWeights{
		RRFWeight:            sw.RRFWeight,
		MemoryDecayWeight:    sw.MemoryDecayWeight,
		ColdStartPriorWeight: sw.ColdStartPriorWeight,
		PageRankWeight:       sw.PageRankWeight,
		ClusterCoeffWeight:   sw.ClusterCoeffWeight,
		RecencyWeight:        sw.RecencyWeight,
		FrequencyWeight:      sw.FrequencyWeight,
	}
}

// =============================================================================
// MD.9.10 PriorBlender
// =============================================================================

// PriorBlender handles blending between cold-start and warm (learned) scores.
// It uses a configurable transition from pure cold-start to pure warm based
// on the number of access traces for a node.
type PriorBlender struct {
	// MinTracesForWarm is the minimum trace count to start warm blending.
	MinTracesForWarm int

	// FullWarmTraces is the trace count for full warm scoring.
	FullWarmTraces int
}

// DefaultPriorBlender creates a PriorBlender with sensible defaults.
func DefaultPriorBlender() *PriorBlender {
	return &PriorBlender{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
	}
}

// ComputeBlendRatio calculates the ratio of warm to cold scoring.
// Returns 0 for pure cold, 1 for pure warm, and linear interpolation between.
func (pb *PriorBlender) ComputeBlendRatio(traceCount int) float64 {
	if traceCount < pb.MinTracesForWarm {
		return 0.0 // Pure cold score
	}

	if traceCount >= pb.FullWarmTraces {
		return 1.0 // Pure warm score
	}

	// Linear interpolation
	rangeVal := float64(pb.FullWarmTraces - pb.MinTracesForWarm)
	progress := float64(traceCount - pb.MinTracesForWarm)

	return progress / rangeVal
}

// Blend combines cold and warm scores based on the blend ratio.
func (pb *PriorBlender) Blend(coldScore, warmScore float64, traceCount int) float64 {
	blendRatio := pb.ComputeBlendRatio(traceCount)
	return (1.0-blendRatio)*coldScore + blendRatio*warmScore
}

// =============================================================================
// MD.9.10 HybridScorer
// =============================================================================

// HybridScorerConfig configures the HybridScorer.
type HybridScorerConfig struct {
	// Weights for each signal.
	Weights *SignalWeights

	// PriorBlender for cold/warm blending.
	Blender *PriorBlender

	// ColdStartEnabled controls whether cold-start priors are used.
	ColdStartEnabled bool

	// DisabledSignals is a set of signals to exclude from scoring.
	DisabledSignals map[SignalType]bool

	// UpdateConfig for Bayesian weight learning.
	UpdateConfig *handoff.UpdateConfig
}

// DefaultHybridScorerConfig returns a default configuration.
func DefaultHybridScorerConfig() *HybridScorerConfig {
	return &HybridScorerConfig{
		Weights:          DefaultSignalWeights(),
		Blender:          DefaultPriorBlender(),
		ColdStartEnabled: true,
		DisabledSignals:  make(map[SignalType]bool),
		UpdateConfig:     handoff.DefaultUpdateConfig(),
	}
}

// HybridScorer combines multiple scoring signals with cold-start support.
// Thread-safe for concurrent access.
type HybridScorer struct {
	// Configuration
	config *HybridScorerConfig

	// Dependencies (interfaces to avoid import cycles)
	memoryAccessor      MemoryAccessor
	coldPriorCalc       ColdStartPriorCalculator
	graphSignals        GraphSignalsProvider

	// Learned weights for adaptive scoring
	learnedWeights   map[SignalType]*handoff.LearnedWeight

	// Thread safety
	mu sync.RWMutex
}

// NewHybridScorer creates a new HybridScorer with the given dependencies.
func NewHybridScorer(
	memoryAccessor MemoryAccessor,
	coldPriorCalc ColdStartPriorCalculator,
	graphSignals GraphSignalsProvider,
) *HybridScorer {
	return NewHybridScorerWithConfig(
		memoryAccessor,
		coldPriorCalc,
		graphSignals,
		DefaultHybridScorerConfig(),
	)
}

// NewHybridScorerWithConfig creates a HybridScorer with custom configuration.
func NewHybridScorerWithConfig(
	memoryAccessor MemoryAccessor,
	coldPriorCalc ColdStartPriorCalculator,
	graphSignals GraphSignalsProvider,
	config *HybridScorerConfig,
) *HybridScorer {
	if config == nil {
		config = DefaultHybridScorerConfig()
	}
	if config.Weights == nil {
		config.Weights = DefaultSignalWeights()
	}
	if config.Blender == nil {
		config.Blender = DefaultPriorBlender()
	}
	if config.DisabledSignals == nil {
		config.DisabledSignals = make(map[SignalType]bool)
	}
	if config.UpdateConfig == nil {
		config.UpdateConfig = handoff.DefaultUpdateConfig()
	}

	hs := &HybridScorer{
		config:          config,
		memoryAccessor:  memoryAccessor,
		coldPriorCalc:   coldPriorCalc,
		graphSignals:    graphSignals,
		learnedWeights:  make(map[SignalType]*handoff.LearnedWeight),
	}

	// Initialize learned weights with priors from config weights
	hs.initializeLearnedWeights()

	return hs
}

// initializeLearnedWeights sets up Beta distributions for each signal weight.
func (hs *HybridScorer) initializeLearnedWeights() {
	weightsMap := hs.config.Weights.ToMap()

	for signal, weight := range weightsMap {
		// Beta distribution centered at the configured weight
		// With moderate strength (sum of alpha+beta = 10)
		alpha := weight * 10.0
		beta := (1.0 - weight) * 10.0
		if alpha < 1.0 {
			alpha = 1.0
		}
		if beta < 1.0 {
			beta = 1.0
		}
		hs.learnedWeights[signal] = handoff.NewLearnedWeight(alpha, beta)
	}
}

// Score computes scored results for the given hybrid results.
// Combines RRF fusion scores with memory decay and cold-start priors.
func (hs *HybridScorer) Score(
	ctx context.Context,
	results []HybridResult,
	query *HybridQuery,
	d domain.Domain,
) ([]ScoreResult, error) {
	if len(results) == 0 {
		return []ScoreResult{}, nil
	}

	hs.mu.RLock()
	config := hs.config
	hs.mu.RUnlock()

	now := time.Now().UTC()
	scored := make([]ScoreResult, 0, len(results))

	for _, result := range results {
		scoreResult, err := hs.scoreResult(ctx, result, d, now, config)
		if err != nil {
			// Log error but continue with other results
			scoreResult = ScoreResult{
				Result:     result,
				FinalScore: result.Score, // Fall back to RRF score
				IsColdStart: false,
			}
		}
		scored = append(scored, scoreResult)
	}

	// Sort by final score descending
	hs.sortByScore(scored)

	return scored, nil
}

// scoreResult computes the score for a single result.
func (hs *HybridScorer) scoreResult(
	ctx context.Context,
	result HybridResult,
	d domain.Domain,
	now time.Time,
	config *HybridScorerConfig,
) (ScoreResult, error) {
	contributions := make([]SignalContribution, 0, 7)
	weights := config.Weights.Normalize()
	weightsMap := weights.ToMap()

	// Get memory data for this node
	var traceCount int
	var activation float64
	var lastAccess time.Time
	hasMemory := false

	if hs.memoryAccessor != nil {
		var err error
		traceCount, err = hs.memoryAccessor.GetAccessCount(ctx, result.ID, d)
		if err == nil && traceCount > 0 {
			hasMemory = true
			activation, _ = hs.memoryAccessor.GetActivation(ctx, result.ID, d, now)
			lastAccess, _ = hs.memoryAccessor.GetLastAccessTime(ctx, result.ID, d)
		}
	}

	// Compute blend ratio
	blendRatio := config.Blender.ComputeBlendRatio(traceCount)
	isColdStart := blendRatio < 1.0 && config.ColdStartEnabled

	// 1. RRF Score (always used)
	if !config.DisabledSignals[SignalRRF] {
		rrfContrib := SignalContribution{
			Signal:       SignalRRF,
			RawValue:     result.Score,
			Weight:       weightsMap[SignalRRF],
			Contribution: result.Score * weightsMap[SignalRRF],
		}
		contributions = append(contributions, rrfContrib)
	}

	// 2. Memory Decay Score (warm)
	if !config.DisabledSignals[SignalMemoryDecay] && hasMemory {
		// Normalize activation to [0,1] using sigmoid
		normalizedActivation := 1.0 / (1.0 + math.Exp(-activation))

		// Apply blend ratio for warm contribution
		weightedActivation := normalizedActivation * blendRatio

		memContrib := SignalContribution{
			Signal:       SignalMemoryDecay,
			RawValue:     normalizedActivation,
			Weight:       weightsMap[SignalMemoryDecay],
			Contribution: weightedActivation * weightsMap[SignalMemoryDecay],
		}
		contributions = append(contributions, memContrib)
	}

	// 3. Cold-Start Prior Score
	if !config.DisabledSignals[SignalColdStartPrior] && isColdStart {
		coldScore := hs.computeColdStartPrior(result.ID)
		// Apply inverse blend ratio for cold contribution
		weightedCold := coldScore * (1.0 - blendRatio)

		coldContrib := SignalContribution{
			Signal:       SignalColdStartPrior,
			RawValue:     coldScore,
			Weight:       weightsMap[SignalColdStartPrior],
			Contribution: weightedCold * weightsMap[SignalColdStartPrior],
		}
		contributions = append(contributions, coldContrib)
	}

	// 4. PageRank Score
	if !config.DisabledSignals[SignalPageRank] && hs.graphSignals != nil {
		pageRankScore := hs.graphSignals.GetPageRank(result.ID)
		if pageRankScore > 0 {
			prContrib := SignalContribution{
				Signal:       SignalPageRank,
				RawValue:     pageRankScore,
				Weight:       weightsMap[SignalPageRank],
				Contribution: pageRankScore * weightsMap[SignalPageRank],
			}
			contributions = append(contributions, prContrib)
		}
	}

	// 5. Clustering Coefficient Score
	if !config.DisabledSignals[SignalClusterCoeff] && hs.graphSignals != nil {
		clusterScore := hs.graphSignals.GetClusteringCoefficient(result.ID)
		if clusterScore > 0 {
			ccContrib := SignalContribution{
				Signal:       SignalClusterCoeff,
				RawValue:     clusterScore,
				Weight:       weightsMap[SignalClusterCoeff],
				Contribution: clusterScore * weightsMap[SignalClusterCoeff],
			}
			contributions = append(contributions, ccContrib)
		}
	}

	// 6. Recency Score
	if !config.DisabledSignals[SignalRecency] && hasMemory && !lastAccess.IsZero() {
		recencyScore := hs.computeRecencyScore(lastAccess, now)
		recContrib := SignalContribution{
			Signal:       SignalRecency,
			RawValue:     recencyScore,
			Weight:       weightsMap[SignalRecency],
			Contribution: recencyScore * weightsMap[SignalRecency],
		}
		contributions = append(contributions, recContrib)
	}

	// 7. Frequency Score
	if !config.DisabledSignals[SignalFrequency] && hasMemory {
		freqScore := hs.computeFrequencyScore(traceCount)
		freqContrib := SignalContribution{
			Signal:       SignalFrequency,
			RawValue:     freqScore,
			Weight:       weightsMap[SignalFrequency],
			Contribution: freqScore * weightsMap[SignalFrequency],
		}
		contributions = append(contributions, freqContrib)
	}

	// Compute final score
	var finalScore float64
	for _, c := range contributions {
		finalScore += c.Contribution
	}

	// Generate explanation
	explanation := hs.generateExplanation(isColdStart, blendRatio, traceCount, contributions)

	return ScoreResult{
		Result:        result,
		FinalScore:    finalScore,
		Contributions: contributions,
		IsColdStart:   isColdStart,
		BlendRatio:    blendRatio,
		AccessCount:   traceCount,
		Explanation:   explanation,
	}, nil
}

// computeColdStartPrior computes the cold-start prior for a node.
func (hs *HybridScorer) computeColdStartPrior(nodeID string) float64 {
	if hs.coldPriorCalc == nil {
		return 0.5 // Default prior
	}
	return hs.coldPriorCalc.ComputePrior(nodeID)
}

// computeRecencyScore computes recency based on last access.
func (hs *HybridScorer) computeRecencyScore(lastAccess, now time.Time) float64 {
	hoursSince := now.Sub(lastAccess).Hours()

	// Gentle decay: 1 / (1 + hours/24)
	// Recent (0 hours) = 1.0, 24 hours = 0.5, 48 hours = 0.33
	return 1.0 / (1.0 + hoursSince/24.0)
}

// computeFrequencyScore computes frequency based on access count.
func (hs *HybridScorer) computeFrequencyScore(accessCount int) float64 {
	// Log-normalized frequency: log(1 + count) / log(101)
	// This maps 0 accesses to 0 and 100 accesses to ~1.0
	return math.Log(1.0+float64(accessCount)) / math.Log(101.0)
}

// generateExplanation creates a human-readable explanation.
func (hs *HybridScorer) generateExplanation(
	isColdStart bool,
	blendRatio float64,
	traceCount int,
	contributions []SignalContribution,
) string {
	if isColdStart && blendRatio == 0 {
		return "Pure cold-start scoring (no access history)"
	}
	if !isColdStart && blendRatio == 1.0 {
		return "Pure warm scoring (sufficient access history)"
	}
	if isColdStart {
		return "Blended scoring (transitioning from cold to warm)"
	}
	return "Standard scoring"
}

// sortByScore sorts results by FinalScore in descending order.
func (hs *HybridScorer) sortByScore(results []ScoreResult) {
	for i := 1; i < len(results); i++ {
		j := i
		for j > 0 && results[j].FinalScore > results[j-1].FinalScore {
			results[j], results[j-1] = results[j-1], results[j]
			j--
		}
	}
}

// =============================================================================
// MD.9.10 HybridScorer Configuration Methods
// =============================================================================

// GetSignalWeights returns the current signal weights as a map.
func (hs *HybridScorer) GetSignalWeights() map[SignalType]float64 {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	return hs.config.Weights.ToMap()
}

// SetSignalWeights updates the signal weights.
func (hs *HybridScorer) SetSignalWeights(weights *SignalWeights) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if weights != nil {
		hs.config.Weights = weights.Clone()
	}
}

// SetColdStartEnabled enables or disables cold-start priors.
func (hs *HybridScorer) SetColdStartEnabled(enabled bool) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	hs.config.ColdStartEnabled = enabled
}

// IsColdStartEnabled returns whether cold-start is enabled.
func (hs *HybridScorer) IsColdStartEnabled() bool {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	return hs.config.ColdStartEnabled
}

// DisableSignal disables a specific scoring signal.
func (hs *HybridScorer) DisableSignal(signal SignalType) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	hs.config.DisabledSignals[signal] = true
}

// EnableSignal enables a specific scoring signal.
func (hs *HybridScorer) EnableSignal(signal SignalType) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	delete(hs.config.DisabledSignals, signal)
}

// IsSignalEnabled returns whether a signal is enabled.
func (hs *HybridScorer) IsSignalEnabled(signal SignalType) bool {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	return !hs.config.DisabledSignals[signal]
}

// SetBlenderConfig updates the prior blender configuration.
func (hs *HybridScorer) SetBlenderConfig(minTraces, fullTraces int) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if minTraces > 0 {
		hs.config.Blender.MinTracesForWarm = minTraces
	}
	if fullTraces > 0 {
		hs.config.Blender.FullWarmTraces = fullTraces
	}
}

// GetBlenderConfig returns the current blender configuration.
func (hs *HybridScorer) GetBlenderConfig() (minTraces, fullTraces int) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	return hs.config.Blender.MinTracesForWarm, hs.config.Blender.FullWarmTraces
}

// =============================================================================
// MD.9.10 Learning Integration
// =============================================================================

// RecordFeedback records user feedback to update learned weights.
// wasUseful indicates whether the retrieved result was helpful.
func (hs *HybridScorer) RecordFeedback(
	ctx context.Context,
	nodeID string,
	wasUseful bool,
	contributions []SignalContribution,
) error {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	// Update learned weights based on which signals contributed
	observation := 0.0
	if wasUseful {
		observation = 1.0
	}

	for _, contrib := range contributions {
		if lw, ok := hs.learnedWeights[contrib.Signal]; ok {
			// Weight the observation by the signal's contribution
			weightedObs := observation
			if contrib.Contribution > 0 {
				// Signals that contributed more get stronger updates
				weightedObs = observation * (0.5 + 0.5*contrib.Contribution)
			}
			lw.Update(weightedObs, hs.config.UpdateConfig)
		}
	}

	return nil
}

// GetLearnedWeights returns the current learned weight means.
func (hs *HybridScorer) GetLearnedWeights() map[SignalType]float64 {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	result := make(map[SignalType]float64)
	for signal, lw := range hs.learnedWeights {
		result[signal] = lw.Mean()
	}
	return result
}

// GetLearnedWeightConfidences returns confidence levels for learned weights.
func (hs *HybridScorer) GetLearnedWeightConfidences() map[SignalType]float64 {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	result := make(map[SignalType]float64)
	for signal, lw := range hs.learnedWeights {
		result[signal] = lw.Confidence()
	}
	return result
}

// =============================================================================
// MD.9.10 Accessors
// =============================================================================

// MemoryAccessor returns the underlying memory accessor.
func (hs *HybridScorer) MemoryAccessor() MemoryAccessor {
	return hs.memoryAccessor
}

// ColdPriorCalc returns the cold prior calculator.
func (hs *HybridScorer) ColdPriorCalc() ColdStartPriorCalculator {
	return hs.coldPriorCalc
}

// GraphSignals returns the graph signals provider.
func (hs *HybridScorer) GraphSignals() GraphSignalsProvider {
	return hs.graphSignals
}

// Config returns a copy of the current configuration.
func (hs *HybridScorer) Config() *HybridScorerConfig {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	return &HybridScorerConfig{
		Weights:          hs.config.Weights.Clone(),
		Blender:          &PriorBlender{
			MinTracesForWarm: hs.config.Blender.MinTracesForWarm,
			FullWarmTraces:   hs.config.Blender.FullWarmTraces,
		},
		ColdStartEnabled: hs.config.ColdStartEnabled,
		DisabledSignals:  copyDisabledSignals(hs.config.DisabledSignals),
		UpdateConfig:     hs.config.UpdateConfig,
	}
}

// copyDisabledSignals creates a copy of the disabled signals map.
func copyDisabledSignals(m map[SignalType]bool) map[SignalType]bool {
	result := make(map[SignalType]bool)
	for k, v := range m {
		result[k] = v
	}
	return result
}
