package quantization

import (
	"math"
	"math/rand"
	"sync"
	"testing"
)

func createStreamingTestCentroids(numSubspaces, centroidsPerSub, subspaceDim int) [][][]float32 {
	centroids := make([][][]float32, numSubspaces)
	for m := 0; m < numSubspaces; m++ {
		centroids[m] = make([][]float32, centroidsPerSub)
		for c := 0; c < centroidsPerSub; c++ {
			centroids[m][c] = make([]float32, subspaceDim)
			for d := 0; d < subspaceDim; d++ {
				centroids[m][c][d] = rand.Float32()
			}
		}
	}
	return centroids
}

func generateTestVector(dim int, bias float32) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = rand.Float32() + bias
	}
	return v
}

func TestStreamingQuantizer_OnInsert(t *testing.T) {
	numSubspaces := 4
	centroidsPerSub := 8
	subspaceDim := 4
	dim := numSubspaces * subspaceDim

	centroids := createStreamingTestCentroids(numSubspaces, centroidsPerSub, subspaceDim)
	sq := NewStreamingQuantizer(centroids, 0.01)

	vector := generateTestVector(dim, 0)
	code := sq.OnInsert(vector)

	if len(code) != numSubspaces {
		t.Errorf("expected code length %d, got %d", numSubspaces, len(code))
	}

	for i, c := range code {
		if int(c) >= centroidsPerSub {
			t.Errorf("code[%d] = %d exceeds centroidsPerSub %d", i, c, centroidsPerSub)
		}
	}

	if sq.InsertCount() != 1 {
		t.Errorf("expected insertCount 1, got %d", sq.InsertCount())
	}
}

func TestStreamingQuantizer_OnInsert_Multiple(t *testing.T) {
	numSubspaces := 4
	centroidsPerSub := 8
	subspaceDim := 4
	dim := numSubspaces * subspaceDim

	centroids := createStreamingTestCentroids(numSubspaces, centroidsPerSub, subspaceDim)
	sq := NewStreamingQuantizer(centroids, 0.01)

	numInserts := 250
	for i := 0; i < numInserts; i++ {
		vector := generateTestVector(dim, 0)
		code := sq.OnInsert(vector)
		if len(code) != numSubspaces {
			t.Fatalf("insert %d: expected code length %d, got %d", i, numSubspaces, len(code))
		}
	}

	if sq.InsertCount() != numInserts {
		t.Errorf("expected insertCount %d, got %d", numInserts, sq.InsertCount())
	}
}

func TestStreamingQuantizer_DriftDetection(t *testing.T) {
	numSubspaces := 2
	centroidsPerSub := 4
	subspaceDim := 2
	dim := numSubspaces * subspaceDim

	centroids := make([][][]float32, numSubspaces)
	for m := 0; m < numSubspaces; m++ {
		centroids[m] = make([][]float32, centroidsPerSub)
		for c := 0; c < centroidsPerSub; c++ {
			centroids[m][c] = make([]float32, subspaceDim)
			for d := 0; d < subspaceDim; d++ {
				centroids[m][c][d] = float32(c) * 10.0
			}
		}
	}

	sq := NewStreamingQuantizer(centroids, 0.001)
	sq.SetCheckInterval(10)

	originalCentroids := sq.GetCentroids()

	for i := 0; i < 100; i++ {
		vector := make([]float32, dim)
		for d := range vector {
			vector[d] = 5.0 + rand.Float32()*0.1
		}
		sq.OnInsert(vector)
	}

	stats := sq.GetDriftStatistics()
	if stats["insertCount"] != 100 {
		t.Errorf("expected insertCount 100, got %f", stats["insertCount"])
	}

	updatedCentroids := sq.GetCentroids()

	changed := false
	for m := 0; m < numSubspaces; m++ {
		for c := 0; c < centroidsPerSub; c++ {
			for d := 0; d < subspaceDim; d++ {
				if originalCentroids[m][c][d] != updatedCentroids[m][c][d] {
					changed = true
					break
				}
			}
		}
	}

	if !changed {
		t.Log("No centroids changed - this may be expected if drift threshold wasn't exceeded")
	}
}

func TestStreamingQuantizer_Reset(t *testing.T) {
	numSubspaces := 4
	centroidsPerSub := 8
	subspaceDim := 4
	dim := numSubspaces * subspaceDim

	centroids := createStreamingTestCentroids(numSubspaces, centroidsPerSub, subspaceDim)
	sq := NewStreamingQuantizer(centroids, 0.01)

	for i := 0; i < 50; i++ {
		vector := generateTestVector(dim, 0)
		sq.OnInsert(vector)
	}

	if sq.InsertCount() != 50 {
		t.Errorf("expected insertCount 50, got %d", sq.InsertCount())
	}

	sq.Reset()

	if sq.InsertCount() != 0 {
		t.Errorf("expected insertCount 0 after reset, got %d", sq.InsertCount())
	}

	stats := sq.GetDriftStatistics()
	if stats["avgDrift"] != 0 {
		t.Errorf("expected avgDrift 0 after reset, got %f", stats["avgDrift"])
	}
}

func TestStreamingQuantizer_GetDriftStatistics(t *testing.T) {
	numSubspaces := 4
	centroidsPerSub := 8
	subspaceDim := 4
	dim := numSubspaces * subspaceDim

	centroids := createStreamingTestCentroids(numSubspaces, centroidsPerSub, subspaceDim)
	sq := NewStreamingQuantizer(centroids, 0.01)

	stats := sq.GetDriftStatistics()
	if stats["insertCount"] != 0 {
		t.Errorf("expected insertCount 0, got %f", stats["insertCount"])
	}
	if stats["avgDrift"] != 0 {
		t.Errorf("expected avgDrift 0 with no inserts, got %f", stats["avgDrift"])
	}

	for i := 0; i < 100; i++ {
		vector := generateTestVector(dim, 0)
		sq.OnInsert(vector)
	}

	stats = sq.GetDriftStatistics()
	if stats["insertCount"] != 100 {
		t.Errorf("expected insertCount 100, got %f", stats["insertCount"])
	}
	if math.IsNaN(stats["avgDrift"]) {
		t.Error("avgDrift is NaN")
	}
	if math.IsNaN(stats["maxDrift"]) {
		t.Error("maxDrift is NaN")
	}
}

func TestStreamingQuantizer_DeriveDriftThreshold(t *testing.T) {
	numSubspaces := 4
	centroidsPerSub := 8
	subspaceDim := 4
	dim := numSubspaces * subspaceDim

	centroids := createStreamingTestCentroids(numSubspaces, centroidsPerSub, subspaceDim)
	sq := NewStreamingQuantizer(centroids, 0.01)

	threshold := sq.DeriveDriftThreshold(nil)
	if threshold != 0.01 {
		t.Errorf("expected default threshold 0.01 for nil samples, got %f", threshold)
	}

	fewSamples := make([][]float32, 50)
	for i := range fewSamples {
		fewSamples[i] = generateTestVector(dim, 0)
	}
	threshold = sq.DeriveDriftThreshold(fewSamples)
	if threshold != 0.01 {
		t.Errorf("expected default threshold 0.01 for <100 samples, got %f", threshold)
	}

	samples := make([][]float32, 200)
	for i := range samples {
		samples[i] = generateTestVector(dim, 0)
	}
	threshold = sq.DeriveDriftThreshold(samples)
	if threshold < 0.001 {
		t.Errorf("expected threshold >= 0.001, got %f", threshold)
	}
	if threshold > 1.0 {
		t.Errorf("expected reasonable threshold < 1.0, got %f", threshold)
	}
}

func TestStreamingQuantizer_ThreadSafety(t *testing.T) {
	numSubspaces := 4
	centroidsPerSub := 8
	subspaceDim := 4
	dim := numSubspaces * subspaceDim

	centroids := createStreamingTestCentroids(numSubspaces, centroidsPerSub, subspaceDim)
	sq := NewStreamingQuantizer(centroids, 0.01)

	var wg sync.WaitGroup
	numGoroutines := 10
	insertsPerGoroutine := 100

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < insertsPerGoroutine; i++ {
				vector := generateTestVector(dim, 0)
				code := sq.OnInsert(vector)
				if len(code) != numSubspaces {
					t.Errorf("unexpected code length")
				}
			}
		}()
	}

	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				stats := sq.GetDriftStatistics()
				if stats == nil {
					t.Error("got nil stats")
				}
			}
		}()
	}

	wg.Wait()

	expectedInserts := numGoroutines * insertsPerGoroutine
	if sq.InsertCount() != expectedInserts {
		t.Errorf("expected insertCount %d, got %d", expectedInserts, sq.InsertCount())
	}
}

func TestStreamingQuantizer_EmptyCentroids(t *testing.T) {
	sq := NewStreamingQuantizer(nil, 0.01)
	if sq.numSubspaces != 0 {
		t.Errorf("expected 0 subspaces for nil centroids, got %d", sq.numSubspaces)
	}

	emptyCentroids := [][][]float32{}
	sq = NewStreamingQuantizer(emptyCentroids, 0.01)
	if sq.numSubspaces != 0 {
		t.Errorf("expected 0 subspaces for empty centroids, got %d", sq.numSubspaces)
	}
}

func TestStreamingQuantizer_DefaultDriftThreshold(t *testing.T) {
	centroids := createStreamingTestCentroids(4, 8, 4)

	sq := NewStreamingQuantizer(centroids, 0)
	if sq.driftThreshold != 0.01 {
		t.Errorf("expected default drift threshold 0.01, got %f", sq.driftThreshold)
	}

	sq = NewStreamingQuantizer(centroids, -1)
	if sq.driftThreshold != 0.01 {
		t.Errorf("expected default drift threshold 0.01 for negative input, got %f", sq.driftThreshold)
	}

	sq = NewStreamingQuantizer(centroids, 0.05)
	if sq.driftThreshold != 0.05 {
		t.Errorf("expected drift threshold 0.05, got %f", sq.driftThreshold)
	}
}

func TestStreamingQuantizer_SetCheckInterval(t *testing.T) {
	centroids := createStreamingTestCentroids(4, 8, 4)
	sq := NewStreamingQuantizer(centroids, 0.01)

	sq.SetCheckInterval(50)

	sq.SetCheckInterval(0)
	sq.SetCheckInterval(-1)
}

func TestStreamingQuantizer_CodeConsistency(t *testing.T) {
	numSubspaces := 4
	centroidsPerSub := 8
	subspaceDim := 4
	dim := numSubspaces * subspaceDim

	centroids := createStreamingTestCentroids(numSubspaces, centroidsPerSub, subspaceDim)
	sq := NewStreamingQuantizer(centroids, 1000.0)

	vector := generateTestVector(dim, 0)
	code1 := sq.OnInsert(vector)
	code2 := sq.OnInsert(vector)

	for i := range code1 {
		if code1[i] != code2[i] {
			t.Errorf("code mismatch at position %d: %d vs %d", i, code1[i], code2[i])
		}
	}
}
