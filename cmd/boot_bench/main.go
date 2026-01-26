package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

func main() {
	var rootPath string
	var workers int
	var batchSize int
	var skipCleanup bool
	var useVoyage bool

	flag.StringVar(&rootPath, "root", "", "Project root to index (defaults to current directory)")
	flag.IntVar(&workers, "workers", runtime.NumCPU(), "Number of parse workers")
	flag.IntVar(&batchSize, "batch", 64, "Embedding batch size")
	flag.BoolVar(&skipCleanup, "skip-cleanup", false, "Skip cleanup of .sylk directory after benchmark")
	flag.BoolVar(&useVoyage, "voyage", false, "Use Voyage API for embeddings (requires VOYAGE_API_KEY)")
	flag.Parse()

	if rootPath == "" {
		var err error
		rootPath, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get cwd: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("=== Boot Pipeline Benchmark ===\n")
	fmt.Printf("Root: %s\n", rootPath)
	fmt.Printf("Workers: %d\n", workers)
	fmt.Printf("Batch size: %d\n", batchSize)
	fmt.Printf("CPU cores: %d\n", runtime.NumCPU())
	fmt.Printf("Use Voyage: %v\n", useVoyage)
	fmt.Println()

	totalStart := time.Now()

	projectRoot, isGitRepo := boot.FindProjectRoot(rootPath)
	fmt.Printf("Project root: %s\n", projectRoot)
	fmt.Printf("Git repo: %v\n", isGitRepo)
	fmt.Println()

	sylkDir := boot.SylkDir(projectRoot)
	existingMeta := boot.MetaExists(projectRoot)
	if existingMeta {
		fmt.Println("Existing .sylk/meta.json found - will perform incremental update")
	} else {
		fmt.Println("No existing .sylk - will perform full bootstrap")
	}
	fmt.Println()

	fmt.Print("Phase 1: Scanning files... ")
	scanStart := time.Now()
	var goFiles []string
	var totalBytes int64
	filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "vendor" || base == "node_modules" || base == ".sylk" {
				return fs.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ingestion.GetLanguage(ext) != "" {
			goFiles = append(goFiles, path)
			if info, err := d.Info(); err == nil {
				totalBytes += info.Size()
			}
		}
		return nil
	})
	scanTime := time.Since(scanStart)
	fmt.Printf("%d files (%.2f MB) in %v\n", len(goFiles), float64(totalBytes)/(1024*1024), scanTime)

	fmt.Print("Phase 2: Parsing symbols... ")
	parseStart := time.Now()
	symbols, lineCount := parseAllSymbols(projectRoot, goFiles, workers)
	parseTime := time.Since(parseStart)
	fmt.Printf("%d symbols from %d lines in %v\n", len(symbols), lineCount, parseTime)
	fmt.Printf("         %.0f files/sec, %.0f lines/sec, %.2f MB/sec\n",
		float64(len(goFiles))/parseTime.Seconds(),
		float64(lineCount)/parseTime.Seconds(),
		float64(totalBytes)/(1024*1024)/parseTime.Seconds())

	fmt.Print("Phase 3: Creating embedder... ")
	embedderStart := time.Now()
	ctx := context.Background()

	var embResult *embedder.FactoryResult
	var embErr error
	if useVoyage {
		embResult, embErr = embedder.NewEmbedder(ctx, embedder.FactoryConfig{})
		if embErr != nil {
			fmt.Fprintf(os.Stderr, "failed to create embedder: %v\n", embErr)
			os.Exit(1)
		}
		if embResult.Source != "voyage-api" {
			fmt.Fprintf(os.Stderr, "voyage requested but got %s (check VOYAGE_API_KEY in .env)\n", embResult.Source)
			os.Exit(1)
		}
	} else {
		// Default: use hybrid-local (enhanced hybrid embedder ~50 MTEB)
		hybridTier := embedder.TierHybridLocal
		embResult, embErr = embedder.NewEmbedder(ctx, embedder.FactoryConfig{
			ForceTier: &hybridTier,
		})
		if embErr != nil {
			fmt.Fprintf(os.Stderr, "failed to create embedder: %v\n", embErr)
			os.Exit(1)
		}
	}
	embedderTime := time.Since(embedderStart)
	fmt.Printf("%s (dim=%d) in %v\n", embResult.Source, embResult.Embedder.Dimension(), embedderTime)

	fmt.Print("Phase 4: Serializing symbols... ")
	serializeStart := time.Now()
	serializer := boot.NewSimpleSerializer()
	texts := make([]string, len(symbols))
	for i, sym := range symbols {
		texts[i] = serializer.Serialize(sym.FilePath, sym.Kind, sym.Name, sym.Signature, sym.Body)
	}
	serializeTime := time.Since(serializeStart)
	var totalTextBytes int
	for _, t := range texts {
		totalTextBytes += len(t)
	}
	fmt.Printf("%d texts (%.2f MB) in %v\n", len(texts), float64(totalTextBytes)/(1024*1024), serializeTime)
	fmt.Printf("         %.0f symbols/sec\n", float64(len(texts))/serializeTime.Seconds())

	if useVoyage {
		batchSize = embedder.VoyageDefaultBatchSize
	}

	numBatches := (len(texts) + batchSize - 1) / batchSize

	fmt.Printf("Phase 5: Generating embeddings (batch=%d, batches=%d)...\n", batchSize, numBatches)
	embedStart := time.Now()
	var embeddedCount atomic.Int64
	var embedErrors atomic.Int64

	progressDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				count := embeddedCount.Load()
				elapsed := time.Since(embedStart)
				rate := float64(count) / elapsed.Seconds()
				pct := float64(count) / float64(len(texts)) * 100
				fmt.Printf("\r         %d/%d (%.1f%%) - %.0f emb/sec", count, len(texts), pct, rate)
			case <-progressDone:
				return
			}
		}
	}()

	embeddings := make([][]float32, len(texts))

	type batchJob struct {
		start int
		end   int
	}

	jobs := make(chan batchJob)

	var wg sync.WaitGroup
	go func() {
		for i := 0; i < len(texts); i += batchSize {
			end := i + batchSize
			if end > len(texts) {
				end = len(texts)
			}
			jobs <- batchJob{start: i, end: end}
		}
		close(jobs)
	}()

	for job := range jobs {
		wg.Add(1)
		go func(j batchJob) {
			defer wg.Done()
			batch := texts[j.start:j.end]
			batchEmb, err := embResult.Embedder.EmbedBatch(ctx, batch)
			if err != nil {
				embedErrors.Add(1)
				return
			}
			for i, emb := range batchEmb {
				embeddings[j.start+i] = emb
			}
			embeddedCount.Add(int64(len(batchEmb)))
		}(job)
	}

	wg.Wait()
	close(progressDone)
	embedTime := time.Since(embedStart)
	fmt.Printf("\r         %d embeddings in %v (%.0f emb/sec)\n",
		len(embeddings), embedTime, float64(len(embeddings))/embedTime.Seconds())

	if embedErrors.Load() > 0 {
		fmt.Printf("         WARNING: %d batch errors\n", embedErrors.Load())
	}

	if ve, ok := embResult.Embedder.(*embedder.VoyageEmbedder); ok {
		_, rateLimits, _ := ve.Stats()
		if rateLimits > 0 {
			fmt.Printf("         429 rate limits: %d\n", rateLimits)
		}
	}

	fmt.Print("Phase 6: Creating .sylk directory structure... ")
	structStart := time.Now()
	if err := boot.EnsureSylkDirs(projectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create dirs: %v\n", err)
		os.Exit(1)
	}
	structTime := time.Since(structStart)
	fmt.Printf("done in %v\n", structTime)

	fmt.Print("Phase 7: Writing meta.json... ")
	metaStart := time.Now()
	meta := boot.NewMeta(isGitRepo, embResult.Source)
	meta.UpdateStats(int64(len(symbols)), 0, int64(len(embeddings)))
	for _, sym := range symbols {
		meta.SetFileHash(sym.FilePath, "")
	}
	if err := meta.Save(projectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "failed to save meta: %v\n", err)
		os.Exit(1)
	}
	metaTime := time.Since(metaStart)
	fmt.Printf("done in %v\n", metaTime)

	totalTime := time.Since(totalStart)

	fmt.Println()
	fmt.Println("=== Summary ===")
	fmt.Printf("Total files:       %d\n", len(goFiles))
	fmt.Printf("Total bytes:       %.2f MB\n", float64(totalBytes)/(1024*1024))
	fmt.Printf("Total lines:       %d\n", lineCount)
	fmt.Printf("Total symbols:     %d\n", len(symbols))
	fmt.Printf("Total embeddings:  %d\n", len(embeddings))
	fmt.Println()
	fmt.Println("=== Timings ===")
	fmt.Printf("Phase 1 (scan):       %v\n", scanTime)
	fmt.Printf("Phase 2 (parse):      %v\n", parseTime)
	fmt.Printf("Phase 3 (embedder):   %v\n", embedderTime)
	fmt.Printf("Phase 4 (serialize):  %v\n", serializeTime)
	fmt.Printf("Phase 5 (embed):      %v\n", embedTime)
	fmt.Printf("Phase 6 (dirs):       %v\n", structTime)
	fmt.Printf("Phase 7 (meta):       %v\n", metaTime)
	fmt.Printf("Total:                %v\n", totalTime)
	fmt.Println()
	fmt.Println("=== Throughput ===")
	fmt.Printf("Scan:     %.0f files/sec\n", float64(len(goFiles))/scanTime.Seconds())
	fmt.Printf("Parse:    %.0f files/sec, %.0f lines/sec\n",
		float64(len(goFiles))/parseTime.Seconds(),
		float64(lineCount)/parseTime.Seconds())
	fmt.Printf("Embed:    %.0f symbols/sec\n", float64(len(embeddings))/embedTime.Seconds())
	fmt.Printf("Overall:  %.0f symbols/sec\n", float64(len(symbols))/totalTime.Seconds())
	fmt.Println()
	fmt.Println("=== Bottleneck Analysis ===")
	phases := []struct {
		name string
		dur  time.Duration
	}{
		{"scan", scanTime},
		{"parse", parseTime},
		{"embedder init", embedderTime},
		{"serialize", serializeTime},
		{"embed", embedTime},
		{"dirs", structTime},
		{"meta", metaTime},
	}
	var maxPhase string
	var maxDur time.Duration
	for _, p := range phases {
		if p.dur > maxDur {
			maxDur = p.dur
			maxPhase = p.name
		}
		pct := float64(p.dur) / float64(totalTime) * 100
		bar := ""
		barLen := int(pct / 2)
		for i := 0; i < barLen; i++ {
			bar += "█"
		}
		fmt.Printf("%-15s %6.1f%% %s\n", p.name+":", pct, bar)
	}
	fmt.Printf("\nBottleneck: %s (%.1f%% of total time)\n", maxPhase, float64(maxDur)/float64(totalTime)*100)

	if !skipCleanup {
		fmt.Printf("\nCleaning up %s...\n", sylkDir)
		os.RemoveAll(sylkDir)
	}
}

type parsedSymbol struct {
	FilePath  string
	Kind      string
	Name      string
	Signature string
	Body      string
}

func parseAllSymbols(root string, files []string, workers int) ([]parsedSymbol, int) {
	tool := treesitter.NewTreeSitterTool()
	ctx := context.Background()

	var symbols []parsedSymbol
	var mu sync.Mutex
	var totalLines atomic.Int64

	workCh := make(chan string, 1000)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local []parsedSymbol

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
				lines := countLines(content)
				totalLines.Add(int64(lines))

				for _, fn := range result.Functions {
					kind := "function"
					if fn.IsMethod {
						kind = "method"
					}
					sig := fn.Parameters
					if fn.ReturnType != "" {
						sig += " " + fn.ReturnType
					}
					body := extractBody(content, int(fn.StartLine), int(fn.EndLine))
					local = append(local, parsedSymbol{
						FilePath:  relPath,
						Kind:      kind,
						Name:      fn.Name,
						Signature: sig,
						Body:      body,
					})
				}

				for _, typ := range result.Types {
					local = append(local, parsedSymbol{
						FilePath:  relPath,
						Kind:      typ.Kind,
						Name:      typ.Name,
						Signature: "",
						Body:      "",
					})
				}
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

	return symbols, int(totalLines.Load())
}

func countLines(data []byte) int {
	count := 1
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	return count
}

func extractBody(data []byte, startLine, endLine int) string {
	lines := splitLines(data)
	if startLine < 1 {
		startLine = 1
	}
	if startLine > len(lines) {
		return ""
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}

	var result []byte
	for i := startLine - 1; i < endLine; i++ {
		result = append(result, lines[i]...)
		result = append(result, '\n')
	}

	if len(result) > 4000 {
		return string(result[:4000])
	}
	return string(result)
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
