package ivf

import (
	"encoding/binary"
	"fmt"
	"hash/crc64"
	"math"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
	"github.com/viterin/vek/vek32"
	"gopkg.in/yaml.v3"
)

const (
	metadataVersion = 1
	metadataFile    = "metadata.yaml"
	centroidsFile   = "centroids.bin"
	partitionsFile  = "partitions.bin"
	bbqMeansFile    = "bbq_means.bin"
	vectorsDir      = "vectors"
	graphDir        = "graph"
	normsDir        = "norms"
	bbqDir          = "bbq"
)

type IndexMetadata struct {
	Version           int       `yaml:"version"`
	NumVectors        int       `yaml:"num_vectors"`
	Dim               int       `yaml:"dim"`
	NumPartitions     int       `yaml:"num_partitions"`
	BBQCodeLen        int       `yaml:"bbq_code_len"`
	GraphR            int       `yaml:"graph_r"`
	GraphMedoid       uint32    `yaml:"graph_medoid"`
	ClusteringQuality float64   `yaml:"clustering_quality"`
	CreatedAt         time.Time `yaml:"created_at"`
	LastModified      time.Time `yaml:"last_modified"`
}

type PersistentIndex struct {
	baseDir string
	meta    *IndexMetadata

	vectorStore    *storage.ShardedVectorStore
	graphStore     *storage.ShardedGraphStore
	normStore      *ShardedNormStore
	bbqStore       *ShardedBBQStore
	centroidStore  *CentroidStore
	partitionIndex *PartitionIndex

	bbq *BBQ
}

func (idx *Index) Save(baseDir string) error {
	if idx.numVectors == 0 {
		return fmt.Errorf("ivf: cannot save empty index")
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("ivf: create base dir: %w", err)
	}

	vectorsPath := filepath.Join(baseDir, vectorsDir)
	vectorStore, err := storage.CreateShardedVectorStore(vectorsPath, idx.dim, 0)
	if err != nil {
		return fmt.Errorf("ivf: create vector store: %w", err)
	}
	defer vectorStore.Close()

	vectors := idx.getVectors()
	if _, err := vectorStore.AppendBatch(vectors); err != nil {
		return fmt.Errorf("ivf: write vectors: %w", err)
	}
	if err := vectorStore.Sync(); err != nil {
		return fmt.Errorf("ivf: sync vectors: %w", err)
	}

	graphPath := filepath.Join(baseDir, graphDir)
	graphStore, err := storage.CreateShardedGraphStore(graphPath, idx.graph.R, 0)
	if err != nil {
		return fmt.Errorf("ivf: create graph store: %w", err)
	}
	defer graphStore.Close()

	nodes := make([]storage.NodeNeighbors, idx.numVectors)
	for i := range idx.numVectors {
		nodes[i] = storage.NodeNeighbors{
			NodeID:    uint32(i),
			Neighbors: idx.graph.adjacency[i],
		}
	}
	if err := graphStore.SetNeighborsBatch(nodes); err != nil {
		return fmt.Errorf("ivf: write graph: %w", err)
	}
	if err := graphStore.Sync(); err != nil {
		return fmt.Errorf("ivf: sync graph: %w", err)
	}

	normsPath := filepath.Join(baseDir, normsDir)
	normStore, err := CreateShardedNormStore(normsPath)
	if err != nil {
		return fmt.Errorf("ivf: create norm store: %w", err)
	}
	defer normStore.Close()

	if _, err := normStore.AppendBatch(idx.vectorNorms); err != nil {
		return fmt.Errorf("ivf: write norms: %w", err)
	}
	if err := normStore.Sync(); err != nil {
		return fmt.Errorf("ivf: sync norms: %w", err)
	}

	bbqPath := filepath.Join(baseDir, bbqDir)
	bbqStore, err := CreateShardedBBQStore(bbqPath, idx.bbqCodeLen)
	if err != nil {
		return fmt.Errorf("ivf: create bbq store: %w", err)
	}
	defer bbqStore.Close()

	codes := make([][]byte, idx.numVectors)
	for i := range idx.numVectors {
		offset := i * idx.bbqCodeLen
		codes[i] = idx.bbqCodes[offset : offset+idx.bbqCodeLen]
	}
	if _, err := bbqStore.AppendBatch(codes); err != nil {
		return fmt.Errorf("ivf: write bbq codes: %w", err)
	}
	if err := bbqStore.Sync(); err != nil {
		return fmt.Errorf("ivf: sync bbq: %w", err)
	}

	centroidsPath := filepath.Join(baseDir, centroidsFile)
	if err := SaveCentroids(centroidsPath, idx.centroids, idx.centroidNorms); err != nil {
		return fmt.Errorf("ivf: save centroids: %w", err)
	}

	partitionsPath := filepath.Join(baseDir, partitionsFile)
	if err := SavePartitionIndex(partitionsPath, idx.partitionIDs); err != nil {
		return fmt.Errorf("ivf: save partitions: %w", err)
	}

	bbqMeansPath := filepath.Join(baseDir, bbqMeansFile)
	if err := saveBBQMeans(bbqMeansPath, idx.bbq.Means()); err != nil {
		return fmt.Errorf("ivf: save bbq means: %w", err)
	}

	now := time.Now()
	meta := &IndexMetadata{
		Version:           metadataVersion,
		NumVectors:        idx.numVectors,
		Dim:               idx.dim,
		NumPartitions:     idx.config.NumPartitions,
		BBQCodeLen:        idx.bbqCodeLen,
		GraphR:            idx.graph.R,
		GraphMedoid:       idx.graph.medoid,
		ClusteringQuality: idx.graph.ClusteringQuality(),
		CreatedAt:         now,
		LastModified:      now,
	}

	metaPath := filepath.Join(baseDir, metadataFile)
	if err := saveMetadata(metaPath, meta); err != nil {
		return fmt.Errorf("ivf: save metadata: %w", err)
	}

	return nil
}

func (idx *Index) getVectors() [][]float32 {
	vectors := make([][]float32, idx.numVectors)
	for i := range idx.numVectors {
		offset := i * idx.dim
		vectors[i] = idx.vectorsFlat[offset : offset+idx.dim]
	}
	return vectors
}

func saveMetadata(path string, meta *IndexMetadata) error {
	data, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

func saveBBQMeans(path string, means []float32) error {
	dim := len(means)
	size := int64(12 + dim*4 + 8)

	region, err := storage.MapFile(path, size, false)
	if err != nil {
		return fmt.Errorf("bbq means: map file: %w", err)
	}
	defer region.Close()

	data := region.Data()

	copy(data[0:4], []byte{'B', 'B', 'Q', 'M'})
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint32(data[8:12], uint32(dim))

	offset := 12
	for _, v := range means {
		binary.LittleEndian.PutUint32(data[offset:], *(*uint32)(unsafe.Pointer(&v)))
		offset += 4
	}

	checksum := crc64.Checksum(data[:offset], crc64.MakeTable(crc64.ECMA))
	binary.LittleEndian.PutUint64(data[offset:], checksum)

	return region.Sync()
}

func loadBBQMeans(path string) ([]float32, error) {
	region, err := storage.MapFile(path, 12, true)
	if err != nil {
		return nil, fmt.Errorf("bbq means: open: %w", err)
	}

	data := region.Data()
	if data[0] != 'B' || data[1] != 'B' || data[2] != 'Q' || data[3] != 'M' {
		region.Close()
		return nil, fmt.Errorf("bbq means: invalid magic")
	}

	dim := int(binary.LittleEndian.Uint32(data[8:12]))
	region.Close()

	size := int64(12 + dim*4 + 8)
	region, err = storage.MapFile(path, size, true)
	if err != nil {
		return nil, fmt.Errorf("bbq means: reopen: %w", err)
	}
	defer region.Close()

	data = region.Data()

	checksumOffset := 12 + dim*4
	storedChecksum := binary.LittleEndian.Uint64(data[checksumOffset:])
	computedChecksum := crc64.Checksum(data[:checksumOffset], crc64.MakeTable(crc64.ECMA))

	if storedChecksum != computedChecksum {
		return nil, fmt.Errorf("bbq means: checksum mismatch")
	}

	meansBytes := data[12:checksumOffset]
	means := unsafe.Slice((*float32)(unsafe.Pointer(&meansBytes[0])), dim)

	result := make([]float32, dim)
	copy(result, means)
	return result, nil
}

func LoadIndex(baseDir string) (*PersistentIndex, error) {
	metaPath := filepath.Join(baseDir, metadataFile)
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("ivf: read metadata: %w", err)
	}

	var meta IndexMetadata
	if err := yaml.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("ivf: parse metadata: %w", err)
	}

	if meta.Version != metadataVersion {
		return nil, fmt.Errorf("ivf: unsupported version %d", meta.Version)
	}

	vectorsPath := filepath.Join(baseDir, vectorsDir)
	vectorStore, err := storage.OpenShardedVectorStore(vectorsPath)
	if err != nil {
		return nil, fmt.Errorf("ivf: open vectors: %w", err)
	}

	graphPath := filepath.Join(baseDir, graphDir)
	graphStore, err := storage.OpenShardedGraphStore(graphPath)
	if err != nil {
		vectorStore.Close()
		return nil, fmt.Errorf("ivf: open graph: %w", err)
	}

	normsPath := filepath.Join(baseDir, normsDir)
	normStore, corrupted, err := OpenShardedNormStore(normsPath)
	if err != nil {
		vectorStore.Close()
		graphStore.Close()
		return nil, fmt.Errorf("ivf: open norms: %w", err)
	}
	if len(corrupted) > 0 {
		vectorStore.Close()
		graphStore.Close()
		normStore.Close()
		return nil, fmt.Errorf("ivf: corrupted norm shards: %v", corrupted)
	}

	bbqPath := filepath.Join(baseDir, bbqDir)
	bbqStore, corrupted, err := OpenShardedBBQStore(bbqPath)
	if err != nil {
		vectorStore.Close()
		graphStore.Close()
		normStore.Close()
		return nil, fmt.Errorf("ivf: open bbq: %w", err)
	}
	if len(corrupted) > 0 {
		vectorStore.Close()
		graphStore.Close()
		normStore.Close()
		bbqStore.Close()
		return nil, fmt.Errorf("ivf: corrupted bbq shards: %v", corrupted)
	}

	centroidsPath := filepath.Join(baseDir, centroidsFile)
	centroidStore, err := LoadCentroids(centroidsPath)
	if err != nil {
		vectorStore.Close()
		graphStore.Close()
		normStore.Close()
		bbqStore.Close()
		return nil, fmt.Errorf("ivf: load centroids: %w", err)
	}

	partitionsPath := filepath.Join(baseDir, partitionsFile)
	partitionIndex, err := LoadPartitionIndex(partitionsPath)
	if err != nil {
		vectorStore.Close()
		graphStore.Close()
		normStore.Close()
		bbqStore.Close()
		centroidStore.Close()
		return nil, fmt.Errorf("ivf: load partitions: %w", err)
	}

	bbqMeansPath := filepath.Join(baseDir, bbqMeansFile)
	bbqMeans, err := loadBBQMeans(bbqMeansPath)
	if err != nil {
		vectorStore.Close()
		graphStore.Close()
		normStore.Close()
		bbqStore.Close()
		centroidStore.Close()
		partitionIndex.Close()
		return nil, fmt.Errorf("ivf: load bbq means: %w", err)
	}

	bbq := NewBBQFromParams(meta.Dim, meta.BBQCodeLen, bbqMeans)

	return &PersistentIndex{
		baseDir:        baseDir,
		meta:           &meta,
		vectorStore:    vectorStore,
		graphStore:     graphStore,
		normStore:      normStore,
		bbqStore:       bbqStore,
		centroidStore:  centroidStore,
		partitionIndex: partitionIndex,
		bbq:            bbq,
	}, nil
}

func (pi *PersistentIndex) Close() error {
	var firstErr error

	if pi.vectorStore != nil {
		if err := pi.vectorStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if pi.graphStore != nil {
		if err := pi.graphStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if pi.normStore != nil {
		if err := pi.normStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if pi.bbqStore != nil {
		if err := pi.bbqStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if pi.centroidStore != nil {
		if err := pi.centroidStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if pi.partitionIndex != nil {
		if err := pi.partitionIndex.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func (pi *PersistentIndex) NumVectors() int {
	return pi.meta.NumVectors
}

func (pi *PersistentIndex) Dim() int {
	return pi.meta.Dim
}

func (pi *PersistentIndex) NumPartitions() int {
	return pi.meta.NumPartitions
}

func (pi *PersistentIndex) GraphMedoid() uint32 {
	return pi.meta.GraphMedoid
}

func (pi *PersistentIndex) GetVector(id uint32) []float32 {
	return pi.vectorStore.Get(id)
}

func (pi *PersistentIndex) GetNeighbors(id uint32) []uint32 {
	return pi.graphStore.GetNeighbors(id)
}

func (pi *PersistentIndex) GetNorm(id uint32) float64 {
	return pi.normStore.Get(id)
}

func (pi *PersistentIndex) GetBBQCode(id uint32) []byte {
	return pi.bbqStore.Get(id)
}

func (pi *PersistentIndex) GetCentroid(idx int) []float32 {
	return pi.centroidStore.GetCentroid(idx)
}

func (pi *PersistentIndex) GetCentroidNorm(idx int) float64 {
	return pi.centroidStore.GetNorm(idx)
}

func (pi *PersistentIndex) GetPartition(p int) []uint32 {
	return pi.partitionIndex.Partition(p)
}

func (pi *PersistentIndex) Search(query []float32, k int, nprobe int) []uint32 {
	if nprobe <= 0 {
		nprobe = 1
	}
	if nprobe > pi.meta.NumPartitions {
		nprobe = pi.meta.NumPartitions
	}

	partitions := pi.findNearestPartitions(query, nprobe)
	queryNorm := math.Sqrt(float64(vek32.Dot(query, query)))

	candidates := make([]searchCandidate, 0, nprobe*1000)

	for _, p := range partitions {
		ids := pi.partitionIndex.Partition(p)
		for _, id := range ids {
			vec := pi.vectorStore.Get(id)
			if vec == nil {
				continue
			}
			vecNorm := pi.normStore.Get(id)
			if vecNorm == 0 {
				continue
			}
			similarity := float64(vek32.Dot(query, vec)) / (queryNorm * vecNorm)
			candidates = append(candidates, searchCandidate{id: id, dist: -similarity})
		}
	}

	partialSort(candidates, k)
	if len(candidates) > k {
		candidates = candidates[:k]
	}

	ids := make([]uint32, len(candidates))
	for i, c := range candidates {
		ids[i] = c.id
	}
	return ids
}

type searchCandidate struct {
	id   uint32
	dist float64
}

func (pi *PersistentIndex) findNearestPartitions(query []float32, nprobe int) []int {
	queryNorm := math.Sqrt(float64(vek32.Dot(query, query)))

	candidates := make([]searchCandidate, pi.meta.NumPartitions)
	for i := range pi.meta.NumPartitions {
		centroid := pi.centroidStore.GetCentroid(i)
		centroidNorm := pi.centroidStore.GetNorm(i)
		dist := 1.0 - float64(vek32.Dot(query, centroid))/(queryNorm*centroidNorm)
		candidates[i] = searchCandidate{id: uint32(i), dist: dist}
	}

	partialSort(candidates, nprobe)

	result := make([]int, nprobe)
	for i := range nprobe {
		result[i] = int(candidates[i].id)
	}
	return result
}

func partialSort(candidates []searchCandidate, k int) {
	if len(candidates) <= k {
		for i := 1; i < len(candidates); i++ {
			j := i
			for j > 0 && candidates[j].dist < candidates[j-1].dist {
				candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
				j--
			}
		}
		return
	}

	pivotIdx := len(candidates) / 2
	pivot := candidates[pivotIdx]
	candidates[pivotIdx], candidates[len(candidates)-1] = candidates[len(candidates)-1], candidates[pivotIdx]

	storeIdx := 0
	for i := 0; i < len(candidates)-1; i++ {
		if candidates[i].dist < pivot.dist {
			candidates[i], candidates[storeIdx] = candidates[storeIdx], candidates[i]
			storeIdx++
		}
	}
	candidates[storeIdx], candidates[len(candidates)-1] = candidates[len(candidates)-1], candidates[storeIdx]

	if storeIdx == k-1 {
		return
	} else if storeIdx > k-1 {
		partialSort(candidates[:storeIdx], k)
	} else {
		partialSort(candidates[storeIdx+1:], k-storeIdx-1)
	}
}
