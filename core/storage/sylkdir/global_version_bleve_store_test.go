package sylkdir

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/search"
)

func TestGlobalVersionBleveStoreOpenClose(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	gvbs := NewGlobalVersionBleveStore(sd, gm.GetHead())
	if err := gvbs.OpenHead(); err != nil {
		t.Fatalf("OpenHead: %v", err)
	}

	if err := gvbs.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
}

func TestGlobalVersionBleveStoreOpenExistingHeadOpensExistingIndex(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	head := gm.GetHead()

	writer := NewGlobalVersionBleveStore(sd, head)
	if err := writer.OpenHead(); err != nil {
		t.Fatalf("OpenHead: %v", err)
	}
	if err := writer.CloseAll(); err != nil {
		t.Fatalf("CloseAll writer: %v", err)
	}

	reader := NewGlobalVersionBleveStore(sd, head)
	if err := reader.OpenExistingHead(); err != nil {
		t.Fatalf("OpenExistingHead: %v", err)
	}
	defer reader.CloseAll()
	if _, err := reader.DocumentCount(); err != nil {
		t.Fatalf("DocumentCount: %v", err)
	}
}

func TestGlobalVersionBleveStoreOpenExistingHeadDoesNotCreateMissingIndex(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	gvbs := NewGlobalVersionBleveStore(sd, gm.GetHead())
	bleveDBPath := filepath.Join(gvbs.HeadBlevePath(), "documents.bleve")
	if _, err := os.Stat(bleveDBPath); err == nil {
		t.Fatalf("test setup unexpectedly created %s", bleveDBPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat missing index: %v", err)
	}

	err := gvbs.OpenExistingHead()
	if !errors.Is(err, ErrBleveHeadUnavailable) {
		t.Fatalf("OpenExistingHead error = %v, want ErrBleveHeadUnavailable", err)
	}
	if _, err := os.Stat(bleveDBPath); err == nil {
		t.Fatalf("OpenExistingHead created missing index at %s", bleveDBPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat missing index after open: %v", err)
	}
}

func TestGlobalVersionBleveStoreIndexAndSearch(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	gvbs := NewGlobalVersionBleveStore(sd, gm.GetHead())
	if err := gvbs.OpenHead(); err != nil {
		t.Fatalf("OpenHead: %v", err)
	}
	defer gvbs.CloseAll()

	// Index a document
	ctx := context.Background()
	doc := &search.Document{
		ID:       "file_1",
		Path:     "test.go",
		Type:     search.DocTypeSourceCode,
		Content:  "package main func main() {}",
		Language: "go",
	}
	if err := gvbs.Index(ctx, doc); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Search for it
	result, err := gvbs.Search(ctx, &search.SearchRequest{
		Query: "main",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.TotalHits == 0 {
		t.Error("expected search results, got 0")
	}

	// Document count
	count, err := gvbs.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount: %v", err)
	}
	if count != 1 {
		t.Errorf("DocumentCount = %d, want 1", count)
	}
}

func TestGlobalVersionBleveStoreIndexBatch(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	gvbs := NewGlobalVersionBleveStore(sd, gm.GetHead())
	if err := gvbs.OpenHead(); err != nil {
		t.Fatalf("OpenHead: %v", err)
	}
	defer gvbs.CloseAll()

	// Index batch
	ctx := context.Background()
	docs := []*search.Document{
		{ID: "file_1", Path: "a.go", Content: "package a"},
		{ID: "file_2", Path: "b.go", Content: "package b"},
		{ID: "file_3", Path: "c.go", Content: "package c"},
	}
	if err := gvbs.IndexBatch(ctx, docs); err != nil {
		t.Fatalf("IndexBatch: %v", err)
	}

	count, err := gvbs.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount: %v", err)
	}
	if count != 3 {
		t.Errorf("DocumentCount = %d, want 3", count)
	}
}

func TestGlobalVersionBleveStoreSnapshotBleve(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// v1.0.0
	v1 := gm.GetHead()

	// Write docs to the global doc store (source of truth for lazy Bleve rebuild).
	docStore, err := NewGlobalVersionDocStore(sd, v1)
	if err != nil {
		t.Fatalf("NewGlobalVersionDocStore: %v", err)
	}
	vdocs := []*VersionDocument{
		{ID: "file_1", Path: "a.go", Type: "source_code", Content: "package a", Language: "go"},
		{ID: "file_2", Path: "b.go", Type: "source_code", Content: "package b", Language: "go"},
	}
	if err := docStore.WriteBatch(vdocs); err != nil {
		t.Fatalf("WriteBatch docs: %v", err)
	}
	if err := docStore.Close(); err != nil {
		t.Fatalf("Close docStore: %v", err)
	}

	// Open Bleve and index into v1.
	gvbs := NewGlobalVersionBleveStore(sd, v1)
	if err := gvbs.OpenHead(); err != nil {
		t.Fatalf("OpenHead: %v", err)
	}

	ctx := context.Background()
	searchDocs := []*search.Document{
		{ID: "file_1", Path: "a.go", Content: "package a"},
		{ID: "file_2", Path: "b.go", Content: "package b"},
	}
	if err := gvbs.IndexBatch(ctx, searchDocs); err != nil {
		t.Fatalf("IndexBatch: %v", err)
	}

	countV1, _ := gvbs.DocumentCount()
	if countV1 != 2 {
		t.Fatalf("v1 count = %d, want 2", countV1)
	}

	// Create v2.0.0, clone data, and snapshot Bleve.
	v2 := v1.BumpMajor()
	if err := sd.CreateGlobalVersion(v2); err != nil {
		t.Fatalf("CreateGlobalVersion: %v", err)
	}
	if err := sd.SnapshotGlobalData(v1, v2); err != nil {
		t.Fatalf("SnapshotGlobalData: %v", err)
	}

	if err := gvbs.SnapshotBleve(v1, v2); err != nil {
		t.Fatalf("SnapshotBleve: %v", err)
	}

	// HEAD should now be v2
	if !gvbs.Head().Equal(v2) {
		t.Errorf("HEAD = %s, want %s", gvbs.Head().String(), v2.String())
	}

	// v2 should have the same docs as v1 (lazy rebuild from doc store)
	countV2, _ := gvbs.DocumentCount()
	if countV2 != 2 {
		t.Errorf("v2 count = %d, want 2", countV2)
	}

	// Add another doc to v2 (write to doc store + Bleve)
	docStore2, err := NewGlobalVersionDocStore(sd, v2)
	if err != nil {
		t.Fatalf("NewGlobalVersionDocStore v2: %v", err)
	}
	if err := docStore2.Write(&VersionDocument{
		ID: "file_3", Path: "c.go", Type: "source_code", Content: "package c", Language: "go",
	}); err != nil {
		t.Fatalf("Write doc to v2: %v", err)
	}
	docStore2.Close()

	if err := gvbs.Index(ctx, &search.Document{ID: "file_3", Path: "c.go", Content: "package c"}); err != nil {
		t.Fatalf("Index to v2: %v", err)
	}

	countV2After, _ := gvbs.DocumentCount()
	if countV2After != 3 {
		t.Errorf("v2 count after add = %d, want 3", countV2After)
	}

	gvbs.CloseAll()
}

func TestGlobalVersionBleveStoreNotOpen(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	gvbs := NewGlobalVersionBleveStore(sd, gm.GetHead())
	// Don't call OpenHead

	ctx := context.Background()
	if err := gvbs.Index(ctx, &search.Document{ID: "test", Content: "test"}); err == nil {
		t.Error("expected error when indexing with store not open")
	}

	if err := gvbs.IndexBatch(ctx, []*search.Document{}); err == nil {
		t.Error("expected error when indexing batch with store not open")
	}
}

func TestGlobalVersionBleveStoreRebuildHead(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	head := gm.GetHead()

	// First, write some docs to the global doc store
	docStore, err := NewGlobalVersionDocStore(sd, head)
	if err != nil {
		t.Fatalf("NewGlobalVersionDocStore: %v", err)
	}
	docs := []*VersionDocument{
		{ID: "file_1", Path: "a.go", Type: "source_code", Content: "package a", Language: "go"},
		{ID: "file_2", Path: "b.go", Type: "source_code", Content: "package b", Language: "go"},
	}
	if err := docStore.WriteBatch(docs); err != nil {
		t.Fatalf("WriteBatch docs: %v", err)
	}
	// Close persists offset indexes + DocIDMap so RebuildHead can read them.
	if err := docStore.Close(); err != nil {
		t.Fatalf("Close docStore: %v", err)
	}

	// Create bleve store and rebuild from shared doc data
	gvbs := NewGlobalVersionBleveStore(sd, head)
	if err := gvbs.RebuildHead(); err != nil {
		t.Fatalf("RebuildHead: %v", err)
	}
	defer gvbs.CloseAll()

	// Should have 2 docs
	count, err := gvbs.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount: %v", err)
	}
	if count != 2 {
		t.Errorf("DocumentCount = %d, want 2", count)
	}

	// Should be searchable
	result, err := gvbs.Search(context.Background(), &search.SearchRequest{
		Query: "package",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.TotalHits != 2 {
		t.Errorf("Search hits = %d, want 2", result.TotalHits)
	}
}
