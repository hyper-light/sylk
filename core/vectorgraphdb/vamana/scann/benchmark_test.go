package scann

import (
	"context"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search/indexer"
	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestScaNN_Sylk(t *testing.T) {
	projectRoot := findProjectRoot(t)
	t.Logf("Project root: %s", projectRoot)

	symbols := collectSymbols(t, projectRoot)
	t.Logf("Collected %d symbols", len(symbols))

	if len(symbols) < 100 {
		t.Skipf("Not enough symbols: %d", len(symbols))
	}

	tmpDir := t.TempDir()
	config := vamana.DefaultConfig()

	vectorStore, labelStore, embeddings := buildStorageLayer(t, tmpDir, symbols)
	defer vectorStore.Close()
	defer labelStore.Close()

	graphStore, err := storage.CreateGraphStore(filepath.Join(tmpDir, "graph.bin"), config.R, len(symbols))
	if err != nil {
		t.Fatalf("CreateGraphStore failed: %v", err)
	}
	defer graphStore.Close()

	magCache := vamana.NewMagnitudeCache(len(symbols))
	for i := range len(symbols) {
		magCache.GetOrCompute(uint32(i), embeddings[i])
	}

	buildResult := buildWithScaNN(t, embeddings, vectorStore, graphStore, magCache, config)

	runSearchBenchmark(t, embeddings, vectorStore, graphStore, magCache, buildResult.Medoid, config)
}

type symbolInfo struct {
	id        string
	name      string
	kind      string
	signature string
	filePath  string
	nodeType  uint16
}

func findProjectRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get cwd: %v", err)
	}

	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "/home/ada/Projects/sylk"
}

func collectSymbols(t *testing.T, projectRoot string) []symbolInfo {
	t.Helper()
	ctx := context.Background()

	scanner := indexer.NewScanner(indexer.ScanConfig{
		RootPath:        projectRoot,
		IncludePatterns: []string{"*.go"},
		ExcludePatterns: []string{"*_test.go", "*.pb.go", "*.gen.go", "vendor/*"},
		MaxFileSize:     1 * 1024 * 1024,
	})

	fileCh, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	var files []string
	for f := range fileCh {
		files = append(files, f.Path)
	}

	tool := treesitter.NewTreeSitterTool()
	var symbols []symbolInfo
	seen := make(map[string]bool)
	var mu sync.Mutex

	workCh := make(chan struct{ path, content string }, 100)
	var wg sync.WaitGroup

	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workCh {
				result, err := tool.ParseFast(ctx, item.path, []byte(item.content))
				if err != nil {
					continue
				}

				relPath, _ := filepath.Rel(projectRoot, item.path)
				var local []symbolInfo

				for _, fn := range result.Functions {
					nt := uint16(vectorgraphdb.NodeTypeFunction)
					if fn.IsMethod {
						nt = uint16(vectorgraphdb.NodeTypeMethod)
					}
					sig := fn.Parameters
					if fn.ReturnType != "" {
						sig += " " + fn.ReturnType
					}
					id := relPath + ":" + fn.Name
					local = append(local, symbolInfo{
						id:        id,
						name:      fn.Name,
						kind:      "function",
						signature: sig,
						filePath:  relPath,
						nodeType:  nt,
					})
				}

				for _, typ := range result.Types {
					nt := uint16(vectorgraphdb.NodeTypeStruct)
					if typ.Kind == "interface" {
						nt = uint16(vectorgraphdb.NodeTypeInterface)
					}
					id := relPath + ":" + typ.Name
					local = append(local, symbolInfo{
						id:        id,
						name:      typ.Name,
						kind:      typ.Kind,
						signature: "",
						filePath:  relPath,
						nodeType:  nt,
					})
				}

				mu.Lock()
				for _, s := range local {
					if !seen[s.id] {
						seen[s.id] = true
						symbols = append(symbols, s)
					}
				}
				mu.Unlock()
			}
		}()
	}

	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		workCh <- struct{ path, content string }{path, string(content)}
	}
	close(workCh)
	wg.Wait()

	return symbols
}

func buildStorageLayer(t *testing.T, tmpDir string, symbols []symbolInfo) (
	*storage.VectorStore,
	*storage.LabelStore,
	[][]float32,
) {
	t.Helper()

	// Use ClusteredMockEmbedder which produces embeddings with semantic locality.
	// Symbols in same directory/kind cluster together, enabling graph locality.
	clustered := embedder.NewClusteredMockEmbedder(768)
	texts := make([]string, len(symbols))
	for i, s := range symbols {
		texts[i] = embedder.SerializeSymbolStandalone(s.name, s.kind, s.signature, s.filePath)
	}

	rawEmbeddings, err := clustered.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	// Make embeddings contiguous for better cache locality during build.
	// All slices point into one flat backing array.
	dim := len(rawEmbeddings[0])
	flat := make([]float32, len(rawEmbeddings)*dim)
	embeddings := make([][]float32, len(rawEmbeddings))
	for i, emb := range rawEmbeddings {
		copy(flat[i*dim:], emb)
		embeddings[i] = flat[i*dim : (i+1)*dim : (i+1)*dim]
	}

	vectorStore, err := storage.CreateVectorStore(filepath.Join(tmpDir, "vectors.bin"), 768, len(symbols))
	if err != nil {
		t.Fatalf("CreateVectorStore failed: %v", err)
	}

	labelStore, err := storage.CreateLabelStore(filepath.Join(tmpDir, "labels.bin"), len(symbols))
	if err != nil {
		vectorStore.Close()
		t.Fatalf("CreateLabelStore failed: %v", err)
	}

	for i, s := range symbols {
		internalID, err := vectorStore.Append(embeddings[i])
		if err != nil {
			vectorStore.Close()
			labelStore.Close()
			t.Fatalf("Append failed: %v", err)
		}

		if err := labelStore.Set(internalID, uint8(vectorgraphdb.DomainCode), s.nodeType); err != nil {
			vectorStore.Close()
			labelStore.Close()
			t.Fatalf("Set label failed: %v", err)
		}
	}

	return vectorStore, labelStore, embeddings
}

func buildWithScaNN(
	t *testing.T,
	embeddings [][]float32,
	vectorStore *storage.VectorStore,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	config vamana.VamanaConfig,
) *BuildResult {
	t.Helper()

	batchConf := DefaultBatchBuildConfig()
	avqConf := DefaultAVQConfig()

	builder := NewBatchBuilder(batchConf, avqConf, config)

	t.Logf("Building index with ScaNN (partitions=%d, R=%d)...", avqConf.NumPartitions, config.R)
	buildStart := time.Now()

	result, timings, err := builder.BuildWithTimings(embeddings, vectorStore, graphStore, magCache)
	if err != nil {
		t.Fatalf("ScaNN Build failed: %v", err)
	}

	buildTime := time.Since(buildStart)
	t.Logf("ScaNN build: %v (%.0f vectors/sec)", buildTime, float64(len(embeddings))/buildTime.Seconds())
	t.Logf("  Timings: KMeans=%v, Codebooks=%v, Magnitudes=%v, GraphInit=%v, Refinement=%v, Medoid=%v",
		timings.KMeans, timings.Codebooks, timings.Magnitudes, timings.GraphInit, timings.Refinement, timings.Medoid)

	return result
}

func runSearchBenchmark(
	t *testing.T,
	embeddings [][]float32,
	vectorStore *storage.VectorStore,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	medoid uint32,
	config vamana.VamanaConfig,
) {
	t.Helper()

	numQueries := 100
	K := 10

	queryIndices := make([]int, numQueries)
	for i := range numQueries {
		queryIndices[i] = rand.IntN(len(embeddings))
	}

	var bruteForceLatency time.Duration
	for _, queryIdx := range queryIndices {
		start := time.Now()
		_ = bruteForceSimilarity(embeddings[queryIdx], embeddings, K)
		bruteForceLatency += time.Since(start)
	}
	t.Logf("Brute force baseline: %v avg per query", bruteForceLatency/time.Duration(numQueries))

	snapshot := vectorStore.Snapshot()

	difficulty := ProbeGraphDifficulty(embeddings, snapshot, graphStore, magCache, medoid, config.R)
	t.Logf("Graph difficulty: expansion=%.2f, pathLen=%.1f, n=%d", difficulty.AvgExpansion, difficulty.AvgPathLength, difficulty.N)

	targets := []float64{0.90, 0.95, 0.98}
	t.Logf("\n=== Derived L Benchmark ===")

	for _, target := range targets {
		derivedL := DeriveL(config.R, target, difficulty)

		var totalLatency time.Duration
		var totalRecall float64

		for _, queryIdx := range queryIndices {
			queryVec := embeddings[queryIdx]

			start := time.Now()
			results := vamana.GreedySearchFast(queryVec, medoid, derivedL, K, snapshot, graphStore, magCache)
			latency := time.Since(start)
			totalLatency += latency

			bruteForce := bruteForceSimilarity(queryVec, embeddings, K)
			recall := computeRecall(results, bruteForce)
			totalRecall += recall
		}

		avgLatency := totalLatency / time.Duration(numQueries)
		avgRecall := totalRecall / float64(numQueries)

		status := "PASS"
		if avgRecall < target {
			status = "MISS"
		}
		t.Logf("  Target %.0f%%: L=%d (%.1fx R) -> latency=%v, recall=%.2f%% [%s]",
			target*100, derivedL, float64(derivedL)/float64(config.R), avgLatency, avgRecall*100, status)
	}

	t.Logf("\n=== Final Benchmark (target=98%%) ===")
	searchL := DeriveL(config.R, 0.98, difficulty)

	var finalLatency time.Duration
	var finalRecall float64
	for _, queryIdx := range queryIndices {
		queryVec := embeddings[queryIdx]
		start := time.Now()
		results := vamana.GreedySearchFast(queryVec, medoid, searchL, K, snapshot, graphStore, magCache)
		finalLatency += time.Since(start)
		bruteForce := bruteForceSimilarity(queryVec, embeddings, K)
		finalRecall += computeRecall(results, bruteForce)
	}

	avgLatency := finalLatency / time.Duration(numQueries)
	avgRecall := finalRecall / float64(numQueries)

	t.Logf("  Vectors: %d", len(embeddings))
	t.Logf("  Derived L: %d (%.1fx R)", searchL, float64(searchL)/float64(config.R))
	t.Logf("  Average latency: %v", avgLatency)
	t.Logf("  Average recall@%d: %.2f%%", K, avgRecall*100)

	if avgLatency > 10*time.Millisecond {
		t.Errorf("FAIL: Average latency %v exceeds 10ms target", avgLatency)
	} else {
		t.Logf("PASS: Average latency %v within 10ms target", avgLatency)
	}

	if avgRecall < 0.90 {
		t.Errorf("FAIL: Average recall %.2f%% below 90%% target", avgRecall*100)
	} else {
		t.Logf("PASS: Average recall %.2f%% meets 90%% target", avgRecall*100)
	}
}

func bruteForceSimilarity(query []float32, embeddings [][]float32, K int) []uint32 {
	type scored struct {
		id         uint32
		similarity float64
	}

	queryMag := vamana.Magnitude(query)
	results := make([]scored, len(embeddings))

	for i, vec := range embeddings {
		mag := vamana.Magnitude(vec)
		dot := vamana.DotProduct(query, vec)
		similarity := float64(dot) / (queryMag * mag)
		results[i] = scored{id: uint32(i), similarity: similarity}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].similarity > results[j].similarity
	})

	topK := make([]uint32, K)
	for i := 0; i < K && i < len(results); i++ {
		topK[i] = results[i].id
	}
	return topK
}

func computeRecall(results []vamana.SearchResult, groundTruth []uint32) float64 {
	truthSet := make(map[uint32]struct{}, len(groundTruth))
	for _, id := range groundTruth {
		truthSet[id] = struct{}{}
	}

	hits := 0
	for _, r := range results {
		if _, ok := truthSet[uint32(r.InternalID)]; ok {
			hits++
		}
	}

	return float64(hits) / float64(len(groundTruth))
}

func greedySearchWithStats(
	query []float32,
	start uint32,
	L, K int,
	vectors *storage.VectorSnapshot,
	graph *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
) ([]vamana.SearchResult, int) {
	distComps := 0

	if vectors == nil || graph == nil || L <= 0 || K <= 0 {
		return nil, 0
	}

	if K > L {
		K = L
	}

	queryMag := vamana.Magnitude(query)
	if queryMag == 0 {
		return nil, 0
	}

	distCache := make(map[uint32]float64, L*4)

	distFn := func(id uint32) float64 {
		if d, ok := distCache[id]; ok {
			return d
		}
		distComps++
		vec := vectors.Get(id)
		if vec == nil {
			return 2.0
		}
		vecMag := magCache.GetOrCompute(id, vec)
		if vecMag == 0 {
			return 2.0
		}
		dot := vamana.DotProduct(query, vec)
		similarity := float64(dot) / (queryMag * vecMag)
		d := 1.0 - similarity
		distCache[id] = d
		return d
	}

	type candidate struct {
		id   uint32
		dist float64
	}

	visited := make(map[uint32]struct{}, L*2)
	candidates := make([]candidate, 0, L*2)
	results := make([]candidate, 0, L)

	startVec := vectors.Get(start)
	if startVec == nil {
		return nil, 0
	}
	startDist := distFn(start)
	candidates = append(candidates, candidate{id: start, dist: startDist})

	for len(candidates) > 0 {
		minIdx := 0
		for i := 1; i < len(candidates); i++ {
			if candidates[i].dist < candidates[minIdx].dist {
				minIdx = i
			}
		}
		current := candidates[minIdx]
		candidates[minIdx] = candidates[len(candidates)-1]
		candidates = candidates[:len(candidates)-1]

		if _, seen := visited[current.id]; seen {
			continue
		}
		visited[current.id] = struct{}{}

		results = append(results, current)
		if len(results) > L {
			maxIdx := 0
			for i := 1; i < len(results); i++ {
				if results[i].dist > results[maxIdx].dist {
					maxIdx = i
				}
			}
			results[maxIdx] = results[len(results)-1]
			results = results[:len(results)-1]
		}

		var bound float64 = 2.0
		if len(results) > 0 {
			bound = results[0].dist
			for i := 1; i < len(results); i++ {
				if results[i].dist > bound {
					bound = results[i].dist
				}
			}
		}

		neighbors := graph.GetNeighbors(current.id)
		for _, neighborID := range neighbors {
			if _, seen := visited[neighborID]; seen {
				continue
			}

			dist := distFn(neighborID)

			if len(results) < L || dist < bound {
				candidates = append(candidates, candidate{id: neighborID, dist: dist})
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].dist < results[j].dist
	})

	n := len(results)
	if n > K {
		n = K
	}

	output := make([]vamana.SearchResult, n)
	for i := 0; i < n; i++ {
		output[i] = vamana.SearchResult{
			InternalID: vamana.InternalID(results[i].id),
			Similarity: 1.0 - results[i].dist,
		}
	}

	return output, distComps
}
