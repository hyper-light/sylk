package hnsw

import (
	"fmt"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// W4P.17: Tests for consistent distance calculation between live and snapshot search

func TestW4P17_LiveVsSnapshot_DistanceConsistency(t *testing.T) {
	// Test that live search and snapshot search produce identical distances
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	queries := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
		{0.577, 0.577, 0.577}, // normalized diagonal
		{0.9, 0.1, 0.0},
		{0.5, 0.5, 0.0},
	}

	for _, query := range queries {
		liveResults := idx.Search(query, 5, nil)
		snapResults := snap.Search(query, 5, nil)

		require.Equal(t, len(liveResults), len(snapResults),
			"Result count mismatch for query %v", query)

		for i := range liveResults {
			assert.Equal(t, liveResults[i].ID, snapResults[i].ID,
				"ID mismatch at position %d for query %v", i, query)
			// Use exact equality - no rounding differences allowed
			assert.Equal(t, liveResults[i].Similarity, snapResults[i].Similarity,
				"Similarity mismatch at position %d for query %v: live=%v, snap=%v",
				i, query, liveResults[i].Similarity, snapResults[i].Similarity)
		}
	}
}

func TestW4P17_CosineDistance_SharedFunction(t *testing.T) {
	// Verify CosineDistance function produces consistent results
	vec1 := []float32{1.0, 0.0, 0.0}
	vec2 := []float32{0.0, 1.0, 0.0}
	vec3 := []float32{1.0, 0.0, 0.0} // same as vec1
	vec4 := []float32{0.577, 0.577, 0.577}

	mag1 := Magnitude(vec1)
	mag2 := Magnitude(vec2)
	mag3 := Magnitude(vec3)
	mag4 := Magnitude(vec4)

	// Orthogonal vectors should have distance 1.0
	dist12 := CosineDistance(vec1, vec2, mag1, mag2)
	assert.InDelta(t, 1.0, dist12, 1e-10, "Orthogonal vectors should have distance 1.0")

	// Same vectors should have distance 0.0
	dist13 := CosineDistance(vec1, vec3, mag1, mag3)
	assert.InDelta(t, 0.0, dist13, 1e-10, "Identical vectors should have distance 0.0")

	// Verify CosineDistance = 1 - CosineSimilarity
	sim14 := CosineSimilarity(vec1, vec4, mag1, mag4)
	dist14 := CosineDistance(vec1, vec4, mag1, mag4)
	assert.InDelta(t, 1.0-sim14, dist14, 1e-10,
		"CosineDistance should equal 1 - CosineSimilarity")
}

func TestW4P17_DistancePrecision_NoFloatingPointIssues(t *testing.T) {
	// Test with values that could cause floating point issues
	testCases := []struct {
		name string
		vec1 []float32
		vec2 []float32
	}{
		{
			name: "very small values",
			vec1: []float32{1e-6, 1e-6, 1e-6},
			vec2: []float32{1e-6, 1e-6, 1e-6},
		},
		{
			name: "very large values",
			vec1: []float32{1e6, 1e6, 1e6},
			vec2: []float32{1e6, 1e6, 1e6},
		},
		{
			name: "mixed scale values",
			vec1: []float32{1e-3, 1.0, 1e3},
			vec2: []float32{1e-3, 1.0, 1e3},
		},
		{
			name: "near-identical but different",
			vec1: []float32{1.0, 0.0, 0.0},
			vec2: []float32{0.9999999, 1e-7, 0.0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mag1 := Magnitude(tc.vec1)
			mag2 := Magnitude(tc.vec2)

			// Distance should be deterministic
			dist1 := CosineDistance(tc.vec1, tc.vec2, mag1, mag2)
			dist2 := CosineDistance(tc.vec1, tc.vec2, mag1, mag2)
			assert.Equal(t, dist1, dist2, "Same inputs should produce exact same distance")

			// Distance should be in valid range [0, 2] with small epsilon for floating point
			// Note: floating point arithmetic can produce tiny negative values for nearly
			// identical vectors due to precision limits
			assert.GreaterOrEqual(t, dist1, -1e-14, "Distance should be >= 0 (within floating point tolerance)")
			assert.LessOrEqual(t, dist1, 2.0, "Distance should be <= 2")

			// Verify consistency: 1 - similarity = distance
			sim := CosineSimilarity(tc.vec1, tc.vec2, mag1, mag2)
			assert.Equal(t, 1.0-sim, dist1,
				"CosineDistance and CosineSimilarity should be perfectly consistent")
		})
	}
}

func TestW4P17_LiveVsSnapshot_RankingConsistency(t *testing.T) {
	// Test that ranking order is identical between live and snapshot
	cfg := DefaultConfig()
	cfg.EfSearch = 100
	idx := New(cfg)

	// Insert many vectors to make ranking meaningful
	for i := 0; i < 100; i++ {
		vec := []float32{
			float32(i%10) * 0.1,
			float32((i/10)%10) * 0.1,
			float32(i%7) * 0.1,
		}
		err := idx.Insert(
			fmt.Sprintf("vec%d", i),
			vec,
			vectorgraphdb.DomainCode,
			vectorgraphdb.NodeTypeFunction,
		)
		require.NoError(t, err)
	}

	snap := NewHNSWSnapshot(idx, 1)

	// Test with several queries
	queries := [][]float32{
		{0.5, 0.5, 0.5},
		{0.0, 0.0, 0.1},
		{0.9, 0.0, 0.0},
	}

	for _, query := range queries {
		k := 20
		liveResults := idx.Search(query, k, nil)
		snapResults := snap.Search(query, k, nil)

		// Verify identical ranking
		for i := range liveResults {
			assert.Equal(t, liveResults[i].ID, snapResults[i].ID,
				"Ranking mismatch at position %d", i)
		}
	}
}

func TestW4P17_SnapshotDistance_ValidAndMissingVectors(t *testing.T) {
	snap := &HNSWSnapshot{
		Vectors:    map[string][]float32{"exists": {1.0, 0.0}},
		Magnitudes: map[string]float64{"exists": 1.0},
	}

	query := []float32{1.0, 0.0}
	queryMag := 1.0

	// Valid vector should return proper distance
	distValid := snap.distance(query, queryMag, "exists")
	assert.InDelta(t, 0.0, distValid, 1e-10, "Distance to identical vector should be 0")

	// Missing vector should return max distance (2.0)
	distMissing := snap.distance(query, queryMag, "missing")
	assert.Equal(t, 2.0, distMissing, "Missing vector should return max distance")

	// Missing magnitude should return max distance (2.0)
	snap.Vectors["nomag"] = []float32{0.0, 1.0}
	distNoMag := snap.distance(query, queryMag, "nomag")
	assert.Equal(t, 2.0, distNoMag, "Missing magnitude should return max distance")
}

func TestW4P17_ZeroMagnitude_HandledConsistently(t *testing.T) {
	// Zero magnitude vectors should be handled consistently
	zeroVec := []float32{0.0, 0.0, 0.0}
	normalVec := []float32{1.0, 0.0, 0.0}

	zeroMag := Magnitude(zeroVec)
	normalMag := Magnitude(normalVec)

	assert.Equal(t, 0.0, zeroMag, "Zero vector should have zero magnitude")

	// CosineSimilarity with zero magnitude should return 0
	sim := CosineSimilarity(normalVec, zeroVec, normalMag, zeroMag)
	assert.Equal(t, 0.0, sim, "Similarity with zero vector should be 0")

	// CosineDistance with zero magnitude should return 1 (since 1 - 0 = 1)
	dist := CosineDistance(normalVec, zeroVec, normalMag, zeroMag)
	assert.Equal(t, 1.0, dist, "Distance to zero vector should be 1.0")
}

func TestW4P17_LiveVsSnapshot_ConcurrentConsistency(t *testing.T) {
	// Test consistency under concurrent access
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			query := []float32{float32(id%3) * 0.33, float32((id+1)%3) * 0.33, float32((id+2)%3) * 0.34}

			liveResults := idx.Search(query, 3, nil)
			snapResults := snap.Search(query, 3, nil)

			if len(liveResults) != len(snapResults) {
				errors <- fmt.Errorf("goroutine %d: result count mismatch", id)
				return
			}

			for j := range liveResults {
				if liveResults[j].Similarity != snapResults[j].Similarity {
					errors <- fmt.Errorf("goroutine %d: similarity mismatch at %d", id, j)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

func TestW4P17_CosineDistance_Commutativity(t *testing.T) {
	// CosineDistance should be commutative: d(a,b) = d(b,a)
	vec1 := []float32{0.8, 0.2, 0.5}
	vec2 := []float32{0.3, 0.9, 0.1}

	mag1 := Magnitude(vec1)
	mag2 := Magnitude(vec2)

	dist12 := CosineDistance(vec1, vec2, mag1, mag2)
	dist21 := CosineDistance(vec2, vec1, mag2, mag1)

	assert.Equal(t, dist12, dist21, "CosineDistance should be commutative")
}

func TestW4P17_CosineDistance_TriangleInequality(t *testing.T) {
	// While cosine distance doesn't strictly satisfy triangle inequality,
	// we verify consistent behavior with known vectors
	vec1 := []float32{1.0, 0.0, 0.0}
	vec2 := []float32{0.707, 0.707, 0.0} // 45 degrees from vec1
	vec3 := []float32{0.0, 1.0, 0.0}     // 90 degrees from vec1

	mag1 := Magnitude(vec1)
	mag2 := Magnitude(vec2)
	mag3 := Magnitude(vec3)

	dist12 := CosineDistance(vec1, vec2, mag1, mag2)
	dist23 := CosineDistance(vec2, vec3, mag2, mag3)
	dist13 := CosineDistance(vec1, vec3, mag1, mag3)

	// vec1 to vec2 is 45 degrees, vec2 to vec3 is 45 degrees, vec1 to vec3 is 90 degrees
	// Distance should increase with angle
	assert.Less(t, dist12, dist13, "45-degree distance should be less than 90-degree")
	assert.Less(t, dist23, dist13, "45-degree distance should be less than 90-degree")
	assert.InDelta(t, 1.0, dist13, 0.001, "Orthogonal vectors should have distance ~1.0")
}
