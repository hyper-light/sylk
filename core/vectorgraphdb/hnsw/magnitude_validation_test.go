package hnsw

import (
	"math"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// W4P.24: Tests for magnitude caching validation

// TestMagnitudeValidation_HappyPath verifies that valid cached magnitudes
// are returned without recomputation when they match the vector.
func TestMagnitudeValidation_HappyPath(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a vector - magnitude should be computed and cached
	vec := []float32{3, 4, 0, 0}
	expectedMag := 5.0 // sqrt(9 + 16) = 5

	err := idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Retrieve vector and magnitude - should return cached value
	idx.mu.RLock()
	retrievedVec, retrievedMag, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if !ok {
		t.Fatal("getVectorAndMagnitude returned false for existing node")
	}

	if retrievedVec == nil {
		t.Fatal("Retrieved vector is nil")
	}

	if math.Abs(retrievedMag-expectedMag) > magnitudeTolerance {
		t.Errorf("Magnitude mismatch: got %v, expected %v", retrievedMag, expectedMag)
	}

	// Verify the cached magnitude matches what we expect
	idx.mu.RLock()
	cachedMag := idx.magnitudes["node1"]
	idx.mu.RUnlock()

	if math.Abs(cachedMag-expectedMag) > magnitudeTolerance {
		t.Errorf("Cached magnitude mismatch: got %v, expected %v", cachedMag, expectedMag)
	}
}

// TestMagnitudeValidation_MismatchDetection verifies that stale magnitudes
// are detected and the correct computed value is returned.
// Note: The cache is NOT updated during read operations to avoid data races.
func TestMagnitudeValidation_MismatchDetection(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a vector normally
	vec := []float32{3, 4, 0, 0}
	err := idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Manually corrupt the cached magnitude to simulate stale cache
	idx.mu.Lock()
	originalMag := idx.magnitudes["node1"]
	staleMag := originalMag * 2.0 // Intentionally wrong value
	idx.magnitudes["node1"] = staleMag
	idx.mu.Unlock()

	// Retrieve vector and magnitude - should detect mismatch and return computed value
	idx.mu.RLock()
	_, retrievedMag, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if !ok {
		t.Fatal("getVectorAndMagnitude returned false for existing node")
	}

	// The returned magnitude should be the correct computed value, not the stale one
	expectedMag := 5.0 // sqrt(9 + 16) = 5
	if math.Abs(retrievedMag-expectedMag) > magnitudeTolerance {
		t.Errorf("Returned magnitude should be corrected: got %v, expected %v", retrievedMag, expectedMag)
	}

	// Note: Cache is NOT updated during read to avoid data races
	// The stale value remains in cache but correct value is always returned
	idx.mu.RLock()
	cachedMag := idx.magnitudes["node1"]
	idx.mu.RUnlock()

	// Verify that the cache still has the stale value (we don't update during read)
	if math.Abs(cachedMag-staleMag) > magnitudeTolerance {
		t.Errorf("Cache should not be updated during read: got %v, expected stale %v", cachedMag, staleMag)
	}
}

// TestMagnitudeValidation_MissingMagnitude verifies that a missing magnitude
// is computed on demand when only the vector exists.
// Note: The cache is NOT updated during read operations to avoid data races.
func TestMagnitudeValidation_MissingMagnitude(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a vector normally
	vec := []float32{3, 4, 0, 0}
	err := idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Manually delete the cached magnitude to simulate missing cache entry
	idx.mu.Lock()
	delete(idx.magnitudes, "node1")
	idx.mu.Unlock()

	// Retrieve vector and magnitude - should compute magnitude on demand
	idx.mu.RLock()
	_, retrievedMag, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if !ok {
		t.Fatal("getVectorAndMagnitude returned false for existing node")
	}

	// The returned magnitude should be correctly computed
	expectedMag := 5.0 // sqrt(9 + 16) = 5
	if math.Abs(retrievedMag-expectedMag) > magnitudeTolerance {
		t.Errorf("Computed magnitude incorrect: got %v, expected %v", retrievedMag, expectedMag)
	}

	// Note: Cache is NOT populated during read to avoid data races
	// The magnitude is computed on demand each time if missing
	idx.mu.RLock()
	_, exists := idx.magnitudes["node1"]
	idx.mu.RUnlock()

	if exists {
		t.Error("Magnitude should NOT be cached during read operation")
	}
}

// TestMagnitudeValidation_UpdateFlow verifies that when a vector is updated,
// the magnitude is also correctly updated.
func TestMagnitudeValidation_UpdateFlow(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert initial vector
	vec1 := []float32{3, 4, 0, 0}
	err := idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Initial insert failed: %v", err)
	}

	// Verify initial magnitude
	idx.mu.RLock()
	_, mag1, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if !ok {
		t.Fatal("getVectorAndMagnitude returned false after initial insert")
	}

	expectedMag1 := 5.0 // sqrt(9 + 16) = 5
	if math.Abs(mag1-expectedMag1) > magnitudeTolerance {
		t.Errorf("Initial magnitude incorrect: got %v, expected %v", mag1, expectedMag1)
	}

	// Update the vector by inserting with the same ID
	vec2 := []float32{5, 12, 0, 0}
	err = idx.Insert("node1", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Update insert failed: %v", err)
	}

	// Verify updated magnitude
	idx.mu.RLock()
	_, mag2, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if !ok {
		t.Fatal("getVectorAndMagnitude returned false after update")
	}

	expectedMag2 := 13.0 // sqrt(25 + 144) = 13
	if math.Abs(mag2-expectedMag2) > magnitudeTolerance {
		t.Errorf("Updated magnitude incorrect: got %v, expected %v", mag2, expectedMag2)
	}
}

// TestMagnitudeValidation_ZeroVector verifies handling of zero vectors.
func TestMagnitudeValidation_ZeroVector(t *testing.T) {
	// Zero vectors are rejected at insert, so we test isMagnitudeValid directly
	// for zero magnitude cases
	if !isMagnitudeValid(0, 0) {
		t.Error("isMagnitudeValid should return true for two zeros")
	}

	if isMagnitudeValid(1.0, 0) {
		t.Error("isMagnitudeValid should return false when computed is 0 but cached is not")
	}

	if isMagnitudeValid(0, 1.0) {
		t.Error("isMagnitudeValid should return false when cached is 0 but computed is not")
	}
}

// TestMagnitudeValidation_Tolerance verifies the tolerance threshold works correctly.
func TestMagnitudeValidation_Tolerance(t *testing.T) {
	baseMag := 100.0

	// Within tolerance should be valid
	withinTolerance := baseMag + (baseMag * magnitudeTolerance * 0.5)
	if !isMagnitudeValid(withinTolerance, baseMag) {
		t.Errorf("Magnitude within tolerance should be valid: cached=%v, computed=%v",
			withinTolerance, baseMag)
	}

	// Beyond tolerance should be invalid
	beyondTolerance := baseMag + (baseMag * magnitudeTolerance * 2.0)
	if isMagnitudeValid(beyondTolerance, baseMag) {
		t.Errorf("Magnitude beyond tolerance should be invalid: cached=%v, computed=%v",
			beyondTolerance, baseMag)
	}

	// Exact match should be valid
	if !isMagnitudeValid(baseMag, baseMag) {
		t.Error("Exact match should be valid")
	}
}

// TestMagnitudeValidation_ConcurrentAccess verifies thread safety of magnitude validation.
func TestMagnitudeValidation_ConcurrentAccess(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert initial vectors
	for i := 0; i < 50; i++ {
		vec := make([]float32, 4)
		vec[0] = float32(i + 1)
		vec[1] = float32(i + 2)
		if err := idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Corrupt some magnitudes to trigger validation
	idx.mu.Lock()
	count := 0
	for id := range idx.magnitudes {
		if count%2 == 0 {
			idx.magnitudes[id] *= 1.5 // Corrupt every other magnitude
		}
		count++
	}
	idx.mu.Unlock()

	// Concurrent reads should all succeed and correct stale magnitudes
	var wg sync.WaitGroup
	errors := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()
			nodeID := randomNodeID(iteration % 50)

			idx.mu.RLock()
			vec, mag, ok := idx.getVectorAndMagnitude(nodeID)
			idx.mu.RUnlock()

			if !ok {
				errors <- nil // Node might not exist due to test setup
				return
			}

			// Verify magnitude is correct for the vector
			expectedMag := Magnitude(vec)
			if math.Abs(mag-expectedMag) > magnitudeTolerance {
				t.Errorf("Concurrent access returned incorrect magnitude for %s: got %v, expected %v",
					nodeID, mag, expectedMag)
			}
		}(i)
	}

	wg.Wait()
	close(errors)
}

// TestMagnitudeValidation_SearchWithStaleMagnitude verifies that search still
// returns correct results even when starting with stale magnitudes.
func TestMagnitudeValidation_SearchWithStaleMagnitude(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert test vectors
	idx.Insert("exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("similar", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("different", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Corrupt all magnitudes
	idx.mu.Lock()
	for id := range idx.magnitudes {
		idx.magnitudes[id] *= 2.0 // Intentionally wrong
	}
	idx.mu.Unlock()

	// Search should still work correctly because validation corrects magnitudes
	results := idx.Search([]float32{1, 0, 0, 0}, 3, nil)

	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}

	// First result should be exact match or similar
	if results[0].ID != "exact" && results[0].ID != "similar" {
		t.Errorf("Expected 'exact' or 'similar' as first result, got %s", results[0].ID)
	}

	// Results should be sorted by descending similarity
	for i := 1; i < len(results); i++ {
		if results[i].Similarity > results[i-1].Similarity {
			t.Errorf("Results not sorted: index %d has similarity %v > index %d with %v",
				i, results[i].Similarity, i-1, results[i-1].Similarity)
		}
	}
}

// TestMagnitudeValidation_InsertWithStaleMagnitude verifies that insert operations
// work correctly even when existing nodes have stale magnitudes.
func TestMagnitudeValidation_InsertWithStaleMagnitude(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert initial vector
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Corrupt the magnitude
	idx.mu.Lock()
	idx.magnitudes["node1"] *= 3.0
	idx.mu.Unlock()

	// Insert another vector - should still work correctly
	err := idx.Insert("node2", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Insert after corruption failed: %v", err)
	}

	// Verify both nodes are searchable
	results := idx.Search([]float32{1, 0, 0, 0}, 2, nil)
	if len(results) < 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
}

// TestIsMagnitudeValid_EdgeCases tests edge cases for the validation function.
func TestIsMagnitudeValid_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		cached   float64
		computed float64
		expected bool
	}{
		{
			name:     "both zero",
			cached:   0,
			computed: 0,
			expected: true,
		},
		{
			name:     "cached zero computed nonzero",
			cached:   0,
			computed: 1.0,
			expected: false,
		},
		{
			name:     "cached nonzero computed zero",
			cached:   1.0,
			computed: 0,
			expected: false,
		},
		{
			name:     "exact match small",
			cached:   1e-10,
			computed: 1e-10,
			expected: true,
		},
		{
			name:     "exact match large",
			cached:   1e10,
			computed: 1e10,
			expected: true,
		},
		{
			name:     "within tolerance positive",
			cached:   100.0,
			computed: 100.0 * (1 + magnitudeTolerance*0.5),
			expected: true,
		},
		{
			name:     "within tolerance negative",
			cached:   100.0,
			computed: 100.0 * (1 - magnitudeTolerance*0.5),
			expected: true,
		},
		{
			name:     "beyond tolerance",
			cached:   100.0,
			computed: 100.0 * (1 + magnitudeTolerance*2),
			expected: false,
		},
		{
			name:     "significantly different",
			cached:   1.0,
			computed: 2.0,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isMagnitudeValid(tt.cached, tt.computed)
			if result != tt.expected {
				t.Errorf("isMagnitudeValid(%v, %v) = %v, expected %v",
					tt.cached, tt.computed, result, tt.expected)
			}
		})
	}
}

// TestMagnitudeValidation_CorrectDistanceCalculations verifies that corrected
// magnitudes result in correct distance calculations.
func TestMagnitudeValidation_CorrectDistanceCalculations(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert vectors with known similarity
	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{1, 0, 0, 0} // Identical to vec1

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Corrupt magnitude for node1
	idx.mu.Lock()
	idx.magnitudes["node1"] = 999.0 // Very wrong
	idx.mu.Unlock()

	// Search for vec1 - node2 (identical vector) should be found
	results := idx.Search(vec1, 2, nil)

	if len(results) < 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// Both should have very high similarity (close to 1.0) after magnitude correction
	for _, r := range results {
		if r.Similarity < 0.99 {
			t.Errorf("Result %s should have high similarity after magnitude correction, got %v",
				r.ID, r.Similarity)
		}
	}
}
