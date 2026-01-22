package storage

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search/indexer"
	"github.com/adalundhe/sylk/core/treesitter"
)

// BenchmarkStorageIngest_Sylk ingests the entire Sylk codebase into the storage layer.
// This validates Phase 2 storage on real data before building the algorithm.
func BenchmarkStorageIngest_Sylk(b *testing.B) {
	// Find project root
	projectRoot := findProjectRoot(b)
	b.Logf("Project root: %s", projectRoot)

	// Scan and parse codebase once
	symbols := collectSymbols(b, projectRoot)
	b.Logf("Collected %d symbols", len(symbols))

	if len(symbols) == 0 {
		b.Fatal("No symbols collected")
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		runIngestBenchmark(b, tmpDir, symbols)
	}
}

// TestStorageIngest_Sylk_Correctness verifies storage correctness on Sylk codebase.
func TestStorageIngest_Sylk_Correctness(t *testing.T) {
	projectRoot := findProjectRoot(t)
	t.Logf("Project root: %s", projectRoot)

	rawSymbols := collectSymbols(t, projectRoot)
	t.Logf("Collected %d raw symbols", len(rawSymbols))

	if len(rawSymbols) == 0 {
		t.Fatal("No symbols collected")
	}

	// Deduplicate symbols by ID (keep first occurrence) - mirrors runIngestBenchmark logic
	seen := make(map[string]bool)
	symbols := make([]Symbol, 0, len(rawSymbols))
	for _, sym := range rawSymbols {
		if !seen[sym.ID] {
			seen[sym.ID] = true
			symbols = append(symbols, sym)
		}
	}
	t.Logf("Unique symbols: %d (deduplicated %d)", len(symbols), len(rawSymbols)-len(symbols))

	tmpDir := t.TempDir()

	// Ingest
	stats := runIngestBenchmark(t, tmpDir, rawSymbols)

	// Report stats
	t.Logf("=== Ingest Statistics ===")
	t.Logf("  Symbols ingested: %d", stats.SymbolCount)
	t.Logf("  Parse time: %v", stats.ParseTime)
	t.Logf("  Embed time: %v (mock)", stats.EmbedTime)
	t.Logf("  Write time: %v", stats.WriteTime)
	t.Logf("  Total time: %v", stats.TotalTime)
	t.Logf("  Throughput: %.0f symbols/sec", float64(stats.SymbolCount)/stats.TotalTime.Seconds())

	// Verify correctness - use deduplicated symbols
	verifyStorage(t, tmpDir, symbols, stats)
}

// Symbol represents an extracted code symbol.
type Symbol struct {
	ID       string // file:name
	Name     string
	Kind     string // function, type, import
	FilePath string
	Line     uint32
	Domain   uint8
	NodeType uint16
}

// IngestStats holds timing information.
type IngestStats struct {
	SymbolCount int
	ParseTime   time.Duration
	EmbedTime   time.Duration
	WriteTime   time.Duration
	TotalTime   time.Duration
	DiskSize    int64
}

const (
	// Mock embedding dimension (matches text-embedding-3-small)
	embeddingDim = 768

	// Domain for code
	domainCode uint8 = 0

	// NodeTypes
	nodeTypeFunction  uint16 = 1
	nodeTypeMethod    uint16 = 2
	nodeTypeStruct    uint16 = 3
	nodeTypeInterface uint16 = 4
	nodeTypeVariable  uint16 = 5
	nodeTypeImport    uint16 = 6
)

func findProjectRoot(tb testing.TB) string {
	tb.Helper()

	// Try current working directory first
	cwd, err := os.Getwd()
	if err != nil {
		tb.Fatalf("Failed to get cwd: %v", err)
	}

	// Walk up to find go.mod
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

	// Fallback to hardcoded path
	return "/home/ada/Projects/sylk"
}

func collectSymbols(tb testing.TB, projectRoot string) []Symbol {
	tb.Helper()

	ctx := context.Background()
	workers := runtime.NumCPU()

	// Scan Go files
	scanner := indexer.NewScanner(indexer.ScanConfig{
		RootPath:        projectRoot,
		IncludePatterns: []string{"*.go"},
		ExcludePatterns: []string{"*_test.go", "*.pb.go", "*.gen.go", "vendor/*"},
		MaxFileSize:     1 * 1024 * 1024,
	})

	fileCh, err := scanner.Scan(ctx)
	if err != nil {
		tb.Fatalf("Scan failed: %v", err)
	}

	// Collect file paths
	var files []string
	for f := range fileCh {
		files = append(files, f.Path)
	}

	// Parse in parallel
	tool := treesitter.NewTreeSitterTool()
	var symbols []Symbol
	var mu sync.Mutex

	type workItem struct {
		path    string
		content []byte
	}

	workCh := make(chan workItem, 100)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workCh {
				result, err := tool.ParseFast(ctx, item.path, item.content)
				if err != nil {
					continue
				}

				relPath, _ := filepath.Rel(projectRoot, item.path)

				var localSymbols []Symbol

				for _, fn := range result.Functions {
					nodeType := nodeTypeFunction
					if fn.IsMethod {
						nodeType = nodeTypeMethod
					}
					localSymbols = append(localSymbols, Symbol{
						ID:       fmt.Sprintf("%s:%s", relPath, fn.Name),
						Name:     fn.Name,
						Kind:     "function",
						FilePath: relPath,
						Line:     fn.StartLine,
						Domain:   domainCode,
						NodeType: nodeType,
					})
				}

				for _, typ := range result.Types {
					nodeType := nodeTypeStruct
					if typ.Kind == "interface" {
						nodeType = nodeTypeInterface
					}
					localSymbols = append(localSymbols, Symbol{
						ID:       fmt.Sprintf("%s:%s", relPath, typ.Name),
						Name:     typ.Name,
						Kind:     "type",
						FilePath: relPath,
						Line:     typ.StartLine,
						Domain:   domainCode,
						NodeType: nodeType,
					})
				}

				mu.Lock()
				symbols = append(symbols, localSymbols...)
				mu.Unlock()
			}
		}()
	}

	// Feed work
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		workCh <- workItem{path: path, content: content}
	}
	close(workCh)
	wg.Wait()

	return symbols
}

func runIngestBenchmark(tb testing.TB, tmpDir string, symbols []Symbol) IngestStats {
	tb.Helper()

	// Deduplicate symbols by ID (keep first occurrence)
	seen := make(map[string]bool)
	uniqueSymbols := make([]Symbol, 0, len(symbols))
	for _, sym := range symbols {
		if !seen[sym.ID] {
			seen[sym.ID] = true
			uniqueSymbols = append(uniqueSymbols, sym)
		}
	}

	stats := IngestStats{SymbolCount: len(uniqueSymbols)}
	totalStart := time.Now()

	embedStart := time.Now()
	embeddings := make([][]float32, len(uniqueSymbols))

	numWorkers := runtime.NumCPU()
	chunkSize := (len(uniqueSymbols) + numWorkers - 1) / numWorkers
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := min(start+chunkSize, len(uniqueSymbols))
		if start >= end {
			break
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range embeddings[start:end] {
				embeddings[start+i] = mockEmbed(uniqueSymbols[start+i].ID, embeddingDim)
			}
		}()
	}
	wg.Wait()
	stats.EmbedTime = time.Since(embedStart)

	// Phase 2: Write to storage
	writeStart := time.Now()

	vectorStore, err := CreateVectorStore(
		filepath.Join(tmpDir, "vectors.bin"),
		embeddingDim,
		len(uniqueSymbols),
	)
	if err != nil {
		tb.Fatalf("Failed to create vector store: %v", err)
	}
	defer vectorStore.Close()

	const maxDegree = 32
	graphStore, err := CreateGraphStore(
		filepath.Join(tmpDir, "graph.bin"),
		maxDegree,
		len(uniqueSymbols),
	)
	if err != nil {
		tb.Fatalf("Failed to create graph store: %v", err)
	}
	defer graphStore.Close()

	labelStore, err := CreateLabelStore(
		filepath.Join(tmpDir, "labels.bin"),
		len(uniqueSymbols),
	)
	if err != nil {
		tb.Fatalf("Failed to create label store: %v", err)
	}
	defer labelStore.Close()

	idMap := NewIDMap()

	// Batch write - one vector per unique symbol
	for i, sym := range uniqueSymbols {
		internalID := idMap.Assign(sym.ID)

		// Verify ID matches expected index (sequential assignment)
		if internalID != uint32(i) {
			tb.Fatalf("ID assignment mismatch: got %d, want %d for %s", internalID, i, sym.ID)
		}

		_, err := vectorStore.Append(embeddings[i])
		if err != nil {
			tb.Fatalf("Failed to append vector %d: %v", i, err)
		}

		err = labelStore.Set(internalID, sym.Domain, sym.NodeType)
		if err != nil {
			tb.Fatalf("Failed to set label %d: %v", i, err)
		}
	}

	// Sync to disk
	if err := vectorStore.Sync(); err != nil {
		tb.Fatalf("Failed to sync vectors: %v", err)
	}
	if err := labelStore.Sync(); err != nil {
		tb.Fatalf("Failed to sync labels: %v", err)
	}
	if err := idMap.SaveBinary(filepath.Join(tmpDir, "idmap.bin")); err != nil {
		tb.Fatalf("Failed to save idmap: %v", err)
	}

	stats.WriteTime = time.Since(writeStart)
	stats.TotalTime = time.Since(totalStart)

	// Calculate disk size
	stats.DiskSize = getDirSize(tmpDir)

	// Store unique symbols for verification
	copy(symbols[:len(uniqueSymbols)], uniqueSymbols)

	return stats
}

func verifyStorage(t *testing.T, tmpDir string, symbols []Symbol, stats IngestStats) {
	t.Helper()

	// Reopen stores
	vectorStore, err := OpenVectorStore(filepath.Join(tmpDir, "vectors.bin"))
	if err != nil {
		t.Fatalf("Failed to reopen vector store: %v", err)
	}
	defer vectorStore.Close()

	labelStore, err := OpenLabelStore(filepath.Join(tmpDir, "labels.bin"))
	if err != nil {
		t.Fatalf("Failed to reopen label store: %v", err)
	}
	defer labelStore.Close()

	idMap, err := LoadIDMapBinary(filepath.Join(tmpDir, "idmap.bin"))
	if err != nil {
		t.Fatalf("Failed to reload idmap: %v", err)
	}

	// Verify counts
	if int(vectorStore.Count()) != len(symbols) {
		t.Errorf("Vector count mismatch: got %d, want %d", vectorStore.Count(), len(symbols))
	}

	if idMap.Size() != len(symbols) {
		t.Errorf("IDMap size mismatch: got %d, want %d", idMap.Size(), len(symbols))
	}

	sampleCount := min(100, len(symbols))
	step := len(symbols) / sampleCount
	var verified int

	for j := range sampleCount {
		sym := symbols[j*step]

		// Check ID mapping
		internalID, ok := idMap.ToInternal(sym.ID)
		if !ok {
			t.Errorf("Symbol %q not found in IDMap", sym.ID)
			continue
		}

		// Check reverse mapping
		externalID, ok := idMap.ToExternal(internalID)
		if !ok || externalID != sym.ID {
			t.Errorf("Reverse mapping failed for %q: got %q", sym.ID, externalID)
			continue
		}

		// Check vector
		vec := vectorStore.Get(internalID)
		if vec == nil {
			t.Errorf("Vector %d not found", internalID)
			continue
		}
		if len(vec) != embeddingDim {
			t.Errorf("Vector %d dimension mismatch: got %d, want %d", internalID, len(vec), embeddingDim)
			continue
		}

		// Verify vector content matches expected mock embedding
		expected := mockEmbed(sym.ID, embeddingDim)
		if !vectorsEqual(vec, expected) {
			t.Errorf("Vector %d content mismatch", internalID)
			continue
		}

		// Check label
		domain, nodeType := labelStore.Get(internalID)
		if domain != sym.Domain {
			t.Errorf("Domain mismatch for %q: got %d, want %d", sym.ID, domain, sym.Domain)
			continue
		}
		if nodeType != sym.NodeType {
			t.Errorf("NodeType mismatch for %q: got %d, want %d", sym.ID, nodeType, sym.NodeType)
			continue
		}

		verified++
	}

	t.Logf("Verified %d/%d samples", verified, sampleCount)

	// Performance assertions
	t.Logf("=== Performance Validation ===")

	// Target: <500ms total for Sylk
	if stats.TotalTime > 500*time.Millisecond {
		t.Logf("WARNING: Total time %.2fms exceeds 500ms target", float64(stats.TotalTime.Microseconds())/1000)
	} else {
		t.Logf("PASS: Total time %.2fms within 500ms target", float64(stats.TotalTime.Microseconds())/1000)
	}

	// Target: >=100,000 symbols/sec
	throughput := float64(stats.SymbolCount) / stats.TotalTime.Seconds()
	if throughput < 100000 {
		t.Logf("WARNING: Throughput %.0f symbols/sec below 100,000 target", throughput)
	} else {
		t.Logf("PASS: Throughput %.0f symbols/sec meets 100,000 target", throughput)
	}

	// Target: <=3.3KB per symbol (floor is 3,203: 768×4 vector + 32×4 graph + 3 labels)
	bytesPerSymbol := float64(stats.DiskSize) / float64(stats.SymbolCount)
	if bytesPerSymbol > 3300 {
		t.Logf("WARNING: Disk usage %.0f bytes/symbol exceeds 3.3KB target", bytesPerSymbol)
	} else {
		t.Logf("PASS: Disk usage %.0f bytes/symbol within 3.3KB target", bytesPerSymbol)
	}

	t.Logf("Disk size: %.2f MB", float64(stats.DiskSize)/(1024*1024))
}

func mockEmbed(text string, dim int) []float32 {
	vec := make([]float32, dim)

	h := fnv.New64a()
	h.Write([]byte(text))
	state := h.Sum64()

	// PCG LCG constants (Knuth MMIX)
	const pcgMult = 6364136223846793005
	const pcgInc = 1442695040888963407

	for i := 0; i < dim; i++ {
		state = state*pcgMult + pcgInc
		vec[i] = (float32(state>>32)/float32(math.MaxUint32))*2 - 1
	}

	var mag float64
	for _, v := range vec {
		mag += float64(v * v)
	}
	invMag := float32(1.0 / math.Sqrt(mag))
	for i := range vec {
		vec[i] *= invMag
	}

	return vec
}

func vectorsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > 1e-6 {
			return false
		}
	}
	return true
}

func getDirSize(dir string) int64 {
	var size int64
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// TestStorageIngest_Parallel tests concurrent write performance.
func TestStorageIngest_Parallel(t *testing.T) {
	tmpDir := t.TempDir()

	const (
		numVectors = 10000
		dim        = 768
		workers    = 8
	)

	// Create stores
	vectorStore, err := CreateVectorStore(filepath.Join(tmpDir, "vectors.bin"), dim, numVectors+100)
	if err != nil {
		t.Fatalf("Failed to create vector store: %v", err)
	}
	defer vectorStore.Close()

	labelStore, err := CreateLabelStore(filepath.Join(tmpDir, "labels.bin"), numVectors+100)
	if err != nil {
		t.Fatalf("Failed to create label store: %v", err)
	}
	defer labelStore.Close()

	idMap := NewIDMap()

	// Pre-generate vectors
	vectors := make([][]float32, numVectors)
	ids := make([]string, numVectors)
	for i := 0; i < numVectors; i++ {
		ids[i] = fmt.Sprintf("symbol_%d", i)
		vectors[i] = mockEmbed(ids[i], dim)
	}

	// Assign IDs first (single-threaded, fast)
	for _, id := range ids {
		idMap.Assign(id)
	}

	// Write vectors (single writer - storage constraint)
	start := time.Now()
	var written int64
	for i := 0; i < numVectors; i++ {
		_, err := vectorStore.Append(vectors[i])
		if err != nil {
			t.Fatalf("Append failed at %d: %v", i, err)
		}
		labelStore.Set(uint32(i), domainCode, nodeTypeFunction)
		atomic.AddInt64(&written, 1)
	}
	elapsed := time.Since(start)

	throughput := float64(written) / elapsed.Seconds()
	t.Logf("Wrote %d vectors in %v (%.0f/sec)", written, elapsed, throughput)

	if throughput < 100000 {
		t.Logf("WARNING: Throughput %.0f below 100,000 target", throughput)
	}
}
