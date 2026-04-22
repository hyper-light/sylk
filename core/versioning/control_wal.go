package versioning

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ControlWAL is a session-scoped append-only log for decision events
// (commit-queue transitions, Copy retention, replica lifecycle,
// session open/close epochs). Complements the semantic VersionedWAL
// which tracks file deltas. Both logs together constitute the
// session's durable state.
//
// Crash safety: each Append writes to the single log file and calls
// fsync before returning. Partial writes (power loss mid-append) are
// detected on Open by CRC validation per entry; the first corrupted
// or truncated entry marks the recovery point. Entries before it are
// durable; entries at/after it never existed.
//
// No rotation, no compaction (yet). The control WAL is small relative
// to the semantic WAL — a few bytes per event vs. file content. A
// full day of heavy activity is measured in megabytes. Compaction is
// driven by water-line advancement: entries referring to versions
// below the water line can be compacted — implemented alongside the
// CopyRetention GC pass.
type ControlWAL struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	nextSeq uint64
	closed  atomic.Bool
}

// ControlWALConfig configures the session's control WAL location.
type ControlWALConfig struct {
	// Dir is the session root; the control WAL lives at
	// filepath.Join(Dir, "control-wal", "log.bin").
	Dir string
}

var (
	// ErrControlWALClosed is returned when Append or Replay is
	// called after Close.
	ErrControlWALClosed = errors.New("control wal: closed")
)

// OpenControlWAL opens or creates the session's control WAL. Existing
// log contents are left intact; the next seq is derived from the
// highest valid seq in the log. Truncation of trailing corruption
// happens during Replay, not Open, so the caller can choose to
// validate before accepting state.
func OpenControlWAL(cfg ControlWALConfig) (*ControlWAL, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("control wal: empty dir")
	}
	logDir := filepath.Join(cfg.Dir, "control-wal")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("control wal: mkdir %s: %w", logDir, err)
	}
	path := filepath.Join(logDir, "log.bin")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("control wal: open %s: %w", path, err)
	}
	w := &ControlWAL{file: file, path: path}
	if seq, err := w.scanNextSeq(); err != nil {
		file.Close()
		return nil, err
	} else {
		w.nextSeq = seq
	}
	return w, nil
}

// scanNextSeq reads the log from the beginning and returns the seq
// that should follow the last valid entry. Truncates any trailing
// corrupt/partial entry so subsequent Appends land cleanly. Runs with
// the file at offset 0; restores offset to end on return.
func (w *ControlWAL) scanNextSeq() (uint64, error) {
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("control wal: seek: %w", err)
	}
	data, err := io.ReadAll(w.file)
	if err != nil {
		return 0, fmt.Errorf("control wal: read: %w", err)
	}
	var (
		off     = 0
		maxSeq  = uint64(0)
		lastOK  = 0
		haveOK  = false
	)
	for off < len(data) {
		entry, consumed, err := DecodeControlEntry(data[off:])
		if err != nil {
			// Truncate at first corruption / truncation.
			break
		}
		if entry.Seq > maxSeq {
			maxSeq = entry.Seq
		}
		off += consumed
		lastOK = off
		haveOK = true
	}
	// If we had trailing garbage, truncate the file.
	if off < len(data) {
		if err := w.file.Truncate(int64(lastOK)); err != nil {
			return 0, fmt.Errorf("control wal: truncate trailing corrupt: %w", err)
		}
		if _, err := w.file.Seek(int64(lastOK), io.SeekStart); err != nil {
			return 0, fmt.Errorf("control wal: seek after truncate: %w", err)
		}
	}
	// Position file pointer at end for appends.
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return 0, fmt.Errorf("control wal: seek end: %w", err)
	}
	next := uint64(1)
	if haveOK {
		next = maxSeq + 1
	}
	return next, nil
}

// Append writes a new entry to the log, assigning it the next
// monotonic seq. Returns the assigned seq. The kind and payload must
// be pre-populated; timestamp is set here. fsync is called before
// return — the caller can safely assume the entry is durable when
// Append returns without error.
func (w *ControlWAL) Append(kind ControlEntryKind, payload ControlEntryPayload) (uint64, error) {
	if w.closed.Load() {
		return 0, ErrControlWALClosed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed.Load() {
		return 0, ErrControlWALClosed
	}
	seq := w.nextSeq
	entry := &ControlEntry{
		Seq:       seq,
		Kind:      kind,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
	buf := EncodeControlEntry(entry)
	if _, err := w.file.Write(buf); err != nil {
		return 0, fmt.Errorf("control wal: write: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("control wal: sync: %w", err)
	}
	w.nextSeq++
	return seq, nil
}

// Replay invokes fn for every durable entry in seq order. Corrupt or
// truncated tail entries are not delivered (they were truncated
// during Open). Iteration stops if fn returns a non-nil error, which
// is propagated to the caller.
func (w *ControlWAL) Replay(fn func(*ControlEntry) error) error {
	if w.closed.Load() {
		return ErrControlWALClosed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("control wal: replay seek: %w", err)
	}
	defer func() {
		_, _ = w.file.Seek(0, io.SeekEnd)
	}()
	data, err := io.ReadAll(w.file)
	if err != nil {
		return fmt.Errorf("control wal: replay read: %w", err)
	}
	off := 0
	for off < len(data) {
		entry, consumed, err := DecodeControlEntry(data[off:])
		if err != nil {
			return nil
		}
		if err := fn(entry); err != nil {
			return err
		}
		off += consumed
	}
	return nil
}

// NextSeq returns the seq that the next Append will assign. Exposed
// for observability / tests; production code should call Append and
// rely on the returned seq.
func (w *ControlWAL) NextSeq() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextSeq
}

// Close closes the underlying file. After Close, Append and Replay
// return ErrControlWALClosed. Idempotent.
func (w *ControlWAL) Close() error {
	if w.closed.Swap(true) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Path returns the absolute path to the log file. Used by tests and
// diagnostics.
func (w *ControlWAL) Path() string {
	return w.path
}
