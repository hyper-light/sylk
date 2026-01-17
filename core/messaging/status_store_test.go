package messaging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestStatusStore(t *testing.T) (*StatusStore, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "status_store_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := DefaultStatusStoreConfig()
	cfg.DBPath = filepath.Join(tmpDir, "test_status.db")
	cfg.NumCounters = 1000
	cfg.MaxCost = 1000000 // 1MB for tests
	cfg.EvictionBatchSize = 10

	store, err := NewStatusStore(cfg)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create status store: %v", err)
	}

	cleanup := func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}

	return store, cleanup
}

func TestStatusStore_NewStatusStore(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	if store == nil {
		t.Fatal("expected store to be created")
	}
	if store.cache == nil {
		t.Error("expected cache to be initialized")
	}
	if store.db == nil {
		t.Error("expected db to be initialized")
	}
}

func TestStatusStore_Track(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	msg := New(TypeRequest, "source", "payload").
		WithSession("session-1").
		WithCorrelation("corr-123").
		WithTarget("target")

	Track(store, msg)

	record, ok := store.Get(msg.ID)
	if !ok {
		t.Fatal("expected record to be found")
	}
	if record.ID != msg.ID {
		t.Errorf("expected ID %s, got %s", msg.ID, record.ID)
	}
	if record.CorrelationID != "corr-123" {
		t.Errorf("expected correlation 'corr-123', got %s", record.CorrelationID)
	}
	if record.Source != "source" {
		t.Errorf("expected source 'source', got %s", record.Source)
	}
	if record.Target != "target" {
		t.Errorf("expected target 'target', got %s", record.Target)
	}
	if record.Status != StatusQueued {
		t.Errorf("expected status 'queued', got %s", record.Status)
	}
}

func TestStatusStore_Get_HotCache(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	msg := New(TypeRequest, "source", "payload").WithSession("session-1")
	Track(store, msg)

	// First get - should be in hot cache
	record, ok := store.Get(msg.ID)
	if !ok {
		t.Fatal("expected record to be found")
	}
	if record.ID != msg.ID {
		t.Errorf("expected ID %s, got %s", msg.ID, record.ID)
	}

	stats := store.Stats()
	if stats.HotHits == 0 {
		t.Error("expected hot cache hit")
	}
}

func TestStatusStore_UpdateStatus(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	msg := New(TypeRequest, "source", "payload").WithSession("session-1")
	Track(store, msg)

	// Update status
	err := store.UpdateStatus(msg.ID, StatusCompleted, "")
	if err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	// Verify update
	record, ok := store.Get(msg.ID)
	if !ok {
		t.Fatal("expected record to be found")
	}
	if record.Status != StatusCompleted {
		t.Errorf("expected status 'completed', got %s", record.Status)
	}
}

func TestStatusStore_UpdateStatus_WithError(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	msg := New(TypeRequest, "source", "payload").WithSession("session-1")
	Track(store, msg)

	// Update status with error
	err := store.UpdateStatus(msg.ID, StatusFailed, "something went wrong")
	if err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	record, ok := store.Get(msg.ID)
	if !ok {
		t.Fatal("expected record to be found")
	}
	if record.Status != StatusFailed {
		t.Errorf("expected status 'failed', got %s", record.Status)
	}
	if record.Error != "something went wrong" {
		t.Errorf("expected error message, got %s", record.Error)
	}
}

func TestStatusStore_Delete(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	msg := New(TypeRequest, "source", "payload").WithSession("session-1")
	Track(store, msg)

	// Delete
	err := store.Delete(msg.ID)
	if err != nil {
		t.Fatalf("failed to delete: %v", err)
	}

	// Verify deleted
	_, ok := store.Get(msg.ID)
	if ok {
		t.Error("expected record to be deleted")
	}
}

func TestStatusStore_GetByStatus(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	// Create messages with different statuses
	msg1 := New(TypeRequest, "source", "payload1").WithSession("session-1")
	msg2 := New(TypeRequest, "source", "payload2").WithSession("session-1")
	msg3 := New(TypeRequest, "source", "payload3").WithSession("session-1")

	Track(store, msg1)
	Track(store, msg2)
	Track(store, msg3)

	// Update statuses
	store.UpdateStatus(msg1.ID, StatusCompleted, "")
	store.UpdateStatus(msg2.ID, StatusFailed, "error")
	// msg3 stays queued

	// Query completed
	completed, err := store.GetByStatus(StatusCompleted, 10)
	if err != nil {
		t.Fatalf("failed to query by status: %v", err)
	}

	foundCompleted := false
	for _, r := range completed {
		if r.ID == msg1.ID {
			foundCompleted = true
		}
	}
	if !foundCompleted {
		t.Error("expected to find completed message")
	}
}

func TestStatusStore_GetByCorrelation(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	// Create messages with same correlation
	correlationID := "corr-shared"

	msg1 := New(TypeRequest, "source", "payload1").WithSession("session-1").WithCorrelation(correlationID)
	msg2 := New(TypeForward, "guide", "payload2").WithSession("session-1").WithCorrelation(correlationID)
	msg3 := New(TypeResponse, "target", "payload3").WithSession("session-1").WithCorrelation(correlationID)

	Track(store, msg1)
	Track(store, msg2)
	Track(store, msg3)

	// Query by correlation
	records, err := store.GetByCorrelation(correlationID)
	if err != nil {
		t.Fatalf("failed to query by correlation: %v", err)
	}

	if len(records) != 3 {
		t.Errorf("expected 3 records with correlation, got %d", len(records))
	}
}

func TestStatusStore_GetActive(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	// Create messages with various statuses
	msg1 := New(TypeRequest, "source", "payload1").WithSession("session-1")
	msg2 := New(TypeRequest, "source", "payload2").WithSession("session-1")
	msg3 := New(TypeRequest, "source", "payload3").WithSession("session-1")

	Track(store, msg1)
	Track(store, msg2)
	Track(store, msg3)

	// Complete one
	store.UpdateStatus(msg1.ID, StatusCompleted, "")

	// Flush to cold storage
	store.Flush()

	// Query active (non-terminal)
	active, err := store.GetActive(10)
	if err != nil {
		t.Fatalf("failed to query active: %v", err)
	}

	// Should not include completed message
	for _, r := range active {
		if r.ID == msg1.ID {
			t.Error("expected completed message to not be in active list")
		}
	}
}

func TestStatusStore_Flush(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	msg := New(TypeRequest, "source", "payload").WithSession("session-1")
	Track(store, msg)

	err := store.Flush()
	if err != nil {
		t.Fatalf("failed to flush: %v", err)
	}

	// Should still be accessible
	record, ok := store.Get(msg.ID)
	if !ok {
		t.Fatal("expected record to be found after flush")
	}
	if record.ID != msg.ID {
		t.Errorf("expected ID %s, got %s", msg.ID, record.ID)
	}
}

func TestStatusStore_Cleanup(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	// Set short TTL for cleanup
	store.config.ColdStorageTTL = 50 * time.Millisecond

	msg := New(TypeRequest, "source", "payload").WithSession("session-1")
	Track(store, msg)

	// Complete and archive
	store.UpdateStatus(msg.ID, StatusCompleted, "")
	store.Flush()

	// Force to cold storage
	store.cache.Del(msg.ID)

	// Archive manually to SQLite with old timestamp
	store.archiveRecord(&StatusRecord{
		ID:        msg.ID,
		Source:    msg.Source,
		Type:      msg.Type,
		Status:    StatusCompleted,
		Priority:  msg.Priority,
		Attempt:   msg.Attempt,
		Timestamp: msg.Timestamp,
	})

	// Set TTL to 1 hour - record archived 2 hours ago should be cleaned
	store.config.ColdStorageTTL = 1 * time.Hour
	archivedAt := time.Now().Add(-2 * time.Hour)
	store.archiveRecord(&StatusRecord{
		ID:         msg.ID,
		Source:     msg.Source,
		Type:       msg.Type,
		Status:     StatusCompleted,
		Priority:   msg.Priority,
		Attempt:    msg.Attempt,
		Timestamp:  msg.Timestamp,
		ArchivedAt: &archivedAt,
	})

	// Run cleanup
	deleted, err := store.Cleanup()
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	// Should have cleaned up the old completed record (archived 2h ago, TTL is 1h)
	if deleted == 0 {
		t.Fatalf("expected cleanup to delete archived record")
	}
}

func TestStatusStore_Stats(t *testing.T) {
	store, cleanup := setupTestStatusStore(t)
	defer cleanup()

	// Track some messages
	msg1 := New(TypeRequest, "source", "payload1").WithSession("session-1")
	msg2 := New(TypeRequest, "source", "payload2").WithSession("session-1")
	Track(store, msg1)
	Track(store, msg2)

	// Get to trigger hits
	store.Get(msg1.ID)
	store.Get("nonexistent")

	stats := store.Stats()
	if stats.TotalStored < 2 {
		t.Errorf("expected at least 2 stored, got %d", stats.TotalStored)
	}
	if stats.HotHits == 0 {
		t.Error("expected hot hits")
	}
	if stats.HotMisses == 0 {
		t.Error("expected hot misses")
	}
}

func TestStatusRecord_Cost(t *testing.T) {
	record := &StatusRecord{
		ID:            "12345678-1234-1234-1234-123456789012",
		CorrelationID: "corr-123",
		Source:        "test-source",
		Target:        "test-target",
		Error:         "some error message",
	}

	cost := record.Cost()
	if cost < 200 {
		t.Errorf("expected cost >= 200, got %d", cost)
	}

	// Cost should increase with string lengths
	record2 := &StatusRecord{
		ID:     "short",
		Source: "s",
	}
	cost2 := record2.Cost()
	if cost2 >= cost {
		t.Error("expected shorter record to have lower cost")
	}
}
