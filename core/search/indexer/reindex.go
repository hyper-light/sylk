// Package indexer provides file scanning and indexing functionality for the Sylk
// Document Search System. This file implements the full reindex pipeline for
// complete index rebuilds.
package indexer

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/parser"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// ReindexDefaultBatchSize is the default number of documents per batch.
	ReindexDefaultBatchSize = 50

	// ReindexDefaultMaxConcurrency is the default maximum concurrent operations.
	ReindexDefaultMaxConcurrency = 4
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrNilIndexManager indicates a nil index manager was provided.
	ErrNilIndexManager = errors.New("index manager cannot be nil")
)

// =============================================================================
// ReindexProgressCallback
// =============================================================================

// ReindexProgressCallback is called to report reindex progress.
type ReindexProgressCallback func(progress IndexingProgress)

// IndexingProgress contains progress information during indexing.
type IndexingProgress struct {
	// Total is the estimated total number of files to process.
	Total int

	// Processed is the number of files processed so far.
	Processed int

	// Indexed is the number of files successfully indexed.
	Indexed int

	// Failed is the number of files that failed to index.
	Failed int

	// Current is the path of the file currently being processed.
	Current string
}

// =============================================================================
// DestroyableIndex Interface
// =============================================================================

// DestroyableIndex represents an index that can be destroyed and recreated.
type DestroyableIndex interface {
	// Index indexes a single document.
	Index(ctx context.Context, doc *search.Document) error

	// IndexBatch indexes multiple documents in a batch.
	IndexBatch(ctx context.Context, docs []*search.Document) error

	// DocumentCount returns the number of indexed documents.
	DocumentCount() (uint64, error)

	// IsOpen returns true if the index is open.
	IsOpen() bool

	// Destroy closes and removes the index from disk.
	Destroy() error

	// Open opens or creates the index.
	Open() error

	// Close closes the index.
	Close() error
}

// =============================================================================
// ReindexConfig
// =============================================================================

// ReindexConfig configures the full reindex pipeline.
type ReindexConfig struct {
	// RootPath is the directory to scan for files.
	RootPath string

	// BatchSize is the number of documents to batch before indexing.
	BatchSize int

	// MaxConcurrency is the maximum concurrent parsing operations.
	MaxConcurrency int

	// ScanConfig provides custom scanning configuration.
	ScanConfig ScanConfig

	// Progress is called to report indexing progress.
	Progress ReindexProgressCallback
}

// =============================================================================
// FullReindexer
// =============================================================================

// FullReindexer handles complete index rebuilds.
type FullReindexer struct {
	config       ReindexConfig
	indexManager DestroyableIndex
}

// NewFullReindexer creates a new full reindex pipeline.
func NewFullReindexer(config ReindexConfig, indexManager DestroyableIndex) (*FullReindexer, error) {
	if err := validateReindexConfig(&config); err != nil {
		return nil, err
	}

	if indexManager == nil {
		return nil, ErrNilIndexManager
	}

	applyReindexDefaults(&config)

	return &FullReindexer{
		config:       config,
		indexManager: indexManager,
	}, nil
}

// validateReindexConfig checks that the configuration is valid.
func validateReindexConfig(config *ReindexConfig) error {
	if config.RootPath == "" {
		return ErrRootPathEmpty
	}
	return nil
}

// applyReindexDefaults sets default values for unspecified config fields.
func applyReindexDefaults(config *ReindexConfig) {
	if config.BatchSize <= 0 {
		config.BatchSize = ReindexDefaultBatchSize
	}
	if config.MaxConcurrency <= 0 {
		config.MaxConcurrency = ReindexDefaultMaxConcurrency
	}
}

// =============================================================================
// Reindex Method
// =============================================================================

// Reindex performs a complete index rebuild.
// 1. Destroys existing index
// 2. Creates fresh index
// 3. Scans all files
// 4. Indexes all documents in batches
// Returns partial results if context is cancelled.
func (f *FullReindexer) Reindex(ctx context.Context) (*search.IndexingResult, error) {
	startTime := time.Now()

	if err := f.destroyAndRecreateIndex(); err != nil {
		return nil, err
	}

	result := f.indexAllFiles(ctx)
	result.Duration = time.Since(startTime)

	return result, nil
}

// destroyAndRecreateIndex removes the existing index and creates a fresh one.
func (f *FullReindexer) destroyAndRecreateIndex() error {
	if err := f.indexManager.Destroy(); err != nil {
		return err
	}
	return f.indexManager.Open()
}

// =============================================================================
// File Indexing
// =============================================================================

// indexAllFiles scans and indexes all files in the root path.
func (f *FullReindexer) indexAllFiles(ctx context.Context) *search.IndexingResult {
	result := search.NewIndexingResult()

	fileCh, err := f.startScanner(ctx)
	if err != nil {
		return result
	}

	f.processFiles(ctx, fileCh, result)

	return result
}

// startScanner creates and starts the file scanner.
func (f *FullReindexer) startScanner(ctx context.Context) (<-chan *FileInfo, error) {
	scanConfig := f.buildScanConfig()
	scanner := NewScanner(scanConfig)
	return scanner.Scan(ctx)
}

// buildScanConfig creates the scan configuration.
func (f *FullReindexer) buildScanConfig() ScanConfig {
	config := f.config.ScanConfig
	config.RootPath = f.config.RootPath

	return config
}

// processFiles iterates over scanned files and indexes them in batches.
func (f *FullReindexer) processFiles(
	ctx context.Context,
	fileCh <-chan *FileInfo,
	result *search.IndexingResult,
) {
	batch := make([]*search.Document, 0, f.config.BatchSize)
	processed := 0

	for fileInfo := range fileCh {
		if reindexIsCancelled(ctx) {
			break
		}

		batch, processed = f.processOneFile(ctx, fileInfo, batch, processed, result)
	}

	f.flushRemainingBatch(ctx, batch, result)
}

// processOneFile handles a single file in the reindex pipeline.
func (f *FullReindexer) processOneFile(
	ctx context.Context,
	fileInfo *FileInfo,
	batch []*search.Document,
	processed int,
	result *search.IndexingResult,
) ([]*search.Document, int) {
	doc, err := f.parseFile(fileInfo)
	if err != nil {
		f.recordFailure(result, fileInfo.Path, err)
		f.reportProgress(processed+1, result)
		return batch, processed + 1
	}

	batch = append(batch, doc)
	batch = f.flushBatchIfFull(ctx, batch, result)
	f.reportProgress(processed+1, result)

	return batch, processed + 1
}

// flushBatchIfFull flushes the batch if it has reached capacity.
func (f *FullReindexer) flushBatchIfFull(
	ctx context.Context,
	batch []*search.Document,
	result *search.IndexingResult,
) []*search.Document {
	if len(batch) < f.config.BatchSize {
		return batch
	}

	f.flushBatch(ctx, batch, result)
	return batch[:0]
}

// flushRemainingBatch flushes any remaining documents in the batch.
func (f *FullReindexer) flushRemainingBatch(
	ctx context.Context,
	batch []*search.Document,
	result *search.IndexingResult,
) {
	if len(batch) > 0 {
		f.flushBatch(ctx, batch, result)
	}
}

// reindexIsCancelled checks if the context has been cancelled.
func reindexIsCancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// =============================================================================
// File Parsing
// =============================================================================

// parseFile reads and parses a file into a Document.
func (f *FullReindexer) parseFile(fileInfo *FileInfo) (*search.Document, error) {
	content, err := readFileContent(fileInfo.Path)
	if err != nil {
		return nil, err
	}

	doc := buildDocument(fileInfo, content)
	enrichWithParser(doc, fileInfo, content)

	return doc, nil
}

// readFileContent reads the entire file content.
func readFileContent(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

// buildDocument creates a basic Document from file info and content.
func buildDocument(fileInfo *FileInfo, content []byte) *search.Document {
	return &search.Document{
		ID:         search.GenerateDocumentID(content),
		Path:       fileInfo.Path,
		Type:       determineDocumentType(fileInfo.Extension),
		Content:    string(content),
		Checksum:   search.GenerateChecksum(content),
		ModifiedAt: fileInfo.ModTime,
		IndexedAt:  time.Now(),
	}
}

// determineDocumentType maps file extension to document type.
func determineDocumentType(extension string) search.DocumentType {
	switch extension {
	case ".md", ".markdown":
		return search.DocTypeMarkdown
	case ".json", ".yaml", ".yml", ".toml":
		return search.DocTypeConfig
	default:
		return search.DocTypeSourceCode
	}
}

// enrichWithParser adds symbols, comments, and imports from the parser.
func enrichWithParser(doc *search.Document, fileInfo *FileInfo, content []byte) {
	p := parser.GetParserForFile(fileInfo.Path)
	if p == nil {
		return
	}

	result, err := p.Parse(content, fileInfo.Path)
	if err != nil {
		return
	}

	doc.Comments = result.Comments
	doc.Imports = result.Imports
	doc.Symbols = extractSymbolNames(result.Symbols)
	doc.Language = detectLanguage(fileInfo.Extension)
}

// extractSymbolNames converts parser symbols to string slice.
func extractSymbolNames(symbols []parser.Symbol) []string {
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	return names
}

// detectLanguage maps file extension to language name.
func detectLanguage(extension string) string {
	languages := map[string]string{
		".go":   "go",
		".py":   "python",
		".ts":   "typescript",
		".tsx":  "typescript",
		".js":   "javascript",
		".jsx":  "javascript",
		".rs":   "rust",
		".java": "java",
		".c":    "c",
		".cpp":  "cpp",
		".h":    "c",
		".hpp":  "cpp",
	}

	if lang, ok := languages[extension]; ok {
		return lang
	}
	return ""
}

// =============================================================================
// Batch Operations
// =============================================================================

// flushBatch indexes a batch of documents.
func (f *FullReindexer) flushBatch(
	ctx context.Context,
	batch []*search.Document,
	result *search.IndexingResult,
) {
	for _, doc := range batch {
		if reindexIsCancelled(ctx) {
			return
		}

		err := f.indexManager.Index(ctx, doc)
		if err != nil {
			f.recordFailure(result, doc.Path, err)
		} else {
			result.AddSuccess()
		}
	}
}

// recordFailure adds a failure to the result.
func (f *FullReindexer) recordFailure(result *search.IndexingResult, path string, err error) {
	result.AddFailure("", path, err.Error(), true)
}

// =============================================================================
// Progress Reporting
// =============================================================================

// reportProgress calls the progress callback if configured.
func (f *FullReindexer) reportProgress(processed int, result *search.IndexingResult) {
	if f.config.Progress == nil {
		return
	}

	f.config.Progress(IndexingProgress{
		Total:     0, // Unknown until scan completes
		Processed: processed,
		Indexed:   result.Indexed,
		Failed:    result.Failed,
	})
}
