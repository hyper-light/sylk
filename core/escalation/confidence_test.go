package escalation

import (
	"math"
	"testing"
	"time"
)

func TestNewConfidenceLevel(t *testing.T) {
	before := time.Now()
	cl := NewConfidenceLevel("agent-1", "coder", "task-42")
	after := time.Now()

	if cl.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want %q", cl.AgentID, "agent-1")
	}
	if cl.AgentType != "coder" {
		t.Errorf("AgentType = %q, want %q", cl.AgentType, "coder")
	}
	if cl.TaskID != "task-42" {
		t.Errorf("TaskID = %q, want %q", cl.TaskID, "task-42")
	}
	if cl.Timestamp.Before(before) || cl.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in [%v, %v]", cl.Timestamp, before, after)
	}
	if cl.Correctness != 0 {
		t.Errorf("Correctness = %v, want 0", cl.Correctness)
	}
	if cl.Completeness != 0 {
		t.Errorf("Completeness = %v, want 0", cl.Completeness)
	}
	if cl.Quality != 0 {
		t.Errorf("Quality = %v, want 0", cl.Quality)
	}
	if cl.Integration != 0 {
		t.Errorf("Integration = %v, want 0", cl.Integration)
	}
}

func TestComposite_DefaultWeights_EqualScores(t *testing.T) {
	cl := &ConfidenceLevel{
		Correctness:  0.8,
		Completeness: 0.8,
		Quality:      0.8,
		Integration:  0.8,
	}
	got := cl.Composite(DefaultWeights())
	// Geometric mean of four equal values is the value itself.
	if math.Abs(got-0.8) > 1e-9 {
		t.Errorf("Composite = %v, want 0.8", got)
	}
}

func TestComposite_DefaultWeights_OneDimensionZero(t *testing.T) {
	cl := &ConfidenceLevel{
		Correctness:  0.9,
		Completeness: 0.0,
		Quality:      0.9,
		Integration:  0.9,
	}
	got := cl.Composite(DefaultWeights())
	if got != 0 {
		t.Errorf("Composite = %v, want 0 (zero dimension)", got)
	}
}

func TestComposite_DefaultWeights_GeometricNotArithmetic(t *testing.T) {
	cl := &ConfidenceLevel{
		Correctness:  0.1,
		Completeness: 0.9,
		Quality:      0.9,
		Integration:  0.9,
	}
	w := DefaultWeights()
	got := cl.Composite(w)

	// Arithmetic mean would be (0.1+0.9+0.9+0.9)/4 = 0.675
	arithmeticMean := (0.1 + 0.9 + 0.9 + 0.9) / 4.0

	// Geometric mean = (0.1 * 0.9 * 0.9 * 0.9)^(1/4)
	expectedGeo := math.Pow(0.1*0.9*0.9*0.9, 0.25)

	if math.Abs(got-expectedGeo) > 1e-9 {
		t.Errorf("Composite = %v, want geometric mean %v", got, expectedGeo)
	}
	// Geometric mean must be strictly less than arithmetic mean for non-equal positive values.
	if got >= arithmeticMean {
		t.Errorf("Composite %v >= arithmetic mean %v; geometric mean should be lower", got, arithmeticMean)
	}
}

func TestComposite_AllZeroWeights(t *testing.T) {
	cl := &ConfidenceLevel{
		Correctness:  0.8,
		Completeness: 0.8,
		Quality:      0.8,
		Integration:  0.8,
	}
	w := ConfidenceWeights{
		Correctness:  0,
		Completeness: 0,
		Quality:      0,
		Integration:  0,
	}
	got := cl.Composite(w)
	if got != 0 {
		t.Errorf("Composite = %v, want 0 (all-zero weights)", got)
	}
}

func TestComposite_CustomDimensions(t *testing.T) {
	cl := &ConfidenceLevel{
		Correctness:  0.8,
		Completeness: 0.8,
		Quality:      0.8,
		Integration:  0.8,
		Dimensions:   map[string]float64{"security": 0.8},
	}
	w := ConfidenceWeights{
		Correctness:  1.0,
		Completeness: 1.0,
		Quality:      1.0,
		Integration:  1.0,
		Custom:       map[string]float64{"security": 1.0},
	}
	got := cl.Composite(w)
	// All five dimensions are 0.8 with equal weights; geometric mean = 0.8.
	if math.Abs(got-0.8) > 1e-9 {
		t.Errorf("Composite = %v, want 0.8 with custom dimension", got)
	}
}

func TestComposite_CustomDimensionDefaultWeight(t *testing.T) {
	// Custom dimension present in ConfidenceLevel but NOT in weights.Custom.
	// Should default to weight 1.0.
	cl := &ConfidenceLevel{
		Correctness:  0.8,
		Completeness: 0.8,
		Quality:      0.8,
		Integration:  0.8,
		Dimensions:   map[string]float64{"security": 0.8},
	}
	w := DefaultWeights() // No Custom map
	got := cl.Composite(w)
	// All five dimensions 0.8 with weight 1.0 each; geometric mean = 0.8.
	if math.Abs(got-0.8) > 1e-9 {
		t.Errorf("Composite = %v, want 0.8 with defaulted custom weight", got)
	}
}

func TestCategorizeConfidence_Boundaries(t *testing.T) {
	tests := []struct {
		score float64
		want  ConfidenceCat
	}{
		{0.0, ConfidenceCritical},
		{0.19, ConfidenceCritical},
		{0.2, ConfidenceLow},
		{0.49, ConfidenceLow},
		{0.5, ConfidenceMedium},
		{0.79, ConfidenceMedium},
		{0.8, ConfidenceHigh},
		{1.0, ConfidenceHigh},
	}
	for _, tt := range tests {
		got := CategorizeConfidence(tt.score)
		if got != tt.want {
			t.Errorf("CategorizeConfidence(%v) = %v, want %v", tt.score, got, tt.want)
		}
	}
}

func TestConfidenceCat_String(t *testing.T) {
	tests := []struct {
		cat  ConfidenceCat
		want string
	}{
		{ConfidenceCritical, "critical"},
		{ConfidenceLow, "low"},
		{ConfidenceMedium, "medium"},
		{ConfidenceHigh, "high"},
		{ConfidenceCat(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.cat.String()
		if got != tt.want {
			t.Errorf("ConfidenceCat(%d).String() = %q, want %q", tt.cat, got, tt.want)
		}
	}
}

func TestCategory_DelegatesToCompositeAndCategorize(t *testing.T) {
	cl := &ConfidenceLevel{
		Correctness:  0.9,
		Completeness: 0.9,
		Quality:      0.9,
		Integration:  0.9,
	}
	w := DefaultWeights()
	got := cl.Category(w)
	// Composite of four 0.9 values = 0.9, which is >= 0.8 → ConfidenceHigh
	if got != ConfidenceHigh {
		t.Errorf("Category = %v, want %v", got, ConfidenceHigh)
	}

	// Now test a low-confidence scenario.
	cl2 := &ConfidenceLevel{
		Correctness:  0.1,
		Completeness: 0.1,
		Quality:      0.1,
		Integration:  0.1,
	}
	got2 := cl2.Category(w)
	if got2 != ConfidenceCritical {
		t.Errorf("Category = %v, want %v", got2, ConfidenceCritical)
	}
}
