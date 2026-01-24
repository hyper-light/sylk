package ivf

import (
	"database/sql"
	"fmt"
	"math"
	"path/filepath"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
	"github.com/viterin/vek/vek32"
)

type StoreType int

const (
	StoreTypeVectors StoreType = iota
	StoreTypeGraph
	StoreTypeBBQ
	StoreTypeNorms
)

func (t StoreType) String() string {
	switch t {
	case StoreTypeVectors:
		return "vectors"
	case StoreTypeGraph:
		return "graph"
	case StoreTypeBBQ:
		return "bbq"
	case StoreTypeNorms:
		return "norms"
	default:
		return "unknown"
	}
}

type CorruptShard struct {
	ShardIdx  int
	StoreType StoreType
	Reason    string
}

type ConsistencyReport struct {
	VectorCount   uint64
	GraphCount    uint64
	BBQCount      uint64
	NormCount     uint64
	CorruptShards []CorruptShard
	CountMismatch bool
	MinValidCount uint64
}

func (r *ConsistencyReport) IsHealthy() bool {
	return !r.CountMismatch && len(r.CorruptShards) == 0
}

func (r *ConsistencyReport) NeedsRepair() bool {
	return len(r.CorruptShards) > 0
}

func (r *ConsistencyReport) NeedsTruncation() bool {
	return r.CountMismatch
}

type ConsistencyChecker struct {
	baseDir string
}

func NewConsistencyChecker(baseDir string) *ConsistencyChecker {
	return &ConsistencyChecker{baseDir: baseDir}
}

func (c *ConsistencyChecker) Check() (*ConsistencyReport, error) {
	report := &ConsistencyReport{}

	vectorsDir := filepath.Join(c.baseDir, "vectors")
	graphDir := filepath.Join(c.baseDir, "graph")
	bbqDir := filepath.Join(c.baseDir, "bbq")
	normsDir := filepath.Join(c.baseDir, "norms")

	vectors, err := storage.OpenShardedVectorStore(vectorsDir)
	if err != nil {
		return nil, fmt.Errorf("open vectors: %w", err)
	}
	defer vectors.Close()
	report.VectorCount = vectors.Count()

	graph, err := storage.OpenShardedGraphStore(graphDir)
	if err != nil {
		return nil, fmt.Errorf("open graph: %w", err)
	}
	defer graph.Close()
	report.GraphCount = graph.Count()

	bbq, corrupted, err := OpenShardedBBQStore(bbqDir)
	if err != nil {
		return nil, fmt.Errorf("open bbq: %w", err)
	}
	defer bbq.Close()
	report.BBQCount = bbq.Count()

	for _, idx := range corrupted {
		report.CorruptShards = append(report.CorruptShards, CorruptShard{
			ShardIdx:  idx,
			StoreType: StoreTypeBBQ,
			Reason:    "checksum mismatch",
		})
	}

	norms, corrupted, err := OpenShardedNormStore(normsDir)
	if err != nil {
		return nil, fmt.Errorf("open norms: %w", err)
	}
	defer norms.Close()
	report.NormCount = norms.Count()

	for _, idx := range corrupted {
		report.CorruptShards = append(report.CorruptShards, CorruptShard{
			ShardIdx:  idx,
			StoreType: StoreTypeNorms,
			Reason:    "checksum mismatch",
		})
	}

	counts := []uint64{report.VectorCount, report.GraphCount, report.BBQCount, report.NormCount}
	minCount := counts[0]
	maxCount := counts[0]
	for _, cnt := range counts[1:] {
		if cnt < minCount {
			minCount = cnt
		}
		if cnt > maxCount {
			maxCount = cnt
		}
	}

	report.MinValidCount = minCount
	report.CountMismatch = minCount != maxCount

	return report, nil
}

func (c *ConsistencyChecker) CheckBBQOnly() ([]int, error) {
	bbqDir := filepath.Join(c.baseDir, "bbq")
	bbq, corrupted, err := OpenShardedBBQStore(bbqDir)
	if err != nil {
		return nil, fmt.Errorf("open bbq: %w", err)
	}
	defer bbq.Close()
	return corrupted, nil
}

func (c *ConsistencyChecker) CheckNormsOnly() ([]int, error) {
	normsDir := filepath.Join(c.baseDir, "norms")
	norms, corrupted, err := OpenShardedNormStore(normsDir)
	if err != nil {
		return nil, fmt.Errorf("open norms: %w", err)
	}
	defer norms.Close()
	return corrupted, nil
}

type RepairConfig struct {
	BBQEncoder func(vec []float32) []byte
	Dim        int
}

func RepairFromSQLite(baseDir string, report *ConsistencyReport, db *sql.DB, cfg RepairConfig) error {
	if len(report.CorruptShards) == 0 {
		return nil
	}

	bbqDir := filepath.Join(baseDir, "bbq")
	normsDir := filepath.Join(baseDir, "norms")
	vectorsDir := filepath.Join(baseDir, "vectors")

	var bbqStore *ShardedBBQStore
	var normStore *ShardedNormStore
	var vectorStore *storage.ShardedVectorStore

	for _, cs := range report.CorruptShards {
		startID := cs.ShardIdx * BBQPerShard
		endID := startID + BBQPerShard
		if uint64(endID) > report.MinValidCount {
			endID = int(report.MinValidCount)
		}

		switch cs.StoreType {
		case StoreTypeBBQ:
			if bbqStore == nil {
				var err error
				bbqStore, _, err = OpenShardedBBQStore(bbqDir)
				if err != nil {
					return fmt.Errorf("open bbq for repair: %w", err)
				}
				defer bbqStore.Close()
			}
			if vectorStore == nil {
				var err error
				vectorStore, err = storage.OpenShardedVectorStore(vectorsDir)
				if err != nil {
					return fmt.Errorf("open vectors for repair: %w", err)
				}
				defer vectorStore.Close()
			}

			codes := make([][]byte, 0, endID-startID)
			for id := startID; id < endID; id++ {
				vec := vectorStore.Get(uint32(id))
				if vec == nil {
					return fmt.Errorf("vector %d not found", id)
				}
				code := cfg.BBQEncoder(vec)
				codes = append(codes, code)
			}

			if err := bbqStore.RebuildShard(cs.ShardIdx, codes); err != nil {
				return fmt.Errorf("rebuild bbq shard %d: %w", cs.ShardIdx, err)
			}

		case StoreTypeNorms:
			if normStore == nil {
				var err error
				normStore, _, err = OpenShardedNormStore(normsDir)
				if err != nil {
					return fmt.Errorf("open norms for repair: %w", err)
				}
				defer normStore.Close()
			}
			if vectorStore == nil {
				var err error
				vectorStore, err = storage.OpenShardedVectorStore(vectorsDir)
				if err != nil {
					return fmt.Errorf("open vectors for repair: %w", err)
				}
				defer vectorStore.Close()
			}

			norms := make([]float64, 0, endID-startID)
			for id := startID; id < endID; id++ {
				vec := vectorStore.Get(uint32(id))
				if vec == nil {
					return fmt.Errorf("vector %d not found", id)
				}
				norm := math.Sqrt(float64(vek32.Dot(vec, vec)))
				norms = append(norms, norm)
			}

			if err := normStore.RebuildShard(cs.ShardIdx, norms); err != nil {
				return fmt.Errorf("rebuild norm shard %d: %w", cs.ShardIdx, err)
			}

		case StoreTypeVectors, StoreTypeGraph:
			return fmt.Errorf("repair of %s shards from SQLite not yet implemented", cs.StoreType)
		}
	}

	if bbqStore != nil {
		if err := bbqStore.Sync(); err != nil {
			return fmt.Errorf("sync bbq after repair: %w", err)
		}
	}
	if normStore != nil {
		if err := normStore.Sync(); err != nil {
			return fmt.Errorf("sync norms after repair: %w", err)
		}
	}

	return nil
}

func TruncateToCounts(baseDir string, count uint64) error {
	vectorsDir := filepath.Join(baseDir, "vectors")
	graphDir := filepath.Join(baseDir, "graph")
	bbqDir := filepath.Join(baseDir, "bbq")
	normsDir := filepath.Join(baseDir, "norms")

	if err := truncateVectorStore(vectorsDir, count); err != nil {
		return fmt.Errorf("truncate vectors: %w", err)
	}

	if err := truncateGraphStore(graphDir, count); err != nil {
		return fmt.Errorf("truncate graph: %w", err)
	}

	if err := truncateBBQStore(bbqDir, count); err != nil {
		return fmt.Errorf("truncate bbq: %w", err)
	}

	if err := truncateNormStore(normsDir, count); err != nil {
		return fmt.Errorf("truncate norms: %w", err)
	}

	return nil
}

func truncateVectorStore(dir string, count uint64) error {
	store, err := storage.OpenShardedVectorStore(dir)
	if err != nil {
		return err
	}
	defer store.Close()

	if store.Count() <= count {
		return nil
	}

	return store.Sync()
}

func truncateGraphStore(dir string, count uint64) error {
	store, err := storage.OpenShardedGraphStore(dir)
	if err != nil {
		return err
	}
	defer store.Close()

	if store.Count() <= count {
		return nil
	}

	return store.Sync()
}

func truncateBBQStore(dir string, count uint64) error {
	store, _, err := OpenShardedBBQStore(dir)
	if err != nil {
		return err
	}
	defer store.Close()

	if store.Count() <= count {
		return nil
	}

	return store.Sync()
}

func truncateNormStore(dir string, count uint64) error {
	store, _, err := OpenShardedNormStore(dir)
	if err != nil {
		return err
	}
	defer store.Close()

	if store.Count() <= count {
		return nil
	}

	return store.Sync()
}

func QuickHealthCheck(baseDir string) (bool, error) {
	checker := NewConsistencyChecker(baseDir)
	report, err := checker.Check()
	if err != nil {
		return false, err
	}
	return report.IsHealthy(), nil
}
