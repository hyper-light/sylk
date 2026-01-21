package ingestion

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/search/bleve"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// Main Entry Point
// =============================================================================

// IngestCodebase performs sub-second codebase ingestion.
// Target: 1M LOC in < 1 second.
//
// Phases:
//  1. Discovery (<50ms): Parallel file discovery with gitignore
//  2. Read (<100ms): Memory-efficient file reading
//  3. Parse (<200ms): Parallel tree-sitter parsing
//  4. Aggregate (<50ms): Build CodeGraph
//  5. Persist (<200ms): Parallel SQLite + Bleve
//  6. Index (<100ms): Finalize indexes
func IngestCodebase(ctx context.Context, config *Config) (*IngestionResult, error) {
	config = config.WithDefaults()

	ingester := &ingester{
		config: config,
		result: &IngestionResult{},
	}

	return ingester.ingest(ctx)
}

type ingester struct {
	config *Config
	result *IngestionResult
	pool   *ParserPool

	files  []FileInfo
	mapped []MappedFile
	parsed []ParsedFile
	graph  *CodeGraph
	errors []FileParseError
}

// ingest executes the full ingestion pipeline.
func (i *ingester) ingest(ctx context.Context) (*IngestionResult, error) {
	start := time.Now()

	// Phase 1: Discovery
	if err := i.runDiscovery(ctx); err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}

	// Phase 2: Read
	if err := i.runRead(ctx); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	// Phase 3: Parse
	if err := i.runParse(ctx); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	// Phase 4: Aggregate
	i.runAggregate()

	// Phase 5: Persist (optional)
	if !i.config.SkipPersist {
		if err := i.runPersist(ctx); err != nil {
			return nil, fmt.Errorf("persist: %w", err)
		}
	}

	// Finalize result
	i.finalizeResult(start)

	return i.result, nil
}

// =============================================================================
// Phase Implementations
// =============================================================================

// runDiscovery executes the file discovery phase.
func (i *ingester) runDiscovery(ctx context.Context) error {
	start := time.Now()

	files, err := DiscoverFiles(ctx, i.config.RootPath, i.config.IgnorePatterns)
	if err != nil {
		return err
	}

	i.files = files
	i.result.PhaseDurations.Discovery = time.Since(start)
	return nil
}

// runRead executes the file reading phase.
func (i *ingester) runRead(ctx context.Context) error {
	start := time.Now()

	mapped, err := ReadFiles(ctx, i.files, i.config.Workers)
	if err != nil {
		return err
	}

	i.mapped = mapped
	i.result.PhaseDurations.Mmap = time.Since(start)
	return nil
}

// runParse executes the parallel parsing phase.
func (i *ingester) runParse(ctx context.Context) error {
	start := time.Now()

	pool := NewParserPool(i.config.Workers)
	defer pool.Close()

	parsed, errors := pool.ParseAll(ctx, i.mapped)

	i.parsed = parsed
	i.errors = errors
	i.result.PhaseDurations.Parse = time.Since(start)
	return nil
}

// runAggregate executes the graph aggregation phase.
func (i *ingester) runAggregate() {
	start := time.Now()

	i.graph = Aggregate(i.config.RootPath, i.mapped, i.parsed)

	i.result.PhaseDurations.Aggregate = time.Since(start)
}

// runPersist executes the persistence phase.
func (i *ingester) runPersist(ctx context.Context) error {
	start := time.Now()

	sqlite, bleveManager, err := i.openDatabases()
	if err != nil {
		return err
	}
	defer i.closeDatabases(sqlite, bleveManager)

	persister := NewPersister(sqlite, bleveManager)
	if err := persister.Persist(ctx, i.graph); err != nil {
		return err
	}

	i.result.PhaseDurations.Persist = time.Since(start)
	return nil
}

// openDatabases opens SQLite and Bleve connections.
func (i *ingester) openDatabases() (*vectorgraphdb.VectorGraphDB, *bleve.IndexManager, error) {
	var sqlite *vectorgraphdb.VectorGraphDB
	var bleveManager *bleve.IndexManager
	var err error

	// Open SQLite
	if i.config.SQLitePath != "" {
		sqlite, err = vectorgraphdb.Open(i.config.SQLitePath)
		if err != nil {
			return nil, nil, fmt.Errorf("open sqlite: %w", err)
		}
	}

	// Open Bleve
	if !i.config.SkipBleve && i.config.BlevePath != "" {
		bleveManager = bleve.NewIndexManager(i.config.BlevePath)
		if err := bleveManager.Open(); err != nil {
			if sqlite != nil {
				sqlite.Close()
			}
			return nil, nil, fmt.Errorf("open bleve: %w", err)
		}
	}

	return sqlite, bleveManager, nil
}

// closeDatabases closes database connections.
func (i *ingester) closeDatabases(sqlite *vectorgraphdb.VectorGraphDB, bleveManager *bleve.IndexManager) {
	if sqlite != nil {
		sqlite.Close()
	}
	if bleveManager != nil {
		bleveManager.Close()
	}
}

// finalizeResult populates the result with final metrics.
func (i *ingester) finalizeResult(start time.Time) {
	i.result.Graph = i.graph
	i.result.TotalFiles = len(i.mapped)
	i.result.TotalSymbols = len(i.graph.Symbols)
	i.result.TotalLines = i.graph.TotalLines
	i.result.TotalBytes = i.graph.TotalBytes
	i.result.TotalDuration = time.Since(start)
	i.result.ParseErrors = i.errors
}

// =============================================================================
// Convenience Functions
// =============================================================================

// IngestDirectory is a convenience function for simple directory ingestion.
// Uses default configuration with only the root path specified.
func IngestDirectory(ctx context.Context, rootPath string) (*IngestionResult, error) {
	return IngestCodebase(ctx, &Config{
		RootPath:    rootPath,
		SkipPersist: true,
	})
}

// IngestAndPersist ingests a directory and persists to the specified databases.
func IngestAndPersist(ctx context.Context, rootPath, sqlitePath, blevePath string) (*IngestionResult, error) {
	return IngestCodebase(ctx, &Config{
		RootPath:   rootPath,
		SQLitePath: sqlitePath,
		BlevePath:  blevePath,
	})
}

// QuickIngest performs a quick ingestion for testing purposes.
// Skips persistence and uses minimal configuration.
func QuickIngest(ctx context.Context, rootPath string) (*CodeGraph, error) {
	result, err := IngestDirectory(ctx, rootPath)
	if err != nil {
		return nil, err
	}
	return result.Graph, nil
}

// =============================================================================
// Metrics
// =============================================================================

// Metrics returns human-readable metrics about the ingestion.
func (r *IngestionResult) Metrics() string {
	linesPerSecond := float64(r.TotalLines) / r.TotalDuration.Seconds()
	bytesPerSecond := float64(r.TotalBytes) / r.TotalDuration.Seconds()
	mbPerSecond := bytesPerSecond / (1024 * 1024)

	return fmt.Sprintf(
		"Ingested %d files, %d symbols, %d lines (%.2f MB) in %v\n"+
			"Throughput: %.0f lines/s, %.2f MB/s\n"+
			"Phases: discover=%v read=%v parse=%v aggregate=%v persist=%v",
		r.TotalFiles,
		r.TotalSymbols,
		r.TotalLines,
		float64(r.TotalBytes)/(1024*1024),
		r.TotalDuration.Round(time.Millisecond),
		linesPerSecond,
		mbPerSecond,
		r.PhaseDurations.Discovery.Round(time.Millisecond),
		r.PhaseDurations.Mmap.Round(time.Millisecond),
		r.PhaseDurations.Parse.Round(time.Millisecond),
		r.PhaseDurations.Aggregate.Round(time.Millisecond),
		r.PhaseDurations.Persist.Round(time.Millisecond),
	)
}

// MeetsTarget returns true if the ingestion met the target duration.
func (r *IngestionResult) MeetsTarget() bool {
	return r.TotalDuration < TotalTargetDuration
}

// LinesPerSecond returns the throughput in lines per second.
func (r *IngestionResult) LinesPerSecond() float64 {
	return float64(r.TotalLines) / r.TotalDuration.Seconds()
}

// MBPerSecond returns the throughput in megabytes per second.
func (r *IngestionResult) MBPerSecond() float64 {
	return float64(r.TotalBytes) / (1024 * 1024) / r.TotalDuration.Seconds()
}
