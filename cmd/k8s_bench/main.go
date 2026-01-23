package main

import (
	"context"
	"fmt"
	"io/fs"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/ivf"
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

func main() {
	root := "/tmp/kubernetes"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	fmt.Printf("=== Kubernetes Full Ingest Benchmark ===\n")
	fmt.Printf("Root: %s\n\n", root)

	totalStart := time.Now()

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

	config := ivf.ConfigForN(len(embeddings), dim)
	fmt.Printf("\nIVF Config: partitions=%d, nprobe=%d, oversample=%d, dim=%d\n",
		config.NumPartitions, config.NProbe, config.Oversample, dim)

	fmt.Print("\nPhase 4: Building IVF index... ")
	buildStart := time.Now()
	idx := ivf.NewIndex(config, dim)
	idx.Build(embeddings)
	buildTime := time.Since(buildStart)

	fmt.Printf("done in %v (%.0f vec/sec)\n", buildTime, float64(len(embeddings))/buildTime.Seconds())

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

	var totalLatency time.Duration
	var totalRecall float64

	for i := range numQueries {
		queryIdx := (i * stride) % n
		queryVec := embeddings[queryIdx]

		start := time.Now()
		results := idx.Search(queryVec, K)
		totalLatency += time.Since(start)

		groundTruth := bruteForceTopK(queryVec, embeddings, K)
		recall := computeRecall(results, groundTruth)
		totalRecall += recall

	}
	avgLatency := totalLatency / time.Duration(numQueries)
	avgRecall := totalRecall / float64(numQueries)
	fmt.Printf("  Avg latency: %v\n", avgLatency)
	fmt.Printf("  Avg recall@%d: %.2f%%\n", K, avgRecall*100)

	totalTime := time.Since(totalStart)
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total files:      %d\n", len(files))
	fmt.Printf("Total symbols:    %d\n", len(symbols))
	fmt.Printf("Total time:       %v\n", totalTime)
	fmt.Printf("Build time:       %v (%.0f vec/sec)\n", buildTime, float64(len(embeddings))/buildTime.Seconds())
	fmt.Printf("Search latency:   %v avg\n", avgLatency)
	fmt.Printf("Recall@%d:        %.2f%%\n", K, avgRecall*100)
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
