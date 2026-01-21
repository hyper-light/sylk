package quantization

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func TestIndexCodebaseSimulation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping codebase indexing simulation in short mode")
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	codebaseRoot := "/home/ada/Projects/sylk"
	dim := 384

	t.Logf("Scanning codebase: %s", codebaseRoot)
	startScan := time.Now()

	var files []string
	err := filepath.Walk(codebaseRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".go" || ext == ".ts" || ext == ".js" || ext == ".py" || ext == ".md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk error: %v", err)
	}

	scanDuration := time.Since(startScan)
	t.Logf("Found %d files in %v", len(files), scanDuration)

	if len(files) == 0 {
		t.Fatal("no files found")
	}

	t.Logf("Generating embeddings (simulated) for %d files...", len(files))
	startEmbed := time.Now()

	vectors := make([][]float32, len(files))
	for i, path := range files {
		vectors[i] = generateDeterministicEmbedding(path, dim, rng)
	}
	embedDuration := time.Since(startEmbed)
	t.Logf("Generated embeddings in %v (%.2f files/sec)", embedDuration, float64(len(files))/embedDuration.Seconds())

	t.Log("Creating QuantizedHNSW index...")
	config := DefaultQuantizedHNSWConfig()
	config.HNSWConfig.Dimension = dim
	config.TrainingThreshold = 0
	config.PQConfig = ProductQuantizerConfig{
		NumSubspaces:         12,
		CentroidsPerSubspace: 32,
	}

	index, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("create index: %v", err)
	}

	t.Logf("Inserting %d vectors...", len(vectors))
	startInsert := time.Now()
	for i, vec := range vectors {
		id := fmt.Sprintf("file_%d", i)
		if err := index.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		if (i+1)%1000 == 0 {
			t.Logf("  inserted %d/%d", i+1, len(vectors))
		}
	}
	insertDuration := time.Since(startInsert)
	t.Logf("Inserted all vectors in %v (%.2f vec/sec)", insertDuration, float64(len(vectors))/insertDuration.Seconds())

	if !index.IsTrained() && len(vectors) >= config.TrainingThreshold {
		t.Log("Training index...")
		startTrain := time.Now()
		if err := index.Train(vectors); err != nil {
			t.Fatalf("train: %v", err)
		}
		trainDuration := time.Since(startTrain)
		t.Logf("Training completed in %v", trainDuration)
	}

	t.Log("Running search queries...")
	numQueries := 100
	k := 10
	queries := make([]int, numQueries)
	for i := range queries {
		queries[i] = rng.Intn(len(vectors))
	}

	startSearch := time.Now()
	var totalRecall float64
	for _, qIdx := range queries {
		results, err := index.Search(vectors[qIdx], k, nil)
		if err != nil {
			t.Fatalf("search: %v", err)
		}

		groundTruth := benchmarkComputeBruteForceKNN(vectors[qIdx], vectors, k)
		gtSet := make(map[int]bool)
		for _, idx := range groundTruth {
			gtSet[idx] = true
		}

		hits := 0
		for _, r := range results {
			idNum := 0
			fmt.Sscanf(r.ID, "file_%d", &idNum)
			if gtSet[idNum] {
				hits++
			}
		}
		totalRecall += float64(hits) / float64(k)
	}
	searchDuration := time.Since(startSearch)
	avgRecall := totalRecall / float64(numQueries)

	t.Logf("Search Results:")
	t.Logf("  Queries: %d", numQueries)
	t.Logf("  k: %d", k)
	t.Logf("  Total time: %v", searchDuration)
	t.Logf("  Avg query time: %v", searchDuration/time.Duration(numQueries))
	t.Logf("  QPS: %.2f", float64(numQueries)/searchDuration.Seconds())
	t.Logf("  Recall@%d: %.2f%%", k, avgRecall*100)

	stats := index.Stats()
	t.Logf("Index Stats:")
	t.Logf("  Total vectors: %d", stats.TotalVectors)
	t.Logf("  Compressed vectors: %d", stats.CompressedVectors)
	t.Logf("  Original memory: %s", formatBytesTest(stats.OriginalMemoryBytes))
	t.Logf("  Compressed memory: %s", formatBytesTest(stats.CompressedMemoryBytes))
	t.Logf("  Compression ratio: %.2fx", stats.CompressionRatio)

	t.Log("\n=== SUMMARY ===")
	t.Logf("Files indexed: %d", len(files))
	t.Logf("Vector dimension: %d", dim)
	t.Logf("Scan time: %v", scanDuration)
	t.Logf("Embed time: %v", embedDuration)
	t.Logf("Insert time: %v", insertDuration)
	t.Logf("Search time (%d queries): %v", numQueries, searchDuration)
	t.Logf("Recall@%d: %.2f%%", k, avgRecall*100)
	t.Logf("Compression: %.2fx", stats.CompressionRatio)
}

func generateDeterministicEmbedding(path string, dim int, _ *rand.Rand) []float32 {
	hash := sha256.Sum256([]byte(path))
	seed := int64(hash[0])<<56 | int64(hash[1])<<48 | int64(hash[2])<<40 | int64(hash[3])<<32 |
		int64(hash[4])<<24 | int64(hash[5])<<16 | int64(hash[6])<<8 | int64(hash[7])
	localRng := rand.New(rand.NewSource(seed))

	vec := make([]float32, dim)
	var norm float32
	for i := range vec {
		vec[i] = float32(localRng.NormFloat64())
		norm += vec[i] * vec[i]
	}
	norm = float32(math.Sqrt(float64(norm)))
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

func benchmarkComputeBruteForceKNN(query []float32, vectors [][]float32, k int) []int {
	type distIdx struct {
		dist float32
		idx  int
	}
	dists := make([]distIdx, len(vectors))
	for i, v := range vectors {
		var sum float32
		for j := range query {
			d := query[j] - v[j]
			sum += d * d
		}
		dists[i] = distIdx{dist: sum, idx: i}
	}
	for i := 0; i < k && i < len(dists); i++ {
		minIdx := i
		for j := i + 1; j < len(dists); j++ {
			if dists[j].dist < dists[minIdx].dist {
				minIdx = j
			}
		}
		dists[i], dists[minIdx] = dists[minIdx], dists[i]
	}
	result := make([]int, k)
	for i := 0; i < k && i < len(dists); i++ {
		result[i] = dists[i].idx
	}
	return result
}

func formatBytesTest(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func TestQueryAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping query accuracy test in short mode")
	}

	rng := rand.New(rand.NewSource(42))
	dim := 384
	numVectors := 2000
	numQueries := 50

	t.Logf("Generating %d vectors of dimension %d", numVectors, dim)
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		var norm float32
		for j := range vectors[i] {
			vectors[i][j] = float32(rng.NormFloat64())
			norm += vectors[i][j] * vectors[i][j]
		}
		norm = float32(math.Sqrt(float64(norm)))
		for j := range vectors[i] {
			vectors[i][j] /= norm
		}
	}

	config := DefaultQuantizedHNSWConfig()
	config.HNSWConfig.Dimension = dim
	config.TrainingThreshold = 0
	config.PQConfig = ProductQuantizerConfig{
		NumSubspaces:         12,
		CentroidsPerSubspace: 32,
	}

	index, err := NewQuantizedHNSW(config)
	if err != nil {
		t.Fatalf("create index: %v", err)
	}

	for i, vec := range vectors {
		id := fmt.Sprintf("vec_%d", i)
		if err := index.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	if err := index.Train(vectors); err != nil {
		t.Fatalf("train: %v", err)
	}

	queries := make([][]float32, numQueries)
	for i := range queries {
		idx := rng.Intn(numVectors)
		queries[i] = make([]float32, dim)
		for j := range queries[i] {
			queries[i][j] = vectors[idx][j] + float32(rng.NormFloat64())*0.1
		}
		var norm float32
		for j := range queries[i] {
			norm += queries[i][j] * queries[i][j]
		}
		norm = float32(math.Sqrt(float64(norm)))
		for j := range queries[i] {
			queries[i][j] /= norm
		}
	}

	kValues := []int{1, 5, 10, 20, 50}

	t.Log("\nQuery Accuracy Results:")
	t.Log("------------------------")
	for _, k := range kValues {
		var totalRecall float64
		for _, query := range queries {
			results, err := index.Search(query, k, nil)
			if err != nil {
				t.Fatalf("search: %v", err)
			}

			gt := benchmarkComputeBruteForceKNN(query, vectors, k)
			gtSet := make(map[int]bool)
			for _, idx := range gt {
				gtSet[idx] = true
			}

			hits := 0
			for _, r := range results {
				var idNum int
				fmt.Sscanf(r.ID, "vec_%d", &idNum)
				if gtSet[idNum] {
					hits++
				}
			}
			totalRecall += float64(hits) / float64(k)
		}
		avgRecall := totalRecall / float64(numQueries)
		t.Logf("  Recall@%d: %.2f%%", k, avgRecall*100)
	}

	stats := index.Stats()
	t.Logf("\nIndex Stats:")
	t.Logf("  Compression ratio: %.2fx", stats.CompressionRatio)
}

var _ = context.Background
