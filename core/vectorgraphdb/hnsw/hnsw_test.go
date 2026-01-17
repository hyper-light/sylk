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

	l.addNeighbor("node1", "node2", 10)
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
		l.addNeighbor("node1", randomNodeID(i), 5)
	}

	added := l.addNeighbor("node1", "overflow", 5)
	if added {
		t.Error("addNeighbor succeeded beyond max")
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
