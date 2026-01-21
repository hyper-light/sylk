// Package ingestion provides sub-second codebase ingestion for the VectorGraphDB.
// It implements parallel file discovery, tree-sitter parsing, and bulk persistence
// to achieve <1 second ingestion of 1M+ lines of code.
package ingestion

import (
	"runtime"
	"time"
)

// =============================================================================
// Physical Constraints (derived from hardware characteristics)
// =============================================================================

// Derived from NVMe SSD sequential read: ~3GB/s, 50MB in ~17ms
// Adding overhead for syscalls and file metadata: ~50ms target.
const (
	// TargetDiscoveryDuration is the target time for file discovery phase.
	// Based on: 10K files @ ~5us/stat = 50ms.
	TargetDiscoveryDuration = 50 * time.Millisecond

	// TargetMmapDuration is the target time for memory-mapping files.
	// Based on: 50MB @ 3GB/s = ~17ms, with mmap overhead = ~100ms.
	TargetMmapDuration = 100 * time.Millisecond

	// TargetParseDuration is the target time for parallel parsing.
	// Based on: tree-sitter parses ~500KB/core/second.
	// 50MB / 16 cores = ~3MB/core @ 500KB/s = ~6ms, with overhead = ~200ms.
	TargetParseDuration = 200 * time.Millisecond

	// TargetAggregateDuration is the target time for graph aggregation.
	// Based on: 100K symbols @ ~500ns/symbol = 50ms.
	TargetAggregateDuration = 50 * time.Millisecond

	// TargetPersistDuration is the target time for parallel persistence.
	// Based on: SQLite bulk insert ~100K rows @ ~1us/row = 100ms.
	// Bleve batch index ~10K docs @ ~10us/doc = 100ms.
	// Running in parallel = max(100ms, 100ms) = ~100ms, with overhead = ~200ms.
	TargetPersistDuration = 200 * time.Millisecond

	// TargetIndexDuration is the target time for index finalization.
	// Based on: SQLite CREATE INDEX on 100K rows = ~50-100ms.
	TargetIndexDuration = 100 * time.Millisecond

	// TotalTargetDuration is the sum of all phase targets.
	// 50 + 100 + 200 + 50 + 200 + 100 = 700ms < 1s.
	TotalTargetDuration = 700 * time.Millisecond
)

// =============================================================================
// Derived Capacity Estimates (from typical codebase characteristics)
// =============================================================================

// CapacityEstimate holds derived capacity values based on codebase size.
type CapacityEstimate struct {
	// Files is the estimated number of files.
	Files int
	// Symbols is the estimated number of symbols (functions, types, etc.).
	Symbols int
	// Imports is the estimated number of import edges.
	Imports int
	// TotalBytes is the estimated total bytes of source code.
	TotalBytes int64
}

// EstimateCapacity derives capacity estimates from total lines of code.
// These ratios are derived from analysis of real codebases.
func EstimateCapacity(totalLOC int) CapacityEstimate {
	// Average lines per file: ~100 (derived from Go stdlib, Linux kernel analysis)
	avgLinesPerFile := 100
	// Average symbols per file: ~10 (functions + types + constants)
	avgSymbolsPerFile := 10
	// Average imports per file: ~5
	avgImportsPerFile := 5
	// Average bytes per line: ~50 (including whitespace)
	avgBytesPerLine := 50

	files := totalLOC / avgLinesPerFile
	if files < 1 {
		files = 1
	}

	return CapacityEstimate{
		Files:      files,
		Symbols:    files * avgSymbolsPerFile,
		Imports:    files * avgImportsPerFile,
		TotalBytes: int64(totalLOC * avgBytesPerLine),
	}
}

// =============================================================================
// Concurrency Configuration (derived from CPU count)
// =============================================================================

// WorkerCount returns the number of workers for parallel operations.
// Derived from runtime.NumCPU() to maximize CPU utilization.
func WorkerCount() int {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	return n
}

// ChannelBufferSize returns the buffer size for channels based on file count.
// Uses file count to prevent channel blocking while avoiding excessive memory.
func ChannelBufferSize(fileCount int) int {
	// Buffer at least WorkerCount() * 2 to prevent starvation
	minBuffer := WorkerCount() * 2
	// But cap at file count to avoid wasting memory
	if fileCount < minBuffer {
		return minBuffer
	}
	return fileCount
}

// =============================================================================
// SQLite Optimization Parameters (derived from Bleve and SQLite documentation)
// =============================================================================

const (
	// SQLiteCacheSizeKB is the SQLite cache size in KB.
	// 64MB cache for bulk operations (derived from SQLite recommendations for bulk insert).
	SQLiteCacheSizeKB = 64 * 1024

	// SQLitePageSize is the SQLite page size in bytes.
	// 4KB is optimal for most SSDs (derived from typical SSD block size).
	SQLitePageSize = 4096

	// SQLiteBatchSize is the number of rows per transaction batch.
	// 1000 rows balances memory usage vs transaction overhead.
	// Derived from: 1 row commit = ~1ms, 1000 rows = ~10ms (100x improvement).
	SQLiteBatchSize = 1000
)

// =============================================================================
// Bleve Optimization Parameters
// =============================================================================

const (
	// BleveBatchSize is the number of documents per Bleve batch.
	// Derived from Bleve documentation: optimal batch size 100-1000.
	BleveBatchSize = 500
)

// =============================================================================
// File Size Thresholds (derived from typical source file characteristics)
// =============================================================================

const (
	// SmallFileThreshold is the max size for a "small" file in bytes.
	// Files below this are batched together for efficient processing.
	// Derived from: average Go file is ~5KB, batch files < 10KB.
	SmallFileThreshold = 10 * 1024 // 10KB

	// LargeFileThreshold is the size above which a file gets dedicated processing.
	// Derived from: files > 100KB are rare and warrant individual attention.
	LargeFileThreshold = 100 * 1024 // 100KB

	// MaxFileSizeBytes is the maximum file size to process.
	// Derived from: source files > 1MB are usually generated/vendor code.
	MaxFileSizeBytes = 1 * 1024 * 1024 // 1MB
)

// =============================================================================
// Language Extensions (derived from common programming language file extensions)
// =============================================================================

// SupportedLanguages maps file extensions to language names.
// These must match the tree-sitter grammar names.
var SupportedLanguages = map[string]string{
	".go":    "go",
	".rs":    "rust",
	".py":    "python",
	".pyi":   "python",
	".js":    "javascript",
	".mjs":   "javascript",
	".cjs":   "javascript",
	".jsx":   "javascript",
	".ts":    "typescript",
	".mts":   "typescript",
	".tsx":   "tsx",
	".java":  "java",
	".c":     "c",
	".h":     "c",
	".cpp":   "cpp",
	".cc":    "cpp",
	".cxx":   "cpp",
	".hpp":   "cpp",
	".hxx":   "cpp",
	".rb":    "ruby",
	".rake":  "ruby",
	".swift": "swift",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".json":  "json",
	".yaml":  "yaml",
	".yml":   "yaml",
	".toml":  "toml",
	".html":  "html",
	".css":   "css",
	".bash":  "bash",
	".sh":    "bash",
	".md":    "markdown",
}

// IsSupportedExtension returns true if the extension is supported.
func IsSupportedExtension(ext string) bool {
	_, ok := SupportedLanguages[ext]
	return ok
}

// GetLanguage returns the language name for the given extension.
// Returns empty string if not supported.
func GetLanguage(ext string) string {
	return SupportedLanguages[ext]
}
