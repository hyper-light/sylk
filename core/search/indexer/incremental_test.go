// Package indexer provides file scanning and indexing functionality for the Sylk
// Document Search System.
package indexer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Mock Implementations for Incremental Indexer
// =============================================================================

// incrementalMockIndexManager provides a test double for IndexManagerInterface.
type incrementalMockIndexManager struct {
	mu             sync.Mutex
	indexed        map[string]*search.Document
	deleted        map[string]bool
	indexErr       error
	indexBatchErr  error
	deleteErr      error
	deleteByPathFn func(ctx context.Context, path string) error
	batchCalls     int // tracks number of IndexBatch calls
}

func newIncrementalMockIndexManager() *incrementalMockIndexManager {
	return &incrementalMockIndexManager{
		indexed: make(map[string]*search.Document),
		deleted: make(map[string]bool),
	}
}

func (m *incrementalMockIndexManager) Index(ctx context.Context, doc *search.Document) error {
	if m.indexErr != nil {
		return m.indexErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexed[doc.ID] = doc
	return nil
}

func (m *incrementalMockIndexManager) IndexBatch(ctx context.Context, docs []*search.Document) error {
	if m.indexBatchErr != nil {
		return m.indexBatchErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batchCalls++
	for _, doc := range docs {
		m.indexed[doc.ID] = doc
	}
	return nil
}

func (m *incrementalMockIndexManager) Delete(ctx context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted[id] = true
	return nil
}

func (m *incrementalMockIndexManager) DeleteByPath(ctx context.Context, path string) error {
	if m.deleteByPathFn != nil {
		return m.deleteByPathFn(ctx, path)
	}
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted[path] = true
	return nil
}

// incrementalMockChecksumStore provides a test double for ChecksumStore.
type incrementalMockChecksumStore struct {
	mu        sync.Mutex
	checksums map[string]string
}

func newIncrementalMockChecksumStore() *incrementalMockChecksumStore {
	return &incrementalMockChecksumStore{
		checksums: make(map[string]string),
	}
}

func (s *incrementalMockChecksumStore) GetChecksum(path string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, ok := s.checksums[path]
	return cs, ok
}

func (s *incrementalMockChecksumStore) SetChecksum(path string, checksum string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checksums[path] = checksum
}

func (s *incrementalMockChecksumStore) DeleteChecksum(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.checksums, path)
}

// incrementalMockDocumentBuilder builds test documents from FileInfo.
type incrementalMockDocumentBuilder struct {
	buildFn  func(fi *FileInfo) (*search.Document, error)
	buildErr error
}

func newIncrementalMockDocumentBuilder() *incrementalMockDocumentBuilder {
	return &incrementalMockDocumentBuilder{
		buildFn: func(fi *FileInfo) (*search.Document, error) {
			// Default builder creates a simple document
			content := "content for " + fi.Path
			checksum := search.GenerateChecksum([]byte(content))
			return &search.Document{
				ID:         search.GenerateDocumentID([]byte(fi.Path)),
				Path:       fi.Path,
				Type:       search.DocTypeSourceCode,
				Content:    content,
				Checksum:   checksum,
				ModifiedAt: fi.ModTime,
				IndexedAt:  time.Now(),
			}, nil
		},
	}
}

func (b *incrementalMockDocumentBuilder) BuildDocument(fi *FileInfo) (*search.Document, error) {
	if b.buildErr != nil {
		return nil, b.buildErr
	}
	return b.buildFn(fi)
}

// =============================================================================
// Test Helpers
// =============================================================================

func defaultTestConfig() IncrementalIndexerConfig {
	return IncrementalIndexerConfig{
		BatchSize:      10,
		MaxConcurrency: 2,
	}
}

func createTestFileInfo(path string) *FileInfo {
	return &FileInfo{
		Path:      path,
		Name:      path,
		Size:      100,
		ModTime:   time.Now(),
		IsDir:     false,
		Extension: ".go",
	}
}

// =============================================================================
// Test: NewIncrementalIndexer
// =============================================================================

func TestNewIncrementalIndexer(t *testing.T) {
	t.Run("creates indexer with valid dependencies", func(t *testing.T) {
		config := defaultTestConfig()
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(config, indexMgr, checksumStore, docBuilder)

		if indexer == nil {
			t.Fatal("expected non-nil indexer")
		}
		if indexer.config.BatchSize != config.BatchSize {
			t.Errorf("expected BatchSize %d, got %d", config.BatchSize, indexer.config.BatchSize)
		}
		if indexer.config.MaxConcurrency != config.MaxConcurrency {
			t.Errorf("expected MaxConcurrency %d, got %d", config.MaxConcurrency, indexer.config.MaxConcurrency)
		}
	})
}

// =============================================================================
// Test: Index - Empty Changes
// =============================================================================

func TestIndex_EmptyChanges(t *testing.T) {
	t.Run("returns empty result for empty changes", func(t *testing.T) {
		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			newIncrementalMockIndexManager(),
			newIncrementalMockChecksumStore(),
			newIncrementalMockDocumentBuilder(),
		)

		result, err := indexer.Index(context.Background(), []FileChange{})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Indexed != 0 {
			t.Errorf("expected 0 indexed, got %d", result.Indexed)
		}
		if result.Failed != 0 {
			t.Errorf("expected 0 failed, got %d", result.Failed)
		}
	})
}

// =============================================================================
// Test: Index - Create Operation
// =============================================================================

func TestIndex_CreateOperation(t *testing.T) {
	t.Run("indexes newly created file", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Indexed != 1 {
			t.Errorf("expected 1 indexed, got %d", result.Indexed)
		}
		if len(indexMgr.indexed) != 1 {
			t.Errorf("expected 1 document in index, got %d", len(indexMgr.indexed))
		}

		// Verify checksum was stored
		if _, ok := checksumStore.GetChecksum("/test/file.go"); !ok {
			t.Error("expected checksum to be stored")
		}
	})

	t.Run("skips create if same checksum exists", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		// Pre-store checksum matching what document builder will generate
		content := "content for /test/file.go"
		existingChecksum := search.GenerateChecksum([]byte(content))
		checksumStore.SetChecksum("/test/file.go", existingChecksum)

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should be skipped due to same checksum
		if result.Indexed != 0 {
			t.Errorf("expected 0 indexed (skipped), got %d", result.Indexed)
		}
		if len(indexMgr.indexed) != 0 {
			t.Errorf("expected 0 documents in index (skipped), got %d", len(indexMgr.indexed))
		}
	})
}

// =============================================================================
// Test: Index - Modify Operation
// =============================================================================

func TestIndex_ModifyOperation(t *testing.T) {
	t.Run("indexes modified file with different checksum", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		// Pre-store old checksum
		checksumStore.SetChecksum("/test/file.go", "old-checksum")

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpModify,
				FileInfo:  createTestFileInfo("/test/file.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Indexed != 1 {
			t.Errorf("expected 1 indexed, got %d", result.Indexed)
		}

		// Verify checksum was updated
		newChecksum, ok := checksumStore.GetChecksum("/test/file.go")
		if !ok {
			t.Fatal("expected checksum to exist")
		}
		if newChecksum == "old-checksum" {
			t.Error("expected checksum to be updated")
		}
	})

	t.Run("skips modified file with same checksum", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		// Pre-store checksum matching what will be generated
		content := "content for /test/file.go"
		existingChecksum := search.GenerateChecksum([]byte(content))
		checksumStore.SetChecksum("/test/file.go", existingChecksum)

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpModify,
				FileInfo:  createTestFileInfo("/test/file.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Indexed != 0 {
			t.Errorf("expected 0 indexed (skipped), got %d", result.Indexed)
		}
	})
}

// =============================================================================
// Test: Index - Delete Operation
// =============================================================================

func TestIndex_DeleteOperation(t *testing.T) {
	t.Run("deletes file from index and checksum store", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		// Pre-store checksum
		checksumStore.SetChecksum("/test/file.go", "some-checksum")

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpDelete,
				FileInfo:  nil, // FileInfo is nil for deletes
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Indexed != 1 {
			t.Errorf("expected 1 indexed (delete counts as success), got %d", result.Indexed)
		}

		// Verify delete was called
		if !indexMgr.deleted["/test/file.go"] {
			t.Error("expected file to be deleted from index")
		}

		// Verify checksum was removed
		if _, ok := checksumStore.GetChecksum("/test/file.go"); ok {
			t.Error("expected checksum to be deleted")
		}
	})
}

// =============================================================================
// Test: Index - Rename Operation
// =============================================================================

func TestIndex_RenameOperation(t *testing.T) {
	t.Run("handles rename by deleting old and indexing new", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		// Pre-store old checksum
		checksumStore.SetChecksum("/test/old.go", "old-checksum")

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/new.go",
				Operation: OpRename,
				FileInfo:  createTestFileInfo("/test/new.go"),
				OldPath:   "/test/old.go",
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Rename counts as 2 operations: 1 delete + 1 index
		if result.Indexed != 2 {
			t.Errorf("expected 2 indexed (delete + index), got %d", result.Indexed)
		}

		// Verify old path was deleted
		if !indexMgr.deleted["/test/old.go"] {
			t.Error("expected old path to be deleted from index")
		}

		// Verify old checksum was removed
		if _, ok := checksumStore.GetChecksum("/test/old.go"); ok {
			t.Error("expected old checksum to be deleted")
		}

		// Verify new checksum was stored
		if _, ok := checksumStore.GetChecksum("/test/new.go"); !ok {
			t.Error("expected new checksum to be stored")
		}
	})
}

// =============================================================================
// Test: Index - Context Cancellation
// =============================================================================

func TestIndex_ContextCancellation(t *testing.T) {
	t.Run("returns partial results on context cancellation", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		// Create a pre-cancelled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		changes := []FileChange{
			{
				Path:      "/test/file1.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file1.go"),
			},
			{
				Path:      "/test/file2.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file2.go"),
			},
		}

		result, err := indexer.Index(ctx, changes)

		// Should return context error
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled error, got %v", err)
		}
		// Should still return a result (partial)
		if result == nil {
			t.Fatal("expected non-nil result even on cancellation")
		}
	})
}

// =============================================================================
// Test: Index - Mixed Operations
// =============================================================================

func TestIndex_MixedOperations(t *testing.T) {
	t.Run("handles mixed operations in single batch", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		// Pre-store checksums for modify and rename operations
		checksumStore.SetChecksum("/test/modify.go", "old-checksum")
		checksumStore.SetChecksum("/test/old.go", "rename-checksum")
		checksumStore.SetChecksum("/test/delete.go", "delete-checksum")

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/create.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/create.go"),
			},
			{
				Path:      "/test/modify.go",
				Operation: OpModify,
				FileInfo:  createTestFileInfo("/test/modify.go"),
			},
			{
				Path:      "/test/delete.go",
				Operation: OpDelete,
				FileInfo:  nil,
			},
			{
				Path:      "/test/new.go",
				Operation: OpRename,
				FileInfo:  createTestFileInfo("/test/new.go"),
				OldPath:   "/test/old.go",
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 5 operations: create=1, modify=1, delete=1, rename=2 (delete + index)
		if result.Indexed != 5 {
			t.Errorf("expected 5 indexed (create+modify+delete+rename(delete+index)), got %d", result.Indexed)
		}
		if result.Failed != 0 {
			t.Errorf("expected 0 failed, got %d", result.Failed)
		}
	})
}

// =============================================================================
// Test: Index - Error Handling
// =============================================================================

func TestIndex_ErrorHandling(t *testing.T) {
	t.Run("collects errors without stopping", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		// Make document builder fail for specific file
		docBuilder.buildFn = func(fi *FileInfo) (*search.Document, error) {
			if fi.Path == "/test/fail.go" {
				return nil, errors.New("build error")
			}
			content := "content for " + fi.Path
			checksum := search.GenerateChecksum([]byte(content))
			return &search.Document{
				ID:         search.GenerateDocumentID([]byte(fi.Path)),
				Path:       fi.Path,
				Type:       search.DocTypeSourceCode,
				Content:    content,
				Checksum:   checksum,
				ModifiedAt: fi.ModTime,
				IndexedAt:  time.Now(),
			}, nil
		}

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/success.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/success.go"),
			},
			{
				Path:      "/test/fail.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/fail.go"),
			},
			{
				Path:      "/test/another.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/another.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Indexed != 2 {
			t.Errorf("expected 2 indexed, got %d", result.Indexed)
		}
		if result.Failed != 1 {
			t.Errorf("expected 1 failed, got %d", result.Failed)
		}
		if len(result.Errors) != 1 {
			t.Errorf("expected 1 error, got %d", len(result.Errors))
		}
	})

	t.Run("handles index error", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		indexMgr.indexBatchErr = errors.New("index batch error")
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Failed != 1 {
			t.Errorf("expected 1 failed, got %d", result.Failed)
		}
	})

	t.Run("handles delete error", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		indexMgr.deleteErr = errors.New("delete error")
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		checksumStore.SetChecksum("/test/file.go", "some-checksum")

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpDelete,
				FileInfo:  nil,
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Failed != 1 {
			t.Errorf("expected 1 failed, got %d", result.Failed)
		}
	})
}

// =============================================================================
// Test: Index - Checksum Store Updates
// =============================================================================

func TestIndex_ChecksumStoreUpdates(t *testing.T) {
	t.Run("updates checksum store on successful index", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file.go"),
			},
		}

		_, err := indexer.Index(context.Background(), changes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify checksum was stored
		checksum, ok := checksumStore.GetChecksum("/test/file.go")
		if !ok {
			t.Fatal("expected checksum to be stored")
		}

		// Verify it matches expected checksum
		expectedContent := "content for /test/file.go"
		expectedChecksum := search.GenerateChecksum([]byte(expectedContent))
		if checksum != expectedChecksum {
			t.Errorf("expected checksum %s, got %s", expectedChecksum, checksum)
		}
	})

	t.Run("does not update checksum store on failed index", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		indexMgr.indexBatchErr = errors.New("index batch error")
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file.go"),
			},
		}

		_, err := indexer.Index(context.Background(), changes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify checksum was NOT stored
		if _, ok := checksumStore.GetChecksum("/test/file.go"); ok {
			t.Error("expected checksum NOT to be stored on failed index")
		}
	})
}

// =============================================================================
// Test: Index - Nil FileInfo Handling
// =============================================================================

func TestIndex_NilFileInfoHandling(t *testing.T) {
	t.Run("handles nil FileInfo for non-delete operation", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpCreate,
				FileInfo:  nil, // Invalid for create
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Failed != 1 {
			t.Errorf("expected 1 failed, got %d", result.Failed)
		}
		if result.Indexed != 0 {
			t.Errorf("expected 0 indexed, got %d", result.Indexed)
		}
	})
}

// =============================================================================
// Test: Index - Duration Tracking
// =============================================================================

func TestIndex_DurationTracking(t *testing.T) {
	t.Run("tracks duration in result", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Duration <= 0 {
			t.Error("expected positive duration")
		}
	})
}

// =============================================================================
// Test: ChangeOperation String
// =============================================================================

func TestChangeOperation_String(t *testing.T) {
	tests := []struct {
		op       ChangeOperation
		expected string
	}{
		{OpCreate, "create"},
		{OpModify, "modify"},
		{OpDelete, "delete"},
		{OpRename, "rename"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.op.String(); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

// =============================================================================
// Test: Batch Indexing (PF.3.6)
// =============================================================================

func TestIndex_UsesBatchIndexing(t *testing.T) {
	t.Run("uses IndexBatch for multiple create operations", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file1.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file1.go"),
			},
			{
				Path:      "/test/file2.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file2.go"),
			},
			{
				Path:      "/test/file3.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file3.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Indexed != 3 {
			t.Errorf("expected 3 indexed, got %d", result.Indexed)
		}
		// Should use batch indexing (single call)
		if indexMgr.batchCalls != 1 {
			t.Errorf("expected 1 batch call, got %d", indexMgr.batchCalls)
		}
		if len(indexMgr.indexed) != 3 {
			t.Errorf("expected 3 documents in index, got %d", len(indexMgr.indexed))
		}
	})

	t.Run("handles batch index error", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		indexMgr.indexBatchErr = errors.New("batch index error")
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		changes := []FileChange{
			{
				Path:      "/test/file1.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file1.go"),
			},
			{
				Path:      "/test/file2.go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file2.go"),
			},
		}

		result, err := indexer.Index(context.Background(), changes)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Both should be recorded as failures
		if result.Failed != 2 {
			t.Errorf("expected 2 failed, got %d", result.Failed)
		}
		if result.Indexed != 0 {
			t.Errorf("expected 0 indexed, got %d", result.Indexed)
		}
	})
}

// =============================================================================
// Test: Batch Accumulation (PF.3.6)
// =============================================================================

func TestAccumulateChange(t *testing.T) {
	t.Run("accumulates changes without immediate flush", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		config := IncrementalIndexerConfig{
			BatchSize:    10,
			MaxBatchSize: 100, // High threshold to prevent auto-flush
		}

		indexer := NewIncrementalIndexer(
			config,
			indexMgr,
			checksumStore,
			docBuilder,
		)

		change := &FileChange{
			Path:      "/test/file.go",
			Operation: OpCreate,
			FileInfo:  createTestFileInfo("/test/file.go"),
		}

		indexer.AccumulateChange(change)

		// Should have 1 pending change
		if indexer.PendingCount() != 1 {
			t.Errorf("expected 1 pending change, got %d", indexer.PendingCount())
		}
		// Index should not have been called yet
		if indexMgr.batchCalls != 0 {
			t.Errorf("expected 0 batch calls, got %d", indexMgr.batchCalls)
		}
	})

	t.Run("auto-flushes when maxBatchSize reached", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		config := IncrementalIndexerConfig{
			BatchSize:    10,
			MaxBatchSize: 3, // Low threshold for auto-flush
		}

		indexer := NewIncrementalIndexer(
			config,
			indexMgr,
			checksumStore,
			docBuilder,
		)

		// Accumulate 3 changes (should trigger auto-flush)
		for i := 0; i < 3; i++ {
			change := &FileChange{
				Path:      "/test/file" + string(rune('0'+i)) + ".go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file" + string(rune('0'+i)) + ".go"),
			}
			indexer.AccumulateChange(change)
		}

		// Should have flushed
		if indexer.PendingCount() != 0 {
			t.Errorf("expected 0 pending changes after auto-flush, got %d", indexer.PendingCount())
		}
		// Should have called batch index
		if indexMgr.batchCalls != 1 {
			t.Errorf("expected 1 batch call after auto-flush, got %d", indexMgr.batchCalls)
		}
	})
}

func TestFlushPending(t *testing.T) {
	t.Run("flushes accumulated changes", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		config := IncrementalIndexerConfig{
			BatchSize:    10,
			MaxBatchSize: 100,
		}

		indexer := NewIncrementalIndexer(
			config,
			indexMgr,
			checksumStore,
			docBuilder,
		)

		// Accumulate some changes
		for i := 0; i < 5; i++ {
			change := &FileChange{
				Path:      "/test/file" + string(rune('0'+i)) + ".go",
				Operation: OpCreate,
				FileInfo:  createTestFileInfo("/test/file" + string(rune('0'+i)) + ".go"),
			}
			indexer.AccumulateChange(change)
		}

		if indexer.PendingCount() != 5 {
			t.Errorf("expected 5 pending changes, got %d", indexer.PendingCount())
		}

		// Flush
		err := indexer.FlushPending(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should be empty now
		if indexer.PendingCount() != 0 {
			t.Errorf("expected 0 pending changes after flush, got %d", indexer.PendingCount())
		}
		// Documents should be indexed
		if len(indexMgr.indexed) != 5 {
			t.Errorf("expected 5 indexed documents, got %d", len(indexMgr.indexed))
		}
	})

	t.Run("returns nil for empty pending", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			defaultTestConfig(),
			indexMgr,
			checksumStore,
			docBuilder,
		)

		err := indexer.FlushPending(context.Background())
		if err != nil {
			t.Errorf("expected nil error for empty flush, got %v", err)
		}
	})

	t.Run("handles delete operations", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		// Pre-store checksum for delete
		checksumStore.SetChecksum("/test/delete.go", "some-checksum")

		config := IncrementalIndexerConfig{
			BatchSize:    10,
			MaxBatchSize: 100,
		}

		indexer := NewIncrementalIndexer(
			config,
			indexMgr,
			checksumStore,
			docBuilder,
		)

		// Accumulate a delete
		indexer.AccumulateChange(&FileChange{
			Path:      "/test/delete.go",
			Operation: OpDelete,
		})

		// Flush
		err := indexer.FlushPending(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have deleted
		if !indexMgr.deleted["/test/delete.go"] {
			t.Error("expected file to be deleted")
		}
		// Checksum should be removed
		if _, ok := checksumStore.GetChecksum("/test/delete.go"); ok {
			t.Error("expected checksum to be deleted")
		}
	})

	t.Run("handles mixed operations", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		config := IncrementalIndexerConfig{
			BatchSize:    10,
			MaxBatchSize: 100,
		}

		indexer := NewIncrementalIndexer(
			config,
			indexMgr,
			checksumStore,
			docBuilder,
		)

		// Accumulate mixed operations
		indexer.AccumulateChange(&FileChange{
			Path:      "/test/create.go",
			Operation: OpCreate,
			FileInfo:  createTestFileInfo("/test/create.go"),
		})
		indexer.AccumulateChange(&FileChange{
			Path:      "/test/delete.go",
			Operation: OpDelete,
		})
		indexer.AccumulateChange(&FileChange{
			Path:      "/test/modify.go",
			Operation: OpModify,
			FileInfo:  createTestFileInfo("/test/modify.go"),
		})

		// Flush
		err := indexer.FlushPending(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have 2 indexed (create + modify)
		if len(indexMgr.indexed) != 2 {
			t.Errorf("expected 2 indexed documents, got %d", len(indexMgr.indexed))
		}
		// Should have 1 deleted
		if !indexMgr.deleted["/test/delete.go"] {
			t.Error("expected delete.go to be deleted")
		}
	})
}

func TestMaxBatchSize(t *testing.T) {
	t.Run("returns configured max batch size", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		config := IncrementalIndexerConfig{
			MaxBatchSize: 50,
		}

		indexer := NewIncrementalIndexer(
			config,
			indexMgr,
			checksumStore,
			docBuilder,
		)

		if indexer.MaxBatchSize() != 50 {
			t.Errorf("expected max batch size 50, got %d", indexer.MaxBatchSize())
		}
	})

	t.Run("uses default when not configured", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			IncrementalIndexerConfig{},
			indexMgr,
			checksumStore,
			docBuilder,
		)

		if indexer.MaxBatchSize() != DefaultMaxBatchSize {
			t.Errorf("expected default max batch size %d, got %d", DefaultMaxBatchSize, indexer.MaxBatchSize())
		}
	})
}

func TestFlushInterval(t *testing.T) {
	t.Run("returns configured flush interval", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		config := IncrementalIndexerConfig{
			FlushInterval: 200 * time.Millisecond,
		}

		indexer := NewIncrementalIndexer(
			config,
			indexMgr,
			checksumStore,
			docBuilder,
		)

		if indexer.FlushInterval() != 200*time.Millisecond {
			t.Errorf("expected flush interval 200ms, got %v", indexer.FlushInterval())
		}
	})

	t.Run("uses default when not configured", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		indexer := NewIncrementalIndexer(
			IncrementalIndexerConfig{},
			indexMgr,
			checksumStore,
			docBuilder,
		)

		if indexer.FlushInterval() != DefaultFlushInterval {
			t.Errorf("expected default flush interval %v, got %v", DefaultFlushInterval, indexer.FlushInterval())
		}
	})
}

// =============================================================================
// Test: Concurrent Batch Accumulation (PF.3.6)
// =============================================================================

func TestAccumulateChange_Concurrent(t *testing.T) {
	t.Run("handles concurrent accumulation safely", func(t *testing.T) {
		indexMgr := newIncrementalMockIndexManager()
		checksumStore := newIncrementalMockChecksumStore()
		docBuilder := newIncrementalMockDocumentBuilder()

		config := IncrementalIndexerConfig{
			MaxBatchSize: 1000, // High to prevent auto-flush
		}

		indexer := NewIncrementalIndexer(
			config,
			indexMgr,
			checksumStore,
			docBuilder,
		)

		// Spawn multiple goroutines to accumulate changes
		var wg sync.WaitGroup
		numGoroutines := 10
		changesPerGoroutine := 10

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < changesPerGoroutine; j++ {
					path := "/test/file" + string(rune('A'+id)) + string(rune('0'+j)) + ".go"
					change := &FileChange{
						Path:      path,
						Operation: OpCreate,
						FileInfo:  createTestFileInfo(path),
					}
					indexer.AccumulateChange(change)
				}
			}(i)
		}

		wg.Wait()

		// All changes should be pending
		expected := numGoroutines * changesPerGoroutine
		if indexer.PendingCount() != expected {
			t.Errorf("expected %d pending changes, got %d", expected, indexer.PendingCount())
		}

		// Flush and verify
		err := indexer.FlushPending(context.Background())
		if err != nil {
			t.Fatalf("unexpected error during flush: %v", err)
		}

		if indexer.PendingCount() != 0 {
			t.Errorf("expected 0 pending after flush, got %d", indexer.PendingCount())
		}
	})
}
