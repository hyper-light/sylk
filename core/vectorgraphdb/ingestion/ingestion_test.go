package ingestion

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestIngestSylkCodebase(t *testing.T) {
	// Get the project root (3 levels up from this test file)
	projectRoot := "../../../"

	// Skip if running in CI without the full codebase
	if _, err := os.Stat(projectRoot + "go.mod"); os.IsNotExist(err) {
		t.Skip("Skipping: not in full codebase")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := IngestDirectory(ctx, projectRoot)
	if err != nil {
		t.Fatalf("Ingestion failed: %v", err)
	}

	// Print metrics
	t.Log(result.Metrics())

	// Verify we found files
	if result.TotalFiles == 0 {
		t.Error("Expected to find files")
	}

	// Verify we found symbols
	if result.TotalSymbols == 0 {
		t.Error("Expected to find symbols")
	}

	// Log performance
	t.Logf("Lines/second: %.0f", result.LinesPerSecond())
	t.Logf("MB/second: %.2f", result.MBPerSecond())

	// Check if we met the target (informational, not a failure)
	if result.MeetsTarget() {
		t.Logf("SUCCESS: Met sub-second target (%v < %v)", result.TotalDuration, TotalTargetDuration)
	} else {
		t.Logf("INFO: Did not meet sub-second target (%v >= %v)", result.TotalDuration, TotalTargetDuration)
	}
}

func TestDiscoverFiles(t *testing.T) {
	projectRoot := "../../../"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	files, err := DiscoverFiles(ctx, projectRoot, nil)
	if err != nil {
		t.Fatalf("Discovery failed: %v", err)
	}

	if len(files) == 0 {
		t.Error("Expected to find files")
	}

	// Count by extension
	extCounts := make(map[string]int)
	for _, f := range files {
		ext := extractExtension(f.Path)
		extCounts[ext]++
	}

	t.Logf("Discovered %d files", len(files))
	for ext, count := range extCounts {
		t.Logf("  %s: %d", ext, count)
	}
}

func TestParseFiles(t *testing.T) {
	projectRoot := "../../../"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Discover
	files, err := DiscoverFiles(ctx, projectRoot, nil)
	if err != nil {
		t.Fatalf("Discovery failed: %v", err)
	}

	// Read
	mapped, err := ReadFiles(ctx, files, WorkerCount())
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// Parse
	pool := NewParserPool(WorkerCount())
	defer pool.Close()

	start := time.Now()
	parsed, errors := pool.ParseAll(ctx, mapped)
	duration := time.Since(start)

	t.Logf("Parsed %d files in %v", len(parsed), duration)
	t.Logf("Parse errors: %d", len(errors))

	// Count symbols
	totalSymbols := 0
	for _, p := range parsed {
		totalSymbols += len(p.Symbols)
	}
	t.Logf("Total symbols: %d", totalSymbols)

	if len(errors) > 0 {
		t.Logf("First 5 errors:")
		for i, e := range errors {
			if i >= 5 {
				break
			}
			t.Logf("  %s: %s", e.Path, e.Error)
		}
	}
}

func TestAggregate(t *testing.T) {
	projectRoot := "../../../"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Full pipeline up to aggregation
	files, _ := DiscoverFiles(ctx, projectRoot, nil)
	mapped, _ := ReadFiles(ctx, files, WorkerCount())

	pool := NewParserPool(WorkerCount())
	defer pool.Close()
	parsed, _ := pool.ParseAll(ctx, mapped)

	// Aggregate
	start := time.Now()
	graph := Aggregate(projectRoot, mapped, parsed)
	duration := time.Since(start)

	t.Logf("Aggregated in %v", duration)
	t.Logf("Files: %d", len(graph.Files))
	t.Logf("Symbols: %d", len(graph.Symbols))
	t.Logf("Import edges: %d", len(graph.ImportEdges))
	t.Logf("Contains edges: %d", len(graph.ContainsEdges))
	t.Logf("Total lines: %d", graph.TotalLines)
	t.Logf("Total bytes: %d", graph.TotalBytes)
}

// BenchmarkIngest benchmarks the full ingestion pipeline.
func BenchmarkIngest(b *testing.B) {
	projectRoot := "../../../"

	ctx := context.Background()

	// Warm up
	_, _ = IngestDirectory(ctx, projectRoot)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := IngestDirectory(ctx, projectRoot)
		if err != nil {
			b.Fatalf("Ingestion failed: %v", err)
		}
		b.ReportMetric(float64(result.TotalLines), "lines")
		b.ReportMetric(result.LinesPerSecond(), "lines/s")
	}
}

// BenchmarkDiscovery benchmarks file discovery.
func BenchmarkDiscovery(b *testing.B) {
	projectRoot := "../../../"
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		files, err := DiscoverFiles(ctx, projectRoot, nil)
		if err != nil {
			b.Fatalf("Discovery failed: %v", err)
		}
		b.ReportMetric(float64(len(files)), "files")
	}
}

// BenchmarkParsing benchmarks parallel parsing.
func BenchmarkParsing(b *testing.B) {
	projectRoot := "../../../"
	ctx := context.Background()

	// Prepare files once
	files, _ := DiscoverFiles(ctx, projectRoot, nil)
	mapped, _ := ReadFiles(ctx, files, WorkerCount())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool := NewParserPool(WorkerCount())
		parsed, _ := pool.ParseAll(ctx, mapped)
		pool.Close()

		totalSymbols := 0
		for _, p := range parsed {
			totalSymbols += len(p.Symbols)
		}
		b.ReportMetric(float64(totalSymbols), "symbols")
	}
}

// Example_ingestCodebase shows how to use the ingestion package.
func Example_ingestCodebase() {
	ctx := context.Background()

	result, err := IngestDirectory(ctx, ".")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Files: %d\n", result.TotalFiles)
	fmt.Printf("Symbols: %d\n", result.TotalSymbols)
	fmt.Printf("Lines: %d\n", result.TotalLines)
	fmt.Printf("Duration: %v\n", result.TotalDuration)
}
