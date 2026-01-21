package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search/indexer"
	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestIngestionPipeline_Sylk(t *testing.T) {
	projectRoot := findProjectRoot(t)
	t.Logf("Project root: %s", projectRoot)

	symbols := collectSymbols(t, projectRoot)
	t.Logf("Collected %d raw symbols", len(symbols))

	seen := make(map[string]bool)
	unique := make([]symbolInfo, 0, len(symbols))
	for _, s := range symbols {
		if !seen[s.id] {
			seen[s.id] = true
			unique = append(unique, s)
		}
	}
	t.Logf("Unique symbols: %d", len(unique))

	tmpDir := t.TempDir()
	stats := runIngestionPipeline(t, tmpDir, unique)

	t.Logf("=== Ingestion Pipeline Statistics ===")
	t.Logf("  Symbols: %d", stats.symbolCount)
	t.Logf("  Serialize time: %v", stats.serializeTime)
	t.Logf("  Embed time: %v", stats.embedTime)
	t.Logf("  Write time: %v", stats.writeTime)
	t.Logf("  Total time: %v", stats.totalTime)
	t.Logf("  Throughput: %.0f symbols/sec", float64(stats.symbolCount)/stats.totalTime.Seconds())

	verifyIngestion(t, tmpDir, unique, stats)
}

type symbolInfo struct {
	id        string
	name      string
	kind      string
	signature string
	filePath  string
	nodeType  NodeType
}

type pipelineStats struct {
	symbolCount   int
	serializeTime time.Duration
	embedTime     time.Duration
	writeTime     time.Duration
	totalTime     time.Duration
	diskSize      int64
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
					nt := NodeTypeFunction
					if fn.IsMethod {
						nt = NodeTypeMethod
					}
					sig := fn.Parameters
					if fn.ReturnType != "" {
						sig += " " + fn.ReturnType
					}
					local = append(local, symbolInfo{
						id:        relPath + ":" + fn.Name,
						name:      fn.Name,
						kind:      "function",
						signature: sig,
						filePath:  relPath,
						nodeType:  nt,
					})
				}

				for _, typ := range result.Types {
					nt := NodeTypeStruct
					if typ.Kind == "interface" {
						nt = NodeTypeInterface
					}
					local = append(local, symbolInfo{
						id:        relPath + ":" + typ.Name,
						name:      typ.Name,
						kind:      typ.Kind,
						signature: "",
						filePath:  relPath,
						nodeType:  nt,
					})
				}

				mu.Lock()
				symbols = append(symbols, local...)
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

func runIngestionPipeline(t *testing.T, tmpDir string, symbols []symbolInfo) pipelineStats {
	t.Helper()

	stats := pipelineStats{symbolCount: len(symbols)}
	totalStart := time.Now()

	serializeStart := time.Now()
	texts := make([]string, len(symbols))
	for i, s := range symbols {
		texts[i] = embedder.SerializeSymbolStandalone(s.name, s.kind, s.signature, s.filePath)
	}
	stats.serializeTime = time.Since(serializeStart)

	embedStart := time.Now()
	mock := embedder.NewMockEmbedder(768)
	embeddings, err := mock.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	stats.embedTime = time.Since(embedStart)

	writeStart := time.Now()

	vectorStore, err := storage.CreateVectorStore(
		filepath.Join(tmpDir, "vectors.bin"),
		768,
		len(symbols),
	)
	if err != nil {
		t.Fatalf("CreateVectorStore failed: %v", err)
	}
	defer vectorStore.Close()

	labelStore, err := storage.CreateLabelStore(
		filepath.Join(tmpDir, "labels.bin"),
		len(symbols),
	)
	if err != nil {
		t.Fatalf("CreateLabelStore failed: %v", err)
	}
	defer labelStore.Close()

	idMap := storage.NewIDMap()

	writer := NewBatchVectorWriter(BatchWriterConfig{
		VectorStore: vectorStore,
		LabelStore:  labelStore,
		IDMap:       idMap,
		BatchSize:   1000,
	})

	for i, s := range symbols {
		if err := writer.Add(s.id, embeddings[i], vectorgraphdb.DomainCode, s.nodeType); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := idMap.SaveBinary(filepath.Join(tmpDir, "idmap.bin")); err != nil {
		t.Fatalf("SaveBinary failed: %v", err)
	}

	stats.writeTime = time.Since(writeStart)
	stats.totalTime = time.Since(totalStart)
	stats.diskSize = getDirSize(tmpDir)

	return stats
}

func verifyIngestion(t *testing.T, tmpDir string, symbols []symbolInfo, stats pipelineStats) {
	t.Helper()

	vectorStore, err := storage.OpenVectorStore(filepath.Join(tmpDir, "vectors.bin"))
	if err != nil {
		t.Fatalf("OpenVectorStore failed: %v", err)
	}
	defer vectorStore.Close()

	labelStore, err := storage.OpenLabelStore(filepath.Join(tmpDir, "labels.bin"))
	if err != nil {
		t.Fatalf("OpenLabelStore failed: %v", err)
	}
	defer labelStore.Close()

	idMap, err := storage.LoadIDMapBinary(filepath.Join(tmpDir, "idmap.bin"))
	if err != nil {
		t.Fatalf("LoadIDMapBinary failed: %v", err)
	}

	if int(vectorStore.Count()) != len(symbols) {
		t.Errorf("Vector count mismatch: got %d, want %d", vectorStore.Count(), len(symbols))
	}

	if idMap.Size() != len(symbols) {
		t.Errorf("IDMap size mismatch: got %d, want %d", idMap.Size(), len(symbols))
	}

	sampleCount := min(100, len(symbols))
	verified := 0

	mock := embedder.NewMockEmbedder(768)

	for i := range sampleCount {
		idx := i * len(symbols) / sampleCount
		s := symbols[idx]

		internalID, ok := idMap.ToInternal(s.id)
		if !ok {
			t.Errorf("Symbol %q not in IDMap", s.id)
			continue
		}

		vec := vectorStore.Get(internalID)
		if vec == nil {
			t.Errorf("Vector %d not found", internalID)
			continue
		}

		text := embedder.SerializeSymbolStandalone(s.name, s.kind, s.signature, s.filePath)
		expected, _ := mock.Embed(context.Background(), text)

		match := true
		for j := range vec {
			if vec[j] != expected[j] {
				match = false
				break
			}
		}
		if !match {
			t.Errorf("Vector content mismatch for %q", s.id)
			continue
		}

		domain, nodeType := labelStore.Get(internalID)
		if domain != uint8(vectorgraphdb.DomainCode) {
			t.Errorf("Domain mismatch for %q: got %d", s.id, domain)
			continue
		}
		if nodeType != s.nodeType {
			t.Errorf("NodeType mismatch for %q: got %d, want %d", s.id, nodeType, s.nodeType)
			continue
		}

		verified++
	}

	t.Logf("Verified %d/%d samples", verified, sampleCount)

	t.Logf("=== Performance Validation ===")

	if stats.totalTime > 500*time.Millisecond {
		if raceEnabled {
			t.Logf("SKIP: Total time %v exceeds 500ms (race detector overhead)", stats.totalTime)
		} else {
			t.Errorf("FAIL: Total time %v exceeds 500ms target", stats.totalTime)
		}
	} else {
		t.Logf("PASS: Total time %v within 500ms target", stats.totalTime)
	}

	throughput := float64(stats.symbolCount) / stats.totalTime.Seconds()
	if throughput < 100000 {
		t.Logf("WARNING: Throughput %.0f symbols/sec below 100,000 target", throughput)
	} else {
		t.Logf("PASS: Throughput %.0f symbols/sec meets 100,000 target", throughput)
	}

	bytesPerSymbol := float64(stats.diskSize) / float64(stats.symbolCount)
	if bytesPerSymbol > 3300 {
		t.Logf("WARNING: Disk usage %.0f bytes/symbol exceeds 3.3KB target", bytesPerSymbol)
	} else {
		t.Logf("PASS: Disk usage %.0f bytes/symbol within 3.3KB target", bytesPerSymbol)
	}

	t.Logf("Disk size: %.2f MB", float64(stats.diskSize)/(1024*1024))
}

func getDirSize(dir string) int64 {
	var size int64
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}
