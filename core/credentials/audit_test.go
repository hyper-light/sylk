package credentials

import (
	"sync"
	"testing"
	"time"
)

func TestCredentialAuditLog_LogGranted(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	entry := log.LogGranted("agent1", "engineer", "github", "tool1", "handle1", "PR creation")

	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.Action != AuditActionGranted {
		t.Errorf("expected action granted, got %s", entry.Action)
	}
	if entry.AgentID != "agent1" {
		t.Errorf("expected agent1, got %s", entry.AgentID)
	}
	if entry.Provider != "github" {
		t.Errorf("expected github, got %s", entry.Provider)
	}
	if entry.EntryHash == "" {
		t.Error("expected entry hash to be set")
	}
	if entry.PrevHash != "" {
		t.Error("first entry should have empty prev hash")
	}
}

func TestCredentialAuditLog_LogDenied(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	entry := log.LogDenied("agent2", "librarian", "github", "scope violation")

	if entry.Action != AuditActionDenied {
		t.Errorf("expected denied, got %s", entry.Action)
	}
	if entry.Reason != "scope violation" {
		t.Errorf("expected 'scope violation', got %s", entry.Reason)
	}
}

func TestCredentialAuditLog_LogResolved(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	entry := log.LogResolved("handle123")

	if entry.Action != AuditActionResolved {
		t.Errorf("expected resolved, got %s", entry.Action)
	}
	if entry.HandleID != "handle123" {
		t.Errorf("expected handle123, got %s", entry.HandleID)
	}
}

func TestCredentialAuditLog_LogRevoked(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	entry := log.LogRevoked("handle456")

	if entry.Action != AuditActionRevoked {
		t.Errorf("expected revoked, got %s", entry.Action)
	}
}

func TestCredentialAuditLog_LogPending(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	entry := log.LogPending("agent3", "orchestrator", "openai", "awaiting user approval")

	if entry.Action != AuditActionPending {
		t.Errorf("expected pending, got %s", entry.Action)
	}
}

func TestCredentialAuditLog_LogExpired(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	entry := log.LogExpired("handle789")

	if entry.Action != AuditActionExpired {
		t.Errorf("expected expired, got %s", entry.Action)
	}
}

func TestCredentialAuditLog_HashChain(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	e1 := log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	e2 := log.LogGranted("a2", "engineer", "openai", "t2", "h2", "r2")
	e3 := log.LogRevoked("h1")

	if e1.PrevHash != "" {
		t.Error("first entry should have empty prev hash")
	}
	if e2.PrevHash != e1.EntryHash {
		t.Error("second entry prev hash should match first entry hash")
	}
	if e3.PrevHash != e2.EntryHash {
		t.Error("third entry prev hash should match second entry hash")
	}

	if e1.EntryHash == e2.EntryHash {
		t.Error("entries should have unique hashes")
	}
}

func TestCredentialAuditLog_Verify_Valid(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogGranted("a2", "engineer", "openai", "t2", "h2", "r2")
	log.LogRevoked("h1")

	if err := log.Verify(); err != nil {
		t.Errorf("expected valid chain, got error: %v", err)
	}
}

func TestCredentialAuditLog_Verify_Tampered(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogGranted("a2", "engineer", "openai", "t2", "h2", "r2")

	log.mu.Lock()
	log.entries[0].AgentID = "tampered"
	log.mu.Unlock()

	if err := log.Verify(); err != ErrTamperedLog {
		t.Errorf("expected tampered error, got: %v", err)
	}
}

func TestCredentialAuditLog_Verify_BrokenChain(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogGranted("a2", "engineer", "openai", "t2", "h2", "r2")

	log.mu.Lock()
	log.entries[1].PrevHash = "broken"
	log.mu.Unlock()

	if err := log.Verify(); err != ErrTamperedLog {
		t.Errorf("expected tampered error, got: %v", err)
	}
}

func TestCredentialAuditLog_Query_ByAgentType(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogGranted("a2", "librarian", "openai", "t2", "h2", "r2")
	log.LogGranted("a3", "engineer", "openai", "t3", "h3", "r3")

	results := log.Query(AuditQueryFilter{AgentType: "engineer"})

	if len(results) != 2 {
		t.Errorf("expected 2 entries, got %d", len(results))
	}
}

func TestCredentialAuditLog_Query_ByProvider(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogGranted("a2", "librarian", "openai", "t2", "h2", "r2")
	log.LogDenied("a3", "orchestrator", "github", "denied")

	results := log.Query(AuditQueryFilter{Provider: "github"})

	if len(results) != 2 {
		t.Errorf("expected 2 entries, got %d", len(results))
	}
}

func TestCredentialAuditLog_Query_ByAction(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogDenied("a2", "librarian", "github", "denied")
	log.LogGranted("a3", "engineer", "openai", "t3", "h3", "r3")

	results := log.Query(AuditQueryFilter{Action: AuditActionDenied})

	if len(results) != 1 {
		t.Errorf("expected 1 entry, got %d", len(results))
	}
	if results[0].AgentID != "a2" {
		t.Errorf("expected a2, got %s", results[0].AgentID)
	}
}

func TestCredentialAuditLog_Query_ByTimeRange(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	time.Sleep(10 * time.Millisecond)
	midpoint := time.Now()
	time.Sleep(10 * time.Millisecond)
	log.LogGranted("a2", "engineer", "openai", "t2", "h2", "r2")

	results := log.Query(AuditQueryFilter{From: midpoint})

	if len(results) != 1 {
		t.Errorf("expected 1 entry after midpoint, got %d", len(results))
	}
}

func TestCredentialAuditLog_Query_Combined(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogGranted("a2", "engineer", "openai", "t2", "h2", "r2")
	log.LogDenied("a3", "engineer", "github", "denied")
	log.LogGranted("a4", "librarian", "github", "t4", "h4", "r4")

	results := log.Query(AuditQueryFilter{
		AgentType: "engineer",
		Provider:  "github",
		Action:    AuditActionGranted,
	})

	if len(results) != 1 {
		t.Errorf("expected 1 entry, got %d", len(results))
	}
	if results[0].AgentID != "a1" {
		t.Errorf("expected a1, got %s", results[0].AgentID)
	}
}

func TestCredentialAuditLog_ThreadSafety(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%3 == 0 {
				log.LogGranted("agent", "engineer", "github", "tool", "handle", "reason")
			} else if n%3 == 1 {
				log.LogDenied("agent", "librarian", "openai", "denied")
			} else {
				log.LogRevoked("handle")
			}
		}(i)
	}

	wg.Wait()

	if log.Count() != 100 {
		t.Errorf("expected 100 entries, got %d", log.Count())
	}

	if err := log.Verify(); err != nil {
		t.Errorf("expected valid chain after concurrent writes, got: %v", err)
	}
}

func TestCredentialAuditLog_Entries(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogGranted("a2", "librarian", "openai", "t2", "h2", "r2")

	entries := log.Entries()

	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	entries[0].AgentID = "modified"
	original := log.Entries()
	if original[0].AgentID == "modified" {
		t.Error("returned entries should be a copy")
	}
}

func TestCredentialAuditLog_LastHash(t *testing.T) {
	log := NewCredentialAuditLog(nil)

	if log.LastHash() != "" {
		t.Error("empty log should have empty hash")
	}

	e1 := log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	if log.LastHash() != e1.EntryHash {
		t.Error("last hash should match last entry hash")
	}

	e2 := log.LogGranted("a2", "engineer", "openai", "t2", "h2", "r2")
	if log.LastHash() != e2.EntryHash {
		t.Error("last hash should update after new entry")
	}
}

type mockAuditStorage struct {
	mu      sync.Mutex
	entries []*CredentialAuditEntry
}

func (m *mockAuditStorage) Append(entry *CredentialAuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockAuditStorage) Load() ([]*CredentialAuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*CredentialAuditEntry, len(m.entries))
	copy(result, m.entries)
	return result, nil
}

func TestCredentialAuditLog_WithStorage(t *testing.T) {
	storage := &mockAuditStorage{}
	log := NewCredentialAuditLog(storage)

	log.LogGranted("a1", "engineer", "github", "t1", "h1", "r1")
	log.LogGranted("a2", "engineer", "openai", "t2", "h2", "r2")

	if len(storage.entries) != 2 {
		t.Errorf("expected 2 entries in storage, got %d", len(storage.entries))
	}

	log2 := NewCredentialAuditLog(storage)

	if log2.Count() != 2 {
		t.Errorf("expected 2 entries loaded from storage, got %d", log2.Count())
	}
	if log2.LastHash() != log.LastHash() {
		t.Error("loaded log should have same hash chain")
	}
}
