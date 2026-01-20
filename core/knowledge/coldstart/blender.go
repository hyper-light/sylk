package coldstart

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/domain"
)

// =============================================================================
// MD.9.5 PriorBlender
// =============================================================================

// BlendCurve defines the type of curve used to transition from cold to warm scoring.
type BlendCurve int

const (
	// BlendCurveLinear uses a linear transition from cold to warm scoring.
	BlendCurveLinear BlendCurve = iota

	// BlendCurveSigmoid uses a sigmoid (S-curve) transition for smoother blending.
	BlendCurveSigmoid

	// BlendCurveStep uses a step function that switches abruptly at the midpoint.
	BlendCurveStep
)

// String returns a string representation of the BlendCurve.
func (bc BlendCurve) String() string {
	switch bc {
	case BlendCurveLinear:
		return "linear"
	case BlendCurveSigmoid:
		return "sigmoid"
	case BlendCurveStep:
		return "step"
	default:
		return "unknown"
	}
}

// BlendConfig configures the behavior of the PriorBlender.
type BlendConfig struct {
	// MinTracesForWarm is the trace count at which blending begins.
	// Below this threshold, pure cold scoring is used.
	MinTracesForWarm int

	// FullWarmTraces is the trace count at which pure warm (ACT-R) scoring is used.
	// At or above this threshold, cold scores are not used.
	FullWarmTraces int

	// Curve determines the shape of the blend transition.
	Curve BlendCurve

	// SigmoidSteepness controls how steep the sigmoid curve is (default: 1.0).
	// Higher values make the transition sharper.
	SigmoidSteepness float64
}

// DefaultBlendConfig returns a BlendConfig with sensible defaults.
func DefaultBlendConfig() *BlendConfig {
	return &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveLinear,
		SigmoidSteepness: 1.0,
	}
}

// Validate checks that the configuration is valid.
// Returns an error if the configuration is invalid.
func (bc *BlendConfig) Validate() error {
	if bc.MinTracesForWarm < 0 {
		return ErrInvalidMinTraces
	}
	if bc.FullWarmTraces < bc.MinTracesForWarm {
		return ErrInvalidFullWarmTraces
	}
	if bc.SigmoidSteepness <= 0 {
		return ErrInvalidSigmoidSteepness
	}
	return nil
}

// DomainBlendConfig contains domain-specific blend parameters.
type DomainBlendConfig struct {
	// MinTracesForWarm overrides the global minimum if set (0 means use global).
	MinTracesForWarm int

	// FullWarmTraces overrides the global full warm threshold if set (0 means use global).
	FullWarmTraces int

	// Curve overrides the global blend curve if set.
	Curve *BlendCurve

	// ColdWeight is a domain-specific weight for cold scores (default: 1.0).
	// Values < 1 reduce cold score influence, > 1 increase it.
	ColdWeight float64

	// WarmWeight is a domain-specific weight for warm scores (default: 1.0).
	// Values < 1 reduce warm score influence, > 1 increase it.
	WarmWeight float64
}

// DefaultDomainBlendConfig returns default domain blend parameters.
func DefaultDomainBlendConfig() *DomainBlendConfig {
	return &DomainBlendConfig{
		MinTracesForWarm: 0, // Use global
		FullWarmTraces:   0, // Use global
		Curve:            nil,
		ColdWeight:       1.0,
		WarmWeight:       1.0,
	}
}

// BlendError represents errors from the PriorBlender.
type BlendError struct {
	msg string
}

func (e BlendError) Error() string {
	return e.msg
}

// Sentinel errors for blend configuration validation.
var (
	ErrInvalidMinTraces        = BlendError{msg: "min traces for warm must be >= 0"}
	ErrInvalidFullWarmTraces   = BlendError{msg: "full warm traces must be >= min traces for warm"}
	ErrInvalidSigmoidSteepness = BlendError{msg: "sigmoid steepness must be > 0"}
)

// =============================================================================
// BlenderMemoryInfo - Interface for memory info needed by blender
// =============================================================================

// BlenderMemoryInfo provides the memory information needed by PriorBlender.
// This interface allows the blender to work without directly importing the memory package.
type BlenderMemoryInfo interface {
	// TraceCount returns the number of access traces for the memory.
	TraceCount() int

	// Activation returns the current activation value at the given time.
	Activation(now time.Time) float64
}

// BlenderMemoryStore provides memory retrieval for the PriorBlender.
// This interface is satisfied by MemoryStoreInterface but defined here
// to allow blender to work independently.
type BlenderMemoryStore interface {
	// GetBlenderMemoryInfo retrieves memory info for a node.
	// Returns nil, nil if no memory exists.
	GetBlenderMemoryInfo(ctx context.Context, nodeID string, d domain.Domain) (BlenderMemoryInfo, error)
}

// =============================================================================
// PriorBlender Implementation
// =============================================================================

// PriorBlender blends cold-start priors with ACT-R memory scores.
// It provides configurable blend curves and supports per-domain parameters.
// Thread-safe for concurrent access.
type PriorBlender struct {
	config       *BlendConfig
	domainConfig map[domain.Domain]*DomainBlendConfig

	// Dependencies
	coldCalculator *ColdPriorCalculator
	memoryStore    BlenderMemoryStore

	mu sync.RWMutex
}

// NewPriorBlender creates a new PriorBlender with the given configuration.
// If config is nil, DefaultBlendConfig is used.
func NewPriorBlender(config *BlendConfig) *PriorBlender {
	if config == nil {
		config = DefaultBlendConfig()
	}

	return &PriorBlender{
		config:       config,
		domainConfig: make(map[domain.Domain]*DomainBlendConfig),
	}
}

// NewPriorBlenderWithDeps creates a PriorBlender with cold calculator and memory store.
func NewPriorBlenderWithDeps(
	config *BlendConfig,
	coldCalculator *ColdPriorCalculator,
	memoryStore BlenderMemoryStore,
) *PriorBlender {
	blender := NewPriorBlender(config)
	blender.coldCalculator = coldCalculator
	blender.memoryStore = memoryStore
	return blender
}

// SetColdCalculator sets the cold prior calculator dependency.
func (pb *PriorBlender) SetColdCalculator(calc *ColdPriorCalculator) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.coldCalculator = calc
}

// SetMemoryStore sets the memory store dependency.
func (pb *PriorBlender) SetMemoryStore(store BlenderMemoryStore) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.memoryStore = store
}

// SetDomainConfig sets blend parameters for a specific domain.
func (pb *PriorBlender) SetDomainConfig(d domain.Domain, config *DomainBlendConfig) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if config == nil {
		delete(pb.domainConfig, d)
		return
	}

	pb.domainConfig[d] = config
}

// GetDomainConfig returns the blend parameters for a specific domain.
// Returns nil if no domain-specific configuration is set.
func (pb *PriorBlender) GetDomainConfig(d domain.Domain) *DomainBlendConfig {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	return pb.domainConfig[d]
}

// Config returns a copy of the current blend configuration.
func (pb *PriorBlender) Config() *BlendConfig {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	return &BlendConfig{
		MinTracesForWarm: pb.config.MinTracesForWarm,
		FullWarmTraces:   pb.config.FullWarmTraces,
		Curve:            pb.config.Curve,
		SigmoidSteepness: pb.config.SigmoidSteepness,
	}
}

// =============================================================================
// Core Blending Methods
// =============================================================================

// BlendScores blends a cold-start score with a warm (ACT-R) score based on
// the trace count. Returns the blended score.
//
// Formula: score = cold * (1-ratio) + warm * ratio
//
// The ratio is determined by the trace count and the configured blend curve.
func (pb *PriorBlender) BlendScores(coldScore, warmScore float64, traceCount int) float64 {
	ratio := pb.ComputeBlendRatio(traceCount)
	return pb.blendWithRatio(coldScore, warmScore, ratio)
}

// BlendScoresForDomain blends scores using domain-specific parameters.
func (pb *PriorBlender) BlendScoresForDomain(
	coldScore, warmScore float64,
	traceCount int,
	d domain.Domain,
) float64 {
	pb.mu.RLock()
	domainCfg := pb.domainConfig[d]
	pb.mu.RUnlock()

	// Compute ratio with domain-specific thresholds if available
	ratio := pb.computeBlendRatioForDomain(traceCount, domainCfg)

	// Apply domain-specific weights if available
	if domainCfg != nil {
		coldScore *= domainCfg.ColdWeight
		warmScore *= domainCfg.WarmWeight
	}

	return pb.blendWithRatio(coldScore, warmScore, ratio)
}

// blendWithRatio performs the actual blending calculation.
func (pb *PriorBlender) blendWithRatio(coldScore, warmScore, ratio float64) float64 {
	// Formula: score = cold * (1-ratio) + warm * ratio
	blended := coldScore*(1.0-ratio) + warmScore*ratio

	// Clamp to [0,1]
	return clamp(blended, 0.0, 1.0)
}

// ComputeBlendRatio calculates the blend ratio [0,1] based on trace count.
// - Returns 0 (pure cold) when traceCount < MinTracesForWarm
// - Returns 1 (pure warm) when traceCount >= FullWarmTraces
// - Returns intermediate value based on the configured curve
func (pb *PriorBlender) ComputeBlendRatio(traceCount int) float64 {
	pb.mu.RLock()
	config := pb.config
	pb.mu.RUnlock()

	return pb.computeRatioWithConfig(traceCount, config, nil)
}

// computeBlendRatioForDomain computes ratio with optional domain overrides.
func (pb *PriorBlender) computeBlendRatioForDomain(
	traceCount int,
	domainCfg *DomainBlendConfig,
) float64 {
	pb.mu.RLock()
	config := pb.config
	pb.mu.RUnlock()

	return pb.computeRatioWithConfig(traceCount, config, domainCfg)
}

// computeRatioWithConfig performs the actual ratio calculation.
func (pb *PriorBlender) computeRatioWithConfig(
	traceCount int,
	config *BlendConfig,
	domainCfg *DomainBlendConfig,
) float64 {
	// Determine effective thresholds
	minTraces := config.MinTracesForWarm
	fullTraces := config.FullWarmTraces
	curve := config.Curve

	// Apply domain overrides if present
	if domainCfg != nil {
		if domainCfg.MinTracesForWarm > 0 {
			minTraces = domainCfg.MinTracesForWarm
		}
		if domainCfg.FullWarmTraces > 0 {
			fullTraces = domainCfg.FullWarmTraces
		}
		if domainCfg.Curve != nil {
			curve = *domainCfg.Curve
		}
	}

	// Boundary conditions
	if traceCount < minTraces {
		return 0.0 // Pure cold
	}
	if traceCount >= fullTraces {
		return 1.0 // Pure warm
	}

	// Calculate normalized position [0,1]
	rangeVal := float64(fullTraces - minTraces)
	if rangeVal == 0 {
		return 1.0
	}
	progress := float64(traceCount - minTraces)
	normalized := progress / rangeVal

	// Apply blend curve
	switch curve {
	case BlendCurveSigmoid:
		return pb.sigmoidCurve(normalized, config.SigmoidSteepness)
	case BlendCurveStep:
		return pb.stepCurve(normalized)
	case BlendCurveLinear:
		fallthrough
	default:
		return normalized // Linear is just the normalized value
	}
}

// sigmoidCurve applies a sigmoid transformation to a normalized value.
// The sigmoid is centered at 0.5 and maps [0,1] -> [~0, ~1].
func (pb *PriorBlender) sigmoidCurve(normalized, steepness float64) float64 {
	// Transform to [-6, 6] range for sigmoid, scaled by steepness
	// sigmoid(x) = 1 / (1 + e^(-x))
	x := (normalized - 0.5) * 12.0 * steepness

	// Prevent overflow for extreme values
	if x > 20 {
		return 1.0
	}
	if x < -20 {
		return 0.0
	}

	return 1.0 / (1.0 + math.Exp(-x))
}

// stepCurve applies a step function at the midpoint.
func (pb *PriorBlender) stepCurve(normalized float64) float64 {
	if normalized < 0.5 {
		return 0.0
	}
	return 1.0
}

// =============================================================================
// GetEffectiveScore Method
// =============================================================================

// GetEffectiveScore computes the final blended score for a node by:
// 1. Computing the cold-start prior from the node signals
// 2. Retrieving the ACT-R memory activation from the memory store
// 3. Blending the two scores based on trace count
//
// Requires coldCalculator and memoryStore to be set.
func (pb *PriorBlender) GetEffectiveScore(
	ctx context.Context,
	nodeID string,
	coldSignals *NodeColdStartSignals,
	d domain.Domain,
) (float64, error) {
	pb.mu.RLock()
	coldCalc := pb.coldCalculator
	memStore := pb.memoryStore
	pb.mu.RUnlock()

	// Compute cold score
	var coldScore float64
	if coldCalc != nil && coldSignals != nil {
		coldScore = coldCalc.ComputeColdPrior(coldSignals)
	}

	// Get warm score from memory store
	var warmScore float64
	var traceCount int

	if memStore != nil {
		memInfo, err := memStore.GetBlenderMemoryInfo(ctx, nodeID, d)
		if err != nil {
			return 0, err
		}
		if memInfo != nil {
			traceCount = memInfo.TraceCount()
			if traceCount > 0 {
				// Convert ACT-R activation to [0,1] score
				activation := memInfo.Activation(time.Now())
				warmScore = pb.activationToScore(activation)
			}
		}
	}

	// Blend the scores
	return pb.BlendScoresForDomain(coldScore, warmScore, traceCount, d), nil
}

// GetEffectiveScoreWithWarm computes the blended score when the warm score
// is already known (avoids querying the memory store).
func (pb *PriorBlender) GetEffectiveScoreWithWarm(
	coldSignals *NodeColdStartSignals,
	warmScore float64,
	traceCount int,
	d domain.Domain,
) float64 {
	pb.mu.RLock()
	coldCalc := pb.coldCalculator
	pb.mu.RUnlock()

	var coldScore float64
	if coldCalc != nil && coldSignals != nil {
		coldScore = coldCalc.ComputeColdPrior(coldSignals)
	}

	return pb.BlendScoresForDomain(coldScore, warmScore, traceCount, d)
}

// activationToScore converts ACT-R activation to a [0,1] score.
// Uses a sigmoid transformation centered around typical activation values.
func (pb *PriorBlender) activationToScore(activation float64) float64 {
	if activation <= -100 {
		return 0.0
	}

	// Sigmoid: 1 / (1 + exp(-activation))
	// Shift by +2 to center around typical values
	shifted := activation + 2.0

	if shifted > 20 {
		return 1.0
	}
	if shifted < -20 {
		return 0.0
	}

	return 1.0 / (1.0 + math.Exp(-shifted))
}

// =============================================================================
// Utility Methods
// =============================================================================

// GetRatioAtTraceCount returns the blend ratio for a given trace count,
// useful for debugging and visualization.
func (pb *PriorBlender) GetRatioAtTraceCount(traceCount int) float64 {
	return pb.ComputeBlendRatio(traceCount)
}

// GetRatioAtTraceCountForDomain returns the blend ratio for a domain.
func (pb *PriorBlender) GetRatioAtTraceCountForDomain(traceCount int, d domain.Domain) float64 {
	pb.mu.RLock()
	domainCfg := pb.domainConfig[d]
	pb.mu.RUnlock()

	return pb.computeBlendRatioForDomain(traceCount, domainCfg)
}

// IsPureCold returns true if the trace count results in pure cold scoring.
func (pb *PriorBlender) IsPureCold(traceCount int) bool {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	return traceCount < pb.config.MinTracesForWarm
}

// IsPureWarm returns true if the trace count results in pure warm scoring.
func (pb *PriorBlender) IsPureWarm(traceCount int) bool {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	return traceCount >= pb.config.FullWarmTraces
}

// IsBlending returns true if the trace count results in blended scoring.
func (pb *PriorBlender) IsBlending(traceCount int) bool {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	return traceCount >= pb.config.MinTracesForWarm && traceCount < pb.config.FullWarmTraces
}
