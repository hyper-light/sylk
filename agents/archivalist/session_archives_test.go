package archivalist

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSessionArchives_WritesDispatchByEntrySessionID pins the
// session-scoped writes invariant: each entry lands in the per-session
// SQLite file determined by entry.SessionID. The on-disk layout must
// match .sylk/sessions/{sid}/agents/archivalist/archive.db so deleting a
// session directory cleanly removes that session's archived data.
func TestSessionArchives_WritesDispatchByEntrySessionID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewSessionArchives(SessionArchivesConfig{WorkDir: dir})
	t.Cleanup(func() { mgr.Close() })

	now := time.Now()
	for _, sid := range []string{"sess-1", "sess-2"} {
		if err := mgr.SaveSession(&Session{ID: sid, StartedAt: now}); err != nil {
			t.Fatalf("SaveSession %s: %v", sid, err)
		}
	}
	entries := []*Entry{
		{ID: "a", SessionID: "sess-1", Category: CategoryGeneral, Content: "alpha", Source: SourceModelClaudeOpus, CreatedAt: now, UpdatedAt: now},
		{ID: "b", SessionID: "sess-2", Category: CategoryGeneral, Content: "beta", Source: SourceModelClaudeOpus, CreatedAt: now, UpdatedAt: now},
		{ID: "c", SessionID: "sess-1", Category: CategoryGeneral, Content: "gamma", Source: SourceModelClaudeOpus, CreatedAt: now, UpdatedAt: now},
	}
	if err := mgr.ArchiveEntries(entries); err != nil {
		t.Fatalf("ArchiveEntries: %v", err)
	}

	for _, sid := range []string{"sess-1", "sess-2"} {
		expected := filepath.Join(dir, ".sylk", "sessions", sid, "agents", "archivalist", "archive.db")
		if _, err := os.Stat(expected); err != nil {
			t.Fatalf("expected session-scoped DB at %s, got %v", expected, err)
		}
	}

	// sess-1 DB owns 2 entries; sess-2 DB owns 1.
	a1, err := mgr.For("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := a1.Query(ArchiveQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("sess-1 entries = %d, want 2", len(got))
	}

	a2, err := mgr.For("sess-2")
	if err != nil {
		t.Fatal(err)
	}
	got, err = a2.Query(ArchiveQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("sess-2 entries = %d, want 1", len(got))
	}
}

// TestSessionArchives_ReadsFanOutAcrossSessions pins the global-reads
// invariant: SearchText / Stats / GetRecentSessions iterate every known
// session DB and merge the results. Without this, a query against the
// archive after the file-per-session migration would only see the
// active session and lose all history from peer sessions.
func TestSessionArchives_ReadsFanOutAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	mgr := NewSessionArchives(SessionArchivesConfig{WorkDir: dir})
	t.Cleanup(func() { mgr.Close() })

	now := time.Now()
	for _, sid := range []string{"sess-A", "sess-B"} {
		if err := mgr.SaveSession(&Session{ID: sid, StartedAt: now}); err != nil {
			t.Fatalf("SaveSession %s: %v", sid, err)
		}
	}
	for _, e := range []*Entry{
		{ID: "x", SessionID: "sess-A", Category: CategoryGeneral, Content: "shared keyword foobar in A", Source: SourceModelClaudeOpus, CreatedAt: now, UpdatedAt: now},
		{ID: "y", SessionID: "sess-B", Category: CategoryGeneral, Content: "shared keyword foobar in B", Source: SourceModelClaudeOpus, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
	} {
		if err := mgr.ArchiveEntry(e); err != nil {
			t.Fatalf("ArchiveEntry: %v", err)
		}
	}

	// SearchText fans out across both session DBs.
	results, err := mgr.SearchText("foobar", 10)
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("SearchText results = %d, want 2 (one per session)", len(results))
	}

	// Stats sums per-session totals.
	stats, err := mgr.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats["total_entries"] != 2 {
		t.Fatalf("stats.total_entries = %d, want 2", stats["total_entries"])
	}

	// GetEntry locates an entry regardless of which session DB owns it.
	entry, err := mgr.GetEntry("y")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry == nil || entry.SessionID != "sess-B" {
		t.Fatalf("GetEntry y = %+v, want SessionID=sess-B", entry)
	}
}

// TestSessionArchives_PathRespectsWorkDir pins the disk layout: every
// per-session DB is under {WorkDir}/.sylk/sessions/{sid}/agents/archivalist/.
// The pre-migration .sylk/archive.db single-file layout must not be
// recreated regardless of the WorkDir setting.
func TestSessionArchives_PathRespectsWorkDir(t *testing.T) {
	dir := t.TempDir()
	mgr := NewSessionArchives(SessionArchivesConfig{WorkDir: dir})
	t.Cleanup(func() { mgr.Close() })

	if _, err := mgr.For("ses_001"); err != nil {
		t.Fatalf("For: %v", err)
	}
	expected := filepath.Join(dir, ".sylk", "sessions", "ses_001", "agents", "archivalist", "archive.db")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected DB at %s, got %v", expected, err)
	}

	// The legacy single-file location must NOT exist.
	if _, err := os.Stat(filepath.Join(dir, ".sylk", "archive.db")); err == nil {
		t.Fatal("legacy .sylk/archive.db exists; per-session migration regressed")
	}
}
