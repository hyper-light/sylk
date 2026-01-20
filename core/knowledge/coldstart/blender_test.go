package coldstart

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
)

// =============================================================================
// Test Helpers
// =============================================================================

// mockBlenderMemoryInfo implements BlenderMemoryInfo for testing.
type mockBlenderMemoryInfo struct {
	traceCount int
	activation float64
}

func (m *mockBlenderMemoryInfo) TraceCount() int {
	return m.traceCount
}

func (m *mockBlenderMemoryInfo) Activation(now time.Time) float64 {
	return m.activation
}

// mockBlenderMemoryStore implements BlenderMemoryStore for testing.
type mockBlenderMemoryStore struct {
	memories map[string]*mockBlenderMemoryInfo
	mu       sync.RWMutex
}

func newMockBlenderMemoryStore() *mockBlenderMemoryStore {
	return &mockBlenderMemoryStore{
		memories: make(map[string]*mockBlenderMemoryInfo),
	}
}

func (m *mockBlenderMemoryStore) GetBlenderMemoryInfo(
	ctx context.Context,
	nodeID string,
	d domain.Domain,
) (BlenderMemoryInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info := m.memories[nodeID]
	if info == nil {
		return nil, nil
	}
	return info, nil
}

func (m *mockBlenderMemoryStore) setMemory(nodeID string, traceCount int, activation float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.memories[nodeID] = &mockBlenderMemoryInfo{
		traceCount: traceCount,
		activation: activation,
	}
}

// =============================================================================
// BlendConfig Tests
// =============================================================================

func TestDefaultBlendConfig(t *testing.T) {
	config := DefaultBlendConfig()

	if config.MinTracesForWarm != 3 {
		t.Errorf("MinTracesForWarm = %d, want 3", config.MinTracesForWarm)
	}
	if config.FullWarmTraces != 15 {
		t.Errorf("FullWarmTraces = %d, want 15", config.FullWarmTraces)
	}
	if config.Curve != BlendCurveLinear {
		t.Errorf("Curve = %v, want BlendCurveLinear", config.Curve)
	}
	if config.SigmoidSteepness != 1.0 {
		t.Errorf("SigmoidSteepness = %v, want 1.0", config.SigmoidSteepness)
	}
}

func TestBlendConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *BlendConfig
		wantErr error
	}{
		{
			name:    "valid config",
			config:  DefaultBlendConfig(),
			wantErr: nil,
		},
		{
			name: "negative min traces",
			config: &BlendConfig{
				MinTracesForWarm: -1,
				FullWarmTraces:   10,
				SigmoidSteepness: 1.0,
			},
			wantErr: ErrInvalidMinTraces,
		},
		{
			name: "full warm less than min",
			config: &BlendConfig{
				MinTracesForWarm: 10,
				FullWarmTraces:   5,
				SigmoidSteepness: 1.0,
			},
			wantErr: ErrInvalidFullWarmTraces,
		},
		{
			name: "zero sigmoid steepness",
			config: &BlendConfig{
				MinTracesForWarm: 3,
				FullWarmTraces:   15,
				SigmoidSteepness: 0,
			},
			wantErr: ErrInvalidSigmoidSteepness,
		},
		{
			name: "negative sigmoid steepness",
			config: &BlendConfig{
				MinTracesForWarm: 3,
				FullWarmTraces:   15,
				SigmoidSteepness: -1.0,
			},
			wantErr: ErrInvalidSigmoidSteepness,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBlendCurve_String(t *testing.T) {
	tests := []struct {
		curve BlendCurve
		want  string
	}{
		{BlendCurveLinear, "linear"},
		{BlendCurveSigmoid, "sigmoid"},
		{BlendCurveStep, "step"},
		{BlendCurve(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.curve.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// PriorBlender Construction Tests
// =============================================================================

func TestNewPriorBlender(t *testing.T) {
	blender := NewPriorBlender(nil)

	if blender == nil {
		t.Fatal("NewPriorBlender returned nil")
	}

	config := blender.Config()
	if config.MinTracesForWarm != 3 {
		t.Errorf("MinTracesForWarm = %d, want 3", config.MinTracesForWarm)
	}
}

func TestNewPriorBlender_CustomConfig(t *testing.T) {
	config := &BlendConfig{
		MinTracesForWarm: 5,
		FullWarmTraces:   20,
		Curve:            BlendCurveSigmoid,
		SigmoidSteepness: 2.0,
	}

	blender := NewPriorBlender(config)

	got := blender.Config()
	if got.MinTracesForWarm != 5 {
		t.Errorf("MinTracesForWarm = %d, want 5", got.MinTracesForWarm)
	}
	if got.FullWarmTraces != 20 {
		t.Errorf("FullWarmTraces = %d, want 20", got.FullWarmTraces)
	}
	if got.Curve != BlendCurveSigmoid {
		t.Errorf("Curve = %v, want BlendCurveSigmoid", got.Curve)
	}
	if got.SigmoidSteepness != 2.0 {
		t.Errorf("SigmoidSteepness = %v, want 2.0", got.SigmoidSteepness)
	}
}

func TestNewPriorBlenderWithDeps(t *testing.T) {
	profile := NewCodebaseProfile()
	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)
	store := newMockBlenderMemoryStore()

	blender := NewPriorBlenderWithDeps(nil, calc, store)

	if blender == nil {
		t.Fatal("NewPriorBlenderWithDeps returned nil")
	}
}

// =============================================================================
// ComputeBlendRatio Tests - Linear Curve
// =============================================================================

func TestComputeBlendRatio_LinearCurve(t *testing.T) {
	config := &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveLinear,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	tests := []struct {
		name       string
		traceCount int
		wantRatio  float64
	}{
		{"zero traces", 0, 0.0},
		{"one trace", 1, 0.0},
		{"two traces", 2, 0.0},
		{"at min threshold", 3, 0.0},
		{"one above min", 4, 1.0 / 12.0},
		{"halfway", 9, 0.5},
		{"near max", 14, 11.0 / 12.0},
		{"at max threshold", 15, 1.0},
		{"above max", 20, 1.0},
		{"way above max", 100, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blender.ComputeBlendRatio(tt.traceCount)
			if math.Abs(got-tt.wantRatio) > 0.001 {
				t.Errorf("ComputeBlendRatio(%d) = %v, want %v",
					tt.traceCount, got, tt.wantRatio)
			}
		})
	}
}

func TestComputeBlendRatio_LinearCurve_CustomThresholds(t *testing.T) {
	config := &BlendConfig{
		MinTracesForWarm: 10,
		FullWarmTraces:   20,
		Curve:            BlendCurveLinear,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	tests := []struct {
		traceCount int
		wantRatio  float64
	}{
		{0, 0.0},
		{9, 0.0},
		{10, 0.0},
		{15, 0.5},
		{20, 1.0},
		{25, 1.0},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := blender.ComputeBlendRatio(tt.traceCount)
			if math.Abs(got-tt.wantRatio) > 0.001 {
				t.Errorf("ComputeBlendRatio(%d) = %v, want %v",
					tt.traceCount, got, tt.wantRatio)
			}
		})
	}
}

// =============================================================================
// ComputeBlendRatio Tests - Sigmoid Curve
// =============================================================================

func TestComputeBlendRatio_SigmoidCurve(t *testing.T) {
	config := &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveSigmoid,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	tests := []struct {
		name       string
		traceCount int
		minRatio   float64
		maxRatio   float64
	}{
		{"zero traces", 0, 0.0, 0.0},
		{"at min threshold", 3, 0.0, 0.01},
		{"early blend", 5, 0.01, 0.2},
		{"halfway", 9, 0.45, 0.55},
		{"late blend", 13, 0.8, 0.99},
		{"at max threshold", 15, 1.0, 1.0},
		{"above max", 20, 1.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blender.ComputeBlendRatio(tt.traceCount)
			if got < tt.minRatio || got > tt.maxRatio {
				t.Errorf("ComputeBlendRatio(%d) = %v, want in [%v, %v]",
					tt.traceCount, got, tt.minRatio, tt.maxRatio)
			}
		})
	}
}

func TestComputeBlendRatio_SigmoidSteepness(t *testing.T) {
	// Create two blenders with different steepness
	configLow := &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveSigmoid,
		SigmoidSteepness: 0.5,
	}
	configHigh := &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveSigmoid,
		SigmoidSteepness: 2.0,
	}

	blenderLow := NewPriorBlender(configLow)
	blenderHigh := NewPriorBlender(configHigh)

	// At the midpoint (trace count = 9), both should be ~0.5
	midpoint := 9
	lowMid := blenderLow.ComputeBlendRatio(midpoint)
	highMid := blenderHigh.ComputeBlendRatio(midpoint)

	if math.Abs(lowMid-0.5) > 0.05 {
		t.Errorf("Low steepness midpoint = %v, want ~0.5", lowMid)
	}
	if math.Abs(highMid-0.5) > 0.05 {
		t.Errorf("High steepness midpoint = %v, want ~0.5", highMid)
	}

	// Away from midpoint, high steepness should be more extreme
	earlyPoint := 5
	lowEarly := blenderLow.ComputeBlendRatio(earlyPoint)
	highEarly := blenderHigh.ComputeBlendRatio(earlyPoint)

	// High steepness should be closer to 0 at early points
	if highEarly >= lowEarly {
		t.Errorf("High steepness early = %v should be < low steepness = %v",
			highEarly, lowEarly)
	}
}

// =============================================================================
// ComputeBlendRatio Tests - Step Curve
// =============================================================================

func TestComputeBlendRatio_StepCurve(t *testing.T) {
	config := &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveStep,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	tests := []struct {
		name       string
		traceCount int
		wantRatio  float64
	}{
		{"zero traces", 0, 0.0},
		{"at min threshold", 3, 0.0},
		{"before midpoint", 8, 0.0},
		{"at midpoint", 9, 1.0},
		{"after midpoint", 10, 1.0},
		{"at max threshold", 15, 1.0},
		{"above max", 20, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blender.ComputeBlendRatio(tt.traceCount)
			if got != tt.wantRatio {
				t.Errorf("ComputeBlendRatio(%d) = %v, want %v",
					tt.traceCount, got, tt.wantRatio)
			}
		})
	}
}

// =============================================================================
// BlendScores Tests
// =============================================================================

func TestBlendScores(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	tests := []struct {
		name       string
		coldScore  float64
		warmScore  float64
		traceCount int
		wantScore  float64
		tolerance  float64
	}{
		{
			name:       "pure cold (0 traces)",
			coldScore:  0.8,
			warmScore:  0.2,
			traceCount: 0,
			wantScore:  0.8,
			tolerance:  0.001,
		},
		{
			name:       "pure cold (2 traces)",
			coldScore:  0.8,
			warmScore:  0.2,
			traceCount: 2,
			wantScore:  0.8,
			tolerance:  0.001,
		},
		{
			name:       "pure warm (15 traces)",
			coldScore:  0.8,
			warmScore:  0.2,
			traceCount: 15,
			wantScore:  0.2,
			tolerance:  0.001,
		},
		{
			name:       "pure warm (20 traces)",
			coldScore:  0.8,
			warmScore:  0.2,
			traceCount: 20,
			wantScore:  0.2,
			tolerance:  0.001,
		},
		{
			name:       "halfway blend",
			coldScore:  0.8,
			warmScore:  0.2,
			traceCount: 9,
			wantScore:  0.5, // 0.8*0.5 + 0.2*0.5
			tolerance:  0.001,
		},
		{
			name:       "quarter blend",
			coldScore:  1.0,
			warmScore:  0.0,
			traceCount: 6,
			wantScore:  0.75, // 1.0*0.75 + 0.0*0.25
			tolerance:  0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blender.BlendScores(tt.coldScore, tt.warmScore, tt.traceCount)
			if math.Abs(got-tt.wantScore) > tt.tolerance {
				t.Errorf("BlendScores(%v, %v, %d) = %v, want %v",
					tt.coldScore, tt.warmScore, tt.traceCount, got, tt.wantScore)
			}
		})
	}
}

func TestBlendScores_Clamping(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	// Test that result is clamped to [0,1]
	tests := []struct {
		name       string
		coldScore  float64
		warmScore  float64
		traceCount int
	}{
		{"high scores", 1.5, 1.5, 9},
		{"negative scores", -0.5, -0.5, 9},
		{"mixed high", 2.0, 0.5, 9},
		{"mixed low", 0.5, -1.0, 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blender.BlendScores(tt.coldScore, tt.warmScore, tt.traceCount)
			if got < 0.0 || got > 1.0 {
				t.Errorf("BlendScores() = %v, should be in [0,1]", got)
			}
		})
	}
}

// =============================================================================
// Domain-Specific Blending Tests
// =============================================================================

func TestSetDomainConfig(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	domainCfg := &DomainBlendConfig{
		MinTracesForWarm: 5,
		FullWarmTraces:   25,
		ColdWeight:       0.8,
		WarmWeight:       1.2,
	}

	blender.SetDomainConfig(domain.DomainLibrarian, domainCfg)

	got := blender.GetDomainConfig(domain.DomainLibrarian)
	if got == nil {
		t.Fatal("GetDomainConfig returned nil")
	}
	if got.MinTracesForWarm != 5 {
		t.Errorf("MinTracesForWarm = %d, want 5", got.MinTracesForWarm)
	}
	if got.FullWarmTraces != 25 {
		t.Errorf("FullWarmTraces = %d, want 25", got.FullWarmTraces)
	}
}

func TestSetDomainConfig_Nil(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	// Set a config
	blender.SetDomainConfig(domain.DomainLibrarian, &DomainBlendConfig{MinTracesForWarm: 10})

	// Remove it with nil
	blender.SetDomainConfig(domain.DomainLibrarian, nil)

	if got := blender.GetDomainConfig(domain.DomainLibrarian); got != nil {
		t.Errorf("GetDomainConfig should return nil after setting nil, got %v", got)
	}
}

func TestBlendScoresForDomain(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	// Set domain-specific config with different thresholds
	domainCfg := &DomainBlendConfig{
		MinTracesForWarm: 5,
		FullWarmTraces:   10,
		ColdWeight:       1.0,
		WarmWeight:       1.0,
	}
	blender.SetDomainConfig(domain.DomainLibrarian, domainCfg)

	// At trace count 7, domain config should give 0.4 ratio (2/5)
	// Global config would give different ratio

	coldScore := 1.0
	warmScore := 0.0
	traceCount := 7

	// Domain blend
	domainResult := blender.BlendScoresForDomain(coldScore, warmScore, traceCount, domain.DomainLibrarian)
	// Expected: 1.0 * (1-0.4) + 0.0 * 0.4 = 0.6
	if math.Abs(domainResult-0.6) > 0.001 {
		t.Errorf("BlendScoresForDomain() = %v, want 0.6", domainResult)
	}

	// Non-domain blend (should use global config)
	otherResult := blender.BlendScoresForDomain(coldScore, warmScore, traceCount, domain.DomainArchivalist)
	// With global config (min=3, full=15), ratio at 7 is 4/12 = 0.333
	// Expected: 1.0 * (1-0.333) + 0.0 * 0.333 = 0.667
	if math.Abs(otherResult-0.667) > 0.01 {
		t.Errorf("BlendScoresForDomain(other) = %v, want ~0.667", otherResult)
	}
}

func TestBlendScoresForDomain_Weights(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	domainCfg := &DomainBlendConfig{
		ColdWeight: 0.5, // Reduce cold influence
		WarmWeight: 2.0, // Increase warm influence
	}
	blender.SetDomainConfig(domain.DomainLibrarian, domainCfg)

	coldScore := 0.8
	warmScore := 0.4
	traceCount := 9 // Halfway in default config (ratio = 0.5)

	result := blender.BlendScoresForDomain(coldScore, warmScore, traceCount, domain.DomainLibrarian)
	// Weighted cold = 0.8 * 0.5 = 0.4
	// Weighted warm = 0.4 * 2.0 = 0.8
	// Blended = 0.4 * 0.5 + 0.8 * 0.5 = 0.6
	if math.Abs(result-0.6) > 0.001 {
		t.Errorf("BlendScoresForDomain with weights = %v, want 0.6", result)
	}
}

func TestBlendScoresForDomain_CurveOverride(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig()) // Linear by default

	stepCurve := BlendCurveStep
	domainCfg := &DomainBlendConfig{
		Curve:      &stepCurve,
		ColdWeight: 1.0,
		WarmWeight: 1.0,
	}
	blender.SetDomainConfig(domain.DomainLibrarian, domainCfg)

	coldScore := 1.0
	warmScore := 0.0

	// Before step threshold (halfway point)
	result1 := blender.BlendScoresForDomain(coldScore, warmScore, 5, domain.DomainLibrarian)
	if result1 != 1.0 {
		t.Errorf("Before step, got %v, want 1.0 (pure cold)", result1)
	}

	// After step threshold
	result2 := blender.BlendScoresForDomain(coldScore, warmScore, 12, domain.DomainLibrarian)
	if result2 != 0.0 {
		t.Errorf("After step, got %v, want 0.0 (pure warm)", result2)
	}
}

// =============================================================================
// GetEffectiveScore Tests
// =============================================================================

func TestGetEffectiveScore(t *testing.T) {
	// Setup
	profile := NewCodebaseProfile()
	profile.MaxPageRank = 1.0
	profile.AvgInDegree = 10.0
	profile.AvgOutDegree = 8.0

	weights := DefaultColdPriorWeights()
	coldCalc := NewColdPriorCalculator(profile, weights)

	memStore := newMockBlenderMemoryStore()

	blender := NewPriorBlenderWithDeps(nil, coldCalc, memStore)

	ctx := context.Background()

	t.Run("no memory record", func(t *testing.T) {
		signals := NewNodeColdStartSignals("node1", "function", 1)
		signals.PageRank = 0.5
		signals.InDegree = 5
		signals.OutDegree = 3
		signals.NameSalience = 0.7
		signals.DocCoverage = 0.6

		score, err := blender.GetEffectiveScore(ctx, "node1", signals, domain.DomainLibrarian)
		if err != nil {
			t.Fatalf("GetEffectiveScore error: %v", err)
		}

		// With no memory, should return pure cold score
		coldScore := coldCalc.ComputeColdPrior(signals)
		if math.Abs(score-coldScore) > 0.001 {
			t.Errorf("Score = %v, want cold score %v", score, coldScore)
		}
	})

	t.Run("with memory traces", func(t *testing.T) {
		signals := NewNodeColdStartSignals("node2", "function", 1)
		signals.PageRank = 0.5
		signals.NameSalience = 0.7

		// Set up memory with some traces (10 traces, activation of 0.5)
		memStore.setMemory("node2", 10, 0.5)

		score, err := blender.GetEffectiveScore(ctx, "node2", signals, domain.DomainLibrarian)
		if err != nil {
			t.Fatalf("GetEffectiveScore error: %v", err)
		}

		// Score should be in valid range
		if score < 0.0 || score > 1.0 {
			t.Errorf("Score = %v, should be in [0,1]", score)
		}
	})
}

func TestGetEffectiveScore_NilDependencies(t *testing.T) {
	blender := NewPriorBlender(nil)
	ctx := context.Background()

	signals := NewNodeColdStartSignals("node1", "function", 1)

	score, err := blender.GetEffectiveScore(ctx, "node1", signals, domain.DomainLibrarian)
	if err != nil {
		t.Fatalf("GetEffectiveScore error: %v", err)
	}

	// With no calculator or memory store, should return 0
	if score != 0.0 {
		t.Errorf("Score = %v, want 0.0 with nil dependencies", score)
	}
}

func TestGetEffectiveScoreWithWarm(t *testing.T) {
	profile := NewCodebaseProfile()
	weights := DefaultColdPriorWeights()
	coldCalc := NewColdPriorCalculator(profile, weights)

	blender := NewPriorBlender(nil)
	blender.SetColdCalculator(coldCalc)

	signals := NewNodeColdStartSignals("node1", "function", 1)
	signals.NameSalience = 0.8
	signals.DocCoverage = 0.7

	warmScore := 0.6
	traceCount := 9 // Halfway blend

	score := blender.GetEffectiveScoreWithWarm(signals, warmScore, traceCount, domain.DomainLibrarian)

	// Should be valid blend
	if score < 0.0 || score > 1.0 {
		t.Errorf("Score = %v, should be in [0,1]", score)
	}

	// Should be between cold and warm (approximately)
	coldScore := coldCalc.ComputeColdPrior(signals)
	if score < math.Min(coldScore, warmScore)-0.1 || score > math.Max(coldScore, warmScore)+0.1 {
		t.Errorf("Score = %v seems outside expected range [cold=%v, warm=%v]",
			score, coldScore, warmScore)
	}
}

// =============================================================================
// Utility Method Tests
// =============================================================================

func TestIsPureCold(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	tests := []struct {
		traceCount int
		want       bool
	}{
		{0, true},
		{1, true},
		{2, true},
		{3, false}, // At threshold, starts blending
		{10, false},
	}

	for _, tt := range tests {
		if got := blender.IsPureCold(tt.traceCount); got != tt.want {
			t.Errorf("IsPureCold(%d) = %v, want %v", tt.traceCount, got, tt.want)
		}
	}
}

func TestIsPureWarm(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	tests := []struct {
		traceCount int
		want       bool
	}{
		{0, false},
		{10, false},
		{14, false},
		{15, true},
		{20, true},
	}

	for _, tt := range tests {
		if got := blender.IsPureWarm(tt.traceCount); got != tt.want {
			t.Errorf("IsPureWarm(%d) = %v, want %v", tt.traceCount, got, tt.want)
		}
	}
}

func TestIsBlending(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	tests := []struct {
		traceCount int
		want       bool
	}{
		{0, false},
		{2, false},
		{3, true},
		{9, true},
		{14, true},
		{15, false},
		{20, false},
	}

	for _, tt := range tests {
		if got := blender.IsBlending(tt.traceCount); got != tt.want {
			t.Errorf("IsBlending(%d) = %v, want %v", tt.traceCount, got, tt.want)
		}
	}
}

func TestGetRatioAtTraceCount(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	// Should be same as ComputeBlendRatio
	for tc := 0; tc <= 20; tc++ {
		got := blender.GetRatioAtTraceCount(tc)
		want := blender.ComputeBlendRatio(tc)
		if got != want {
			t.Errorf("GetRatioAtTraceCount(%d) = %v, want %v", tc, got, want)
		}
	}
}

func TestGetRatioAtTraceCountForDomain(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	domainCfg := &DomainBlendConfig{
		MinTracesForWarm: 10,
		FullWarmTraces:   20,
	}
	blender.SetDomainConfig(domain.DomainLibrarian, domainCfg)

	// Check domain-specific ratio
	ratio := blender.GetRatioAtTraceCountForDomain(15, domain.DomainLibrarian)
	// At 15 with min=10, full=20: ratio = 5/10 = 0.5
	if math.Abs(ratio-0.5) > 0.001 {
		t.Errorf("GetRatioAtTraceCountForDomain(15) = %v, want 0.5", ratio)
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestPriorBlender_ThreadSafety(t *testing.T) {
	blender := NewPriorBlender(DefaultBlendConfig())

	var wg sync.WaitGroup
	numGoroutines := 100
	numOps := 1000

	// Start concurrent readers
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				_ = blender.ComputeBlendRatio(j % 20)
				_ = blender.BlendScores(0.5, 0.5, j%20)
				_ = blender.Config()
				_ = blender.IsPureCold(j % 20)
				_ = blender.IsPureWarm(j % 20)
			}
		}()
	}

	// Start concurrent writers
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				cfg := &DomainBlendConfig{
					MinTracesForWarm: id + j%10,
					ColdWeight:       float64(id) / 100.0,
				}
				blender.SetDomainConfig(domain.Domain(id%5), cfg)
			}
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases Tests
// =============================================================================

func TestBlendScores_EqualThresholds(t *testing.T) {
	// Edge case: min == full
	config := &BlendConfig{
		MinTracesForWarm: 5,
		FullWarmTraces:   5,
		Curve:            BlendCurveLinear,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	tests := []struct {
		traceCount int
		wantRatio  float64
	}{
		{0, 0.0},
		{4, 0.0},
		{5, 1.0},
		{6, 1.0},
	}

	for _, tt := range tests {
		ratio := blender.ComputeBlendRatio(tt.traceCount)
		if ratio != tt.wantRatio {
			t.Errorf("ComputeBlendRatio(%d) = %v, want %v", tt.traceCount, ratio, tt.wantRatio)
		}
	}
}

func TestBlendScores_ZeroThresholds(t *testing.T) {
	// Edge case: min = 0, full = 0
	config := &BlendConfig{
		MinTracesForWarm: 0,
		FullWarmTraces:   0,
		Curve:            BlendCurveLinear,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	// Any trace count should result in pure warm
	for tc := 0; tc <= 10; tc++ {
		ratio := blender.ComputeBlendRatio(tc)
		if ratio != 1.0 {
			t.Errorf("ComputeBlendRatio(%d) = %v, want 1.0", tc, ratio)
		}
	}
}

func TestBlender_ActivationToScore(t *testing.T) {
	blender := NewPriorBlender(nil)

	tests := []struct {
		activation float64
		minScore   float64
		maxScore   float64
	}{
		{-100, 0.0, 0.0},
		{-200, 0.0, 0.0},
		{-20, 0.0, 0.01},
		{-2, 0.4, 0.6},
		{0, 0.7, 0.95},
		{2, 0.95, 1.0},
		{20, 1.0, 1.0},
	}

	for _, tt := range tests {
		score := blender.activationToScore(tt.activation)
		if score < tt.minScore || score > tt.maxScore {
			t.Errorf("activationToScore(%v) = %v, want in [%v, %v]",
				tt.activation, score, tt.minScore, tt.maxScore)
		}
	}
}

func TestDefaultDomainBlendConfig(t *testing.T) {
	cfg := DefaultDomainBlendConfig()

	if cfg.MinTracesForWarm != 0 {
		t.Errorf("MinTracesForWarm = %d, want 0", cfg.MinTracesForWarm)
	}
	if cfg.FullWarmTraces != 0 {
		t.Errorf("FullWarmTraces = %d, want 0", cfg.FullWarmTraces)
	}
	if cfg.Curve != nil {
		t.Errorf("Curve = %v, want nil", cfg.Curve)
	}
	if cfg.ColdWeight != 1.0 {
		t.Errorf("ColdWeight = %v, want 1.0", cfg.ColdWeight)
	}
	if cfg.WarmWeight != 1.0 {
		t.Errorf("WarmWeight = %v, want 1.0", cfg.WarmWeight)
	}
}

// =============================================================================
// Monotonicity Tests
// =============================================================================

func TestComputeBlendRatio_Monotonicity_Linear(t *testing.T) {
	config := &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveLinear,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	// Ratio should be monotonically non-decreasing
	prevRatio := -1.0
	for tc := 0; tc <= 25; tc++ {
		ratio := blender.ComputeBlendRatio(tc)
		if ratio < prevRatio {
			t.Errorf("Ratio not monotonic: at tc=%d got %v, prev was %v", tc, ratio, prevRatio)
		}
		prevRatio = ratio
	}
}

func TestComputeBlendRatio_Monotonicity_Sigmoid(t *testing.T) {
	config := &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveSigmoid,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	// Ratio should be monotonically non-decreasing
	prevRatio := -1.0
	for tc := 0; tc <= 25; tc++ {
		ratio := blender.ComputeBlendRatio(tc)
		if ratio < prevRatio {
			t.Errorf("Ratio not monotonic: at tc=%d got %v, prev was %v", tc, ratio, prevRatio)
		}
		prevRatio = ratio
	}
}

func TestComputeBlendRatio_Monotonicity_Step(t *testing.T) {
	config := &BlendConfig{
		MinTracesForWarm: 3,
		FullWarmTraces:   15,
		Curve:            BlendCurveStep,
		SigmoidSteepness: 1.0,
	}
	blender := NewPriorBlender(config)

	// Ratio should be monotonically non-decreasing
	prevRatio := -1.0
	for tc := 0; tc <= 25; tc++ {
		ratio := blender.ComputeBlendRatio(tc)
		if ratio < prevRatio {
			t.Errorf("Ratio not monotonic: at tc=%d got %v, prev was %v", tc, ratio, prevRatio)
		}
		prevRatio = ratio
	}
}

// =============================================================================
// BlenderMemoryInfo Tests
// =============================================================================

func TestMockBlenderMemoryInfo(t *testing.T) {
	info := &mockBlenderMemoryInfo{
		traceCount: 10,
		activation: 0.75,
	}

	if info.TraceCount() != 10 {
		t.Errorf("TraceCount() = %d, want 10", info.TraceCount())
	}

	if info.Activation(time.Now()) != 0.75 {
		t.Errorf("Activation() = %v, want 0.75", info.Activation(time.Now()))
	}
}

func TestMockBlenderMemoryStore_NilReturn(t *testing.T) {
	store := newMockBlenderMemoryStore()
	ctx := context.Background()

	info, err := store.GetBlenderMemoryInfo(ctx, "nonexistent", domain.DomainLibrarian)
	if err != nil {
		t.Errorf("GetBlenderMemoryInfo error: %v", err)
	}
	if info != nil {
		t.Errorf("GetBlenderMemoryInfo should return nil for nonexistent node")
	}
}
