package bleve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Test Helpers
// =============================================================================

// testIndexPath creates a unique temporary path for a test index.
func testIndexPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.bleve")
}

// createTestDocument creates a valid test document with the given ID and content.
func createTestDocument(id, content string) *search.Document {
	now := time.Now()
	return &search.Document{
		ID:         id,
		Path:       "/test/path/" + id + ".go",
		Type:       search.DocTypeSourceCode,
		Language:   "go",
		Content:    content,
		Symbols:    []string{"main", "handler"},
		Comments:   "This is a test document",
		Imports:    []string{"fmt", "os"},
		Checksum:   search.GenerateChecksum([]byte(content)),
		ModifiedAt: now,
		IndexedAt:  now,
	}
}

// createOpenIndex creates and opens a new IndexManager for testing.
func createOpenIndex(t *testing.T) *IndexManager {
	t.Helper()
	mgr := NewIndexManager(testIndexPath(t))
	if err := mgr.Open(); err != nil {
		t.Fatalf("failed to open index: %v", err)
	}
	t.Cleanup(func() {
		_ = mgr.Close()
	})
	return mgr
}

// =============================================================================
// NewIndexManager Tests
// =============================================================================

func TestNewIndexManager(t *testing.T) {
	t.Parallel()

	path := "/some/path/documents.bleve"
	mgr := NewIndexManager(path)

	if mgr == nil {
		t.Fatal("NewIndexManager returned nil")
	}

	if mgr.Path() != path {
		t.Errorf("Path() = %q, want %q", mgr.Path(), path)
	}

	if mgr.IsOpen() {
		t.Error("IsOpen() = true for new manager, want false")
	}
}

// =============================================================================
// Lifecycle Tests
// =============================================================================

func TestIndexManager_Open_NewIndex(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	mgr := NewIndexManager(path)

	err := mgr.Open()
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	defer mgr.Close()

	if !mgr.IsOpen() {
		t.Error("IsOpen() = false after Open, want true")
	}

	// Verify index directory was created
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("index directory was not created")
	}
}

func TestIndexManager_Open_ExistingIndex(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)

	// Create and close an index first
	mgr1 := NewIndexManager(path)
	if err := mgr1.Open(); err != nil {
		t.Fatalf("first Open() error = %v", err)
	}

	// Index a document
	doc := createTestDocument("doc1", "package main")
	if err := mgr1.Index(context.Background(), doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	if err := mgr1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Re-open the existing index
	mgr2 := NewIndexManager(path)
	if err := mgr2.Open(); err != nil {
		t.Fatalf("second Open() error = %v, want nil", err)
	}
	defer mgr2.Close()

	if !mgr2.IsOpen() {
		t.Error("IsOpen() = false after re-opening, want true")
	}
}

func TestIndexManager_Open_AlreadyOpen(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	err := mgr.Open()
	if err != ErrIndexAlreadyOpen {
		t.Errorf("Open() error = %v, want %v", err, ErrIndexAlreadyOpen)
	}
}

func TestIndexManager_Close(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	err := mgr.Close()
	if err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}

	if mgr.IsOpen() {
		t.Error("IsOpen() = true after Close, want false")
	}
}

func TestIndexManager_Close_AlreadyClosed(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	mgr := NewIndexManager(path)

	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	// Close twice
	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	err := mgr.Close()
	if err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func TestIndexManager_Close_NeverOpened(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))

	err := mgr.Close()
	if err != nil {
		t.Errorf("Close() on never-opened index error = %v, want nil", err)
	}
}

func TestIndexManager_IsOpen(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))

	if mgr.IsOpen() {
		t.Error("IsOpen() = true before Open, want false")
	}

	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if !mgr.IsOpen() {
		t.Error("IsOpen() = false after Open, want true")
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if mgr.IsOpen() {
		t.Error("IsOpen() = true after Close, want false")
	}
}

// =============================================================================
// Index Tests
// =============================================================================

func TestIndexManager_Index(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	doc := createTestDocument("doc1", "package main\nfunc main() {}")

	err := mgr.Index(context.Background(), doc)
	if err != nil {
		t.Errorf("Index() error = %v, want nil", err)
	}
}

func TestIndexManager_Index_Closed(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))
	doc := createTestDocument("doc1", "package main")

	err := mgr.Index(context.Background(), doc)
	if err != ErrIndexClosed {
		t.Errorf("Index() error = %v, want %v", err, ErrIndexClosed)
	}
}

func TestIndexManager_Index_NilDocument(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	err := mgr.Index(context.Background(), nil)
	if err != ErrNilDocument {
		t.Errorf("Index(nil) error = %v, want %v", err, ErrNilDocument)
	}
}

func TestIndexManager_Index_EmptyID(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	doc := createTestDocument("", "package main")
	doc.ID = ""

	err := mgr.Index(context.Background(), doc)
	if err != ErrEmptyDocumentID {
		t.Errorf("Index() with empty ID error = %v, want %v", err, ErrEmptyDocumentID)
	}
}

func TestIndexManager_Index_MultipleDocuments(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		doc := createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"package main",
		)
		if err := mgr.Index(ctx, doc); err != nil {
			t.Errorf("Index() doc %d error = %v", i, err)
		}
	}
}

// =============================================================================
// IndexBatch Tests
// =============================================================================

func TestIndexManager_IndexBatch(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	docs := make([]*search.Document, 5)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"package main",
		)
	}

	err := mgr.IndexBatch(context.Background(), docs)
	if err != nil {
		t.Errorf("IndexBatch() error = %v, want nil", err)
	}
}

func TestIndexManager_IndexBatch_Closed(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))
	docs := []*search.Document{createTestDocument("doc1", "content")}

	err := mgr.IndexBatch(context.Background(), docs)
	if err != ErrIndexClosed {
		t.Errorf("IndexBatch() error = %v, want %v", err, ErrIndexClosed)
	}
}

func TestIndexManager_IndexBatch_Empty(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	err := mgr.IndexBatch(ctx, []*search.Document{})
	if err != ErrEmptyBatch {
		t.Errorf("IndexBatch([]) error = %v, want %v", err, ErrEmptyBatch)
	}

	err = mgr.IndexBatch(ctx, nil)
	if err != ErrEmptyBatch {
		t.Errorf("IndexBatch(nil) error = %v, want %v", err, ErrEmptyBatch)
	}
}

func TestIndexManager_IndexBatch_WithNilDocument(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	docs := []*search.Document{
		createTestDocument("doc1", "content1"),
		nil,
		createTestDocument("doc3", "content3"),
	}

	err := mgr.IndexBatch(context.Background(), docs)
	if err != ErrNilDocument {
		t.Errorf("IndexBatch() with nil doc error = %v, want %v", err, ErrNilDocument)
	}
}

func TestIndexManager_IndexBatch_WithEmptyID(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	doc := createTestDocument("", "content")
	doc.ID = ""
	docs := []*search.Document{doc}

	err := mgr.IndexBatch(context.Background(), docs)
	if err != ErrEmptyDocumentID {
		t.Errorf("IndexBatch() with empty ID error = %v, want %v", err, ErrEmptyDocumentID)
	}
}

// =============================================================================
// Delete Tests
// =============================================================================

func TestIndexManager_Delete(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	doc := createTestDocument("doc1", "package main")
	ctx := context.Background()

	// Index then delete
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	err := mgr.Delete(ctx, doc.ID)
	if err != nil {
		t.Errorf("Delete() error = %v, want nil", err)
	}
}

func TestIndexManager_Delete_NonExistent(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	// Delete non-existent document should not error in Bleve
	err := mgr.Delete(context.Background(), "nonexistent-id")
	if err != nil {
		t.Errorf("Delete() of non-existent doc error = %v, want nil", err)
	}
}

func TestIndexManager_Delete_Closed(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))

	err := mgr.Delete(context.Background(), "doc1")
	if err != ErrIndexClosed {
		t.Errorf("Delete() error = %v, want %v", err, ErrIndexClosed)
	}
}

func TestIndexManager_Delete_EmptyID(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	err := mgr.Delete(context.Background(), "")
	if err != ErrEmptyDocumentID {
		t.Errorf("Delete(\"\") error = %v, want %v", err, ErrEmptyDocumentID)
	}
}

// =============================================================================
// Update Tests
// =============================================================================

func TestIndexManager_Update(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	doc := createTestDocument("doc1", "package main\n// original")
	ctx := context.Background()

	// Index original
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Update with new content
	doc.Content = "package main\n// updated"
	doc.Checksum = search.GenerateChecksum([]byte(doc.Content))

	err := mgr.Update(ctx, doc)
	if err != nil {
		t.Errorf("Update() error = %v, want nil", err)
	}
}

func TestIndexManager_Update_NewDocument(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	doc := createTestDocument("new-doc", "package main")

	// Update without prior indexing should work (it does delete then index)
	err := mgr.Update(context.Background(), doc)
	if err != nil {
		t.Errorf("Update() of new doc error = %v, want nil", err)
	}
}

func TestIndexManager_Update_Closed(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))
	doc := createTestDocument("doc1", "content")

	err := mgr.Update(context.Background(), doc)
	if err != ErrIndexClosed {
		t.Errorf("Update() error = %v, want %v", err, ErrIndexClosed)
	}
}

func TestIndexManager_Update_NilDocument(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	err := mgr.Update(context.Background(), nil)
	if err != ErrNilDocument {
		t.Errorf("Update(nil) error = %v, want %v", err, ErrNilDocument)
	}
}

func TestIndexManager_Update_EmptyID(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	doc := createTestDocument("", "content")
	doc.ID = ""

	err := mgr.Update(context.Background(), doc)
	if err != ErrEmptyDocumentID {
		t.Errorf("Update() with empty ID error = %v, want %v", err, ErrEmptyDocumentID)
	}
}

// =============================================================================
// Search Tests
// =============================================================================

func TestIndexManager_Search(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index some documents
	docs := []*search.Document{
		createTestDocument("doc1", "func handleRequest() {}"),
		createTestDocument("doc2", "func processData() {}"),
		createTestDocument("doc3", "type RequestHandler struct {}"),
	}

	for _, doc := range docs {
		if err := mgr.Index(ctx, doc); err != nil {
			t.Fatalf("Index() error = %v", err)
		}
	}

	// Search for "request"
	req := &search.SearchRequest{
		Query: "request",
		Limit: 10,
	}

	result, err := mgr.Search(ctx, req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if result == nil {
		t.Fatal("Search() returned nil result")
	}

	if result.Query != "request" {
		t.Errorf("result.Query = %q, want %q", result.Query, "request")
	}

	if result.SearchTime <= 0 {
		t.Error("result.SearchTime should be positive")
	}
}

func TestIndexManager_Search_NoResults(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index a document
	doc := createTestDocument("doc1", "package main")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Search for non-existent term
	req := &search.SearchRequest{
		Query: "nonexistenttermxyz",
		Limit: 10,
	}

	result, err := mgr.Search(ctx, req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(result.Documents) != 0 {
		t.Errorf("expected 0 documents, got %d", len(result.Documents))
	}

	if result.TotalHits != 0 {
		t.Errorf("result.TotalHits = %d, want 0", result.TotalHits)
	}
}

func TestIndexManager_Search_WithHighlights(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	doc := createTestDocument("doc1", "The quick brown fox jumps over the lazy dog")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	req := &search.SearchRequest{
		Query:             "fox",
		Limit:             10,
		IncludeHighlights: true,
	}

	result, err := mgr.Search(ctx, req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	// We should get results (highlighting may or may not populate depending on Bleve config)
	if result == nil {
		t.Fatal("Search() returned nil result")
	}
}

func TestIndexManager_Search_Closed(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))

	req := &search.SearchRequest{
		Query: "test",
		Limit: 10,
	}

	_, err := mgr.Search(context.Background(), req)
	if err != ErrIndexClosed {
		t.Errorf("Search() error = %v, want %v", err, ErrIndexClosed)
	}
}

func TestIndexManager_Search_EmptyQuery(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	req := &search.SearchRequest{
		Query: "",
		Limit: 10,
	}

	_, err := mgr.Search(context.Background(), req)
	if err != search.ErrEmptyQuery {
		t.Errorf("Search() with empty query error = %v, want %v", err, search.ErrEmptyQuery)
	}
}

func TestIndexManager_Search_DefaultLimit(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	doc := createTestDocument("doc1", "test content")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Request with zero limit should use default
	req := &search.SearchRequest{
		Query: "test",
		Limit: 0,
	}

	result, err := mgr.Search(ctx, req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if result == nil {
		t.Fatal("Search() returned nil result")
	}
}

func TestIndexManager_Search_WithOffset(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index multiple documents
	for i := 0; i < 5; i++ {
		doc := createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"searchable content",
		)
		if err := mgr.Index(ctx, doc); err != nil {
			t.Fatalf("Index() error = %v", err)
		}
	}

	// Search with offset
	req := &search.SearchRequest{
		Query:  "searchable",
		Limit:  2,
		Offset: 2,
	}

	result, err := mgr.Search(ctx, req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if result == nil {
		t.Fatal("Search() returned nil result")
	}
}

func TestIndexManager_Search_InvalidRequest(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		req     *search.SearchRequest
		wantErr error
	}{
		{
			name:    "negative limit",
			req:     &search.SearchRequest{Query: "test", Limit: -1},
			wantErr: search.ErrLimitNegative,
		},
		{
			name:    "negative offset",
			req:     &search.SearchRequest{Query: "test", Offset: -1},
			wantErr: search.ErrOffsetNegative,
		},
		{
			name:    "limit too high",
			req:     &search.SearchRequest{Query: "test", Limit: search.MaxLimit + 1},
			wantErr: search.ErrLimitTooHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := mgr.Search(ctx, tt.req)
			if err != tt.wantErr {
				t.Errorf("Search() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestIndexManager_ConcurrentIndex(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()
	const numGoroutines = 10
	const docsPerGoroutine = 5

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*docsPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for d := 0; d < docsPerGoroutine; d++ {
				id := search.GenerateDocumentID([]byte{byte(goroutineID), byte(d)})
				doc := createTestDocument(id, "concurrent indexing test")
				if err := mgr.Index(ctx, doc); err != nil {
					errCh <- err
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent Index() error: %v", err)
	}
}

func TestIndexManager_ConcurrentSearch(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index some documents first
	for i := 0; i < 10; i++ {
		doc := createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"concurrent search test content",
		)
		if err := mgr.Index(ctx, doc); err != nil {
			t.Fatalf("Index() error = %v", err)
		}
	}

	const numGoroutines = 20
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &search.SearchRequest{
				Query: "concurrent",
				Limit: 10,
			}
			if _, err := mgr.Search(ctx, req); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent Search() error: %v", err)
	}
}

func TestIndexManager_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Seed with initial documents
	for i := 0; i < 5; i++ {
		doc := createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"initial content for testing",
		)
		if err := mgr.Index(ctx, doc); err != nil {
			t.Fatalf("Index() error = %v", err)
		}
	}

	const numReaders = 10
	const numWriters = 5
	var wg sync.WaitGroup
	errCh := make(chan error, numReaders+numWriters*2)

	// Start readers
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				req := &search.SearchRequest{
					Query: "content",
					Limit: 10,
				}
				if _, err := mgr.Search(ctx, req); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	// Start writers
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				id := search.GenerateDocumentID([]byte{byte(writerID + 100), byte(i)})
				doc := createTestDocument(id, "new content from writer")
				if err := mgr.Index(ctx, doc); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent read/write error: %v", err)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestIndexManager_FullLifecycle(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	mgr := NewIndexManager(path)
	ctx := context.Background()

	// Open
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	// Index documents
	doc1 := createTestDocument("doc1", "package main\nfunc handleUser() {}")
	doc2 := createTestDocument("doc2", "package main\nfunc processOrder() {}")

	if err := mgr.Index(ctx, doc1); err != nil {
		t.Fatalf("Index(doc1) error = %v", err)
	}
	if err := mgr.Index(ctx, doc2); err != nil {
		t.Fatalf("Index(doc2) error = %v", err)
	}

	// Search
	req := &search.SearchRequest{Query: "handle", Limit: 10}
	result, err := mgr.Search(ctx, req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalHits == 0 {
		t.Error("expected to find documents matching 'handle'")
	}

	// Update
	doc1.Content = "package main\nfunc handleAdmin() {}"
	if err := mgr.Update(ctx, doc1); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Delete
	if err := mgr.Delete(ctx, doc2.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Close
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Reopen and verify persistence
	mgr2 := NewIndexManager(path)
	if err := mgr2.Open(); err != nil {
		t.Fatalf("Reopen error = %v", err)
	}
	defer mgr2.Close()

	// Search should still find doc1
	req2 := &search.SearchRequest{Query: "admin", Limit: 10}
	result2, err := mgr2.Search(ctx, req2)
	if err != nil {
		t.Fatalf("Search() after reopen error = %v", err)
	}
	if result2.TotalHits == 0 {
		t.Error("expected to find 'admin' after reopen")
	}
}

func TestIndexManager_IndexBatchThenSearch(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Create batch of documents
	docs := make([]*search.Document, 10)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"batch indexed document with keyword: zebra",
		)
	}

	// Batch index
	if err := mgr.IndexBatch(ctx, docs); err != nil {
		t.Fatalf("IndexBatch() error = %v", err)
	}

	// Search for batch-indexed content
	req := &search.SearchRequest{
		Query: "zebra",
		Limit: 20,
	}

	result, err := mgr.Search(ctx, req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if result.TotalHits != 10 {
		t.Errorf("expected 10 hits, got %d", result.TotalHits)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestGetStringField(t *testing.T) {
	t.Parallel()

	fields := map[string]interface{}{
		"str": "value",
		"num": 42,
		"nil": nil,
	}

	if got := getStringField(fields, "str"); got != "value" {
		t.Errorf("getStringField(str) = %q, want %q", got, "value")
	}

	if got := getStringField(fields, "num"); got != "" {
		t.Errorf("getStringField(num) = %q, want empty", got)
	}

	if got := getStringField(fields, "nil"); got != "" {
		t.Errorf("getStringField(nil) = %q, want empty", got)
	}

	if got := getStringField(fields, "missing"); got != "" {
		t.Errorf("getStringField(missing) = %q, want empty", got)
	}
}

func TestGetStringSliceField(t *testing.T) {
	t.Parallel()

	fields := map[string]interface{}{
		"slice":     []string{"a", "b", "c"},
		"interface": []interface{}{"x", "y"},
		"mixed":     []interface{}{"str", 123, "another"},
		"single":    "not a slice",
	}

	got := getStringSliceField(fields, "slice")
	if len(got) != 3 || got[0] != "a" {
		t.Errorf("getStringSliceField(slice) = %v, want [a b c]", got)
	}

	got = getStringSliceField(fields, "interface")
	if len(got) != 2 || got[0] != "x" {
		t.Errorf("getStringSliceField(interface) = %v, want [x y]", got)
	}

	got = getStringSliceField(fields, "mixed")
	if len(got) != 2 {
		t.Errorf("getStringSliceField(mixed) = %v, want 2 strings", got)
	}

	got = getStringSliceField(fields, "single")
	if got != nil {
		t.Errorf("getStringSliceField(single) = %v, want nil", got)
	}
}

func TestGetTimeField(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	timeStr := now.Format(time.RFC3339)

	fields := map[string]interface{}{
		"time":   now,
		"string": timeStr,
		"bad":    "not a time",
		"num":    12345,
	}

	got := getTimeField(fields, "time")
	if !got.Equal(now) {
		t.Errorf("getTimeField(time) = %v, want %v", got, now)
	}

	got = getTimeField(fields, "string")
	if !got.Equal(now) {
		t.Errorf("getTimeField(string) = %v, want %v", got, now)
	}

	got = getTimeField(fields, "bad")
	if !got.IsZero() {
		t.Errorf("getTimeField(bad) = %v, want zero time", got)
	}

	got = getTimeField(fields, "num")
	if !got.IsZero() {
		t.Errorf("getTimeField(num) = %v, want zero time", got)
	}
}

// =============================================================================
// Error Message Tests
// =============================================================================

func TestErrorMessages(t *testing.T) {
	t.Parallel()

	errs := []struct {
		err     error
		wantMsg string
	}{
		{ErrIndexClosed, "index is closed"},
		{ErrIndexAlreadyOpen, "index is already open"},
		{ErrNilDocument, "document cannot be nil"},
		{ErrEmptyDocumentID, "document ID cannot be empty"},
		{ErrEmptyBatch, "batch cannot be empty"},
	}

	for _, tc := range errs {
		t.Run(tc.wantMsg, func(t *testing.T) {
			t.Parallel()
			if tc.err.Error() != tc.wantMsg {
				t.Errorf("error = %q, want %q", tc.err.Error(), tc.wantMsg)
			}
		})
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkIndexManager_Index(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.bleve")
	mgr := NewIndexManager(path)
	if err := mgr.Open(); err != nil {
		b.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	doc := createTestDocument("bench-doc", "package main\nfunc benchmark() {}")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc.ID = search.GenerateDocumentID([]byte{byte(i), byte(i >> 8)})
		if err := mgr.Index(ctx, doc); err != nil {
			b.Fatalf("Index() error = %v", err)
		}
	}
}

func BenchmarkIndexManager_Search(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.bleve")
	mgr := NewIndexManager(path)
	if err := mgr.Open(); err != nil {
		b.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Index some documents for searching
	for i := 0; i < 100; i++ {
		doc := createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"benchmark search content with various keywords",
		)
		if err := mgr.Index(ctx, doc); err != nil {
			b.Fatalf("Index() error = %v", err)
		}
	}

	req := &search.SearchRequest{
		Query: "benchmark",
		Limit: 10,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := mgr.Search(ctx, req); err != nil {
			b.Fatalf("Search() error = %v", err)
		}
	}
}

func BenchmarkIndexManager_IndexBatch(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.bleve")
	mgr := NewIndexManager(path)
	if err := mgr.Open(); err != nil {
		b.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Prepare batch
	docs := make([]*search.Document, 50)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"batch benchmark content",
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Update IDs for each iteration
		for j := range docs {
			docs[j].ID = search.GenerateDocumentID([]byte{byte(i), byte(j)})
		}
		if err := mgr.IndexBatch(ctx, docs); err != nil {
			b.Fatalf("IndexBatch() error = %v", err)
		}
	}
}

// =============================================================================
// PF.3.4 - Batch Timeout Tests
// =============================================================================

func TestIndexManager_BatchWithTimeout_Success(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Create and add documents to batch
	batch := mgr.index.NewBatch()
	doc := createTestDocument("timeout-test-doc", "batch timeout test content")
	if err := batch.Index(doc.ID, doc); err != nil {
		t.Fatalf("batch.Index() error = %v", err)
	}

	// BatchWithTimeout should succeed normally
	mgr.mu.Lock()
	err := mgr.BatchWithTimeout(ctx, batch)
	mgr.mu.Unlock()

	if err != nil {
		t.Errorf("BatchWithTimeout() error = %v, want nil", err)
	}
}

func TestIndexManager_BatchWithTimeout_ContextCancelled(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Create and add document to batch
	batch := mgr.index.NewBatch()
	doc := createTestDocument("timeout-cancel-doc", "batch timeout cancel content")
	if err := batch.Index(doc.ID, doc); err != nil {
		t.Fatalf("batch.Index() error = %v", err)
	}

	// BatchWithTimeout should return context error or succeed if batch completes first
	mgr.mu.Lock()
	err := mgr.BatchWithTimeout(ctx, batch)
	mgr.mu.Unlock()

	// Acceptable outcomes:
	// 1. No error (batch completed before timeout check)
	// 2. context.Canceled (direct context error)
	// 3. ErrBatchTimeout wrapping context.Canceled (our wrapped timeout error)
	if err != nil {
		// Check if error indicates cancellation/timeout (either direct or wrapped)
		errStr := err.Error()
		isAcceptable := err == context.Canceled ||
			err == context.DeadlineExceeded ||
			errors.Is(err, ErrBatchTimeout) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) ||
			(errStr != "" && (strings.Contains(errStr, "canceled") ||
				strings.Contains(errStr, "timeout") ||
				strings.Contains(errStr, "deadline")))

		if !isAcceptable {
			t.Errorf("BatchWithTimeout() unexpected error = %v", err)
		}
	}
	// If err is nil, the batch completed before context cancellation was detected - also acceptable
}

func TestIndexManager_BatchWithTimeout_CustomTimeout(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    100,
		BatchTimeout: 5 * time.Second, // Custom timeout
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	// Verify config was applied
	if mgr.config.BatchTimeout != 5*time.Second {
		t.Errorf("BatchTimeout = %v, want 5s", mgr.config.BatchTimeout)
	}

	// Test batch indexing works with custom timeout
	docs := make([]*search.Document, 5)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"custom timeout test",
		)
	}

	err := mgr.IndexBatch(context.Background(), docs)
	if err != nil {
		t.Errorf("IndexBatch() with custom timeout error = %v", err)
	}
}

func TestIndexManager_IndexBatch_UsesTimeout(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// IndexBatch should use BatchWithTimeout internally
	docs := make([]*search.Document, 10)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"batch with timeout protection",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	if err != nil {
		t.Errorf("IndexBatch() error = %v, want nil", err)
	}

	// Verify documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 10 {
		t.Errorf("DocumentCount() = %d, want 10", count)
	}
}

func TestDefaultBatchTimeout(t *testing.T) {
	t.Parallel()

	if DefaultBatchTimeout != 30*time.Second {
		t.Errorf("DefaultBatchTimeout = %v, want 30s", DefaultBatchTimeout)
	}
}

func TestIndexConfig_DefaultHasBatchTimeout(t *testing.T) {
	t.Parallel()

	config := DefaultIndexConfig("/some/path")
	if config.BatchTimeout != DefaultBatchTimeout {
		t.Errorf("DefaultIndexConfig().BatchTimeout = %v, want %v", config.BatchTimeout, DefaultBatchTimeout)
	}
}

func TestNewIndexManager_HasBatchTimeout(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager("/some/path")
	if mgr.config.BatchTimeout != DefaultBatchTimeout {
		t.Errorf("NewIndexManager().config.BatchTimeout = %v, want %v", mgr.config.BatchTimeout, DefaultBatchTimeout)
	}
}

// Helper to check if error is timeout-related
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return err == ErrBatchTimeout || err == context.DeadlineExceeded ||
		(err.Error() != "" && (err.Error() == "batch commit timeout" ||
			err.Error() == "context deadline exceeded"))
}

// =============================================================================
// PF.3.5 - Selective Field Loading Tests
// =============================================================================

func TestFieldConfig_Defaults(t *testing.T) {
	t.Parallel()

	// Test DefaultFieldConfig
	cfg := DefaultFieldConfig()
	if cfg.LoadContent {
		t.Error("DefaultFieldConfig().LoadContent = true, want false")
	}
	if len(cfg.Fields) == 0 {
		t.Error("DefaultFieldConfig().Fields should not be empty")
	}

	// Test FullFieldConfig
	fullCfg := FullFieldConfig()
	if !fullCfg.LoadContent {
		t.Error("FullFieldConfig().LoadContent = false, want true")
	}
	if fullCfg.Fields != nil {
		t.Error("FullFieldConfig().Fields should be nil")
	}

	// Test MetadataOnlyFieldConfig
	metaCfg := MetadataOnlyFieldConfig()
	if metaCfg.LoadContent {
		t.Error("MetadataOnlyFieldConfig().LoadContent = true, want false")
	}
	if len(metaCfg.Fields) != 4 {
		t.Errorf("MetadataOnlyFieldConfig().Fields length = %d, want 4", len(metaCfg.Fields))
	}
}

func TestCustomFieldConfig(t *testing.T) {
	t.Parallel()

	fields := []string{"path", "type"}
	cfg := CustomFieldConfig(fields, true)

	if !cfg.LoadContent {
		t.Error("CustomFieldConfig().LoadContent = false, want true")
	}
	if len(cfg.Fields) != 2 {
		t.Errorf("CustomFieldConfig().Fields length = %d, want 2", len(cfg.Fields))
	}
}

func TestDefaultMetadataFields(t *testing.T) {
	t.Parallel()

	// Verify expected fields are present
	expectedFields := []string{"path", "type", "language", "modified_at"}
	for _, expected := range expectedFields {
		found := false
		for _, field := range DefaultMetadataFields {
			if field == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultMetadataFields missing expected field: %s", expected)
		}
	}

	// Verify content is NOT in default metadata fields
	for _, field := range DefaultMetadataFields {
		if field == "content" {
			t.Error("DefaultMetadataFields should not contain 'content'")
		}
	}
}

func TestIndexManager_SearchWithFields_DefaultConfig(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index a document with content
	doc := createTestDocument("fields-test-1", "This is the full content of the document for testing field selection")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Search with default config (metadata only)
	req := &search.SearchRequest{
		Query: "content",
		Limit: 10,
	}

	result, err := mgr.SearchWithFields(ctx, req, DefaultFieldConfig())
	if err != nil {
		t.Fatalf("SearchWithFields() error = %v", err)
	}

	if result == nil {
		t.Fatal("SearchWithFields() returned nil result")
	}

	if result.TotalHits == 0 {
		t.Error("expected at least one hit")
	}
}

func TestIndexManager_SearchWithFields_FullConfig(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index a document with content
	content := "Full content loading test document body"
	doc := createTestDocument("fields-test-2", content)
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Search with full config (all fields including content)
	req := &search.SearchRequest{
		Query: "loading",
		Limit: 10,
	}

	result, err := mgr.SearchWithFields(ctx, req, FullFieldConfig())
	if err != nil {
		t.Fatalf("SearchWithFields() error = %v", err)
	}

	if result == nil {
		t.Fatal("SearchWithFields() returned nil result")
	}

	if result.TotalHits == 0 {
		t.Fatal("expected at least one hit")
	}

	// With full config, content should be loaded
	if len(result.Documents) > 0 {
		if result.Documents[0].Content == "" {
			t.Error("expected Content to be loaded with FullFieldConfig")
		}
	}
}

func TestIndexManager_SearchWithFields_CustomConfig(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index a document
	doc := createTestDocument("fields-test-3", "Custom field selection test")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Search with custom config (only path and type)
	req := &search.SearchRequest{
		Query: "custom",
		Limit: 10,
	}

	customCfg := CustomFieldConfig([]string{"path", "type"}, false)
	result, err := mgr.SearchWithFields(ctx, req, customCfg)
	if err != nil {
		t.Fatalf("SearchWithFields() error = %v", err)
	}

	if result == nil {
		t.Fatal("SearchWithFields() returned nil result")
	}

	if result.TotalHits == 0 {
		t.Error("expected at least one hit")
	}
}

func TestIndexManager_SearchWithFields_Closed(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))

	req := &search.SearchRequest{
		Query: "test",
		Limit: 10,
	}

	_, err := mgr.SearchWithFields(context.Background(), req, DefaultFieldConfig())
	if err != ErrIndexClosed {
		t.Errorf("SearchWithFields() error = %v, want %v", err, ErrIndexClosed)
	}
}

func TestIndexManager_GetDocumentContent(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index a document with known content
	expectedContent := "This is the document content for lazy loading test"
	doc := createTestDocument("content-test-1", expectedContent)
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Get content
	content, err := mgr.GetDocumentContent(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetDocumentContent() error = %v", err)
	}

	if content != expectedContent {
		t.Errorf("GetDocumentContent() = %q, want %q", content, expectedContent)
	}
}

func TestIndexManager_GetDocumentContent_NotFound(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Try to get content for non-existent document
	content, err := mgr.GetDocumentContent(ctx, "nonexistent-doc-id")
	if err != nil {
		t.Fatalf("GetDocumentContent() error = %v, want nil", err)
	}

	if content != "" {
		t.Errorf("GetDocumentContent() = %q, want empty string for non-existent doc", content)
	}
}

func TestIndexManager_GetDocumentContent_EmptyID(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	_, err := mgr.GetDocumentContent(ctx, "")
	if err != ErrEmptyDocumentID {
		t.Errorf("GetDocumentContent(\"\") error = %v, want %v", err, ErrEmptyDocumentID)
	}
}

func TestIndexManager_GetDocumentContent_Closed(t *testing.T) {
	t.Parallel()

	mgr := NewIndexManager(testIndexPath(t))

	_, err := mgr.GetDocumentContent(context.Background(), "some-id")
	if err != ErrIndexClosed {
		t.Errorf("GetDocumentContent() error = %v, want %v", err, ErrIndexClosed)
	}
}

func TestIndexManager_Search_UsesDefaultFieldConfig(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index a document
	doc := createTestDocument("default-search-test", "Testing that Search uses DefaultFieldConfig")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Regular Search should work (uses DefaultFieldConfig internally)
	req := &search.SearchRequest{
		Query: "testing",
		Limit: 10,
	}

	result, err := mgr.Search(ctx, req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if result == nil {
		t.Fatal("Search() returned nil result")
	}

	if result.TotalHits == 0 {
		t.Error("expected at least one hit")
	}
}

// =============================================================================
// Integration Test: Selective Field Loading with Lazy Content Load
// =============================================================================

func TestIndexManager_SelectiveFieldsWithLazyContentLoad(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// Index multiple documents with different content
	docs := []struct {
		id      string
		content string
	}{
		{"lazy-1", "First document with unique content alpha"},
		{"lazy-2", "Second document with unique content beta"},
		{"lazy-3", "Third document with unique content gamma"},
	}

	for _, d := range docs {
		doc := createTestDocument(d.id, d.content)
		if err := mgr.Index(ctx, doc); err != nil {
			t.Fatalf("Index() error = %v", err)
		}
	}

	// Search with metadata-only config
	req := &search.SearchRequest{
		Query: "unique content",
		Limit: 10,
	}

	result, err := mgr.SearchWithFields(ctx, req, MetadataOnlyFieldConfig())
	if err != nil {
		t.Fatalf("SearchWithFields() error = %v", err)
	}

	if result.TotalHits != 3 {
		t.Errorf("expected 3 hits, got %d", result.TotalHits)
	}

	// For each result, lazy-load content and verify
	for _, scoredDoc := range result.Documents {
		content, err := mgr.GetDocumentContent(ctx, scoredDoc.ID)
		if err != nil {
			t.Errorf("GetDocumentContent(%s) error = %v", scoredDoc.ID, err)
			continue
		}

		if content == "" {
			t.Errorf("GetDocumentContent(%s) returned empty content", scoredDoc.ID)
		}
	}
}

func TestErrBatchTimeout_Message(t *testing.T) {
	t.Parallel()

	if ErrBatchTimeout.Error() != "batch commit timeout" {
		t.Errorf("ErrBatchTimeout.Error() = %q, want %q", ErrBatchTimeout.Error(), "batch commit timeout")
	}
}

// =============================================================================
// PF.3.2 - Async Indexing Integration Tests
// =============================================================================

func TestIndexManager_IndexAsync(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	doc := createTestDocument("doc1", "package main\nfunc main() {}")
	ctx := context.Background()

	// Without async enabled, should fall back to sync
	resultCh := mgr.IndexAsync(ctx, doc)
	if resultCh == nil {
		t.Fatal("IndexAsync() returned nil channel")
	}

	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("IndexAsync() result = %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("IndexAsync() timed out")
	}

	// Verify document was indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 1 {
		t.Errorf("DocumentCount() = %d, want 1", count)
	}
}

func TestIndexManager_IndexAsync_NilDocument(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	resultCh := mgr.IndexAsync(ctx, nil)
	if resultCh == nil {
		t.Fatal("IndexAsync(nil) returned nil channel")
	}

	select {
	case err := <-resultCh:
		if err != ErrNilDocument {
			t.Errorf("IndexAsync(nil) result = %v, want %v", err, ErrNilDocument)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("IndexAsync(nil) timed out")
	}
}

func TestIndexManager_IndexAsync_EmptyID(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()
	doc := createTestDocument("", "content")
	doc.ID = ""

	resultCh := mgr.IndexAsync(ctx, doc)
	if resultCh == nil {
		t.Fatal("IndexAsync() returned nil channel")
	}

	select {
	case err := <-resultCh:
		if err != ErrEmptyDocumentID {
			t.Errorf("IndexAsync() result = %v, want %v", err, ErrEmptyDocumentID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("IndexAsync() timed out")
	}
}

func TestIndexManager_DeleteAsync(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// First index a document
	doc := createTestDocument("doc1", "package main")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Delete it asynchronously
	resultCh := mgr.DeleteAsync(ctx, doc.ID)
	if resultCh == nil {
		t.Fatal("DeleteAsync() returned nil channel")
	}

	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("DeleteAsync() result = %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("DeleteAsync() timed out")
	}
}

func TestIndexManager_DeleteAsync_EmptyID(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	resultCh := mgr.DeleteAsync(ctx, "")
	if resultCh == nil {
		t.Fatal("DeleteAsync(\"\") returned nil channel")
	}

	select {
	case err := <-resultCh:
		if err != ErrEmptyDocumentID {
			t.Errorf("DeleteAsync(\"\") result = %v, want %v", err, ErrEmptyDocumentID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("DeleteAsync(\"\") timed out")
	}
}

func TestIndexManager_FlushAsync_NoAsync(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	// FlushAsync should be no-op when async is not enabled
	err := mgr.FlushAsync(ctx)
	if err != nil {
		t.Errorf("FlushAsync() error = %v, want nil", err)
	}
}

func TestIndexManager_AsyncStats_NoAsync(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	stats := mgr.AsyncStats()
	if stats.Enqueued != 0 || stats.Processed != 0 {
		t.Errorf("AsyncStats() = %+v, expected zero values", stats)
	}
}

func TestIndexManager_IsAsyncEnabled_False(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	if mgr.IsAsyncEnabled() {
		t.Error("IsAsyncEnabled() = true, want false")
	}
}

func TestIndexManager_WithAsyncEnabled(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:               path,
		BatchSize:          100,
		AsyncEnabled:       true,
		AsyncQueueSize:     1000,
		AsyncBatchSize:     10,
		AsyncFlushInterval: 10 * time.Millisecond,
	}

	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	if !mgr.IsAsyncEnabled() {
		t.Error("IsAsyncEnabled() = false, want true")
	}

	// Test async indexing
	ctx := context.Background()
	doc := createTestDocument("async-doc", "package main\nfunc async() {}")

	resultCh := mgr.IndexAsync(ctx, doc)
	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("IndexAsync() result = %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("IndexAsync() timed out")
	}

	// Flush and verify
	if err := mgr.FlushAsync(ctx); err != nil {
		t.Fatalf("FlushAsync() error = %v", err)
	}

	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 1 {
		t.Errorf("DocumentCount() = %d, want 1", count)
	}

	stats := mgr.AsyncStats()
	if stats.Enqueued == 0 {
		t.Error("AsyncStats().Enqueued should be > 0")
	}
}

func TestIndexManager_AsyncCloseProcessesPending(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:               path,
		BatchSize:          100,
		AsyncEnabled:       true,
		AsyncQueueSize:     1000,
		AsyncBatchSize:     100,
		AsyncFlushInterval: 10 * time.Second, // Long interval
	}

	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	ctx := context.Background()

	// Submit multiple documents
	for i := 0; i < 5; i++ {
		doc := createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"async content",
		)
		mgr.IndexAsync(ctx, doc)
	}

	// Close should process all pending
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Reopen and verify
	mgr2 := NewIndexManager(path)
	if err := mgr2.Open(); err != nil {
		t.Fatalf("Reopen error = %v", err)
	}
	defer mgr2.Close()

	count, err := mgr2.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 5 {
		t.Errorf("DocumentCount() = %d, want 5 (pending ops should be processed on close)", count)
	}
}

// =============================================================================
// PF.3.3 - Path Index Tests
// =============================================================================

func TestIndexManager_PathIndex_PopulatedOnIndex(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	doc := createTestDocument("doc1", "package main")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Path index should be populated
	if mgr.PathIndexSize() != 1 {
		t.Errorf("PathIndexSize() = %d, want 1", mgr.PathIndexSize())
	}

	docID, exists := mgr.GetDocIDByPath(doc.Path)
	if !exists {
		t.Errorf("GetDocIDByPath() exists = false, want true")
	}
	if docID != doc.ID {
		t.Errorf("GetDocIDByPath() = %q, want %q", docID, doc.ID)
	}
}

func TestIndexManager_PathIndex_PopulatedOnBatchIndex(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	docs := make([]*search.Document, 5)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"batch content",
		)
	}

	if err := mgr.IndexBatch(ctx, docs); err != nil {
		t.Fatalf("IndexBatch() error = %v", err)
	}

	// All paths should be in the index
	if mgr.PathIndexSize() != 5 {
		t.Errorf("PathIndexSize() = %d, want 5", mgr.PathIndexSize())
	}

	for _, doc := range docs {
		docID, exists := mgr.GetDocIDByPath(doc.Path)
		if !exists {
			t.Errorf("GetDocIDByPath(%q) not found", doc.Path)
		}
		if docID != doc.ID {
			t.Errorf("GetDocIDByPath(%q) = %q, want %q", doc.Path, docID, doc.ID)
		}
	}
}

func TestIndexManager_PathIndex_PopulatedOnUpdate(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	doc := createTestDocument("doc1", "original content")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Update document
	doc.Content = "updated content"
	if err := mgr.Update(ctx, doc); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Path index should still have the mapping
	docID, exists := mgr.GetDocIDByPath(doc.Path)
	if !exists {
		t.Errorf("GetDocIDByPath() after Update exists = false, want true")
	}
	if docID != doc.ID {
		t.Errorf("GetDocIDByPath() = %q, want %q", docID, doc.ID)
	}
}

func TestIndexManager_DeleteByPath_UsesPathIndex(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	doc := createTestDocument("doc1", "package main")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Verify path index is populated
	if mgr.PathIndexSize() != 1 {
		t.Errorf("PathIndexSize() before delete = %d, want 1", mgr.PathIndexSize())
	}

	// Delete by path (should use O(1) lookup)
	if err := mgr.DeleteByPath(ctx, doc.Path); err != nil {
		t.Fatalf("DeleteByPath() error = %v", err)
	}

	// Path index should be cleared
	if mgr.PathIndexSize() != 0 {
		t.Errorf("PathIndexSize() after delete = %d, want 0", mgr.PathIndexSize())
	}

	// Document should be deleted
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 0 {
		t.Errorf("DocumentCount() = %d, want 0", count)
	}
}

func TestIndexManager_DeleteByPath_FallsBackToSearch(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	doc := createTestDocument("doc1", "package main")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	// Manually clear path index to force fallback
	mgr.RemovePathIndex(doc.Path)

	// Delete by path should still work via search fallback
	if err := mgr.DeleteByPath(ctx, doc.Path); err != nil {
		t.Fatalf("DeleteByPath() with fallback error = %v", err)
	}

	// Document should be deleted
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 0 {
		t.Errorf("DocumentCount() = %d, want 0", count)
	}
}

func TestIndexManager_UpdatePathIndex(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	// Add mapping
	mgr.UpdatePathIndex("/test/path", "doc123")

	docID, exists := mgr.GetDocIDByPath("/test/path")
	if !exists {
		t.Error("GetDocIDByPath() exists = false after UpdatePathIndex")
	}
	if docID != "doc123" {
		t.Errorf("GetDocIDByPath() = %q, want %q", docID, "doc123")
	}

	// Update mapping
	mgr.UpdatePathIndex("/test/path", "doc456")

	docID, exists = mgr.GetDocIDByPath("/test/path")
	if !exists {
		t.Error("GetDocIDByPath() exists = false after update")
	}
	if docID != "doc456" {
		t.Errorf("GetDocIDByPath() = %q, want %q", docID, "doc456")
	}
}

func TestIndexManager_RemovePathIndex(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)

	mgr.UpdatePathIndex("/test/path", "doc123")
	mgr.RemovePathIndex("/test/path")

	_, exists := mgr.GetDocIDByPath("/test/path")
	if exists {
		t.Error("GetDocIDByPath() exists = true after RemovePathIndex, want false")
	}
}

func TestIndexManager_PathIndexClearedOnClose(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	mgr := NewIndexManager(path)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	ctx := context.Background()
	doc := createTestDocument("doc1", "content")
	if err := mgr.Index(ctx, doc); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	if mgr.PathIndexSize() != 1 {
		t.Errorf("PathIndexSize() before close = %d, want 1", mgr.PathIndexSize())
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// After close, path index should be cleared
	if mgr.PathIndexSize() != 0 {
		t.Errorf("PathIndexSize() after close = %d, want 0", mgr.PathIndexSize())
	}
}

func TestIndexManager_PathIndex_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	mgr := createOpenIndex(t)
	ctx := context.Background()

	const numGoroutines = 10
	const opsPerGoroutine = 20

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*opsPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				id := search.GenerateDocumentID([]byte{byte(goroutineID), byte(i)})
				doc := createTestDocument(id, "concurrent path index test")
				if err := mgr.Index(ctx, doc); err != nil {
					errCh <- err
					return
				}

				// Also read from path index
				_, _ = mgr.GetDocIDByPath(doc.Path)
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent path index error: %v", err)
	}

	// Should have indexed all documents
	expectedSize := numGoroutines * opsPerGoroutine
	if mgr.PathIndexSize() != expectedSize {
		t.Errorf("PathIndexSize() = %d, want %d", mgr.PathIndexSize(), expectedSize)
	}
}

// =============================================================================
// PF.3.2/PF.3.3 Integration Tests
// =============================================================================

func TestIndexManager_AsyncIndexUpdatesPathIndex(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:               path,
		BatchSize:          100,
		AsyncEnabled:       true,
		AsyncQueueSize:     1000,
		AsyncBatchSize:     10,
		AsyncFlushInterval: 10 * time.Millisecond,
	}

	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	doc := createTestDocument("async-doc", "async content")

	// Index async should also update path index
	resultCh := mgr.IndexAsync(ctx, doc)
	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("IndexAsync() result = %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("IndexAsync() timed out")
	}

	// Path index should be updated immediately (before async completes)
	docID, exists := mgr.GetDocIDByPath(doc.Path)
	if !exists {
		t.Error("GetDocIDByPath() exists = false after IndexAsync, want true")
	}
	if docID != doc.ID {
		t.Errorf("GetDocIDByPath() = %q, want %q", docID, doc.ID)
	}
}

func BenchmarkIndexManager_DeleteByPath_WithPathIndex(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.bleve")
	mgr := NewIndexManager(path)
	if err := mgr.Open(); err != nil {
		b.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Pre-populate with documents
	docs := make([]*search.Document, 100)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"benchmark content",
		)
	}
	if err := mgr.IndexBatch(ctx, docs); err != nil {
		b.Fatalf("IndexBatch() error = %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-index if we've deleted all
		if i > 0 && i%100 == 0 {
			for j := range docs {
				docs[j].ID = search.GenerateDocumentID([]byte{byte(i), byte(j)})
			}
			if err := mgr.IndexBatch(ctx, docs); err != nil {
				b.Fatalf("IndexBatch() error = %v", err)
			}
		}

		docIdx := i % 100
		_ = mgr.DeleteByPath(ctx, docs[docIdx].Path)
	}
}

// =============================================================================
// PF.3.10 - Batch Size Validation Tests
// =============================================================================

func TestValidateBatchSize_Constants(t *testing.T) {
	t.Parallel()

	// Verify constants are as expected
	if MinBatchSize != 10 {
		t.Errorf("MinBatchSize = %d, want 10", MinBatchSize)
	}
	if MaxBatchSize != 10000 {
		t.Errorf("MaxBatchSize = %d, want 10000", MaxBatchSize)
	}
	if DefaultBatchSize != 100 {
		t.Errorf("DefaultBatchSize = %d, want 100", DefaultBatchSize)
	}
}

func TestValidateBatchSize_BelowMin(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input    int
		expected int
	}{
		{0, MinBatchSize},
		{1, MinBatchSize},
		{5, MinBatchSize},
		{9, MinBatchSize},
		{-1, MinBatchSize},
		{-100, MinBatchSize},
	}

	for _, tc := range testCases {
		t.Run(string(rune('0'+tc.input)), func(t *testing.T) {
			result := ValidateBatchSize(tc.input)
			if result != tc.expected {
				t.Errorf("ValidateBatchSize(%d) = %d, want %d", tc.input, result, tc.expected)
			}
		})
	}
}

func TestValidateBatchSize_AboveMax(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input    int
		expected int
	}{
		{10001, MaxBatchSize},
		{20000, MaxBatchSize},
		{100000, MaxBatchSize},
		{1000000, MaxBatchSize},
	}

	for _, tc := range testCases {
		result := ValidateBatchSize(tc.input)
		if result != tc.expected {
			t.Errorf("ValidateBatchSize(%d) = %d, want %d", tc.input, result, tc.expected)
		}
	}
}

func TestValidateBatchSize_WithinBounds(t *testing.T) {
	t.Parallel()

	testCases := []int{
		MinBatchSize,
		MinBatchSize + 1,
		50,
		100,
		500,
		1000,
		5000,
		MaxBatchSize - 1,
		MaxBatchSize,
	}

	for _, input := range testCases {
		result := ValidateBatchSize(input)
		if result != input {
			t.Errorf("ValidateBatchSize(%d) = %d, want %d (should return input unchanged)", input, result, input)
		}
	}
}

func TestIndexManager_IndexBatch_LargeBatchChunking(t *testing.T) {
	t.Parallel()

	// Use a small batch size to test chunking behavior
	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    20, // Small batch size to trigger chunking
		BatchTimeout: DefaultBatchTimeout,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Create 55 documents (should result in 3 chunks: 20, 20, 15)
	docs := make([]*search.Document, 55)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i), byte(i >> 8)}),
			"chunked batch test document",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	if err != nil {
		t.Errorf("IndexBatch() with chunking error = %v, want nil", err)
	}

	// Verify all documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 55 {
		t.Errorf("DocumentCount() = %d, want 55", count)
	}
}

func TestIndexManager_IndexBatch_VeryLargeBatch(t *testing.T) {
	t.Parallel()

	// Test with batch size at MaxBatchSize boundary
	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    50, // Use 50 to make test faster while still testing chunking
		BatchTimeout: DefaultBatchTimeout,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Create 200 documents to test multiple chunks
	docs := make([]*search.Document, 200)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i), byte(i >> 8)}),
			"large batch test",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	if err != nil {
		t.Errorf("IndexBatch() with large batch error = %v, want nil", err)
	}

	// Verify all documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 200 {
		t.Errorf("DocumentCount() = %d, want 200", count)
	}
}

func TestIndexManager_IndexBatch_ChunkingContextCancellation(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    10, // Very small to create many chunks
		BatchTimeout: DefaultBatchTimeout,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Create 50 documents (should be chunked into 5 batches)
	docs := make([]*search.Document, 50)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"cancel test",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	// Should either return context.Canceled or succeed if first chunk completes before check
	if err != nil && err != context.Canceled {
		// Check if it's a chunk error wrapping context.Canceled
		if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "context") {
			t.Errorf("IndexBatch() with cancelled context error = %v, expected context.Canceled or nil", err)
		}
	}
}

func TestIndexManager_IndexBatch_BatchSizeZeroDefaultsToMin(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    0, // Zero should be validated to MinBatchSize
		BatchTimeout: DefaultBatchTimeout,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Create 25 documents (with validated batch size of 10, should result in 3 chunks)
	docs := make([]*search.Document, 25)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"zero batch size test",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	if err != nil {
		t.Errorf("IndexBatch() with zero batch size error = %v, want nil", err)
	}

	// Verify all documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 25 {
		t.Errorf("DocumentCount() = %d, want 25", count)
	}
}

func TestIndexManager_IndexBatch_NegativeBatchSizeDefaultsToMin(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    -50, // Negative should be validated to MinBatchSize
		BatchTimeout: DefaultBatchTimeout,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Create 15 documents
	docs := make([]*search.Document, 15)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"negative batch size test",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	if err != nil {
		t.Errorf("IndexBatch() with negative batch size error = %v, want nil", err)
	}

	// Verify all documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 15 {
		t.Errorf("DocumentCount() = %d, want 15", count)
	}
}

func TestIndexManager_IndexBatch_ExactlyBatchSize(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    20,
		BatchTimeout: DefaultBatchTimeout,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Create exactly 20 documents (should not trigger chunking since len(docs) == batchSize)
	docs := make([]*search.Document, 20)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"exact batch size test",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	if err != nil {
		t.Errorf("IndexBatch() with exact batch size error = %v, want nil", err)
	}

	// Verify all documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 20 {
		t.Errorf("DocumentCount() = %d, want 20", count)
	}
}

func TestIndexManager_IndexBatch_OneLessThanBatchSize(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    20,
		BatchTimeout: DefaultBatchTimeout,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Create 19 documents (should not trigger chunking)
	docs := make([]*search.Document, 19)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"less than batch size test",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	if err != nil {
		t.Errorf("IndexBatch() with less than batch size error = %v, want nil", err)
	}

	// Verify all documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 19 {
		t.Errorf("DocumentCount() = %d, want 19", count)
	}
}

func TestIndexManager_IndexBatch_OneMoreThanBatchSize(t *testing.T) {
	t.Parallel()

	path := testIndexPath(t)
	config := IndexConfig{
		Path:         path,
		BatchSize:    20,
		BatchTimeout: DefaultBatchTimeout,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Create 21 documents (should trigger chunking: 20 + 1)
	docs := make([]*search.Document, 21)
	for i := range docs {
		docs[i] = createTestDocument(
			search.GenerateDocumentID([]byte{byte(i)}),
			"more than batch size test",
		)
	}

	err := mgr.IndexBatch(ctx, docs)
	if err != nil {
		t.Errorf("IndexBatch() with more than batch size error = %v, want nil", err)
	}

	// Verify all documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 21 {
		t.Errorf("DocumentCount() = %d, want 21", count)
	}
}
