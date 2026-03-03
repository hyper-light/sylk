package agentlog

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// SessionEventLogger is a unified entry point that owns both a WAL journal
// and a JSONL writer for a single agent within a session. Supports lazy
// binding: created before the session exists, bound when the first request
// arrives carrying a session ID.
type SessionEventLogger struct {
	mu        sync.Mutex
	agentName string
	walName   string
	bound     atomic.Bool
	journal   *AgentJournal
	jsonl     *JSONLWriter
	sessionID string
}

// NewSessionEventLogger creates an unbound logger. Call BindSession to
// activate WAL and JSONL writing.
func NewSessionEventLogger(agentName, walName string) *SessionEventLogger {
	return &SessionEventLogger{
		agentName: agentName,
		walName:   walName,
	}
}

// BindSession opens the WAL journal and JSONL writer at the session-scoped
// directory. Idempotent for the same sessionID. Closes old writers on
// session switch.
func (s *SessionEventLogger) BindSession(sessionDir, sessionID string) error {
	if s == nil {
		return nil
	}

	// Fast path: already bound to this session.
	if s.bound.Load() {
		s.mu.Lock()
		same := s.sessionID == sessionID
		s.mu.Unlock()
		if same {
			return nil
		}
		// Session switch — close old writers.
		s.closeWriters()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	walDir := filepath.Join(sessionDir, "agents", s.agentName, "wal")
	logsDir := filepath.Join(sessionDir, "agents", s.agentName, "logs")

	journal, err := OpenJournalDirect(walDir, JournalConfig{
		WALName: s.walName,
	})
	if err != nil {
		slog.Warn("session_logger: journal open failed",
			"agent", s.agentName, "session", sessionID, "err", err)
		return fmt.Errorf("session_logger: open journal: %w", err)
	}

	jsonl, err := NewJSONLWriter(logsDir, "activity", DefaultRotationPolicy(), defaultSyncInterval)
	if err != nil {
		journal.Close()
		slog.Warn("session_logger: jsonl writer open failed",
			"agent", s.agentName, "session", sessionID, "err", err)
		return fmt.Errorf("session_logger: open jsonl writer: %w", err)
	}

	s.journal = journal
	s.jsonl = jsonl
	s.sessionID = sessionID
	s.bound.Store(true)
	return nil
}

// IsBound reports whether the logger has been bound to a session.
func (s *SessionEventLogger) IsBound() bool {
	if s == nil {
		return false
	}
	return s.bound.Load()
}

// LogWALJSON writes a structured event to the binary WAL. No-op if unbound.
func (s *SessionEventLogger) LogWALJSON(eventType EventType, v any) {
	if s == nil || !s.bound.Load() {
		return
	}
	s.mu.Lock()
	j := s.journal
	s.mu.Unlock()

	if j == nil {
		return
	}
	if _, err := j.AppendJSON(eventType, v); err != nil {
		slog.Warn("session_logger: WAL append failed",
			"event", eventType.String(), "err", err)
	}
}

// LogEvent writes a structured entry to the JSONL file. No-op if unbound.
func (s *SessionEventLogger) LogEvent(entry JSONLEntry) {
	if s == nil || !s.bound.Load() {
		return
	}
	s.mu.Lock()
	j := s.jsonl
	s.mu.Unlock()

	if j == nil {
		return
	}
	if err := j.Write(entry); err != nil {
		slog.Warn("session_logger: JSONL write failed",
			"event", entry.Event, "err", err)
	}
}

// Close closes both the WAL journal and JSONL writer. Idempotent.
func (s *SessionEventLogger) Close() error {
	if s == nil {
		return nil
	}
	s.closeWriters()
	return nil
}

func (s *SessionEventLogger) closeWriters() {
	s.mu.Lock()
	journal := s.journal
	jsonl := s.jsonl
	s.journal = nil
	s.jsonl = nil
	s.bound.Store(false)
	s.mu.Unlock()

	if journal != nil {
		journal.Close()
	}
	if jsonl != nil {
		jsonl.Close()
	}
}
