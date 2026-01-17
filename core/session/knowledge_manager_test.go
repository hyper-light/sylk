package session

import (
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestKnowledgeManager(t *testing.T, isArchivalist bool) (*SessionKnowledgeManager, string) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "knowledge-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := KnowledgeManagerConfig{
		ProjectID:     "test-project",
		SessionID:     "test-session",
		BaseDir:       tmpDir,
		IsArchivalist: isArchivalist,
	}

	manager, err := NewSessionKnowledgeManager(cfg)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create knowledge manager: %v", err)
	}

	return manager, tmpDir
}

func TestNewSessionKnowledgeManager(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	if manager == nil {
		t.Fatal("expected non-nil manager")
	}
	if manager.projectDB == nil {
		t.Error("expected non-nil projectDB")
	}
	if manager.sessionDB == nil {
		t.Error("expected non-nil sessionDB")
	}
}

func TestSessionKnowledgeManager_StoreSessionKnowledge(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "test_key",
		Value: "test_value",
	}

	err := manager.StoreKnowledge(entry)
	if err != nil {
		t.Fatalf("StoreKnowledge failed: %v", err)
	}

	results, err := manager.QueryKnowledge("test", QueryOpts{})
	if err != nil {
		t.Fatalf("QueryKnowledge failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSessionKnowledgeManager_StoreProjectKnowledge_Archivalist(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, true)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		Scope: ScopeProject,
		Type:  "fact",
		Key:   "project_key",
		Value: "project_value",
	}

	err := manager.StoreKnowledge(entry)
	if err != nil {
		t.Fatalf("StoreKnowledge failed for Archivalist: %v", err)
	}

	results, err := manager.QueryKnowledge("project", QueryOpts{})
	if err != nil {
		t.Fatalf("QueryKnowledge failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSessionKnowledgeManager_StoreProjectKnowledge_NonArchivalist(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		Scope: ScopeProject,
		Type:  "fact",
		Key:   "project_key",
		Value: "project_value",
	}

	err := manager.StoreKnowledge(entry)
	if err != ErrUnauthorizedWrite {
		t.Errorf("expected ErrUnauthorizedWrite, got %v", err)
	}
}

func TestSessionKnowledgeManager_StoreUnknownScope(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		Scope: "unknown",
		Type:  "fact",
		Key:   "key",
		Value: "value",
	}

	err := manager.StoreKnowledge(entry)
	if err != ErrUnknownScope {
		t.Errorf("expected ErrUnknownScope, got %v", err)
	}
}

func TestSessionKnowledgeManager_QueryByType(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.StoreKnowledge(KnowledgeEntry{
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "key1",
		Value: "value1",
	})
	manager.StoreKnowledge(KnowledgeEntry{
		Scope: ScopeSession,
		Type:  "summary",
		Key:   "key2",
		Value: "value2",
	})

	results, err := manager.QueryKnowledge("value", QueryOpts{Type: "fact"})
	if err != nil {
		t.Fatalf("QueryKnowledge failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result with type filter, got %d", len(results))
	}
	if results[0].Type != "fact" {
		t.Errorf("expected type 'fact', got '%s'", results[0].Type)
	}
}

func TestSessionKnowledgeManager_QueryWithLimit(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	for i := 0; i < 10; i++ {
		manager.StoreKnowledge(KnowledgeEntry{
			Scope: ScopeSession,
			Type:  "fact",
			Key:   "test_key",
			Value: "test_value",
		})
	}

	results, err := manager.QueryKnowledge("test", QueryOpts{Limit: 3})
	if err != nil {
		t.Fatalf("QueryKnowledge failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results with limit, got %d", len(results))
	}
}

func TestSessionKnowledgeManager_GetEntry(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		ID:    "test-id-123",
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "key",
		Value: "value",
	}

	manager.StoreKnowledge(entry)

	retrieved, err := manager.GetEntry("test-id-123")
	if err != nil {
		t.Fatalf("GetEntry failed: %v", err)
	}
	if retrieved.ID != "test-id-123" {
		t.Errorf("expected ID 'test-id-123', got '%s'", retrieved.ID)
	}
}

func TestSessionKnowledgeManager_GetEntry_NotFound(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	_, err := manager.GetEntry("nonexistent")
	if err != ErrKnowledgeNotFound {
		t.Errorf("expected ErrKnowledgeNotFound, got %v", err)
	}
}

func TestSessionKnowledgeManager_DeleteEntry(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		ID:    "delete-me",
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "key",
		Value: "value",
	}

	manager.StoreKnowledge(entry)
	manager.DeleteEntry("delete-me")

	_, err := manager.GetEntry("delete-me")
	if err != ErrKnowledgeNotFound {
		t.Error("expected entry to be deleted")
	}
}

func TestSessionKnowledgeManager_OtherSessionViews(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "knowledge-multi-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	manager1, err := NewSessionKnowledgeManager(KnowledgeManagerConfig{
		ProjectID:     "test-project",
		SessionID:     "session-1",
		BaseDir:       tmpDir,
		IsArchivalist: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager1.Close()

	manager1.StoreKnowledge(KnowledgeEntry{
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "session1_key",
		Value: "session1_value",
	})

	manager2, err := NewSessionKnowledgeManager(KnowledgeManagerConfig{
		ProjectID:     "test-project",
		SessionID:     "session-2",
		BaseDir:       tmpDir,
		IsArchivalist: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager2.Close()

	err = manager2.AddOtherSessionView("session-1")
	if err != nil {
		t.Fatalf("AddOtherSessionView failed: %v", err)
	}

	results, err := manager2.QueryKnowledge("session1", QueryOpts{IncludeOtherSessions: true})
	if err != nil {
		t.Fatalf("QueryKnowledge failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result from other session, got %d", len(results))
	}
}

func TestSessionKnowledgeManager_RemoveOtherSessionView(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.AddOtherSessionView("other-session")
	manager.RemoveOtherSessionView("other-session")

	if len(manager.otherSessionViews) != 0 {
		t.Error("expected other session view to be removed")
	}
}

func TestSessionKnowledgeManager_GetStats(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	manager.StoreKnowledge(KnowledgeEntry{
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "k1",
		Value: "v1",
	})
	manager.StoreKnowledge(KnowledgeEntry{
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "k2",
		Value: "v2",
	})
	manager.StoreKnowledge(KnowledgeEntry{
		Scope: ScopeSession,
		Type:  "summary",
		Key:   "k3",
		Value: "v3",
	})

	stats := manager.GetSessionStats()
	if stats.EntryCount != 3 {
		t.Errorf("expected 3 entries, got %d", stats.EntryCount)
	}
	if stats.TypeCounts["fact"] != 2 {
		t.Errorf("expected 2 facts, got %d", stats.TypeCounts["fact"])
	}
	if stats.TypeCounts["summary"] != 1 {
		t.Errorf("expected 1 summary, got %d", stats.TypeCounts["summary"])
	}
}

func TestSessionKnowledgeManager_SharedProjectKnowledge(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "knowledge-shared-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	archivalist, err := NewSessionKnowledgeManager(KnowledgeManagerConfig{
		ProjectID:     "test-project",
		SessionID:     "archivalist",
		BaseDir:       tmpDir,
		IsArchivalist: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer archivalist.Close()

	archivalist.StoreKnowledge(KnowledgeEntry{
		Scope: ScopeProject,
		Type:  "fact",
		Key:   "shared_key",
		Value: "shared_value",
	})

	otherSession, err := NewSessionKnowledgeManager(KnowledgeManagerConfig{
		ProjectID:     "test-project",
		SessionID:     "other-session",
		BaseDir:       tmpDir,
		IsArchivalist: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer otherSession.Close()

	results, err := otherSession.QueryKnowledge("shared", QueryOpts{})
	if err != nil {
		t.Fatalf("QueryKnowledge failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 shared result, got %d", len(results))
	}
}

func TestSessionKnowledgeManager_Deduplication(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, true)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		ID:    "dup-id",
		Scope: ScopeProject,
		Type:  "fact",
		Key:   "dup_key",
		Value: "dup_value",
	}

	manager.StoreKnowledge(entry)

	entry.Scope = ScopeSession
	manager.StoreKnowledge(entry)

	results, err := manager.QueryKnowledge("dup", QueryOpts{})
	if err != nil {
		t.Fatalf("QueryKnowledge failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 deduplicated result, got %d", len(results))
	}
}

func TestSessionKnowledgeManager_WithMetadata(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		ID:    "meta-test",
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "key",
		Value: "value",
		Metadata: map[string]string{
			"source":     "test",
			"confidence": "high",
		},
	}

	manager.StoreKnowledge(entry)

	retrieved, _ := manager.GetEntry("meta-test")
	if retrieved.Metadata["source"] != "test" {
		t.Errorf("expected metadata source 'test', got '%s'", retrieved.Metadata["source"])
	}
}

func TestSessionKnowledgeManager_Close(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)

	err := manager.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	err = manager.StoreKnowledge(KnowledgeEntry{Scope: ScopeSession})
	if err != ErrKnowledgeManagerClosed {
		t.Errorf("expected ErrKnowledgeManagerClosed, got %v", err)
	}
}

func TestSessionKnowledgeManager_DoubleClose(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)

	manager.Close()
	err := manager.Close()

	if err != ErrKnowledgeManagerClosed {
		t.Errorf("expected ErrKnowledgeManagerClosed on double close, got %v", err)
	}
}

func TestKnowledgeEntry_Timestamps(t *testing.T) {
	manager, tmpDir := newTestKnowledgeManager(t, false)
	defer os.RemoveAll(tmpDir)
	defer manager.Close()

	entry := KnowledgeEntry{
		ID:    "time-test",
		Scope: ScopeSession,
		Type:  "fact",
		Key:   "key",
		Value: "value1",
	}

	manager.StoreKnowledge(entry)
	first, _ := manager.GetEntry("time-test")

	time.Sleep(10 * time.Millisecond)

	entry.Value = "value2"
	manager.StoreKnowledge(entry)
	second, _ := manager.GetEntry("time-test")

	if !second.UpdatedAt.After(first.CreatedAt) {
		t.Error("expected updated_at to be after created_at on update")
	}
}
