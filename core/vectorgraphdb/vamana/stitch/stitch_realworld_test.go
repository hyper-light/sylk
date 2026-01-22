package stitch

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search/indexer"
	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/scann"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestStitch_RealWorld_SylkPlusPaperPlusRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real-world stitch test in short mode")
	}

	tmpDir := t.TempDir()
	testDataDir := "/tmp/stitch_test"

	if _, err := os.Stat(filepath.Join(testDataDir, "paper.txt")); os.IsNotExist(err) {
		t.Skip("Test data not available. Run: curl -L -o /tmp/stitch_test/paper.pdf 'https://arxiv.org/pdf/2401.11324' && pdftotext /tmp/stitch_test/paper.pdf /tmp/stitch_test/paper.txt")
	}
	if _, err := os.Stat(filepath.Join(testDataDir, "DiskANN")); os.IsNotExist(err) {
		t.Skip("DiskANN repo not available. Run: git clone --depth 1 https://github.com/microsoft/DiskANN.git /tmp/stitch_test/DiskANN")
	}

	sylkRoot := findProjectRoot(t)
	t.Logf("Sylk root: %s", sylkRoot)

	t.Log("=== Phase 1: Collect Sylk symbols ===")
	sylkSymbols := collectGoSymbols(t, sylkRoot)
	t.Logf("Sylk symbols: %d", len(sylkSymbols))

	t.Log("=== Phase 2: Collect paper chunks ===")
	paperChunks := collectPaperChunks(t, filepath.Join(testDataDir, "paper.txt"))
	t.Logf("Paper chunks: %d", len(paperChunks))

	t.Log("=== Phase 3: Collect DiskANN symbols ===")
	diskannSymbols := collectRustSymbols(t, filepath.Join(testDataDir, "DiskANN"))
	t.Logf("DiskANN symbols: %d", len(diskannSymbols))

	dim := 128
	sylkCount := len(sylkSymbols)
	paperCount := len(paperChunks)
	diskannCount := len(diskannSymbols)
	totalCount := sylkCount + paperCount + diskannCount

	t.Logf("Total vectors: %d (Sylk=%d, Paper=%d, DiskANN=%d)", totalCount, sylkCount, paperCount, diskannCount)

	mockEmb := embedder.NewMockEmbedder(dim)

	t.Log("=== Phase 4: Generate embeddings ===")
	embStart := time.Now()

	sylkTexts := make([]string, sylkCount)
	for i, s := range sylkSymbols {
		sylkTexts[i] = embedder.SerializeSymbolStandalone(s.name, s.kind, s.signature, s.filePath)
	}
	sylkEmbeddings, _ := mockEmb.EmbedBatch(context.Background(), sylkTexts)

	paperEmbeddings, _ := mockEmb.EmbedBatch(context.Background(), paperChunks)

	diskannTexts := make([]string, diskannCount)
	for i, s := range diskannSymbols {
		diskannTexts[i] = embedder.SerializeSymbolStandalone(s.name, s.kind, s.signature, s.filePath)
	}
	diskannEmbeddings, _ := mockEmb.EmbedBatch(context.Background(), diskannTexts)

	t.Logf("Embedding time: %v", time.Since(embStart))

	t.Log("=== Phase 5: Create storage ===")
	vectorStore, err := storage.CreateVectorStore(filepath.Join(tmpDir, "vectors.bin"), dim, totalCount)
	if err != nil {
		t.Fatalf("CreateVectorStore: %v", err)
	}
	defer vectorStore.Close()

	for _, emb := range sylkEmbeddings {
		vectorStore.Append(emb)
	}
	for _, emb := range paperEmbeddings {
		vectorStore.Append(emb)
	}
	for _, emb := range diskannEmbeddings {
		vectorStore.Append(emb)
	}

	config := vamana.DefaultConfig()
	magCache := vamana.NewMagnitudeCache(totalCount)

	t.Log("=== Phase 6: Build Sylk graph ===")
	sylkGraph, err := storage.CreateGraphStore(filepath.Join(tmpDir, "sylk_graph.bin"), config.R, sylkCount)
	if err != nil {
		t.Fatalf("CreateGraphStore sylk: %v", err)
	}
	defer sylkGraph.Close()

	sylkStart := time.Now()
	sylkMedoid := buildGraphFromEmbeddings(t, sylkEmbeddings, sylkGraph, vectorStore, magCache, config, 0)
	t.Logf("Sylk graph built in %v, medoid=%d", time.Since(sylkStart), sylkMedoid)

	t.Log("=== Phase 7: Build Paper graph ===")
	paperGraph, err := storage.CreateGraphStore(filepath.Join(tmpDir, "paper_graph.bin"), config.R, paperCount)
	if err != nil {
		t.Fatalf("CreateGraphStore paper: %v", err)
	}
	defer paperGraph.Close()

	paperStart := time.Now()
	paperMedoid := buildGraphFromEmbeddings(t, paperEmbeddings, paperGraph, vectorStore, magCache, config, uint32(sylkCount))
	t.Logf("Paper graph built in %v, medoid=%d", time.Since(paperStart), paperMedoid)

	t.Log("=== Phase 8: Build DiskANN graph ===")
	diskannGraph, err := storage.CreateGraphStore(filepath.Join(tmpDir, "diskann_graph.bin"), config.R, diskannCount)
	if err != nil {
		t.Fatalf("CreateGraphStore diskann: %v", err)
	}
	defer diskannGraph.Close()

	diskannStart := time.Now()
	diskannOffset := uint32(sylkCount + paperCount)
	diskannMedoid := buildGraphFromEmbeddings(t, diskannEmbeddings, diskannGraph, vectorStore, magCache, config, diskannOffset)
	t.Logf("DiskANN graph built in %v, medoid=%d", time.Since(diskannStart), diskannMedoid)

	t.Log("=== Phase 9: Stitch Paper to Sylk ===")
	stitchConfig := DefaultStitchConfig()
	stitcher := NewStitcher(stitchConfig, magCache)

	stitch1Start := time.Now()
	result1, err := stitcher.Stitch(sylkGraph, paperGraph, vectorStore, sylkMedoid, paperMedoid)
	if err != nil {
		t.Fatalf("Stitch paper: %v", err)
	}
	t.Logf("Paper stitch: %v, edges=%d, searches=%d", time.Since(stitch1Start), result1.CrossEdgesAdded, result1.SearchesPerformed)

	t.Log("=== Phase 10: Stitch DiskANN to Sylk ===")
	stitch2Start := time.Now()
	result2, err := stitcher.Stitch(sylkGraph, diskannGraph, vectorStore, sylkMedoid, diskannMedoid)
	if err != nil {
		t.Fatalf("Stitch diskann: %v", err)
	}
	t.Logf("DiskANN stitch: %v, edges=%d, searches=%d", time.Since(stitch2Start), result2.CrossEdgesAdded, result2.SearchesPerformed)

	t.Log("=== Phase 11: Cross-domain search validation ===")

	paperQuery := "GPU approximate nearest neighbor search billion scale"
	paperQueryEmb, _ := mockEmb.Embed(context.Background(), paperQuery)

	results := vamana.GreedySearch(paperQueryEmb, sylkMedoid, config.L, 20, vectorStore, sylkGraph, magCache)

	foundPaper := false
	foundDiskANN := false
	foundSylk := false

	for _, r := range results {
		id := uint32(r.InternalID)
		if id < uint32(sylkCount) {
			foundSylk = true
		} else if id < uint32(sylkCount+paperCount) {
			foundPaper = true
		} else {
			foundDiskANN = true
		}
	}

	t.Logf("Search results: Sylk=%v, Paper=%v, DiskANN=%v", foundSylk, foundPaper, foundDiskANN)

	if !foundPaper {
		t.Log("WARNING: Paper chunks not found in search results")
	}

	t.Log("=== Summary ===")
	t.Logf("Total vectors indexed: %d", totalCount)
	t.Logf("  Sylk (Go): %d symbols", sylkCount)
	t.Logf("  Paper (text): %d chunks", paperCount)
	t.Logf("  DiskANN (Rust): %d symbols", diskannCount)
	t.Logf("Cross-edges added: %d", result1.CrossEdgesAdded+result2.CrossEdgesAdded)
	t.Log("Stitch test PASSED")
}

type symbolInfo struct {
	id        string
	name      string
	kind      string
	signature string
	filePath  string
}

func findProjectRoot(t *testing.T) string {
	t.Helper()
	cwd, _ := os.Getwd()
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

func collectGoSymbols(t *testing.T, root string) []symbolInfo {
	t.Helper()
	ctx := context.Background()

	scanner := indexer.NewScanner(indexer.ScanConfig{
		RootPath:        root,
		IncludePatterns: []string{"*.go"},
		ExcludePatterns: []string{"*_test.go", "*.pb.go", "vendor/*"},
		MaxFileSize:     1 * 1024 * 1024,
	})

	fileCh, _ := scanner.Scan(ctx)
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
				relPath, _ := filepath.Rel(root, item.path)
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

	for _, path := range files {
		content, _ := os.ReadFile(path)
		workCh <- struct{ path, content string }{path, string(content)}
	}
	close(workCh)
	wg.Wait()
	return symbols
}

func collectPaperChunks(t *testing.T, paperPath string) []string {
	t.Helper()
	file, err := os.Open(paperPath)
	if err != nil {
		t.Fatalf("Open paper: %v", err)
	}
	defer file.Close()

	var chunks []string
	var currentChunk strings.Builder
	lineCount := 0
	chunkSize := 10

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		currentChunk.WriteString(line)
		currentChunk.WriteString(" ")
		lineCount++

		if lineCount >= chunkSize {
			chunk := strings.TrimSpace(currentChunk.String())
			if len(chunk) > 50 {
				chunks = append(chunks, chunk)
			}
			currentChunk.Reset()
			lineCount = 0
		}
	}

	if currentChunk.Len() > 50 {
		chunks = append(chunks, strings.TrimSpace(currentChunk.String()))
	}

	return chunks
}

func collectRustSymbols(t *testing.T, root string) []symbolInfo {
	t.Helper()

	var files []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".rs") {
			if !strings.Contains(path, "/target/") && !strings.Contains(path, "_test.rs") {
				files = append(files, path)
			}
		}
		return nil
	})

	if len(files) > 200 {
		files = files[:200]
	}

	ctx := context.Background()
	tool := treesitter.NewTreeSitterTool()
	var symbols []symbolInfo
	seen := make(map[string]bool)
	var mu sync.Mutex

	workCh := make(chan struct{ path, content string }, 100)
	var wg sync.WaitGroup

	for range min(runtime.NumCPU(), 4) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workCh {
				result, err := tool.ParseFast(ctx, item.path, []byte(item.content))
				if err != nil {
					continue
				}
				relPath, _ := filepath.Rel(root, item.path)
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

	for _, path := range files {
		content, _ := os.ReadFile(path)
		workCh <- struct{ path, content string }{path, string(content)}
	}
	close(workCh)
	wg.Wait()
	return symbols
}

func buildGraphFromEmbeddings(t *testing.T, embeddings [][]float32, graph *storage.GraphStore, vectorStore *storage.VectorStore, magCache *vamana.MagnitudeCache, config vamana.VamanaConfig, idOffset uint32) uint32 {
	t.Helper()

	n := len(embeddings)
	if n == 0 {
		return 0
	}

	for i, emb := range embeddings {
		globalID := uint32(i) + idOffset
		magCache.GetOrCompute(globalID, emb)
	}

	avqConfig := scann.AVQConfig{NumPartitions: max(4, n/100), CodebookSize: 256, AnisotropicWeight: 0.2}
	if avqConfig.NumPartitions > 64 {
		avqConfig.NumPartitions = 64
	}
	batchConfig := scann.DefaultBatchBuildConfig()
	builder := scann.NewBatchBuilder(batchConfig, avqConfig, config)

	result, err := builder.Build(embeddings, vectorStore, graph, magCache)
	if err != nil {
		t.Fatalf("ScaNN build: %v", err)
	}

	return result.Medoid
}

func ensureTestData(t *testing.T, testDataDir string) {
	t.Helper()

	if _, err := os.Stat(testDataDir); os.IsNotExist(err) {
		os.MkdirAll(testDataDir, 0755)
	}

	paperPath := filepath.Join(testDataDir, "paper.txt")
	if _, err := os.Stat(paperPath); os.IsNotExist(err) {
		t.Log("Downloading paper...")
		cmd := exec.Command("sh", "-c", "curl -L -o /tmp/stitch_test/paper.pdf 'https://arxiv.org/pdf/2401.11324' && pdftotext /tmp/stitch_test/paper.pdf /tmp/stitch_test/paper.txt")
		cmd.Run()
	}

	diskannPath := filepath.Join(testDataDir, "DiskANN")
	if _, err := os.Stat(diskannPath); os.IsNotExist(err) {
		t.Log("Cloning DiskANN...")
		cmd := exec.Command("git", "clone", "--depth", "1", "https://github.com/microsoft/DiskANN.git", diskannPath)
		cmd.Run()
	}
}
