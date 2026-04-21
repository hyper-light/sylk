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

// SessionEventLogger is the unified per-agent logging entry point. It
// owns a binary WAL journal (recovery), a per-day JSONL event stream
// (the primary structured log — events.{nnn}.jsonl), a per-day tool
// stream (tools.{nnn}.jsonl, written via ToolRecorder), a per-day LLM
// stream (llm.{nnn}.jsonl, written via LLMRecorder), an optional bus
// trace stream, a prompt corpus for content-addressed payloads, and
// an in-memory ring buffer for zero-I/O diagnostics.
//
// Supports lazy binding: created before the session exists, bound
// when the first request arrives carrying a session ID. A nil logger
// is safe to call methods on — every method short-circuits when the
// receiver is nil or the logger is unbound.
type SessionEventLogger struct {
	mu        sync.Mutex
	agentName string
	walName   string
	bound     atomic.Bool
	sessionID string

	config SessionLoggerConfig

	// Persistent handles.
	journal *AgentJournal
	events  *StreamWriter
	toolsW  *StreamWriter
	llmW    *StreamWriter
	busW    *StreamWriter
	corpus  *PromptCorpus

	toolRec *ToolRecorder
	llmRec  *LLMRecorder

	// Ring buffer: fixed-size circular buffer written inside LogEvent.
	ring    [ringSize]JSONLEntry
	ringPos atomic.Uint32
	ringLen atomic.Uint32 // tracks how many slots have been written (capped at ringSize)

	// Running stats: updated inside LogEvent via atomics.
	totalCount  atomic.Int64
	errorCount  atomic.Int64
	warnCount   atomic.Int64
	lastErrNano atomic.Int64

	// Broadcast callback: optional, called inside LogEvent after write.
	broadcastMu sync.RWMutex
	broadcast   func(JSONLEntry)
}

// SessionLoggerConfig controls which streams are enabled and how they
// rotate. Zero values resolve to the design-doc defaults: events on,
// tools on, llm on, bus off, 64 MiB per-file, local TZ. A nil
// Redactor is replaced with a no-op redactor.
type SessionLoggerConfig struct {
	StreamWriterConfig  StreamWriterConfig
	Redactor            Redactor
	EnableBusStream     bool
	DisableToolStream   bool
	DisableLLMStream    bool
	LLMRecorderConfig   LLMRecorderConfig
}

// NewSessionEventLogger creates an unbound logger. Call BindSession to
// activate the writers and content-addressed corpus.
func NewSessionEventLogger(agentName, walName string) *SessionEventLogger {
	return &SessionEventLogger{
		agentName: agentName,
		walName:   walName,
	}
}

// SetConfig overrides the default SessionLoggerConfig. Must be called
// before BindSession — after binding, the config is captured and new
// values are ignored.
func (s *SessionEventLogger) SetConfig(cfg SessionLoggerConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = cfg
}

// BindSession opens the WAL journal, event/tool/llm/bus stream writers,
// and prompt corpus at the session-scoped directory. Idempotent for
// the same sessionID. Closes old handles on session switch.
func (s *SessionEventLogger) BindSession(sessionDir, sessionID string) error {
	if s == nil {
		return nil
	}

	if s.bound.Load() {
		s.mu.Lock()
		same := s.sessionID == sessionID
		s.mu.Unlock()
		if same {
			return nil
		}
		s.closeWriters()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.config
	if cfg.Redactor == nil {
		cfg.Redactor = noopRedactor{}
	}

	walDir := filepath.Join(sessionDir, "agents", s.agentName, "wal")
	logsDir := filepath.Join(sessionDir, "agents", s.agentName, "logs")
	promptDir := filepath.Join(sessionDir, "prompts")

	journal, err := OpenJournalDirect(walDir, JournalConfig{WALName: s.walName})
	if err != nil {
		slog.Warn("session_logger: journal open failed",
			"agent", s.agentName, "session", sessionID, "err", err)
		return fmt.Errorf("session_logger: open journal: %w", err)
	}

	events, err := NewStreamWriter(logsDir, "events", cfg.StreamWriterConfig, cfg.Redactor)
	if err != nil {
		journal.Close()
		return fmt.Errorf("session_logger: events writer: %w", err)
	}

	var toolsW *StreamWriter
	if !cfg.DisableToolStream {
		toolsW, err = NewStreamWriter(logsDir, "tools", cfg.StreamWriterConfig, cfg.Redactor)
		if err != nil {
			events.Close()
			journal.Close()
			return fmt.Errorf("session_logger: tools writer: %w", err)
		}
	}

	var llmW *StreamWriter
	if !cfg.DisableLLMStream {
		llmW, err = NewStreamWriter(logsDir, "llm", cfg.StreamWriterConfig, cfg.Redactor)
		if err != nil {
			if toolsW != nil {
				toolsW.Close()
			}
			events.Close()
			journal.Close()
			return fmt.Errorf("session_logger: llm writer: %w", err)
		}
	}

	var busW *StreamWriter
	if cfg.EnableBusStream {
		busW, err = NewStreamWriter(logsDir, "bus", cfg.StreamWriterConfig, cfg.Redactor)
		if err != nil {
			if llmW != nil {
				llmW.Close()
			}
			if toolsW != nil {
				toolsW.Close()
			}
			events.Close()
			journal.Close()
			return fmt.Errorf("session_logger: bus writer: %w", err)
		}
	}

	corpus := NewPromptCorpus(promptDir, cfg.Redactor)

	s.journal = journal
	s.events = events
	s.toolsW = toolsW
	s.llmW = llmW
	s.busW = busW
	s.corpus = corpus
	s.toolRec = NewToolRecorder(toolsW)
	s.llmRec = NewLLMRecorder(llmW, corpus, cfg.LLMRecorderConfig)
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

// LogEvent writes a structured entry to the events stream, appends to
// the in-memory ring buffer, updates running stats, and fires the
// broadcast callback. Ring buffer and stats update even if the JSONL
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

	s.mu.Lock()
	w := s.events
	s.mu.Unlock()

	if w != nil {
		if err := w.Write(entry); err != nil {
			slog.Warn("session_logger: events write failed",
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

// ToolRecorder returns the recorder for tool-call lifecycle records.
// Returns a nil-safe recorder if the logger is unbound or the tool
// stream is disabled — callers can invoke Start/Complete without
// guards.
func (s *SessionEventLogger) ToolRecorder() *ToolRecorder {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolRec
}

// LLMRecorder returns the recorder for LLM round-trip records.
// Returns a nil-safe recorder if the logger is unbound or the llm
// stream is disabled.
func (s *SessionEventLogger) LLMRecorder() *LLMRecorder {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.llmRec
}

// BusStream returns the optional bus wire-trace writer. Nil when the
// bus stream is disabled (the default) or the logger is unbound.
// Callers must check for nil before writing.
func (s *SessionEventLogger) BusStream() *StreamWriter {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busW
}

// Corpus returns the content-addressed prompt corpus bound to this
// session, or nil when unbound. Callers that want to persist custom
// content-addressed payloads (other than LLM prompts) can use this.
func (s *SessionEventLogger) Corpus() *PromptCorpus {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corpus
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
// after the events-stream write. Used to publish log entries to the bus
// for archivalist ingest. Pass nil to clear.
func (s *SessionEventLogger) SetBroadcast(fn func(JSONLEntry)) {
	if s == nil {
		return
	}
	s.broadcastMu.Lock()
	s.broadcast = fn
	s.broadcastMu.Unlock()
}

// Close closes the WAL journal and every stream writer. Idempotent.
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
	events := s.events
	toolsW := s.toolsW
	llmW := s.llmW
	busW := s.busW
	s.journal = nil
	s.events = nil
	s.toolsW = nil
	s.llmW = nil
	s.busW = nil
	s.toolRec = nil
	s.llmRec = nil
	s.corpus = nil
	s.bound.Store(false)
	s.mu.Unlock()

	if journal != nil {
		journal.Close()
	}
	if events != nil {
		events.Close()
	}
	if toolsW != nil {
		toolsW.Close()
	}
	if llmW != nil {
		llmW.Close()
	}
	if busW != nil {
		busW.Close()
	}
}
