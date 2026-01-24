package ivf

import (
	"math"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/viterin/vek/vek32"
)

func TestPersistence_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	dim := 64
	n := 1000
	k := 16

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: k,
		NProbe:        4,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadIndex(indexDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()

	if loaded.NumVectors() != n {
		t.Errorf("NumVectors: got %d, want %d", loaded.NumVectors(), n)
	}
	if loaded.Dim() != dim {
		t.Errorf("Dim: got %d, want %d", loaded.Dim(), dim)
	}
	if loaded.NumPartitions() != k {
		t.Errorf("NumPartitions: got %d, want %d", loaded.NumPartitions(), k)
	}
}

func TestPersistence_VectorIntegrity(t *testing.T) {
	dir := t.TempDir()
	dim := 64
	n := 500
	k := 8

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: k,
		NProbe:        4,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadIndex(indexDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()

	for i := 0; i < n; i += 50 {
		original := vectors[i]
		loadedVec := loaded.GetVector(uint32(i))

		if loadedVec == nil {
			t.Fatalf("GetVector(%d): nil", i)
		}

		for j := range dim {
			if math.Abs(float64(original[j]-loadedVec[j])) > 1e-6 {
				t.Errorf("vector[%d][%d]: got %v, want %v", i, j, loadedVec[j], original[j])
				break
			}
		}
	}
}

func TestPersistence_SearchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dim := 64
	n := 1000
	numPartitions := 16

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: numPartitions,
		NProbe:        4,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadIndex(indexDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()

	query := vectors[0]
	k := 10

	originalResults := idx.SearchIVF(query, k)
	loadedResults := loaded.Search(query, k, 4)

	if len(loadedResults) == 0 {
		t.Error("loaded search returned no results")
	}

	if loadedResults[0] != 0 {
		foundOriginal := false
		for _, id := range loadedResults {
			if id == 0 {
				foundOriginal = true
				break
			}
		}
		if !foundOriginal {
			t.Logf("Warning: query vector not in top-%d results", k)
		}
	}

	if len(originalResults) > 0 && len(loadedResults) > 0 {
		overlap := 0
		loadedSet := make(map[uint32]bool)
		for _, id := range loadedResults {
			loadedSet[id] = true
		}
		for _, r := range originalResults {
			if loadedSet[r.ID] {
				overlap++
			}
		}
		overlapRatio := float64(overlap) / float64(len(originalResults))
		if overlapRatio < 0.5 {
			t.Logf("Warning: low overlap between original and loaded results: %.2f", overlapRatio)
		}
	}
}

func TestPersistence_GraphIntegrity(t *testing.T) {
	dir := t.TempDir()
	dim := 64
	n := 500
	k := 8

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: k,
		NProbe:        4,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadIndex(indexDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()

	for i := 0; i < n; i += 50 {
		neighbors := loaded.GetNeighbors(uint32(i))
		if neighbors == nil {
			t.Logf("node %d has no neighbors", i)
			continue
		}

		for _, neighbor := range neighbors {
			if int(neighbor) >= n {
				t.Errorf("node %d has invalid neighbor %d (n=%d)", i, neighbor, n)
			}
		}
	}
}

func TestPersistence_NormIntegrity(t *testing.T) {
	dir := t.TempDir()
	dim := 64
	n := 500
	k := 8

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: k,
		NProbe:        4,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadIndex(indexDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()

	for i := 0; i < n; i += 50 {
		vec := vectors[i]
		expectedNorm := math.Sqrt(float64(vek32.Dot(vec, vec)))
		loadedNorm := loaded.GetNorm(uint32(i))

		if math.Abs(loadedNorm-expectedNorm) > 1e-6 {
			t.Errorf("norm[%d]: got %v, want %v", i, loadedNorm, expectedNorm)
		}
	}
}

func TestPersistence_CentroidIntegrity(t *testing.T) {
	dir := t.TempDir()
	dim := 64
	n := 1000
	k := 16

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: k,
		NProbe:        4,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadIndex(indexDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()

	for i := range k {
		original := idx.centroids[i]
		loadedCentroid := loaded.GetCentroid(i)

		if loadedCentroid == nil {
			t.Fatalf("GetCentroid(%d): nil", i)
		}

		for j := range dim {
			if math.Abs(float64(original[j]-loadedCentroid[j])) > 1e-6 {
				t.Errorf("centroid[%d][%d]: got %v, want %v", i, j, loadedCentroid[j], original[j])
				break
			}
		}

		originalNorm := idx.centroidNorms[i]
		loadedNorm := loaded.GetCentroidNorm(i)
		if math.Abs(originalNorm-loadedNorm) > 1e-6 {
			t.Errorf("centroid norm[%d]: got %v, want %v", i, loadedNorm, originalNorm)
		}
	}
}

func TestPersistence_PartitionIntegrity(t *testing.T) {
	dir := t.TempDir()
	dim := 64
	n := 1000
	k := 16

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: k,
		NProbe:        4,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadIndex(indexDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()

	totalCount := 0
	for i := range k {
		partition := loaded.GetPartition(i)
		totalCount += len(partition)

		for _, id := range partition {
			if int(id) >= n {
				t.Errorf("partition %d has invalid ID %d (n=%d)", i, id, n)
			}
		}
	}

	if totalCount != n {
		t.Errorf("total IDs in partitions: got %d, want %d", totalCount, n)
	}
}

func TestPersistence_EmptyIndex(t *testing.T) {
	dir := t.TempDir()
	dim := 64

	config := Config{
		NumPartitions: 8,
		NProbe:        4,
	}

	idx := NewIndex(config, dim)

	indexDir := filepath.Join(dir, "index")
	err := idx.Save(indexDir)
	if err == nil {
		t.Error("expected error when saving empty index")
	}
}

func generateRandomVectors(n, dim int) [][]float32 {
	rng := rand.New(rand.NewPCG(42, 0))
	vectors := make([][]float32, n)
	for i := range n {
		vectors[i] = make([]float32, dim)
		for j := range dim {
			vectors[i][j] = rng.Float32()*2 - 1
		}
	}
	return vectors
}

func BenchmarkPersistence_Load(b *testing.B) {
	dir := b.TempDir()
	dim := 128
	n := 10000
	k := 64

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: k,
		NProbe:        8,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		b.Fatalf("save: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loaded, err := LoadIndex(indexDir)
		if err != nil {
			b.Fatalf("load: %v", err)
		}
		loaded.Close()
	}
}

func BenchmarkPersistence_Search(b *testing.B) {
	dir := b.TempDir()
	dim := 128
	n := 10000
	numPartitions := 64

	vectors := generateRandomVectors(n, dim)

	config := Config{
		NumPartitions: numPartitions,
		NProbe:        8,
	}

	idx := NewIndex(config, dim)
	idx.Build(vectors)

	indexDir := filepath.Join(dir, "index")
	if err := idx.Save(indexDir); err != nil {
		b.Fatalf("save: %v", err)
	}

	loaded, err := LoadIndex(indexDir)
	if err != nil {
		b.Fatalf("load: %v", err)
	}
	defer loaded.Close()

	query := vectors[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = loaded.Search(query, 10, 8)
	}
}
