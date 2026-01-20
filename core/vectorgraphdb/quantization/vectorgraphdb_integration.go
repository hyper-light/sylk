package quantization

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

// =============================================================================
// Quantized VectorIndex Integration
// =============================================================================

// QuantizedVectorIndex implements vectorgraphdb.VectorIndex using QuantizedHNSW.
// It provides a drop-in replacement for the standard HNSW index with memory-efficient
// compressed vector storage via product quantization.
type QuantizedVectorIndex struct {
	qhnsw *QuantizedHNSW
	mu    sync.RWMutex
}

// NewQuantizedVectorIndex creates a new quantized vector index that implements
// the vectorgraphdb.VectorIndex interface.
func NewQuantizedVectorIndex(config QuantizedHNSWConfig) (*QuantizedVectorIndex, error) {
	qh, err := NewQuantizedHNSW(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create QuantizedHNSW: %w", err)
	}
	return &QuantizedVectorIndex{qhnsw: qh}, nil
}

// Search performs k-nearest neighbor search using asymmetric distance computation.
func (qvi *QuantizedVectorIndex) Search(query []float32, k int, filter *vectorgraphdb.SnapshotSearchFilter) []vectorgraphdb.SnapshotSearchResult {
	qvi.mu.RLock()
	defer qvi.mu.RUnlock()

	// Convert filter to hnsw.SearchFilter
	var hnswFilter *hnsw.SearchFilter
	if filter != nil {
		hnswFilter = &hnsw.SearchFilter{
			Domains:       filter.Domains,
			NodeTypes:     filter.NodeTypes,
			MinSimilarity: filter.MinSimilarity,
		}
	}

	results, err := qvi.qhnsw.Search(query, k, hnswFilter)
	if err != nil || len(results) == 0 {
		return nil
	}

	// Convert to SnapshotSearchResult
	snapshotResults := make([]vectorgraphdb.SnapshotSearchResult, len(results))
	for i, r := range results {
		snapshotResults[i] = vectorgraphdb.SnapshotSearchResult{
			ID:         r.ID,
			Similarity: r.Similarity,
			Domain:     r.Domain,
			NodeType:   r.NodeType,
		}
	}
	return snapshotResults
}

// Insert adds a vector to the index.
func (qvi *QuantizedVectorIndex) Insert(id string, vector []float32, domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType) error {
	qvi.mu.Lock()
	defer qvi.mu.Unlock()
	return qvi.qhnsw.Insert(id, vector, domain, nodeType)
}

// Delete removes a vector from the index.
func (qvi *QuantizedVectorIndex) Delete(id string) error {
	qvi.mu.Lock()
	defer qvi.mu.Unlock()
	return qvi.qhnsw.Delete(id)
}

// Size returns the number of vectors in the index.
func (qvi *QuantizedVectorIndex) Size() int {
	qvi.mu.RLock()
	defer qvi.mu.RUnlock()
	return qvi.qhnsw.Size()
}

// Train explicitly trains the quantizer with provided vectors.
func (qvi *QuantizedVectorIndex) Train(vectors [][]float32) error {
	qvi.mu.Lock()
	defer qvi.mu.Unlock()
	return qvi.qhnsw.Train(vectors)
}

// IsTrained returns whether the quantizer has been trained.
func (qvi *QuantizedVectorIndex) IsTrained() bool {
	qvi.mu.RLock()
	defer qvi.mu.RUnlock()
	return qvi.qhnsw.IsTrained()
}

// Stats returns compression statistics.
func (qvi *QuantizedVectorIndex) Stats() QuantizedHNSWStats {
	qvi.mu.RLock()
	defer qvi.mu.RUnlock()
	return qvi.qhnsw.Stats()
}

// MemoryUsage returns approximate memory usage in bytes.
func (qvi *QuantizedVectorIndex) MemoryUsage() int64 {
	qvi.mu.RLock()
	defer qvi.mu.RUnlock()
	return qvi.qhnsw.MemoryUsage()
}

// GetQuantizedHNSW returns the underlying QuantizedHNSW for advanced operations.
func (qvi *QuantizedVectorIndex) GetQuantizedHNSW() *QuantizedHNSW {
	return qvi.qhnsw
}

// =============================================================================
// Lazy Training Manager
// =============================================================================

// LazyTrainingConfig configures the lazy training behavior.
type LazyTrainingConfig struct {
	// MinVectorsForTraining is the minimum number of vectors required before training.
	MinVectorsForTraining int

	// TrainingTriggerCount is the count at which training is automatically triggered.
	TrainingTriggerCount int

	// AutoTrainEnabled enables automatic training when threshold is reached.
	AutoTrainEnabled bool

	// TrainingSampleRatio is the fraction of vectors to use for training (0-1).
	// A value of 1.0 uses all vectors. Lower values reduce training time.
	TrainingSampleRatio float64
}

// DefaultLazyTrainingConfig returns sensible defaults for lazy training.
func DefaultLazyTrainingConfig() LazyTrainingConfig {
	return LazyTrainingConfig{
		MinVectorsForTraining: DefaultMinTrainingSamples,
		TrainingTriggerCount:  DefaultTrainingThreshold,
		AutoTrainEnabled:      true,
		TrainingSampleRatio:   1.0,
	}
}

// LazyTrainingManager manages automatic training of the quantizer.
// It accumulates vectors and triggers training when a threshold is reached.
type LazyTrainingManager struct {
	index        *QuantizedVectorIndex
	config       LazyTrainingConfig
	insertCount  int64
	trainingDone int32 // atomic flag
	mu           sync.Mutex
}

// NewLazyTrainingManager creates a new lazy training manager.
func NewLazyTrainingManager(index *QuantizedVectorIndex, config LazyTrainingConfig) *LazyTrainingManager {
	return &LazyTrainingManager{
		index:  index,
		config: config,
	}
}

// OnInsert should be called after each successful insert.
// It may trigger training if conditions are met.
func (ltm *LazyTrainingManager) OnInsert() error {
	if atomic.LoadInt32(&ltm.trainingDone) == 1 {
		return nil // Already trained
	}

	if !ltm.config.AutoTrainEnabled {
		return nil
	}

	count := atomic.AddInt64(&ltm.insertCount, 1)
	if count >= int64(ltm.config.TrainingTriggerCount) {
		return ltm.TriggerTraining()
	}

	return nil
}

// TriggerTraining explicitly triggers training if not already done.
func (ltm *LazyTrainingManager) TriggerTraining() error {
	// Quick check without lock
	if atomic.LoadInt32(&ltm.trainingDone) == 1 {
		return nil
	}

	ltm.mu.Lock()
	defer ltm.mu.Unlock()

	// Double-check after acquiring lock
	if atomic.LoadInt32(&ltm.trainingDone) == 1 {
		return nil
	}

	if ltm.index.IsTrained() {
		atomic.StoreInt32(&ltm.trainingDone, 1)
		return nil
	}

	// Check minimum vectors
	if ltm.index.Size() < ltm.config.MinVectorsForTraining {
		return ErrInsufficientTrainingData
	}

	// Train with accumulated vectors (nil uses pending vectors)
	if err := ltm.index.Train(nil); err != nil {
		return err
	}

	atomic.StoreInt32(&ltm.trainingDone, 1)
	return nil
}

// IsTrained returns whether training has been completed.
func (ltm *LazyTrainingManager) IsTrained() bool {
	return atomic.LoadInt32(&ltm.trainingDone) == 1 || ltm.index.IsTrained()
}

// InsertCount returns the number of inserts tracked.
func (ltm *LazyTrainingManager) InsertCount() int64 {
	return atomic.LoadInt64(&ltm.insertCount)
}

// =============================================================================
// Compression Metrics
// =============================================================================

// CompressionMetrics provides detailed metrics about quantization.
type CompressionMetrics struct {
	// General stats
	TotalVectors          int64
	CompressedVectors     int64
	PendingVectors        int64
	TrainingVectorsUsed   int64
	IsTrained             bool

	// Memory metrics
	OriginalMemoryBytes   int64
	CompressedMemoryBytes int64
	CompressionRatio      float64
	MemorySavedBytes      int64
	MemorySavedPercent    float64

	// Quantization parameters
	VectorDimension          int
	NumSubspaces             int
	CentroidsPerSubspace     int
	BitsPerSubspace          int
	TheoreticalCompression   float64

	// Quality metrics (if available)
	EstimatedRecall float64
}

// CollectCompressionMetrics gathers comprehensive compression metrics.
func CollectCompressionMetrics(qvi *QuantizedVectorIndex) CompressionMetrics {
	stats := qvi.Stats()
	compStats := qvi.GetQuantizedHNSW().GetCompressionStats()

	metrics := CompressionMetrics{
		TotalVectors:        stats.TotalVectors,
		CompressedVectors:   stats.CompressedVectors,
		PendingVectors:      stats.PendingVectors,
		TrainingVectorsUsed: stats.TrainingVectorCount,
		IsTrained:           qvi.IsTrained(),

		OriginalMemoryBytes:   stats.OriginalMemoryBytes,
		CompressedMemoryBytes: stats.CompressedMemoryBytes,
		CompressionRatio:      stats.CompressionRatio,

		VectorDimension:        compStats.VectorDimension,
		NumSubspaces:           compStats.NumSubspaces,
		CentroidsPerSubspace:   compStats.CentroidsPerSubspace,
		BitsPerSubspace:        compStats.QuantizationBits,
		TheoreticalCompression: compStats.TheoreticalCompressionRatio,
	}

	// Calculate memory saved
	if stats.OriginalMemoryBytes > 0 {
		metrics.MemorySavedBytes = stats.OriginalMemoryBytes - stats.CompressedMemoryBytes
		metrics.MemorySavedPercent = float64(metrics.MemorySavedBytes) / float64(stats.OriginalMemoryBytes) * 100
	}

	return metrics
}

// =============================================================================
// Factory Functions
// =============================================================================

// VectorIndexConfig holds configuration for creating a vector index.
type VectorIndexConfig struct {
	// UseQuantization enables product quantization for memory efficiency.
	UseQuantization bool

	// HNSW configuration
	M           int
	EfConstruct int
	EfSearch    int
	Dimension   int

	// Quantization configuration (only used if UseQuantization is true)
	NumSubspaces         int
	CentroidsPerSubspace int
	TrainingThreshold    int
	StoreOriginalVectors bool
}

// DefaultVectorIndexConfig returns sensible defaults without quantization.
func DefaultVectorIndexConfig() VectorIndexConfig {
	return VectorIndexConfig{
		UseQuantization:      false,
		M:                    vectorgraphdb.DefaultM,
		EfConstruct:          vectorgraphdb.DefaultEfConstruct,
		EfSearch:             vectorgraphdb.DefaultEfSearch,
		Dimension:            vectorgraphdb.EmbeddingDimension,
		NumSubspaces:         DefaultNumSubspaces,
		CentroidsPerSubspace: DefaultCentroidsPerSubspace,
		TrainingThreshold:    DefaultTrainingThreshold,
		StoreOriginalVectors: false,
	}
}

// DefaultQuantizedVectorIndexConfig returns defaults with quantization enabled.
func DefaultQuantizedVectorIndexConfig() VectorIndexConfig {
	cfg := DefaultVectorIndexConfig()
	cfg.UseQuantization = true
	return cfg
}

// CreateVectorIndex creates either a standard HNSW or quantized index based on config.
// Returns the index as a vectorgraphdb.VectorIndex interface.
func CreateVectorIndex(config VectorIndexConfig) (vectorgraphdb.VectorIndex, error) {
	if config.UseQuantization {
		qhConfig := QuantizedHNSWConfig{
			HNSWConfig: hnsw.Config{
				M:           config.M,
				EfConstruct: config.EfConstruct,
				EfSearch:    config.EfSearch,
				Dimension:   config.Dimension,
			},
			PQConfig: ProductQuantizerConfig{
				NumSubspaces:         config.NumSubspaces,
				CentroidsPerSubspace: config.CentroidsPerSubspace,
			},
			TrainingThreshold:    config.TrainingThreshold,
			StoreOriginalVectors: config.StoreOriginalVectors,
		}
		return NewQuantizedVectorIndex(qhConfig)
	}

	// Return standard HNSW wrapped as VectorIndex
	hnswConfig := hnsw.Config{
		M:           config.M,
		EfConstruct: config.EfConstruct,
		EfSearch:    config.EfSearch,
		Dimension:   config.Dimension,
	}
	return NewHNSWVectorIndex(hnswConfig), nil
}

// =============================================================================
// Standard HNSW Wrapper
// =============================================================================

// HNSWVectorIndex wraps hnsw.Index to implement vectorgraphdb.VectorIndex.
type HNSWVectorIndex struct {
	index *hnsw.Index
}

// NewHNSWVectorIndex creates a new HNSW vector index wrapper.
func NewHNSWVectorIndex(config hnsw.Config) *HNSWVectorIndex {
	return &HNSWVectorIndex{
		index: hnsw.New(config),
	}
}

// Search performs k-nearest neighbor search.
func (hvi *HNSWVectorIndex) Search(query []float32, k int, filter *vectorgraphdb.SnapshotSearchFilter) []vectorgraphdb.SnapshotSearchResult {
	var hnswFilter *hnsw.SearchFilter
	if filter != nil {
		hnswFilter = &hnsw.SearchFilter{
			Domains:       filter.Domains,
			NodeTypes:     filter.NodeTypes,
			MinSimilarity: filter.MinSimilarity,
		}
	}

	results := hvi.index.Search(query, k, hnswFilter)
	if len(results) == 0 {
		return nil
	}

	snapshotResults := make([]vectorgraphdb.SnapshotSearchResult, len(results))
	for i, r := range results {
		snapshotResults[i] = vectorgraphdb.SnapshotSearchResult{
			ID:         r.ID,
			Similarity: r.Similarity,
			Domain:     r.Domain,
			NodeType:   r.NodeType,
		}
	}
	return snapshotResults
}

// Insert adds a vector to the index.
func (hvi *HNSWVectorIndex) Insert(id string, vector []float32, domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType) error {
	return hvi.index.Insert(id, vector, domain, nodeType)
}

// Delete removes a vector from the index.
func (hvi *HNSWVectorIndex) Delete(id string) error {
	return hvi.index.Delete(id)
}

// Size returns the number of vectors in the index.
func (hvi *HNSWVectorIndex) Size() int {
	return hvi.index.Size()
}

// GetIndex returns the underlying HNSW index.
func (hvi *HNSWVectorIndex) GetIndex() *hnsw.Index {
	return hvi.index
}

// =============================================================================
// Index Conversion Utilities
// =============================================================================

// ConvertToQuantized converts a standard HNSW index to a quantized index.
// This is useful for migrating existing indices to use compression.
// Note: This operation may take time for large indices.
func ConvertToQuantized(hnswIndex *hnsw.Index, qhConfig QuantizedHNSWConfig) (*QuantizedVectorIndex, error) {
	qvi, err := NewQuantizedVectorIndex(qhConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create quantized index: %w", err)
	}

	// Get all vectors from the HNSW index
	hnswIndex.RLock()
	vectors := hnswIndex.GetVectors()
	domains := hnswIndex.GetDomains()
	nodeTypes := hnswIndex.GetNodeTypes()
	hnswIndex.RUnlock()

	// Insert all vectors into the quantized index
	for id, vec := range vectors {
		domain := domains[id]
		nodeType := nodeTypes[id]
		if err := qvi.Insert(id, vec, domain, nodeType); err != nil {
			return nil, fmt.Errorf("failed to insert vector %s: %w", id, err)
		}
	}

	// Train if we have enough vectors
	if qvi.Size() >= qhConfig.TrainingThreshold {
		if err := qvi.Train(nil); err != nil {
			// Training failure is not fatal - index will work, just not compressed
			// In production, log this warning
		}
	}

	return qvi, nil
}

// EstimateMemorySavings estimates the memory savings from quantization.
// Returns original bytes, compressed bytes, and savings ratio.
func EstimateMemorySavings(numVectors, vectorDim, numSubspaces int) (original, compressed int64, savingsRatio float64) {
	original = int64(numVectors) * int64(vectorDim) * 4 // 4 bytes per float32
	compressed = int64(numVectors) * int64(numSubspaces) // 1 byte per subspace

	if compressed > 0 {
		savingsRatio = float64(original) / float64(compressed)
	}

	return original, compressed, savingsRatio
}
