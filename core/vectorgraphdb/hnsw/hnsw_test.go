package hnsw

import (
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

	if err != ErrEmptyVector {
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

	if err != ErrNodeNotFound {
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

	if err != ErrNodeNotFound {
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
