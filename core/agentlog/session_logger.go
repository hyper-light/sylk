package agentlog

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const ringSize = 256

// LoggerStats holds running statistics updated atomically inside LogEvent.
type LoggerStats struct {
	TotalCount int64     `json:"total_count"`
	ErrorCount int64     `json:"error_count"`
	WarnCount  int64     `json:"warn_count"`
	LastError  time.Time `json:"last_error,omitempty"`
}

// SessionEventLogger is a unified entry point that owns both a WAL journal
// and a JSONL writer for a single agent within a session. Supports lazy
// binding: created before the session exists, bound when the first request
// arrives carrying a session ID.
//
// It also maintains an in-memory ring buffer of recent entries and running
// statistics, enabling zero-I/O diagnostics via RecentEntries and Stats.
type SessionEventLogger struct {
	mu        sync.Mutex
	agentName string
	walName   string
	bound     atomic.Bool
	journal   *AgentJournal
	jsonl     *JSONLWriter
	sessionID string

	// Ring buffer: fixed-size circular buffer written inside LogEvent.
	ring    [ringSize]JSONLEntry
	ringPos atomic.Uint32
	ringLen atomic.Uint32 // tracks how many slots have been written (capped at ringSize)

	// Running stats: updated inside LogEvent via atomics.
	totalCount   atomic.Int64
	errorCount   atomic.Int64
	warnCount    atomic.Int64
	lastErrNano  atomic.Int64

	// Broadcast callback: optional, called inside LogEvent after write.
	broadcastMu sync.RWMutex
	broadcast   func(JSONLEntry)
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

// LogEvent writes a structured entry to the JSONL file, appends to the
// in-memory ring buffer, updates running stats, and fires the broadcast
// callback. The ring buffer and stats are always updated even if the JSONL
// write fails. No-op if unbound.
func (s *SessionEventLogger) LogEvent(entry JSONLEntry) {
	if s == nil || !s.bound.Load() {
		return
	}

	// Ring buffer write (lock-free via atomic position).
	pos := s.ringPos.Add(1) - 1
	s.ring[pos%ringSize] = entry
	if cur := s.ringLen.Load(); cur < ringSize {
		s.ringLen.CompareAndSwap(cur, min(cur+1, ringSize))
	}

	// Running stats via atomics.
	s.totalCount.Add(1)
	switch entry.Level {
	case "error":
		s.errorCount.Add(1)
		s.lastErrNano.Store(entry.Timestamp.UnixNano())
	case "warn":
		s.warnCount.Add(1)
	}

	// JSONL write.
	s.mu.Lock()
	j := s.jsonl
	s.mu.Unlock()

	if j != nil {
		if err := j.Write(entry); err != nil {
			slog.Warn("session_logger: JSONL write failed",
				"event", entry.Event, "err", err)
		}
	}

	// Broadcast callback (fire-and-forget).
	s.broadcastMu.RLock()
	fn := s.broadcast
	s.broadcastMu.RUnlock()
	if fn != nil {
		fn(entry)
	}
}

// RecentEntries returns the last n entries from the ring buffer (newest first).
// Zero I/O — reads only from memory. Nil-safe.
func (s *SessionEventLogger) RecentEntries(n int) []JSONLEntry {
	if s == nil {
		return nil
	}
	length := int(s.ringLen.Load())
	if length == 0 {
		return nil
	}
	if n > length {
		n = length
	}

	pos := s.ringPos.Load()
	result := make([]JSONLEntry, n)
	for i := range n {
		idx := (pos - 1 - uint32(i)) % ringSize
		result[i] = s.ring[idx]
	}
	return result
}

// Stats returns an instant health pulse from running atomic counters.
// Zero I/O — reads only from memory. Nil-safe.
func (s *SessionEventLogger) Stats() LoggerStats {
	if s == nil {
		return LoggerStats{}
	}
	stats := LoggerStats{
		TotalCount: s.totalCount.Load(),
		ErrorCount: s.errorCount.Load(),
		WarnCount:  s.warnCount.Load(),
	}
	if nano := s.lastErrNano.Load(); nano > 0 {
		stats.LastError = time.Unix(0, nano)
	}
	return stats
}

// SetBroadcast wires a callback that is invoked for every LogEvent call
// after the JSONL write. Used to publish log entries to the bus for
// archivalist ingest. Pass nil to clear.
func (s *SessionEventLogger) SetBroadcast(fn func(JSONLEntry)) {
	if s == nil {
		return
	}
	s.broadcastMu.Lock()
	s.broadcast = fn
	s.broadcastMu.Unlock()
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
