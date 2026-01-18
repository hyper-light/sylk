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
	deleteErr      error
	deleteByPathFn func(ctx context.Context, path string) error
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
		if result.Indexed != 1 {
			t.Errorf("expected 1 indexed, got %d", result.Indexed)
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
		// All 4 operations should succeed
		if result.Indexed != 4 {
			t.Errorf("expected 4 indexed, got %d", result.Indexed)
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
		indexMgr.indexErr = errors.New("index error")
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
		indexMgr.indexErr = errors.New("index error")
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
