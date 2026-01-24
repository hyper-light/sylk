package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/ivf"
	_ "github.com/mattn/go-sqlite3"
)

type scored struct {
	id         uint32
	similarity float64
}

type symbol struct {
	id        string
	name      string
	kind      string
	signature string
	filePath  string
	nodeType  uint16
}

const benchDir = "/tmp/ivfbench"

func initSQLite(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS symbols (
			id INTEGER PRIMARY KEY,
			symbol_id TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			signature TEXT,
			file_path TEXT NOT NULL,
			node_type INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS embeddings (
			id INTEGER PRIMARY KEY,
			embedding BLOB NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_path);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func storeSymbolsAndEmbeddings(db *sql.DB, symbols []symbol, embeddings [][]float32) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	symbolStmt, err := tx.Prepare("INSERT INTO symbols (id, symbol_id, name, kind, signature, file_path, node_type) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer symbolStmt.Close()

	embedStmt, err := tx.Prepare("INSERT INTO embeddings (id, embedding) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer embedStmt.Close()

	for i, s := range symbols {
		if _, err := symbolStmt.Exec(i, s.id, s.name, s.kind, s.signature, s.filePath, s.nodeType); err != nil {
			return err
		}

		embBytes := make([]byte, len(embeddings[i])*4)
		for j, v := range embeddings[i] {
			binary.LittleEndian.PutUint32(embBytes[j*4:], math.Float32bits(v))
		}
		if _, err := embedStmt.Exec(i, embBytes); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func loadEmbeddingFromDB(db *sql.DB, id int) ([]float32, error) {
	var embBytes []byte
	err := db.QueryRow("SELECT embedding FROM embeddings WHERE id = ?", id).Scan(&embBytes)
	if err != nil {
		return nil, err
	}

	dim := len(embBytes) / 4
	vec := make([]float32, dim)
	for i := range dim {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(embBytes[i*4:]))
	}
	return vec, nil
}

func computeRecallUint32(results []uint32, groundTruth []uint32) float64 {
	if len(results) == 0 || len(groundTruth) == 0 {
		return 0
	}

	truthSet := make(map[uint32]struct{}, len(groundTruth))
	for _, id := range groundTruth {
		truthSet[id] = struct{}{}
	}

	hits := 0
	for _, id := range results {
		if _, ok := truthSet[id]; ok {
			hits++
		}
	}

	return float64(hits) / float64(len(groundTruth))
}

func corruptBBQShard(indexDir string, shardIdx int) {
	shardPath := filepath.Join(indexDir, "bbq", fmt.Sprintf("shard_%04d.bin", shardIdx))
	data, err := os.ReadFile(shardPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read bbq shard: %v\n", err)
		return
	}

	if len(data) > 100 {
		for i := 50; i < 100; i++ {
			data[i] ^= 0xFF
		}
	}
	if err := os.WriteFile(shardPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write corrupted bbq shard: %v\n", err)
	}
}

func corruptNormShard(indexDir string, shardIdx int) {
	shardPath := filepath.Join(indexDir, "norms", fmt.Sprintf("shard_%04d.bin", shardIdx))
	data, err := os.ReadFile(shardPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read norm shard: %v\n", err)
		return
	}

	if len(data) > 100 {
		for i := 50; i < 100; i++ {
			data[i] ^= 0xFF
		}
	}
	if err := os.WriteFile(shardPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write corrupted norm shard: %v\n", err)
	}
}

func main() {
	cpuProfile := flag.String("cpuprofile", "", "write cpu profile to file")
	memProfile := flag.String("memprofile", "", "write memory profile to file")
	flag.Parse()

	root := "/tmp/kubernetes"
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	fmt.Printf("=== Kubernetes Full Ingest Benchmark ===\n")
	fmt.Printf("Root: %s\n\n", root)

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not create CPU profile: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "could not start CPU profile: %v\n", err)
			os.Exit(1)
		}
		defer pprof.StopCPUProfile()
	}

	totalStart := time.Now()

	os.RemoveAll(benchDir)
	if err := os.MkdirAll(benchDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create bench dir: %v\n", err)
		os.Exit(1)
	}

	fmt.Print("Phase 1: Scanning files... ")
	scanStart := time.Now()
	var files []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".go" {
			files = append(files, path)
		}
		return nil
	})
	fmt.Printf("%d files in %v\n", len(files), time.Since(scanStart))

	fmt.Print("Phase 2: Parsing symbols... ")
	parseStart := time.Now()
	symbols := parseAllSymbols(root, files)
	fmt.Printf("%d symbols in %v (%.0f files/sec)\n",
		len(symbols), time.Since(parseStart),
		float64(len(files))/time.Since(parseStart).Seconds())

	fmt.Print("Phase 3: Generating embeddings... ")
	embedStart := time.Now()
	dim := embedder.DefaultConfig().Dimension
	emb := embedder.NewClusteredMockEmbedder(dim)
	embeddings := generateEmbeddings(emb, symbols)
	fmt.Printf("%d embeddings in %v (%.0f emb/sec)\n",
		len(embeddings), time.Since(embedStart),
		float64(len(embeddings))/time.Since(embedStart).Seconds())

	fmt.Print("Phase 3b: Storing in SQLite... ")
	sqliteStart := time.Now()
	dbPath := filepath.Join(benchDir, "knowledge.db")
	db, err := initSQLite(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init sqlite: %v\n", err)
		os.Exit(1)
	}
	if err := storeSymbolsAndEmbeddings(db, symbols, embeddings); err != nil {
		fmt.Fprintf(os.Stderr, "failed to store data: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("done in %v\n", time.Since(sqliteStart))

	config := ivf.ConfigForN(len(embeddings), dim)
	fmt.Printf("\nIVF Config: partitions=%d, nprobe=%d, oversample=%d, dim=%d\n",
		config.NumPartitions, config.NProbe, config.Oversample, dim)

	fmt.Print("\nPhase 4: Building IVF index... ")
	buildStart := time.Now()
	idx := ivf.NewIndex(config, dim)
	_, stats := idx.Build(embeddings)
	buildTime := time.Since(buildStart)

	fmt.Printf("done in %v (%.0f vec/sec)\n", buildTime, float64(len(embeddings))/buildTime.Seconds())
	fmt.Printf("  Store: %dms, BBQ: %dms, Partition: %dms, Graph: %dms\n",
		stats.StoreNanos/1e6, stats.BBQNanos/1e6, stats.PartitionNanos/1e6, stats.GraphNanos/1e6)

	gs := idx.GraphStats()
	fmt.Printf("  Graph: nodes=%d, edges=%d, avgDeg=%.1f, minDeg=%d, maxDeg=%d\n",
		gs.NumNodes, gs.NumEdges, gs.AvgDegree, gs.MinDegree, gs.MaxDegree)

	n := len(embeddings)
	logN := bits.Len(uint(n))

	// K: search depth = log2(N), one result per level of binary search tree
	K := logN

	// numQueries: for recall standard error < 2%, need variance < 0.0004
	// With recall ~0.9, variance = 0.09/numQueries, so numQueries > 225
	// log2(N)^2 gives 361 for N=309K, achieving SE ~1.6%
	numQueries := logN * logN

	// stride: evenly distribute queries across dataset
	stride := n / numQueries

	fmt.Printf("\nPhase 5: Search benchmark (n=%d, queries=%d, K=%d, stride=%d)...\n",
		n, numQueries, K, stride)

	var vamanaTotalLatency time.Duration
	var vamanaTotalRecall float64
	var ivfTotalLatency time.Duration
	var ivfTotalRecall float64

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
	vamanaAvgLatency := vamanaTotalLatency / time.Duration(numQueries)
	vamanaAvgRecall := vamanaTotalRecall / float64(numQueries)
	fmt.Printf("  [Vamana] Avg latency: %v, recall@%d: %.2f%%\n", vamanaAvgLatency, K, vamanaAvgRecall*100)

	ivfAvgLatency := ivfTotalLatency / time.Duration(numQueries)
	ivfAvgRecall := ivfTotalRecall / float64(numQueries)
	fmt.Printf("  [IVF]    Avg latency: %v, recall@%d: %.2f%%\n", ivfAvgLatency, K, ivfAvgRecall*100)

	indexDir := filepath.Join(benchDir, "index")

	fmt.Print("\nPhase 6: Saving index to disk... ")
	saveStart := time.Now()
	if err := idx.Save(indexDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to save index: %v\n", err)
		os.Exit(1)
	}
	saveTime := time.Since(saveStart)
	fmt.Printf("done in %v\n", saveTime)

	fmt.Print("Phase 7: Loading index from disk... ")
	loadStart := time.Now()
	loadedIdx, err := ivf.LoadIndexInMemory(indexDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load index: %v\n", err)
		os.Exit(1)
	}
	loadTime := time.Since(loadStart)
	fmt.Printf("done in %v (%d vectors)\n", loadTime, loadedIdx.NumVectors())

	fmt.Printf("Phase 7b: Validating loaded index search (queries=%d, K=%d)...\n", numQueries, K)

	var loadedTotalRecall float64
	var loadedTotalLatency time.Duration

	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]

		groundTruth := bruteForceTopK(queryVec, embeddings, K)

		start := time.Now()
		loadedResults := loadedIdx.SearchIVF(queryVec, K)
		loadedTotalLatency += time.Since(start)
		loadedTotalRecall += computeRecall(loadedResults, groundTruth)
	}

	loadedAvgLatency := loadedTotalLatency / time.Duration(numQueries)
	loadedAvgRecall := loadedTotalRecall / float64(numQueries)
	fmt.Printf("  [Loaded] Avg latency: %v, recall@%d: %.2f%%\n", loadedAvgLatency, K, loadedAvgRecall*100)

	if loadedAvgRecall < 0.85 {
		fmt.Fprintf(os.Stderr, "ERROR: Loaded index recall too low: %.2f%% (expected >85%%)\n",
			loadedAvgRecall*100)
		os.Exit(1)
	}

	fmt.Print("\nPhase 8: Corruption test... ")
	corruptBBQShard(indexDir, 0)
	corruptNormShard(indexDir, 1)
	fmt.Println("corrupted BBQ shard 0 and Norm shard 1")

	fmt.Print("Phase 8b: Running consistency check... ")
	checker := ivf.NewConsistencyChecker(indexDir)
	report, err := checker.Check()
	if err != nil {
		fmt.Fprintf(os.Stderr, "consistency check failed: %v\n", err)
		os.Exit(1)
	}

	if report.IsHealthy() {
		fmt.Fprintf(os.Stderr, "ERROR: Corrupted index reported as healthy!\n")
		os.Exit(1)
	}

	fmt.Printf("detected %d corrupt shards\n", len(report.CorruptShards))
	for _, cs := range report.CorruptShards {
		fmt.Printf("  - %s shard %d: %s\n", cs.StoreType, cs.ShardIdx, cs.Reason)
	}

	fmt.Print("\nPhase 9: Repairing corrupted shards... ")
	repairStart := time.Now()
	bbqEncoder := idx.BBQEncoder()
	repairCfg := ivf.RepairConfig{
		BBQEncoder: bbqEncoder,
		Dim:        dim,
	}
	if err := ivf.RepairFromSQLite(indexDir, report, nil, repairCfg); err != nil {
		fmt.Fprintf(os.Stderr, "repair failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("done in %v\n", time.Since(repairStart))

	fmt.Print("Phase 9b: Verifying repair with consistency check... ")
	report, err = checker.Check()
	if err != nil {
		fmt.Fprintf(os.Stderr, "post-repair check failed: %v\n", err)
		os.Exit(1)
	}

	if !report.IsHealthy() {
		fmt.Fprintf(os.Stderr, "ERROR: Index still unhealthy after repair!\n")
		os.Exit(1)
	}
	fmt.Println("healthy")

	fmt.Print("Phase 9c: Validating repaired index search... ")
	repairedIdx, err := ivf.LoadIndexInMemory(indexDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load repaired index: %v\n", err)
		os.Exit(1)
	}

	var repairedTotalRecall float64
	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]
		groundTruth := bruteForceTopK(queryVec, embeddings, K)
		repairedResults := repairedIdx.SearchIVF(queryVec, K)
		repairedTotalRecall += computeRecall(repairedResults, groundTruth)
	}

	repairedAvgRecall := repairedTotalRecall / float64(numQueries)
	fmt.Printf("recall@%d: %.2f%%\n", K, repairedAvgRecall*100)

	if math.Abs(repairedAvgRecall-loadedAvgRecall) > 0.01 {
		fmt.Fprintf(os.Stderr, "ERROR: Repaired index recall differs from pre-corruption loaded recall: %.2f%% vs %.2f%%\n",
			repairedAvgRecall*100, loadedAvgRecall*100)
		os.Exit(1)
	}

	db.Close()

	fmt.Printf("\n=== Phase 10: Streaming Insert Benchmark ===\n")

	splitPoint := int(float64(len(embeddings)) * 0.99)
	buildEmbeddings := embeddings[:splitPoint]
	insertEmbeddings := embeddings[splitPoint:]

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
			fmt.Fprintf(os.Stderr, "insert failed: %v\n", err)
			os.Exit(1)
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

	fmt.Print("Phase 10b: Testing recall after inserts... ")
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

	fmt.Printf("\n=== Phase 10c: StitchBatch Benchmark ===\n")
	fmt.Printf("Building fresh index with %d vectors (99%%)...\n", len(buildEmbeddings))
	stitchTestIdx := ivf.NewIndex(ivf.ConfigForN(len(buildEmbeddings), dim), dim)
	stitchTestIdx.Build(buildEmbeddings)

	fmt.Printf("Stitching %d vectors (1%%)...\n", len(insertEmbeddings))
	stitchStart := time.Now()
	stitchResult, err := stitchTestIdx.StitchBatch(insertEmbeddings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stitch failed: %v\n", err)
		os.Exit(1)
	}
	stitchTime := time.Since(stitchStart)

	fmt.Printf("  Stitched %d vectors in %v\n", stitchResult.VectorsAdded, stitchTime)
	fmt.Printf("  Boundary nodes: %d, Edges added: %d\n", stitchResult.BoundaryNodes, stitchResult.EdgesAdded)
	fmt.Printf("  Searches performed: %d (vs %d for InsertBatch)\n", stitchResult.SearchesPerformed, len(insertEmbeddings))
	fmt.Printf("  Stitch throughput: %.0f vec/sec\n", float64(len(insertEmbeddings))/stitchTime.Seconds())

	fmt.Print("Phase 10d: Testing recall after stitch... ")
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

	fmt.Printf("\n  Comparison: InsertBatch=%v (%.0f vec/sec), StitchBatch=%v (%.0f vec/sec)\n",
		totalInsertTime, float64(len(insertEmbeddings))/totalInsertTime.Seconds(),
		stitchTime, float64(len(insertEmbeddings))/stitchTime.Seconds())
	fmt.Printf("  Speedup: %.2fx\n", totalInsertTime.Seconds()/stitchTime.Seconds())

	fmt.Print("\nPhase 11: Maintenance Stats... ")
	mstats := insertTestIdx.MaintenanceStats()
	fmt.Printf("\n  Vectors: %d, Partitions: %d\n", mstats.NumVectors, mstats.NumPartitions)
	fmt.Printf("  Partition balance: gini=%.4f, cv=%.4f\n", mstats.PartitionSizeGini, mstats.PartitionSizeCV)
	fmt.Printf("  Drift ratio: %.4f (1.0 = baseline)\n", mstats.DriftRatio)
	fmt.Printf("  Graph health: connectivity=%.4f, fill_rate=%.4f\n", mstats.ConnectivityRatio, mstats.DegreeFillRate)

	fmt.Print("\nPhase 11b: Refreshing centroids... ")
	refreshStart := time.Now()
	refreshResult, err := insertTestIdx.RefreshCentroids(3)
	if err != nil {
		fmt.Printf("error: %v\n", err)
	} else {
		fmt.Printf("done in %v (%d vectors reassigned)\n", time.Since(refreshStart), refreshResult.VectorsReassigned)

		fmt.Print("Phase 11c: Testing recall after centroid refresh... ")
		var postRefreshRecall float64
		for i := range numQueries {
			queryIdx := (i * stride) % n
			queryVec := embeddings[queryIdx]
			groundTruth := bruteForceTopK(queryVec, embeddings, K)
			results := insertTestIdx.SearchVamana(queryVec, K)
			postRefreshRecall += computeRecall(results, groundTruth)
		}
		postRefreshAvgRecall := postRefreshRecall / float64(numQueries)
		fmt.Printf("recall@%d: %.2f%%\n", K, postRefreshAvgRecall*100)
	}

	fmt.Print("\nPhase 11d: Optimizing graph... ")
	optimizeStart := time.Now()
	optimizeResult := insertTestIdx.OptimizeGraph(0)
	fmt.Printf("done in %v (nodes=%d, +%d/-%d edges)\n",
		time.Since(optimizeStart), optimizeResult.NodesUpdated, optimizeResult.EdgesAdded, optimizeResult.EdgesRemoved)

	fmt.Print("Phase 11e: Testing recall after graph optimization... ")
	var postOptimizeRecall float64
	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]
		groundTruth := bruteForceTopK(queryVec, embeddings, K)
		results := insertTestIdx.SearchVamana(queryVec, K)
		postOptimizeRecall += computeRecall(results, groundTruth)
	}
	postOptimizeAvgRecall := postOptimizeRecall / float64(numQueries)
	fmt.Printf("recall@%d: %.2f%%\n", K, postOptimizeAvgRecall*100)

	if postOptimizeAvgRecall < postInsertAvgRecall-0.02 {
		fmt.Fprintf(os.Stderr, "WARNING: Graph optimization hurt recall: %.2f%% -> %.2f%%\n",
			postInsertAvgRecall*100, postOptimizeAvgRecall*100)
	}

	totalTime := time.Since(totalStart)
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total files:      %d\n", len(files))
	fmt.Printf("Total symbols:    %d\n", len(symbols))
	fmt.Printf("Total time:       %v\n", totalTime)
	fmt.Printf("Build time:       %v (%.0f vec/sec)\n", buildTime, float64(len(embeddings))/buildTime.Seconds())
	fmt.Printf("Search latency:   %v avg\n", vamanaAvgLatency)
	fmt.Printf("Recall@%d:        %.2f%%\n", K, vamanaAvgRecall*100)
	fmt.Printf("Persistence:      save=%v, load=%v\n", saveTime, loadTime)
	fmt.Printf("Loaded recall:    %.2f%%\n", loadedAvgRecall*100)
	fmt.Printf("Repaired recall:  %.2f%%\n", repairedAvgRecall*100)
	fmt.Printf("Insert:           %v avg, %.0f vec/sec\n", avgInsertLatency, float64(len(insertEmbeddings))/totalInsertTime.Seconds())
	fmt.Printf("Post-insert recall: %.2f%%\n", postInsertAvgRecall*100)

	if *memProfile != "" {
		f, err := os.Create(*memProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not create memory profile: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "could not write memory profile: %v\n", err)
			os.Exit(1)
		}
	}
}

func parseAllSymbols(root string, files []string) []symbol {
	tool := treesitter.NewTreeSitterTool()
	ctx := context.Background()

	var symbols []symbol
	var mu sync.Mutex
	var parsed atomic.Int64

	workCh := make(chan string, 1000)
	var wg sync.WaitGroup

	workers := runtime.NumCPU()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local []symbol

			for path := range workCh {
				content, err := os.ReadFile(path)
				if err != nil {
					continue
				}

				result, err := tool.ParseFast(ctx, path, content)
				if err != nil {
					continue
				}

				relPath, _ := filepath.Rel(root, path)

				for _, fn := range result.Functions {
					nt := uint16(vectorgraphdb.NodeTypeFunction)
					if fn.IsMethod {
						nt = uint16(vectorgraphdb.NodeTypeMethod)
					}
					sig := fn.Parameters
					if fn.ReturnType != "" {
						sig += " " + fn.ReturnType
					}
					local = append(local, symbol{
						id:        relPath + ":" + fn.Name,
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
					local = append(local, symbol{
						id:        relPath + ":" + typ.Name,
						name:      typ.Name,
						kind:      typ.Kind,
						signature: "",
						filePath:  relPath,
						nodeType:  nt,
					})
				}

				parsed.Add(1)
			}

			mu.Lock()
			symbols = append(symbols, local...)
			mu.Unlock()
		}()
	}

	for _, f := range files {
		workCh <- f
	}
	close(workCh)
	wg.Wait()

	return symbols
}

func generateEmbeddings(emb *embedder.ClusteredMockEmbedder, symbols []symbol) [][]float32 {
	texts := make([]string, len(symbols))
	for i, s := range symbols {
		texts[i] = embedder.SerializeSymbolStandalone(s.name, s.kind, s.signature, s.filePath)
	}

	rawEmbeddings, err := emb.EmbedBatch(context.Background(), texts)
	if err != nil {
		panic(err)
	}

	dim := emb.Dimension()
	flat := make([]float32, len(rawEmbeddings)*dim)
	embeddings := make([][]float32, len(rawEmbeddings))
	for i, e := range rawEmbeddings {
		copy(flat[i*dim:], e)
		embeddings[i] = flat[i*dim : (i+1)*dim : (i+1)*dim]
	}

	return embeddings
}

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
