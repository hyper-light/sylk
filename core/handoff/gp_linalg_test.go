package handoff

import (
	"math"
	"testing"
)

// known3x3 returns a known positive-definite 3×3 matrix and its Cholesky factor.
func known3x3() (*FlatMatrix, *FlatMatrix) {
	// A = [[4, 12, -16], [12, 37, -43], [-16, -43, 98]]
	// L = [[2, 0, 0], [6, 1, 0], [-8, 5, 3]]
	A := NewFlatMatrix(3, 3)
	A.Data = []float64{4, 12, -16, 12, 37, -43, -16, -43, 98}

	L := NewFlatMatrix(3, 3)
	L.Data = []float64{2, 0, 0, 6, 1, 0, -8, 5, 3}

	return A, L
}

func TestFlatMatrixBasics(t *testing.T) {
	m := NewFlatMatrix(3, 3)
	m.Set(0, 0, 1)
	m.Set(1, 2, 5)

	if m.At(0, 0) != 1 {
		t.Fatalf("At(0,0) = %f, want 1", m.At(0, 0))
	}
	if m.At(1, 2) != 5 {
		t.Fatalf("At(1,2) = %f, want 5", m.At(1, 2))
	}

	row := m.Row(1)
	if len(row) != 3 || row[2] != 5 {
		t.Fatalf("Row(1) = %v, want [0 0 5]", row)
	}
}

func TestFlatMatrixAddToDiagonal(t *testing.T) {
	m := NewFlatMatrix(3, 3)
	m.AddToDiagonal(2.5)

	for i := range 3 {
		if m.At(i, i) != 2.5 {
			t.Fatalf("diagonal[%d] = %f, want 2.5", i, m.At(i, i))
		}
	}
}

func TestCholeskyFlat_Known(t *testing.T) {
	A, expectedL := known3x3()

	L, err := CholeskyFlat(A)
	if err != nil {
		t.Fatalf("CholeskyFlat: %v", err)
	}

	for i := range 3 {
		for j := range 3 {
			if math.Abs(L.At(i, j)-expectedL.At(i, j)) > 1e-10 {
				t.Errorf("L[%d][%d] = %f, want %f", i, j, L.At(i, j), expectedL.At(i, j))
			}
		}
	}
}

func TestCholeskyFlat_Identity(t *testing.T) {
	n := 5
	A := NewFlatMatrix(n, n)
	for i := range n {
		A.Set(i, i, 1)
	}

	L, err := CholeskyFlat(A)
	if err != nil {
		t.Fatalf("CholeskyFlat(I): %v", err)
	}

	for i := range n {
		for j := range n {
			expected := 0.0
			if i == j {
				expected = 1.0
			}
			if math.Abs(L.At(i, j)-expected) > 1e-10 {
				t.Errorf("L[%d][%d] = %f, want %f", i, j, L.At(i, j), expected)
			}
		}
	}
}

func TestSolveTriangularFlat_RoundTrip(t *testing.T) {
	_, L := known3x3()

	// Choose a known x, compute b = L*x, then solve for x.
	x := []float64{1, 2, 3}
	b := make([]float64, 3)

	// b = L*x manually: b[i] = Σ L[i][j]*x[j] for j <= i
	for i := range 3 {
		for j := 0; j <= i; j++ {
			b[i] += L.At(i, j) * x[j]
		}
	}

	// Solve L*result = b
	result := make([]float64, 3)
	SolveTriangularFlat(L, b, true, result)

	for i := range 3 {
		if math.Abs(result[i]-x[i]) > 1e-10 {
			t.Errorf("x[%d] = %f, want %f", i, result[i], x[i])
		}
	}
}

func TestSolveTriangularFlat_Transpose(t *testing.T) {
	_, L := known3x3()

	// Choose x, compute b = Lᵀ*x, then solve Lᵀ*result = b.
	x := []float64{1, 2, 3}
	b := make([]float64, 3)

	// b = Lᵀ*x: b[i] = Σ L[j][i]*x[j] for j >= i
	for i := range 3 {
		for j := i; j < 3; j++ {
			b[i] += L.At(j, i) * x[j]
		}
	}

	result := make([]float64, 3)
	SolveTriangularFlat(L, b, false, result)

	for i := range 3 {
		if math.Abs(result[i]-x[i]) > 1e-10 {
			t.Errorf("x[%d] = %f, want %f", i, result[i], x[i])
		}
	}
}

func TestScaledDistanceVek_Reference(t *testing.T) {
	x1 := []float64{1, 2, 3}
	x2 := []float64{4, 6, 8}
	ls := []float64{1, 2, 5}

	// Reference scalar computation.
	var sum float64
	for d := range 3 {
		diff := (x1[d] - x2[d]) / ls[d]
		sum += diff * diff
	}
	expected := math.Sqrt(sum)

	got := ScaledDistanceVek(x1, x2, ls)
	if math.Abs(got-expected) > 1e-12 {
		t.Errorf("ScaledDistanceVek = %f, want %f", got, expected)
	}
}

func TestScaledDistanceVek_Zero(t *testing.T) {
	x := []float64{1, 2, 3}
	ls := []float64{1, 1, 1}
	got := ScaledDistanceVek(x, x, ls)
	if got != 0 {
		t.Errorf("distance to self = %f, want 0", got)
	}
}

func TestKernelMatrixFlat_Symmetric(t *testing.T) {
	obs := []*GPObservation{
		NewGPObservation(1000, 100, 5, 0.8),
		NewGPObservation(2000, 200, 10, 0.7),
		NewGPObservation(3000, 300, 15, 0.6),
	}
	hp := DefaultGPHyperparams()

	K := KernelMatrixFlat(obs, hp)

	for i := range 3 {
		for j := range 3 {
			if math.Abs(K.At(i, j)-K.At(j, i)) > 1e-12 {
				t.Errorf("K[%d][%d] = %f != K[%d][%d] = %f", i, j, K.At(i, j), j, i, K.At(j, i))
			}
		}
	}
}

func TestKernelMatrixFlat_PositiveDiagonal(t *testing.T) {
	obs := []*GPObservation{
		NewGPObservation(1000, 100, 5, 0.8),
		NewGPObservation(5000, 500, 25, 0.5),
	}
	hp := DefaultGPHyperparams()

	K := KernelMatrixFlat(obs, hp)

	for i := range 2 {
		if K.At(i, i) <= 0 {
			t.Errorf("K[%d][%d] = %f, want > 0", i, i, K.At(i, i))
		}
	}
}

func TestKernelVectorFlat_Length(t *testing.T) {
	obs := []*GPObservation{
		NewGPObservation(1000, 100, 5, 0.8),
		NewGPObservation(2000, 200, 10, 0.7),
	}
	hp := DefaultGPHyperparams()

	kStar := KernelVectorFlat([]float64{1500, 150, 7}, obs, hp)
	if len(kStar) != 2 {
		t.Fatalf("len(kStar) = %d, want 2", len(kStar))
	}
	for i, v := range kStar {
		if v <= 0 {
			t.Errorf("kStar[%d] = %f, want > 0", i, v)
		}
	}
}

func TestDotVek(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{4, 5, 6}
	got := DotVek(a, b)
	want := 32.0 // 1*4 + 2*5 + 3*6
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("DotVek = %f, want %f", got, want)
	}
}

// Benchmarks

func benchObservations(n int) []*GPObservation {
	obs := make([]*GPObservation, n)
	for i := range n {
		obs[i] = NewGPObservation(i*100, i*10, i, 0.8-float64(i)*0.005)
	}
	return obs
}

func BenchmarkCholeskyFlat_N50(b *testing.B) {
	obs := benchObservations(50)
	hp := DefaultGPHyperparams()
	b.ResetTimer()
	for range b.N {
		K := KernelMatrixFlat(obs, hp)
		_, _ = CholeskyFlat(K)
	}
}

func BenchmarkCholeskyFlat_N100(b *testing.B) {
	obs := benchObservations(100)
	hp := DefaultGPHyperparams()
	b.ResetTimer()
	for range b.N {
		K := KernelMatrixFlat(obs, hp)
		_, _ = CholeskyFlat(K)
	}
}

func BenchmarkKernelMatrixFlat_N50(b *testing.B) {
	obs := benchObservations(50)
	hp := DefaultGPHyperparams()
	b.ResetTimer()
	for range b.N {
		KernelMatrixFlat(obs, hp)
	}
}

func BenchmarkKernelMatrixFlat_N100(b *testing.B) {
	obs := benchObservations(100)
	hp := DefaultGPHyperparams()
	b.ResetTimer()
	for range b.N {
		KernelMatrixFlat(obs, hp)
	}
}

func BenchmarkPredictFromFeatures_N50(b *testing.B) {
	gp := NewAgentGaussianProcess(nil)
	for _, obs := range benchObservations(50) {
		gp.AddObservation(obs)
	}
	features := []float64{2500, 250, 12}
	b.ResetTimer()
	for range b.N {
		gp.PredictFromFeatures(features)
	}
}

func BenchmarkPredictFromFeatures_N100(b *testing.B) {
	gp := NewAgentGaussianProcess(nil)
	for _, obs := range benchObservations(100) {
		gp.AddObservation(obs)
	}
	features := []float64{5000, 500, 25}
	b.ResetTimer()
	for range b.N {
		gp.PredictFromFeatures(features)
	}
}
