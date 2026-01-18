package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Mock Implementations
// =============================================================================

// mockDocument creates a test document for indexing.
func mockDocument(id, path, content string) *search.Document {
	return &search.Document{
		ID:         id,
		Path:       path,
		Type:       search.DocTypeSourceCode,
		Language:   "go",
		Content:    content,
		Checksum:   search.GenerateChecksum([]byte(content)),
		ModifiedAt: time.Now(),
		IndexedAt:  time.Now(),
	}
}

// mockDestroyableIndex implements DestroyableIndex for testing.
type mockDestroyableIndex struct {
	mu            sync.Mutex
	documents     map[string]*search.Document
	isOpen        bool
	destroyCalled bool
	openCalled    bool
	destroyErr    error
	openErr       error
	indexErr      error
}

func newMockDestroyableIndex() *mockDestroyableIndex {
	return &mockDestroyableIndex{
		documents: make(map[string]*search.Document),
		isOpen:    true,
	}
}

func (m *mockDestroyableIndex) Index(ctx context.Context, doc *search.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.indexErr != nil {
		return m.indexErr
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	m.documents[doc.ID] = doc
	return nil
}

func (m *mockDestroyableIndex) IndexBatch(ctx context.Context, docs []*search.Document) error {
	for _, doc := range docs {
		if err := m.Index(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockDestroyableIndex) DocumentCount() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return uint64(len(m.documents)), nil
}

func (m *mockDestroyableIndex) IsOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isOpen
}

func (m *mockDestroyableIndex) Destroy() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destroyCalled = true

	if m.destroyErr != nil {
		return m.destroyErr
	}

	m.documents = make(map[string]*search.Document)
	m.isOpen = false
	return nil
}

func (m *mockDestroyableIndex) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openCalled = true

	if m.openErr != nil {
		return m.openErr
	}

	m.isOpen = true
	return nil
}

func (m *mockDestroyableIndex) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isOpen = false
	return nil
}

// countingMockDestroyableIndex fails after a specified number of successful indexes.
type countingMockDestroyableIndex struct {
	*mockDestroyableIndex
	successLimit int
	indexCount   int
}

func newCountingMockDestroyableIndex(successLimit int) *countingMockDestroyableIndex {
	return &countingMockDestroyableIndex{
		mockDestroyableIndex: newMockDestroyableIndex(),
		successLimit:         successLimit,
	}
}

func (c *countingMockDestroyableIndex) Index(ctx context.Context, doc *search.Document) error {
	c.mockDestroyableIndex.mu.Lock()
	c.indexCount++
	count := c.indexCount
	c.mockDestroyableIndex.mu.Unlock()

	if count > c.successLimit {
		return errors.New("index error after limit")
	}

	return c.mockDestroyableIndex.Index(ctx, doc)
}

// =============================================================================
// ReindexConfig Tests
// =============================================================================

func TestReindexConfig_Defaults(t *testing.T) {
	t.Parallel()

	config := ReindexConfig{
		RootPath: "/some/path",
	}

	if config.BatchSize != 0 {
		t.Error("expected BatchSize to be zero before NewFullReindexer")
	}

	if config.MaxConcurrency != 0 {
		t.Error("expected MaxConcurrency to be zero before NewFullReindexer")
	}
}

// =============================================================================
// NewFullReindexer Tests
// =============================================================================

func TestNewFullReindexer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:       dir,
		BatchSize:      50,
		MaxConcurrency: 4,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	if reindexer == nil {
		t.Fatal("NewFullReindexer() returned nil")
	}
}

func TestNewFullReindexer_EmptyRootPath(t *testing.T) {
	t.Parallel()

	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath: "",
	}

	_, err := NewFullReindexer(config, index)
	if err == nil {
		t.Error("NewFullReindexer() expected error for empty root path")
	}
}

func TestNewFullReindexer_NilIndexManager(t *testing.T) {
	t.Parallel()

	config := ReindexConfig{
		RootPath: "/some/path",
	}

	_, err := NewFullReindexer(config, nil)
	if err == nil {
		t.Error("NewFullReindexer() expected error for nil index manager")
	}
}

func TestNewFullReindexer_DefaultBatchSize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 0, // Should use default
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	if reindexer.config.BatchSize != ReindexDefaultBatchSize {
		t.Errorf("BatchSize = %d, want default %d", reindexer.config.BatchSize, ReindexDefaultBatchSize)
	}
}

// =============================================================================
// Reindex Tests - Happy Path
// =============================================================================

func TestFullReindexer_Reindex_EmptyDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	if result.Indexed != 0 {
		t.Errorf("Indexed = %d, want 0", result.Indexed)
	}

	if result.Failed != 0 {
		t.Errorf("Failed = %d, want 0", result.Failed)
	}

	// Index should have been destroyed and reopened
	if !index.destroyCalled {
		t.Error("expected Destroy() to be called")
	}

	if !index.openCalled {
		t.Error("expected Open() to be called")
	}
}

func TestFullReindexer_Reindex_SingleFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "main.go", "package main\nfunc main() {}")

	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	if result.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1", result.Indexed)
	}

	// Verify document count in index
	count, _ := index.DocumentCount()
	if count != 1 {
		t.Errorf("DocumentCount = %d, want 1", count)
	}
}

func TestFullReindexer_Reindex_MultipleFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Use unique content for each file to get unique document IDs
	createFile(t, dir, "main.go", "package main\n\nfunc main() {}")
	createFile(t, dir, "handler.go", "package main\n\nfunc handler() {}")
	createDir(t, dir, "pkg")
	createFile(t, dir, "pkg/util.go", "package pkg\n\nfunc util() {}")

	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	if result.Indexed != 3 {
		t.Errorf("Indexed = %d, want 3", result.Indexed)
	}

	count, _ := index.DocumentCount()
	if count != 3 {
		t.Errorf("DocumentCount = %d, want 3", count)
	}
}

func TestFullReindexer_Reindex_IndexDestroyedAndRecreated(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "main.go", "package main")

	index := newMockDestroyableIndex()
	// Pre-populate the index
	_ = index.Index(context.Background(), mockDocument("old-id", "/old/path", "old content"))

	oldCount, _ := index.DocumentCount()
	if oldCount != 1 {
		t.Fatalf("expected 1 pre-existing document, got %d", oldCount)
	}

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	// Only the new file should exist
	if result.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1", result.Indexed)
	}

	count, _ := index.DocumentCount()
	if count != 1 {
		t.Errorf("DocumentCount = %d, want 1 (old documents should be removed)", count)
	}

	// The old document should not exist
	if _, exists := index.documents["old-id"]; exists {
		t.Error("old document should have been removed during reindex")
	}
}

// =============================================================================
// Reindex Tests - Progress Callback
// =============================================================================

func TestFullReindexer_Reindex_ProgressCallback(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "file1.go", "package main")
	createFile(t, dir, "file2.go", "package main")
	createFile(t, dir, "file3.go", "package main")

	index := newMockDestroyableIndex()

	var progressCalls []IndexingProgress
	var mu sync.Mutex

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 1, // Small batch to trigger more progress calls
		Progress: func(p IndexingProgress) {
			mu.Lock()
			progressCalls = append(progressCalls, p)
			mu.Unlock()
		},
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	_, err = reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	mu.Lock()
	callCount := len(progressCalls)
	mu.Unlock()

	if callCount == 0 {
		t.Error("expected progress callback to be called at least once")
	}

	// Verify final progress shows files were processed
	// Note: Total is 0 because we don't know total count until scan completes (streaming)
	mu.Lock()
	lastProgress := progressCalls[len(progressCalls)-1]
	mu.Unlock()

	if lastProgress.Processed == 0 {
		t.Error("expected Processed to be non-zero")
	}

	if lastProgress.Indexed == 0 {
		t.Error("expected Indexed to be non-zero")
	}
}

// =============================================================================
// Reindex Tests - Error Handling
// =============================================================================

func TestFullReindexer_Reindex_DestroyError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	index := newMockDestroyableIndex()
	index.destroyErr = errors.New("destroy failed")

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	_, err = reindexer.Reindex(context.Background())
	if err == nil {
		t.Error("Reindex() expected error for destroy failure")
	}

	if !errors.Is(err, index.destroyErr) {
		t.Errorf("Reindex() error = %v, want %v", err, index.destroyErr)
	}
}

func TestFullReindexer_Reindex_OpenError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	index := newMockDestroyableIndex()
	index.openErr = errors.New("open failed")

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	_, err = reindexer.Reindex(context.Background())
	if err == nil {
		t.Error("Reindex() expected error for open failure")
	}

	if !errors.Is(err, index.openErr) {
		t.Errorf("Reindex() error = %v, want %v", err, index.openErr)
	}
}

func TestFullReindexer_Reindex_ContextCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create many files to ensure we have time to cancel
	for i := 0; i < 100; i++ {
		createFile(t, dir, filepath.Join("subdir", "file"+string(rune('a'+i%26))+string(rune('0'+i/26))+".go"), "package main")
	}

	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 1, // Small batches to give more cancel opportunities
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a very short delay
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	result, err := reindexer.Reindex(ctx)

	// Should return partial results, not an error
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Reindex() unexpected error = %v", err)
	}

	// Result should still be returned with partial data
	if result == nil {
		t.Error("expected partial result on cancellation")
	}
}

func TestFullReindexer_Reindex_ContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "main.go", "package main")

	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := reindexer.Reindex(ctx)

	// Should still get a result even if cancelled early
	if result == nil {
		t.Error("expected result even when context is already cancelled")
	}
}

// =============================================================================
// Reindex Tests - Custom ScanConfig
// =============================================================================

func TestFullReindexer_Reindex_WithCustomScanConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "main.go", "package main")
	createFile(t, dir, "main_test.go", "package main")
	createFile(t, dir, "readme.md", "# README")

	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
		ScanConfig: ScanConfig{
			IncludePatterns: []string{"*.go"},
			ExcludePatterns: []string{"*_test.go"},
		},
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	// Should only index main.go (not test file or markdown)
	if result.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1 (only main.go)", result.Indexed)
	}
}

// =============================================================================
// Reindex Tests - Memory Bounded (Streaming)
// =============================================================================

func TestFullReindexer_Reindex_StreamingBehavior(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create enough files to require multiple batches
	for i := 0; i < 25; i++ {
		createFile(t, dir, filepath.Join("pkg", "file"+string(rune('a'+i%26))+".go"), "package pkg")
	}

	index := newMockDestroyableIndex()

	var batchSizes []int
	var mu sync.Mutex

	// We use progress callback to observe batch processing
	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 5, // Small batches
		Progress: func(p IndexingProgress) {
			mu.Lock()
			batchSizes = append(batchSizes, p.Processed)
			mu.Unlock()
		},
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	if result.Indexed != 25 {
		t.Errorf("Indexed = %d, want 25", result.Indexed)
	}

	// Multiple progress calls indicate streaming behavior
	mu.Lock()
	callCount := len(batchSizes)
	mu.Unlock()

	if callCount < 2 {
		t.Error("expected multiple progress callbacks indicating streaming/batched processing")
	}
}

// =============================================================================
// Reindex Tests - Duration Tracking
// =============================================================================

func TestFullReindexer_Reindex_DurationTracking(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "main.go", "package main")

	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	if result.Duration == 0 {
		t.Error("expected Duration to be non-zero")
	}
}

// =============================================================================
// Reindex Tests - Error Collection
// =============================================================================

func TestFullReindexer_Reindex_IndexErrorCollection(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "file1.go", "package main")
	createFile(t, dir, "file2.go", "package main")

	index := newCountingMockDestroyableIndex(1) // Fail after 1 success

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 1,
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() should not return error for individual file failures: %v", err)
	}

	if result.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1", result.Indexed)
	}

	if result.Failed != 1 {
		t.Errorf("Failed = %d, want 1", result.Failed)
	}

	if len(result.Errors) != 1 {
		t.Errorf("Errors count = %d, want 1", len(result.Errors))
	}
}

// =============================================================================
// IndexingProgress Tests
// =============================================================================

func TestIndexingProgress_Fields(t *testing.T) {
	t.Parallel()

	progress := IndexingProgress{
		Total:     100,
		Processed: 50,
		Indexed:   45,
		Failed:    5,
		Current:   "/path/to/file.go",
	}

	if progress.Total != 100 {
		t.Errorf("Total = %d, want 100", progress.Total)
	}

	if progress.Processed != 50 {
		t.Errorf("Processed = %d, want 50", progress.Processed)
	}

	if progress.Indexed != 45 {
		t.Errorf("Indexed = %d, want 45", progress.Indexed)
	}

	if progress.Failed != 5 {
		t.Errorf("Failed = %d, want 5", progress.Failed)
	}

	if progress.Current != "/path/to/file.go" {
		t.Errorf("Current = %q, want %q", progress.Current, "/path/to/file.go")
	}
}

// =============================================================================
// ProgressCallback Tests
// =============================================================================

func TestProgressCallback_NilIsAllowed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "main.go", "package main")

	index := newMockDestroyableIndex()

	config := ReindexConfig{
		RootPath:  dir,
		BatchSize: 10,
		Progress:  nil, // Explicitly nil
	}

	reindexer, err := NewFullReindexer(config, index)
	if err != nil {
		t.Fatalf("NewFullReindexer() error = %v", err)
	}

	// Should not panic with nil progress callback
	result, err := reindexer.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex() error = %v", err)
	}

	if result.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1", result.Indexed)
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkFullReindexer_Reindex(b *testing.B) {
	dir := b.TempDir()

	// Create test files
	for i := 0; i < 100; i++ {
		subDir := filepath.Join(dir, "pkg"+string(rune('a'+i%10)))
		if err := os.MkdirAll(subDir, 0755); err != nil {
			b.Fatalf("mkdir error: %v", err)
		}
		filePath := filepath.Join(subDir, "file"+string(rune('a'+i%26))+".go")
		if err := os.WriteFile(filePath, []byte("package pkg"), 0644); err != nil {
			b.Fatalf("write error: %v", err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		index := newMockDestroyableIndex()
		config := ReindexConfig{
			RootPath:  dir,
			BatchSize: 50,
		}

		reindexer, err := NewFullReindexer(config, index)
		if err != nil {
			b.Fatalf("NewFullReindexer() error = %v", err)
		}

		_, err = reindexer.Reindex(context.Background())
		if err != nil {
			b.Fatalf("Reindex() error = %v", err)
		}
	}
}
