package quantization

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrTrainerNotInitialized    = errors.New("lopq_trainer: not initialized")
	ErrTrainerInsufficientData  = errors.New("lopq_trainer: insufficient training data")
	ErrTrainerPartitionNotFound = errors.New("lopq_trainer: partition not found")
)

type LocalCodebookTrainer struct {
	config           LOPQConfig
	coarseQuantizer  *CoarseQuantizer
	codebooks        map[PartitionID]*LocalCodebook
	codebookVersions map[PartitionID]uint64
	partitionVectors map[PartitionID][][]float32
	banditConfig     *BanditConfig
	mu               sync.RWMutex
}

func NewLocalCodebookTrainer(config LOPQConfig, coarseQuantizer *CoarseQuantizer) (*LocalCodebookTrainer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if coarseQuantizer == nil || !coarseQuantizer.IsTrained() {
		return nil, ErrTrainerNotInitialized
	}

	return &LocalCodebookTrainer{
		config:           config,
		coarseQuantizer:  coarseQuantizer,
		codebooks:        make(map[PartitionID]*LocalCodebook),
		codebookVersions: make(map[PartitionID]uint64),
		partitionVectors: make(map[PartitionID][][]float32),
	}, nil
}

func (t *LocalCodebookTrainer) AddVectors(vectors [][]float32) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, vec := range vectors {
		partition := t.coarseQuantizer.GetPartition(vec)
		if !partition.Valid() {
			continue
		}
		t.partitionVectors[partition] = append(t.partitionVectors[partition], vec)
	}
}

func (t *LocalCodebookTrainer) GetPartitionVectorCount(partition PartitionID) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.partitionVectors[partition])
}

func (t *LocalCodebookTrainer) IsPartitionReadyForTraining(partition PartitionID) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.partitionVectors[partition]) >= t.config.TrainingThreshold
}

func (t *LocalCodebookTrainer) GetPartitionsReadyForTraining() []PartitionID {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var ready []PartitionID
	for partition, vecs := range t.partitionVectors {
		if len(vecs) >= t.config.TrainingThreshold {
			if _, hasCodebook := t.codebooks[partition]; !hasCodebook {
				ready = append(ready, partition)
			}
		}
	}
	return ready
}

func (t *LocalCodebookTrainer) TrainPartition(ctx context.Context, partition PartitionID) (*LocalCodebook, error) {
	t.mu.RLock()
	vectors := t.partitionVectors[partition]
	if len(vectors) < t.config.TrainingThreshold {
		t.mu.RUnlock()
		return nil, fmt.Errorf("%w: partition %d has %d vectors, need %d",
			ErrTrainerInsufficientData, partition, len(vectors), t.config.TrainingThreshold)
	}
	vectorsCopy := make([][]float32, len(vectors))
	for i, v := range vectors {
		vectorsCopy[i] = make([]float32, len(v))
		copy(vectorsCopy[i], v)
	}
	t.mu.RUnlock()

	residuals, err := t.computeResiduals(vectorsCopy, partition)
	if err != nil {
		return nil, fmt.Errorf("compute residuals: %w", err)
	}

	pqConfig := t.config.LocalPQConfig
	if pqConfig == nil {
		pqConfig = t.deriveLocalPQConfig()
	}

	pq, err := NewProductQuantizer(t.config.VectorDimension, *pqConfig)
	if err != nil {
		return nil, fmt.Errorf("create product quantizer: %w", err)
	}

	if err := pq.Train(ctx, residuals); err != nil {
		return nil, fmt.Errorf("train product quantizer: %w", err)
	}

	t.mu.Lock()
	t.codebookVersions[partition]++
	version := t.codebookVersions[partition]
	codebook := &LocalCodebook{
		PartitionID: partition,
		Quantizer:   pq,
		TrainedAt:   time.Now(),
		VectorCount: len(vectorsCopy),
		Version:     version,
	}
	t.codebooks[partition] = codebook
	t.mu.Unlock()

	return codebook, nil
}

func (t *LocalCodebookTrainer) computeResiduals(vectors [][]float32, partition PartitionID) ([][]float32, error) {
	residuals := make([][]float32, len(vectors))
	for i, vec := range vectors {
		residual, err := t.coarseQuantizer.ComputeResidual(vec, partition)
		if err != nil {
			return nil, fmt.Errorf("vector %d: %w", i, err)
		}
		residuals[i] = residual
	}
	return residuals, nil
}

func (t *LocalCodebookTrainer) deriveLocalPQConfig() *ProductQuantizerConfig {
	subspaceDimTarget, minSubspaces, centroidsPerSubspace := t.getBanditParams()

	numSubspaces := t.config.VectorDimension / subspaceDimTarget
	if numSubspaces < minSubspaces {
		numSubspaces = minSubspaces
	}
	if t.config.VectorDimension%numSubspaces != 0 {
		for numSubspaces > 2 {
			if t.config.VectorDimension%numSubspaces == 0 {
				break
			}
			numSubspaces--
		}
	}
	if numSubspaces < 2 {
		numSubspaces = 2
	}
	return &ProductQuantizerConfig{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: centroidsPerSubspace,
	}
}

func (t *LocalCodebookTrainer) getBanditParams() (subspaceDimTarget, minSubspaces, centroidsPerSubspace int) {
	if t.banditConfig != nil {
		return t.banditConfig.SubspaceDimTarget, t.banditConfig.MinSubspaces, t.banditConfig.CentroidsPerSubspace
	}
	defaults := DefaultBanditConfig()
	return defaults.SubspaceDimTarget, defaults.MinSubspaces, defaults.CentroidsPerSubspace
}

func (t *LocalCodebookTrainer) SetBanditConfig(config *BanditConfig) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.banditConfig = config
}

func (t *LocalCodebookTrainer) GetCodebook(partition PartitionID) *LocalCodebook {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.codebooks[partition]
}

func (t *LocalCodebookTrainer) GetAllCodebooks() map[PartitionID]*LocalCodebook {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[PartitionID]*LocalCodebook, len(t.codebooks))
	for k, v := range t.codebooks {
		result[k] = v
	}
	return result
}

func (t *LocalCodebookTrainer) SetCodebook(codebook *LocalCodebook) {
	if codebook == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.codebooks[codebook.PartitionID] = codebook
	if codebook.Version > t.codebookVersions[codebook.PartitionID] {
		t.codebookVersions[codebook.PartitionID] = codebook.Version
	}
}

func (t *LocalCodebookTrainer) EncodeVector(vector []float32) (LOPQCode, error) {
	partition := t.coarseQuantizer.GetPartition(vector)
	if !partition.Valid() {
		return LOPQCode{}, ErrLOPQPartitionNotFound
	}

	t.mu.RLock()
	codebook := t.codebooks[partition]
	t.mu.RUnlock()

	if codebook == nil || !codebook.IsReady() {
		return LOPQCode{
			Partition:    partition,
			FallbackCode: nil,
		}, nil
	}

	residual, err := t.coarseQuantizer.ComputeResidual(vector, partition)
	if err != nil {
		return LOPQCode{}, fmt.Errorf("compute residual: %w", err)
	}

	localCode, err := codebook.Quantizer.Encode(residual)
	if err != nil {
		return LOPQCode{}, fmt.Errorf("encode residual: %w", err)
	}

	return LOPQCode{
		Partition: partition,
		LocalCode: localCode,
	}, nil
}

func (t *LocalCodebookTrainer) DecodeVector(code LOPQCode) ([]float32, error) {
	if !code.Partition.Valid() {
		return nil, ErrLOPQPartitionNotFound
	}

	centroid, err := t.coarseQuantizer.GetCentroid(code.Partition)
	if err != nil {
		return nil, fmt.Errorf("get centroid: %w", err)
	}

	if !code.HasLocalCode() {
		return centroid, nil
	}

	t.mu.RLock()
	codebook := t.codebooks[code.Partition]
	t.mu.RUnlock()

	if codebook == nil || !codebook.IsReady() {
		return centroid, nil
	}

	residual := t.reconstructFromPQCode(codebook.Quantizer, code.LocalCode)
	if residual == nil {
		return centroid, nil
	}

	result := make([]float32, len(centroid))
	for i := range centroid {
		result[i] = centroid[i] + residual[i]
	}
	return result, nil
}

func (t *LocalCodebookTrainer) reconstructFromPQCode(pq *ProductQuantizer, code PQCode) []float32 {
	if pq == nil || !pq.IsTrained() || len(code) != pq.NumSubspaces() {
		return nil
	}

	centroids := pq.Centroids()
	if centroids == nil {
		return nil
	}

	result := make([]float32, pq.VectorDim())
	subspaceDim := pq.SubspaceDim()

	for m := 0; m < pq.NumSubspaces(); m++ {
		centroidIdx := int(code[m])
		if centroidIdx >= len(centroids[m]) {
			return nil
		}
		centroid := centroids[m][centroidIdx]
		start := m * subspaceDim
		copy(result[start:start+subspaceDim], centroid)
	}

	return result
}

func (t *LocalCodebookTrainer) ClearPartitionVectors(partition PartitionID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.partitionVectors, partition)
}

func (t *LocalCodebookTrainer) Stats() LocalCodebookTrainerStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var totalVectors int64
	var readyPartitions int
	for _, vecs := range t.partitionVectors {
		totalVectors += int64(len(vecs))
	}

	for partition := range t.partitionVectors {
		if _, has := t.codebooks[partition]; has {
			readyPartitions++
		}
	}

	return LocalCodebookTrainerStats{
		TotalPartitions:    len(t.partitionVectors),
		TrainedPartitions:  len(t.codebooks),
		ReadyForTraining:   readyPartitions,
		TotalVectorsStored: totalVectors,
		TrainingThreshold:  t.config.TrainingThreshold,
	}
}

type LocalCodebookTrainerStats struct {
	TotalPartitions    int
	TrainedPartitions  int
	ReadyForTraining   int
	TotalVectorsStored int64
	TrainingThreshold  int
}
