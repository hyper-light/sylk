package shared

import (
	"encoding/json"
	"math"
	"testing"
)

// --- DefaultRefactorLoopConfig ---

func TestDefaultRefactorLoopConfig_MaxIterations(t *testing.T) {
	cfg := DefaultRefactorLoopConfig()
	if cfg.MaxIterations != 3 {
		t.Fatalf("MaxIterations = %d, want 3", cfg.MaxIterations)
	}
}

func TestDefaultRefactorLoopConfig_TesterTimeout(t *testing.T) {
	cfg := DefaultRefactorLoopConfig()
	expected := DefaultConsultationTimeout * 2
	if cfg.TesterTimeout != expected {
		t.Fatalf("TesterTimeout = %v, want %v (DefaultConsultationTimeout*2)", cfg.TesterTimeout, expected)
	}
}

func TestDefaultRefactorLoopConfig_EscalateOnExhaustion(t *testing.T) {
	cfg := DefaultRefactorLoopConfig()
	if !cfg.EscalateOnExhaustion {
		t.Fatal("EscalateOnExhaustion should be true")
	}
}

func TestDefaultRefactorLoopConfig_MinQualityComposite(t *testing.T) {
	cfg := DefaultRefactorLoopConfig()
	if cfg.MinQualityComposite != 0.7 {
		t.Fatalf("MinQualityComposite = %f, want 0.7", cfg.MinQualityComposite)
	}
}

// --- ComputeComposite ---

func TestComputeComposite_EqualScores(t *testing.T) {
	scores := make(map[QualityDimension]float64)
	weights := make(map[QualityDimension]float64)
	for _, dim := range AllQualityDimensions() {
		scores[dim] = 0.8
		weights[dim] = 1.0
	}
	got := ComputeComposite(scores, weights)
	// With equal scores and equal weights, geometric mean = the score itself.
	if math.Abs(got-0.8) > 1e-9 {
		t.Fatalf("expected ~0.8 for uniform scores, got %f", got)
	}
}

func TestComputeComposite_ZeroDimension(t *testing.T) {
	scores := map[QualityDimension]float64{
		DimCorrectness:   0.9,
		DimTaskAdherence: 0.0, // zero dimension
	}
	weights := DefaultQualityWeights()
	got := ComputeComposite(scores, weights)
	if got != 0 {
		t.Fatalf("expected 0 when any dimension is zero, got %f", got)
	}
}

func TestComputeComposite_CustomWeightsShiftResult(t *testing.T) {
	scores := map[QualityDimension]float64{
		DimCorrectness:   0.9,
		DimTaskAdherence: 0.5,
	}
	equalWeights := map[QualityDimension]float64{
		DimCorrectness:   1.0,
		DimTaskAdherence: 1.0,
	}
	skewedWeights := map[QualityDimension]float64{
		DimCorrectness:   10.0, // heavily favor the higher score
		DimTaskAdherence: 0.1,
	}

	equalResult := ComputeComposite(scores, equalWeights)
	skewedResult := ComputeComposite(scores, skewedWeights)

	// Skewing toward the higher dimension should produce a higher composite.
	if skewedResult <= equalResult {
		t.Fatalf("skewed result (%f) should exceed equal result (%f)", skewedResult, equalResult)
	}
}

func TestComputeComposite_EmptyScores(t *testing.T) {
	got := ComputeComposite(nil, DefaultQualityWeights())
	if got != 0 {
		t.Fatalf("expected 0 for empty/nil scores, got %f", got)
	}
}

// --- NewQualityScores ---

func TestNewQualityScores_CreatesValid(t *testing.T) {
	qs := NewQualityScores("test reasoning")
	if qs == nil {
		t.Fatal("expected non-nil QualityScores")
	}
	if qs.Scores == nil {
		t.Fatal("Scores map should be initialized")
	}
	if qs.Issues == nil {
		t.Fatal("Issues map should be initialized")
	}
	if qs.Reasoning != "test reasoning" {
		t.Fatalf("Reasoning = %q, want %q", qs.Reasoning, "test reasoning")
	}
	if qs.Composite != 0 {
		t.Fatalf("Composite should be zero initially, got %f", qs.Composite)
	}
}

// --- QualityScores.ComputeAndSet ---

func TestQualityScores_ComputeAndSet(t *testing.T) {
	qs := NewQualityScores("test")
	for _, dim := range AllQualityDimensions() {
		qs.Scores[dim] = 0.8
	}
	qs.ComputeAndSet(DefaultQualityWeights())
	// After ComputeAndSet, Composite should be close to 0.8 (uniform scores).
	if math.Abs(qs.Composite-0.8) > 1e-9 {
		t.Fatalf("expected Composite ~0.8 after ComputeAndSet, got %f", qs.Composite)
	}
}

// --- TestFeedback.ShouldContinueLoop ---

func TestShouldContinueLoop_QualityAboveThreshold(t *testing.T) {
	cfg := DefaultRefactorLoopConfig()
	tf := TestFeedback{
		Quality: &QualityScores{
			Scores:    map[QualityDimension]float64{DimCorrectness: 0.9},
			Composite: cfg.MinQualityComposite + 0.1,
		},
	}
	if tf.ShouldContinueLoop(cfg.MinQualityComposite) {
		t.Fatal("should NOT continue when composite exceeds threshold")
	}
}

func TestShouldContinueLoop_QualityBelowThreshold(t *testing.T) {
	cfg := DefaultRefactorLoopConfig()
	tf := TestFeedback{
		Quality: &QualityScores{
			Scores:    map[QualityDimension]float64{DimCorrectness: 0.3},
			Composite: cfg.MinQualityComposite - 0.1,
		},
	}
	if !tf.ShouldContinueLoop(cfg.MinQualityComposite) {
		t.Fatal("should continue when composite is below threshold")
	}
}

func TestShouldContinueLoop_NilQuality_FallbackPassed(t *testing.T) {
	tf := TestFeedback{TestsPassed: true}
	if tf.ShouldContinueLoop(0.7) {
		t.Fatal("should NOT continue when Quality is nil and TestsPassed is true")
	}
}

func TestShouldContinueLoop_NilQuality_FallbackFailed(t *testing.T) {
	tf := TestFeedback{TestsPassed: false}
	if !tf.ShouldContinueLoop(0.7) {
		t.Fatal("should continue when Quality is nil and TestsPassed is false")
	}
}

// --- AllQualityDimensions ---

func TestAllQualityDimensions_Count(t *testing.T) {
	dims := AllQualityDimensions()
	const expectedCount = 7
	if len(dims) != expectedCount {
		t.Fatalf("expected %d dimensions, got %d", expectedCount, len(dims))
	}
}

func TestAllQualityDimensions_AllUnique(t *testing.T) {
	dims := AllQualityDimensions()
	seen := make(map[QualityDimension]struct{}, len(dims))
	for _, dim := range dims {
		if _, exists := seen[dim]; exists {
			t.Fatalf("duplicate dimension: %q", dim)
		}
		seen[dim] = struct{}{}
	}
}

// --- JSON roundtrip ---

func TestTestFeedback_JSONRoundtrip(t *testing.T) {
	qs := NewQualityScores("roundtrip test")
	qs.Scores[DimCorrectness] = 0.85
	qs.Scores[DimReadability] = 0.72
	qs.ComputeAndSet(DefaultQualityWeights())

	original := TestFeedback{
		TaskID:      "task-1",
		Iteration:   2,
		TestsPassed: true,
		PassCount:   10,
		FailCount:   1,
		Confidence:  0.9,
		Quality:     qs,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored TestFeedback
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.TaskID != original.TaskID {
		t.Fatalf("TaskID mismatch: got %q, want %q", restored.TaskID, original.TaskID)
	}
	if restored.Quality == nil {
		t.Fatal("Quality lost during roundtrip")
	}
	if math.Abs(restored.Quality.Composite-original.Quality.Composite) > 1e-12 {
		t.Fatalf("Composite mismatch: got %f, want %f",
			restored.Quality.Composite, original.Quality.Composite)
	}
	if restored.Quality.Scores[DimCorrectness] != original.Quality.Scores[DimCorrectness] {
		t.Fatalf("DimCorrectness score lost in roundtrip")
	}
}
