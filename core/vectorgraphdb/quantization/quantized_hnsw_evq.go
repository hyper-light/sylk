package quantization

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

type QuantizationMode int

const (
	QuantizationModePQ QuantizationMode = iota
	QuantizationModeRaBitQ
	QuantizationModeLOPQ
	QuantizationModeHybrid
)

func (m QuantizationMode) String() string {
	switch m {
	case QuantizationModePQ:
		return "PQ"
	case QuantizationModeRaBitQ:
		return "RaBitQ"
	case QuantizationModeLOPQ:
		return "LOPQ"
	case QuantizationModeHybrid:
		return "Hybrid"
	default:
		return "Unknown"
	}
}

type EVQConfig struct {
	HNSWConfig        hnsw.Config
	Mode              QuantizationMode
	PQConfig          *ProductQuantizerConfig
	RaBitQConfig      *RaBitQConfig
	LOPQConfig        *LOPQConfig
	TrainingThreshold int
	FallbackToRaBitQ  bool
}

func DefaultEVQConfig() EVQConfig {
	return EVQConfig{
		HNSWConfig:        hnsw.DefaultConfig(),
		Mode:              QuantizationModeHybrid,
		TrainingThreshold: DefaultTrainingThreshold,
		FallbackToRaBitQ:  true,
	}
}

type EVQIndex struct {
	index           *hnsw.Index
	mode            QuantizationMode
	pqQuantizer     *ProductQuantizer
	rabitqEncoder   RaBitQEncoder
	lopqTrainer     *LocalCodebookTrainer
	codebookSwapper *CodebookSwapper

	pqCodes     map[uint64]PQCode
	rabitqCodes map[uint64]RaBitQCode
	lopqCodes   map[uint64]LOPQCode

	pendingVectors   map[uint64][]float32
	pendingDomains   map[uint64]vectorgraphdb.Domain
	pendingNodeTypes map[uint64]vectorgraphdb.NodeType

	idMap        map[string]uint64
	reverseIDMap map[uint64]string
	nextID       atomic.Uint64

	config       EVQConfig
	banditConfig *BanditConfig
	trained      atomic.Bool
	mu           sync.RWMutex
	stats        EVQStats
}

type EVQStats struct {
	TotalVectors         int64
	PQEncodedVectors     int64
	RaBitQEncodedVectors int64
	LOPQEncodedVectors   int64
	PendingVectors       int64
	ReadyPartitions      int
	TotalPartitions      int
	CompressionRatio     float64
}

func NewEVQIndex(config EVQConfig) (*EVQIndex, error) {
	idx := &EVQIndex{
		index:            hnsw.New(config.HNSWConfig),
		mode:             config.Mode,
		pqCodes:          make(map[uint64]PQCode),
		rabitqCodes:      make(map[uint64]RaBitQCode),
		lopqCodes:        make(map[uint64]LOPQCode),
		pendingVectors:   make(map[uint64][]float32),
		pendingDomains:   make(map[uint64]vectorgraphdb.Domain),
		pendingNodeTypes: make(map[uint64]vectorgraphdb.NodeType),
		idMap:            make(map[string]uint64),
		reverseIDMap:     make(map[uint64]string),
		config:           config,
	}
	idx.nextID.Store(1)

	vectorDim := config.HNSWConfig.Dimension
	if vectorDim == 0 {
		vectorDim = vectorgraphdb.EmbeddingDimension
	}

	switch config.Mode {
	case QuantizationModePQ, QuantizationModeHybrid:
		pqConfig := config.PQConfig
		if pqConfig == nil {
			defaultCfg := DefaultProductQuantizerConfig()
			pqConfig = &defaultCfg
		}
		pq, err := NewProductQuantizer(vectorDim, *pqConfig)
		if err != nil {
			return nil, err
		}
		idx.pqQuantizer = pq

	case QuantizationModeRaBitQ:
		rbqConfig := config.RaBitQConfig
		if rbqConfig == nil {
			defaultCfg := DefaultRaBitQConfig()
			defaultCfg.Dimension = vectorDim
			rbqConfig = &defaultCfg
		}
		encoder, err := NewRaBitQEncoder(*rbqConfig)
		if err != nil {
			return nil, err
		}
		idx.rabitqEncoder = encoder
		idx.trained.Store(true)

	case QuantizationModeLOPQ:
		lopqConfig := config.LOPQConfig
		if lopqConfig == nil {
			defaultCfg := DefaultLOPQConfig()
			defaultCfg.VectorDimension = vectorDim
			lopqConfig = &defaultCfg
		}
		idx.codebookSwapper = NewCodebookSwapper()
	}

	return idx, nil
}

func (idx *EVQIndex) Insert(id string, vector []float32, domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	numID, exists := idx.idMap[id]
	if !exists {
		numID = idx.nextID.Add(1) - 1
		idx.idMap[id] = numID
		idx.reverseIDMap[numID] = id
	}

	if err := idx.index.Insert(id, vector, domain, nodeType); err != nil {
		return err
	}

	idx.stats.TotalVectors++

	switch idx.mode {
	case QuantizationModeRaBitQ:
		idx.encodeRaBitQ(numID, vector)
	case QuantizationModePQ:
		idx.encodePQ(numID, vector)
	case QuantizationModeLOPQ:
		idx.encodeLOPQ(numID, vector)
	case QuantizationModeHybrid:
		idx.encodeHybrid(numID, vector)
	}

	return nil
}

func (idx *EVQIndex) encodeRaBitQ(numID uint64, vector []float32) {
	if idx.rabitqEncoder == nil {
		return
	}
	code, err := idx.rabitqEncoder.Encode(vector)
	if err != nil {
		return
	}
	idx.rabitqCodes[numID] = code
	idx.stats.RaBitQEncodedVectors++
}

func (idx *EVQIndex) encodePQ(numID uint64, vector []float32) {
	if !idx.trained.Load() {
		idx.pendingVectors[numID] = vector
		idx.stats.PendingVectors++
		idx.checkLazyTraining()
		return
	}
	code, err := idx.pqQuantizer.Encode(vector)
	if err != nil {
		return
	}
	idx.pqCodes[numID] = code
	idx.stats.PQEncodedVectors++
}

func (idx *EVQIndex) encodeLOPQ(numID uint64, vector []float32) {
	if idx.lopqTrainer == nil {
		if idx.config.FallbackToRaBitQ && idx.rabitqEncoder != nil {
			idx.encodeRaBitQ(numID, vector)
		} else {
			idx.pendingVectors[numID] = vector
			idx.stats.PendingVectors++
		}
		return
	}

	code, err := idx.lopqTrainer.EncodeVector(vector)
	if err != nil {
		if idx.config.FallbackToRaBitQ && idx.rabitqEncoder != nil {
			idx.encodeRaBitQ(numID, vector)
		}
		return
	}

	if code.HasLocalCode() {
		idx.lopqCodes[numID] = code
		idx.stats.LOPQEncodedVectors++
	} else if idx.config.FallbackToRaBitQ && idx.rabitqEncoder != nil {
		idx.encodeRaBitQ(numID, vector)
	}
}

func (idx *EVQIndex) encodeHybrid(numID uint64, vector []float32) {
	if idx.trained.Load() && idx.pqQuantizer != nil && idx.pqQuantizer.IsTrained() {
		idx.encodePQ(numID, vector)
		return
	}

	if idx.rabitqEncoder != nil {
		idx.encodeRaBitQ(numID, vector)
	} else {
		idx.pendingVectors[numID] = vector
		idx.stats.PendingVectors++
	}
	idx.checkLazyTraining()
}

func (idx *EVQIndex) checkLazyTraining() {
	if idx.trained.Load() {
		return
	}
	if idx.config.TrainingThreshold <= 0 {
		return
	}
	if len(idx.pendingVectors) >= idx.config.TrainingThreshold {
		idx.trainPQLocked()
	}
}

func (idx *EVQIndex) trainPQLocked() {
	if idx.pqQuantizer == nil {
		return
	}
	vectors := make([][]float32, 0, len(idx.pendingVectors))
	for _, v := range idx.pendingVectors {
		vectors = append(vectors, v)
	}

	trainConfig := DeriveTrainConfig(idx.pqQuantizer.CentroidsPerSubspace(), idx.pqQuantizer.SubspaceDim())
	minSamples := int(float32(idx.pqQuantizer.CentroidsPerSubspace()) * trainConfig.MinSamplesRatio)
	if len(vectors) < minSamples {
		return
	}

	if err := idx.pqQuantizer.TrainParallelWithConfig(context.Background(), vectors, trainConfig, 0); err != nil {
		return
	}

	idx.trained.Store(true)

	for id, vec := range idx.pendingVectors {
		code, err := idx.pqQuantizer.Encode(vec)
		if err != nil {
			continue
		}
		idx.pqCodes[id] = code
		idx.stats.PQEncodedVectors++
	}
	idx.pendingVectors = make(map[uint64][]float32)
	idx.stats.PendingVectors = 0
}

func (idx *EVQIndex) Search(query []float32, k int, filter *hnsw.SearchFilter) ([]hnsw.SearchResult, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if k <= 0 {
		return nil, nil
	}

	efSearch := idx.config.HNSWConfig.EfSearch
	if efSearch == 0 {
		efSearch = vectorgraphdb.DefaultEfSearch
	}
	candidateMultiplier := idx.getCandidateMultiplier()
	candidateCount := max(k*candidateMultiplier, efSearch)

	candidates := idx.index.Search(query, candidateCount, filter)
	if len(candidates) == 0 {
		return nil, nil
	}

	switch idx.mode {
	case QuantizationModeRaBitQ:
		return idx.rerankRaBitQ(query, candidates, k), nil
	case QuantizationModePQ:
		return idx.rerankPQ(query, candidates, k), nil
	case QuantizationModeLOPQ:
		return idx.rerankLOPQ(query, candidates, k), nil
	case QuantizationModeHybrid:
		return idx.rerankHybrid(query, candidates, k), nil
	}

	if len(candidates) > k {
		return candidates[:k], nil
	}
	return candidates, nil
}

func (idx *EVQIndex) rerankRaBitQ(query []float32, candidates []hnsw.SearchResult, k int) []hnsw.SearchResult {
	encoder, ok := idx.rabitqEncoder.(*RaBitQEncoderImpl)
	if !ok || encoder == nil {
		if len(candidates) > k {
			return candidates[:k]
		}
		return candidates
	}

	type scored struct {
		result   hnsw.SearchResult
		distance float32
	}
	scored_results := make([]scored, 0, len(candidates))

	for _, c := range candidates {
		numID, exists := idx.idMap[c.ID]
		if !exists {
			scored_results = append(scored_results, scored{result: c, distance: float32(1 - c.Similarity)})
			continue
		}

		code, hasCode := idx.rabitqCodes[numID]
		if !hasCode {
			scored_results = append(scored_results, scored{result: c, distance: float32(1 - c.Similarity)})
			continue
		}

		dist, err := encoder.ApproximateL2Distance(query, code)
		if err != nil {
			scored_results = append(scored_results, scored{result: c, distance: float32(1 - c.Similarity)})
			continue
		}
		scored_results = append(scored_results, scored{result: c, distance: dist})
	}

	sort.Slice(scored_results, func(i, j int) bool {
		return scored_results[i].distance < scored_results[j].distance
	})

	results := make([]hnsw.SearchResult, 0, min(k, len(scored_results)))
	for i := 0; i < len(scored_results) && i < k; i++ {
		r := scored_results[i].result
		r.Similarity = 1.0 - float64(scored_results[i].distance)/2.0
		if r.Similarity < 0 {
			r.Similarity = 0
		}
		results = append(results, r)
	}
	return results
}

func (idx *EVQIndex) rerankPQ(query []float32, candidates []hnsw.SearchResult, k int) []hnsw.SearchResult {
	if idx.pqQuantizer == nil || !idx.pqQuantizer.IsTrained() {
		if len(candidates) > k {
			return candidates[:k]
		}
		return candidates
	}

	distTable, err := idx.pqQuantizer.ComputeDistanceTable(query)
	if err != nil {
		if len(candidates) > k {
			return candidates[:k]
		}
		return candidates
	}

	type scored struct {
		result   hnsw.SearchResult
		distance float32
	}
	scored_results := make([]scored, 0, len(candidates))

	for _, c := range candidates {
		numID, exists := idx.idMap[c.ID]
		if !exists {
			scored_results = append(scored_results, scored{result: c, distance: float32(1 - c.Similarity)})
			continue
		}

		code, hasCode := idx.pqCodes[numID]
		if !hasCode {
			scored_results = append(scored_results, scored{result: c, distance: float32(1 - c.Similarity)})
			continue
		}

		dist := idx.pqQuantizer.AsymmetricDistance(distTable, code)
		scored_results = append(scored_results, scored{result: c, distance: dist})
	}

	sort.Slice(scored_results, func(i, j int) bool {
		return scored_results[i].distance < scored_results[j].distance
	})

	results := make([]hnsw.SearchResult, 0, min(k, len(scored_results)))
	for i := 0; i < len(scored_results) && i < k; i++ {
		r := scored_results[i].result
		r.Similarity = 1.0 - float64(scored_results[i].distance)/2.0
		if r.Similarity < 0 {
			r.Similarity = 0
		}
		results = append(results, r)
	}
	return results
}

func (idx *EVQIndex) rerankLOPQ(query []float32, candidates []hnsw.SearchResult, k int) []hnsw.SearchResult {
	type scored struct {
		result   hnsw.SearchResult
		distance float32
	}
	scored_results := make([]scored, 0, len(candidates))

	for _, c := range candidates {
		numID, exists := idx.idMap[c.ID]
		if !exists {
			scored_results = append(scored_results, scored{result: c, distance: float32(1 - c.Similarity)})
			continue
		}

		if code, hasLOPQ := idx.lopqCodes[numID]; hasLOPQ && code.HasLocalCode() {
			codebook := idx.codebookSwapper.Get(code.Partition)
			if codebook != nil && codebook.IsReady() {
				distTable, err := codebook.Quantizer.ComputeDistanceTable(query)
				if err == nil {
					dist := codebook.Quantizer.AsymmetricDistance(distTable, code.LocalCode)
					scored_results = append(scored_results, scored{result: c, distance: dist})
					continue
				}
			}
		}

		if code, hasRaBitQ := idx.rabitqCodes[numID]; hasRaBitQ {
			if encoder, ok := idx.rabitqEncoder.(*RaBitQEncoderImpl); ok && encoder != nil {
				dist, err := encoder.ApproximateL2Distance(query, code)
				if err == nil {
					scored_results = append(scored_results, scored{result: c, distance: dist})
					continue
				}
			}
		}

		scored_results = append(scored_results, scored{result: c, distance: float32(1 - c.Similarity)})
	}

	sort.Slice(scored_results, func(i, j int) bool {
		return scored_results[i].distance < scored_results[j].distance
	})

	results := make([]hnsw.SearchResult, 0, min(k, len(scored_results)))
	for i := 0; i < len(scored_results) && i < k; i++ {
		r := scored_results[i].result
		r.Similarity = 1.0 - float64(scored_results[i].distance)/2.0
		if r.Similarity < 0 {
			r.Similarity = 0
		}
		results = append(results, r)
	}
	return results
}

func (idx *EVQIndex) rerankHybrid(query []float32, candidates []hnsw.SearchResult, k int) []hnsw.SearchResult {
	if idx.pqQuantizer != nil && idx.pqQuantizer.IsTrained() {
		return idx.rerankPQ(query, candidates, k)
	}
	if idx.rabitqEncoder != nil {
		return idx.rerankRaBitQ(query, candidates, k)
	}
	if len(candidates) > k {
		return candidates[:k]
	}
	return candidates
}

func (idx *EVQIndex) Delete(id string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	numID, exists := idx.idMap[id]
	if !exists {
		return ErrVectorNotFound
	}

	if err := idx.index.Delete(id); err != nil {
		return err
	}

	delete(idx.pqCodes, numID)
	delete(idx.rabitqCodes, numID)
	delete(idx.lopqCodes, numID)
	delete(idx.pendingVectors, numID)
	delete(idx.pendingDomains, numID)
	delete(idx.pendingNodeTypes, numID)
	delete(idx.idMap, id)
	delete(idx.reverseIDMap, numID)

	idx.stats.TotalVectors--
	return nil
}

func (idx *EVQIndex) Stats() EVQStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	stats := idx.stats
	if idx.codebookSwapper != nil {
		stats.ReadyPartitions = idx.codebookSwapper.Count()
	}
	if idx.lopqTrainer != nil {
		trainerStats := idx.lopqTrainer.Stats()
		stats.TotalPartitions = trainerStats.TotalPartitions
	}

	vectorDim := idx.config.HNSWConfig.Dimension
	if vectorDim == 0 {
		vectorDim = vectorgraphdb.EmbeddingDimension
	}

	originalBytes := stats.TotalVectors * int64(vectorDim*4)
	var compressedBytes int64

	lopqCodeSize := idx.getLOPQCodeSizeBytes()

	if idx.pqQuantizer != nil {
		compressedBytes += stats.PQEncodedVectors * int64(idx.pqQuantizer.NumSubspaces())
	}
	compressedBytes += stats.RaBitQEncodedVectors * int64((vectorDim+7)/8)
	compressedBytes += stats.LOPQEncodedVectors * int64(lopqCodeSize)
	compressedBytes += stats.PendingVectors * int64(vectorDim*4)

	if compressedBytes > 0 {
		stats.CompressionRatio = float64(originalBytes) / float64(compressedBytes)
	}

	return stats
}

func (idx *EVQIndex) Mode() QuantizationMode {
	return idx.mode
}

func (idx *EVQIndex) IsTrained() bool {
	return idx.trained.Load()
}

func (idx *EVQIndex) Size() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return int(idx.stats.TotalVectors)
}

func (idx *EVQIndex) ComputeRecall(queries [][]float32, k int) (float64, error) {
	if len(queries) == 0 || k <= 0 {
		return 0, nil
	}

	var totalRecall float64
	for _, query := range queries {
		exactResults := idx.index.Search(query, k, nil)
		if len(exactResults) == 0 {
			continue
		}

		quantizedResults, err := idx.Search(query, k, nil)
		if err != nil || len(quantizedResults) == 0 {
			continue
		}

		exactSet := make(map[string]bool)
		for _, r := range exactResults {
			exactSet[r.ID] = true
		}

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

type EVQStatus struct {
	Mode             string
	IsTrained        bool
	TotalVectors     int64
	EncodedVectors   int64
	PendingVectors   int64
	CompressionRatio float64
	ReadyPartitions  int
	TotalPartitions  int
	MemoryUsageBytes int64
}

func (idx *EVQIndex) Status() EVQStatus {
	stats := idx.Stats()

	vectorDim := idx.config.HNSWConfig.Dimension
	if vectorDim == 0 {
		vectorDim = vectorgraphdb.EmbeddingDimension
	}

	lopqCodeSize := idx.getLOPQCodeSizeBytes()

	var memoryBytes int64
	if idx.pqQuantizer != nil {
		memoryBytes += stats.PQEncodedVectors * int64(idx.pqQuantizer.NumSubspaces())
	}
	memoryBytes += stats.RaBitQEncodedVectors * int64((vectorDim+7)/8)
	memoryBytes += stats.LOPQEncodedVectors * int64(lopqCodeSize)
	memoryBytes += stats.PendingVectors * int64(vectorDim*4)

	return EVQStatus{
		Mode:             idx.mode.String(),
		IsTrained:        idx.trained.Load(),
		TotalVectors:     stats.TotalVectors,
		EncodedVectors:   stats.PQEncodedVectors + stats.RaBitQEncodedVectors + stats.LOPQEncodedVectors,
		PendingVectors:   stats.PendingVectors,
		CompressionRatio: stats.CompressionRatio,
		ReadyPartitions:  stats.ReadyPartitions,
		TotalPartitions:  stats.TotalPartitions,
		MemoryUsageBytes: memoryBytes,
	}
}

func (idx *EVQIndex) getCandidateMultiplier() int {
	if idx.banditConfig != nil {
		return idx.banditConfig.CandidateMultiplier
	}
	return DefaultBanditConfig().CandidateMultiplier
}

func (idx *EVQIndex) getLOPQCodeSizeBytes() int {
	if idx.banditConfig != nil {
		return idx.banditConfig.LOPQCodeSizeBytes
	}
	return DefaultBanditConfig().LOPQCodeSizeBytes
}

func (idx *EVQIndex) SetBanditConfig(config *BanditConfig) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.banditConfig = config
}
