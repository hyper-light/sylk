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

// StreamWriter appends JSONL records to a per-day, size-rolled file
// tree under a base directory. Records land at
//
//	{baseDir}/{YYYY-MM-DD}/{streamName}.{nnn}.jsonl
//
// where nnn is a three-digit zero-padded continuation index that
// starts at 000 and increments whenever the active file reaches the
// configured size cap. The writer is responsible for rotating across
// both the daily boundary (evaluated at each write using the
// configured clock) and the size boundary.
//
// All writes run through the associated Redactor before serialization.
// Callers pass raw structures; secrets never reach disk.
//
// Safe for concurrent use. Close is idempotent.
type StreamWriter struct {
	baseDir    string
	streamName string
	cfg        StreamWriterConfig
	redactor   Redactor

	mu       sync.Mutex
	file     *os.File
	buf      *bufio.Writer
	path     string
	sizeNow  int64
	dayKey   string // "2026-04-21" of the currently open file
	partNum  int
	closed   atomic.Bool
	writeErr atomic.Int64 // cumulative count of dropped writes
}

// StreamWriterConfig tunes rotation and IO behavior. Zero values fall
// back to the design-doc defaults: 64 MiB size cap, local TZ, 2s sync
// interval. A nil Redactor is replaced with a no-op.
type StreamWriterConfig struct {
	MaxBytesPerFile int64
	Location        *time.Location
	SyncInterval    time.Duration
	Now             func() time.Time
}

const (
	defaultMaxBytesPerFile = 64 * 1024 * 1024 // 64 MiB
	defaultStreamSync      = 2 * time.Second
	continuationWidth      = 3
)

// NewStreamWriter creates a writer anchored at baseDir. baseDir does
// not need to exist yet — day directories are created lazily on first
// write. If redactor is nil, records pass through unchanged.
func NewStreamWriter(baseDir, streamName string, cfg StreamWriterConfig, redactor Redactor) (*StreamWriter, error) {
	if streamName == "" {
		return nil, fmt.Errorf("agentlog: stream writer requires a stream name")
	}
	if cfg.MaxBytesPerFile <= 0 {
		cfg.MaxBytesPerFile = defaultMaxBytesPerFile
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = defaultStreamSync
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if redactor == nil {
		redactor = noopRedactor{}
	}

	w := &StreamWriter{
		baseDir:    baseDir,
		streamName: streamName,
		cfg:        cfg,
		redactor:   redactor,
	}
	return w, nil
}

// Write appends one record. The record is marshaled to JSON, then a
// newline is appended. A write failure is surfaced as an error but
// the writer remains usable for subsequent calls.
func (w *StreamWriter) Write(record any) error {
	if w == nil {
		return nil
	}
	if w.closed.Load() {
		return fmt.Errorf("agentlog: stream writer closed")
	}

	redacted := w.redactor.RedactValue(record)
	data, err := json.Marshal(redacted)
	if err != nil {
		w.writeErr.Add(1)
		return fmt.Errorf("agentlog: marshal stream record: %w", err)
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.ensureOpenLocked(int64(len(data))); err != nil {
		w.writeErr.Add(1)
		return err
	}

	n, err := w.buf.Write(data)
	if err != nil {
		w.writeErr.Add(1)
		return fmt.Errorf("agentlog: stream write: %w", err)
	}
	w.sizeNow += int64(n)
	return nil
}

// Flush forces any buffered data to disk. Useful for tests; the sync
// timer normally handles this in the background.
func (w *StreamWriter) Flush() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushLocked()
}

// Close flushes and releases the current file handle. Idempotent.
func (w *StreamWriter) Close() error {
	if w == nil {
		return nil
	}
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeLocked()
}

// --- internals ---

// ensureOpenLocked guarantees that an appropriate file handle is open
// for the current clock time and has room for appendSize more bytes.
// Rolls to a new file if either the day boundary or the size cap is
// crossed. Must be called with w.mu held.
func (w *StreamWriter) ensureOpenLocked(appendSize int64) error {
	now := w.cfg.Now().In(w.cfg.Location)
	dayKey := now.Format("2006-01-02")

	// Day rotation: close current and reset continuation index.
	if w.file != nil && dayKey != w.dayKey {
		if err := w.closeLocked(); err != nil {
			return err
		}
		w.partNum = 0
	}

	// Size rotation: close current, advance continuation index.
	if w.file != nil && w.sizeNow+appendSize > w.cfg.MaxBytesPerFile {
		if err := w.closeLocked(); err != nil {
			return err
		}
		w.partNum++
	}

	if w.file == nil {
		if err := w.openLocked(dayKey); err != nil {
			return err
		}
	}
	return nil
}

// openLocked opens (or creates) the file for the given day key. If the
// writer's recorded partNum points at a file that does not exist on
// disk, it creates it at 0; if it exists, it opens for append and
// seeds sizeNow from the file size. When a directory is entered fresh
// (no continuation files yet), partNum resets to 0.
func (w *StreamWriter) openLocked(dayKey string) error {
	dir := filepath.Join(w.baseDir, dayKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("agentlog: create stream dir: %w", err)
	}

	// If partNum is 0, scan the directory for the highest existing
	// continuation so a process restart resumes appending rather than
	// clobbering. We only seek continuations for the *current* stream.
	if w.partNum == 0 {
		if p, ok := latestPartForStream(dir, w.streamName); ok {
			w.partNum = p
		}
	}

	path := streamPath(dir, w.streamName, w.partNum)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("agentlog: open stream file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("agentlog: stat stream file: %w", err)
	}

	// If the existing file is already at or past the cap, advance
	// past it rather than appending into an oversized file.
	if info.Size() >= w.cfg.MaxBytesPerFile {
		f.Close()
		w.partNum++
		path = streamPath(dir, w.streamName, w.partNum)
		f, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("agentlog: open stream file: %w", err)
		}
		info, err = f.Stat()
		if err != nil {
			f.Close()
			return fmt.Errorf("agentlog: stat stream file: %w", err)
		}
	}

	w.file = f
	w.buf = bufio.NewWriter(f)
	w.path = path
	w.sizeNow = info.Size()
	w.dayKey = dayKey
	return nil
}

func (w *StreamWriter) flushLocked() error {
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			return err
		}
	}
	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

func (w *StreamWriter) closeLocked() error {
	var firstErr error
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			firstErr = err
		}
	}
	if w.file != nil {
		if err := w.file.Sync(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := w.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	w.buf = nil
	w.file = nil
	w.path = ""
	w.sizeNow = 0
	w.dayKey = ""
	return firstErr
}

// streamPath builds a {baseDir}/{streamName}.{nnn}.jsonl path for the
// given zero-based part index.
func streamPath(dayDir, streamName string, part int) string {
	return filepath.Join(dayDir, fmt.Sprintf("%s.%0*d.jsonl", streamName, continuationWidth, part))
}

// latestPartForStream scans a day directory for files matching
// {streamName}.{nnn}.jsonl and returns the highest index found, or
// (0, false) if none exist.
func latestPartForStream(dayDir, streamName string) (int, bool) {
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		return 0, false
	}
	prefix := streamName + "."
	const suffix = ".jsonl"
	highest := -1
	for _, entry := range entries {
		name := entry.Name()
		if !hasPrefix(name, prefix) || !hasSuffix(name, suffix) {
			continue
		}
		middle := name[len(prefix) : len(name)-len(suffix)]
		if len(middle) != continuationWidth {
			continue
		}
		n, ok := parseContinuation(middle)
		if !ok {
			continue
		}
		if n > highest {
			highest = n
		}
	}
	if highest < 0 {
		return 0, false
	}
	return highest, true
}

func hasPrefix(s, prefix string) bool { return len(s) >= len(prefix) && s[:len(prefix)] == prefix }
func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func parseContinuation(s string) (int, bool) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// noopRedactor is substituted when the caller passes nil. Kept
// internal — public Redactor implementations should come from the
// exported NewRedactor constructor.
type noopRedactor struct{}

func (noopRedactor) RedactString(s string) string { return s }
func (noopRedactor) RedactValue(v any) any        { return v }
