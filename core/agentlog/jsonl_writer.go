package agentlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// RotationPolicy controls how often JSONL files are rotated.
type RotationPolicy struct {
	Interval time.Duration // e.g. 1*time.Hour
}

// DefaultRotationPolicy returns hourly rotation.
func DefaultRotationPolicy() RotationPolicy {
	return RotationPolicy{Interval: time.Hour}
}

// JSONLEntry is a single structured log record written to the JSONL file.
//
// Design-doc fields (pipeline_id, tool_call_key, llm_call_id, agent_id,
// msg, fields) were added on top of the original shape to support the
// unified logging scheme — see docs/LOGGING.md. Existing callers that
// only populate the original fields continue to work; sylk trace and
// newer emitters read the additional fields when present.
type JSONLEntry struct {
	Timestamp   time.Time `json:"ts"`
	Level       string    `json:"level"`
	Agent       string    `json:"agent"`
	AgentID     string    `json:"agent_id,omitempty"`
	PipelineID  string    `json:"pipeline_id,omitempty"`
	SessionID   string    `json:"session_id"`
	Event       string    `json:"event"`
	EventCode   EventType `json:"event_code"`
	Message     string    `json:"msg,omitempty"`
	CorrID      string    `json:"corr_id,omitempty"`
	ToolCallKey string    `json:"tool_call_key,omitempty"`
	LLMCallID   string    `json:"llm_call_id,omitempty"`
	DurationNs  int64     `json:"dur_ns,omitempty"`
	Data        any       `json:"data,omitempty"`
	Fields      any       `json:"fields,omitempty"`
	Error       string    `json:"err,omitempty"`
}

// JSONLWriter is a time-rotated, buffered JSONL file writer with coalesced sync.
type JSONLWriter struct {
	mu       sync.Mutex
	dir      string
	baseName string
	policy   RotationPolicy

	file     *os.File
	buf      *bufio.Writer
	boundary time.Time // current rotation boundary (truncated to policy.Interval)

	syncInterval time.Duration
	syncTimer    *time.Timer
	dirty        atomic.Bool
	closed       atomic.Bool

	// nowFunc is injectable for testing. Defaults to time.Now.
	nowFunc func() time.Time
}

// NewJSONLWriter creates a JSONL writer that rotates files based on policy.
// Files are written to dir/<baseName>.jsonl with rotated files named
// <baseName>-YYYYMMDD-HH.jsonl (for hourly rotation).
func NewJSONLWriter(dir, baseName string, policy RotationPolicy, syncInterval time.Duration) (*JSONLWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("agentlog: create jsonl dir: %w", err)
	}

	if syncInterval <= 0 {
		syncInterval = defaultSyncInterval
	}

	w := &JSONLWriter{
		dir:          dir,
		baseName:     baseName,
		policy:       policy,
		syncInterval: syncInterval,
		nowFunc:      time.Now,
	}

	if err := w.openCurrent(); err != nil {
		return nil, err
	}

	w.startSyncTimer()
	return w, nil
}

// Write appends a JSONLEntry to the current file, rotating if the time boundary
// has been crossed. Returns the number of bytes written.
func (w *JSONLWriter) Write(entry JSONLEntry) error {
	if w.closed.Load() {
		return fmt.Errorf("agentlog: jsonl writer closed")
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("agentlog: marshal jsonl entry: %w", err)
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.rotateIfNeeded(); err != nil {
		return err
	}

	if _, err := w.buf.Write(data); err != nil {
		return fmt.Errorf("agentlog: write jsonl: %w", err)
	}

	w.dirty.Store(true)
	return nil
}

// Close stops the sync timer, flushes buffered data, fsyncs, and closes the file.
// Idempotent.
func (w *JSONLWriter) Close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.syncTimer != nil {
		w.syncTimer.Stop()
	}

	return w.flushAndClose()
}

// --- internal ---

func (w *JSONLWriter) now() time.Time {
	return w.nowFunc()
}

func (w *JSONLWriter) currentPath() string {
	return filepath.Join(w.dir, w.baseName+".jsonl")
}

func (w *JSONLWriter) rotatedName(boundary time.Time) string {
	return fmt.Sprintf("%s-%s.jsonl", w.baseName, boundary.UTC().Format("20060102-15"))
}

func (w *JSONLWriter) openCurrent() error {
	path := w.currentPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("agentlog: open jsonl: %w", err)
	}

	w.file = f
	w.buf = bufio.NewWriter(f)
	w.boundary = w.now().Truncate(w.policy.Interval)
	return nil
}

func (w *JSONLWriter) rotateIfNeeded() error {
	current := w.now().Truncate(w.policy.Interval)
	if current.Equal(w.boundary) {
		return nil
	}

	// Flush and close current file.
	prevBoundary := w.boundary
	if err := w.flushAndClose(); err != nil {
		return err
	}

	// Rename current file to rotated name.
	src := w.currentPath()
	dst := filepath.Join(w.dir, w.rotatedName(prevBoundary))
	if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("agentlog: rotate jsonl: %w", err)
	}

	return w.openCurrent()
}

func (w *JSONLWriter) flushAndClose() error {
	if w.buf == nil {
		return nil
	}

	flushErr := w.buf.Flush()
	var syncErr, closeErr error
	if w.file != nil {
		syncErr = w.file.Sync()
		closeErr = w.file.Close()
	}

	w.buf = nil
	w.file = nil

	if flushErr != nil {
		return flushErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (w *JSONLWriter) startSyncTimer() {
	w.syncTimer = time.AfterFunc(w.syncInterval, w.timerSync)
}

func (w *JSONLWriter) timerSync() {
	if w.closed.Load() {
		return
	}

	if w.dirty.CompareAndSwap(true, false) {
		w.mu.Lock()
		if w.buf != nil {
			_ = w.buf.Flush()
		}
		if w.file != nil {
			_ = w.file.Sync()
		}
		w.mu.Unlock()
	}

	w.syncTimer.Reset(w.syncInterval)
}
