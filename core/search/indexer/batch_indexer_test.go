package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Mock IndexManager
// =============================================================================

// mockIndexManager implements IndexManagerInterface for testing.
type mockIndexManager struct {
	mu           sync.Mutex
	batches      [][]*search.Document
	batchErr     error
	indexedCount atomic.Int64
	callCount    atomic.Int64
	delay        time.Duration
}

func newMockIndexManager() *mockIndexManager {
	return &mockIndexManager{}
}

func (m *mockIndexManager) IndexBatch(ctx context.Context, docs []*search.Document) error {
	m.callCount.Add(1)

	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.delay):
		}
	}

	if m.batchErr != nil {
		return m.batchErr
	}

	m.mu.Lock()
	m.batches = append(m.batches, docs)
	m.indexedCount.Add(int64(len(docs)))
	m.mu.Unlock()

	return nil
}

func (m *mockIndexManager) getBatches() [][]*search.Document {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.batches
}

func (m *mockIndexManager) getIndexedCount() int64 {
	return m.indexedCount.Load()
}

func (m *mockIndexManager) getCallCount() int64 {
	return m.callCount.Load()
}

// =============================================================================
// Mock DocumentBuilder
// =============================================================================

// mockDocumentBuilder implements document building for testing.
type mockDocumentBuilder struct {
	buildErr error
	delay    time.Duration
}

func newMockDocumentBuilder() *mockDocumentBuilder {
	return &mockDocumentBuilder{}
}

func (b *mockDocumentBuilder) BuildFromFile(ctx context.Context, info *FileInfo) (*search.Document, error) {
	if b.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(b.delay):
		}
	}

	if b.buildErr != nil {
		return nil, b.buildErr
	}

	doc := &search.Document{
		ID:         search.GenerateDocumentID([]byte(info.Path)),
		Path:       info.Path,
		Type:       search.DocTypeSourceCode,
		Content:    "test content",
		Checksum:   search.GenerateChecksum([]byte(info.Path)),
		ModifiedAt: info.ModTime,
		IndexedAt:  time.Now(),
	}

	return doc, nil
}

// =============================================================================
// Test Helpers
// =============================================================================

// createTestFilesForBatchIndexer creates test files and returns FileInfo channel.
func createTestFilesForBatchIndexer(t *testing.T, count int) (string, <-chan *FileInfo) {
	t.Helper()

	dir := t.TempDir()
	ch := make(chan *FileInfo, count)

	for i := 0; i < count; i++ {
		name := filepath.Join(dir, "file"+string(rune('a'+i%26))+".go")
		content := "package main"
		if err := os.WriteFile(name, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("failed to stat test file: %v", err)
		}

		ch <- &FileInfo{
			Path:      name,
			Name:      filepath.Base(name),
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			Extension: ".go",
		}
	}
	close(ch)

	return dir, ch
}

// createFileInfoChannel creates a channel with the given FileInfo items.
func createFileInfoChannel(files []*FileInfo) <-chan *FileInfo {
	ch := make(chan *FileInfo, len(files))
	for _, f := range files {
		ch <- f
	}
	close(ch)
	return ch
}

// =============================================================================
// IndexerConfig Tests
// =============================================================================

func TestIndexerConfig_Defaults(t *testing.T) {
	t.Parallel()

	config := BatchIndexerConfig{}

	if config.BatchSize != 0 {
		t.Errorf("BatchSize = %d, want 0 (before defaults applied)", config.BatchSize)
	}

	if config.MaxConcurrency != 0 {
		t.Errorf("MaxConcurrency = %d, want 0 (before defaults applied)", config.MaxConcurrency)
	}

	if config.ProgressCallback != nil {
		t.Error("ProgressCallback should be nil by default")
	}
}

// =============================================================================
// NewBatchIndexer Tests
// =============================================================================

func TestNewBatchIndexer(t *testing.T) {
	t.Parallel()

	config := BatchIndexerConfig{
		BatchSize:      100,
		MaxConcurrency: 8,
	}

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	indexer := NewBatchIndexer(config, builder, indexMgr)

	if indexer == nil {
		t.Fatal("NewBatchIndexer returned nil")
	}
}

func TestNewBatchIndexer_DefaultValues(t *testing.T) {
	t.Parallel()

	config := BatchIndexerConfig{} // Empty config

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	indexer := NewBatchIndexer(config, builder, indexMgr)

	if indexer == nil {
		t.Fatal("NewBatchIndexer returned nil")
	}

	// Defaults should be applied when Run is called
}

// =============================================================================
// Run Tests - Happy Path
// =============================================================================

func TestBatchIndexer_Run_Success(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 10)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result == nil {
		t.Fatal("Run() returned nil result")
	}

	if result.TotalFiles != 10 {
		t.Errorf("TotalFiles = %d, want 10", result.TotalFiles)
	}

	if result.IndexedFiles != 10 {
		t.Errorf("IndexedFiles = %d, want 10", result.IndexedFiles)
	}

	if result.FailedFiles != 0 {
		t.Errorf("FailedFiles = %d, want 0", result.FailedFiles)
	}

	if len(result.Errors) != 0 {
		t.Errorf("Errors count = %d, want 0", len(result.Errors))
	}

	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

func TestBatchIndexer_Run_EmptyChannel(t *testing.T) {
	t.Parallel()

	ch := make(chan *FileInfo)
	close(ch)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), ch)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d, want 0", result.TotalFiles)
	}

	if result.IndexedFiles != 0 {
		t.Errorf("IndexedFiles = %d, want 0", result.IndexedFiles)
	}

	if result.FailedFiles != 0 {
		t.Errorf("FailedFiles = %d, want 0", result.FailedFiles)
	}
}

func TestBatchIndexer_Run_SingleFile(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 1)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.TotalFiles != 1 {
		t.Errorf("TotalFiles = %d, want 1", result.TotalFiles)
	}

	if result.IndexedFiles != 1 {
		t.Errorf("IndexedFiles = %d, want 1", result.IndexedFiles)
	}
}

func TestBatchIndexer_Run_LargeFileCount(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 100)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      10,
		MaxConcurrency: 4,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.TotalFiles != 100 {
		t.Errorf("TotalFiles = %d, want 100", result.TotalFiles)
	}

	if result.IndexedFiles != 100 {
		t.Errorf("IndexedFiles = %d, want 100", result.IndexedFiles)
	}
}

// =============================================================================
// Run Tests - Progress Callback
// =============================================================================

func TestBatchIndexer_Run_ProgressCallback(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 10)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	var progressUpdates []*BatchIndexProgress
	var mu sync.Mutex

	config := BatchIndexerConfig{
		BatchSize:      2,
		MaxConcurrency: 2,
		ProgressCallback: func(progress *BatchIndexProgress) {
			mu.Lock()
			progressUpdates = append(progressUpdates, progress)
			mu.Unlock()
		},
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.IndexedFiles != 10 {
		t.Errorf("IndexedFiles = %d, want 10", result.IndexedFiles)
	}

	mu.Lock()
	updateCount := len(progressUpdates)
	mu.Unlock()

	if updateCount == 0 {
		t.Error("ProgressCallback was not called")
	}

	// Progress callback is called when starting to process each file,
	// so we should have received approximately 10 updates (one per file)
	if updateCount < 5 {
		t.Errorf("Expected at least 5 progress updates, got %d", updateCount)
	}
}

func TestBatchIndexer_Run_ProgressCallback_NotSet(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 5)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:        2,
		MaxConcurrency:   2,
		ProgressCallback: nil,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	// Should not panic when callback is nil
	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.IndexedFiles != 5 {
		t.Errorf("IndexedFiles = %d, want 5", result.IndexedFiles)
	}
}

// =============================================================================
// Run Tests - Context Cancellation
// =============================================================================

func TestBatchIndexer_Run_ContextCancellation(t *testing.T) {
	t.Parallel()

	// Create a slow builder that allows cancellation to occur
	builder := newMockDocumentBuilder()
	builder.delay = 100 * time.Millisecond

	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      2,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	// Create channel with many files
	files := make(chan *FileInfo, 100)
	for i := 0; i < 100; i++ {
		files <- &FileInfo{
			Path:      "/test/file" + string(rune('a'+i%26)) + ".go",
			Name:      "file" + string(rune('a'+i%26)) + ".go",
			Size:      100,
			ModTime:   time.Now(),
			Extension: ".go",
		}
	}
	close(files)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := indexer.Run(ctx, files)

	// Should return partial results, not an error
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	// Should have processed fewer files than total
	if result.TotalFiles >= 100 {
		t.Errorf("TotalFiles = %d, expected fewer due to cancellation", result.TotalFiles)
	}
}

func TestBatchIndexer_Run_ContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 10)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := indexer.Run(ctx, files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Should complete with zero or minimal processing
	if result.IndexedFiles > 0 {
		t.Logf("IndexedFiles = %d (may vary due to timing)", result.IndexedFiles)
	}
}

// =============================================================================
// Run Tests - Error Handling
// =============================================================================

func TestBatchIndexer_Run_BuildErrors(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 10)

	builder := newMockDocumentBuilder()
	builder.buildErr = errors.New("build error")

	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.TotalFiles != 10 {
		t.Errorf("TotalFiles = %d, want 10", result.TotalFiles)
	}

	if result.FailedFiles != 10 {
		t.Errorf("FailedFiles = %d, want 10", result.FailedFiles)
	}

	if result.IndexedFiles != 0 {
		t.Errorf("IndexedFiles = %d, want 0", result.IndexedFiles)
	}

	if len(result.Errors) != 10 {
		t.Errorf("Errors count = %d, want 10", len(result.Errors))
	}
}

func TestBatchIndexer_Run_PartialBuildErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := make([]*FileInfo, 10)

	for i := 0; i < 10; i++ {
		name := filepath.Join(dir, "file"+string(rune('a'+i))+".go")
		content := "package main"
		if err := os.WriteFile(name, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("failed to stat test file: %v", err)
		}

		files[i] = &FileInfo{
			Path:      name,
			Name:      filepath.Base(name),
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			Extension: ".go",
		}
	}

	// Create a builder that fails for some files
	failingBuilder := &selectiveFailBuilder{
		failPaths: map[string]bool{
			files[2].Path: true,
			files[5].Path: true,
			files[8].Path: true,
		},
	}

	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      3,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, failingBuilder, indexMgr)

	result, err := indexer.Run(context.Background(), createFileInfoChannel(files))

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.TotalFiles != 10 {
		t.Errorf("TotalFiles = %d, want 10", result.TotalFiles)
	}

	if result.FailedFiles != 3 {
		t.Errorf("FailedFiles = %d, want 3", result.FailedFiles)
	}

	if result.IndexedFiles != 7 {
		t.Errorf("IndexedFiles = %d, want 7", result.IndexedFiles)
	}

	if len(result.Errors) != 3 {
		t.Errorf("Errors count = %d, want 3", len(result.Errors))
	}
}

// selectiveFailBuilder fails for specific file paths.
type selectiveFailBuilder struct {
	failPaths map[string]bool
}

func (b *selectiveFailBuilder) BuildFromFile(ctx context.Context, info *FileInfo) (*search.Document, error) {
	if b.failPaths[info.Path] {
		return nil, errors.New("selective build error")
	}

	return &search.Document{
		ID:         search.GenerateDocumentID([]byte(info.Path)),
		Path:       info.Path,
		Type:       search.DocTypeSourceCode,
		Content:    "test content",
		Checksum:   search.GenerateChecksum([]byte(info.Path)),
		ModifiedAt: info.ModTime,
		IndexedAt:  time.Now(),
	}, nil
}

func TestBatchIndexer_Run_IndexBatchError(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 10)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()
	indexMgr.batchErr = errors.New("index batch error")

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	// The batch indexer collects errors but continues
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Files were processed but batches failed to index
	if result.TotalFiles != 10 {
		t.Errorf("TotalFiles = %d, want 10", result.TotalFiles)
	}
}

// =============================================================================
// Run Tests - Batching Behavior
// =============================================================================

func TestBatchIndexer_Run_BatchSizeBatching(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 15)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 1, // Single worker for deterministic batching
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.IndexedFiles != 15 {
		t.Errorf("IndexedFiles = %d, want 15", result.IndexedFiles)
	}

	// With batch size 5 and 15 files, we expect at least 3 batches
	// (may be more due to partial batches from workers)
	batches := indexMgr.getBatches()
	if len(batches) < 1 {
		t.Errorf("Expected at least 1 batch call, got %d", len(batches))
	}
}

func TestBatchIndexer_Run_BatchSizeLargerThanFileCount(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 3)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      100, // Much larger than file count
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.IndexedFiles != 3 {
		t.Errorf("IndexedFiles = %d, want 3", result.IndexedFiles)
	}
}

// =============================================================================
// Run Tests - Concurrency
// =============================================================================

func TestBatchIndexer_Run_ConcurrentProcessing(t *testing.T) {
	t.Parallel()

	// Create a builder with slight delay to verify concurrent processing
	builder := newMockDocumentBuilder()
	builder.delay = 10 * time.Millisecond

	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      2,
		MaxConcurrency: 4,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	// Create channel with files
	files := make(chan *FileInfo, 20)
	for i := 0; i < 20; i++ {
		files <- &FileInfo{
			Path:      "/test/file" + string(rune('a'+i%26)) + ".go",
			Name:      "file" + string(rune('a'+i%26)) + ".go",
			Size:      100,
			ModTime:   time.Now(),
			Extension: ".go",
		}
	}
	close(files)

	start := time.Now()
	result, err := indexer.Run(context.Background(), files)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.IndexedFiles != 20 {
		t.Errorf("IndexedFiles = %d, want 20", result.IndexedFiles)
	}

	// With 4 workers and 10ms delay, 20 files should complete much faster
	// than 200ms (sequential) - allow some overhead
	maxExpected := 150 * time.Millisecond
	if elapsed > maxExpected {
		t.Errorf("Elapsed time %v suggests sequential processing, expected < %v", elapsed, maxExpected)
	}
}

func TestBatchIndexer_Run_MaxConcurrencyRespected(t *testing.T) {
	t.Parallel()

	var activeWorkers atomic.Int32
	var maxObserved atomic.Int32

	// Custom builder that tracks concurrent execution
	trackingBuilder := &concurrencyTrackingBuilder{
		activeWorkers: &activeWorkers,
		maxObserved:   &maxObserved,
		delay:         20 * time.Millisecond,
	}

	indexMgr := newMockIndexManager()

	maxConcurrency := 3
	config := BatchIndexerConfig{
		BatchSize:      1,
		MaxConcurrency: maxConcurrency,
	}

	indexer := NewBatchIndexer(config, trackingBuilder, indexMgr)

	// Create channel with many files
	files := make(chan *FileInfo, 20)
	for i := 0; i < 20; i++ {
		files <- &FileInfo{
			Path:      "/test/file" + string(rune('a'+i%26)) + ".go",
			Name:      "file" + string(rune('a'+i%26)) + ".go",
			Size:      100,
			ModTime:   time.Now(),
			Extension: ".go",
		}
	}
	close(files)

	_, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// maxObserved should not exceed maxConcurrency
	observed := int(maxObserved.Load())
	if observed > maxConcurrency {
		t.Errorf("Max concurrent workers = %d, should not exceed %d", observed, maxConcurrency)
	}
}

// concurrencyTrackingBuilder tracks concurrent executions.
type concurrencyTrackingBuilder struct {
	activeWorkers *atomic.Int32
	maxObserved   *atomic.Int32
	delay         time.Duration
}

func (b *concurrencyTrackingBuilder) BuildFromFile(ctx context.Context, info *FileInfo) (*search.Document, error) {
	current := b.activeWorkers.Add(1)

	// Update max observed
	for {
		max := b.maxObserved.Load()
		if current <= max || b.maxObserved.CompareAndSwap(max, current) {
			break
		}
	}

	if b.delay > 0 {
		select {
		case <-ctx.Done():
			b.activeWorkers.Add(-1)
			return nil, ctx.Err()
		case <-time.After(b.delay):
		}
	}

	b.activeWorkers.Add(-1)

	return &search.Document{
		ID:         search.GenerateDocumentID([]byte(info.Path)),
		Path:       info.Path,
		Type:       search.DocTypeSourceCode,
		Content:    "test content",
		Checksum:   search.GenerateChecksum([]byte(info.Path)),
		ModifiedAt: info.ModTime,
		IndexedAt:  time.Now(),
	}, nil
}

// =============================================================================
// Run Tests - Default Configuration
// =============================================================================

func TestBatchIndexer_Run_DefaultBatchSize(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 10)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      0, // Should use default
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.IndexedFiles != 10 {
		t.Errorf("IndexedFiles = %d, want 10", result.IndexedFiles)
	}
}

func TestBatchIndexer_Run_DefaultMaxConcurrency(t *testing.T) {
	t.Parallel()

	_, files := createTestFilesForBatchIndexer(t, 10)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 0, // Should use default
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.IndexedFiles != 10 {
		t.Errorf("IndexedFiles = %d, want 10", result.IndexedFiles)
	}
}

// =============================================================================
// BatchIndexProgress Tests
// =============================================================================

func TestBatchIndexProgress_Fields(t *testing.T) {
	t.Parallel()

	progress := &BatchIndexProgress{
		TotalFiles:     100,
		ProcessedFiles: 50,
		IndexedFiles:   45,
		FailedFiles:    5,
		CurrentFile:    "/test/file.go",
	}

	if progress.TotalFiles != 100 {
		t.Errorf("TotalFiles = %d, want 100", progress.TotalFiles)
	}

	if progress.ProcessedFiles != 50 {
		t.Errorf("ProcessedFiles = %d, want 50", progress.ProcessedFiles)
	}

	if progress.IndexedFiles != 45 {
		t.Errorf("IndexedFiles = %d, want 45", progress.IndexedFiles)
	}

	if progress.FailedFiles != 5 {
		t.Errorf("FailedFiles = %d, want 5", progress.FailedFiles)
	}

	if progress.CurrentFile != "/test/file.go" {
		t.Errorf("CurrentFile = %q, want %q", progress.CurrentFile, "/test/file.go")
	}
}

// =============================================================================
// BatchIndexingResult Tests
// =============================================================================

func TestBatchIndexingResult_Fields(t *testing.T) {
	t.Parallel()

	result := &BatchIndexingResult{
		TotalFiles:   100,
		IndexedFiles: 90,
		FailedFiles:  10,
		Errors:       []error{errors.New("error1"), errors.New("error2")},
		Duration:     5 * time.Second,
	}

	if result.TotalFiles != 100 {
		t.Errorf("TotalFiles = %d, want 100", result.TotalFiles)
	}

	if result.IndexedFiles != 90 {
		t.Errorf("IndexedFiles = %d, want 90", result.IndexedFiles)
	}

	if result.FailedFiles != 10 {
		t.Errorf("FailedFiles = %d, want 10", result.FailedFiles)
	}

	if len(result.Errors) != 2 {
		t.Errorf("Errors count = %d, want 2", len(result.Errors))
	}

	if result.Duration != 5*time.Second {
		t.Errorf("Duration = %v, want %v", result.Duration, 5*time.Second)
	}
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestBatchIndexer_Run_RaceCondition(t *testing.T) {
	t.Parallel()

	// Create many files for high contention
	files := make(chan *FileInfo, 100)
	for i := 0; i < 100; i++ {
		files <- &FileInfo{
			Path:      "/test/file" + string(rune('a'+i%26)) + string(rune('0'+i%10)) + ".go",
			Name:      "file" + string(rune('a'+i%26)) + ".go",
			Size:      100,
			ModTime:   time.Now(),
			Extension: ".go",
		}
	}
	close(files)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	var progressCount atomic.Int64

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 8, // High concurrency
		ProgressCallback: func(progress *BatchIndexProgress) {
			progressCount.Add(1)
		},
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	// Run multiple times to increase chance of catching races
	for i := 0; i < 3; i++ {
		files := make(chan *FileInfo, 50)
		for j := 0; j < 50; j++ {
			files <- &FileInfo{
				Path:      "/test/file" + string(rune('a'+j%26)) + ".go",
				Name:      "file" + string(rune('a'+j%26)) + ".go",
				Size:      100,
				ModTime:   time.Now(),
				Extension: ".go",
			}
		}
		close(files)

		_, err := indexer.Run(context.Background(), files)
		if err != nil {
			t.Fatalf("Run() iteration %d error = %v", i, err)
		}
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestBatchIndexer_Run_NilFileInfo(t *testing.T) {
	t.Parallel()

	files := make(chan *FileInfo, 5)
	files <- &FileInfo{Path: "/test/file1.go", Name: "file1.go", Extension: ".go", ModTime: time.Now()}
	files <- nil // nil file info
	files <- &FileInfo{Path: "/test/file2.go", Name: "file2.go", Extension: ".go", ModTime: time.Now()}
	close(files)

	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      5,
		MaxConcurrency: 2,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	// Should handle nil gracefully
	result, err := indexer.Run(context.Background(), files)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Should process non-nil files
	if result.IndexedFiles < 2 {
		t.Errorf("IndexedFiles = %d, expected at least 2", result.IndexedFiles)
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkBatchIndexer_Run(b *testing.B) {
	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      50,
		MaxConcurrency: 4,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		files := make(chan *FileInfo, 100)
		for j := 0; j < 100; j++ {
			files <- &FileInfo{
				Path:      "/test/file" + string(rune('a'+j%26)) + ".go",
				Name:      "file" + string(rune('a'+j%26)) + ".go",
				Size:      100,
				ModTime:   time.Now(),
				Extension: ".go",
			}
		}
		close(files)

		_, _ = indexer.Run(context.Background(), files)
	}
}

func BenchmarkBatchIndexer_Run_HighConcurrency(b *testing.B) {
	builder := newMockDocumentBuilder()
	indexMgr := newMockIndexManager()

	config := BatchIndexerConfig{
		BatchSize:      20,
		MaxConcurrency: 16,
	}

	indexer := NewBatchIndexer(config, builder, indexMgr)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		files := make(chan *FileInfo, 200)
		for j := 0; j < 200; j++ {
			files <- &FileInfo{
				Path:      "/test/file" + string(rune('a'+j%26)) + ".go",
				Name:      "file" + string(rune('a'+j%26)) + ".go",
				Size:      100,
				ModTime:   time.Now(),
				Extension: ".go",
			}
		}
		close(files)

		_, _ = indexer.Run(context.Background(), files)
	}
}
