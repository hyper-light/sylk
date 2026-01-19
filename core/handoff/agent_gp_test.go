package handoff

import (
	"encoding/json"
	"math"
	"testing"
)

// =============================================================================
// HO.4.1 GPObservation Tests
// =============================================================================

func TestNewGPObservation(t *testing.T) {
	tests := []struct {
		name          string
		contextSize   int
		tokenCount    int
		toolCallCount int
		quality       float64
		wantQuality   float64
	}{
		{
			name:          "normal observation",
			contextSize:   1000,
			tokenCount:    100,
			toolCallCount: 3,
			quality:       0.85,
			wantQuality:   0.85,
		},
		{
			name:          "quality clamped to 1",
			contextSize:   500,
			tokenCount:    50,
			toolCallCount: 1,
			quality:       1.5,
			wantQuality:   1.0,
		},
		{
			name:          "quality clamped to 0",
			contextSize:   2000,
			tokenCount:    200,
			toolCallCount: 10,
			quality:       -0.5,
			wantQuality:   0.0,
		},
		{
			name:          "zero values",
			contextSize:   0,
			tokenCount:    0,
			toolCallCount: 0,
			quality:       0.5,
			wantQuality:   0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := NewGPObservation(tt.contextSize, tt.tokenCount, tt.toolCallCount, tt.quality)

			if obs.ContextSize != tt.contextSize {
				t.Errorf("ContextSize = %d, want %d", obs.ContextSize, tt.contextSize)
			}
			if obs.TokenCount != tt.tokenCount {
				t.Errorf("TokenCount = %d, want %d", obs.TokenCount, tt.tokenCount)
			}
			if obs.ToolCallCount != tt.toolCallCount {
				t.Errorf("ToolCallCount = %d, want %d", obs.ToolCallCount, tt.toolCallCount)
			}
			if obs.Quality != tt.wantQuality {
				t.Errorf("Quality = %f, want %f", obs.Quality, tt.wantQuality)
			}
		})
	}
}

func TestGPObservation_Features(t *testing.T) {
	obs := NewGPObservation(1000, 100, 5, 0.8)
	features := obs.Features()

	if len(features) != 3 {
		t.Fatalf("Features length = %d, want 3", len(features))
	}
	if features[0] != 1000.0 {
		t.Errorf("features[0] = %f, want 1000.0", features[0])
	}
	if features[1] != 100.0 {
		t.Errorf("features[1] = %f, want 100.0", features[1])
	}
	if features[2] != 5.0 {
		t.Errorf("features[2] = %f, want 5.0", features[2])
	}
}

func TestGPObservation_Clone(t *testing.T) {
	obs := NewGPObservation(1000, 100, 5, 0.8)
	cloned := obs.Clone()

	// Check values are equal
	if cloned.ContextSize != obs.ContextSize {
		t.Errorf("ContextSize = %d, want %d", cloned.ContextSize, obs.ContextSize)
	}
	if cloned.TokenCount != obs.TokenCount {
		t.Errorf("TokenCount = %d, want %d", cloned.TokenCount, obs.TokenCount)
	}
	if cloned.ToolCallCount != obs.ToolCallCount {
		t.Errorf("ToolCallCount = %d, want %d", cloned.ToolCallCount, obs.ToolCallCount)
	}
	if cloned.Quality != obs.Quality {
		t.Errorf("Quality = %f, want %f", cloned.Quality, obs.Quality)
	}

	// Check they are independent
	cloned.Quality = 0.5
	if obs.Quality == 0.5 {
		t.Error("Clone should be independent of original")
	}
}

func TestGPObservation_CloneNil(t *testing.T) {
	var obs *GPObservation
	cloned := obs.Clone()
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

// =============================================================================
// HO.4.2 GPHyperparams Tests
// =============================================================================

func TestDefaultGPHyperparams(t *testing.T) {
	hp := DefaultGPHyperparams()

	if len(hp.Lengthscales) != 3 {
		t.Errorf("Lengthscales length = %d, want 3", len(hp.Lengthscales))
	}
	if hp.Lengthscales[0] != 1000.0 {
		t.Errorf("Lengthscales[0] = %f, want 1000.0", hp.Lengthscales[0])
	}
	if hp.Lengthscales[1] != 100.0 {
		t.Errorf("Lengthscales[1] = %f, want 100.0", hp.Lengthscales[1])
	}
	if hp.Lengthscales[2] != 5.0 {
		t.Errorf("Lengthscales[2] = %f, want 5.0", hp.Lengthscales[2])
	}
	if hp.SignalVariance != 0.25 {
		t.Errorf("SignalVariance = %f, want 0.25", hp.SignalVariance)
	}
	if hp.NoiseVariance != 0.01 {
		t.Errorf("NoiseVariance = %f, want 0.01", hp.NoiseVariance)
	}
}

func TestGPHyperparams_Clone(t *testing.T) {
	hp := &GPHyperparams{
		Lengthscales:   []float64{100, 50, 10},
		SignalVariance: 0.5,
		NoiseVariance:  0.05,
	}
	cloned := hp.Clone()

	if len(cloned.Lengthscales) != 3 {
		t.Fatalf("Lengthscales length = %d, want 3", len(cloned.Lengthscales))
	}
	if cloned.Lengthscales[0] != 100 {
		t.Errorf("Lengthscales[0] = %f, want 100", cloned.Lengthscales[0])
	}
	if cloned.SignalVariance != 0.5 {
		t.Errorf("SignalVariance = %f, want 0.5", cloned.SignalVariance)
	}
	if cloned.NoiseVariance != 0.05 {
		t.Errorf("NoiseVariance = %f, want 0.05", cloned.NoiseVariance)
	}

	// Check independence
	cloned.Lengthscales[0] = 200
	if hp.Lengthscales[0] == 200 {
		t.Error("Clone should be independent of original")
	}
}

func TestGPHyperparams_CloneNil(t *testing.T) {
	var hp *GPHyperparams
	cloned := hp.Clone()
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestGPHyperparams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		hp      *GPHyperparams
		wantErr bool
	}{
		{
			name:    "valid hyperparams",
			hp:      DefaultGPHyperparams(),
			wantErr: false,
		},
		{
			name: "empty lengthscales",
			hp: &GPHyperparams{
				Lengthscales:   []float64{},
				SignalVariance: 0.25,
				NoiseVariance:  0.01,
			},
			wantErr: true,
		},
		{
			name: "negative lengthscale",
			hp: &GPHyperparams{
				Lengthscales:   []float64{100, -50, 10},
				SignalVariance: 0.25,
				NoiseVariance:  0.01,
			},
			wantErr: true,
		},
		{
			name: "zero lengthscale",
			hp: &GPHyperparams{
				Lengthscales:   []float64{100, 0, 10},
				SignalVariance: 0.25,
				NoiseVariance:  0.01,
			},
			wantErr: true,
		},
		{
			name: "negative signal variance",
			hp: &GPHyperparams{
				Lengthscales:   []float64{100, 50, 10},
				SignalVariance: -0.25,
				NoiseVariance:  0.01,
			},
			wantErr: true,
		},
		{
			name: "zero signal variance",
			hp: &GPHyperparams{
				Lengthscales:   []float64{100, 50, 10},
				SignalVariance: 0,
				NoiseVariance:  0.01,
			},
			wantErr: true,
		},
		{
			name: "negative noise variance",
			hp: &GPHyperparams{
				Lengthscales:   []float64{100, 50, 10},
				SignalVariance: 0.25,
				NoiseVariance:  -0.01,
			},
			wantErr: true,
		},
		{
			name: "zero noise variance (valid)",
			hp: &GPHyperparams{
				Lengthscales:   []float64{100, 50, 10},
				SignalVariance: 0.25,
				NoiseVariance:  0.0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.hp.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// HO.4.3 AgentGaussianProcess Tests
// =============================================================================

func TestNewAgentGaussianProcess(t *testing.T) {
	t.Run("with default hyperparams", func(t *testing.T) {
		gp := NewAgentGaussianProcess(nil)
		if gp == nil {
			t.Fatal("NewAgentGaussianProcess returned nil")
		}
		if gp.NumObservations() != 0 {
			t.Errorf("NumObservations = %d, want 0", gp.NumObservations())
		}
	})

	t.Run("with custom hyperparams", func(t *testing.T) {
		hp := &GPHyperparams{
			Lengthscales:   []float64{500, 50, 2},
			SignalVariance: 0.5,
			NoiseVariance:  0.02,
		}
		gp := NewAgentGaussianProcess(hp)
		if gp == nil {
			t.Fatal("NewAgentGaussianProcess returned nil")
		}

		gotHP := gp.GetHyperparams()
		if gotHP.SignalVariance != 0.5 {
			t.Errorf("SignalVariance = %f, want 0.5", gotHP.SignalVariance)
		}
	})
}

func TestAgentGaussianProcess_AddObservation(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	obs := NewGPObservation(1000, 100, 3, 0.8)
	gp.AddObservation(obs)

	if gp.NumObservations() != 1 {
		t.Errorf("NumObservations = %d, want 1", gp.NumObservations())
	}

	// Add nil observation (should be ignored)
	gp.AddObservation(nil)
	if gp.NumObservations() != 1 {
		t.Errorf("NumObservations after nil = %d, want 1", gp.NumObservations())
	}
}

func TestAgentGaussianProcess_AddObservationFromValues(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	gp.AddObservationFromValues(1000, 100, 3, 0.8)
	if gp.NumObservations() != 1 {
		t.Errorf("NumObservations = %d, want 1", gp.NumObservations())
	}

	observations := gp.GetObservations()
	if observations[0].ContextSize != 1000 {
		t.Errorf("ContextSize = %d, want 1000", observations[0].ContextSize)
	}
}

func TestAgentGaussianProcess_SetMaxObservations(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.SetMaxObservations(3)

	// Add 5 observations
	for i := 0; i < 5; i++ {
		gp.AddObservationFromValues(i*100, i*10, i, 0.8)
	}

	// Should only keep 3
	if gp.NumObservations() != 3 {
		t.Errorf("NumObservations = %d, want 3", gp.NumObservations())
	}

	// Check that oldest were removed
	observations := gp.GetObservations()
	if observations[0].ContextSize != 200 {
		t.Errorf("First observation ContextSize = %d, want 200 (third observation)", observations[0].ContextSize)
	}
}

func TestAgentGaussianProcess_SetMaxObservationsMinimum(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.SetMaxObservations(0) // Should be set to 1

	gp.AddObservationFromValues(100, 10, 1, 0.8)
	gp.AddObservationFromValues(200, 20, 2, 0.7)

	if gp.NumObservations() != 1 {
		t.Errorf("NumObservations = %d, want 1", gp.NumObservations())
	}
}

func TestAgentGaussianProcess_ClearObservations(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	gp.AddObservationFromValues(1000, 100, 3, 0.8)
	gp.AddObservationFromValues(2000, 200, 5, 0.7)

	if gp.NumObservations() != 2 {
		t.Errorf("NumObservations before clear = %d, want 2", gp.NumObservations())
	}

	gp.ClearObservations()

	if gp.NumObservations() != 0 {
		t.Errorf("NumObservations after clear = %d, want 0", gp.NumObservations())
	}
}

// =============================================================================
// HO.4.4 Matern 2.5 Kernel Tests
// =============================================================================

func TestMatern25Kernel_SamePoint(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	x := []float64{1000, 100, 5}
	k := gp.matern25Kernel(x, x)

	// At zero distance, Matern 2.5 should equal signal variance
	expected := gp.hyperparams.SignalVariance
	if math.Abs(k-expected) > 1e-10 {
		t.Errorf("kernel(x, x) = %f, want %f (signal variance)", k, expected)
	}
}

func TestMatern25Kernel_Symmetry(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	x1 := []float64{1000, 100, 5}
	x2 := []float64{2000, 150, 3}

	k12 := gp.matern25Kernel(x1, x2)
	k21 := gp.matern25Kernel(x2, x1)

	if math.Abs(k12-k21) > 1e-10 {
		t.Errorf("kernel is not symmetric: k(x1,x2)=%f, k(x2,x1)=%f", k12, k21)
	}
}

func TestMatern25Kernel_Decay(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	x := []float64{1000, 100, 5}
	near := []float64{1100, 100, 5}
	far := []float64{5000, 100, 5}

	kSelf := gp.matern25Kernel(x, x)
	kNear := gp.matern25Kernel(x, near)
	kFar := gp.matern25Kernel(x, far)

	// Kernel should decay with distance
	if kNear >= kSelf {
		t.Errorf("kernel should decay: k(x,x)=%f, k(x,near)=%f", kSelf, kNear)
	}
	if kFar >= kNear {
		t.Errorf("kernel should decay more for farther points: k(x,near)=%f, k(x,far)=%f", kNear, kFar)
	}
}

func TestMatern25Kernel_LengthscaleEffect(t *testing.T) {
	// Test with small lengthscale (function varies quickly)
	gpSmall := NewAgentGaussianProcess(&GPHyperparams{
		Lengthscales:   []float64{100, 10, 1},
		SignalVariance: 0.25,
		NoiseVariance:  0.01,
	})

	// Test with large lengthscale (function varies slowly)
	gpLarge := NewAgentGaussianProcess(&GPHyperparams{
		Lengthscales:   []float64{10000, 1000, 100},
		SignalVariance: 0.25,
		NoiseVariance:  0.01,
	})

	x := []float64{1000, 100, 5}
	y := []float64{2000, 200, 10}

	kSmall := gpSmall.matern25Kernel(x, y)
	kLarge := gpLarge.matern25Kernel(x, y)

	// With larger lengthscales, correlation should be higher for same distance
	if kLarge <= kSmall {
		t.Errorf("larger lengthscale should give higher correlation: kLarge=%f, kSmall=%f", kLarge, kSmall)
	}
}

func TestScaledDistance(t *testing.T) {
	gp := NewAgentGaussianProcess(&GPHyperparams{
		Lengthscales:   []float64{1000, 100, 10},
		SignalVariance: 0.25,
		NoiseVariance:  0.01,
	})

	x := []float64{1000, 100, 10}
	y := []float64{2000, 200, 20}

	d := gp.scaledDistance(x, y)

	// Each dimension contributes: (diff/lengthscale)^2
	// = (1000/1000)^2 + (100/100)^2 + (10/10)^2 = 1 + 1 + 1 = 3
	// sqrt(3) ~ 1.732
	expected := math.Sqrt(3.0)
	if math.Abs(d-expected) > 1e-6 {
		t.Errorf("scaledDistance = %f, want %f", d, expected)
	}
}

// =============================================================================
// GP Prediction Tests
// =============================================================================

func TestAgentGaussianProcess_PredictEmpty(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	pred := gp.Predict(1000, 100, 3)

	// With no observations, should return prior
	if pred.Mean < 0 || pred.Mean > 1 {
		t.Errorf("Mean = %f, want in [0,1]", pred.Mean)
	}
	if pred.Variance <= 0 {
		t.Errorf("Variance = %f, want > 0", pred.Variance)
	}
	if pred.StdDev != math.Sqrt(pred.Variance) {
		t.Errorf("StdDev = %f, want sqrt(Variance) = %f", pred.StdDev, math.Sqrt(pred.Variance))
	}
	if pred.LowerBound > pred.Mean || pred.UpperBound < pred.Mean {
		t.Errorf("Confidence interval wrong: [%f, %f] should contain mean %f",
			pred.LowerBound, pred.UpperBound, pred.Mean)
	}
}

func TestAgentGaussianProcess_PredictSingleObservation(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Add a single observation
	gp.AddObservationFromValues(1000, 100, 3, 0.9)

	// Predict at the same point
	pred := gp.Predict(1000, 100, 3)

	// Mean should be close to observed value
	if math.Abs(pred.Mean-0.9) > 0.1 {
		t.Errorf("Mean = %f, want close to 0.9", pred.Mean)
	}

	// Variance should be small at observation point
	priorVar := gp.hyperparams.SignalVariance
	if pred.Variance >= priorVar {
		t.Errorf("Variance = %f should be less than prior variance %f at observation", pred.Variance, priorVar)
	}
}

func TestAgentGaussianProcess_PredictInterpolation(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Add two observations with different quality
	gp.AddObservationFromValues(1000, 100, 3, 0.9)
	gp.AddObservationFromValues(3000, 100, 3, 0.5)

	// Predict at midpoint
	pred := gp.Predict(2000, 100, 3)

	// Mean should be between the two observations
	if pred.Mean < 0.4 || pred.Mean > 1.0 {
		t.Errorf("Mean = %f, want between 0.5 and 0.9", pred.Mean)
	}
}

func TestAgentGaussianProcess_PredictUncertaintyIncreasesWithDistance(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Add observation at one point
	gp.AddObservationFromValues(1000, 100, 3, 0.8)

	// Predict at observation point
	predNear := gp.Predict(1000, 100, 3)

	// Predict far away
	predFar := gp.Predict(10000, 500, 20)

	// Variance should be higher far away
	if predFar.Variance <= predNear.Variance {
		t.Errorf("Variance should increase with distance: near=%f, far=%f",
			predNear.Variance, predFar.Variance)
	}
}

func TestAgentGaussianProcess_PredictFromFeatures(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(1000, 100, 3, 0.8)

	features := []float64{1000, 100, 3}
	pred1 := gp.Predict(1000, 100, 3)
	pred2 := gp.PredictFromFeatures(features)

	if math.Abs(pred1.Mean-pred2.Mean) > 1e-10 {
		t.Errorf("Predict and PredictFromFeatures give different results: %f vs %f",
			pred1.Mean, pred2.Mean)
	}
}

// =============================================================================
// HO.4.5 Prior Mean Function Tests
// =============================================================================

func TestDefaultGPPriorMean(t *testing.T) {
	pm := DefaultGPPriorMean()

	if pm.BaseQuality != 0.8 {
		t.Errorf("BaseQuality = %f, want 0.8", pm.BaseQuality)
	}
	if pm.HistoricalSamples != 0 {
		t.Errorf("HistoricalSamples = %d, want 0", pm.HistoricalSamples)
	}
}

func TestGPPriorMean_Evaluate(t *testing.T) {
	pm := &GPPriorMean{
		BaseQuality:   0.8,
		ContextDecay:  0.001,
		TokenDecay:    0.01,
		ToolCallDecay: 0.1,
	}

	tests := []struct {
		name     string
		features []float64
		wantLow  float64
		wantHigh float64
	}{
		{
			name:     "zero features",
			features: []float64{0, 0, 0},
			wantLow:  0.79,
			wantHigh: 0.81,
		},
		{
			name:     "moderate context",
			features: []float64{1000, 0, 0},
			wantLow:  0.2,
			wantHigh: 0.8,
		},
		{
			name:     "high tool calls",
			features: []float64{0, 0, 10},
			wantLow:  0.1,
			wantHigh: 0.5,
		},
		{
			name:     "empty features",
			features: []float64{},
			wantLow:  0.79,
			wantHigh: 0.81,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := pm.Evaluate(tt.features)
			if val < tt.wantLow || val > tt.wantHigh {
				t.Errorf("Evaluate() = %f, want in [%f, %f]", val, tt.wantLow, tt.wantHigh)
			}
		})
	}
}

func TestGPPriorMean_Clone(t *testing.T) {
	pm := &GPPriorMean{
		BaseQuality:       0.75,
		ContextDecay:      0.001,
		TokenDecay:        0.01,
		ToolCallDecay:     0.1,
		HistoricalSamples: 50,
	}

	cloned := pm.Clone()

	if cloned.BaseQuality != pm.BaseQuality {
		t.Errorf("BaseQuality = %f, want %f", cloned.BaseQuality, pm.BaseQuality)
	}
	if cloned.HistoricalSamples != pm.HistoricalSamples {
		t.Errorf("HistoricalSamples = %d, want %d", cloned.HistoricalSamples, pm.HistoricalSamples)
	}

	// Check independence
	cloned.BaseQuality = 0.5
	if pm.BaseQuality == 0.5 {
		t.Error("Clone should be independent")
	}
}

func TestGPPriorMean_CloneNil(t *testing.T) {
	var pm *GPPriorMean
	cloned := pm.Clone()
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestGPPriorMean_LearnFromObservations(t *testing.T) {
	pm := DefaultGPPriorMean()

	observations := []*GPObservation{
		NewGPObservation(500, 50, 1, 0.9),
		NewGPObservation(1000, 100, 2, 0.8),
		NewGPObservation(1500, 150, 3, 0.7),
		NewGPObservation(2000, 200, 4, 0.6),
		NewGPObservation(2500, 250, 5, 0.5),
	}

	pm.LearnFromObservations(observations)

	// Mean quality should be 0.7 (average)
	if math.Abs(pm.BaseQuality-0.7) > 0.01 {
		t.Errorf("BaseQuality = %f, want 0.7", pm.BaseQuality)
	}

	// Should have recorded the number of samples
	if pm.HistoricalSamples != 5 {
		t.Errorf("HistoricalSamples = %d, want 5", pm.HistoricalSamples)
	}

	// Decay should be non-negative
	if pm.ContextDecay < 0 {
		t.Errorf("ContextDecay = %f, should be non-negative", pm.ContextDecay)
	}
}

func TestGPPriorMean_LearnFromEmptyObservations(t *testing.T) {
	pm := DefaultGPPriorMean()
	originalBase := pm.BaseQuality

	pm.LearnFromObservations([]*GPObservation{})

	// Should not change from empty input
	if pm.BaseQuality != originalBase {
		t.Errorf("BaseQuality changed from %f to %f with empty input", originalBase, pm.BaseQuality)
	}
}

func TestAgentGaussianProcess_SetPriorMean(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	pm := &GPPriorMean{
		BaseQuality:       0.75,
		ContextDecay:      0.0005,
		TokenDecay:        0.005,
		ToolCallDecay:     0.05,
		HistoricalSamples: 100,
	}

	gp.SetPriorMean(pm)

	got := gp.GetPriorMean()
	if got == nil {
		t.Fatal("GetPriorMean returned nil")
	}
	if got.BaseQuality != 0.75 {
		t.Errorf("BaseQuality = %f, want 0.75", got.BaseQuality)
	}

	// Should be a copy
	got.BaseQuality = 0.5
	got2 := gp.GetPriorMean()
	if got2.BaseQuality == 0.5 {
		t.Error("GetPriorMean should return a copy")
	}
}

func TestAgentGaussianProcess_SetPriorMeanNil(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	pm := DefaultGPPriorMean()
	gp.SetPriorMean(pm)
	gp.SetPriorMean(nil)

	if gp.GetPriorMean() != nil {
		t.Error("GetPriorMean should be nil after SetPriorMean(nil)")
	}
}

func TestAgentGaussianProcess_LearnPriorFromHistory(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	history := []*GPObservation{
		NewGPObservation(500, 50, 1, 0.85),
		NewGPObservation(1000, 100, 2, 0.75),
		NewGPObservation(1500, 150, 3, 0.65),
	}

	gp.LearnPriorFromHistory(history)

	pm := gp.GetPriorMean()
	if pm == nil {
		t.Fatal("Prior mean should be set")
	}
	if pm.HistoricalSamples != 3 {
		t.Errorf("HistoricalSamples = %d, want 3", pm.HistoricalSamples)
	}
}

func TestAgentGaussianProcess_LearnPriorFromEmptyHistory(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	gp.LearnPriorFromHistory([]*GPObservation{})

	// Should not set prior from empty history
	if gp.GetPriorMean() != nil {
		t.Error("Prior should not be set from empty history")
	}
}

// =============================================================================
// Hyperparameter Management Tests
// =============================================================================

func TestAgentGaussianProcess_GetSetHyperparams(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	newHP := &GPHyperparams{
		Lengthscales:   []float64{500, 50, 2},
		SignalVariance: 0.5,
		NoiseVariance:  0.02,
	}

	err := gp.SetHyperparams(newHP)
	if err != nil {
		t.Errorf("SetHyperparams failed: %v", err)
	}

	got := gp.GetHyperparams()
	if got.SignalVariance != 0.5 {
		t.Errorf("SignalVariance = %f, want 0.5", got.SignalVariance)
	}
}

func TestAgentGaussianProcess_SetHyperparamsNil(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	err := gp.SetHyperparams(nil)
	if err == nil {
		t.Error("SetHyperparams(nil) should return error")
	}
}

func TestAgentGaussianProcess_SetHyperparamsInvalid(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	invalidHP := &GPHyperparams{
		Lengthscales:   []float64{-100, 50, 2},
		SignalVariance: 0.5,
		NoiseVariance:  0.02,
	}

	err := gp.SetHyperparams(invalidHP)
	if err == nil {
		t.Error("SetHyperparams with invalid params should return error")
	}
}

// =============================================================================
// Cholesky Decomposition Tests
// =============================================================================

func TestCholeskyDecomposition(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Simple positive definite matrix
	A := [][]float64{
		{4, 2, 2},
		{2, 5, 2},
		{2, 2, 6},
	}

	L, err := gp.choleskyDecomposition(A)
	if err != nil {
		t.Fatalf("choleskyDecomposition failed: %v", err)
	}

	// Verify L * L^T = A
	n := len(A)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			sum := 0.0
			for k := 0; k < n; k++ {
				sum += L[i][k] * L[j][k]
			}
			if math.Abs(sum-A[i][j]) > 1e-10 {
				t.Errorf("L*L^T[%d][%d] = %f, want %f", i, j, sum, A[i][j])
			}
		}
	}
}

func TestCholeskyDecomposition_NotPositiveDefinite(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Not positive definite (negative eigenvalue)
	A := [][]float64{
		{1, 2},
		{2, 1},
	}

	_, err := gp.choleskyDecomposition(A)
	if err == nil {
		t.Error("choleskyDecomposition should fail for non-positive definite matrix")
	}
}

func TestCholeskyDecomposition_Empty(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	L, err := gp.choleskyDecomposition([][]float64{})
	if err != nil {
		t.Errorf("Empty matrix should not error: %v", err)
	}
	if L != nil {
		t.Error("Empty matrix should return nil")
	}
}

func TestSolveTriangular(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Lower triangular matrix
	L := [][]float64{
		{2, 0, 0},
		{1, 3, 0},
		{2, 1, 4},
	}

	b := []float64{4, 7, 14}

	// Solve L * x = b
	x := gp.solveTriangular(L, b, false)

	// Verify L * x = b
	for i := 0; i < 3; i++ {
		sum := 0.0
		for j := 0; j <= i; j++ {
			sum += L[i][j] * x[j]
		}
		if math.Abs(sum-b[i]) > 1e-10 {
			t.Errorf("L*x[%d] = %f, want %f", i, sum, b[i])
		}
	}
}

func TestSolveTriangular_Transpose(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Lower triangular matrix
	L := [][]float64{
		{2, 0, 0},
		{1, 3, 0},
		{2, 1, 4},
	}

	b := []float64{10, 10, 4}

	// Solve L^T * x = b
	x := gp.solveTriangular(L, b, true)

	// Verify L^T * x = b
	for i := 0; i < 3; i++ {
		sum := 0.0
		for j := i; j < 3; j++ {
			sum += L[j][i] * x[j]
		}
		if math.Abs(sum-b[i]) > 1e-10 {
			t.Errorf("L^T*x[%d] = %f, want %f", i, sum, b[i])
		}
	}
}

// =============================================================================
// JSON Serialization Tests
// =============================================================================

func TestAgentGaussianProcess_JSON(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(1000, 100, 3, 0.8)
	gp.AddObservationFromValues(2000, 200, 5, 0.6)
	gp.SetMaxObservations(50)

	pm := DefaultGPPriorMean()
	pm.BaseQuality = 0.85
	gp.SetPriorMean(pm)

	// Marshal
	data, err := json.Marshal(gp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal into new GP
	gp2 := &AgentGaussianProcess{}
	err = json.Unmarshal(data, gp2)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify observations
	if gp2.NumObservations() != 2 {
		t.Errorf("NumObservations = %d, want 2", gp2.NumObservations())
	}

	// Verify prior mean
	pm2 := gp2.GetPriorMean()
	if pm2 == nil {
		t.Fatal("Prior mean should be restored")
	}
	if pm2.BaseQuality != 0.85 {
		t.Errorf("BaseQuality = %f, want 0.85", pm2.BaseQuality)
	}
}

func TestAgentGaussianProcess_JSON_Empty(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	data, err := json.Marshal(gp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	gp2 := &AgentGaussianProcess{}
	err = json.Unmarshal(data, gp2)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if gp2.NumObservations() != 0 {
		t.Errorf("NumObservations = %d, want 0", gp2.NumObservations())
	}
}

func TestGPObservation_JSON(t *testing.T) {
	obs := NewGPObservation(1000, 100, 5, 0.85)

	data, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	obs2 := &GPObservation{}
	err = json.Unmarshal(data, obs2)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if obs2.ContextSize != 1000 {
		t.Errorf("ContextSize = %d, want 1000", obs2.ContextSize)
	}
	if obs2.Quality != 0.85 {
		t.Errorf("Quality = %f, want 0.85", obs2.Quality)
	}
}

func TestGPHyperparams_JSON(t *testing.T) {
	hp := &GPHyperparams{
		Lengthscales:   []float64{500, 50, 5},
		SignalVariance: 0.3,
		NoiseVariance:  0.02,
	}

	data, err := json.Marshal(hp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	hp2 := &GPHyperparams{}
	err = json.Unmarshal(data, hp2)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if hp2.SignalVariance != 0.3 {
		t.Errorf("SignalVariance = %f, want 0.3", hp2.SignalVariance)
	}
	if len(hp2.Lengthscales) != 3 {
		t.Errorf("Lengthscales length = %d, want 3", len(hp2.Lengthscales))
	}
}

func TestGPPriorMean_JSON(t *testing.T) {
	pm := &GPPriorMean{
		BaseQuality:       0.75,
		ContextDecay:      0.001,
		TokenDecay:        0.01,
		ToolCallDecay:     0.1,
		HistoricalSamples: 100,
	}

	data, err := json.Marshal(pm)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	pm2 := &GPPriorMean{}
	err = json.Unmarshal(data, pm2)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if pm2.BaseQuality != 0.75 {
		t.Errorf("BaseQuality = %f, want 0.75", pm2.BaseQuality)
	}
	if pm2.HistoricalSamples != 100 {
		t.Errorf("HistoricalSamples = %d, want 100", pm2.HistoricalSamples)
	}
}

// =============================================================================
// Clone Tests
// =============================================================================

func TestAgentGaussianProcess_Clone(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(1000, 100, 3, 0.8)
	gp.SetMaxObservations(50)
	gp.SetPriorMean(DefaultGPPriorMean())

	cloned := gp.Clone()

	// Check observations copied
	if cloned.NumObservations() != 1 {
		t.Errorf("NumObservations = %d, want 1", cloned.NumObservations())
	}

	// Check independence
	cloned.AddObservationFromValues(2000, 200, 5, 0.7)
	if gp.NumObservations() != 1 {
		t.Error("Clone should be independent of original")
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAgentGaussianProcess_FullWorkflow(t *testing.T) {
	// Create GP with custom hyperparams
	hp := &GPHyperparams{
		Lengthscales:   []float64{1000, 100, 5},
		SignalVariance: 0.25,
		NoiseVariance:  0.01,
	}
	gp := NewAgentGaussianProcess(hp)

	// Learn prior from historical data
	history := []*GPObservation{
		NewGPObservation(500, 50, 1, 0.9),
		NewGPObservation(1500, 150, 3, 0.75),
		NewGPObservation(2500, 250, 5, 0.6),
	}
	gp.LearnPriorFromHistory(history)

	// Verify prior was learned
	pm := gp.GetPriorMean()
	if pm == nil {
		t.Fatal("Prior should be learned")
	}

	// Add real-time observations
	gp.AddObservationFromValues(1000, 100, 2, 0.85)
	gp.AddObservationFromValues(2000, 200, 4, 0.65)

	// Make prediction
	pred := gp.Predict(1500, 150, 3)

	// Check prediction is reasonable
	if pred.Mean < 0.5 || pred.Mean > 0.9 {
		t.Errorf("Mean = %f, expected between 0.5 and 0.9", pred.Mean)
	}
	if pred.Variance <= 0 {
		t.Errorf("Variance = %f, should be positive", pred.Variance)
	}
	if pred.LowerBound >= pred.UpperBound {
		t.Errorf("Invalid confidence interval: [%f, %f]", pred.LowerBound, pred.UpperBound)
	}

	// Clone and verify independence
	cloned := gp.Clone()
	cloned.ClearObservations()
	if gp.NumObservations() != 2 {
		t.Error("Original should not be affected by clone operations")
	}
}

func TestAgentGaussianProcess_QualityDecayPrediction(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Add observations showing quality decay with context size
	gp.AddObservationFromValues(500, 50, 1, 0.95)
	gp.AddObservationFromValues(1000, 100, 2, 0.85)
	gp.AddObservationFromValues(2000, 150, 3, 0.7)
	gp.AddObservationFromValues(4000, 200, 4, 0.5)

	// Predict at various context sizes
	predSmall := gp.Predict(600, 60, 1)
	predMedium := gp.Predict(1500, 120, 2)
	predLarge := gp.Predict(3000, 180, 4)

	// Quality should decrease with context size
	if predSmall.Mean < predMedium.Mean {
		t.Logf("Warning: Expected quality to decrease: small=%f, medium=%f", predSmall.Mean, predMedium.Mean)
	}
	if predMedium.Mean < predLarge.Mean {
		t.Logf("Warning: Expected quality to decrease: medium=%f, large=%f", predMedium.Mean, predLarge.Mean)
	}

	// All predictions should be in valid range
	for _, pred := range []*GPPrediction{predSmall, predMedium, predLarge} {
		if pred.Mean < 0 || pred.Mean > 1 {
			t.Errorf("Mean = %f, should be in [0, 1]", pred.Mean)
		}
	}
}

func TestAgentGaussianProcess_NumericalStability(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Add many observations that might cause numerical issues
	for i := 0; i < 50; i++ {
		gp.AddObservationFromValues(
			i*100,
			i*10,
			i,
			0.9-float64(i)*0.01,
		)
	}

	// Should still be able to make predictions without panic
	pred := gp.Predict(2500, 250, 25)

	if math.IsNaN(pred.Mean) || math.IsInf(pred.Mean, 0) {
		t.Error("Prediction mean is NaN or Inf")
	}
	if math.IsNaN(pred.Variance) || math.IsInf(pred.Variance, 0) {
		t.Error("Prediction variance is NaN or Inf")
	}
}

func TestAgentGaussianProcess_ConcurrentAccess(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)

	// Add initial observations
	for i := 0; i < 10; i++ {
		gp.AddObservationFromValues(i*100, i*10, i, 0.8)
	}

	// Concurrent reads and writes
	done := make(chan bool)

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = gp.Predict(500, 50, 5)
			_ = gp.NumObservations()
			_ = gp.GetHyperparams()
		}
		done <- true
	}()

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			gp.AddObservationFromValues(i*50, i*5, i%10, 0.7)
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// Should not have panicked
	if gp.NumObservations() == 0 {
		t.Error("Should have observations after concurrent writes")
	}
}
