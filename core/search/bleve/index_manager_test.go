package bleve

import (
	"context"
	"os"
	"path/filepath"
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
