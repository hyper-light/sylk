package stitch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/scann"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestStitch_SylkPlusKubernetes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real-world stitch test in short mode")
	}

	k8sRoot := "/tmp/kubernetes"
	if _, err := os.Stat(k8sRoot); os.IsNotExist(err) {
		t.Skip("Kubernetes not available at /tmp/kubernetes. Run: git clone --depth 1 https://github.com/kubernetes/kubernetes.git /tmp/kubernetes")
	}

	sylkRoot := findProjectRoot(t)
	tmpDir := t.TempDir()

	t.Log("=== Phase 1: Collect Sylk symbols ===")
	collectStart := time.Now()
	sylkSymbols := collectAllGoSymbols(t, sylkRoot)
	t.Logf("Sylk: %d symbols in %v", len(sylkSymbols), time.Since(collectStart))

	t.Log("=== Phase 2: Collect Kubernetes symbols ===")
	collectStart = time.Now()
	k8sSymbols := collectAllGoSymbols(t, k8sRoot)
	t.Logf("Kubernetes: %d symbols in %v", len(k8sSymbols), time.Since(collectStart))

	dim := 768
	sylkCount := len(sylkSymbols)
	k8sCount := len(k8sSymbols)
	totalCount := sylkCount + k8sCount

	t.Logf("Total vectors: %d (Sylk=%d, K8s=%d)", totalCount, sylkCount, k8sCount)

	clusteredEmb := embedder.NewClusteredMockEmbedder(dim)

	t.Log("=== Phase 3: Generate embeddings ===")
	embStart := time.Now()

	sylkTexts := make([]string, sylkCount)
	for i, s := range sylkSymbols {
		sylkTexts[i] = embedder.SerializeSymbolStandalone(s.name, s.kind, s.signature, s.filePath)
	}
	sylkEmbeddings, _ := clusteredEmb.EmbedBatch(context.Background(), sylkTexts)

	k8sTexts := make([]string, k8sCount)
	for i, s := range k8sSymbols {
		k8sTexts[i] = embedder.SerializeSymbolStandalone(s.name, s.kind, s.signature, s.filePath)
	}
	k8sEmbeddings, _ := clusteredEmb.EmbedBatch(context.Background(), k8sTexts)

	t.Logf("Embedding time: %v", time.Since(embStart))

	t.Log("=== Phase 4: Create storage ===")
	vectorStore, err := storage.CreateVectorStore(filepath.Join(tmpDir, "vectors.bin"), dim, totalCount)
	if err != nil {
		t.Fatalf("CreateVectorStore: %v", err)
	}
	defer vectorStore.Close()

	for _, emb := range sylkEmbeddings {
		vectorStore.Append(emb)
	}
	for _, emb := range k8sEmbeddings {
		vectorStore.Append(emb)
	}

	config := vamana.DefaultConfig()
	magCache := vamana.NewMagnitudeCache(totalCount)

	t.Log("=== Phase 5: Build Sylk graph ===")
	sylkGraph, err := storage.CreateGraphStore(filepath.Join(tmpDir, "sylk_graph.bin"), config.R, totalCount)
	if err != nil {
		t.Fatalf("CreateGraphStore sylk: %v", err)
	}
	defer sylkGraph.Close()

	sylkBuildStart := time.Now()
	sylkMedoid := buildGraphK8s(t, sylkEmbeddings, sylkGraph, vectorStore, magCache, config, 0)
	t.Logf("Sylk graph: %v, medoid=%d", time.Since(sylkBuildStart), sylkMedoid)

	t.Log("=== Phase 6: Build Kubernetes graph ===")
	k8sGraph, err := storage.CreateGraphStore(filepath.Join(tmpDir, "k8s_graph.bin"), config.R, k8sCount)
	if err != nil {
		t.Fatalf("CreateGraphStore k8s: %v", err)
	}
	defer k8sGraph.Close()

	k8sBuildStart := time.Now()
	k8sOffset := uint32(sylkCount)
	k8sMedoid := buildGraphK8s(t, k8sEmbeddings, k8sGraph, vectorStore, magCache, config, k8sOffset)
	t.Logf("K8s graph: %v, medoid=%d", time.Since(k8sBuildStart), k8sMedoid)

	t.Log("=== Phase 7: Stitch Kubernetes to Sylk ===")
	stitchConfig := DefaultStitchConfig()
	stitcher := NewStitcher(stitchConfig, magCache)

	stitchStart := time.Now()
	result, err := stitcher.Stitch(sylkGraph, k8sGraph, vectorStore, sylkMedoid, k8sMedoid)
	if err != nil {
		t.Fatalf("Stitch: %v", err)
	}
	t.Logf("Stitch: %v, cross-edges=%d, searches=%d", time.Since(stitchStart), result.CrossEdgesAdded, result.SearchesPerformed)

	t.Log("=== Phase 8: Cross-domain search validation ===")
	snapshot := vectorStore.Snapshot()

	t.Log("Search from Sylk medoid using K8s vector:")
	k8sQueryEmb := k8sEmbeddings[0]
	resultsFromSylk := vamana.GreedySearchFast(k8sQueryEmb, sylkMedoid, 300, 20, snapshot, sylkGraph, magCache)
	sylkHits, k8sHits := 0, 0
	for _, r := range resultsFromSylk {
		if uint32(r.InternalID) < uint32(sylkCount) {
			sylkHits++
		} else {
			k8sHits++
		}
	}
	t.Logf("  From Sylk medoid: Sylk=%d, K8s=%d (should find K8s via stitch)", sylkHits, k8sHits)

	t.Log("Search from K8s medoid using Sylk vector:")
	sylkQueryEmb := sylkEmbeddings[0]
	resultsFromK8s := vamana.GreedySearchFast(sylkQueryEmb, k8sMedoid, 300, 20, snapshot, k8sGraph, magCache)
	sylkHits, k8sHits = 0, 0
	for _, r := range resultsFromK8s {
		if uint32(r.InternalID) < uint32(sylkCount) {
			sylkHits++
		} else {
			k8sHits++
		}
	}
	t.Logf("  From K8s medoid: Sylk=%d, K8s=%d (should find Sylk via stitch)", sylkHits, k8sHits)

	t.Log("Verify stitch edges exist:")
	sylkNeighbors := sylkGraph.GetNeighbors(sylkMedoid)
	crossEdges := 0
	for _, n := range sylkNeighbors {
		if n >= uint32(sylkCount) {
			crossEdges++
		}
	}
	t.Logf("  Sylk medoid has %d neighbors, %d cross to K8s", len(sylkNeighbors), crossEdges)

	t.Log("=== Phase 9: Latency validation ===")
	numQueries := 100
	K := 10

	var totalLatency time.Duration
	for i := range numQueries {
		var queryEmb []float32
		if i%2 == 0 && i/2 < len(sylkEmbeddings) {
			queryEmb = sylkEmbeddings[i/2]
		} else if i/2 < len(k8sEmbeddings) {
			queryEmb = k8sEmbeddings[i/2]
		} else {
			continue
		}

		start := time.Now()
		_ = vamana.GreedySearchFast(queryEmb, sylkMedoid, 300, K, snapshot, sylkGraph, magCache)
		totalLatency += time.Since(start)
	}

	avgLatency := totalLatency / time.Duration(numQueries)
	t.Logf("Average search latency: %v", avgLatency)

	t.Log("=== Summary ===")
	t.Logf("Sylk: %d symbols", sylkCount)
	t.Logf("Kubernetes: %d symbols", k8sCount)
	t.Logf("Total: %d vectors", totalCount)
	t.Logf("Cross-edges: %d", result.CrossEdgesAdded)
	t.Logf("Avg latency: %v", avgLatency)

	if avgLatency > 50*time.Millisecond {
		t.Errorf("FAIL: Latency %v exceeds 50ms threshold", avgLatency)
	} else {
		t.Logf("PASS: Latency %v within threshold", avgLatency)
	}
}

func collectAllGoSymbols(t *testing.T, root string) []symbolInfo {
	t.Helper()
	ctx := context.Background()

	var goFiles []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			goFiles = append(goFiles, path)
		}
		return nil
	})

	t.Logf("Found %d Go files in %s", len(goFiles), root)

	tool := treesitter.NewTreeSitterTool()
	var symbols []symbolInfo
	seen := make(map[string]bool)
	var mu sync.Mutex

	workCh := make(chan string, 1000)
	var wg sync.WaitGroup

	workers := runtime.NumCPU()

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
				var local []symbolInfo
				for _, fn := range result.Functions {
					id := relPath + ":" + fn.Name
					local = append(local, symbolInfo{id: id, name: fn.Name, kind: "function", signature: fn.Parameters, filePath: relPath})
				}
				for _, typ := range result.Types {
					id := relPath + ":" + typ.Name
					local = append(local, symbolInfo{id: id, name: typ.Name, kind: typ.Kind, filePath: relPath})
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

	for _, path := range goFiles {
		workCh <- path
	}
	close(workCh)
	wg.Wait()

	return symbols
}

func buildGraphK8s(t *testing.T, embeddings [][]float32, graph *storage.GraphStore, vectorStore *storage.VectorStore, magCache *vamana.MagnitudeCache, config vamana.VamanaConfig, idOffset uint32) uint32 {
	t.Helper()

	n := len(embeddings)
	if n == 0 {
		return 0
	}

	for i, emb := range embeddings {
		globalID := uint32(i) + idOffset
		magCache.GetOrCompute(globalID, emb)
	}

	avqConfig := scann.AVQConfig{NumPartitions: max(4, n/500), CodebookSize: 256, AnisotropicWeight: 0.2}
	if avqConfig.NumPartitions > 64 {
		avqConfig.NumPartitions = 64
	}
	batchConfig := scann.DefaultBatchBuildConfig()
	builder := scann.NewBatchBuilder(batchConfig, avqConfig, config)

	result, timings, err := builder.BuildWithFlashAndTimings(embeddings, vectorStore, graph, magCache)
	if err != nil {
		t.Fatalf("ScaNN+Flash build: %v", err)
	}

	t.Logf("  Build timings: KMeans=%v, Codebooks=%v, Magnitudes=%v, GraphInit=%v, Refinement=%v, Medoid=%v",
		timings.KMeans, timings.Codebooks, timings.Magnitudes, timings.GraphInit, timings.Refinement, timings.Medoid)

	return result.Medoid
}

func bruteForceK8s(query []float32, sylkEmbs, k8sEmbs [][]float32, K int) []uint32 {
	type scored struct {
		id         uint32
		similarity float64
	}

	queryMag := vamana.Magnitude(query)
	var results []scored

	for i, emb := range sylkEmbs {
		mag := vamana.Magnitude(emb)
		dot := vamana.DotProduct(query, emb)
		sim := float64(dot) / (queryMag * mag)
		results = append(results, scored{id: uint32(i), similarity: sim})
	}

	offset := uint32(len(sylkEmbs))
	for i, emb := range k8sEmbs {
		mag := vamana.Magnitude(emb)
		dot := vamana.DotProduct(query, emb)
		sim := float64(dot) / (queryMag * mag)
		results = append(results, scored{id: offset + uint32(i), similarity: sim})
	}

	for i := range results {
		maxIdx := i
		for j := i + 1; j < len(results); j++ {
			if results[j].similarity > results[maxIdx].similarity {
				maxIdx = j
			}
		}
		results[i], results[maxIdx] = results[maxIdx], results[i]
	}

	topK := make([]uint32, min(K, len(results)))
	for i := range topK {
		topK[i] = results[i].id
	}
	return topK
}

func computeRecallK8s(results []vamana.SearchResult, groundTruth []uint32) float64 {
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
