package main

import (
	"context"
	"flag"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"time"

	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/ivf"
)

const benchDir = "/tmp/ivfbench"

// maxFileSizeOverride is the maximum file size for discovery (10 MB).
const maxFileSizeOverride = 10 * 1024 * 1024


type scored struct {
	id         uint32
	similarity float64
}

type benchConfig struct {
	root       string
	cpuProfile string
	memProfile string
}

func parseFlags() benchConfig {
	cpuProfile := flag.String("cpuprofile", "", "write cpu profile to file")
	memProfile := flag.String("memprofile", "", "write memory profile to file")
	flag.Parse()

	root := "/tmp/kubernetes"
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	return benchConfig{
		root:       root,
		cpuProfile: *cpuProfile,
		memProfile: *memProfile,
	}
}

func main() {
	cfg := parseFlags()

	fmt.Printf("=== Kubernetes Full Ingest Benchmark ===\n")
	fmt.Printf("Root: %s\n\n", cfg.root)

	setupProfiles(&cfg)
	defer teardownProfiles(&cfg)

	totalStart := time.Now()

	os.RemoveAll(benchDir)
	if err := os.MkdirAll(benchDir, 0755); err != nil {
		fatal("create bench dir: %v", err)
	}

	ctx := context.Background()

	// Phase 1: Session setup
	sd, sess, emb := setupSession(ctx)
	defer sd.Close()
	defer sess.Close()

	dim := emb.Dimension()

	// Phase 2: Production ingestion
	ingestResult := runIngestion(ctx, sess, emb, cfg.root)

	// Phase 3: Extract vectors
	embeddings := extractVectors(sess)

	// Phase 4: Build IVF index
	idx, buildTime := buildIVF(embeddings, dim)

	// Phase 5: Recall benchmark
	K, numQueries, stride := deriveSearchParams(len(embeddings))
	vamanaRecall, ivfRecall := runRecallBenchmark(embeddings, idx, K, numQueries, stride)

	// Phase 6-7: Save/load/validate
	indexDir := filepath.Join(benchDir, "index")
	saveTime, loadTime, loadedRecall := runPersistenceTest(embeddings, idx, indexDir, K, numQueries, stride)

	// Phase 8: Streaming insert
	insertRecall := runInsertBenchmark(embeddings, dim, K, numQueries, stride)

	// Phase 9: StitchBatch
	runStitchBenchmark(embeddings, dim, K, numQueries, stride)

	// Phase 10: Maintenance
	runMaintenance(embeddings, dim, K, numQueries, stride)

	// Summary
	totalTime := time.Since(totalStart)
	printSummary(ingestResult, embeddings, dim, buildTime, saveTime, loadTime,
		vamanaRecall, ivfRecall, loadedRecall, insertRecall, K, totalTime)

	writeMemProfile(cfg.memProfile)
}

// setupSession initializes the sylkdir, session, and embedder.
func setupSession(ctx context.Context) (*sylkdir.SylkDir, *sylkdir.Session, embedder.Embedder) {
	fmt.Print("Phase 1: Setting up session infrastructure... ")
	start := time.Now()

	sd := sylkdir.New(benchDir)
	if err := sd.Init(); err != nil {
		fatal("init sylkdir: %v", err)
	}
	if err := sd.Lock(); err != nil {
		fatal("lock sylkdir: %v", err)
	}

	ss := sylkdir.NewSessionStore(sd)
	sess, err := ss.Create(1, nil)
	if err != nil {
		fatal("create session: %v", err)
	}

	result, err := embedder.NewEmbedder(ctx, embedder.FactoryConfig{})
	if err != nil {
		fatal("create embedder: %v", err)
	}

	fmt.Printf("done in %v (embedder: %s, dim=%d)\n",
		time.Since(start), result.Source, result.Embedder.Dimension())

	return sd, sess, result.Embedder
}

// runIngestion uses the production pipeline to ingest all files.
func runIngestion(ctx context.Context, sess *sylkdir.Session, emb embedder.Embedder, root string) *sylkdir.SessionIngestionResult {
	fmt.Print("Phase 2: Ingesting via production pipeline... ")
	start := time.Now()

	si := sylkdir.NewSessionIngestion(sess)
	si.SetEmbedder(emb)

	config := &ingestion.Config{
		RootPath: root,
		Discovery: &ingestion.DiscoveryOptions{
			IncludeAllExtensions: true,
			SkipDefaultPatterns:  true,
			IncludeVendorDirs:    true,
			MaxFileSizeOverride:  maxFileSizeOverride,
		},
	}

	result, err := si.IngestDirectoryWithConfig(ctx, config)
	if err != nil {
		fatal("ingest: %v", err)
	}

	fmt.Printf("done in %v\n", time.Since(start))
	fmt.Printf("  Files: %d, Nodes: %d, Edges: %d, Vectors: %d, Docs: %d\n",
		result.FilesProcessed, result.NodesCreated, result.EdgesCreated,
		result.VectorsCreated, result.DocsCreated)
	if result.EmbeddingErrors > 0 {
		fmt.Printf("  Embedding errors: %d\n", result.EmbeddingErrors)
	}

	return result
}

// extractVectors reads all vectors from the production version store.
func extractVectors(sess *sylkdir.Session) [][]float32 {
	fmt.Print("Phase 3: Extracting vectors from version store... ")
	start := time.Now()

	vectorStore := sylkdir.NewVersionVectorStore(sess)
	versionVectors, err := vectorStore.ReadAllFromAncestorChain()
	if err != nil {
		fatal("read vectors: %v", err)
	}

	embeddings := make([][]float32, len(versionVectors))
	for i, vv := range versionVectors {
		embeddings[i] = vv.Vector
	}

	fmt.Printf("%d vectors in %v\n", len(embeddings), time.Since(start))
	return embeddings
}

// buildIVF builds an IVF index from the extracted vectors.
func buildIVF(embeddings [][]float32, dim int) (*ivf.Index, time.Duration) {
	config := ivf.ConfigForN(len(embeddings), dim)
	fmt.Printf("\nIVF Config: partitions=%d, nprobe=%d, oversample=%d, dim=%d\n",
		config.NumPartitions, config.NProbe, config.Oversample, dim)

	fmt.Print("\nPhase 4: Building IVF index... ")
	start := time.Now()
	idx := ivf.NewIndex(config, dim)
	_, stats := idx.Build(embeddings)
	buildTime := time.Since(start)

	fmt.Printf("done in %v (%.0f vec/sec)\n", buildTime, float64(len(embeddings))/buildTime.Seconds())
	fmt.Printf("  Store: %dms, BBQ: %dms, Partition: %dms, Graph: %dms\n",
		stats.StoreNanos/1e6, stats.BBQNanos/1e6, stats.PartitionNanos/1e6, stats.GraphNanos/1e6)

	gs := idx.GraphStats()
	fmt.Printf("  Graph: nodes=%d, edges=%d, avgDeg=%.1f, minDeg=%d, maxDeg=%d\n",
		gs.NumNodes, gs.NumEdges, gs.AvgDegree, gs.MinDegree, gs.MaxDegree)

	return idx, buildTime
}

// deriveSearchParams derives K, numQueries, and stride from the dataset size.
func deriveSearchParams(n int) (K, numQueries, stride int) {
	logN := bits.Len(uint(n))
	K = logN
	numQueries = logN * logN
	stride = n / numQueries
	return K, numQueries, stride
}

// runRecallBenchmark measures recall for Vamana and IVF search.
func runRecallBenchmark(embeddings [][]float32, idx *ivf.Index, K, numQueries, stride int) (vamanaRecall, ivfRecall float64) {
	n := len(embeddings)
	fmt.Printf("\nPhase 5: Search benchmark (n=%d, queries=%d, K=%d, stride=%d)...\n",
		n, numQueries, K, stride)

	var vamanaTotalLatency, ivfTotalLatency time.Duration
	var vamanaTotalRecall, ivfTotalRecall float64

	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]
		groundTruth := bruteForceTopK(queryVec, embeddings, K)

		start := time.Now()
		results := idx.SearchVamana(queryVec, K)
		vamanaTotalLatency += time.Since(start)
		vamanaTotalRecall += computeRecall(results, groundTruth)

		start = time.Now()
		ivfResults := idx.SearchIVF(queryVec, K)
		ivfTotalLatency += time.Since(start)
		ivfTotalRecall += computeRecall(ivfResults, groundTruth)
	}

	vamanaAvg := vamanaTotalLatency / time.Duration(numQueries)
	vamanaRecall = vamanaTotalRecall / float64(numQueries)
	fmt.Printf("  [Vamana] Avg latency: %v, recall@%d: %.2f%%\n", vamanaAvg, K, vamanaRecall*100)

	ivfAvg := ivfTotalLatency / time.Duration(numQueries)
	ivfRecall = ivfTotalRecall / float64(numQueries)
	fmt.Printf("  [IVF]    Avg latency: %v, recall@%d: %.2f%%\n", ivfAvg, K, ivfRecall*100)

	return vamanaRecall, ivfRecall
}

// runPersistenceTest saves, loads, and validates the index.
func runPersistenceTest(embeddings [][]float32, idx *ivf.Index, indexDir string, K, numQueries, stride int) (saveTime, loadTime time.Duration, loadedRecall float64) {
	n := len(embeddings)

	fmt.Print("\nPhase 6: Saving index to disk... ")
	saveStart := time.Now()
	if err := idx.Save(indexDir); err != nil {
		fatal("save index: %v", err)
	}
	saveTime = time.Since(saveStart)
	fmt.Printf("done in %v\n", saveTime)

	fmt.Print("Phase 7: Loading index from disk... ")
	loadStart := time.Now()
	loadedIdx, err := ivf.LoadIndexInMemory(indexDir)
	if err != nil {
		fatal("load index: %v", err)
	}
	loadTime = time.Since(loadStart)
	fmt.Printf("done in %v (%d vectors)\n", loadTime, loadedIdx.NumVectors())

	fmt.Printf("Phase 7b: Validating loaded index search (queries=%d, K=%d)...\n", numQueries, K)
	var totalRecall float64
	var totalLatency time.Duration

	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]
		groundTruth := bruteForceTopK(queryVec, embeddings, K)

		start := time.Now()
		results := loadedIdx.SearchIVF(queryVec, K)
		totalLatency += time.Since(start)
		totalRecall += computeRecall(results, groundTruth)
	}

	loadedRecall = totalRecall / float64(numQueries)
	avgLatency := totalLatency / time.Duration(numQueries)
	fmt.Printf("  [Loaded] Avg latency: %v, recall@%d: %.2f%%\n", avgLatency, K, loadedRecall*100)

	if loadedRecall < 0.85 {
		fmt.Fprintf(os.Stderr, "ERROR: Loaded index recall too low: %.2f%% (expected >85%%)\n",
			loadedRecall*100)
		os.Exit(1)
	}

	return saveTime, loadTime, loadedRecall
}

// runInsertBenchmark tests streaming insert and recall after insertion.
func runInsertBenchmark(embeddings [][]float32, dim, K, numQueries, stride int) float64 {
	n := len(embeddings)
	splitPoint := int(float64(n) * 0.99)
	buildEmbeddings := embeddings[:splitPoint]
	insertEmbeddings := embeddings[splitPoint:]

	fmt.Printf("\n=== Phase 8: Streaming Insert Benchmark ===\n")
	fmt.Printf("Building index with %d vectors (99%%)...\n", len(buildEmbeddings))
	insertTestIdx := ivf.NewIndex(ivf.ConfigForN(len(buildEmbeddings), dim), dim)
	insertTestIdx.Build(buildEmbeddings)

	fmt.Printf("Streaming insert of %d vectors (1%%)...\n", len(insertEmbeddings))
	insertStart := time.Now()
	var insertLatencies []time.Duration
	for _, vec := range insertEmbeddings {
		opStart := time.Now()
		_, err := insertTestIdx.Insert(vec)
		if err != nil {
			fatal("insert: %v", err)
		}
		insertLatencies = append(insertLatencies, time.Since(opStart))
	}
	totalInsertTime := time.Since(insertStart)

	var totalLatency time.Duration
	for _, lat := range insertLatencies {
		totalLatency += lat
	}
	avgInsertLatency := totalLatency / time.Duration(len(insertLatencies))

	fmt.Printf("  Inserted %d vectors in %v\n", len(insertEmbeddings), totalInsertTime)
	fmt.Printf("  Avg insert latency: %v\n", avgInsertLatency)
	fmt.Printf("  Insert throughput: %.0f vec/sec\n", float64(len(insertEmbeddings))/totalInsertTime.Seconds())

	fmt.Print("Phase 8b: Testing recall after inserts... ")
	var postInsertRecall float64
	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]
		groundTruth := bruteForceTopK(queryVec, embeddings, K)
		results := insertTestIdx.SearchVamana(queryVec, K)
		postInsertRecall += computeRecall(results, groundTruth)
	}
	postInsertAvgRecall := postInsertRecall / float64(numQueries)
	fmt.Printf("recall@%d: %.2f%%\n", K, postInsertAvgRecall*100)

	return postInsertAvgRecall
}

// runStitchBenchmark tests StitchBatch and recall after stitching.
func runStitchBenchmark(embeddings [][]float32, dim, K, numQueries, stride int) {
	n := len(embeddings)
	splitPoint := int(float64(n) * 0.99)
	buildEmbeddings := embeddings[:splitPoint]
	insertEmbeddings := embeddings[splitPoint:]

	fmt.Printf("\n=== Phase 9: StitchBatch Benchmark ===\n")
	fmt.Printf("Building fresh index with %d vectors (99%%)...\n", len(buildEmbeddings))
	stitchTestIdx := ivf.NewIndex(ivf.ConfigForN(len(buildEmbeddings), dim), dim)
	stitchTestIdx.Build(buildEmbeddings)

	fmt.Printf("Stitching %d vectors (1%%)...\n", len(insertEmbeddings))
	stitchStart := time.Now()
	stitchResult, err := stitchTestIdx.StitchBatch(insertEmbeddings)
	if err != nil {
		fatal("stitch: %v", err)
	}
	stitchTime := time.Since(stitchStart)

	fmt.Printf("  Stitched %d vectors in %v\n", stitchResult.VectorsAdded, stitchTime)
	fmt.Printf("  Boundary nodes: %d, Edges added: %d\n", stitchResult.BoundaryNodes, stitchResult.EdgesAdded)
	fmt.Printf("  Stitch throughput: %.0f vec/sec\n", float64(len(insertEmbeddings))/stitchTime.Seconds())

	fmt.Print("Phase 9b: Testing recall after stitch... ")
	var postStitchRecall float64
	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]
		groundTruth := bruteForceTopK(queryVec, embeddings, K)
		results := stitchTestIdx.SearchVamana(queryVec, K)
		postStitchRecall += computeRecall(results, groundTruth)
	}
	postStitchAvgRecall := postStitchRecall / float64(numQueries)
	fmt.Printf("recall@%d: %.2f%%\n", K, postStitchAvgRecall*100)
}

// runMaintenance tests centroid refresh, graph optimization, and recall impact.
func runMaintenance(embeddings [][]float32, dim, K, numQueries, stride int) {
	n := len(embeddings)
	splitPoint := int(float64(n) * 0.99)
	buildEmbeddings := embeddings[:splitPoint]
	insertEmbeddings := embeddings[splitPoint:]

	// Build index with inserts for maintenance testing
	maintIdx := ivf.NewIndex(ivf.ConfigForN(len(buildEmbeddings), dim), dim)
	maintIdx.Build(buildEmbeddings)
	for _, vec := range insertEmbeddings {
		maintIdx.Insert(vec)
	}

	fmt.Print("\nPhase 10: Maintenance Stats... ")
	mstats := maintIdx.MaintenanceStats()
	fmt.Printf("\n  Vectors: %d, Partitions: %d\n", mstats.NumVectors, mstats.NumPartitions)
	fmt.Printf("  Partition balance: gini=%.4f, cv=%.4f\n", mstats.PartitionSizeGini, mstats.PartitionSizeCV)
	fmt.Printf("  Drift ratio: %.4f (1.0 = baseline)\n", mstats.DriftRatio)
	fmt.Printf("  Graph health: connectivity=%.4f, fill_rate=%.4f\n", mstats.ConnectivityRatio, mstats.DegreeFillRate)

	fmt.Print("\nPhase 10b: Refreshing centroids... ")
	refreshStart := time.Now()
	refreshResult, err := maintIdx.RefreshCentroids(3)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("done in %v (%d vectors reassigned)\n", time.Since(refreshStart), refreshResult.VectorsReassigned)

	fmt.Print("Phase 10c: Testing recall after centroid refresh... ")
	var postRefreshRecall float64
	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]
		groundTruth := bruteForceTopK(queryVec, embeddings, K)
		results := maintIdx.SearchVamana(queryVec, K)
		postRefreshRecall += computeRecall(results, groundTruth)
	}
	postRefreshAvgRecall := postRefreshRecall / float64(numQueries)
	fmt.Printf("recall@%d: %.2f%%\n", K, postRefreshAvgRecall*100)

	fmt.Print("\nPhase 10d: Optimizing graph... ")
	optimizeStart := time.Now()
	optimizeResult := maintIdx.OptimizeGraph(0)
	fmt.Printf("done in %v (nodes=%d, +%d/-%d edges)\n",
		time.Since(optimizeStart), optimizeResult.NodesUpdated, optimizeResult.EdgesAdded, optimizeResult.EdgesRemoved)

	fmt.Print("Phase 10e: Testing recall after graph optimization... ")
	var postOptimizeRecall float64
	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]
		groundTruth := bruteForceTopK(queryVec, embeddings, K)
		results := maintIdx.SearchVamana(queryVec, K)
		postOptimizeRecall += computeRecall(results, groundTruth)
	}
	postOptimizeAvgRecall := postOptimizeRecall / float64(numQueries)
	fmt.Printf("recall@%d: %.2f%%\n", K, postOptimizeAvgRecall*100)
}

// printSummary outputs the final benchmark summary.
func printSummary(
	ingest *sylkdir.SessionIngestionResult,
	embeddings [][]float32, dim int,
	buildTime, saveTime, loadTime time.Duration,
	vamanaRecall, ivfRecall, loadedRecall, insertRecall float64,
	K int, totalTime time.Duration,
) {
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Files processed:  %d\n", ingest.FilesProcessed)
	fmt.Printf("Nodes created:    %d\n", ingest.NodesCreated)
	fmt.Printf("Vectors created:  %d\n", ingest.VectorsCreated)
	fmt.Printf("Dimension:        %d\n", dim)
	fmt.Printf("Total time:       %v\n", totalTime)
	fmt.Printf("Build time:       %v (%.0f vec/sec)\n", buildTime, float64(len(embeddings))/buildTime.Seconds())
	fmt.Printf("Persistence:      save=%v, load=%v\n", saveTime, loadTime)
	fmt.Printf("Recall@%d:\n", K)
	fmt.Printf("  Vamana:         %.2f%%\n", vamanaRecall*100)
	fmt.Printf("  IVF:            %.2f%%\n", ivfRecall*100)
	fmt.Printf("  Loaded:         %.2f%%\n", loadedRecall*100)
	fmt.Printf("  Post-insert:    %.2f%%\n", insertRecall*100)
}

// =============================================================================
// Utilities
// =============================================================================

func bruteForceTopK(query []float32, embeddings [][]float32, K int) []uint32 {
	results := bruteForceWithSims(query, embeddings, K)
	topK := make([]uint32, len(results))
	for i := range topK {
		topK[i] = results[i].id
	}
	return topK
}

func bruteForceWithSims(query []float32, embeddings [][]float32, K int) []scored {
	queryMag := vamana.Magnitude(query)
	results := make([]scored, len(embeddings))

	for i, vec := range embeddings {
		mag := vamana.Magnitude(vec)
		dot := vamana.DotProduct(query, vec)
		results[i] = scored{id: uint32(i), similarity: float64(dot) / (queryMag * mag)}
	}

	slices.SortFunc(results, func(a, b scored) int {
		if a.similarity > b.similarity {
			return -1
		}
		if a.similarity < b.similarity {
			return 1
		}
		if a.id < b.id {
			return -1
		}
		if a.id > b.id {
			return 1
		}
		return 0
	})

	return results[:min(K, len(results))]
}

func computeRecall(results []ivf.SearchResult, groundTruth []uint32) float64 {
	if len(results) == 0 || len(groundTruth) == 0 {
		return 0
	}

	truthSet := make(map[uint32]struct{}, len(groundTruth))
	for _, id := range groundTruth {
		truthSet[id] = struct{}{}
	}

	hits := 0
	for _, r := range results {
		if _, ok := truthSet[r.ID]; ok {
			hits++
		}
	}

	return float64(hits) / float64(len(groundTruth))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}

func setupProfiles(cfg *benchConfig) {
	if cfg.cpuProfile == "" {
		return
	}
	f, err := os.Create(cfg.cpuProfile)
	if err != nil {
		fatal("create CPU profile: %v", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		fatal("start CPU profile: %v", err)
	}
}

func teardownProfiles(cfg *benchConfig) {
	if cfg.cpuProfile != "" {
		pprof.StopCPUProfile()
	}
}

func writeMemProfile(path string) {
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		fatal("create memory profile: %v", err)
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		fatal("write memory profile: %v", err)
	}
}

