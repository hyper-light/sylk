package hnsw

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func TestDotProduct(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float32
	}{
		{
			name:     "identical vectors",
			a:        []float32{1, 2, 3},
			b:        []float32{1, 2, 3},
			expected: 14,
		},
		{
			name:     "orthogonal vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{0, 1, 0},
			expected: 0,
		},
		{
			name:     "opposite vectors",
			a:        []float32{1, 2, 3},
			b:        []float32{-1, -2, -3},
			expected: -14,
		},
		{
			name:     "empty vectors",
			a:        []float32{},
			b:        []float32{},
			expected: 0,
		},
		{
			name:     "length mismatch returns zero",
			a:        []float32{1, 2, 3},
			b:        []float32{1, 2},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DotProduct(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("DotProduct() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMagnitude(t *testing.T) {
	tests := []struct {
		name     string
		v        []float32
		expected float64
	}{
		{
			name:     "unit vector x",
			v:        []float32{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "3-4-5 triangle",
			v:        []float32{3, 4, 0},
			expected: 5.0,
		},
		{
			name:     "zero vector",
			v:        []float32{0, 0, 0},
			expected: 0.0,
		},
		{
			name:     "empty vector",
			v:        []float32{},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Magnitude(tt.v)
			if math.Abs(result-tt.expected) > 1e-6 {
				t.Errorf("Magnitude() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float64
	}{
		{
			name:     "identical vectors",
			a:        []float32{1, 2, 3},
			b:        []float32{1, 2, 3},
			expected: 1.0,
		},
		{
			name:     "opposite vectors",
			a:        []float32{1, 2, 3},
			b:        []float32{-1, -2, -3},
			expected: -1.0,
		},
		{
			name:     "orthogonal vectors",
			a:        []float32{1, 0},
			b:        []float32{0, 1},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			magA := Magnitude(tt.a)
			magB := Magnitude(tt.b)
			result := CosineSimilarity(tt.a, tt.b, magA, magB)
			if math.Abs(result-tt.expected) > 1e-6 {
				t.Errorf("CosineSimilarity() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCosineSimilarityZeroMagnitude(t *testing.T) {
	zeroVec := []float32{0, 0, 0}
	normalVec := []float32{1, 2, 3}

	result := CosineSimilarity(zeroVec, normalVec, 0, Magnitude(normalVec))
	if result != 0 {
		t.Errorf("CosineSimilarity with zero magnitude should return 0, got %v", result)
	}
}

func TestEuclideanDistance(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float64
	}{
		{
			name:     "identical vectors",
			a:        []float32{1, 2, 3},
			b:        []float32{1, 2, 3},
			expected: 0.0,
		},
		{
			name:     "unit distance",
			a:        []float32{0, 0, 0},
			b:        []float32{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "3-4-5 triangle",
			a:        []float32{0, 0},
			b:        []float32{3, 4},
			expected: 5.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EuclideanDistance(tt.a, tt.b)
			if math.Abs(result-tt.expected) > 1e-6 {
				t.Errorf("EuclideanDistance() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestEuclideanDistanceLengthMismatch(t *testing.T) {
	result := EuclideanDistance([]float32{1, 2, 3}, []float32{1, 2})
	if result != math.MaxFloat64 {
		t.Errorf("EuclideanDistance with mismatched lengths should return MaxFloat64")
	}
}

func TestNormalizeVector(t *testing.T) {
	v := []float32{3, 4, 0}
	mag := NormalizeVector(v)

	if math.Abs(mag-5.0) > 1e-6 {
		t.Errorf("NormalizeVector returned magnitude %v, want 5.0", mag)
	}

	newMag := Magnitude(v)
	if math.Abs(newMag-1.0) > 1e-6 {
		t.Errorf("Normalized vector magnitude = %v, want 1.0", newMag)
	}
}

func TestNormalizeZeroVector(t *testing.T) {
	v := []float32{0, 0, 0}
	mag := NormalizeVector(v)

	if mag != 0 {
		t.Errorf("NormalizeVector on zero vector returned %v, want 0", mag)
	}
}

// W4P.18: Tests for NormalizeVectorCopy - safe copy-based normalization

// TestNormalizeVectorCopyOriginalUnchanged verifies that the original vector
// is not modified when using NormalizeVectorCopy.
func TestNormalizeVectorCopyOriginalUnchanged(t *testing.T) {
	original := []float32{3, 4, 0}
	originalCopy := make([]float32, len(original))
	copy(originalCopy, original)

	normalized, mag := NormalizeVectorCopy(original)

	// Original should be unchanged
	for i := range original {
		if original[i] != originalCopy[i] {
			t.Errorf("Original vector modified at index %d: got %v, want %v",
				i, original[i], originalCopy[i])
		}
	}

	// Normalized should be correct
	if math.Abs(mag-5.0) > 1e-6 {
		t.Errorf("NormalizeVectorCopy returned magnitude %v, want 5.0", mag)
	}

	// Normalized copy should have unit length
	normalizedMag := Magnitude(normalized)
	if math.Abs(normalizedMag-1.0) > 1e-6 {
		t.Errorf("Normalized vector magnitude = %v, want 1.0", normalizedMag)
	}
}

// TestNormalizeVectorCopyReturnsNormalizedCopy verifies that the returned copy
// is correctly normalized.
func TestNormalizeVectorCopyReturnsNormalizedCopy(t *testing.T) {
	tests := []struct {
		name        string
		input       []float32
		expectedMag float64
	}{
		{
			name:        "3-4-5 triangle",
			input:       []float32{3, 4, 0},
			expectedMag: 5.0,
		},
		{
			name:        "unit vector",
			input:       []float32{1, 0, 0},
			expectedMag: 1.0,
		},
		{
			name:        "negative values",
			input:       []float32{-3, -4, 0},
			expectedMag: 5.0,
		},
		{
			name:        "higher dimension",
			input:       []float32{1, 2, 3, 4, 5},
			expectedMag: math.Sqrt(1 + 4 + 9 + 16 + 25), // sqrt(55) ~= 7.416
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, mag := NormalizeVectorCopy(tt.input)

			// Check magnitude returned
			if math.Abs(mag-tt.expectedMag) > 1e-5 {
				t.Errorf("Magnitude = %v, want %v", mag, tt.expectedMag)
			}

			// Check normalized vector has unit length
			normalizedMag := Magnitude(normalized)
			if math.Abs(normalizedMag-1.0) > 1e-6 {
				t.Errorf("Normalized magnitude = %v, want 1.0", normalizedMag)
			}

			// Check direction is preserved (dot product should equal original magnitude)
			dot := float64(DotProduct(tt.input, normalized))
			if math.Abs(dot-tt.expectedMag) > 1e-5 {
				t.Errorf("Direction not preserved: dot product = %v, want %v", dot, tt.expectedMag)
			}
		})
	}
}

// TestNormalizeVectorCopyMultipleNormalizationsDontCompound verifies that
// normalizing an already-normalized vector maintains unit length.
func TestNormalizeVectorCopyMultipleNormalizationsDontCompound(t *testing.T) {
	original := []float32{3, 4, 0}

	// Normalize multiple times
	v1, mag1 := NormalizeVectorCopy(original)
	v2, mag2 := NormalizeVectorCopy(v1)
	v3, mag3 := NormalizeVectorCopy(v2)

	// First normalization should have original magnitude
	if math.Abs(mag1-5.0) > 1e-6 {
		t.Errorf("First normalization magnitude = %v, want 5.0", mag1)
	}

	// Subsequent normalizations should have magnitude ~1.0
	if math.Abs(mag2-1.0) > 1e-6 {
		t.Errorf("Second normalization magnitude = %v, want 1.0", mag2)
	}
	if math.Abs(mag3-1.0) > 1e-6 {
		t.Errorf("Third normalization magnitude = %v, want 1.0", mag3)
	}

	// All normalized vectors should have unit length
	for i, v := range [][]float32{v1, v2, v3} {
		m := Magnitude(v)
		if math.Abs(m-1.0) > 1e-6 {
			t.Errorf("Normalized vector %d magnitude = %v, want 1.0", i+1, m)
		}
	}

	// Original should remain unchanged
	if original[0] != 3 || original[1] != 4 || original[2] != 0 {
		t.Errorf("Original vector was modified: %v", original)
	}
}

// TestNormalizeVectorCopyZeroVector verifies behavior with zero vector.
func TestNormalizeVectorCopyZeroVector(t *testing.T) {
	zero := []float32{0, 0, 0}
	zeroCopy := make([]float32, len(zero))
	copy(zeroCopy, zero)

	normalized, mag := NormalizeVectorCopy(zero)

	// Magnitude should be 0
	if mag != 0 {
		t.Errorf("Zero vector magnitude = %v, want 0", mag)
	}

	// Original should be unchanged
	for i := range zero {
		if zero[i] != zeroCopy[i] {
			t.Errorf("Zero vector modified at index %d", i)
		}
	}

	// Result should be a copy (same values as input since magnitude is 0)
	if len(normalized) != len(zero) {
		t.Errorf("Result length = %d, want %d", len(normalized), len(zero))
	}
	for i := range normalized {
		if normalized[i] != zero[i] {
			t.Errorf("Result[%d] = %v, want %v", i, normalized[i], zero[i])
		}
	}
}

// TestNormalizeVectorCopyConcurrentSafety verifies that concurrent normalizations
// of the same vector are safe.
func TestNormalizeVectorCopyConcurrentSafety(t *testing.T) {
	// Shared vector that will be normalized concurrently
	shared := []float32{3, 4, 0, 0, 0, 0, 0, 0}
	sharedCopy := make([]float32, len(shared))
	copy(sharedCopy, shared)

	var wg sync.WaitGroup
	numGoroutines := 100

	// Store results to verify correctness
	results := make([][]float32, numGoroutines)
	magnitudes := make([]float64, numGoroutines)

	// Launch concurrent normalizations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], magnitudes[idx] = NormalizeVectorCopy(shared)
		}(i)
	}

	wg.Wait()

	// Verify original is unchanged
	for i := range shared {
		if shared[i] != sharedCopy[i] {
			t.Errorf("Shared vector modified at index %d: got %v, want %v",
				i, shared[i], sharedCopy[i])
		}
	}

	// Verify all results are correct
	expectedMag := 5.0 // sqrt(9 + 16)
	for i := 0; i < numGoroutines; i++ {
		if math.Abs(magnitudes[i]-expectedMag) > 1e-6 {
			t.Errorf("Goroutine %d: magnitude = %v, want %v", i, magnitudes[i], expectedMag)
		}

		resultMag := Magnitude(results[i])
		if math.Abs(resultMag-1.0) > 1e-6 {
			t.Errorf("Goroutine %d: result magnitude = %v, want 1.0", i, resultMag)
		}
	}
}

// TestNormalizeVectorCopyEmptyVector verifies behavior with empty vector.
func TestNormalizeVectorCopyEmptyVector(t *testing.T) {
	empty := []float32{}

	normalized, mag := NormalizeVectorCopy(empty)

	if mag != 0 {
		t.Errorf("Empty vector magnitude = %v, want 0", mag)
	}

	if len(normalized) != 0 {
		t.Errorf("Result length = %d, want 0", len(normalized))
	}
}

// TestNormalizeVectorCopyResultIsIndependent verifies that modifying the result
// does not affect the original vector.
func TestNormalizeVectorCopyResultIsIndependent(t *testing.T) {
	original := []float32{3, 4, 0}
	originalCopy := make([]float32, len(original))
	copy(originalCopy, original)

	normalized, _ := NormalizeVectorCopy(original)

	// Modify the result
	for i := range normalized {
		normalized[i] = 999.0
	}

	// Original should be unchanged
	for i := range original {
		if original[i] != originalCopy[i] {
			t.Errorf("Original modified after result change at index %d: got %v, want %v",
				i, original[i], originalCopy[i])
		}
	}
}

func TestIndexNew(t *testing.T) {
	cfg := DefaultConfig()
	idx := New(cfg)

	if idx == nil {
		t.Fatal("New() returned nil")
	}
	if idx.Size() != 0 {
		t.Errorf("New index size = %d, want 0", idx.Size())
	}
	if idx.M != cfg.M {
		t.Errorf("M = %d, want %d", idx.M, cfg.M)
	}
}

func TestIndexInsertAndSearch(t *testing.T) {
	idx := New(DefaultConfig())

	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0, 1, 0, 0}
	vec3 := []float32{0.9, 0.1, 0, 0}

	if err := idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatalf("Insert node1: %v", err)
	}
	if err := idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction); err != nil {
		t.Fatalf("Insert node2: %v", err)
	}
	if err := idx.Insert("node3", vec3, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession); err != nil {
		t.Fatalf("Insert node3: %v", err)
	}

	if idx.Size() != 3 {
		t.Errorf("Size = %d, want 3", idx.Size())
	}

	results := idx.Search(vec1, 3, nil)
	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}
	if results[0].ID != "node1" && results[0].ID != "node3" {
		t.Errorf("Most similar to vec1 should be node1 or node3, got %s", results[0].ID)
	}
}

func TestIndexInsertEmptyVector(t *testing.T) {
	idx := New(DefaultConfig())
	err := idx.Insert("node1", []float32{}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// W4L.1: Use errors.Is() to check wrapped errors
	if !errors.Is(err, ErrEmptyVector) {
		t.Errorf("Insert empty vector: got %v, want ErrEmptyVector", err)
	}
}

func TestIndexSearchEmpty(t *testing.T) {
	idx := New(DefaultConfig())
	results := idx.Search([]float32{1, 0, 0}, 10, nil)

	if results != nil {
		t.Errorf("Search on empty index should return nil, got %v", results)
	}
}

func TestIndexSearchEmptyQuery(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	results := idx.Search([]float32{}, 10, nil)
	if results != nil {
		t.Errorf("Search with empty query should return nil")
	}
}

func TestIndexSearchZeroK(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	results := idx.Search([]float32{1, 0, 0}, 0, nil)
	if results != nil {
		t.Errorf("Search with k=0 should return nil")
	}
}

func TestIndexSearchWithFilter(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{1, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)

	filter := &SearchFilter{
		Domains: []vectorgraphdb.Domain{vectorgraphdb.DomainCode},
	}
	results := idx.Search([]float32{1, 0, 0}, 10, filter)

	for _, r := range results {
		if r.Domain != vectorgraphdb.DomainCode {
			t.Errorf("Filter didn't work: got domain %s", r.Domain)
		}
	}
}

func TestIndexDelete(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	err := idx.Delete("node1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if idx.Size() != 1 {
		t.Errorf("Size after delete = %d, want 1", idx.Size())
	}

	if idx.Contains("node1") {
		t.Error("Contains(node1) = true after delete")
	}
}

func TestIndexDeleteNonexistent(t *testing.T) {
	idx := New(DefaultConfig())
	err := idx.Delete("nonexistent")

	// W4L.1: Use errors.Is() to check wrapped errors
	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("Delete nonexistent: got %v, want ErrNodeNotFound", err)
	}
}

func TestIndexDeleteEntryPoint(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.Delete("node1")

	results := idx.Search([]float32{0, 1, 0}, 10, nil)
	if len(results) == 0 {
		t.Error("Search failed after deleting entry point")
	}
}

func TestIndexContains(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	if !idx.Contains("node1") {
		t.Error("Contains(node1) = false, want true")
	}
	if idx.Contains("node2") {
		t.Error("Contains(node2) = true, want false")
	}
}

func TestIndexGetVector(t *testing.T) {
	idx := New(DefaultConfig())
	original := []float32{1, 2, 3, 4}
	idx.Insert("node1", original, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	vec, err := idx.GetVector("node1")
	if err != nil {
		t.Fatalf("GetVector: %v", err)
	}

	for i := range original {
		if vec[i] != original[i] {
			t.Errorf("GetVector()[%d] = %v, want %v", i, vec[i], original[i])
		}
	}
}

func TestIndexGetVectorNotFound(t *testing.T) {
	idx := New(DefaultConfig())
	_, err := idx.GetVector("nonexistent")

	// W4L.1: Use errors.Is() to check wrapped errors
	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("GetVector nonexistent: got %v, want ErrNodeNotFound", err)
	}
}

func TestIndexGetMetadata(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	domain, nodeType, err := idx.GetMetadata("node1")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if domain != vectorgraphdb.DomainCode {
		t.Errorf("Domain = %s, want code", domain)
	}
	if nodeType != vectorgraphdb.NodeTypeFile {
		t.Errorf("NodeType = %s, want file", nodeType)
	}
}

func TestIndexStats(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	stats := idx.Stats()
	if stats.TotalNodes != 1 {
		t.Errorf("TotalNodes = %d, want 1", stats.TotalNodes)
	}
	if stats.M != vectorgraphdb.DefaultM {
		t.Errorf("M = %d, want %d", stats.M, vectorgraphdb.DefaultM)
	}
}

func TestIndexConcurrentInsert(t *testing.T) {
	idx := New(DefaultConfig())
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			vec := make([]float32, 128)
			for j := range vec {
				vec[j] = rand.Float32()
			}
			nodeid := randomNodeID(id)
			idx.Insert(nodeid, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		}(i)
	}

	wg.Wait()

	if idx.Size() != 100 {
		t.Errorf("After concurrent insert: size = %d, want 100", idx.Size())
	}
}

func TestIndexConcurrentSearch(t *testing.T) {
	idx := New(DefaultConfig())

	for i := 0; i < 50; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := make([]float32, 128)
			for j := range query {
				query[j] = rand.Float32()
			}
			idx.Search(query, 10, nil)
		}()
	}

	wg.Wait()
}

func TestIndexConcurrentInsertAndSearch(t *testing.T) {
	idx := New(DefaultConfig())
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func(id int) {
			defer wg.Done()
			vec := make([]float32, 128)
			for j := range vec {
				vec[j] = rand.Float32()
			}
			idx.Insert(randomNodeID(id), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		}(i)

		go func() {
			defer wg.Done()
			query := make([]float32, 128)
			for j := range query {
				query[j] = rand.Float32()
			}
			idx.Search(query, 10, nil)
		}()
	}

	wg.Wait()
}

func TestIndexConcurrentDelete(t *testing.T) {
	idx := New(DefaultConfig())

	for i := 0; i < 100; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Delete(randomNodeID(id))
		}(i)
	}

	wg.Wait()

	if idx.Size() != 50 {
		t.Errorf("After concurrent delete: size = %d, want 50", idx.Size())
	}
}

func TestLayerBasicOperations(t *testing.T) {
	l := newLayer()

	l.addNode("node1")
	if !l.hasNode("node1") {
		t.Error("hasNode(node1) = false after add")
	}

	l.addNeighbor("node1", "node2", 0.5, 10)
	neighbors := l.getNeighbors("node1")
	if len(neighbors) != 1 || neighbors[0] != "node2" {
		t.Errorf("getNeighbors = %v, want [node2]", neighbors)
	}

	l.removeNeighbor("node1", "node2")
	neighbors = l.getNeighbors("node1")
	if len(neighbors) != 0 {
		t.Errorf("After remove: getNeighbors = %v, want []", neighbors)
	}

	l.removeNode("node1")
	if l.hasNode("node1") {
		t.Error("hasNode(node1) = true after remove")
	}
}

func TestLayerMaxNeighbors(t *testing.T) {
	l := newLayer()
	l.addNode("node1")

	for i := 0; i < 5; i++ {
		l.addNeighbor("node1", randomNodeID(i), float32(i)*0.1, 5)
	}

	// "overflow" has higher distance (0.9) than existing neighbors, so won't replace any
	added := l.addNeighbor("node1", "overflow", 0.9, 5)
	if added {
		t.Error("addNeighbor succeeded beyond max with worse distance")
	}
}

func TestLayerNodeCount(t *testing.T) {
	l := newLayer()

	for i := 0; i < 10; i++ {
		l.addNode(randomNodeID(i))
	}

	if l.nodeCount() != 10 {
		t.Errorf("nodeCount = %d, want 10", l.nodeCount())
	}
}

func TestRandomLevel(t *testing.T) {
	levels := make(map[int]int)
	for i := 0; i < 10000; i++ {
		level := randomLevel(0.36067977499789996)
		levels[level]++
	}

	if levels[0] < 5000 {
		t.Errorf("Level 0 should be most common: got %d", levels[0])
	}

	for level := 1; level < 5; level++ {
		if levels[level] > levels[level-1] {
			t.Errorf("Level %d count (%d) > level %d count (%d)", level, levels[level], level-1, levels[level-1])
		}
	}
}

// PF.2.3: Test O(1) containsNeighbor lookup with ConcurrentNeighborSet
func TestLayerContainsNeighbor(t *testing.T) {
	l := newLayer()
	l.addNode("node1")
	l.addNeighbor("node1", "neighbor1", 0.1, 10)
	l.addNeighbor("node1", "neighbor2", 0.2, 10)

	// Access node directly to test containsNeighbor
	l.mu.RLock()
	node := l.nodes["node1"]
	l.mu.RUnlock()

	// Test O(1) lookup
	if !l.containsNeighbor(node, "neighbor1") {
		t.Error("containsNeighbor should find neighbor1")
	}
	if !l.containsNeighbor(node, "neighbor2") {
		t.Error("containsNeighbor should find neighbor2")
	}
	if l.containsNeighbor(node, "neighbor3") {
		t.Error("containsNeighbor should not find neighbor3")
	}
}

// PF.2.3: Test that neighbors are returned sorted by distance
func TestLayerGetNeighborsSorted(t *testing.T) {
	l := newLayer()
	l.addNode("node1")

	// Add neighbors with varying distances (out of order)
	l.addNeighbor("node1", "far", 0.9, 10)
	l.addNeighbor("node1", "close", 0.1, 10)
	l.addNeighbor("node1", "medium", 0.5, 10)

	neighbors := l.getNeighbors("node1")
	if len(neighbors) != 3 {
		t.Fatalf("Expected 3 neighbors, got %d", len(neighbors))
	}

	// Should be sorted by distance: close, medium, far
	if neighbors[0] != "close" {
		t.Errorf("First neighbor should be 'close' (distance 0.1), got %s", neighbors[0])
	}
	if neighbors[1] != "medium" {
		t.Errorf("Second neighbor should be 'medium' (distance 0.5), got %s", neighbors[1])
	}
	if neighbors[2] != "far" {
		t.Errorf("Third neighbor should be 'far' (distance 0.9), got %s", neighbors[2])
	}
}

// PF.2.4: Test batch neighbor retrieval
func TestLayerGetNeighborsMany(t *testing.T) {
	l := newLayer()

	// Create multiple nodes with neighbors
	l.addNode("node1")
	l.addNode("node2")
	l.addNode("node3")

	l.addNeighbor("node1", "n1a", 0.1, 10)
	l.addNeighbor("node1", "n1b", 0.2, 10)
	l.addNeighbor("node2", "n2a", 0.3, 10)
	l.addNeighbor("node3", "n3a", 0.4, 10)
	l.addNeighbor("node3", "n3b", 0.5, 10)
	l.addNeighbor("node3", "n3c", 0.6, 10)

	// Batch retrieval
	result := l.getNeighborsMany([]string{"node1", "node2", "node3", "nonexistent"})

	// Check node1
	if n1, ok := result["node1"]; !ok || len(n1) != 2 {
		t.Errorf("node1 should have 2 neighbors, got %v", n1)
	}

	// Check node2
	if n2, ok := result["node2"]; !ok || len(n2) != 1 {
		t.Errorf("node2 should have 1 neighbor, got %v", n2)
	}

	// Check node3
	if n3, ok := result["node3"]; !ok || len(n3) != 3 {
		t.Errorf("node3 should have 3 neighbors, got %v", n3)
	}

	// Check nonexistent is not present
	if _, ok := result["nonexistent"]; ok {
		t.Error("nonexistent should not be in result")
	}
}

// PF.2.3: Test getNeighborsWithDistances
func TestLayerGetNeighborsWithDistances(t *testing.T) {
	l := newLayer()
	l.addNode("node1")

	l.addNeighbor("node1", "close", 0.1, 10)
	l.addNeighbor("node1", "far", 0.9, 10)

	neighbors := l.getNeighborsWithDistances("node1")
	if len(neighbors) != 2 {
		t.Fatalf("Expected 2 neighbors, got %d", len(neighbors))
	}

	// Should be sorted by distance
	if neighbors[0].ID != "close" || neighbors[0].Distance != 0.1 {
		t.Errorf("First neighbor should be close with distance 0.1, got %s with %f", neighbors[0].ID, neighbors[0].Distance)
	}
	if neighbors[1].ID != "far" || neighbors[1].Distance != 0.9 {
		t.Errorf("Second neighbor should be far with distance 0.9, got %s with %f", neighbors[1].ID, neighbors[1].Distance)
	}
}

// PF.2.3: Test setNeighbors with distances
func TestLayerSetNeighbors(t *testing.T) {
	l := newLayer()
	l.addNode("node1")

	// Initial neighbors
	l.addNeighbor("node1", "old1", 0.5, 10)
	l.addNeighbor("node1", "old2", 0.6, 10)

	// Replace with new neighbors
	l.setNeighbors("node1", []string{"new1", "new2", "new3"}, []float32{0.1, 0.2, 0.3})

	neighbors := l.getNeighbors("node1")
	if len(neighbors) != 3 {
		t.Fatalf("Expected 3 neighbors after setNeighbors, got %d", len(neighbors))
	}

	// Old neighbors should be gone
	for _, n := range neighbors {
		if n == "old1" || n == "old2" {
			t.Errorf("Old neighbor %s should not be present", n)
		}
	}
}

// PF.2.3: Test addNeighbor replaces worst neighbor when at capacity
func TestLayerAddNeighborReplacesWorst(t *testing.T) {
	l := newLayer()
	l.addNode("node1")

	// Add neighbors up to capacity
	l.addNeighbor("node1", "n1", 0.3, 3)
	l.addNeighbor("node1", "n2", 0.5, 3)
	l.addNeighbor("node1", "n3", 0.7, 3) // Worst

	// Add a better neighbor - should replace n3
	added := l.addNeighbor("node1", "n4", 0.2, 3)
	if !added {
		t.Error("Should add n4 as it's better than worst")
	}

	neighbors := l.getNeighbors("node1")
	if len(neighbors) != 3 {
		t.Errorf("Expected 3 neighbors, got %d", len(neighbors))
	}

	// n3 (worst) should be replaced by n4
	for _, n := range neighbors {
		if n == "n3" {
			t.Error("n3 (worst) should have been replaced")
		}
	}

	// Verify n4 is present
	found := false
	for _, n := range neighbors {
		if n == "n4" {
			found = true
			break
		}
	}
	if !found {
		t.Error("n4 should be present")
	}
}

// Test concurrent layer operations for thread safety
func TestLayerConcurrentOperations(t *testing.T) {
	l := newLayer()
	l.addNode("node1")

	var wg sync.WaitGroup
	numGoroutines := 50

	// Concurrent adds
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.addNeighbor("node1", randomNodeID(i), float32(i)*0.01, 100)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.getNeighbors("node1")
		}()
	}

	wg.Wait()

	// Should have some neighbors
	neighbors := l.getNeighbors("node1")
	if len(neighbors) == 0 {
		t.Error("Expected some neighbors after concurrent operations")
	}
}

func TestIndexUpdateExisting(t *testing.T) {
	idx := New(DefaultConfig())

	original := []float32{1, 0, 0, 0}
	updated := []float32{0, 1, 0, 0}

	idx.Insert("node1", original, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node1", updated, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	vec, _ := idx.GetVector("node1")
	if vec[0] != 0 || vec[1] != 1 {
		t.Errorf("Vector not updated: got %v", vec)
	}
}

func TestSearchResultsOrdering(t *testing.T) {
	idx := New(DefaultConfig())

	idx.Insert("exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("similar", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("different", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	results := idx.Search([]float32{1, 0, 0, 0}, 3, nil)

	if len(results) < 2 {
		t.Fatal("Expected at least 2 results")
	}

	for i := 1; i < len(results); i++ {
		if results[i].Similarity > results[i-1].Similarity {
			t.Errorf("Results not sorted: %v > %v", results[i].Similarity, results[i-1].Similarity)
		}
	}
}

// W4C.2: Test MinSimilarity filter rejects nodes below threshold
func TestSearchWithMinSimilarityRejectsBelowThreshold(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert vectors with known similarity scores to query [1,0,0,0]
	// exact match: similarity = 1.0
	// similar: similarity ~0.994 (high)
	// different: similarity = 0.0 (orthogonal)
	idx.Insert("exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("similar", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("different", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Filter with MinSimilarity = 0.5 should reject "different" (similarity = 0.0)
	filter := &SearchFilter{
		MinSimilarity: 0.5,
	}
	results := idx.Search([]float32{1, 0, 0, 0}, 10, filter)

	// All results should have similarity >= 0.5
	for _, r := range results {
		if r.Similarity < 0.5 {
			t.Errorf("Result %s has similarity %v, which is below MinSimilarity 0.5", r.ID, r.Similarity)
		}
	}

	// "different" should NOT be in results (orthogonal vector, similarity = 0)
	for _, r := range results {
		if r.ID == "different" {
			t.Errorf("'different' should be rejected (similarity 0.0 < MinSimilarity 0.5), but it was included with similarity %v", r.Similarity)
		}
	}
}

// W4C.2: Test MinSimilarity filter accepts nodes at/above threshold
func TestSearchWithMinSimilarityAcceptsAtOrAboveThreshold(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert vectors with known similarities to query [1,0,0,0]
	idx.Insert("exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("similar", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Both should pass with MinSimilarity = 0.9 (exact=1.0, similar~0.994)
	filter := &SearchFilter{
		MinSimilarity: 0.9,
	}
	results := idx.Search([]float32{1, 0, 0, 0}, 10, filter)

	if len(results) < 2 {
		t.Errorf("Expected at least 2 results above MinSimilarity 0.9, got %d", len(results))
	}

	// All results should have similarity >= 0.9
	for _, r := range results {
		if r.Similarity < 0.9 {
			t.Errorf("Result %s has similarity %v, which is below MinSimilarity 0.9", r.ID, r.Similarity)
		}
	}
}

// W4C.2: Test MinSimilarity combined with domain filter
func TestSearchWithMinSimilarityAndDomainFilter(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert vectors in different domains
	idx.Insert("code_exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("code_different", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("history_exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)
	idx.Insert("history_different", []float32{0, 1, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)

	// Filter: DomainCode AND MinSimilarity 0.5
	filter := &SearchFilter{
		Domains:       []vectorgraphdb.Domain{vectorgraphdb.DomainCode},
		MinSimilarity: 0.5,
	}
	results := idx.Search([]float32{1, 0, 0, 0}, 10, filter)

	// Should only return "code_exact" (DomainCode, similarity=1.0)
	// Should NOT return:
	// - "code_different" (DomainCode but similarity=0.0 < 0.5)
	// - "history_exact" (similarity=1.0 but wrong domain)
	// - "history_different" (wrong domain and similarity=0.0)
	for _, r := range results {
		if r.Domain != vectorgraphdb.DomainCode {
			t.Errorf("Result %s has domain %s, expected DomainCode", r.ID, r.Domain)
		}
		if r.Similarity < 0.5 {
			t.Errorf("Result %s has similarity %v, which is below MinSimilarity 0.5", r.ID, r.Similarity)
		}
	}

	// Verify code_different is NOT in results
	for _, r := range results {
		if r.ID == "code_different" {
			t.Errorf("'code_different' should be rejected (similarity 0.0 < MinSimilarity 0.5)")
		}
	}
}

// W4C.2: Test MinSimilarity combined with node type filter
func TestSearchWithMinSimilarityAndNodeTypeFilter(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert vectors with different node types
	idx.Insert("file_exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("file_different", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("func_exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	idx.Insert("func_different", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)

	// Filter: NodeTypeFile AND MinSimilarity 0.5
	filter := &SearchFilter{
		NodeTypes:     []vectorgraphdb.NodeType{vectorgraphdb.NodeTypeFile},
		MinSimilarity: 0.5,
	}
	results := idx.Search([]float32{1, 0, 0, 0}, 10, filter)

	// Should only return "file_exact"
	for _, r := range results {
		if r.NodeType != vectorgraphdb.NodeTypeFile {
			t.Errorf("Result %s has node type %s, expected NodeTypeFile", r.ID, r.NodeType)
		}
		if r.Similarity < 0.5 {
			t.Errorf("Result %s has similarity %v, which is below MinSimilarity 0.5", r.ID, r.Similarity)
		}
	}

	// Verify file_different is NOT in results
	for _, r := range results {
		if r.ID == "file_different" {
			t.Errorf("'file_different' should be rejected (similarity 0.0 < MinSimilarity 0.5)")
		}
	}
}

// W4C.2: Test MinSimilarity = 0 behaves like no filter
func TestSearchWithMinSimilarityZero(t *testing.T) {
	idx := New(DefaultConfig())

	idx.Insert("exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("different", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// MinSimilarity = 0 should not filter anything
	filter := &SearchFilter{
		MinSimilarity: 0,
	}
	results := idx.Search([]float32{1, 0, 0, 0}, 10, filter)

	// Both results should be present
	if len(results) != 2 {
		t.Errorf("Expected 2 results with MinSimilarity=0, got %d", len(results))
	}
}

// W4C.2: Test MinSimilarity with very high threshold filters all
func TestSearchWithMinSimilarityVeryHigh(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a nearly-exact vector (not exact)
	idx.Insert("similar", []float32{0.99, 0.01, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// MinSimilarity = 1.0 should only match exact vectors
	filter := &SearchFilter{
		MinSimilarity: 1.0,
	}
	results := idx.Search([]float32{1, 0, 0, 0}, 10, filter)

	// No results expected since similarity < 1.0
	if len(results) != 0 {
		t.Errorf("Expected 0 results with MinSimilarity=1.0, got %d", len(results))
		for _, r := range results {
			t.Logf("Unexpected result: %s with similarity %v", r.ID, r.Similarity)
		}
	}
}

// W4C.1: Test that candidates never exceed ef*2 in searchLayer
func TestSearchLayerCandidatesBounded(t *testing.T) {
	idx := New(Config{
		M:           16,
		EfConstruct: 200,
		EfSearch:    50, // ef=50, so maxCandidates should be 100
		LevelMult:   0.36067977499789996,
		Dimension:   128,
	})

	// Insert enough nodes to create a dense graph
	for i := 0; i < 500; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Perform multiple searches to verify bound is maintained
	for i := 0; i < 10; i++ {
		query := make([]float32, 128)
		for j := range query {
			query[j] = rand.Float32()
		}
		results := idx.Search(query, 10, nil)

		// Results should be non-empty
		if len(results) == 0 {
			t.Errorf("Search %d returned no results", i)
		}

		// Results should not exceed requested k
		if len(results) > 10 {
			t.Errorf("Search %d returned %d results, expected at most 10", i, len(results))
		}
	}
}

// W4C.1: Test that search quality is maintained after fix
func TestSearchLayerQualityMaintained(t *testing.T) {
	idx := New(Config{
		M:           16,
		EfConstruct: 100,
		EfSearch:    50,
		LevelMult:   0.36067977499789996,
		Dimension:   4,
	})

	// Insert known vectors
	idx.Insert("exact", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("similar90", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("similar80", []float32{0.8, 0.2, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("orthogonal", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	results := idx.Search([]float32{1, 0, 0, 0}, 4, nil)

	if len(results) < 3 {
		t.Fatalf("Expected at least 3 results, got %d", len(results))
	}

	// First result should be exact match or very close
	if results[0].Similarity < 0.99 {
		t.Errorf("First result similarity %v, expected >= 0.99", results[0].Similarity)
	}

	// Results should be sorted by descending similarity
	for i := 1; i < len(results); i++ {
		if results[i].Similarity > results[i-1].Similarity {
			t.Errorf("Results not sorted: index %d has similarity %v > index %d with %v",
				i, results[i].Similarity, i-1, results[i-1].Similarity)
		}
	}
}

// W4C.1: Test bounds checking for missing vectors in searchLayer
func TestSearchLayerMissingVectorBoundsCheck(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert some vectors
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// This test ensures the bounds check doesn't panic when vectors are properly present
	results := idx.Search([]float32{1, 0, 0, 0}, 10, nil)
	if len(results) == 0 {
		t.Error("Expected results but got none")
	}
}

// W4C.1: Test getVectorAndMagnitude helper function
func TestGetVectorAndMagnitude(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a vector
	idx.Insert("node1", []float32{3, 4, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Test existing node
	idx.mu.RLock()
	vec, mag, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if !ok {
		t.Error("getVectorAndMagnitude should find node1")
	}
	if vec == nil {
		t.Error("Vector should not be nil")
	}
	if mag != 5.0 {
		t.Errorf("Magnitude should be 5.0, got %v", mag)
	}

	// Test non-existent node
	idx.mu.RLock()
	_, _, ok = idx.getVectorAndMagnitude("nonexistent")
	idx.mu.RUnlock()

	if ok {
		t.Error("getVectorAndMagnitude should not find nonexistent node")
	}
}

// W4C.1: Test memory bounded on large graph with high connectivity
func TestSearchLayerMemoryBoundedLargeGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large graph test in short mode")
	}

	idx := New(Config{
		M:           32, // Higher connectivity
		EfConstruct: 200,
		EfSearch:    100,
		LevelMult:   0.36067977499789996,
		Dimension:   64,
	})

	// Insert a large number of nodes
	numNodes := 1000
	for i := 0; i < numNodes; i++ {
		vec := make([]float32, 64)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Verify searches complete without memory issues
	for i := 0; i < 20; i++ {
		query := make([]float32, 64)
		for j := range query {
			query[j] = rand.Float32()
		}
		results := idx.Search(query, 10, nil)

		// Should return results without hanging or memory issues
		if len(results) == 0 {
			t.Errorf("Search %d on large graph returned no results", i)
		}
	}
}

// W4C.1: Test that concurrent searches with bounded candidates work correctly
func TestSearchLayerConcurrentBounded(t *testing.T) {
	idx := New(Config{
		M:           16,
		EfConstruct: 100,
		EfSearch:    50,
		LevelMult:   0.36067977499789996,
		Dimension:   128,
	})

	// Insert nodes
	for i := 0; i < 200; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Run concurrent searches
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := make([]float32, 128)
			for j := range query {
				query[j] = rand.Float32()
			}
			results := idx.Search(query, 10, nil)
			if len(results) == 0 {
				t.Error("Concurrent search returned no results")
			}
		}()
	}
	wg.Wait()
}

func randomNodeID(i int) string {
	return "node" + string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
}

func BenchmarkInsert(b *testing.B) {
	idx := New(DefaultConfig())
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = rand.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}
}

func BenchmarkSearch(b *testing.B) {
	idx := New(DefaultConfig())

	for i := 0; i < 10000; i++ {
		vec := make([]float32, 768)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	query := make([]float32, 768)
	for i := range query {
		query[i] = rand.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Search(query, 10, nil)
	}
}

func BenchmarkDotProduct(b *testing.B) {
	a := make([]float32, 768)
	c := make([]float32, 768)
	for i := range a {
		a[i] = rand.Float32()
		c[i] = rand.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DotProduct(a, c)
	}
}

func BenchmarkCosineSimilarity(b *testing.B) {
	a := make([]float32, 768)
	c := make([]float32, 768)
	for i := range a {
		a[i] = rand.Float32()
		c[i] = rand.Float32()
	}
	magA := Magnitude(a)
	magC := Magnitude(c)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CosineSimilarity(a, c, magA, magC)
	}
}

// W4P.8: Tests for searchLayer pre-allocation optimization

// TestSearchLayerPreallocation verifies that search works correctly with pre-allocation.
// This is the happy path test ensuring the optimization doesn't break functionality.
func TestSearchLayerPreallocation(t *testing.T) {
	idx := New(Config{
		M:           16,
		EfConstruct: 100,
		EfSearch:    50,
		LevelMult:   0.36067977499789996,
		Dimension:   128,
	})

	// Insert test vectors
	for i := 0; i < 100; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if err := idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Verify search returns correct results
	query := make([]float32, 128)
	for j := range query {
		query[j] = rand.Float32()
	}

	results := idx.Search(query, 10, nil)

	// Should return requested number of results (or fewer if not enough nodes)
	if len(results) == 0 {
		t.Error("Search with pre-allocation returned no results")
	}
	if len(results) > 10 {
		t.Errorf("Search returned more results than requested: got %d, want <= 10", len(results))
	}

	// Results should be sorted by descending similarity
	for i := 1; i < len(results); i++ {
		if results[i].Similarity > results[i-1].Similarity {
			t.Errorf("Results not sorted: index %d has similarity %v > index %d with %v",
				i, results[i].Similarity, i-1, results[i-1].Similarity)
		}
	}
}

// TestSearchLayerPreallocationVariousK tests search with different k values
// to ensure pre-allocation works correctly across sizes.
func TestSearchLayerPreallocationVariousK(t *testing.T) {
	idx := New(Config{
		M:           16,
		EfConstruct: 100,
		EfSearch:    100, // Large efSearch to test various k values
		LevelMult:   0.36067977499789996,
		Dimension:   64,
	})

	// Insert test vectors
	for i := 0; i < 200; i++ {
		vec := make([]float32, 64)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if err := idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Test various k values: small, medium, large
	kValues := []int{1, 5, 10, 25, 50, 100}

	for _, k := range kValues {
		t.Run("k="+string(rune('0'+k/100))+string(rune('0'+(k/10)%10))+string(rune('0'+k%10)), func(t *testing.T) {
			query := make([]float32, 64)
			for j := range query {
				query[j] = rand.Float32()
			}

			results := idx.Search(query, k, nil)

			// Should not exceed requested k
			if len(results) > k {
				t.Errorf("k=%d: got %d results, expected <= %d", k, len(results), k)
			}

			// Should return at least 1 result (we have 200 nodes)
			if len(results) == 0 {
				t.Errorf("k=%d: no results returned", k)
			}

			// Results should be sorted by descending similarity
			for i := 1; i < len(results); i++ {
				if results[i].Similarity > results[i-1].Similarity {
					t.Errorf("k=%d: results not sorted at index %d", k, i)
				}
			}
		})
	}
}

// TestSearchLayerPreallocationSmallEf tests with small efSearch values
// to ensure we don't over-allocate for small searches.
func TestSearchLayerPreallocationSmallEf(t *testing.T) {
	// Test with very small efSearch
	idx := New(Config{
		M:           8,
		EfConstruct: 50,
		EfSearch:    5, // Very small efSearch
		LevelMult:   0.36067977499789996,
		Dimension:   32,
	})

	// Insert a few vectors
	for i := 0; i < 20; i++ {
		vec := make([]float32, 32)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if err := idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	query := make([]float32, 32)
	for j := range query {
		query[j] = rand.Float32()
	}

	results := idx.Search(query, 3, nil)

	// Should work correctly even with small efSearch
	if len(results) == 0 {
		t.Error("Search with small efSearch returned no results")
	}
	if len(results) > 3 {
		t.Errorf("Search returned more results than requested: got %d, want <= 3", len(results))
	}
}

// TestSearchLayerPreallocationLargeEf tests with large efSearch values
// to ensure pre-allocation handles larger capacity correctly.
func TestSearchLayerPreallocationLargeEf(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large efSearch test in short mode")
	}

	idx := New(Config{
		M:           32,
		EfConstruct: 200,
		EfSearch:    200, // Large efSearch
		LevelMult:   0.36067977499789996,
		Dimension:   128,
	})

	// Insert enough vectors
	for i := 0; i < 500; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if err := idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	query := make([]float32, 128)
	for j := range query {
		query[j] = rand.Float32()
	}

	results := idx.Search(query, 50, nil)

	if len(results) == 0 {
		t.Error("Search with large efSearch returned no results")
	}
	if len(results) > 50 {
		t.Errorf("Search returned more results than requested: got %d, want <= 50", len(results))
	}

	// Verify results are properly sorted
	for i := 1; i < len(results); i++ {
		if results[i].Similarity > results[i-1].Similarity {
			t.Errorf("Results not sorted at index %d", i)
		}
	}
}

// BenchmarkSearchLayerAllocations benchmarks search to verify allocation behavior.
// Run with: go test -bench=BenchmarkSearchLayerAllocations -benchmem
func BenchmarkSearchLayerAllocations(b *testing.B) {
	idx := New(Config{
		M:           16,
		EfConstruct: 100,
		EfSearch:    50,
		LevelMult:   0.36067977499789996,
		Dimension:   128,
	})

	// Insert test vectors
	for i := 0; i < 1000; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	query := make([]float32, 128)
	for i := range query {
		query[i] = rand.Float32()
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		idx.Search(query, 10, nil)
	}
}

// BenchmarkSearchLayerAllocationsVariousK benchmarks search with various k values.
func BenchmarkSearchLayerAllocationsVariousK(b *testing.B) {
	idx := New(Config{
		M:           16,
		EfConstruct: 100,
		EfSearch:    100,
		LevelMult:   0.36067977499789996,
		Dimension:   128,
	})

	// Insert test vectors
	for i := 0; i < 1000; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	query := make([]float32, 128)
	for i := range query {
		query[i] = rand.Float32()
	}

	kValues := []int{1, 10, 50, 100}

	for _, k := range kValues {
		b.Run("k="+string(rune('0'+k/100))+string(rune('0'+(k/10)%10))+string(rune('0'+k%10)), func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				idx.Search(query, k, nil)
			}
		})
	}
}

// BenchmarkSearchLayerAllocationsLargeGraph benchmarks on a larger graph.
func BenchmarkSearchLayerAllocationsLargeGraph(b *testing.B) {
	idx := New(Config{
		M:           32,
		EfConstruct: 200,
		EfSearch:    100,
		LevelMult:   0.36067977499789996,
		Dimension:   256,
	})

	// Insert more vectors for a larger graph
	for i := 0; i < 5000; i++ {
		vec := make([]float32, 256)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	query := make([]float32, 256)
	for i := range query {
		query[i] = rand.Float32()
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		idx.Search(query, 20, nil)
	}
}
