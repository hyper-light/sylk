package archivalist

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// SessionArchives is the spec-compliant entry point to per-session archive
// storage. Per agents/archivalist/ARCHITECTURE.md ("session-scoped writes,
// global reads") each session owns an isolated SQLite file at
// .sylk/sessions/{sessionID}/agents/archivalist/archive.db; writes dispatch
// to the file owning the row's session, and cross-session reads fan out
// across all registered session DBs and merge the results.
//
// The single-file global .sylk/archive.db is intentionally NOT supported.
// That layout conflated WRITE attribution (every row already carries a
// session_id column) with FILE locality, which made per-session cleanup
// (deleting a session's data) require row-level surgery instead of a
// single rm. The new layout makes session deletion equal to deleting the
// session directory, and keeps per-session SQLite files small enough that
// concurrent sessions don't contend on a single WAL.
type SessionArchives struct {
	mu       sync.Mutex
	workDir  string
	open     map[string]*Archive
	listFn   func() []string
}

// SessionArchivesConfig configures the per-session archive manager.
type SessionArchivesConfig struct {
	// WorkDir is the project root (typically "."). Per-session DBs are
	// opened under WorkDir/.sylk/sessions/{sessionID}/agents/archivalist/.
	WorkDir string

	// ListSessions enumerates session IDs known to the runtime. Optional;
	// when nil or returning empty, the manager falls back to scanning the
	// .sylk/sessions/ directory. Injected by the caller so the live
	// session.Manager can drive enumeration when available.
	ListSessions func() []string
}

// NewSessionArchives constructs a manager. It does not open any DBs —
// per-session archives open lazily on first write or read.
func NewSessionArchives(cfg SessionArchivesConfig) *SessionArchives {
	if strings.TrimSpace(cfg.WorkDir) == "" {
		cfg.WorkDir = "."
	}
	return &SessionArchives{
		workDir: cfg.WorkDir,
		open:    make(map[string]*Archive),
		listFn:  cfg.ListSessions,
	}
}

// pathFor returns the on-disk path for a session's archive DB. Sessions
// with whitespace-only IDs fall back to "default" so callers that haven't
// plumbed a session ID still write somewhere predictable rather than
// failing the call.
func (s *SessionArchives) pathFor(sessionID string) string {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		sid = "default"
	}
	return filepath.Join(s.workDir, ".sylk", "sessions", sid, "agents", "archivalist", "archive.db")
}

// For returns the writable Archive for sessionID, opening it lazily on
// first call. The returned handle is owned by the manager — callers must
// not Close it directly.
func (s *SessionArchives) For(sessionID string) (*Archive, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		sid = "default"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.open[sid]; ok {
		return a, nil
	}
	a, err := NewArchive(ArchiveConfig{Path: s.pathFor(sid)})
	if err != nil {
		return nil, fmt.Errorf("session archives: open %s: %w", sid, err)
	}
	s.open[sid] = a
	return a, nil
}

// allSessionIDs returns the union of explicitly registered sessions
// (from ListSessions) and on-disk session directories. Used by fan-out
// reads so a query against an unopened session still observes its data.
func (s *SessionArchives) allSessionIDs() []string {
	seen := make(map[string]struct{})
	if s.listFn != nil {
		for _, sid := range s.listFn() {
			sid = strings.TrimSpace(sid)
			if sid != "" {
				seen[sid] = struct{}{}
			}
		}
	}
	root := filepath.Join(s.workDir, ".sylk", "sessions")
	entries, err := os.ReadDir(root)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := strings.TrimSpace(e.Name())
			if name == "" || name == "active" {
				continue
			}
			if _, statErr := os.Stat(s.pathFor(name)); statErr != nil {
				continue
			}
			seen[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for sid := range seen {
		out = append(out, sid)
	}
	sort.Strings(out)
	return out
}

// allOpen returns a snapshot of every per-session archive currently open
// PLUS any registered sessions whose archives haven't been opened yet.
// Used by fan-out read methods.
func (s *SessionArchives) allOpen() ([]*Archive, error) {
	ids := s.allSessionIDs()
	archives := make([]*Archive, 0, len(ids))
	for _, sid := range ids {
		a, err := s.For(sid)
		if err != nil {
			return nil, err
		}
		archives = append(archives, a)
	}
	return archives, nil
}

// Close closes every open per-session archive. Safe to call multiple times.
func (s *SessionArchives) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for sid, a := range s.open {
		if err := a.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("session archives: close %s: %w", sid, err)
		}
		delete(s.open, sid)
	}
	return firstErr
}

// =============================================================================
// Write dispatch — the entry/session/summary's own session_id picks the DB.
// =============================================================================

// ArchiveEntry stores an entry in the archive owning entry.SessionID.
func (s *SessionArchives) ArchiveEntry(entry *Entry) error {
	if entry == nil {
		return nil
	}
	a, err := s.For(entry.SessionID)
	if err != nil {
		return err
	}
	return a.ArchiveEntry(entry)
}

// ArchiveEntries groups entries by SessionID and dispatches each group to
// its session DB in a single transaction. Entries lacking a SessionID land
// in the "default" archive.
func (s *SessionArchives) ArchiveEntries(entries []*Entry) error {
	if len(entries) == 0 {
		return nil
	}
	groups := make(map[string][]*Entry)
	for _, e := range entries {
		if e == nil {
			continue
		}
		groups[strings.TrimSpace(e.SessionID)] = append(groups[strings.TrimSpace(e.SessionID)], e)
	}
	for sid, group := range groups {
		a, err := s.For(sid)
		if err != nil {
			return err
		}
		if err := a.ArchiveEntries(group); err != nil {
			return err
		}
	}
	return nil
}

// SaveSession dispatches to the archive owning session.ID.
func (s *SessionArchives) SaveSession(session *Session) error {
	if session == nil {
		return nil
	}
	a, err := s.For(session.ID)
	if err != nil {
		return err
	}
	return a.SaveSession(session)
}

// SaveSummary dispatches to the archive owning summary.SessionID.
func (s *SessionArchives) SaveSummary(summary *CompactedSummary) error {
	if summary == nil {
		return nil
	}
	a, err := s.For(summary.SessionID)
	if err != nil {
		return err
	}
	return a.SaveSummary(summary)
}

// SaveFactDecision dispatches to the archive owning fact.SessionID.
func (s *SessionArchives) SaveFactDecision(fact *FactDecision) error {
	if fact == nil {
		return nil
	}
	a, err := s.For(fact.SessionID)
	if err != nil {
		return err
	}
	return a.SaveFactDecision(fact)
}

// SaveFactPattern dispatches to the archive owning fact.SessionID.
func (s *SessionArchives) SaveFactPattern(fact *FactPattern) error {
	if fact == nil {
		return nil
	}
	a, err := s.For(fact.SessionID)
	if err != nil {
		return err
	}
	return a.SaveFactPattern(fact)
}

// SaveFactFailure dispatches to the archive owning fact.SessionID.
func (s *SessionArchives) SaveFactFailure(fact *FactFailure) error {
	if fact == nil {
		return nil
	}
	a, err := s.For(fact.SessionID)
	if err != nil {
		return err
	}
	return a.SaveFactFailure(fact)
}

// SaveFactFileChange dispatches to the archive owning fact.SessionID.
func (s *SessionArchives) SaveFactFileChange(fact *FactFileChange) error {
	if fact == nil {
		return nil
	}
	a, err := s.For(fact.SessionID)
	if err != nil {
		return err
	}
	return a.SaveFactFileChange(fact)
}

// =============================================================================
// Fan-out reads — iterate every known session DB and merge results.
// =============================================================================

// GetEntry searches every session archive for the entry; returns the first
// match. Entry IDs are globally unique, so at most one DB will return a row.
func (s *SessionArchives) GetEntry(id string) (*Entry, error) {
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	for _, a := range archives {
		entry, err := a.GetEntry(id)
		if err == nil && entry != nil {
			return entry, nil
		}
	}
	return nil, fmt.Errorf("entry %s not found in any session archive", id)
}

// GetSession searches every session archive for the session record.
func (s *SessionArchives) GetSession(id string) (*Session, error) {
	a, err := s.For(id)
	if err != nil {
		return nil, err
	}
	return a.GetSession(id)
}

// Query routes to a specific session DB when q.SessionIDs has exactly one
// entry; otherwise fans out across all sessions and merges.
func (s *SessionArchives) Query(q ArchiveQuery) ([]*Entry, error) {
	if len(q.SessionIDs) == 1 {
		a, err := s.For(q.SessionIDs[0])
		if err != nil {
			return nil, err
		}
		return a.Query(q)
	}
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	var merged []*Entry
	for _, a := range archives {
		entries, err := a.Query(q)
		if err != nil {
			return nil, err
		}
		merged = append(merged, entries...)
	}
	sortEntriesForQuery(merged, q)
	if q.Limit > 0 && len(merged) > q.Limit {
		merged = merged[:q.Limit]
	}
	return merged, nil
}

// SearchText fans out across every session DB and merges results, sorted
// by created_at desc, then truncated to limit.
func (s *SessionArchives) SearchText(searchText string, limit int) ([]*Entry, error) {
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	var merged []*Entry
	for _, a := range archives {
		entries, err := a.SearchText(searchText, limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, entries...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].CreatedAt.After(merged[j].CreatedAt)
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

// GetRecentSessions fans out across every session DB and merges by
// started_at desc, then truncates to limit.
func (s *SessionArchives) GetRecentSessions(limit int) ([]*Session, error) {
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	var merged []*Session
	for _, a := range archives {
		sessions, err := a.GetRecentSessions(limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, sessions...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].StartedAt.After(merged[j].StartedAt)
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

// Stats fans out across every session and sums the per-session totals.
func (s *SessionArchives) Stats() (map[string]int, error) {
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	merged := make(map[string]int)
	for _, a := range archives {
		stats, err := a.Stats()
		if err != nil {
			return nil, err
		}
		for k, v := range stats {
			merged[k] += v
		}
	}
	return merged, nil
}

// SearchSummaries fans out across every session DB.
func (s *SessionArchives) SearchSummaries(searchText string, limit int) ([]*CompactedSummary, error) {
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	var merged []*CompactedSummary
	for _, a := range archives {
		summaries, err := a.SearchSummaries(searchText, limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, summaries...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].CreatedAt.After(merged[j].CreatedAt)
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

// QueryFactDecisions routes to a single session when sessionID is set,
// otherwise fans out across all sessions and merges.
func (s *SessionArchives) QueryFactDecisions(sessionID string, limit int) ([]*FactDecision, error) {
	if strings.TrimSpace(sessionID) != "" {
		a, err := s.For(sessionID)
		if err != nil {
			return nil, err
		}
		return a.QueryFactDecisions(sessionID, limit)
	}
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	var merged []*FactDecision
	for _, a := range archives {
		decisions, err := a.QueryFactDecisions("", limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, decisions...)
	}
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

// QueryFactPatterns fans out across every session DB. Pattern category is
// cross-session by design (a "test_isolation" pattern in session A is the
// same concept as in session B), so callers usually want the union.
func (s *SessionArchives) QueryFactPatterns(category string, limit int) ([]*FactPattern, error) {
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	var merged []*FactPattern
	for _, a := range archives {
		patterns, err := a.QueryFactPatterns(category, limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, patterns...)
	}
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

// QueryFactFailures routes to a single session when sessionID is set,
// otherwise fans out across all sessions and merges.
func (s *SessionArchives) QueryFactFailures(sessionID string, limit int) ([]*FactFailure, error) {
	if strings.TrimSpace(sessionID) != "" {
		a, err := s.For(sessionID)
		if err != nil {
			return nil, err
		}
		return a.QueryFactFailures(sessionID, limit)
	}
	archives, err := s.allOpen()
	if err != nil {
		return nil, err
	}
	var merged []*FactFailure
	for _, a := range archives {
		failures, err := a.QueryFactFailures("", limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, failures...)
	}
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}
