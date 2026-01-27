package sylkdir

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/bleve"
)

func TestBleveStoreInit(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)
	if err := store.Open(); err != nil {
		t.Fatalf("BleveStore open failed: %v", err)
	}
	defer store.Close()

	// Verify index was created
	if !store.Exists() {
		t.Error("Expected index to exist after open")
	}

	// Verify it's at the correct path
	expectedPath := tmpDir + "/.sylk/bleve/index/documents.bleve"
	if store.IndexPath() != expectedPath {
		t.Errorf("IndexPath = %s, want %s", store.IndexPath(), expectedPath)
	}
}

func TestBleveStorePaths(t *testing.T) {
	tmpDir := "/project/root"
	sd := New(tmpDir)
	store := NewBleveStore(sd)

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"Path", store.Path(), "/project/root/.sylk/bleve"},
		{"IndexPath", store.IndexPath(), "/project/root/.sylk/bleve/index/documents.bleve"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %s, expected %s", tt.got, tt.expected)
			}
		})
	}
}

func TestBleveStoreExistsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)

	// Should not exist before opening
	if store.Exists() {
		t.Error("Exists should return false before opening")
	}
}

func TestBleveStoreOpenClose(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)

	// Open
	if err := store.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Verify manager is set
	if store.Manager() == nil {
		t.Error("Manager should be set after open")
	}

	// Close
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestBleveStoreIndexAndSearch(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)
	if err := store.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Index a document
	doc := &search.Document{
		ID:         "test-doc-1",
		Path:       "/test/file.go",
		Type:       search.DocTypeSourceCode,
		Language:   "go",
		Content:    "func TestExample() { return nil }",
		ModifiedAt: time.Now(),
		IndexedAt:  time.Now(),
	}

	if err := store.Index(ctx, doc); err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// Give Bleve time to index
	time.Sleep(100 * time.Millisecond)

	// Verify document count
	count, err := store.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("DocumentCount = %d, want 1", count)
	}

	// Search for the document
	result, err := store.Search(ctx, &search.SearchRequest{
		Query: "TestExample",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if result.TotalHits < 1 {
		t.Errorf("TotalHits = %d, want >= 1", result.TotalHits)
	}
}

func TestBleveStoreDelete(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)
	if err := store.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Index something
	ctx := context.Background()
	doc := &search.Document{
		ID:        "test-doc",
		Path:      "/test/file.go",
		Content:   "test content",
		IndexedAt: time.Now(),
	}
	store.Index(ctx, doc)

	// Delete store
	if err := store.Delete(); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify index is gone
	if store.Exists() {
		t.Error("Exists should return false after delete")
	}
}

func TestBleveStorePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	ctx := context.Background()

	// Create and populate store
	store1 := NewBleveStore(sd)
	if err := store1.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	doc := &search.Document{
		ID:        "persist-doc",
		Path:      "/test/persist.go",
		Content:   "persisted content unique",
		IndexedAt: time.Now(),
	}
	if err := store1.Index(ctx, doc); err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// Give Bleve time to flush
	time.Sleep(100 * time.Millisecond)

	// Close store
	if err := store1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Reopen with new store instance
	store2 := NewBleveStore(sd)
	if err := store2.Open(); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer store2.Close()

	// Verify document persisted
	count, err := store2.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("DocumentCount after reopen = %d, want 1", count)
	}

	// Search for the persisted document
	result, err := store2.Search(ctx, &search.SearchRequest{
		Query: "persisted unique",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if result.TotalHits < 1 {
		t.Errorf("TotalHits = %d, want >= 1", result.TotalHits)
	}
}

func TestBleveStoreStats(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)

	// Stats before open
	stats := store.Stats()
	if stats.Exists {
		t.Error("Stats.Exists should be false before open")
	}
	if stats.IsOpen {
		t.Error("Stats.IsOpen should be false before open")
	}

	// Open
	if err := store.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	// Stats after open
	stats = store.Stats()
	if !stats.Exists {
		t.Error("Stats.Exists should be true after open")
	}
	if !stats.IsOpen {
		t.Error("Stats.IsOpen should be true after open")
	}
	if !stats.IndexHealthy {
		t.Error("Stats.IndexHealthy should be true for new index")
	}
}

func TestBleveStoreOpenWithConfig(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)

	config := bleve.IndexConfig{
		Path:          "/should/be/overridden",
		BatchSize:     50,
		MaxConcurrent: 2,
		BatchTimeout:  60 * time.Second,
	}

	if err := store.OpenWithConfig(config); err != nil {
		t.Fatalf("OpenWithConfig failed: %v", err)
	}
	defer store.Close()

	// Verify path was overridden to use SylkDir
	if !strings.Contains(store.Manager().Path(), tmpDir) {
		t.Errorf("Path should contain tmpDir, got %s", store.Manager().Path())
	}
}

func TestBleveStoreIndexBatch(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)
	if err := store.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create batch of documents
	docs := make([]*search.Document, 10)
	for i := range docs {
		docs[i] = &search.Document{
			ID:        search.GenerateDocumentID([]byte{byte(i)}),
			Path:      "/test/batch" + string(rune('0'+i)) + ".go",
			Content:   "batch document content",
			IndexedAt: time.Now(),
		}
	}

	if err := store.IndexBatch(ctx, docs); err != nil {
		t.Fatalf("IndexBatch failed: %v", err)
	}

	// Give Bleve time to index
	time.Sleep(100 * time.Millisecond)

	// Verify all documents were indexed
	count, err := store.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount failed: %v", err)
	}
	if count != 10 {
		t.Errorf("DocumentCount = %d, want 10", count)
	}
}

func TestBleveStoreDeleteNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	// Don't init SylkDir - bleve path doesn't exist

	store := NewBleveStore(sd)

	// Delete should not error for nonexistent path
	if err := store.Delete(); err != nil {
		t.Errorf("Delete should not error for nonexistent path: %v", err)
	}
}

func TestBleveStoreOperationsOnClosedStore(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)

	// Operations on unopened store should error
	ctx := context.Background()

	_, err := store.DocumentCount()
	if err == nil {
		t.Error("DocumentCount should error on closed store")
	}

	err = store.Index(ctx, &search.Document{ID: "test"})
	if err == nil {
		t.Error("Index should error on closed store")
	}

	_, err = store.Search(ctx, &search.SearchRequest{Query: "test"})
	if err == nil {
		t.Error("Search should error on closed store")
	}
}

func TestBleveStoreIndexPathInSylkDir(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewBleveStore(sd)
	if err := store.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	// Verify index is created inside .sylk directory
	indexPath := store.IndexPath()
	if !strings.HasPrefix(indexPath, sd.RootPath()) {
		t.Errorf("Index path %s should be inside .sylk directory %s", indexPath, sd.RootPath())
	}

	// Verify the actual index directory exists
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Errorf("Index directory should exist at %s", indexPath)
	}
}
