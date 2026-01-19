package context

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func setupTestStore(t *testing.T) (*UniversalContentStore, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "content_store_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := vectorgraphdb.Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open vectorgraphdb: %v", err)
	}

	cfg := ContentStoreConfig{
		BlevePath:      filepath.Join(tmpDir, "documents.bleve"),
		VectorDB:       db,
		IndexQueueSize: 100,
		IndexWorkers:   2,
	}

	store, err := NewUniversalContentStore(cfg)
	if err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create content store: %v", err)
	}

	cleanup := func() {
		store.Close()
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return store, cleanup
}

func TestNewUniversalContentStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.IsClosed() {
		t.Fatal("expected store to be open")
	}
}

func TestNewUniversalContentStore_InvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  ContentStoreConfig
	}{
		{
			name: "nil VectorDB",
			cfg: ContentStoreConfig{
				BlevePath: "/tmp/test.bleve",
				VectorDB:  nil,
			},
		},
		{
			name: "empty BlevePath",
			cfg: ContentStoreConfig{
				BlevePath: "",
				VectorDB:  &vectorgraphdb.VectorGraphDB{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewUniversalContentStore(tt.cfg)
			if err == nil {
				t.Error("expected error for invalid config")
			}
		})
	}
}

func TestIndexContent_HappyPath(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	entry := &ContentEntry{
		SessionID:   "session1",
		AgentID:     "agent1",
		AgentType:   "engineer",
		ContentType: ContentTypeUserPrompt,
		Content:     "This is a test content entry for indexing.",
		TokenCount:  10,
		Timestamp:   time.Now(),
		TurnNumber:  1,
		Keywords:    []string{"test", "indexing"},
	}

	err := store.IndexContent(entry)
	if err != nil {
		t.Fatalf("IndexContent failed: %v", err)
	}

	if entry.ID == "" {
		t.Error("expected entry ID to be set")
	}

	// Wait for async indexing
	time.Sleep(100 * time.Millisecond)
}

func TestIndexContent_NilEntry(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	err := store.IndexContent(nil)
	if err != ErrNilEntry {
		t.Errorf("expected ErrNilEntry, got: %v", err)
	}
}

func TestIndexContent_EmptyContent(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	entry := &ContentEntry{
		SessionID: "session1",
		Content:   "",
	}

	err := store.IndexContent(entry)
	if err != ErrEmptyContent {
		t.Errorf("expected ErrEmptyContent, got: %v", err)
	}
}

func TestIndexContent_ClosedStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	cleanup()

	entry := &ContentEntry{
		SessionID: "session1",
		Content:   "test content",
	}

	err := store.IndexContent(entry)
	if err != ErrStoreClosed {
		t.Errorf("expected ErrStoreClosed, got: %v", err)
	}
}

func TestSearch_HappyPath(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	entry := &ContentEntry{
		SessionID:   "session1",
		AgentID:     "agent1",
		AgentType:   "engineer",
		ContentType: ContentTypeUserPrompt,
		Content:     "unique searchable content for testing",
		TokenCount:  10,
		Timestamp:   time.Now(),
		TurnNumber:  1,
	}

	if err := store.IndexContent(entry); err != nil {
		t.Fatalf("IndexContent failed: %v", err)
	}

	// Wait for indexing
	time.Sleep(200 * time.Millisecond)

	results, err := store.Search("searchable", nil, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("expected at least one search result")
	}
}

func TestSearch_WithFilters(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	entry1 := &ContentEntry{
		SessionID:   "session1",
		AgentID:     "agent1",
		AgentType:   "engineer",
		ContentType: ContentTypeUserPrompt,
		Content:     "filtered content session one",
		TokenCount:  10,
		Timestamp:   time.Now(),
		TurnNumber:  1,
	}

	entry2 := &ContentEntry{
		SessionID:   "session2",
		AgentID:     "agent2",
		AgentType:   "engineer",
		ContentType: ContentTypeUserPrompt,
		Content:     "filtered content session two",
		TokenCount:  10,
		Timestamp:   time.Now(),
		TurnNumber:  1,
	}

	store.IndexContent(entry1)
	store.IndexContent(entry2)
	time.Sleep(200 * time.Millisecond)

	filters := &SearchFilters{SessionID: "session1"}
	results, err := store.Search("filtered", filters, 10)
	if err != nil {
		t.Fatalf("Search with filters failed: %v", err)
	}

	for _, r := range results {
		if r.SessionID != "" && r.SessionID != "session1" {
			t.Errorf("expected session1, got: %s", r.SessionID)
		}
	}
}

func TestSearch_ClosedStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	cleanup()

	_, err := store.Search("test", nil, 10)
	if err != ErrStoreClosed {
		t.Errorf("expected ErrStoreClosed, got: %v", err)
	}
}

func TestGetByIDs_HappyPath(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	entry := &ContentEntry{
		SessionID:   "session1",
		AgentID:     "agent1",
		AgentType:   "engineer",
		ContentType: ContentTypeUserPrompt,
		Content:     "content for ID lookup",
		TokenCount:  10,
		Timestamp:   time.Now(),
		TurnNumber:  1,
	}

	store.IndexContent(entry)
	time.Sleep(200 * time.Millisecond)

	results, err := store.GetByIDs([]string{entry.ID})
	if err != nil {
		t.Fatalf("GetByIDs failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got: %d", len(results))
	}
}

func TestGetByIDs_EmptyIDs(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	results, err := store.GetByIDs([]string{})
	if err != nil {
		t.Fatalf("GetByIDs failed: %v", err)
	}

	if results != nil {
		t.Error("expected nil results for empty IDs")
	}
}

func TestGetByIDs_ClosedStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	cleanup()

	_, err := store.GetByIDs([]string{"id1"})
	if err != ErrStoreClosed {
		t.Errorf("expected ErrStoreClosed, got: %v", err)
	}
}

func TestGetByTurnRange_HappyPath(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	for i := 1; i <= 5; i++ {
		entry := &ContentEntry{
			SessionID:   "session1",
			AgentID:     "agent1",
			AgentType:   "engineer",
			ContentType: ContentTypeUserPrompt,
			Content:     "turn content " + string(rune('0'+i)),
			TokenCount:  10,
			Timestamp:   time.Now(),
			TurnNumber:  i,
		}
		store.IndexContent(entry)
	}

	time.Sleep(300 * time.Millisecond)

	results, err := store.GetByTurnRange("session1", 2, 4)
	if err != nil {
		t.Fatalf("GetByTurnRange failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results (turns 2-4), got: %d", len(results))
	}

	for _, r := range results {
		if r.TurnNumber < 2 || r.TurnNumber > 4 {
			t.Errorf("turn %d outside range [2,4]", r.TurnNumber)
		}
	}
}

func TestGetByTurnRange_ClosedStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	cleanup()

	_, err := store.GetByTurnRange("session1", 1, 5)
	if err != ErrStoreClosed {
		t.Errorf("expected ErrStoreClosed, got: %v", err)
	}
}

func TestStoreReference_HappyPath(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ref := &ContextReference{
		ID:          "ref1",
		Type:        RefTypeConversation,
		SessionID:   "session1",
		AgentID:     "agent1",
		ContentIDs:  []string{"id1", "id2"},
		Summary:     "Test reference summary",
		TokensSaved: 100,
		TurnRange:   [2]int{1, 5},
		Timestamp:   time.Now(),
		Topics:      []string{"testing"},
	}

	err := store.StoreReference(ref)
	if err != nil {
		t.Fatalf("StoreReference failed: %v", err)
	}
}

func TestStoreReference_ClosedStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	cleanup()

	ref := &ContextReference{
		ID:      "ref1",
		Summary: "test",
	}

	err := store.StoreReference(ref)
	if err != ErrStoreClosed {
		t.Errorf("expected ErrStoreClosed, got: %v", err)
	}
}

func TestGetReference_HappyPath(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ref := &ContextReference{
		ID:          "ref1",
		Type:        RefTypeConversation,
		SessionID:   "session1",
		AgentID:     "agent1",
		ContentIDs:  []string{"id1", "id2"},
		Summary:     "Test reference summary",
		TokensSaved: 100,
		TurnRange:   [2]int{1, 5},
		Timestamp:   time.Now(),
		Topics:      []string{"testing"},
		Entities:    []string{"entity1"},
		QueryHints:  []string{"hint1"},
	}

	if err := store.StoreReference(ref); err != nil {
		t.Fatalf("StoreReference failed: %v", err)
	}

	retrieved, err := store.GetReference("ref1")
	if err != nil {
		t.Fatalf("GetReference failed: %v", err)
	}

	if retrieved.ID != ref.ID {
		t.Errorf("ID mismatch: %s != %s", retrieved.ID, ref.ID)
	}
	if retrieved.Type != ref.Type {
		t.Errorf("Type mismatch: %s != %s", retrieved.Type, ref.Type)
	}
	if retrieved.Summary != ref.Summary {
		t.Errorf("Summary mismatch: %s != %s", retrieved.Summary, ref.Summary)
	}
}

func TestGetReference_NotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	_, err := store.GetReference("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent reference")
	}
}

func TestGetReference_ClosedStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	cleanup()

	_, err := store.GetReference("ref1")
	if err != ErrStoreClosed {
		t.Errorf("expected ErrStoreClosed, got: %v", err)
	}
}

func TestContentStore_Close_Idempotent(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	if err := store.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("second Close should succeed: %v", err)
	}

	if !store.IsClosed() {
		t.Error("store should be closed")
	}
}

func TestDocumentCount(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	entry := &ContentEntry{
		SessionID:   "session1",
		AgentID:     "agent1",
		AgentType:   "engineer",
		ContentType: ContentTypeUserPrompt,
		Content:     "content for counting",
		TokenCount:  10,
		Timestamp:   time.Now(),
		TurnNumber:  1,
	}

	store.IndexContent(entry)
	time.Sleep(200 * time.Millisecond)

	count, err := store.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount failed: %v", err)
	}

	if count == 0 {
		t.Error("expected positive document count")
	}
}

func TestEntryCount(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	entry := &ContentEntry{
		SessionID:   "session1",
		AgentID:     "agent1",
		AgentType:   "engineer",
		ContentType: ContentTypeUserPrompt,
		Content:     "content for entry counting",
		TokenCount:  10,
		Timestamp:   time.Now(),
		TurnNumber:  1,
	}

	store.IndexContent(entry)
	time.Sleep(200 * time.Millisecond)

	count, err := store.EntryCount()
	if err != nil {
		t.Fatalf("EntryCount failed: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 entry, got: %d", count)
	}
}

func TestQueueLength(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	length := store.QueueLength()
	if length != 0 {
		t.Errorf("expected 0 queue length, got: %d", length)
	}
}

func TestConcurrentIndexing(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	var wg sync.WaitGroup
	numGoroutines := 10
	entriesPerGoroutine := 5

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < entriesPerGoroutine; j++ {
				entry := &ContentEntry{
					SessionID:   "session1",
					AgentID:     "agent1",
					AgentType:   "engineer",
					ContentType: ContentTypeUserPrompt,
					Content:     "concurrent content " + string(rune('A'+workerID)) + string(rune('0'+j)),
					TokenCount:  10,
					Timestamp:   time.Now(),
					TurnNumber:  workerID*10 + j,
				}
				if err := store.IndexContent(entry); err != nil {
					t.Errorf("concurrent IndexContent failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	count, err := store.EntryCount()
	if err != nil {
		t.Fatalf("EntryCount failed: %v", err)
	}

	expected := int64(numGoroutines * entriesPerGoroutine)
	if count != expected {
		t.Errorf("expected %d entries, got: %d", expected, count)
	}
}

func TestConcurrentSearch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	entry := &ContentEntry{
		SessionID:   "session1",
		AgentID:     "agent1",
		AgentType:   "engineer",
		ContentType: ContentTypeUserPrompt,
		Content:     "searchable content for concurrent test",
		TokenCount:  10,
		Timestamp:   time.Now(),
		TurnNumber:  1,
	}

	store.IndexContent(entry)
	time.Sleep(200 * time.Millisecond)

	var wg sync.WaitGroup
	numSearches := 20

	for i := 0; i < numSearches; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Search("searchable", nil, 10)
			if err != nil {
				t.Errorf("concurrent Search failed: %v", err)
			}
		}()
	}

	wg.Wait()
}

func TestGenerateContentID(t *testing.T) {
	content := []byte("test content")
	id1 := GenerateContentID(content)
	id2 := GenerateContentID(content)

	if id1 != id2 {
		t.Error("same content should produce same ID")
	}

	differentContent := []byte("different content")
	id3 := GenerateContentID(differentContent)

	if id1 == id3 {
		t.Error("different content should produce different ID")
	}

	if len(id1) != 64 { // SHA256 hex is 64 chars
		t.Errorf("expected 64 char ID, got: %d", len(id1))
	}
}

func TestDefaultContentStoreConfig(t *testing.T) {
	cfg := DefaultContentStoreConfig("/tmp/test")

	if cfg.BlevePath != "/tmp/test/documents.bleve" {
		t.Errorf("unexpected BlevePath: %s", cfg.BlevePath)
	}
	if cfg.IndexQueueSize != 1000 {
		t.Errorf("unexpected IndexQueueSize: %d", cfg.IndexQueueSize)
	}
	if cfg.IndexWorkers != 4 {
		t.Errorf("unexpected IndexWorkers: %d", cfg.IndexWorkers)
	}
}

func TestSearchFilters(t *testing.T) {
	t.Run("HasSessionFilter", func(t *testing.T) {
		filters := &SearchFilters{SessionID: "session1"}
		if !filters.HasSessionFilter() {
			t.Error("expected HasSessionFilter to return true")
		}

		emptyFilters := &SearchFilters{}
		if emptyFilters.HasSessionFilter() {
			t.Error("expected HasSessionFilter to return false")
		}
	})

	t.Run("HasAgentFilter", func(t *testing.T) {
		filters := &SearchFilters{AgentID: "agent1"}
		if !filters.HasAgentFilter() {
			t.Error("expected HasAgentFilter to return true")
		}
	})

	t.Run("HasTurnRange", func(t *testing.T) {
		filters := &SearchFilters{TurnRange: [2]int{1, 5}}
		if !filters.HasTurnRange() {
			t.Error("expected HasTurnRange to return true")
		}

		emptyFilters := &SearchFilters{TurnRange: [2]int{0, 0}}
		if emptyFilters.HasTurnRange() {
			t.Error("expected HasTurnRange to return false")
		}
	})
}

func TestBleveIndexAccessor(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	index := store.BleveIndex()
	if index == nil {
		t.Error("expected non-nil BleveIndex")
	}
}

func TestVectorDBAccessor(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	db := store.VectorDB()
	if db == nil {
		t.Error("expected non-nil VectorDB")
	}
}

func TestGracefulShutdownWithPendingWork(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Queue up several entries
	for i := 0; i < 10; i++ {
		entry := &ContentEntry{
			SessionID:   "session1",
			AgentID:     "agent1",
			AgentType:   "engineer",
			ContentType: ContentTypeUserPrompt,
			Content:     "shutdown test content " + string(rune('0'+i)),
			TokenCount:  10,
			Timestamp:   time.Now(),
			TurnNumber:  i,
		}
		store.IndexContent(entry)
	}

	// Immediately close
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		store.Close()
		close(done)
	}()

	select {
	case <-done:
		// Good, closed successfully
	case <-ctx.Done():
		t.Error("shutdown took too long")
	}
}
