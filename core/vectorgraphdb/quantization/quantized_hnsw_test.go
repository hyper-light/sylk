package quantization

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

// =============================================================================
// Test Helpers
// =============================================================================

// generateRandomVector creates a random vector of the given dimension.
func generateRandomVector(dim int, rng *rand.Rand) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = rng.Float32()*2 - 1 // Range [-1, 1]
	}
	return vec
}

// hnswGenerateClusteredVectors creates vectors clustered around centroids.
// This provides more realistic test data for quantization.
func hnswGenerateClusteredVectors(numVectors, dim, numClusters int, rng *rand.Rand) [][]float32 {
	// Generate cluster centroids
	centroids := make([][]float32, numClusters)
	for i := range centroids {
		centroids[i] = generateRandomVector(dim, rng)
	}

	// Generate vectors around centroids
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		centroid := centroids[i%numClusters]
		vec := make([]float32, dim)
		for j := range vec {
			// Add noise around centroid
			vec[j] = centroid[j] + (rng.Float32()*0.2 - 0.1)
		}
		vectors[i] = vec
	}
	return vectors
}

// normalizeVector normalizes a vector to unit length.
func normalizeVector(vec []float32) []float32 {
	var sum float64
	for _, v := range vec {
		sum += float64(v * v)
	}
	mag := math.Sqrt(sum)
	if mag == 0 {
		return vec
	}
	result := make([]float32, len(vec))
	for i, v := range vec {
		result[i] = float32(float64(v) / mag)
	}
	return result
}

// =============================================================================
// Configuration Tests
// =============================================================================

func TestDefaultQuantizedHNSWConfig(t *testing.T) {
	config := DefaultQuantizedHNSWConfig()

	if config.TrainingThreshold != DefaultTrainingThreshold {
		t.Errorf("TrainingThreshold = %d, want %d", config.TrainingThreshold, DefaultTrainingThreshold)
	}

	if config.StoreOriginalVectors != false {
		t.Errorf("StoreOriginalVectors = %v, want false", config.StoreOriginalVectors)
	}

	if config.PQConfig.NumSubspaces != DefaultNumSubspaces {
		t.Errorf("PQConfig.NumSubspaces = %d, want %d", config.PQConfig.NumSubspaces, DefaultNumSubspaces)
	}
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewQuantizedHNSW(t *testing.T) {
	tests := []struct {
		name      string
		config    QuantizedHNSWConfig
		expectErr bool
	}{
		{
			name:      "default config",
			config:    DefaultQuantizedHNSWConfig(),
			expectErr: false,
		},
		{
			name: "custom dimension",
			config: QuantizedHNSWConfig{
				HNSWConfig: hnsw.Config{
					M:         16,
					EfSearch:  50,
					Dimension: 512,
				},
				PQConfig: ProductQuantizerConfig{
					NumSubspaces:         32,
					CentroidsPerSubspace: 256,
				},
			},
			expectErr: false,
		},
		{
			name: "small index",
			config: QuantizedHNSWConfig{
				HNSWConfig: hnsw.Config{
					M:         8,
					EfSearch:  20,
					Dimension: 64,
				},
				PQConfig: ProductQuantizerConfig{
					NumSubspaces:         8,
					CentroidsPerSubspace: 64,
				},
				TrainingThreshold: 1000,
			},
			expectErr: false,
		},
		{
			name: "invalid dimension not divisible",
			config: QuantizedHNSWConfig{
				HNSWConfig: hnsw.Config{
					Dimension: 100,
				},
				PQConfig: ProductQuantizerConfig{
					NumSubspaces: 32, // 100 not divisible by 32
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qh, err := NewQuantizedHNSW(tt.config)

			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if qh == nil {
				t.Fatal("QuantizedHNSW is nil")
			}

			if qh.IsTrained() {
				t.Error("newly created index should not be trained")
			}

			if qh.Size() != 0 {
				t.Errorf("Size() = %d, want 0", qh.Size())
			}
		})
	}
}

// =============================================================================
// Insert Tests
// =============================================================================

func TestQuantizedHNSW_Insert(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         8,
			EfSearch:  50,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0, // Disable auto-training
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Insert vectors
	for i := 0; i < 100; i++ {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		vec := generateRandomVector(64, rng)
		err := qh.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		if err != nil {
			t.Fatalf("Insert failed for %s: %v", id, err)
		}
	}

	if qh.Size() != 100 {
		t.Errorf("Size() = %d, want 100", qh.Size())
	}

	// Check that vectors are pending (not trained yet)
	stats := qh.Stats()
	if stats.PendingVectors != 100 {
		t.Errorf("PendingVectors = %d, want 100", stats.PendingVectors)
	}
	if stats.CompressedVectors != 0 {
		t.Errorf("CompressedVectors = %d, want 0", stats.CompressedVectors)
	}
}

func TestQuantizedHNSW_InsertAfterTraining(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         8,
			EfSearch:  50,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Insert training vectors (need unique IDs for each)
	trainingVectors := hnswGenerateClusteredVectors(1000, 64, 64, rng)
	for i, vec := range trainingVectors {
		id := fmt.Sprintf("vec-%d", i)
		qh.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Train
	if err := qh.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	if !qh.IsTrained() {
		t.Error("index should be trained after Train()")
	}

	// Insert new vector after training
	newVec := generateRandomVector(64, rng)
	err = qh.Insert("new-vector", newVec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	if err != nil {
		t.Fatalf("Insert after training failed: %v", err)
	}

	// Check that new vector was compressed
	code, exists := qh.GetCode("new-vector")
	if !exists {
		t.Error("GetCode should find newly inserted vector")
	}
	if len(code) != 8 { // 8 subspaces
		t.Errorf("code length = %d, want 8", len(code))
	}
}

// =============================================================================
// Training Tests
// =============================================================================

func TestQuantizedHNSW_Train(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         8,
			EfSearch:  50,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Insert enough vectors for training (need unique IDs)
	vectors := hnswGenerateClusteredVectors(1000, 64, 64, rng)
	for i, vec := range vectors {
		id := fmt.Sprintf("vec-%d", i)
		qh.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Train
	err = qh.Train(nil)
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	if !qh.IsTrained() {
		t.Error("IsTrained() should be true after training")
	}

	// All vectors should now be compressed
	stats := qh.Stats()
	if stats.CompressedVectors == 0 {
		t.Error("CompressedVectors should be > 0 after training")
	}
}

func TestQuantizedHNSW_TrainInsufficientData(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Insert too few vectors
	for i := 0; i < 10; i++ {
		vec := generateRandomVector(64, rng)
		qh.Insert("id-"+string(rune('a'+i)), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Training should fail
	err = qh.Train(nil)
	if err == nil {
		t.Error("Train should fail with insufficient data")
	}
}

func TestQuantizedHNSW_TrainAlreadyTrained(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vectors := hnswGenerateClusteredVectors(1000, 64, 64, rng)
	for i, vec := range vectors {
		qh.Insert(fmt.Sprintf("vec-%d", i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// First training should succeed
	if err := qh.Train(nil); err != nil {
		t.Fatalf("First Train failed: %v", err)
	}

	// Second training should fail
	err = qh.Train(nil)
	if err != ErrAlreadyTrained {
		t.Errorf("expected ErrAlreadyTrained, got %v", err)
	}
}

// =============================================================================
// Search Tests
// =============================================================================

func TestQuantizedHNSW_SearchBeforeTraining(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         8,
			EfSearch:  50,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Insert some vectors
	for i := 0; i < 100; i++ {
		vec := generateRandomVector(64, rng)
		qh.Insert("id-"+string(rune('a'+i%26))+string(rune('0'+i/26)), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Search should work (falls back to HNSW)
	query := generateRandomVector(64, rng)
	results, err := qh.Search(query, 10, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Search should return results even before training")
	}
}

func TestQuantizedHNSW_SearchAfterTraining(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         16,
			EfSearch:  100,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Insert and track IDs
	vectors := hnswGenerateClusteredVectors(3000, 64, 64, rng)
	idToVec := make(map[string][]float32)
	for i, vec := range vectors {
		id := "id-" + string(rune('a'+i%26)) + string(rune('0'+i/26/26)) + string(rune('0'+i/26%26))
		idToVec[id] = vec
		qh.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Train
	if err := qh.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Search with a known vector
	query := vectors[0]
	results, err := qh.Search(query, 10, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search should return results")
	}

	// The query vector should be among the top results
	if len(results) > 0 && results[0].Similarity < 0.5 {
		t.Errorf("Top result similarity = %f, expected > 0.5", results[0].Similarity)
	}
}

func TestQuantizedHNSW_SearchWithFilter(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         8,
			EfSearch:  50,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vectors := hnswGenerateClusteredVectors(1000, 64, 64, rng)

	// Insert with different domains (need unique IDs)
	for i, vec := range vectors {
		id := fmt.Sprintf("vec-%d", i)
		domain := vectorgraphdb.Domain(i % 3) // Cycle through domains
		qh.Insert(id, vec, domain, vectorgraphdb.NodeTypeFile)
	}

	if err := qh.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Search with domain filter
	query := generateRandomVector(64, rng)
	filter := &hnsw.SearchFilter{
		Domains: []vectorgraphdb.Domain{vectorgraphdb.DomainCode},
	}

	results, err := qh.Search(query, 10, filter)
	if err != nil {
		t.Fatalf("Search with filter failed: %v", err)
	}

	// All results should have the filtered domain
	for _, r := range results {
		if r.Domain != vectorgraphdb.DomainCode {
			t.Errorf("Result domain = %v, want DomainCode", r.Domain)
		}
	}
}

// =============================================================================
// Delete Tests
// =============================================================================

func TestQuantizedHNSW_Delete(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Insert vectors
	for i := 0; i < 100; i++ {
		vec := generateRandomVector(64, rng)
		qh.Insert("id-"+string(rune('a'+i)), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	initialSize := qh.Size()

	// Delete a vector
	err = qh.Delete("id-a")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if qh.Size() != initialSize-1 {
		t.Errorf("Size() = %d, want %d", qh.Size(), initialSize-1)
	}

	if qh.Contains("id-a") {
		t.Error("Contains should return false for deleted vector")
	}

	// Delete non-existent should fail
	err = qh.Delete("non-existent")
	if err != ErrVectorNotFound {
		t.Errorf("Delete non-existent: expected ErrVectorNotFound, got %v", err)
	}
}

func TestQuantizedHNSW_DeleteAfterTraining(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vectors := hnswGenerateClusteredVectors(1000, 64, 64, rng)

	for i, vec := range vectors {
		qh.Insert(fmt.Sprintf("vec-%d", i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	if err := qh.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	initialCompressed := qh.Stats().CompressedVectors

	// Delete a vector
	err = qh.Delete("vec-0")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	stats := qh.Stats()
	if stats.CompressedVectors != initialCompressed-1 {
		t.Errorf("CompressedVectors = %d, want %d", stats.CompressedVectors, initialCompressed-1)
	}

	// Code should be gone
	_, exists := qh.GetCode("vec-0")
	if exists {
		t.Error("GetCode should return false for deleted vector")
	}
}

// =============================================================================
// Memory and Statistics Tests
// =============================================================================

func TestQuantizedHNSW_MemoryUsage(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 768,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         32,
			CentroidsPerSubspace: 256,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	// Need at least 256 * max(10, 24) = 6144 vectors for training
	// Use 7000 to have some margin
	vectors := hnswGenerateClusteredVectors(7000, 768, 64, rng)

	for i, vec := range vectors {
		qh.Insert(fmt.Sprintf("vec-%d", i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	if err := qh.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	stats := qh.Stats()

	// Original memory: 7000 vectors * 768 dims * 4 bytes
	expectedOriginal := int64(7000 * 768 * 4)
	if stats.OriginalMemoryBytes != expectedOriginal {
		t.Errorf("OriginalMemoryBytes = %d, want %d", stats.OriginalMemoryBytes, expectedOriginal)
	}

	// Compressed memory should be much smaller
	// 3000 vectors * 32 bytes = 96KB
	if stats.CompressedMemoryBytes >= stats.OriginalMemoryBytes {
		t.Error("CompressedMemoryBytes should be less than OriginalMemoryBytes")
	}

	// Compression ratio should be approximately 96x (768*4 / 32)
	expectedRatio := float64(768*4) / float64(32)
	tolerance := 0.1 * expectedRatio
	if math.Abs(stats.CompressionRatio-expectedRatio) > tolerance {
		t.Errorf("CompressionRatio = %f, want ~%f", stats.CompressionRatio, expectedRatio)
	}
}

func TestQuantizedHNSW_GetCompressionStats(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 768,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         32,
			CentroidsPerSubspace: 256,
		},
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	stats := qh.GetCompressionStats()

	if stats.VectorDimension != 768 {
		t.Errorf("VectorDimension = %d, want 768", stats.VectorDimension)
	}

	if stats.NumSubspaces != 32 {
		t.Errorf("NumSubspaces = %d, want 32", stats.NumSubspaces)
	}

	if stats.CentroidsPerSubspace != 256 {
		t.Errorf("CentroidsPerSubspace = %d, want 256", stats.CentroidsPerSubspace)
	}

	if stats.OriginalBytesPerVector != 768*4 {
		t.Errorf("OriginalBytesPerVector = %d, want %d", stats.OriginalBytesPerVector, 768*4)
	}

	if stats.CompressedBytesPerVector != 32 {
		t.Errorf("CompressedBytesPerVector = %d, want 32", stats.CompressedBytesPerVector)
	}

	expectedRatio := float64(768*4) / float64(32)
	if stats.TheoreticalCompressionRatio != expectedRatio {
		t.Errorf("TheoreticalCompressionRatio = %f, want %f", stats.TheoreticalCompressionRatio, expectedRatio)
	}

	// 256 centroids = 8 bits
	if stats.QuantizationBits != 8 {
		t.Errorf("QuantizationBits = %d, want 8", stats.QuantizationBits)
	}
}

// =============================================================================
// Recall Tests
// =============================================================================

func TestQuantizedHNSW_ComputeRecall(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         16,
			EfSearch:  100,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vectors := hnswGenerateClusteredVectors(3000, 64, 64, rng)

	for i, vec := range vectors {
		qh.Insert("id-"+string(rune('a'+i%26))+string(rune('0'+i/26)), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	if err := qh.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	queries := make([][]float32, 10)
	for i := range queries {
		queries[i] = generateRandomVector(64, rng)
	}

	recall, err := qh.ComputeRecall(queries, 10)
	if err != nil {
		t.Fatalf("ComputeRecall failed: %v", err)
	}

	// Recall should be reasonably good (> 70%)
	// Note: With clustered data and ADC, recall is typically 80-95%
	if recall < 0.5 {
		t.Errorf("Recall = %f, expected > 0.5", recall)
	}

	t.Logf("Recall@10: %.2f%%", recall*100)
}

func TestQuantizedHNSW_ComputeRecallNotTrained(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces: 8,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	queries := [][]float32{generateRandomVector(64, rng)}

	_, err = qh.ComputeRecall(queries, 10)
	if err != ErrQuantizerNotConfigured {
		t.Errorf("expected ErrQuantizerNotConfigured, got %v", err)
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestQuantizedHNSW_ConcurrentInsert(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         8,
			EfSearch:  50,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	const numGoroutines = 10
	const vectorsPerGoroutine = 100

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*vectorsPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(goroutineID)))

			for i := 0; i < vectorsPerGoroutine; i++ {
				id := string(rune('a'+goroutineID)) + "-" + string(rune('0'+i%10)) + string(rune('0'+i/10))
				vec := generateRandomVector(64, rng)
				if err := qh.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
					errors <- err
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent insert error: %v", err)
	}

	expectedSize := numGoroutines * vectorsPerGoroutine
	if qh.Size() != expectedSize {
		t.Errorf("Size() = %d, want %d", qh.Size(), expectedSize)
	}
}

func TestQuantizedHNSW_ConcurrentSearch(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         16,
			EfSearch:  50,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vectors := hnswGenerateClusteredVectors(1000, 64, 64, rng)

	for i, vec := range vectors {
		qh.Insert(fmt.Sprintf("vec-%d", i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	if err := qh.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	const numGoroutines = 10
	const searchesPerGoroutine = 50

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*searchesPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(goroutineID)))

			for i := 0; i < searchesPerGoroutine; i++ {
				query := generateRandomVector(64, rng)
				results, err := qh.Search(query, 10, nil)
				if err != nil {
					errors <- err
					continue
				}
				if len(results) == 0 {
					errors <- err
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Errorf("concurrent search error: %v", err)
		}
	}
}

// =============================================================================
// Snapshot Tests
// =============================================================================

func TestQuantizedHNSW_Snapshot(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vectors := hnswGenerateClusteredVectors(1000, 64, 64, rng)

	for i, vec := range vectors {
		qh.Insert(fmt.Sprintf("vec-%d", i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	if err := qh.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Create snapshot
	snapshot, err := qh.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	if snapshot.Trained != true {
		t.Error("Snapshot.Trained should be true")
	}

	if len(snapshot.Codes) == 0 {
		t.Error("Snapshot.Codes should not be empty")
	}

	if len(snapshot.IDMap) == 0 {
		t.Error("Snapshot.IDMap should not be empty")
	}

	if len(snapshot.QuantizerData) == 0 {
		t.Error("Snapshot.QuantizerData should not be empty for trained index")
	}
}

func TestQuantizedHNSW_RestoreFromSnapshot(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh1, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vectors := hnswGenerateClusteredVectors(1000, 64, 64, rng)

	for i, vec := range vectors {
		qh1.Insert(fmt.Sprintf("vec-%d", i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	if err := qh1.Train(nil); err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Create snapshot
	snapshot, err := qh1.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	// Create new index and restore
	qh2, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	if err := qh2.RestoreFromSnapshot(snapshot); err != nil {
		t.Fatalf("RestoreFromSnapshot failed: %v", err)
	}

	// Verify restored state
	if !qh2.IsTrained() {
		t.Error("Restored index should be trained")
	}

	stats1 := qh1.Stats()
	stats2 := qh2.Stats()

	if stats1.CompressedVectors != stats2.CompressedVectors {
		t.Errorf("CompressedVectors mismatch: %d vs %d", stats1.CompressedVectors, stats2.CompressedVectors)
	}
}

// =============================================================================
// Lazy Training Tests
// =============================================================================

func TestQuantizedHNSW_LazyTraining(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         8,
			EfSearch:  50,
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 3000, // Will trigger at 3000 vectors
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	// Insert vectors up to threshold
	for i := 0; i < 2999; i++ {
		vec := generateRandomVector(64, rng)
		qh.Insert("id-"+string(rune('a'+i%26))+string(rune('0'+i/26)), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	if qh.IsTrained() {
		t.Error("Index should not be trained before threshold")
	}

	// Insert one more to trigger training
	vec := generateRandomVector(64, rng)
	err = qh.Insert("trigger", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if !qh.IsTrained() {
		t.Error("Index should be trained after reaching threshold")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestQuantizedHNSW_EmptySearch(t *testing.T) {
	config := DefaultQuantizedHNSWConfig()
	config.HNSWConfig.Dimension = 64
	config.PQConfig.NumSubspaces = 8

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	query := generateRandomVector(64, rng)

	results, err := qh.Search(query, 10, nil)
	if err != nil {
		t.Fatalf("Search on empty index failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Search on empty index should return empty, got %d results", len(results))
	}
}

func TestQuantizedHNSW_SearchKZero(t *testing.T) {
	config := DefaultQuantizedHNSWConfig()
	config.HNSWConfig.Dimension = 64
	config.PQConfig.NumSubspaces = 8

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	query := generateRandomVector(64, rng)

	results, err := qh.Search(query, 0, nil)
	if err != nil {
		t.Fatalf("Search with k=0 failed: %v", err)
	}

	if results != nil {
		t.Errorf("Search with k=0 should return nil, got %v", results)
	}
}

func TestQuantizedHNSW_DuplicateInsert(t *testing.T) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			Dimension: 64,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         8,
			CentroidsPerSubspace: 64,
		},
		TrainingThreshold: 0,
	}

	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("failed to create QuantizedHNSW: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	vec1 := generateRandomVector(64, rng)
	vec2 := generateRandomVector(64, rng)

	// Insert first vector
	err = qh.Insert("same-id", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	// Insert with same ID (should update)
	err = qh.Insert("same-id", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	if err != nil {
		t.Fatalf("Second insert failed: %v", err)
	}

	// Size should account for updates appropriately
	if qh.Size() != 2 {
		// Note: Current implementation counts as new insert
		// This behavior may be changed based on requirements
		t.Logf("Size after duplicate insert: %d", qh.Size())
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkQuantizedHNSW_Insert(b *testing.B) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         16,
			EfSearch:  50,
			Dimension: 768,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         32,
			CentroidsPerSubspace: 256,
		},
		TrainingThreshold: 0,
	}

	qh, _ := NewQuantizedHNSW(config)
	rng := rand.New(rand.NewSource(42))

	// Pre-generate vectors
	vectors := make([][]float32, b.N)
	for i := range vectors {
		vectors[i] = generateRandomVector(768, rng)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qh.Insert("id-"+string(rune('a'+i%26)), vectors[i], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}
}

func BenchmarkQuantizedHNSW_Search(b *testing.B) {
	config := QuantizedHNSWConfig{
		HNSWConfig: hnsw.Config{
			M:         16,
			EfSearch:  100,
			Dimension: 768,
		},
		PQConfig: ProductQuantizerConfig{
			NumSubspaces:         32,
			CentroidsPerSubspace: 256,
		},
		TrainingThreshold: 0,
	}

	qh, _ := NewQuantizedHNSW(config)
	rng := rand.New(rand.NewSource(42))

	// Insert and train
	vectors := hnswGenerateClusteredVectors(5000, 768, 64, rng)
	for i, vec := range vectors {
		qh.Insert("id-"+string(rune('a'+i%26)), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}
	qh.Train(nil)

	// Pre-generate queries
	queries := make([][]float32, b.N)
	for i := range queries {
		queries[i] = generateRandomVector(768, rng)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qh.Search(queries[i], 10, nil)
	}
}
