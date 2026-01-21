package quantization

import (
	"math"
	"testing"
)

func TestSAQRefiner_Refine(t *testing.T) {
	centroids := createTestCentroids(4, 8, 4)

	refiner := NewSAQRefiner(centroids, 5, 1e-6)

	vector := make([]float32, 16)
	for i := range vector {
		vector[i] = float32(i) * 0.1
	}

	initialCode := []uint8{0, 0, 0, 0}

	refined := refiner.Refine(vector, initialCode)

	if len(refined) != len(initialCode) {
		t.Fatalf("expected refined code length %d, got %d", len(initialCode), len(refined))
	}

	initialError := refiner.reconstructionError(vector, initialCode)
	refinedError := refiner.reconstructionError(vector, refined)

	if refinedError > initialError {
		t.Errorf("refinement increased error: initial=%f, refined=%f", initialError, refinedError)
	}
}

func TestSAQRefiner_RefineWithMismatchedCode(t *testing.T) {
	centroids := createTestCentroids(4, 8, 4)
	refiner := NewSAQRefiner(centroids, 5, 1e-6)

	vector := make([]float32, 16)
	wrongSizeCode := []uint8{0, 0}

	result := refiner.Refine(vector, wrongSizeCode)

	if len(result) != len(wrongSizeCode) {
		t.Errorf("expected unchanged code for mismatched size")
	}
}

func TestSAQRefiner_ReconstructionError(t *testing.T) {
	centroids := [][][]float32{
		{{1.0, 0.0}, {0.0, 1.0}},
		{{0.5, 0.5}, {-0.5, -0.5}},
	}

	refiner := NewSAQRefiner(centroids, 5, 1e-6)

	code := []uint8{0, 0}
	reconstructed := refiner.reconstruct(code)

	expected := []float32{1.0, 0.0, 0.5, 0.5}
	for i, v := range expected {
		if i >= len(reconstructed) || math.Abs(float64(reconstructed[i]-v)) > 1e-6 {
			t.Errorf("reconstruction mismatch at %d: expected %f, got %f", i, v, reconstructed[i])
		}
	}

	vector := []float32{1.0, 0.0, 0.5, 0.5}
	err := refiner.reconstructionError(vector, code)
	if err > 1e-10 {
		t.Errorf("expected zero error for exact match, got %f", err)
	}

	vector2 := []float32{2.0, 0.0, 0.5, 0.5}
	err2 := refiner.reconstructionError(vector2, code)
	expectedErr := float32(1.0)
	if math.Abs(float64(err2-expectedErr)) > 1e-6 {
		t.Errorf("expected error %f, got %f", expectedErr, err2)
	}
}

func TestSAQRefiner_BatchRefine(t *testing.T) {
	centroids := createTestCentroids(4, 8, 4)
	refiner := NewSAQRefiner(centroids, 5, 1e-6)

	vectors := make([][]float32, 10)
	codes := make([][]uint8, 10)
	for i := range vectors {
		vectors[i] = make([]float32, 16)
		for j := range vectors[i] {
			vectors[i][j] = float32(i*16+j) * 0.01
		}
		codes[i] = []uint8{0, 0, 0, 0}
	}

	refined := refiner.BatchRefine(vectors, codes)

	if len(refined) != len(vectors) {
		t.Fatalf("expected %d refined codes, got %d", len(vectors), len(refined))
	}

	for i := range vectors {
		initialErr := refiner.reconstructionError(vectors[i], codes[i])
		refinedErr := refiner.reconstructionError(vectors[i], refined[i])
		if refinedErr > initialErr+1e-6 {
			t.Errorf("vector %d: refinement increased error from %f to %f", i, initialErr, refinedErr)
		}
	}
}

func TestSAQRefiner_ComputeAverageImprovement(t *testing.T) {
	centroids := createTestCentroids(4, 16, 4)
	refiner := NewSAQRefiner(centroids, 5, 1e-6)

	vectors := make([][]float32, 20)
	codes := make([][]uint8, 20)
	for i := range vectors {
		vectors[i] = make([]float32, 16)
		for j := range vectors[i] {
			vectors[i][j] = float32(j) * 0.1
		}
		codes[i] = []uint8{0, 0, 0, 0}
	}

	improvement := refiner.ComputeAverageImprovement(vectors, codes)

	if improvement < 0 || improvement > 1 {
		t.Errorf("improvement should be between 0 and 1, got %f", improvement)
	}

	emptyImprovement := refiner.ComputeAverageImprovement(nil, nil)
	if emptyImprovement != 0 {
		t.Errorf("expected 0 improvement for empty input, got %f", emptyImprovement)
	}
}

func TestSAQRefiner_DeriveToleranceFromData(t *testing.T) {
	centroids := createTestCentroids(4, 8, 4)
	refiner := NewSAQRefiner(centroids, 5, 1e-6)

	vectors := make([][]float32, 100)
	for i := range vectors {
		vectors[i] = make([]float32, 16)
		for j := range vectors[i] {
			vectors[i][j] = float32(j) + float32(i)*0.01
		}
	}

	tolerance := refiner.DeriveToleranceFromData(vectors)

	if tolerance <= 0 {
		t.Errorf("tolerance should be positive, got %f", tolerance)
	}

	emptyTolerance := refiner.DeriveToleranceFromData(nil)
	if emptyTolerance != 1e-6 {
		t.Errorf("expected default tolerance 1e-6 for empty input, got %f", emptyTolerance)
	}
}

func TestSAQRefiner_DefaultValues(t *testing.T) {
	centroids := createTestCentroids(2, 4, 2)

	refiner := NewSAQRefiner(centroids, 0, 0)

	if refiner.maxIterations != 5 {
		t.Errorf("expected default maxIterations=5, got %d", refiner.maxIterations)
	}
	if refiner.tolerance != 1e-6 {
		t.Errorf("expected default tolerance=1e-6, got %f", refiner.tolerance)
	}
}

func TestSAQRefiner_EmptyCentroids(t *testing.T) {
	refiner := NewSAQRefiner(nil, 5, 1e-6)

	if refiner.numSubspaces != 0 {
		t.Errorf("expected 0 subspaces for nil centroids, got %d", refiner.numSubspaces)
	}

	vector := []float32{1.0, 2.0}
	code := []uint8{}
	result := refiner.Refine(vector, code)
	if len(result) != 0 {
		t.Errorf("expected empty result for empty code")
	}
}

func TestSAQRefiner_CoordinateDescentConvergence(t *testing.T) {
	centroids := [][][]float32{
		{{0.0}, {1.0}, {2.0}, {3.0}},
		{{0.0}, {1.0}, {2.0}, {3.0}},
	}

	refiner := NewSAQRefiner(centroids, 10, 1e-8)

	vector := []float32{1.0, 2.0}

	initialCode := []uint8{0, 0}
	refined := refiner.Refine(vector, initialCode)

	reconstructed := refiner.reconstruct(refined)
	expectedBest := []float32{1.0, 2.0}

	for i, v := range expectedBest {
		if i >= len(reconstructed) || math.Abs(float64(reconstructed[i]-v)) > 1e-5 {
			t.Errorf("coordinate descent did not converge to optimal: expected %v, got %v", expectedBest, reconstructed)
			break
		}
	}
}

func TestSAQRefiner_OutOfBoundsCentroid(t *testing.T) {
	centroids := createTestCentroids(2, 4, 2)
	refiner := NewSAQRefiner(centroids, 5, 1e-6)

	code := []uint8{10, 10}

	reconstructed := refiner.reconstruct(code)

	if len(reconstructed) != 4 {
		t.Errorf("expected reconstructed length 4, got %d", len(reconstructed))
	}
}

func createTestCentroids(numSubspaces, centroidsPerSubspace, subspaceDim int) [][][]float32 {
	centroids := make([][][]float32, numSubspaces)
	for m := 0; m < numSubspaces; m++ {
		centroids[m] = make([][]float32, centroidsPerSubspace)
		for c := 0; c < centroidsPerSubspace; c++ {
			centroids[m][c] = make([]float32, subspaceDim)
			for d := 0; d < subspaceDim; d++ {
				centroids[m][c][d] = float32(c) * 0.1
			}
		}
	}
	return centroids
}
