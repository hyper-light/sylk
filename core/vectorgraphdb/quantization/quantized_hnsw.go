package quantization

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

// =============================================================================
// QuantizedHNSW Constants
// =============================================================================

const (
	// DefaultMinTrainingSamples is the minimum number of vectors required before training.
	// This ensures the quantizer has enough data to learn good centroids.
	DefaultMinTrainingSamples = 2560 // 10x the number of centroids (256)

	// DefaultTrainingThreshold is the default threshold for lazy training.
	// When this many vectors are accumulated, training is automatically triggered.
	DefaultTrainingThreshold = 10000
)

// =============================================================================
// QuantizedHNSW Errors
// =============================================================================

var (
	// ErrQuantizerNotConfigured is returned when operations require a trained quantizer.
	ErrQuantizerNotConfigured = errors.New("quantizer not configured")

	// ErrInsufficientTrainingData is returned when training is attempted with too few vectors.
	ErrInsufficientTrainingData = errors.New("insufficient training data")

	// ErrVectorNotFound is returned when a vector ID is not found in the index.
	ErrVectorNotFound = errors.New("vector not found")

	// ErrIndexNotReady is returned when the index is not ready for operations.
	ErrIndexNotReady = errors.New("index not ready")

	// ErrAlreadyTrained is returned when attempting to retrain an already trained quantizer.
	ErrAlreadyTrained = errors.New("quantizer already trained")
)

// =============================================================================
// QuantizedHNSWConfig
// =============================================================================

// QuantizedHNSWConfig holds configuration for creating a QuantizedHNSW index.
type QuantizedHNSWConfig struct {
	// HNSWConfig configures the underlying HNSW index.
	HNSWConfig hnsw.Config

	// PQConfig configures the product quantizer.
	PQConfig ProductQuantizerConfig

	// TrainingThreshold is the number of vectors to accumulate before lazy training.
	// Set to 0 to require explicit training via Train().
	TrainingThreshold int

	// StoreOriginalVectors determines whether to keep original vectors after compression.
	// If false, only compressed codes are stored (maximum compression).
	// If true, original vectors are retained for retraining or exact search fallback.
	StoreOriginalVectors bool

	// UseFastScan enables 4-bit FastScan PQ instead of standard 8-bit PQ.
	// FastScan uses 16 centroids per subspace (vs 256 for standard PQ).
	// Trade-off: faster distance computation but slightly lower accuracy.
	UseFastScan bool
}

// DefaultQuantizedHNSWConfig returns a QuantizedHNSWConfig with sensible defaults.
func DefaultQuantizedHNSWConfig() QuantizedHNSWConfig {
	return QuantizedHNSWConfig{
		HNSWConfig:           hnsw.DefaultConfig(),
		PQConfig:             DefaultProductQuantizerConfig(),
		TrainingThreshold:    DefaultTrainingThreshold,
		StoreOriginalVectors: false,
	}
}

// =============================================================================
// QuantizedHNSW
// =============================================================================

// QuantizedHNSW wraps an HNSW index with product quantization for compressed vector storage.
// It provides approximate nearest neighbor search with significantly reduced memory usage.
//
// The index operates in two modes:
// 1. Pre-training: Vectors are stored uncompressed until training threshold is reached
// 2. Post-training: Vectors are compressed to PQ codes for memory-efficient storage
//
// Search uses asymmetric distance computation (ADC) where the query remains uncompressed
// but database vectors are represented as PQ codes. This provides a good balance between
// search quality and memory efficiency.
type QuantizedHNSW struct {
	// index is the underlying HNSW graph structure
	index *hnsw.Index

	// quantizer handles vector compression/decompression
	quantizer *ProductQuantizer

	// bbq provides fast binary quantization for first-layer filtering
	bbq *BBQEncoder

	// bbqCodes stores BBQ-encoded vectors for fast distance computation
	bbqCodes map[uint64][]byte

	// codes stores compressed vectors by node ID (uint64 representation)
	codes map[uint64]PQCode

	// pendingVectors stores vectors before training (for lazy training)
	pendingVectors map[uint64][]float32

	// pendingDomains stores domain metadata for pending vectors
	pendingDomains map[uint64]vectorgraphdb.Domain

	// pendingNodeTypes stores node type metadata for pending vectors
	pendingNodeTypes map[uint64]vectorgraphdb.NodeType

	// idMap maps string IDs to uint64 for code storage
	idMap map[string]uint64

	// reverseIDMap maps uint64 back to string IDs
	reverseIDMap map[uint64]string

	// nextID is the next available uint64 ID
	nextID uint64

	// config holds the configuration
	config QuantizedHNSWConfig

	// trained indicates if the quantizer has been trained
	trained bool

	// mu protects all mutable state
	mu sync.RWMutex

	// stats tracks compression statistics
	stats QuantizedHNSWStats

	// difficultyEstimator predicts query difficulty for adaptive search
	difficultyEstimator *QueryDifficultyEstimator

	// avq provides anisotropic loss weighting for OOD queries
	avq *AnisotropicVQ

	// fastScan is the optional 4-bit FastScan quantizer
	fastScan *FastScanPQ

	// fastScanCodes stores packed 4-bit codes when FastScan is enabled
	fastScanCodes map[uint64][]byte

	// mnru handles safe vector updates with connectivity preservation
	mnru *hnsw.MNRUUpdater
}

// QuantizedHNSWStats tracks statistics for the quantized index.
type QuantizedHNSWStats struct {
	// TotalVectors is the total number of vectors in the index
	TotalVectors int64

	// CompressedVectors is the number of vectors stored as PQ codes
	CompressedVectors int64

	// PendingVectors is the number of vectors awaiting training
	PendingVectors int64

	// OriginalMemoryBytes is the estimated memory for uncompressed vectors
	OriginalMemoryBytes int64

	// CompressedMemoryBytes is the actual memory used for compressed storage
	CompressedMemoryBytes int64

	// CompressionRatio is the ratio of original to compressed size
	CompressionRatio float64

	// TrainingVectorCount is the number of vectors used for training
	TrainingVectorCount int64

	// QuantizationError is the mean squared reconstruction error from training
	QuantizationError float64
}

// NewQuantizedHNSW creates a new quantized HNSW index with the given configuration.
func NewQuantizedHNSW(config QuantizedHNSWConfig) (*QuantizedHNSW, error) {
	// Create the underlying HNSW index
	index := hnsw.New(config.HNSWConfig)

	// Determine vector dimension from config or use default
	vectorDim := config.HNSWConfig.Dimension
	if vectorDim == 0 {
		vectorDim = vectorgraphdb.EmbeddingDimension
	}

	// Create the product quantizer
	quantizer, err := NewProductQuantizer(vectorDim, config.PQConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create product quantizer: %w", err)
	}

	bbq := NewBBQEncoder(vectorDim, 4)

	qh := &QuantizedHNSW{
		index:            index,
		quantizer:        quantizer,
		bbq:              bbq,
		bbqCodes:         make(map[uint64][]byte),
		codes:            make(map[uint64]PQCode),
		pendingVectors:   make(map[uint64][]float32),
		pendingDomains:   make(map[uint64]vectorgraphdb.Domain),
		pendingNodeTypes: make(map[uint64]vectorgraphdb.NodeType),
		idMap:            make(map[string]uint64),
		reverseIDMap:     make(map[uint64]string),
		nextID:           1,
		config:           config,
		trained:          false,
		avq:              NewAnisotropicVQ(vectorDim),
	}

	if config.UseFastScan {
		numSubspaces := quantizer.NumSubspaces()
		qh.fastScan = NewFastScanPQ(vectorDim, numSubspaces)
		qh.fastScanCodes = make(map[uint64][]byte)
	}

	qh.mnru = hnsw.NewMNRUUpdater(index)

	return qh, nil
}

// =============================================================================
// Training
// =============================================================================

// Train trains the quantizer on sample vectors.
// If vectors is nil, uses the accumulated pending vectors for training.
// Returns ErrInsufficientTrainingData if there aren't enough vectors.
func (qh *QuantizedHNSW) Train(vectors [][]float32) error {
	qh.mu.Lock()
	defer qh.mu.Unlock()

	if qh.trained {
		return ErrAlreadyTrained
	}

	// Use pending vectors if none provided
	if vectors == nil {
		vectors = make([][]float32, 0, len(qh.pendingVectors))
		for _, v := range qh.pendingVectors {
			vectors = append(vectors, v)
		}
	}

	// Check minimum training data using derived config
	trainConfig := DeriveTrainConfig(qh.quantizer.CentroidsPerSubspace(), qh.quantizer.SubspaceDim())
	minSamples := int(float32(qh.quantizer.CentroidsPerSubspace()) * trainConfig.MinSamplesRatio)
	if len(vectors) < minSamples {
		return fmt.Errorf("%w: have %d vectors, need at least %d",
			ErrInsufficientTrainingData, len(vectors), minSamples)
	}

	// Train the quantizer with derived config
	if err := qh.quantizer.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 0); err != nil {
		return fmt.Errorf("failed to train quantizer: %w", err)
	}

	qh.trained = true
	qh.stats.TrainingVectorCount = int64(len(vectors))
	qh.stats.QuantizationError = qh.computeQuantizationError(vectors)

	qh.initDifficultyEstimator(vectors)

	// Compress all pending vectors
	for id, vec := range qh.pendingVectors {
		code, err := qh.quantizer.Encode(vec)
		if err != nil {
			continue // Skip vectors that fail to encode
		}
		qh.codes[id] = code
		qh.stats.CompressedVectors++
	}

	// Clear pending vectors if not storing originals
	if !qh.config.StoreOriginalVectors {
		qh.pendingVectors = make(map[uint64][]float32)
		qh.stats.PendingVectors = 0
	}

	qh.updateMemoryStats()
	return nil
}

// IsTrained returns true if the quantizer has been trained.
func (qh *QuantizedHNSW) IsTrained() bool {
	qh.mu.RLock()
	defer qh.mu.RUnlock()
	return qh.trained
}

func (qh *QuantizedHNSW) deriveCandidateCount(k, adaptedEf int) int {
	quantErr := qh.stats.QuantizationError
	if quantErr <= 0 {
		return adaptedEf
	}
	rerankMultiplier := math.Sqrt(1.0 / quantErr)
	rerankMultiplier = math.Max(1.0, math.Min(rerankMultiplier, float64(adaptedEf)/float64(k)))

	candidateCount := int(float64(k) * rerankMultiplier)
	return max(candidateCount, adaptedEf)
}

func (qh *QuantizedHNSW) computeQuantizationError(vectors [][]float32) float64 {
	if len(vectors) == 0 {
		return 0
	}

	centroids := qh.quantizer.Centroids()
	if centroids == nil {
		return 0
	}

	numSubspaces := qh.quantizer.NumSubspaces()
	subspaceDim := qh.quantizer.SubspaceDim()
	vectorDim := qh.quantizer.VectorDim()

	var totalError float64
	var count int

	for _, vec := range vectors {
		code, err := qh.quantizer.Encode(vec)
		if err != nil {
			continue
		}

		reconstructed := make([]float32, vectorDim)
		for m := range numSubspaces {
			centroidIdx := int(code[m])
			start := m * subspaceDim
			copy(reconstructed[start:start+subspaceDim], centroids[m][centroidIdx])
		}

		var sqErr float64
		for j, v := range vec {
			d := float64(v - reconstructed[j])
			sqErr += d * d
		}
		totalError += sqErr / float64(len(vec))
		count++
	}

	if count == 0 {
		return 0
	}
	return totalError / float64(count)
}

func (qh *QuantizedHNSW) initDifficultyEstimator(vectors [][]float32) {
	if len(vectors) == 0 {
		return
	}
	k := qh.quantizer.CentroidsPerSubspace()
	config := DeriveKMeansConfig(k)
	centroids, err := KMeansOptimal(context.Background(), vectors, k, config)
	if err != nil || len(centroids) == 0 {
		return
	}
	qh.difficultyEstimator = NewQueryDifficultyEstimator(centroids)
	qh.difficultyEstimator.CalibrateOODThreshold(vectors)
}

// =============================================================================
// Insert Operations
// =============================================================================

// Insert adds a vector to the index.
// If the quantizer is trained, the vector is compressed immediately.
// Otherwise, it's stored pending until training occurs.
func (qh *QuantizedHNSW) Insert(id string, vector []float32, domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType) error {
	qh.mu.Lock()
	defer qh.mu.Unlock()

	// Generate or retrieve uint64 ID
	numID, exists := qh.idMap[id]
	if !exists {
		numID = qh.nextID
		qh.nextID++
		qh.idMap[id] = numID
		qh.reverseIDMap[numID] = id
	}

	// Insert into underlying HNSW index
	if err := qh.index.Insert(id, vector, domain, nodeType); err != nil {
		return fmt.Errorf("failed to insert into HNSW: %w", err)
	}

	qh.stats.TotalVectors++

	if qh.trained {
		// Compress and store
		code, err := qh.quantizer.Encode(vector)
		if err != nil {
			return fmt.Errorf("failed to encode vector: %w", err)
		}
		qh.codes[numID] = code
		qh.bbqCodes[numID] = qh.bbq.EncodeDatabase(vector)
		if qh.fastScan != nil {
			qh.fastScanCodes[numID] = qh.fastScan.Encode(vector)
		}
		qh.stats.CompressedVectors++

		if qh.config.StoreOriginalVectors {
			qh.pendingVectors[numID] = vector
		}
	} else {
		// Store pending for later compression
		qh.pendingVectors[numID] = vector
		qh.pendingDomains[numID] = domain
		qh.pendingNodeTypes[numID] = nodeType
		qh.stats.PendingVectors++

		// Check for lazy training trigger
		if qh.config.TrainingThreshold > 0 && len(qh.pendingVectors) >= qh.config.TrainingThreshold {
			if err := qh.trainLocked(); err != nil {
				// Training failed, but insert succeeded - continue
				// Log error in production
			}
		}
	}

	qh.updateMemoryStats()
	return nil
}

func (qh *QuantizedHNSW) UpdateVector(id string, newVector []float32) error {
	if err := qh.mnru.UpdateVector(id, newVector); err != nil {
		return err
	}

	qh.mu.Lock()
	defer qh.mu.Unlock()

	numID, exists := qh.idMap[id]
	if !exists {
		return ErrVectorNotFound
	}

	if qh.trained {
		code, err := qh.quantizer.Encode(newVector)
		if err != nil {
			return fmt.Errorf("failed to encode vector: %w", err)
		}
		qh.codes[numID] = code
		qh.bbqCodes[numID] = qh.bbq.EncodeDatabase(newVector)
		if qh.fastScan != nil {
			qh.fastScanCodes[numID] = qh.fastScan.Encode(newVector)
		}
	} else {
		qh.pendingVectors[numID] = newVector
	}

	return nil
}

// trainLocked performs training while already holding the lock.
func (qh *QuantizedHNSW) trainLocked() error {
	if qh.trained {
		return nil
	}

	vectors := make([][]float32, 0, len(qh.pendingVectors))
	for _, v := range qh.pendingVectors {
		vectors = append(vectors, v)
	}

	trainConfig := DeriveTrainConfig(qh.quantizer.CentroidsPerSubspace(), qh.quantizer.SubspaceDim())
	minSamples := int(float32(qh.quantizer.CentroidsPerSubspace()) * trainConfig.MinSamplesRatio)
	if len(vectors) < minSamples {
		return ErrInsufficientTrainingData
	}

	if err := qh.quantizer.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 0); err != nil {
		return fmt.Errorf("failed to train quantizer: %w", err)
	}

	qh.bbq.Train(vectors)

	if qh.fastScan != nil {
		qh.fastScan.Train(vectors)
	}

	qh.trained = true
	qh.stats.TrainingVectorCount = int64(len(vectors))

	for id, vec := range qh.pendingVectors {
		code, err := qh.quantizer.Encode(vec)
		if err != nil {
			continue
		}
		qh.codes[id] = code
		qh.bbqCodes[id] = qh.bbq.EncodeDatabase(vec)
		if qh.fastScan != nil {
			qh.fastScanCodes[id] = qh.fastScan.Encode(vec)
		}
		qh.stats.CompressedVectors++
	}

	if !qh.config.StoreOriginalVectors {
		qh.pendingVectors = make(map[uint64][]float32)
		qh.stats.PendingVectors = 0
	}

	return nil
}

// =============================================================================
// Search Operations
// =============================================================================

// searchResult is used internally for sorting search results.
type searchResult struct {
	id       string
	distance float32
}

// Search finds approximate nearest neighbors using asymmetric distance computation.
// The query vector remains uncompressed for accuracy, while database vectors are
// compared using their PQ codes for efficiency.
func (qh *QuantizedHNSW) Search(query []float32, k int, filter *hnsw.SearchFilter) ([]hnsw.SearchResult, error) {
	qh.mu.RLock()
	defer qh.mu.RUnlock()

	if k <= 0 {
		return nil, nil
	}

	// If not trained, fall back to regular HNSW search
	if !qh.trained {
		return qh.index.Search(query, k, filter), nil
	}

	efSearch := qh.config.HNSWConfig.EfSearch
	if efSearch == 0 {
		efSearch = vectorgraphdb.DefaultEfSearch
	}

	if qh.avq != nil {
		qh.avq.UpdateQueryDistribution(query)
	}

	adaptedEf := efSearch
	isOOD := false
	if qh.difficultyEstimator != nil {
		adaptedEf = qh.difficultyEstimator.AdaptEfSearch(efSearch, query)
		isOOD = qh.difficultyEstimator.IsOOD(query)
	}
	candidateCount := qh.deriveCandidateCount(k, adaptedEf)

	candidates := qh.index.Search(query, candidateCount, filter)
	if len(candidates) == 0 {
		return nil, nil
	}

	if isOOD && qh.avq != nil {
		return qh.rerankWithAVQ(query, candidates, k)
	}

	bbqFilterThreshold := k * 4
	if len(candidates) > bbqFilterThreshold && len(qh.bbqCodes) > 0 {
		candidates = qh.filterWithBBQ(query, candidates, candidateCount)
	}

	var results []searchResult
	if qh.fastScan != nil && len(qh.fastScanCodes) > 0 {
		results = qh.rerankWithFastScan(query, candidates)
	} else {
		results = qh.rerankWithPQ(query, candidates)
	}

	// Sort by distance (ascending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].distance < results[j].distance
	})

	// Convert to SearchResult format and limit to k
	finalResults := make([]hnsw.SearchResult, 0, min(k, len(results)))
	for i := 0; i < len(results) && i < k; i++ {
		// Convert squared L2 distance back to similarity
		// For normalized vectors: similarity = 1 - dist/2
		// For unnormalized: use approximate conversion
		similarity := 1.0 - float64(results[i].distance)/2.0
		if similarity < 0 {
			similarity = 0
		}
		if similarity > 1 {
			similarity = 1
		}

		domain, nodeType, _ := qh.index.GetMetadata(results[i].id)
		finalResults = append(finalResults, hnsw.SearchResult{
			ID:         results[i].id,
			Similarity: similarity,
			Domain:     domain,
			NodeType:   nodeType,
		})
	}

	return finalResults, nil
}

type bbqCandidate struct {
	result   hnsw.SearchResult
	distance int
}

func (qh *QuantizedHNSW) filterWithBBQ(query []float32, candidates []hnsw.SearchResult, targetCount int) []hnsw.SearchResult {
	queryCode := qh.bbq.EncodeQuery(query)

	bbqCandidates := make([]bbqCandidate, 0, len(candidates))
	for _, c := range candidates {
		numID, exists := qh.idMap[c.ID]
		if !exists {
			continue
		}

		bbqCode, hasBBQ := qh.bbqCodes[numID]
		if !hasBBQ {
			bbqCandidates = append(bbqCandidates, bbqCandidate{
				result:   c,
				distance: math.MaxInt32,
			})
			continue
		}

		dist := qh.bbq.AsymmetricDistance(queryCode, bbqCode)
		bbqCandidates = append(bbqCandidates, bbqCandidate{
			result:   c,
			distance: dist,
		})
	}

	sort.Slice(bbqCandidates, func(i, j int) bool {
		return bbqCandidates[i].distance < bbqCandidates[j].distance
	})

	keepCount := min(targetCount, len(bbqCandidates))
	filtered := make([]hnsw.SearchResult, keepCount)
	for i := range keepCount {
		filtered[i] = bbqCandidates[i].result
	}

	return filtered
}

func (qh *QuantizedHNSW) rerankWithPQ(query []float32, candidates []hnsw.SearchResult) []searchResult {
	distTable, err := qh.quantizer.ComputeDistanceTable(query)
	if err != nil {
		results := make([]searchResult, len(candidates))
		for i, c := range candidates {
			results[i] = searchResult{id: c.ID, distance: float32(1.0 - c.Similarity)}
		}
		return results
	}

	results := make([]searchResult, 0, len(candidates))
	for _, c := range candidates {
		numID, exists := qh.idMap[c.ID]
		if !exists {
			continue
		}

		code, hasCode := qh.codes[numID]
		if !hasCode {
			results = append(results, searchResult{id: c.ID, distance: float32(1.0 - c.Similarity)})
			continue
		}

		dist := qh.quantizer.AsymmetricDistance(distTable, code)
		results = append(results, searchResult{id: c.ID, distance: dist})
	}
	return results
}

func (qh *QuantizedHNSW) rerankWithFastScan(query []float32, candidates []hnsw.SearchResult) []searchResult {
	tables := qh.fastScan.ComputeDistanceTable(query)

	results := make([]searchResult, 0, len(candidates))
	for _, c := range candidates {
		numID, exists := qh.idMap[c.ID]
		if !exists {
			continue
		}

		code, hasCode := qh.fastScanCodes[numID]
		if !hasCode {
			results = append(results, searchResult{id: c.ID, distance: float32(1.0 - c.Similarity)})
			continue
		}

		dist := qh.fastScan.AsymmetricDistance(tables, code)
		results = append(results, searchResult{id: c.ID, distance: dist})
	}
	return results
}

func (qh *QuantizedHNSW) rerankWithAVQ(query []float32, candidates []hnsw.SearchResult, k int) ([]hnsw.SearchResult, error) {
	queryDir := qh.avq.GetPrincipalQueryDirection()
	centroids := qh.quantizer.Centroids()
	numSubspaces := qh.quantizer.NumSubspaces()
	subspaceDim := qh.quantizer.SubspaceDim()

	type avqResult struct {
		id   string
		loss float64
	}

	results := make([]avqResult, 0, len(candidates))
	for _, c := range candidates {
		numID, exists := qh.idMap[c.ID]
		if !exists {
			continue
		}

		code, hasCode := qh.codes[numID]
		if !hasCode {
			results = append(results, avqResult{id: c.ID, loss: 1.0 - c.Similarity})
			continue
		}

		reconstructed := make([]float32, qh.quantizer.VectorDim())
		for m := range numSubspaces {
			centroidIdx := int(code[m])
			start := m * subspaceDim
			copy(reconstructed[start:start+subspaceDim], centroids[m][centroidIdx])
		}

		loss := qh.avq.AnisotropicLoss(query, reconstructed, queryDir)
		results = append(results, avqResult{id: c.ID, loss: loss})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].loss < results[j].loss
	})

	finalResults := make([]hnsw.SearchResult, 0, min(k, len(results)))
	for i := 0; i < len(results) && i < k; i++ {
		domain, nodeType, _ := qh.index.GetMetadata(results[i].id)
		similarity := 1.0 - results[i].loss
		if similarity < 0 {
			similarity = 0
		}
		if similarity > 1 {
			similarity = 1
		}
		finalResults = append(finalResults, hnsw.SearchResult{
			ID:         results[i].id,
			Similarity: similarity,
			Domain:     domain,
			NodeType:   nodeType,
		})
	}

	return finalResults, nil
}

// SearchWithDistances returns search results with both similarity and PQ distance.
// This is useful for debugging and analysis.
func (qh *QuantizedHNSW) SearchWithDistances(query []float32, k int) ([]uint64, []float32, error) {
	qh.mu.RLock()
	defer qh.mu.RUnlock()

	if k <= 0 {
		return nil, nil, nil
	}

	if !qh.trained {
		return nil, nil, ErrQuantizerNotConfigured
	}

	// Get candidates from HNSW
	efSearch := qh.config.HNSWConfig.EfSearch
	if efSearch == 0 {
		efSearch = vectorgraphdb.DefaultEfSearch
	}
	candidateCount := max(k*4, efSearch)

	candidates := qh.index.Search(query, candidateCount, nil)
	if len(candidates) == 0 {
		return nil, nil, nil
	}

	// Compute distance table
	distTable, err := qh.quantizer.ComputeDistanceTable(query)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compute distance table: %w", err)
	}

	// Compute distances and sort
	type distResult struct {
		id   uint64
		dist float32
	}
	results := make([]distResult, 0, len(candidates))

	for _, c := range candidates {
		numID, exists := qh.idMap[c.ID]
		if !exists {
			continue
		}

		code, hasCode := qh.codes[numID]
		if !hasCode {
			continue
		}

		dist := qh.quantizer.AsymmetricDistance(distTable, code)
		results = append(results, distResult{id: numID, dist: dist})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].dist < results[j].dist
	})

	// Return top k
	n := min(k, len(results))
	ids := make([]uint64, n)
	distances := make([]float32, n)
	for i := 0; i < n; i++ {
		ids[i] = results[i].id
		distances[i] = results[i].dist
	}

	return ids, distances, nil
}

// =============================================================================
// Delete Operations
// =============================================================================

// Delete removes a vector from the index.
func (qh *QuantizedHNSW) Delete(id string) error {
	qh.mu.Lock()
	defer qh.mu.Unlock()

	numID, exists := qh.idMap[id]
	if !exists {
		return ErrVectorNotFound
	}

	// Delete from underlying HNSW
	if err := qh.index.Delete(id); err != nil {
		return fmt.Errorf("failed to delete from HNSW: %w", err)
	}

	// Remove from our maps
	delete(qh.codes, numID)
	delete(qh.pendingVectors, numID)
	delete(qh.pendingDomains, numID)
	delete(qh.pendingNodeTypes, numID)
	delete(qh.idMap, id)
	delete(qh.reverseIDMap, numID)

	qh.stats.TotalVectors--
	if qh.trained {
		qh.stats.CompressedVectors--
	} else {
		qh.stats.PendingVectors--
	}

	qh.updateMemoryStats()
	return nil
}

// =============================================================================
// Statistics and Memory
// =============================================================================

// MemoryUsage returns the approximate memory usage of the quantized index in bytes.
func (qh *QuantizedHNSW) MemoryUsage() int64 {
	qh.mu.RLock()
	defer qh.mu.RUnlock()
	return qh.stats.CompressedMemoryBytes
}

// Stats returns current statistics for the index.
func (qh *QuantizedHNSW) Stats() QuantizedHNSWStats {
	qh.mu.RLock()
	defer qh.mu.RUnlock()
	return qh.stats
}

// updateMemoryStats recalculates memory statistics.
// Must be called while holding the lock.
func (qh *QuantizedHNSW) updateMemoryStats() {
	vectorDim := qh.quantizer.VectorDim()
	bytesPerFloat := 4

	// Original memory: all vectors at full precision
	qh.stats.OriginalMemoryBytes = qh.stats.TotalVectors * int64(vectorDim*bytesPerFloat)

	// Compressed memory: PQ codes + any pending vectors
	codeSize := qh.quantizer.NumSubspaces()
	qh.stats.CompressedMemoryBytes = qh.stats.CompressedVectors * int64(codeSize)
	qh.stats.CompressedMemoryBytes += qh.stats.PendingVectors * int64(vectorDim*bytesPerFloat)

	// Compression ratio
	if qh.stats.CompressedMemoryBytes > 0 {
		qh.stats.CompressionRatio = float64(qh.stats.OriginalMemoryBytes) / float64(qh.stats.CompressedMemoryBytes)
	} else {
		qh.stats.CompressionRatio = 0
	}
}

// =============================================================================
// Accessor Methods
// =============================================================================

// Size returns the total number of vectors in the index.
func (qh *QuantizedHNSW) Size() int {
	qh.mu.RLock()
	defer qh.mu.RUnlock()
	return int(qh.stats.TotalVectors)
}

// Contains checks if a vector with the given ID exists in the index.
func (qh *QuantizedHNSW) Contains(id string) bool {
	qh.mu.RLock()
	defer qh.mu.RUnlock()
	_, exists := qh.idMap[id]
	return exists
}

// GetCode returns the PQ code for a vector, if it exists and is trained.
func (qh *QuantizedHNSW) GetCode(id string) (PQCode, bool) {
	qh.mu.RLock()
	defer qh.mu.RUnlock()

	numID, exists := qh.idMap[id]
	if !exists {
		return nil, false
	}

	code, exists := qh.codes[numID]
	return code, exists
}

// GetVector returns the original vector for an ID.
// Only available if StoreOriginalVectors is true or vector is pending.
func (qh *QuantizedHNSW) GetVector(id string) ([]float32, error) {
	qh.mu.RLock()
	defer qh.mu.RUnlock()

	numID, exists := qh.idMap[id]
	if !exists {
		return nil, ErrVectorNotFound
	}

	if vec, exists := qh.pendingVectors[numID]; exists {
		result := make([]float32, len(vec))
		copy(result, vec)
		return result, nil
	}

	// Try to get from underlying HNSW index
	return qh.index.GetVector(id)
}

// GetQuantizer returns the underlying product quantizer.
// Useful for advanced operations or inspection.
func (qh *QuantizedHNSW) GetQuantizer() *ProductQuantizer {
	return qh.quantizer
}

// GetIndex returns the underlying HNSW index.
// Useful for advanced operations or inspection.
func (qh *QuantizedHNSW) GetIndex() *hnsw.Index {
	return qh.index
}

// =============================================================================
// Utility Functions
// =============================================================================

// ComputeRecall computes the recall@k of quantized search vs exact search.
// This is useful for evaluating quantization quality.
func (qh *QuantizedHNSW) ComputeRecall(queries [][]float32, k int) (float64, error) {
	if len(queries) == 0 || k <= 0 {
		return 0, nil
	}

	if !qh.IsTrained() {
		return 0, ErrQuantizerNotConfigured
	}

	var totalRecall float64
	for _, query := range queries {
		// Get exact results from HNSW
		exactResults := qh.index.Search(query, k, nil)
		if len(exactResults) == 0 {
			continue
		}

		// Get quantized results
		quantizedResults, err := qh.Search(query, k, nil)
		if err != nil || len(quantizedResults) == 0 {
			continue
		}

		// Build set of exact result IDs
		exactSet := make(map[string]bool)
		for _, r := range exactResults {
			exactSet[r.ID] = true
		}

		// Count matches
		matches := 0
		for _, r := range quantizedResults {
			if exactSet[r.ID] {
				matches++
			}
		}

		totalRecall += float64(matches) / float64(len(exactResults))
	}

	return totalRecall / float64(len(queries)), nil
}

// max returns the maximum of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// =============================================================================
// Serialization Support
// =============================================================================

// QuantizedHNSWSnapshot captures the state for persistence.
type QuantizedHNSWSnapshot struct {
	// Codes maps uint64 IDs to their PQ codes
	Codes map[uint64][]byte

	// IDMap maps string IDs to uint64 IDs
	IDMap map[string]uint64

	// NextID is the next available uint64 ID
	NextID uint64

	// Trained indicates if the quantizer was trained
	Trained bool

	// QuantizerData is the serialized ProductQuantizer
	QuantizerData []byte

	// Stats holds the snapshot statistics
	Stats QuantizedHNSWStats
}

// Snapshot creates a serializable snapshot of the index state.
func (qh *QuantizedHNSW) Snapshot() (*QuantizedHNSWSnapshot, error) {
	qh.mu.RLock()
	defer qh.mu.RUnlock()

	snapshot := &QuantizedHNSWSnapshot{
		Codes:   make(map[uint64][]byte, len(qh.codes)),
		IDMap:   make(map[string]uint64, len(qh.idMap)),
		NextID:  qh.nextID,
		Trained: qh.trained,
		Stats:   qh.stats,
	}

	// Copy codes
	for id, code := range qh.codes {
		data, err := code.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal code: %w", err)
		}
		snapshot.Codes[id] = data
	}

	// Copy ID map
	for k, v := range qh.idMap {
		snapshot.IDMap[k] = v
	}

	// Serialize quantizer if trained
	if qh.trained {
		data, err := qh.quantizer.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal quantizer: %w", err)
		}
		snapshot.QuantizerData = data
	}

	return snapshot, nil
}

// RestoreFromSnapshot restores index state from a snapshot.
func (qh *QuantizedHNSW) RestoreFromSnapshot(snapshot *QuantizedHNSWSnapshot) error {
	qh.mu.Lock()
	defer qh.mu.Unlock()

	// Restore quantizer if trained
	if snapshot.Trained && len(snapshot.QuantizerData) > 0 {
		if err := qh.quantizer.UnmarshalBinary(snapshot.QuantizerData); err != nil {
			return fmt.Errorf("failed to unmarshal quantizer: %w", err)
		}
	}

	// Restore codes
	qh.codes = make(map[uint64]PQCode, len(snapshot.Codes))
	for id, data := range snapshot.Codes {
		var code PQCode
		if err := code.UnmarshalBinary(data); err != nil {
			return fmt.Errorf("failed to unmarshal code: %w", err)
		}
		qh.codes[id] = code
	}

	// Restore ID maps
	qh.idMap = make(map[string]uint64, len(snapshot.IDMap))
	qh.reverseIDMap = make(map[uint64]string, len(snapshot.IDMap))
	for k, v := range snapshot.IDMap {
		qh.idMap[k] = v
		qh.reverseIDMap[v] = k
	}

	qh.nextID = snapshot.NextID
	qh.trained = snapshot.Trained
	qh.stats = snapshot.Stats

	return nil
}

// =============================================================================
// Compression Analysis
// =============================================================================

// CompressionStats returns detailed compression statistics.
type CompressionStats struct {
	// VectorDimension is the original vector dimension
	VectorDimension int

	// NumSubspaces is the number of PQ subspaces
	NumSubspaces int

	// CentroidsPerSubspace is the number of centroids per subspace
	CentroidsPerSubspace int

	// OriginalBytesPerVector is bytes per uncompressed vector
	OriginalBytesPerVector int

	// CompressedBytesPerVector is bytes per PQ code
	CompressedBytesPerVector int

	// TheoreticalCompressionRatio is the theoretical compression ratio
	TheoreticalCompressionRatio float64

	// ActualCompressionRatio is the observed compression ratio
	ActualCompressionRatio float64

	// QuantizationBits is bits per subspace (log2 of centroids)
	QuantizationBits int
}

// GetCompressionStats returns detailed compression statistics.
func (qh *QuantizedHNSW) GetCompressionStats() CompressionStats {
	qh.mu.RLock()
	defer qh.mu.RUnlock()

	vectorDim := qh.quantizer.VectorDim()
	numSubspaces := qh.quantizer.NumSubspaces()
	centroidsPerSubspace := qh.quantizer.CentroidsPerSubspace()

	originalBytes := vectorDim * 4  // 4 bytes per float32
	compressedBytes := numSubspaces // 1 byte per subspace (uint8)

	quantizationBits := int(math.Log2(float64(centroidsPerSubspace)))

	return CompressionStats{
		VectorDimension:             vectorDim,
		NumSubspaces:                numSubspaces,
		CentroidsPerSubspace:        centroidsPerSubspace,
		OriginalBytesPerVector:      originalBytes,
		CompressedBytesPerVector:    compressedBytes,
		TheoreticalCompressionRatio: float64(originalBytes) / float64(compressedBytes),
		ActualCompressionRatio:      qh.stats.CompressionRatio,
		QuantizationBits:            quantizationBits,
	}
}
