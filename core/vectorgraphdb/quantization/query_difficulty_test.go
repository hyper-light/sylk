package quantization

import (
	"math"
	"sync"
	"testing"
)

func TestQueryDifficultyEstimator_NewEstimator(t *testing.T) {
	// Test with nil centroids
	e := NewQueryDifficultyEstimator(nil)
	if e == nil {
		t.Fatal("expected non-nil estimator")
	}
	if len(e.partitionCentroids) != 0 {
		t.Errorf("expected 0 centroids, got %d", len(e.partitionCentroids))
	}

	// Test with centroids
	centroids := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}
	e = NewQueryDifficultyEstimator(centroids)
	if len(e.partitionCentroids) != 3 {
		t.Errorf("expected 3 centroids, got %d", len(e.partitionCentroids))
	}
	if len(e.partitionDifficulty) != 3 {
		t.Errorf("expected 3 partition difficulties, got %d", len(e.partitionDifficulty))
	}

	// Check initial values
	if e.avgQueryVariance != 1.0 {
		t.Errorf("expected avgQueryVariance=1.0, got %f", e.avgQueryVariance)
	}
	if e.avgQueryEntropy != 1.0 {
		t.Errorf("expected avgQueryEntropy=1.0, got %f", e.avgQueryEntropy)
	}
	for i, d := range e.partitionDifficulty {
		if d != 1.0 {
			t.Errorf("expected partitionDifficulty[%d]=1.0, got %f", i, d)
		}
	}
}

func TestQueryDifficultyEstimator_EstimateDifficulty(t *testing.T) {
	centroids := [][]float32{
		{1.0, 0.0, 0.0, 0.0},
		{0.0, 1.0, 0.0, 0.0},
	}
	e := NewQueryDifficultyEstimator(centroids)

	// Test basic estimation
	query := []float32{0.5, 0.5, 0.5, 0.5}
	difficulty := e.EstimateDifficulty(query)

	// Should be in valid range
	if difficulty < 0.5 || difficulty > 2.0 {
		t.Errorf("difficulty %f outside valid range [0.5, 2.0]", difficulty)
	}

	// High variance query should be easier
	highVarQuery := []float32{10.0, -10.0, 5.0, -5.0}
	lowVarQuery := []float32{0.1, 0.1, 0.1, 0.1}

	highVarDiff := e.EstimateDifficulty(highVarQuery)
	lowVarDiff := e.EstimateDifficulty(lowVarQuery)

	// High variance = easier = lower difficulty
	if highVarDiff >= lowVarDiff {
		t.Errorf("expected high variance query to be easier: highVar=%f, lowVar=%f", highVarDiff, lowVarDiff)
	}
}

func TestQueryDifficultyEstimator_AdaptEfSearch(t *testing.T) {
	e := NewQueryDifficultyEstimator(nil)

	baseEf := 100
	query := []float32{1.0, 2.0, 3.0, 4.0}

	adaptedEf := e.AdaptEfSearch(baseEf, query)

	// Should be in reasonable range (baseEf * [0.5, 2.0])
	minExpected := int(float64(baseEf) * 0.5)
	maxExpected := int(float64(baseEf) * 2.0)

	if adaptedEf < minExpected || adaptedEf > maxExpected {
		t.Errorf("adapted ef %d outside expected range [%d, %d]", adaptedEf, minExpected, maxExpected)
	}
}

func TestQueryDifficultyEstimator_RecordSearch(t *testing.T) {
	centroids := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
	}
	e := NewQueryDifficultyEstimator(centroids)

	// Record some easy searches near centroid 0
	for i := 0; i < 50; i++ {
		query := []float32{1.0 + float32(i)*0.01, 0.1, 0.1}
		e.RecordSearch(query, 50, 0.95) // Low iterations = easy
	}

	// Record some hard searches near centroid 1
	for i := 0; i < 50; i++ {
		query := []float32{0.1, 1.0 + float32(i)*0.01, 0.1}
		e.RecordSearch(query, 200, 0.85) // High iterations = hard
	}

	stats := e.GetStatistics()

	// Partition 0 should be easier than partition 1
	if len(stats.PartitionDifficulty) != 2 {
		t.Fatalf("expected 2 partition difficulties, got %d", len(stats.PartitionDifficulty))
	}

	// After training, partition 0 (easy) should have lower difficulty than partition 1 (hard)
	if stats.PartitionDifficulty[0] >= stats.PartitionDifficulty[1] {
		t.Errorf("expected partition 0 to be easier: partition0=%f, partition1=%f",
			stats.PartitionDifficulty[0], stats.PartitionDifficulty[1])
	}

	// Check observation count
	if stats.ObservationCount != 100 {
		t.Errorf("expected 100 observations, got %d", stats.ObservationCount)
	}
}

func TestQueryDifficultyEstimator_ComputeVariance(t *testing.T) {
	e := NewQueryDifficultyEstimator(nil)

	// Uniform vector should have zero variance
	uniform := []float32{1.0, 1.0, 1.0, 1.0}
	variance := e.computeVariance(uniform)
	if variance > 1e-10 {
		t.Errorf("expected near-zero variance for uniform vector, got %f", variance)
	}

	// Variable vector should have positive variance
	variable := []float32{1.0, 2.0, 3.0, 4.0}
	variance = e.computeVariance(variable)
	if variance <= 0 {
		t.Errorf("expected positive variance for variable vector, got %f", variance)
	}

	// Empty vector
	empty := []float32{}
	variance = e.computeVariance(empty)
	if variance != 0 {
		t.Errorf("expected 0 variance for empty vector, got %f", variance)
	}
}

func TestQueryDifficultyEstimator_ComputeEntropy(t *testing.T) {
	e := NewQueryDifficultyEstimator(nil)

	// Single non-zero element = zero entropy
	sparse := []float32{1.0, 0.0, 0.0, 0.0}
	entropy := e.computeEntropy(sparse)
	if entropy > 1e-10 {
		t.Errorf("expected near-zero entropy for sparse vector, got %f", entropy)
	}

	// Uniform non-zero elements = maximum entropy
	uniform := []float32{1.0, 1.0, 1.0, 1.0}
	uniformEntropy := e.computeEntropy(uniform)

	// Skewed distribution = lower entropy
	skewed := []float32{10.0, 1.0, 1.0, 1.0}
	skewedEntropy := e.computeEntropy(skewed)

	if uniformEntropy <= skewedEntropy {
		t.Errorf("expected uniform to have higher entropy: uniform=%f, skewed=%f", uniformEntropy, skewedEntropy)
	}

	// Zero vector
	zero := []float32{0.0, 0.0, 0.0, 0.0}
	zeroEntropy := e.computeEntropy(zero)
	if zeroEntropy != 0 {
		t.Errorf("expected 0 entropy for zero vector, got %f", zeroEntropy)
	}
}

func TestQueryDifficultyEstimator_FindNearestPartition(t *testing.T) {
	centroids := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}
	e := NewQueryDifficultyEstimator(centroids)

	// Query near centroid 0
	query0 := []float32{0.9, 0.1, 0.0}
	idx := e.findNearestPartition(query0)
	if idx != 0 {
		t.Errorf("expected partition 0 for query near centroid 0, got %d", idx)
	}

	// Query near centroid 1
	query1 := []float32{0.0, 0.9, 0.1}
	idx = e.findNearestPartition(query1)
	if idx != 1 {
		t.Errorf("expected partition 1 for query near centroid 1, got %d", idx)
	}

	// Query near centroid 2
	query2 := []float32{0.1, 0.0, 0.9}
	idx = e.findNearestPartition(query2)
	if idx != 2 {
		t.Errorf("expected partition 2 for query near centroid 2, got %d", idx)
	}

	// No centroids
	emptyEstimator := NewQueryDifficultyEstimator(nil)
	idx = emptyEstimator.findNearestPartition(query0)
	if idx != -1 {
		t.Errorf("expected -1 for no centroids, got %d", idx)
	}
}

func TestQueryDifficultyEstimator_SquaredL2(t *testing.T) {
	e := NewQueryDifficultyEstimator(nil)

	a := []float32{1.0, 2.0, 3.0}
	b := []float32{4.0, 5.0, 6.0}

	dist := e.squaredL2(a, b)
	expected := 27.0
	if math.Abs(dist-expected) > 1e-6 {
		t.Errorf("expected squared L2 = %f, got %f", expected, dist)
	}

	dist = e.squaredL2(a, a)
	if dist != 0 {
		t.Errorf("expected 0 distance for same vector, got %f", dist)
	}
}

func TestQueryDifficultyEstimator_ThreadSafety(t *testing.T) {
	centroids := [][]float32{
		{1.0, 0.0, 0.0, 0.0},
		{0.0, 1.0, 0.0, 0.0},
	}
	e := NewQueryDifficultyEstimator(centroids)

	var wg sync.WaitGroup
	numGoroutines := 10
	numIterations := 100

	// Concurrent reads and writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				query := []float32{float32(id), float32(j), 0.5, 0.5}

				// Concurrent estimation
				_ = e.EstimateDifficulty(query)
				_ = e.AdaptEfSearch(100, query)

				// Concurrent recording
				e.RecordSearch(query, 50+id+j, 0.9)

				// Concurrent stats read
				_ = e.GetStatistics()
			}
		}(i)
	}

	wg.Wait()

	// Verify state is consistent
	stats := e.GetStatistics()
	expectedObs := numGoroutines * numIterations
	if stats.ObservationCount != expectedObs {
		t.Errorf("expected %d observations, got %d", expectedObs, stats.ObservationCount)
	}
}

func TestQueryDifficultyEstimator_DifficultyRangeClamping(t *testing.T) {
	e := NewQueryDifficultyEstimator(nil)

	// Test extreme queries to ensure clamping works

	// Very high variance query (should tend toward easy = 0.5)
	highVar := []float32{1000.0, -1000.0, 500.0, -500.0}
	diff := e.EstimateDifficulty(highVar)
	if diff < 0.5 || diff > 2.0 {
		t.Errorf("difficulty %f outside clamped range [0.5, 2.0]", diff)
	}

	// Very low variance query (should tend toward hard = 2.0)
	lowVar := []float32{0.001, 0.001, 0.001, 0.001}
	diff = e.EstimateDifficulty(lowVar)
	if diff < 0.5 || diff > 2.0 {
		t.Errorf("difficulty %f outside clamped range [0.5, 2.0]", diff)
	}

	// Zero query
	zero := []float32{0.0, 0.0, 0.0, 0.0}
	diff = e.EstimateDifficulty(zero)
	if diff < 0.5 || diff > 2.0 {
		t.Errorf("difficulty %f outside clamped range [0.5, 2.0]", diff)
	}
}

func TestQueryDifficultyEstimator_LearningRateAdaptation(t *testing.T) {
	e := NewQueryDifficultyEstimator(nil)

	query := []float32{1.0, 2.0, 3.0, 4.0}

	// First few observations should have high learning rate (1/n)
	for i := 1; i <= 10; i++ {
		e.RecordSearch(query, 100, 0.9)
		stats := e.GetStatistics()
		if stats.ObservationCount != i {
			t.Errorf("expected %d observations, got %d", i, stats.ObservationCount)
		}
	}

	// After 100 observations, learning rate should be fixed at 0.01
	for i := 11; i <= 150; i++ {
		e.RecordSearch(query, 100, 0.9)
	}

	stats := e.GetStatistics()
	if stats.ObservationCount != 150 {
		t.Errorf("expected 150 observations, got %d", stats.ObservationCount)
	}
}

func TestQueryDifficultyEstimator_GetStatistics(t *testing.T) {
	centroids := [][]float32{
		{1.0, 0.0},
		{0.0, 1.0},
	}
	e := NewQueryDifficultyEstimator(centroids)

	// Initial statistics
	stats := e.GetStatistics()
	if stats.AvgQueryVariance != 1.0 {
		t.Errorf("expected initial avgQueryVariance=1.0, got %f", stats.AvgQueryVariance)
	}
	if stats.AvgQueryEntropy != 1.0 {
		t.Errorf("expected initial avgQueryEntropy=1.0, got %f", stats.AvgQueryEntropy)
	}
	if stats.AvgSearchIterations != 100.0 {
		t.Errorf("expected initial avgSearchIterations=100.0, got %f", stats.AvgSearchIterations)
	}
	if stats.ObservationCount != 0 {
		t.Errorf("expected initial observationCount=0, got %d", stats.ObservationCount)
	}
	if len(stats.PartitionDifficulty) != 2 {
		t.Errorf("expected 2 partition difficulties, got %d", len(stats.PartitionDifficulty))
	}

	// Returned slice should be a copy
	stats.PartitionDifficulty[0] = 999.0
	stats2 := e.GetStatistics()
	if stats2.PartitionDifficulty[0] == 999.0 {
		t.Error("GetStatistics should return a copy, not the internal slice")
	}
}

func TestQueryDifficultyEstimator_EmptyQuery(t *testing.T) {
	e := NewQueryDifficultyEstimator(nil)

	// Empty query should not panic
	empty := []float32{}
	diff := e.EstimateDifficulty(empty)
	if diff < 0.5 || diff > 2.0 {
		t.Errorf("difficulty %f outside clamped range for empty query", diff)
	}

	ef := e.AdaptEfSearch(100, empty)
	if ef < 50 || ef > 200 {
		t.Errorf("adapted ef %d outside expected range for empty query", ef)
	}

	// Recording empty query should not panic
	e.RecordSearch(empty, 50, 0.9)
}

func BenchmarkQueryDifficultyEstimator_EstimateDifficulty(b *testing.B) {
	centroids := make([][]float32, 16)
	for i := range centroids {
		centroids[i] = make([]float32, 128)
		centroids[i][i%128] = 1.0
	}
	e := NewQueryDifficultyEstimator(centroids)

	query := make([]float32, 128)
	for i := range query {
		query[i] = float32(i) / 128.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.EstimateDifficulty(query)
	}
}

func BenchmarkQueryDifficultyEstimator_RecordSearch(b *testing.B) {
	centroids := make([][]float32, 16)
	for i := range centroids {
		centroids[i] = make([]float32, 128)
		centroids[i][i%128] = 1.0
	}
	e := NewQueryDifficultyEstimator(centroids)

	query := make([]float32, 128)
	for i := range query {
		query[i] = float32(i) / 128.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.RecordSearch(query, 100, 0.9)
	}
}
